//go:build !niwa_functional

package testhook

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRelease_HooksDoNothing(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "probe.jsonl")
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+LabelBorrowClone+",error:injected@"+LabelDissociateCopy)
	t.Setenv("NIWA_TEST_MIRROR_LOCK_WAIT", "1ms")
	t.Setenv("NIWA_TEST_GIT_ENV_PROBE", probe)
	for _, label := range []string{LabelBorrowClone, LabelDissociateCopy} {
		if err := Fault(label); err != nil {
			t.Fatalf("Fault(%q) = %v, want nil", label, err)
		}
	}
	if got := MirrorLockWait(10 * time.Second); got != 10*time.Second {
		t.Fatalf("MirrorLockWait(10s) = %s, want it unchanged", got)
	}
	ProbeGitEnv(KindNetwork, "clone", []string{"GIT_SSH_COMMAND=ssh"})
	if _, err := os.Lstat(probe); !os.IsNotExist(err) {
		t.Fatalf("the release build wrote the probe (%v)", err)
	}
}
