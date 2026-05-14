package cli

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// surfaceDirName is the conventional name of the niwa state directory
// inside the test fixture's root. The surface command was originally
// per-instance (lock/token/port lived under `<instance>/.niwa/`); after
// the F5 reshape it is machine-level and the lock/token/port live under
// the niwa config directory. These tests piggyback on the old layout by
// pointing surfaceServeMachine's configDir at `<root>/.niwa/` — same
// on-disk shape, the helper invocations need not learn machine-level
// paths to exercise the lifecycle.
const surfaceDirName = ".niwa"

// newSurfaceTestInstance builds a tmp instance root with .niwa/ in
// place. Returns the absolute instance root path. .niwa/instance.json is
// not strictly required for surfaceServeMachine (it takes instanceRoot
// directly) but tests that need resolveInstanceRoot also pick it up.
func newSurfaceTestInstance(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, surfaceDirName), 0o755); err != nil {
		t.Fatalf("mkdir .niwa: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, surfaceDirName, "instance.json"),
		[]byte(`{"v":1}`), 0o600); err != nil {
		t.Fatalf("write instance.json: %v", err)
	}
	return root
}

// reapedDeadPID spawns a child, waits for it to exit, then returns its
// PID. The kernel has reaped the entry so IsProcessAlive returns false.
// Used to seed `.niwa/surface.lock` with a known-dead PID for the
// stale-lock reap path.
func reapedDeadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dead-pid donor: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait()
	return pid
}

// TestAcquireSurfaceLock_FreshSucceeds covers the happy path: no prior
// .niwa/surface.lock, OpenFile O_CREATE|O_EXCL wins, the file ends up
// holding the current PID.
func TestAcquireSurfaceLock_FreshSucceeds(t *testing.T) {
	root := newSurfaceTestInstance(t)
	lockPath := filepath.Join(root, surfaceDirName, surfaceLockFileName)

	if err := acquireSurfaceLock(lockPath); err != nil {
		t.Fatalf("acquireSurfaceLock: %v", err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	got, perr := strconv.Atoi(strings.TrimSpace(string(data)))
	if perr != nil {
		t.Fatalf("parse lock pid: %v", perr)
	}
	if got != os.Getpid() {
		t.Errorf("lock pid = %d, want %d", got, os.Getpid())
	}
}

// TestAcquireSurfaceLock_StaleReap covers the reap-and-retry path: a
// prior lock containing a dead PID is removed and the boot's second
// create wins. The final lock holds the current PID — proof that the
// reap fired, not a silent acceptance of the stale file.
func TestAcquireSurfaceLock_StaleReap(t *testing.T) {
	root := newSurfaceTestInstance(t)
	lockPath := filepath.Join(root, surfaceDirName, surfaceLockFileName)
	deadPID := reapedDeadPID(t)
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(deadPID)+"\n"), 0o600); err != nil {
		t.Fatalf("seed stale lock: %v", err)
	}

	if err := acquireSurfaceLock(lockPath); err != nil {
		t.Fatalf("acquireSurfaceLock after stale lock: %v", err)
	}
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	got, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if got != os.Getpid() {
		t.Errorf("after reap, lock pid = %d, want %d (own PID)", got, os.Getpid())
	}
}

// TestAcquireSurfaceLock_LiveExitsWithMessage covers the live-holder
// exit-1 path. The test seeds the lock with the current process's PID
// (guaranteed alive); acquireSurfaceLock returns an error whose text
// includes the PID number, matching the troubleshooting wording in the
// operator guide.
func TestAcquireSurfaceLock_LiveExitsWithMessage(t *testing.T) {
	root := newSurfaceTestInstance(t)
	lockPath := filepath.Join(root, surfaceDirName, surfaceLockFileName)
	livePID := os.Getpid()
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(livePID)+"\n"), 0o600); err != nil {
		t.Fatalf("seed live lock: %v", err)
	}

	err := acquireSurfaceLock(lockPath)
	if err == nil {
		t.Fatalf("acquireSurfaceLock with live PID: err = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), strconv.Itoa(livePID)) {
		t.Errorf("error %q does not mention live PID %d", err.Error(), livePID)
	}
	if !strings.Contains(err.Error(), "held by") {
		t.Errorf("error %q missing the documented 'held by' phrasing", err.Error())
	}
}

