package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// memSink is a tiny AuditSink for tests. It records every AuditEntry
// Emit receives and optionally returns a sentinel error so the
// dual-target failure isolation can be exercised.
type memSink struct {
	entries []AuditEntry
	failWith error
}

func (m *memSink) Emit(e AuditEntry) error {
	m.entries = append(m.entries, e)
	return m.failWith
}

// TestAppendChangeEvent_FansToBothTargets is the canonical happy path:
// one event with ChangeID set produces exactly one transitions.log
// line inside the change directory AND exactly one in-memory audit
// entry.
func TestAppendChangeEvent_FansToBothTargets(t *testing.T) {
	root := t.TempDir()
	id, dir := reserveAndWriteInitial(t, root)
	sink := &memSink{}

	err := AppendChangeEvent(root, sink, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id, "url": "http://127.0.0.1:9999/changes/" + id},
	})
	if err != nil {
		t.Fatalf("AppendChangeEvent: %v", err)
	}

	// transitions.log: one line, full payload.
	data, err := os.ReadFile(filepath.Join(dir, transitionsLogFileName))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d transitions lines, want 1: %q", len(lines), string(data))
	}
	var entry changeLogEntry
	if err := json.Unmarshal([]byte(lines[0]), &entry); err != nil {
		t.Fatalf("parse transitions line: %v", err)
	}
	if entry.Event != ChangeEventReady {
		t.Errorf("transitions entry Event = %q, want %q", entry.Event, ChangeEventReady)
	}
	if entry.ChangeID != id {
		t.Errorf("transitions entry ChangeID = %q, want %q", entry.ChangeID, id)
	}
	if entry.Payload["url"] != "http://127.0.0.1:9999/changes/"+id {
		t.Errorf("transitions entry Payload missing url: %v", entry.Payload)
	}

	// audit: one entry, mirrored Kind=event Event=change_ready.
	if len(sink.entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(sink.entries))
	}
	ae := sink.entries[0]
	if ae.Kind != "event" {
		t.Errorf("audit Kind = %q, want event", ae.Kind)
	}
	if ae.Event != ChangeEventReady {
		t.Errorf("audit Event = %q, want %q", ae.Event, ChangeEventReady)
	}
	if !ae.OK {
		t.Errorf("audit OK = false, want true")
	}
	if ae.Role != "" || ae.TaskID != "" {
		t.Errorf("audit Role/TaskID expected zero, got %q / %q", ae.Role, ae.TaskID)
	}
}

// TestAppendChangeEvent_EmptyChangeIDSkipsTransitions covers the
// review_surface_opened case: ChangeID is empty so the per-change
// branch must skip without error, while the audit emit still runs.
func TestAppendChangeEvent_EmptyChangeIDSkipsTransitions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := &memSink{}
	err := AppendChangeEvent(root, sink, ChangeEvent{
		Kind:    ChangeEventSurfaceOpened,
		Payload: map[string]any{"reviewer": "ops"},
	})
	if err != nil {
		t.Fatalf("AppendChangeEvent: %v", err)
	}
	if len(sink.entries) != 1 {
		t.Fatalf("got %d audit entries, want 1", len(sink.entries))
	}
	if sink.entries[0].Event != ChangeEventSurfaceOpened {
		t.Errorf("audit Event = %q, want %q", sink.entries[0].Event, ChangeEventSurfaceOpened)
	}
	// And nothing should exist under .niwa/changes/ — the empty
	// ChangeID must not trigger a directory traversal or write.
	if _, err := os.Stat(filepath.Join(root, ".niwa", changesDirName)); !os.IsNotExist(err) {
		t.Errorf("changes/ dir should not exist after surface_opened with empty ChangeID, got err = %v", err)
	}
}

