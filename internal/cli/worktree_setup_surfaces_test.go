package cli

import (
	"os"
	"strings"
	"testing"
)

// TestEveryWorktreeSurfaceReportsSetup enumerates the surfaces that install
// content into a worktree and asserts each one reports the setup outcome.
//
// It exists because the behavioural tests could not see this. Deleting the
// reporting call from `niwa worktree create` and `niwa worktree apply` left the
// entire suite green: the helper tests call reportWorktreeSetup directly with a
// hand-built result, so they check its formatting and cannot check whether
// anyone calls it, and only the delegated-create test exercised delivery end to
// end -- one surface of four.
//
// A setup failure that is computed and never reported is this feature's own
// defect class, on the two surfaces a human actually types. So the surface list
// is enumerated on one side, the call is asserted on the other, and the two
// have to agree -- the same instrument
// TestEveryProvisioningSurfaceResolvesStrictness uses for strictness, and the
// same one the feature itself uses for hook events and for RepoOverride's
// fields.
//
// Adding a fifth CLI surface that installs worktree content means adding a line
// here, which is the point: the omission becomes a failing test rather than
// silence.
func TestEveryWorktreeSurfaceReportsSetup(t *testing.T) {
	surfaces := map[string]string{
		"session_lifecycle_cmd.go": "niwa worktree create and niwa worktree apply",
		"session_from_hook_cmd.go": "the delegated WorktreeCreate hook",
		"apply.go":                 "niwa apply at worktree scope",
	}

	for file, surface := range surfaces {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		// Match the CALL form, not the bare name: reportWorktreeSetup is
		// DEFINED in one of these files, so a substring check on the name
		// alone would pass that file with zero calls in it. Found by mutation
		// -- deleting a call left this test green until the pattern got
		// specific.
		if !strings.Contains(string(data), "reportWorktreeSetup(cmd.") {
			t.Errorf("%s (%s) installs worktree content without reporting the setup "+
				"outcome; a failure there is computed and thrown away", file, surface)
		}
	}

	// session_lifecycle_cmd.go carries TWO surfaces -- create and apply -- so a
	// single occurrence would leave one of them silent while the check above
	// passed. This is the shape the deletion mutation exploited.
	data, err := os.ReadFile("session_lifecycle_cmd.go")
	if err != nil {
		t.Fatalf("reading session_lifecycle_cmd.go: %v", err)
	}
	if got := strings.Count(string(data), "reportWorktreeSetup(cmd."); got < 2 {
		t.Errorf("session_lifecycle_cmd.go calls reportWorktreeSetup %d time(s); "+
			"both `niwa worktree create` and `niwa worktree apply` must report, and "+
			"one call covers only one of them", got)
	}

	// The fifth entry path, the apply pipeline's per-worktree fan-out, does not
	// use this helper: it reports through the pipeline's own deferred warnings
	// and the counted verdict, which is where a worktree failure belongs on
	// that surface. Recorded here so the asymmetry is documented rather than
	// looking like an omission from the list above.
}
