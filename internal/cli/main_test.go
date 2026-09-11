package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
)

// agentStubScript stands in for every agent binary a dispatch can launch. It
// appends its name, arguments, and working directory -- a test's t.TempDir(),
// which names the test -- to a log beside itself, then fails without starting
// anything. It locates the log through $0 rather than having a path spliced in,
// so no temp-dir name needs shell quoting.
//
// A bare `--version` is a probe, not a launch: workspace.SupportsWorktreeHooks
// runs `claude --version` and treats a failure as "supported". Every stub fails
// that probe without logging it, which is exactly what those callers see in
// CI, where no agent is installed. A real launch always carries a prompt, so it
// can never match the exemption.
const agentStubScript = `#!/bin/sh
[ "$*" = --version ] && exit 1
printf '%s %s (cwd %s)\n' "$(basename "$0")" "$*" "$PWD" >> "$(dirname "$0")/invocations.log"
echo "$(basename "$0"): stubbed by internal/cli TestMain; unit tests must not launch a real agent" >&2
exit 1
`

// TestMain puts a stub first on PATH for every agent binary a dispatch can
// launch, so a test that reaches the real launcher by mistake runs the stub
// instead of starting a live session.
//
// Without this, the mistake is silent on a developer machine: a background
// `claude --bg` succeeds, registers in the developer's agent view with a
// working directory the test framework then deletes, and stays there until
// someone removes it by hand. CI never notices, because CI has no agent
// installed.
func TestMain(m *testing.M) {
	os.Exit(runWithAgentStubs(m))
}

// runWithAgentStubs runs the package's tests with the agent binaries stubbed
// and fails the run if any stub was invoked.
//
// The invocation log, not the stub's exit status, is the signal. Only a
// backgrounded launch waits for the process and reports its exit; a foreground
// launch does not treat a non-zero exit as an error, a detached one never
// waits, and a test can discard the error anyway, which is how the original
// leak went unnoticed. A detached launch's log write can still land after this
// function reads the log, so detection of that one mode is best-effort; the
// stub keeps it from reaching a real agent either way.
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

	for _, name := range launchedAgentBinaries() {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(agentStubScript), 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "writing the %s stub: %v\n", name, err)
			return 1
		}
	}
	if err := os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		fmt.Fprintf(os.Stderr, "prepending the agent stubs to PATH: %v\n", err)
		return 1
	}

	code := m.Run()

	if log, err := os.ReadFile(filepath.Join(dir, "invocations.log")); err == nil && len(log) > 0 {
		fmt.Fprintf(os.Stderr, "FAIL: a test in this package ran an agent binary instead of a fake.\n"+
			"On a machine with that agent installed it would have started a real session.\n"+
			"Stub the launcher (dispatchLaunch) or empty PATH in the test. Invocations:\n%s", log)
		return 1
	}
	return code
}

// launchedAgentBinaries reads the binary names from the launch specs dispatch
// itself uses, so an agent that gains a launch spec is stubbed without anyone
// remembering to add it here. TestMain runs before any test can swap the
// dispatchLaunchSpec seam, so this sees the production table.
func launchedAgentBinaries() []string {
	var names []string
	for _, ag := range agent.All() {
		if spec, ok := dispatchLaunchSpec(ag); ok {
			names = append(names, spec.Binary)
		}
	}
	return names
}
