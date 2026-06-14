package workspace

import (
	"context"
	"io"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/vault"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

// EffectiveConfigOptions tunes the resolve+merge helper for a single call site.
//
// AllowMissingSecrets threads through to resolve.ResolveOptions.AllowMissing on
// both the team and personal-overlay walks; when true, missing vault keys
// downgrade to empty MaybeSecret values with a stderr warning instead of an
// error. The instance apply path threads its Applier.AllowMissingSecrets here;
// the worktree apply path sets this true so a transient vault outage degrades
// a worktree re-materialization to a warning rather than a hard failure (the
// instance create gate already enforced strictness at bootstrap time).
//
// GlobalConfigDir is forwarded to MergeGlobalOverride so personal-overlay hook
// scripts can be resolved to absolute paths. Empty when no global config is
// registered (no override is being merged anyway, in that case).
//
// Stderr receives the resolver's AllowMissing warnings. Nil falls back to
// os.Stderr inside the resolver.
type EffectiveConfigOptions struct {
	AllowMissingSecrets bool
	GlobalConfigDir     string
	Stderr              io.Writer
}

// ResolveAndMergeEffectiveConfig runs the vault resolve + personal-overlay
// merge pipeline shared by the instance apply path
// (internal/workspace/apply.go) and the worktree apply path
// (internal/cli/session_lifecycle_cmd.go). It exists to keep those two paths
// from drifting: every change to "what does an effective WorkspaceConfig look
// like after personal overlay resolution" must land here, in one place, so
// both call sites pick it up.
//
// The helper takes caller-built bundles. The instance apply call site needs
// the bundle handles for diagnostic plumbing (R12 collision enforcement,
// shadow detection, R13.1 unreachable warnings, public-remote secrets
// guardrail) BEFORE resolution runs, so building bundles outside this helper
// keeps the diagnostic emits in their established positions and lets the
// worktree path call this helper with its own minimal bundle pair. The caller
// is responsible for closing the bundles (defer CloseAll at the call site).
//
// globalOverride may be nil (no personal overlay registered); the helper then
// resolves only the team workspace and returns the resolved cfg with no merge.
// In that case the returned EnvExamplePolicy is also nil — the resolver treats
// nil as "no global rung".
//
// The returned *config.WorkspaceConfig is the effective config that should
// drive every downstream materializer (env, settings, files, hooks). The
// returned *config.EnvExamplePolicy is the flattened personal/global
// .env.example failure policy for the active workspace, suitable for threading
// into WorktreeApplyOptions.GlobalEnvExamplePolicy or the
// repoMaterializeInputs.GlobalEnvExamplePolicy used by the instance pipeline.
// The returned config.OutputTargets is the flattened personal/global
// secret-output target declaration, suitable for threading into
// WorktreeApplyOptions.GlobalEnvOutput or the
// repoMaterializeInputs.GlobalEnvOutput used by the instance pipeline.
func ResolveAndMergeEffectiveConfig(
	ctx context.Context,
	cfg *config.WorkspaceConfig,
	globalOverride *config.GlobalConfigOverride,
	teamBundle *vault.Bundle,
	personalBundle *vault.Bundle,
	opts EffectiveConfigOptions,
) (*config.WorkspaceConfig, *config.EnvExamplePolicy, config.OutputTargets, error) {
	// Resolve the team workspace config first.
	resolvedCfg, err := resolve.ResolveWorkspace(ctx, cfg, resolve.ResolveOptions{
		AllowMissing: opts.AllowMissingSecrets,
		TeamBundle:   teamBundle,
		Stderr:       opts.Stderr,
	})
	if err != nil {
		return nil, nil, nil, err
	}

	// No overlay registered: return the team-only resolved cfg unchanged.
	if globalOverride == nil {
		return resolvedCfg, nil, nil, nil
	}

	// Resolve the personal overlay, then merge it into the team workspace.
	// The merge happens AFTER resolution so that R8 team_only enforcement
	// in MergeGlobalOverride sees the overlay's resolved MaybeSecret values,
	// not pre-resolve URIs.
	resolvedOverride, err := resolve.ResolveGlobalOverride(ctx, globalOverride, resolve.ResolveOptions{
		AllowMissing:   opts.AllowMissingSecrets,
		PersonalBundle: personalBundle,
		Stderr:         opts.Stderr,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	flattened := ResolveGlobalOverride(resolvedOverride, cfg.Workspace.Name)
	merged, err := MergeGlobalOverride(resolvedCfg, flattened, opts.GlobalConfigDir)
	if err != nil {
		return nil, nil, nil, err
	}
	return merged, flattened.EnvExamplePolicy, flattened.EnvOutput, nil
}
