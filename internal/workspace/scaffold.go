package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
#
# --- Verbatim file distribution at the non-repo levels ---
# [files] above targets each managed repo and inserts a .local infix so the
# output stays gitignored. The two tables below target the non-git levels --
# the instance root and the workspace root -- and copy VERBATIM (no .local),
# so a tool config that loads by an exact filename keeps its name. Example: a
# Claude Code project .mcp.json available to sessions started at those levels.
# [instance.files]
# "mcp.json" = ".mcp.json"
# [root.files]
# "mcp.json" = ".mcp.json"
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

// scaffoldFromSourceTemplate is the bootstrap-scaffold body per PRD
// Appendix A. Five placeholders are substituted by ScaffoldFromSource:
// <workspace-name>, <source-org>, <bootstrap-repo>, <vis-key>,
// <vis-value>. Section ordering, blank lines, and comments are part of
// the byte-equality contract — DO NOT reformat this string.
//
// The trailing schema-doc-link comment is identical to the one Scaffold
// emits via schemaDocLinkFooter (extracted helper). Keeping a copy here
// rather than splicing the helper into the template keeps the literal
// readable for diffing against the PRD.
const scaffoldFromSourceTemplate = `[workspace]
name = "<workspace-name>"
content_dir = "claude"

[[sources]]
org = "<source-org>"
repos = ["<bootstrap-repo>"]

[groups.<vis-key>]
visibility = "<vis-value>"
# Bind the bootstrap repo to this group by name: explicit-repos sources carry
# no live visibility, so name membership is what places the repo in a group.
repos = ["<bootstrap-repo>"]

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, instance).
`

// schemaDocLinkFooter returns the schema doc-link comment lines reused
// by Scaffold and ScaffoldFromSource. Kept as a function rather than a
// const so future variants (e.g., per-version doc-link URLs) can be
// switched at a single site.
func schemaDocLinkFooter() string {
	return "# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md\n" +
		"# for the full schema (claude.*, env.*, vault.*, files, instance).\n"
}

// ScaffoldOptions controls ScaffoldFromSource's output. Fields:
//
//   - Name           — workspace name (positional arg or slug repo basename).
//   - Org            — source org from the --from slug owner segment.
//   - Repo           — bootstrap repo name from the --from slug repo segment.
//   - Private        — visibility-from-bool input. NEVER plumbed from any
//     string field. See ScaffoldFromSource's docstring for the load-bearing
//     R16 invariant.
//   - IncludeGitkeep — when true, ScaffoldFromSource writes an empty
//     .niwa/claude/.gitkeep so the otherwise-empty content directory is
//     trackable by git (R15). Production callers always set true; some
//     tests suppress to assert the file is genuinely zero-byte.
type ScaffoldOptions struct {
	Name           string
	Org            string
	Repo           string
	Private        bool
	IncludeGitkeep bool
}

// ScaffoldFromSource writes a bootstrap workspace.toml under dir/.niwa/
// matching PRD Appendix A byte-for-byte after placeholder substitution.
// It is a sibling of Scaffold(dir, name): the existing Scaffold and its
// callers are unchanged.
//
// LOAD-BEARING R16 INVARIANT — visibility is derived from opts.Private
// (a bool). This function does NOT read or accept any string-typed
// visibility field. A future refactor that wants to switch to a
// string-derived visibility would have to change ScaffoldOptions.Private
// from `bool` to `string` — a visible diff in this file — because today
// no caller can pass a string here.
//
// This guards against TOML-metacharacter injection from a malicious
// GitHub API response (PRD security model): even if the API returns
// `"visibility": "]\n[evil"`, ScaffoldFromSource only consults the
// parallel `private` bool, which has no metacharacter representation.
func ScaffoldFromSource(dir string, opts ScaffoldOptions) error {
	if opts.Name == "" {
		return fmt.Errorf("ScaffoldFromSource: opts.Name is required")
	}
	if opts.Org == "" {
		return fmt.Errorf("ScaffoldFromSource: opts.Org is required")
	}
	if opts.Repo == "" {
		return fmt.Errorf("ScaffoldFromSource: opts.Repo is required")
	}

	niwaDir := filepath.Join(dir, StateDir)
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		return fmt.Errorf("creating %s directory: %w", StateDir, err)
	}

	visKey, visValue := "public", "public"
	if opts.Private {
		visKey, visValue = "private", "private"
	}

	body := strings.NewReplacer(
		"<workspace-name>", opts.Name,
		"<source-org>", opts.Org,
		"<bootstrap-repo>", opts.Repo,
		"<vis-key>", visKey,
		"<vis-value>", visValue,
	).Replace(scaffoldFromSourceTemplate)

	configPath := filepath.Join(niwaDir, WorkspaceConfigFile)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", WorkspaceConfigFile, err)
	}

	contentDir := filepath.Join(niwaDir, "claude")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return fmt.Errorf("creating content directory: %w", err)
	}

	if opts.IncludeGitkeep {
		gitkeep := filepath.Join(contentDir, ".gitkeep")
		// Zero-byte file — explicit empty slice so future maintainers
		// don't accidentally append a newline.
		if err := os.WriteFile(gitkeep, []byte{}, 0o644); err != nil {
			return fmt.Errorf("writing .gitkeep: %w", err)
		}
	}

	return nil
}
