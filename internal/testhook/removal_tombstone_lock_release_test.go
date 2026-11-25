//go:build !niwa_functional

package testhook

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRelease_RemovalAtTombstoneLockRunsNothing checks the untagged build never
// runs the plant command at the tombstone lock, whatever the environment says.
func TestRelease_RemovalAtTombstoneLockRunsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "planted")
	t.Setenv("NIWA_TEST_REMOVAL_PLANT_CMD", "touch "+marker)
	t.Setenv("NIWA_TEST_REMOVAL_PLANT_AT", StageTombstoneLock)
	RemovalAtTombstoneLock("/workspace/instance")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a release build ran the plant command at the tombstone lock")
	}
}
