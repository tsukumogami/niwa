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

// Run validates cfg, performs the synchronous on-boot sweep, and spawns
// the ticker goroutine. Returns a stop function that cancels the
// ticker and blocks until the goroutine exits.
//
// The caller may also cancel ctx to drive shutdown; the goroutine
// observes ctx.Done() and exits without the caller ever calling stop.
// In that case stop returns immediately because the goroutine has
// already drained the done channel.
//
// Returning an error elides the goroutine entirely: a misconfigured
// gc_interval_hours causes `niwa surface serve` to exit 1 at boot per
// PRD R9 before the listener accepts requests.
func Run(ctx context.Context, instanceRoot string, sink mcp.AuditSink, cfg Config) (func(), error) {
	if cfg.IntervalHours < 1 || cfg.IntervalHours > 168 {
		return nil, errors.New(errInvalidIntervalHours)
	}
	if cfg.AbandonDays < 1 || cfg.AbandonDays > 365 {
		return nil, errors.New(errInvalidAbandonDays)
	}

	sweepOnce(time.Now(), instanceRoot, sink, cfg)

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
				sweepOnce(t, instanceRoot, sink, cfg)
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

// sweepOnce iterates `.niwa/changes/` and reclaims abandoned pending
// changes. Skips non-pending entries up-front; non-pending changes are
// never auto-cleaned per PRD R9 (an in-review change has a reviewer
// attached, and verdict-cast is F10's terminal state).
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
		if st.State != mcp.ChangeStatePending {
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
			if cur.State != mcp.ChangeStatePending {
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
