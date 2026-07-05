package cli

import (
	"fmt"
	"sort"
	"strings"
)

// modelCategories maps niwa's portable, vendor-neutral capability categories to
// the concrete versionless model name forwarded to `claude --model`. The values
// are deliberately versionless (e.g. "opus", not "claude-opus-4-8") so niwa
// stays out of the version-pinning business: Claude Code resolves the alias to
// whatever concrete model that name currently points at.
//
// The category vocabulary is the abstraction a caller reaches for when they care
// about capability, not vendor -- "give me the fast one" -- and it is the layer
// that a future multi-vendor router can remap without touching call sites.
var modelCategories = map[string]string{
	"fast":     "haiku",
	"balanced": "sonnet",
	"powerful": "opus",
}

// knownModelNames is the set of versionless vendor model names niwa recognizes
// and forwards unchanged. Membership only suppresses the "unrecognized" warning;
// an unknown value is still forwarded (see resolveDispatchModel), so a brand-new
// alias or a full model id keeps working before niwa learns about it.
var knownModelNames = map[string]bool{
	"fable":  true,
	"opus":   true,
	"sonnet": true,
	"haiku":  true,
}

// resolveDispatchModel maps a user-supplied --model value (a category or a
// versionless vendor name) to the concrete value forwarded to `claude --model`,
// plus an optional warning to surface on stderr.
//
// Resolution order:
//   - "" -> ("", "") -- no model selected, forward nothing.
//   - a known category (fast/balanced/powerful) -> its concrete model, no warning.
//   - a known vendor name (fable/opus/sonnet/haiku) -> that name lowercased, no warning.
//   - anything else -> the raw value UNCHANGED, plus a warning.
//
// The unknown case forwards rather than rejects on purpose: niwa must not become
// a gatekeeper that breaks the instant Anthropic ships a new alias or a caller
// passes a full model id. The warning surfaces typos without blocking the launch.
func resolveDispatchModel(raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	key := strings.ToLower(trimmed)
	if concrete, ok := modelCategories[key]; ok {
		return concrete, ""
	}
	if knownModelNames[key] {
		return key, ""
	}
	return trimmed, fmt.Sprintf(
		"unrecognized model %q; forwarding to claude as-is (categories: %s; models: %s)",
		trimmed, joinSortedKeys(modelCategories), joinSortedBoolKeys(knownModelNames),
	)
}

// knownModelHint returns a human-readable one-line summary of the accepted
// values, used in flag help text so the vocabulary is discoverable from
// `niwa dispatch --help`.
func knownModelHint() string {
	return fmt.Sprintf("categories: %s; versionless names: %s",
		joinSortedKeys(modelCategories), joinSortedBoolKeys(knownModelNames))
}

func joinSortedKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

func joinSortedBoolKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
