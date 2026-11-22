//go:build !niwa_functional

package mirror

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/testhook"
)

// A release build reads none of the test build's hooks: with all three
// NIWA_TEST_* variables set, the mirror seeds, refreshes and restores as it
// would without them, waits its full lock wait, and writes no probe file.
func TestReleaseBuild_TheTestHooksChangeNothing(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "git-env-probe.jsonl")
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+testhook.LabelBorrowClone+",error:injected@"+testhook.LabelDissociateCopy)
	t.Setenv("NIWA_TEST_MIRROR_LOCK_WAIT", "1ms")
	t.Setenv("NIWA_TEST_GIT_ENV_PROBE", probe)

	for _, label := range []string{testhook.LabelBorrowClone, testhook.LabelDissociateCopy} {
		if err := testhook.Fault(label); err != nil {
			t.Fatalf("testhook.Fault(%q) = %v in a release build, want nil", label, err)
		}
	}
	if got := testhook.MirrorLockWait(DefaultLockWait); got != DefaultLockWait {
		t.Fatalf("testhook.MirrorLockWait(%s) = %s in a release build, want it unchanged", DefaultLockWait, got)
	}

	f := seeded(t)
	pushCommit(t, f, "main", "second")
	requireOutcome(t, seedCall(t, f, refreshing(f.seedOpts())), FaultNone, OutcomeHealthy)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.mdir(), ".store")); err != nil {
		t.Fatal(err)
	}
	requireRestored(t, "Seed after the mirror was deleted", seedCall(t, f, f.seedOpts()))

	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "other restore")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	f.borrow(t)
	opts := f.opts()
	opts.LockWait = 300 * time.Millisecond
	start := time.Now()
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if waited := time.Since(start); waited < opts.LockWait {
		t.Fatalf("the restore gave up on the held lock after %s, want the full %s", waited, opts.LockWait)
	}
	if len(res) != 1 || res[0].Outcome != OutcomeBusy {
		t.Fatalf("result = %+v, want busy", res)
	}

	if _, err := os.Lstat(probe); !os.IsNotExist(err) {
		t.Fatalf("a release build wrote the probe file %s (%v)", probe, err)
	}
}
