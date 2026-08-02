package cli

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tsukumogami/niwa/internal/promptcapture"
)

// runDispatchArgs invokes runDispatch with an arbitrary argument slice, so the
// zero-argument capture path is reachable from a test.
func runDispatchArgs(t *testing.T, args []string) (stdout, stderr string, err error) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	cmd.SetContext(context.Background())
	return outBuf.String(), errBuf.String(), runDispatch(cmd, args)
}

// stubCapture replaces the capture seam for the duration of a test.
func stubCapture(t *testing.T, fn func() (string, error)) {
	t.Helper()
	prev := dispatchPromptCapture
	dispatchPromptCapture = fn
	t.Cleanup(func() { dispatchPromptCapture = prev })
}

// stubCaptureTTY forces the interactivity gate for the capture path.
func stubCaptureTTY(t *testing.T, stdinTTY, stderrTTY bool) {
	t.Helper()
	prevIn, prevErr := IsStdinTTY, IsStderrTTY
	IsStdinTTY = func() bool { return stdinTTY }
	IsStderrTTY = func() bool { return stderrTTY }
	t.Cleanup(func() { IsStdinTTY, IsStderrTTY = prevIn, prevErr })
}

func workspaceForDispatch(t *testing.T) (*dispatchFakes, *string) {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	root := setupDispatchWorkspace(t)
	chdir(t, root)
	setHostConfig(t, "")
	f := installDispatchFakes(t, root)
	provisionWithInstanceSettings(t, f, "")
	launched := new(string)
	captureLaunchPrompt(f, launched, nil)
	return f, launched
}

func TestDispatchCapture_TextReachesTheLauncher(t *testing.T) {
	_, launched := workspaceForDispatch(t)
	stubCaptureTTY(t, true, true)
	stubCapture(t, func() (string, error) { return "pasted failure text", nil })

	if _, _, err := runDispatchArgs(t, nil); err != nil {
		t.Fatalf("dispatch with a captured prompt: %v", err)
	}
	if got := *launched; got != "pasted failure text" {
		t.Fatalf("launcher received %q, want the captured text", got)
	}
}

func TestDispatchCapture_MetacharacterPayloadSurvivesVerbatim(t *testing.T) {
	_, launched := workspaceForDispatch(t)
	stubCaptureTTY(t, true, true)
	payload := `a "quoted" $VAR \back\slash 'single' ` + "`tick`" + " \n second line"
	stubCapture(t, func() (string, error) { return payload, nil })

	if _, _, err := runDispatchArgs(t, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got := *launched; got != payload {
		t.Fatalf("payload altered on the way to argv:\n got %q\nwant %q", got, payload)
	}
}

func TestDispatchCapture_GateRequiresBothStreams(t *testing.T) {
	for _, tc := range []struct {
		name           string
		stdin, stderrT bool
		wantCapture    bool
	}{
		{"both terminals", true, true, true},
		{"stdin only", true, false, false},
		{"stderr only", false, true, false},
		{"neither", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _ = workspaceForDispatch(t)
			stubCaptureTTY(t, tc.stdin, tc.stderrT)

			called := false
			stubCapture(t, func() (string, error) {
				called = true
				return "captured", nil
			})

			_, _, err := runDispatchArgs(t, nil)
			if called != tc.wantCapture {
				t.Fatalf("capture called=%v, want %v", called, tc.wantCapture)
			}
			if tc.wantCapture {
				return
			}
			if err == nil {
				t.Fatal("expected a refusal when the session is not interactive")
			}
			if !strings.Contains(err.Error(), `niwa dispatch "<task>"`) {
				t.Fatalf("refusal does not name the positional form: %v", err)
			}
		})
	}
}

func TestDispatchCapture_PositionalArgumentNeverConsultsTheTerminal(t *testing.T) {
	_, _ = workspaceForDispatch(t)

	consulted := false
	prevIn, prevErr := IsStdinTTY, IsStderrTTY
	IsStdinTTY = func() bool { consulted = true; return true }
	IsStderrTTY = func() bool { consulted = true; return true }
	t.Cleanup(func() { IsStdinTTY, IsStderrTTY = prevIn, prevErr })

	stubCapture(t, func() (string, error) {
		t.Fatal("capture was invoked on the positional-argument path")
		return "", nil
	})

	if _, _, err := runDispatchArgs(t, []string{"a task"}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if consulted {
		t.Fatal("the positional path consulted terminal state")
	}
}

func TestDispatchCapture_AbandonmentProvisionsNothing(t *testing.T) {
	f, _ := workspaceForDispatch(t)
	stubCaptureTTY(t, true, true)
	stubCapture(t, func() (string, error) { return "", promptcapture.ErrCanceled })

	_, _, err := runDispatchArgs(t, nil)
	if err == nil {
		t.Fatal("expected a non-nil error for an abandoned capture")
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provisioned %d instances after abandonment; want 0", f.provisionCalled)
	}
}

func TestDispatchCapture_EndOfInputOnEmptyBufferIsEmptyPromptError(t *testing.T) {
	f, _ := workspaceForDispatch(t)
	stubCaptureTTY(t, true, true)
	stubCapture(t, func() (string, error) { return "", promptcapture.ErrEndOfInput })

	_, _, err := runDispatchArgs(t, nil)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("got %v, want the empty-prompt error", err)
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provisioned %d instances; want 0", f.provisionCalled)
	}
}

