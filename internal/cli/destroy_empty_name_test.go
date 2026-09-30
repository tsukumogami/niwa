package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunDestroy_EmptyNameRefuses: `niwa destroy "$NAME"` with NAME empty is
// the #342 shape without --force. It used to fall into the no-name modes, which
// delete an empty workspace outright and destroy the only instance of a
// one-instance workspace. It refuses instead, with and without --force, and
// leaves everything in place.
func TestRunDestroy_EmptyNameRefuses(t *testing.T) {
	for _, names := range [][]string{nil, {"alpha"}, {"alpha", "beta"}} {
		for _, force := range []bool{false, true} {
			for _, arg := range []string{"", "  "} {
				root := destroyTestSetup(t, names)
				chdirTo(t, root)
				quietDestroyCmd()
				setDestroyFlags(t, force, false, "")

				err := runDestroy(destroyCmd, []string{arg})
				if err == nil || !strings.Contains(err.Error(), "instance name is empty") {
					t.Fatalf("instances=%v force=%v arg=%q: expected the empty-name refusal, got %v", names, force, arg, err)
				}
				assertInstancesExist(t, root, names...)
				if _, err := os.Stat(filepath.Join(root, ".niwa", "workspace.toml")); err != nil {
					t.Errorf("instances=%v force=%v arg=%q: workspace should still exist: %v", names, force, arg, err)
				}
			}
		}
	}
}
