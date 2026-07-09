package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// dispatchInstanceNameRe mirrors the CLI's isDispatchInstanceName signature: a
// "+" (end-of-config marker), an optional dash-free slug, a "-", then 8 lowercase
// hex digits at the end. It matches "<config>+-<8hex>" (no-name) and
// "<config>+<slug>-<8hex>" (named); there is no "disp" literal.
var dispatchInstanceNameRe = regexp.MustCompile(`\+[a-z0-9_]*-[0-9a-f]{8}$`)

// dispatch_steps_test.go holds step definitions for the `niwa dispatch`
// lifecycle integration (DESIGN-instance-dispatch, PLAN Issue 6). The runtime
// steps drive the REAL niwa binary's dispatch and reap commands offline against
// the localGitServer, with a FAKE `claude` on PATH that simulates what dispatch
// needs WITHOUT a real claude, daemon, or network.
//
// The fake claude's `--bg` invocation writes the Claude Code job state that the
// capture path correlates by cwd: $HOME/.claude/jobs/<short>/state.json carrying
// the chosen full UUID and cwd == the launch cwd. dispatch sets cmd.Dir to the
// instance dir, so the fake (which runs with that cwd) records the instance dir
// as cwd, and dispatch_capture.go matches it. HOME is sandboxed, so the jobs dir
// is hermetic.

// dispatchFakeClaudeScript is the fake claude that the dispatch scenarios put on
// PATH. behaviour selects the --bg outcome:
//   - "ok": --bg writes a live job state for $FAKE_CLAUDE_SESSION_ID and exits 0
//     (the success path);
//   - "launch-fail": --bg exits non-zero, writing nothing (the induced launch
//     failure that must roll the instance back).
//
// attach/logs exit 0 (dispatch only calls attach without --detach; the scenarios
// pass --detach, so attach is never reached, but the fake handles it for
// completeness). stop rewrites the job state to a terminal "done" (the shape a
// real `claude stop` produces); note that under delete-only teardown a terminal
// state keeps the instance -- only deleting the session (its job entry gone)
// makes a later reap reclaim it. Any other invocation exits non-zero so a stray
// real code path fails loudly rather than silently hitting the network.
func dispatchFakeClaudeScript(behaviour string) string {
	bg := `  sid="${FAKE_CLAUDE_SESSION_ID:-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa}"
  short=$(printf '%s' "$sid" | cut -c1-8)
  jobdir="$HOME/.claude/jobs/$short"
  mkdir -p "$jobdir"
  printf '%s\n' "$*" > "$HOME/dispatch-launch-argv"
  cwd=$(pwd)
  printf '{"sessionId":"%s","template":"bg","state":"running","cwd":"%s"}\n' "$sid" "$cwd" > "$jobdir/state.json"
  printf 'backgrounded · %s\n' "$short"
  exit 0`
	if behaviour == "launch-fail" {
		bg = `  echo "fake claude: induced launch failure" >&2
  exit 1`
	}
	return fmt.Sprintf(`#!/bin/sh
case "$1" in
  --bg)
%s
    ;;
  attach|logs)
    exit 0
    ;;
  stop)
    sid="$2"
    short=$(printf '%%s' "$sid" | cut -c1-8)
    jobdir="$HOME/.claude/jobs/$short"
    if [ -f "$jobdir/state.json" ]; then
      printf '{"sessionId":"%%s","template":"bg","state":"done","cwd":""}\n' "$sid" > "$jobdir/state.json"
    fi
    exit 0
    ;;
  *)
    echo "fake claude: unsupported invocation: $*" >&2
    exit 1
    ;;
esac
`, bg)
}

// installDispatchFakeClaude writes the fake claude with the given behaviour into
// a scenario-local bin dir and prepends it to PATH for every subsequent niwa
// subprocess via testState.pathPrefix.
func installDispatchFakeClaude(s *testState, behaviour string) error {
	binDir := filepath.Join(s.homeDir, "fake-claude-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("mkdir fake-claude-bin: %w", err)
	}
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte(dispatchFakeClaudeScript(behaviour)), 0o755); err != nil {
		return fmt.Errorf("writing fake claude script: %w", err)
	}
	s.pathPrefix = binDir
	return nil
}

// aFakeClaudeForDispatchWithSession installs the success-path fake claude and
// pins the UUID it will record, so the scenario can assert the mapping is keyed
// on exactly that id.
func aFakeClaudeForDispatchWithSession(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := installDispatchFakeClaude(s, "ok"); err != nil {
		return ctx, err
	}
	s.envOverrides["FAKE_CLAUDE_SESSION_ID"] = sessionID
	return ctx, nil
}

