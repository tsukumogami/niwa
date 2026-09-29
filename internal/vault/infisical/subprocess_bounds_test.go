package infisical

// Tests for the deadline and process hygiene of defaultCommander and
// runInfisicalExport. Every subprocess here is a /bin/sh stub written
// into a t.TempDir(), reached either through a test-controlled PATH or
// by absolute path; none of them runs the operator's real infisical
// binary.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/vault"
)

// writeInfisicalStub writes an executable shell script named
// "infisical" into a fresh temp dir and returns its path.
func writeInfisicalStub(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "infisical")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// putStubOnPath makes the stub the first "infisical" on PATH for the
// rest of the test, ahead of any real client the host has installed.
func putStubOnPath(t *testing.T, stub string) {
	t.Helper()
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// stubCommander runs defaultCommander against an absolute stub path
// instead of looking "infisical" up on PATH, so tests that can't
// touch the environment still exercise the production commander.
type stubCommander struct{ path string }

func (s stubCommander) Run(ctx context.Context, _ string, args []string) ([]byte, []byte, int, error) {
	return defaultCommander{}.Run(ctx, s.path, args)
}

// hangingCommander is a fake that simulates a CLI that never answers:
// it blocks until its context is done, the way every fake that stands
// in for a hang should.
type hangingCommander struct{}

func (hangingCommander) Run(ctx context.Context, _ string, _ []string) ([]byte, []byte, int, error) {
	<-ctx.Done()
	return nil, nil, -1, nil
}

// countGroupKills wraps killProcessGroup for the rest of the test and
// returns a counter of the group signals Run sends.
func countGroupKills(t *testing.T) *atomic.Int32 {
	t.Helper()
	var n atomic.Int32
	orig := killProcessGroup
	killProcessGroup = func(pgid int) error {
		n.Add(1)
		return orig(pgid)
	}
	t.Cleanup(func() { killProcessGroup = orig })
	return &n
}

// readPID reads the PID a stub wrote to path, waiting briefly for the
// write to land.
func readPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("stub never recorded its child's PID in %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// processGone reports whether pid no longer names a live process. A
// zombie waiting for its new parent to reap it counts as gone.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	if runtime.GOOS == "linux" {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err != nil {
			return true
		}
		// The state follows the parenthesised command name.
		if i := strings.LastIndexByte(string(raw), ')'); i >= 0 && i+2 < len(raw) && raw[i+2] == 'Z' {
			return true
		}
	}
	return false
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !processGone(pid) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("forked child %d is still alive after the call returned", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertExportTimeout checks the error an export that ran out of time
// returns: unreachable, not "client not installed", not a generic
// exit-code error, and naming the export and the bound.
func assertExportTimeout(t *testing.T, err error, bound string) {
	t.Helper()
	if err == nil {
		t.Fatal("export returned no error, want a timeout")
	}
	if !errors.Is(err, vault.ErrProviderUnreachable) {
		t.Errorf("error %q does not wrap ErrProviderUnreachable", err)
	}
	if errors.Is(err, vault.ErrClientNotInstalled) {
		t.Errorf("error %q reports the client as not installed", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "exited") {
		t.Errorf("error %q reads as an exit-code failure", msg)
	}
	if want := "export timed out after " + bound; !strings.Contains(msg, want) {
		t.Errorf("error %q does not contain %q", msg, want)
	}
}

// R7 mechanism: a stub that never exits comes back within the bound
// plus 5 s, as a timeout.
func TestRunInfisicalExport_HangingStubTimesOut(t *testing.T) {
	quietOverride(t, "500ms")
	kills := countGroupKills(t)
	putStubOnPath(t, writeInfisicalStub(t, "sleep 600"))

	start := time.Now()
	_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	elapsed := time.Since(start)

	assertExportTimeout(t, err, "500ms")
	if elapsed > 500*time.Millisecond+5*time.Second {
		t.Errorf("export returned after %s, want within the bound plus 5s", elapsed)
	}
	if kills.Load() == 0 {
		t.Error("a timed-out export sent no process-group kill")
	}
}

// R7 mechanism: a stub that forks a child holding stdout open. In the
// first case the stub waits on the child, so the deadline kills the
// group; in the second it exits at once and leaves the child behind,
// so WaitDelay cuts the pipes. Either way the call returns in time,
// reports a timeout, and the child is gone.
func TestRunInfisicalExport_ForkedChildHoldingStdout(t *testing.T) {
	cases := []struct {
		name string
		tail string
	}{
		{name: "stub waits on child", tail: "wait"},
		{name: "stub exits, child keeps stdout", tail: "exit 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			quietOverride(t, "500ms")
			pidFile := filepath.Join(t.TempDir(), "child.pid")
			t.Setenv("STUB_CHILD_PID_FILE", pidFile)
			putStubOnPath(t, writeInfisicalStub(t, `sleep 600 &
echo $! > "$STUB_CHILD_PID_FILE"
`+tc.tail))

			start := time.Now()
			_, _, err := runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
			elapsed := time.Since(start)

			if elapsed > 500*time.Millisecond+5*time.Second {
				t.Errorf("export returned after %s, want within the bound plus 5s", elapsed)
			}
			if err == nil || !errors.Is(err, vault.ErrProviderUnreachable) || !strings.Contains(err.Error(), "timed out") {
				t.Errorf("err = %v, want a timeout wrapping ErrProviderUnreachable", err)
			}
			assertProcessGone(t, readPID(t, pidFile))
		})
	}
}

// A run that finishes normally never signals a process group: once
// the child is reaped its group ID may belong to someone else.
func TestDefaultCommander_NormalRunSendsNoGroupSignal(t *testing.T) {
	kills := countGroupKills(t)

	ok := writeInfisicalStub(t, `echo '{"API_KEY":"value"}'`)
	stdout, _, code, err := defaultCommander{}.Run(context.Background(), ok, []string{"export"})
	if err != nil || code != 0 || !strings.Contains(string(stdout), "API_KEY") {
		t.Fatalf("Run = (%q, %d, %v), want the stub's output with exit 0", stdout, code, err)
	}

	failing := writeInfisicalStub(t, "echo 'error: something' >&2\nexit 3")
	_, stderr, code, err := defaultCommander{}.Run(context.Background(), failing, nil)
	if err != nil || code != 3 || !strings.Contains(string(stderr), "something") {
		t.Fatalf("Run = (stderr %q, %d, %v), want exit 3 with captured stderr", stderr, code, err)
	}

	if n := kills.Load(); n != 0 {
		t.Errorf("normal runs sent %d process-group signals, want 0", n)
	}
}

// The start-failure path is unchanged: exit code -1 and a non-nil
// error, which the export maps to "client not installed".
func TestDefaultCommander_StartFailure(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "infisical")
	_, _, code, err := defaultCommander{}.Run(context.Background(), missing, nil)
	if code != -1 || err == nil {
		t.Fatalf("Run on a missing binary = (%d, %v), want (-1, non-nil)", code, err)
	}

	t.Setenv("PATH", t.TempDir())
	_, _, err = runInfisicalExport(context.Background(), nil, "proj", "dev", "/", "")
	if !errors.Is(err, vault.ErrClientNotInstalled) {
		t.Fatalf("export with no client on PATH: err = %v, want ErrClientNotInstalled", err)
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("start failure reported as a timeout: %v", err)
	}
}

