package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/config"
)

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
// SessionStart/SessionEnd hook invokes. The full command is
// "<abs-niwa> " + this suffix; abs-niwa is resolved at materialize time via
// os.Executable(). It is the instance-level hook entry point (distinct from
// "worktree from-hook"); see internal/cli/instance_from_hook.go.
const instanceFromHookCommandSuffix = "instance from-hook"

// rootSessionHookTimeoutSeconds is the per-command timeout written on the
// workspace-root SessionStart/SessionEnd hook entries. It is generous (>= 120s)
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
}

// MaterializeWorkspaceRoot writes the workspace-root managed config:
//
//   - <workspaceRoot>/.claude/settings.json built via the shared
//     buildSettingsDoc, carrying the SessionStart and SessionEnd hook entries
//     (each piping stdin to "niwa instance from-hook" with a generous timeout),
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

	claudePath, err := writeRootClaudeMD(cfg, workspaceRoot)
	if err != nil {
		return nil, err
	}
	written = append(written, claudePath)

	return written, nil
}

// writeRootSettings builds and writes <workspaceRoot>/.claude/settings.json via
// the shared buildSettingsDoc. The permission posture is sourced from the
// effective [claude.settings] block (MergeInstanceOverrides) -- the same input
// the instance-root materializer feeds to buildSettingsDoc -- so the
// permissions.defaultMode value matches what an instance would get. The
// SessionStart/SessionEnd hook entries and the ephemeral-mode flag are layered
// on top via the SessionHooks injection.
func writeRootSettings(cfg *config.WorkspaceConfig, workspaceRoot, niwaPath string, ephemeral bool) (string, error) {
	effective := MergeInstanceOverrides(cfg)

	includeGit := false
	var reports []string
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		// Permission posture: sourced exactly as the instance root sources it,
		// from the effective [claude.settings] map. buildSettingsDoc maps the
		// "permissions" key to permissions.defaultMode.
		Settings:               effective.Claude.Settings,
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

// writeRootClaudeMD writes <workspaceRoot>/CLAUDE.md with workspace-context
// content at root altitude. A session launched at the workspace root loads this
// file at startup; without it the coordinator and any root session start with
// no workspace orientation.
//
// At init time the workspace has no cloned repos to enumerate, so this does not
// reuse generateWorkspaceContext (which classifies discovered repos). It writes
// a minimal workspace-root CLAUDE.md describing the workspace and the
// ephemeral-session model instead.
func writeRootClaudeMD(cfg *config.WorkspaceConfig, workspaceRoot string) (string, error) {
	content := generateRootClaudeContent(cfg)
	claudePath := filepath.Join(workspaceRoot, rootClaudeFile)
	if err := os.WriteFile(claudePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("writing workspace-root CLAUDE.md: %w", err)
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

## Ephemeral sessions

This workspace can provision a dedicated ephemeral instance per dispatched
session. When ephemeral-session mode is enabled, a background session launched
at this root is given its own niwa instance on SessionStart and that instance is
torn down on SessionEnd. The SessionStart/SessionEnd hooks in
` + "`.claude/settings.json`" + ` drive this; they invoke ` + "`niwa instance from-hook`" + `.
`
}
