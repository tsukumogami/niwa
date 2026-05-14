package gc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// recordingSink captures every audit event the sweep emits. The
// dual-target emitter (AppendChangeEvent) fans to both transitions.log
// and this sink; the tests assert on both targets.
type recordingSink struct {
	mu      sync.Mutex
	entries []mcp.AuditEntry
}

func (s *recordingSink) Emit(e mcp.AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, e)
	return nil
}

func (s *recordingSink) byEvent(event string) []mcp.AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []mcp.AuditEntry
	for _, e := range s.entries {
		if e.Event == event {
			out = append(out, e)
		}
	}
	return out
}

// seedChangeWithUpdatedAt materialises a change on disk in `state`
// with the supplied UpdatedAt timestamp. The state.json carries the
// stamp verbatim so the sweep's `now.Sub(updated_at)` math is
// observable without waiting wall-clock days.
func seedChangeWithUpdatedAt(t *testing.T, root, state string, updatedAt time.Time, diff []byte) string {
	t.Helper()
	id, err := mcp.ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	stamp := updatedAt.UTC().Format(time.RFC3339Nano)
	cs := mcp.ChangeState{
		V:                  1,
		ID:                 id,
		State:              state,
		OriginatingSession: "abcdef01",
		OriginatingTasks:   []string{},
		CreatedAt:          stamp,
		UpdatedAt:          stamp,
		BaseRef:            "base-sha",
		HeadRef:            "head-sha",
		Branch:             "feature/x",
		WorktreePath:       "/tmp/worktree",
		DiffPath:           "diff.patch",
		Metadata:           map[string]any{},
	}
	if err := mcp.WriteInitial(root, cs); err != nil {
		t.Fatalf("WriteInitial: %v", err)
	}
	// WriteInitial preserves the supplied UpdatedAt because it does
	// not pass through the mutator path. Confirm just in case so the
	// rest of the test reasons about a known stamp.
	got, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.UpdatedAt != stamp {
		t.Fatalf("UpdatedAt = %q, want %q (WriteInitial unexpectedly rewrote the stamp)",
			got.UpdatedAt, stamp)
	}
	dir, _ := mcp.ChangeDir(root, id)
	if err := os.WriteFile(filepath.Join(dir, "diff.patch"), diff, 0o600); err != nil {
		t.Fatalf("write diff.patch: %v", err)
	}
	return id
}

// TestRunRejectsInvalidIntervalHours covers the bounds check on
// cfg.IntervalHours. Both `0` (below) and `169` (above) cause Run to
// return the verbatim PRD R9 error and no goroutine spawns.
func TestRunRejectsInvalidIntervalHours(t *testing.T) {
	for _, hours := range []int{0, -1, 169, 10_000} {
		stop, err := Run(context.Background(), []Target{{InstanceRoot: t.TempDir()}},
			Config{IntervalHours: hours, AbandonDays: 14})
		if stop != nil {
			t.Errorf("hours=%d: stop = non-nil, want nil", hours)
		}
		if err == nil {
			t.Errorf("hours=%d: err = nil, want non-nil", hours)
			continue
		}
		want := "invalid gc_interval_hours: must be between 1 and 168"
		if err.Error() != want {
			t.Errorf("hours=%d: err = %q, want %q", hours, err.Error(), want)
		}
	}
}

// TestRunRejectsInvalidAbandonDays covers the bounds check on
// cfg.AbandonDays. Out-of-range values cause Run to return the
// documented error and no goroutine spawns.
func TestRunRejectsInvalidAbandonDays(t *testing.T) {
	for _, days := range []int{0, -1, 366, 10_000} {
		stop, err := Run(context.Background(), []Target{{InstanceRoot: t.TempDir()}},
			Config{IntervalHours: 6, AbandonDays: days})
		if stop != nil {
			t.Errorf("days=%d: stop = non-nil, want nil", days)
		}
		if err == nil {
			t.Errorf("days=%d: err = nil, want non-nil", days)
			continue
		}
		want := "invalid gc_abandon_days: must be between 1 and 365"
		if err.Error() != want {
			t.Errorf("days=%d: err = %q, want %q", days, err.Error(), want)
		}
	}
}