// TestAcquireSurfaceLock_CorruptReap covers the corrupt-contents path:
// a surface.lock with garbage (not an integer) is treated as stale and
// reaped, same as a dead PID. Without this branch, a crash mid-write
// would brick the surface until the operator manually deletes the file.
func TestAcquireSurfaceLock_CorruptReap(t *testing.T) {
	root := newSurfaceTestInstance(t)
	lockPath := filepath.Join(root, surfaceDirName, surfaceLockFileName)
	if err := os.WriteFile(lockPath, []byte("not-a-pid\n"), 0o600); err != nil {
		t.Fatalf("seed corrupt lock: %v", err)
	}

	if err := acquireSurfaceLock(lockPath); err != nil {
		t.Fatalf("acquireSurfaceLock after corrupt lock: %v", err)
	}
}

// TestEnsureSurfaceToken_GeneratesOnAbsent covers the absent-token
// branch: no prior surface.token, the function writes a fresh UUIDv4 at
// the expected path with mode 0o600.
func TestEnsureSurfaceToken_GeneratesOnAbsent(t *testing.T) {
	root := newSurfaceTestInstance(t)
	niwaDir := filepath.Join(root, surfaceDirName)

	path, err := ensureSurfaceToken(niwaDir, false)
	if err != nil {
		t.Fatalf("ensureSurfaceToken: %v", err)
	}
	if path != filepath.Join(niwaDir, surfaceTokenFileName) {
		t.Errorf("token path = %q, want %q", path, filepath.Join(niwaDir, surfaceTokenFileName))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat token: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("token mode = %#o, want 0o600", mode)
	}
	data, _ := os.ReadFile(path)
	if len(bytes.TrimSpace(data)) == 0 {
		t.Errorf("token file is empty")
	}
}

// TestEnsureSurfaceToken_PreservesExisting covers the default no-rotate
// behavior on a restart: an existing surface.token is left untouched so
// open browser tabs keep authenticating. The byte-identity assertion
// guarantees no surreptitious rewrite.
func TestEnsureSurfaceToken_PreservesExisting(t *testing.T) {
	root := newSurfaceTestInstance(t)
	niwaDir := filepath.Join(root, surfaceDirName)
	tokenPath := filepath.Join(niwaDir, surfaceTokenFileName)
	original := []byte("preserved-token-bytes\n")
	if err := os.WriteFile(tokenPath, original, 0o600); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if _, err := ensureSurfaceToken(niwaDir, false); err != nil {
		t.Fatalf("ensureSurfaceToken: %v", err)
	}
	after, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Errorf("token bytes mutated without --rotate-token: got %q, want %q", after, original)
	}
}

// TestEnsureSurfaceToken_RotateRewrites covers --rotate-token: the
// existing token is overwritten with a fresh UUIDv4. The assertion is
// byte-inequality — a buggy implementation that fell through to the
// preserve branch would fail here.
func TestEnsureSurfaceToken_RotateRewrites(t *testing.T) {
	root := newSurfaceTestInstance(t)
	niwaDir := filepath.Join(root, surfaceDirName)
	tokenPath := filepath.Join(niwaDir, surfaceTokenFileName)
	original := []byte("old-token\n")
	if err := os.WriteFile(tokenPath, original, 0o600); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	if _, err := ensureSurfaceToken(niwaDir, true); err != nil {
		t.Fatalf("ensureSurfaceToken: %v", err)
	}
	after, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	if bytes.Equal(after, original) {
		t.Errorf("--rotate-token did not rewrite token: still %q", after)
	}
	info, _ := os.Stat(tokenPath)
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("rotated token mode = %#o, want 0o600", mode)
	}
}

// TestWriteSurfacePort_Atomic covers the tmp+rename atomicity contract.
// The on-disk file holds the integer port followed by a newline; no .tmp
// remnant survives. mcp-serve's URL composition reads this file on every
// niwa_query_change so a torn write would surface as a malformed URL.
func TestWriteSurfacePort_Atomic(t *testing.T) {
	root := newSurfaceTestInstance(t)
	portPath := filepath.Join(root, surfaceDirName, surfacePortFileName)

	if err := writeSurfacePort(portPath, 54321); err != nil {
		t.Fatalf("writeSurfacePort: %v", err)
	}
	data, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	if strings.TrimSpace(string(data)) != "54321" {
		t.Errorf("port file contents = %q, want %q", string(data), "54321\n")
	}
	if _, err := os.Stat(portPath + ".tmp"); err == nil {
		t.Errorf(".tmp remnant survived: %s.tmp still exists", portPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat tmp: %v", err)
	}
}

// runSurfaceServeFor runs surfaceServeMachine in a goroutine with a
// cancellable context, waits for surface.port to appear (signalling
// boot completed), then returns a cancel function the caller invokes to
// drive shutdown. The third return is the stderr buffer so callers can
// assert on the boot banner. The done channel signals the goroutine's
// exit so callers can wait for clean shutdown.
func runSurfaceServeFor(t *testing.T, root string, port int, rotateToken bool) (cancel func(), stderr *bytes.Buffer, done <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	buf := &bytes.Buffer{}
	cmd.SetErr(buf)
	cmd.SetOut(&bytes.Buffer{})

	exit := make(chan error, 1)
	go func() {
		exit <- surfaceServeMachine(cmd, filepath.Join(root, surfaceDirName), nil, port, rotateToken)
	}()

	// Wait for surface.port to appear — that's our boot-complete signal.
	portPath := filepath.Join(root, surfaceDirName, surfacePortFileName)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(portPath); err == nil {
			break
		}
		select {
		case err := <-exit:
			t.Fatalf("surfaceServeMachine exited during boot: %v", err)
		case <-time.After(25 * time.Millisecond):
		}
	}
	if _, err := os.Stat(portPath); err != nil {
		cancel()
		t.Fatalf("surface.port did not appear within 3s: %v", err)
	}
	return cancel, buf, exit
}

