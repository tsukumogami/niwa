// changelog.go is the dual-target emitter for F5 change-lifecycle events.
// One event is fanned to two destinations:
//
//  1. Per-change transitions.log under `.niwa/changes/<id>/`,
//     appended NDJSON under the per-change exclusive flock with
//     O_APPEND|O_NOFOLLOW + fsync. Skipped when ChangeID is empty
//     (the `review_surface_opened` case, which is not bound to a
//     single change).
//
//  2. Workspace-wide audit log at `.niwa/mcp-audit.log` via the
//     AuditSink interface. Audit entries are v=2, Kind="event", with
//     Event=<change-kind> and Payload=<caller-supplied>; the 2 KB
//     payload budget enforced by fileAuditSink.Emit downgrades over-
//     budget entries (only on the audit side — the per-change line
//     is unconditional and carries the full payload).
//
// A failure on one target does NOT skip the other. The helper returns
// errors.Join(audit_err, transitions_err) so callers can observe both
// failures while neither tear-down blocks the other. The order
// (transitions first, then audit) reflects the per-change view being
// the authoritative timeline for that change's history — losing an
// audit line is less consequential than losing the per-change entry.
//
// The DESIGN's original signature took `*fileAuditSink` directly. This
// file widens that to the narrow `AuditSink` interface so callers
// outside the `mcp` package — specifically `internal/web/gc/` (Issue
// #9) — can invoke the helper without forcing fileAuditSink to be
// exported. The narrowing is the only inter-package contract the
// design did not pin (acknowledged in the PLAN's "Notes for the
// Implementer").

package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AuditSink is the narrow contract AppendChangeEvent needs from an
// audit destination. The concrete *fileAuditSink in audit.go satisfies
// it. Tests use an in-memory implementation; the GC sweep (issue #9)
// passes the same *fileAuditSink the listener constructed at boot.
type AuditSink interface {
	Emit(AuditEntry) error
}

// ChangeEvent carries one F5 change-lifecycle event. Kind is the
// canonical event-kind string (see the ChangeEvent* constants below);
// ChangeID is the UUIDv4 the event refers to (empty for events not
// bound to a single change); Payload is the structured event data the
// caller owns — never LLM-supplied tool argument values.
type ChangeEvent struct {
	Kind     string
	ChangeID string
	Payload  map[string]any
}

// Event-kind constants. The four F5 kinds correspond to:
//
//   - change_ready          — change_create handler wrote state.json
//     and diff.patch under .niwa/changes/<id>/.
//   - review_surface_opened — GET /changes/ index page rendered. Not
//     bound to a single change; emitted by the listener with
//     ChangeID="".
//   - change_engaged        — GET /changes/<id> handler rendered the
//     per-change view. Emitted per HTTP hit, not per state transition,
//     so the count reflects reviewer attention rather than the
//     pending → in-review mutation.
//   - change_cleaned        — GC sweep moved a pending change past the
//     gc_abandon_days threshold to cleaned and removed diff.patch.
const (
	ChangeEventReady          = "change_ready"
	ChangeEventSurfaceOpened  = "review_surface_opened"
	ChangeEventEngaged        = "change_engaged"
	ChangeEventCleaned        = "change_cleaned"
	transitionsLogFileName    = "transitions.log"
	changeEventKindAuditValue = "event"
)

// changeLogEntry is the on-disk NDJSON shape for a per-change
// transitions.log line. V is bumped if the schema evolves; today only
// v=1 is written. Kind is always "event" at F5 — the substrate has no
// non-event transitions to log (state.json carries those).
type changeLogEntry struct {
	V        int            `json:"v"`
	At       string         `json:"at"`
	Kind     string         `json:"kind"`
	Event    string         `json:"event"`
	ChangeID string         `json:"change_id"`
	Payload  map[string]any `json:"payload"`
}

// AppendChangeEvent fans one change-lifecycle event to the per-change
// transitions.log and to the workspace audit log. Both targets are
// attempted unconditionally; failures are joined via errors.Join so
// callers can observe both. The order is per-change first, audit
// second — the per-change view is the authoritative timeline for that
// change.
//
// When e.ChangeID is empty (review_surface_opened), the per-change
// branch is skipped and only the audit emit runs. The audit entry
// carries Kind="event", Event=e.Kind, Payload=e.Payload, OK=true; Role
// and TaskID are zero (this is a workspace-level event, not bound to
// the per-session NIWA_TASK_ID context).
func AppendChangeEvent(instanceRoot string, sink AuditSink, e ChangeEvent) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)

	var transitionsErr error
	if e.ChangeID != "" {
		transitionsErr = appendChangeTransition(instanceRoot, e, now)
	}

	var auditErr error
	if sink != nil {
		auditErr = sink.Emit(AuditEntry{
			V:       2,
			At:      now,
			Kind:    changeEventKindAuditValue,
			Event:   e.Kind,
			Payload: e.Payload,
			OK:      true,
		})
	}

	return errors.Join(transitionsErr, auditErr)
}

// appendChangeTransition writes one NDJSON line to
// `<instanceRoot>/.niwa/changes/<id>/transitions.log` under the
// per-change exclusive flock. UUIDv4 validation on e.ChangeID happens
// up-front (defense-in-depth; the typical caller resolves the id from
// state.json so it is already trusted, but a bug in a future caller
// path must not turn into a filesystem write).
//
// O_APPEND atomicity on Linux makes the write itself crash-safe for
// payloads under PIPE_BUF (~4 KiB). The flock is held for the duration
// of the open+write+fsync so a concurrent UpdateChangeState writer
// (taking the exclusive lock for state.json mutations) cannot
// interleave with this log append on the same change.
func appendChangeTransition(instanceRoot string, e ChangeEvent, at string) error {
	dir, err := ChangeDir(instanceRoot, e.ChangeID)
	if err != nil {
		return err
	}
	lf, err := openChangeLock(dir)
	if err != nil {
		return err
	}
	defer lf.Close()
	if err := acquireFlock(lf, true); err != nil {
		return err
	}
	defer func() { _ = releaseFlock(lf) }()

	entry := changeLogEntry{
		V:        1,
		At:       at,
		Kind:     changeEventKindAuditValue,
		Event:    e.Kind,
		ChangeID: e.ChangeID,
		Payload:  e.Payload,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal change log entry: %w", err)
	}
	data = append(data, '\n')

	path := filepath.Join(dir, transitionsLogFileName)
	f, err := os.OpenFile(path,
		os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync %s: %w", path, err)
	}
	return f.Close()
}
