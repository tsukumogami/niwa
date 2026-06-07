package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// seedSessionList writes a minimal instance layout with two persisted
// sessions. Returns the instance root path.
func seedSessionList(t *testing.T) string {
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

	wt1 := filepath.Join(root, "wt-1")
	if err := os.MkdirAll(filepath.Join(wt1, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	wt2 := filepath.Join(root, "wt-2")
	if err := os.MkdirAll(filepath.Join(wt2, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, st := range []worktree.SessionLifecycleState{
		worktree.NewSessionLifecycleState("aabbccdd", "myrepo", "first test", "", wt1, ""),
		worktree.NewSessionLifecycleState("11223344", "myrepo", "second test", "", wt2, ""),
	} {
		if err := worktree.WriteSessionLifecycleState(sessionsDir, st); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestSessionList_TableColumns verifies the default table view renders the
// expected columns and omits the removed DAEMON column.
func TestSessionList_TableColumns(t *testing.T) {
	root := seedSessionList(t)
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
	for _, col := range []string{"SESSION_ID", "REPO", "STATUS", "AVAILABILITY", "CREATED", "PURPOSE"} {
		if !strings.Contains(out, col) {
			t.Errorf("table header missing %s column:\n%s", col, out)
		}
	}
	if strings.Contains(out, "DAEMON") {
		t.Errorf("table header still renders the removed DAEMON column:\n%s", out)
	}
}

// TestSessionList_JSONShape verifies --json emits an array of session
// rows, each carrying the embedded SessionLifecycleState plus the
// CLI-side availability projection, and no longer carrying a daemon
// sub-object.
func TestSessionList_JSONShape(t *testing.T) {
	root := seedSessionList(t)
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
		if _, ok := r["availability"].(string); !ok {
			t.Errorf("row missing availability projection: %v", r)
		}
		if _, ok := r["daemon"]; ok {
			t.Errorf("row still carries removed daemon sub-object: %v", r)
		}
	}
}

// TestSessionList_JSONEmptyArray verifies --json emits [] (not null) when
// no sessions match the filter.
func TestSessionList_JSONEmptyArray(t *testing.T) {
	root := seedSessionList(t)
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

// TestSessionList_JSONAttachShapeMatchesMCP verifies the wire-shape
// contract introduced by the blocker-3 fix: when a live attach lock is
// held, --json emits a top-level `attach` sub-object with the same
// shape niwa_list_sessions returns (v, owner_pid, owner_start_time,
// started_at, lock_path). When no lock is held, the `attach` key is
// absent (per PRD R12's "absent, not null" contract). A separate
// CLI-only `availability` string is always present so JSON consumers
// can distinguish `stale` from `available` without walking PIDs.
func TestSessionList_JSONAttachShapeMatchesMCP(t *testing.T) {
	// Seed one session with a live attach.state sentinel and one without.
	root := seedSessionList(t)
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	// Seed the live attach sentinel on the first session worktree.
	liveWT := filepath.Join(root, "wt-1")
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	if err := worktree.WriteAttachState(liveWT, worktree.AttachState{
		V:              1,
		OwnerPID:       myPID,
		OwnerStartTime: myStart,
		StartedAt:      "2026-05-10T14:32:11Z",
		LockPath:       ".niwa/attach.lock",
	}); err != nil {
		t.Fatalf("seed attach sentinel: %v", err)
	}

	resetSessionListFlags(t)
	t.Cleanup(func() { resetSessionListFlags(t) })
	sessionListJSON = true
	sessionListStatus = "active"

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

	// Find the attached row (the one we seeded the sentinel on).
	var attachedRow, freeRow map[string]any
	for _, r := range rows {
		avail, _ := r["availability"].(string)
		switch avail {
		case "attached":
			attachedRow = r
		case "available":
			freeRow = r
		}
	}
	if attachedRow == nil {
		t.Fatalf("no row marked availability=attached:\n%s", stdout.String())
	}
	if freeRow == nil {
		t.Fatalf("no row marked availability=available:\n%s", stdout.String())
	}

	// Attached row: the `attach` key must be a full sub-object with the
	// MCP shape, NOT a CLI-narrower `{availability: ...}` shape.
	attach, ok := attachedRow["attach"].(map[string]any)
	if !ok {
		t.Fatalf("attached row missing `attach` sub-object: %v", attachedRow)
	}
	for _, k := range []string{"v", "owner_pid", "owner_start_time", "started_at", "lock_path"} {
		if _, found := attach[k]; !found {
			t.Errorf("attach sub-object missing %q key: %v", k, attach)
		}
	}
	// Specifically verify the operator-facing fields parse to expected types.
	if pid, ok := attach["owner_pid"].(float64); !ok || int(pid) != myPID {
		t.Errorf("attach.owner_pid = %v, want %d", attach["owner_pid"], myPID)
	}
	if started, ok := attach["started_at"].(string); !ok || started != "2026-05-10T14:32:11Z" {
		t.Errorf("attach.started_at = %q, want %q", attach["started_at"], "2026-05-10T14:32:11Z")
	}
	// Cross-check: this row's old `attach.availability` nested key must NOT
	// be present (it was the wire-divergent shape the fix removed).
	if _, found := attach["availability"]; found {
		t.Errorf("attach sub-object should NOT carry an embedded `availability` key: %v", attach)
	}

	// Free row: the `attach` key must be absent (not null, not present).
	// Compare the marshaled bytes since map[string]any treats absent and
	// null identically once parsed.
	freeBytes, _ := json.Marshal(freeRow)
	if strings.Contains(string(freeBytes), `"attach"`) {
		t.Errorf("free row has `attach` key when no lock is held: %s", freeBytes)
	}
	if !strings.Contains(string(freeBytes), `"availability":"available"`) {
		t.Errorf("free row missing availability=available: %s", freeBytes)
	}
}

// TestSessionList_EmptyResultMessage verifies the table view emits a
// "no sessions match" line when the filter yields no rows.
func TestSessionList_EmptyResultMessage(t *testing.T) {
	root := seedSessionList(t)
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
	sessionListAttached = false
	sessionListAvailable = false
	sessionListJSON = false
}
