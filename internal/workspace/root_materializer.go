package workspace

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/config"
)

// rootSkillsFS embeds the workspace-root project skills tree
// (internal/workspace/rootskills). Each rootskills/<name>/SKILL.md is
// materialized as a project skill under <workspaceRoot>/.claude/skills/<name>/
// so a Claude Code session launched at the workspace root loads it from the cwd
// regardless of plugin enablement. These are niwa-owned skills (e.g. dispatch)
// that must exist even when no plugin marketplace does; the workspace's own
// plugins are forwarded separately into the root settings by writeRootSettings
// (see rootHoistableConfig).
//
//go:embed rootskills
var rootSkillsFS embed.FS

// rootSkillsDir is the embedded directory holding the workspace-root project
// skills, and also the path prefix walked within rootSkillsFS.
const rootSkillsDir = "rootskills"

// rootSkillsTargetDir is the subdirectory under <workspaceRoot>/.claude where
// project skills are installed.
const rootSkillsTargetDir = "skills"

// rootSkillFileName is the per-skill manifest file Claude Code loads.
const rootSkillFileName = "SKILL.md"

// rootClaudeDir is the workspace-root managed config directory. The
// workspace root is a non-git directory above the instances, so the
// settings file is the non-.local settings.json (the same form the
// instance root uses), not settings.local.json.
const rootClaudeDir = ".claude"

// rootSettingsFile is the workspace-root managed settings file name.
const rootSettingsFile = "settings.json"

// rootClaudeFile is the workspace-root CLAUDE.md file name. A session
// launched at the workspace root loads this at startup; without it the
// coordinator (and any root session) starts with no workspace orientation.
const rootClaudeFile = "CLAUDE.md"

// instanceFromHookCommandSuffix is the niwa subcommand the workspace-root
// SessionStart hook invokes. The full command is
// "<abs-niwa> " + this suffix; abs-niwa is resolved at materialize time via
// os.Executable(). It is the instance-level hook entry point (distinct from
// "worktree from-hook"); see internal/cli/instance_from_hook.go.
const instanceFromHookCommandSuffix = "instance from-hook"

// rootSessionHookTimeoutSeconds is the per-command timeout written on the
// workspace-root SessionStart hook entry. It is generous (>= 120s)
// on purpose: a SessionStart hook provisions an ephemeral instance via
// `niwa create`, whose clone + vault cost can exceed a default harness timeout.
const rootSessionHookTimeoutSeconds = 180

// instanceFromHookCommand returns the absolute-path hook command string Claude
// invokes for the workspace-root session hooks, e.g.
// "/abs/niwa instance from-hook". The niwa path is slash-normalized so the
// JSON command is stable across platforms.
func instanceFromHookCommand(niwaPath string) string {
	return filepath.ToSlash(niwaPath) + " " + instanceFromHookCommandSuffix
}

// RootMaterializeOptions carries the inputs the root materializer needs that
// are not derivable from the config alone.
type RootMaterializeOptions struct {
	// NiwaPath is the absolute path of the running niwa binary
	// (os.Executable()). It is used to build the absolute-path session-hook
	// command. When empty, MaterializeWorkspaceRoot resolves it itself.
	NiwaPath string

	// EphemeralSessionMode is the ephemeral-session opt-in recorded in the
	// workspace-root state. It is emitted into the settings doc so the
	// materialized config records the workspace's ephemeral posture alongside
	// the hooks that act on it.
	EphemeralSessionMode bool

	// Agent is the resolved session-global coding agent this materialize
	// prepares the workspace root for. The zero value behaves as Claude
	// (agent.AgentClaude), so a caller that does not set it writes the
	// workspace-root context file exactly as before (CLAUDE.md). Under Codex it
	// selects AGENTS.md.
	Agent agent.Agent

	// ConfigDir is the workspace config source directory (typically
	// <workspaceRoot>/.niwa). It is the source root for [root.files] verbatim
	// file distribution; required only when [root.files] is non-empty.
	ConfigDir string
}

