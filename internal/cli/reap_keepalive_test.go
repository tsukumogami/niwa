package cli

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// mapEphemeralKeepAlive writes an ephemeral, keep-alive session mapping binding
// sessionID to instancePath, the shape an armed `niwa dispatch --keep-alive`
// records.
func mapEphemeralKeepAlive(t *testing.T, workspaceRoot, sessionID, instancePath string) {
	t.Helper()
	m := workspace.SessionMapping{
		SessionID:    sessionID,
		InstanceName: filepath.Base(instancePath),
		InstancePath: instancePath,
		Ephemeral:    true,
		Origin:       "dispatch",
		KeepAlive:    true,
	}
	if err := workspace.WriteSessionMapping(workspaceRoot, m); err != nil {
		t.Fatal(err)
	}
}

// TestReap_KeepAliveMapping_NoCoupling pins the no-new-reaper-coupling
// requirement (R9): the reaper never reads SessionMapping.KeepAlive, so a
// keep-alive mapping whose session's job entry is GONE is reclaimed exactly
// like any other dead ephemeral -- the marker must not defer or suppress
// reaping. (The spared direction needs no keep-alive variant: liveness keys
// purely on the job entry, covered by the existing spared tests.)
func TestReap_KeepAliveMapping_NoCoupling(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir() // empty: no job entry -> the session is dead

	inst := makeReapInstance(t, root, "test-ws-keepalive")
	mapEphemeralKeepAlive(t, root, reapDeadSessionID, inst)

	destroyed := stubDestroyAll(t)

	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatalf("reapWorkspace error: %v", err)
	}
	if n != 1 {
		t.Fatalf("reaped count = %d, want 1 (keep-alive must not suppress reaping)", n)
	}
	if len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("destroyed = %v, want [%s]", *destroyed, inst)
	}
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
		t.Error("keep-alive mapping for a dead session still present after reap; want deleted")
	}
}
