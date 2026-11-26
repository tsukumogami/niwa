//go:build !niwa_functional

package testhook

import "testing"

// TestRelease_RemovalCAFileIsEmpty checks the untagged build never hands the
// removal path's git calls a CA bundle, whatever the environment says.
func TestRelease_RemovalCAFileIsEmpty(t *testing.T) {
	t.Setenv("NIWA_TEST_REMOVAL_CA_FILE", "/tmp/planted-ca.pem")
	if got := RemovalCAFile(); got != "" {
		t.Fatalf("RemovalCAFile() = %q in a release build", got)
	}
}
