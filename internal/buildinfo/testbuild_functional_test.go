//go:build niwa_functional

package buildinfo

import "testing"

func TestIsTestBuild_TrueWithTheTag(t *testing.T) {
	if !IsTestBuild() {
		t.Fatal("IsTestBuild() = false in the niwa_functional build")
	}
}
