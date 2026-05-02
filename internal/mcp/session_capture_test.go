package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRegisterSessionID_WrittenOnStartup verifies that a valid CLAUDE_SESSION_ID
// is written to Worker.ClaudeSessionID in state.json when the server starts
// as a worker (taskID != "").
func TestRegisterSessionID_WrittenOnStartup(t *testing.T) {
	root := t.TempDir()
	taskID := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	t.Setenv("CLAUDE_SESSION_ID", "abcdef01-session-id")
	s := &Server{
		instanceRoot: root,
		taskID:       taskID,
	}
	s.registerSessionID()

	taskDir := taskDirPath(root, taskID)
	_, st, err := ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Worker.ClaudeSessionID != "abcdef01-session-id" {
		t.Errorf("ClaudeSessionID = %q, want %q", st.Worker.ClaudeSessionID, "abcdef01-session-id")
	}
}

// TestRegisterSessionID_SkippedForCoordinator verifies that coordinator sessions
// (taskID == "") do not write to any state.json.
func TestRegisterSessionID_SkippedForCoordinator(t *testing.T) {
	root := t.TempDir()
	taskID := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	t.Setenv("CLAUDE_SESSION_ID", "abcdef01-session-id")
	s := &Server{
		instanceRoot: root,
		taskID:       "", // coordinator
	}
	s.registerSessionID()

	taskDir := taskDirPath(root, taskID)
	_, st, err := ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	// No write should have occurred — ClaudeSessionID must stay empty.
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("expected ClaudeSessionID empty for coordinator; got %q", st.Worker.ClaudeSessionID)
	}
}

// TestRegisterSessionID_InvalidID_NoError verifies that an absent or invalid
// CLAUDE_SESSION_ID causes no write and no error.
func TestRegisterSessionID_InvalidID_NoError(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{"empty", ""},
		{"too_short", "abc"},
		{"invalid_chars", "has spaces here!"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			taskID := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

			t.Setenv("CLAUDE_SESSION_ID", tc.val)
			s := &Server{instanceRoot: root, taskID: taskID}
			s.registerSessionID() // must not panic or error

			taskDir := taskDirPath(root, taskID)
			_, st, err := ReadState(taskDir)
			if err != nil {
				t.Fatalf("ReadState: %v", err)
			}
			if st.Worker.ClaudeSessionID != "" {
				t.Errorf("expected ClaudeSessionID empty for invalid input %q; got %q", tc.val, st.Worker.ClaudeSessionID)
			}
		})
	}
}

// TestRegisterSessionID_OverwritesExisting verifies that a new session ID
// overwrites a pre-existing one (resume scenario: new Claude session on same task).
func TestRegisterSessionID_OverwritesExisting(t *testing.T) {
	root := t.TempDir()
	taskID := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	// Pre-populate an existing session ID.
	taskDir := taskDirPath(root, taskID)
	if err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.Worker.ClaudeSessionID = "old-session-existing01"
		return &next, nil, nil
	}); err != nil {
		t.Fatalf("pre-populate: %v", err)
	}

	t.Setenv("CLAUDE_SESSION_ID", "new-session-resumed02")
	s := &Server{instanceRoot: root, taskID: taskID}
	s.registerSessionID()

	_, st, err := ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.Worker.ClaudeSessionID != "new-session-resumed02" {
		t.Errorf("ClaudeSessionID = %q, want %q", st.Worker.ClaudeSessionID, "new-session-resumed02")
	}
}

