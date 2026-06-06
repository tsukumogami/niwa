package worktree

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
)

// TestIsProcessAlive_Self asserts the helper recognises the running test
// binary's PID as alive. Self-signalling (kill(0) to one's own PID) never
// errors so this exercises the nil-error → alive branch.
func TestIsProcessAlive_Self(t *testing.T) {
	if !IsProcessAlive(os.Getpid()) {
		t.Fatalf("IsProcessAlive(%d) = false, want true for own PID", os.Getpid())
	}
}

// TestIsProcessAlive_NonPositive covers the guard against PID 0 and
// negative PIDs. surface.lock should never carry these but the helper
// short-circuits rather than passing them to Signal(0).
func TestIsProcessAlive_NonPositive(t *testing.T) {
	for _, pid := range []int{0, -1, -1234} {
		if IsProcessAlive(pid) {
			t.Errorf("IsProcessAlive(%d) = true, want false", pid)
		}
	}
}

// TestIsProcessAlive_Dead exercises the os.ErrProcessDone / syscall.ESRCH
// branch by spawning a short-lived child, waiting for it to exit, then
// asserting IsProcessAlive returns false against the reaped PID.
func TestIsProcessAlive_Dead(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		// The child returned non-zero exit (we asked for 0); ignore
		// — Wait still reaped it which is what we need.
		t.Logf("child wait: %v (non-fatal; PID %d should still be reaped)", err, pid)
	}
	if IsProcessAlive(pid) {
		t.Fatalf("IsProcessAlive(%d) = true after exit+wait, want false", pid)
	}
}

// TestIsProcessAlive_EPERM_Stub overrides the processSignal seam to
// return syscall.EPERM and asserts the helper fails closed (returns
// true). D12 commits to this branch: an EPERM from a UID-mismatched
// holder must not let the reaper race the live process out of its lock.
func TestIsProcessAlive_EPERM_Stub(t *testing.T) {
	prev := processSignal
	t.Cleanup(func() { processSignal = prev })
	processSignal = func(pid int) error { return syscall.EPERM }

	if !IsProcessAlive(42) {
		t.Fatalf("IsProcessAlive under stub returning EPERM = false, want true (fail-closed)")
	}
}

// TestIsProcessAlive_UnknownError_Stub covers the catch-all fail-closed
// path: any error neither ErrProcessDone nor ESRCH (e.g. a hypothetical
// EBADF from a corrupted kernel state) is treated as alive so the reap
// path never executes on an indeterminate signal result.
func TestIsProcessAlive_UnknownError_Stub(t *testing.T) {
	prev := processSignal
	t.Cleanup(func() { processSignal = prev })
	sentinel := errors.New("unknown signal failure")
	processSignal = func(pid int) error { return sentinel }

	if !IsProcessAlive(42) {
		t.Fatalf("IsProcessAlive under stub returning unknown error = false, want true (fail-closed)")
	}
}

// TestIsProcessAlive_ESRCH_Stub covers the ESRCH alias path explicitly.
// Linux returns os.ErrProcessDone post-1.21; the ESRCH fallback exists
// for portability so we exercise both with separate stubs.
func TestIsProcessAlive_ESRCH_Stub(t *testing.T) {
	prev := processSignal
	t.Cleanup(func() { processSignal = prev })
	processSignal = func(pid int) error { return syscall.ESRCH }

	if IsProcessAlive(42) {
		t.Fatalf("IsProcessAlive under stub returning ESRCH = true, want false")
	}
}
