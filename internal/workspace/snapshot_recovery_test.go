package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// crashBetweenRenames leaves target the way a swap killed between its two
// renames does: target moved to target.prev, staging not moved in.
func crashBetweenRenames(t *testing.T, target, staging string) {
	t.Helper()
	t.Setenv("NIWA_TEST_FAULT", "error:killed@snapshot-swap-mid")
	if err := SwapSnapshotAtomic(target, staging); err == nil || !strings.Contains(err.Error(), "killed") {
		t.Fatalf("expected the mid-swap fault, got %v", err)
	}
	t.Setenv("NIWA_TEST_FAULT", "")
	snapshotMustNotExist(t, target)
	snapshotReadFile(t, filepath.Join(target+".prev", "marker"), "old")
}

// TestSwapSnapshotAtomic_PreflightRestoresPrevAfterCrash: after a swap dies
// between its renames, .prev is the only copy of the snapshot. The next swap's
// preflight must put it back, not delete it as a leftover. The second swap is
// stopped by the pre-rename fault so the restored copy can be inspected.
func TestSwapSnapshotAtomic_PreflightRestoresPrevAfterCrash(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".niwa")
	staging := filepath.Join(dir, ".niwa.next")
	for _, d := range []string{target, staging} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	snapshotWriteFile(t, filepath.Join(target, "marker"), "old")
	snapshotWriteFile(t, filepath.Join(staging, "marker"), "new")
	crashBetweenRenames(t, target, staging)

	t.Setenv("NIWA_TEST_FAULT", "error:stop@snapshot-swap")
	if err := SwapSnapshotAtomic(target, staging); err == nil {
		t.Fatal("expected the pre-rename fault")
	}
	snapshotReadFile(t, filepath.Join(target, "marker"), "old")
	snapshotMustNotExist(t, target+".prev")

	// With the fault gone the swap completes as usual.
	t.Setenv("NIWA_TEST_FAULT", "")
	if err := SwapSnapshotAtomic(target, staging); err != nil {
		t.Fatalf("swap: %v", err)
	}
	snapshotReadFile(t, filepath.Join(target, "marker"), "new")
	snapshotMustNotExist(t, target+".prev")
}

// TestEnsureConfigSnapshot_RestoresPrevBeforeCarryingLocalState is the end to
// end case from the issue: a refresh killed mid-swap leaves .niwa.prev holding
// a local file nobody else has a copy of. The next refresh must restore it
// before building the new snapshot, or the carry-over finds no config dir,
// carries nothing, and the file is gone once the swap completes.
func TestEnsureConfigSnapshot_RestoresPrevBeforeCarryingLocalState(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{"workspace.toml": "name = one"})
	writeLocal(t, configDir, "notes/mine.md", "only copy", 0o644)

	t.Setenv("NIWA_TEST_FAULT", "error:killed@snapshot-swap-mid")
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-2", map[string]string{"workspace.toml": "name = two"}), nil); err == nil {
		t.Fatal("expected the mid-swap fault")
	}
	t.Setenv("NIWA_TEST_FAULT", "")
	if _, err := os.Lstat(configDir); !os.IsNotExist(err) {
		t.Fatalf("the fault should leave no config dir, got %v", err)
	}

	var buf bytes.Buffer
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-3", map[string]string{"workspace.toml": "name = three"}), NewReporter(&buf)); err != nil {
		t.Fatalf("refresh after the crash: %v", err)
	}
	if got := readLocal(t, configDir, "notes/mine.md"); got != "only copy" {
		t.Errorf("local file = %q", got)
	}
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = three" {
		t.Errorf("source content not applied: %q", got)
	}
	if !strings.Contains(buf.String(), "restored "+configDir) {
		t.Errorf("expected the restore to be reported, got %q", buf.String())
	}
	if _, err := os.Lstat(configDir + ".prev"); !os.IsNotExist(err) {
		t.Errorf("%s.prev left behind: %v", configDir, err)
	}
}

// TestEnsureConfigSnapshot_RestoresHandMadeCrashState covers the same state
// without the fault seam, made by renaming the config dir by hand, so the test
// does not depend on where the seam sits.
func TestEnsureConfigSnapshot_RestoresHandMadeCrashState(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{"workspace.toml": "name = one"})
	writeLocal(t, configDir, "notes.md", "only copy", 0o644)
	if err := os.Rename(configDir, configDir+".prev"); err != nil {
		t.Fatal(err)
	}

	refreshWithManifest(t, configDir, "oid-2", map[string]string{"workspace.toml": "name = two"})
	if got := readLocal(t, configDir, "notes.md"); got != "only copy" {
		t.Errorf("local file = %q", got)
	}
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = two" {
		t.Errorf("source content not applied: %q", got)
	}
}