// TestCreateTaskEnvelope_MaxResumes verifies that createTaskEnvelope writes
// MaxResumes: 2 to the new task's state.json.
func TestCreateTaskEnvelope_MaxResumes(t *testing.T) {
	s := newTestServer(t, "coordinator", "web")

	taskID, result := s.createTaskEnvelope("web", json.RawMessage(`{}`), "", "")
	if result.IsError {
		t.Fatalf("createTaskEnvelope error: %v", result.Content)
	}

	taskDir := taskDirPath(s.instanceRoot, taskID)
	_, st, err := ReadState(taskDir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.MaxResumes != 2 {
		t.Errorf("MaxResumes = %d, want 2", st.MaxResumes)
	}
}

// TestAuditLog_ExcludesClaudeSessionID verifies that calling an MCP tool handler
// when Worker.ClaudeSessionID is populated does not write "claude_session_id"
// to the audit log.
func TestAuditLog_ExcludesClaudeSessionID(t *testing.T) {
	root := t.TempDir()
	taskID := writeTaskFixture(t, root, "coordinator", "web", TaskStateRunning)

	// Set a ClaudeSessionID in state.json.
	taskDir := taskDirPath(root, taskID)
	if err := UpdateState(taskDir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		next := *cur
		next.Worker.ClaudeSessionID = "audit-test-session-id1"
		return &next, nil, nil
	}); err != nil {
		t.Fatalf("set ClaudeSessionID: %v", err)
	}

	_ = os.MkdirAll(filepath.Join(root, ".niwa"), 0o700)
	s := New("web", root)
	// Override the taskID to match the fixture worker.
	s.taskID = taskID

	// Call niwa_check_messages — a lightweight tool that reads the inbox.
	p := toolCallParams{
		Name:      "niwa_check_messages",
		Arguments: json.RawMessage(`{}`),
	}
	_ = s.callTool(p)
	// Emit the audit entry.
	_ = s.audit.Emit(buildAuditEntry(s.role, s.taskID, p, toolResult{}))

	auditPath := filepath.Join(root, ".niwa", "mcp-audit.log")
	auditBytes, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if strings.Contains(string(auditBytes), "claude_session_id") {
		t.Errorf("audit log contains 'claude_session_id': %s", auditBytes)
	}
}

// TestBackwardCompat_OldStateRoundTrip verifies that a state.json fixture
// lacking all four new fields round-trips through ReadState → UpdateState
// without data loss.
func TestBackwardCompat_OldStateRoundTrip(t *testing.T) {
	root := t.TempDir()
	taskID := NewTaskID()
	dir := taskDirPath(root, taskID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	// Old-format envelope: no new fields.
	oldEnv := map[string]any{
		"v":       1,
		"id":      taskID,
		"from":    map[string]any{"role": "coordinator", "pid": 1000},
		"to":      map[string]any{"role": "web"},
		"body":    map[string]any{},
		"sent_at": now,
	}
	envBytes, _ := json.MarshalIndent(oldEnv, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, envelopeFileName), envBytes, 0o600); err != nil {
		t.Fatalf("write envelope: %v", err)
	}

	// Old-format state: no ClaudeSessionID, no ResumeCount, no MaxResumes, no Resume.
	oldState := map[string]any{
		"v":       1,
		"task_id": taskID,
		"state":   TaskStateRunning,
		"state_transitions": []any{
			map[string]any{"from": "", "to": TaskStateQueued, "at": now},
		},
		"restart_count": 0,
		"max_restarts":  3,
		"worker":        map[string]any{"role": "web"},
		"delegator_role": "coordinator",
		"target_role":    "web",
		"updated_at":     now,
	}
	stBytes, _ := json.MarshalIndent(oldState, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, stateFileName), stBytes, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	// Round-trip: ReadState then UpdateState with a no-op mutator.
	_, st, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}
	if st.MaxResumes != 0 {
		t.Errorf("MaxResumes = %d, want 0 for old fixture", st.MaxResumes)
	}
	if st.Worker.ClaudeSessionID != "" {
		t.Errorf("ClaudeSessionID = %q, want empty for old fixture", st.Worker.ClaudeSessionID)
	}
	if st.Worker.ResumeCount != 0 {
		t.Errorf("ResumeCount = %d, want 0 for old fixture", st.Worker.ResumeCount)
	}

	// UpdateState no-op write should not change the state.
	if err := UpdateState(dir, func(cur *TaskState) (*TaskState, *TransitionLogEntry, error) {
		return cur, nil, nil
	}); err != nil {
		t.Fatalf("UpdateState no-op: %v", err)
	}

	_, after, err := ReadState(dir)
	if err != nil {
		t.Fatalf("ReadState after: %v", err)
	}
	if after.MaxResumes != 0 || after.Worker.ClaudeSessionID != "" || after.Worker.ResumeCount != 0 {
		t.Errorf("new fields mutated during no-op round-trip: MaxResumes=%d, ClaudeSessionID=%q, ResumeCount=%d",
			after.MaxResumes, after.Worker.ClaudeSessionID, after.Worker.ResumeCount)
	}
}
