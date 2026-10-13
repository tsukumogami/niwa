package claudetrust

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	f := newFixture(t)
	writeFile(t, f.file, `{}`, 0o600)
	good := Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	if err := good.Validate(f.key); err != nil {
		t.Fatalf("valid grant refused: %v", err)
	}
	missing := good
	missing.ConfigFile = filepath.Join(f.env.Home, "gone", ".claude.json")
	if err := missing.Validate(f.key); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file error = %v, want one wrapping fs.ErrNotExist", err)
	}
	var nilGrant *Grant
	if err := nilGrant.Validate(f.key); err == nil {
		t.Error("nil grant validated")
	}
}

// jsonNumber is how decodeAny represents a number.
func jsonNumber(s string) json.Number { return json.Number(s) }

func TestWithdrawRefusesBadRecords(t *testing.T) {
	f := newFixture(t)
	content := `{"projects": {` + jsonKey(t, f.key) + `: {"hasTrustDialogAccepted": true}, "/other": {"hasTrustDialogAccepted": true}}}`
	writeFile(t, f.file, content, 0o600)
	wrongName := filepath.Join(f.env.Home, "config.json")
	writeFile(t, wrongName, content, 0o600)
	dirNamed := filepath.Join(f.env.Home, "dir", ".claude.json")
	if err := os.MkdirAll(dirNamed, 0o700); err != nil {
		t.Fatal(err)
	}
	good := Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}

	cases := map[string]struct {
		mutate func(g *Grant)
		how    WithdrawMode
	}{
		"unknown mode":         {mutate: func(g *Grant) { g.Mode = "owned" }, how: Destroy},
		"empty mode":           {mutate: func(g *Grant) { g.Mode = "" }, how: Destroy},
		"unknown source":       {mutate: func(g *Grant) { g.Source = "other" }, how: Destroy},
		"wrong basename":       {mutate: func(g *Grant) { g.ConfigFile = wrongName }, how: Destroy},
		"directory":            {mutate: func(g *Grant) { g.ConfigFile = dirNamed }, how: Destroy},
		"other key":            {mutate: func(g *Grant) { g.Key = "/other" }, how: Destroy},
		"empty key":            {mutate: func(g *Grant) { g.Key = "" }, how: Destroy},
		"relative config file": {mutate: func(g *Grant) { g.ConfigFile = ".claude.json" }, how: Destroy},
		"unclean config file": {mutate: func(g *Grant) {
			g.ConfigFile = f.env.Home + "/x/../.claude.json"
		}, how: Destroy},
		"zero withdraw mode":    {mutate: func(*Grant) {}, how: 0},
		"unknown withdraw mode": {mutate: func(*Grant) {}, how: 99},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			before := snap(t, f.file)
			beforeWrong := snap(t, wrongName)
			g := good
			tc.mutate(&g)
			if w := Withdraw(&g, f.key, tc.how); w == "" {
				t.Error("Withdraw accepted a bad record")
			}
			assertUntouched(t, f.file, before)
			assertUntouched(t, wrongName, beforeWrong)
			if entries, _ := os.ReadDir(dirNamed); len(entries) != 0 {
				t.Errorf("wrote into a directory: %v", entries)
			}
			assertClean(t, f.file)
		})
	}

	if w := Withdraw(nil, f.key, Destroy); w != "" {
		t.Errorf("nil grant warning = %q, want a silent no-op", w)
	}
}

