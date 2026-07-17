package watch

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
)

func ws(keys ...string) *WorkspaceScope {
	return NewWorkspaceScope(keys, nil)
}

// pr is a terse PRRef constructor for the decision tables. The current head SHA
// is carried separately (in the heads map), matching how the pass polls it.
func pr(owner, repo string, number int, createdAt string) github.PRRef {
	return github.PRRef{Owner: owner, Repo: repo, Number: number, CreatedAt: createdAt}
}

// kindsByRepo projects a plan list onto repo#number -> PlanKind for assertions.
func kindsByRepo(plans []Plan) map[string]PlanKind {
	out := map[string]PlanKind{}
	for _, p := range plans {
		out[p.PR.Repo+"#"+itoa(p.PR.Number)] = p.Kind
	}
	return out
}

func countKind(plans []Plan, kind PlanKind) int {
	n := 0
	for _, p := range plans {
		if p.Kind == kind {
			n++
		}
	}
	return n
}

// TestDecide_NeverHandledIsFresh: a PR absent from the handled-set stages fresh,
// and a PR outside the workspace is dropped entirely.
func TestDecide_NeverHandledIsFresh(t *testing.T) {
	prs := []github.PRRef{
		pr("acme", "api", 42, "2026-01-03T00:00:00Z"),
		pr("acme", "web", 7, "2026-01-01T00:00:00Z"),
		pr("other", "out", 1, "2026-01-02T00:00:00Z"), // outside workspace
	}
	plans := Decide(prs, ws("acme/api", "acme/web"), nil, nil, nil, 10)

	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2 (out-of-scope dropped): %+v", len(plans), plans)
	}
	// Oldest-first: web#7 (Jan 1) before api#42 (Jan 3).
	if plans[0].PR.Repo != "web" || plans[0].Kind != Fresh {
		t.Errorf("first = %+v, want acme/web#7 Fresh", plans[0])
	}
	if plans[1].PR.Repo != "api" || plans[1].Kind != Fresh {
		t.Errorf("second = %+v, want acme/api#42 Fresh", plans[1])
	}
}

func TestDecide_OutOfWorkspaceDropped(t *testing.T) {
	prs := []github.PRRef{pr("stranger", "repo", 1, "2026-01-01T00:00:00Z")}
	plans := Decide(prs, ws("acme/api"), nil, nil, nil, 10)
	if len(plans) != 0 {
		t.Fatalf("expected out-of-workspace PR dropped, got %+v", plans)
	}
}

// TestDecide_UnchangedHeadIsNoop: a PR handled at a SHA that still matches the
// current head does nothing this pass.
func TestDecide_UnchangedHeadIsNoop(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: "aaaaaaa"}
	heads := map[string]string{id: "aaaaaaa"}

	plans := Decide(prs, ws("acme/api"), handled, nil, heads, 10)
	if len(plans) != 1 || plans[0].Kind != Noop {
		t.Fatalf("unchanged head must be Noop, got %+v", plans)
	}
}

// TestDecide_NewHeadNoLiveIsFresh: the head advanced past the recorded SHA and
// no live session is reviewing it -> re-fire fresh.
func TestDecide_NewHeadNoLiveIsFresh(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: "oldsha0"}
	heads := map[string]string{id: "newsha1"}

	plans := Decide(prs, ws("acme/api"), handled, nil, heads, 10)
	if len(plans) != 1 || plans[0].Kind != Fresh {
		t.Fatalf("new head with no live session must be Fresh, got %+v", plans)
	}
}

// TestDecide_NewHeadDismissedIsFresh: a dismissed/crashed-and-reaped session is
// no live record, so a new head re-fires fresh even though the PR was handled.
func TestDecide_NewHeadDismissedIsFresh(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: "oldsha0"}
	heads := map[string]string{id: "newsha1"}
	live := map[string]bool{id: false} // record gone / session dead

	plans := Decide(prs, ws("acme/api"), handled, live, heads, 10)
	if len(plans) != 1 || plans[0].Kind != Fresh {
		t.Fatalf("new head after dismissal must be Fresh, got %+v", plans)
	}
}

// TestDecide_NewHeadWhileLiveIsDefer: the head advanced but a session is still
// reviewing the PR -> Defer (the suppress-while-live fallback; Issue 5 flips a
// detached-idle session to Continue).
func TestDecide_NewHeadWhileLiveIsDefer(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: "oldsha0"}
	heads := map[string]string{id: "newsha1"}
	live := map[string]bool{id: true}

	plans := Decide(prs, ws("acme/api"), handled, live, heads, 10)
	if len(plans) != 1 || plans[0].Kind != Defer {
		t.Fatalf("new head while live must Defer, got %+v", plans)
	}
}

// TestDecide_LegacyUnknownSHAIsNoopAdopt: a legacy SHA-less entry ("" recorded)
// is a Noop on first observation -- adopt the current head, never stage a fresh
// upgrade storm -- regardless of liveness.
func TestDecide_LegacyUnknownSHAIsNoopAdopt(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: ""} // legacy unknown-SHA entry
	heads := map[string]string{id: "currenthead"}

	plans := Decide(prs, ws("acme/api"), handled, nil, heads, 10)
	if len(plans) != 1 || plans[0].Kind != Noop {
		t.Fatalf("legacy unknown-SHA entry must be Noop (adopt), got %+v", plans)
	}
}

// TestDecide_UnconfirmedHeadIsNoop: a handled PR whose current head could not be
// fetched this pass fails closed -- Noop, never a spurious re-fire.
func TestDecide_UnconfirmedHeadIsNoop(t *testing.T) {
	prs := []github.PRRef{pr("acme", "api", 42, "2026-01-01T00:00:00Z")}
	id := HandledIdentity("acme", "api", 42)
	handled := map[string]string{id: "oldsha0"}
	// heads deliberately empty: the head re-check did not land.

	plans := Decide(prs, ws("acme/api"), handled, nil, nil, 10)
	if len(plans) != 1 || plans[0].Kind != Noop {
		t.Fatalf("unconfirmed head must fail closed to Noop, got %+v", plans)
	}
}

