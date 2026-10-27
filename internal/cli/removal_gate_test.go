package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/watch"
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

// TestRemovalGate_CheckReturnsTheReasonWithoutPrinting: check gives a caller
// the verdict and the reason and prints nothing, so destroy can list reasons
// its own way; ask prints the same reason after the instance name.
func TestRemovalGate_CheckReturnsTheReasonWithoutPrinting(t *testing.T) {
	root := setupHookWorkspace(t, true)
	appendBeforeRemoveConfig(t, root, `command = "echo 'run in progress'; exit 1"`)
	inst := filepath.Join(root, "test-ws-check")

	var out strings.Builder
	gate := newRemovalGate(root, removeStepDestroy, &out)
	allowed, reason := gate.check(inst, nil)
	if allowed {
		t.Fatal("check allowed a path the command refused")
	}
	if reason != `reason from before_remove: "run in progress"` {
		t.Errorf("reason = %q", reason)
	}
	if out.Len() != 0 {
		t.Errorf("check printed %q; it must print nothing", out.String())
	}
	if gate.ask(inst, nil) {
		t.Fatal("ask allowed a path the command refused")
	}
	if want := "niwa: kept test-ws-check: " + reason + "\n"; out.String() != want {
		t.Errorf("ask printed %q, want %q", out.String(), want)
	}
}

// TestRemovalGate_SpentBudgetKeepsWithoutRunning: once a pass's budget is
// spent, the next path is kept without starting the command, and the reason
// says the time ran out.
func TestRemovalGate_SpentBudgetKeepsWithoutRunning(t *testing.T) {
	root := setupHookWorkspace(t, true)
	log := filepath.Join(t.TempDir(), "runs.log")
	appendBeforeRemoveConfig(t, root, fmt.Sprintf(`command = "echo run >> '%s'; sleep 1; exit 0"`, log))

	gate := newRemovalGate(root, removeStepReap, &strings.Builder{})
	gate.budget = 500 * time.Millisecond
	if allowed, _ := gate.check(filepath.Join(root, "first"), nil); allowed {
		t.Fatal("the first run outlived the budget and should have been cut")
	}
	allowed, reason := gate.check(filepath.Join(root, "second"), nil)
	if allowed || !strings.Contains(reason, "time ran out") {
		t.Errorf("second check = %v, %q; want kept because the time ran out", allowed, reason)
	}
	if runs := onReapRuns(t, log); len(runs) != 1 {
		t.Errorf("the command ran %d times, want 1 (the second path must not start it)", len(runs))
	}
}

// TestRemovalGate_UnreadableConfigKeeps: a workspace configuration that
// exists but doesn't parse keeps every path and says why.
func TestRemovalGate_UnreadableConfigKeeps(t *testing.T) {
	root := setupHookWorkspace(t, true)
	if err := os.WriteFile(workspaceConfigPath(root), []byte("[workspace\nname = "), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := newRemovalGate(root, removeStepReap, &strings.Builder{})
	allowed, reason := gate.check(filepath.Join(root, "any"), nil)
	if allowed || !strings.Contains(reason, "could not read [session.before_remove]") {
		t.Errorf("check = %v, %q; want kept because the configuration can't be read", allowed, reason)
	}
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

// ---- The backstop and the watch discard ----

// backstopInstance makes one unmapped, dispatch-named instance whose marker is
// past the backstop's TTL, so the backstop targets it, and returns its path.
func backstopInstance(t *testing.T, root, name string) string {
	t.Helper()
	inst := makeReapInstance(t, root, name)
	writeDispatchMarkerAt(t, inst, time.Now().Add(-2*dispatchBackstopTTL))
	return inst
}

// gateLogCommand returns a command fragment that appends one line per run --
// "<NIWA_REMOVE_STEP>|<NIWA_SESSION_IDS>" -- to a log file, and the file.
func gateLogCommand(t *testing.T) (command, logPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "gate.log")
	return fmt.Sprintf(`printf '%%s|%%s\n' "$NIWA_REMOVE_STEP" "$NIWA_SESSION_IDS" >> '%s'`, logPath), logPath
}

// stubShellLogging points onReapShell at a script that logs each start and
// exits 1, and returns the log, which exists only if something started.
func stubShellLogging(t *testing.T) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "shell.log")
	stub := filepath.Join(t.TempDir(), "sh")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho run >> '"+log+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := onReapShell
	onReapShell = stub
	t.Cleanup(func() { onReapShell = prev })
	return log
}

