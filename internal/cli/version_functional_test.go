//go:build niwa_functional

package cli

import (
	"strings"
	"testing"
)

func TestVersion_TestBuildSaysSo(t *testing.T) {
	out := versionOutput(t)
	if !strings.Contains(out, "  build:  test build\n") {
		t.Fatalf("the niwa_functional build printed %q, want a \"test build\" line", out)
	}
}
