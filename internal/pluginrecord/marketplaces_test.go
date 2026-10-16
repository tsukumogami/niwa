package pluginrecord

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKnownMarketplaces(t *testing.T, base, body string) string {
	t.Helper()
	dir := filepath.Join(base, ".claude", "plugins")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "known_marketplaces.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readEntry(t *testing.T, path, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("result not valid JSON: %v", err)
	}
	return top[name]
}

func backupCount(t *testing.T, path string) int {
	t.Helper()
	matches, _ := filepath.Glob(path + ".niwa-bak.*")
	return len(matches)
}

func TestReconcileAutoUpdate_FlipsExistingAndPreservesFields(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{
  "shirabe": {
    "source": { "source": "github", "repo": "tsukumogami/shirabe" },
    "installLocation": "/home/u/.claude/plugins/marketplaces/shirabe",
    "lastUpdated": "2026-06-19T20:58:03.976Z",
    "autoUpdate": true
  },
  "other": { "source": { "source": "github", "repo": "x/other" }, "autoUpdate": true }
}`)

	rep, err := ReconcileAutoUpdate(map[string]bool{"shirabe": false}, WithBaseDir(base))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Updated) != 1 || rep.Updated[0] != "shirabe" {
		t.Fatalf("Updated = %v, want [shirabe]", rep.Updated)
	}

	shirabe := readEntry(t, path, "shirabe")
	if shirabe["autoUpdate"] != false {
		t.Errorf("shirabe.autoUpdate = %v, want false", shirabe["autoUpdate"])
	}
	if shirabe["installLocation"] != "/home/u/.claude/plugins/marketplaces/shirabe" {
		t.Errorf("installLocation not preserved: %v", shirabe["installLocation"])
	}
	if shirabe["lastUpdated"] != "2026-06-19T20:58:03.976Z" {
		t.Errorf("lastUpdated not preserved: %v", shirabe["lastUpdated"])
	}
	if src, ok := shirabe["source"].(map[string]any); !ok || src["repo"] != "tsukumogami/shirabe" {
		t.Errorf("source not preserved: %v", shirabe["source"])
	}

	// Untargeted entry left as-is.
	if other := readEntry(t, path, "other"); other["autoUpdate"] != true {
		t.Errorf("untargeted 'other' was modified: %v", other["autoUpdate"])
	}
	if backupCount(t, path) != 1 {
		t.Errorf("expected exactly one backup, got %d", backupCount(t, path))
	}
}

func TestReconcileAutoUpdate_AddsFieldWhenMissing(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{"m": {"source": {"source": "github", "repo": "x/m"}}}`)
	rep, err := ReconcileAutoUpdate(map[string]bool{"m": false}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Updated) != 1 {
		t.Fatalf("Updated = %v, want one entry", rep.Updated)
	}
	if v := readEntry(t, path, "m")["autoUpdate"]; v != false {
		t.Errorf("autoUpdate = %v, want false", v)
	}
}

func TestReconcileAutoUpdate_NoChangeWhenAlreadyDesired(t *testing.T) {
	base := t.TempDir()
	body := `{"m": {"autoUpdate": false}}`
	path := writeKnownMarketplaces(t, base, body)
	before, _ := os.ReadFile(path)

	rep, err := ReconcileAutoUpdate(map[string]bool{"m": false}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Updated) != 0 {
		t.Errorf("Updated = %v, want empty", rep.Updated)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("file changed on a no-op reconcile")
	}
	if backupCount(t, path) != 0 {
		t.Errorf("backup written on a no-op reconcile")
	}
}

