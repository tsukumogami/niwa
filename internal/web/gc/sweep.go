// Package gc implements the F5 niwa-surface GC sweep.
//
// One synchronous on-boot sweep runs inside Run before any ticker
// spawns; thereafter a ticker fires every cfg.IntervalHours hours and
// runs the same sweepOnce body. Each sweep iterates `.niwa/changes/`
// and, for every change in `pending` whose UpdatedAt is older than
// cfg.AbandonDays days, advances the state to `cleaned` (under the
// per-change exclusive flock — UpdateChangeState owns the write
// discipline), removes diff.patch to reclaim disk, and emits the
// `change_cleaned` audit event (PRD R5 payload shape).
//
// Failure handling: errors during a single change's sweep are logged
// but do not abort the sweep. `state.json` and `transitions.log` are
// preserved for forensics even after the diff is dropped — only the
// disk-heavy `diff.patch` is reclaimed.
//
// The package is loopback-only: no network calls, no spawned
// subprocesses. The mcp.AuditSink dependency is the only inter-package
// import, and AppendChangeEvent's narrow interface lets tests pass a
// memory sink.
package gc

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/tsukumogami/niwa/internal/mcp"
)

// Config carries the sweep schedule. IntervalHours ∈ [1, 168] (PRD R9
// — 1 hour to 1 week between sweeps). AbandonDays ∈ [1, 365] — the
// threshold past which a pending change is considered abandoned.
type Config struct {
	IntervalHours int
	AbandonDays   int
}

// PRD R9 verbatim error wordings. The gc_interval_hours wording is
// spelled out in the PRD; gc_abandon_days follows the same parallel
// form for symmetry — the PRD only states "out-of-range exit 1".
const (
	errInvalidIntervalHours = "invalid gc_interval_hours: must be between 1 and 168"
	errInvalidAbandonDays   = "invalid gc_abandon_days: must be between 1 and 365"
)

// diffPatchFileName names the per-change unified-diff snapshot the
// niwa_create_change MCP handler writes alongside state.json. The
// sweep removes this file on pending → cleaned to reclaim disk;
// `state.json` and `transitions.log` are preserved for forensics.
const diffPatchFileName = "diff.patch"

// Target is one (instanceRoot, sink) pair the sweep visits. The
// machine-scope surface aggregates several of these — one per
// discovered niwa instance under each registered workspace.
type Target struct {
	InstanceRoot string
	Sink         mcp.AuditSink
}

// Run validates cfg, performs one synchronous on-boot sweep across every
// supplied target, and spawns a single ticker goroutine that iterates
// the same target list each tick. Returns a stop function that cancels
// the ticker and blocks until the goroutine exits.
//
// The caller may also cancel ctx to drive shutdown; the goroutine
// observes ctx.Done() and exits without the caller ever calling stop.
// In that case stop returns immediately because the goroutine has
// already drained the done channel.
//
// Returning an error elides the goroutine entirely: a misconfigured
// gc_interval_hours causes `niwa surface serve` to exit 1 at boot per
// PRD R9 before the listener accepts requests. An empty target list is
// not an error — the sweep is a no-op, the ticker still runs in case
// instances appear later (next-restart pickup, not hot-reload).
func Run(ctx context.Context, targets []Target, cfg Config) (func(), error) {
	if cfg.IntervalHours < 1 || cfg.IntervalHours > 168 {
		return nil, errors.New(errInvalidIntervalHours)
	}
	if cfg.AbandonDays < 1 || cfg.AbandonDays > 365 {
		return nil, errors.New(errInvalidAbandonDays)
	}

	sweepAll(time.Now(), targets, cfg)

	// Internal cancellation: stop() short-circuits the goroutine
	// exit even when the caller's ctx is still live. ctx.Done()
	// continues to drive the same exit path.
	runCtx, cancel := context.WithCancel(ctx)
	ticker := time.NewTicker(time.Duration(cfg.IntervalHours) * time.Hour)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer ticker.Stop()
		for {
			select {
			case <-runCtx.Done():
				return
			case t := <-ticker.C:
				sweepAll(t, targets, cfg)
			}
		}
	}()

	var stopOnce sync.Once
	stop := func() {
		stopOnce.Do(func() {
			cancel()
			<-done
		})
	}
	return stop, nil
}

// sweepAll runs sweepOnce against every target. Errors in one target
// do not affect the others — sweepOnce already swallows per-change
// errors, and sweepAll inherits the same isolation discipline at the
// per-instance boundary.
func sweepAll(now time.Time, targets []Target, cfg Config) {
	for _, t := range targets {
		sweepOnce(now, t.InstanceRoot, t.Sink, cfg)
	}
}

