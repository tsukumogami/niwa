package testhook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuard_NoReleaseBuildFileNamesTheRemovalCAVariable keeps the variable
// that hands the removal path a CA bundle out of every release build.
func TestGuard_NoReleaseBuildFileNamesTheRemovalCAVariable(t *testing.T) {
	const name = "NIWA_TEST_REMOVAL_CA_FILE"
	root := moduleRoot(t)
	for _, path := range goFiles(t, root) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !inReleaseBuild(buildConstraint(t, path, src)) {
			continue
		}
		if bytes.Contains(src, []byte(name)) {
			rel, _ := filepath.Rel(root, path)
			t.Errorf("%s names %s but is built without the niwa_functional tag", rel, name)
		}
	}
}
