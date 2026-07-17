package watch

import (
	"strings"
	"testing"
)

func TestClassifySessionActivity(t *testing.T) {
	cases := []struct {
		name string
		in   JobActivity
		want SessionActivity
	}{
		{
			name: "unreadable state is dead/unknown",
			in:   JobActivity{Readable: false, State: "done", Tempo: "idle"},
			want: ActivityDeadUnknown,
		},
		{
			name: "done+idle with nothing in flight is the only continuable state",
			in:   JobActivity{Readable: true, State: "done", Tempo: "idle", InFlightTasks: 0},
			want: ActivityDetachedIdle,
		},
		{
			name: "working+active is busy",
			in:   JobActivity{Readable: true, State: "working", Tempo: "active", InFlightTasks: 17},
			want: ActivityBusy,
		},
		{
			name: "in-flight tasks force busy even when state reads done",
			in:   JobActivity{Readable: true, State: "done", Tempo: "idle", InFlightTasks: 3},
			want: ActivityBusy,
		},
		{
			name: "active tempo forces busy even when state reads done",
			in:   JobActivity{Readable: true, State: "done", Tempo: "active"},
			want: ActivityBusy,
		},
		{
			name: "working state forces busy even when tempo reads idle",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle"},
			want: ActivityBusy,
		},
		{
			name: "awaiting a human answer is attached (defer), takes precedence over idle",
			in:   JobActivity{Readable: true, State: "done", Tempo: "idle", AwaitingInput: true},
			want: ActivityAttached,
		},
		{
			name: "blocked tempo is attached",
			in:   JobActivity{Readable: true, State: "working", Tempo: "blocked"},
			want: ActivityAttached,
		},
		{
			name: "blocked state is attached",
			in:   JobActivity{Readable: true, State: "blocked", Tempo: "blocked"},
			want: ActivityAttached,
		},
		{
			name: "unrecognized combination fails closed to dead/unknown",
			in:   JobActivity{Readable: true, State: "done", Tempo: "waiting"},
			want: ActivityDeadUnknown,
		},
		{
			name: "empty-but-readable fails closed (not idle)",
			in:   JobActivity{Readable: true},
			want: ActivityDeadUnknown,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifySessionActivity(tc.in); got != tc.want {
				t.Fatalf("ClassifySessionActivity(%+v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestClassifyOnlyDetachedIdleIsContinuable pins the fail-closed contract: across
// a broad sweep of state/tempo/inflight/awaiting combinations, ActivityDetachedIdle
// is returned for EXACTLY the done+idle+nothing-in-flight+no-human combination and
// nothing else. Any drift that widens continuation eligibility breaks this.
func TestClassifyOnlyDetachedIdleIsContinuable(t *testing.T) {
	states := []string{"done", "working", "blocked", "queued", ""}
	tempos := []string{"idle", "active", "blocked", "paused", ""}
	for _, st := range states {
		for _, tp := range tempos {
			for _, inflight := range []int{0, 1} {
				for _, awaiting := range []bool{false, true} {
					in := JobActivity{Readable: true, State: st, Tempo: tp, InFlightTasks: inflight, AwaitingInput: awaiting}
					got := ClassifySessionActivity(in)
					wantContinuable := st == "done" && tp == "idle" && inflight == 0 && !awaiting
					if wantContinuable && got != ActivityDetachedIdle {
						t.Fatalf("expected DetachedIdle for %+v, got %v", in, got)
					}
					if !wantContinuable && got == ActivityDetachedIdle {
						t.Fatalf("unexpectedly continuable for %+v", in)
					}
				}
			}
		}
	}
}

// TestBuildResumePrompt_FixedTemplate asserts the re-review prompt is a fixed
// template that references only the clone/draft relative paths and carries no
// PR-derived free text.
func TestBuildResumePrompt_FixedTemplate(t *testing.T) {
	got := BuildResumePrompt("pr-clone", "watch-review-draft.md")
	for _, want := range []string{"pr-clone", "watch-review-draft.md", "STOP", "untrusted"} {
		if !strings.Contains(got, want) {
			t.Fatalf("resume prompt missing %q; got:\n%s", want, got)
		}
	}
	// Determinism: identical paths -> identical prompt.
	if BuildResumePrompt("pr-clone", "watch-review-draft.md") != got {
		t.Fatal("BuildResumePrompt is not deterministic")
	}
	// No PR coordinates leak in (the fresh-stage prompt embeds owner/repo/#; the
	// resume prompt must not embed anything PR-derived).
	if strings.Contains(got, "#") || strings.Contains(got, "github.com") {
		t.Fatalf("resume prompt unexpectedly contains PR-derived text:\n%s", got)
	}
}
