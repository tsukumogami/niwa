package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// seedSessionList writes a minimal instance layout with two persisted
// sessions: one whose daemon.pid points at the test process (alive), one
// without a daemon.pid file (dead). Returns the instance root path.
func seedSessionList(t *testing.T, includeLiveDaemon bool) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".niwa-marker"), []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(root, ".niwa", "sessions")

	// Session 1: live daemon (daemon.pid points at this test process).
	liveWT := filepath.Join(root, "wt-live")
	if err := os.MkdirAll(filepath.Join(liveWT, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	if includeLiveDaemon {
		pid := os.Getpid()
		startTime, _ := mcp.PIDStartTime(pid)
		pidContent := []byte(fmt.Sprintf("%d\n%d\n", pid, startTime))
		if err := os.WriteFile(filepath.Join(liveWT, ".niwa", "daemon.pid"), pidContent, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	// Session 2: dead daemon (no daemon.pid).
	deadWT := filepath.Join(root, "wt-dead")
	if err := os.MkdirAll(filepath.Join(deadWT, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, st := range []mcp.SessionLifecycleState{
		mcp.NewSessionLifecycleState("aabbccdd", "myrepo", "live test", "", liveWT),
		mcp.NewSessionLifecycleState("11223344", "myrepo", "dead test", "", deadWT),
	} {
		if err := mcp.WriteSessionLifecycleState(sessionsDir, st); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestSessionList_TableHasDaemonColumn verifies Issue 13: the default
// table view gains a DAEMON column rendering alive/dead.
func TestSessionList_TableHasDaemonColumn(t *testing.T) {
	root := seedSessionList(t, true)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListStatus = "active" // force lifecycle path

	stdout := &bytes.Buffer{}
	sessionListCmd.SetOut(stdout)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "DAEMON") {
		t.Errorf("table header missing DAEMON column:\n%s", out)
	}
	if !strings.Contains(out, "alive") {
		t.Errorf("expected at least one row to render 'alive':\n%s", out)
	}
	if !strings.Contains(out, "dead") {
		t.Errorf("expected at least one row to render 'dead':\n%s", out)
	}
}

// TestSessionList_JSONShape verifies --json emits an array of session
// rows, each carrying the embedded SessionLifecycleState plus a daemon
// sub-object with the alive/pid/started_at fields.
func TestSessionList_JSONShape(t *testing.T) {
	root := seedSessionList(t, true)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListJSON = true

	stdout := &bytes.Buffer{}
	sessionListCmd.SetOut(stdout)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	for _, r := range rows {
		daemon, ok := r["daemon"].(map[string]any)
		if !ok {
			t.Errorf("row missing daemon sub-object: %v", r)
			continue
		}
		if _, ok := daemon["alive"].(bool); !ok {
			t.Errorf("daemon.alive must be boolean: %v", daemon)
		}
	}
}

// TestSessionList_JSONEmptyArray verifies --json emits [] (not null) when
// no sessions match the filter.
func TestSessionList_JSONEmptyArray(t *testing.T) {
	root := seedSessionList(t, false)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListJSON = true
	sessionListRepo = "nonexistent"

	stdout := &bytes.Buffer{}
	sessionListCmd.SetOut(stdout)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	got := strings.TrimSpace(stdout.String())
	if got != "[]" {
		t.Errorf("empty result: got %q, want []", got)
	}
}

// TestSessionList_VerboseColumns verifies --verbose adds PID and STARTED-AT.
func TestSessionList_VerboseColumns(t *testing.T) {
	root := seedSessionList(t, true)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListVerbose = true

	stdout := &bytes.Buffer{}
	sessionListCmd.SetOut(stdout)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	out := stdout.String()
	if !strings.Contains(out, "PID") {
		t.Errorf("verbose header missing PID: %s", out)
	}
	if !strings.Contains(out, "STARTED-AT") {
		t.Errorf("verbose header missing STARTED-AT: %s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("%d", os.Getpid())) {
		t.Errorf("verbose row missing PID for live session: %s", out)
	}
}

// TestSessionList_EmptyResultMessage verifies the table view emits a
// "no sessions match" line when the filter yields no rows.
func TestSessionList_EmptyResultMessage(t *testing.T) {
	root := seedSessionList(t, false)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListRepo = "nonexistent"

	stdout := &bytes.Buffer{}
	sessionListCmd.SetOut(stdout)
	defer sessionListCmd.SetOut(os.Stdout)

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	if !strings.Contains(stdout.String(), "no sessions match") {
		t.Errorf("expected empty-state message; got: %s", stdout.String())
	}
}

func resetSessionListFlags(t *testing.T) {
	t.Helper()
	sessionListRepo = ""
	sessionListStatus = ""
	sessionListJSON = false
	sessionListVerbose = false
}
