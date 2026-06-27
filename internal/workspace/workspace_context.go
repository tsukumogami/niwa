package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// Marketplace track values. Track selects which version of a github
// marketplace to register against.
const (
	// trackRelease registers against the latest stable (non-prerelease)
	// release tag. It is the default for github sources.
	trackRelease = "release"
	// trackMain registers against the default branch (no pin).
	trackMain = "main"
)

// resolveLatestStableRelease is the release-resolution SEAM. Given a github
// "org/repo", it returns the highest non-prerelease semver tag and true, or
// ("", false) when the repo has no stable release (or cannot be reached).
//
// It is a package-level var so unit tests inject a fake and never hit the
// network. The default implementation shells out to git ls-remote.
//
// SPIKE FINDING (Decision 6): Claude Code does NOT honor a ref/tag/commit
// pin field on a github marketplace SOURCE object — the object niwa writes
// into known_marketplaces.json / extraKnownMarketplaces only accepts
// source/repo/sparsePaths, and Claude clones the default-branch HEAD. The
// `ref` field is honored only inside a marketplace.json catalog entry
// (git-subdir source), which niwa does not author. We still resolve the
// release and emit a best-effort "ref" on the github source: it is ignored
// by Claude today but is forward-compatible if Claude adds source-level
// pinning, and the resolution itself drives the report surfaced to users.
var resolveLatestStableRelease = defaultResolveLatestStableRelease

// defaultResolveLatestStableRelease lists the repo's tags via git ls-remote
// and returns the highest non-prerelease semver tag.
func defaultResolveLatestStableRelease(repo string) (string, bool) {
	url := "https://github.com/" + repo
	out, err := exec.Command("git", "ls-remote", "--tags", "--refs", url).Output()
	if err != nil {
		return "", false
	}
	var best string
	var bestParsed [3]int
	found := false
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		tag := strings.TrimPrefix(fields[1], "refs/tags/")
		parsed, ok := parseStableSemver(tag)
		if !ok {
			continue
		}
		if !found || compareSemver(parsed, bestParsed) > 0 {
			best = tag
			bestParsed = parsed
			found = true
		}
	}
	if !found {
		return "", false
	}
	return best, true
}

// parseStableSemver parses a "vX.Y.Z" (or "X.Y.Z") tag into its numeric
// components. It rejects prerelease tags (those carrying a "-" suffix such
// as v1.2.3-rc.1 or v1.2.3-dev) and any tag that is not a clean three-part
// numeric version, so only stable releases are considered.
func parseStableSemver(tag string) ([3]int, bool) {
	var v [3]int
	s := strings.TrimPrefix(tag, "v")
	// Reject prerelease and build-metadata suffixes.
	if strings.ContainsAny(s, "-+") {
		return v, false
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

// compareSemver returns -1, 0, or 1 comparing two parsed semver triples.
func compareSemver(a, b [3]int) int {
	for i := 0; i < 3; i++ {
		switch {
		case a[i] < b[i]:
			return -1
		case a[i] > b[i]:
			return 1
		}
	}
	return 0
}

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
	// installedHookPaths is the flat list of hook scripts this apply actually
	// installed. It — not a walk of the output directory — is what gets tracked
	// in ManagedFiles, so a hook script no longer declared by any config (for
	// example one synthesized by a since-removed feature) is left out of the
	// produced set and pruned by cleanRemovedFiles on the next apply.
	var installedHookPaths []string
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
					installedHookPaths = append(installedHookPaths, target)
				}
				installedHooks[event] = append(installedHooks[event], InstalledHookEntry{
					Matcher: entry.Matcher,
					Paths:   installedPaths,
				})
			}
		}
	}

	// Resolve env vars using the same pipeline as repo sessions so vault-resolved
	// secrets are revealed correctly (not redacted to "***" by .String()).
	wsEnvFile, _, _ := DiscoverEnvFiles(configDir)
	relWsEnv := wsEnvFile
	if relWsEnv != "" {
		if r, err := filepath.Rel(configDir, relWsEnv); err == nil {
			relWsEnv = r
		}
	}
	mctx := &MaterializeContext{
		Config:    cfg,
		Effective: effective,
		RepoDir:   instanceRoot,
		ConfigDir: configDir,
		DiscoveredEnv: &DiscoveredEnv{
			WorkspaceFile: relWsEnv,
		},
	}
	envResult, _, err := resolveClaudeEnvVars(mctx)
	if err != nil {
		return nil, fmt.Errorf("resolving env vars for workspace root: %w", err)
	}

	includeGit := false
	var reports []string
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
		Reports:                &reports,
	})
	if err != nil {
		return nil, fmt.Errorf("building workspace root settings: %w", err)
	}
	emitReports(nil, reports)

	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling workspace root settings: %w", err)
	}
	data = append(data, '\n')

	claudeDir := filepath.Join(instanceRoot, ".claude")
	os.MkdirAll(claudeDir, 0o755)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, data, secretFileMode); err != nil {
		return nil, fmt.Errorf("writing workspace root settings: %w", err)
	}

	var written []string
	written = append(written, settingsPath)
	// Track only the hook scripts this apply installed, not every file present
	// in the output directory. Walking .claude/hooks/ here would re-adopt
	// orphaned scripts left by removed features, marking them as produced and
	// shielding them from cleanRemovedFiles forever.
	written = append(written, installedHookPaths...)

	// Distribute [instance.files] verbatim (no .local) to the instance root.
	// Reuse the MaterializeContext already built above (RepoDir=instanceRoot,
	// ConfigDir=configDir). Appending to written joins these files to the
	// instance's ManagedFiles set, so drift detection and cleanRemovedFiles
	// apply: dropping an [instance.files] entry deletes the file on next apply.
	instanceFiles, err := materializeVerbatimFiles(mctx, effective.InstanceFiles)
	if err != nil {
		return nil, fmt.Errorf("materializing instance-root files: %w", err)
	}
	written = append(written, instanceFiles...)

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

