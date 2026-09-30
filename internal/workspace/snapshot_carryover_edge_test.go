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

// TestEnsureConfigSnapshot_RefusesLineBreakInSuppliedPath: the manifest is one
// path per line, so a source file named "x\nnotes.md" would record a
// "notes.md" line, and the next refresh would delete a local notes.md as if the
// source had supplied it. The refresh refuses such a name instead, and the
// local file and the old snapshot survive.
func TestEnsureConfigSnapshot_RefusesLineBreakInSuppliedPath(t *testing.T) {
	_, configDir := planSnapshotWorkspace(t)
	refreshWithManifest(t, configDir, "oid-1", map[string]string{"workspace.toml": "name = one"})
	writeLocal(t, configDir, "notes.md", "mine", 0o644)

	err := EnsureConfigSnapshot(context.Background(), configDir,
		upstreamFetcher(t, "oid-2", map[string]string{"workspace.toml": "name = two", "x\nnotes.md": "forged"}), nil)
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("expected a refusal naming the line break, got %v", err)
	}
	if got := readLocal(t, configDir, "notes.md"); got != "mine" {
		t.Errorf("local file changed: %q", got)
	}
	if got := readLocal(t, configDir, "workspace.toml"); got != "name = one" {
		t.Errorf("snapshot changed despite the refusal: %q", got)
	}
	manifest := readLocal(t, configDir, SnapshotManifestFile)
	if strings.Contains(manifest, "notes.md") {
		t.Errorf("manifest names notes.md: %q", manifest)
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
	if !strings.Contains(buf.String(), "dropped 1 entr(ies)") || !strings.Contains(buf.String(), "tools/pipe") {
		t.Errorf("expected a warning naming tools/pipe, got %q", buf.String())
	}
}
