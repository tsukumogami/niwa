package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// refreshWithManifest gives configDir a manifest-bearing snapshot whose source
// supplied files.
func refreshWithManifest(t *testing.T, configDir, oid string, files map[string]string) {
	t.Helper()
	if err := EnsureConfigSnapshot(context.Background(), configDir, upstreamFetcher(t, oid, files), nil); err != nil {
		t.Fatalf("refresh to %s: %v", oid, err)
	}
}

// TestEnsureConfigSnapshot_LegacyConversionDropsGitDir: converting a legacy
// working tree has no manifest to go by, so the carry keeps every path the new
// snapshot lacks. .git must not be one of them: dropping it is the point of the
// conversion, and a carried .git would never be removed afterwards, because no
// later manifest lists it. Untracked local files are kept.
func TestEnsureConfigSnapshot_LegacyConversionDropsGitDir(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), StateDir)
	initGitRepo(t, configDir)
	out, err := exec.Command("git", "-C", configDir, "remote", "add", "origin", "https://github.com/org/repo.git").CombinedOutput()
	if err != nil {
		t.Fatalf("git remote add: %v\n%s", err, out)
	}
	writeLocal(t, configDir, "workspace.toml", "name = old", 0o644)
	writeLocal(t, configDir, "notes/mine.md", "local", 0o644)

	fetcher := upstreamFetcher(t, "new-oid", map[string]string{"workspace.toml": "name = new"})
	converted, _, err := EnsureConfigSnapshotWithStatus(context.Background(), configDir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("conversion: %v", err)
	}
	if !converted {
		t.Fatal("expected the working tree to be converted, so the swap never ran")
	}
	assertAbsent(t, configDir, ".git")
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = new" {
		t.Errorf("source content not applied: %q", got)
	}
	readLocal(t, configDir, "notes/mine.md")
}

// TestEnsureConfigSnapshot_RecordsLineBreakNamesExactly: a source file named
// "x\nnotes.md" must not stand for a "notes.md" entry. Under a line-based
// manifest it did, so the next refresh would have deleted a local notes.md as
// if the source had supplied it. The NUL-separated manifest records such names
// exactly, at the top level and nested, with \n and \r alike, so the refresh
// accepts them, and the local files matching the part after the line break
// survive the next refresh while the source's odd names go away with it.
func TestEnsureConfigSnapshot_RecordsLineBreakNamesExactly(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	odd := []string{"x\nnotes.md", "y\rtools.sh", "dir/z\nmine.md"}
	refreshWithManifest(t, configDir, "oid-1", map[string]string{
		"workspace.toml": "name = one",
		odd[0]:           "s",
		odd[1]:           "s",
		"dir/":           "",
		odd[2]:           "s",
	})

	supplied, ok, err := readSnapshotManifest(configDir)
	if err != nil || !ok {
		t.Fatalf("read manifest: ok=%v err=%v", ok, err)
	}
	want := append([]string{"workspace.toml", "dir"}, odd...)
	for _, p := range want {
		if !supplied[p] {
			t.Errorf("manifest lacks %q", p)
		}
	}
	if len(supplied) != len(want) {
		t.Errorf("manifest has %d entries, want %d: %v", len(supplied), len(want), supplied)
	}

	for _, local := range []string{"notes.md", "tools.sh", "mine.md", "dir/mine.md"} {
		writeLocal(t, configDir, local, "local "+local, 0o644)
	}
	refreshWithManifest(t, configDir, "oid-2", map[string]string{"workspace.toml": "name = two"})
	for _, local := range []string{"notes.md", "tools.sh", "mine.md", "dir/mine.md"} {
		if got := readLocal(t, configDir, local); got != "local "+local {
			t.Errorf("%s = %q", local, got)
		}
	}
	for _, p := range odd {
		assertAbsent(t, configDir, p)
	}
}

