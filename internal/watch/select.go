package watch

import (
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/github"
)

// DefaultPerRunBound is the first-run safety floor on how many new review
// agents a single `watch --once` pass stages. It prevents a workspace with
// many pending review requests from staging a burst of instances at once.
// Richer, configurable concurrency control is deferred.
const DefaultPerRunBound = 3

// DefaultMaxStaged is the cross-run cap on how many live staged review agents may
// exist at once, composed with DefaultPerRunBound. It sits modestly above the
// per-run bound of 3 because each staged agent is a full, self-contained instance
// (its own clone and Claude Code session); 5 lets a small backlog drain over a
// couple of passes without letting staged instances accumulate without limit.
const DefaultMaxStaged = 5

// StageBudget composes the per-run bound with the cross-run staged-agent cap into
// the number of fresh reviews a single pass may stage:
// min(perRunBound, maxStaged - liveCount), clamped to never go negative. A return
// of 0 means the cap is saturated and no fresh review may be staged this pass
// (the caller must short-circuit rather than pass 0 into Decide, whose own
// bound <= 0 fallback would restore DefaultPerRunBound). Only Fresh consumes the
// budget; Continue reuses an already-counted live agent and is cap-neutral, so it
// must not decrement liveCount against this budget.
func StageBudget(perRunBound, maxStaged, liveCount int) int {
	remaining := maxStaged - liveCount
	if remaining < 0 {
		remaining = 0
	}
	if remaining < perRunBound {
		return remaining
	}
	return perRunBound
}

// RepoKey is the workspace-membership key for a repository: "owner/repo",
// lowercased so matching is robust to case differences between the GitHub
// search payload and the workspace config.
func RepoKey(owner, repo string) string {
	return strings.ToLower(owner) + "/" + strings.ToLower(repo)
}

// PlanKind is the per-PR re-dispatch verdict the decision layer produces. It is
// keyed on the last-dispatched head SHA versus the PR's current head and on
// whether a live staged session already exists for the PR.
type PlanKind int

const (
	// Fresh stages a new review agent for the PR this pass: the PR was never
	// handled, or its head advanced and no live session is still reviewing it.
	// Fresh is the only kind that consumes the per-run bound.
	Fresh PlanKind = iota
	// Noop does nothing durable to stage: the current head matches the last
	// dispatched SHA (already reviewed at this head), or the current head could
	// not be confirmed this pass (fail-closed: never re-fire on uncertainty). A
	// legacy unknown-SHA entry is also a Noop whose only effect is adopting the
	// current head into the handled-set without restaging (handled by the caller).
	Noop
	// Defer suppresses a re-fire while a live session is still reviewing the PR at
	// an older head. It is the fallback for the "new head, live session" case;
	// Issue 5 flips a detached-idle live session to Continue (context-preserving
	// resume) while a busy/attached one stays Defer.
	Defer
	// Continue is Issue 5's context-preserving resume of a live, detached-idle
	// session at the new head. Decide returns it for the "new head, live session"
	// case when the session is classified detached-idle AND carries a valid
	// captured resume id (both surfaced through the continuable map); a
	// busy/attached/unclassifiable live session stays Defer. Continue reuses an
	// already-counted live agent, so it is cap-neutral (it does not consume the
	// per-run/cross-run Fresh budget).
	Continue
)

// Plan is the decision layer's verdict for one requested PR: the PR itself plus
// the kind of action the pass should take. It is the SHA-aware successor to the
// bare []PRRef the pass selected before -- the applying pass keys on Kind.
type Plan struct {
	PR   github.PRRef
	Kind PlanKind
}

