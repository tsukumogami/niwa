package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSessionsJSON writes sessions.json for the test's instanceRoot.
func writeSessionsJSON(t *testing.T, instanceRoot string, sessions []SessionEntry) {
	t.Helper()
	sessionsDir := filepath.Join(instanceRoot, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	reg := SessionRegistry{Sessions: sessions}
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		t.Fatalf("marshal sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "sessions.json"), data, 0o600); err != nil {
		t.Fatalf("write sessions.json: %v", err)
	}
}

// listInboxFiles returns non-directory .json filenames in inboxDir.
func listInboxFiles(t *testing.T, inboxDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(inboxDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	return names
}

// TestHandleAsk_LiveCoordinator_WritesTaskAsk verifies that handleAsk writes a
// task.ask notification to the coordinator's inbox and does NOT write a
// task.delegate when a live coordinator session is registered.
func TestHandleAsk_LiveCoordinator_WritesTaskAsk(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// Register a live coordinator in sessions.json.
	pid := os.Getpid()
	start, _ := PIDStartTime(pid)
	writeSessionsJSON(t, root, []SessionEntry{{
		ID:           "coord-session",
		Role:         "coordinator",
		PID:          pid,
		StartTime:    start,
		InboxDir:     filepath.Join(root, ".niwa", "roles", "coordinator", "inbox"),
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	coordInbox := filepath.Join(root, ".niwa", "roles", "coordinator", "inbox")

	// Call handleAsk in a goroutine so we can check the inbox without waiting
	// for the timeout.
	done := make(chan toolResult, 1)
	go func() {
		done <- s.handleAsk(askArgs{
			To:             "coordinator",
			Body:           json.RawMessage(`{"text":"approve?"}`),
			TimeoutSeconds: 2,
		})
	}()

	// Wait briefly for handleAsk to write the notification.
	deadline := time.Now().Add(500 * time.Millisecond)
	var files []string
	for time.Now().Before(deadline) {
		files = listInboxFiles(t, coordInbox)
		if len(files) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if len(files) == 0 {
		t.Fatal("expected task.ask notification in coordinator inbox, found none")
	}

	// Read and validate the notification.
	data, err := os.ReadFile(filepath.Join(coordInbox, files[0]))
	if err != nil {
		t.Fatalf("read notification: %v", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal notification: %v", err)
	}
	if msg.Type != "task.ask" {
		t.Errorf("expected type=task.ask, got %q", msg.Type)
	}
	if msg.From.Role != "frontend" {
		t.Errorf("expected from.role=frontend, got %q", msg.From.Role)
	}
	if msg.To.Role != "coordinator" {
		t.Errorf("expected to.role=coordinator, got %q", msg.To.Role)
	}
	if msg.TaskID == "" {
		t.Error("expected non-empty task_id")
	}

	// Validate body fields.
	var body struct {
		AskTaskID string          `json:"ask_task_id"`
		FromRole  string          `json:"from_role"`
		NiwaNote  string          `json:"_niwa_note"`
		Question  json.RawMessage `json:"question"`
	}
	if err := json.Unmarshal(msg.Body, &body); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if body.AskTaskID == "" {
		t.Error("body.ask_task_id is empty")
	}
	if body.FromRole != "frontend" {
		t.Errorf("body.from_role=%q, want frontend", body.FromRole)
	}
	if body.NiwaNote == "" {
		t.Error("body._niwa_note is empty")
	}
	if string(body.Question) != `{"text":"approve?"}` {
		t.Errorf("body.question=%s, want {\"text\":\"approve?\"}", body.Question)
	}

	// Also verify that no task.delegate was written to the coordinator inbox.
	for _, f := range files {
		d, _ := os.ReadFile(filepath.Join(coordInbox, f))
		var m Message
		if err := json.Unmarshal(d, &m); err == nil && m.Type == "task.delegate" {
			t.Errorf("unexpected task.delegate found in coordinator inbox: %s", f)
		}
	}

	<-done // drain goroutine after timeout
}

// TestHandleAsk_DeadCoordinator_ReturnsNoLiveSession verifies that handleAsk
// returns no_live_session immediately (without writing any task directory) when
// the registered coordinator PID is dead, and that the stale entry is pruned.
func TestHandleAsk_DeadCoordinator_ReturnsNoLiveSession(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// Register a stale (dead PID) coordinator entry.
	writeSessionsJSON(t, root, []SessionEntry{{
		ID:           "coord-session-dead",
		Role:         "coordinator",
		PID:          deadPID,
		StartTime:    0,
		InboxDir:     filepath.Join(root, ".niwa", "roles", "coordinator", "inbox"),
		RegisteredAt: time.Now().UTC().Format(time.RFC3339),
	}})

	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"text":"hello"}`),
		TimeoutSeconds: 2,
	})

	if res.IsError {
		t.Fatalf("expected non-error result, got error: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"status":"no_live_session"`) {
		t.Errorf("want no_live_session status, got %s", text)
	}
	if !strings.Contains(text, `"role":"coordinator"`) {
		t.Errorf("want role=coordinator in response, got %s", text)
	}

	// No task directory must have been created.
	tasksDir := filepath.Join(root, ".niwa", "tasks")
	entries, _ := os.ReadDir(tasksDir)
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) != 0 {
		t.Errorf("expected zero task subdirectories, found %v", subdirs)
	}

	// Verify the stale entry was pruned.
	sessionsPath := filepath.Join(root, ".niwa", "sessions", "sessions.json")
	regData, err := os.ReadFile(sessionsPath)
	if err != nil {
		t.Fatalf("read sessions.json: %v", err)
	}
	var reg SessionRegistry
	if err := json.Unmarshal(regData, &reg); err != nil {
		t.Fatalf("unmarshal sessions.json: %v", err)
	}
	for _, entry := range reg.Sessions {
		if entry.Role == "coordinator" && entry.PID == deadPID {
			t.Error("stale coordinator entry not pruned from sessions.json")
		}
	}
}

// TestHandleAsk_NoSessions_ReturnsNoLiveSession verifies that handleAsk returns
// no_live_session immediately (without writing any task directory) when
// sessions.json does not exist.
func TestHandleAsk_NoSessions_ReturnsNoLiveSession(t *testing.T) {
	s := newTestServer(t, "frontend", "coordinator")
	root := s.instanceRoot

	// No sessions.json created.
	res := s.handleAsk(askArgs{
		To:             "coordinator",
		Body:           json.RawMessage(`{"text":"hello"}`),
		TimeoutSeconds: 2,
	})

	if res.IsError {
		t.Fatalf("expected non-error result, got error: %s", res.Content[0].Text)
	}
	text := res.Content[0].Text
	if !strings.Contains(text, `"status":"no_live_session"`) {
		t.Errorf("want no_live_session status, got %s", text)
	}

	// No task directory must have been created.
	tasksDir := filepath.Join(root, ".niwa", "tasks")
	entries, _ := os.ReadDir(tasksDir)
	var subdirs []string
	for _, e := range entries {
		if e.IsDir() {
			subdirs = append(subdirs, e.Name())
		}
	}
	if len(subdirs) != 0 {
		t.Errorf("expected zero task subdirectories, found %v", subdirs)
	}
}
