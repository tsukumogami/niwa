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

// TestEmbedded_CarriesThePluginManifest pins the one thing a bare
// //go:embed directory pattern silently takes away.
//
// The tree's `.claude-plugin/plugin.json` is what makes a delivered copy
// resolve its skills as `niwa:<skill>` rather than as bare loose names.
// Go's embed drops every path element beginning with a dot unless the
// pattern carries the `all:` prefix, so losing that prefix leaves the file
// on disk, out of the binary, and unmentioned by any error: the manifest
// the installer reads is `manifest.json`, which embeds either way, so
// nothing downstream notices until a session resolves the wrong name.
//
// Reading through pluginFS rather than off disk is the whole point — the
// on-disk tree is not the artifact that ships.
func TestEmbedded_CarriesThePluginManifest(t *testing.T) {
	data, err := pluginFS.ReadFile(pluginSourceRoot + "/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("embedded tree is missing .claude-plugin/plugin.json: %v (does the //go:embed directive still carry its all: prefix?)", err)
	}
	var m struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parsing the embedded plugin manifest: %v", err)
	}
	if m.Name != "niwa" {
		t.Errorf("embedded plugin manifest name = %q, want %q", m.Name, "niwa")
	}
}
