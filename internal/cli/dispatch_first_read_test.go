package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/firstread"
)

// firstReadLaunch captures the launch's settings document hooks and the
// instance it ran in.
type firstReadLaunch struct {
	hooks    json.RawMessage
	instance string
	pass     []string
}

func captureFirstReadLaunch(f *dispatchFakes, got *firstReadLaunch) {
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		f.launchCalled++
		got.instance = req.InstanceDir
		got.pass = req.Passthrough
		for i := 0; i+1 < len(req.Passthrough); i++ {
			if req.Passthrough[i] != "--settings" {
				continue
			}
			var doc struct {
				Hooks json.RawMessage `json:"hooks"`
			}
			if err := json.Unmarshal([]byte(req.Passthrough[i+1]), &doc); err == nil {
				got.hooks = doc.Hooks
			}
		}
		return nil
	}
}

func writeBrief(t *testing.T, root string, lines int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= lines; i++ {
		b.WriteString("line\n")
	}
	p := filepath.Join(root, "brief.md")
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDispatch_FirstRead_RecordsAndCarriesTheGuard(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got firstReadLaunch
	captureFirstReadLaunch(f, &got)
	dispatchBrief = writeBrief(t, root, 10)
	dispatchFirstRead = 3
	dispatchDetach = true

	if _, stderr, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v\n%s", err, stderr)
	}
	decl, ok := firstread.Load(got.instance)
	if !ok {
		t.Fatalf("the instance %s holds no first-read declaration", got.instance)
	}
	wantBrief, _ := filepath.EvalSymlinks(dispatchBrief)
	if decl.Brief != wantBrief || decl.Lines != 3 {
		t.Errorf("declaration = %+v, want brief %s, 3 lines", decl, wantBrief)
	}
	var hooks struct {
		PreToolUse []struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"PreToolUse"`
	}
	if err := json.Unmarshal(got.hooks, &hooks); err != nil || len(hooks.PreToolUse) != 1 {
		t.Fatalf("launch hooks = %s, want one PreToolUse entry", got.hooks)
	}
	entry := hooks.PreToolUse[0]
	if entry.Matcher != firstReadGuardMatcher || len(entry.Hooks) != 1 || !strings.HasSuffix(entry.Hooks[0].Command, "' "+firstReadGuardSuffix) || strings.Contains(entry.Hooks[0].Command, "command -v") {
		t.Errorf("guard entry = %+v", entry)
	}
	if strings.Contains(string(got.hooks), "brief.md") {
		t.Errorf("the launch document must not carry the brief's path: %s", got.hooks)
	}
}

// TestFirstReadGuardHooks_RunsTheDispatchingBinary pins the hook command to
// the niwa that ran the dispatch. A PATH-first command runs whichever niwa
// the worker's PATH names first, and one that predates the subcommand prints
// help and exits 0, which lets every read through: the guard is then
// silently off.
func TestFirstReadGuardHooks_RunsTheDispatchingBinary(t *testing.T) {
	hooks := firstReadGuardHooks("/opt/niwa it's/bin/niwa")
	entry := hooks["PreToolUse"].([]any)[0].(map[string]any)
	cmd := entry["hooks"].([]any)[0].(map[string]any)["command"].(string)
	want := `'/opt/niwa it'\''s/bin/niwa' ` + firstReadGuardSuffix
	if cmd != want {
		t.Errorf("hook command = %q, want %q", cmd, want)
	}
}

// TestFirstReadGuardHooks_IgnoresAStaleNiwaOnPath runs the hook command
// through a shell, as Claude Code does, with a stale `niwa` first on PATH: one
// that doesn't know the subcommand, prints help and exits 0. Exit 0 is
// "allow", so a command that resolves niwa from PATH lets the read through.
// The dispatching binary here is a stand-in that refuses (exit 2); the hook
// must reach it.
func TestFirstReadGuardHooks_IgnoresAStaleNiwaOnPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to run the hook command")
	}
	dir := t.TempDir()
	stale := filepath.Join(dir, "path")
	if err := os.Mkdir(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	writeScript := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeScript(filepath.Join(stale, "niwa"), "echo 'Instance-level operations'; exit 0")
	dispatching := filepath.Join(dir, "dispatching niwa")
	writeScript(dispatching, "echo refused >&2; exit 2")

	hooks := firstReadGuardHooks(dispatching)
	entry := hooks["PreToolUse"].([]any)[0].(map[string]any)
	command := entry["hooks"].([]any)[0].(map[string]any)["command"].(string)

	sh := exec.Command("sh", "-c", command)
	sh.Env = append(os.Environ(), "PATH="+stale+string(os.PathListSeparator)+os.Getenv("PATH"))
	err := sh.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != guardBlockExit {
		t.Fatalf("hook command %q with a stale niwa on PATH: %v; want exit %d from the dispatching binary", command, err, guardBlockExit)
	}
}