// Decide turns the raw poll results into a per-PR re-dispatch plan. It is a pure
// function so the deterministic decision contract is table-testable, the way the
// earlier Select was:
//
//   - keep only PRs whose repository is in the workspace (scope) -- a
//     directly-requested PR outside the workspace is dropped entirely;
//   - order the remainder oldest-PR-first by CreatedAt, with a stable tie-break
//     on repo then number, so a repeat run over unchanged state produces the same
//     plan;
//   - classify each kept PR against the SHA-aware handled-set and liveness:
//   - never handled (absent from handledSHAs)          -> Fresh
//   - legacy unknown-SHA entry (recorded == "")        -> Noop (adopt current
//     head without restaging; the caller records it)
//   - current head unconfirmed (absent from heads)     -> Noop (fail-closed)
//   - current head == recorded SHA                     -> Noop (already reviewed)
//   - new head, a live+detached-idle+resumable session  -> Continue
//   - new head, a live but busy/attached session        -> Defer
//   - new head, no live session                         -> Fresh
//   - truncate to bound, counting only Fresh plans -- Noop, Defer, and Continue
//     do not consume the bound (Continue reuses an already-counted live agent),
//     and excess Fresh plans (oldest-first) beyond the bound are dropped, exactly
//     as Select dropped the overflow.
//
// handledSHAs maps a PR identity (HandledIdentity) to its last-dispatched head
// SHA ("" for a legacy unknown-SHA entry); heads maps a PR identity to its
// current head SHA (populated by the caller only for PRs it re-checked, so an
// absent entry means "head not confirmed this pass"); live maps a PR identity to
// whether a staged review session is still alive for it; continuable maps a PR
// identity to whether that live session is safe to context-preserving-resume
// (classified detached-idle by the caller AND carrying a valid captured resume
// id). continuable is a strict subset of live: an id is continuable only if it
// is also live. Keeping the classification out of Decide (the caller computes
// it) keeps this a pure function of maps, table-testable the way Select is.
func Decide(prs []github.PRRef, scope *WorkspaceScope, handledSHAs map[string]string, live, continuable map[string]bool, heads map[string]string, bound int) []Plan {
	if bound <= 0 {
		bound = DefaultPerRunBound
	}

	var kept []github.PRRef
	for _, pr := range prs {
		if !scope.Contains(pr.Owner, pr.Repo) {
			continue
		}
		kept = append(kept, pr)
	}

	sort.SliceStable(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.CreatedAt != b.CreatedAt {
			return a.CreatedAt < b.CreatedAt // oldest first (RFC3339 sorts lexically)
		}
		ak, bk := RepoKey(a.Owner, a.Repo), RepoKey(b.Owner, b.Repo)
		if ak != bk {
			return ak < bk
		}
		return a.Number < b.Number
	})

	var plans []Plan
	freshUsed := 0
	for _, pr := range kept {
		kind := decideKind(pr, handledSHAs, live, continuable, heads)
		if kind == Fresh {
			if freshUsed >= bound {
				continue // per-run bound: drop overflow Fresh, re-decided next pass
			}
			freshUsed++
		}
		plans = append(plans, Plan{PR: pr, Kind: kind})
	}
	return plans
}

// decideKind is the pure per-PR classifier behind Decide. It consults only the
// SHA-aware handled-set, the current-head map, liveness, and continuation
// eligibility -- no ordering or bound. See Decide's doc for the branch table.
func decideKind(pr github.PRRef, handledSHAs map[string]string, live, continuable map[string]bool, heads map[string]string) PlanKind {
	id := HandledIdentity(pr.Owner, pr.Repo, pr.Number)
	recorded, handledBefore := handledSHAs[id]
	if !handledBefore {
		return Fresh // never handled: stage a first review
	}
	if recorded == "" {
		return Noop // legacy unknown-SHA entry: adopt current head, do not restage
	}
	head, haveHead := heads[id]
	if !haveHead {
		return Noop // current head unconfirmed this pass: fail-closed, do not re-fire
	}
	if head == recorded {
		return Noop // already reviewed at this head
	}
	if live[id] {
		// New head while a session is still reviewing the PR. A detached-idle
		// session that carries a valid captured resume id (continuable) is
		// context-preserving-resumed at the new head; a busy/attached or
		// unclassifiable live session stays Defer (fail-closed: continuation
		// fires only on positive confirmation).
		if continuable[id] {
			return Continue
		}
		return Defer
	}
	return Fresh // new head, no live session: re-fire a review
}
