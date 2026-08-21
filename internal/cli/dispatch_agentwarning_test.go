package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// This file covers the two things niwa dispatch has to say before it spends
// anything: that --agent is not the flag that picks the launched agent, and
// that a worker launched at the instance root arrives with no workspace
// context. Both are only useful early, so every assertion here is about WHEN
// the line is printed as much as whether it is.

// dispatchCmdWithBuffers builds a cobra command whose streams are readable
// while the run is still in flight. runDispatchCmd returns strings after the
// fact, which cannot answer "had this printed yet when X happened".
func dispatchCmdWithBuffers() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	return cmd, &outBuf, &errBuf
}

// recordStderrAtLaunch replaces the launcher with one that snapshots stderr as
// it is called, so a test can tell a warning printed before the launch from one
// printed after it. Must be called AFTER installDispatchFakes.
func recordStderrAtLaunch(t *testing.T, errBuf *bytes.Buffer, into *string) {
	t.Helper()
	prev := dispatchLaunch
	dispatchLaunch = func(context.Context, launchRequest) error {
		*into = errBuf.String()
		return nil
	}
	t.Cleanup(func() { dispatchLaunch = prev })
}

// unorientedWarningFragment is the stable middle of the root-launch warning.
//
// The warning used to say a root-launched worker received nothing at all, and
// was triggered by row 2. Row 2 was corrected -- orientation does reach a
// session at the instance root -- so the sentence and its trigger both narrowed
// to what is still true: the project layer niwa writes inside repositories is
// not written at the root, and the skills, MCP servers and posture that ride it
// do not arrive. The fragment moved with it, which is the point of pinning a
// fragment rather than the whole line.
const unorientedWarningFragment = "none of the workspace's skills, MCP servers or posture"

// TestDispatch_AgentFlagNamingAnotherAgentWarnsBeforeTheLaunch is the defect:
// `niwa dispatch --agent codex` provisions an instance and launches a Claude
// worker with a subagent type no installation defines. The launch fails inside
// the worker, where the backgrounded path wires no stdout or stderr, so the
// developer gets an exit code and nothing else. The warning is the diagnosis,
// and it is worth nothing after the launch.
func TestDispatch_AgentFlagNamingAnotherAgentWarnsBeforeTheLaunch(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchDetach = true
	dispatchAgent = "codex"

	cmd, _, errBuf := dispatchCmdWithBuffers()
	var atLaunch string
	recordStderrAtLaunch(t, errBuf, &atLaunch)

	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	// Named three times over: what --agent is, what is actually launching, and
	// the flag that selects.
	for _, want := range []string{"subagent type", "claude", "--" + harnessFlagName + " codex"} {
		if !strings.Contains(atLaunch, want) {
			t.Fatalf("stderr at launch does not mention %q; --agent codex launched a claude worker with no warning.\nstderr at launch: %q", want, atLaunch)
		}
	}
}

// TestDispatch_AgentFlagAgreeingWithTheLaunchedAgentIsSilent holds the other
// half: --agent claude on a Claude dispatch is a subagent type that happens to
// share a name, and there is nothing to correct.
func TestDispatch_AgentFlagAgreeingWithTheLaunchedAgentIsSilent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchDetach = true
	dispatchAgent = "claude"

	cmd, _, errBuf := dispatchCmdWithBuffers()
	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errBuf.String(), "subagent type") {
		t.Fatalf("--agent claude on a claude dispatch warned about a mismatch that does not exist: %q", errBuf.String())
	}
}

// TestDispatch_AgentFlagThatNamesNoAgentIsSilent keeps the trigger narrow.
// Ordinary subagent types are the flag's whole purpose and must pass without
// comment.
func TestDispatch_AgentFlagThatNamesNoAgentIsSilent(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchDetach = true
	dispatchAgent = "code-reviewer"

	cmd, _, errBuf := dispatchCmdWithBuffers()
	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(errBuf.String(), "subagent type") {
		t.Fatalf("--agent code-reviewer, an ordinary subagent type, drew a mismatch warning: %q", errBuf.String())
	}
}

// TestDispatch_AgentFlagIsNeverARefusal pins the warning as a warning. A
// subagent type called after an agent is a legitimate thing to have, so the
// dispatch goes through: the instance is provisioned, the worker launches, and
// the flag is still forwarded.
func TestDispatch_AgentFlagIsNeverARefusal(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchDetach = true
	dispatchAgent = "codex"

	cmd, _, _ := dispatchCmdWithBuffers()
	if err := runDispatch(cmd, []string{"do a thing"}); err != nil {
		t.Fatalf("--agent codex was refused rather than warned about: %v", err)
	}
	if f.launchCalled != 1 {
		t.Fatalf("launch called %d times, want 1: the warning must not stop the dispatch", f.launchCalled)
	}
}

// TestDispatch_UnorientedWorkerWarningPrintsBeforeThePromptCapture is the
// timing defect. The warning is advice about a prompt that has not been written
// yet -- what the worker will be missing decides what the prompt has to carry
// -- so it has to arrive before the capture opens. Printed after the launch, it
// reaches a developer who has already written a prompt assuming a worker that
// had more than it did, and undoing that means hunting a detached process by
// hand.
func TestDispatch_UnorientedWorkerWarningPrintsBeforeThePromptCapture(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	t.Setenv("NIWA_DISPATCH_HARNESS", "")
	dispatchHarness = "codex"
	dispatchDetach = true
	stubCaptureTTY(t, true, true)

	cmd, _, errBuf := dispatchCmdWithBuffers()
	var atCapture string
	stubCapture(t, func() (string, error) {
		atCapture = errBuf.String()
		return "do a thing", nil
	})

	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if !strings.Contains(errBuf.String(), unorientedWarningFragment) {
		t.Fatalf("the unoriented-worker warning never printed for a codex dispatch: %q", errBuf.String())
	}
	if !strings.Contains(atCapture, unorientedWarningFragment) {
		t.Fatalf("the prompt capture opened before the unoriented-worker warning printed, so the developer writes the prompt without it.\nstderr when the capture opened: %q", atCapture)
	}
}
