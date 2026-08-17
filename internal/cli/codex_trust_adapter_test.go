package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/workspace"
)

func TestConfigureCodexTrustWiresTheWriter(t *testing.T) {
	applier := workspace.NewApplier(nil)
	if applier.EnsureCodexTrust != nil {
		t.Fatal("NewApplier wired the trust writer; the default must stay nil so unit suites never edit the developer's home")
	}
	configureCodexTrust(applier)
	if applier.EnsureCodexTrust == nil {
		t.Fatal("configureCodexTrust left the seam nil")
	}
}

// TestEveryApplierConstructionConfiguresCodexTrust guards the wiring across
// surfaces: an Applier that runs the pipeline without it prepares repositories
// whose Codex sessions cannot write a file, and nothing in the run says so.
func TestEveryApplierConstructionConfiguresCodexTrust(t *testing.T) {
	construct := regexp.MustCompile(`workspace\.NewApplier\(`)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".go" || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !construct.Match(body) {
			continue
		}
		if !regexp.MustCompile(`configureCodexTrust\(`).Match(body) {
			t.Errorf("%s constructs an Applier without calling configureCodexTrust", name)
		}
	}
}
