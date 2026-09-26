package watch

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
)

// TestFreshness walks the freshness decision matrix. The predicate is pure, so the
// whole contract is table-testable: only a confirmed stale signal (not requested,
// or a diverged head) discards; ordinary advancement and an inconclusive ancestry
// check both keep the review.
func TestFreshness(t *testing.T) {
	rec := StagedRecord{Owner: "acme", Repo: "api", Number: 42, DispatchedSHA: "abc1234"}

	cases := []struct {
		name           string
		stillRequested bool
		ancestry       github.Ancestry
		wantOK         bool
	}{
		{"still requested + ancestor -> fresh (happy path, NOT discarded)", true, github.AncestryAncestor, true},
		{"still requested + unknown -> fresh (conservative, not discarded)", true, github.AncestryUnknown, true},
		{"not requested -> stale (closed/merged/un-requested)", false, github.AncestryAncestor, false},
		{"diverged -> stale (force-push/rebase off dispatched head)", true, github.AncestryDiverged, false},
		{"not requested wins even over unknown ancestry", false, github.AncestryUnknown, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := Freshness(rec, tc.stillRequested, tc.ancestry)
			if ok != tc.wantOK {
				t.Fatalf("Freshness ok = %v, want %v (reason %q)", ok, tc.wantOK, reason)
			}
			if ok && reason != "" {
				t.Errorf("fresh result must carry an empty reason, got %q", reason)
			}
			if !ok && reason == "" {
				t.Errorf("stale result must name the failed condition, got empty reason")
			}
		})
	}
}

// TestFreshness_ReasonNamesCondition pins the reason text so a caller (the prune
// log line and the pre-flight stderr) reports which check discarded the review.
func TestFreshness_ReasonNamesCondition(t *testing.T) {
	rec := StagedRecord{Owner: "acme", Repo: "api", Number: 42, DispatchedSHA: "abc1234"}

	if _, reason := Freshness(rec, false, github.AncestryAncestor); reason != "PR no longer open or no longer requesting review" {
		t.Errorf("not-requested reason = %q", reason)
	}
	if _, reason := Freshness(rec, true, github.AncestryDiverged); reason != "PR was force-pushed/rebased off the dispatched head" {
		t.Errorf("diverged reason = %q", reason)
	}
}
