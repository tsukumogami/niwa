package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Action describes what Install did.
type Action int

const (
	// Installed means a fresh install path was materialized this call.
	Installed Action = iota
	// UpToDate means the on-disk plugin already matched the embedded
	// version; no filesystem changes were made.
	UpToDate
	// Skipped means the install was explicitly opted out — no
	// filesystem reads or writes happened under the install path.
	Skipped
	// Failed means a user-environment error prevented install (read-only
	// $HOME, permission denied, mid-rename failure rolled back). The
	// caller should warn-and-continue.
	Failed
)

// InstallOpts controls Install's behavior.
type InstallOpts struct {
	// SkipInstall short-circuits Install: no filesystem reads happen
	// under the install path, no atomic stage-and-rename runs. Set by
	// the CLI when the user passed --no-install-plugins or set
	// auto_install_plugins = false in their global config.
	SkipInstall bool
}

// ManualInstallCommand is the copy-paste command the skip-notice
// surfaces so users who opted out (or hit a filesystem error) can
// install the plugin manually. The string must match a real,
// shipping CLI subcommand — see internal/cli/plugins.go.
//
// It is exported because the notice that carries it is emitted by
// the caller now: this package returns what it did and the caller
// decides what the user hears.
const ManualInstallCommand = "niwa plugins install"

// Install ensures the embedded niwa plugin is materialized under the
// given developer home, at <home>/.claude/plugins/marketplaces/niwa/.
// The function is idempotent: when the on-disk plugin already matches
// the embedded version it returns (UpToDate, nil) without mutating
// the filesystem.
//
// Nothing here reports to the user. The Action is the report, and the
// caller turns it into a notice (workspace.EmitPluginInstallNotice
// does that mapping) — which is what keeps this package a leaf that
// the workspace registry can call rather than one that has to call
// back into it.
//
// Action values:
//
//   - Installed: the plugin was just written (fresh install or
//     replacement).
//   - UpToDate: the on-disk plugin matched the embedded version.
//     Callers still report it, so users see the installation status
//     once per workspace.
//   - Skipped: opts.SkipInstall was true. No filesystem reads or
//     writes happened under the install path.
//   - Failed: a user-environment error prevented install. Returns
//     (Failed, nil) so the apply pipeline can warn-and-continue,
//     surfacing ManualInstallCommand. The error return is non-nil
//     ONLY on programmer error: a malformed embedded manifest, or an
//     empty home from a caller that was never wired to one.
func Install(home string, opts InstallOpts) (Action, error) {
	embedded, err := Embedded()
	if err != nil {
		// Embedded() returns errors only on programmer/build-time
		// errors. Surface as a real error.
		return Failed, err
	}

	// The opt-out is checked before the home is resolved: a skipped
	// install touches nothing, and an unwired caller that skips has
	// no business failing over a home it was never going to use.
	if opts.SkipInstall {
		return Skipped, nil
	}

	installPath, err := InstallPath(home)
	if err != nil {
		return Failed, err
	}

	// Idempotence check: read the on-disk manifest if present and
	// compare to the embedded version.
	if onDisk, statErr := readInstalledManifest(installPath); statErr == nil {
		if onDisk.Version == embedded.Version {
			return UpToDate, nil
		}
	}

	// Fresh install or version mismatch: atomic stage-and-rename.
	if err := stageAndRename(installPath); err != nil {
		return Failed, nil
	}

	return Installed, nil
}

// readInstalledManifest returns the parsed manifest at the given
// install path. Returns an error when the file is missing or
// malformed; callers treat any error as "needs install."
func readInstalledManifest(installPath string) (*manifest, error) {
	data, err := os.ReadFile(filepath.Join(installPath, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// stageAndRename writes the embedded tree to <installPath>.next/,
// then atomically swaps it into place:
//
//  1. MaterializeTo(.next) — fs.WalkDir + os.WriteFile / os.MkdirAll
//  2. if <installPath> exists, Rename(installPath, installPath.prev)
//  3. Rename(installPath.next, installPath)
//  4. RemoveAll(installPath.prev) — best effort cleanup
//
// On any mid-swap failure the function rolls back: removes .next/ if
// the prep failed; restores .prev/ if step 3 failed after step 2.
func stageAndRename(installPath string) error {
	nextPath := installPath + ".next"
	prevPath := installPath + ".prev"

	// Idempotent cleanup of stale staging directories from a prior
	// crashed install.
	_ = os.RemoveAll(nextPath)
	_ = os.RemoveAll(prevPath)

	if err := os.MkdirAll(filepath.Dir(installPath), 0o755); err != nil {
		return fmt.Errorf("plugin: ensure parent dir: %w", err)
	}

	if err := MaterializeTo(nextPath); err != nil {
		_ = os.RemoveAll(nextPath)
		return fmt.Errorf("plugin: stage embedded tree: %w", err)
	}

	movedAside := false
	if _, statErr := os.Stat(installPath); statErr == nil {
		if err := os.Rename(installPath, prevPath); err != nil {
			_ = os.RemoveAll(nextPath)
			return fmt.Errorf("plugin: move-aside existing install: %w", err)
		}
		movedAside = true
	}

	if err := os.Rename(nextPath, installPath); err != nil {
		// Promotion failed: roll back the move-aside if we did one.
		if movedAside {
			_ = os.Rename(prevPath, installPath)
		}
		_ = os.RemoveAll(nextPath)
		return fmt.Errorf("plugin: promote staging dir: %w", err)
	}

	// Best-effort cleanup of the previous install.
	if movedAside {
		_ = os.RemoveAll(prevPath)
	}

	return nil
}

// MaterializeTo copies the pluginFS contents rooted at
// pluginSourceRoot into dst. Uses fs.WalkDir + os.WriteFile /
// os.MkdirAll — no archive parser dependency (verified by
// TestPlugin_NoArchiveDeps).
//
// It is exported because the install path under the developer's home
// is not the only place the embedded tree belongs: a delivery that
// puts the tree somewhere inside an instance needs the same write
// without the manifest comparison and the atomic swap around it.
// Writing into a directory that already holds files merges rather
// than replaces, so callers wanting replacement stage into a fresh
// destination the way stageAndRename does.
func MaterializeTo(dst string) error {
	return fs.WalkDir(pluginFS, pluginSourceRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(pluginSourceRoot, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := pluginFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
