package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// authorityMachineConfig returns the machine layer for apply's authority
// warnings: the caller's MachineConfig, or the machine config loaded now. A
// config that fails to load gives no machine layer; its loader reports it.
func (a *Applier) authorityMachineConfig() *config.GlobalConfig {
	if a.MachineConfig != nil {
		return a.MachineConfig
	}
	g, err := config.LoadGlobalConfig()
	if err != nil {
		return nil
	}
	return g
}

// ResolveAuthorityOverlay finds the overlay whose [authority] table narrows
// the policy, mirroring the three branches provisioning uses to pick an
// overlay (runPipeline's step 0.5), without cloning or syncing anything:
//
//   - NoOverlay in stateDir's .niwa/instance.json: no overlay.
//   - An overlay_url the state records: that overlay. Its clone must exist
//     and parse, or this returns an error naming `niwa apply`. Treating a
//     recorded overlay that's missing as "no narrowing" would silently widen
//     policy on exactly the machine that lacks it.
//   - Otherwise the convention overlay derived from configSourceURL (the
//     registered config source, looked up by the effective workspace name):
//     used when its clone exists locally, treated as absent when it doesn't.
//
// A missing state file is the third branch. It returns nil with no error when
// there is no overlay.
func ResolveAuthorityOverlay(stateDir, configSourceURL string) (*config.WorkspaceOverlay, error) {
	state, err := LoadState(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if state != nil && state.NoOverlay {
		return nil, nil
	}
	if state != nil && state.OverlayURL != "" {
		dir, err := config.OverlayDir(state.OverlayURL)
		if err != nil {
			return nil, fmt.Errorf("locating the workspace overlay %s: %w; run niwa apply to restore it", state.OverlayURL, err)
		}
		if _, err := os.Stat(dir); err != nil {
			return nil, fmt.Errorf("the workspace overlay %s recorded for this workspace has no local clone at %s; run niwa apply to restore it", state.OverlayURL, dir)
		}
		o, err := config.ParseOverlay(filepath.Join(dir, "workspace-overlay.toml"))
		if err != nil {
			return nil, fmt.Errorf("reading the workspace overlay %s: %w; run niwa apply to restore it", state.OverlayURL, err)
		}
		return o, nil
	}
	if configSourceURL == "" {
		return nil, nil
	}
	conventionURL, ok := config.DeriveOverlayURL(configSourceURL)
	if !ok {
		return nil, nil
	}
	dir, err := config.OverlayDir(conventionURL)
	if err != nil {
		return nil, nil
	}
	if _, err := os.Stat(dir); err != nil {
		return nil, nil
	}
	o, err := config.ParseOverlay(filepath.Join(dir, "workspace-overlay.toml"))
	if err != nil {
		return nil, fmt.Errorf("reading the workspace overlay %s: %w; run niwa apply to restore it", conventionURL, err)
	}
	return o, nil
}