// TestRemovalGate_BackstopRefusalKeepsAndExitZeroRemoves: a command that
// prints a reason and exits 1 keeps an unmapped, past-TTL dispatch instance
// the backstop targets, and says why; the command saw step reap-backstop and
// no session ids. Once the command exits 0, the backstop removes it.
func TestRemovalGate_BackstopRefusalKeepsAndExitZeroRemoves(t *testing.T) {
	root := setupHookWorkspace(t, true)
	base, err := os.ReadFile(workspaceConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	inst := backstopInstance(t, root, dispInstOld)
	stubDestroyRemoving(t)
	out := captureReapStderr(t)
	logCmd, log := gateLogCommand(t)

	setBeforeRemoveCommand(t, root, string(base), logCmd+"; echo 'kept by test'; exit 1")
	n, err := reapBackstop(root, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("backstop reclaimed %d instance(s); the gate kept it", n)
	}
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("instance gone after a refusing gate: %v", err)
	}
	want := `niwa: kept ` + dispInstOld + `: reason from before_remove: "kept by test"`
	if kept := keptLines(out.String()); len(kept) != 1 || kept[0] != want {
		t.Errorf("spare lines = %q, want [%q]", kept, want)
	}
	if runs := onReapRuns(t, log); len(runs) != 1 || runs[0] != removeStepReapBackstop+"|" {
		t.Errorf("gate runs = %q, want one with step %s and no session ids", runs, removeStepReapBackstop)
	}

	setBeforeRemoveCommand(t, root, string(base), logCmd+"; exit 0")
	n, err = reapBackstop(root, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backstop with an allowing gate reclaimed %d, want 1", n)
	}
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance still on disk after an allowing gate: %v", err)
	}
	if runs := onReapRuns(t, log); len(runs) != 2 || runs[1] != removeStepReapBackstop+"|" {
		t.Errorf("gate runs = %q, want a second with step %s and no session ids", runs, removeStepReapBackstop)
	}
}

// TestRemovalGate_WatchDiscardRefusalKeepsAndExitZeroDiscards: niwa watch's
// discard of a staged review whose session is gone asks the gate first. A
// refusal keeps the instance and the staged record, prints the spare line and
// runs no on-reap command; the command saw step watch-discard and the review's
// session id. Once the command exits 0 the review is discarded as before and
// on-reap runs.
func TestRemovalGate_WatchDiscardRefusalKeepsAndExitZeroDiscards(t *testing.T) {
	root := setupDispatchWorkspace(t)
	onReapCmd, onReapLog := onReapLogCommand(t)
	appendOnReapConfig(t, root, fmt.Sprintf("command = %q", onReapCmd))
	base, err := os.ReadFile(workspaceConfigPath(root))
	if err != nil {
		t.Fatal(err)
	}
	inst := filepath.Join(root, "test-ws-review")
	if err := os.MkdirAll(inst, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := watch.StagedRecord{Handle: "gone0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: inst, DispatchedSHA: "aaaa111", SessionID: reapDeadSessionID, ReapKey: onReapDispatchID}
	if err := watch.SaveStagedRecord(root, rec); err != nil {
		t.Fatal(err)
	}
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, _ string) bool { return false }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
	stubDestroyRemoving(t)
	logCmd, log := gateLogCommand(t)

	setBeforeRemoveCommand(t, root, string(base), logCmd+"; echo 'kept by test'; exit 1")
	var out bytes.Buffer
	pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})
	if _, err := os.Stat(inst); err != nil {
		t.Fatalf("instance gone after a refusing gate: %v", err)
	}
	if handles, _ := watch.ListStagedHandles(root); !equalStrings(handles, []string{"gone0001"}) {
		t.Errorf("records left = %v, want the kept review's record", handles)
	}
	want := `niwa: kept test-ws-review: reason from before_remove: "kept by test"`
	if kept := keptLines(out.String()); len(kept) != 1 || kept[0] != want {
		t.Errorf("spare lines = %q, want [%q]", kept, want)
	}
	if strings.Contains(out.String(), "discarding staged review") {
		t.Errorf("a kept review was reported discarded: %q", out.String())
	}
	if runs := onReapRuns(t, log); len(runs) != 1 || runs[0] != removeStepWatchDiscard+"|"+reapDeadSessionID {
		t.Errorf("gate runs = %q, want one with step %s and the review's session id", runs, removeStepWatchDiscard)
	}
	if runs := onReapRuns(t, onReapLog); len(runs) != 0 {
		t.Errorf("on-reap ran %q for a kept review; it runs only after a discard", runs)
	}

	setBeforeRemoveCommand(t, root, string(base), logCmd+"; exit 0")
	out.Reset()
	pruneStagedRecords(context.Background(), &out, root, &fakeFreshnessClient{}, map[string]bool{})
	if _, err := os.Stat(inst); !os.IsNotExist(err) {
		t.Errorf("instance still on disk after an allowing gate: %v", err)
	}
	if handles, _ := watch.ListStagedHandles(root); len(handles) != 0 {
		t.Errorf("records left = %v, want the review discarded", handles)
	}
	if kept := keptLines(out.String()); len(kept) != 0 {
		t.Errorf("spare lines with an allowing gate: %q", kept)
	}
	if runs := onReapRuns(t, onReapLog); len(runs) != 1 {
		t.Errorf("on-reap runs = %q, want one after the discard", runs)
	}
}