func TestDispatch_FirstRead_AbsentChangesNothing(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	var got firstReadLaunch
	captureFirstReadLaunch(f, &got)
	dispatchBrief = writeBrief(t, root, 10)
	dispatchDetach = true

	if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.hooks != nil {
		t.Errorf("a dispatch without --first-read carries hooks: %s", got.hooks)
	}
	if _, ok := firstread.Load(got.instance); ok {
		t.Error("a dispatch without --first-read recorded a declaration")
	}
}

func TestDispatch_FirstRead_RefusalsLeaveNothingBehind(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"without --brief", func(t *testing.T, root string) { dispatchFirstRead = 3 }},
		{"negative", func(t *testing.T, root string) {
			dispatchBrief = writeBrief(t, root, 10)
			dispatchFirstRead = -1
		}},
		{"covers the whole brief", func(t *testing.T, root string) {
			dispatchBrief = writeBrief(t, root, 3)
			dispatchFirstRead = 3
		}},
		{"codex worker", func(t *testing.T, root string) {
			dispatchBrief = writeBrief(t, root, 10)
			dispatchFirstRead = 3
			t.Setenv("NIWA_DISPATCH_HARNESS", string(agent.AgentCodex))
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			f := installDispatchFakes(t, root)
			c.setup(t, root)
			if _, _, err := runDispatchCmd(t, "do a thing"); err == nil {
				t.Fatal("the dispatch must be refused")
			}
			if f.provisionCalled != 0 || f.launchCalled != 0 {
				t.Errorf("a refusal must come before anything is created: provisioned %d, launched %d", f.provisionCalled, f.launchCalled)
			}
		})
	}
}

// TestDispatch_FirstRead_ExplicitZeroIsRefused parses a real --first-read=0:
// the flag's zero value also means "not given", so only the parsed flag can
// tell a typed 0 apart, and a typed 0 is a count below 1.
func TestDispatch_FirstRead_ExplicitZeroIsRefused(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	dispatchBrief = writeBrief(t, root, 10)

	cmd := &cobra.Command{}
	cmd.Flags().IntVar(&dispatchFirstRead, "first-read", 0, "")
	if err := cmd.Flags().Parse([]string{"--first-read=0"}); err != nil {
		t.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())

	err := runDispatch(cmd, []string{"do a thing"})
	if err == nil || !strings.Contains(err.Error(), "--first-read 0") {
		t.Fatalf("an explicit --first-read 0 must be refused, got %v", err)
	}
	if f.provisionCalled != 0 || f.launchCalled != 0 {
		t.Errorf("a refusal must come before anything is created: provisioned %d, launched %d", f.provisionCalled, f.launchCalled)
	}
}

// TestFirstReadGuard_Command runs the hook command over a recorded
// declaration: the chained read is refused, the bounded read passes and is
// recorded, and after it nothing is refused.
func TestFirstReadGuard_Command(t *testing.T) {
	instance := t.TempDir()
	if err := os.Mkdir(filepath.Join(instance, ".niwa"), 0o700); err != nil {
		t.Fatal(err)
	}
	brief := writeBrief(t, t.TempDir(), 10)
	brief, _ = filepath.EvalSymlinks(brief)
	if err := firstread.Write(instance, firstread.Declaration{Brief: brief, Lines: 3}); err != nil {
		t.Fatal(err)
	}
	run := func(cmd string) (int, string) {
		in, _ := json.Marshal(map[string]any{"tool_name": "Bash", "tool_input": map[string]string{"command": cmd}, "cwd": instance})
		var stderr bytes.Buffer
		code := runFirstReadGuard(bytes.NewReader(in), &stderr, instance)
		return code, stderr.String()
	}

	code, msg := run("sha256sum " + brief + " && cat " + brief)
	if code != guardBlockExit || !strings.Contains(msg, "sed -n 1,3p") {
		t.Fatalf("chained read: code %d, stderr %q; want a refusal naming the bounded read", code, msg)
	}
	if code, _ := run("sha256sum " + brief); code != 0 {
		t.Fatalf("standalone checksum refused: %d", code)
	}
	if firstread.Done(instance) {
		t.Fatal("a checksum must not count as the first read")
	}
	if code, _ := run("sed -n 1,3p " + brief); code != 0 {
		t.Fatalf("bounded read refused: %d", code)
	}
	if !firstread.Done(instance) {
		t.Fatal("the bounded read was not recorded")
	}
	if code, _ := run("cat " + brief); code != 0 {
		t.Fatalf("after the first read a whole read must pass, got %d", code)
	}
}

func TestFirstReadGuard_FailsOpen(t *testing.T) {
	var stderr bytes.Buffer
	if code := runFirstReadGuard(strings.NewReader("not json"), &stderr, ""); code != 0 {
		t.Errorf("unparseable payload: code %d", code)
	}
	in := `{"tool_name":"Bash","tool_input":{"command":"cat /x/brief.md"},"cwd":"` + t.TempDir() + `"}`
	if code := runFirstReadGuard(strings.NewReader(in), &stderr, ""); code != 0 {
		t.Errorf("no instance: code %d", code)
	}
}