// readMarketplaceManifestName reads the declared marketplace name from
// dir/.claude-plugin/marketplace.json. It returns (name, true) when the
// manifest can be read, parsed, and carries a non-empty "name". It returns
// ("", false) on any failure (missing/unreadable/malformed manifest or empty
// name) so callers can fall back to ref-derived keying without crashing.
func readMarketplaceManifestName(dir string) (string, bool) {
	manifestPath := filepath.Join(dir, ".claude-plugin", "marketplace.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return "", false
	}
	var manifest struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", false
	}
	if manifest.Name == "" {
		return "", false
	}
	return manifest.Name, true
}

// mapMarketplaceSourceWithIndex converts a niwa marketplace source string to the
// Claude Code extraKnownMarketplaces format. Returns the marketplace name,
// the entry object, an optional human-readable report (release-tracking
// notice, empty when there is nothing to report), and an error. It accepts a
// repoIndex for resolving repo: references to absolute directory paths,
// autoUpdate to emit the configured per-marketplace auto-update policy, and
// track to select the version a github source registers against.
//
// For local (repo:/directory) sources the registration key is read from the
// marketplace's declared name in .claude-plugin/marketplace.json, falling back
// to the repo-ref-derived name when the manifest cannot be read. Local sources
// ignore track entirely. For github sources the repo name is used as the key.
//
// Track interpretation for github sources:
//   - "" or "release": resolve the highest non-prerelease release tag and emit
//     it as a best-effort "ref" on the source (see resolveLatestStableRelease
//     for the SPIKE FINDING on why this is best-effort). When the repo has no
//     stable release, fall back to the default branch (no ref) and return a
//     report describing the fallback (R14, R16).
//   - "main": register against the default branch, no ref (R15).
//   - any other value: treated as an explicit ref and emitted verbatim (R17).
func mapMarketplaceSourceWithIndex(source string, repoIndex map[string]string, autoUpdate bool, track string) (string, map[string]any, string, error) {
	name, err := marketplaceRegistrationName(source, repoIndex)
	if err != nil {
		return "", nil, "", err
	}

	if strings.HasPrefix(source, repoRefPrefix) {
		// repo:tools/.claude-plugin/marketplace.json -> directory source.
		// Local sources ignore track (no remote version to resolve).
		resolved, err := ResolveMarketplaceSource(source, repoIndex)
		if err != nil {
			return "", nil, "", err
		}
		// The directory source type points to the directory containing
		// .claude-plugin/marketplace.json. Strip the filename and the
		// .claude-plugin directory to get the root.
		dir := filepath.Dir(filepath.Dir(resolved))
		return name, map[string]any{
			"source": map[string]any{
				"source": "directory",
				"path":   dir,
			},
			"autoUpdate": autoUpdate,
		}, "", nil
	}

	// GitHub ref: "org/repo" -> {source: {source: "github", repo: "org/repo"}}
	parts := strings.SplitN(source, "/", 3)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		src := map[string]any{
			"source": "github",
			"repo":   source,
		}
		report := applyGithubTrack(src, source, track)
		return name, map[string]any{
			"source":     src,
			"autoUpdate": autoUpdate,
		}, report, nil
	}

	return "", nil, "", nil
}

// marketplaceRegistrationName computes the registry key niwa uses for a
// marketplace source: the manifest-declared name for a local repo:/directory
// source (falling back to the repo-ref-derived name), or the repo name for a
// github source. It is the single source of truth for the name shared by
// mapMarketplaceSourceWithIndex (project-settings materialization) and the
// global known_marketplaces reconciliation. Returns "" for an unrecognized
// source.
func marketplaceRegistrationName(source string, repoIndex map[string]string) (string, error) {
	if strings.HasPrefix(source, repoRefPrefix) {
		resolved, err := ResolveMarketplaceSource(source, repoIndex)
		if err != nil {
			return "", err
		}
		dir := filepath.Dir(filepath.Dir(resolved))
		ref := strings.TrimPrefix(source, repoRefPrefix)
		name := ref[:strings.IndexByte(ref, '/')]
		if declared, ok := readMarketplaceManifestName(dir); ok {
			name = declared
		}
		return name, nil
	}

	parts := strings.SplitN(source, "/", 3)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[1], nil
	}
	return "", nil
}

// applyGithubTrack mutates a github source map to register against the
// configured track and returns a report string (empty when nothing needs
// reporting). repo is the "org/repo" reference used for release resolution.
func applyGithubTrack(src map[string]any, repo, track string) string {
	switch track {
	case "", trackRelease:
		tag, ok := resolveLatestStableRelease(repo)
		if !ok {
			// No stable release: fall back to the default branch and report it.
			return fmt.Sprintf(
				"marketplace %q has no stable release; tracking the default branch",
				repo,
			)
		}
		src["ref"] = tag
		return ""
	case trackMain:
		// Default branch: emit no ref.
		return ""
	default:
		// Explicit ref/tag/version: emit verbatim.
		src["ref"] = track
		return ""
	}
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
