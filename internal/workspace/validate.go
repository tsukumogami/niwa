// Package workspace contains workspace-level helpers shared across init,
// apply, and other niwa commands.
package workspace

import (
	"fmt"

	"github.com/tsukumogami/niwa/internal/config"
)

// ValidateInitName checks a positional name supplied to `niwa init <name>`.
//
// It applies the workspace.toml name regex and rejects three literals that
// pass the regex but would land niwa in confused state: ".", ".." (path
// traversal sentinels) and ".niwa" (the marker directory niwa itself
// creates inside a workspace). Returned errors quote the offending input
// verbatim and describe the allowed character set in human-readable terms.
//
// Exported so future entry points that ingest a workspace name (RPC, MCP
// tool calls, etc.) reuse the same rules.
func ValidateInitName(name string) error {
	if name == "" {
		return fmt.Errorf("workspace name cannot be empty (allowed: alphanumerics, dots, underscores, hyphens)")
	}
	switch name {
	case ".":
		return fmt.Errorf("workspace name %q is rejected as a path-traversal sentinel (allowed: alphanumerics, dots, underscores, hyphens)", name)
	case "..":
		return fmt.Errorf("workspace name %q is rejected as a path-traversal sentinel (allowed: alphanumerics, dots, underscores, hyphens)", name)
	case ".niwa":
		return fmt.Errorf("workspace name %q conflicts with niwa's state directory marker (allowed: alphanumerics, dots, underscores, hyphens)", name)
	}
	if !config.NamePattern.MatchString(name) {
		return fmt.Errorf("workspace name %q contains characters outside the allowed set (alphanumerics, dots, underscores, hyphens)", name)
	}
	return nil
}
