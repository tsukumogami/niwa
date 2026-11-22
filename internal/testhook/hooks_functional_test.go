//go:build niwa_functional

package testhook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Run with go test -tags niwa_functional ./internal/testhook/.

func TestFunctional_FaultFiresForItsLabel(t *testing.T) {
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+LabelBorrowClone)
	if err := Fault(LabelBorrowClone); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("Fault(%q) = %v, want the injected error", LabelBorrowClone, err)
	}
	if err := Fault(LabelDissociateCopy); err != nil {
		t.Fatalf("Fault(%q) = %v, want nil for a label the spec doesn't name", LabelDissociateCopy, err)
	}
}

func TestFunctional_MirrorLockWaitShortensOnly(t *testing.T) {
	def := 5 * time.Minute
	cases := map[string]time.Duration{
		"":      def,
		"2s":    2 * time.Second,
		"5m":    def,
		"10m":   def,
		"0":     def,
		"-1s":   def,
		"abc":   def,
		"250ms": 250 * time.Millisecond,
	}
	for raw, want := range cases {
		t.Setenv(lockWaitEnv, raw)
		if got := MirrorLockWait(def); got != want {
			t.Errorf("MirrorLockWait(%s) with %q = %s, want %s", def, raw, got, want)
		}
	}
	t.Setenv(lockWaitEnv, "20s")
	if got := MirrorLockWait(10 * time.Second); got != 10*time.Second {
		t.Errorf("MirrorLockWait(10s) with 20s = %s, want 10s", got)
	}
}

func TestFunctional_ProbeGitEnvAppendsOneLinePerProcess(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "probe.jsonl")
	t.Setenv(probeEnv, probe)
	ProbeGitEnv(KindNetwork, "clone", []string{"PATH=/bin", "GIT_SSH_COMMAND=ssh -v", "GIT_TERMINAL_PROMPT=1", "GIT_TERMINAL_PROMPT=0", "NOT_GIT=1"})
	ProbeGitEnv(KindLocal, "rev-parse", []string{"GIT_OPTIONAL_LOCKS=0"})
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("the probe holds %d lines, want 2:\n%s", len(lines), data)
	}
	var first, second probeLine
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if first.Kind != KindNetwork || first.Call != "clone" || len(first.Env) != 2 ||
		first.Env["GIT_SSH_COMMAND"] != "ssh -v" || first.Env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Fatalf("first line = %+v", first)
	}
	if second.Kind != KindLocal || second.Call != "rev-parse" || len(second.Env) != 1 || second.Env["GIT_OPTIONAL_LOCKS"] != "0" {
		t.Fatalf("second line = %+v", second)
	}
	info, err := os.Stat(probe)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the probe file's mode = %v (%v), want 0600", info.Mode().Perm(), err)
	}
}

func TestFunctional_ProbeGitEnvWritesNothingWhenUnset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(probeEnv, "")
	t.Chdir(dir)
	ProbeGitEnv(KindLocal, "rev-parse", []string{"GIT_OPTIONAL_LOCKS=0"})
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("an unset probe wrote %v (%v)", entries, err)
	}
}
