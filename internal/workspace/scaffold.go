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
#
# --- Explicit repos (from outside source orgs) ---
# [repos.external-tool]
# url = "git@github.com:other-org/tool.git"
# group = "private"

# --- Claude Code configuration, content hierarchy, environment ---
# See docs/designs/DESIGN-workspace-config.md for full schema reference.
# [claude.content.workspace]
# source = "workspace.md"
# [claude]
# marketplaces = ["my-org/my-plugins"]
# plugins = ["my-tool@my-plugins"]
# [[claude.hooks.pre_tool_use]]
# matcher = "Bash"
# scripts = ["hooks/pre_tool_use/gate.sh"]
# [claude.settings]
# [claude.env]
# promote = ["GH_TOKEN"]
# [claude.env.vars]
# EXTRA_FLAG = "settings-only"
# [claude.env.secrets]
# ANTHROPIC_API_KEY = "vault://team/ANTHROPIC_API_KEY"
# --- Instance root overrides (workspace-level Claude Code session) ---
# [instance.claude.settings]
# permissions = "ask"
# [env]
# [env.vars]
# LOG_LEVEL = "debug"
# [env.secrets]
# GITHUB_TOKEN = "vault://team/GITHUB_TOKEN"
# [files]
# "extensions/design.md" = ".claude/shirabe-extensions/"
# [channels]
#
# --- Vault providers (optional) ---
# Pick ONE shape. The anonymous singular shape lets vault:// URIs omit
# the provider name (e.g., vault://API_KEY). The named shape allows
# multiple providers; URIs must name one (e.g., vault://team/API_KEY).
#
# [vault.provider]
# kind = "infisical"
# project_id = "your-project-id"
# env = "prod"
#
# OR:
#
# [vault.providers.team]
# kind = "infisical"
# project_id = "team-project"
# [vault.providers.personal]
# kind = "sops"
# key_path = "keys/personal.age"
#
# [vault]
# team_only = ["CRITICAL_TOKEN"]
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