// TestDecide_BoundTruncatesFreshOnly: the per-run bound caps Fresh plans
// (oldest-first) while Noop/Defer plans pass through uncapped.
func TestDecide_BoundTruncatesFreshOnly(t *testing.T) {
	prs := []github.PRRef{
		pr("acme", "a", 1, "2026-01-05T00:00:00Z"), // fresh, newest
		pr("acme", "b", 2, "2026-01-01T00:00:00Z"), // fresh, oldest
		pr("acme", "c", 3, "2026-01-02T00:00:00Z"), // fresh
		pr("acme", "d", 4, "2026-01-03T00:00:00Z"), // noop (unchanged head)
	}
	scope := ws("acme/a", "acme/b", "acme/c", "acme/d")
	idD := HandledIdentity("acme", "d", 4)
	handled := map[string]string{idD: "ddddddd"}
	heads := map[string]string{idD: "ddddddd"}

	plans := Decide(prs, scope, handled, nil, heads, 2)
	if got := countKind(plans, Fresh); got != 2 {
		t.Fatalf("bound=2 must cap Fresh at 2, got %d: %+v", got, plans)
	}
	// The two Fresh are the oldest fresh PRs: b (Jan 1), c (Jan 2). a (Jan 5) is
	// dropped as overflow.
	byRepo := kindsByRepo(plans)
	if byRepo["b#2"] != Fresh || byRepo["c#3"] != Fresh {
		t.Errorf("oldest two fresh (b, c) must be the kept Fresh plans: %+v", byRepo)
	}
	if _, ok := byRepo["a#1"]; ok {
		t.Errorf("overflow Fresh (a) must be dropped, got %+v", byRepo)
	}
	// The Noop is not subject to the bound.
	if byRepo["d#4"] != Noop {
		t.Errorf("unchanged-head d must remain Noop uncapped: %+v", byRepo)
	}
}

// TestDecide_Determinism: a repeat run over unchanged state yields an identical
// plan (oldest-first with the stable tie-break).
func TestDecide_Determinism(t *testing.T) {
	prs := []github.PRRef{
		pr("acme", "b", 2, "2026-01-01T00:00:00Z"),
		pr("acme", "a", 9, "2026-01-01T00:00:00Z"),
		pr("acme", "a", 3, "2026-01-01T00:00:00Z"),
	}
	scope := ws("acme/a", "acme/b")
	first := Decide(prs, scope, nil, nil, nil, 10)
	second := Decide(prs, scope, nil, nil, nil, 10)
	if len(first) != 3 || len(second) != 3 {
		t.Fatalf("expected 3 plans each, got %d and %d", len(first), len(second))
	}
	// Tie-break on same CreatedAt: repo then number -> a#3, a#9, b#2.
	want := []string{"a#3", "a#9", "b#2"}
	for i, w := range want {
		got := first[i].PR.Repo + "#" + itoa(first[i].PR.Number)
		if got != w || first[i] != second[i] {
			t.Errorf("pos %d = %s (deterministic=%v), want %s", i, got, first[i] == second[i], w)
		}
	}
}

// TestDecide_ZeroBoundUsesDefault: a zero bound falls back to DefaultPerRunBound
// for Fresh truncation.
func TestDecide_ZeroBoundUsesDefault(t *testing.T) {
	var prs []github.PRRef
	for i := 0; i < 5; i++ {
		prs = append(prs, pr("acme", "r", i+1, "2026-01-01T00:00:00Z"))
	}
	plans := Decide(prs, ws("acme/r"), nil, nil, nil, 0)
	if got := countKind(plans, Fresh); got != DefaultPerRunBound {
		t.Fatalf("zero bound should fall back to DefaultPerRunBound=%d, got %d", DefaultPerRunBound, got)
	}
}

// TestStageBudget composes the per-run bound with the cross-run staged-agent cap.
// It is the pure helper runWatchOnce uses to size a pass's fresh budget.
func TestStageBudget(t *testing.T) {
	cases := []struct {
		name        string
		perRunBound int
		maxStaged   int
		liveCount   int
		want        int
	}{
		// Plenty of cap slack: the per-run bound governs (min picks the bound).
		{"slack larger than bound -> bound", DefaultPerRunBound, DefaultMaxStaged, 0, DefaultPerRunBound},
		// Cap slack smaller than the per-run bound: the remaining cap governs.
		{"slack smaller than bound -> remaining", DefaultPerRunBound, DefaultMaxStaged, 3, 2},
		{"one slot of slack -> 1", DefaultPerRunBound, DefaultMaxStaged, 4, 1},
		// Cap exactly saturated: zero budget, caller must short-circuit.
		{"cap reached -> 0", DefaultPerRunBound, DefaultMaxStaged, DefaultMaxStaged, 0},
		// Over-saturated (more live than the cap, e.g. after lowering it): clamped to 0.
		{"over cap -> clamped 0", DefaultPerRunBound, DefaultMaxStaged, DefaultMaxStaged + 2, 0},
		// Slack equal to the bound: the bound governs (min of equals).
		{"slack equals bound", 3, 6, 3, 3},
	}
	for _, tc := range cases {
		if got := StageBudget(tc.perRunBound, tc.maxStaged, tc.liveCount); got != tc.want {
			t.Errorf("%s: StageBudget(%d, %d, %d) = %d, want %d",
				tc.name, tc.perRunBound, tc.maxStaged, tc.liveCount, got, tc.want)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