// TestRemovalGate_BackstopAndDiscardWithNothingDeclared: with no
// [session.before_remove], the backstop removes its instance and the watch
// discard discards its review as before, and no shell starts for the gate.
func TestRemovalGate_BackstopAndDiscardWithNothingDeclared(t *testing.T) {
	root := setupHookWorkspace(t, true)
	inst := backstopInstance(t, root, dispInstOld)
	review := filepath.Join(root, "test-ws-review")
	if err := watch.SaveStagedRecord(root, watch.StagedRecord{Handle: "gone0001", Owner: "acme", Repo: "api", Number: 1, InstancePath: review, SessionID: reapDeadSessionID}); err != nil {
		t.Fatal(err)
	}
	origLive := stagedInstanceLiveFunc
	stagedInstanceLiveFunc = func(_, _ string) bool { return false }
	t.Cleanup(func() { stagedInstanceLiveFunc = origLive })
	destroyed := stubDestroyAll(t)
	out := captureReapStderr(t)
	shellLog := stubShellLogging(t)

	n, err := reapBackstop(root, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("backstop reclaimed %d, want 1", n)
	}
	var watchOut bytes.Buffer
	pruneStagedRecords(context.Background(), &watchOut, root, &fakeFreshnessClient{}, map[string]bool{})
	if want := []string{inst, review}; !equalStrings(*destroyed, want) {
		t.Errorf("destroyed = %v, want %v", *destroyed, want)
	}
	if handles, _ := watch.ListStagedHandles(root); len(handles) != 0 {
		t.Errorf("records left = %v, want the review discarded", handles)
	}
	if _, err := os.Stat(shellLog); !os.IsNotExist(err) {
		t.Error("a shell started with nothing declared")
	}
	if kept := keptLines(out.String() + watchOut.String()); len(kept) != 0 {
		t.Errorf("spare lines with nothing declared: %q", kept)
	}
}

