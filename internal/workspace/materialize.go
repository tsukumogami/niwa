package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// MaterializeContext holds the state needed by materializers when installing
// configuration into a repository directory.
type MaterializeContext struct {
	Config         *config.WorkspaceConfig
	Effective      EffectiveConfig
	RepoName       string
	RepoDir        string
	ConfigDir      string
	InstalledHooks map[string][]string // event -> installed script paths, populated by hooks materializer
}

// Materializer is the interface for components that install workspace
// configuration artifacts into a repository directory.
type Materializer interface {
	Name() string
	Materialize(ctx *MaterializeContext) ([]string, error)
}

// HooksMaterializer installs hook scripts from the config directory into a
// repository's .claude/hooks/ directory structure. It reads the merged hooks
// from EffectiveConfig and copies each script file to the target location.
type HooksMaterializer struct{}

// Name returns the materializer identifier.
func (h *HooksMaterializer) Name() string {
	return "hooks"
}

// Materialize copies hook scripts into {repoDir}/.claude/hooks/{event}/ and
// sets them executable. It populates ctx.InstalledHooks with the mapping of
// event names to installed script paths and returns the list of all written
// file paths.
func (h *HooksMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	hooks := ctx.Effective.Claude.Hooks
	if len(hooks) == 0 {
		return nil, nil
	}

	installed := make(map[string][]string, len(hooks))
	var written []string

	for event, scripts := range hooks {
		for _, scriptPath := range scripts {
			src := filepath.Join(ctx.ConfigDir, scriptPath)

			if err := checkContainment(src, ctx.ConfigDir); err != nil {
				return nil, fmt.Errorf("hook script %q: %w", scriptPath, err)
			}

			targetDir := filepath.Join(ctx.RepoDir, ".claude", "hooks", event)
			target := filepath.Join(targetDir, filepath.Base(scriptPath))

			if err := os.MkdirAll(targetDir, 0o755); err != nil {
				return nil, fmt.Errorf("creating hooks directory %s: %w", targetDir, err)
			}

			data, err := os.ReadFile(src)
			if err != nil {
				return nil, fmt.Errorf("reading hook script %s: %w", src, err)
			}

			if err := os.WriteFile(target, data, 0o644); err != nil {
				return nil, fmt.Errorf("writing hook script %s: %w", target, err)
			}

			if err := os.Chmod(target, 0o755); err != nil {
				return nil, fmt.Errorf("setting executable permission on %s: %w", target, err)
			}

			installed[event] = append(installed[event], target)
			written = append(written, target)
		}
	}

	ctx.InstalledHooks = installed
	return written, nil
}

// permissionsMapping translates niwa permission values to Claude Code
// settings.local.json permission mode strings.
var permissionsMapping = map[string]string{
	"bypass": "bypassPermissions",
	"ask":    "askPermissions",
}

// hookEventMapping translates snake_case hook event names used in niwa config
// to PascalCase event names expected by Claude Code settings.
var hookEventMapping = map[string]string{
	"pre_tool_use":  "PreToolUse",
	"post_tool_use": "PostToolUse",
	"stop":          "Stop",
	"notification":  "Notification",
}

// snakeToPascal converts a snake_case string to PascalCase as a fallback when
// the event name is not in hookEventMapping.
func snakeToPascal(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// SettingsMaterializer generates the .claude/settings.local.json file from
// effective settings and installed hooks.
type SettingsMaterializer struct{}

// Name returns the materializer identifier.
func (s *SettingsMaterializer) Name() string {
	return "settings"
}

// Materialize builds and writes {repoDir}/.claude/settings.local.json from
// the effective settings and installed hooks. It returns the list of written
// file paths, or nil if there is nothing to write.
func (s *SettingsMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	settings := ctx.Effective.Claude.Settings
	hooks := ctx.InstalledHooks

	if len(settings) == 0 && len(hooks) == 0 {
		return nil, nil
	}

	doc := make(map[string]any)

	// Build permissions block from settings.
	if perm, ok := settings["permissions"]; ok {
		mapped, known := permissionsMapping[perm]
		if !known {
			return nil, fmt.Errorf("unknown permissions value %q", perm)
		}
		doc["permissions"] = map[string]any{
			"defaultMode": mapped,
		}
	}

	// Build hooks block from installed hooks.
	if len(hooks) > 0 {
		hooksDoc := make(map[string]any, len(hooks))

		// Sort event names for deterministic output.
		events := make([]string, 0, len(hooks))
		for event := range hooks {
			events = append(events, event)
		}
		sort.Strings(events)

		for _, event := range events {
			paths := hooks[event]
			pascalEvent, ok := hookEventMapping[event]
			if !ok {
				pascalEvent = snakeToPascal(event)
			}

			entries := make([]map[string]string, 0, len(paths))
			for _, absPath := range paths {
				rel, err := filepath.Rel(ctx.RepoDir, absPath)
				if err != nil {
					return nil, fmt.Errorf("computing relative path for hook %s: %w", absPath, err)
				}
				entries = append(entries, map[string]string{
					"type":    "command",
					"command": filepath.ToSlash(rel),
				})
			}
			hooksDoc[pascalEvent] = entries
		}
		doc["hooks"] = hooksDoc
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling settings: %w", err)
	}
	// Append trailing newline for clean file output.
	data = append(data, '\n')

	claudeDir := filepath.Join(ctx.RepoDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating .claude directory: %w", err)
	}

	target := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(target, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing settings file: %w", err)
	}

	return []string{target}, nil
}
