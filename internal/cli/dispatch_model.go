package cli

import (
	"fmt"
	"strings"

	"github.com/tsukumogami/niwa/internal/agentplan"
)

// resolveDispatchModel maps a user-supplied --model value -- a portable
// capability category or a versionless vendor name -- to the concrete value
// forwarded to the agent being launched, plus an optional warning for stderr.
//
// Resolution order, against the launched agent's own vocabulary:
//   - "" -> ("", "") -- no model selected, forward nothing.
//   - a known category -> the concrete model that agent binds it to, no warning.
//   - a known vendor name -> that name lowercased, no warning.
//   - anything else -> the raw value unchanged, plus a warning.
//
// The unknown case forwards rather than rejects on purpose: niwa must not become
// a gatekeeper that breaks the instant a vendor ships a new alias or a caller
// passes a full model id. The warning surfaces a typo without blocking the
// launch.
//
// Both vocabularies come from the agent's launch spec, so this function reads
// the same table the launch itself does and there is no second list to drift
// from it.
func resolveDispatchModel(spec agentplan.LaunchSpec, raw string) (string, string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	key := strings.ToLower(trimmed)
	if concrete, ok := spec.ModelCategories[key]; ok {
		return concrete, ""
	}
	for _, known := range spec.KnownModels {
		if key == known {
			return key, ""
		}
	}
	return trimmed, fmt.Sprintf(
		"unrecognized model %q; forwarding to %s as-is (categories: %s; models: %s)",
		trimmed, spec.Binary,
		strings.Join(spec.ModelCategoryNames(), ", "),
		strings.Join(spec.KnownModelNames(), ", "),
	)
}

// dispatchModelFlagHelp is the --model help line. It names the portable
// categories, which are niwa's own vocabulary and the same three words whoever
// is launched, and leaves the versionless names to the agent -- printing one
// agent's list in help text for a flag that reaches whichever agent the
// workspace resolves to would be a lie for the other.
func dispatchModelFlagHelp() string {
	return "model for the worker's main chat loop: a capability category (" +
		strings.Join(agentplan.ModelCategories(), ", ") +
		") or a versionless model name the launched agent accepts; overrides the [global] dispatch_model default"
}