// aFakeClaudeForDispatchThatFailsToLaunch installs the launch-fail fake claude,
// whose --bg exits non-zero so dispatch's deferred self-rollback fires.
func aFakeClaudeForDispatchThatFailsToLaunch(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	return ctx, installDispatchFakeClaude(s, "launch-fail")
}

// iRunCommandFromTheWorkspaceRoot runs the given niwa command from the workspace
// root (where ClassifyCwd resolves the enclosing workspace) and, when it is a
// dispatch, records the dispatch instance directory it created.
func iRunCommandFromTheWorkspaceRoot(ctx context.Context, command string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if err := runNiwa(s, s.workspaceRoot, command); err != nil {
		return ctx, err
	}
	if strings.Contains(command, "dispatch") {
		s.lastDispatchInstancePath = findDispatchInstance(s.workspaceRoot)
	}
	return ctx, nil
}

// findDispatchInstance returns the absolute path of the single dispatch instance
// under workspaceRoot, or "" when none exists. The dispatch instance name is
// "<config>+-<8 hex>" (no-name) or "<config>+<slug>-<8 hex>" (named), which the
// structural dispatchInstanceNameRe uniquely identifies.
func findDispatchInstance(workspaceRoot string) string {
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && dispatchInstanceNameRe.MatchString(e.Name()) {
			return filepath.Join(workspaceRoot, e.Name())
		}
	}
	return ""
}

// aDispatchInstanceWasCreatedWithAWellFormedInstanceFile asserts a dispatch
// instance directory exists and carries a parseable .niwa/instance.json.
func aDispatchInstanceWasCreatedWithAWellFormedInstanceFile(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return fmt.Errorf("no dispatch instance found under %s\nstdout:\n%s\nstderr:\n%s", s.workspaceRoot, s.stdout, s.stderr)
	}
	statePath := filepath.Join(inst, ".niwa", "instance.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("reading instance state %s: %w", statePath, err)
	}
	var js map[string]any
	if err := json.Unmarshal(data, &js); err != nil {
		return fmt.Errorf("instance state %s is not well-formed JSON: %w", statePath, err)
	}
	s.lastDispatchInstancePath = inst
	return nil
}

// theDispatchInstanceStillExists asserts the recorded dispatch instance directory is
// still on disk (the reaper spared it).
func theDispatchInstanceStillExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastDispatchInstancePath == "" {
		return fmt.Errorf("no dispatch instance path recorded")
	}
	if _, err := os.Stat(s.lastDispatchInstancePath); err != nil {
		return fmt.Errorf("dispatch instance %s should still exist: %w", s.lastDispatchInstancePath, err)
	}
	return nil
}

// noDispatchInstanceRemains asserts there is no dispatch instance under the
// workspace root (rollback or reclamation removed it).
func noDispatchInstanceRemains(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if inst := findDispatchInstance(s.workspaceRoot); inst != "" {
		return fmt.Errorf("a dispatch instance still exists at %s; expected none\nstdout:\n%s\nstderr:\n%s", inst, s.stdout, s.stderr)
	}
	return nil
}

