//go:build !niwa_functional

package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/testhook"
)

// The three test-build variables do nothing in a release build: a create
// with them set makes the same clones, borrowing as they do without them,
// and succeeds as one without them, and writes no probe file.
func TestReleaseBuild_TheTestHookVariablesDoNotChangeACreate(t *testing.T) {
	sb := newBorrowSandbox(t)
	extra := "share_git_history = true\n"

	plain := sb.create(t, extra)
	if plain.err != nil {
		t.Fatalf("Create without the variables: %v", plain.err)
	}
	want := plain.clones(t)
	plain.requireBorrowsByDefault(t)

	probe := filepath.Join(t.TempDir(), "git-env-probe.jsonl")
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+testhook.LabelBorrowClone)
	t.Setenv("NIWA_TEST_MIRROR_LOCK_WAIT", "1ms")
	t.Setenv("NIWA_TEST_GIT_ENV_PROBE", probe)

	hooked := sb.create(t, extra)
	if hooked.err != nil {
		t.Fatalf("Create with the variables: %v, want it to succeed as it does without them", hooked.err)
	}
	if got := hooked.clones(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("clones with the variables = %v, want %v as without them", got, want)
	}
	// The borrow-clone fault, had it fired, would have made each clone full.
	hooked.requireBorrowsByDefault(t)
	if _, err := os.Lstat(probe); !os.IsNotExist(err) {
		t.Fatalf("a release build wrote the probe file %s (%v)", probe, err)
	}
}
