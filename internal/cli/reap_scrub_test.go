package cli

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/cli/sessionattach"
)

// reapScrubToken is a GitHub token shape ScrubEvidence knows by its own
// form.
const reapScrubToken = "ghp_abcdefghijklmnopqrstuvwxyz0123"

// TestRemovalGate_SpareLineScrubsTheNameAndTheReason: the gate's spare line
// scrubs the instance's name and the command's last line, whole escape
// sequences and credentials included, where before both went through the
// control removal alone.
func TestRemovalGate_SpareLineScrubsTheNameAndTheReason(t *testing.T) {
	root := setupHookWorkspace(t, true)
	appendBeforeRemoveConfig(t, root, `command = "printf 'password=hunter2 \\033[31mred\\033[0m\\n'; exit 1"`)
	inst := filepath.Join(root, "test-ws-"+reapScrubToken)

	var out bytes.Buffer
	gate := newRemovalGate(root, removeStepReap, &out)
	if gate.ask(inst, nil) {
		t.Fatal("ask allowed a path the command refused")
	}
	want := `niwa: kept test-ws-[redacted]: reason from before_remove: "password=[redacted] red"` + "\n"
	if out.String() != want {
		t.Fatalf("spare line %q; want %q", out.String(), want)
	}
}

// TestOnReap_WarningAndOutputAreScrubbed: the on-reap warning scrubs the
// session id and the reason quoting the command's stderr, and each line the
// command prints on stdout is scrubbed on its way through.
func TestOnReap_WarningAndOutputAreScrubbed(t *testing.T) {
	root := setupHookWorkspace(t, true)
	appendOnReapConfig(t, root, `command = "printf 'closed token=hunter2\\n'; printf 'bad Authorization: Bearer abc.def\\n' >&2; exit 1"`)
	var buf bytes.Buffer
	step := newOnReapStep(root, &buf)
	if step.run(removedSession{SessionID: reapDeadSessionID, ReapKey: reapDeadSessionID}) {
		t.Fatal("a failed run reported success")
	}
	want := "closed token=[redacted]\n" +
		"niwa: warning: on-reap for session " + reapDeadSessionID + ": exited with status 1: bad Authorization: Bearer [redacted]\n"
	if buf.String() != want {
		t.Fatalf("output %q; want %q", buf.String(), want)
	}

	buf.Reset()
	step.warn("id-"+reapScrubToken, "\x1b]0;title\x07reason")
	if want := "niwa: warning: on-reap for session id-[redacted]: reason\n"; buf.String() != want {
		t.Fatalf("warning %q; want %q", buf.String(), want)
	}
}

// TestReportSparedInstances_ScrubsNamesAndReasons: the spared report scrubs
// each name and reason, and two reasons that differ only in what scrubbing
// removes share a line.
func TestReportSparedInstances_ScrubsNamesAndReasons(t *testing.T) {
	var buf bytes.Buffer
	reportSparedInstances(&buf, []sparedInstance{
		{Name: "a-" + reapScrubToken, Reason: "held by token=one"},
		{Name: "b", Reason: "held by token=two"},
	})
	want := "niwa: kept 2 instance(s) this sweep would not reclaim (a-[redacted], b): held by token=[redacted]\n" +
		"niwa: reclaim one with `niwa destroy <name>` when you are done with its session\n"
	if buf.String() != want {
		t.Fatalf("report %q; want %q", buf.String(), want)
	}
}

// TestScrubReapError_ScrubsTheTextAndKeepsTheChain: an error reap returns
// prints scrubbed, and errors.As still finds an exit status inside it.
func TestScrubReapError_ScrubsTheTextAndKeepsTheChain(t *testing.T) {
	if scrubReapError(nil) != nil {
		t.Fatal("a nil error came back non-nil")
	}
	err := scrubReapError(fmt.Errorf("enumerating instances: open /ws/x-%s: denied", reapScrubToken))
	if got, want := err.Error(), "enumerating instances: open /ws/x-[redacted]: denied"; got != want {
		t.Fatalf("error %q; want %q", got, want)
	}
	var ece *sessionattach.ExitCodeError
	if !errors.As(scrubReapError(&sessionattach.ExitCodeError{Code: 1}), &ece) || ece.Code != 1 {
		t.Fatal("the exit status was lost in the wrapper")
	}
	if strings.Contains(err.Error(), "ghp_") {
		t.Fatalf("the token reached the text: %q", err.Error())
	}
}
