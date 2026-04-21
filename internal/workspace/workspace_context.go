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

const workspaceContextFile = "workspace-context.md"
const workspaceContextImport = "@workspace-context.md"

const overlayClaudeFile = "CLAUDE.overlay.md"
const overlayClaudeImport = "@CLAUDE.overlay.md"

const globalClaudeFile = "CLAUDE.global.md"
const globalClaudeImport = "@CLAUDE.global.md"

const workspaceRulesFile = ".claude/rules/workspace-imports.md"

// writeWorkspaceRulesFile creates (or overwrites) the rules file with a single
// @import line pointing to absPath.
func writeWorkspaceRulesFile(rulesPath, absPath string) error {
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(rulesPath, []byte("@"+absPath+"\n"), 0o644)
}

// appendToWorkspaceRulesFile appends an @import line to the rules file if not
// already present. Creates the file and its parent directory if needed.
func appendToWorkspaceRulesFile(rulesPath, absPath string) error {
	importLine := "@" + absPath
	existing, err := os.ReadFile(rulesPath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(existing), importLine) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
		return err
	}
	content := string(existing)
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += importLine + "\n"
	return os.WriteFile(rulesPath, []byte(content), 0o644)
}

// removeImportFromCLAUDE removes an old relative @import from CLAUDE.md
// (migration support). No-op if not present or file does not exist.
func removeImportFromCLAUDE(claudePath, importLine string) error {
	data, err := os.ReadFile(claudePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	content := string(data)
	if !strings.Contains(content, importLine) {
		return nil
	}
	// ensureImportInCLAUDE always added "line\n\n"; try that form first.
	content = strings.Replace(content, importLine+"\n\n", "", 1)
	content = strings.Replace(content, importLine+"\n", "", 1)
	return os.WriteFile(claudePath, []byte(content), 0o644)
}

// InstallWorkspaceContext generates a workspace context file at the instance
// root and writes an @import to .claude/rules/workspace-imports.md using an
// absolute path. This gives workspace-level visibility without triggering the
// "Allow external CLAUDE.md file imports?" dialog when starting Claude from a
// sub-repo directory.
func InstallWorkspaceContext(cfg *config.WorkspaceConfig, classified []ClassifiedRepo, instanceRoot string) ([]string, error) {
	content := generateWorkspaceContext(cfg, classified)

	contextPath := filepath.Join(instanceRoot, workspaceContextFile)
	if err := os.WriteFile(contextPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("writing workspace context: %w", err)
	}

	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		return nil, fmt.Errorf("writing workspace rules file: %w", err)
	}

	// Migrate: remove old relative import from CLAUDE.md if present.
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := removeImportFromCLAUDE(claudePath, workspaceContextImport); err != nil {
		return nil, fmt.Errorf("removing old workspace context import: %w", err)
	}

	return []string{contextPath, rulesPath}, nil
}

// InstallOverlayClaudeContent copies CLAUDE.overlay.md from the overlay clone
// into the instance root and appends an absolute @import to
// .claude/rules/workspace-imports.md. Returns the installed path when the file
// was present, or ("", nil) when it was absent.
func InstallOverlayClaudeContent(overlayDir, instanceRoot string) (string, error) {
	srcPath := filepath.Join(overlayDir, overlayClaudeFile)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("reading %s: %w", overlayClaudeFile, err)
	}

	destPath := filepath.Join(instanceRoot, overlayClaudeFile)
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", overlayClaudeFile, err)
	}

	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := appendToWorkspaceRulesFile(rulesPath, destPath); err != nil {
		return "", fmt.Errorf("adding overlay to workspace rules file: %w", err)
	}

	// Migrate: remove old relative import from CLAUDE.md if present.
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := removeImportFromCLAUDE(claudePath, overlayClaudeImport); err != nil {
		return "", fmt.Errorf("removing old overlay import: %w", err)
	}

	return destPath, nil
}

