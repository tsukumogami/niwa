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
	"sort"
	"strings"
	"sync"
	"time"
)

const protocolVersion = "2024-11-05"

// Server is a stateless stdio MCP server. It reads from in, writes to out,
// and watches inboxDir for new message files to push as channel notifications.
type Server struct {
	inboxDir    string // this session's inbox: <instance-root>/.niwa/sessions/<id>/inbox/
	sessionsDir string // <instance-root>/.niwa/sessions/
	sessionRole string
	sessionID   string

	mu  sync.Mutex
	enc *json.Encoder

	// seenFiles tracks files already delivered as notifications so we don't
	// re-notify on the next poll tick.
	seenMu    sync.Mutex
	seenFiles map[string]struct{}
}

// New constructs a Server. inboxDir is where this session receives messages;
// sessionsDir is the parent where all sessions register (for routing sends).
func New(inboxDir, sessionsDir, sessionRole, sessionID string) *Server {
	return &Server{
		inboxDir:    inboxDir,
		sessionsDir: sessionsDir,
		sessionRole: sessionRole,
		sessionID:   sessionID,
		seenFiles:   make(map[string]struct{}),
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
	default:
		return errResult("unknown tool: " + p.Name)
	}
}

// handleCheckMessages reads all message files from the inbox, formats them as
// markdown, and marks them as seen so pollInbox does not re-notify within
// this server session. Files stay in inbox/ — moving to read/ is handled
// by Issue 5's notifyNewFile for reply-awaited messages, and by the TTL
// sweep for everything else.
func (s *Server) handleCheckMessages() toolResult {
	if s.inboxDir == "" {
		return errResult("NIWA_INBOX_DIR not set; is this session registered?")
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
	if args.To == "" || args.Type == "" {
		return errResult("to and type are required")
	}
	if !isValidMessageType(args.Type) {
		return errResultCode("MESSAGE_TYPE_UNKNOWN",
			fmt.Sprintf("unknown message type %q; valid types: question.ask, question.answer, task.delegate, task.ack, task.result, task.progress, review.feedback, status.update, session.hello, session.bye", args.Type))
	}

	target, err := s.resolveRole(args.To)
	if err != nil {
		return errResultCode("RECIPIENT_NOT_REGISTERED",
			fmt.Sprintf("no session registered with role %q: %v", args.To, err))
	}

	if err := os.MkdirAll(target.InboxDir, 0o755); err != nil {
		return errResultCode("INBOX_UNWRITABLE", "cannot create inbox: "+err.Error())
	}

	msgID := newUUID()
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
		return errResult("cannot marshal message: " + err.Error())
	}

	// Atomic write: write to temp file in same directory, then rename.
	tmpFile := filepath.Join(target.InboxDir, msgID+".json.tmp")
	if err := os.WriteFile(tmpFile, data, 0o644); err != nil {
		return errResultCode("INBOX_UNWRITABLE", "cannot write message: "+err.Error())
	}
	dest := filepath.Join(target.InboxDir, msgID+".json")
	if err := os.Rename(tmpFile, dest); err != nil {
		_ = os.Remove(tmpFile)
		return errResultCode("INBOX_UNWRITABLE", "cannot rename message: "+err.Error())
	}

	// Check if the recipient's PID is alive to determine delivery status.
	status := "queued"
	if IsPIDAlive(target.PID, target.StartTime) {
		status = "delivered"
	}

	return textResult(fmt.Sprintf("Message sent.\n- **ID**: %s\n- **Status**: %s\n- **To**: %s", msgID, status, args.To))
}

// pollInbox is called by watchInboxPolling as the fsnotify fallback.
func (s *Server) pollInbox() {
	entries, err := os.ReadDir(s.inboxDir)
	if err != nil {
		return
	}
	var newMsgs []Message
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		if s.hasSeen(e.Name()) {
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
		if m.ExpiresAt != "" {
			exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
			if err == nil && time.Now().After(exp) {
				continue
			}
		}
		newMsgs = append(newMsgs, m)
		s.markSeen(e.Name())
	}
	if len(newMsgs) == 0 {
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "You have %d new message(s) in your niwa inbox.\n\n", len(newMsgs))
	for _, m := range newMsgs {
		fmt.Fprintf(&sb, "- **%s** from **%s**: %s\n", m.Type, m.From.Role, bodyPreview(m.Body))
	}
	fmt.Fprintf(&sb, "\nCall `niwa_check_messages` to read them.")

	s.notify("notifications/claude/channel", channelNotificationParams{
		Source:  "niwa",
		Content: sb.String(),
	})
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

func bodyPreview(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 80 {
		return s[:77] + "..."
	}
	return s
}

var validMessageTypes = map[string]bool{
	"question.ask": true, "question.answer": true,
	"task.delegate": true, "task.ack": true, "task.result": true, "task.progress": true,
	"review.feedback": true, "status.update": true,
	"session.hello": true, "session.bye": true,
}

func isValidMessageType(t string) bool {
	return validMessageTypes[t]
}
