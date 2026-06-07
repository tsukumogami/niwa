package config

import (
	"testing"

	"github.com/BurntSushi/toml"
)

func actionPtr(a Action) *Action { return &a }

// TestActionUnmarshalTOML verifies that warn/fail decode and invalid values
// are rejected.
func TestActionUnmarshalTOML(t *testing.T) {
	type holder struct {
		Policy EnvExamplePolicy `toml:"env_example_policy"`
	}

	t.Run("valid", func(t *testing.T) {
		var h holder
		input := `
[env_example_policy]
vendor_token = "fail"
entropy = "warn"

[env_example_policy.vars]
STRIPE_EXAMPLE_KEY = "warn"
`
		if err := toml.Unmarshal([]byte(input), &h); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if h.Policy.VendorToken == nil || *h.Policy.VendorToken != ActionFail {
			t.Errorf("vendor_token = %v, want fail", h.Policy.VendorToken)
		}
		if h.Policy.Entropy == nil || *h.Policy.Entropy != ActionWarn {
			t.Errorf("entropy = %v, want warn", h.Policy.Entropy)
		}
		if got := h.Policy.Vars["STRIPE_EXAMPLE_KEY"]; got != ActionWarn {
			t.Errorf("vars[STRIPE_EXAMPLE_KEY] = %v, want warn", got)
		}
	})

	t.Run("invalid", func(t *testing.T) {
		var h holder
		input := `
[env_example_policy]
vendor_token = "block"
`
		if err := toml.Unmarshal([]byte(input), &h); err == nil {
			t.Fatal("expected error for invalid action, got nil")
		}
	})
}

// TestEffectiveEnvExamplePolicyDefault verifies the warn default when nothing
// is configured at any level.
func TestEffectiveEnvExamplePolicyDefault(t *testing.T) {
	got := EffectiveEnvExamplePolicy(nil, nil, "repo", "KEY", CategoryVendorToken, nil)
	if got != ActionWarn {
		t.Errorf("default = %v, want warn", got)
	}
}

// TestEffectiveEnvExamplePolicyGlobalCategory verifies the global category rung
// applies when nothing more specific is set.
func TestEffectiveEnvExamplePolicyGlobalCategory(t *testing.T) {
	global := &EnvExamplePolicy{VendorToken: actionPtr(ActionFail)}
	ws := &WorkspaceConfig{}

	if got := EffectiveEnvExamplePolicy(global, ws, "repo", "KEY", CategoryVendorToken, nil); got != ActionFail {
		t.Errorf("vendor-token = %v, want fail (global)", got)
	}
	// A different category falls through to the warn default.
	if got := EffectiveEnvExamplePolicy(global, ws, "repo", "KEY", CategoryEntropy, nil); got != ActionWarn {
		t.Errorf("entropy = %v, want warn (global only set vendor_token)", got)
	}
}

// TestEffectiveEnvExamplePolicyWorkspaceOverridesGlobal verifies workspace
// category beats global category.
func TestEffectiveEnvExamplePolicyWorkspaceOverridesGlobal(t *testing.T) {
	global := &EnvExamplePolicy{VendorToken: actionPtr(ActionFail)}
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{VendorToken: actionPtr(ActionWarn)},
		},
	}
	if got := EffectiveEnvExamplePolicy(global, ws, "repo", "KEY", CategoryVendorToken, nil); got != ActionWarn {
		t.Errorf("vendor-token = %v, want warn (workspace overrides global)", got)
	}
}

// TestEffectiveEnvExamplePolicyRepoOverridesWorkspace verifies per-repo
// category beats workspace category.
func TestEffectiveEnvExamplePolicyRepoOverridesWorkspace(t *testing.T) {
	global := &EnvExamplePolicy{VendorToken: actionPtr(ActionWarn)}
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{VendorToken: actionPtr(ActionWarn)},
		},
		Repos: map[string]RepoOverride{
			"repo": {EnvExamplePolicy: &EnvExamplePolicy{VendorToken: actionPtr(ActionFail)}},
		},
	}
	if got := EffectiveEnvExamplePolicy(global, ws, "repo", "KEY", CategoryVendorToken, nil); got != ActionFail {
		t.Errorf("vendor-token = %v, want fail (repo overrides workspace)", got)
	}
	// An unrelated repo inherits the workspace value.
	if got := EffectiveEnvExamplePolicy(global, ws, "other", "KEY", CategoryVendorToken, nil); got != ActionWarn {
		t.Errorf("other repo vendor-token = %v, want warn (inherits workspace)", got)
	}
}

