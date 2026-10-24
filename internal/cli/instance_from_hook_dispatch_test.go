package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// TestSessionStart_DispatchMapping_NoOp: a session whose id already has a
// dispatch-written mapping (a dispatched worker resumed with its SessionStart
// at the workspace root) passes every other guard, yet provisions nothing and
// leaves the mapping byte for byte as dispatch wrote it.
func TestSessionStart_DispatchMapping_NoOp(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	rec := stubProvision(t, "# instance guidance\n")
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "ws-dispatched",
		InstancePath: filepath.Join(root, "ws-dispatched"),
		Ephemeral:    true,
		Origin:       "dispatch",
		DispatchID:   "3c803591-4c01-486f-a927-f96ec399d6a7",
	}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".niwa", "sessions", testSessionID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	out, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           root,
	}, jobsDir)
	if err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if rec.called {
		t.Error("provisioned an instance for a session dispatch already mapped")
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the dispatch mapping changed:\nbefore %s\nafter  %s", before, after)
	}
}

// TestSessionStart_HookMapping_StillProvisions: only a dispatch mapping stops
// the hook; a mapping a hook wrote earlier for the same id doesn't.
func TestSessionStart_HookMapping_StillProvisions(t *testing.T) {
	root := setupHookWorkspace(t, true)
	jobsDir := t.TempDir()
	writeJobState(t, jobsDir, testSessionID[:8], testSessionID, "bg")
	rec := stubProvision(t, "# instance guidance\n")
	if err := workspace.WriteSessionMapping(root, workspace.SessionMapping{
		SessionID:    testSessionID,
		InstanceName: "ws-old",
		InstancePath: filepath.Join(root, "ws-old"),
		Ephemeral:    true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runStart(t, instanceHookPayload{
		HookEventName: hookEventSessionStart,
		SessionID:     testSessionID,
		Cwd:           root,
	}, jobsDir); err != nil {
		t.Fatalf("runInstanceHookStart: %v", err)
	}
	if !rec.called {
		t.Error("a hook-written mapping stopped provisioning")
	}
}
