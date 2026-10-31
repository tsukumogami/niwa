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
		{
			name: "armed idle worker is detached-idle",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 1, CronOnly: true},
			want: ActivityDetachedIdle,
		},
		{
			name: "done+idle with only an armed self-wake is detached-idle",
			in:   JobActivity{Readable: true, State: "done", Tempo: "idle", InFlightTasks: 1, CronOnly: true},
			want: ActivityDetachedIdle,
		},
		{
			// The tempo reading for an armed worker mid-turn is "active" on
			// Claude Code 2.1.314. It was read from an existing job file on
			// the development host, of a real dispatched worker mid-turn with
			// one session_cron task in flight, not from a worker armed for the
			// purpose on a disposable host. Because it reads "active", the job
			// state alone separates that worker from an armed idle one. A
			// confirmation on a disposable host, with a worker armed for the
			// purpose, is still owed.
			name: "armed worker mid-turn is busy",
			in:   JobActivity{Readable: true, State: "working", Tempo: "active", InFlightTasks: 1, CronOnly: true},
			want: ActivityBusy,
		},
		{
			name: "cron plus a non-cron task is busy",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 2, NonCronInFlight: 1},
			want: ActivityBusy,
		},
		{
			name: "kinds short of the task count count every task",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 2, NonCronInFlight: 2},
			want: ActivityBusy,
		},
		{
			name: "a non-cron task is busy even when CronOnly is set",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 1, CronOnly: true, NonCronInFlight: 1},
			want: ActivityBusy,
		},
		{
			name: "cron-only awaiting a human is attached",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 1, CronOnly: true, AwaitingInput: true},
			want: ActivityAttached,
		},
		{
			name: "cron-only with blocked tempo is attached",
			in:   JobActivity{Readable: true, State: "working", Tempo: "blocked", InFlightTasks: 1, CronOnly: true},
			want: ActivityAttached,
		},
		{
			name: "cron-only working with empty tempo fails closed",
			in:   JobActivity{Readable: true, State: "working", Tempo: "", InFlightTasks: 1, CronOnly: true},
			want: ActivityDeadUnknown,
		},
		{
			name: "cron-only working with paused tempo fails closed",
			in:   JobActivity{Readable: true, State: "working", Tempo: "paused", InFlightTasks: 1, CronOnly: true},
			want: ActivityDeadUnknown,
		},
		{
			name: "cron-only queued+idle fails closed",
			in:   JobActivity{Readable: true, State: "queued", Tempo: "idle", InFlightTasks: 1, CronOnly: true},
			want: ActivityDeadUnknown,
		},
		{
			name: "working+idle with nothing in flight and no CronOnly stays busy",
			in:   JobActivity{Readable: true, State: "working", Tempo: "idle", InFlightTasks: 0, CronOnly: false},
			want: ActivityBusy,
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

// TestClassifyCronOnlyContinuable sweeps the same states, tempos and awaiting
// values as TestClassifyOnlyDetachedIdleIsContinuable for an armed worker (one
// in-flight task, cron-only). It is continuable exactly when the tempo is idle,
// the state is done or working, and no human is awaited.
func TestClassifyCronOnlyContinuable(t *testing.T) {
	states := []string{"done", "working", "blocked", "queued", ""}
	tempos := []string{"idle", "active", "blocked", "paused", ""}
	for _, st := range states {
		for _, tp := range tempos {
			for _, awaiting := range []bool{false, true} {
				in := JobActivity{Readable: true, State: st, Tempo: tp, InFlightTasks: 1, CronOnly: true, NonCronInFlight: 0, AwaitingInput: awaiting}
				got := ClassifySessionActivity(in)
				wantContinuable := tp == "idle" && (st == "done" || st == "working") && !awaiting
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
