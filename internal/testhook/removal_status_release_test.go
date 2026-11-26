//go:build !niwa_functional

package testhook

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRelease_RemovalBeforeStatusRunsNothing checks the untagged build never
// runs the plant command, whatever the environment says, at either stage.
func TestRelease_RemovalBeforeStatusRunsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "planted")
	t.Setenv("NIWA_TEST_REMOVAL_PLANT_CMD", "touch "+marker)
	for _, stage := range []string{StageRule, StageReread} {
		t.Setenv("NIWA_TEST_REMOVAL_PLANT_AT", stage)
		RemovalBeforeStatus(stage, "/repo/.git", "/repo")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a release build ran the plant command")
	}
}
