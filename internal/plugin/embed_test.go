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

func TestEmbedded_DescribesTheEmbeddedManifest(t *testing.T) {
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
}

// A home the caller never supplied is a wiring error rather than a
// prompt to guess one, so InstallPath refuses instead of returning a
// path relative to nothing.
func TestInstallPath_EmptyHomeIsAnError(t *testing.T) {
	for _, home := range []string{"", "   "} {
		if _, err := InstallPath(home); err == nil {
			t.Errorf("InstallPath(%q) returned nil error", home)
		}
	}
}

func TestInstallPath_LandsUnderTheGivenHome(t *testing.T) {
	got, err := InstallPath("/home/somebody")
	if err != nil {
		t.Fatalf("InstallPath: %v", err)
	}
	if !strings.HasSuffix(got, "/.claude/plugins/marketplaces/niwa") {
		t.Errorf("InstallPath = %q, want suffix /.claude/plugins/marketplaces/niwa", got)
	}
	if !strings.HasPrefix(got, "/home/somebody/") {
		t.Errorf("InstallPath = %q, want it under the given home", got)
	}
}
