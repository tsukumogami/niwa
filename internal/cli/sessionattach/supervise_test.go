package sessionattach

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestSuperviseClaudeBinNotFound(t *testing.T) {
	// Use an obviously missing binary name to force LookPath to fail.
	_, err := Supervise(context.Background(), SuperviseOptions{
		ClaudeBin: "",
		ConvID:    "11111111-2222-3333-4444-555555555555",
		WorkerCWD: t.TempDir(),
	})
	// LookPath may succeed if a real `claude` is installed on the test
	// machine -- this test only asserts the LookPath path when no binary
	// can be found. Skip when the test machine has claude installed.
	if err == nil {
		t.Skip("test machine has claude installed; skipping not-found path")
	}
	if err.Error() == "" {
		t.Errorf("error should have a message")
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
