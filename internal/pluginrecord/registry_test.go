package pluginrecord

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// registryDir is the directory the registry lives in under a test base.
func registryDir(base string) string {
	return filepath.Join(base, ".claude", "plugins")
}

// seedRegistry writes content to the registry path under base, creating
// parent directories, and returns the registry path.
func seedRegistry(t *testing.T, base, content string, mode os.FileMode) string {
	t.Helper()
	dir := registryDir(base)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "installed_plugins.json")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("seed registry: %v", err)
	}
	return path
}

func TestLocate_InjectedBaseDir(t *testing.T) {
	base := t.TempDir()
	got, err := Locate(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	want := filepath.Join(base, ".claude", "plugins", "installed_plugins.json")
	if got != want {
		t.Fatalf("Locate = %q, want %q", got, want)
	}
}

func TestLoad_AbsentRegistryIsEmptyNoOp(t *testing.T) {
	base := t.TempDir()

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load absent: %v", err)
	}
	if reg.present {
		t.Fatalf("absent registry reported present")
	}
	if len(reg.Plugins) != 0 {
		t.Fatalf("absent registry has %d plugins, want 0", len(reg.Plugins))
	}

	// Loading an absent registry must not create the file.
	path := filepath.Join(base, ".claude", "plugins", "installed_plugins.json")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("absent registry was created by Load: stat err = %v", err)
	}
}

func TestLoad_MalformedReturnsTypedErrorAndLeavesFileUnchanged(t *testing.T) {
	base := t.TempDir()
	const bad = `{ this is not json `
	path := seedRegistry(t, base, bad, 0o644)

	_, err := Load(WithBaseDir(base))
	if err == nil {
		t.Fatalf("Load malformed: expected error, got nil")
	}
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("Load malformed: error %v does not wrap ErrMalformed", err)
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read after malformed load: %v", readErr)
	}
	if string(after) != bad {
		t.Fatalf("malformed file changed by Load: got %q", string(after))
	}
}

func TestRoundTrip_PreservesUnknownTopLevelKeysAndRecordFields(t *testing.T) {
	base := t.TempDir()
	// version is an unmodelled top-level key; "future" is an unknown
	// top-level key; each record carries an unmodelled "version" field.
	const content = `{"version":3,"future":{"nested":["a",1,true]},"plugins":{"skill@market":[{"scope":"project","projectPath":"/p/a","installPath":"/i/a","version":"1.2.3","extra":{"k":"v"}}]}}`
	seedRegistry(t, base, content, 0o644)

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Modeled fields are parsed.
	recs := reg.Plugins["skill@market"]
	if len(recs) != 1 {
		t.Fatalf("got %d records, want 1", len(recs))
	}
	if recs[0].Scope != "project" || recs[0].ProjectPath != "/p/a" || recs[0].InstallPath != "/i/a" {
		t.Fatalf("modeled fields wrong: %+v", recs[0])
	}

	out, err := reg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Byte-stable round trip: the input already has plugin keys and record
	// fields in a stable order, so Marshal should reproduce it exactly.
	if string(out) != content {
		t.Fatalf("round trip not byte-stable:\n got:  %s\n want: %s", out, content)
	}
}

func TestRoundTrip_PreservesPluginKeyOrder(t *testing.T) {
	base := t.TempDir()
	// Plugin keys deliberately out of sorted order: a byte-stable round
	// trip must re-emit them in source order, not alphabetized.
	const content = `{"version":1,"plugins":{"z@m":[{"scope":"user","projectPath":"/p","installPath":"/i"}],"a@m":[{"scope":"user","projectPath":"/q","installPath":"/j"}]}}`
	seedRegistry(t, base, content, 0o644)

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	out, err := reg.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(out) != content {
		t.Fatalf("plugin key order not preserved:\n got:  %s\n want: %s", out, content)
	}
}

