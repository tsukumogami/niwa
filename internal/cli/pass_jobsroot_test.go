package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// configDirHome sets HOME to one scratch directory and CLAUDE_CONFIG_DIR to
// another, and returns both, so a test can tell which jobs root a pass read.
func configDirHome(t *testing.T) (home, cfg string) {
	t.Helper()
	home, cfg = canonicalTempDir(t), canonicalTempDir(t)
	t.Setenv("HOME", home)
	t.Setenv(removal.ConfigDirVar, cfg)
	return home, cfg
}

func TestPassJobsRoot_ReadsLivenessUnderTheConfigDir(t *testing.T) {
	f := newPassFixture(t)
	_, cfg := configDirHome(t)
	writeJobEntry(t, filepath.Join(cfg, "jobs"), passSessionID)

	text, _, err := runReapCommand(t, f.root, "--verdict-only")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "test-ws-pass: keep\n") || !strings.Contains(text, "its session is still there") {
		t.Errorf("verdict-only output:\n%s\nwant the unit kept as live, read under %s", text, cfg)
	}
	n, err := reapWorkspaceResolved(f.root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(*f.destroyed) != 0 {
		t.Fatalf("reaped %d, destroyed %v; the session's entry under the config directory makes it live", n, *f.destroyed)
	}
}

func TestPassJobsRoot_IgnoresAnEntryUnderTheDefaultRootWhenTheConfigDirIsSet(t *testing.T) {
	f := newPassFixture(t)
	home, _ := configDirHome(t)
	writeJobEntry(t, filepath.Join(home, ".claude", "jobs"), passSessionID)

	text, _, err := runReapCommand(t, f.root, "--verdict-only")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(text, "test-ws-pass: reap\n") {
		t.Errorf("verdict-only output:\n%s\nwant the unit reaped: its entry is not under the config directory", text)
	}
	n, err := reapWorkspaceResolved(f.root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(*f.destroyed) != 1 {
		t.Fatalf("reaped %d, destroyed %v; want the unit removed\n%s", n, *f.destroyed, f.stderr.String())
	}
}

func TestPassJobsRoot_JudgesAGoneSessionsJobDirectoryUnderTheConfigDir(t *testing.T) {
	f := newPassFixture(t)
	f.appendConfig(t, "\n[session.removal]\njob_scratch = \"unique\"\n")
	home, cfg := configDirHome(t)
	writeTestFile(t, filepath.Join(cfg, "jobs", passSessionID, "notes.txt"), "n\n")
	// The same session's directory under the default root is never read.
	writeTestFile(t, filepath.Join(home, ".claude", "jobs", passSessionID, "decoy.txt"), "d\n")

	text, _, err := runReapCommand(t, f.root, "--verdict-only")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "test-ws-pass: keep\n") || !strings.Contains(text, "job:notes.txt") {
		t.Errorf("verdict-only output:\n%s\nwant the unit kept on job:notes.txt", text)
	}
	if strings.Contains(text, "decoy") {
		t.Errorf("verdict-only output:\n%s\nreads the default root's job directory", text)
	}
	n, err := reapWorkspaceResolved(f.root, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(*f.destroyed) != 0 {
		t.Fatalf("reaped %d, destroyed %v; want the unit kept on its job directory", n, *f.destroyed)
	}
	out := f.stderr.String()
	if !strings.Contains(out, "niwa: kept test-ws-pass:") || !strings.Contains(out, "job:notes.txt") || strings.Contains(out, "decoy") {
		t.Errorf("reap output %q; want the kept line naming job:notes.txt", out)
	}
}

func TestPassJobsRoot_KeepsEveryUnitWhenTheHomeDirectoryCantBeResolved(t *testing.T) {
	// The error carries a token, which no output may show.
	const token = "AKIAABCDEFGHIJKLMNOP"
	for _, cfg := range []string{"", "set"} {
		t.Run("config dir "+cfg, func(t *testing.T) {
			f := newPassFixture(t)
			if cfg == "set" {
				t.Setenv(removal.ConfigDirVar, canonicalTempDir(t))
			} else {
				t.Setenv(removal.ConfigDirVar, "")
			}
			prev := passHomeDir
			passHomeDir = func() (string, error) { return "", errors.New("$HOME is not defined " + token) }
			t.Cleanup(func() { passHomeDir = prev })

			text, _, err := runReapCommand(t, f.root, "--verdict-only")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "test-ws-pass: keep\n") || !strings.Contains(text, "home directory can't be resolved") {
				t.Errorf("verdict-only output:\n%s\nwant the unit kept, naming the home directory", text)
			}
			n, err := reapWorkspaceResolved(f.root, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			reapOpportunistically(f.root)
			if n != 0 || len(*f.destroyed) != 0 {
				t.Fatalf("reaped %d, destroyed %v; a home directory that can't be resolved removes nothing", n, *f.destroyed)
			}
			out := f.stderr.String()
			if !strings.Contains(out, "niwa: kept test-ws-pass: liveness check:") || !strings.Contains(out, "home directory can't be resolved") {
				t.Errorf("reap output %q; want a kept line naming the home directory", out)
			}
			if strings.Contains(text+out, token) {
				t.Errorf("the token in the error reached the output:\n%s\n%s", text, out)
			}
		})
	}
}

func TestPassJobsRoot_KeepsEveryUnitOnARelativeOrUncleanConfigDir(t *testing.T) {
	for _, value := range []string{"relative/claude", "~/claude", "/tmp/../claude", "/tmp/claude/"} {
		t.Run(value, func(t *testing.T) {
			f := newPassFixture(t)
			t.Setenv("HOME", canonicalTempDir(t))
			t.Setenv(removal.ConfigDirVar, value)

			text, _, err := runReapCommand(t, f.root, "--verdict-only")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(text, "test-ws-pass: keep\n") || !strings.Contains(text, removal.ConfigDirVar) {
				t.Errorf("verdict-only output:\n%s\nwant the unit kept, naming %s", text, removal.ConfigDirVar)
			}

			if _, _, err := runReapCommand(t, f.root); err != nil {
				t.Fatal(err)
			}
			reapOpportunistically(f.root)
			if len(*f.destroyed) != 0 {
				t.Fatalf("destroyed %v; a jobs root that can't be resolved removes nothing", *f.destroyed)
			}
			out := f.stderr.String()
			if !strings.Contains(out, "niwa: kept test-ws-pass: liveness check:") || !strings.Contains(out, removal.ConfigDirVar) {
				t.Errorf("reap output %q; want a kept line naming %s", out, removal.ConfigDirVar)
			}
			if _, err := workspace.ReadSessionMapping(f.root, passSessionID); err != nil {
				t.Errorf("the kept unit's mapping is gone: %v", err)
			}
		})
	}
}
