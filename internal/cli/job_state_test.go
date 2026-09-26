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

// TestSessionLive_EntryPresentIsLive verifies the entry-present liveness rule
// (DESIGN Decision 6, revised): a session is live exactly while its job entry
// exists, regardless of a terminal `state`, a stamped `firstTerminalAt`, or a
// stale `updatedAt`. Those conditions are all true of a live idle-but-resumable
// session, so keying on them was the reaped-on-completion / reaped-on-idle bug.
// Only an entry that is GONE (the delete proxy) reads as dead.
func TestSessionLive_EntryPresentIsLive(t *testing.T) {
	const sid = "a23abd0f-d6d4-4200-be90-090eb43c3581"
	// `now` is far past every timestamp below; the entry-present rule ignores it.
	now := time.Date(2026, 6, 26, 5, 0, 0, 0, time.UTC)

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
			// A completed-but-resumable session: terminal `done` + firstTerminalAt
			// stamped, but still listed and re-openable. Entry present => LIVE.
			name: "done with firstTerminalAt is live (idle-but-resumable)",
			body: `{"sessionId":"` + sid + `","state":"done","firstTerminalAt":"2026-06-26T01:10:05Z","updatedAt":"2026-06-26T01:10:05Z"}`,
			live: true,
		},
		{
			name: "stopped state alone is live while its entry exists",
			body: `{"sessionId":"` + sid + `","state":"stopped","updatedAt":"2026-06-26T01:11:59Z"}`,
			live: true,
		},
		{
			// updatedAt hours past the old 30-minute TTL: TTL is gone, entry present => LIVE.
			name: "stale updatedAt (past the removed TTL) is live",
			body: `{"sessionId":"` + sid + `","state":"running","updatedAt":"2026-06-26T01:00:00Z"}`,
			live: true,
		},
		{
			name: "running with fresh updatedAt is live",
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

// TestSessionLive_EntryGoneIsDead verifies that a session whose job entry is
// absent reads as dead -- the delete proxy the reaper reclaims on. A recorded
// sessionId for a DIFFERENT session is likewise dead-for-our-session.
func TestSessionLive_EntryGoneIsDead(t *testing.T) {
	const sid = "a23abd0f-d6d4-4200-be90-090eb43c3581"
	now := time.Date(2026, 6, 26, 5, 0, 0, 0, time.UTC)

	t.Run("no job entry is dead", func(t *testing.T) {
		dir := t.TempDir() // empty jobs dir: the session was deleted.
		if sessionLive(dir, sid, now) {
			t.Fatal("sessionLive = true for a gone job entry; want false (deleted)")
		}
	})

	t.Run("empty jobsDir is dead", func(t *testing.T) {
		if sessionLive("", sid, now) {
			t.Fatal("sessionLive = true for an empty jobsDir; want false")
		}
	})

	t.Run("prefix collision with a different sessionId is dead", func(t *testing.T) {
		dir := t.TempDir()
		jobDir := filepath.Join(dir, sid[:8])
		if err := os.MkdirAll(jobDir, 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		body := `{"sessionId":"ffffffff-0000-0000-0000-000000000000","state":"running"}`
		if err := os.WriteFile(filepath.Join(jobDir, "state.json"), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if sessionLive(dir, sid, now) {
			t.Fatal("sessionLive = true for a sessionId mismatch; want false")
		}
	})
}
