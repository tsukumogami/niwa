package watch

import (
	"os"
	"os/exec"
	"runtime"
	"testing"
)

// The live gate for a review continuation, TestResumedSessionDeniesEgress_OnHarness,
// lives in internal/cli (watch_continuation_live_test.go), where it can reach the
// production stage launch, the pre-wake gate and the flagless wake. The helpers
// below are shared by the live tests that stay in this package.

// requireDisposableLiveHost gates the live containment tests. They launch REAL
// authenticated `claude` sessions that share your real ~/.claude (symlinked into an
// isolated HOME so the session can authenticate) and run them under a no-egress
// sandbox. On a primary workstation those throwaway no-egress sessions can look
// like your own login broke, and pointing sandboxed sessions at your real daemon
// and credential store is not something to do casually. So beyond
// NIWA_WATCH_LIVE_TEST=1, they require an explicit acknowledgment that this is a
// disposable/CI host -- never a developer's primary machine.
func requireDisposableLiveHost(t *testing.T) {
	t.Helper()
	if os.Getenv("NIWA_WATCH_LIVE_TEST") != "1" {
		t.Skip("live containment gate: set NIWA_WATCH_LIVE_TEST=1 to run; skipping (never a false pass)")
	}
	if os.Getenv("NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST") != "1" {
		t.Skip("live containment gate: these tests run REAL authenticated claude sessions " +
			"against your real ~/.claude under a no-egress sandbox. Run ONLY on a disposable/CI " +
			"host, never your primary workstation. Set NIWA_WATCH_LIVE_TEST_DISPOSABLE_HOST=1 to confirm.")
	}
	if runtime.GOOS == "windows" {
		t.Skip("OS sandbox unavailable on Windows (feature fails closed there)")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not on PATH; the live containment gate needs the harness")
	}
}

// fileExists reports whether path names an existing (non-dir) file.
func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

// instFiles lists the base names of files directly in dir (diagnostics only).
func instFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{"<unreadable: " + err.Error() + ">"}
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) == 0 {
		return []string{"<empty>"}
	}
	return names
}
