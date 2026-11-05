package watch

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// bashWriteAttempts are the writes the live check makes from a sandboxed review's
// Bash, in the order it reports them, each with the shell that attempts it from
// the instance root.
var bashWriteAttempts = []struct{ name, shell string }{
	{"CREATE_SETTINGS_LOCAL", `printf '{}' > .claude/settings.local.json`},
	{"APPEND_SETTINGS", `printf ' ' >> .claude/settings.json`},
	{"CREATE_SKILL", `mkdir -p .claude/skills/x && printf 'x' > .claude/skills/x/SKILL.md`},
	{"CREATE_MCP_JSON", `printf '{}' > .mcp.json`},
	{"RENAME_CLAUDE_DIR", `mv .claude .claude-moved && mv .claude-moved .claude`},
	{"HARDLINK_SETTINGS", `ln .claude/settings.json hl`},
}

// bashWritesProbe is the script the review session runs with one Bash call. It
// tries each attempt in a subshell and writes NAME=LANDED or NAME=REFUSED per
// attempt, then DONE, to bash-writes.result in the instance root. The hard link
// runs before the rename so a rename that lands can't make it fail for the wrong
// reason.
func bashWritesProbe() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\nr=bash-writes.result\n: > \"$r\"\n")
	b.WriteString("try() { n=$1; shift; if sh -c \"$1\" >/dev/null 2>&1; then echo \"$n=LANDED\" >> \"$r\"; else echo \"$n=REFUSED\" >> \"$r\"; fi; }\n")
	order := []int{0, 1, 2, 3, 5, 4}
	for _, i := range order {
		a := bashWriteAttempts[i]
		fmt.Fprintf(&b, "try %s %s\n", a.name, shellQuote(a.shell))
	}
	b.WriteString("echo DONE >> \"$r\"\n")
	return b.String()
}

