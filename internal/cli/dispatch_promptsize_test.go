package cli

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// The dispatch prompt travels to claude as one discrete argv element, so what
// bounds it is the kernel's PER-STRING argument limit, not ARG_MAX. These tests
// pin that boundary from three directions: the derivation of the constants, the
// real exec behavior on the host, and the command's own accept/reject edge with
// keep-alive both armed and unarmed.

// TestDispatchPromptLimits_DerivedFromExecLimit pins how the three constants
// relate, spelled as derivations rather than as round numbers. A round number
// is exactly how this regressed: 128*1024 reads like a conservative bound but
// is precisely MAX_ARG_STRLEN, which counts the NUL terminator.
func TestDispatchPromptLimits_DerivedFromExecLimit(t *testing.T) {
	// MAX_ARG_STRLEN is 32 pages at the usual 4 KiB page size, inclusive of the
	// terminating NUL, so the largest usable argument is one byte less.
	const linuxMaxArgStrlen = 32 * 4096
	if maxArgStringBytes != linuxMaxArgStrlen-1 {
		t.Errorf("maxArgStringBytes = %d, want MAX_ARG_STRLEN-1 = %d (the NUL is counted against the limit)",
			maxArgStringBytes, linuxMaxArgStrlen-1)
	}
	if maxArgStringBytes >= linuxMaxArgStrlen {
		t.Errorf("maxArgStringBytes = %d must sit strictly below MAX_ARG_STRLEN = %d",
			maxArgStringBytes, linuxMaxArgStrlen)
	}
	if dispatchPromptReserve != len(keepAliveArmingInstruction) {
		t.Errorf("dispatchPromptReserve = %d, want len(keepAliveArmingInstruction) = %d; everything prepended after the check must be reserved",
			dispatchPromptReserve, len(keepAliveArmingInstruction))
	}
	if maxPromptBytes != maxArgStringBytes-dispatchPromptReserve {
		t.Errorf("maxPromptBytes = %d, want maxArgStringBytes-dispatchPromptReserve = %d",
			maxPromptBytes, maxArgStringBytes-dispatchPromptReserve)
	}
}

// TestDispatchPromptLimit_LargestArgumentExecs probes the constant against the
// real thing: an argument of exactly maxArgStringBytes must survive execve on
// this host. It asserts only the accepting direction. The rejecting direction is
// not portable -- MAX_ARG_STRLEN scales with page size, so a 64 KiB-page kernel
// accepts far more, and macOS has no per-string cap at all -- and a test that
// demanded failure there would fail on a perfectly good host.
func TestDispatchPromptLimit_LargestArgumentExecs(t *testing.T) {
	bin, err := exec.LookPath("true")
	if err != nil {
		t.Skipf("no 'true' binary on PATH to probe the exec limit with: %v", err)
	}
	arg := strings.Repeat("a", maxArgStringBytes)
	if err := exec.Command(bin, arg).Run(); err != nil {
		t.Fatalf("execve rejected a %d-byte argument (%v); maxArgStringBytes is above what this host accepts", len(arg), err)
	}
}

// A prompt at exactly the accepted size dispatches, with keep-alive UNARMED.
// The launched prompt must be the user's bytes untouched.
func TestDispatch_PromptAtLimit_Unarmed_Dispatches(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var launched string
	captureLaunchPrompt(f, &launched, nil)

	atLimit := strings.Repeat("a", maxPromptBytes)
	if _, _, err := runDispatchCmd(t, atLimit); err != nil {
		t.Fatalf("a prompt at the accepted limit must dispatch: %v", err)
	}
	if len(launched) != maxPromptBytes {
		t.Fatalf("launched prompt is %d bytes, want the %d supplied unchanged", len(launched), maxPromptBytes)
	}
	if len(launched) > maxArgStringBytes {
		t.Fatalf("launched prompt is %d bytes, over the %d-byte exec limit", len(launched), maxArgStringBytes)
	}
}