// sweepOnce iterates `.niwa/changes/` and reclaims abandoned changes,
// plus reaps any leaked artefacts on already-cleaned changes from a
// prior crashed sweep.
//
// The sweep visits two categories of change:
//
//   - Pending changes older than AbandonDays — the original PRD R9
//     scope. A pending change has nobody attached to it; if AbandonDays
//     have passed without any HTTP hit advancing it to in-review, it is
//     abandoned and reclaimed.
//   - In-review changes older than AbandonDays — added because between
//     F5 ship and F10 ship the in-review state has no exit transition.
//     Any HTTP GET on a change flips pending → in-review under the per-
//     change flock, so stale bookmarks, search-bot crawls, or
//     accidental clicks can permanently park a change in in-review. The
//     same AbandonDays threshold applies: once F10 ships and a verdict
//     can advance the state, this branch becomes a safety net rather
//     than a primary path. PRD R9's original rationale ("an in-review
//     change has a reviewer attached") assumed F10 was imminent; until
//     then, in-review is otherwise immortal.
//   - Cleaned changes whose diff.patch is still on disk — defence
//     against a daemon that crashed between sweepChange's state
//     mutation (step 1) and its diff.patch removal (step 2). The
//     reclaim is idempotent.
//
// Per-entry errors are logged and the sweep continues — a single
// corrupt change directory must not poison the rest of the workspace's
// cleanup cadence.
func sweepOnce(now time.Time, instanceRoot string, sink mcp.AuditSink, cfg Config) {
	changesDir := mcp.ChangesDir(instanceRoot)
	entries, err := os.ReadDir(changesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		log.Printf("gc sweep: read %s: %v", changesDir, err)
		return
	}
	threshold := time.Duration(cfg.AbandonDays) * 24 * time.Hour
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		st, rerr := mcp.Read(instanceRoot, id)
		if rerr != nil {
			// Non-UUIDv4 names (the per-session create lock files in
			// handlers_change.go's `.session-<sid>.create.lock`)
			// surface here as Read errors and get skipped, same as
			// genuinely corrupt directories.
			continue
		}
		// Cleaned-change janitor: a prior sweep crashed between the
		// state mutation and the diff.patch removal. Probe and reclaim;
		// no event re-emit (the original event may or may not have
		// landed, and we can't distinguish without parsing the audit
		// log).
		if st.State == mcp.ChangeStateCleaned {
			reclaimCleanedDiff(instanceRoot, id)
			continue
		}
		// Pending and in-review are eligible for the AbandonDays sweep.
		// verdict-cast (F10) is intentionally not swept here — once F10
		// adds it, a verdict-cast change has a human attestation and
		// must persist through to whatever F10's lifecycle continues
		// into.
		if st.State != mcp.ChangeStatePending && st.State != mcp.ChangeStateInReview {
			continue
		}
		updated, perr := time.Parse(time.RFC3339Nano, st.UpdatedAt)
		if perr != nil {
			log.Printf("gc sweep: parse UpdatedAt for %s: %v", id, perr)
			continue
		}
		elapsed := now.Sub(updated)
		if elapsed < threshold {
			continue
		}
		if err := sweepChange(instanceRoot, id, cfg.AbandonDays, sink); err != nil {
			log.Printf("gc sweep: %s: %v", id, err)
		}
	}
}

// reclaimCleanedDiff probes for and removes a diff.patch leak on a
// change that is already in the `cleaned` state. Used by the on-boot
// sweep's idempotency pass so that a daemon that crashed between
// sweepChange's state mutation and its diff.patch removal eventually
// reclaims the disk on a later boot. No event emit; the audit
// substrate already has the original change_cleaned (if step 1
// succeeded) or never will (if step 1 was the failure point), and
// either way the file removal is the only reclaimable side-effect.
func reclaimCleanedDiff(instanceRoot, id string) {
	dir, derr := mcp.ChangeDir(instanceRoot, id)
	if derr != nil {
		return
	}
	path := filepath.Join(dir, diffPatchFileName)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Printf("gc sweep: reclaim cleaned diff %s: %v", id, err)
	}
}

// sweepChange applies the cleaning to one change. Order per PRD R9:
//
//  1. UpdateChangeState mutator: pending → cleaned. Idempotent under
//     the per-change flock; a concurrent state change between the scan
//     read and the mutator is observed by the mutator's `cur.State !=
//     pending` short-circuit.
//  2. Remove diff.patch to reclaim disk.
//  3. Emit `change_cleaned` audit event.
//
// Each step's failure is observable to the caller; sweepOnce logs and
// moves on rather than retrying.
func sweepChange(instanceRoot, id string, abandonDays int, sink mcp.AuditSink) error {
	updateErr := mcp.UpdateChangeState(instanceRoot, id,
		func(cur *mcp.ChangeState) (*mcp.ChangeState, error) {
			// Allow both pending and in-review: F10 has not yet added a
			// verdict-cast exit for in-review, and a stale in-review past
			// AbandonDays is the same kind of abandonment as a stale
			// pending. See sweepOnce's doc comment for the full
			// rationale.
			if cur.State != mcp.ChangeStatePending && cur.State != mcp.ChangeStateInReview {
				return nil, nil
			}
			next := *cur
			next.State = mcp.ChangeStateCleaned
			return &next, nil
		})
	if updateErr != nil {
		return fmt.Errorf("update state: %w", updateErr)
	}

	dir, derr := mcp.ChangeDir(instanceRoot, id)
	if derr != nil {
		return fmt.Errorf("resolve change dir: %w", derr)
	}
	diffPath := filepath.Join(dir, diffPatchFileName)
	if err := os.Remove(diffPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", diffPath, err)
	}

	// Best-effort event emit; AppendChangeEvent returns errors.Join of
	// transitions and audit failures. Per PRD R4/DESIGN D5 a failed
	// audit emit does not unwind the sweep — the disk reclamation
	// already happened and state.json is consistent.
	if err := mcp.AppendChangeEvent(instanceRoot, sink, mcp.ChangeEvent{
		Kind:     mcp.ChangeEventCleaned,
		ChangeID: id,
		Payload: map[string]any{
			"change_id": id,
			"reason":    "abandoned_after_n_days",
			"n_days":    abandonDays,
		},
	}); err != nil {
		return fmt.Errorf("emit change_cleaned: %w", err)
	}
	return nil
}
