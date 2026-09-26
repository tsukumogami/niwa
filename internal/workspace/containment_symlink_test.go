package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// TestContainmentResolvesBothSidesTheSameWay pins the fix for a failure that
// only appeared on macOS.
//
// checkContainment used to resolve the target's longest existing prefix while
// falling back to the unresolved path for a parent that did not exist yet.
// Where a temporary directory sits under a symlink -- macOS resolves /var to
// /private/var -- the two sides then disagreed and a path well inside its
// parent read as an escape. Linux has no such symlink, so the whole test suite
// passed there while skills delivery for a github-sourced marketplace was
// broken on macOS.
//
// The symlinked root here reproduces that condition on any platform.
func TestContainmentResolvesBothSidesTheSameWay(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The parent deliberately does not exist yet, which is the case that broke:
	// a marketplace is checked before its content root is created.
	parent := filepath.Join(link, "content", "marketplaces")
	target := filepath.Join(parent, "tools")

	if err := checkContainment(target, parent); err != nil {
		t.Fatalf("a path directly inside its parent was rejected: %v", err)
	}

	// The check must still catch a real escape, or the fix would have bought
	// correctness on one platform by disabling the guard everywhere.
	if err := checkContainment(filepath.Join(parent, "..", "..", "escape"), parent); err == nil {
		t.Fatal("a path outside the parent was accepted")
	}
}