// TestSweepBoundary confirms a pending change is swept exactly at the
// gc_abandon_days threshold. The "boundary" semantics: a change whose
// UpdatedAt is exactly cfg.AbandonDays days old is swept, while a
// change one second younger is preserved. Uses fake clock arithmetic
// (now - 14d).
func TestSweepBoundary(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	atThreshold := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-time.Duration(cfg.AbandonDays)*24*time.Hour),
		[]byte("diff at threshold"))
	justUnder := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-time.Duration(cfg.AbandonDays)*24*time.Hour+time.Second),
		[]byte("diff just under"))

	sweepOnce(now, root, sink, cfg)

	atRead, err := mcp.Read(root, atThreshold)
	if err != nil {
		t.Fatalf("Read atThreshold: %v", err)
	}
	if atRead.State != mcp.ChangeStateCleaned {
		t.Errorf("at-threshold change state = %q, want cleaned", atRead.State)
	}
	underRead, err := mcp.Read(root, justUnder)
	if err != nil {
		t.Fatalf("Read justUnder: %v", err)
	}
	if underRead.State != mcp.ChangeStatePending {
		t.Errorf("just-under-threshold change state = %q, want pending", underRead.State)
	}
}

// TestSweepReapsStaleInReview confirms that an in-review change older
// than AbandonDays is swept to cleaned, with diff.patch removed and a
// change_cleaned event emitted. This is the safety net for the F5/F10
// gap: in-review has no verdict-cast exit until F10 ships, so the
// sweep treats stale in-review as the same kind of abandonment as
// stale pending. PRD R9 originally said "only pending is swept"; that
// rationale assumed F10's verdict-cast was imminent.
func TestSweepReapsStaleInReview(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	veryOld := now.Add(-365 * 24 * time.Hour)
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStateInReview, veryOld, []byte("ir"))

	sweepOnce(now, root, sink, cfg)

	got, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.State != mcp.ChangeStateCleaned {
		t.Errorf("state = %q, want cleaned", got.State)
	}
	dir, _ := mcp.ChangeDir(root, id)
	if _, err := os.Stat(filepath.Join(dir, "diff.patch")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("diff.patch not removed: %v", err)
	}
	if got := sink.byEvent(mcp.ChangeEventCleaned); len(got) != 1 {
		t.Errorf("change_cleaned event count = %d, want 1", len(got))
	}
}

// TestSweepNeverTouchesVerdictCast confirms changes in verdict-cast
// state are preserved regardless of UpdatedAt age — once a human has
// attested via F10's verdict cast, the change must persist through to
// whatever F10's lifecycle continues into.
func TestSweepNeverTouchesVerdictCast(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	veryOld := now.Add(-365 * 24 * time.Hour)
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStateVerdictCast, veryOld, []byte("vc"))

	sweepOnce(now, root, sink, cfg)

	got, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.State != mcp.ChangeStateVerdictCast {
		t.Errorf("state = %q, want verdict-cast", got.State)
	}
	dir, _ := mcp.ChangeDir(root, id)
	if _, err := os.Stat(filepath.Join(dir, "diff.patch")); err != nil {
		t.Errorf("diff.patch removed: %v", err)
	}
	if got := sink.byEvent(mcp.ChangeEventCleaned); len(got) != 0 {
		t.Errorf("change_cleaned event emitted for verdict-cast change: %+v", got)
	}
}

