package cli

import "github.com/spf13/cobra"

// allowMissingSecretsFlag is the retired tolerance flag. It used to ask
// create and apply to keep going when a vault:// reference could not be
// resolved. Nothing has to ask for that any more: an unresolved reference
// is recorded and its key omitted on every path, so the behaviour the flag
// named is what happens anyway.
//
// It stays registered because it appears in existing scripts and CI
// invocations. Accepting it as a documented no-op costs a line; removing it
// would break those callers for no gain.
const allowMissingSecretsFlag = "allow-missing-secrets"

// allowMissingSecretsUsage is the help-output description. It is short
// because pflag appends the deprecation notice below to the same line.
const allowMissingSecretsUsage = "deprecated no-op, still accepted so existing invocations keep working"

// allowMissingSecretsDeprecation is what pflag prints on stderr when the
// flag is used ("Flag --allow-missing-secrets has been deprecated, ...")
// and appends to the help line as "(DEPRECATED: ...)". Both readings have
// to work, so it is phrased as a sentence that follows a comma.
const allowMissingSecretsDeprecation = "it changes nothing: an unresolved vault:// reference never stops create or apply, with or without the flag. Drop it from your scripts."

// registerAllowMissingSecretsFlag registers the deprecated flag on cmd.
//
// The flag deliberately binds to no variable. There is nothing left to read
// it, and giving it a package-level home would invite someone to wire it
// back into the pipeline.
//
// Call this LAST in a command's init, after every other flag on that
// command is registered: the mutual-exclusion group below can only be
// declared once the flag it contradicts exists.
func registerAllowMissingSecretsFlag(cmd *cobra.Command) {
	cmd.Flags().Bool(allowMissingSecretsFlag, false, allowMissingSecretsUsage)

	// MarkDeprecated also sets Hidden, which would keep the notice out of
	// --help -- exactly where someone still passing the flag goes looking.
	// Un-hide it so the help line carries "(DEPRECATED: ...)"; pflag still
	// prints the stderr notice when the flag is actually used.
	_ = cmd.Flags().MarkDeprecated(allowMissingSecretsFlag, allowMissingSecretsDeprecation)
	if f := cmd.Flags().Lookup(allowMissingSecretsFlag); f != nil {
		f.Hidden = false
	}

	// Passing this flag together with --strict-secrets states two opposite
	// intents, so cobra rejects the invocation rather than picking a winner
	// silently. The lookup guard is why this call has to come last:
	// MarkFlagsMutuallyExclusive panics on a flag name the command does not
	// have, and not every command that takes the deprecated flag has to take
	// the strict one.
	if cmd.Flags().Lookup(strictSecretsFlagName) != nil {
		cmd.MarkFlagsMutuallyExclusive(allowMissingSecretsFlag, strictSecretsFlagName)
	}
}
