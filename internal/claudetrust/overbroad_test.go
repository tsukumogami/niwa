package claudetrust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// assertEmptyDir fails unless dir exists and holds nothing.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("%s holds %s, want nothing written", dir, e.Name())
	}
}

// assertRefused fails unless out is a refusal of key that wrote nothing:
// no trust, no change, prior returned as it was, and a warning naming key.
func assertRefused(t *testing.T, out Outcome, prior *Grant, key, reason string) {
	t.Helper()
	if out.Trusted || out.Changed || out.Created {
		t.Errorf("outcome = %+v, want nothing trusted or changed", out)
	}
	if out.Grant != prior {
		t.Errorf("grant = %+v, want prior %+v returned as it was", out.Grant, prior)
	}
	if !strings.Contains(out.Warning, key) || !strings.Contains(out.Warning, reason) {
		t.Errorf("warning = %q, want one naming %s and %q", out.Warning, key, reason)
	}
}

// TestEnsureRefusesOverBroadKey checks a key that would make Claude Code
// trust far more than one instance, through its parent walk, is refused with
// nothing written anywhere.
func TestEnsureRefusesOverBroadKey(t *testing.T) {
	cases := map[string]struct {
		root   func(f fixture) string
		reason string
	}{
		"home":             {func(f fixture) string { return f.env.Home }, "is the home directory"},
		"ancestor of home": {func(f fixture) string { return filepath.Dir(f.env.Home) }, "is an ancestor of the home directory"},
		"filesystem root":  {func(fixture) string { return string(filepath.Separator) }, "is the filesystem root"},
		"config home":      {func(f fixture) string { return filepath.Join(f.env.Home, ".claude") }, "is the Claude config home"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			configHome := filepath.Join(f.env.Home, ".claude")
			if err := os.MkdirAll(configHome, 0o700); err != nil {
				t.Fatal(err)
			}
			root := tc.root(f)
			key := mustKey(t, root)

			out := Ensure(f.env, root, nil, SourceWorkspace)
			assertRefused(t, out, nil, key, tc.reason)
			// The home holds only the empty config home made above: no
			// config file, no lock, no temp file.
			entries, err := os.ReadDir(f.env.Home)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != ".claude" {
				t.Errorf("home holds %v, want only the empty .claude", entries)
			}
			assertEmptyDir(t, configHome)
		})
	}
}

// TestEnsureRefusesAncestorOfConfigHome checks a key above CLAUDE_CONFIG_DIR,
// in a tree apart from the home directory, is refused too, as is the config
// home itself.
func TestEnsureRefusesAncestorOfConfigHome(t *testing.T) {
	f := newFixture(t)
	parent := filepath.Join(t.TempDir(), "cfg")
	dir := filepath.Join(parent, "claude")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	env := Env{Home: f.env.Home, ClaudeConfigDir: dir}
	for root, reason := range map[string]string{
		parent: "is an ancestor of the Claude config home",
		dir:    "is the Claude config home",
	} {
		out := Ensure(env, root, nil, SourceWorkspace)
		assertRefused(t, out, nil, mustKey(t, root), reason)
		assertEmptyDir(t, dir)
	}
}

// TestEnsureRefusesSymlinkedHome checks the home directory is compared with
// its symlinks resolved: an instance root at the real directory behind a
// symlinked Home is still the home directory.
func TestEnsureRefusesSymlinkedHome(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(t.TempDir(), "home-link")
	if err := os.Symlink(f.env.Home, link); err != nil {
		t.Fatal(err)
	}
	out := Ensure(Env{Home: link}, f.env.Home, nil, SourceWorkspace)
	assertRefused(t, out, nil, mustKey(t, f.env.Home), "is the home directory")
	assertEmptyDir(t, f.env.Home)
}

// TestEnsureRefusalKeepsPrior checks a refusal returns the prior record
// untouched, as every other skip does.
func TestEnsureRefusalKeepsPrior(t *testing.T) {
	f := newFixture(t)
	key := mustKey(t, f.env.Home)
	prior := &Grant{ConfigFile: f.file, Key: key, Mode: ModeCreated, Source: SourceWorkspace}
	out := Ensure(f.env, f.env.Home, prior, SourceWorkspace)
	assertRefused(t, out, prior, key, "is the home directory")
	if _, err := os.Lstat(f.file); !os.IsNotExist(err) {
		t.Errorf("%s written for an over-broad key (err=%v)", f.file, err)
	}
}

// TestEnsureAcceptsDirectoryUnderHome checks the guard stops at the home
// directory: an instance root beneath it is trusted as usual.
func TestEnsureAcceptsDirectoryUnderHome(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.env.Home, "work", "inst")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	key := mustKey(t, root)
	out := Ensure(f.env, root, nil, SourceWorkspace)
	if out.Warning != "" || !out.Trusted || !out.Changed || out.Grant == nil || out.Grant.Key != key {
		t.Fatalf("outcome = %+v, want a new grant for %s", out, key)
	}
	if e, ok := entryOf(t, f.file, key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("entry = %v, want the flag set", e)
	}
}