func TestWithdrawSemantics(t *testing.T) {
	other := `"/other/project": {"hasTrustDialogAccepted": true, "allowedTools": ["Bash"]}`
	cases := []struct {
		name      string
		entry     string // the entry at the instance key; "" for none
		mode      Mode
		how       WithdrawMode
		wantEntry map[string]any // nil means the entry is gone
		wantNoOp  bool
	}{
		{name: "destroy created removes entry", entry: `{"hasTrustDialogAccepted": true, "allowedTools": ["Read"]}`,
			mode: ModeCreated, how: Destroy, wantEntry: nil},
		{name: "destroy flag-only removes only flag", entry: `{"hasTrustDialogAccepted": true, "allowedTools": ["Read"]}`,
			mode: ModeFlagOnly, how: Destroy, wantEntry: map[string]any{"allowedTools": []any{"Read"}}},
		{name: "retract created removes only flag", entry: `{"hasTrustDialogAccepted": true, "allowedTools": ["Read"]}`,
			mode: ModeCreated, how: Retract, wantEntry: map[string]any{"allowedTools": []any{"Read"}}},
		{name: "retract flag-only removes only flag", entry: `{"hasTrustDialogAccepted": true, "x": 1}`,
			mode: ModeFlagOnly, how: Retract, wantEntry: map[string]any{"x": jsonNumber("1")}},
		{name: "destroy created with entry gone", entry: "", mode: ModeCreated, how: Destroy, wantNoOp: true},
		{name: "retract with entry gone", entry: "", mode: ModeFlagOnly, how: Retract, wantNoOp: true},
		{name: "destroy flag-only with flag gone", entry: `{"x": 1}`, mode: ModeFlagOnly, how: Destroy, wantNoOp: true},
		{name: "retract created with flag gone", entry: `{"x": 1}`, mode: ModeCreated, how: Retract, wantNoOp: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			projects := other
			if tc.entry != "" {
				projects = jsonKey(t, f.key) + ": " + tc.entry + ", " + other
			}
			writeFile(t, f.file, `{"keep": "<v>", "projects": {`+projects+`}}`, 0o600)
			before := snap(t, f.file)
			g := &Grant{ConfigFile: f.file, Key: f.key, Mode: tc.mode, Source: SourceWorkspace}

			if w := Withdraw(g, f.key, tc.how); w != "" {
				t.Fatalf("warning: %s", w)
			}
			if tc.wantNoOp {
				assertUntouched(t, f.file, before)
				return
			}
			e, ok := entryOf(t, f.file, f.key)
			switch {
			case tc.wantEntry == nil && ok:
				t.Errorf("entry = %v, want it removed", e)
			case tc.wantEntry != nil && !reflect.DeepEqual(e, tc.wantEntry):
				t.Errorf("entry = %v, want %v", e, tc.wantEntry)
			}
			if o, ok := entryOf(t, f.file, "/other/project"); !ok || o["hasTrustDialogAccepted"] != true {
				t.Errorf("other project changed: %v", o)
			}
			if decodeAny(t, readFile(t, f.file))["keep"] != "<v>" {
				t.Error("other top-level key changed")
			}
			assertClean(t, f.file)
			assertNoHooksKey(t, f.file)
		})
	}
}

func TestWithdrawNoOpWithoutProjectsOrFile(t *testing.T) {
	f := newFixture(t)
	g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}

	if w := Withdraw(g, f.key, Destroy); w != "" {
		t.Errorf("missing file warning = %q, want a silent no-op", w)
	}
	if _, err := os.Stat(f.file); !os.IsNotExist(err) {
		t.Error("withdrawal created the config file")
	}

	for _, content := range []string{"", `{"keep": 1}`, `{"projects": {}}`} {
		writeFile(t, f.file, content, 0o600)
		before := snap(t, f.file)
		if w := Withdraw(g, f.key, Destroy); w != "" {
			t.Errorf("%q: warning %q", content, w)
		}
		assertUntouched(t, f.file, before)
		assertClean(t, f.file)
	}
}

func TestWithdrawSkipsUnparseable(t *testing.T) {
	cases := map[string]string{
		"invalid JSON":     `{"projects": `,
		"top-level array":  `[]`,
		"projects array":   `{"projects": []}`,
		"entry not object": `{"projects": {KEY: "yes"}}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			writeFile(t, f.file, strings.ReplaceAll(content, "KEY", jsonKey(t, f.key)), 0o600)
			before := snap(t, f.file)
			g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
			w := Withdraw(g, f.key, Destroy)
			if !strings.Contains(w, f.file) || !strings.Contains(w, f.key) {
				t.Errorf("warning = %q, want it to name %s and %s", w, f.file, f.key)
			}
			assertUntouched(t, f.file, before)
			assertClean(t, f.file)
		})
	}
}

func TestWithdrawUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-0000 files")
	}
	f := newFixture(t)
	writeFile(t, f.file, `{"projects": {}}`, 0o600)
	if err := os.Chmod(f.file, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.file, 0o600) })
	g := &Grant{ConfigFile: f.file, Key: f.key, Mode: ModeCreated, Source: SourceWorkspace}
	if w := Withdraw(g, f.key, Destroy); !strings.Contains(w, f.file) {
		t.Errorf("warning = %q, want it to name %s", w, f.file)
	}
	assertClean(t, f.file)
}

func TestWithdrawThroughSymlinkedConfig(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(filepath.Dir(f.env.Home), "dotfiles", "claude.json")
	writeFile(t, target, `{"keep": 1}`, 0o640)
	if err := os.Symlink(target, f.file); err != nil {
		t.Fatal(err)
	}
	out := Ensure(f.env, f.root, nil, SourceWorkspace)
	if out.Grant == nil {
		t.Fatalf("outcome = %+v", out)
	}
	if w := Withdraw(out.Grant, f.key, Destroy); w != "" {
		t.Fatalf("warning: %s", w)
	}
	if info, err := os.Lstat(f.file); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Error("withdrawal replaced the symlink")
	}
	if _, ok := entryOf(t, target, f.key); ok {
		t.Error("entry still in the target")
	}
	assertMode(t, target, 0o640)
	assertClean(t, f.file)
	assertClean(t, target)
}
