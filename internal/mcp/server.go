// Package mcp implements a stdio MCP server for niwa's session mesh.
//
// The server exposes niwa_check_messages and niwa_send_message tools and
// declares the claude/channel experimental capability. When a message file
// lands in the inbox directory, the server sends a notifications/claude/channel
// notification so Claude Code can surface it without a poll.
package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

// fieldPattern validates message Type and To fields: alphanumeric plus . _ -
// between 1 and 64 characters, no ".." sequences.
var fieldPattern = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,64}$`)

// typeWaiter accumulates messages matching a type/from filter set and signals
// when the threshold count is reached.
type typeWaiter struct {
	types     map[string]bool
	from      map[string]bool
	threshold int
	mu        sync.Mutex
	msgs      []Message
	signal    chan struct{} // buffered-1
}

// matches returns true if m satisfies the waiter's type and from filters.
// An empty filter set means "any".
func (tw *typeWaiter) matches(m Message) bool {
	if len(tw.types) > 0 && !tw.types[m.Type] {
		return false
	}
	if len(tw.from) > 0 && !tw.from[m.From.Role] {
		return false
	}
	return true
}

// sendResult holds the outcome of an internal sendMessage call.
type sendResult struct {
	MessageID string
	Status    string
}

// Server is a stdio MCP server. It reads from in, writes to out,
// and watches inboxDir for new message files to push as channel notifications.
// It also maintains reply-waiter and type-waiter maps for niwa_ask and niwa_wait.
type Server struct {
	inboxDir     string // this session's inbox: <instance-root>/.niwa/sessions/<id>/inbox/
	sessionsDir  string // <instance-root>/.niwa/sessions/
	sessionRole  string
	sessionID    string
	instanceRoot string // <instance-root>

	mu  sync.Mutex
	enc *json.Encoder

	// seenFiles tracks files already delivered as notifications so we don't
	// re-notify on the next poll tick.
	seenMu    sync.Mutex
	seenFiles map[string]struct{}

	waitersMu   sync.Mutex
	waiters     map[string]chan toolResult // msgID → reply channel
	typeWaiters map[string]*typeWaiter    // key → waiter
}

// New constructs a Server. inboxDir is where this session receives messages;
// sessionsDir is the parent where all sessions register (for routing sends);
// instanceRoot is the workspace instance root (for TTL sweep).
func New(inboxDir, sessionsDir, sessionRole, sessionID, instanceRoot string) *Server {
	return &Server{
		inboxDir:     inboxDir,
		sessionsDir:  sessionsDir,
		sessionRole:  sessionRole,
		sessionID:    sessionID,
		instanceRoot: instanceRoot,
		seenFiles:    make(map[string]struct{}),
		waiters:      make(map[string]chan toolResult),
		typeWaiters:  make(map[string]*typeWaiter),
	}
}

// Run starts the server. It reads newline-delimited JSON-RPC from r, writes
// responses to w, and launches an inbox watcher goroutine that sends push
// notifications when new messages arrive.
func (s *Server) Run(r io.Reader, w io.Writer) error {
	s.enc = json.NewEncoder(w)

	if s.inboxDir != "" {
		go s.watchInbox()
	}

	s.startTTLSweep()

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			continue
		}
		// Dispatch synchronously so responses are written before stdin closes.
		// The watchInbox goroutine handles notifications independently.
		s.dispatch(req)
	}
	return scanner.Err()
}

func (s *Server) dispatch(req request) {
	switch req.Method {
	case "initialize":
		s.send(response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: initializeResult{
				ProtocolVersion: protocolVersion,
				Capabilities: capabilities{
					Experimental: map[string]any{"claude/channel": map[string]any{}},
					Tools:        map[string]any{},
				},
				ServerInfo: serverInfo{Name: "niwa", Version: "0.1.0"},
			},
		})
	case "notifications/initialized":
		// client-side notification, no response needed
	case "tools/list":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: s.toolsList()})
	case "tools/call":
		var p toolCallParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			s.sendError(req.ID, -32600, "invalid params")
			return
		}
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: s.callTool(p)})
	case "ping":
		s.send(response{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	default:
		if req.ID != nil {
			s.sendError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *Server) toolsList() toolsListResult {
	return toolsListResult{Tools: []toolDef{
		{
			Name:        "niwa_check_messages",
			Description: "Check for new messages in this session's inbox. Call this at idle points and every ~10 tool calls while working.",
			InputSchema: inputSchema{Type: "object", Properties: map[string]schemaProp{}},
		},
		{
			Name:        "niwa_send_message",
			Description: "Send a typed message to another session by role.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"to":         {Type: "string", Description: "Recipient role (e.g. coordinator, niwa-worker)"},
					"type":       {Type: "string", Description: "Message type (e.g. question.ask, task.delegate, task.result)"},
					"body":       {Type: "object", Description: "Message payload (type-specific)"},
					"reply_to":   {Type: "string", Description: "Message ID being replied to (optional)"},
					"task_id":    {Type: "string", Description: "Stable task UUID for correlation (optional)"},
					"expires_at": {Type: "string", Description: "ISO 8601 expiry deadline (optional)"},
				},
				Required: []string{"to", "type", "body"},
			},
		},
		{
			Name:        "niwa_ask",
			Description: "Send a question to another session and block until an answer arrives (or timeout). Returns the answer body.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"to":      {Type: "string", Description: "Recipient role to ask"},
					"body":    {Type: "object", Description: "Question payload"},
					"timeout": {Type: "number", Description: "Seconds to wait for reply (default 600)"},
				},
				Required: []string{"to", "body"},
			},
		},
		{
			Name:        "niwa_wait",
			Description: "Block until a threshold number of messages matching optional type/from filters arrive. Returns accumulated messages.",
			InputSchema: inputSchema{
				Type: "object",
				Properties: map[string]schemaProp{
					"types":   {Type: "array", Description: "Message types to accept (empty = any)"},
					"from":    {Type: "array", Description: "Sender roles to accept (empty = any)"},
					"count":   {Type: "number", Description: "Number of messages to accumulate before returning (default 1)"},
					"timeout": {Type: "number", Description: "Seconds to wait (default 600)"},
				},
			},
		},
	}}
}

func (s *Server) callTool(p toolCallParams) toolResult {
	switch p.Name {
	case "niwa_check_messages":
		return s.handleCheckMessages()
	case "niwa_send_message":
		var args sendMessageArgs
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
		return s.handleSendMessage(args)
	case "niwa_ask":
		var args askArgs
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
		return s.handleAsk(args)
	case "niwa_wait":
		var args waitArgs
		if err := json.Unmarshal(p.Arguments, &args); err != nil {
			return errResult("invalid arguments: " + err.Error())
		}
		return s.handleWait(args)
	default:
		return errResult("unknown tool: " + p.Name)
	}
}

// handleCheckMessages reads all message files from the inbox, formats them as
// markdown, and marks them as seen so pollInbox does not re-notify within
// this server session. Files stay in inbox/ — moving to read/ is handled
// by notifyNewFile for reply-awaited messages, and by the TTL sweep for
// everything else.
func (s *Server) handleCheckMessages() toolResult {
	if s.inboxDir == "" {
		return errResult("NIWA_SESSION_ID not set; is this session registered?")
	}

	entries, err := os.ReadDir(s.inboxDir)
	if err != nil {
		if os.IsNotExist(err) {
			return textResult("No new messages.")
		}
		return errResult("cannot read inbox: " + err.Error())
	}

	var msgs []Message
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.inboxDir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		// Skip expired messages without touching the filesystem.
		if m.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				continue
			}
		}
		msgs = append(msgs, m)
		s.markSeen(e.Name())
	}

	if len(msgs) == 0 {
		return textResult("No new messages.")
	}

	// Sort by sent_at ascending.
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].SentAt < msgs[j].SentAt
	})

	var sb strings.Builder
	fmt.Fprintf(&sb, "## %d new message(s)\n\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&sb, "### Message %d — %s from %s\n", i+1, m.Type, m.From.Role)
		fmt.Fprintf(&sb, "- **ID**: %s\n", m.ID)
		fmt.Fprintf(&sb, "- **Sent**: %s\n", m.SentAt)
		if m.ReplyTo != "" {
			fmt.Fprintf(&sb, "- **Reply to**: %s\n", m.ReplyTo)
		}
		if m.TaskID != "" {
			fmt.Fprintf(&sb, "- **Task ID**: %s\n", m.TaskID)
		}
		if m.ExpiresAt != "" {
			fmt.Fprintf(&sb, "- **Expires**: %s\n", m.ExpiresAt)
		}
		fmt.Fprintf(&sb, "\n**Body**:\n```json\n%s\n```\n\n", prettyJSON(m.Body))
	}

	return textResult(sb.String())
}

// handleSendMessage routes a message to the target role's inbox via atomic rename.
func (s *Server) handleSendMessage(args sendMessageArgs) toolResult {
	sr, errTR := s.sendMessage(args)
	if errTR.IsError {
		return errTR
	}
	return textResult(fmt.Sprintf("Message sent.\n- **ID**: %s\n- **Status**: %s\n- **To**: %s", sr.MessageID, sr.Status, args.To))
}

// sendMessage validates args, generates a fresh message ID, and writes the
// message to the target's inbox atomically. Returns (sendResult, errTR) where
// errTR.IsError is true on failure.
func (s *Server) sendMessage(args sendMessageArgs) (sendResult, toolResult) {
	return s.sendMessageWithID(newUUID(), args)
}

// sendMessageWithID is like sendMessage but uses the caller-supplied msgID.
// handleAsk uses this to register a reply waiter for the ID before the message
// is written, eliminating the race between send and waiter registration.
func (s *Server) sendMessageWithID(msgID string, args sendMessageArgs) (sendResult, toolResult) {
	if args.To == "" || args.Type == "" {
		return sendResult{}, errResult("to and type are required")
	}
	if !fieldPattern.MatchString(args.To) || strings.Contains(args.To, "..") {
		return sendResult{}, errResult("invalid to field: must match ^[a-zA-Z0-9._-]{1,64}$ and not contain ..")
	}
	if !fieldPattern.MatchString(args.Type) || strings.Contains(args.Type, "..") {
		return sendResult{}, errResult("invalid type field: must match ^[a-zA-Z0-9._-]{1,64}$ and not contain ..")
	}
	// Reject null JSON (len==4) and truly empty bodies.
	if len(args.Body) == 0 || string(args.Body) == "null" {
		return sendResult{}, errResult("body is required")
	}
	if len(args.Body) > 64*1024 {
		return sendResult{}, errResult("body exceeds 64 KB limit")
	}

	target, err := s.resolveRole(args.To)
	if err != nil {
		return sendResult{}, errResultCode("RECIPIENT_NOT_REGISTERED",
			fmt.Sprintf("no session registered with role %q: %v", args.To, err))
	}

	if err := os.MkdirAll(target.InboxDir, 0o700); err != nil {
		return sendResult{}, errResultCode("INBOX_UNWRITABLE", "cannot create inbox: "+err.Error())
	}

	msg := Message{
		V:    1,
		ID:   msgID,
		Type: args.Type,
		From: MessageFrom{
			Role: s.sessionRole,
		},
		To:        MessageTo{Role: args.To},
		ReplyTo:   args.ReplyTo,
		TaskID:    args.TaskID,
		SentAt:    time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: args.ExpiresAt,
		Body:      args.Body,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return sendResult{}, errResult("cannot marshal message: " + err.Error())
	}

	// Atomic write: write to temp file in same directory, then rename.
	tmpFile := filepath.Join(target.InboxDir, msgID+".json.tmp")
	if err := os.WriteFile(tmpFile, data, 0o600); err != nil {
		return sendResult{}, errResultCode("INBOX_UNWRITABLE", "cannot write message: "+err.Error())
	}
	dest := filepath.Join(target.InboxDir, msgID+".json")
	if err := os.Rename(tmpFile, dest); err != nil {
		_ = os.Remove(tmpFile)
		return sendResult{}, errResultCode("INBOX_UNWRITABLE", "cannot rename message: "+err.Error())
	}

	// Check if the recipient's PID is alive to determine delivery status.
	status := "queued"
	if IsPIDAlive(target.PID, target.StartTime) {
		status = "delivered"
	}

	return sendResult{MessageID: msgID, Status: status}, toolResult{}
}

// handleAsk sends a question.ask message to the target role and blocks until
// a reply arrives (matched by reply_to == msgID) or the timeout elapses.
// The reply waiter is registered before sending so no reply can arrive between
// send and registration.
func (s *Server) handleAsk(args askArgs) toolResult {
	if args.Timeout <= 0 {
		args.Timeout = 600
	}
	if args.To == "" {
		return errResult("to is required")
	}

	// Reserve a message ID so we can register the waiter before the message
	// is written. A reply arriving between write and registration would
	// otherwise be dropped.
	msgID := newUUID()
	replyCh, cancel := s.registerWaiter(msgID)
	defer cancel()

	_, errTR := s.sendMessageWithID(msgID, sendMessageArgs{
		To:   args.To,
		Type: "question.ask",
		Body: args.Body,
	})
	if errTR.IsError {
		return errTR
	}

	select {
	case reply := <-replyCh:
		return reply
	case <-time.After(time.Duration(args.Timeout) * time.Second):
		return errResultCode("ASK_TIMEOUT", fmt.Sprintf("no reply received within %ds", args.Timeout))
	}
}

// handleWait registers a typeWaiter and blocks until the threshold count of
// matching messages arrives or the timeout elapses.
func (s *Server) handleWait(args waitArgs) toolResult {
	if args.Timeout <= 0 {
		args.Timeout = 600
	}
	if args.Count <= 0 {
		args.Count = 1
	}

	tw := &typeWaiter{
		types:     toSet(args.Types),
		from:      toSet(args.From),
		threshold: args.Count,
		signal:    make(chan struct{}, 1),
	}

	// Register FIRST, then scan. This prevents a race where a message arrives
	// between the scan and registration.
	key := newUUID()
	s.waitersMu.Lock()
	s.typeWaiters[key] = tw
	s.waitersMu.Unlock()
	cancel := func() {
		s.waitersMu.Lock()
		delete(s.typeWaiters, key)
		s.waitersMu.Unlock()
	}
	defer cancel()

	s.scanExistingForWaiter(tw)

	tw.mu.Lock()
	if len(tw.msgs) >= tw.threshold {
		msgs := append([]Message(nil), tw.msgs...)
		tw.mu.Unlock()
		return formatWaitResult(msgs)
	}
	tw.mu.Unlock()

	select {
	case <-tw.signal:
		tw.mu.Lock()
		msgs := append([]Message(nil), tw.msgs...)
		tw.mu.Unlock()
		return formatWaitResult(msgs)
	case <-time.After(time.Duration(args.Timeout) * time.Second):
		return errResultCode("WAIT_TIMEOUT", fmt.Sprintf("timeout after %ds", args.Timeout))
	}
}

// registerWaiter allocates a buffered-1 channel for the given message ID and
// stores it in waiters. Returns the channel and a cancel func that removes the entry.
func (s *Server) registerWaiter(msgID string) (chan toolResult, func()) {
	ch := make(chan toolResult, 1)
	s.waitersMu.Lock()
	s.waiters[msgID] = ch
	s.waitersMu.Unlock()
	cancel := func() {
		s.waitersMu.Lock()
		delete(s.waiters, msgID)
		s.waitersMu.Unlock()
	}
	return ch, cancel
}

// scanExistingForWaiter reads existing inbox files and appends matching messages
// to tw. The waiter is already registered before this is called, so notifyNewFile
// may run concurrently — tw.mu guards all appends.
func (s *Server) scanExistingForWaiter(tw *typeWaiter) {
	if s.inboxDir == "" {
		return
	}
	entries, err := os.ReadDir(s.inboxDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.inboxDir, e.Name()))
		if err != nil {
			continue
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if m.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				continue
			}
		}
		if tw.matches(m) {
			tw.mu.Lock()
			tw.msgs = append(tw.msgs, m)
			tw.mu.Unlock()
		}
		// Mark seen so pollInbox does not deliver the same file a second time
		// via notifyNewFile, which would double-count it in tw.msgs.
		s.markSeen(e.Name())
	}
}

// formatWaitResult formats accumulated messages as a human-readable tool result.
func formatWaitResult(msgs []Message) toolResult {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d message(s) received\n\n", len(msgs))
	for i, m := range msgs {
		fmt.Fprintf(&sb, "### Message %d — %s from %s\n", i+1, m.Type, m.From.Role)
		fmt.Fprintf(&sb, "- **ID**: %s\n", m.ID)
		fmt.Fprintf(&sb, "- **Sent**: %s\n", m.SentAt)
		fmt.Fprintf(&sb, "\n**Body**:\n```json\n%s\n```\n\n", prettyJSON(m.Body))
	}
	return textResult(sb.String())
}

// toSet converts a string slice to a set map.
func toSet(ss []string) map[string]bool {
	if len(ss) == 0 {
		return nil
	}
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

// pollInbox is called by watchInboxPolling as the fsnotify fallback.
// It routes each unseen file through notifyNewFile so that reply-waiter and
// type-waiter correlation works on platforms without fsnotify support.
func (s *Server) pollInbox() {
	entries, err := os.ReadDir(s.inboxDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if s.hasSeen(e.Name()) {
			continue
		}
		path := filepath.Join(s.inboxDir, e.Name())
		s.notifyNewFile(path, e.Name())
	}
}

func (s *Server) resolveRole(role string) (*SessionEntry, error) {
	registryPath := filepath.Join(s.sessionsDir, "sessions.json")
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, fmt.Errorf("cannot read sessions.json: %w", err)
	}
	var registry SessionRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("cannot parse sessions.json: %w", err)
	}
	for i, sess := range registry.Sessions {
		if sess.Role == role {
			return &registry.Sessions[i], nil
		}
	}
	return nil, fmt.Errorf("role not found")
}

func (s *Server) markSeen(name string) {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	s.seenFiles[name] = struct{}{}
}

func (s *Server) hasSeen(name string) bool {
	s.seenMu.Lock()
	defer s.seenMu.Unlock()
	_, ok := s.seenFiles[name]
	return ok
}

func (s *Server) send(v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enc.Encode(v)
}

func (s *Server) notify(method string, params any) {
	s.send(notification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *Server) sendError(id any, code int, msg string) {
	s.send(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

// TTL sweep

const defaultTTL = 24 * time.Hour

func (s *Server) startTTLSweep() {
	go func() {
		s.sweepRead(defaultTTL)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			s.sweepRead(defaultTTL)
		}
	}()
}

func (s *Server) sweepRead(ttl time.Duration) {
	if s.sessionsDir == "" {
		return
	}
	entries, err := os.ReadDir(s.sessionsDir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-ttl)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		readDir := filepath.Join(s.sessionsDir, e.Name(), "inbox", "read")
		files, err := os.ReadDir(readDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			fi, err := f.Info()
			if err != nil {
				continue
			}
			if fi.ModTime().Before(cutoff) {
				_ = os.Remove(filepath.Join(readDir, f.Name()))
			}
		}
	}
}

// Helpers

func textResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}}
}

func errResult(msg string) toolResult {
	return toolResult{IsError: true, Content: []contentBlock{{Type: "text", Text: msg}}}
}

func errResultCode(code, detail string) toolResult {
	return toolResult{IsError: true, Content: []contentBlock{{
		Type: "text",
		Text: fmt.Sprintf("error_code: %s\ndetail: %s", code, detail),
	}}}
}

func prettyJSON(raw json.RawMessage) string {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(b)
}
