package cli

import (
	"github.com/spf13/cobra"
	"github.com/tsukumogami/niwa/internal/config"
)

// strictSecretsFlagName is the flag every provisioning command registers
// through registerStrictSecretsFlag. It is a constant because the precedence
// rule reads the flag back by name (Flags().Changed), so a typo in either
// place would silently disable the de-escalating form.
const strictSecretsFlagName = "strict-secrets"

// The flag targets live here rather than beside each command's other flags so
// that adding strict mode to a command is one registration line in its init().
// Three separate variables rather than one shared target: cobra binds a flag to
// an address per command, and a shared address would let a value set on one
// command be read by another within the same process (the tests run every
// command in one).
var (
	strictSecretsApply  bool
	strictSecretsCreate bool
	strictSecretsInit   bool
)

// registerStrictSecretsFlag registers --strict-secrets on cmd, bound to p.
//
// One registrar for all three commands is the point: the flag's name, its
// default, and its help text are the user-facing contract for a
// security-relevant setting, and three hand-written registrations would be
// three places for that contract to drift.
func registerStrictSecretsFlag(cmd *cobra.Command, p *bool) {
	cmd.Flags().BoolVar(p, strictSecretsFlagName, false,
		"fail when a declared env key could not be supplied, instead of materializing without it. "+
			"Overrides the workspace's strict_secrets setting for this invocation; "+
			"--strict-secrets=false turns it off for a workspace that sets it.")
}

// strictSecretsFor resolves this invocation's strictness from the flag and the
// workspace setting.
//
// cmd may be nil, which is how the unattended paths ask: `niwa dispatch`, the
// SessionStart hook, reset and the reaper have no flag to consult, so they get
// the setting alone. That is what makes strict mode a property of the
// workspace rather than of the invocation -- a path that could not see the
// setting would quietly provision an instance the workspace said not to.
func strictSecretsFor(cmd *cobra.Command, flagValue bool, cfg *config.WorkspaceConfig) bool {
	changed := cmd != nil && cmd.Flags().Changed(strictSecretsFlagName)
	var setting *bool
	if cfg != nil {
		setting = cfg.Workspace.StrictSecrets
	}
	return config.ResolveStrictSecrets(setting, changed, flagValue)
}
