package cli

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// Canonical fixture session ids (valid lowercase UUIDs). Their leading hex is
// used as the job-state directory name in production; the tests write job state
// under the full id for simplicity (readJobState's fast path).
const (
	reapDeadSessionID   = "11111111-1111-1111-1111-111111111111"
	reapLiveSessionID   = "22222222-2222-2222-2222-222222222222"
	reapNonEphSessionID = "33333333-3333-3333-3333-333333333333"
)

// makeReapInstance creates an instance directory under workspaceRoot named name,
// carrying a .niwa/instance.json (so EnumerateInstances discovers it) but no
// workspace.toml (so it is a destroyable instance, not a workspace root). It
// returns the instance's absolute path.
func makeReapInstance(t *testing.T, workspaceRoot, name string) string {
	t.Helper()
	dir := filepath.Join(workspaceRoot, name)
	state := &workspace.InstanceState{
		SchemaVersion: workspace.SchemaVersion,
		InstanceName:  name,
		Root:          dir,
		Repos:         map[string]workspace.RepoState{},
	}
	if err := workspace.SaveState(dir, state); err != nil {
		t.Fatal(err)
	}
	return dir
}

// mapEphemeral writes an ephemeral session mapping binding sessionID to the
// instance at instancePath under workspaceRoot.
func mapEphemeral(t *testing.T, workspaceRoot, sessionID, instancePath string, ephemeral bool) {
	t.Helper()
	m := workspace.SessionMapping{
		SessionID:    sessionID,
		InstanceName: filepath.Base(instancePath),
		InstancePath: instancePath,
		Ephemeral:    ephemeral,
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, m); err != nil {
		t.Fatal(err)
	}
}

// writeJobEntry writes a present job-state entry for sessionID under jobsDir, so
// the reaper's entry-present liveness rule reads the session as LIVE. The `body`
// JSON only needs to carry the matching sessionId; the reaper keys on the entry
// existing, not on any field inside it (DESIGN Decision 6). The dir is named by
// the full session id (readJobState's fast path).
func writeJobEntry(t *testing.T, jobsDir, sessionID string) {
	t.Helper()
	dir := filepath.Join(jobsDir, sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"sessionId":"` + sessionID + `","template":"` + bgJobTemplate + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// stubDestroyAll installs a fake destroyInstanceFunc that records every path it
// was asked to destroy, without touching the filesystem. It restores the
// original on cleanup and returns a pointer to the recorded slice.
func stubDestroyAll(t *testing.T) *[]string {
	t.Helper()
	var destroyed []string
	prev := destroyInstanceFunc
	destroyInstanceFunc = func(path string) error {
		destroyed = append(destroyed, path)
		return nil
	}
	t.Cleanup(func() { destroyInstanceFunc = prev })
	return &destroyed
}

// TestReap_DeadEphemeralOrphan_Reclaimed: an ephemeral instance whose session
// has no live job (job entry gone) is reclaimed -- destroyed and its mapping
// deleted.
func TestReap_DeadEphemeralOrphan_Reclaimed(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: no job for any session -> dead

	inst := makeReapInstance(t, root, "test-ws-dead")
	mapEphemeral(t, root, reapDeadSessionID, inst, true)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}

	// The mapping must be deleted after reclamation.
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
		t.Errorf("mapping for dead session still present after reap; want deleted")
	}
}

// TestReap_CompletedResumable_Spared: an ephemeral instance whose session
// recorded a terminal state but whose job entry is still present (the
// completed-but-resumable case) is SPARED. Under the old rule a terminal state
// reaped it the instant the task finished; entry-present liveness keeps it.
func TestReap_CompletedResumable_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-done")
	mapEphemeral(t, root, reapLiveSessionID, inst, true)
	// Job entry present (the session is still listed and resumable).
	writeJobEntry(t, jobsDir, reapLiveSessionID)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (completed-but-resumable must be spared)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (resumable instance must not be destroyed)", *destroyed)
	}
	if _, err := workspace.ReadSessionMapping(root, reapLiveSessionID); err != nil {
		t.Errorf("mapping for resumable session was deleted; want retained: %v", err)
	}
}

// TestReap_LiveIdleEphemeral_Spared: an ephemeral instance whose session's job
// entry is present (a live or idle-but-resumable worker) is SPARED -- never
// destroyed, mapping retained.
func TestReap_LiveIdleEphemeral_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-live")
	mapEphemeral(t, root, reapLiveSessionID, inst, true)
	writeJobEntry(t, jobsDir, reapLiveSessionID)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (live instance must be spared)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (live instance must not be destroyed)", *destroyed)
	}
	if _, err := workspace.ReadSessionMapping(root, reapLiveSessionID); err != nil {
		t.Errorf("mapping for live session was deleted; want retained: %v", err)
	}
}

