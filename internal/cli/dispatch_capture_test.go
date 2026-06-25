package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeJobStateFixture writes a fabricated <jobsDir>/<shortID>/state.json with
// the given sessionId and cwd, mirroring the shape Claude Code emits.
func writeJobStateFixture(t *testing.T, jobsDir, shortID, sessionID, cwd string) {
	t.Helper()
	dir := filepath.Join(jobsDir, shortID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir job dir: %v", err)
	}
	body := `{
  "sessionId": "` + sessionID + `",
  "template": "bg",
  "state": "running",
  "cwd": "` + cwd + `",
  "updatedAt": "2026-01-01T00:00:00Z"
}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write state.json: %v", err)
	}
}

func TestCaptureSessionID(t *testing.T) {
	const sid = "12345678-90ab-cdef-1234-567890abcdef"
	const other = "ffffffff-ffff-ffff-ffff-ffffffffffff"

	t.Run("present immediately returns the UUID", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()
		writeJobStateFixture(t, jobsDir, "1234", sid, instanceDir)

		got, err := captureSessionID(jobsDir, instanceDir, time.Second, nil, time.Millisecond)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if got != sid {
			t.Errorf("got %q, want %q", got, sid)
		}
	})

	t.Run("appears within the bound", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()

		// Write the fixture shortly after the call begins; the poll re-reads
		// each pass so it is picked up before the timeout.
		go func() {
			time.Sleep(20 * time.Millisecond)
			writeJobStateFixture(t, jobsDir, "1234", sid, instanceDir)
		}()

		got, err := captureSessionID(jobsDir, instanceDir, 2*time.Second, nil, 5*time.Millisecond)
		if err != nil {
			t.Fatalf("capture: %v", err)
		}
		if got != sid {
			t.Errorf("got %q, want %q", got, sid)
		}
	})

	t.Run("never appears yields a timeout error", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()

		_, err := captureSessionID(jobsDir, instanceDir, 30*time.Millisecond, nil, 5*time.Millisecond)
		if err == nil {
			t.Fatal("expected a timeout error, got nil")
		}
	})

	t.Run("two jobs same cwd is ambiguous", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()
		writeJobStateFixture(t, jobsDir, "1234", sid, instanceDir)
		writeJobStateFixture(t, jobsDir, "5678", other, instanceDir)

		_, err := captureSessionID(jobsDir, instanceDir, time.Second, nil, time.Millisecond)
		if err == nil {
			t.Fatal("expected an ambiguity error, got nil")
		}
	})

	t.Run("non-matching cwd is ignored", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()
		elsewhere := t.TempDir()
		writeJobStateFixture(t, jobsDir, "9999", other, elsewhere)

		_, err := captureSessionID(jobsDir, instanceDir, 30*time.Millisecond, nil, 5*time.Millisecond)
		if err == nil {
			t.Fatal("expected a timeout error (no matching cwd), got nil")
		}
	})

	t.Run("invalid sessionId keeps polling then times out", func(t *testing.T) {
		jobsDir := t.TempDir()
		instanceDir := t.TempDir()
		// Matching cwd but a non-UUID sessionId: treated as not-yet-ready.
		writeJobStateFixture(t, jobsDir, "1234", "not-a-uuid", instanceDir)

		_, err := captureSessionID(jobsDir, instanceDir, 30*time.Millisecond, nil, 5*time.Millisecond)
		if err == nil {
			t.Fatal("expected a timeout error for an invalid sessionId, got nil")
		}
	})

	t.Run("symlinked instance path still matches", func(t *testing.T) {
		jobsDir := t.TempDir()
		realDir := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(realDir, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		// state.json records the real dir; we capture against the symlink.
		writeJobStateFixture(t, jobsDir, "1234", sid, realDir)

		got, err := captureSessionID(jobsDir, link, time.Second, nil, time.Millisecond)
		if err != nil {
			t.Fatalf("capture via symlink: %v", err)
		}
		if got != sid {
			t.Errorf("got %q, want %q", got, sid)
		}
	})
}