// TestRemovalGate_BackstopWithinTimeoutAndStartFailure: a command that sleeps
// 2 seconds and exits 0 under a 10-second timeout lets the backstop remove
// its instance; a shell that can't start keeps it and says so.
func TestRemovalGate_BackstopWithinTimeoutAndStartFailure(t *testing.T) {
	t.Run("within timeout", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		inst := backstopInstance(t, root, dispInstOld)
		destroyed := stubDestroyAll(t)
		out := captureReapStderr(t)
		appendBeforeRemoveConfig(t, root, "command = \"sleep 2; exit 0\"\ntimeout = \"10s\"")

		n, err := reapBackstop(root, t.TempDir(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if n != 1 || len(*destroyed) != 1 || (*destroyed)[0] != inst {
			t.Fatalf("reclaimed %d, destroyed %v; want %s removed", n, *destroyed, inst)
		}
		if kept := keptLines(out.String()); len(kept) != 0 {
			t.Errorf("spare lines = %q, want none", kept)
		}
	})
	t.Run("start failure", func(t *testing.T) {
		root := setupHookWorkspace(t, true)
		inst := backstopInstance(t, root, dispInstOld)
		destroyed := stubDestroyAll(t)
		out := captureReapStderr(t)
		appendBeforeRemoveConfig(t, root, `command = "exit 0"`)
		prev := onReapShell
		onReapShell = filepath.Join(t.TempDir(), "no-such-shell")
		t.Cleanup(func() { onReapShell = prev })

		n, err := reapBackstop(root, t.TempDir(), time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 || len(*destroyed) != 0 {
			t.Fatalf("reclaimed %d, destroyed %v; want %s kept when the command could not start", n, *destroyed, inst)
		}
		kept := keptLines(out.String())
		if len(kept) != 1 || !strings.HasPrefix(kept[0], "niwa: kept "+dispInstOld+": ") || !strings.Contains(kept[0], "could not start") {
			t.Errorf("spare lines = %q, want one naming the start failure", kept)
		}
	})
}

// backstopBudgetInstances makes four backstop targets for the budget tests.
func backstopBudgetInstances(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"test-ws+-0000a001", "test-ws+-0000a002", "test-ws+-0000a003", "test-ws+-0000a004"} {
		backstopInstance(t, root, name)
	}
}

// checkBackstopBudget runs one backstop pass over four targets whose command
// never answers in time, and checks that three runs were cut by their timeout
// and kept their instances, and the fourth kept its instance without running
// because the step's time ran out. firstTimeout is what the first two lines
// name; the third run's timeout is whatever was left of the budget.
func checkBackstopBudget(t *testing.T, root, runLog, firstTimeout string, maxElapsed time.Duration) {
	t.Helper()
	destroyed := stubDestroyAll(t)
	out := captureReapStderr(t)

	start := time.Now()
	n, err := reapBackstop(root, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > maxElapsed {
		t.Errorf("the backstop took %s, want at most %s", elapsed, maxElapsed)
	}
	if n != 0 || len(*destroyed) != 0 {
		t.Fatalf("reclaimed %d, destroyed %v; want every instance kept", n, *destroyed)
	}
	kept := keptLines(out.String())
	if len(kept) != 4 {
		t.Fatalf("spare lines = %q, want one per instance", kept)
	}
	timedOut, ranOut := 0, 0
	for i, line := range kept {
		switch {
		case strings.Contains(line, "before_remove timed out after "):
			timedOut++
			if i < 2 && !strings.HasSuffix(line, "timed out after "+firstTimeout) {
				t.Errorf("line %d = %q, want it cut at %s", i, line, firstTimeout)
			}
		case strings.Contains(line, "time ran out"):
			ranOut++
		default:
			t.Errorf("unexpected spare line %q", line)
		}
	}
	if timedOut != 3 || ranOut != 1 || !strings.Contains(kept[3], "time ran out") {
		t.Errorf("timed out %d, time ran out %d, want 3 then 1:\n%s", timedOut, ranOut, out.String())
	}
	if runs := onReapRuns(t, runLog); len(runs) != 3 {
		t.Errorf("the command started %d times, want 3 (the fourth must not start)", len(runs))
	}
}

// TestRemovalGate_BackstopBudget is the criterion as written: no timeout
// declared, so each run gets the 10-second default, and four targets whose
// command sleeps 12 seconds. The first three runs are cut and keep their
// instances; the fourth doesn't start, since the 30-second budget is spent.
func TestRemovalGate_BackstopBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("takes 30 seconds")
	}
	root := setupHookWorkspace(t, true)
	backstopBudgetInstances(t, root)
	runLog := filepath.Join(t.TempDir(), "runs.log")
	appendBeforeRemoveConfig(t, root, fmt.Sprintf(`command = "echo run >> '%s'; sleep 12; exit 0"`, runLog))
	checkBackstopBudget(t, root, runLog, "10s", 33*time.Second)
}

// TestRemovalGate_BackstopBudgetScaled is the same rule at a scale fast
// enough for every run: a 3-second budget and a 1-second timeout.
func TestRemovalGate_BackstopBudgetScaled(t *testing.T) {
	prev := defaultRemovalGateBudget
	defaultRemovalGateBudget = 3 * time.Second
	t.Cleanup(func() { defaultRemovalGateBudget = prev })
	root := setupHookWorkspace(t, true)
	backstopBudgetInstances(t, root)
	runLog := filepath.Join(t.TempDir(), "runs.log")
	appendBeforeRemoveConfig(t, root, fmt.Sprintf("command = \"echo run >> '%s'; sleep 5; exit 0\"\ntimeout = \"1s\"", runLog))
	checkBackstopBudget(t, root, runLog, "1s", 6*time.Second)
}
