package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// seedSessionRegistry writes sessions.json with the given entries. All
// entries are assumed to be coordinator-role since workers never
// register (AC-O1). Returns the instance root so tests can use it as
// NIWA_INSTANCE_ROOT.
func seedSessionRegistry(t *testing.T, entries []mcp.SessionEntry) string {
	t.Helper()
	root := t.TempDir()
	sessionsDir := filepath.Join(root, ".niwa", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	registry := mcp.SessionRegistry{Sessions: entries}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sessionsDir, "sessions.json"), data, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return root
}

func TestSessionList_EmptyRegistryShowsHeaderOnly(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	buf := &bytes.Buffer{}
	sessionListCmd.SetOut(buf)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	for _, col := range []string{"ROLE", "PID", "STATUS", "LAST-SEEN", "PENDING"} {
		if !strings.Contains(out, col) {
			t.Errorf("header missing column %q in %q", col, out)
		}
	}
}

func TestSessionList_CoordinatorOnlyView(t *testing.T) {
	// Seed two coordinator entries (role names pulled from team topology).
	// Workers never appear in sessions.json by design (AC-O1, AC-O2): the
	// register handler only writes coordinator-role sessions, so
	// `session list` reads whatever the registry contains, which is
	// guaranteed coordinator-only upstream. This test locks in that
	// list renders the registry verbatim with liveness + pending count
	// columns — no worker-specific filtering required here.
	now := time.Now().UTC().Format(time.RFC3339)
	root := seedSessionRegistry(t, []mcp.SessionEntry{
		{ID: "id-coord", Role: "coordinator", PID: 99999, RegisteredAt: now},
		{ID: "id-second", Role: "frontend", PID: 99998, RegisteredAt: now},
	})
	// Seed a pending message for coordinator to exercise the pending
	// count column.
	inboxDir := filepath.Join(root, ".niwa", "roles", "coordinator", "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("mkdir inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "msg.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write msg: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	buf := &bytes.Buffer{}
	sessionListCmd.SetOut(buf)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "coordinator") {
		t.Errorf("expected coordinator row in %q", out)
	}
	if !strings.Contains(out, "frontend") {
		t.Errorf("expected frontend row in %q", out)
	}
	// PID 99999 very likely does not exist → status should render as dead.
	if !strings.Contains(out, "dead") {
		t.Errorf("expected 'dead' status for stale PID in %q", out)
	}
}

func TestCountPendingInbox(t *testing.T) {
	root := t.TempDir()
	inboxDir := filepath.Join(root, ".niwa", "roles", "coord", "inbox")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 2 pending messages, 1 subdir (should be ignored), 1 non-JSON file
	// (should be ignored).
	for _, name := range []string{"a.json", "b.json"} {
		if err := os.WriteFile(filepath.Join(inboxDir, name), []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(inboxDir, "in-progress"), 0o700); err != nil {
		t.Fatalf("mkdir subdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "note.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatalf("write non-json: %v", err)
	}
	if got := countPendingInbox(root, "coord"); got != 2 {
		t.Errorf("countPendingInbox = %d, want 2", got)
	}
	// Missing inbox returns 0 (not an error).
	if got := countPendingInbox(root, "missing"); got != 0 {
		t.Errorf("missing inbox should return 0, got %d", got)
	}
}