// TestEffectiveEnvExamplePolicyCategoryInheritanceFallThrough verifies that an
// unset per-repo category falls through to workspace, then global.
func TestEffectiveEnvExamplePolicyCategoryInheritanceFallThrough(t *testing.T) {
	global := &EnvExamplePolicy{Entropy: actionPtr(ActionFail)}
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{VendorToken: actionPtr(ActionWarn)},
		},
		Repos: map[string]RepoOverride{
			"repo": {EnvExamplePolicy: &EnvExamplePolicy{}},
		},
	}
	// Entropy is set only at global; repo+workspace are unset for it.
	if got := EffectiveEnvExamplePolicy(global, ws, "repo", "KEY", CategoryEntropy, nil); got != ActionFail {
		t.Errorf("entropy = %v, want fail (falls through to global)", got)
	}
}

// TestEffectiveEnvExamplePolicyInlineBeatsCategory verifies an inline
// annotation outranks any per-category config (but not per-variable config).
func TestEffectiveEnvExamplePolicyInlineBeatsCategory(t *testing.T) {
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{VendorToken: actionPtr(ActionFail)},
		},
	}
	inline := actionPtr(ActionWarn)
	if got := EffectiveEnvExamplePolicy(nil, ws, "repo", "KEY", CategoryVendorToken, inline); got != ActionWarn {
		t.Errorf("inline = %v, want warn (inline beats category)", got)
	}
}

// TestEffectiveEnvExamplePolicyWorkspaceVarsBeatsInline verifies the operator's
// workspace per-variable entry outranks an inline annotation.
func TestEffectiveEnvExamplePolicyWorkspaceVarsBeatsInline(t *testing.T) {
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{
				Vars: map[string]Action{"KEY": ActionFail},
			},
		},
	}
	inline := actionPtr(ActionWarn)
	if got := EffectiveEnvExamplePolicy(nil, ws, "repo", "KEY", CategoryVendorToken, inline); got != ActionFail {
		t.Errorf("= %v, want fail (workspace vars beats inline)", got)
	}
}

// TestEffectiveEnvExamplePolicyRepoVarsBeatsWorkspaceVars verifies per-repo
// vars outranks workspace vars (the most-specific rung).
func TestEffectiveEnvExamplePolicyRepoVarsBeatsWorkspaceVars(t *testing.T) {
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{
				Vars: map[string]Action{"KEY": ActionWarn},
			},
		},
		Repos: map[string]RepoOverride{
			"repo": {EnvExamplePolicy: &EnvExamplePolicy{
				Vars: map[string]Action{"KEY": ActionFail},
			}},
		},
	}
	if got := EffectiveEnvExamplePolicy(nil, ws, "repo", "KEY", CategoryVendorToken, nil); got != ActionFail {
		t.Errorf("= %v, want fail (repo vars beats workspace vars)", got)
	}
}

// TestEffectiveEnvExamplePolicyVarsOnlyForNamedKey verifies a vars entry only
// applies to its own key; other keys fall through to category/default.
func TestEffectiveEnvExamplePolicyVarsOnlyForNamedKey(t *testing.T) {
	ws := &WorkspaceConfig{
		Workspace: WorkspaceMeta{
			EnvExamplePolicy: &EnvExamplePolicy{
				VendorToken: actionPtr(ActionFail),
				Vars:        map[string]Action{"OTHER": ActionWarn},
			},
		},
	}
	if got := EffectiveEnvExamplePolicy(nil, ws, "repo", "KEY", CategoryVendorToken, nil); got != ActionFail {
		t.Errorf("KEY = %v, want fail (vars entry is for OTHER, KEY uses category)", got)
	}
	if got := EffectiveEnvExamplePolicy(nil, ws, "repo", "OTHER", CategoryVendorToken, nil); got != ActionWarn {
		t.Errorf("OTHER = %v, want warn (vars entry)", got)
	}
}
