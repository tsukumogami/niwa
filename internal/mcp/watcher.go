package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

// watchInbox watches the inbox directory using inotify (Linux) / kqueue (macOS)
// and sends a notifications/claude/channel push when new message files arrive.
// This replaces the polling goroutine in server.go.
func (s *Server) watchInbox() {
	if err := os.MkdirAll(s.inboxDir, 0o700); err != nil {
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Fall back to polling if fsnotify is unavailable.
		s.watchInboxPolling()
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.inboxDir); err != nil {
		s.watchInboxPolling()
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			// Only act on file creation (atomic rename fires Create for the dest name).
			if !event.Has(fsnotify.Create) {
				continue
			}
			name := filepath.Base(event.Name)
			if !strings.HasSuffix(name, ".json") || s.hasSeen(name) {
				continue
			}
			// Brief pause to let the write complete before reading.
			time.Sleep(10 * time.Millisecond)
			s.notifyNewFile(event.Name, name)
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// watchInboxPolling is the fallback when fsnotify is not available.
func (s *Server) watchInboxPolling() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.pollInbox()
	}
}

// watchRoleInbox watches .niwa/roles/<s.role>/inbox/ for new message files.
// This is the primary delivery channel in the post-Issue-2 layout; the
// per-session inbox (watchInbox) is retained for backward compatibility.
//
// On any arriving file, notifyNewFile inspects the message type and routes
// task-terminal messages (task.completed/abandoned/cancelled) to
// awaitWaiters[body.task_id] before falling through to reply-waiter /
// type-waiter dispatch.
func (s *Server) watchRoleInbox() {
	if err := os.MkdirAll(s.roleInboxDir, 0o700); err != nil {
		return
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		s.watchRoleInboxPolling()
		return
	}
	defer watcher.Close()

	if err := watcher.Add(s.roleInboxDir); err != nil {
		s.watchRoleInboxPolling()
		return
	}

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !event.Has(fsnotify.Create) {
				continue
			}
			name := filepath.Base(event.Name)
			if !strings.HasSuffix(name, ".json") || s.hasSeen(name) {
				continue
			}
			time.Sleep(10 * time.Millisecond)
			s.notifyNewFile(event.Name, name)
		case _, ok := <-watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

// watchRoleInboxPolling is the fallback when fsnotify is unavailable.
func (s *Server) watchRoleInboxPolling() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.pollRoleInbox()
	}
}

func (s *Server) pollRoleInbox() {
	entries, err := os.ReadDir(s.roleInboxDir)
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
		path := filepath.Join(s.roleInboxDir, e.Name())
		s.notifyNewFile(path, e.Name())
	}
}

func (s *Server) notifyNewFile(path, name string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return
	}
	if m.ExpiresAt != "" {
		exp, err := time.Parse(time.RFC3339, m.ExpiresAt)
		if err == nil && time.Now().After(exp) {
			return
		}
	}

	// Task-terminal dispatch: task.completed / task.abandoned / task.cancelled
	// messages wake an awaitWaiter keyed by body.task_id. This path runs
	// BEFORE the reply_to dispatch so sync niwa_delegate / niwa_await_task
	// waiters unblock on terminal messages even when the underlying message
	// happens to carry a reply_to (e.g. a reply from a spawned ask task).
	if kind, ok := taskTerminalKind(m.Type); ok {
		taskID := extractTaskID(m)
		if taskID != "" {
			s.waitersMu.Lock()
			ch, ok := s.awaitWaiters[taskID]
			s.waitersMu.Unlock()
			if ok {
				readDir := filepath.Join(filepath.Dir(path), "read")
				_ = os.MkdirAll(readDir, 0o700)
				_ = os.Rename(path, filepath.Join(readDir, name))
				// Buffered-1 send; drop if a previous event already queued.
				evt := taskEvent{TaskID: taskID, Kind: kind, At: time.Now()}
				switch kind {
				case EvtCompleted:
					evt.Result = extractBodyField(m.Body, "result")
				case EvtAbandoned:
					evt.Reason = extractBodyField(m.Body, "reason")
				}
				select {
				case ch <- evt:
				default:
				}
				s.markSeen(name)
				return
			}
		}
	}

	// Check reply waiters first: if this message is a reply to a pending ask,
	// move the file to inbox/read/ atomically, then unblock the waiter.
	if m.ReplyTo != "" {
		s.waitersMu.Lock()
		ch, ok := s.waiters[m.ReplyTo]
		s.waitersMu.Unlock()
		if ok {
			readDir := filepath.Join(filepath.Dir(path), "read")
			_ = os.MkdirAll(readDir, 0o700)
			dest := filepath.Join(readDir, name)
			if err := os.Rename(path, dest); err == nil {
				ch <- textResult(prettyJSON(m.Body))
			}
			return
		}
	}

	// Notify any typeWaiters that match this message.
	s.waitersMu.Lock()
	var matched []*typeWaiter
	for _, tw := range s.typeWaiters {
		if tw.matches(m) {
			matched = append(matched, tw)
		}
	}
	s.waitersMu.Unlock()

	for _, tw := range matched {
		tw.mu.Lock()
		tw.msgs = append(tw.msgs, m)
		if len(tw.msgs) >= tw.threshold {
			select {
			case tw.signal <- struct{}{}:
			default:
			}
		}
		tw.mu.Unlock()
	}

	// Mark seen and send a channel notification (standard behavior).
	s.markSeen(name)
	content := fmt.Sprintf(
		"**New message in your niwa inbox** — %s from **%s**\n\nCall `niwa_check_messages` to read it.",
		m.Type, m.From.Role,
	)
	s.notify("notifications/claude/channel", channelNotificationParams{
		Source:  "niwa",
		Content: content,
	})
}

// taskTerminalKind maps a message type to its TaskEventKind when the type
// indicates a terminal task outcome. Returns (kind, true) on match.
func taskTerminalKind(msgType string) (TaskEventKind, bool) {
	switch msgType {
	case "task.completed":
		return EvtCompleted, true
	case "task.abandoned":
		return EvtAbandoned, true
	case "task.cancelled":
		return EvtCancelled, true
	}
	return 0, false
}

// extractTaskID returns the task_id carried by a task-terminal message. It
// prefers the top-level Message.TaskID field and falls back to body.task_id
// when the sender populated it there instead.
func extractTaskID(m Message) string {
	if m.TaskID != "" {
		return m.TaskID
	}
	var probe struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(m.Body, &probe); err == nil {
		return probe.TaskID
	}
	return ""
}

// extractBodyField returns the JSON value at body.<key> as raw bytes, or nil
// if the key is absent or the body is unparseable. Used to surface terminal
// result/reason payloads on the taskEvent carried to awaiters.
func extractBodyField(body json.RawMessage, key string) json.RawMessage {
	if len(body) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil
	}
	return m[key]
}
