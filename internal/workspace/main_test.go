package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points XDG_STATE_HOME at a throwaway directory for the whole
// package. The provisioning pipeline writes every value it resolves
// through a store-identified provider to the store of last-resolved
// values under $XDG_STATE_HOME, so without this a test that runs the
// pipeline against a stubbed Infisical CLI would write into the
// developer's real store. Tests that inspect the store set their own
// XDG_STATE_HOME with t.Setenv. HOME and XDG_CONFIG_HOME get throwaway
// directories for the same reason.
func TestMain(m *testing.M) {
	os.Exit(runWithPrivateStateHome(m))
}

func runWithPrivateStateHome(m *testing.M) int {
	dir, err := os.MkdirTemp("", "niwa-workspace-state-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "creating the private state directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)
	if err := os.Setenv("XDG_STATE_HOME", dir); err != nil {
		fmt.Fprintf(os.Stderr, "setting XDG_STATE_HOME: %v\n", err)
		return 1
	}
	// Tests that clone an overlay without setting HOME or XDG_CONFIG_HOME
	// would otherwise write under the developer's ~/.config/niwa.
	for k, v := range map[string]string{
		"HOME":            filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
	} {
		if err := os.Setenv(k, v); err != nil {
			fmt.Fprintf(os.Stderr, "setting %s: %v\n", k, err)
			return 1
		}
	}
	return m.Run()
}
