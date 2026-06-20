package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"
)

// worktree_delegation_steps_test.go holds step definitions for the
// worktree-delegation integration (DESIGN-niwa-default-worktree). The runtime
// steps drive `niwa worktree from-hook` with synthetic Claude hook JSON on
// stdin (exercising the end-to-end create/remove path WITHOUT a real Claude);
// the install steps make the apply-time `claude --version` harness probe
// deterministic by putting a FAKE `claude` on PATH.

// aFakeClaudeOnPATH writes a tiny executable `claude` script into a
// scenario-local bin dir and prepends that dir to PATH for every subsequent
// niwa subprocess (via testState.pathPrefix). The script prints the supplied
// version in the `claude --version` format ("<version> (Claude Code)") so the
// harness probe parses it. This is the deterministic seam for the supported
// vs unsupported branches: the probe shells out to `claude --version`, and a
// fake on PATH controls exactly what it sees, without weakening production
// behavior.
func aFakeClaudeOnPATH(ctx context.Context, version string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	binDir := filepath.Join(s.homeDir, "fake-claude-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return ctx, fmt.Errorf("mkdir fake-claude-bin: %w", err)
	}
	// Only respond to `--version`; any other invocation exits non-zero so a
	// stray real-claude code path in a test fails loudly rather than silently
	// hitting the network.
	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then
  echo "%s (Claude Code)"
  exit 0
fi
echo "fake claude: unsupported invocation: $*" >&2
exit 1
`, version)
	scriptPath := filepath.Join(binDir, "claude")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		return ctx, fmt.Errorf("writing fake claude script: %w", err)
	}
	s.pathPrefix = binDir
	return ctx, nil
}

// aWorktreeDelegationOptOutWorkspace mirrors iSetUpSingleRepoChanneledWorkspace
// (one bare source repo "app" under group "apps" plus a config repo) but inits
// the instance with `--no-worktree-delegation`, so the whole integration block
// is skipped at apply time regardless of the harness version.
func aWorktreeDelegationOptOutWorkspace(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, err := s.gitServer.SourceRepo("app")
	if err != nil {
		return ctx, fmt.Errorf("creating source repo %q: %w", "app", err)
	}
	s.repoURLs["app"] = url

	body := fmt.Sprintf(`[workspace]
name = %q

[groups.apps]

[repos.app]
url = %q
group = "apps"
`, name, url)
	cfgURL, err := s.gitServer.ConfigRepo(name, body)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = cfgURL
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+cfgURL+" --no-worktree-delegation"); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init --no-worktree-delegation exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iPipeWorktreeCreateHook simulates Claude firing the WorktreeCreate hook: it
// builds the synthetic hook JSON ({"hook_event_name":"WorktreeCreate","cwd":
// "<repo path>","name":"<name>","session_id":"x"}) with cwd pointing at the
// repo inside the instance, and pipes it to `niwa worktree from-hook` on stdin.
// from-hook prints ONLY the absolute worktree path to stdout, which is recorded
// for later existence assertions.
func iPipeWorktreeCreateHook(ctx context.Context, groupRepo, name, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	repoPath := filepath.Join(s.workspaceRoot, instance, groupRepo)
	payload := fmt.Sprintf(
		`{"hook_event_name":"WorktreeCreate","cwd":%q,"name":%q,"session_id":"x"}`,
		repoPath, name)
	if err := runNiwaWithStdin(s, repoPath, "niwa worktree from-hook", payload); err != nil {
		return ctx, fmt.Errorf("niwa worktree from-hook (create): %w", err)
	}
	// On success from-hook prints the bare worktree path; record it (trimmed)
	// regardless of exit so a failing scenario surfaces the captured output.
	s.printedWorktreePath = strings.TrimSpace(s.stdout)
	return ctx, nil
}

// iPipeWorktreeRemoveHook simulates Claude firing the WorktreeRemove hook for
// the worktree path printed by a preceding create step. It pipes the synthetic
// remove JSON (carrying worktree_path) to from-hook on stdin. WorktreeRemove is
// non-blocking, so from-hook always exits 0.
func iPipeWorktreeRemoveHook(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return ctx, fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	payload := fmt.Sprintf(
		`{"hook_event_name":"WorktreeRemove","worktree_path":%q,"session_id":"x"}`,
		s.printedWorktreePath)
	if err := runNiwaWithStdin(s, instRoot, "niwa worktree from-hook", payload); err != nil {
		return ctx, fmt.Errorf("niwa worktree from-hook (remove): %w", err)
	}
	return ctx, nil
}

// runNiwaWithStdin is runNiwa plus a stdin payload. It executes the test binary
// with the given args from cwd, feeding stdin, and records stdout/stderr/exit
// code into state. Used to drive `niwa worktree from-hook`, whose entire
// contract is reading the Claude hook JSON on stdin.
func runNiwaWithStdin(s *testState, cwd, command, stdin string) error {
	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = cwd
	cmd.Env = s.buildEnv()
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	s.stdout = stdout.String()
	s.stderr = stderr.String()
	s.shellPwd = ""
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return nil
		}
		return fmt.Errorf("command execution failed: %w", err)
	}
	s.exitCode = 0
	return nil
}

// iCommitAllChangesInPrintedWorktree stages and commits everything in the
// worktree printed by the create hook (including the niwa scaffolding git
// reports as untracked), leaving the worktree clean. This lets a following
// WorktreeRemove exercise the design's CLEAN-removal path (clean -> ended)
// rather than the dirty log-and-retain path — without forcing the teardown
// past the dirty guard, which production never does.
func iCommitAllChangesInPrintedWorktree(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first")
	}
	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=niwa-test",
		"GIT_AUTHOR_EMAIL=niwa-test@example.com",
		"GIT_COMMITTER_NAME=niwa-test",
		"GIT_COMMITTER_EMAIL=niwa-test@example.com",
	)
	for _, args := range [][]string{
		{"add", "-A"},
		{"commit", "-m", "commit delegated worktree scaffolding"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = s.printedWorktreePath
		cmd.Env = gitEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("git %v in worktree %s: %w\n%s", args, s.printedWorktreePath, err, out)
		}
	}
	return nil
}

// thePrintedWorktreePathExists asserts the worktree path printed by the last
// WorktreeCreate hook dispatch exists on disk (a real niwa worktree was
// created).
func thePrintedWorktreePathExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return fmt.Errorf("no printed worktree path stored; run a WorktreeCreate hook first\nstdout:\n%s\nstderr:\n%s", s.stdout, s.stderr)
	}
	fi, err := os.Stat(s.printedWorktreePath)
	if err != nil {
		return fmt.Errorf("printed worktree path %q does not exist: %w\nstderr:\n%s", s.printedWorktreePath, err, s.stderr)
	}
	if !fi.IsDir() {
		return fmt.Errorf("printed worktree path %q is not a directory", s.printedWorktreePath)
	}
	return nil
}

// thePrintedWorktreePathDoesNotExist asserts the worktree path printed earlier
// has been removed (a clean WorktreeRemove tore the worktree down).
func thePrintedWorktreePathDoesNotExist(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.printedWorktreePath == "" {
		return fmt.Errorf("no printed worktree path stored")
	}
	if _, err := os.Stat(s.printedWorktreePath); err == nil {
		return fmt.Errorf("printed worktree path %q still exists after WorktreeRemove", s.printedWorktreePath)
	}
	return nil
}

// registerWorktreeDelegationSteps wires the worktree-delegation steps into the
// scenario context. Called from initializeScenario.
func registerWorktreeDelegationSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^a fake claude reporting version "([^"]*)" is on PATH$`, aFakeClaudeOnPATH)
	ctx.Step(`^a worktree-delegation opt-out workspace "([^"]*)" exists$`, aWorktreeDelegationOptOutWorkspace)
	ctx.Step(`^I pipe a WorktreeCreate hook for repo "([^"]*)" with name "([^"]*)" in instance "([^"]*)"$`, iPipeWorktreeCreateHook)
	ctx.Step(`^I pipe a WorktreeRemove hook for the printed worktree path in instance "([^"]*)"$`, iPipeWorktreeRemoveHook)
	ctx.Step(`^I commit all changes in the printed worktree$`, iCommitAllChangesInPrintedWorktree)
	ctx.Step(`^the printed worktree path exists$`, thePrintedWorktreePathExists)
	ctx.Step(`^the printed worktree path does not exist$`, thePrintedWorktreePathDoesNotExist)
}
