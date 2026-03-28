package workspace

import (
	"fmt"
	"os"
	"path/filepath"
)

// scaffoldTemplate is the commented workspace.toml template.
// Only [workspace] is active; all other sections are commented-out examples.
const scaffoldTemplate = `[workspace]
name = "%s"
# version = "0.1.0"
default_branch = "main"
content_dir = "claude"

# --- Sources: GitHub orgs to discover repos from ---
# Uncomment and configure at least one source before running niwa apply.
#
# [[sources]]
# org = "my-org"

# --- Groups: classify repos into directories ---
# [groups.public]
# visibility = "public"
#
# [groups.private]
# visibility = "private"

# --- Per-repo overrides ---
# [repos.my-repo]
# claude = false

# --- Content hierarchy ---
# [content.workspace]
# source = "workspace.md"

# --- Hooks, settings, environment, channels ---
# See docs/designs/DESIGN-workspace-config.md for full schema reference.
# [hooks]
# [settings]
# [env]
# [channels]
`

// defaultWorkspaceName is used when no name is provided to Scaffold.
const defaultWorkspaceName = "workspace"

// Scaffold creates a .niwa/ directory under dir with a commented workspace.toml
// template and an empty content directory (claude/). If name is empty, the
// workspace name defaults to "workspace".
func Scaffold(dir, name string) error {
	if name == "" {
		name = defaultWorkspaceName
	}

	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		return fmt.Errorf("creating %s directory: %w", StateDir, err)
	}

	content := fmt.Sprintf(scaffoldTemplate, name)
	configPath := filepath.Join(niwaDir, WorkspaceConfigFile)
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", WorkspaceConfigFile, err)
	}

	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return fmt.Errorf("creating content directory: %w", err)
	}

	return nil
}
