package testhook

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGuard_NoReleaseBuildFileNamesTheRemovalPlantVariables keeps the
// variables that run a command between a configuration read and a status on
// the removal path out of every release build.
func TestGuard_NoReleaseBuildFileNamesTheRemovalPlantVariables(t *testing.T) {
	names := []string{"NIWA_TEST_REMOVAL_PLANT_AT", "NIWA_TEST_REMOVAL_PLANT_CMD"}
	root := moduleRoot(t)
	tagged := 0
	for _, path := range goFiles(t, root) {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !inReleaseBuild(buildConstraint(t, path, src)) {
			tagged++
			continue
		}
		for _, name := range names {
			if bytes.Contains(src, []byte(name)) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("%s names %s but is built without the niwa_functional tag", rel, name)
			}
		}
	}
	if tagged == 0 {
		t.Fatal("no file built only with the niwa_functional tag was found; the guard reads nothing")
	}
}
