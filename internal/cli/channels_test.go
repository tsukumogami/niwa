package cli

import (
	"bytes"
	"os"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

// newTestCmd returns a minimal cobra command with a buffer for stderr output,
// suitable for calling resolveChannelsActivation in tests.
func newTestCmd() (*cobra.Command, *bytes.Buffer) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetErr(&buf)
	return cmd, &buf
}

func TestResolveChannelsActivation_NoChannelsWinsOverAll(t *testing.T) {
	t.Setenv("NIWA_CHANNELS", "1")

	cfg := &config.WorkspaceConfig{}
	cfg.Channels.Mesh = &config.ChannelsMeshConfig{}

	cmd, _ := newTestCmd()
	// --no-channels should disable even when config has a section and env is "1".
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, true)
	if got.Channels.Mesh != nil {
		t.Error("--no-channels should set Channels.Mesh to nil")
	}
	if fromFlag {
		t.Error("fromFlag should be false when --no-channels disables channels")
	}
}

func TestResolveChannelsActivation_ChannelsFlagSynthesizes(t *testing.T) {
	cfg := &config.WorkspaceConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, true, false)
	if got.Channels.Mesh == nil {
		t.Error("--channels should synthesize Channels.Mesh when not in config")
	}
	if !fromFlag {
		t.Error("fromFlag should be true when --channels synthesizes Mesh")
	}
}

func TestResolveChannelsActivation_ChannelsFlagWithExistingConfig(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	cfg.Channels.Mesh = &config.ChannelsMeshConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, true, false)
	if got.Channels.Mesh == nil {
		t.Error("Channels.Mesh should remain non-nil when config has section")
	}
	if fromFlag {
		t.Error("fromFlag should be false when config already has channels.mesh")
	}
}

func TestResolveChannelsActivation_ConfigSectionAlreadyPresent(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	cfg.Channels.Mesh = &config.ChannelsMeshConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, false)
	if got.Channels.Mesh == nil {
		t.Error("Channels.Mesh should remain non-nil when config has section")
	}
	if fromFlag {
		t.Error("fromFlag should be false when config section is the source")
	}
}

func TestResolveChannelsActivation_EnvVar1Enables(t *testing.T) {
	t.Setenv("NIWA_CHANNELS", "1")

	cfg := &config.WorkspaceConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, false)
	if got.Channels.Mesh == nil {
		t.Error("NIWA_CHANNELS=1 should synthesize Channels.Mesh")
	}
	if !fromFlag {
		t.Error("fromFlag should be true when NIWA_CHANNELS=1 synthesizes Mesh")
	}
}

func TestResolveChannelsActivation_EnvVar0Disables(t *testing.T) {
	t.Setenv("NIWA_CHANNELS", "0")

	cfg := &config.WorkspaceConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, false)
	if got.Channels.Mesh != nil {
		t.Error("NIWA_CHANNELS=0 should leave Channels.Mesh nil")
	}
	if fromFlag {
		t.Error("fromFlag should be false when NIWA_CHANNELS=0")
	}
}

func TestResolveChannelsActivation_EnvVarInvalidWarns(t *testing.T) {
	t.Setenv("NIWA_CHANNELS", "yes")

	cfg := &config.WorkspaceConfig{}

	cmd, errBuf := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, false)
	if got.Channels.Mesh != nil {
		t.Error("invalid NIWA_CHANNELS should leave Channels.Mesh nil")
	}
	if fromFlag {
		t.Error("fromFlag should be false for invalid NIWA_CHANNELS")
	}
	if errBuf.Len() == 0 {
		t.Error("expected warning in stderr for invalid NIWA_CHANNELS value")
	}
}

func TestResolveChannelsActivation_EnvVarNotSet(t *testing.T) {
	os.Unsetenv("NIWA_CHANNELS")

	cfg := &config.WorkspaceConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, false)
	if got.Channels.Mesh != nil {
		t.Error("unset NIWA_CHANNELS should leave Channels.Mesh nil")
	}
	if fromFlag {
		t.Error("fromFlag should be false when NIWA_CHANNELS is unset")
	}
}

func TestResolveChannelsActivation_NoChannelsClearsExistingMesh(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	cfg.Channels.Mesh = &config.ChannelsMeshConfig{}

	cmd, _ := newTestCmd()
	got, fromFlag := resolveChannelsActivation(cmd, cfg, false, true)
	if got.Channels.Mesh != nil {
		t.Error("--no-channels should clear existing Channels.Mesh")
	}
	if fromFlag {
		t.Error("fromFlag should be false for --no-channels")
	}
}

func TestResolveChannelsActivation_NoChannelsTakesPriorityOverChannelsFlag(t *testing.T) {
	cfg := &config.WorkspaceConfig{}

	cmd, _ := newTestCmd()
	// Both --channels and --no-channels: --no-channels wins.
	got, fromFlag := resolveChannelsActivation(cmd, cfg, true, true)
	if got.Channels.Mesh != nil {
		t.Error("--no-channels should win over --channels")
	}
	if fromFlag {
		t.Error("fromFlag should be false when --no-channels wins")
	}
}
