package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// upstreamFetcher serves a config repo at commit oid with the given files
// (paths relative to the repo root, dir entries ending in "/").
func upstreamFetcher(t *testing.T, oid string, files map[string]string) *fakeFetcher {
	t.Helper()
	entries := map[string]string{"wrap/": ""}
	for p, c := range files {
		entries["wrap/"+p] = c
	}
	return &fakeFetcher{tarball: makeFakeTarball(t, entries), commitOID: oid}
}

func writeLocal(t *testing.T, configDir, rel, content string, mode os.FileMode) {
	t.Helper()
	p := filepath.Join(configDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func readLocal(t *testing.T, configDir, rel string) string {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(configDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s is gone after the refresh: %v", rel, err)
	}
	return string(got)
}

func assertAbsent(t *testing.T, configDir, rel string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(configDir, filepath.FromSlash(rel))); !os.IsNotExist(err) {
		t.Errorf("%s should be gone after the refresh, got err=%v", rel, err)
	}
}

// TestEnsureConfigSnapshot_KeepsUnmanagedNestedDirectory is the reported
// defect: a directory nobody's config declares, kept under the workspace-root
// .niwa/ by a session at the root, was deleted by the first apply after the
// config source moved. Only the three names niwa writes itself were carried
// across the swap, so a nested tree of notes and scripts disappeared while
// dispatch-briefs/ beside it survived.
//
// It also covers the upgrade case: the snapshot here predates the manifest,
// which is the state every existing workspace is in on the first refresh after
// this fix ships.
func TestEnsureConfigSnapshot_KeepsUnmanagedNestedDirectory(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	writeLocal(t, configDir, "coordinator-tools/runbook.md", "# runbook\n", 0o644)
	writeLocal(t, configDir, "coordinator-tools/sweep.sh", "#!/bin/sh\n", 0o755)
	writeLocal(t, configDir, "coordinator-tools/friction/log.md", "entry\n", 0o600)
	if err := os.Symlink("runbook.md", filepath.Join(configDir, "coordinator-tools", "current")); err != nil {
		t.Fatal(err)
	}

	fetcher := upstreamFetcher(t, "new-oid", map[string]string{"workspace.toml": "name = updated"})
	if err := EnsureConfigSnapshot(context.Background(), configDir, fetcher, nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	if got := readLocal(t, configDir, "workspace.toml"); got != "name = updated" {
		t.Fatalf("upstream content not refreshed, so the swap never ran: %q", got)
	}
	if got := readLocal(t, configDir, "coordinator-tools/friction/log.md"); got != "entry\n" {
		t.Errorf("nested file content changed: %q", got)
	}
	readLocal(t, configDir, "coordinator-tools/runbook.md")
	info, err := os.Stat(filepath.Join(configDir, "coordinator-tools", "sweep.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("script mode = %v, want 0755", info.Mode().Perm())
	}
	info, err = os.Stat(filepath.Join(configDir, "coordinator-tools", "friction", "log.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("private file mode = %v, want 0600", info.Mode().Perm())
	}
	if link, err := os.Readlink(filepath.Join(configDir, "coordinator-tools", "current")); err != nil || link != "runbook.md" {
		t.Errorf("symlink not kept as a symlink: %q, %v", link, err)
	}

	// The manifest records upstream's paths only.
	manifest := readLocal(t, configDir, SnapshotManifestFile)
	if manifest != "workspace.toml\x00" {
		t.Errorf("manifest = %q, want only the upstream path", manifest)
	}
}

// TestEnsureConfigSnapshot_RemovesOnlyWhatUpstreamStoppedSupplying pins the
// other half: with a manifest in place, a path upstream supplied and then
// deleted does go away, while a local file -- including one a user dropped
// inside an upstream directory -- stays.
func TestEnsureConfigSnapshot_RemovesOnlyWhatUpstreamStoppedSupplying(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)

	first := upstreamFetcher(t, "oid-1", map[string]string{
		"workspace.toml":  "name = one",
		"hooks/":          "",
		"hooks/gate.sh":   "upstream gate",
		"hooks/old.sh":    "retired soon",
		"extensions/":     "",
		"extensions/a.md": "a",
	})
	if err := EnsureConfigSnapshot(context.Background(), configDir, first, nil); err != nil {
		t.Fatalf("first refresh: %v", err)
	}

	writeLocal(t, configDir, "notes/today.md", "local", 0o644)
	writeLocal(t, configDir, "hooks/mine.sh", "local hook", 0o755)

	second := upstreamFetcher(t, "oid-2", map[string]string{
		"workspace.toml": "name = two",
		"hooks/":         "",
		"hooks/gate.sh":  "upstream gate v2",
	})
	if err := EnsureConfigSnapshot(context.Background(), configDir, second, nil); err != nil {
		t.Fatalf("second refresh: %v", err)
	}

	if got := readLocal(t, configDir, "workspace.toml"); got != "name = two" {
		t.Fatalf("second refresh did not land: %q", got)
	}
	if got := readLocal(t, configDir, "hooks/gate.sh"); got != "upstream gate v2" {
		t.Errorf("upstream file not updated: %q", got)
	}
	assertAbsent(t, configDir, "hooks/old.sh")
	assertAbsent(t, configDir, "extensions")
	if got := readLocal(t, configDir, "notes/today.md"); got != "local" {
		t.Errorf("local file changed: %q", got)
	}
	if got := readLocal(t, configDir, "hooks/mine.sh"); got != "local hook" {
		t.Errorf("local file inside an upstream dir changed: %q", got)
	}

	// A third refresh keeps the local files again: they stay out of the
	// manifest, so they are never mistaken for upstream's.
	third := upstreamFetcher(t, "oid-3", map[string]string{"workspace.toml": "name = three"})
	if err := EnsureConfigSnapshot(context.Background(), configDir, third, nil); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
	readLocal(t, configDir, "notes/today.md")
	readLocal(t, configDir, "hooks/mine.sh")
	assertAbsent(t, configDir, "hooks/gate.sh")
}

// TestEnsureConfigSnapshot_RefusesWhenUpstreamClaimsALocalPath: a local file
// that upstream starts supplying under the same name has two owners. The
// refresh refuses and leaves the existing snapshot, local file included,
// exactly as it was, rather than overwrite either copy.
func TestEnsureConfigSnapshot_RefusesWhenUpstreamClaimsALocalPath(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-1", map[string]string{"workspace.toml": "name = one"}), nil); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	writeLocal(t, configDir, "notes.md", "mine", 0o644)

	err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-2", map[string]string{"workspace.toml": "name = two", "notes.md": "theirs"}), nil)
	if err == nil || !strings.Contains(err.Error(), "notes.md") {
		t.Fatalf("expected a refusal naming notes.md, got %v", err)
	}
	if got := readLocal(t, configDir, "notes.md"); got != "mine" {
		t.Errorf("local file overwritten: %q", got)
	}
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = one" {
		t.Errorf("snapshot changed despite the refusal: %q", got)
	}
}

// TestEnsureConfigSnapshot_ReservedNamesAreTopLevelOnly: the names niwa writes
// at the top of the config dir are handled by their own steps, but the same
// names deeper in the tree are ordinary paths and are carried like any other.
func TestEnsureConfigSnapshot_ReservedNamesAreTopLevelOnly(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	writeLocal(t, configDir, "tools/instance.json", "{}", 0o644)
	writeLocal(t, configDir, "tools/sessions/x", "x", 0o644)

	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "new-oid", map[string]string{"workspace.toml": "name = updated"}), nil); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	readLocal(t, configDir, "tools/instance.json")
	readLocal(t, configDir, "tools/sessions/x")
}
