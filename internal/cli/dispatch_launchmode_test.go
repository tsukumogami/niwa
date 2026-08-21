package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// TestDispatchForegroundCaptureFailureKeepsTheWork is the data-safety half of
// the process-model change, and the case every existing rollback test misses
// because they all exercise the detached shape.
//
// The rollback is armed before the launch and disarmed after the mapping is
// durable, so a capture failure destroys the instance. Detached that is right:
// the worker started moments ago, the directory holds its logs and nothing
// else, and cleaning up costs a diagnostic. Foreground the launch WAITED for
// the turn to end, so everything the worker produced is in that directory. A
// run that did real work and then yielded no discoverable session record --
// a refused prompt, a crash after writing files, a record the scanner cannot
// match -- would have its output deleted by a rollback armed for the case
// where there was none.
//
// The same function already reasons this way about a non-zero exit and
// declines to roll back work that may have happened. Without this test one arm
// protected the work and the other deleted it.
func TestDispatchForegroundCaptureFailureKeepsTheWork(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	spec := base
	spec.Runner = agentplan.RunnerForeground

	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	substituteLaunchSpec(t, spec)
	dispatchDetach = false

	prevCapture := dispatchCapture
	dispatchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return "", "", errors.New("capture timeout")
	}
	t.Cleanup(func() { dispatchCapture = prevCapture })

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("a foreground turn that ran is not a failed dispatch: %v", err)
	}
	if f.destroyCalled != 0 {
		t.Errorf("destroy called %d times; a foreground capture failure must not delete a directory holding a completed turn's work", f.destroyCalled)
	}
	if !strings.Contains(stderr, "nothing to resume") {
		t.Errorf("the developer was not told why there is no session to resume:\n%s", stderr)
	}

	// The retain marker, asserted here rather than left to the reaper's own
	// test. Disarming the rollback only keeps this directory for the length of
	// the command; reapBackstop runs out-of-process at the top of the next
	// dispatch, create or watch, and its eligibility signal is the directory
	// NAME. This instance is dispatch-named and unmapped, and the capture timed
	// out -- which is precisely the condition under which no session record
	// names this cwd, so nothing spares it. Thirty minutes later the work is
	// gone.
	//
	// Without this line the marker's write can be deleted and every assertion
	// above still passes: destroy is not called during this command, and the
	// message is still printed. The reaper's test proves it HONORS a marker,
	// not that the dispatch writes one, so the two halves were held together by
	// a comment and nothing else.
	if _, marked := dispatchRetainReason(f.instancePath); !marked {
		t.Error("no retain marker was written, so the next sweep will delete the work this dispatch said it was keeping")
	}
}

// TestDispatchForegroundMappingFailureKeepsTheWork is the second error exit
// below the point where the foreground path disarms its rollback, and it needs
// its own test for the same reason the first one did.
//
// Once `success` is true, every later `return` leaves the instance on disk. Two
// of them can happen before a mapping exists: the capture failing, and the
// mapping write itself failing. An instance with no mapping is dispatch-named
// and unmapped, which is the reaper backstop's target, so both have to leave the
// retain marker behind or the work is deleted half an hour later by an
// unrelated command.
//
// The capture branch got the marker first and this one was added afterwards,
// without a test -- so disabling it left the whole package green. The existing
// rollback test for a mapping-write failure drives the *backgrounded* path,
// where the rollback is still armed and this branch never runs.
//
// The failure is forced the way that test forces it: capture returns an id that
// is not a UUID, which WriteSessionMapping rejects without writing.
func TestDispatchForegroundMappingFailureKeepsTheWork(t *testing.T) {
	base, ok := agentplan.For(agent.AgentClaude).LaunchSpec()
	if !ok {
		t.Fatal("no launch spec for the default agent")
	}
	spec := base
	spec.Runner = agentplan.RunnerForeground

	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	substituteLaunchSpec(t, spec)
	dispatchDetach = false

	prevCapture := dispatchCapture
	dispatchCapture = func(_ agentplan.SessionRecords, _, _ string, _ time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
		return "not-a-uuid", "shortid1", nil
	}
	t.Cleanup(func() { dispatchCapture = prevCapture })

	_, stderr, err := runDispatchCmd(t, "do a thing")
	if err == nil {
		t.Fatal("a mapping that could not be written is an error, whatever else is kept")
	}
	if f.destroyCalled != 0 {
		t.Errorf("destroy called %d times; the turn already ran, so its output is in that directory", f.destroyCalled)
	}
	if _, marked := dispatchRetainReason(f.instancePath); !marked {
		t.Error("no retain marker was written, so the next sweep will delete the finished turn's work")
	}
	if !strings.Contains(stderr, "the work is kept at") {
		t.Errorf("the developer was never told the directory was kept:\n%s", stderr)
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

	stdout, stderr, err := runDispatchCmd(t, "do a thing")
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// The exact message, against BOTH streams. Two holes closed here, each
	// demonstrated rather than imagined. Grepping "still running" would pass
	// with the notice reworded away and fail with it absent but a spared
	// instance present, because the reaper's sparing report says the same
	// words on this same stderr. And reading stderr alone let a copy of the
	// notice routed to stdout go undetected -- a reviewer planted one and this
	// test stayed green.
	both := stdout + stderr
	if notice := unopenableSessionNotice(spec.Binary); strings.Contains(both, notice) {
		t.Errorf("a dispatch whose turn ran in this terminal apologized for not opening the session:\n%s", both)
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
	if notice := unopenableSessionNotice(spec.Binary); !strings.Contains(stderr, notice) {
		t.Errorf("a detached worker its agent will not hand over left the developer nothing to explain a resume that refuses:\n%s", stderr)
	}
}