// TestReviewSandboxBashSettingsWrites_OnHarness is the live check of whether a
// sandboxed review's Bash can write the instance's own Claude Code settings. The
// filesystem guard covers only the built-in file tools; Bash writes are confined by
// the OS sandbox to the instance, which contains .claude/ and .mcp.json. It applies
// the production sandboxed review settings, launches a real `claude --bg` session in
// the instance, and from that session's Bash attempts each write in
// bashWriteAttempts, reporting each result separately. Any attempt that lands fails
// the test.
//
// It skips unless NIWA_WATCH_LIVE_TEST=1 and NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1
// are both set (see requireDisposableLiveHost), and it never reports a pass when it
// can't run the check: a session that never writes its result is inconclusive.
func TestReviewSandboxBashSettingsWrites_OnHarness(t *testing.T) {
	requireDisposableLiveHost(t)
	inst := t.TempDir()

	guardBin := filepath.Join(t.TempDir(), "niwa")
	if out, err := exec.Command("go", "build", "-o", guardBin, "github.com/tsukumogami/niwa/cmd/niwa").CombinedOutput(); err != nil {
		t.Fatalf("building niwa for the review hooks: %v\n%s", err, out)
	}
	useGuardBin(t, guardBin)

	// Share ~/.claude (credentials, daemon runtime) through an isolated HOME, as the
	// other live gates do, so the session authenticates without touching the real
	// ~/.claude.json.
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolving real HOME: %v", err)
	}
	testHome := t.TempDir()
	if err := os.Symlink(filepath.Join(realHome, ".claude"), filepath.Join(testHome, ".claude")); err != nil {
		t.Fatalf("sharing ~/.claude into the isolated test HOME: %v", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(realHome, ".claude.json")); rerr == nil {
		if werr := os.WriteFile(filepath.Join(testHome, ".claude.json"), data, 0o600); werr != nil {
			t.Fatalf("seeding the isolated test HOME config: %v", werr)
		}
	}
	claudeEnv := append(os.Environ(), "HOME="+testHome)

	if err := ApplyReviewSettings(inst, true, false); err != nil {
		t.Fatalf("ApplyReviewSettings: %v", err)
	}
	settingsPath := filepath.Join(inst, ".claude", "settings.json")
	settingsBefore, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(inst, "probe.sh"), []byte(bashWritesProbe()), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	prompt := "You are testing a sandbox. Run exactly this one Bash command with the Bash tool, once: sh ./probe.sh\n" +
		"Do not run anything else and do not try to work around a failure. Then STOP."
	cmd := exec.CommandContext(ctx, "claude", "--bg", "--strict-mcp-config", prompt)
	cmd.Dir = inst
	cmd.Env = claudeEnv
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("launch claude --bg (failIfUnavailable=true means a start failure = no enforceable sandbox here): %v\n%s", err, out)
	}
	if sid := parseBgSessionID(out); sid != "" {
		defer func() {
			stop := exec.Command("claude", "stop", sid)
			stop.Env = claudeEnv
			_ = stop.Run()
		}()
	}

	resultPath := filepath.Join(inst, "bash-writes.result")
	var content string
	for ctx.Err() == nil {
		if b, rerr := os.ReadFile(resultPath); rerr == nil && strings.Contains(string(b), "DONE") {
			content = string(b)
			break
		}
		time.Sleep(3 * time.Second)
	}
	if content == "" {
		t.Fatalf("inconclusive: the sandboxed session never finished the probe (instance holds: %s)", strings.Join(instFiles(inst), ", "))
	}

	reported := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			reported[k] = v
		}
	}
	// The filesystem is the authority where it can be read; the probe's own line
	// covers the rename, which it undoes.
	landed := map[string]bool{
		"CREATE_SETTINGS_LOCAL": fileExists(filepath.Join(inst, ".claude", "settings.local.json")),
		"CREATE_SKILL":          fileExists(filepath.Join(inst, ".claude", "skills", "x", "SKILL.md")),
		"CREATE_MCP_JSON":       fileExists(filepath.Join(inst, ".mcp.json")),
		"HARDLINK_SETTINGS":     fileExists(filepath.Join(inst, "hl")),
	}
	if after, rerr := os.ReadFile(settingsPath); rerr == nil {
		landed["APPEND_SETTINGS"] = !bytes.Equal(after, settingsBefore)
	}
	var got []string
	for _, a := range bashWriteAttempts {
		r := reported[a.name]
		if r == "" {
			t.Fatalf("inconclusive: the probe reported nothing for %s (result %q)", a.name, content)
		}
		if landed[a.name] {
			r = "LANDED"
		}
		t.Logf("sandboxed review Bash: %s = %s", a.name, r)
		if r == "LANDED" {
			got = append(got, a.name)
		}
	}
	if len(got) > 0 {
		t.Errorf("sandboxed review Bash wrote the instance's Claude Code settings: %s", strings.Join(got, ", "))
	}
}

// TestBashWritesProbe_LandsOutsideSandbox is the live check's soundness step, run
// everywhere: outside any sandbox, every attempt the probe makes lands, so a
// REFUSED inside the sandboxed session is the sandbox's doing, not the probe's.
func TestBashWritesProbe_LandsOutsideSandbox(t *testing.T) {
	inst := t.TempDir()
	writeAt(t, filepath.Join(inst, ".claude", "settings.json"), "{}")
	if err := os.WriteFile(filepath.Join(inst, "probe.sh"), []byte(bashWritesProbe()), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "./probe.sh")
	cmd.Dir = inst
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("probe: %v\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(inst, "bash-writes.result"))
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range bashWriteAttempts {
		if !strings.Contains(string(data), a.name+"=LANDED\n") {
			t.Errorf("%s did not land outside the sandbox; result:\n%s", a.name, data)
		}
	}
	if !strings.Contains(string(data), "DONE") {
		t.Errorf("probe did not finish; result:\n%s", data)
	}
}
