package pluginrecord

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// mkdirT creates a directory (and parents) under the test tree.
func mkdirT(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	return path
}

// recordJSON renders a single install record object for seeding registries.
func recordJSON(scope, projectPath, installPath string) string {
	return fmt.Sprintf(
		`{"scope":%q,"projectPath":%q,"installPath":%q,"version":"1.0.0"}`,
		scope, projectPath, installPath,
	)
}

// registryJSON wraps a set of plugin keys (key -> JSON array of records) into a
// full registry document. Order is sorted for stable seeds.
func registryJSON(plugins map[string][]string) string {
	keys := make([]string, 0, len(plugins))
	for k := range plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	sb.WriteString(`{"version":1,"plugins":{`)
	for i, k := range keys {
		if i > 0 {
			sb.WriteByte(',')
		}
		fmt.Fprintf(&sb, "%q:[%s]", k, strings.Join(plugins[k], ","))
	}
	sb.WriteString("}}")
	return sb.String()
}

func TestDangling_MissingInstallPath(t *testing.T) {
	base := t.TempDir()
	live := mkdirT(t, filepath.Join(base, "project"))
	rec := Record{ProjectPath: live, InstallPath: filepath.Join(base, "gone-install")}
	if !Dangling(rec) {
		t.Fatalf("expected record with missing installPath to be dangling")
	}
}

func TestDangling_MissingProjectPath(t *testing.T) {
	base := t.TempDir()
	live := mkdirT(t, filepath.Join(base, "install"))
	rec := Record{ProjectPath: filepath.Join(base, "gone-project"), InstallPath: live}
	if !Dangling(rec) {
		t.Fatalf("expected record with missing projectPath to be dangling")
	}
}

func TestDangling_BothPresentKept(t *testing.T) {
	base := t.TempDir()
	rec := Record{
		ProjectPath: mkdirT(t, filepath.Join(base, "project")),
		InstallPath: mkdirT(t, filepath.Join(base, "install")),
	}
	if Dangling(rec) {
		t.Fatalf("expected record with both directories present to be kept")
	}
}

func TestDangling_EmptyPathsIgnored(t *testing.T) {
	if Dangling(Record{Scope: "user"}) {
		t.Fatalf("expected record with no non-empty paths to be kept")
	}
}

func TestDangling_LstatSymlinkSemantics(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "removed-target")
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	// The target never exists, but the symlink itself does. Lstat sees the
	// link, so the record is judged present (not dangling) on the link.
	rec := Record{ProjectPath: link, InstallPath: mkdirT(t, filepath.Join(base, "install"))}
	if Dangling(rec) {
		t.Fatalf("expected dangling to use Lstat: a symlink to a removed target must count as present")
	}
}

func TestInstanceOwned_MatchWithinRoot(t *testing.T) {
	root := "/base/inst1"
	sel := InstanceOwned(root)
	if !sel(Record{ProjectPath: "/base/inst1/repo-a"}) {
		t.Fatalf("expected project within root to be owned")
	}
}

func TestInstanceOwned_SiblingPrefixNotMatched(t *testing.T) {
	sel := InstanceOwned("/base/inst1")
	// Shares the textual prefix "/base/inst1" but is a sibling, not a child.
	if sel(Record{ProjectPath: "/base/inst1-sibling/repo"}) {
		t.Fatalf("expected sibling sharing a textual prefix NOT to be owned")
	}
}

func TestInstanceOwned_RootItself(t *testing.T) {
	sel := InstanceOwned("/base/inst1")
	if !sel(Record{ProjectPath: "/base/inst1"}) {
		t.Fatalf("expected the root itself to be owned")
	}
}

func TestInstanceOwned_EmptyProjectPathNotMatched(t *testing.T) {
	sel := InstanceOwned("/base/inst1")
	if sel(Record{ProjectPath: ""}) {
		t.Fatalf("expected empty projectPath NOT to be owned")
	}
}

func TestPrune_RemovesOnlyMatchesAndDropsEmptyKeys(t *testing.T) {
	base := t.TempDir()
	liveProject := mkdirT(t, filepath.Join(base, "live-project"))
	liveInstall := mkdirT(t, filepath.Join(base, "live-install"))
	goneA := filepath.Join(base, "gone-a")
	goneB := filepath.Join(base, "gone-b")

	content := registryJSON(map[string][]string{
		"a@market": {
			recordJSON("user", goneA, liveInstall),       // dangling (project gone)
			recordJSON("user", liveProject, liveInstall), // live
		},
		"b@market": {
			recordJSON("user", goneB, liveInstall), // dangling, sole record
		},
	})
	seedRegistry(t, base, content, 0o644)

	report, err := Prune(Dangling, WithPruneBaseDir(base))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Removed != 2 {
		t.Fatalf("Removed = %d, want 2", report.Removed)
	}
	if report.PerPlugin["a@market"] != 1 || report.PerPlugin["b@market"] != 1 {
		t.Fatalf("PerPlugin = %v, want a@market:1 b@market:1", report.PerPlugin)
	}
	if len(report.DroppedKeys) != 1 || report.DroppedKeys[0] != "b@market" {
		t.Fatalf("DroppedKeys = %v, want [b@market]", report.DroppedKeys)
	}
	if report.BackupPath == "" {
		t.Fatalf("expected a backup path for a mutating prune")
	}
	if _, err := os.Stat(report.BackupPath); err != nil {
		t.Fatalf("backup file not found at %q: %v", report.BackupPath, err)
	}

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, ok := reg.Plugins["b@market"]; ok {
		t.Fatalf("expected emptied key b@market to be dropped")
	}
	a := reg.Plugins["a@market"]
	if len(a) != 1 || a[0].ProjectPath != liveProject {
		t.Fatalf("a@market = %+v, want only the live record", a)
	}
}

