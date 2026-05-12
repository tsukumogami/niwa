// audit.go provides per-MCP-call audit observability. The dispatch loop
// emits one entry per `tools/call` to a sink whose default implementation
// appends NDJSON lines to <instance-root>/.niwa/mcp-audit.log. The v=2
// schema (see DESIGN-niwa-change-primitive.md) deliberately captures
// argument *keys* but never *values* — LLM-supplied tool arguments
// routinely contain prompt injections, secrets, or PII, and leaking that
// into a logfile would re-create the exfiltration risks the rest of the
// design works to prevent. v=1 records on disk continue to parse cleanly;
// the reader's effectiveKind helper recovers the implicit "tool_call"
// classification from pre-F5 records.
package mcp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

// AuditEntry is the v=2 schema for an MCP audit record. One entry per
// `tools/call` request reaching dispatch is appended to .niwa/mcp-audit.log
// in emission order. F5 extends the schema with three optional fields
// (Kind, Event, Payload) so the same log can carry change-lifecycle events
// alongside tool-call entries; v=1 records on disk continue to parse
// cleanly (the new fields are all `omitempty`).
//
// Privacy: the schema deliberately captures argument *keys* but never
// *values*. The Payload field is intended for structured event data the
// emitter owns (e.g. {change_id, reason}), not LLM-supplied tool
// arguments. A 2 KB budget enforced by fileAuditSink.Emit prevents an
// over-payload from bloating the log; oversized entries are rewritten
// with Payload={} and error_code="payload_too_large" on disk.
type AuditEntry struct {
	V         int      `json:"v"`
	At        string   `json:"at"`
	Role      string   `json:"role,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Tool      string   `json:"tool,omitempty"`
	ArgKeys   []string `json:"arg_keys,omitempty"`
	OK        bool     `json:"ok"`
	ErrorCode string   `json:"error_code,omitempty"`
	Kind      string   `json:"kind,omitempty"`
	Event     string   `json:"event,omitempty"`
	// Payload is serialised without omitempty so the budget-downgrade path
	// can write a literal `"payload":{}` sentinel. An empty map with
	// `omitempty` would be elided by encoding/json's zero-length-map rule,
	// hiding the fact that the payload was dropped. Callers who do not
	// emit an event payload (tool_call entries) leave this nil, which
	// serialises as `"payload":null`.
	Payload map[string]any `json:"payload"`
}

// auditPayloadBudget caps a single serialised audit line. NDJSON lines
// over this size are rewritten with an empty Payload and the
// "payload_too_large" error code so the log stays scannable and no
// single event can blow out tail-following tools. The 2 KiB ceiling
// sits well below the Linux PIPE_BUF 4096-byte atomicity guarantee.
const auditPayloadBudget = 2048

// fileAuditSink appends NDJSON lines to a fixed path. Linux guarantees
// O_APPEND writes smaller than PIPE_BUF (~4096 bytes) are atomic, so
// concurrent writers from multiple processes never tear or interleave.
// Audit entries are well under that limit. A zero-value path is the
// no-op sentinel: callers (and the unit-test setups that exercise
// individual handlers without a workspace) get a sink whose Emit silently
// returns without touching the filesystem.
type fileAuditSink struct {
	path string
	mu   sync.Mutex
}

// NewFileAuditSink returns a sink that appends NDJSON to
// <instanceRoot>/.niwa/mcp-audit.log. When instanceRoot is empty, returns
// a sink with an empty path; its Emit is a no-op so callers never have
// to special-case the no-workspace path. The .niwa/ directory is expected
// to exist (provisioned by `niwa create`); if it doesn't, Emit returns
// an error which dispatch swallows silently.
func NewFileAuditSink(instanceRoot string) *fileAuditSink {
	if instanceRoot == "" {
		return &fileAuditSink{}
	}
	return &fileAuditSink{
		path: filepath.Join(instanceRoot, ".niwa", auditLogFileName),
	}
}

const auditLogFileName = "mcp-audit.log"

// Emit appends one NDJSON line. Best-effort: a failed open or write
// returns the error to the caller, which is expected to ignore it so a
// degraded sink never breaks a real tool call. The mutex serializes
// in-process emits so the file pointer can't race; cross-process safety
// rests on O_APPEND atomicity.
//
// The 2 KB payload budget: the caller's AuditEntry value is taken by
// value, so any mutation here (autofilling V/At, dropping an oversize
// Payload) stays local to this method — the caller's record is never
// modified. If the marshalled line exceeds auditPayloadBudget, Emit
// rewrites the entry with Payload set to an empty map and ErrorCode set
// to "payload_too_large", re-marshals, and writes the smaller buffer.
func (s *fileAuditSink) Emit(e AuditEntry) error {
	if s.path == "" {
		return nil
	}
	if e.V == 0 {
		e.V = 2
	}
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if len(data) > auditPayloadBudget {
		e.Payload = map[string]any{}
		e.ErrorCode = "payload_too_large"
		data, err = json.Marshal(e)
		if err != nil {
			return err
		}
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path,
		os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) {
			return errors.New("symlink at audit log path: " + s.path)
		}
		return err
	}
	if _, werr := f.Write(data); werr != nil {
		_ = f.Close()
		return werr
	}
	return f.Close()
}

// extractArgKeys returns the sorted top-level keys present in raw, or an
// empty slice if raw is empty/null/non-object. The unmarshaling discards
// values immediately; this is the only place argument JSON is examined for
// the audit path, so values never escape this function's stack frame.
func extractArgKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return []string{}
	}
	// Trim surrounding whitespace so a literal "null" payload (with or
	// without spaces) returns empty.
	if string(raw) == "null" {
		return []string{}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return []string{}
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// extractErrorCode returns the structured code from a tool result, or
// empty when the result is not an error. Falls back to the literal
// "ERROR" for IsError results that don't carry the canonical
// `error_code: <CODE>` prefix produced by errResultCode (server.go).
// The single source of truth for the prefix grammar is errorCodeFromText;
// this wrapper adds the IsError gating and ERROR fallback the audit
// schema requires.
func extractErrorCode(res toolResult) string {
	if !res.IsError {
		return ""
	}
	if len(res.Content) == 0 {
		return "ERROR"
	}
	if code := errorCodeFromText(res.Content[0].Text); code != "" {
		return code
	}
	return "ERROR"
}

// buildAuditEntry composes one entry from the dispatch context. role and
// taskID come from the Server (set at startup from NIWA_SESSION_ROLE /
// NIWA_TASK_ID); p is the request params; res is the handler result.
// Always carries Kind="tool_call" so the v=2 schema's effectiveKind
// inference is unambiguous for fresh writers; readers handle v=1 records
// (no Kind set) via the fallback in effectiveKind.
func buildAuditEntry(role, taskID string, p toolCallParams, res toolResult) AuditEntry {
	return AuditEntry{
		V:         2,
		At:        time.Now().UTC().Format(time.RFC3339Nano),
		Role:      role,
		TaskID:    taskID,
		Kind:      "tool_call",
		Tool:      p.Name,
		ArgKeys:   extractArgKeys(p.Arguments),
		OK:        !res.IsError,
		ErrorCode: extractErrorCode(res),
	}
}
