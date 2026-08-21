package cli

import (
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
)

// foregroundRunnerAgent is a launchable agent whose runner executes the turn in
// the foreground -- the only kind that offers both process models, and so the
// only kind that can show --detach changing the capture deadline while
// everything else stays the same.
func foregroundRunnerAgent(t *testing.T) agent.Agent {
	t.Helper()
	for _, ag := range agentplan.LaunchableAgents() {
		spec, ok := dispatchLaunchSpec(ag)
		if ok && spec.Runner == agentplan.RunnerForeground {
			return ag
		}
	}
	t.Skip("no launchable agent runs its turn in the foreground, so there is no second process model to compare")
	return ""
}

// TestDispatch_CaptureTimeoutFollowsTheProcessModel pins the deadline the
// session-record poll is given to what the launch actually did.
//
// Detached, the worker started moments ago and the record is about to appear,
// so the full timeout is the point. Foreground, the launch already waited for
// the turn to end: the record exists on the first pass or it never will, and
// the difference is thirty seconds of rescanning the record store after the
// developer watched the turn finish.
//
// The two deadlines are read from the constants rather than typed out, because
// what this holds is the selection and not the numbers. What it will not let
// pass is the two collapsing into one: replace the conditional with the plain
// long timeout and the foreground case fails.
func TestDispatch_CaptureTimeoutFollowsTheProcessModel(t *testing.T) {
	if dispatchForegroundCaptureTimeout >= dispatchCaptureTimeout {
		t.Fatalf("the foreground deadline (%v) is not shorter than the detached one (%v); there is nothing here to select between",
			dispatchForegroundCaptureTimeout, dispatchCaptureTimeout)
	}

	for _, tc := range []struct {
		name   string
		detach bool
		want   time.Duration
		why    string
	}{
		{
			name:   "foreground",
			detach: false,
			want:   dispatchForegroundCaptureTimeout,
			why:    "the turn already ended in this terminal, so the poll is a grace period for a slow filesystem",
		},
		{
			name:   "detached",
			detach: true,
			want:   dispatchCaptureTimeout,
			why:    "the worker was just released and the record has not been written yet",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ag := foregroundRunnerAgent(t)
			root := setupDispatchWorkspace(t)
			chdir(t, root)
			setHostConfig(t, "")
			installDispatchFakes(t, root)

			var deadlines []time.Duration
			prev := dispatchCapture
			dispatchCapture = func(_ agentplan.SessionRecords, _, _ string, timeout time.Duration, _ func() time.Time, _ time.Duration) (string, string, error) {
				deadlines = append(deadlines, timeout)
				return dispatchTestSessionID, dispatchTestShortID, nil
			}
			t.Cleanup(func() { dispatchCapture = prev })

			dispatchLaunchAgent = string(ag)
			dispatchDetach = tc.detach
			t.Setenv("NIWA_AGENT", "")

			if _, _, err := runDispatchCmd(t, "do a thing"); err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if len(deadlines) != 1 {
				t.Fatalf("the capture poll ran %d times, want once", len(deadlines))
			}
			if deadlines[0] != tc.want {
				t.Errorf("a %s launch polled for the record with a %v deadline, want %v: %s", tc.name, deadlines[0], tc.want, tc.why)
			}
		})
	}
}
