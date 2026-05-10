package sessionattach

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/mcp"
)

func TestEncodeProjectDirEmpiricalCases(t *testing.T) {
	// These are the cases the round 2 transcript-failure-modes agent
	// empirically verified against claude v2.1.138.
	cases := []struct{ in, want string }{
		{"/tmp/claude-resume-test", "-tmp-claude-resume-test"},
		{"/tmp/cr.dotted/sub_dir", "-tmp-cr-dotted-sub-dir"},
		{"/tmp/cr space/test", "-tmp-cr-space-test"},
		{"/tmp/cr@upper-CASE+plus/x", "-tmp-cr-upper-CASE-plus-x"},
		{"a", "a"},
		{"", ""},
	}
	for _, c := range cases {
		if got := EncodeProjectDir(c.in); got != c.want {
			t.Errorf("EncodeProjectDir(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTranscriptPathLayout(t *testing.T) {
	got := TranscriptPath("/home/dan", "/tmp/wt/repo", "11111111-2222-3333-4444-555555555555")
	want := "/home/dan/.claude/projects/-tmp-wt-repo/11111111-2222-3333-4444-555555555555.jsonl"
	if got != want {
		t.Errorf("TranscriptPath = %q, want %q", got, want)
	}
}

func TestPreflightCaseAEmptyConvID(t *testing.T) {
	state := mcp.SessionLifecycleState{
		SessionID:            "abcd1234",
		ClaudeConversationID: "", // Case A trigger
	}
	err := Preflight(state, PreflightOptions{HomeDir: "/tmp/h", WorkerCWD: "/tmp/wt"})
	if err == nil {
		t.Fatalf("want preflight error, got nil")
	}
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err is not *PreflightError: %T", err)
	}
	if pe.Case != CaseA {
		t.Errorf("Case = %c, want %c", pe.Case, CaseA)
	}
	msg := pe.Error()
	wantSubstrs := []string{
		"niwa: error: session abcd1234 has no captured claude conversation id",
		"`niwa session show abcd1234`",
		"`niwa session destroy abcd1234`",
	}
	for _, s := range wantSubstrs {
		if !strings.Contains(msg, s) {
			t.Errorf("error message missing %q: got %q", s, msg)
		}
	}
}

func TestPreflightCaseBTranscriptMissing(t *testing.T) {
	home := t.TempDir()
	state := mcp.SessionLifecycleState{
		SessionID:            "abcd1234",
		ClaudeConversationID: "11111111-2222-3333-4444-555555555555",
	}
	err := Preflight(state, PreflightOptions{HomeDir: home, WorkerCWD: "/tmp/wt"})
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err is not *PreflightError: %T", err)
	}
	if pe.Case != CaseB {
		t.Errorf("Case = %c, want %c", pe.Case, CaseB)
	}
	if !strings.Contains(pe.Error(), "claude transcript missing") {
		t.Errorf("missing 'claude transcript missing': %q", pe.Error())
	}
	if !strings.Contains(pe.Error(), pe.Path) {
		t.Errorf("error message must include path %q: %q", pe.Path, pe.Error())
	}
}

func TestPreflightCaseCEmptyTranscript(t *testing.T) {
	home := t.TempDir()
	convID := "11111111-2222-3333-4444-555555555555"
	wt := "/tmp/wt"
	path := TranscriptPath(home, wt, convID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("seed empty file: %v", err)
	}
	state := mcp.SessionLifecycleState{
		SessionID:            "abcd1234",
		ClaudeConversationID: convID,
	}
	err := Preflight(state, PreflightOptions{HomeDir: home, WorkerCWD: wt})
	var pe *PreflightError
	if !errors.As(err, &pe) {
		t.Fatalf("err is not *PreflightError: %T", err)
	}
	if pe.Case != CaseC {
		t.Errorf("Case = %c, want %c", pe.Case, CaseC)
	}
	if !strings.Contains(pe.Error(), "claude transcript is empty") {
		t.Errorf("missing 'claude transcript is empty': %q", pe.Error())
	}
}

func TestPreflightHappyPath(t *testing.T) {
	home := t.TempDir()
	convID := "11111111-2222-3333-4444-555555555555"
	wt := "/tmp/wt"
	path := TranscriptPath(home, wt, convID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"sessionId":"x"}`), 0o600); err != nil {
		t.Fatalf("seed transcript: %v", err)
	}
	state := mcp.SessionLifecycleState{
		SessionID:            "abcd1234",
		ClaudeConversationID: convID,
	}
	if err := Preflight(state, PreflightOptions{HomeDir: home, WorkerCWD: wt}); err != nil {
		t.Errorf("preflight failed unexpectedly: %v", err)
	}
}
