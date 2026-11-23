//go:build !niwa_functional

package cli

import (
	"strings"
	"testing"
)

func TestVersion_ReleaseBuildDoesNotSayTestBuild(t *testing.T) {
	out := versionOutput(t)
	if !strings.HasPrefix(out, "niwa ") {
		t.Fatalf("niwa version printed %q, want it to start with the version line", out)
	}
	if strings.Contains(out, "test build") {
		t.Fatalf("a build without the niwa_functional tag printed %q, which names the test build", out)
	}
}