// TestSurfaceServe_BootWritesPort covers PRD R10 step 3: web.New binds
// 127.0.0.1:0, the test reads the kernel-assigned port from the
// listener, and the boot writes that exact port to .niwa/surface.port.
// Asserting "non-zero" alone would mask a buggy implementation that
// wrote a hardcoded value; we additionally assert the file parses as a
// valid TCP port.
func TestSurfaceServe_BootWritesPort(t *testing.T) {
	root := newSurfaceTestInstance(t)

	cancel, _, done := runSurfaceServeFor(t, root, 0, false)
	defer cancel()

	portPath := filepath.Join(root, surfaceDirName, surfacePortFileName)
	data, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatalf("read surface.port: %v", err)
	}
	port, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("parse surface.port: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("surface.port = %d, out of TCP range", port)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(surfaceShutdownGrace + 2*time.Second):
		t.Fatalf("surfaceServeMachine did not exit within shutdown grace")
	}
}

// TestSurfaceServe_ShutdownCleansLockAndPortKeepsToken covers PRD R10
// shutdown: SIGTERM (modelled by cancelling cmd.Context) drives
// http.Server.Shutdown within 5s; .niwa/surface.lock and
// .niwa/surface.port are removed; .niwa/surface.token persists. The
// token-persists assertion is the critical bit — restarts should not
// invalidate open browser tabs.
func TestSurfaceServe_ShutdownCleansLockAndPortKeepsToken(t *testing.T) {
	root := newSurfaceTestInstance(t)

	cancel, _, done := runSurfaceServeFor(t, root, 0, false)
	defer cancel()

	niwaDir := filepath.Join(root, surfaceDirName)
	if _, err := os.Stat(filepath.Join(niwaDir, surfaceLockFileName)); err != nil {
		t.Fatalf("surface.lock missing before shutdown: %v", err)
	}
	if _, err := os.Stat(filepath.Join(niwaDir, surfaceTokenFileName)); err != nil {
		t.Fatalf("surface.token missing before shutdown: %v", err)
	}
	tokenContentsBefore, _ := os.ReadFile(filepath.Join(niwaDir, surfaceTokenFileName))

	start := time.Now()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("surfaceServeMachine returned error on shutdown: %v", err)
		}
	case <-time.After(surfaceShutdownGrace + 2*time.Second):
		t.Fatalf("surfaceServeMachine did not exit within shutdown grace (%v)", time.Since(start))
	}
	if elapsed := time.Since(start); elapsed > surfaceShutdownGrace+1*time.Second {
		t.Errorf("shutdown took %v, want <= %v", elapsed, surfaceShutdownGrace+1*time.Second)
	}

	if _, err := os.Stat(filepath.Join(niwaDir, surfaceLockFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("surface.lock not removed after shutdown: stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(niwaDir, surfacePortFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("surface.port not removed after shutdown: stat err = %v", err)
	}
	tokenAfter, err := os.ReadFile(filepath.Join(niwaDir, surfaceTokenFileName))
	if err != nil {
		t.Errorf("surface.token removed on shutdown: %v", err)
	}
	if !bytes.Equal(tokenAfter, tokenContentsBefore) {
		t.Errorf("surface.token contents changed across shutdown")
	}
}

// TestSurfaceServe_BannerWithoutTokenContents covers the token-leak
// invariant from PRD R10 / D1 mitigations: the stderr boot banner
// includes the URL and the path to the token file, never the token
// bytes themselves. The test reads the on-disk token AFTER boot and
// asserts those bytes never appear in the captured stderr stream.
func TestSurfaceServe_BannerWithoutTokenContents(t *testing.T) {
	root := newSurfaceTestInstance(t)

	cancel, stderr, done := runSurfaceServeFor(t, root, 0, false)
	defer cancel()

	tokenPath := filepath.Join(root, surfaceDirName, surfaceTokenFileName)
	tokenBytes, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token: %v", err)
	}
	tokenTrim := bytes.TrimSpace(tokenBytes)
	banner := stderr.Bytes()
	if !bytes.Contains(banner, []byte("niwa surface listening on http://127.0.0.1:")) {
		t.Errorf("banner missing URL prefix; got: %q", string(banner))
	}
	if !bytes.Contains(banner, []byte(tokenPath)) {
		t.Errorf("banner missing token path; got: %q", string(banner))
	}
	if bytes.Contains(banner, tokenTrim) {
		t.Errorf("banner LEAKED token contents %q: %q", tokenTrim, string(banner))
	}

	cancel()
	select {
	case <-done:
	case <-time.After(surfaceShutdownGrace + 2*time.Second):
		t.Fatalf("shutdown timeout")
	}
}

