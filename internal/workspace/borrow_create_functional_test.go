//go:build niwa_functional

package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/testhook"
)

// The borrow-clone fault makes the borrowing clone fail after git ran: the
// create still succeeds, the clone is a full one with nothing left beside it,
// and the warning names the repo and borrow-clone-failed.
func TestTestBuildCreate_TheBorrowCloneFaultClonesInFull(t *testing.T) {
	sb := newBorrowSandbox(t)
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+testhook.LabelBorrowClone)
	c := createWithSetting(t, sb, "")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	for _, name := range borrowRepos {
		c.requireFull(t, name)
		requireOnlyEntryAmong(t, filepath.Join(c.instanceRoot, "tools"), name)
		want := "warning: " + fallbackWarning(name, fallbackBorrowCloneFailed)
		if !strings.Contains(c.stderr, want) {
			t.Errorf("the output lacks %q:\n%s", want, c.stderr)
		}
	}
}