// MaterializeWorkspaceRoot writes the workspace-root managed config:
//
//   - <workspaceRoot>/.claude/settings.json built via the shared
//     buildSettingsDoc, carrying the SessionStart hook entry
//     (piping stdin to "niwa instance from-hook" with a generous timeout; no
//     SessionEnd entry -- teardown is reaper-driven, DESIGN Decision 6),
//     the permission posture (permissions.defaultMode, sourced the same way
//     instance materialization sources it), and the ephemeral-session-mode flag.
//   - <workspaceRoot>/CLAUDE.md carrying workspace-context content at root
//     altitude.
//
// It is the workspace-root counterpart to InstallWorkspaceRootSettings (which,
// despite its name, targets an INSTANCE root). The true workspace root -- the
// parent directory holding .niwa/workspace.toml and the instance subdirs -- is
// not a managed surface today; this is the materializer that makes it one.
//
// Returns the list of written file paths.
func MaterializeWorkspaceRoot(cfg *config.WorkspaceConfig, workspaceRoot string, opts RootMaterializeOptions) ([]string, error) {
	niwaPath := opts.NiwaPath
	if niwaPath == "" {
		resolved, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolving niwa binary path for root session hooks: %w", err)
		}
		niwaPath = resolved
	}

	var written []string

	settingsPath, err := writeRootSettings(cfg, workspaceRoot, niwaPath, opts.EphemeralSessionMode)
	if err != nil {
		return nil, err
	}
	written = append(written, settingsPath)

	claudePath, err := writeRootClaudeMD(cfg, workspaceRoot, opts.Agent)
	if err != nil {
		return nil, err
	}
	written = append(written, claudePath)

	skillPaths, err := writeRootSkills(workspaceRoot)
	if err != nil {
		return nil, err
	}
	written = append(written, skillPaths...)

	// Distribute [root.files] verbatim (no .local) to the workspace root.
	// Unlike the instance root, the workspace root has no managed-file state
	// store, so these writes are overwrite-idempotent like the other
	// root-managed files (settings.json, CLAUDE.md, skills): re-written every
	// apply, not removal-cleaned. The returned paths are reported but the
	// callers do not yet track them.
	if rootFiles := MergeInstanceOverrides(cfg).RootFiles; len(rootFiles) > 0 {
		if opts.ConfigDir == "" {
			return nil, fmt.Errorf("materializing workspace-root files: [root.files] is set but no config dir was provided")
		}
		mctx := &MaterializeContext{
			Config:    cfg,
			RepoDir:   workspaceRoot,
			ConfigDir: opts.ConfigDir,
		}
		filePaths, fErr := materializeVerbatimFiles(mctx, rootFiles)
		if fErr != nil {
			return nil, fmt.Errorf("materializing workspace-root files: %w", fErr)
		}
		written = append(written, filePaths...)
	}

	return written, nil
}

// writeRootSkills materializes the embedded rootskills tree as project skills
// under <workspaceRoot>/.claude/skills/. For each rootskills/<name>/SKILL.md it
// writes <workspaceRoot>/.claude/skills/<name>/SKILL.md. Project skills are
// loaded by Claude Code from the cwd's .claude/skills tree regardless of plugin
// enablement, so installing them here makes the skill available at the
// workspace root without depending on plugin loading.
//
// The walk is generic: every <name>/SKILL.md under the embedded tree is picked
// up, so adding a new root skill needs no change here. The writes are plain
// overwrites — the content is static, so re-running is idempotent. Returns the
// list of written absolute paths.
func writeRootSkills(workspaceRoot string) ([]string, error) {
	skillsRoot := filepath.Join(workspaceRoot, rootClaudeDir, rootSkillsTargetDir)

	var written []string
	walkErr := fs.WalkDir(rootSkillsFS, rootSkillsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != rootSkillFileName {
			return nil
		}
		// path is rootskills/<name>/SKILL.md; the skill name is the parent dir.
		name := filepath.Base(filepath.Dir(path))

		data, readErr := rootSkillsFS.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading embedded root skill %q: %w", path, readErr)
		}

		skillDir := filepath.Join(skillsRoot, name)
		if mkErr := os.MkdirAll(skillDir, 0o755); mkErr != nil {
			return fmt.Errorf("creating workspace-root skill directory %q: %w", skillDir, mkErr)
		}
		skillPath := filepath.Join(skillDir, rootSkillFileName)
		if wErr := os.WriteFile(skillPath, data, 0o644); wErr != nil {
			return fmt.Errorf("writing workspace-root skill %q: %w", skillPath, wErr)
		}
		written = append(written, skillPath)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("installing workspace-root skills: %w", walkErr)
	}
	return written, nil
}