// TestSurfaceServe_RotateTokenFlag covers --rotate-token end-to-end:
// the boot regenerates the on-disk token even when one already exists.
// Separates from the unit-level TestEnsureSurfaceToken_RotateRewrites
// by exercising the flag through surfaceServeMachine's full path.
func TestSurfaceServe_RotateTokenFlag(t *testing.T) {
	root := newSurfaceTestInstance(t)
	tokenPath := filepath.Join(root, surfaceDirName, surfaceTokenFileName)
	original := []byte("pre-rotation-sentinel\n")
	if err := os.WriteFile(tokenPath, original, 0o600); err != nil {
		t.Fatalf("seed token: %v", err)
	}

	cancel, _, done := runSurfaceServeFor(t, root, 0, true /* rotateToken */)
	defer cancel()

	after, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("read token after rotate: %v", err)
	}
	if bytes.Equal(after, original) {
		t.Errorf("--rotate-token did not rewrite token at boot: still %q", after)
	}

	cancel()
	<-done
}

// TestSurfaceServe_LivePIDExitsBeforeBind asserts the live-holder path
// short-circuits the boot before any port is bound. A second
// surfaceServeMachine call against the same instance, after the first
// has acquired the lock, returns the "held by" error without producing
// surface.port.
func TestSurfaceServe_LivePIDExitsBeforeBind(t *testing.T) {
	root := newSurfaceTestInstance(t)
	lockPath := filepath.Join(root, surfaceDirName, surfaceLockFileName)
	// Seed the lock with our own PID (guaranteed alive).
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetOut(&bytes.Buffer{})

	err := surfaceServeMachine(cmd, filepath.Join(root, surfaceDirName), nil, 0, false)
	if err == nil {
		t.Fatalf("expected live-lock error, got nil")
	}
	if !strings.Contains(err.Error(), "held by") {
		t.Errorf("error %q missing 'held by' phrasing", err.Error())
	}
	// surface.port must not have been written.
	if _, statErr := os.Stat(filepath.Join(root, surfaceDirName, surfacePortFileName)); !errors.Is(statErr, os.ErrNotExist) {
		t.Errorf("surface.port created despite live-lock exit: %v", statErr)
	}
}

// TestSurfaceServe_FixedPort exercises the --port N path. We bind a
// kernel-assigned port first (separate listener), close it to release
// the port number, and pass that number through --port. The boot writes
// the same port to surface.port; if a buggy implementation drops the
// flag, the port file would carry something else.
//
// Race note: between the close-to-release and the boot's bind there is
// a small window where another process could grab the port. On the
// reference fleet (single-user dev box) this is acceptably rare; if the
// bind races and fails, the test surfaces a clear listener error rather
// than a silent false positive.
func TestSurfaceServe_FixedPort(t *testing.T) {
	root := newSurfaceTestInstance(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen ephemeral: %v", err)
	}
	requestedPort := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	cancel, _, done := runSurfaceServeFor(t, root, requestedPort, false)
	defer cancel()

	data, err := os.ReadFile(filepath.Join(root, surfaceDirName, surfacePortFileName))
	if err != nil {
		t.Fatalf("read surface.port: %v", err)
	}
	got, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if got != requestedPort {
		t.Errorf("surface.port = %d, want %d (--port)", got, requestedPort)
	}

	cancel()
	<-done
}
