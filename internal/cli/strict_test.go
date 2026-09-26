package cli

import (
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

// TestStrictSecretsFlagRegisteredOnEveryProvisioningCommand checks the three
// commands that accept flags and provision, and checks them against each other
// rather than against a literal: the point of the shared registrar is that the
// three cannot drift, so the assertion is that they are identical.
func TestStrictSecretsFlagRegisteredOnEveryProvisioningCommand(t *testing.T) {
	cmds := map[string]*cobra.Command{
		"apply":  applyCmd,
		"create": createCmd,
		"init":   initCmd,
	}

	var usage string
	for name, cmd := range cmds {
		f := cmd.Flags().Lookup(strictSecretsFlagName)
		if f == nil {
			t.Fatalf("%s: --%s is not registered", name, strictSecretsFlagName)
		}
		if f.DefValue != "false" {
			t.Errorf("%s: --%s defaults to %q, want false -- tolerant is the default this work establishes",
				name, strictSecretsFlagName, f.DefValue)
		}
		if usage == "" {
			usage = f.Usage
			continue
		}
		if f.Usage != usage {
			t.Errorf("%s: --%s help text differs from another command's; both come from registerStrictSecretsFlag, so this means one was registered by hand",
				name, strictSecretsFlagName)
		}
	}
	if !strings.Contains(usage, "strict_secrets") {
		t.Errorf("help text should name the workspace setting it overrides, got: %q", usage)
	}
}

// TestStrictSecretsForReadsFlagPresenceNotValue drives the precedence rule
// through cobra, which is where the "was it changed" question is actually
// answered. The de-escalating row is the one worth the round trip: it is the
// only one a value-based implementation would get wrong, and it passes through
// a real flag parse here rather than a hand-set boolean.
func TestStrictSecretsForReadsFlagPresenceNotValue(t *testing.T) {
	strictCfg := &config.WorkspaceConfig{}
	on := true
	strictCfg.Workspace.StrictSecrets = &on

	tests := []struct {
		name string
		args []string
		cfg  *config.WorkspaceConfig
		want bool
	}{
		{"no flag, no setting", nil, &config.WorkspaceConfig{}, false},
		{"no flag, setting on", nil, strictCfg, true},
		{"flag on, no setting", []string{"--strict-secrets"}, &config.WorkspaceConfig{}, true},
		{"explicit false de-escalates the setting", []string{"--strict-secrets=false"}, strictCfg, false},
		{"explicit true agrees with the setting", []string{"--strict-secrets=true"}, strictCfg, true},
		{"nil config is tolerant", nil, nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var target bool
			cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
			registerStrictSecretsFlag(cmd, &target)
			cmd.SetArgs(tt.args)
			cmd.SetOut(&strings.Builder{})
			cmd.SetErr(&strings.Builder{})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("parsing %v: %v", tt.args, err)
			}
			if got := strictSecretsFor(cmd, target, tt.cfg); got != tt.want {
				t.Errorf("strictSecretsFor(%v) = %v, want %v", tt.args, got, tt.want)
			}
		})
	}
}

// TestStrictSecretsForWithNoCommandReadsTheSetting is the unattended shape:
// dispatch, the SessionStart hook, reset and the reaper have no command line,
// pass nil, and get the workspace's answer.
func TestStrictSecretsForWithNoCommandReadsTheSetting(t *testing.T) {
	cfg := &config.WorkspaceConfig{}
	on := true
	cfg.Workspace.StrictSecrets = &on
	if !strictSecretsFor(nil, false, cfg) {
		t.Error("a flagless caller must still honour the workspace setting")
	}
	off := false
	cfg.Workspace.StrictSecrets = &off
	if strictSecretsFor(nil, false, cfg) {
		t.Error("a flagless caller must honour an explicit false too")
	}
}

// TestEveryProvisioningSurfaceResolvesStrictness records that the unattended
// surfaces honour strict mode deliberately, not incidentally.
//
// It is a source-level assertion because the wiring is one assignment per
// surface, and the alternative -- standing up five end-to-end provisioning runs
// against a fake vault -- would test the pipeline over again to observe a field
// being set. What can actually go wrong here is a new provisioning surface
// (or a rewritten one) that never sets the field and silently provisions
// tolerantly in a strict workspace; that is what this catches.
//
// `niwa watch --once` and the reaper are covered through instance_from_hook.go:
// both provision via provisionInstanceFunc, whose production implementation is
// realProvisionInstance there. The final check pins that routing, since a watch
// or reap that grew its own applier would slip past the list.
func TestEveryProvisioningSurfaceResolvesStrictness(t *testing.T) {
	surfaces := map[string]string{
		"create.go":             "niwa create",
		"apply.go":              "niwa apply",
		"init.go":               "niwa init --bootstrap",
		"reset.go":              "niwa reset",
		"instance_from_hook.go": "the SessionStart hook, niwa dispatch, niwa watch and the reaper",
	}

	for file, surface := range surfaces {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		if !strings.Contains(string(data), "StrictSecrets = strictSecretsFor(") {
			t.Errorf("%s (%s) provisions without resolving strictness; every provisioning surface must consult the workspace setting", file, surface)
		}
	}

	for _, file := range []string{"watch.go", "reap.go"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("reading %s: %v", file, err)
		}
		body := string(data)
		if strings.Contains(body, "workspace.NewApplier(") {
			t.Errorf("%s builds its own applier; it must provision through provisionInstanceFunc, which is what makes it honour strict mode", file)
		}
	}
}
