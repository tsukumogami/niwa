package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestExtractArgKeys(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"empty", "", []string{}},
		{"null literal", "null", []string{}},
		{"empty object", "{}", []string{}},
		{"single key", `{"to":"web"}`, []string{"to"}},
		{"sorted multi-key", `{"to":"web","body":{},"mode":"async"}`, []string{"body", "mode", "to"}},
		{"keys-only no values revealed", `{"secret":"hunter2","other":"xyz"}`, []string{"other", "secret"}},
		{"non-object array", `[1,2,3]`, []string{}},
		{"non-object string", `"foo"`, []string{}},
		{"malformed json", `{not-json`, []string{}},
		{"nested keys ignored", `{"body":{"deep":{"nested":1}}}`, []string{"body"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractArgKeys(json.RawMessage(tc.raw))
			if !equalStrings(got, tc.want) {
				t.Errorf("extractArgKeys(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestExtractErrorCode(t *testing.T) {
	mkRes := func(isErr bool, text string) toolResult {
		return toolResult{IsError: isErr, Content: []contentBlock{{Type: "text", Text: text}}}
	}
	cases := []struct {
		name string
		res  toolResult
		want string
	}{
		{"ok result", mkRes(false, "anything"), ""},
		{"error with code prefix", mkRes(true, "error_code: NOT_TASK_PARTY\ndetail: caller is not authorized"), "NOT_TASK_PARTY"},
		{"error with multi-word code", mkRes(true, "error_code: TASK_ALREADY_TERMINAL\ndetail: state=completed"), "TASK_ALREADY_TERMINAL"},
		{"error without prefix", mkRes(true, "something went wrong"), "ERROR"},
		{"empty content error", toolResult{IsError: true}, "ERROR"},
		{"bare uppercase token (no error_code prefix)", mkRes(true, "UNKNOWN_ROLE"), "ERROR"},
		{"single-letter not a code", mkRes(true, "A: not a code"), "ERROR"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractErrorCode(tc.res)
			if got != tc.want {
				t.Errorf("extractErrorCode(%+v) = %q, want %q", tc.res, got, tc.want)
			}
		})
	}
}

// TestBuildAuditEntry_RealErrorCodes pipes outputs from the production
// errResultCode helper through buildAuditEntry and asserts the codes land
// in AuditEntry.ErrorCode rather than the literal "ERROR". This is the
// regression guard for the bug where extractErrorCode's earlier regex
// (`^[A-Z]...`) could not match the lowercase `error_code: ` prefix that
// errResultCode actually emits, so every audit row had ErrorCode="ERROR".
func TestBuildAuditEntry_RealErrorCodes(t *testing.T) {
	codes := []string{"NOT_TASK_PARTY", "UNKNOWN_ROLE", "BAD_TYPE", "TASK_ALREADY_TERMINAL", "TASK_NOT_FOUND", "INVALID_ARGS"}
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			res := errResultCode(code, "synthetic detail for "+code)
			entry := buildAuditEntry("coordinator", "", toolCallParams{Name: "niwa_delegate"}, res)
			if entry.OK {
				t.Fatalf("OK = true on errResultCode result, want false")
			}
			if entry.ErrorCode != code {
				t.Errorf("ErrorCode = %q, want %q (errResultCode shape no longer matches extractErrorCode)", entry.ErrorCode, code)
			}
		})
	}
}

func TestFileAuditSink_AppendAndRead(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	entries := []AuditEntry{
		{Tool: "niwa_delegate", Role: "coordinator", ArgKeys: []string{"body", "to"}, OK: true},
		{Tool: "niwa_finish_task", Role: "web", TaskID: "t-1", ArgKeys: []string{"outcome", "result", "task_id"}, OK: true},
		{Tool: "niwa_cancel_task", Role: "coordinator", ArgKeys: []string{"task_id"}, OK: false, ErrorCode: "TASK_NOT_FOUND"},
	}
	for i := range entries {
		if err := sink.Emit(entries[i]); err != nil {
			t.Fatalf("emit %d: %v", i, err)
		}
	}

	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != len(entries) {
		t.Fatalf("got %d entries, want %d", len(got), len(entries))
	}
	for i, want := range entries {
		if got[i].Tool != want.Tool || got[i].Role != want.Role ||
			got[i].TaskID != want.TaskID || got[i].OK != want.OK ||
			got[i].ErrorCode != want.ErrorCode {
			t.Errorf("entry %d mismatch: got %+v, want %+v", i, got[i], want)
		}
		if got[i].V != 1 {
			t.Errorf("entry %d V = %d, want 1 (autofilled)", i, got[i].V)
		}
		if got[i].At == "" {
			t.Errorf("entry %d At is empty (autofill failed)", i)
		}
	}
}

