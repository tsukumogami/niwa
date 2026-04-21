package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

// resolveChannelsActivation applies the channels activation priority rules and
// returns the (possibly modified) config along with a boolean indicating whether
// cfg.Channels.Mesh was synthesized (i.e. channels were activated via --channels
// or NIWA_CHANNELS rather than a permanent [channels.mesh] config section).
//
// Priority (highest to lowest):
//  1. --no-channels flag → channels disabled regardless of all else
//  2. --channels flag    → channels enabled
//  3. [channels.mesh] config section present → channels enabled (no synthesis)
//  4. NIWA_CHANNELS=1 env var → channels enabled default
//
// When channels are activated via flag or env var without an existing
// [channels.mesh] section, cfg.Channels.Mesh is synthesized as
// &config.ChannelsMeshConfig{} so downstream provisioning treats it as enabled.
// The returned bool is true only in that synthesized case (not when the config
// already had the section), so callers can emit a one-time "add config" hint.
func resolveChannelsActivation(cmd *cobra.Command, cfg *config.WorkspaceConfig, channelsFlag, noChannelsFlag bool) (*config.WorkspaceConfig, bool) {
	// Priority 1: --no-channels wins over everything.
	if noChannelsFlag {
		cfg.Channels.Mesh = nil
		return cfg, false
	}

	// Priority 2: --channels explicit flag.
	if channelsFlag {
		if cfg.Channels.Mesh == nil {
			cfg.Channels.Mesh = &config.ChannelsMeshConfig{}
			return cfg, true
		}
		// Config already has the section — channels are on, but not from flag alone.
		return cfg, false
	}

	// Priority 3: [channels.mesh] config section already present — no synthesis needed.
	if cfg.Channels.Mesh != nil {
		return cfg, false
	}

	// Priority 4: NIWA_CHANNELS env var.
	niwaChannels := os.Getenv("NIWA_CHANNELS")
	switch niwaChannels {
	case "":
		// Env var not set — channels remain disabled.
		return cfg, false
	case "0":
		// Explicitly disabled via env var.
		return cfg, false
	case "1":
		// Enabled via env var.
		cfg.Channels.Mesh = &config.ChannelsMeshConfig{}
		return cfg, true
	default:
		// Any other non-empty value: warn and ignore.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: NIWA_CHANNELS=%q is not a recognized value (use \"1\" to enable or \"0\" to disable); ignoring\n", niwaChannels)
		return cfg, false
	}
}