// InstallWorkspaceRootSettings generates .claude/settings.json at the instance
// root with hooks, permissions, env, plugins, and marketplaces. Uses
// settings.json (not .local) because the instance root is a non-git directory.
// Plugins and marketplaces are declared declaratively -- Claude Code's startup
// reconciler handles materialization.
func InstallWorkspaceRootSettings(cfg *config.WorkspaceConfig, configDir, instanceRoot string, repoIndex map[string]string) ([]string, error) {
	effective := MergeInstanceOverrides(cfg)

	// Merge discovered hooks.
	discoveredHooks, _ := DiscoverHooks(configDir)
	if len(discoveredHooks) > 0 {
		if effective.Claude.Hooks == nil {
			effective.Claude.Hooks = config.HooksConfig{}
		}
		for event, entries := range discoveredHooks {
			if _, exists := effective.Claude.Hooks[event]; !exists {
				relEntries := make([]config.HookEntry, 0, len(entries))
				for _, e := range entries {
					relScripts := make([]string, 0, len(e.Scripts))
					for _, s := range e.Scripts {
						if rel, err := filepath.Rel(configDir, s); err == nil {
							relScripts = append(relScripts, rel)
						}
					}
					relEntries = append(relEntries, config.HookEntry{Matcher: e.Matcher, Scripts: relScripts})
				}
				effective.Claude.Hooks[event] = relEntries
			}
		}
	}

	// Copy hook scripts to .claude/hooks/ (no .local rename for instance root).
	installedHooks := make(map[string][]InstalledHookEntry)
	if len(effective.Claude.Hooks) > 0 {
		for event, entries := range effective.Claude.Hooks {
			for _, entry := range entries {
				var installedPaths []string
				for _, script := range entry.Scripts {
					var src string
					if filepath.IsAbs(script) {
						src = script
					} else {
						src = filepath.Join(configDir, script)
					}
					targetName := filepath.Base(script)
					targetDir := filepath.Join(instanceRoot, ".claude", "hooks", event)
					target := filepath.Join(targetDir, targetName)

					data, err := os.ReadFile(src)
					if err != nil {
						continue
					}
					os.MkdirAll(targetDir, 0o755)
					os.WriteFile(target, data, 0o755)

					installedPaths = append(installedPaths, target)
				}
				installedHooks[event] = append(installedHooks[event], InstalledHookEntry{
					Matcher: entry.Matcher,
					Paths:   installedPaths,
				})
			}
		}
	}

	// Resolve env vars.
	envResult := make(map[string]string)
	if len(cfg.Claude.Env.Promote) > 0 || len(cfg.Claude.Env.Vars.Values) > 0 {
		if len(cfg.Claude.Env.Promote) > 0 {
			resolved, _ := resolveEnvFromConfig(cfg, configDir)
			for _, key := range cfg.Claude.Env.Promote {
				if val, ok := resolved[key]; ok {
					envResult[key] = val
				}
			}
		}
		for k, v := range cfg.Claude.Env.Vars.Values {
			envResult[k] = v.String()
		}
	}

	includeGit := false
	doc, err := buildSettingsDoc(BuildSettingsConfig{
		Settings:               effective.Claude.Settings,
		InstalledHooks:         installedHooks,
		ResolvedEnvVars:        envResult,
		Plugins:                effective.Plugins,
		Marketplaces:           effective.Claude.Marketplaces,
		RepoIndex:              repoIndex,
		BaseDir:                instanceRoot,
		IncludeGitInstructions: &includeGit,
		UseAbsolutePaths:       true,
	})
	if err != nil {
		return nil, fmt.Errorf("building workspace root settings: %w", err)
	}

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling workspace root settings: %w", err)
	}
	data = append(data, '\n')

	claudeDir := filepath.Join(instanceRoot, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing workspace root settings: %w", err)
	}

	var written []string
	written = append(written, settingsPath)
	// Collect hook files.
	hooksDir := filepath.Join(claudeDir, "hooks")
	filepath.Walk(hooksDir, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			written = append(written, path)
		}
		return nil
	})

	return written, nil
}

// InstallGlobalClaudeContent copies CLAUDE.global.md from the global config
// directory into the instance root and appends an absolute @import to
// .claude/rules/workspace-imports.md.
// Returns nil, nil when CLAUDE.global.md does not exist in globalConfigDir.
func InstallGlobalClaudeContent(globalConfigDir, instanceRoot string) ([]string, error) {
	srcPath := filepath.Join(globalConfigDir, globalClaudeFile)
	data, err := os.ReadFile(srcPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading %s: %w", globalClaudeFile, err)
	}

	destPath := filepath.Join(instanceRoot, globalClaudeFile)
	if err := os.WriteFile(destPath, data, 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", globalClaudeFile, err)
	}

	rulesPath := filepath.Join(instanceRoot, workspaceRulesFile)
	if err := appendToWorkspaceRulesFile(rulesPath, destPath); err != nil {
		return nil, fmt.Errorf("adding global to workspace rules file: %w", err)
	}

	// Migrate: remove old relative import from CLAUDE.md if present.
	claudePath := filepath.Join(instanceRoot, "CLAUDE.md")
	if err := removeImportFromCLAUDE(claudePath, globalClaudeImport); err != nil {
		return nil, fmt.Errorf("removing old global import: %w", err)
	}

	return []string{destPath}, nil
}

