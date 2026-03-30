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

// DiscoveredEnv holds auto-discovered env file paths at workspace and repo
// levels. These are used as fallbacks when no explicit [env].files are
// configured.
type DiscoveredEnv struct {
	WorkspaceFile string            // auto-discovered workspace.env path (empty if none)
	RepoFiles     map[string]string // repoName -> auto-discovered env file path
}

// MaterializeContext holds the state needed by materializers when installing
// configuration into a repository directory.
type MaterializeContext struct {
	Config         *config.WorkspaceConfig
	Effective      EffectiveConfig
	RepoName       string
	RepoDir        string
	ConfigDir      string
	InstalledHooks map[string][]string // event -> installed script paths, populated by hooks materializer
	DiscoveredEnv  *DiscoveredEnv      // auto-discovered env files, may be nil
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
	claudeEnv := ctx.Effective.Claude.Env

	hasEnv := len(claudeEnv.Promote) > 0 || len(claudeEnv.Vars) > 0

	if len(settings) == 0 && len(hooks) == 0 && !hasEnv {
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

	// Build env block from promoted keys + inline vars.
	if hasEnv {
		envResult := make(map[string]string)

		// Step 1: resolve promoted keys from the env pipeline.
		if len(claudeEnv.Promote) > 0 {
			resolvedEnv, err := ResolveEnvVars(ctx)
			if err != nil {
				return nil, fmt.Errorf("resolving env for promote: %w", err)
			}
			if resolvedEnv == nil {
				resolvedEnv = map[string]string{}
			}
			for _, key := range claudeEnv.Promote {
				val, found := resolvedEnv[key]
				if !found {
					return nil, fmt.Errorf("claude.env: promoted key %q not found in resolved env vars", key)
				}
				envResult[key] = val
			}
		}

		// Step 2: overlay inline vars (inline wins over promoted).
		for k, v := range claudeEnv.Vars {
			envResult[k] = v
		}

		if len(envResult) > 0 {
			envDoc := make(map[string]any, len(envResult))
			envKeys := make([]string, 0, len(envResult))
			for k := range envResult {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)
			for _, k := range envKeys {
				envDoc[k] = envResult[k]
			}
			doc["env"] = envDoc
		}
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

// EnvMaterializer generates a .local.env file in the repository directory from
// explicit env config, discovered env files, and inline variables.
type EnvMaterializer struct{}

// Name returns the materializer identifier.
func (e *EnvMaterializer) Name() string {
	return "env"
}

// Materialize reads env files and inline vars, merges them with discovered env
// files, and writes the result to {repoDir}/.local.env. Returns the list of
// written file paths, or nil if there is nothing to write.
// ResolveEnvVars merges env files, inline vars, and discovered files into a
// single key-value map. This is the canonical env resolution function used by
// both the EnvMaterializer (to write .local.env) and the SettingsMaterializer
// (to look up promoted keys).
func ResolveEnvVars(ctx *MaterializeContext) (map[string]string, error) {
	envCfg := ctx.Effective.Env
	discovered := ctx.DiscoveredEnv

	files := envCfg.Files
	if len(files) == 0 && discovered != nil && discovered.WorkspaceFile != "" {
		files = []string{discovered.WorkspaceFile}
	}

	hasVars := len(envCfg.Vars) > 0
	hasRepoFile := discovered != nil && discovered.RepoFiles != nil && discovered.RepoFiles[ctx.RepoName] != ""

	if len(files) == 0 && !hasVars && !hasRepoFile {
		return nil, nil
	}

	vars := make(map[string]string)

	for _, f := range files {
		src := filepath.Join(ctx.ConfigDir, f)
		if err := checkContainment(src, ctx.ConfigDir); err != nil {
			return nil, fmt.Errorf("env file %q: %w", f, err)
		}

		parsed, err := parseEnvFile(src)
		if err != nil {
			return nil, fmt.Errorf("reading env file %s: %w", f, err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
	}

	for k, v := range envCfg.Vars {
		vars[k] = v
	}

	if hasRepoFile {
		repoEnvPath := discovered.RepoFiles[ctx.RepoName]
		parsed, err := parseEnvFile(repoEnvPath)
		if err != nil {
			return nil, fmt.Errorf("reading discovered repo env file %s: %w", repoEnvPath, err)
		}
		for k, v := range parsed {
			vars[k] = v
		}
	}

	if len(vars) == 0 {
		return nil, nil
	}

	return vars, nil
}

// Materialize reads env files and inline vars, merges them with discovered env
// files, and writes the result to {repoDir}/.local.env. Returns the list of
// written file paths, or nil if there is nothing to write.
func (e *EnvMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	vars, err := ResolveEnvVars(ctx)
	if err != nil {
		return nil, err
	}
	if len(vars) == 0 {
		return nil, nil
	}

	// Build output with sorted keys for deterministic output.
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf strings.Builder
	buf.WriteString("# Generated by niwa - do not edit manually\n")
	for _, k := range keys {
		buf.WriteString(k)
		buf.WriteByte('=')
		buf.WriteString(vars[k])
		buf.WriteByte('\n')
	}

	target := filepath.Join(ctx.RepoDir, ".local.env")
	if err := os.WriteFile(target, []byte(buf.String()), 0o644); err != nil {
		return nil, fmt.Errorf("writing env file: %w", err)
	}

	return []string{target}, nil
}

// parseEnvFile reads a file and parses KEY=VALUE lines. Lines starting with #
// and blank lines are skipped.
func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	vars := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		vars[key] = value
	}
	return vars, nil
}

// localRename inserts ".local" before the file extension. Files without an
// extension get ".local" appended. This ensures distributed files match the
// *.local* gitignore pattern in target repos.
//
//	"design.md"    -> "design.local.md"
//	"config.json"  -> "config.local.json"
//	"Makefile"     -> "Makefile.local"
//	".eslintrc"    -> ".eslintrc.local"
func localRename(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" || ext == filename {
		// No extension or dotfile where the whole name is the "extension" (e.g., ".eslintrc").
		return filename + ".local"
	}
	base := strings.TrimSuffix(filename, ext)
	if base == "" {
		// Dotfile like ".eslintrc" where Ext returns the whole name.
		return filename + ".local"
	}
	return base + ".local" + ext
}

// FilesMaterializer copies arbitrary files from the config directory into a
// repository directory. It reads source-to-destination mappings from the
// effective config and applies .local renaming when the destination is a
// directory (ends with /).
type FilesMaterializer struct{}

// Name returns the materializer identifier.
func (f *FilesMaterializer) Name() string {
	return "files"
}

// Materialize copies files from configDir to repoDir based on effective file
// mappings. Returns the list of written file paths.
func (f *FilesMaterializer) Materialize(ctx *MaterializeContext) ([]string, error) {
	files := ctx.Effective.Files
	if len(files) == 0 {
		return nil, nil
	}

	var written []string

	// Sort source keys for deterministic output.
	sources := make([]string, 0, len(files))
	for src := range files {
		sources = append(sources, src)
	}
	sort.Strings(sources)

	for _, src := range sources {
		dest := files[src]
		if dest == "" {
			continue // removed by per-repo override
		}

		isDir := strings.HasSuffix(src, "/")

		if isDir {
			w, err := f.materializeDir(ctx, src, dest)
			if err != nil {
				return nil, err
			}
			written = append(written, w...)
		} else {
			w, err := f.materializeFile(ctx, src, dest)
			if err != nil {
				return nil, err
			}
			written = append(written, w...)
		}
	}

	return written, nil
}

// materializeFile copies a single file from the config directory to the repo.
func (f *FilesMaterializer) materializeFile(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcPath := filepath.Join(ctx.ConfigDir, src)
	if err := checkContainment(srcPath, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("file source %q: %w", src, err)
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return nil, fmt.Errorf("reading file %s: %w", src, err)
	}

	var targetPath string
	if strings.HasSuffix(dest, "/") {
		// Directory destination: auto-rename with .local
		targetDir := filepath.Join(ctx.RepoDir, dest)
		targetPath = filepath.Join(targetDir, localRename(filepath.Base(src)))
	} else {
		// Explicit filename: use as-is
		targetPath = filepath.Join(ctx.RepoDir, dest)
	}

	if err := checkContainment(targetPath, ctx.RepoDir); err != nil {
		return nil, fmt.Errorf("file destination %q: %w", dest, err)
	}

	targetDir := filepath.Dir(targetPath)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating directory %s: %w", targetDir, err)
	}

	if err := os.WriteFile(targetPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing file %s: %w", targetPath, err)
	}

	return []string{targetPath}, nil
}

// materializeDir walks a source directory and copies each file to the
// destination, preserving directory structure and applying .local renaming.
func (f *FilesMaterializer) materializeDir(ctx *MaterializeContext, src, dest string) ([]string, error) {
	srcDir := filepath.Join(ctx.ConfigDir, strings.TrimSuffix(src, "/"))
	if err := checkContainment(srcDir, ctx.ConfigDir); err != nil {
		return nil, fmt.Errorf("directory source %q: %w", src, err)
	}

	var written []string

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}

		// Apply .local renaming to the filename, preserving subdirectory structure.
		dir := filepath.Dir(rel)
		renamed := localRename(filepath.Base(rel))
		var targetPath string
		if strings.HasSuffix(dest, "/") {
			if dir == "." {
				targetPath = filepath.Join(ctx.RepoDir, dest, renamed)
			} else {
				targetPath = filepath.Join(ctx.RepoDir, dest, dir, renamed)
			}
		} else {
			if dir == "." {
				targetPath = filepath.Join(ctx.RepoDir, dest, filepath.Base(rel))
			} else {
				targetPath = filepath.Join(ctx.RepoDir, dest, dir, filepath.Base(rel))
			}
		}

		if err := checkContainment(targetPath, ctx.RepoDir); err != nil {
			return fmt.Errorf("file destination %q: %w", targetPath, err)
		}

		targetDir := filepath.Dir(targetPath)
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			return fmt.Errorf("creating directory %s: %w", targetDir, err)
		}

		if err := os.WriteFile(targetPath, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", targetPath, err)
		}

		written = append(written, targetPath)
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("walking directory %s: %w", src, err)
	}

	return written, nil
}
