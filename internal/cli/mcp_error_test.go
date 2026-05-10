package cli

import (
	"strings"
	"testing"
)

func TestRenderMCPError_LegacyShape(t *testing.T) {
	in := "error_code: SESSION_REQUIRED\ndetail: niwa_delegate requires a session_id"
	err := renderMCPError(in)
	got := err.Error()
	if !strings.Contains(got, "SESSION_REQUIRED") {
		t.Errorf("missing code: %q", got)
	}
	if !strings.Contains(got, "niwa_delegate requires a session_id") {
		t.Errorf("missing detail: %q", got)
	}
	if !strings.Contains(got, "hint:") {
		t.Errorf("missing hint: %q", got)
	}
	if !strings.Contains(got, "niwa session create") {
		t.Errorf("hint should suggest niwa session create: %q", got)
	}
}

func TestRenderMCPError_StructuredJSONShape(t *testing.T) {
	in := `{"error_code":"MISSING_SKILLS","missing":["shirabe:rpd"],"available":["shirabe:*","niwa-mesh"],"detail":"required_skills references 1 missing skill(s)"}`
	err := renderMCPError(in)
	got := err.Error()
	if !strings.Contains(got, "MISSING_SKILLS") {
		t.Errorf("missing code: %q", got)
	}
	if !strings.Contains(got, "shirabe:rpd") {
		t.Errorf("hint must include the missing skill name: %q", got)
	}
	if !strings.Contains(got, "niwa session list") {
		t.Errorf("hint should suggest niwa session list: %q", got)
	}
}

func TestRenderMCPError_DaemonSpawnTimeout(t *testing.T) {
	in := "error_code: DAEMON_SPAWN_TIMEOUT\ndetail: daemon did not become ready"
	err := renderMCPError(in)
	got := err.Error()
	if !strings.Contains(got, "daemon.log") {
		t.Errorf("hint should point at daemon.log: %q", got)
	}
	if !strings.Contains(got, "rolled back") {
		t.Errorf("hint should mention rollback: %q", got)
	}
}

func TestRenderMCPError_SourceBodyLost(t *testing.T) {
	in := `{"error_code":"SOURCE_BODY_LOST","source_task_id":"abc-123","detail":"source envelope.json is missing"}`
	err := renderMCPError(in)
	got := err.Error()
	if !strings.Contains(got, "--body-overrides") {
		t.Errorf("hint should suggest --body-overrides: %q", got)
	}
}

func TestRenderMCPError_UnknownCodePassesThrough(t *testing.T) {
	in := "error_code: SOME_NEW_CODE\ndetail: a future error"
	err := renderMCPError(in)
	got := err.Error()
	if !strings.Contains(got, "SOME_NEW_CODE") {
		t.Errorf("unknown codes must surface the code: %q", got)
	}
	// No hint for unknown codes — but the message itself must still appear.
	if !strings.Contains(got, "a future error") {
		t.Errorf("detail must surface even for unknown codes: %q", got)
	}
	if strings.Contains(got, "hint:") {
		t.Errorf("unknown codes must not invent hints: %q", got)
	}
}

func TestRenderMCPError_NonStructuredFallsThrough(t *testing.T) {
	in := "some prose-only error message"
	err := renderMCPError(in)
	if err.Error() != in {
		t.Errorf("non-structured error should pass through verbatim; got %q", err.Error())
	}
}
