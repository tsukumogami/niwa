package vault

import "sort"

// ProviderShadow describes a provider name declared by both the team
// and the personal layers. Unlike workspace.Shadow (which names key-
// level overlaps in env/files/settings), ProviderShadow fires at
// Bundle granularity — before the R12 hard-error check in
// resolve.CheckProviderNameCollision — so the apply pipeline can
// surface a visible diagnostic even though the apply will fail.
//
// Provider names are non-secret identifiers (the anonymous provider
// renders as the empty string; the diagnostic layer substitutes a
// human-readable placeholder). ProviderShadow never carries
// secret.Value material, matching the invariant on
// workspace.Shadow.
type ProviderShadow struct {
	// Name is the colliding provider name. The empty string denotes
	// the anonymous [vault.provider] shape; CLI formatters substitute
	// "(anonymous)" when rendering.
	Name string

	// TeamSource is the file path the team bundle attributed to this
	// provider declaration. The Bundle does not retain ProviderSpec
	// sources post-Build, so v1 callers populate this from the
	// pipeline's known file paths; the field is carried here for
	// forward compatibility with a future Bundle.ProviderSource API.
	TeamSource string

	// PersonalSource is the file path of the personal overlay that
	// redeclared the name.
	PersonalSource string
}

// DetectProviderShadows returns the set of provider names declared by
// both bundles. The function is pure and non-blocking: it only reads
// Bundle.Names(). It is the informational sibling of
// resolve.CheckProviderNameCollision; callers SHOULD emit the
// diagnostic first (so the user sees the shadow line) and then let
// CheckProviderNameCollision return the hard error.
//
// TeamSource and PersonalSource are left blank when callers do not
// have per-provider attribution on hand; the CLI diagnostic wrapper
// is responsible for substituting "workspace.toml" / "niwa.toml"
// defaults. This keeps the pure function free of string constants
// that belong to the apply pipeline's attribution policy.
//
// Both bundles nil returns nil. A nil team or nil personal bundle
// means "no collisions possible" — returns nil, never an error.
// Results are sorted by Name for deterministic output.
func DetectProviderShadows(team, personal *Bundle) []ProviderShadow {
	if team == nil || personal == nil {
		return nil
	}
	teamNames := map[string]bool{}
	for _, n := range team.Names() {
		teamNames[n] = true
	}
	var out []ProviderShadow
	for _, n := range personal.Names() {
		if teamNames[n] {
			out = append(out, ProviderShadow{Name: n})
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
