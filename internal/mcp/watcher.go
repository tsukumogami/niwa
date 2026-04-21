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

	// Check reply waiters first: if this message is a reply to a pending ask,
	// move the file to inbox/read/ atomically, then unblock the waiter.
	if m.ReplyTo != "" {
		s.waitersMu.Lock()
		ch, ok := s.waiters[m.ReplyTo]
		s.waitersMu.Unlock()
		if ok {
			readDir := filepath.Join(s.inboxDir, "read")
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