// The same prompt with keep-alive ARMED also dispatches, and the final string
// lands exactly on the exec ceiling: maxPromptBytes plus the arming instruction
// is maxArgStringBytes, by construction. This is the case the old cap admitted
// and then died on at execve.
func TestDispatch_PromptAtLimit_KeepAliveArmed_Dispatches(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, hostRConDispatch) // RC on, so keep-alive can actually arm
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	var launched string
	captureLaunchPrompt(f, &launched, nil)
	dispatchKeepAlive = kaBoolPtr(true)

	atLimit := strings.Repeat("a", maxPromptBytes)
	if _, _, err := runDispatchCmd(t, atLimit); err != nil {
		t.Fatalf("a prompt at the accepted limit must dispatch with keep-alive armed: %v", err)
	}
	if !strings.HasPrefix(launched, keepAliveArmingInstruction) {
		t.Fatal("keep-alive did not arm; this test must exercise the prepend path")
	}
	if len(launched) != maxArgStringBytes {
		t.Fatalf("armed prompt is %d bytes, want exactly the %d-byte exec ceiling", len(launched), maxArgStringBytes)
	}
	// The whole thing still rides one argv element -- that is what the limit
	// applies to, and what the D8 no-shell-interpolation guard depends on.
	args := buildClaudeBgArgs(launched, nil)
	if got := len(args[len(args)-1]); got != maxArgStringBytes {
		t.Fatalf("final argv element is %d bytes, want %d", got, maxArgStringBytes)
	}
}

// The launcher's own backstop refuses a prompt over the exec ceiling before it
// reaches execve, naming the limit instead of yielding an opaque E2BIG. Nothing
// upstream can produce such a value today -- that is the point: it is the guard
// for a future prepend that forgets dispatchPromptReserve. It fires before the
// claude lookup, so this never spawns anything.
func TestDispatchLaunch_OverCeilingPrompt_RefusedBeforeExec(t *testing.T) {
	over := strings.Repeat("a", maxArgStringBytes+1)
	err := realDispatchLaunch(context.Background(), t.TempDir(), "", over, nil, []string{})
	if err == nil {
		t.Fatal("expected the launcher to refuse a prompt over the exec ceiling")
	}
	if !strings.Contains(err.Error(), "exec limit") {
		t.Fatalf("error should name the single-argument exec limit, got: %v", err)
	}
}

// One byte over is rejected by the up-front check, before anything is
// provisioned, with a message that names both sizes.
func TestDispatch_PromptOneByteOver_RejectedBeforeProvision(t *testing.T) {
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	f := installDispatchFakes(t, root)

	over := strings.Repeat("a", maxPromptBytes+1)
	_, _, err := runDispatchCmd(t, over)
	if err == nil {
		t.Fatal("expected a rejection for a prompt one byte over the limit")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error should say the prompt is too long, got: %v", err)
	}
	for _, want := range []string{strconv.Itoa(len(over)), strconv.Itoa(maxPromptBytes)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should report %s (the supplied and permitted sizes), got: %v", want, err)
		}
	}
	if f.provisionCalled != 0 {
		t.Errorf("nothing must be provisioned for an over-limit prompt; provision called %d", f.provisionCalled)
	}
	if f.launchCalled != 0 {
		t.Errorf("no launch for an over-limit prompt; launch called %d", f.launchCalled)
	}
}

// The rejection is unconditional: it does not depend on keep-alive resolving on
// (which cannot be known before an instance exists). A prompt one byte over is
// refused with keep-alive explicitly OFF too, and still provisions nothing.
func TestDispatch_PromptOneByteOver_RejectedWithKeepAliveOff(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	dispatchKeepAlive = kaBoolPtr(false)

	over := strings.Repeat("a", maxPromptBytes+1)
	if _, _, err := runDispatchCmd(t, over); err == nil {
		t.Fatal("expected a rejection regardless of keep-alive resolution")
	}
	if f.provisionCalled != 0 {
		t.Errorf("nothing must be provisioned; provision called %d", f.provisionCalled)
	}
}
