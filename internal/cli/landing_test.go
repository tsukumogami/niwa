package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestWriteLandingPath_ResponseFileSet_WritesFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	responseFile := filepath.Join(tmp, "niwa-response")
	withResponseFile(t, responseFile)

	landing := "/home/user/ws/myrepo"
	if err := writeLandingPath(landing); err != nil {
		t.Fatalf("unexpected error: %v", err)
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

	landing := "/home/user/ws/myrepo"
	if err := writeLandingPath(landing); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading response file: %v", err)
	}
	if string(data) != landing+"\n" {
		t.Errorf("response file content: got %q, want %q", string(data), landing+"\n")
	}
}

func TestWriteLandingPath_ResponseFileAbsent_IsNoOp(t *testing.T) {
	withResponseFile(t, "")
	// Must not error and must not panic; stdout is untouched.
	if err := writeLandingPath("/home/user/ws/myrepo"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteLandingPath_ResponseFileOutsideTmp_ReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	outsidePath := "/home/user/.bashrc"
	withResponseFile(t, outsidePath)

	err := writeLandingPath("/home/user/ws/myrepo")
	if err == nil {
		t.Fatal("expected error for path outside temp directory, got nil")
	}
	if !strings.Contains(err.Error(), "NIWA_RESPONSE_FILE") {
		t.Errorf("error should mention NIWA_RESPONSE_FILE, got: %v", err)
	}
	if !strings.Contains(err.Error(), outsidePath) {
		t.Errorf("error should mention the rejected path, got: %v", err)
	}

	if _, err := os.Stat(outsidePath); err == nil {
		t.Errorf("writeLandingPath created file at rejected path %q", outsidePath)
	}
}

func TestWriteLandingPath_ResponseFileInSlashTmp_Accepted(t *testing.T) {
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
	if err := writeLandingPath("/home/user/ws/myrepo"); err != nil {
		t.Fatalf("unexpected error for /tmp path with unrelated TMPDIR: %v", err)
	}
}

func TestWriteLandingPath_ResponseFileTraversal_Rejected(t *testing.T) {
	t.Setenv("TMPDIR", "/var/tmp/niwa-test-does-not-exist")

	cases := []string{
		"/tmp/../home/user/.bashrc",
		"/tmp/./../etc/passwd",
		"/tmp/sub/../../home/user/.bashrc",
	}
	for _, attack := range cases {
		t.Run(attack, func(t *testing.T) {
			withResponseFile(t, attack)

			err := writeLandingPath("/home/user/ws/myrepo")
			if err == nil {
				t.Fatalf("expected error for traversal path %q, got nil", attack)
			}
			if !strings.Contains(err.Error(), "NIWA_RESPONSE_FILE") {
				t.Errorf("error should mention NIWA_RESPONSE_FILE, got: %v", err)
			}
			resolved := filepath.Clean(attack)
			if _, statErr := os.Stat(resolved); statErr == nil {
				t.Logf("note: %q pre-exists on host, skipping absence check", resolved)
			}
		})
	}
}

func TestCaptureNiwaResponseFile_UnsetsEnv(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	responseFile := filepath.Join(tmp, "niwa-response")
	t.Setenv(niwaResponseFileEnv, responseFile)

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