func TestSave_AtomicWriteNoLeftoverTemp(t *testing.T) {
	base := t.TempDir()
	const content = `{"version":1,"plugins":{"a@m":[{"scope":"user","projectPath":"/p","installPath":"/i"}]}}`
	path := seedRegistry(t, base, content, 0o644)

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read after save: %v", err)
	}
	if string(got) != content {
		t.Fatalf("save changed content:\n got:  %s\n want: %s", got, content)
	}

	// No temp files should remain in the registry directory.
	entries, err := os.ReadDir(registryDir(base))
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), "niwa-tmp") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

func TestBackup_CreatesTimestampedSiblingWithSourceMode(t *testing.T) {
	base := t.TempDir()
	const content = `{"version":1,"plugins":{}}`
	const srcMode = os.FileMode(0o600)
	seedRegistry(t, base, content, srcMode)

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	backupPath, err := reg.Backup(defaultBackupRetention)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if backupPath == "" {
		t.Fatalf("Backup returned empty path for present registry")
	}

	if !strings.Contains(filepath.Base(backupPath), ".niwa-bak.") {
		t.Fatalf("backup name lacks .niwa-bak. marker: %s", backupPath)
	}

	bdata, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(bdata) != content {
		t.Fatalf("backup content mismatch: got %q", string(bdata))
	}

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Mode().Perm() != srcMode {
		t.Fatalf("backup mode = %v, want %v", info.Mode().Perm(), srcMode)
	}
}

func TestBackup_AbsentRegistryIsNoOp(t *testing.T) {
	base := t.TempDir()
	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	backupPath, err := reg.Backup(defaultBackupRetention)
	if err != nil {
		t.Fatalf("Backup absent: %v", err)
	}
	if backupPath != "" {
		t.Fatalf("Backup of absent registry returned %q, want empty", backupPath)
	}
}

func TestBackup_RotationRetainsLastN(t *testing.T) {
	base := t.TempDir()
	const content = `{"version":1,"plugins":{}}`
	seedRegistry(t, base, content, 0o644)
	dir := registryDir(base)
	srcPath := filepath.Join(dir, "installed_plugins.json")

	// Plant 7 pre-existing backups with sortable RFC3339-style timestamps.
	const prefix = "installed_plugins.json.niwa-bak."
	stamps := []string{
		"2020-01-01T00:00:00Z",
		"2020-01-02T00:00:00Z",
		"2020-01-03T00:00:00Z",
		"2020-01-04T00:00:00Z",
		"2020-01-05T00:00:00Z",
		"2020-01-06T00:00:00Z",
		"2020-01-07T00:00:00Z",
	}
	for _, s := range stamps {
		if err := os.WriteFile(filepath.Join(dir, prefix+s), []byte("old"), 0o644); err != nil {
			t.Fatalf("plant backup: %v", err)
		}
	}

	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	const retain = 5
	if err := rotateBackups(srcPath, retain); err != nil {
		t.Fatalf("rotateBackups: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var remaining []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			remaining = append(remaining, e.Name())
		}
	}
	if len(remaining) != retain {
		t.Fatalf("after rotation %d backups remain, want %d: %v", len(remaining), retain, remaining)
	}
	// The oldest two (Jan 1 and Jan 2) must be gone.
	for _, name := range remaining {
		if strings.Contains(name, "2020-01-01") || strings.Contains(name, "2020-01-02") {
			t.Fatalf("rotation kept an old backup: %s", name)
		}
	}
	_ = reg
}

func TestSave_OnRegistryBuiltInMemoryEmitsPluginsKey(t *testing.T) {
	// A registry loaded from an absent file has no top-level fields; Save
	// must still emit a valid document with a "plugins" key so a later
	// load round-trips.
	base := t.TempDir()
	reg, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	reg.Plugins["x@m"] = []Record{{
		Scope:       "user",
		ProjectPath: "/p",
		InstallPath: "/i",
		raw:         []byte(`{"scope":"user","projectPath":"/p","installPath":"/i"}`),
	}}

	if err := reg.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(WithBaseDir(base))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(reloaded.Plugins["x@m"]) != 1 {
		t.Fatalf("reload lost record: %+v", reloaded.Plugins)
	}
}
