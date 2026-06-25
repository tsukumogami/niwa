package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestJobState_DecodesCwd verifies the additive Cwd field decodes from a
// state.json carrying a "cwd" key.
func TestJobState_DecodesCwd(t *testing.T) {
	dir := t.TempDir()
	const sid = "11111111-2222-3333-4444-555555555555"
	jobDir := filepath.Join(dir, "1111")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{
  "sessionId": "` + sid + `",
  "template": "bg",
  "state": "running",
  "cwd": "/home/u/work/tsuku-disp-deadbeef",
  "updatedAt": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	js, ok := readJobState(dir, sid)
	if !ok {
		t.Fatalf("readJobState: not found")
	}
	if js.Cwd != "/home/u/work/tsuku-disp-deadbeef" {
		t.Errorf("Cwd = %q, want %q", js.Cwd, "/home/u/work/tsuku-disp-deadbeef")
	}
}

// TestJobState_AbsentCwdDecodesEmpty verifies a state.json without a "cwd" key
// (the shape the hook path / older Claude Code writes) decodes with Cwd == "".
func TestJobState_AbsentCwdDecodesEmpty(t *testing.T) {
	dir := t.TempDir()
	const sid = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	jobDir := filepath.Join(dir, "aaaa")
	if err := os.MkdirAll(jobDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{
  "sessionId": "` + sid + `",
  "template": "bg",
  "state": "running"
}`
	if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	js, ok := readJobState(dir, sid)
	if !ok {
		t.Fatalf("readJobState: not found")
	}
	if js.Cwd != "" {
		t.Errorf("Cwd = %q, want empty", js.Cwd)
	}
}
