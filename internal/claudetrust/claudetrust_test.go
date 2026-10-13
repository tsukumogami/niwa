package claudetrust

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestConfigFile(t *testing.T) {
	tests := []struct {
		name    string
		env     Env
		want    string
		wantErr bool
	}{
		{name: "CLAUDE_CONFIG_DIR wins", env: Env{Home: "/h", ClaudeConfigDir: "/c"}, want: "/c/.claude.json"},
		{name: "CLAUDE_CONFIG_DIR cleaned", env: Env{Home: "/h", ClaudeConfigDir: "/c/./d/"}, want: "/c/d/.claude.json"},
		{name: "empty CLAUDE_CONFIG_DIR falls back to home", env: Env{Home: "/h", ClaudeConfigDir: ""}, want: "/h/.claude.json"},
		{name: "relative CLAUDE_CONFIG_DIR refused", env: Env{Home: "/h", ClaudeConfigDir: "rel/dir"}, wantErr: true},
		{name: "no home and no dir", env: Env{}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConfigFile(tt.env)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ConfigFile = %q, want error", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("ConfigFile = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKeyResolvesSymlinks(t *testing.T) {
	base := t.TempDir() // on macOS this is under /var, a symlink to /private/var
	real := filepath.Join(base, "real-inst")
	parent := filepath.Join(base, "parent")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(parent, "inst"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkRoot := filepath.Join(base, "link-inst")
	linkParent := filepath.Join(base, "parent-link")
	if err := os.Symlink(real, linkRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, linkParent); err != nil {
		t.Fatal(err)
	}

	wantReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	wantChild, err := filepath.EvalSymlinks(filepath.Join(parent, "inst"))
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ in, want string }{
		{real, wantReal},
		{linkRoot, wantReal},
		{filepath.Join(linkParent, "inst"), wantChild},
		{filepath.Join(linkParent, ".", "inst", "..", "inst"), wantChild},
	} {
		if got := mustKey(t, tc.in); got != tc.want {
			t.Errorf("Key(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}

	if _, err := Key(filepath.Join(base, "missing")); err == nil {
		t.Error("Key of a missing root succeeded, want error")
	}
}

func TestEnsureCreatesFileUnderClaudeConfigDir(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(filepath.Dir(f.env.Home), "claude-config")
	env := Env{Home: f.env.Home, ClaudeConfigDir: dir}

	out := Ensure(env, f.root, nil, SourceWorkspace)
	if out.Warning != "" {
		t.Fatalf("warning: %s", out.Warning)
	}
	file := filepath.Join(dir, ".claude.json")
	want := &Grant{ConfigFile: file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	if !reflect.DeepEqual(out.Grant, want) || !out.Changed {
		t.Fatalf("outcome = %+v (grant %+v), want created grant %+v", out, out.Grant, want)
	}
	assertMode(t, dir, 0o700)
	assertMode(t, file, 0o600)
	doc := decodeAny(t, readFile(t, file))
	wantDoc := map[string]any{"projects": map[string]any{f.key: map[string]any{"hasTrustDialogAccepted": true}}}
	if !reflect.DeepEqual(doc, wantDoc) {
		t.Errorf("file = %v, want %v", doc, wantDoc)
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Errorf("$HOME/.claude.json touched with CLAUDE_CONFIG_DIR set (err=%v)", err)
	}
	assertClean(t, file)
	assertNoHooksKey(t, file)
}

func TestEnsureEmptyConfigDirUsesHome(t *testing.T) {
	f := newFixture(t)
	out := Ensure(Env{Home: f.env.Home, ClaudeConfigDir: ""}, f.root, nil, SourceWorkspace)
	if out.Warning != "" || out.Grant == nil || out.Grant.ConfigFile != f.file {
		t.Fatalf("outcome = %+v, want a grant for %s", out, f.file)
	}
	assertMode(t, f.file, 0o600)
}

func TestEnsureRefusesRelativeConfigDir(t *testing.T) {
	f := newFixture(t)
	prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	out := Ensure(Env{Home: f.env.Home, ClaudeConfigDir: "rel-claude-dir"}, f.root, prior, SourceWorkspace)
	if out.Warning == "" || !strings.Contains(out.Warning, "rel-claude-dir") {
		t.Errorf("warning = %q, want one naming the relative dir", out.Warning)
	}
	if out.Grant != prior || out.Changed {
		t.Errorf("outcome = %+v, want prior unchanged", out)
	}
	if _, err := os.Stat("rel-claude-dir"); !os.IsNotExist(err) {
		t.Error("a relative CLAUDE_CONFIG_DIR was created in the working directory")
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("$HOME/.claude.json written despite the refusal")
	}
}

func TestEnsureEmptyHomeWritesNothing(t *testing.T) {
	f := newFixture(t)
	dir := filepath.Join(filepath.Dir(f.env.Home), "claude-config")
	prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeFlagOnly, Source: SourceReview}
	out := Ensure(Env{ClaudeConfigDir: dir}, f.root, prior, SourceWorkspace)
	if out.Grant != prior || out.Changed || out.Warning != "" {
		t.Errorf("outcome = %+v, want a silent no-op returning prior", out)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("CLAUDE_CONFIG_DIR created with an empty Home")
	}
}

func TestEnsureUnknownSourceRefused(t *testing.T) {
	f := newFixture(t)
	out := Ensure(f.env, f.root, nil, Source("bogus"))
	if out.Warning == "" || out.Grant != nil {
		t.Errorf("outcome = %+v, want a warning and no grant", out)
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("config written for an unknown source")
	}
}

func TestEnsureMissingRootWarns(t *testing.T) {
	f := newFixture(t)
	out := Ensure(f.env, filepath.Join(f.root, "gone"), nil, SourceWorkspace)
	if out.Warning == "" || out.Grant != nil {
		t.Errorf("outcome = %+v, want a warning and no grant", out)
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("config written for a missing root")
	}
}

func TestEnsureEmptyFileIsEmptyObject(t *testing.T) {
	for name, content := range map[string]string{"zero bytes": "", "whitespace": " \n\t"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			writeFile(t, f.file, content, 0o600)
			out := Ensure(f.env, f.root, nil, SourceWorkspace)
			if out.Warning != "" || out.Grant == nil || out.Grant.Mode != ModeCreated {
				t.Fatalf("outcome = %+v, want a created grant", out)
			}
			e, ok := entryOf(t, f.file, f.key)
			if !ok || e["hasTrustDialogAccepted"] != true {
				t.Errorf("entry = %v, want the flag set", e)
			}
		})
	}
}

func TestEnsureFlagAbsentOrFalseIsFlagOnly(t *testing.T) {
	for name, flag := range map[string]string{"absent": "", "false": `"hasTrustDialogAccepted": false, `} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			writeFile(t, f.file, `{"userID": "u", "projects": {`+jsonKey(t, f.key)+`: {`+flag+
				`"allowedTools": ["Read"], "lastCost": 0.120, "history": [{"display": "a<b"}]}}}`, 0o600)
			before := decodeAny(t, readFile(t, f.file))

			out := Ensure(f.env, f.root, nil, SourceWorkspace)
			want := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeFlagOnly, Source: SourceWorkspace}
			if out.Warning != "" || !out.Changed || !reflect.DeepEqual(out.Grant, want) {
				t.Fatalf("outcome = %+v (grant %+v), want %+v", out, out.Grant, want)
			}
			after := decodeAny(t, readFile(t, f.file))
			entry := after["projects"].(map[string]any)[f.key].(map[string]any)
			if entry["hasTrustDialogAccepted"] != true {
				t.Errorf("flag = %v, want true", entry["hasTrustDialogAccepted"])
			}
			delete(entry, "hasTrustDialogAccepted")
			beforeEntry := before["projects"].(map[string]any)[f.key].(map[string]any)
			delete(beforeEntry, "hasTrustDialogAccepted")
			if !reflect.DeepEqual(before, after) {
				t.Errorf("other keys changed:\nbefore %v\nafter  %v", before, after)
			}
			assertNoHooksKey(t, f.file)
			assertClean(t, f.file)
		})
	}
}

