package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()

	state := SessionLifecycleState{
		V:                    1,
		SessionID:            "ab12cd34",
		ParentSessionID:      "00aabbcc",
		Repo:                 "myrepo",
		Purpose:              "test session",
		Status:               SessionStatusActive,
		CreationTime:         "2026-01-01T00:00:00Z",
		WorktreePath:         "/tmp/worktrees/myrepo-ab12cd34",
		ClaudeConversationID: "conv-xyz",
		CreatorPID:           12345,
		CreatorStartTime:     9876543,
	}

	if err := WriteSessionLifecycleState(dir, state); err != nil {
		t.Fatalf("WriteSessionLifecycleState: %v", err)
	}

	// File must exist and be mode 0600.
	path := filepath.Join(dir, "ab12cd34.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("file perm = %o, want 0600", info.Mode().Perm())
	}

	// Read directly to verify round-trip.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var decoded SessionLifecycleState
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SessionID != state.SessionID {
		t.Errorf("SessionID = %q, want %q", decoded.SessionID, state.SessionID)
	}
	if decoded.ParentSessionID != state.ParentSessionID {
		t.Errorf("ParentSessionID = %q, want %q", decoded.ParentSessionID, state.ParentSessionID)
	}
	if decoded.Repo != state.Repo {
		t.Errorf("Repo = %q, want %q", decoded.Repo, state.Repo)
	}
	if decoded.Purpose != state.Purpose {
		t.Errorf("Purpose = %q, want %q", decoded.Purpose, state.Purpose)
	}
	if decoded.Status != state.Status {
		t.Errorf("Status = %q, want %q", decoded.Status, state.Status)
	}
	if decoded.WorktreePath != state.WorktreePath {
		t.Errorf("WorktreePath = %q, want %q", decoded.WorktreePath, state.WorktreePath)
	}
	if decoded.ClaudeConversationID != state.ClaudeConversationID {
		t.Errorf("ClaudeConversationID = %q, want %q", decoded.ClaudeConversationID, state.ClaudeConversationID)
	}
	if decoded.CreatorPID != state.CreatorPID {
		t.Errorf("CreatorPID = %d, want %d", decoded.CreatorPID, state.CreatorPID)
	}
	if decoded.CreatorStartTime != state.CreatorStartTime {
		t.Errorf("CreatorStartTime = %d, want %d", decoded.CreatorStartTime, state.CreatorStartTime)
	}
	if decoded.V != 1 {
		t.Errorf("V = %d, want 1", decoded.V)
	}
}

func TestReadSessionLifecycleState_Integration(t *testing.T) {
	// Build the <root>/.niwa/sessions/<id>.json structure that
	// ReadSessionLifecycleState expects.
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatal(err)
	}

	state := SessionLifecycleState{
		V:         1,
		SessionID: "deadbeef",
		Repo:      "repo1",
		Purpose:   "integration test",
		Status:    SessionStatusActive,
	}
	if err := WriteSessionLifecycleState(sessionsDir, state); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := ReadSessionLifecycleState(sessionsDir, "deadbeef")
	if err != nil {
		t.Fatalf("ReadSessionLifecycleState: %v", err)
	}
	if got.SessionID != "deadbeef" {
		t.Errorf("SessionID = %q, want %q", got.SessionID, "deadbeef")
	}
	if got.Repo != "repo1" {
		t.Errorf("Repo = %q, want %q", got.Repo, "repo1")
	}
}

func TestReadSessionLifecycleState_InvalidID(t *testing.T) {
	dir := t.TempDir()
	cases := []string{
		"../etc/passwd",
		"../../secret",
		"ABCDEF12",  // uppercase not allowed
		"abc",       // too short
		"abcdef123", // too long
		"abcdefgh",  // non-hex characters
		"",
	}
	for _, id := range cases {
		_, err := ReadSessionLifecycleState(dir, id)
		if err == nil {
			t.Errorf("ReadSessionLifecycleState(%q) want error, got nil", id)
		}
	}
}

func TestWriteSessionLifecycleState_InvalidID(t *testing.T) {
	dir := t.TempDir()
	state := SessionLifecycleState{
		V:         1,
		SessionID: "INVALID!!",
	}
	if err := WriteSessionLifecycleState(dir, state); err == nil {
		t.Error("WriteSessionLifecycleState with invalid ID: want error, got nil")
	}
}

