package workspace

import (
	"sort"

	"github.com/tsukumogami/niwa/internal/config"
)

// Shadow describes a single team-declared value that a personal
// overlay redefines. Values are intentionally omitted: a Shadow carries
// only non-secret identifiers (key name, source file paths, layer
// label) so the record can flow through stderr diagnostics and
// state.json without leaking plaintext or redacted placeholders.
//
// A compile-time invariant (asserted by TestShadowHasNoSecretValueField)
// forbids any secret.Value field on this struct. The diagnostic pipeline
// MUST NOT depend on inspecting the resolved secret bytes: the shadow
// record is a structural fact about which layer declared which key,
// not a statement about the value's content.
type Shadow struct {
	// Kind names the category being shadowed. One of:
	// "env-var", "env-secret", "claude-env-var", "claude-env-secret",
	// "files", "settings".
	Kind string `json:"kind"`

	// Name is the key identifier being shadowed: the env var name for
	// env-var/env-secret/claude-env-*, the file source path for
	// "files", or the setting key for "settings".
	Name string `json:"name"`

	// TeamSource is the file path of the team declaration. Today this
	// is always "workspace.toml" for shadows detected by DetectShadows;
	// a future iteration may carry per-table attribution.
	TeamSource string `json:"team_source"`

	// PersonalSource is the file path of the personal overlay that
	// redeclares Name. Today this is always "niwa.toml".
	PersonalSource string `json:"personal_source"`

	// Layer names the overlay layer responsible for the shadow. For
	// v1 this is always "personal-overlay"; future multi-layer
	// configurations (e.g., a machine-local layer) would extend the
	// vocabulary.
	Layer string `json:"layer"`
}

// ShadowLayerPersonalOverlay is the Layer value emitted by
// DetectShadows for personal-overlay shadows. Extracted as a constant
// so CLI diagnostics and state-migration code can match on it without
// hard-coding the string.
const ShadowLayerPersonalOverlay = "personal-overlay"

// shadowKind* constants mirror the Shadow.Kind vocabulary used by
// DetectShadows. Keeping them inside this file (rather than as
// exported constants) avoids coupling future consumers to the exact
// enumeration: diagnostics match on the string, not the identifier.
const (
	shadowKindEnvVar          = "env-var"
	shadowKindEnvSecret       = "env-secret"
	shadowKindClaudeEnvVar    = "claude-env-var"
	shadowKindClaudeEnvSecret = "claude-env-secret"
	shadowKindFiles           = "files"
	shadowKindSettings        = "settings"
)

// teamSourceDefault / personalSourceDefault hold the file path
// attributions emitted by DetectShadows when the underlying config
// structs do not carry per-slot provenance. Both files are the
// canonical v1 locations; Issue 7+ may add per-struct SourceFile
// fields, in which case DetectShadows can be extended to prefer those
// values over the defaults.
const (
	teamSourceDefault     = "workspace.toml"
	personalSourceDefault = "niwa.toml"
)

// DetectShadows returns the set of personal-overlay shadows over a
// team workspace config. The function is pure: it does not mutate
// team or overlay, consult any vault provider, or read secret bytes.
// The returned slice is sorted by (Kind, Name) for deterministic
// stderr output.
//
// A Shadow is emitted when the overlay's [global] block OR the
// overlay's [workspaces.<scope>] block declares a key that the team
// also declares in the corresponding table. The overlay's
// [workspaces.<scope>] sub-block is matched against the team
// workspace name (team.Workspace.Name), mirroring the key used by
// workspace.ResolveGlobalOverride to select which sub-block applies
// at merge time.
//
// Both layers nil or empty returns a nil slice. Callers SHOULD treat
// a nil return as "no shadows" without special-casing.
func DetectShadows(team *config.WorkspaceConfig, overlay *config.GlobalConfigOverride) []Shadow {
	if team == nil || overlay == nil {
		return nil
	}

	var shadows []Shadow
	add := func(kind, name string) {
		shadows = append(shadows, Shadow{
			Kind:           kind,
			Name:           name,
			TeamSource:     teamSourceDefault,
			PersonalSource: personalSourceDefault,
			Layer:          ShadowLayerPersonalOverlay,
		})
	}

	collectLayer := func(g config.GlobalOverride) {
		// env.vars
		for k := range g.Env.Vars.Values {
			if _, ok := team.Env.Vars.Values[k]; ok {
				add(shadowKindEnvVar, k)
			}
		}
		// env.secrets
		for k := range g.Env.Secrets.Values {
			if _, ok := team.Env.Secrets.Values[k]; ok {
				add(shadowKindEnvSecret, k)
			}
		}
		// claude.env.vars / claude.env.secrets / claude.settings
		if g.Claude != nil {
			for k := range g.Claude.Env.Vars.Values {
				if _, ok := team.Claude.Env.Vars.Values[k]; ok {
					add(shadowKindClaudeEnvVar, k)
				}
			}
			for k := range g.Claude.Env.Secrets.Values {
				if _, ok := team.Claude.Env.Secrets.Values[k]; ok {
					add(shadowKindClaudeEnvSecret, k)
				}
			}
			for k := range g.Claude.Settings {
				if _, ok := team.Claude.Settings[k]; ok {
					add(shadowKindSettings, k)
				}
			}
		}
		// files
		for k := range g.Files {
			if _, ok := team.Files[k]; ok {
				add(shadowKindFiles, k)
			}
		}
	}

	// [global] layer applies to every workspace.
	collectLayer(overlay.Global)

	// Per-workspace [workspaces.<scope>] applies only when the scope
	// key matches the team's workspace name. This mirrors
	// workspace.ResolveGlobalOverride's selection logic so detection
	// stays in lock-step with which overlay values actually land in
	// the merged config.
	if ws, ok := overlay.Workspaces[team.Workspace.Name]; ok {
		collectLayer(ws)
	}

	if len(shadows) == 0 {
		return nil
	}
	sort.Slice(shadows, func(i, j int) bool {
		if shadows[i].Kind != shadows[j].Kind {
			return shadows[i].Kind < shadows[j].Kind
		}
		return shadows[i].Name < shadows[j].Name
	})
	return shadows
}