func TestEnsureAlreadyTrusted(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.file, `{"projects": {`+jsonKey(t, f.key)+`: {"hasTrustDialogAccepted": true, "x": 1}}}`, 0o600)
	before := snap(t, f.file)

	t.Run("not recorded returns no grant", func(t *testing.T) {
		out := Ensure(f.env, f.root, nil, SourceWorkspace)
		if out.Grant != nil || out.Changed || out.Warning != "" {
			t.Errorf("outcome = %+v, want nothing recorded", out)
		}
		assertUntouched(t, f.file, before)
	})
	t.Run("recorded returns prior", func(t *testing.T) {
		prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceReview}
		out := Ensure(f.env, f.root, prior, SourceWorkspace)
		if out.Grant != prior || out.Changed || out.Warning != "" {
			t.Errorf("outcome = %+v, want prior (source review) unchanged", out)
		}
		assertUntouched(t, f.file, before)
	})
}

func TestEnsureSecondCallIsNoOp(t *testing.T) {
	f := newFixture(t)
	first := Ensure(f.env, f.root, nil, SourceWorkspace)
	if first.Grant == nil || !first.Changed {
		t.Fatalf("first outcome = %+v", first)
	}
	before := snap(t, f.file)
	second := Ensure(f.env, f.root, first.Grant, SourceWorkspace)
	if second.Grant != first.Grant || second.Changed || second.Warning != "" {
		t.Errorf("second outcome = %+v, want prior returned unchanged", second)
	}
	assertUntouched(t, f.file, before)
	assertClean(t, f.file)
}