// resolveEnvFromConfig resolves env vars from config files without a full
// MaterializeContext.
func resolveEnvFromConfig(cfg *config.WorkspaceConfig, configDir string) (map[string]string, error) {
	vars := make(map[string]string)
	for _, f := range cfg.Env.Files {
		parsed, err := parseEnvFile(filepath.Join(configDir, f))
		if err != nil {
			continue
		}
		for k, v := range parsed {
			vars[k] = v
		}
	}
	for k, v := range cfg.Env.Vars.Values {
		vars[k] = v.String()
	}
	for k, v := range cfg.Env.Secrets.Values {
		vars[k] = v.String()
	}
	return vars, nil
}

// mapMarketplaceSourceWithIndex converts a niwa marketplace source string to the
// Claude Code extraKnownMarketplaces format. Returns the marketplace name,
// the entry object, and an error. It accepts a repoIndex for resolving repo:
// references to absolute directory paths.
func mapMarketplaceSourceWithIndex(source string, repoIndex map[string]string) (string, map[string]any, error) {
	if strings.HasPrefix(source, repoRefPrefix) {
		// repo:tools/.claude-plugin/marketplace.json -> directory source
		resolved, err := ResolveMarketplaceSource(source, repoIndex)
		if err != nil {
			return "", nil, err
		}
		// The directory source type points to the directory containing
		// .claude-plugin/marketplace.json. Strip the filename and the
		// .claude-plugin directory to get the root.
		dir := filepath.Dir(filepath.Dir(resolved))
		// Use the repo name as the marketplace name.
		ref := strings.TrimPrefix(source, repoRefPrefix)
		slashIdx := strings.IndexByte(ref, '/')
		name := ref[:slashIdx]
		return name, map[string]any{
			"source": map[string]any{
				"source": "directory",
				"path":   dir,
			},
			"autoUpdate": true,
		}, nil
	}

	// GitHub ref: "org/repo" -> {source: {source: "github", repo: "org/repo"}}
	parts := strings.SplitN(source, "/", 3)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		name := parts[1] // use repo name as marketplace name
		return name, map[string]any{
			"source": map[string]any{
				"source": "github",
				"repo":   source,
			},
			"autoUpdate": true,
		}, nil
	}

	return "", nil, nil
}

// generateWorkspaceContext produces the markdown content for the workspace
// context file, auto-generated from the classified repos.
func generateWorkspaceContext(cfg *config.WorkspaceConfig, classified []ClassifiedRepo) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Workspace: %s\n\n", cfg.Workspace.Name)
	b.WriteString("You are at the root of a multi-repo workspace managed by niwa. This is NOT\n")
	b.WriteString("a single git repository -- each subdirectory under the group folders is a\n")
	b.WriteString("separate git repo.\n\n")

	// Group repos by group name.
	groups := make(map[string][]string)
	var groupOrder []string
	for _, cr := range classified {
		if _, seen := groups[cr.Group]; !seen {
			groupOrder = append(groupOrder, cr.Group)
		}
		groups[cr.Group] = append(groups[cr.Group], cr.Repo.Name)
	}
	sort.Strings(groupOrder)

	b.WriteString("## Repos\n\n")
	for _, group := range groupOrder {
		repos := groups[group]
		sort.Strings(repos)
		fmt.Fprintf(&b, "### %s/\n\n", group)
		for _, repo := range repos {
			fmt.Fprintf(&b, "- `%s/%s/`\n", group, repo)
		}
		b.WriteString("\n")
	}

	b.WriteString("## Working in this workspace\n\n")
	b.WriteString("- Each repo is a separate git repo at `{group}/{repo}/`\n")
	b.WriteString("- Navigate into a repo before running git commands\n")
	b.WriteString("- To search across repos, use tools from this directory\n")
	b.WriteString("- To make changes, navigate into the specific repo first\n")

	return b.String()
}
