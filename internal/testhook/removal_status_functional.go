//go:build niwa_functional

package testhook

import (
	"os"
	"os/exec"
)

// removalPlantAtEnv names the stage the plant command runs at, StageRule or
// StageReread, and removalPlantCmdEnv the command.
const (
	removalPlantAtEnv  = "NIWA_TEST_REMOVAL_PLANT_AT"
	removalPlantCmdEnv = "NIWA_TEST_REMOVAL_PLANT_CMD"
)

// RemovalBeforeStatus runs the command NIWA_TEST_REMOVAL_PLANT_CMD names,
// with /bin/sh, when NIWA_TEST_REMOVAL_PLANT_AT names stage: only in the test
// build. The removal path calls it between a repository's configuration read
// and the git status after it, so a scenario can write a key into the
// repository's configuration in that window, as a process still alive in
// the instance could. The command gets the repository as
// NIWA_PLANT_GIT_DIR and NIWA_PLANT_WORK_TREE and runs once per call; what
// it prints and how it exits are ignored.
func RemovalBeforeStatus(stage, gitDir, workTree string) {
	cmdText := os.Getenv(removalPlantCmdEnv)
	if cmdText == "" || os.Getenv(removalPlantAtEnv) != stage {
		return
	}
	cmd := exec.Command("/bin/sh", "-c", cmdText)
	cmd.Env = append(os.Environ(), "NIWA_PLANT_GIT_DIR="+gitDir, "NIWA_PLANT_WORK_TREE="+workTree)
	_ = cmd.Run()
}
