package cli

import (
	"path/filepath"
	"testing"
)

// canonicalTempDir returns t.TempDir() with its symlinks resolved.
//
// t.TempDir hands back the path unresolved. On macOS that path lives under
// /var, which is a symlink to /private/var, and a symlinked TMPDIR is common
// enough on Linux too. Production code, meanwhile, canonicalizes: os.Getwd
// returns the resolved spelling, and several commands call
// filepath.EvalSymlinks deliberately so that a workspace root recorded in the
// registry or handed to a shell is stable no matter how the user reached it.
//
// A test that keeps the raw t.TempDir() path and compares it against a path
// the code produced is therefore comparing two spellings of the same
// directory. Resolving once, here at the boundary, lets every assertion
// downstream stay an exact equality check instead of being loosened to a
// suffix or substring match that would also accept a genuinely wrong path.
//
// Use this anywhere a test's temp dir will be compared against a path that
// came back out of the code under test.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolving temp dir %q: %v", dir, err)
	}
	return resolved
}
