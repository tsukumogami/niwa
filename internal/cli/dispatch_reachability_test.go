package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// The interactive capture must be unreachable from any caller that does not go
// through the command's own run path. `niwa watch` is the one that matters: it
// invokes dispatchLaunch directly, on a cron timer, with no terminal. A capture
// placed one layer too deep would give a scheduled review sweep an interactive
// read that never returns.
//
// This test fails the day someone moves the capture into the launcher.
func TestLauncherCannotReachTheCapture(t *testing.T) {
	stubCapture(t, func() (string, error) {
		t.Fatal("the capture was invoked from the launcher path; a cron-driven " +
			"sweep would block forever waiting on input that never arrives")
		return "", nil
	})

	// Force the gate open too, so the test fails on reachability rather than
	// passing because a TTY check happened to refuse first.
	stubCaptureTTY(t, true, true)

	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")

	// Drive the real launcher seam the way watch.go does: a prompt built by the
	// caller, passed positionally, with no terminal involved.
	err := dispatchLaunch(context.Background(), launchRequest{
		Spec: claudeLaunchSpec(), Mode: agentplan.LaunchBackgrounded,
		InstanceDir: root, Body: "resume the review",
	})
	if err != nil {
		t.Fatalf("launcher returned %v", err)
	}
	if f.launchCalled == 0 {
		t.Fatal("the launcher seam was never exercised, so this test proved nothing")
	}
}

// The launcher rejects an empty prompt rather than reaching for one. This is the
// property that makes the reachability guarantee structural: there is no branch
// in the launcher that could ever want to ask.
func TestLauncherRejectsEmptyPromptRatherThanCapturing(t *testing.T) {
	stubCapture(t, func() (string, error) {
		t.Fatal("the launcher tried to capture a prompt instead of rejecting an empty one")
		return "", nil
	})

	err := realDispatchLaunch(context.Background(), launchRequest{
		Spec: claudeLaunchSpec(), Mode: agentplan.LaunchBackgrounded,
		InstanceDir: t.TempDir(),
	})
	if err == nil {
		t.Fatal("expected the launcher to reject an empty prompt")
	}
	if !strings.Contains(err.Error(), "empty prompt") {
		t.Fatalf("got %v, want an empty-prompt rejection", err)
	}
}
