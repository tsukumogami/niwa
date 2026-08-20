package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// captureLaunchMode replaces the launch seam with one that records the process
// model the command asked for, so a test can see the decision rather than its
// consequences.
func captureLaunchMode(t *testing.T, into *agentplan.LaunchMode) {
	t.Helper()
	prev := dispatchLaunch
	dispatchLaunch = func(_ context.Context, req launchRequest) error {
		*into = req.Mode
		return nil
	}
	t.Cleanup(func() { dispatchLaunch = prev })
}

// substituteLaunchSpec points the command's declaration lookup at one spec,
// whatever agent it resolves, so a test can vary a single declared property
// while everything else about the run stays real.
func substituteLaunchSpec(t *testing.T, spec agentplan.LaunchSpec) {
	t.Helper()
	prev := dispatchLaunchSpec
	dispatchLaunchSpec = func(agent.Agent) (agentplan.LaunchSpec, bool) { return spec, true }
	t.Cleanup(func() { dispatchLaunchSpec = prev })
}

// TestDispatchResolvesTheLaunchModeFromTheRunnerAndTheFlag is the command-level
// half of the defect this work fixes, and the one that fails if the decision
// ever moves back inside the declaration.
//
// The process model used to be read off the launch description alone.
// `realDispatchLaunch` switched on it and nothing else, so --detach could not
// reach the decision: it was wired to whether an attach step ran afterwards.
// A worker whose runner executes its turn in the foreground was therefore
// detached whether or not the developer asked for that.
//
// The assertion is about movement, not constants. For a runner that offers two
// process models the flag has to change the answer; for one that offers a
// single model it must not, because there is nothing to override it into.
func TestDispatchResolvesTheLaunchModeFromTheRunnerAndTheFlag(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}

	for _, tc := range []struct {
		name   string
		runner agentplan.RunnerKind
		detach bool
		want   agentplan.LaunchMode
	}{
		{"foreground runner, no flag", agentplan.RunnerForeground, false, agentplan.LaunchForeground},
		{"foreground runner, detached", agentplan.RunnerForeground, true, agentplan.LaunchDetached},
		{"self-backgrounding runner, no flag", agentplan.RunnerSelfBackgrounding, false, agentplan.LaunchBackgrounded},
		{"self-backgrounding runner, detached", agentplan.RunnerSelfBackgrounding, true, agentplan.LaunchBackgrounded},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)

			spec := base
			spec.Runner = tc.runner
			// So a run that reaches the end does not try to open a session
			// through a fake; which mode was asked for is this test's subject.
			spec.ResumeDuringTurn = true
			substituteLaunchSpec(t, spec)

			var got agentplan.LaunchMode
			captureLaunchMode(t, &got)
			dispatchDetach = tc.detach
			prevAttach := dispatchAttach
			dispatchAttach = func(agentplan.LaunchSpec, string, string) error { return nil }
			t.Cleanup(func() { dispatchAttach = prevAttach })

			if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if got != tc.want {
				t.Errorf("launch mode = %d, want %d; the model is resolved from the runner AND --detach, never from the declaration alone", got, tc.want)
			}
		})
	}
}

// TestDispatchDoesNotApologizeForATurnTheDeveloperWatched covers the surface
// that stops being true the moment a foreground run is possible.
//
// The "will not open a session while its turn is still running" line exists
// because dispatch's last step used to be an attach on a worker that had just
// started. On a plain dispatch of a foreground runner there is no such worker:
// the launch waited, the turn is over, and the developer watched it. Printing
// an apology there tells them niwa could not do the thing it just did.
//
// It stays correct and reachable in the case it was written for -- a worker
// that is still running and an agent that will not hand its session over --
// which is what a detached launch of the same agent produces.
func TestDispatchDoesNotApologizeForATurnTheDeveloperWatched(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	spec := base
	spec.Runner = agentplan.RunnerForeground
	spec.ResumeDuringTurn = false

	// Plain dispatch: the turn ran here and ended.
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	substituteLaunchSpec(t, spec)

	attached := false
	prevAttach := dispatchAttach
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		attached = true
		return nil
	}
	t.Cleanup(func() { dispatchAttach = prevAttach })

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if strings.Contains(stderr, "still running") {
		t.Errorf("a dispatch whose turn ran in this terminal apologized for not opening the session:\n%s", stderr)
	}
	if !strings.Contains(stderr, "turn ended") {
		t.Errorf("a foreground dispatch said nothing about the turn ending, which is the one thing it can honestly report:\n%s", stderr)
	}
	if attached {
		t.Error("a foreground dispatch opened a session afterwards; the run the developer watched is the way in, not a step before one")
	}

	// Detached: the worker is going, and this agent will not hand over a
	// session whose turn is still running. The line is exactly right there.
	root = setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	installDispatchFakes(t, root)
	substituteLaunchSpec(t, spec)
	dispatchAttach = func(agentplan.LaunchSpec, string, string) error {
		attached = true
		return nil
	}
	dispatchDetach = true

	_, stderr, err = runDispatchCmd(t, "do another thing")
	if err != nil {
		t.Fatalf("dispatch --detach: %v", err)
	}
	if !strings.Contains(stderr, "still running") {
		t.Errorf("a detached worker its agent will not hand over left the developer nothing to explain a resume that refuses:\n%s", stderr)
	}
}
