package watch

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
)

func ws(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

func TestSelect_IntersectsWorkspaceAndDropsHandled(t *testing.T) {
	prs := []github.PRRef{
		{Owner: "acme", Repo: "api", Number: 42, CreatedAt: "2026-01-03T00:00:00Z"},
		{Owner: "acme", Repo: "web", Number: 7, CreatedAt: "2026-01-01T00:00:00Z"},
		{Owner: "other", Repo: "out", Number: 1, CreatedAt: "2026-01-02T00:00:00Z"}, // outside workspace
		{Owner: "acme", Repo: "api", Number: 5, CreatedAt: "2026-01-02T00:00:00Z"},  // already handled
	}
	workspace := ws("acme/api", "acme/web")
	handled := map[string]bool{HandledKey("acme", "api", 5): true}

	got := Select(prs, workspace, handled, 10)
	if len(got) != 2 {
		t.Fatalf("got %d, want 2: %+v", len(got), got)
	}
	// Oldest-first: web#7 (Jan 1) before api#42 (Jan 3).
	if got[0].Repo != "web" || got[0].Number != 7 {
		t.Errorf("first = %+v, want acme/web#7", got[0])
	}
	if got[1].Repo != "api" || got[1].Number != 42 {
		t.Errorf("second = %+v, want acme/api#42", got[1])
	}
}

func TestSelect_OutOfWorkspaceDropped(t *testing.T) {
	prs := []github.PRRef{{Owner: "stranger", Repo: "repo", Number: 1, CreatedAt: "2026-01-01T00:00:00Z"}}
	got := Select(prs, ws("acme/api"), nil, 10)
	if len(got) != 0 {
		t.Fatalf("expected out-of-workspace PR dropped, got %+v", got)
	}
}

func TestSelect_BoundAndDeterminism(t *testing.T) {
	prs := []github.PRRef{
		{Owner: "acme", Repo: "a", Number: 1, CreatedAt: "2026-01-05T00:00:00Z"},
		{Owner: "acme", Repo: "b", Number: 2, CreatedAt: "2026-01-01T00:00:00Z"},
		{Owner: "acme", Repo: "c", Number: 3, CreatedAt: "2026-01-02T00:00:00Z"},
		{Owner: "acme", Repo: "d", Number: 4, CreatedAt: "2026-01-03T00:00:00Z"},
	}
	workspace := ws("acme/a", "acme/b", "acme/c", "acme/d")

	first := Select(prs, workspace, nil, 2)
	if len(first) != 2 {
		t.Fatalf("bound not applied: %d", len(first))
	}
	// Oldest two: b (Jan 1), c (Jan 2).
	if first[0].Repo != "b" || first[1].Repo != "c" {
		t.Errorf("bounded selection = %s,%s want b,c", first[0].Repo, first[1].Repo)
	}
	// Determinism: repeat over unchanged state -> identical selection.
	second := Select(prs, workspace, nil, 2)
	if len(second) != 2 || second[0] != first[0] || second[1] != first[1] {
		t.Errorf("selection not deterministic across runs")
	}
}

func TestSelect_TieBreakStable(t *testing.T) {
	// Same CreatedAt -> tie-break on repo then number.
	prs := []github.PRRef{
		{Owner: "acme", Repo: "b", Number: 2, CreatedAt: "2026-01-01T00:00:00Z"},
		{Owner: "acme", Repo: "a", Number: 9, CreatedAt: "2026-01-01T00:00:00Z"},
		{Owner: "acme", Repo: "a", Number: 3, CreatedAt: "2026-01-01T00:00:00Z"},
	}
	got := Select(prs, ws("acme/a", "acme/b"), nil, 10)
	want := []string{"a#3", "a#9", "b#2"}
	for i, w := range want {
		if got[i].Repo+"#"+itoa(got[i].Number) != w {
			t.Errorf("pos %d = %s#%d, want %s", i, got[i].Repo, got[i].Number, w)
		}
	}
}

func TestSelect_ZeroBoundUsesDefault(t *testing.T) {
	var prs []github.PRRef
	for i := 0; i < 5; i++ {
		prs = append(prs, github.PRRef{Owner: "acme", Repo: "r", Number: i + 1, CreatedAt: "2026-01-01T00:00:00Z"})
	}
	got := Select(prs, ws("acme/r"), nil, 0)
	if len(got) != DefaultPerRunBound {
		t.Fatalf("zero bound should fall back to DefaultPerRunBound=%d, got %d", DefaultPerRunBound, len(got))
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
