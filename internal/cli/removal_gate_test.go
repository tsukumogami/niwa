package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/workspace"
)

// ---- The removal gate ----
//
// These tests run real commands under /bin/sh in a scratch workspace. Every
// command is a neutral shell one-liner, and the environment they record holds
// only fake values the test sets. Nothing here reads or removes anything
// outside t.TempDir().

// appendBeforeRemoveConfig adds a [session.before_remove] table to the
// workspace config under root. body is the table's lines.
func appendBeforeRemoveConfig(t *testing.T, root, body string) {
	t.Helper()
	f, err := os.OpenFile(workspaceConfigPath(root), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[session.before_remove]\n%s\n", body); err != nil {
		t.Fatal(err)
	}
}

// setBeforeRemoveCommand replaces the workspace config's [session.before_remove]
// table with one holding command, by rewriting the file from its base.
func setBeforeRemoveCommand(t *testing.T, root, base, command string) {
	t.Helper()
	if err := os.WriteFile(workspaceConfigPath(root), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	appendBeforeRemoveConfig(t, root, fmt.Sprintf("command = %q", command))
}

// reapableInstance makes one mapped ephemeral instance whose session's job
// entry is gone, so the reap targets it, and returns its path.
func reapableInstance(t *testing.T, root, name, sessionID string) string {
	t.Helper()
	inst := makeReapInstance(t, root, name)
	mapEphemeral(t, root, sessionID, inst, true)
	return inst
}

// keptLines returns the gate's spare lines in out.
func keptLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "niwa: kept ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestRemovalGate_RefusalSparesAndExitZeroRemoves: a command that prints a
// reason and exits 1 keeps a reapable instance and its mapping, says why on
// the reap's stderr, and leaves the on-reap failure count alone across
// repeated reaps; once the command exits 0, the next reap removes it.
func TestRemovalGate_RefusalSparesAndExitZeroRemoves(t *testing.T) {
	root := setupHookWorkspace(t, true)
	base, err := os.ReadFile(workspaceConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	jobsDir := t.TempDir()
	inst := reapableInstance(t, root, "test-ws-kept", reapDeadSessionID)
	stubDestroyRemoving(t)
	out := captureReapStderr(t)

	setBeforeRemoveCommand(t, string(root), string(base), "echo 'some detail'; echo 'kept by test'; exit 1")
	for i := 0; i < 3; i++ {
		n, err := reapWorkspace(root, jobsDir, time.Now())
		if err != nil {
			t.Fatalf("reap %d: %v", i, err)
		}
		if n != 0 {
			t.Fatalf("reap %d reclaimed %d instance(s); the gate kept it", i, n)
		}
	}
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("instance gone after a refusing gate: %v", err)
	}
	m, err := workspace.ReadSessionMapping(root, reapDeadSessionID)
	if err != nil {
		t.Fatalf("mapping gone after a refusing gate: %v", err)
	}
	if m.OnReapFailures != 0 {
		t.Errorf("on-reap failure count = %d after spares, want 0", m.OnReapFailures)
	}
	kept := keptLines(out.String())
	if len(kept) != 3 {
		t.Fatalf("spare lines = %q, want one per reap", kept)
	}
	want := `niwa: kept test-ws-kept: reason from before_remove: "kept by test"`
	if kept[0] != want {
		t.Errorf("spare line = %q, want %q", kept[0], want)
	}
	if strings.Contains(out.String(), "some detail") {
		t.Errorf("the command's own output reached niwa's stream: %q", out.String())
	}

	setBeforeRemoveCommand(t, root, string(base), "exit 0")
	n, err := reapWorkspace(root, jobsDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reap with an allowing gate reclaimed %d, want 1", n)
	}
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance still on disk after an allowing gate: %v", err)
	}
	if _, err := workspace.ReadSessionMapping(root, reapDeadSessionID); err == nil {
		t.Error("mapping still present after the instance was removed")
	}
}