// TestFileAuditSink_ConcurrentEmit asserts that parallel goroutines
// emitting simultaneously produce a parseable file with no torn lines.
// O_APPEND is the load-bearing guarantee here; the sink's mutex serializes
// in-process callers but the cross-process atomicity is what matters at
// PIPE_BUF-sized writes.
func TestFileAuditSink_ConcurrentEmit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	const goroutines = 10
	const perGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = sink.Emit(AuditEntry{
					Tool:    "niwa_check_messages",
					Role:    "web",
					ArgKeys: []string{"goroutine", "i"},
					OK:      true,
				})
			}
		}(g)
	}
	wg.Wait()

	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != goroutines*perGoroutine {
		t.Fatalf("got %d entries, want %d (some lines were dropped or torn)",
			len(got), goroutines*perGoroutine)
	}
	for i, e := range got {
		if e.Tool != "niwa_check_messages" || e.Role != "web" || !e.OK {
			t.Fatalf("entry %d corrupt: %+v", i, e)
		}
	}
}

// TestFileAuditSink_NoLeakageOfArgValues is the privacy regression.
// Argument values must NEVER appear in the audit file. The test emits an
// entry whose ArgKeys list is correct but whose AuditEntry is built from
// a hypothetical sensitive arguments object via buildAuditEntry; the file
// must contain only the key names.
func TestFileAuditSink_NoLeakageOfArgValues(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sink := NewFileAuditSink(root)

	const secret = "hunter2-CANARY-DO-NOT-LOG"
	args := json.RawMessage(`{"to":"web","body":{"password":"` + secret + `"}}`)
	res := toolResult{}
	entry := buildAuditEntry("coordinator", "", toolCallParams{Name: "niwa_send_message", Arguments: args}, res)
	if err := sink.Emit(entry); err != nil {
		t.Fatalf("emit: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, ".niwa", "mcp-audit.log"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("secret leaked into audit log:\n%s", data)
	}
	if !strings.Contains(string(data), `"body"`) || !strings.Contains(string(data), `"to"`) {
		t.Errorf("expected key names body and to in audit, got: %s", data)
	}
}

func TestNewFileAuditSink_EmptyInstanceIsNop(t *testing.T) {
	sink := NewFileAuditSink("")
	// Must not panic, must not return an error, must not touch the
	// filesystem. There's no observable to assert beyond "did not error";
	// the absence of a panic and the nop type assertion below cover it.
	if err := sink.Emit(AuditEntry{Tool: "x"}); err != nil {
		t.Errorf("nop sink returned error: %v", err)
	}
	if _, ok := sink.(nopAuditSink); !ok {
		t.Errorf("expected nopAuditSink, got %T", sink)
	}
}

func TestReadAuditLog_MissingFileIsEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %d entries", len(got))
	}
}

func TestReadAuditLog_SkipsTornLines(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".niwa")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Hand-written log with one valid line, one torn line, one valid.
	content := `{"v":1,"at":"2026-04-24T00:00:00Z","tool":"niwa_delegate","arg_keys":["to"],"ok":true}
{"v":1,"at":"2026-04-24T00:00:01Z","tool":"niwa_fini` + "\n" +
		`{"v":1,"at":"2026-04-24T00:00:02Z","tool":"niwa_check_messages","arg_keys":[],"ok":true}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "mcp-audit.log"), []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadAuditLog(root)
	if err != nil {
		t.Fatalf("ReadAuditLog: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 valid entries (torn skipped), got %d", len(got))
	}
	if got[0].Tool != "niwa_delegate" || got[1].Tool != "niwa_check_messages" {
		t.Errorf("got tools %q,%q", got[0].Tool, got[1].Tool)
	}
}

func TestFilterAudit(t *testing.T) {
	tru := true
	fal := false
	entries := []AuditEntry{
		{Tool: "niwa_delegate", Role: "coordinator", ArgKeys: []string{"body", "to"}, OK: true},
		{Tool: "niwa_delegate", Role: "coordinator", ArgKeys: []string{"body", "to"}, OK: true},
		{Tool: "niwa_finish_task", Role: "web", TaskID: "t-1", ArgKeys: []string{"outcome", "task_id"}, OK: true},
		{Tool: "niwa_finish_task", Role: "backend", TaskID: "t-2", ArgKeys: []string{"outcome", "task_id"}, OK: true},
		{Tool: "niwa_cancel_task", Role: "coordinator", ArgKeys: []string{"task_id"}, OK: false, ErrorCode: "TASK_NOT_FOUND"},
	}
	cases := []struct {
		name string
		f    AuditFilter
		want int
	}{
		{"by tool", AuditFilter{Tool: "niwa_delegate"}, 2},
		{"by role", AuditFilter{Role: "coordinator"}, 3},
		{"by tool and role", AuditFilter{Tool: "niwa_finish_task", Role: "web"}, 1},
		{"by task_id", AuditFilter{TaskID: "t-2"}, 1},
		{"only ok=true", AuditFilter{OK: &tru}, 4},
		{"only ok=false", AuditFilter{OK: &fal}, 1},
		{"by has-key", AuditFilter{HasKey: "outcome"}, 2},
		{"no match", AuditFilter{Tool: "niwa_unknown"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := FilterAudit(entries, tc.f)
			if len(got) != tc.want {
				t.Errorf("FilterAudit(%+v) returned %d, want %d", tc.f, len(got), tc.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
