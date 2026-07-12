package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/onboard"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// operatorBearerEnvOverride is a deliberately narrow escape hatch --
// see resolveOperatorBearer's doc comment for why it exists instead of
// a real extraction mechanism.
const operatorBearerEnvOverride = "NIWA_INFISICAL_OPERATOR_TOKEN"

// onboardConfigBundle collects every real-config-sourced value
// resolveAndRunOnboard needs to populate onboard.Options (R14: no
// baked-in org/workspace/project identifier -- every such value below
// comes from the team workspace config's [vault.provider] block or
// the personal-overlay [global.vault.provider] block, never a
// literal).
type onboardConfigBundle struct {
	teamVault      *config.VaultRegistry
	globalOverride *config.GlobalConfigOverride
	overlayDir     string
	registeredRepo string

	kind            string
	projectID       string
	identityID      string
	identityName    string
	authMethod      string
	environmentSlug string
	secretPath      string
	apiURLConfigVal string

	syncSpec vault.ProviderSpec
}

// loadOnboardConfig resolves the current instance's team workspace
// config and the operator's personal-overlay config, then extracts the
// [vault.provider] fields the team and individual runners need.
//
// Absent config produces a clear, actionable error naming exactly what
// to declare -- never a panic or a silently zero-valued Options field
// (the wiring's own bar, since a caller-bug nil-check inside
// onboard.Run would otherwise be the first thing a misconfigured
// workspace hits, which is a worse diagnostic than this one).
func loadOnboardConfig() (onboardConfigBundle, error) {
	var bundle onboardConfigBundle

	cwd, err := os.Getwd()
	if err != nil {
		return bundle, fmt.Errorf("onboard: resolving the current directory: %w", err)
	}

	configPath, _, err := config.Discover(cwd)
	if err != nil {
		return bundle, fmt.Errorf("onboard: niwa onboard must be run from inside a niwa workspace or instance: %w", err)
	}
	result, err := config.Load(configPath)
	if err != nil {
		return bundle, fmt.Errorf("onboard: loading workspace config: %w", err)
	}
	cfg := result.Config
	bundle.teamVault = cfg.Vault

	if cfg.Vault == nil || cfg.Vault.IsEmpty() {
		return bundle, fmt.Errorf(
			"onboard: the workspace config at %s declares no [vault.provider] block yet; "+
				"author one (kind, project, identity_id, identity_name, environment) "+
				"before running `niwa onboard`",
			configPath,
		)
	}
	if cfg.Vault.Provider == nil {
		return bundle, fmt.Errorf(
			"onboard: the workspace config declares [vault.providers.<name>] entries, " +
				"but niwa onboard only supports the anonymous [vault.provider] shape today",
		)
	}
	provider := cfg.Vault.Provider
	bundle.kind = provider.Kind

	getStr := func(key string) string {
		v, _ := provider.Config[key].(string)
		return v
	}
	bundle.projectID = getStr("project")
	bundle.identityID = getStr("identity_id")
	bundle.identityName = getStr("identity_name")
	bundle.authMethod = getStr("auth_method")
	bundle.environmentSlug = getStr("environment")
	bundle.secretPath = getStr("secret_path")
	bundle.apiURLConfigVal = getStr("api_url")

	if bundle.authMethod == "" {
		// "Universal Auth" is Infisical's own generic auth-method
		// vocabulary, not an org/workspace/project-specific identifier
		// (R14/AC-23), so defaulting it (unlike identity_name or
		// project below) doesn't reintroduce a baked-in constant.
		bundle.authMethod = "Universal Auth"
	}

	var missing []string
	if bundle.projectID == "" {
		missing = append(missing, "project")
	}
	if bundle.identityID == "" {
		missing = append(missing, "identity_id")
	}
	if bundle.identityName == "" {
		missing = append(missing, "identity_name")
	}
	if bundle.environmentSlug == "" {
		missing = append(missing, "environment")
	}
	if len(missing) > 0 {
		return bundle, fmt.Errorf(
			"onboard: the workspace config's [vault.provider] block is missing %s; "+
				"declare %s under [vault.provider] before running `niwa onboard`",
			strings.Join(missing, ", "), strings.Join(missing, ", "),
		)
	}

	overlayDir, err := config.GlobalConfigDir()
	if err != nil {
		return bundle, fmt.Errorf("onboard: resolving the personal-overlay config directory: %w", err)
	}
	bundle.overlayDir = overlayDir

	if globalCfg, gerr := config.LoadGlobalConfig(); gerr == nil {
		bundle.registeredRepo = globalCfg.GlobalConfig.Repo
	}

	overridePath := filepath.Join(overlayDir, workspace.GlobalConfigOverrideFile)
	data, err := os.ReadFile(overridePath)
	switch {
	case err == nil:
		parsed, perr := config.ParseGlobalConfigOverride(data)
		if perr != nil {
			return bundle, fmt.Errorf("onboard: parsing personal-overlay config at %s: %w", overridePath, perr)
		}
		bundle.globalOverride = parsed
	case os.IsNotExist(err):
		// Not yet scaffolded -- the R22 precondition
		// (onboard.EnsurePersonalOverlay) handles that later in Run;
		// an empty override here just means "no credential-sync
		// provider declared yet," which downstream code already
		// treats as a normal (if incomplete) state, not a panic.
		bundle.globalOverride = &config.GlobalConfigOverride{}
	default:
		return bundle, fmt.Errorf("onboard: reading personal-overlay config at %s: %w", overridePath, err)
	}

	if spec := onboard.PickCredentialSyncSpec(bundle.globalOverride.Global); spec != nil {
		bundle.syncSpec = *spec
	}

	return bundle, nil
}

