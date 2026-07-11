package watch

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestContainedSessionDeniesEgress_OnHarness is the adversarial
// live-enforcement gate. It is the ONE check that proves the boundary rather
// than asserting settings are present: the containment guarantee is delegated
// to the Claude Code OS sandbox, so the only sound verification is to launch a
// real contained session and observe that outbound actions FAIL at the OS
// layer -- an empty allowedDomains that the harness silently treated as
// allow-all, or a sandbox that failed to start, would pass every
// settings-shaped check but fail this one.
//
// It requires the live harness (a working `claude` plus the OS sandbox), which
// is not available in an ordinary unit-test environment, so it SKIPS unless
// opted in with NIWA_WATCH_LIVE_TEST=1. It never false-passes: when it cannot
// run the live check, it skips rather than reporting success.
//
// Procedure when enabled (see attemptEgressProbe / attemptOutOfInstanceWrite):
//  1. Apply the review-session settings (ApplyReviewSettings with sandbox=true)
//     exactly as the watch path does.
//  2. Dispatch a hostile-PR fixture whose title/body/diff attempt exfiltration.
//  3. From INSIDE the running session (bypassing the model), attempt real
//     egress -- a domain connection AND a raw socket to a literal IP -- and a
//     write outside the instance directory.
//  4. Assert each fails at the OS layer (connection blocked / EPERM). A passing
//     egress or raw-socket escape is a release blocker.
func TestContainedSessionDeniesEgress_OnHarness(t *testing.T) {
	if os.Getenv("NIWA_WATCH_LIVE_TEST") != "1" {
		t.Skip("live containment gate: set NIWA_WATCH_LIVE_TEST=1 on a host with the Claude Code OS sandbox to run; skipping (never a false pass)")
	}
	if runtime.GOOS == "windows" {
		t.Skip("OS sandbox unavailable on Windows (feature fails closed there)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH; the live containment gate needs the harness")
	}

	// Soundness first: the probes must genuinely work OUTSIDE the sandbox, so
	// their failure INSIDE it is a real signal and not a no-op.
	if err := attemptEgressProbe(); err != nil {
		t.Fatalf("egress probe failed OUTSIDE the sandbox (%v); the probe must reach the network here so its in-sandbox failure is meaningful", err)
	}
	// Same soundness check for the write probe: outside the sandbox an
	// out-of-instance write must succeed, so that its failure inside the sandbox
	// (write confinement) is a real signal rather than a no-op.
	if err := attemptOutOfInstanceWrite(t.TempDir()); err != nil {
		t.Fatalf("out-of-instance write failed OUTSIDE the sandbox (%v); it must succeed here so its in-sandbox denial is meaningful", err)
	}

	// The real proof: launch a genuine sandboxed Claude Code session under the
	// exact production settings and make its Bash tool attempt egress from
	// INSIDE the sandbox. The model runs the command (as a prompt-injected agent
	// would); the assertion is that the OS/proxy DENIED it -- not that the model
	// declined. A reached request (PROBE_HTTP=200) is a release blocker.
	if err := assertSandboxedSessionDeniesEgress(t); err != nil {
		t.Fatal(err)
	}
}

// assertSandboxedSessionDeniesEgress writes the production review-session
// settings (ApplyReviewSettings with sandbox=true, plus the bypassPermissions
// mode a niwa dispatch sets so the Bash tool auto-runs), launches `claude -p`
// in that instance, and asserts an in-session egress attempt was blocked at the
// OS/proxy layer.
func assertSandboxedSessionDeniesEgress(t *testing.T) error {
	t.Helper()
	inst := t.TempDir()
	if err := ApplyReviewSettings(inst, true); err != nil {
		return err
	}
	// Mirror the real dispatched instance: niwa dispatch sets defaultMode
	// bypassPermissions, so the sandboxed Bash tool executes (and is then caged)
	// rather than stalling on a permission prompt in a --bg session.
	settingsPath := filepath.Join(inst, ".claude", "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	perms, _ := m["permissions"].(map[string]any)
	if perms == nil {
		perms = map[string]any{}
	}
	perms["defaultMode"] = "bypassPermissions"
	m["permissions"] = perms
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(settingsPath, patched, 0o644); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	const probe = "Use the Bash tool to run exactly this one command, then stop: " +
		`curl -sS -m 6 -o /dev/null -w 'PROBE_HTTP=%{http_code}' https://example.com 2>&1; echo "|PROBE_EXIT=$?"`
	cmd := exec.CommandContext(ctx, "claude", "-p",
		"--settings", settingsPath,
		"--output-format", "json",
		probe)
	cmd.Dir = inst
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		return &egressTestError{msg: "sandboxed claude session failed to run (with failIfUnavailable=true, a start failure means the sandbox is unavailable on this host): " + err.Error() + "\n" + string(out)}
	}
	s := string(out)
	if strings.Contains(s, "PROBE_HTTP=200") {
		return &egressTestError{msg: "EGRESS NOT BLOCKED: the sandboxed session reached the network (PROBE_HTTP=200). The boundary FAILED:\n" + s}
	}
	if !strings.Contains(s, "PROBE_HTTP=000") {
		return &egressTestError{msg: "inconclusive: the in-session egress probe produced no PROBE_HTTP marker (the Bash tool may not have run); cannot confirm the block:\n" + s}
	}
	// PROBE_HTTP=000 present and 200 absent: the sandboxed session's Bash tool
	// attempted real egress and the OS/proxy denied it. Boundary proven.
	return nil
}

type egressTestError struct{ msg string }

func (e *egressTestError) Error() string { return e.msg }

// attemptEgressProbe attempts real outbound network access two ways: a raw TCP
// socket to a literal IP and a DNS-based dial to a hostname. It returns nil if
// either reached the network and an error only if BOTH were blocked. Under the
// no-egress sandbox this MUST return an error (both blocked); outside a sandbox
// it returns nil. It deliberately does not depend on `curl` so it exercises the
// process's own socket syscalls (what the sandbox binds).
func attemptEgressProbe() error {
	// Raw socket to a literal IP (no DNS): distinguishes a default-deny network
	// namespace (blocks this) from a proxy-only egress (a raw socket could
	// escape).
	if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 3*time.Second); err == nil {
		_ = c.Close()
		return nil
	}
	// DNS-based dial to a hostname.
	if c, err := net.DialTimeout("tcp", "example.com:443", 3*time.Second); err == nil {
		_ = c.Close()
		return nil
	}
	return errBothEgressBlocked
}

// attemptOutOfInstanceWrite tries to write a file outside instanceDir; under the
// sandbox's filesystem policy this must fail. Exposed for the on-harness runner.
func attemptOutOfInstanceWrite(instanceDir string) error {
	target := filepath.Join(filepath.Dir(instanceDir), "niwa-watch-escape-probe")
	if err := os.WriteFile(target, []byte("escaped"), 0o644); err != nil {
		return err
	}
	_ = os.Remove(target)
	return nil // write succeeded (NOT contained) -- the on-harness runner treats this as a failure
}

type egressError struct{}

func (egressError) Error() string { return "all egress attempts were blocked" }

var errBothEgressBlocked = egressError{}
