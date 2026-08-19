package workspace

import (
	"fmt"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

// This file is the workspace half of the agent-neutral session environment: it
// turns the configured [session.env] table into the literal values a producer
// can render, once per apply, for every agent to be generated from.
//
// Resolving it once rather than per agent is the whole point of the neutral
// declaration. Two resolutions could disagree -- a vault lookup that succeeded
// for one and not the other, a promote that saw a different pipeline -- and a
// workspace where the two agents' sessions carry different values for the same
// declared variable is the exact asymmetry this contract exists to remove.
//
// Nothing here knows which agent it is preparing for. Where the values land, in
// which document, under which key, and whether a given agent reads them from
// this document at all are answers the producers give.

// SessionEnvVars resolves the workspace's [session.env] declaration into the
// values every prepared session receives.
//
// It is workspace-scoped by construction, matching the declaration: the
// promote list is resolved against the workspace's own [env] pipeline, with no
// repository in scope, so the same map is what every repository's session
// gets. A repository that needs its own variables has the [env] pipeline
// itself, which is per-repo and untouched by this.
//
// The returned SourceEntry list is the provenance of the inputs that
// contributed bytes, for the callers that roll it into a fingerprint.
func SessionEnvVars(cfg *config.WorkspaceConfig, effective EffectiveConfig, configDir string) (map[string]string, []SourceEntry, error) {
	if cfg == nil || cfg.Session.Env.IsEmpty() {
		return nil, nil, nil
	}

	// The same workspace-scoped context the instance root's own settings
	// document is resolved against: the workspace env file if one was
	// discovered, and no repository.
	wsEnvFile, _, _ := DiscoverEnvFiles(configDir)
	if wsEnvFile != "" {
		if rel, err := filepath.Rel(configDir, wsEnvFile); err == nil {
			wsEnvFile = rel
		}
	}
	ctx := &MaterializeContext{
		Config:        cfg,
		Effective:     effective,
		ConfigDir:     configDir,
		DiscoveredEnv: &DiscoveredEnv{WorkspaceFile: wsEnvFile},
	}

	vars, sources, err := resolveDeclaredEnvVars(ctx, "session.env", cfg.Session.Env.Promote, cfg.Session.Env.Vars)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving the session environment: %w", err)
	}
	return vars, sources, nil
}

// mergeSessionEnv layers an agent-specific env declaration over the neutral
// one, with the agent-specific values winning per key.
//
// The precedence is the narrower declaration over the broader one, which is the
// direction every other override in this codebase runs. It does not make the
// Claude-named table a gate: a key declared only in [session.env] reaches a
// Claude session exactly as it reaches a Codex one, and a workspace that
// declares no [claude.env] at all still gets the neutral table delivered.
func mergeSessionEnv(session, agentSpecific map[string]string) map[string]string {
	if len(session) == 0 {
		return agentSpecific
	}
	if len(agentSpecific) == 0 {
		return session
	}
	out := make(map[string]string, len(session)+len(agentSpecific))
	for key, value := range session {
		out[key] = value
	}
	for key, value := range agentSpecific {
		out[key] = value
	}
	return out
}