// TestSweepReclaimsLeakedDiffOnCleanedChange covers issue #10's
// idempotency gap: if a prior sweep crashed between sweepChange's
// state mutation (step 1) and its diff.patch removal (step 2), the
// next on-boot sweep encounters a cleaned change with a leaked diff.
// The reclaimer must drop the diff without re-emitting events.
func TestSweepReclaimsLeakedDiffOnCleanedChange(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	// Seed a change directly in cleaned state with diff.patch still on
	// disk — simulating the post-crash mid-sweep layout.
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStateCleaned, now, []byte("leaked diff"))

	sweepOnce(now, root, sink, cfg)

	dir, _ := mcp.ChangeDir(root, id)
	if _, err := os.Stat(filepath.Join(dir, "diff.patch")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("leaked diff.patch not reclaimed: %v", err)
	}
	// No event re-emit: the original sweep may have emitted, may not
	// have, and the reclaimer can't tell. Idempotency is silent.
	if got := sink.byEvent(mcp.ChangeEventCleaned); len(got) != 0 {
		t.Errorf("reclaim emitted spurious change_cleaned event: %+v", got)
	}
	// state.json must still say cleaned.
	st, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after reclaim: %v", err)
	}
	if st.State != mcp.ChangeStateCleaned {
		t.Errorf("state = %q after reclaim, want cleaned", st.State)
	}
}

// TestChangeCleanedPayload confirms the audit event carries the PRD R5
// shape: change_id, reason="abandoned_after_n_days", n_days=AbandonDays.
func TestChangeCleanedPayload(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-30*24*time.Hour),
		[]byte("old diff"))

	sweepOnce(now, root, sink, cfg)

	events := sink.byEvent(mcp.ChangeEventCleaned)
	if len(events) != 1 {
		t.Fatalf("change_cleaned event count = %d, want 1", len(events))
	}
	e := events[0]
	if e.Kind != "event" {
		t.Errorf("audit Kind = %q, want event", e.Kind)
	}
	if e.Payload["change_id"] != id {
		t.Errorf("payload change_id = %v, want %q", e.Payload["change_id"], id)
	}
	if e.Payload["reason"] != "abandoned_after_n_days" {
		t.Errorf("payload reason = %v, want abandoned_after_n_days", e.Payload["reason"])
	}
	// JSON numeric round-trip: n_days flows through map[string]any, so
	// the int we emitted remains an int (the same address space round
	// trip — no JSON marshalling here because the sink is in-memory).
	if e.Payload["n_days"] != cfg.AbandonDays {
		t.Errorf("payload n_days = %v, want %d", e.Payload["n_days"], cfg.AbandonDays)
	}
}

// TestSweepRemovesDiffPreservesStateJSON: after a successful sweep,
// `diff.patch` is gone but `state.json` remains on disk so
// post-mortem forensics survives.
func TestSweepRemovesDiffPreservesStateJSON(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-30*24*time.Hour),
		[]byte("old diff body"))

	sweepOnce(now, root, sink, cfg)

	dir, _ := mcp.ChangeDir(root, id)

	// diff.patch must be gone.
	if _, err := os.Stat(filepath.Join(dir, "diff.patch")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("diff.patch should be removed; Stat err = %v", err)
	}
	// state.json must still exist and reflect state=cleaned.
	st, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after sweep: %v", err)
	}
	if st.State != mcp.ChangeStateCleaned {
		t.Errorf("state = %q, want cleaned", st.State)
	}
}

// TestRunPerformsOnBootSweepSynchronously: the on-boot sweep runs
// before Run returns, so a caller that constructs Run on an already-
// abandoned pending change sees state=cleaned immediately.
func TestRunPerformsOnBootSweepSynchronously(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-30*24*time.Hour),
		[]byte("old diff"))

	stop, err := Run(context.Background(), []Target{{InstanceRoot: root, Sink: sink}}, cfg)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	defer stop()

	st, err := mcp.Read(root, id)
	if err != nil {
		t.Fatalf("Read after Run: %v", err)
	}
	if st.State != mcp.ChangeStateCleaned {
		t.Errorf("state after Run = %q, want cleaned (on-boot sweep must be synchronous)",
			st.State)
	}
}

// TestTickerExitsWithin100msOfCtxDone: cancelling the caller's ctx
// shuts the goroutine down within 100 ms. The stop() helper blocks
// until the goroutine drains, so we measure the time between cancel
// and stop's return as a proxy for the exit latency.
func TestTickerExitsWithin100msOfCtxDone(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}

	ctx, cancel := context.WithCancel(context.Background())
	stop, err := Run(ctx, []Target{{InstanceRoot: root, Sink: sink}},
		Config{IntervalHours: 1, AbandonDays: 14})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	cancel()
	start := time.Now()
	stop()
	elapsed := time.Since(start)
	if elapsed > 100*time.Millisecond {
		t.Errorf("ticker exit took %v, want <100ms", elapsed)
	}
}

