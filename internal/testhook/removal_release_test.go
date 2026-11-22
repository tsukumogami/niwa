//go:build !niwa_functional

package testhook

import "testing"

// TestRelease_RemovalFileRemotesIsOff checks the untagged build never admits
// file remotes, whatever the environment says.
func TestRelease_RemovalFileRemotesIsOff(t *testing.T) {
	t.Setenv("NIWA_TEST_REMOVAL_FILE_REMOTES", "1")
	if RemovalFileRemotes() {
		t.Fatal("RemovalFileRemotes() = true in a release build")
	}
}
