package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
)

// modelCategoriesByAgent maps niwa's portable, vendor-neutral capability
// categories to the concrete versionless model name forwarded to the selected
// agent, per agent. The values are deliberately versionless (e.g. "opus", not
// "claude-opus-4-8") so niwa stays out of the version-pinning business: the
// agent resolves the alias to whatever concrete model that name currently
// points at.
//
// The category vocabulary is the abstraction a caller reaches for when they care
// about capability, not vendor -- "give me the fast one" -- and it is the layer
// that the multi-vendor router remaps per agent without touching call sites. The
// Codex values are versionless placeholders chosen for the seam; like the Claude
// names, they are adjusted freely as the vendor's aliases change.
var modelCategoriesByAgent = map[agent.Agent]map[string]string{
	agent.AgentClaude: {
		"fast":     "haiku",
		"balanced": "sonnet",
		"powerful": "opus",
	},
	agent.AgentCodex: {
		"fast":     "gpt-5-codex-mini",
		"balanced": "gpt-5-codex",
		"powerful": "gpt-5",
	},
}

// knownModelNamesByAgent is, per agent, the set of versionless vendor model
// names niwa recognizes and forwards unchanged. Membership only suppresses the
// "unrecognized" warning; an unknown value is still forwarded (see
// resolveDispatchModel), so a brand-new alias or a full model id keeps working
// before niwa learns about it.
var knownModelNamesByAgent = map[agent.Agent]map[string]bool{
	agent.AgentClaude: {
		"fable":  true,
		"opus":   true,
		"sonnet": true,
		"haiku":  true,
	},
	agent.AgentCodex: {
		"gpt-5":            true,
		"gpt-5-codex":      true,
		"gpt-5-codex-mini": true,
	},
}

// modelCategoriesFor returns the category map for the given agent (the Claude
// map for the zero value, so the default path is unchanged).
func modelCategoriesFor(ag agent.Agent) map[string]string {
	if m, ok := modelCategoriesByAgent[ag]; ok {
		return m
	}
	return modelCategoriesByAgent[agent.AgentClaude]
}

// knownModelNamesFor returns the known-name set for the given agent (the Claude
// set for the zero value).
func knownModelNamesFor(ag agent.Agent) map[string]bool {
	if m, ok := knownModelNamesByAgent[ag]; ok {
		return m
	}
	return knownModelNamesByAgent[agent.AgentClaude]
}

// agentBinaryName is the vendor binary the resolved model is forwarded to, used
// only in the unrecognized-model warning text. The zero value maps to "claude"
// so the default path's message is unchanged.
func agentBinaryName(ag agent.Agent) string {
	if ag == agent.AgentCodex {
		return "codex"
	}
	return "claude"
}

// resolveDispatchModel maps a user-supplied --model value (a category or a
// versionless vendor name) to the concrete value forwarded to the selected
// agent, plus an optional warning to surface on stderr.
//
// Resolution order (per the selected agent's sets):
//   - "" -> ("", "") -- no model selected, forward nothing.
//   - a known category (fast/balanced/powerful) -> its concrete model, no warning.
//   - a known vendor name -> that name lowercased, no warning.
//   - anything else -> the raw value UNCHANGED, plus a warning.
//
// The unknown case forwards rather than rejects on purpose: niwa must not become
// a gatekeeper that breaks the instant a vendor ships a new alias or a caller
// passes a full model id. The warning surfaces typos without blocking the launch.
func resolveDispatchModel(ag agent.Agent, raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	key := strings.ToLower(trimmed)
	cats := modelCategoriesFor(ag)
	if concrete, ok := cats[key]; ok {
		return concrete, ""
	}
	known := knownModelNamesFor(ag)
	if known[key] {
		return key, ""
	}
	return trimmed, fmt.Sprintf(
		"unrecognized model %q; forwarding to %s as-is (categories: %s; models: %s)",
		trimmed, agentBinaryName(ag), joinSortedKeys(cats), joinSortedBoolKeys(known),
	)
}

// knownModelHint returns a human-readable one-line summary of the accepted
// values for the given agent, used in flag help text so the vocabulary is
// discoverable from `niwa dispatch --help`.
func knownModelHint(ag agent.Agent) string {
	return fmt.Sprintf("categories: %s; versionless names: %s",
		joinSortedKeys(modelCategoriesFor(ag)), joinSortedBoolKeys(knownModelNamesFor(ag)))
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
