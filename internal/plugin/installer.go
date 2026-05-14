package plugin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/workspace"
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

// manualInstallCommand is the copy-paste command the skip-notice
// surfaces so users who opted out (or hit a filesystem error) can
// install the plugin manually.
const manualInstallCommand = "niwa --install-plugins"

// Install ensures the embedded niwa plugin is materialized at the
// install path (~/.claude/plugins/marketplaces/niwa/). The function
// is idempotent: when the on-disk plugin already matches the
// embedded version it returns (UpToDate, nil) without mutating the
// filesystem.
//
// Action values:
//
//   - Installed: the plugin was just written (fresh install or
//     replacement). NoticeIDPluginInstalled is emitted via the
//     reporter when state is non-nil.
//   - UpToDate: the on-disk plugin matched the embedded version.
//     NoticeIDPluginInstalled is still emitted (so users see the
//     installation status once per workspace).
//   - Skipped: opts.SkipInstall was true. NoticeIDPluginSkipped is
//     emitted with the manual-install command.
//   - Failed: a user-environment error prevented install. Returns
//     (Failed, nil) with NoticeIDPluginSkipped emitted (surfacing
//     the same manual-install command) so the apply pipeline can
//     warn-and-continue. The error return is non-nil ONLY on
//     programmer error (malformed embedded manifest).
//
// state may be nil; in that case the install proceeds but no
// DisclosedNotices bookkeeping happens — the same nil-state contract
// EmitRank2Notice / EmitPluginNotice document.
//
// reporter may also be nil; in that case the notice is suppressed
// but the install action still runs.
func Install(state *workspace.InstanceState, reporter *workspace.Reporter, opts InstallOpts) (Action, error) {
	embedded, err := Embedded()
	if err != nil {
		// Embedded() returns errors only on programmer/build-time
		// errors. Surface as a real error.
		return Failed, err
	}

	if opts.SkipInstall {
		workspace.EmitPluginNotice(state, workspace.NoticeIDPluginSkipped, manualInstallCommand, reporter)
		return Skipped, nil
	}

	// Idempotence check: read the on-disk manifest if present and
	// compare to the embedded version.
	if onDisk, statErr := readInstalledManifest(embedded.Path); statErr == nil {
		if onDisk.Version == embedded.Version {
			workspace.EmitPluginNotice(state, workspace.NoticeIDPluginInstalled, manualInstallCommand, reporter)
			return UpToDate, nil
		}
	}

	// Fresh install or version mismatch: atomic stage-and-rename.
	if err := stageAndRename(embedded.Path); err != nil {
		// Fall back to skip-notice (which carries the manual-install
		// command) so the user sees a corrective hint.
		workspace.EmitPluginNotice(state, workspace.NoticeIDPluginSkipped, manualInstallCommand, reporter)
		return Failed, nil
	}

	workspace.EmitPluginNotice(state, workspace.NoticeIDPluginInstalled, manualInstallCommand, reporter)
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
//  1. fs.WalkDir + os.WriteFile / os.MkdirAll into .next/
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

	if err := writeEmbeddedTree(nextPath); err != nil {
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

// writeEmbeddedTree copies the pluginFS contents rooted at
// pluginSourceRoot into dst. Uses fs.WalkDir + os.WriteFile /
// os.MkdirAll — no archive parser dependency (verified by
// TestPlugin_NoArchiveDeps).
func writeEmbeddedTree(dst string) error {
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
