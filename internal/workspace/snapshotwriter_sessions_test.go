package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const preservedSessionID = "01a00000-0000-7000-8000-0000000000aa"

// planSnapshotWorkspace returns a workspace root whose .niwa is an existing
// GitHub-sourced snapshot, ready for a drift refresh.
func planSnapshotWorkspace(t *testing.T) (root, configDir string) {
	t.Helper()
	root = t.TempDir()
	configDir = filepath.Join(root, StateDir)
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWriteMarker(t, configDir, Provenance{
		SourceURL: "org/repo", Owner: "org", Repo: "repo",
		ResolvedCommit: "old-oid", FetchedAt: time.Now(), FetchMechanism: "github-tarball",
	})
	return root, configDir
}

// driftedFetcher serves a config repo that has moved on, which is what makes
// the swap fire. For a GitHub source the refresh short-circuits unless upstream
// has drifted, so the real-world trigger is a teammate pushing to the shared
// config repo -- not anything the dispatching developer does.
func driftedFetcher(t *testing.T) *fakeFetcher {
	t.Helper()
	return &fakeFetcher{
		tarball: makeFakeTarball(t, map[string]string{
			"wrap/":               "",
			"wrap/workspace.toml": "name = updated",
		}),
		commitOID: "new-oid",
	}
}

// TestEnsureConfigSnapshot_PreservesSessionMappingsAcrossRefresh is the defect:
// dispatch writes its session mapping to <workspaceRoot>/.niwa/sessions/, and
// the snapshot writer rotates that whole directory. Only instance.json and
// dispatch-briefs/ were carried across, so a refresh took every mapping in the
// workspace with it -- the resume handle the dispatch printed once became
// unrecoverable, and the reaper's mapped sweep lost the join between an
// instance and the session that owns it.
func TestEnsureConfigSnapshot_PreservesSessionMappingsAcrossRefresh(t *testing.T) {
	root, configDir := planSnapshotWorkspace(t)

	planted := SessionMapping{
		SessionID:    preservedSessionID,
		InstanceName: "ws+task-0000cafe",
		InstancePath: filepath.Join(root, "ws+task-0000cafe"),
		Agent:        "codex",
		Ephemeral:    true,
		Origin:       "dispatch",
		KeepAlive:    true,
	}
	if err := WriteSessionMapping(root, planted); err != nil {
		t.Fatal(err)
	}

	if err := EnsureConfigSnapshot(context.Background(), configDir, driftedFetcher(t), nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	// Upstream content really did land, so the swap really did happen.
	if got, err := os.ReadFile(filepath.Join(configDir, "workspace.toml")); err != nil {
		t.Fatalf("workspace.toml missing after refresh: %v", err)
	} else if string(got) != "name = updated" {
		t.Errorf("upstream content not refreshed: got %q", got)
	}

	// The handle is still reachable.
	got, err := ReadSessionMapping(root, preservedSessionID)
	if err != nil {
		t.Fatalf("session mapping destroyed by the snapshot swap: %v", err)
	}
	if got.Agent != planted.Agent || got.InstancePath != planted.InstancePath || !got.KeepAlive {
		t.Errorf("session mapping changed across refresh\n  was:  %+v\n  now:  %+v", planted, got)
	}

	// And the reaper's sweep still has its join: it enumerates the store rather
	// than reading one id it was handed.
	mappings, err := ListSessionMappings(root)
	if err != nil {
		t.Fatalf("listing session mappings after refresh: %v", err)
	}
	if len(mappings) != 1 || mappings[0].SessionID != preservedSessionID {
		t.Fatalf("the mapped sweep found %d mappings after refresh, want the one that was planted: %+v", len(mappings), mappings)
	}
}

// TestEnsureConfigSnapshot_PreservedSessionStoreKeepsItsMode checks the
// carry-over does not widen what the store asked for. The store creates its
// directory 0700 and its files 0600; a copy that recreated the directory 0755
// would publish which sessions a developer is running to every account on the
// machine.
//
// The modes are asserted absolutely rather than against what the directory
// happened to be before the refresh. Both sides of a before-and-after
// comparison come through the same umask, so under `umask 077` a carry-over
// that recreated the store 0755 still lands at 0700 and the comparison holds on
// genuinely broken code. 0700 is what this code intends, so 0700 is what it is
// checked against.
func TestEnsureConfigSnapshot_PreservedSessionStoreKeepsItsMode(t *testing.T) {
	root, configDir := planSnapshotWorkspace(t)
	if err := WriteSessionMapping(root, SessionMapping{
		SessionID:    preservedSessionID,
		InstancePath: filepath.Join(root, "ws+task-0000cafe"),
		Agent:        "codex",
	}); err != nil {
		t.Fatal(err)
	}

	before, err := os.Stat(filepath.Join(configDir, sessionsDirName))
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != 0o700 {
		t.Fatalf("the session store was created %v, not 0700; the carry-over check below is written against what the store asks for", before.Mode().Perm())
	}
	if err := EnsureConfigSnapshot(context.Background(), configDir, driftedFetcher(t), nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	after, err := os.Stat(filepath.Join(configDir, sessionsDirName))
	if err != nil {
		t.Fatalf("session store missing after refresh: %v", err)
	}
	if after.Mode().Perm() != 0o700 {
		t.Errorf("session store is %v after the refresh, want 0700; a carried-over store must not be readable by anyone but its owner", after.Mode().Perm())
	}
	entry, err := os.Stat(filepath.Join(configDir, sessionsDirName, preservedSessionID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if entry.Mode().Perm() != 0o600 {
		t.Errorf("session mapping file mode changed across refresh: now %v, want 0600", entry.Mode().Perm())
	}
}

// TestEnsureConfigSnapshot_NoSessionStoreToPreserveIsBenign asserts the
// carry-over is a no-op for a workspace that has never dispatched: the refresh
// succeeds and does not invent a store.
func TestEnsureConfigSnapshot_NoSessionStoreToPreserveIsBenign(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)

	if err := EnsureConfigSnapshot(context.Background(), configDir, driftedFetcher(t), nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, sessionsDirName)); err == nil {
		t.Error("sessions/ appeared spuriously after a refresh in a workspace that never dispatched")
	}
}