func TestPrune_NonMatchingNeverRemoved(t *testing.T) {
	base := t.TempDir()
	liveProject := mkdirT(t, filepath.Join(base, "live-project"))
	liveInstall := mkdirT(t, filepath.Join(base, "live-install"))
	content := registryJSON(map[string][]string{
		"a@market": {recordJSON("user", liveProject, liveInstall)},
	})
	path := seedRegistry(t, base, content, 0o644)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	report, err := Prune(Dangling, WithPruneBaseDir(base))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Removed != 0 {
		t.Fatalf("Removed = %d, want 0", report.Removed)
	}
	if report.BackupPath != "" {
		t.Fatalf("expected no backup when nothing is removed, got %q", report.BackupPath)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("registry changed despite no matches:\nbefore=%s\nafter=%s", before, after)
	}
	// No backup sibling should have been created.
	assertNoBackup(t, base)
}

func TestPrune_DryRunNoWriteNoBackup(t *testing.T) {
	base := t.TempDir()
	liveInstall := mkdirT(t, filepath.Join(base, "live-install"))
	gone := filepath.Join(base, "gone")
	content := registryJSON(map[string][]string{
		"a@market": {recordJSON("user", gone, liveInstall)},
	})
	path := seedRegistry(t, base, content, 0o644)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read before: %v", err)
	}

	report, err := Prune(Dangling, WithPruneBaseDir(base), withDryRun())
	if err != nil {
		t.Fatalf("Prune dryRun: %v", err)
	}
	if report.Removed != 1 || report.PerPlugin["a@market"] != 1 {
		t.Fatalf("dryRun report = %+v, want Removed 1 for a@market", report)
	}
	if report.BackupPath != "" {
		t.Fatalf("dryRun must not take a backup, got %q", report.BackupPath)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("dryRun wrote to the registry; before != after")
	}
	assertNoBackup(t, base)
}

func TestPrune_MalformedFailSafe(t *testing.T) {
	base := t.TempDir()
	bad := `{"version":1,"plugins": [this is not json`
	path := seedRegistry(t, base, bad, 0o644)

	_, err := Prune(Dangling, WithPruneBaseDir(base))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Prune on malformed = %v, want wrapped ErrMalformed", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after: %v", err)
	}
	if string(after) != bad {
		t.Fatalf("malformed registry was modified; want unchanged")
	}
	assertNoBackup(t, base)
}

func TestPrune_AbsentRegistryNoOp(t *testing.T) {
	base := t.TempDir()
	report, err := Prune(Dangling, WithPruneBaseDir(base))
	if err != nil {
		t.Fatalf("Prune absent: %v", err)
	}
	if report.Removed != 0 || report.BackupPath != "" {
		t.Fatalf("absent prune report = %+v, want empty", report)
	}
	if _, err := os.Stat(filepath.Join(registryDir(base), "installed_plugins.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent prune created a registry file")
	}
}

func TestPrune_InstanceOwnedSelector(t *testing.T) {
	base := t.TempDir()
	inst1 := mkdirT(t, filepath.Join(base, "inst1"))
	inst2 := mkdirT(t, filepath.Join(base, "inst2"))
	repo1 := mkdirT(t, filepath.Join(inst1, "repo"))
	repo2 := mkdirT(t, filepath.Join(inst2, "repo"))
	install := mkdirT(t, filepath.Join(base, "install"))

	content := registryJSON(map[string][]string{
		"shared@market": {
			recordJSON("user", repo1, install),
			recordJSON("user", repo2, install),
		},
	})
	seedRegistry(t, base, content, 0o644)

	report, err := Prune(InstanceOwned(inst1), WithPruneBaseDir(base))
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if report.Removed != 1 || report.PerPlugin["shared@market"] != 1 {
		t.Fatalf("report = %+v, want exactly inst1's record removed", report)
	}

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	recs := reg.Plugins["shared@market"]
	if len(recs) != 1 || recs[0].ProjectPath != repo2 {
		t.Fatalf("remaining records = %+v, want only inst2's", recs)
	}
}

// assertNoBackup fails if any .niwa-bak. sibling exists in the registry dir.
func assertNoBackup(t *testing.T, base string) {
	t.Helper()
	entries, err := os.ReadDir(registryDir(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		t.Fatalf("read registry dir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), backupSuffix) {
			t.Fatalf("unexpected backup file %q", e.Name())
		}
	}
}