// TestReap_MappedDeadButLiveJobRooted_Spared: belt-and-suspenders for the
// primary sweep. An ephemeral instance whose MAPPING reads dead by the
// entry-present rule (no job entry for its session id) is nonetheless SPARED
// when a live Claude Code job is rooted inside it (a mis-keyed or stale mapping
// must never let the reaper delete a directory a running session lives in).
func TestReap_MappedDeadButLiveJobRooted_Spared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-stale")
	// Mapping points at a session with NO job entry -> sessionLive reads dead.
	mapEphemeral(t, root, reapDeadSessionID, inst, true)
	// But a live worker is actually rooted in the instance (its cwd is the dir),
	// recorded under a job whose name is not a prefix of reapDeadSessionID.
	writeJobStateCwd(t, jobsDir, "0000aa11", inst)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (a live-rooted instance must be spared even with a dead mapping)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (live instance must not be destroyed)", *destroyed)
	}
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err != nil {
		t.Errorf("mapping deleted while a live job was rooted in the instance; want retained: %v", err)
	}
}

// TestReap_NonEphemeralInstance_NeverTargeted: a developer (non-ephemeral)
// instance is NEVER reaped, even with a dead session and an empty jobs dir. The
// ephemeral marker is the load-bearing guard.
func TestReap_NonEphemeralInstance_NeverTargeted(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: every session reads as dead

	inst := makeReapInstance(t, root, "test-ws-dev")
	// A mapping exists but is explicitly NOT ephemeral.
	mapEphemeral(t, root, reapNonEphSessionID, inst, false)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (non-ephemeral must never be reaped)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want [] (non-ephemeral instance must not be destroyed)", *destroyed)
	}
}

// TestReap_NoMapping_NotTargeted: an instance with no session mapping at all is
// never a target -- there is no ephemeral provenance and no session id to
// declare dead.
func TestReap_NoMapping_NotTargeted(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()

	makeReapInstance(t, root, "test-ws-orphan") // no mapping written

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 0 {
		t.Fatalf("reaped count = %d, want 0 (instance with no mapping is not a target)", n)
	}
	if len(*destroyed) != 0 {
		t.Fatalf("destroyed = %v, want []", *destroyed)
	}
}

// TestReap_MixedWorkspace_OnlyDeadEphemeralReaped: a workspace containing a dead
// ephemeral orphan, a live ephemeral worker, and a non-ephemeral developer
// instance reaps exactly the dead ephemeral one.
func TestReap_MixedWorkspace_OnlyDeadEphemeralReaped(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	dead := makeReapInstance(t, root, "test-ws-dead")
	live := makeReapInstance(t, root, "test-ws-live")
	dev := makeReapInstance(t, root, "test-ws-dev")

	mapEphemeral(t, root, reapDeadSessionID, dead, true)   // no job -> dead
	mapEphemeral(t, root, reapLiveSessionID, live, true)   // live job
	mapEphemeral(t, root, reapNonEphSessionID, dev, false) // non-ephemeral

	writeJobEntry(t, jobsDir, reapLiveSessionID)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != dead {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, dead)
	}

	// Live and dev mappings survive; dead mapping is gone.
	if _, err := workspace.ReadSessionMapping(root, reapLiveSessionID); err != nil {
		t.Errorf("live mapping deleted; want retained: %v", err)
	}
	if _, err := workspace.ReadSessionMapping(root, reapNonEphSessionID); err != nil {
		t.Errorf("dev mapping deleted; want retained: %v", err)
	}
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
		t.Errorf("dead mapping retained; want deleted")
	}
}

// TestSelectReapTargets_DeterministicSelection exercises the pure selection
// logic (no destroy) across the full matrix in one workspace and asserts the
// exact set of targets, independent of the destroy path.
func TestSelectReapTargets_DeterministicSelection(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	dead := makeReapInstance(t, root, "test-ws-dead")
	live := makeReapInstance(t, root, "test-ws-live")
	dev := makeReapInstance(t, root, "test-ws-dev")

	mapEphemeral(t, root, reapDeadSessionID, dead, true)
	mapEphemeral(t, root, reapLiveSessionID, live, true)
	mapEphemeral(t, root, reapNonEphSessionID, dev, false)
	writeJobEntry(t, jobsDir, reapLiveSessionID)

	targets, err := selectReapTargets(root, jobsDir, now)
	if err != nil {
		t.Fatalf("selectReapTargets error: %v", err)
	}

	gotPaths := make([]string, 0, len(targets))
	for _, tg := range targets {
		gotPaths = append(gotPaths, tg.InstancePath)
	}
	sort.Strings(gotPaths)

	want := []string{dead}
	if len(gotPaths) != len(want) || gotPaths[0] != want[0] {
		t.Fatalf("targets = %v, want %v", gotPaths, want)
	}
	if targets[0].SessionID != reapDeadSessionID {
		t.Errorf("target session id = %q, want %q", targets[0].SessionID, reapDeadSessionID)
	}
}