// The caller cancelling its own context is not this call's timeout.
func TestRunInfisicalExport_CallerCancellationIsNotATimeout(t *testing.T) {
	putStubOnPath(t, writeInfisicalStub(t, "sleep 600"))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, _, err := runInfisicalExport(ctx, nil, "proj", "dev", "/", "")
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("export returned %s after the caller cancelled, want promptly", elapsed)
	}
	if err == nil {
		t.Fatal("cancelled export returned no error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("caller cancellation reported as a timeout: %v", err)
	}
}

// A caller that cancels while a forked child keeps the pipes open past
// WaitDelay gets neither a timeout nor "client not installed": the
// client started and exited, so its exit code decides.
func TestRunInfisicalExport_CallerCancelWithLeftoverChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("STUB_CHILD_PID_FILE", pidFile)
	putStubOnPath(t, writeInfisicalStub(t, `echo '{"API_KEY":"value"}'
sleep 600 &
echo $! > "$STUB_CHILD_PID_FILE"
exit 0`))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	values, _, err := runInfisicalExport(ctx, nil, "proj", "dev", "/", "")
	if errors.Is(err, vault.ErrClientNotInstalled) {
		t.Fatalf("started client reported as not installed: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "timed out") {
		t.Errorf("caller cancellation reported as a timeout: %v", err)
	}
	if err == nil && values["API_KEY"] != "value" {
		t.Errorf("values = %v, want the stub's output", values)
	}
	assertProcessGone(t, readPID(t, pidFile))
}

// A fake commander that simulates a hang by blocking on its context
// is cut off by the export's own deadline.
func TestRunInfisicalExport_HangingFakeTimesOut(t *testing.T) {
	quietOverride(t, "100ms")
	_, _, err := runInfisicalExport(context.Background(), hangingCommander{}, "proj", "dev", "/", "")
	assertExportTimeout(t, err, "100ms")
}

// R7 with the real bound: a hanging stub against the unshortened 30 s
// export deadline returns within 35 s.
func TestRunInfisicalExport_RealBound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits for the real 30s export bound")
	}
	t.Parallel()
	if os.Getenv(testTimeoutEnv) != "" {
		t.Skipf("%s is set in the environment; this test needs the real bound", testTimeoutEnv)
	}
	c := stubCommander{path: writeInfisicalStub(t, "sleep 600")}

	start := time.Now()
	_, _, err := runInfisicalExport(context.Background(), c, "proj", "dev", "/", "")
	elapsed := time.Since(start)

	assertExportTimeout(t, err, "30s")
	if elapsed > 35*time.Second {
		t.Errorf("export returned after %s, want within 35s", elapsed)
	}
}