// TestEnsureConfigSnapshot_ReadsLineBasedManifest: snapshots written before the
// switch to NUL separators carry a manifest with one path per line. It is
// still the record of what the source supplied: a path it lists goes when the
// source drops it, a local path it doesn't list is kept, and no upgrade
// warning fires, since there is a manifest to decide by.
func TestEnsureConfigSnapshot_ReadsLineBasedManifest(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{
		"workspace.toml": "name = one",
		"hooks/":         "",
		"hooks/old.sh":   "source hook",
	})
	writeLocal(t, configDir, SnapshotManifestFile, "hooks\nhooks/old.sh\nworkspace.toml\n", 0o644)
	writeLocal(t, configDir, "hooks/mine.sh", "local hook", 0o755)

	var buf bytes.Buffer
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-2", map[string]string{"workspace.toml": "name = two"}), NewReporter(&buf)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	assertAbsent(t, configDir, "hooks/old.sh")
	if got := readLocal(t, configDir, "hooks/mine.sh"); got != "local hook" {
		t.Errorf("local hook = %q", got)
	}
	if strings.Contains(buf.String(), "kept") {
		t.Errorf("a line-based manifest is still a manifest; got the upgrade warning %q", buf.String())
	}
	if got := readLocal(t, configDir, SnapshotManifestFile); got != "workspace.toml\x00" {
		t.Errorf("the refresh should rewrite the manifest NUL-separated, got %q", got)
	}
}

// TestEnsureConfigSnapshot_UnreadableLocalFileFailsTheRefresh: a local file
// niwa can't read can't be carried, and the swap would delete it, so the
// refresh fails naming it and leaves the config dir as it was. Skipping it
// with a warning, as a FIFO is, would lose what may be the only copy.
func TestEnsureConfigSnapshot_UnreadableLocalFileFailsTheRefresh(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{"workspace.toml": "name = one"})
	writeLocal(t, configDir, "notes/locked.md", "secret", 0o000)

	err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-2", map[string]string{"workspace.toml": "name = two"}), nil)
	if err == nil || !strings.Contains(err.Error(), filepath.Join("notes", "locked.md")) {
		t.Fatalf("expected a failure naming notes/locked.md, got %v", err)
	}
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = one" {
		t.Errorf("snapshot changed despite the failure: %q", got)
	}
	if info, err := os.Lstat(filepath.Join(configDir, "notes", "locked.md")); err != nil || info.Mode().Perm() != 0 {
		t.Errorf("the unreadable file should be left as it was: %v, %v", info, err)
	}
	for _, leftover := range []string{configDir + ".next", configDir + ".prev"} {
		if _, err := os.Lstat(leftover); !os.IsNotExist(err) {
			t.Errorf("%s left behind: %v", leftover, err)
		}
	}
}

// TestEnsureConfigSnapshot_UpgradeWarningListsKeptPaths: on the first refresh
// of a snapshot written before the manifest existed, the kept paths are
// reported, since some may be files the source deleted.
func TestEnsureConfigSnapshot_UpgradeWarningListsKeptPaths(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	writeLocal(t, configDir, "coordinator-tools/runbook.md", "x", 0o644)
	writeLocal(t, configDir, "notes.md", "x", 0o644)

	var buf bytes.Buffer
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "new-oid", map[string]string{"workspace.toml": "name = updated"}), NewReporter(&buf)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"warning: kept 2 path(s)", "coordinator-tools", "notes.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q should contain %q", got, want)
		}
	}

	// With a manifest in place, keeping local paths is the normal case and
	// says nothing.
	buf.Reset()
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "newer-oid", map[string]string{"workspace.toml": "name = newer"}), NewReporter(&buf)); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if strings.Contains(buf.String(), "kept") {
		t.Errorf("no warning expected once a manifest exists, got %q", buf.String())
	}
}