// TestRemovalGate_NothingDeclaredStartsNoProcess: with no
// [session.before_remove], the reap removes the instance as it does today and
// the gate starts no shell at all.
func TestRemovalGate_NothingDeclaredStartsNoProcess(t *testing.T) {
	root := setupHookWorkspace(t, true)
	inst := reapableInstance(t, root, "test-ws-plain", reapDeadSessionID)
	destroyed := stubDestroyAll(t)
	out := captureReapStderr(t)

	log := filepath.Join(t.TempDir(), "shell.log")
	stub := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho run >> '"+log+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := onReapShell
	onReapShell = stub
	t.Cleanup(func() { onReapShell = prev })

	n, err := reapWorkspace(root, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(*destroyed) != 1 || (*destroyed)[0] != inst {
		t.Fatalf("reaped %d, destroyed %v; want the instance removed as today", n, *destroyed)
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Error("a shell started with nothing declared")
	}
	if kept := keptLines(out.String()); len(kept) != 0 {
		t.Errorf("spare lines with nothing declared: %q", kept)
	}
}

// TestRemovalGate_FixedEnvironment: the command sees exactly PATH, HOME,
// NIWA_REMOVE_PATH, NIWA_REMOVE_STEP, NIWA_SESSION_IDS and the declared names
// that are set, runs from the workspace root, and reads end of file on stdin.
func TestRemovalGate_FixedEnvironment(t *testing.T) {
	root := setupHookWorkspace(t, true)
	dir := t.TempDir()
	envOut := filepath.Join(dir, "env.txt")
	cwdOut := filepath.Join(dir, "cwd.txt")
	stdinOut := filepath.Join(dir, "stdin.txt")
	appendBeforeRemoveConfig(t, root, fmt.Sprintf(`command = "env > '%s'; pwd -P > '%s'; cat > '%s'; exit 1"
env = ["LISTED_MARK", "UNSET_LISTED_MARK"]`, envOut, cwdOut, stdinOut))
	t.Setenv("LISTED_MARK", "listed-fake")
	t.Setenv("UNSET_LISTED_MARK", "")
	os.Unsetenv("UNSET_LISTED_MARK")
	t.Setenv("UNRELATED_MARK", "unrelated-fake")

	inst := reapableInstance(t, root, "test-ws-env", reapDeadSessionID)
	mapEphemeral(t, root, reapLiveSessionID, inst, true) // a second mapping to the same instance
	stubDestroyAll(t)
	captureReapStderr(t)

	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(envOut)
	if err != nil {
		t.Fatalf("the command did not run: %v", err)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		got[k] = v
	}
	// The shell itself may add PWD, SHLVL and _; anything else is a leak.
	for _, shellOwn := range []string{"PWD", "SHLVL", "_", "OLDPWD"} {
		delete(got, shellOwn)
	}
	ids := strings.Fields(got["NIWA_SESSION_IDS"])
	if len(ids) != 2 {
		t.Errorf("NIWA_SESSION_IDS = %q, want both mapped ids", got["NIWA_SESSION_IDS"])
	}
	delete(got, "NIWA_SESSION_IDS")
	want := map[string]string{
		"PATH":             os.Getenv("PATH"),
		"HOME":             os.Getenv("HOME"),
		"NIWA_REMOVE_PATH": inst,
		"NIWA_REMOVE_STEP": "reap",
		"LISTED_MARK":      "listed-fake",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
		delete(got, k)
	}
	if len(got) != 0 {
		t.Errorf("unexpected variables reached the command: %v", got)
	}

	cwd, _ := os.ReadFile(cwdOut)
	wantRoot, _ := filepath.EvalSymlinks(root)
	if strings.TrimSpace(string(cwd)) != wantRoot {
		t.Errorf("working directory = %q, want %q", strings.TrimSpace(string(cwd)), wantRoot)
	}
	if in, _ := os.ReadFile(stdinOut); len(in) != 0 {
		t.Errorf("stdin carried %q, want end of file", in)
	}
}

// TestRemovalGate_ReasonIsBoundedAndSanitized: only the last non-empty output
// line is quoted, cut to 200 bytes, with control characters and bidi
// overrides replaced.
func TestRemovalGate_ReasonIsBoundedAndSanitized(t *testing.T) {
	root := setupHookWorkspace(t, true)
	reapableInstance(t, root, "test-ws-long", reapDeadSessionID)
	stubDestroyAll(t)
	out := captureReapStderr(t)
	appendBeforeRemoveConfig(t, root, `command = "printf 'first\\n'; printf 'a\\033[31mred\\342\\200\\256x%0300d\\n' 0; exit 3"`)

	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	kept := keptLines(out.String())
	if len(kept) != 1 {
		t.Fatalf("spare lines = %q, want one", kept)
	}
	reason := strings.TrimPrefix(kept[0], "niwa: kept test-ws-long: reason from before_remove: ")
	if reason == kept[0] {
		t.Fatalf("spare line has no quoted reason: %q", kept[0])
	}
	if strings.Contains(kept[0], "first") {
		t.Errorf("an earlier output line was quoted: %q", kept[0])
	}
	if strings.ContainsRune(kept[0], '\x1b') || strings.ContainsRune(kept[0], '‮') {
		t.Errorf("a control character or bidi override reached the line: %q", kept[0])
	}
	unquoted := strings.Trim(reason, `"`)
	if len(unquoted) > maxRemovalGateReasonBytes {
		t.Errorf("reason is %d bytes, want at most %d", len(unquoted), maxRemovalGateReasonBytes)
	}
	if !strings.HasSuffix(unquoted, "...") {
		t.Errorf("a cut reason should end with ...: %q", unquoted)
	}
}

// TestRemovalGate_TimeoutAndStartFailureSpare: a command past its timeout,
// and a shell that can't start, both keep the instance and say which.
func TestRemovalGate_TimeoutAndStartFailureSpare(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		inst := reapableInstance(t, root, "test-ws-slow", reapDeadSessionID)
		destroyed := stubDestroyAll(t)
		out := captureReapStderr(t)
		appendBeforeRemoveConfig(t, root, "command = \"sleep 5\"\ntimeout = \"1s\"")

		start := time.Now()
		if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 4*time.Second {
			t.Errorf("the reap took %s; the run should be cut at its 1s timeout", elapsed)
		}
		if len(*destroyed) != 0 {
			t.Errorf("destroyed %v after a timeout, want %s kept", *destroyed, inst)
		}
		if kept := keptLines(out.String()); len(kept) != 1 || !strings.Contains(kept[0], "timed out after 1s") {
			t.Errorf("spare lines = %q, want one naming the timeout", kept)
		}
	})
	t.Run("start failure", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		reapableInstance(t, root, "test-ws-nostart", reapDeadSessionID)
		destroyed := stubDestroyAll(t)
		out := captureReapStderr(t)
		appendBeforeRemoveConfig(t, root, `command = "exit 0"`)
		prev := onReapShell
		onReapShell = filepath.Join(t.TempDir(), "no-such-shell")
		t.Cleanup(func() { onReapShell = prev })

		if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
			t.Fatal(err)
		}
		if len(*destroyed) != 0 {
			t.Errorf("destroyed %v when the command could not start", *destroyed)
		}
		if kept := keptLines(out.String()); len(kept) != 1 || !strings.Contains(kept[0], "could not start") {
			t.Errorf("spare lines = %q, want one naming the start failure", kept)
		}
	})
}

// TestRemovalGate_SilentNonZeroNamesTheStatus: a command that exits non-zero
// with no output still gets a reason.
func TestRemovalGate_SilentNonZeroNamesTheStatus(t *testing.T) {
	root := setupHookWorkspace(t, true)
	reapableInstance(t, root, "test-ws-quiet", reapDeadSessionID)
	stubDestroyAll(t)
	out := captureReapStderr(t)
	appendBeforeRemoveConfig(t, root, `command = "exit 7"`)

	if _, err := reapWorkspace(root, t.TempDir(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if kept := keptLines(out.String()); len(kept) != 1 || !strings.HasSuffix(kept[0], "before_remove exited with status 7") {
		t.Errorf("spare lines = %q, want one naming status 7", kept)
	}
}
