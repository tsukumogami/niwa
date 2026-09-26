package watch

import "github.com/tsukumogami/niwa/internal/github"

// Freshness is the deterministic predicate that decides whether a staged review
// is still worth presenting, run both by the watcher-pass GC (over live staged
// records) and by the session pre-flight subcommand before a session presents its
// draft. It is pure and table-testable: it consumes only the record, whether the
// PR is still requesting review (from the poll), and the ancestry of the
// dispatched SHA versus the PR's current head (from the trusted compare API). No
// model participates -- the discard is code-driven, never a judgment call.
//
// The rules, in order:
//
//   - not stillRequested -> STALE. The PR closed/merged, or the developer is no
//     longer the requested reviewer; the staged review has nothing to present.
//   - AncestryDiverged -> STALE. The head was force-pushed/rebased off the
//     dispatched base, so the review was drafted against a history that no longer
//     leads to the current head.
//   - stillRequested AND AncestryAncestor -> FRESH. The head only advanced
//     (ordinary advancement); that is new activity handled by the re-dispatch
//     path, NOT a freshness failure, so the current staged review is not
//     discarded here.
//   - AncestryUnknown (with stillRequested) -> FRESH, conservatively. An
//     inconclusive ancestry check (a transient compare-API hiccup) is not
//     evidence of divergence. Discarding is the destructive action, so it is
//     taken only on a confirmed stale signal -- never on doubt. The next pass
//     re-checks and prunes if the divergence is real.
//
// ok reports freshness; reason names the failed condition (empty when fresh) so
// the caller can print or return which check discarded the review.
func Freshness(rec StagedRecord, stillRequested bool, ancestry github.Ancestry) (ok bool, reason string) {
	if !stillRequested {
		return false, "PR no longer open or no longer requesting review"
	}
	if ancestry == github.AncestryDiverged {
		return false, "PR was force-pushed/rebased off the dispatched head"
	}
	// AncestryAncestor (ordinary advancement) and AncestryUnknown (inconclusive)
	// both keep the review: advancement is the re-dispatch path's business, and an
	// inconclusive check must not discard a still-valid review.
	return true, ""
}
