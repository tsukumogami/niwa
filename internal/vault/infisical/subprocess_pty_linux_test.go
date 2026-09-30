package infisical

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// ptyHelperEnv switches TestPTYHelperProcess from a no-op into the
// child half of TestDefaultCommander_NoTerminalAccess.
const ptyHelperEnv = "NIWA_TEST_PTY_HELPER_OUT"

// R23 mechanism: niwa runs on a pseudo-terminal, as it does inside an
// interactive shell or a session-start hook attached to one. The
// subprocess it starts must see the null device on stdin and must not
// be able to open /dev/tty.
//
// The test binary re-runs itself as a helper that leads a new session
// with the pty's follower side as its controlling terminal, and the
// helper calls defaultCommander.Run on a stub that records what it
// could reach.
func TestDefaultCommander_NoTerminalAccess(t *testing.T) {
	leader, follower := openPTY(t)

	out := filepath.Join(t.TempDir(), "result")
	cmd := exec.Command(os.Args[0], "-test.run=^TestPTYHelperProcess$")
	cmd.Env = append(os.Environ(), ptyHelperEnv+"="+out)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = follower, follower, follower
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting pty helper: %v", err)
	}
	follower.Close()
	// Drain the terminal so the helper never blocks writing to it.
	go func() { _, _ = io.Copy(io.Discard, leader) }()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("pty helper failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("pty helper did not finish")
	}

	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("reading helper result: %v", err)
	}
	got := string(raw)
	if !strings.Contains(got, "helper-tty=ok") {
		t.Fatalf("the helper itself could not open /dev/tty, so the test proves nothing:\n%s", got)
	}
	if !strings.Contains(got, "stdin=/dev/null") {
		t.Errorf("stub's stdin was not the null device:\n%s", got)
	}
	if !strings.Contains(got, "tty=denied") {
		t.Errorf("stub could open /dev/tty:\n%s", got)
	}
}

// TestPTYHelperProcess is the child half of
// TestDefaultCommander_NoTerminalAccess. It does nothing unless that
// test started it.
func TestPTYHelperProcess(t *testing.T) {
	out := os.Getenv(ptyHelperEnv)
	if out == "" {
		t.Skip("helper for TestDefaultCommander_NoTerminalAccess")
	}
	var report strings.Builder
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		f.Close()
		report.WriteString("helper-tty=ok\n")
	} else {
		fmt.Fprintf(&report, "helper-tty=%v\n", err)
	}

	stub := filepath.Join(filepath.Dir(out), "infisical")
	script := `#!/bin/sh
echo "stdin=$(readlink /proc/self/fd/0)"
if (exec 3<>/dev/tty) 2>/dev/null; then echo tty=opened; else echo tty=denied; fi
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code, err := defaultCommander{}.Run(context.Background(), stub, nil)
	if err != nil || code != 0 {
		fmt.Fprintf(&report, "run: code=%d err=%v stderr=%q\n", code, err, stderr)
	}
	report.Write(stdout)
	if err := os.WriteFile(out, []byte(report.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// openPTY allocates a pseudo-terminal pair through /dev/ptmx.
func openPTY(t *testing.T) (leader, follower *os.File) {
	t.Helper()
	leader, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal support: %v", err)
	}
	t.Cleanup(func() { leader.Close() })
	fd := int(leader.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlocking pty: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("reading pty number: %v", err)
	}
	follower, err = os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("opening pty follower: %v", err)
	}
	return leader, follower
}