// TestSweepIgnoresNonDirectoryEntries confirms files at the top of
// `.niwa/changes/` (e.g. handlers_change.go's per-session create lock
// `.session-<sid>.create.lock`) do not poison the sweep.
func TestSweepIgnoresNonDirectoryEntries(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	changesDir := mcp.ChangesDir(root)
	if err := os.MkdirAll(changesDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(changesDir, ".session-abcdef01.create.lock"),
		[]byte(""), 0o600); err != nil {
		t.Fatalf("write fake lock: %v", err)
	}

	// Should not panic, should not produce any change_cleaned events.
	sweepOnce(time.Now(), root, sink, cfg)

	if got := sink.byEvent(mcp.ChangeEventCleaned); len(got) != 0 {
		t.Errorf("change_cleaned emitted for non-directory entry: %+v", got)
	}
}

// TestSweepLogsErrorsButContinues confirms a corrupt change directory
// does not abort the sweep. We seed two pending changes, manually
// corrupt the first one's state.json (truncate it), then assert the
// second one still gets swept.
func TestSweepLogsErrorsButContinues(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	// First change: corrupt state.json so mcp.Read fails.
	corruptID, err := mcp.ReserveChangeID(root)
	if err != nil {
		t.Fatalf("ReserveChangeID: %v", err)
	}
	corruptDir, _ := mcp.ChangeDir(root, corruptID)
	if err := os.WriteFile(filepath.Join(corruptDir, "state.json"),
		[]byte("not json"), 0o600); err != nil {
		t.Fatalf("write corrupt state.json: %v", err)
	}

	// Second change: healthy + abandoned.
	healthyID := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-30*24*time.Hour),
		[]byte("body"))

	// Silence log output so the test report stays clean. log.Default()
	// writes to stderr; redirect to /dev/null for the duration.
	devnull, _ := os.Open(os.DevNull)
	defer devnull.Close()

	sweepOnce(now, root, sink, cfg)

	// Healthy change must still be swept despite the corrupt sibling.
	st, err := mcp.Read(root, healthyID)
	if err != nil {
		t.Fatalf("Read healthy: %v", err)
	}
	if st.State != mcp.ChangeStateCleaned {
		t.Errorf("healthy change state = %q, want cleaned", st.State)
	}
	// Exactly one change_cleaned event (the healthy one).
	if got := sink.byEvent(mcp.ChangeEventCleaned); len(got) != 1 {
		t.Errorf("change_cleaned event count = %d, want 1", len(got))
	}
}

// TestSweepEmitsTransitionEntry confirms the dual-target emitter wrote
// a transitions.log entry alongside the audit sink line.
func TestSweepEmitsTransitionEntry(t *testing.T) {
	root := t.TempDir()
	sink := &recordingSink{}
	cfg := Config{IntervalHours: 6, AbandonDays: 14}

	now := time.Now().UTC()
	id := seedChangeWithUpdatedAt(t, root,
		mcp.ChangeStatePending,
		now.Add(-30*24*time.Hour),
		[]byte("body"))

	sweepOnce(now, root, sink, cfg)

	dir, _ := mcp.ChangeDir(root, id)
	data, err := os.ReadFile(filepath.Join(dir, "transitions.log"))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	if !strings.Contains(string(data), "change_cleaned") {
		t.Errorf("transitions.log missing change_cleaned line; got:\n%s", data)
	}
	if !strings.Contains(string(data), "abandoned_after_n_days") {
		t.Errorf("transitions.log missing reason; got:\n%s", data)
	}
}

// TestStopIsIdempotent confirms calling stop twice does not deadlock
// or panic. The sync.Once inside Run guards the cancel/wait pair.
func TestStopIsIdempotent(t *testing.T) {
	root := t.TempDir()
	stop, err := Run(context.Background(), []Target{{InstanceRoot: root}},
		Config{IntervalHours: 1, AbandonDays: 14})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	stop()
	stop()
}