// personalCredResolves is Detect's R15 free local signal: whether the
// personal overlay already declares a credential-sync entry that
// resolves for (kind, project). Only called on the auto-detect path
// (no --team/--individual override), since it's the one place Detect
// actually consults it. Every failure mode (no credential-sync
// provider declared, provider unreachable, malformed body) reports
// simply "does not resolve yet" -- this is a UX signal for which
// setup to infer, not a verification gate; VerifyIndividual/R11 is the
// authoritative check that runs later in the pipeline.
func personalCredResolves(ctx context.Context, bundle onboardConfigBundle) bool {
	result, err := workspace.CheckProviderAuth(ctx, bundle.globalOverride, bundle.teamVault, nil, bundle.kind, bundle.projectID)
	if err != nil {
		return false
	}
	return result.Target.Err == nil
}

// resolveOperatorBearer obtains the operator's own live Infisical
// session bearer for the wizard's REST calls (Decision 4: every
// privileged call rides the operator's own session, never a niwa-
// custodied token).
//
// KNOWN GAP, flagged rather than papered over: production Infisical
// CLI documents no way to print a live session's raw access token --
// NOTE-onboard-rest-verification.md's Assumption C only verifies
// `infisical login status --json` for ORG CONTEXT, never a token --
// and no function in this repository extracts one from an
// authenticated `infisical login` session. This is therefore a
// deliberately narrow escape hatch: an env-var override (mirroring the
// established NIWA_INFISICAL_API_URL pattern), which the functional
// test doubles wire directly since neither double validates bearer
// content, plus a clear, actionable error otherwise -- never a silent
// hardcoded value. Resolving this for a real production run against
// real Infisical is a follow-up this issue does not close.
func resolveOperatorBearer() (secret.Value, error) {
	if v := os.Getenv(operatorBearerEnvOverride); v != "" {
		return secret.New([]byte(v), secret.Origin{ProviderName: "infisical", Key: "operator-bearer"}), nil
	}
	return secret.Value{}, fmt.Errorf(
		"onboard: no operator session bearer is available; set %s "+
			"(temporary escape hatch pending a supported way to read the active "+
			"`infisical login` session's token)",
		operatorBearerEnvOverride,
	)
}
