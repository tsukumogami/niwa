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
	"path/filepath"
	"strings"
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
//
// There is no install path in here. Where the plugin lands is a
// function of the developer's home directory, and this package never
// resolves that home itself — callers pass it in (see InstallPath).
type InstalledPlugin struct {
	Name    string
	Version string
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
// It reads the embedded manifest and verifies the plugin name is
// "niwa" (a build-time invariant — anyone forking the niwa binary
// should also rename the plugin).
//
// Returns an error only if the embedded manifest is missing or
// malformed — those are build-time invariants that should fail
// loudly, so every error out of here is a programmer error.
// User-environment errors (permission denied under the install path)
// surface from Install and are reported as (Failed, nil) so the apply
// pipeline can warn-and-continue.
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

	return InstalledPlugin{
		Name:    m.Name,
		Version: m.Version,
	}, nil
}

// InstallPath returns where the embedded plugin belongs under the
// given developer home: <home>/.claude/plugins/marketplaces/niwa.
//
// The home arrives as data rather than being resolved here, the same
// posture workspace.EnsureCodexTrust takes for the one other file
// niwa writes outside an instance: a caller that was never wired to a
// real home cannot reach the developer's own directory by accident,
// and a test can point the write anywhere without redirecting $HOME
// for the whole process. An empty home is a wiring error, not a user
// environment to work around, so it fails rather than defaulting.
func InstallPath(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("plugin: no home directory to resolve the install path under")
	}
	return filepath.Join(home, ".claude", "plugins", "marketplaces", "niwa"), nil
}
