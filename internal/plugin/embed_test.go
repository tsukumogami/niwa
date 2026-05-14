package plugin

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestEmbedded_ManifestNameIsNiwa pins the build-time invariant
// that the embedded plugin's manifest name is "niwa". This guards
// against accidental forks shipping a renamed plugin under the
// same niwa binary.
func TestEmbedded_ManifestNameIsNiwa(t *testing.T) {
	data, err := pluginFS.ReadFile(pluginSourceRoot + "/manifest.json")
	if err != nil {
		t.Fatalf("read embedded manifest: %v", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse embedded manifest: %v", err)
	}
	if m.Name != "niwa" {
		t.Errorf("embedded manifest name = %q, want %q", m.Name, "niwa")
	}
}

func TestEmbedded_ReturnsCanonicalInstallPath(t *testing.T) {
	p, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded: %v", err)
	}
	if p.Name != "niwa" {
		t.Errorf("Name = %q, want %q", p.Name, "niwa")
	}
	if p.Version == "" {
		t.Error("Version is empty")
	}
	if !strings.HasSuffix(p.Path, "/.claude/plugins/marketplaces/niwa") {
		t.Errorf("Path = %q, want suffix /.claude/plugins/marketplaces/niwa", p.Path)
	}
}