func TestEnsureSkipsBadShapes(t *testing.T) {
	cases := map[string]string{
		"invalid JSON":          `{"projects": {`,
		"top-level array":       `[]`,
		"top-level null":        `null`,
		"top-level string":      `"x"`,
		"projects array":        `{"projects": []}`,
		"projects null":         `{"projects": null}`,
		"entry not object":      `{"projects": {KEY: true}}`,
		"entry null":            `{"projects": {KEY: null}}`,
		"flag not boolean":      `{"projects": {KEY: {"hasTrustDialogAccepted": "yes"}}}`,
		"trailing garbage":      `{"projects": {}} x`,
		"entry array":           `{"projects": {KEY: []}}`,
		"flag numeric":          `{"projects": {KEY: {"hasTrustDialogAccepted": 1}}}`,
		"projects string value": `{"projects": "none"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			writeFile(t, f.file, strings.ReplaceAll(content, "KEY", jsonKey(t, f.key)), 0o600)
			before := snap(t, f.file)
			prior := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeFlagOnly, Source: SourceWorkspace}

			for _, p := range []*Grant{nil, prior} {
				out := Ensure(f.env, f.root, p, SourceWorkspace)
				if !strings.Contains(out.Warning, f.file) {
					t.Errorf("warning = %q, want it to name %s", out.Warning, f.file)
				}
				if out.Grant != p || out.Changed {
					t.Errorf("outcome = %+v, want prior %v unchanged", out, p)
				}
				assertUntouched(t, f.file, before)
				assertClean(t, f.file)
			}
		})
	}
}

func TestEnsureSkipsDirectoryAtConfigPath(t *testing.T) {
	f := newFixture(t)
	if err := os.Mkdir(f.file, 0o700); err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if !strings.Contains(out.Warning, f.file) || out.Grant != nil {
		t.Errorf("outcome = %+v, want a warning naming %s", out, f.file)
	}
	if entries, _ := os.ReadDir(f.file); len(entries) != 0 {
		t.Errorf("wrote into the directory: %v", entries)
	}
}

func TestEnsureSkipsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-0000 files")
	}
	f := newFixture(t)
	writeFile(t, f.file, `{"keep": true}`, 0o600)
	if err := os.Chmod(f.file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.file, 0o600) })
	infoBefore, _ := os.Stat(f.file)

	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if !strings.Contains(out.Warning, f.file) || out.Grant != nil || out.Changed {
		t.Errorf("outcome = %+v, want a warning naming %s", out, f.file)
	}
	infoAfter, _ := os.Stat(f.file)
	if !os.SameFile(infoBefore, infoAfter) || !infoBefore.ModTime().Equal(infoAfter.ModTime()) || infoAfter.Mode().Perm() != 0 {
		t.Error("unreadable file was replaced or changed")
	}
	_ = os.Chmod(f.file, 0o600)
	if got := string(readFile(t, f.file)); got != `{"keep": true}` {
		t.Errorf("contents = %q, want unchanged", got)
	}
	assertClean(t, f.file)
}

func TestEnsureSkipsDanglingConfigSymlink(t *testing.T) {
	f := newFixture(t)
	if err := os.Symlink(filepath.Join(f.env.Home, "nowhere.json"), f.file); err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if !strings.Contains(out.Warning, f.file) || out.Grant != nil {
		t.Errorf("outcome = %+v, want a warning naming %s", out, f.file)
	}
	if _, err := os.Stat(filepath.Join(f.env.Home, "nowhere.json")); !os.IsNotExist(err) {
		t.Error("a dangling symlink's target was created")
	}
}

func TestEnsureWritesThroughSymlinkedConfig(t *testing.T) {
	f := newFixture(t)
	fastLock(t, 300*time.Millisecond)
	dotfiles := filepath.Join(filepath.Dir(f.env.Home), "dotfiles")
	target := filepath.Join(dotfiles, "claude.json")
	writeFile(t, target, `{"keep": 1}`, 0o640)
	if err := os.Symlink(target, f.file); err != nil {
		t.Fatal(err)
	}
	// A lock held on the target's name must not matter: the lock Claude Code
	// takes, and so niwa takes, is named after the link.
	if err := os.Mkdir(target+lockSuffix, 0o700); err != nil {
		t.Fatal(err)
	}

	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Warning != "" || out.Grant == nil {
		t.Fatalf("outcome = %+v, want a grant", out)
	}
	if out.Grant.ConfigFile != f.file {
		t.Errorf("grant config file = %s, want the unresolved link %s", out.Grant.ConfigFile, f.file)
	}
	info, err := os.Lstat(f.file)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: mode %v err %v", info.Mode(), err)
	}
	if dest, _ := os.Readlink(f.file); dest != target {
		t.Errorf("link now points at %s, want %s", dest, target)
	}
	if e, ok := entryOf(t, target, f.key); !ok || e["hasTrustDialogAccepted"] != true {
		t.Errorf("target entry = %v, want the flag", e)
	}
	if doc := decodeAny(t, readFile(t, target)); doc["keep"] == nil {
		t.Errorf("other key lost: %v", doc)
	}
	assertMode(t, target, 0o640)
	if _, err := os.Lstat(target + lockSuffix); err != nil {
		t.Errorf("the target-named lock (not niwa's) was removed: %v", err)
	}
	if err := os.Remove(target + lockSuffix); err != nil {
		t.Fatal(err)
	}
	assertClean(t, f.file)
	assertClean(t, target)

	// A lock held on the link's name blocks the write.
	if err := os.Mkdir(f.file+lockSuffix, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(f.root), "inst-2")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	before := snap(t, target)
	blocked := Ensure(f.env, other, nil, SourceWorkspace)
	if !strings.Contains(blocked.Warning, f.file+lockSuffix) || blocked.Grant != nil {
		t.Errorf("outcome = %+v, want a skip naming %s", blocked, f.file+lockSuffix)
	}
	assertUntouched(t, target, before)
}

func TestEnsureKeysSymlinkedRoot(t *testing.T) {
	f := newFixture(t)
	real := f.root
	link := filepath.Join(filepath.Dir(f.env.Home), "inst-link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, link, nil, SourceWorkspace)
	if out.Grant == nil || out.Grant.Key != resolved {
		t.Fatalf("outcome = %+v, want key %s", out, resolved)
	}
	if _, ok := entryOf(t, f.file, resolved); !ok {
		t.Errorf("no entry at the resolved key %s", resolved)
	}
	if _, ok := entryOf(t, f.file, link); ok {
		t.Errorf("entry written at the unresolved link path %s", link)
	}
}

// TestEnsureAndWithdrawPreserveValues checks R8 and R19: every value outside
// the one flag survives a write and a withdrawal byte-for-byte, and the file
// keeps its mode.
func TestEnsureAndWithdrawPreserveValues(t *testing.T) {
	f := newFixture(t)
	// escaped is a JSON string written with backslash-u escapes, assembled so
	// the source holds no such sequence an editor could fold into runes.
	u := `\` + "u"
	escaped := `"` + u + `003cscript` + u + `003e \"q\" \\ ` + u + `00e9"`
	original := `{
  "numStartups": 12345678901234567890,
  "tip": "<a&b>",
  "escaped": ` + escaped + `,
  "floats": [2.50, 1e400, -0.0],
  "projects": {
    "/other/project": {"allowedTools": ["Bash(<x>)"], "hasTrustDialogAccepted": true, "big": 98765432109876543210}
  },
  "oauthAccount": {"emailAddress": "a&b@example.com"}
}`
	writeFile(t, f.file, original, 0o644)
	before := decodeAny(t, []byte(original))
	tokens := []string{
		`12345678901234567890`, `"<a&b>"`, escaped,
		`2.50`, `1e400`, `-0.0`, `"Bash(<x>)"`, `98765432109876543210`, `"a&b@example.com"`,
	}
	check := func(stage string) {
		t.Helper()
		data := readFile(t, f.file)
		for _, tok := range tokens {
			if !bytes.Contains(data, []byte(tok)) {
				t.Errorf("%s: value %s not preserved:\n%s", stage, tok, data)
			}
		}
		if bytes.Contains(data, []byte(u+"0026")) || bytes.Contains(data, []byte(`"`+u+"003ca")) {
			t.Errorf("%s: HTML escaping applied:\n%s", stage, data)
		}
		if !bytes.HasSuffix(data, []byte("}\n")) || !bytes.Contains(data, []byte("\n  \"projects\": {\n")) {
			t.Errorf("%s: not two-space indented with a trailing newline:\n%s", stage, data)
		}
		assertMode(t, f.file, 0o644)
		assertNoHooksKey(t, f.file)
		assertClean(t, f.file)
	}

	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Warning != "" || out.Grant == nil || out.Grant.Mode != ModeCreated {
		t.Fatalf("outcome = %+v", out)
	}
	check("after Ensure")
	after := decodeAny(t, readFile(t, f.file))
	delete(after["projects"].(map[string]any), f.key)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("values changed:\nbefore %v\nafter  %v", before, after)
	}

	if w := Withdraw(out.Grant, f.key, Destroy); w != "" {
		t.Fatalf("withdraw warning: %s", w)
	}
	check("after Withdraw")
	if !reflect.DeepEqual(before, decodeAny(t, readFile(t, f.file))) {
		t.Errorf("values changed after withdraw:\n%s", readFile(t, f.file))
	}
}

