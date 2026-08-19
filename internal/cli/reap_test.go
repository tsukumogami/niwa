package cli

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
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

// stubRemoveTrust installs a fake removeInstanceTrustFunc that records every path
// whose trust entry reap asked to remove, restoring the original on cleanup.
func stubRemoveTrust(t *testing.T) *[]string {
	t.Helper()
	var untrusted []string
	prev := removeInstanceTrustFunc
	removeInstanceTrustFunc = func(path string) error {
		untrusted = append(untrusted, path)
		return nil
	}
	t.Cleanup(func() { removeInstanceTrustFunc = prev })
	return &untrusted
}

// TestReap_RemovesWorkspaceTrust: reclaiming an instance also removes its Claude
// Code workspace-trust entry, so a successfully-staged review's trust grant does
// not outlive the instance it was granted for.
func TestReap_RemovesWorkspaceTrust(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: no job for any session -> dead

	inst := makeReapInstance(t, root, "test-ws-trust")
	mapEphemeral(t, root, reapDeadSessionID, inst, true)

	stubDestroyAll(t)
	untrusted := stubRemoveTrust(t)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1", n)
	}
	if len(*untrusted) != 1 || (*untrusted)[0] != inst {
		t.Fatalf("reap must remove the trust entry for the reclaimed instance; untrusted = %v, want [%s]", *untrusted, inst)
	}
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

// TestReap_MappingWhoseLivenessCannotBeRead_Spared is the safety property the
// mapping's agent field exists for, and it is written against the declaration
// rather than against a named agent so it keeps meaning something as the table
// changes.
//
// The entry-present rule needs a record that disappears when the developer
// deletes a session. An agent niwa launches no worker for has no record store
// at all; an agent whose records are never removed has one that says a session
// once existed and nothing about whether it still does. Either way the sweep
// has no evidence, and the store it reads comes back empty -- which is
// indistinguishable from "the developer deleted this session". Reclaiming on
// that destroys the instance a working session lives in, silently.
//
// The reaper therefore spares anything it cannot prove is gone. Sparing an
// instance nobody is using costs a directory; the other mistake costs the work
// in it.
func TestReap_MappingWhoseLivenessCannotBeRead_Spared(t *testing.T) {
	for _, ag := range agent.All() {
		spec, hasSpec := agentplan.For(ag).LaunchSpec()
		if hasSpec && spec.Records.Liveness == agentplan.LivenessRecordPresence {
			// This agent's records do answer the question; the sweep is
			// entitled to act on them, and the tests around this one cover it.
			continue
		}

		t.Run(string(ag), func(t *testing.T) {
			root := setupHookWorkspace(t, true)
			jobsDir := t.TempDir()
			now := time.Now()

			inst := makeReapInstance(t, root, "test-ws-unreadable")
			m := workspace.SessionMapping{
				SessionID:    reapDeadSessionID,
				InstanceName: filepath.Base(inst),
				InstancePath: inst,
				Ephemeral:    true,
				Agent:        string(ag),
			}
			if err := workspace.WriteSessionMapping(root, m); err != nil {
				t.Fatal(err)
			}

			destroyed := stubDestroyAll(t)

			n, err := reapWorkspace(root, jobsDir, now)
			if err != nil {
				t.Fatalf("reapWorkspace error: %v", err)
			}
			if n != 0 {
				t.Fatalf("reaped count = %d, want 0 (a session whose liveness cannot be read must never be reclaimed)", n)
			}
			if len(*destroyed) != 0 {
				t.Fatalf("destroyed = %v, want []", *destroyed)
			}
			if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err != nil {
				t.Fatalf("the mapping must survive a spared sweep: %v", err)
			}
		})
	}
}

// TestReap_SparedInstancesAreReported is the visible half of a declared gap.
//
// An instance whose liveness cannot be read is spared forever, which means it
// accumulates. A sweep that spares something and says nothing is
// indistinguishable from a sweep that found nothing, so the accumulation has no
// symptom until somebody counts directories. This asserts every spared instance
// is named, with a reason, and with something to do about it.
func TestReap_SparedInstancesAreReported(t *testing.T) {
	unreadable := []agent.Agent{}
	for _, ag := range agent.All() {
		if _, spared := livenessUnreadable(string(ag)); spared {
			unreadable = append(unreadable, ag)
		}
	}
	if len(unreadable) == 0 {
		t.Skip("every agent's liveness is readable; nothing is spared for this reason")
	}

	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()

	inst := makeReapInstance(t, root, "test-ws-spared")
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    reapDeadSessionID,
		InstanceName: filepath.Base(inst),
		InstancePath: inst,
		Ephemeral:    true,
		Agent:        string(unreadable[0]),
	}); err != nil {
		t.Fatal(err)
	}

	_, spared, err := selectReapTargets(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("selectReapTargets: %v", err)
	}
	if len(spared) != 1 {
		t.Fatalf("spared %d instance(s), want 1", len(spared))
	}
	if spared[0].Path != inst {
		t.Errorf("spared path = %q, want %q", spared[0].Path, inst)
	}
	if spared[0].Reason == "" {
		t.Error("an instance was spared with no reason; a reader cannot act on that")
	}
	if !strings.Contains(spared[0].Reason, string(unreadable[0])) {
		t.Errorf("the reason %q does not name the agent it is about", spared[0].Reason)
	}
}

// TestReap_MalformedAgentIsSparedAndNamed covers the value nothing validates on
// the way in. A mapping recording an agent this build cannot parse is spared
// permanently -- an instance leak rather than data loss, which is the right
// direction -- but silently it would be a leak with no symptom at all.
func TestReap_MalformedAgentIsSparedAndNamed(t *testing.T) {
	reason, spared := livenessUnreadable("Claude")
	if !spared {
		t.Fatal("a mapping recording an unparseable agent was judged readable")
	}
	if !strings.Contains(reason, "Claude") {
		t.Errorf("the reason %q does not quote the value it could not parse", reason)
	}
}

// TestReap_MappingWithNoAgentRecordedIsClaude pins the compatibility reading of
// an absent agent field. Every mapping written before the field existed
// describes a Claude session, because that is the only kind niwa wrote one for,
// and the zero Agent is Claude by internal/agent's own contract. A mapping with
// no agent and no job entry is therefore still reclaimable, exactly as it was.
func TestReap_MappingWithNoAgentRecordedIsClaude(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	now := time.Now()

	inst := makeReapInstance(t, root, "test-ws-legacy")
	mapEphemeral(t, root, reapDeadSessionID, inst, true)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, now)
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1 (an agent-less mapping reads as Claude and its session is gone)", n)
	}
	if len(*destroyed) != 1 {
		t.Fatalf("destroyed = %v, want one instance", *destroyed)
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

	targets, _, err := selectReapTargets(root, jobsDir, now)
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
