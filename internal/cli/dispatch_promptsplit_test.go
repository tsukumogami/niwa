package cli

import (
	"context"
	"strings"
	"testing"
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
	err := realDispatchLaunch(context.Background(), t.TempDir(),
		keepAliveArmingInstruction, "", nil, nil)
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
	// A missing claude binary is the next failure after the guard, so reaching
	// it proves the guard let the prompt through.
	err := realDispatchLaunch(context.Background(), t.TempDir(), "", "do the thing", nil, nil)
	if err != nil && strings.Contains(err.Error(), "empty prompt") {
		t.Fatal("a non-empty body with no prefix was rejected as empty")
	}
}

// TestComposedArgvIsPrefixThenBody pins the order. The arming instruction is
// written as a preamble -- it opens "before starting the task below" and closes
// "then proceed with the task" -- so putting the body first would leave a
// dangling forward reference, and would put untrusted text ahead of niwa's own
// framing on every path.
func TestComposedArgvIsPrefixThenBody(t *testing.T) {
	args := buildClaudeBgArgs(keepAliveArmingInstruction+"the task", nil)
	final := args[len(args)-1]

	if !strings.HasPrefix(final, keepAliveArmingInstruction) {
		t.Error("composed argv does not begin with the arming instruction")
	}
	if !strings.HasSuffix(final, "the task") {
		t.Error("composed argv does not end with the developer's text")
	}
}