// writeRootSettings builds and writes <workspaceRoot>/.claude/settings.json via
// the shared buildSettingsDoc. The permission posture is sourced from the
// effective [claude.settings] block (MergeInstanceOverrides) -- the same input
// the instance-root materializer feeds to buildSettingsDoc -- so the
// permissions.defaultMode value matches what an instance would get. The
// effective plugins and marketplaces are forwarded too, filtered to the subset
// that resolves at the workspace root (see rootHoistableConfig), so a
// root-launched session loads the workspace's plugins/skills. The
// SessionStart hook entry and the ephemeral-mode flag are layered
// on top via the SessionHooks injection.
func writeRootSettings(cfg *config.WorkspaceConfig, workspaceRoot, niwaPath string, ephemeral bool) (string, error) {
	effective := MergeInstanceOverrides(cfg)

	// Forward only the plugins/marketplaces that have a root-resolvable form.
	// repo:-sourced marketplaces point into an instance checkout that does not
	// exist at the workspace root, so they (and the plugins bound to them) are
	// excluded and reported -- never silently dropped.
	rootPlugins, rootMarketplaces, hoistReports := rootHoistableConfig(effective.Plugins, effective.Claude.Marketplaces)

	includeGit := false
	reports := append([]string(nil), hoistReports...)
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		// Permission posture: sourced exactly as the instance root sources it,
		// from the effective [claude.settings] map. buildSettingsDoc maps the
		// "permissions" key to permissions.defaultMode.
		Settings: effective.Claude.Settings,
		// Plugins/marketplaces: the root-hoistable subset, so the workspace's
		// plugins (and the skills they carry) load for a session launched at the
		// workspace root, matching what an instance would get for the github-
		// sourced entries.
		Plugins:                rootPlugins,
		Marketplaces:           rootMarketplaces,
		BaseDir:                workspaceRoot,
		IncludeGitInstructions: &includeGit,
		UseAbsolutePaths:       true,
		Reports:                &reports,
		SessionHooks: &SessionHooks{
			Command:        instanceFromHookCommand(niwaPath),
			TimeoutSeconds: rootSessionHookTimeoutSeconds,
		},
	})
	if err != nil {
		return "", fmt.Errorf("building workspace-root settings: %w", err)
	}
	emitReports(nil, reports)

	// Ephemeral-session-mode flag: recorded alongside the hooks that act on it
	// so the materialized config carries the workspace's ephemeral posture.
	doc["ephemeralSessionMode"] = ephemeral

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling workspace-root settings: %w", err)
	}
	data = append(data, '\n')

	claudeDir := filepath.Join(workspaceRoot, rootClaudeDir)
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return "", fmt.Errorf("creating workspace-root .claude directory: %w", err)
	}
	settingsPath := filepath.Join(claudeDir, rootSettingsFile)
	if err := os.WriteFile(settingsPath, data, secretFileMode); err != nil {
		return "", fmt.Errorf("writing workspace-root settings: %w", err)
	}
	return settingsPath, nil
}

// rootHoistableConfig partitions the effective workspace plugins and
// marketplaces into the subset that loads at the workspace root and the subset
// that does not. A root-launched session resolves its Claude config from the
// workspace root, where no instance has been provisioned and no repos are
// cloned, so an instance-relative source has no path to point at.
//
//   - github-sourced marketplaces ("org/repo") are root-stable: the source is a
//     remote reference that resolves identically anywhere, so they hoist as-is.
//   - repo:-sourced marketplaces ("repo:<name>/...") resolve to a directory
//     inside an instance checkout (e.g. a private `tools` repo) that exists only
//     once an instance is created. They have no root-resolvable path and are
//     excluded.
//
// A plugin is kept only when its marketplace hoisted (or it carries no
// "@marketplace" qualifier): Claude Code cannot enable a plugin from a
// marketplace it does not know at the root. Excluded marketplaces and plugins
// are returned as human-readable reports so the omission is visible rather than
// silent; the caller surfaces them via emitReports.
func rootHoistableConfig(plugins []string, marketplaces []config.MarketplaceConfig) (keptPlugins []string, keptMarketplaces []config.MarketplaceConfig, reports []string) {
	// Registration names of the marketplaces that survived to the root. This is
	// the set the plugin filter consults. github names derive without a
	// repoIndex (the repo name), which is all the kept entries need.
	rootMarketplaceNames := make(map[string]bool)
	var excludedMarketplaces []string
	for _, mc := range marketplaces {
		if strings.HasPrefix(mc.Source, repoRefPrefix) {
			excludedMarketplaces = append(excludedMarketplaces, mc.Source)
			continue
		}
		keptMarketplaces = append(keptMarketplaces, mc)
		if name, err := marketplaceRegistrationName(mc.Source, nil); err == nil && name != "" {
			rootMarketplaceNames[name] = true
		}
	}

	var excludedPlugins []string
	for _, p := range plugins {
		mkt := pluginMarketplace(p)
		if mkt == "" || rootMarketplaceNames[mkt] {
			keptPlugins = append(keptPlugins, p)
			continue
		}
		excludedPlugins = append(excludedPlugins, p)
	}

	if len(excludedMarketplaces) > 0 {
		reports = append(reports, fmt.Sprintf(
			"workspace root: omitting %d instance-local marketplace(s) with no root-resolvable path (%s); their plugins load only inside a provisioned instance",
			len(excludedMarketplaces), strings.Join(excludedMarketplaces, ", "),
		))
	}
	if len(excludedPlugins) > 0 {
		reports = append(reports, fmt.Sprintf(
			"workspace root: omitting %d plugin(s) bound to an instance-local marketplace (%s); they load only inside a provisioned instance",
			len(excludedPlugins), strings.Join(excludedPlugins, ", "),
		))
	}
	return keptPlugins, keptMarketplaces, reports
}

