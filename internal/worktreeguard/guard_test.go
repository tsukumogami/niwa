package worktreeguard

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func bashPayload(t *testing.T, command string) Payload {
	t.Helper()
	in, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		t.Fatal(err)
	}
	return Payload{ToolName: "Bash", ToolInput: in, Cwd: "/ws/alpha"}
}

func TestDecide_Bash(t *testing.T) {
	scope := Scope{InstanceRoot: "/ws/alpha", WorktreesDir: "/ws/alpha/.niwa/worktrees", Repos: []string{"koto", "niwa"}}
	refused := []string{
		"git worktree add ../x",
		"git -C public/niwa worktree add x",
		"cd public/niwa && git worktree add x",
		"GIT_DIR=x git worktree add y",
		"git --git-dir=.git worktree add x",
		"/usr/bin/git worktree add x",
		"env FOO=1 git worktree add x",
		"git -c core.bare=false worktree add x",
		"echo hi; git worktree add x",
		"make build & git worktree add x",
		"true || git worktree add x",
		"cat <<EOF > notes.txt\ngit worktree add inside heredoc\nEOF\ngit worktree add real",
	}
	for _, cmd := range refused {
		d := Decide(bashPayload(t, cmd), scope, os.Readlink)
		if d.Allow {
			t.Errorf("%q should be refused", cmd)
			continue
		}
		for _, want := range []string{"use EnterWorktree with name", "koto", "niwa", "koto/<slug>"} {
			if !strings.Contains(d.Reason, want) {
				t.Errorf("%q: reason %q lacks %q", cmd, d.Reason, want)
			}
		}
	}

	allowed := []string{
		"git worktree list",
		"git worktree remove x",
		"git worktree prune",
		"git worktree move a b",
		`grep -r "git worktree add" docs`,
		"echo git worktree add",
		`git commit -m "mention git worktree add"`,
		`bash -c "git worktree add x"`,
		"cat <<EOF\ngit worktree add only in the heredoc\nEOF",
		`echo "unterminated`,
		"",
		"git status",
	}
	for _, cmd := range allowed {
		if d := Decide(bashPayload(t, cmd), scope, os.Readlink); !d.Allow {
			t.Errorf("%q should pass, got refusal %q", cmd, d.Reason)
		}
	}
}

func TestDecide_EnterWorktree(t *testing.T) {
	root := t.TempDir()
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	instance := filepath.Join(resolvedRoot, "alpha")
	worktrees := filepath.Join(instance, ".niwa", "worktrees")
	niwaWT := filepath.Join(worktrees, "niwa-abc")
	native := filepath.Join(instance, "public", "niwa", ".claude", "worktrees", "x")
	other := filepath.Join(resolvedRoot, "beta", ".niwa", "worktrees", "niwa-def")
	outside := filepath.Join(resolvedRoot, "outside")
	for _, d := range []string{niwaWT, native, other, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(outside, "link-into-worktrees")
	if err := os.Symlink(niwaWT, link); err != nil {
		t.Fatal(err)
	}
	escapeLink := filepath.Join(worktrees, "escape")
	if err := os.Symlink(outside, escapeLink); err != nil {
		t.Fatal(err)
	}

	scope := Scope{InstanceRoot: instance, WorktreesDir: worktrees, Repos: []string{"koto", "niwa"}}
	enter := func(cwd, path string) Payload {
		in, _ := json.Marshal(map[string]string{"path": path})
		return Payload{ToolName: "EnterWorktree", ToolInput: in, Cwd: cwd}
	}
	cases := []struct {
		name  string
		p     Payload
		allow bool
	}{
		{"native checkout refused", enter(instance, native), false},
		{"niwa worktree allowed", enter(instance, niwaWT), true},
		{"relative path into worktrees allowed", enter(instance, ".niwa/worktrees/niwa-abc"), true},
		{"dot-dot escape refused", enter(instance, ".niwa/worktrees/../../public/niwa/.claude/worktrees/x"), false},
		{"symlink from outside judged by target", enter(instance, link), true},
		{"symlink inside pointing out refused", enter(instance, escapeLink), false},
		{"another instance's worktree refused", enter(instance, other), false},
		{"nonexistent path refused", enter(instance, filepath.Join(worktrees, "nope")), false},
		{"worktrees dir itself refused", enter(instance, worktrees), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := Decide(tc.p, scope, filepath.EvalSymlinks)
			if d.Allow != tc.allow {
				t.Fatalf("allow = %v, want %v (reason %q)", d.Allow, tc.allow, d.Reason)
			}
			if !d.Allow && !strings.Contains(d.Reason, "use EnterWorktree with name") {
				t.Errorf("reason %q lacks the name form", d.Reason)
			}
		})
	}

	for _, input := range []string{`{"name":"niwa/x"}`, `{}`} {
		p := Payload{ToolName: "EnterWorktree", ToolInput: json.RawMessage(input), Cwd: instance}
		if d := Decide(p, scope, filepath.EvalSymlinks); !d.Allow {
			t.Errorf("EnterWorktree %s should pass, got %q", input, d.Reason)
		}
	}
}

func TestDecide_OptedOutAllowsEverything(t *testing.T) {
	scope := Scope{InstanceRoot: "/ws/alpha", WorktreesDir: "/ws/alpha/.niwa/worktrees", OptedOut: true}
	if d := Decide(bashPayload(t, "git worktree add x"), scope, os.Readlink); !d.Allow {
		t.Errorf("opted-out instance should allow git worktree add, got %q", d.Reason)
	}
	in, _ := json.Marshal(map[string]string{"path": "/anywhere"})
	if d := Decide(Payload{ToolName: "EnterWorktree", ToolInput: in}, scope, os.Readlink); !d.Allow {
		t.Errorf("opted-out instance should allow a path-form EnterWorktree, got %q", d.Reason)
	}
}

func TestDecide_NoInstanceListsInstances(t *testing.T) {
	scope := Scope{Instances: []string{"alpha", "beta"}}
	d := Decide(bashPayload(t, "git worktree add x"), scope, os.Readlink)
	if d.Allow || !strings.Contains(d.Reason, "alpha, beta") || !strings.Contains(d.Reason, "workspace root") {
		t.Errorf("want a refusal naming the instances, got %+v", d)
	}
	in, _ := json.Marshal(map[string]string{"path": "/ws/alpha/public/niwa/.claude/worktrees/x"})
	if d := Decide(Payload{ToolName: "EnterWorktree", ToolInput: in, Cwd: "/ws"}, scope, os.Readlink); d.Allow {
		t.Error("path-form EnterWorktree at a workspace root with no instance should be refused")
	}
}

func TestDecide_OtherToolsAndMalformedInput(t *testing.T) {
	scope := Scope{InstanceRoot: "/ws/alpha"}
	for _, p := range []Payload{
		{ToolName: "Read", ToolInput: json.RawMessage(`{"file_path":"x"}`)},
		{ToolName: "Bash", ToolInput: json.RawMessage(`not json`)},
		{ToolName: "EnterWorktree", ToolInput: json.RawMessage(`not json`)},
	} {
		if d := Decide(p, scope, os.Readlink); !d.Allow {
			t.Errorf("%s with %s should pass, got %q", p.ToolName, p.ToolInput, d.Reason)
		}
	}
}

// TestPackageStartsNoProcessOrConnection keeps the guard cheap on every Bash
// call: the package must not import os/exec or net.
func TestPackageStartsNoProcessOrConnection(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Skipf("go list unavailable: %v", err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if dep == "os/exec" || dep == "net" {
			t.Errorf("internal/worktreeguard must not depend on %s", dep)
		}
	}
}