// aDispatchOriginMappingExistsForSession asserts the mapping at
// .niwa/sessions/<sid>.json exists and records ephemeral:true, origin:"dispatch".
func aDispatchOriginMappingExistsForSession(ctx context.Context, sessionID string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("expected dispatch mapping at %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	var m struct {
		Ephemeral bool   `json:"ephemeral"`
		Origin    string `json:"origin"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parsing mapping %s: %w", path, err)
	}
	if !m.Ephemeral {
		return fmt.Errorf("mapping %s is not ephemeral; want ephemeral:true", path)
	}
	if m.Origin != "dispatch" {
		return fmt.Errorf("mapping %s origin = %q; want \"dispatch\"", path, m.Origin)
	}
	return nil
}

// noDispatchOriginMappingRemains asserts the sessions store holds no mapping at
// all (rollback removed it, or none was ever written).
func noDispatchOriginMappingRemains(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading sessions dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			return fmt.Errorf("a session mapping still exists at %s; expected none", filepath.Join(dir, e.Name()))
		}
	}
	return nil
}

// theDispatchSessionIsDeleted removes the session's Claude Code job entry under
// $HOME/.claude/jobs/<short>/, the shape produced when the developer deletes the
// session from the Agent View. A gone job entry is the reaper's liveness signal
// (DESIGN Decision 6, revised -- delete-only teardown): the reaper then reads
// the session as dead and reclaims its mapped instance. A session that merely
// finished a task or went idle keeps its entry and is spared.
func theDispatchSessionIsDeleted(ctx context.Context, sessionID string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	jobDir := filepath.Join(s.homeDir, ".claude", "jobs", short)
	if err := os.RemoveAll(jobDir); err != nil {
		return ctx, fmt.Errorf("removing job-state dir %q: %w", jobDir, err)
	}
	return ctx, nil
}

// theDispatchOriginMappingIsRemoved deletes every session mapping under the
// workspace root's .niwa/sessions store, modeling a dispatch instance whose
// mapping was lost while its worker keeps running -- the unmapped-but-live shape
// the reaper backstop must not reclaim. The live job entry the fake claude wrote
// is left in place, so a session is still rooted in the instance.
func theDispatchOriginMappingIsRemoved(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return ctx, nil
		}
		return ctx, fmt.Errorf("reading sessions dir %s: %w", dir, err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return ctx, fmt.Errorf("removing mapping %s: %w", e.Name(), err)
			}
		}
	}
	return ctx, nil
}

// theDispatchInstanceIsAgedPastTheBackstopTTL backdates the recorded dispatch
// instance directory's mtime well past the name+TTL backstop window (30 minutes;
// two hours here for a generous margin) so the backstop considers it old enough
// to reclaim. A successful dispatch already removed the pending-marker, so the
// backstop ages the instance by directory mtime; the marker is also removed here
// for determinism. Combined with a removed mapping and a still-present live job,
// this is the exact shape that must be SPARED by the liveness guard.
func theDispatchInstanceIsAgedPastTheBackstopTTL(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return ctx, fmt.Errorf("no dispatch instance recorded to age")
	}
	_ = os.Remove(filepath.Join(inst, ".niwa", "dispatch-pending"))
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(inst, old, old); err != nil {
		return ctx, fmt.Errorf("backdating instance mtime %s: %w", inst, err)
	}
	return ctx, nil
}

// theLaunchedClaudeWasInvokedWith asserts that the argv the fake claude recorded
// on its --bg launch contains the given substring (e.g. "--model opus"). The
// success-path fake writes its full argument line to $HOME/dispatch-launch-argv.
func theLaunchedClaudeWasInvokedWith(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.homeDir, "dispatch-launch-argv")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading launched claude argv %s: %w\nstdout:\n%s\nstderr:\n%s", path, err, s.stdout, s.stderr)
	}
	if !strings.Contains(string(data), want) {
		return fmt.Errorf("launched claude argv %q does not contain %q", strings.TrimSpace(string(data)), want)
	}
	return nil
}

// registerDispatchSteps wires the dispatch-lifecycle steps into the scenario
// context. Called from initializeScenario.
func registerDispatchSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the launched claude was invoked with "([^"]*)"$`, theLaunchedClaudeWasInvokedWith)
	ctx.Step(`^a fake claude for dispatch with session "([^"]*)"$`, aFakeClaudeForDispatchWithSession)
	ctx.Step(`^a fake claude for dispatch that fails to launch$`, aFakeClaudeForDispatchThatFailsToLaunch)
	ctx.Step(`^I run "([^"]*)" from the workspace root$`, iRunCommandFromTheWorkspaceRoot)
	ctx.Step(`^a dispatch instance was created with a well-formed instance file$`, aDispatchInstanceWasCreatedWithAWellFormedInstanceFile)
	ctx.Step(`^the dispatch instance still exists$`, theDispatchInstanceStillExists)
	ctx.Step(`^no dispatch instance remains$`, noDispatchInstanceRemains)
	ctx.Step(`^a dispatch-origin mapping exists for session "([^"]*)"$`, aDispatchOriginMappingExistsForSession)
	ctx.Step(`^no dispatch-origin mapping remains$`, noDispatchOriginMappingRemains)
	ctx.Step(`^the dispatch session "([^"]*)" is deleted from the Agent View$`, theDispatchSessionIsDeleted)
	ctx.Step(`^the dispatch-origin mapping is removed$`, theDispatchOriginMappingIsRemoved)
	ctx.Step(`^the dispatch instance is aged past the backstop TTL$`, theDispatchInstanceIsAgedPastTheBackstopTTL)
}
