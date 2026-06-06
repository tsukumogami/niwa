package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionList_FlaglessShowsLifecycleView(t *testing.T) {
	// Flagless `niwa session list` shows the lifecycle view directly.
	// With no sessions on disk this is just the table header.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".niwa", "sessions"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".niwa", "instance.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	t.Setenv("NIWA_INSTANCE_ROOT", root)

	resetSessionListFlags(t)
	defer resetSessionListFlags(t)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	sessionListCmd.SetOut(stdoutBuf)
	sessionListCmd.SetErr(stderrBuf)
	defer func() {
		sessionListCmd.SetOut(os.Stdout)
		sessionListCmd.SetErr(os.Stderr)
	}()

	if err := runSessionList(sessionListCmd, nil); err != nil {
		t.Fatalf("runSessionList: %v", err)
	}
	stdout := stdoutBuf.String()
	stderr := stderrBuf.String()
	// Lifecycle view is the new default: column header SESSION_ID is
	// present in stdout.
	if !strings.Contains(stdout, "SESSION_ID") {
		t.Errorf("expected lifecycle view header SESSION_ID in stdout, got %q", stdout)
	}
	// Coordinator-registry markers (ROLE / PID columns from mesh list)
	// must NOT appear -- the alias is gone.
	if strings.Contains(stdout, "ROLE") || strings.Contains(stdout, "PENDING") {
		t.Errorf("flagless lifecycle view leaked mesh-list columns: %q", stdout)
	}
	// No deprecation warning on stderr.
	if strings.Contains(stderr, "deprecated") {
		t.Errorf("deprecation warning still present after PLAN issue 10: %q", stderr)
	}
}
