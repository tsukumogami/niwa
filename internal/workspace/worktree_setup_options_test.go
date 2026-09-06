package workspace

import (
	"bytes"
	"os"
	"testing"
)

// TestReporterFor_Precedence pins the rule documented on
// WorktreeApplyOptions.Reporter. Two fields can supply an output channel, so
// the precedence between them has to be stated and held rather than left to
// whichever branch an implementer writes first.
func TestReporterFor_Precedence(t *testing.T) {
	t.Run("explicit reporter wins over stderr", func(t *testing.T) {
		var chosen, ignored bytes.Buffer
		r := NewReporter(&chosen)
		got := reporterFor(WorktreeApplyOptions{Reporter: r, Stderr: &ignored})
		if got != r {
			t.Fatal("an explicit Reporter must win over Stderr")
		}
		got.Log("hello")
		if chosen.Len() == 0 {
			t.Error("output did not reach the explicit Reporter's writer")
		}
		if ignored.Len() != 0 {
			t.Errorf("output leaked to Stderr: %q", ignored.String())
		}
	})

	t.Run("stderr is the fallback", func(t *testing.T) {
		var buf bytes.Buffer
		got := reporterFor(WorktreeApplyOptions{Stderr: &buf})
		if got == nil {
			t.Fatal("expected a Reporter wrapping Stderr, got nil")
		}
		got.Log("hello")
		if buf.Len() == 0 {
			t.Error("output did not reach the supplied Stderr")
		}
	})

	t.Run("neither supplied still yields a usable reporter", func(t *testing.T) {
		got := reporterFor(WorktreeApplyOptions{})
		if got == nil {
			t.Fatal("expected a Reporter wrapping os.Stderr, got nil")
		}
	})
}

// TestWorktreeApplyOptions_ZeroValueIsUnchangedBehaviour is the regression guard
// that makes the clone-path and existing-caller guarantee cheap to hold.
//
// WorktreeApplyOptions{} is what the existing suite passes throughout. It must
// keep meaning "no reporter override, no redactor, do not report the setup
// outcome" -- so the new fields are nil-tolerant sinks and inputs rather than
// anything a caller has to opt out of.
func TestWorktreeApplyOptions_ZeroValueIsUnchangedBehaviour(t *testing.T) {
	var opts WorktreeApplyOptions

	if opts.Setup != nil {
		t.Error("Setup must default to nil, so a caller that wants no outcome gets a no-op")
	}
	if opts.Reporter != nil {
		t.Error("Reporter must default to nil, so Stderr remains the fallback")
	}
	if opts.Redactor != nil {
		t.Error("Redactor must default to nil")
	}

	// The zero value must still resolve to something usable rather than
	// panicking, which is what makes every existing WorktreeApplyOptions{}
	// call site keep working untouched.
	if reporterFor(opts) == nil {
		t.Error("the zero value must still resolve a Reporter")
	}
}

// TestSetupSink_NilIsSilent pins the sink half of the idiom against the shape
// it borrows from -- Exempt *[]string, filled through collectExempt, which
// returns silently on nil.
//
// The property that matters: filling the sink is what a caller opts into, and
// a caller that does not opt in observes nothing. If this ever became a
// returned value instead, every caller would have to handle it, and the one
// that handles it by treating a setup failure as an error is the one that
// deletes a worktree.
func TestSetupSink_NilIsSilent(t *testing.T) {
	var sink *SetupResult
	recordSetupOutcome(sink, &SetupResult{RepoName: "app"})
	// No panic, nothing observable. The assertion is that the call above is
	// safe; a nil sink is the common case on the paths that do not report.

	got := &SetupResult{}
	recordSetupOutcome(got, &SetupResult{RepoName: "app", Skipped: true})
	if got.RepoName != "app" || !got.Skipped {
		t.Errorf("a non-nil sink must receive the outcome, got %+v", got)
	}
}

func TestReporterFor_DoesNotWriteToOsStderrWhenStderrGiven(t *testing.T) {
	// Guard against a fallback that resolves os.Stderr even when the caller
	// supplied a writer -- the interactive commands rely on capturing output.
	var buf bytes.Buffer
	r := reporterFor(WorktreeApplyOptions{Stderr: &buf})
	r.Log("captured")
	if buf.Len() == 0 {
		t.Fatalf("expected output in the supplied writer; os.Stderr is %v", os.Stderr)
	}
}
