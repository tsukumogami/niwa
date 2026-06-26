package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
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

// TestSessionLive_StoppedAndFirstTerminalAt verifies the reaper treats a
// `claude stop`-produced session as terminal. `claude stop` drives the job
// state to `state: "stopped"` (not in the legacy terminal-state list) and
// stamps `firstTerminalAt`; either signal must mark the session dead so its
// instance is reclaimed promptly rather than only after the TTL.
func TestSessionLive_StoppedAndFirstTerminalAt(t *testing.T) {
	const sid = "a23abd0f-d6d4-4200-be90-090eb43c3581"
	now := time.Date(2026, 6, 26, 1, 12, 0, 0, time.UTC)

	write := func(t *testing.T, body string) string {
		t.Helper()
		dir := t.TempDir()
		jobDir := filepath.Join(dir, sid[:8])
		if err := os.MkdirAll(jobDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return dir
	}

	cases := []struct {
		name string
		body string
		live bool
	}{
		{
			name: "stopped state with firstTerminalAt is dead",
			body: `{"sessionId":"` + sid + `","state":"stopped","firstTerminalAt":"2026-06-26T01:10:05Z","updatedAt":"2026-06-26T01:10:05Z"}`,
			live: false,
		},
		{
			name: "stopped state alone (no firstTerminalAt) is dead via the state set",
			body: `{"sessionId":"` + sid + `","state":"stopped","updatedAt":"2026-06-26T01:11:59Z"}`,
			live: false,
		},
		{
			name: "unknown terminal label rescued by firstTerminalAt",
			body: `{"sessionId":"` + sid + `","state":"someNewTerminalLabel","firstTerminalAt":"2026-06-26T01:10:05Z","updatedAt":"2026-06-26T01:11:59Z"}`,
			live: false,
		},
		{
			name: "running with fresh updatedAt and no firstTerminalAt is live",
			body: `{"sessionId":"` + sid + `","state":"running","updatedAt":"2026-06-26T01:11:59Z"}`,
			live: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := write(t, tc.body)
			if got := sessionLive(dir, sid, now); got != tc.live {
				t.Fatalf("sessionLive = %v, want %v", got, tc.live)
			}
		})
	}
}