// TestEnsureConfigSnapshot_KeepsFlatLocalFileAndReadOnlyDirModeAndClearsPrev covers a
// top-level local file and a read-only local directory, whose mode must come
// through exactly.
func TestEnsureConfigSnapshot_KeepsFlatLocalFileAndReadOnlyDirModeAndClearsPrev(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{"workspace.toml": "name = one"})
	writeLocal(t, configDir, "notes.md", "flat", 0o644)
	writeLocal(t, configDir, "frozen/a.txt", "a", 0o444)
	frozen := filepath.Join(configDir, "frozen")
	if err := os.Chmod(frozen, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(filepath.Join(configDir, "frozen"), 0o755)
	})

	refreshWithManifest(t, configDir, "oid-2", map[string]string{"workspace.toml": "name = two"})
	if got := readLocal(t, configDir, "notes.md"); got != "flat" {
		t.Errorf("flat local file: %q", got)
	}
	readLocal(t, configDir, "frozen/a.txt")
	info, err := os.Stat(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o555 {
		t.Errorf("read-only dir mode = %v, want 0555", info.Mode().Perm())
	}
	// The swap must still be able to clear the previous snapshot, read-only
	// directory and all; a leftover .prev fails every later swap's cleanup.
	if _, err := os.Lstat(configDir + ".prev"); !os.IsNotExist(err) {
		t.Errorf("%s.prev left behind: %v", configDir, err)
	}
	refreshWithManifest(t, configDir, "oid-3", map[string]string{"workspace.toml": "name = three"})
}

// TestEnsureConfigSnapshot_RefusalNamesEveryConflict: a nested file, a
// directory where the source now supplies a file, and a local directory under a
// path the source supplies as a file (the walk meets ENOTDIR there) are all
// conflicts, and the refusal names every one of them.
func TestEnsureConfigSnapshot_RefusalNamesEveryConflict(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{
		"workspace.toml": "name = one",
		"hooks/":         "",
		"hooks/gate.sh":  "g",
		"foo":            "source file",
	})
	writeLocal(t, configDir, "hooks/mine.sh", "local hook", 0o755)
	writeLocal(t, configDir, "tools/run.sh", "local tool", 0o755)
	// Replace the source's file foo with a local directory foo/x.
	if err := os.Remove(filepath.Join(configDir, "foo")); err != nil {
		t.Fatal(err)
	}
	writeLocal(t, configDir, "foo/x", "local", 0o644)

	err := EnsureConfigSnapshot(context.Background(), configDir, upstreamFetcher(t, "oid-2", map[string]string{
		"workspace.toml": "name = two",
		"hooks/":         "",
		"hooks/gate.sh":  "g",
		"hooks/mine.sh":  "source hook",
		"tools":          "source file",
		"foo":            "source file",
	}), nil)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	for _, want := range []string{"hooks/mine.sh", "tools", "foo/x"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q should name %s", err, want)
		}
	}
	if got := readLocal(t, configDir, "hooks/mine.sh"); got != "local hook" {
		t.Errorf("local hook changed: %q", got)
	}
	readLocal(t, configDir, "tools/run.sh")
	readLocal(t, configDir, "foo/x")
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = one" {
		t.Errorf("snapshot changed despite the refusal: %q", got)
	}
}

// TestEnsureConfigSnapshot_SkipsFIFOWithWarning: a FIFO (or socket) under the
// config dir can't be copied. It is dropped with a warning instead of failing
// every refresh, and everything else is still kept.
func TestEnsureConfigSnapshot_SkipsFIFOWithWarning(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	writeLocal(t, configDir, "tools/notes.md", "x", 0o644)
	if err := syscall.Mkfifo(filepath.Join(configDir, "tools", "pipe"), 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}

	var buf bytes.Buffer
	if err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "new-oid", map[string]string{"workspace.toml": "name = updated"}), NewReporter(&buf)); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	readLocal(t, configDir, "tools/notes.md")
	assertAbsent(t, configDir, "tools/pipe")
	if !strings.Contains(buf.String(), "dropped 1 entr(ies)") || !strings.Contains(buf.String(), "tools/pipe") {
		t.Errorf("expected a warning naming tools/pipe, got %q", buf.String())
	}
}
