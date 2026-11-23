package firstread

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const brief = "/ws/.niwa/dispatch-briefs/lane-v7.md"

var decl = Declaration{Schema: schemaVersion, Brief: brief, Lines: 3}

// noResolve resolves nothing, so only exact path matches count as the brief.
func noResolve(string) (string, error) { return "", errors.New("no such file") }

func bash(cmd, cwd string) Payload {
	in, _ := json.Marshal(map[string]string{"command": cmd})
	return Payload{ToolName: "Bash", ToolInput: in, Cwd: cwd}
}

func read(path string, offset, limit *int) Payload {
	m := map[string]any{"file_path": path}
	if offset != nil {
		m["offset"] = *offset
	}
	if limit != nil {
		m["limit"] = *limit
	}
	in, _ := json.Marshal(m)
	return Payload{ToolName: "Read", ToolInput: in, Cwd: "/ws"}
}

func intp(n int) *int { return &n }

func TestDecideBash(t *testing.T) {
	cases := []struct {
		name      string
		cmd       string
		cwd       string
		allow     bool
		satisfies bool
	}{
		{"unrelated command", "git status", "/ws", true, false},
		{"bounded sed", "sed -n 1,3p " + brief, "/ws", true, true},
		{"bounded sed quoted", "sed -n '1,3p' '" + brief + "'", "/ws", true, true},
		{"bounded sed shorter range", "sed -n 1,2p " + brief, "/ws", true, true},
		{"sed skips the opening lines", "sed -n 2,3p " + brief, "/ws", false, false},
		{"sed single later line", "sed -n 3,3p " + brief, "/ws", false, false},
		{"bounded sed relative", "sed -n 1,3p lane-v7.md", "/ws/.niwa/dispatch-briefs", true, true},
		{"head -n", "head -n 3 " + brief, "/ws", true, true},
		{"head -nK", "head -n2 " + brief, "/ws", true, true},
		{"head -K", "head -3 " + brief, "/ws", true, true},
		{"sha256sum alone", "sha256sum " + brief, "/ws", true, false},
		{"shasum alone", "shasum -a 256 " + brief, "/ws", true, false},

		{"sed past the bound", "sed -n 1,4p " + brief, "/ws", false, false},
		{"sed from zero", "sed -n 0,3p " + brief, "/ws", false, false},
		{"head past the bound", "head -n 10 " + brief, "/ws", false, false},
		{"cat", "cat " + brief, "/ws", false, false},
		{"chained sha and cat", "sha256sum " + brief + " && cat " + brief, "/ws", false, false},
		{"chained sha and bounded sed", "sha256sum " + brief + " && sed -n 1,3p " + brief, "/ws", false, false},
		{"semicolon chain", "sed -n 1,3p " + brief + "; cat " + brief, "/ws", false, false},
		{"pipe", "sed -n 1,3p " + brief + " | cat", "/ws", false, false},
		{"redirection", "sed -n 1,3p " + brief + " > /tmp/x", "/ws", false, false},
		{"substitution", "echo $(cat " + brief + ")", "/ws", false, false},
		{"unclosed quote", "cat '" + brief, "/ws", false, false},
		{"another file of the same name", "cat /elsewhere/lane-v7.md", "/ws", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(bash(c.cmd, c.cwd), decl, noResolve)
			if got.Allow != c.allow || got.Satisfies != c.satisfies {
				t.Fatalf("Decide(%q) = %+v, want allow=%v satisfies=%v", c.cmd, got, c.allow, c.satisfies)
			}
			if !got.Allow && !strings.Contains(got.Reason, "sed -n 1,3p '"+brief+"'") {
				t.Errorf("refusal %q does not name the allowed command", got.Reason)
			}
		})
	}
}

func TestDecideRead(t *testing.T) {
	cases := []struct {
		name          string
		path          string
		offset, limit *int
		allow, sat    bool
	}{
		{"other file", "/ws/README.md", nil, nil, true, false},
		{"whole brief", brief, nil, nil, false, false},
		{"limit within bound", brief, nil, intp(3), true, true},
		{"offset 1 and limit within bound", brief, intp(1), intp(3), true, true},
		{"offset 0 and limit within bound", brief, intp(0), intp(3), true, true},
		{"offset skips the opening lines", brief, intp(2), intp(2), false, false},
		{"limit past bound", brief, nil, intp(4), false, false},
		{"offset past bound", brief, intp(3), intp(2), false, false},
		{"offset only", brief, intp(1), nil, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Decide(read(c.path, c.offset, c.limit), decl, noResolve)
			if got.Allow != c.allow || got.Satisfies != c.sat {
				t.Fatalf("got %+v, want allow=%v satisfies=%v", got, c.allow, c.sat)
			}
		})
	}
}

func TestDecideGrepAndOtherTools(t *testing.T) {
	in, _ := json.Marshal(map[string]string{"pattern": "x", "path": brief})
	if got := Decide(Payload{ToolName: "Grep", ToolInput: in, Cwd: "/ws"}, decl, noResolve); got.Allow {
		t.Errorf("Grep on the brief allowed: %+v", got)
	}
	in, _ = json.Marshal(map[string]string{"pattern": "x", "path": "/ws/src"})
	if got := Decide(Payload{ToolName: "Grep", ToolInput: in, Cwd: "/ws"}, decl, noResolve); !got.Allow {
		t.Errorf("Grep elsewhere refused: %+v", got)
	}
	if got := Decide(Payload{ToolName: "Edit", ToolInput: json.RawMessage(`{}`)}, decl, noResolve); !got.Allow {
		t.Errorf("unrelated tool refused: %+v", got)
	}
	if got := Decide(Payload{ToolName: "Bash", ToolInput: json.RawMessage(`not json`)}, decl, noResolve); !got.Allow {
		t.Errorf("unreadable input refused: %+v", got)
	}
}

func TestDecideResolvesSymlinks(t *testing.T) {
	resolve := func(p string) (string, error) {
		if p == "/link/lane-v7.md" {
			return brief, nil
		}
		return "", errors.New("no such file")
	}
	if got := Decide(bash("sed -n 1,3p /link/lane-v7.md", "/ws"), decl, resolve); !got.Satisfies {
		t.Errorf("bounded read through a symlink not recognized: %+v", got)
	}
}

func TestDeclarationRoundTripAndDone(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, stateDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(dir); ok {
		t.Fatal("Load found a declaration in an empty instance")
	}
	if err := Write(dir, Declaration{Brief: brief, Lines: 3}); err != nil {
		t.Fatal(err)
	}
	got, ok := Load(dir)
	if !ok || got.Brief != brief || got.Lines != 3 {
		t.Fatalf("Load = %+v, %v", got, ok)
	}
	if Done(dir) {
		t.Fatal("Done before MarkDone")
	}
	if err := MarkDone(dir); err != nil {
		t.Fatal(err)
	}
	if !Done(dir) {
		t.Fatal("not Done after MarkDone")
	}
	if err := os.WriteFile(filepath.Join(dir, stateDir, DeclarationFile), []byte(`{"schema":99,"brief":"x","lines":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := Load(dir); ok {
		t.Error("Load accepted an unknown schema")
	}
}

func TestCountLines(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2} {
		if got := CountLines([]byte(in)); got != want {
			t.Errorf("CountLines(%q) = %d, want %d", in, got, want)
		}
	}
}
