package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// withResponseFile sets the package-level cache that writeLandingPath reads
// from, restoring the previous value when the test completes. This mirrors
// what the root command's PersistentPreRunE does at process start.
func withResponseFile(t *testing.T, value string) {
	t.Helper()
	prev := niwaResponseFile
	niwaResponseFile = value
	t.Cleanup(func() { niwaResponseFile = prev })
}

func newTestCmd() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{}
	var outBuf, errBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetErr(&errBuf)
	return cmd, &outBuf, &errBuf
}

func TestWriteLandingPath_ResponseFileSet_WritesFileAndNotStdout(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	responseFile := filepath.Join(tmp, "niwa-response")
	withResponseFile(t, responseFile)

	cmd, out, _ := newTestCmd()
	landing := "/home/user/ws/myrepo"

	if err := writeLandingPath(cmd, landing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("stdout must be empty when NIWA_RESPONSE_FILE is set, got: %q", out.String())
	}

	data, err := os.ReadFile(responseFile)
	if err != nil {
		t.Fatalf("reading response file: %v", err)
	}
	want := landing + "\n"
	if string(data) != want {
		t.Errorf("response file content: got %q, want %q", string(data), want)
	}
}

func TestWriteLandingPath_ResponseFileSet_DefaultTmpDir(t *testing.T) {
	// With TMPDIR unset, /tmp is the fallback. Use the real /tmp so the
	// prefix check accepts the path.
	t.Setenv("TMPDIR", "")
	f, err := os.CreateTemp("/tmp", "niwa-response-*")
	if err != nil {
		t.Skipf("cannot create file in /tmp: %v", err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	withResponseFile(t, path)

	cmd, out, _ := newTestCmd()
	landing := "/home/user/ws/myrepo"
	if err := writeLandingPath(cmd, landing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if out.Len() != 0 {
		t.Errorf("stdout must be empty, got: %q", out.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading response file: %v", err)
	}
	if string(data) != landing+"\n" {
		t.Errorf("response file content: got %q, want %q", string(data), landing+"\n")
	}
}

func TestWriteLandingPath_ResponseFileAbsent_WritesStdout(t *testing.T) {
	withResponseFile(t, "")

	cmd, out, _ := newTestCmd()
	landing := "/home/user/ws/myrepo"

	if err := writeLandingPath(cmd, landing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := strings.TrimRight(out.String(), "\n")
	if got != landing {
		t.Errorf("stdout: got %q, want %q", got, landing)
	}
	if !strings.HasSuffix(out.String(), "\n") {
		t.Errorf("stdout output must end with a newline, got: %q", out.String())
	}
}

func TestWriteLandingPath_ResponseFileOutsideTmp_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	// Path outside both $TMPDIR and /tmp.
	outsidePath := "/home/user/.bashrc"
	withResponseFile(t, outsidePath)

	cmd, out, _ := newTestCmd()
	err := writeLandingPath(cmd, "/home/user/ws/myrepo")
	if err == nil {
		t.Fatal("expected error for path outside temp directory, got nil")
	}
	if !strings.Contains(err.Error(), "NIWA_RESPONSE_FILE") {
		t.Errorf("error should mention NIWA_RESPONSE_FILE, got: %v", err)
	}
	if !strings.Contains(err.Error(), outsidePath) {
		t.Errorf("error should mention the rejected path, got: %v", err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout must be empty on error, got: %q", out.String())
	}

	// Ensure no file was created at the rejected location.
	if _, err := os.Stat(outsidePath); err == nil {
		t.Errorf("writeLandingPath created file at rejected path %q", outsidePath)
	}
}

func TestWriteLandingPath_ResponseFileInSlashTmp_Accepted(t *testing.T) {
	// When TMPDIR is set to something else, /tmp paths are still accepted
	// (per the helper's fallback prefix check).
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	f, err := os.CreateTemp("/tmp", "niwa-response-*")
	if err != nil {
		t.Skipf("cannot create file in /tmp: %v", err)
	}
	path := f.Name()
	f.Close()
	t.Cleanup(func() { os.Remove(path) })

	withResponseFile(t, path)

	cmd, _, _ := newTestCmd()
	if err := writeLandingPath(cmd, "/home/user/ws/myrepo"); err != nil {
		t.Fatalf("unexpected error for /tmp path with unrelated TMPDIR: %v", err)
	}
}

func TestCaptureNiwaResponseFile_UnsetsEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	responseFile := filepath.Join(tmp, "niwa-response")
	t.Setenv(niwaResponseFileEnv, responseFile)

	// Reset the cache after the test so we don't leak into other tests.
	prev := niwaResponseFile
	t.Cleanup(func() { niwaResponseFile = prev })

	if err := captureNiwaResponseFile(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if niwaResponseFile != responseFile {
		t.Errorf("cache: got %q, want %q", niwaResponseFile, responseFile)
	}

	if v, ok := os.LookupEnv(niwaResponseFileEnv); ok {
		t.Errorf("NIWA_RESPONSE_FILE should be unset, still has value %q", v)
	}
}