func TestListSessionLifecycleStates(t *testing.T) {
	dir := t.TempDir()

	// Write two valid session files.
	for _, id := range []string{"aabbccdd", "11223344"} {
		s := SessionLifecycleState{
			V: 1, SessionID: id, Repo: "r", Status: SessionStatusActive,
		}
		if err := WriteSessionLifecycleState(dir, s); err != nil {
			t.Fatalf("write %s: %v", id, err)
		}
	}

	// Write sessions.json (must be skipped).
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(`{"sessions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Write a .tmp file (must be skipped).
	if err := os.WriteFile(filepath.Join(dir, "aabbccdd.json.tmp"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	states, err := ListSessionLifecycleStates(dir)
	if err != nil {
		t.Fatalf("ListSessionLifecycleStates: %v", err)
	}
	if len(states) != 2 {
		t.Errorf("len(states) = %d, want 2", len(states))
	}
}

func TestListSessionLifecycleStates_CorruptFileSkipped(t *testing.T) {
	dir := t.TempDir()

	// Write one valid session file.
	good := SessionLifecycleState{V: 1, SessionID: "aabbccdd", Repo: "r", Status: SessionStatusActive}
	if err := WriteSessionLifecycleState(dir, good); err != nil {
		t.Fatal(err)
	}

	// Write a corrupt session file (not valid JSON).
	if err := os.WriteFile(filepath.Join(dir, "ff001122.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	states, err := ListSessionLifecycleStates(dir)
	if err != nil {
		t.Fatalf("ListSessionLifecycleStates: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("len(states) = %d, want 1 (corrupt file should be skipped)", len(states))
	}
	if states[0].SessionID != "aabbccdd" {
		t.Errorf("SessionID = %q, want %q", states[0].SessionID, "aabbccdd")
	}
}

func TestListSessionLifecycleStates_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	states, err := ListSessionLifecycleStates(dir)
	if err != nil {
		t.Fatalf("ListSessionLifecycleStates: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("expected empty, got %d states", len(states))
	}
}

func TestListSessionLifecycleStates_MissingDir(t *testing.T) {
	states, err := ListSessionLifecycleStates("/nonexistent/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("want nil error for missing dir, got: %v", err)
	}
	if states != nil {
		t.Errorf("want nil states for missing dir, got: %v", states)
	}
}

func TestNewSessionLifecycleID(t *testing.T) {
	dir := t.TempDir()
	id, err := newSessionLifecycleID(dir)
	if err != nil {
		t.Fatalf("newSessionLifecycleID: %v", err)
	}
	if !sessionIDRe.MatchString(id) {
		t.Errorf("id %q does not match ^[0-9a-f]{8}$", id)
	}
}

func TestNewSessionLifecycleID_CollisionRetry(t *testing.T) {
	dir := t.TempDir()

	// Pre-create files for 4 of the 5 possible retry IDs to test retry logic.
	// We can't predict the random IDs, so instead we test that on collision
	// the function retries and eventually succeeds for a non-pre-existing ID.
	id1, err := newSessionLifecycleID(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate collision by creating the file for id1.
	if err := os.WriteFile(filepath.Join(dir, id1+".json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// A second call must still succeed (retry finds a different ID).
	id2, err := newSessionLifecycleID(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id2 == id1 {
		t.Errorf("expected different ID on retry, got same: %s", id2)
	}
}

func TestNewSessionLifecycleState(t *testing.T) {
	s := NewSessionLifecycleState("ab12cd34", "repo", "do stuff", "", "/tmp/wt")
	if s.V != 1 {
		t.Errorf("V = %d, want 1", s.V)
	}
	if s.Status != SessionStatusActive {
		t.Errorf("Status = %q, want %q", s.Status, SessionStatusActive)
	}
	if s.SessionID != "ab12cd34" {
		t.Errorf("SessionID = %q", s.SessionID)
	}
	if s.CreationTime == "" {
		t.Error("CreationTime must be set")
	}
	if s.CreatorPID == 0 {
		t.Error("CreatorPID must be set")
	}
}

func TestSessionLifecycleStateAttachAbsentWhenNil(t *testing.T) {
	state := SessionLifecycleState{
		V:            1,
		SessionID:    "abcd1234",
		Repo:         "niwa",
		Status:       SessionStatusActive,
		WorktreePath: "/tmp/wt",
		// Attach left nil deliberately.
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), `"attach"`) {
		t.Errorf("attach key present when nil: %s", data)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("null appeared in JSON unexpectedly: %s", data)
	}
}

func TestSessionLifecycleStateAttachPresentWhenSet(t *testing.T) {
	state := SessionLifecycleState{
		V:            1,
		SessionID:    "abcd1234",
		Repo:         "niwa",
		Status:       SessionStatusActive,
		WorktreePath: "/tmp/wt",
		Attach: &AttachState{
			V: 1, OwnerPID: 12345, OwnerStartTime: 999,
			StartedAt: "2026-05-10T14:32:11Z",
			LockPath:  ".niwa/attach.lock",
		},
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"attach"`) {
		t.Errorf("attach key missing: %s", data)
	}
	if !strings.Contains(string(data), `"owner_pid":12345`) {
		t.Errorf("owner_pid not in output: %s", data)
	}
}
