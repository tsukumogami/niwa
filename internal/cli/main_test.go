package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// agentStubBinaries are the worker binaries a dispatch can launch. TestMain
// puts a stub for each one first on PATH, so a test that reaches the real
// launcher by mistake runs the stub instead of starting a live session.
//
// Without this, the mistake is silent on a developer machine: a background
// `claude --bg` succeeds, registers in the developer's agent view with a
// working directory the test framework then deletes, and stays there until
// someone removes it by hand. CI never notices, because CI has no claude.
var agentStubBinaries = []string{"claude", "codex"}

func TestMain(m *testing.M) {
	os.Exit(runWithAgentStubs(m))
}

// runWithAgentStubs runs the package's tests with the agent binaries stubbed
// and fails the run if any stub was invoked. A stub exits non-zero, so the
// test that reached it usually fails on its own; the invocation log is what
// catches a test that tolerates the error, which is how the original leak
// went unnoticed.
//
// Tests that set PATH themselves -- emptying it, or prepending their own fake
// -- are unaffected: t.Setenv replaces the value set here for that test only.
func runWithAgentStubs(m *testing.M) int {
	dir, err := os.MkdirTemp("", "niwa-cli-agent-stubs-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating the agent stub directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	// A bare `--version` is a probe, not a launch: workspace.SupportsWorktreeHooks
	// runs it and treats a failure as "supported". The stub fails it without
	// logging, which is exactly what those callers see in CI, where no agent is
	// installed.
	logPath := filepath.Join(dir, "invocations.log")
	for _, name := range agentStubBinaries {
		script := fmt.Sprintf("#!/bin/sh\n"+
			"[ \"$*\" = --version ] && exit 1\n"+
			"printf '%%s %%s\\n' %q \"$*\" >> %q\n"+
			"echo %q >&2\n"+
			"exit 1\n",
			name, logPath, name+": stubbed by internal/cli TestMain; unit tests must not launch a real agent")
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "writing the %s stub: %v\n", name, err)
			return 1
		}
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintf(os.Stderr, "prepending the agent stubs to PATH: %v\n", err)
		return 1
	}

	code := m.Run()

	if log, err := os.ReadFile(logPath); err == nil && len(log) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: a test in this package ran an agent binary instead of a fake.\n"+
			"On a machine with that agent installed it would have started a real session.\n"+
			"Stub the launcher (dispatchLaunch) or empty PATH in the test. Invocations:\n%s", log)
		return 1
	}
	return code
}
