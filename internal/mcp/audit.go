// audit.go provides per-MCP-call audit observability. The dispatch loop
// emits one entry per `tools/call` to a sink whose default implementation
// appends NDJSON lines to <instance-root>/.niwa/mcp-audit.log. The schema
// (v=1, see DESIGN-mcp-call-telemetry.md) deliberately captures argument
// *keys* but never *values* — LLM-supplied tool arguments routinely contain
// prompt injections, secrets, or PII, and leaking that into a logfile would
// re-create the exfiltration risks the rest of the design works to prevent.
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

// AuditEntry is the v=1 schema for an MCP tool-call audit record. One
// entry is emitted per `tools/call` request reaching dispatch; entries are
// appended to .niwa/mcp-audit.log in emission order.
//
// The entry is intentionally minimal: it answers "which session called
// which tool with which named arguments and did it succeed?" without
// revealing the argument values or the response body.
type AuditEntry struct {
	V         int      `json:"v"`
	At        string   `json:"at"`
	Role      string   `json:"role,omitempty"`
	TaskID    string   `json:"task_id,omitempty"`
	Tool      string   `json:"tool"`
	ArgKeys   []string `json:"arg_keys"`
	OK        bool     `json:"ok"`
	ErrorCode string   `json:"error_code,omitempty"`
}

// AuditSink writes one entry per tool call. Implementations must be safe
// for concurrent use — multiple in-flight `tools/call` dispatches may emit
// simultaneously when MCP gains per-handler goroutine fan-out (today the
// dispatch loop is single-goroutine, but the contract is forward-looking).
type AuditSink interface {
	Emit(entry AuditEntry) error
}

// nopAuditSink discards entries. Used when the Server has no instance root
// (unit-test setups that exercise individual handlers without provisioning
// a workspace) so audit emission never tries to write outside the test's
// tempdir.
type nopAuditSink struct{}

func (nopAuditSink) Emit(AuditEntry) error { return nil }

// fileAuditSink appends NDJSON lines to a fixed path. Linux guarantees
// O_APPEND writes smaller than PIPE_BUF (~4096 bytes) are atomic, so
// concurrent writers from multiple processes never tear or interleave.
// Audit entries are well under that limit.
type fileAuditSink struct {
	path string
	mu   sync.Mutex
}

// NewFileAuditSink returns a sink that appends NDJSON to
// <instanceRoot>/.niwa/mcp-audit.log. When instanceRoot is empty, returns
// a nop sink so callers don't have to special-case the no-workspace path.
// The .niwa/ directory is expected to exist (provisioned by `niwa create`);
// if it doesn't, Emit returns an error which dispatch swallows silently.
func NewFileAuditSink(instanceRoot string) AuditSink {
	if instanceRoot == "" {
		return nopAuditSink{}
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
func (s *fileAuditSink) Emit(e AuditEntry) error {
	if e.V == 0 {
		e.V = 1
	}
	if e.At == "" {
		e.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if e.ArgKeys == nil {
		e.ArgKeys = []string{}
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
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
func buildAuditEntry(role, taskID string, p toolCallParams, res toolResult) AuditEntry {
	return AuditEntry{
		V:         1,
		At:        time.Now().UTC().Format(time.RFC3339Nano),
		Role:      role,
		TaskID:    taskID,
		Tool:      p.Name,
		ArgKeys:   extractArgKeys(p.Arguments),
		OK:        !res.IsError,
		ErrorCode: extractErrorCode(res),
	}
}
