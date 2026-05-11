package sessionattach

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func TestSuperviseClaudeBinNotFound(t *testing.T) {
	// Override PATH to a guaranteed-empty directory so exec.LookPath cannot
	// resolve `claude` to anything. Without this guard the test would invoke
	// the real claude binary on machines where it is installed -- the test
	// dev environment frequently has claude installed and a real invocation
	// authenticates against the user's account, leaks subprocesses, and
	// retains 200+ MB RSS.
	emptyDir := t.TempDir()
	t.Setenv("PATH", emptyDir)
	_, err := Supervise(context.Background(), SuperviseOptions{
		ClaudeBin: "", // empty triggers exec.LookPath
		ConvID:    "00000000-0000-0000-0000-000000000000",
		WorkerCWD: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected claude-not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "claude binary not found") {
		t.Errorf("error message missing 'claude binary not found': %v", err)
	}
}

// fakeBin returns a path to a small shell script that exits with the given
// code. Used to verify exit-code propagation without depending on a real
// claude binary.
func fakeBin(t *testing.T, exitCode int) string {
	t.Helper()
	bin, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not available")
	}
	dir := t.TempDir()
	scriptPath := dir + "/fake-claude.sh"
	if err := writeFile(scriptPath, "#!/bin/sh\nexit "+itoa(exitCode)+"\n"); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := chmod(scriptPath, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	_ = bin // bin used implicitly via the shebang
	return scriptPath
}

func TestSuperviseExitCodePropagation(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		exit int
		want int
	}{
		{0, 0},
		{1, 1},
		{42, 42},
		{125, 125},
		{126, 125}, // capped
		{200, 125}, // capped
	}
	for _, c := range cases {
		got, err := Supervise(ctx, SuperviseOptions{
			ClaudeBin: fakeBin(t, c.exit),
			ConvID:    "x",
			WorkerCWD: t.TempDir(),
			Stdin:     &bytes.Buffer{},
			Stdout:    &bytes.Buffer{},
			Stderr:    &bytes.Buffer{},
		})
		if err != nil {
			t.Errorf("Supervise(exit %d): unexpected err %v", c.exit, err)
			continue
		}
		if got != c.want {
			t.Errorf("Supervise(exit %d) = %d, want %d", c.exit, got, c.want)
		}
	}
}

func TestExitCodeFromWaitErrNil(t *testing.T) {
	if got := exitCodeFromWaitErr(nil); got != 0 {
		t.Errorf("nil err = %d, want 0", got)
	}
}

func TestExitCodeFromWaitErrUnknown(t *testing.T) {
	got := exitCodeFromWaitErr(errors.New("not exit error"))
	if got != 1 {
		t.Errorf("unknown err = %d, want 1", got)
	}
}