func TestEnsureChangedConfigFileRetractsOldGrant(t *testing.T) {
	f := newFixture(t)
	base := filepath.Dir(f.env.Home)
	envA := Env{Home: f.env.Home, ClaudeConfigDir: filepath.Join(base, "a")}
	envB := Env{Home: f.env.Home} // CLAUDE_CONFIG_DIR unset at the later apply
	fileA := filepath.Join(base, "a", ".claude.json")

	t.Run("created grant", func(t *testing.T) {
		resetLockState(t)
		g := Ensure(envA, f.root, nil, SourceWorkspace).Grant
		if g == nil || g.ConfigFile != fileA {
			t.Fatalf("grant in A = %+v", g)
		}
		out := Ensure(envB, f.root, g, SourceWorkspace)
		if out.Warning != "" {
			t.Fatalf("warning: %s", out.Warning)
		}
		want := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
		if !reflect.DeepEqual(out.Grant, want) {
			t.Errorf("grant = %+v, want %+v", out.Grant, want)
		}
		e, ok := entryOf(t, fileA, f.key)
		if !ok {
			t.Fatal("Retract removed the whole entry from A")
		}
		if _, has := e["hasTrustDialogAccepted"]; has {
			t.Errorf("flag still in A: %v", e)
		}
	})

	t.Run("flag-only grant keeps other keys", func(t *testing.T) {
		resetLockState(t)
		writeFile(t, fileA, `{"projects": {`+jsonKey(t, f.key)+`: {"allowedTools": ["Read"]}}}`, 0o600)
		_ = os.Remove(f.file)
		g := Ensure(envA, f.root, nil, SourceWorkspace).Grant
		if g == nil || g.Mode != ModeFlagOnly {
			t.Fatalf("grant in A = %+v", g)
		}
		out := Ensure(envB, f.root, g, SourceWorkspace)
		if out.Warning != "" || out.Grant == nil || out.Grant.ConfigFile != f.file {
			t.Fatalf("outcome = %+v", out)
		}
		e, _ := entryOf(t, fileA, f.key)
		if _, has := e["hasTrustDialogAccepted"]; has || e["allowedTools"] == nil {
			t.Errorf("A entry = %v, want allowedTools kept and flag gone", e)
		}
	})

	t.Run("failed withdrawal keeps the record and writes nothing new", func(t *testing.T) {
		resetLockState(t)
		_ = os.Remove(f.file)
		g := &Grant{ConfigFile: fileA, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
		writeFile(t, fileA, `{not json`, 0o600)
		before := snap(t, fileA)
		out := Ensure(envB, f.root, g, SourceWorkspace)
		if out.Grant != g || out.Changed || !strings.Contains(out.Warning, fileA) {
			t.Errorf("outcome = %+v, want prior kept with a warning naming %s", out, fileA)
		}
		assertUntouched(t, fileA, before)
		if _, err := os.Stat(f.file); !os.IsNotExist(err) {
			t.Error("new file written although the old grant was not withdrawn")
		}
	})

	t.Run("old file gone", func(t *testing.T) {
		resetLockState(t)
		_ = os.Remove(fileA)
		g := &Grant{ConfigFile: fileA, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
		out := Ensure(envB, f.root, g, SourceWorkspace)
		if out.Warning != "" || out.Grant == nil || out.Grant.ConfigFile != f.file {
			t.Errorf("outcome = %+v, want a fresh grant in %s", out, f.file)
		}
		if _, err := os.Stat(fileA); !os.IsNotExist(err) {
			t.Error("withdrawal recreated the old file")
		}
	})
}

func TestEnsureMovedInstanceLeavesOldEntry(t *testing.T) {
	f := newFixture(t)
	g := Ensure(f.env, f.root, nil, SourceWorkspace).Grant
	if g == nil {
		t.Fatal("no grant")
	}
	moved := filepath.Join(filepath.Dir(f.root), "inst-moved")
	if err := os.Rename(f.root, moved); err != nil {
		t.Fatal(err)
	}

	out := Ensure(f.env, moved, g, SourceWorkspace)
	if !strings.Contains(out.Warning, g.Key) || !strings.Contains(out.Warning, f.file) {
		t.Errorf("warning = %q, want it to name %s and %s", out.Warning, g.Key, f.file)
	}
	newKey := mustKey(t, moved)
	want := &Grant{ConfigFile: f.file, Key: newKey, Mode: ModeCreated, Source: SourceWorkspace}
	if !reflect.DeepEqual(out.Grant, want) {
		t.Errorf("grant = %+v, want %+v", out.Grant, want)
	}
	old, ok := entryOf(t, f.file, g.Key)
	if !ok || !reflect.DeepEqual(old, map[string]any{"hasTrustDialogAccepted": true}) {
		t.Errorf("old entry = %v, want it left as it was", old)
	}
}

func TestEnsureMalformedPriorIsIgnored(t *testing.T) {
	f := newFixture(t)
	elsewhere := filepath.Join(filepath.Dir(f.env.Home), "elsewhere.json")
	writeFile(t, elsewhere, `{"projects": {}}`, 0o600)
	before := snap(t, elsewhere)
	prior := &Grant{ConfigFile: elsewhere, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}

	out := Ensure(f.env, f.root, prior, SourceWorkspace)
	if out.Warning == "" {
		t.Error("no warning for a malformed prior record")
	}
	if out.Grant == nil || out.Grant.ConfigFile != f.file || out.Grant.Mode != ModeCreated {
		t.Errorf("grant = %+v, want a fresh grant in %s", out.Grant, f.file)
	}
	assertUntouched(t, elsewhere, before)
}
