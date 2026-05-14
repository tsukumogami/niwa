// Package plugin owns the embedded niwa Claude Code plugin and the
// installer that materializes it under the user's
// ~/.claude/plugins/marketplaces/niwa/ directory.
//
// The plugin source tree lives at //plugins/niwa/ in the niwa
// repository (manifest.json + skills/*). The embed.FS in this file
// captures that tree at build time, so the niwa binary ships every
// file the installer needs without consulting the network.
package plugin

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// pluginFS captures the embedded plugin source tree at build time.
// The tree lives at internal/plugin/files/niwa/ — the //go:embed
// directive resolves paths relative to the directory containing
// this file, so the tree must be co-located with the package.
//
//go:embed files/niwa
var pluginFS embed.FS

// InstalledPlugin captures the in-memory description of the embedded
// niwa plugin after Embedded() has resolved its manifest.
type InstalledPlugin struct {
	Name    string
	Version string
	Path    string
}

// manifest is the on-disk shape of plugins/niwa/manifest.json.
type manifest struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
}

// pluginSourceRoot is the path inside pluginFS where the embedded
// plugin tree is rooted.
const pluginSourceRoot = "files/niwa"

// Embedded returns the canonical description of the embedded plugin.
// It reads the embedded manifest, verifies the plugin name is "niwa"
// (a build-time invariant — anyone forking the niwa binary should
// also rename the plugin), and computes the install path from the
// current user's $HOME.
//
// Returns a programmer-error error only if the embedded manifest is
// missing or malformed — those are build-time invariants that should
// fail loudly. User-environment errors (no $HOME, permission denied)
// surface from Install (which calls Embedded) and are reported as
// (Failed, nil) so the apply pipeline can warn-and-continue.
func Embedded() (InstalledPlugin, error) {
	data, err := pluginFS.ReadFile(pluginSourceRoot + "/manifest.json")
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: read embedded manifest: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: parse embedded manifest: %w", err)
	}
	if m.Name != "niwa" {
		return InstalledPlugin{}, fmt.Errorf("plugin: embedded manifest name = %q, want %q (build-time invariant violated)", m.Name, "niwa")
	}
	if m.Version == "" {
		return InstalledPlugin{}, errors.New("plugin: embedded manifest has empty version")
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return InstalledPlugin{}, fmt.Errorf("plugin: resolve home directory: %w", err)
	}
	installPath := filepath.Join(homeDir, ".claude", "plugins", "marketplaces", "niwa")

	return InstalledPlugin{
		Name:    m.Name,
		Version: m.Version,
		Path:    installPath,
	}, nil
}
