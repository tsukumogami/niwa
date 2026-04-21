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
	if err := os.MkdirAll(s.inboxDir, 0o755); err != nil {
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
	s.markSeen(name)

	content := fmt.Sprintf(
		"**New message in your niwa inbox** — %s from **%s**\n\n%s\n\nCall `niwa_check_messages` to read it.",
		m.Type, m.From.Role, bodyPreview(m.Body),
	)
	s.notify("notifications/claude/channel", channelNotificationParams{
		Source:  "niwa",
		Content: content,
	})
}
