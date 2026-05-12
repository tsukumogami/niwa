package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEffectiveKind(t *testing.T) {
	cases := []struct {
		name string
		e    AuditEntry
		want string
	}{
		{"explicit tool_call", AuditEntry{Kind: "tool_call", Tool: "x"}, "tool_call"},
		{"explicit event", AuditEntry{Kind: "event", Event: "change_ready"}, "event"},
		{"v1 fallback infers tool_call from Tool", AuditEntry{Tool: "niwa_delegate"}, "tool_call"},
		{"neither kind nor tool returns empty", AuditEntry{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.e.effectiveKind()
			if got != tc.want {
				t.Errorf("effectiveKind() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestReadAuditLog_BackwardCompatV1 hand-writes a v=1 NDJSON fixture that
// pre-dates the F5 schema (no kind, no payload) and verifies the reader
// loads every record and every effectiveKind() returns "tool_call".
func TestReadAuditLog_BackwardCompatV1(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	v1 := `{"v":1,"at":"2026-04-24T00:00:00Z","tool":"niwa_delegate","arg_keys":["to"],"ok":true}
{"v":1,"at":"2026-04-24T00:00:01Z","tool":"niwa_finish_task","arg_keys":["outcome","task_id"],"ok":true}
{"v":1,"at":"2026-04-24T00:00:02Z","tool":"niwa_cancel_task","arg_keys":["task_id"],"ok":false,"error_code":"TASK_NOT_FOUND"}
`
	if err := os.WriteFile(filepath.Join(dir, "mcp-audit.log"), []byte(v1), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	for i := range got {
		if got[i].effectiveKind() != "tool_call" {
			t.Errorf("entry %d effectiveKind = %q, want tool_call", i, got[i].effectiveKind())
		}
	}
}

// TestReadAuditLog_ForwardCompatMixed reads a fixture containing both v=1
// and v=2 records, asserting that v=2 event records report
// effectiveKind() == "event" and v=1 tool-call records report "tool_call".
func TestReadAuditLog_ForwardCompatMixed(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mixed := `{"v":1,"at":"2026-04-24T00:00:00Z","tool":"niwa_delegate","arg_keys":["to"],"ok":true}
{"v":2,"at":"2026-04-24T00:00:01Z","kind":"event","event":"change_ready","payload":{"change_id":"abc"},"ok":true}
{"v":2,"at":"2026-04-24T00:00:02Z","kind":"tool_call","tool":"niwa_create_change","arg_keys":["session_id"],"ok":true}
`
	if err := os.WriteFile(filepath.Join(dir, "mcp-audit.log"), []byte(mixed), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries, want 3", len(got))
	}
	want := []string{"tool_call", "event", "tool_call"}
	for i, w := range want {
		if got[i].effectiveKind() != w {
			t.Errorf("entry %d effectiveKind = %q, want %q", i, got[i].effectiveKind(), w)
		}
	}
	if got[1].Event != "change_ready" {
		t.Errorf("entry 1 Event = %q, want change_ready", got[1].Event)
	}
	if got[1].Payload["change_id"] != "abc" {
		t.Errorf("entry 1 Payload.change_id = %v, want abc", got[1].Payload["change_id"])
	}
}

// TestFileAuditSink_PayloadBudgetDowngrade emits an event whose payload
// marshals well over 2 KB; the on-disk line must have payload set to {}
// and error_code = "payload_too_large", while the caller's original
// AuditEntry must retain its full payload (Emit takes by value).
func TestFileAuditSink_PayloadBudgetDowngrade(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	big := strings.Repeat("x", 4096)
	entry := AuditEntry{
		Kind:    "event",
		Event:   "change_ready",
		Payload: map[string]any{"change_id": "abc", "blob": big},
		OK:      true,
	}
	if err := sink.Emit(entry); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	// Caller's record is untouched.
	if entry.Payload["blob"] != big {
		t.Errorf("caller's Payload was mutated by Emit")
	}
	if entry.ErrorCode != "" {
		t.Errorf("caller's ErrorCode was mutated by Emit: %q", entry.ErrorCode)
	}

	// On-disk line has payload={} and the downgrade code.
	data, err := os.ReadFile(filepath.Join(root, ".niwa", "mcp-audit.log"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), `"payload":{}`) {
		t.Errorf("expected payload={} in downgraded line, got: %s", data)
	}
	if !strings.Contains(string(data), `"error_code":"payload_too_large"`) {
		t.Errorf("expected error_code=payload_too_large in downgraded line, got: %s", data)
	}
	if strings.Contains(string(data), big) {
		t.Errorf("oversized payload leaked into log")
	}

	// Re-read via ReadAuditLog to confirm the downgraded entry parses.
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].ErrorCode != "payload_too_large" {
		t.Errorf("on-disk ErrorCode = %q, want payload_too_large", got[0].ErrorCode)
	}
	if len(got[0].Payload) != 0 {
		t.Errorf("on-disk Payload non-empty: %v", got[0].Payload)
	}
}

// TestFileAuditSink_SmallPayloadIntact ensures payloads under the budget
// pass through unchanged — guarding against an over-eager downgrade.
func TestFileAuditSink_SmallPayloadIntact(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	entry := AuditEntry{
		Kind:    "event",
		Event:   "change_ready",
		Payload: map[string]any{"change_id": "abc", "url": "http://127.0.0.1:9999/changes/abc"},
		OK:      true,
	}
	if err := sink.Emit(entry); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	if got[0].ErrorCode != "" {
		t.Errorf("ErrorCode set on a small payload: %q", got[0].ErrorCode)
	}
	if got[0].Payload["change_id"] != "abc" {
		t.Errorf("Payload lost change_id, got: %v", got[0].Payload)
	}
}

// TestBuildAuditEntry_KindToolCall verifies dispatch-built entries carry
// Kind="tool_call" and V=2.
func TestBuildAuditEntry_KindToolCall(t *testing.T) {
	res := toolResult{}
	entry := buildAuditEntry("coordinator", "", toolCallParams{Name: "niwa_delegate", Arguments: json.RawMessage(`{"to":"web"}`)}, res)
	if entry.Kind != "tool_call" {
		t.Errorf("Kind = %q, want tool_call", entry.Kind)
	}
	if entry.V != 2 {
		t.Errorf("V = %d, want 2", entry.V)
	}
	if entry.Tool != "niwa_delegate" {
		t.Errorf("Tool = %q, want niwa_delegate", entry.Tool)
	}
}
