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

func TestSessionList_FlaglessShowsLifecycleView(t *testing.T) {
	// PLAN issue 10 removed the flagless deprecation alias to `niwa mesh
	// list`. Flagless `niwa session list` now shows the lifecycle view
	// directly. With no sessions on disk this is just the table header
	// (no deprecation warning, no fall-through to mesh list).
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	defer resetSessionListFlags(t)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	sessionListCmd.SetOut(stdoutBuf)
	sessionListCmd.SetErr(stderrBuf)
	defer func() {
		sessionListCmd.SetOut(os.Stdout)
		sessionListCmd.SetErr(os.Stderr)
	}()

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	// Lifecycle view is the new default: column header SESSION_ID is
	// present in stdout.
	if !strings.Contains(stdout, "SESSION_ID") {
		t.Errorf("expected lifecycle view header SESSION_ID in stdout, got %q", stdout)
	}
	// Coordinator-registry markers (ROLE / PID columns from mesh list)
	// must NOT appear -- the alias is gone.
	if strings.Contains(stdout, "ROLE") || strings.Contains(stdout, "PENDING") {
		t.Errorf("flagless lifecycle view leaked mesh-list columns: %q", stdout)
	}
	// No deprecation warning on stderr.
	if strings.Contains(stderr, "deprecated") {
		t.Errorf("deprecation warning still present after PLAN issue 10: %q", stderr)
	}
}

func TestMeshList_StillWorksDirectly(t *testing.T) {
	// PLAN issue 10 removed the deprecated `niwa session list` -> `niwa
	// mesh list` alias, but `niwa mesh list` itself is unchanged. This
	// test seeds a coordinator registry and asserts the direct
	// invocation still renders.
	now := time.Now().UTC().Format(time.RFC3339)
	root := seedSessionRegistry(t, []mcp.SessionEntry{
		{ID: "id-coord", Role: "coordinator", PID: 99999, RegisteredAt: now},
	})
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	buf := &bytes.Buffer{}
	meshListCmd.SetOut(buf)
	defer meshListCmd.SetOut(os.Stdout)
	if err := runMeshList(meshListCmd, nil); err != nil {
		t.Fatalf("runMeshList: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "coordinator") {
		t.Errorf("expected coordinator row in mesh list output, got %q", out)
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