// TestAppendChangeEvent_OverBudgetAuditDowngradeKeepsTransitionsFull
// is the cross-issue acceptance criterion from PLAN #4: an over-2KB
// payload must downgrade on the audit side (payload={}, error_code=
// payload_too_large) while the per-change line stays full.
func TestAppendChangeEvent_OverBudgetAuditDowngradeKeepsTransitionsFull(t *testing.T) {
	root := t.TempDir()
	id, dir := reserveAndWriteInitial(t, root)
	// Use the real fileAuditSink so the 2 KB budget enforcement runs
	// end-to-end (memSink would bypass it).
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	big := strings.Repeat("x", 4096)
	err := AppendChangeEvent(root, sink, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id, "blob": big},
	})
	if err != nil {
		t.Fatalf("AppendChangeEvent: %v", err)
	}

	// Audit line: downgraded payload + error_code.
	auditBytes, err := os.ReadFile(filepath.Join(root, ".niwa", auditLogFileName))
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	if !strings.Contains(string(auditBytes), `"payload":{}`) {
		t.Errorf("audit line missing payload={} downgrade: %s", auditBytes)
	}
	if !strings.Contains(string(auditBytes), `"error_code":"payload_too_large"`) {
		t.Errorf("audit line missing payload_too_large code: %s", auditBytes)
	}
	if strings.Contains(string(auditBytes), big) {
		t.Errorf("oversized blob leaked into audit line")
	}

	// transitions.log line: full payload, no downgrade.
	transBytes, err := os.ReadFile(filepath.Join(dir, transitionsLogFileName))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	if !strings.Contains(string(transBytes), big) {
		t.Errorf("transitions line should retain full payload, did not contain blob")
	}
	if strings.Contains(string(transBytes), "payload_too_large") {
		t.Errorf("transitions line should not carry payload_too_large code")
	}
}

// TestAppendChangeEvent_AuditFailureDoesNotSkipTransitions exercises
// the "errors from one target do not skip the other" contract: the
// audit sink returns a sentinel error, but the transitions.log line
// still lands on disk and the helper's returned error surfaces the
// audit failure.
func TestAppendChangeEvent_AuditFailureDoesNotSkipTransitions(t *testing.T) {
	root := t.TempDir()
	id, dir := reserveAndWriteInitial(t, root)
	sentinel := errors.New("audit sink wedged")
	sink := &memSink{failWith: sentinel}

	err := AppendChangeEvent(root, sink, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id},
	})
	if err == nil {
		t.Fatal("AppendChangeEvent err = nil, want sentinel")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("err = %v, want errors.Is sentinel", err)
	}
	// transitions.log line is still on disk despite the audit failure.
	data, err := os.ReadFile(filepath.Join(dir, transitionsLogFileName))
	if err != nil {
		t.Fatalf("read transitions.log: %v", err)
	}
	if len(data) == 0 {
		t.Errorf("transitions.log empty; audit failure incorrectly aborted the per-change write")
	}
}

// TestAppendChangeEvent_TransitionsFailureDoesNotSkipAudit covers the
// inverse: a transitions.log write failure (unwritable change
// directory) must not prevent the audit emit. The audit sink records
// the entry while the helper surfaces the transitions failure.
func TestAppendChangeEvent_TransitionsFailureDoesNotSkipAudit(t *testing.T) {
	root := t.TempDir()
	// Use a UUIDv4 id that does NOT exist on disk. The flock open
	// inside appendChangeTransition will fail because the change
	// directory was never created (openChangeLock returns os.ErrNotExist
	// wrapped). The audit emit must still run.
	id, err := uuidV4Generator()
	if err != nil {
		t.Fatalf("uuidV4Generator: %v", err)
	}
	sink := &memSink{}

	err = AppendChangeEvent(root, sink, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: id,
		Payload:  map[string]any{"change_id": id},
	})
	if err == nil {
		t.Fatal("AppendChangeEvent err = nil, want transitions error")
	}
	if len(sink.entries) != 1 {
		t.Errorf("audit entries = %d, want 1 (audit must run despite transitions failure)", len(sink.entries))
	}
}

// TestAppendChangeEvent_RejectsInvalidChangeID confirms path-traversal
// defense propagates from ChangeDir into AppendChangeEvent.
func TestAppendChangeEvent_RejectsInvalidChangeID(t *testing.T) {
	root := t.TempDir()
	sink := &memSink{}
	err := AppendChangeEvent(root, sink, ChangeEvent{
		Kind:     ChangeEventReady,
		ChangeID: "../etc/passwd",
		Payload:  map[string]any{},
	})
	if err == nil {
		t.Fatal("AppendChangeEvent with traversal id: err = nil, want rejection")
	}
	// audit was still attempted; that's by design — the workspace-wide
	// observer should record the attempted event even if the per-change
	// branch refused. The PLAN's #4 acceptance treats audit-and-
	// transitions independently.
	if len(sink.entries) != 1 {
		t.Errorf("audit entries = %d, want 1 (audit always attempted)", len(sink.entries))
	}
}
