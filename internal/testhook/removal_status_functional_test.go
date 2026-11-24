//go:build niwa_functional

package testhook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Run with go test -tags niwa_functional ./internal/testhook/.

// TestFunctional_RemovalBeforeStatusRunsAtItsStageOnly checks the test
// build runs the plant command at the named stage, with the repository in
// its environment, and at no other stage.
func TestFunctional_RemovalBeforeStatusRunsAtItsStageOnly(t *testing.T) {
	out := filepath.Join(t.TempDir(), "planted")
	t.Setenv(removalPlantCmdEnv, `printf '%s %s\n' "$NIWA_PLANT_GIT_DIR" "$NIWA_PLANT_WORK_TREE" >> `+out)
	t.Setenv(removalPlantAtEnv, StageReread)
	RemovalBeforeStatus(StageRule, "/a/.git", "/a")
	if _, err := os.Stat(out); err == nil {
		t.Fatal("the plant command ran at a stage the variable doesn't name")
	}
	RemovalBeforeStatus(StageReread, "/b/.git", "/b")
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the plant command didn't run at its stage: %v", err)
	}
	if strings.TrimSpace(string(got)) != "/b/.git /b" {
		t.Fatalf("plant command saw %q, want the repository's directories", got)
	}
}
