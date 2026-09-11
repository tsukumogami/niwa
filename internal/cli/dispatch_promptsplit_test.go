package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// TestEmptyPromptGuardBindsToBodyNotTheComposedString is the regression this
// split could silently introduce.
//
// The launcher's emptiness check used to run against the whole prompt. After
// the split the obvious port is `prefix+body == ""`, which never fires when
// keep-alive is armed, because the prefix is a long constant. An empty task
// would then launch a worker whose entire instruction is "arm your keep-alive"
// -- a dispatch that does nothing, reported as a success.
func TestEmptyPromptGuardBindsToBodyNotTheComposedString(t *testing.T) {
	err := realDispatchLaunch(context.Background(), launchRequest{
		Spec: claudeLaunchSpec(), Mode: agentplan.LaunchBackgrounded,
		InstanceDir: t.TempDir(), Prefix: keepAliveArmingInstruction,
	})
	if err == nil {
		t.Fatal("an empty task with keep-alive armed was accepted; " +
			"the emptiness check is testing the composed string, not the body")
	}
	if !strings.Contains(err.Error(), "empty prompt") {
		t.Fatalf("error should name the empty prompt, got: %v", err)
	}
}

// TestNonEmptyBodyWithEmptyPrefixIsAccepted is the other side of the same
// check: a prefix is optional, a body is not.
func TestNonEmptyBodyWithEmptyPrefixIsAccepted(t *testing.T) {
	// The binary lookup is the next step after the guard, so failing there
	// proves the guard let the prompt through. PATH is emptied to make that
	// failure certain: with claude installed, the lookup would succeed and this
	// test would start a real background session on every run.
	t.Setenv("PATH", "")
	err := realDispatchLaunch(context.Background(), launchRequest{
		Spec: claudeLaunchSpec(), Mode: agentplan.LaunchBackgrounded,
		InstanceDir: t.TempDir(), Body: "do the thing",
	})
	if err == nil || !strings.Contains(err.Error(), "not found in PATH") {
		t.Fatalf("expected the launcher to pass the empty-prompt guard and stop at the binary lookup, got: %v", err)
	}
}

// TestComposedArgvIsPrefixThenBody pins the order. The arming instruction is
// written as a preamble -- it opens "before starting the task below" and closes
// "then proceed with the task" -- so putting the body first would leave a
// dangling forward reference, and would put untrusted text ahead of niwa's own
// framing on every path.
func TestComposedArgvIsPrefixThenBody(t *testing.T) {
	args := buildLaunchArgs(claudeLaunchSpec(), agentplan.LaunchBackgrounded, "/inst", keepAliveArmingInstruction+"the task", nil)
	final := args[len(args)-1]

	if !strings.HasPrefix(final, keepAliveArmingInstruction) {
		t.Error("composed argv does not begin with the arming instruction")
	}
	if !strings.HasSuffix(final, "the task") {
		t.Error("composed argv does not end with the developer's text")
	}
}