func TestDispatchCapture_WhitespaceOnlySubmissionIsRefused(t *testing.T) {
	f, _ := workspaceForDispatch(t)
	stubCaptureTTY(t, true, true)
	stubCapture(t, func() (string, error) { return "  \n\t  \n", nil })

	_, _, err := runDispatchArgs(t, nil)
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("got %v, want the empty-prompt error", err)
	}
	if f.provisionCalled != 0 {
		t.Fatalf("provisioned %d instances; want 0", f.provisionCalled)
	}
}

// Both paths must behave identically on an oversized prompt: same spill file
// contents, same pointer wording, same excerpt. This replaces the pair of tests
// that asserted both paths quote the same REFUSAL -- there is no refusal now,
// and "both paths agree" is still the property worth holding.
func TestDispatchCapture_BothPathsSpillIdentically(t *testing.T) {
	oversized := strings.Repeat("x", maxArgStringBytes+1)

	argPrompt := launchedPromptFor(t, oversized, false)
	capPrompt := launchedPromptFor(t, oversized, true)

	argNorm := normalizeSpillPointer(argPrompt)
	capNorm := normalizeSpillPointer(capPrompt)
	if argNorm != capNorm {
		t.Fatalf("paths produced different pointers once the instance dir and token are normalized:\n arg: %s\n cap: %s",
			argNorm, capNorm)
	}
	if strings.Contains(strings.ToLower(argPrompt), "too long") {
		t.Fatalf("an oversized prompt still surfaces a size refusal: %s", argPrompt)
	}
}

// launchedPromptFor dispatches oversized text by one of the two paths and
// returns the argv element the worker would have received.
func launchedPromptFor(t *testing.T, body string, viaCapture bool) string {
	t.Helper()
	f, _ := workspaceForDispatch(t)
	var got string
	captureLaunchPrompt(f, &got, nil)
	if viaCapture {
		stubCaptureTTY(t, true, true)
		stubCapture(t, func() (string, error) { return body, nil })
		if _, _, err := runDispatchArgs(t, nil); err != nil {
			t.Fatalf("capture path: %v", err)
		}
		return got
	}
	if _, _, err := runDispatchArgs(t, []string{body}); err != nil {
		t.Fatalf("argument path: %v", err)
	}
	return got
}

// normalizeSpillPointer replaces the two things that legitimately differ
// between runs -- the instance directory and the per-launch token -- so the
// rest of the pointer can be compared byte for byte.
func normalizeSpillPointer(s string) string {
	s = regexp.MustCompile(`prompt-[0-9a-f]{16}`).ReplaceAllString(s, "prompt-<token>")
	s = regexp.MustCompile(`NIWA-PROMPT-EXCERPT-[0-9a-f]{16}`).ReplaceAllString(s, "NIWA-PROMPT-EXCERPT-<token>")
	s = regexp.MustCompile(`file: \S+`).ReplaceAllString(s, "file: <path>")
	return s
}

func TestDispatchCapture_ExplicitEmptyArgumentStillErrors(t *testing.T) {
	_, _ = workspaceForDispatch(t)
	stubCapture(t, func() (string, error) {
		t.Fatal("an explicit empty argument must not trigger a capture")
		return "", nil
	})

	_, _, err := runDispatchArgs(t, []string{""})
	if err == nil || !strings.Contains(err.Error(), "must not be empty") {
		t.Fatalf("got %v, want the empty-prompt error", err)
	}
}

func TestDispatchCapture_TwoPositionalArgumentsAreAnArgumentError(t *testing.T) {
	if err := dispatchCmd.Args(dispatchCmd, []string{"one", "two"}); err == nil {
		t.Fatal("two positional arguments were accepted; expected an argument error")
	}
	if err := dispatchCmd.Args(dispatchCmd, []string{"one"}); err != nil {
		t.Fatalf("one positional argument rejected: %v", err)
	}
	if err := dispatchCmd.Args(dispatchCmd, nil); err != nil {
		t.Fatalf("zero positional arguments rejected: %v", err)
	}
}

func TestDispatchCapture_CanceledIsNotConflatedWithEndOfInput(t *testing.T) {
	if errors.Is(promptcapture.ErrCanceled, promptcapture.ErrEndOfInput) {
		t.Fatal("cancellation and end-of-input are the same sentinel")
	}
}