func TestReconcileAutoUpdate_DoesNotAddAbsentMarketplace(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{"present": {"autoUpdate": true}}`)
	rep, err := ReconcileAutoUpdate(map[string]bool{"ghost": false}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Updated) != 0 {
		t.Errorf("Updated = %v, want empty", rep.Updated)
	}
	data, _ := os.ReadFile(path)
	var top map[string]any
	_ = json.Unmarshal(data, &top)
	if _, exists := top["ghost"]; exists {
		t.Errorf("absent marketplace 'ghost' was added")
	}
}

// claudeFormattedRegistry is a registry in the layout Claude Code writes: two
// spaces of indent, a trailing newline.
const claudeFormattedRegistry = `{
  "shirabe": {
    "source": {
      "source": "github",
      "repo": "tsukumogami/shirabe"
    },
    "installLocation": "/home/u/.claude/plugins/marketplaces/shirabe",
    "lastUpdated": "2026-06-19T20:58:03.976Z",
    "autoUpdate": true
  },
  "tsukumogami": {
    "source": {
      "source": "directory",
      "path": "/ws/instance-3/private/tools"
    },
    "installLocation": "/ws/instance-3/private/tools",
    "lastUpdated": "2026-06-19T20:58:03.976Z",
    "autoUpdate": false
  }
}
`

func TestReconcileMarketplaces_RepointsDirectoryAndKeepsOtherEntries(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, claudeFormattedRegistry)

	managed := "/ws/.niwa/marketplaces/tools/current"
	rep, err := ReconcileMarketplaces(map[string]MarketplaceWant{
		"tsukumogami": {Path: managed},
	}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Repointed) != 1 || rep.Repointed[0] != "tsukumogami" || len(rep.Updated) != 0 {
		t.Fatalf("report = %+v, want only tsukumogami repointed", rep)
	}
	if rep.BackupPath == "" || backupCount(t, path) != 1 {
		t.Errorf("want one backup, got %q (%d)", rep.BackupPath, backupCount(t, path))
	}

	want := strings.ReplaceAll(claudeFormattedRegistry, "/ws/instance-3/private/tools", managed)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("registry after repoint:\n%s\nwant:\n%s", got, want)
	}

	// Already pointing there: nothing to write, no new backup.
	rep, err = ReconcileMarketplaces(map[string]MarketplaceWant{"tsukumogami": {Path: managed}}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Repointed) != 0 || backupCount(t, path) != 1 {
		t.Errorf("second reconcile changed something: %+v, %d backups", rep, backupCount(t, path))
	}
}

func TestReconcileMarketplaces_ReplacesOtherSourceKinds(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{
  "tsukumogami": {
    "source": {"source": "github", "repo": "org/tools", "ref": "v1"},
    "installLocation": "/home/u/.claude/plugins/marketplaces/tsukumogami",
    "autoUpdate": true
  }
}`)
	if _, err := ReconcileMarketplaces(map[string]MarketplaceWant{
		"tsukumogami": {Path: "/ws/.niwa/marketplaces/tools/current", AutoUpdate: false},
	}, WithBaseDir(base)); err != nil {
		t.Fatal(err)
	}
	e := readEntry(t, path, "tsukumogami")
	src, _ := e["source"].(map[string]any)
	if len(src) != 2 || src["source"] != "directory" || src["path"] != "/ws/.niwa/marketplaces/tools/current" {
		t.Errorf("source = %v, want only a directory source at the managed path", e["source"])
	}
	if e["installLocation"] != "/ws/.niwa/marketplaces/tools/current" || e["autoUpdate"] != false {
		t.Errorf("entry = %v", e)
	}
}

func TestReconcileMarketplaces_DoesNotAddAbsentMarketplace(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{"present": {"autoUpdate": true}}`)
	rep, err := ReconcileMarketplaces(map[string]MarketplaceWant{"ghost": {Path: "/x"}}, WithBaseDir(base))
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Repointed) != 0 || readEntry(t, path, "ghost") != nil {
		t.Errorf("absent marketplace was added: %+v", rep)
	}
}

func TestReconcileAutoUpdate_AbsentFileIsNoOp(t *testing.T) {
	base := t.TempDir()
	rep, err := ReconcileAutoUpdate(map[string]bool{"m": false}, WithBaseDir(base))
	if err != nil {
		t.Fatalf("absent file should be a no-op, got %v", err)
	}
	if len(rep.Updated) != 0 {
		t.Errorf("Updated = %v, want empty", rep.Updated)
	}
}

func TestReconcileAutoUpdate_MalformedIsFailSafe(t *testing.T) {
	base := t.TempDir()
	path := writeKnownMarketplaces(t, base, `{ this is not json`)
	before, _ := os.ReadFile(path)

	_, err := ReconcileAutoUpdate(map[string]bool{"m": false}, WithBaseDir(base))
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("error = %v, want ErrMalformed", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("malformed file was modified")
	}
}
