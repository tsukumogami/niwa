package cli

import (
	"os"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// setupTerminalAvailable reports whether this process may tell setup scripts
// that a terminal is available. Both stdin and stderr must be terminals: a
// script that prompts reads its answer from the terminal and draws on it, and
// niwa's own progress output on stderr shares that screen. Stdout is not
// consulted.
//
// It reads IsStdinTTY and IsStderrTTY so tests can stub the answer.
func setupTerminalAvailable() bool {
	return IsStdinTTY() && IsStderrTTY()
}

// newInteractiveApplier builds the Applier for the commands a person runs
// directly at a terminal -- `niwa apply` and `niwa create`. Its Reporter
// animates progress when stderr is a terminal and --no-progress is unset, and
// its setup scripts are told about the terminal when setupTerminalAvailable
// says one is there.
func newInteractiveApplier(gh github.Client) *workspace.Applier {
	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporterWithTTY(os.Stderr, !noProgress && IsStderrTTY())
	applier.SetupTerminal = setupTerminalAvailable()
	return applier
}

// newProvisionApplier builds the Applier for provisioning an instance with no
// person attached: the SessionStart hook, dispatch, watch and reap all come
// through realProvisionInstance. SetupTerminal stays false regardless of what
// the descriptors look like. A hook's stdin is a protocol stream, and a
// dispatched worker may inherit a terminal it must not block on, so a setup
// script there must never be invited to prompt.
func newProvisionApplier(gh github.Client) *workspace.Applier {
	applier := workspace.NewApplier(gh)
	applier.Reporter = workspace.NewReporter(os.Stderr)
	return applier
}