// pluginMarketplace returns the marketplace registration name a plugin entry
// binds to: the substring after the last "@" in a "plugin@marketplace" string.
// Returns "" when the entry carries no "@" qualifier (or a trailing "@" with no
// name), in which case the plugin is not tied to a specific marketplace.
func pluginMarketplace(plugin string) string {
	at := strings.LastIndexByte(plugin, '@')
	if at < 0 || at == len(plugin)-1 {
		return ""
	}
	return plugin[at+1:]
}

// writeRootClaudeMD writes <workspaceRoot>/CLAUDE.md with workspace-context
// content at root altitude. A session launched at the workspace root loads this
// file at startup; without it the coordinator and any root session start with
// no workspace orientation.
//
// At init time the workspace has no cloned repos to enumerate, so this does not
// reuse generateWorkspaceContext (which classifies discovered repos). It writes
// a minimal workspace-root CLAUDE.md describing the workspace and the
// ephemeral-session model instead.
func writeRootClaudeMD(cfg *config.WorkspaceConfig, workspaceRoot string, ag agent.Agent) (string, error) {
	content := generateRootClaudeContent(cfg)
	claudePath := filepath.Join(workspaceRoot, ag.RootContextFileName())
	if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing workspace-root context file: %w", err)
	}
	return claudePath, nil
}

// generateRootClaudeContent produces the markdown for the workspace-root
// CLAUDE.md. It orients a session launched at the workspace root: the workspace
// is a multi-repo tree of instances, each a separate managed sandbox, and a
// dispatched background session is provisioned its own ephemeral instance.
func generateRootClaudeContent(cfg *config.WorkspaceConfig) string {
	name := ""
	if cfg != nil {
		name = cfg.Workspace.Name
	}
	header := "# Workspace Root"
	if name != "" {
		header = fmt.Sprintf("# Workspace: %s", name)
	}

	return header + `

You are at the root of a multi-repo workspace managed by niwa. This directory is
NOT a single git repository: it holds the workspace config (` + "`.niwa/workspace.toml`" + `)
and one or more instance subdirectories, each a separate managed sandbox of
cloned repos.

## Working from the root

- Each instance lives in its own subdirectory under this root.
- ` + "`niwa list`" + ` enumerates the instances; ` + "`niwa create`" + ` provisions a new one.
- Run niwa commands from the root to manage the workspace as a whole, or from
  inside an instance to operate on that instance.

## Dispatching work to an isolated agent

When you have been discussing what to build and are ready to hand the work off to
run on its own, invoke the ` + "`/dispatch`" + ` skill. It synthesizes the conversation
into a self-contained task brief and launches a background worker in its own fresh
niwa instance via ` + "`niwa dispatch`" + ` -- the worker boots rooted in that instance
(loading its full configuration) and appears in Agent View. The underlying command is
` + "`niwa dispatch \"<task>\" --name <slug> [--detach]`" + `; ` + "`/dispatch`" + ` is the
front door that writes the brief and runs it for you.

## Ephemeral sessions

This workspace can provision a dedicated ephemeral instance per dispatched
session. When ephemeral-session mode is enabled, a background session launched
at this root is given its own niwa instance on SessionStart. The instance is
kept while the session exists -- including after it finishes a task or goes idle
(the session stays resumable) -- and is reclaimed by ` + "`niwa reap`" + ` only once the
session is deleted. The SessionStart hook in
` + "`.claude/settings.json`" + ` drives provisioning; it invokes ` + "`niwa instance from-hook`" + `.
`
}
