package claudetrust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnsureReportsTrusted checks Outcome.Trusted on every path: true exactly
// when the entry ends up holding the flag, whoever set it, and false on every
// skip, the silent empty-Home skip included.
func TestEnsureReportsTrusted(t *testing.T) {
	cases := []struct {
		name string
		// run sets up the fixture and returns the outcome to check.
		run         func(t *testing.T, f fixture) Outcome
		wantTrusted bool
	}{
		{"new entry", func(t *testing.T, f fixture) Outcome {
			return Ensure(f.env, f.root, nil, SourceWorkspace)
		}, true},
		{"flag set on an existing entry", func(t *testing.T, f fixture) Outcome {
			writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": false}}}`, 0o600)
			return Ensure(f.env, f.root, nil, SourceWorkspace)
		}, true},
		{"already ours", func(t *testing.T, f fixture) Outcome {
			first := Ensure(f.env, f.root, nil, SourceWorkspace)
			return Ensure(f.env, f.root, first.Grant, SourceWorkspace)
		}, true},
		{"already trusted by someone else", func(t *testing.T, f fixture) Outcome {
			writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true}}}`, 0o600)
			return Ensure(f.env, f.root, nil, SourceWorkspace)
		}, true},
		{"stale record dropped beside a write", func(t *testing.T, f fixture) Outcome {
			prior := &Grant{ConfigFile: f.file, Key: "/somewhere/else", Mode: ModeCreated, Source: SourceWorkspace}
			return Ensure(f.env, f.root, prior, SourceWorkspace)
		}, true},
		{"empty Home", func(t *testing.T, f fixture) Outcome {
			return Ensure(Env{}, f.root, nil, SourceWorkspace)
		}, false},
		{"unknown source", func(t *testing.T, f fixture) Outcome {
			return Ensure(f.env, f.root, nil, Source("bogus"))
		}, false},
		{"relative CLAUDE_CONFIG_DIR", func(t *testing.T, f fixture) Outcome {
			return Ensure(Env{Home: f.env.Home, ClaudeConfigDir: "rel"}, f.root, nil, SourceWorkspace)
		}, false},
		{"missing root", func(t *testing.T, f fixture) Outcome {
			return Ensure(f.env, filepath.Join(f.root, "gone"), nil, SourceWorkspace)
		}, false},
		{"unparseable file", func(t *testing.T, f fixture) Outcome {
			writeFile(t, f.file, `{"projects": {`, 0o600)
			return Ensure(f.env, f.root, nil, SourceWorkspace)
		}, false},
		{"legacy config file present", func(t *testing.T, f fixture) Outcome {
			writeFile(t, filepath.Join(f.env.Home, ".claude", ".config.json"), `{}`, 0o600)
			return Ensure(f.env, f.root, nil, SourceWorkspace)
		}, false},
		{"retraction from the old file skipped", func(t *testing.T, f fixture) Outcome {
			old := filepath.Join(filepath.Dir(f.env.Home), "claude-old", ".claude.json")
			writeFile(t, old, `{"projects": {`, 0o600)
			prior := &Grant{ConfigFile: old, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
			return Ensure(f.env, f.root, prior, SourceWorkspace)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			out := tc.run(t, f)
			if out.Trusted != tc.wantTrusted {
				t.Errorf("Trusted = %v, want %v (outcome %+v)", out.Trusted, tc.wantTrusted, out)
			}
		})
	}
}

// TestOutcomeUndo checks Undo takes back exactly what the call wrote: the
// entry it created, the flag it set on an entry that was already there, and
// nothing when the call changed nothing.
func TestOutcomeUndo(t *testing.T) {
	t.Run("created entry is removed", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"keep": 1}`, 0o600)
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if w := out.Undo(); w != "" {
			t.Fatalf("Undo warning = %q", w)
		}
		if e, ok := entryOf(t, f.file, f.key); ok {
			t.Errorf("entry = %v, want it removed", e)
		}
		if decodeAny(t, readFile(t, f.file))["keep"] == nil {
			t.Error("another key was lost")
		}
	})
	t.Run("flag set on an existing entry is retracted", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"allowedTools": ["Read"]}}}`, 0o600)
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if w := out.Undo(); w != "" {
			t.Fatalf("Undo warning = %q", w)
		}
		e, ok := entryOf(t, f.file, f.key)
		if !ok || e["allowedTools"] == nil {
			t.Fatalf("entry = %v (present %v), want it kept", e, ok)
		}
		if _, has := e[trustFlag]; has {
			t.Errorf("flag still set: %v", e)
		}
	})
	t.Run("flag re-set under a created record is only retracted", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": false, "x": 1}}}`, 0o600)
		prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
		out := Ensure(f.env, f.root, prior, SourceWorkspace)
		if out.Grant.Mode != ModeCreated || out.Created {
			t.Fatalf("outcome = %+v (grant %+v), want a re-set flag under the created record", out, out.Grant)
		}
		if w := out.Undo(); w != "" {
			t.Fatalf("Undo warning = %q", w)
		}
		if e, ok := entryOf(t, f.file, f.key); !ok || e["x"] == nil {
			t.Errorf("entry = %v (present %v), want it kept", e, ok)
		}
	})
	t.Run("nothing written is a no-op", func(t *testing.T) {
		f := newFixture(t)
		writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true}}}`, 0o600)
		before := snap(t, f.file)
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if w := out.Undo(); w != "" {
			t.Fatalf("Undo warning = %q", w)
		}
		assertUntouched(t, f.file, before)
		if w := (Outcome{}).Undo(); w != "" {
			t.Errorf("zero Outcome Undo warning = %q", w)
		}
	})
}

func TestEnvFor(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/opt/claude")
	if got := EnvFor("/home/u"); got != (Env{Home: "/home/u", ClaudeConfigDir: "/opt/claude"}) {
		t.Errorf("EnvFor = %+v", got)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if got := EnvFor("/home/u"); got != (Env{Home: "/home/u"}) {
		t.Errorf("EnvFor with CLAUDE_CONFIG_DIR empty = %+v", got)
	}
}

// TestEnsureRefusesWhenLegacyConfigExists checks a legacy .config.json in the
// config home, which Claude Code reads instead, stops the write with a warning
// naming it, under HOME/.claude and under CLAUDE_CONFIG_DIR, and that the
// prior record is kept.
func TestEnsureRefusesWhenLegacyConfigExists(t *testing.T) {
	cases := map[string]func(f fixture) (env Env, file, legacy string){
		"HOME/.claude": func(f fixture) (Env, string, string) {
			return f.env, f.file, filepath.Join(f.env.Home, ".claude", ".config.json")
		},
		"CLAUDE_CONFIG_DIR": func(f fixture) (Env, string, string) {
			dir := filepath.Join(filepath.Dir(f.env.Home), "claude-config")
			return Env{Home: f.env.Home, ClaudeConfigDir: dir}, filepath.Join(dir, ".claude.json"), filepath.Join(dir, ".config.json")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			env, file, legacy := setup(f)
			writeFile(t, legacy, `{"projects": {}}`, 0o600)
			legacyBefore := snap(t, legacy)
			prior := &Grant{ConfigFile: file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}

			out := Ensure(env, f.root, prior, SourceWorkspace)
			if out.Trusted || out.Changed || out.Grant != prior || !strings.Contains(out.Warning, legacy) {
				t.Errorf("outcome = %+v, want a skip naming %s that keeps prior", out, legacy)
			}
			if _, err := os.Stat(file); !os.IsNotExist(err) {
				t.Errorf("%s written while %s exists", file, legacy)
			}
			assertUntouched(t, legacy, legacyBefore)
		})
	}
}

// TestWithdrawIgnoresLegacyConfig checks a legacy .config.json that appeared
// after the grant doesn't stop Withdraw from removing what the record names.
func TestWithdrawIgnoresLegacyConfig(t *testing.T) {
	f := newFixture(t)
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if !out.Trusted || out.Grant == nil {
		t.Fatalf("outcome = %+v", out)
	}
	legacy := filepath.Join(f.env.Home, ".claude", ".config.json")
	writeFile(t, legacy, `{}`, 0o600)

	if w := Withdraw(out.Grant, f.key, Destroy); w != "" {
		t.Fatalf("Withdraw warning = %q", w)
	}
	if e, ok := entryOf(t, f.file, f.key); ok {
		t.Errorf("entry = %v, want it removed from the recorded file", e)
	}
	if got := string(readFile(t, legacy)); got != `{}` {
		t.Errorf("legacy file = %q, want it untouched", got)
	}
}
