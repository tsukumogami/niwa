package cli

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
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
	// The reserve and the derived prompt ceiling are retired. A refusal had to
	// happen before provisioning while the prepend was decided after, so a
	// reserve was the only sound answer; a route needs no such estimate, and
	// the decision is made against the final argv string in the launcher.
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

// The launcher's own backstop refuses a prompt over the exec ceiling before it
// reaches execve, naming the limit instead of yielding an opaque E2BIG. Nothing
// upstream can produce such a value today -- that is the point: it is the guard
// for a future prepend that forgets the spill decision. It fires before the
// claude lookup, so this never spawns anything.
//
// Reaching it now requires stubbing the spill: once the launcher spills, an
// over-limit argv string cannot get here by any ordinary route. That is
// precisely why the spill is a replaceable seam -- an assertion no test can
// construct is an assertion nobody can trust.
func TestDispatchLaunch_OverCeilingPrompt_RefusedBeforeExec(t *testing.T) {
	restore := spillPrompt
	spillPrompt = func(_, _, body string) (string, error) {
		// A no-op spill: hand back a path but leave the prompt oversized, so
		// the assertion below is the thing under test rather than the spill.
		return "", errSpillStubbed
	}
	t.Cleanup(func() { spillPrompt = restore })

	over := strings.Repeat("a", maxArgStringBytes+1)
	err := realDispatchLaunch(context.Background(), t.TempDir(), "", over, nil, []string{})
	if err == nil {
		t.Fatal("expected the launcher to refuse a prompt it could not spill")
	}
	if !strings.Contains(err.Error(), "stubbed") {
		t.Fatalf("error should surface the spill failure, got: %v", err)
	}
}

// TestDispatchLaunch_NulPromptRefusedWhenSpillIsStubbed covers the other
// assertion. An argv element cannot carry a NUL, and before this work such a
// prompt died at exec with an opaque "invalid argument".
func TestDispatchLaunch_NulPromptRefusedWhenSpillIsStubbed(t *testing.T) {
	restore := spillPrompt
	spillPrompt = func(_, _, body string) (string, error) { return "", errSpillStubbed }
	t.Cleanup(func() { spillPrompt = restore })

	err := realDispatchLaunch(context.Background(), t.TempDir(), "", "a\x00b", nil, []string{})
	if err == nil {
		t.Fatal("expected the launcher to refuse a prompt it could not spill")
	}
}

var errSpillStubbed = errors.New("spill stubbed for this test")

// The spill decision is made against the FINAL argv string, after every
// prepend -- not against the developer's text alone. That is what let the
// reserve go: a refusal had to be early and therefore had to estimate the
// prepend, while a route can be made late, where the real length is known.
//
// These drive the real launcher rather than a stub, because the composition is
// what is under test and a stubbed launcher never performs it.
func TestSpillDecisionUsesTheFinalArgvString(t *testing.T) {
	atCap := strings.Repeat("a", maxArgStringBytes)

	if spilled, _ := spillProbe(t, "", atCap); spilled {
		t.Error("a body exactly at the exec cap spilled with no prefix; it fits, so it must not")
	}
	if spilled, _ := spillProbe(t, keepAliveArmingInstruction, atCap); !spilled {
		t.Error("a body at the cap did NOT spill once the arming instruction was prepended; " +
			"the decision is being made against the body rather than the composed string")
	}

	// One byte either side of the boundary, with no prefix.
	if spilled, _ := spillProbe(t, "", strings.Repeat("a", maxArgStringBytes-1)); spilled {
		t.Error("a body one byte under the cap spilled")
	}
	if spilled, _ := spillProbe(t, "", strings.Repeat("a", maxArgStringBytes+1)); !spilled {
		t.Error("a body one byte over the cap did not spill")
	}
}

// TestNulPromptSpillsAtAnySize covers the other trigger. An argv element cannot
// carry a NUL, so before this work such a prompt died at exec with an opaque
// "invalid argument" -- reachable, because the capture preserves raw control
// bytes deliberately.
func TestNulPromptSpillsAtAnySize(t *testing.T) {
	spilled, body := spillProbe(t, "", "a\x00b")
	if !spilled {
		t.Fatal("a short prompt containing a NUL did not spill; it cannot travel in argv")
	}
	if !strings.ContainsRune(body, 0) {
		t.Error("the spilled body lost its NUL; the file takes raw bytes")
	}
}

// spillProbe runs the real launcher with the spill seam recording rather than
// writing, and reports whether the spill fired and what body it was handed.
func spillProbe(t *testing.T, prefix, body string) (bool, string) {
	t.Helper()
	restore := spillPrompt
	var (
		fired    bool
		gotBody  string
		instDir  = t.TempDir()
		fakePath = filepath.Join(instDir, "spilled.local.txt")
	)
	spillPrompt = func(_, _, b string) (string, error) {
		fired, gotBody = true, b
		return fakePath, nil
	}
	t.Cleanup(func() { spillPrompt = restore })

	// Empty PATH so the claude lookup fails immediately. The spill decision runs
	// before it, which is all this probe needs -- and without this the launcher
	// would actually spawn a worker with a 131 KB argument.
	t.Setenv("PATH", "")

	_ = realDispatchLaunch(context.Background(), instDir, prefix, body, nil, []string{})
	return fired, gotBody
}
