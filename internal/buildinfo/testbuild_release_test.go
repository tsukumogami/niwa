//go:build !niwa_functional

package buildinfo

import "testing"

func TestIsTestBuild_FalseWithoutTheTag(t *testing.T) {
	if IsTestBuild() {
		t.Fatal("IsTestBuild() = true in a build without the niwa_functional tag")
	}
}
