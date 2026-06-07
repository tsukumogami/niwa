package config

import "fmt"

// Action is the response the .env.example pre-pass takes for an undeclared
// probable-secret key: warn (continue) or fail (abort apply). A nil *Action
// at any config level means "unset/inherit", matching the *bool idiom used by
// ReadEnvExample.
type Action string

const (
	// ActionWarn emits a diagnostic and proceeds.
	ActionWarn Action = "warn"
	// ActionFail accumulates an error that aborts apply.
	ActionFail Action = "fail"
)

// UnmarshalText implements encoding.TextUnmarshaler so TOML string values
// decode into Action. Only "warn" and "fail" are accepted; any other value
// is a parse error.
func (a *Action) UnmarshalText(text []byte) error {
	switch s := string(text); Action(s) {
	case ActionWarn, ActionFail:
		*a = Action(s)
		return nil
	default:
		return fmt.Errorf("invalid env_example_policy action %q (want \"warn\" or \"fail\")", s)
	}
}

// MarshalText implements encoding.TextMarshaler, emitting the action literal.
func (a Action) MarshalText() ([]byte, error) {
	return []byte(a), nil
}

// EnvDetectionCategory is the typed result of classifying a .env.example value.
// It keys the per-category policy lookup so control flow no longer parses the
// diagnostic reason string. It lives in internal/config (not internal/workspace)
// so the resolver here and the classifier in internal/workspace can share the
// type without an import cycle: workspace imports config, config imports nothing
// from workspace.
type EnvDetectionCategory int

const (
	// CategorySafe means the value is not a probable secret.
	CategorySafe EnvDetectionCategory = iota
	// CategoryVendorToken means the value matched a vendor-token prefix.
	CategoryVendorToken
	// CategoryEntropy means the value exceeded the entropy threshold.
	CategoryEntropy
)

// String returns the value-free category name used in diagnostics. It never
// contains value bytes, fragments, the matched vendor prefix, or the entropy
// score (R22).
func (c EnvDetectionCategory) String() string {
	switch c {
	case CategorySafe:
		return "safe"
	case CategoryVendorToken:
		return "vendor-token"
	case CategoryEntropy:
		return "entropy"
	default:
		return "unknown"
	}
}

// EnvExamplePolicy expresses the warn/fail response policy for the
// .env.example pre-pass. VendorToken and Entropy are per-category settings; a
// nil pointer means unset/inherit. Vars is a per-variable override map that is
// project-scope only (workspace top level and per-repo positions); the personal
// global position carries category keys only.
type EnvExamplePolicy struct {
	VendorToken *Action           `toml:"vendor_token,omitempty"`
	Entropy     *Action           `toml:"entropy,omitempty"`
	Vars        map[string]Action `toml:"vars,omitempty"`
}

// categoryAction returns the per-category Action pointer for the given category
// from this policy, or nil when unset (or when p is nil). CategorySafe has no
// configurable action and always returns nil.
func (p *EnvExamplePolicy) categoryAction(category EnvDetectionCategory) *Action {
	if p == nil {
		return nil
	}
	switch category {
	case CategoryVendorToken:
		return p.VendorToken
	case CategoryEntropy:
		return p.Entropy
	default:
		return nil
	}
}

// varsAction returns the per-variable Action for key from this policy, or nil
// when unset (or when p is nil or has no vars map).
func (p *EnvExamplePolicy) varsAction(key string) *Action {
	if p == nil || p.Vars == nil {
		return nil
	}
	if a, ok := p.Vars[key]; ok {
		return &a
	}
	return nil
}

// EffectiveEnvExamplePolicy resolves the effective Action for one undeclared
// key+category. globalPolicy is the resolved personal/global-override policy,
// passed explicitly because it is not part of WorkspaceConfig. inline is the
// per-key annotation parsed from the .env.example file (nil when absent).
//
// Precedence, most-specific first; unset levels inherit the next broader level:
//
//  1. operator per-variable entry: per-repo vars, then workspace vars
//  2. inline annotation for the key
//  3. per-category policy: per-repo, then workspace, then global
//  4. default warn
func EffectiveEnvExamplePolicy(globalPolicy *EnvExamplePolicy, ws *WorkspaceConfig, repoName, key string, category EnvDetectionCategory, inline *Action) Action {
	var repoPolicy, wsPolicy *EnvExamplePolicy
	if ws != nil {
		wsPolicy = ws.Workspace.EnvExamplePolicy
		if override, ok := ws.Repos[repoName]; ok {
			repoPolicy = override.EnvExamplePolicy
		}
	}

	// 1. Operator per-variable entry (per-repo, then workspace). Vars is
	// project-scope only, so the global policy is not consulted here.
	if a := repoPolicy.varsAction(key); a != nil {
		return *a
	}
	if a := wsPolicy.varsAction(key); a != nil {
		return *a
	}

	// 2. Inline annotation for the key.
	if inline != nil {
		return *inline
	}

	// 3. Per-category policy: per-repo, then workspace, then global.
	if a := repoPolicy.categoryAction(category); a != nil {
		return *a
	}
	if a := wsPolicy.categoryAction(category); a != nil {
		return *a
	}
	if a := globalPolicy.categoryAction(category); a != nil {
		return *a
	}

	// 4. Default.
	return ActionWarn
}
