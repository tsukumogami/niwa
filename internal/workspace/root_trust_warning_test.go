package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/claudetrust"
)

func TestRootTrustWarning(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	workspaceRoot := t.TempDir()
	instance := filepath.Join(workspaceRoot, "alpha")
	if err := os.MkdirAll(instance, 0o755); err != nil {
		t.Fatal(err)
	}

	// No config file: untrusted, so warn.
	w := RootTrustWarning(home, instance)
	for _, want := range []string{instance, "untrusted", "trust_instances = true", "trust the workspace root"} {
		if !strings.Contains(w, want) {
			t.Errorf("warning %q lacks %q", w, want)
		}
	}

	// A trusted workspace root covers the instance under it.
	rootKey, err := claudetrust.Key(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude.json")
	body := `{"projects":{"` + rootKey + `":{"hasTrustDialogAccepted":true}}}`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := RootTrustWarning(home, instance); w != "" {
		t.Errorf("an instance under a trusted workspace root should not warn, got %q", w)
	}
	if after, _ := os.ReadFile(config); string(after) != body {
		t.Error("checking trust must not change the user's Claude config")
	}

	// No home: no check, no warning.
	if w := RootTrustWarning("", instance); w != "" {
		t.Errorf("an empty home must skip the check, got %q", w)
	}
}

// TestApplierRootTrustWarnings checks the Applier warns only when it wrote
// the integration and the instance root is untrusted.
func TestApplierRootTrustWarnings(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	instance := t.TempDir()
	a := &Applier{DeveloperHome: home}

	if got := a.rootTrustWarnings(instance, false); len(got) != 0 {
		t.Errorf("no integration written: want no warning, got %v", got)
	}
	if got := a.rootTrustWarnings(instance, true); len(got) != 1 {
		t.Errorf("untrusted root with integration: want one warning, got %v", got)
	}
	if got := (&Applier{}).rootTrustWarnings(instance, true); len(got) != 0 {
		t.Errorf("no developer home: want no warning, got %v", got)
	}
}
