package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// worktreeRulesFile is the per-worktree rules import file. A worktree, when
// launched as its own Claude Code project root, does not inherit the instance
// root's .claude/rules/ (rules load for the launched root only, not walked-up
// parents). So the worktree needs its own import pointing at the instance's
// workspace-context.md (and overlay/global where present).
const worktreeRulesFile = ".claude/rules/worktree-imports.md"

// worktreeContextHeading marks the generated purpose/branch section appended
// to the worktree's CLAUDE.local.md. It is a stable sentinel so the section
// can be replaced idempotently on re-apply rather than duplicated.
const worktreeContextHeading = "## Worktree Context (niwa worktree)"

// repoMaterializeInputs bundles the inputs the per-repo materializer loop needs.
// Both the instance apply pipeline (apply.go) and ApplyToWorktree construct one
// of these and call runRepoMaterializers, so the two paths share the exact same
// materializer invocation logic (no forked installer).
type repoMaterializeInputs struct {
	Cfg                   *config.WorkspaceConfig
	ConfigDir             string
	RepoName              string
	RepoDir               string
	DiscoveredHooks       config.HooksConfig
	DiscoveredEnv         *DiscoveredEnv
	RepoIndex             map[string]string
	SourceTuples          map[string][]SourceEntry
	AllowPlaintextSecrets bool
	Stderr                io.Writer
}

// runRepoMaterializers runs the given materializers for a single repo against
// in.RepoDir. It merges discovered hooks beneath explicit config (matching the
// apply pipeline), builds the MaterializeContext, and skips the hooks/settings
// materializers when claude is disabled for the repo. Returns the list of
// written files. This is the single shared materializer path used by both the
// instance apply pipeline and ApplyToWorktree.
func runRepoMaterializers(materializers []Materializer, in repoMaterializeInputs) ([]string, error) {
	effective := MergeOverrides(in.Cfg, in.RepoName)

	// Merge discovered hooks as base; explicit config entries run first per event.
	if len(in.DiscoveredHooks) > 0 {
		merged := make(config.HooksConfig, len(in.DiscoveredHooks)+len(effective.Claude.Hooks))
		// Start with discovered hooks (converted to relative paths).
		for event, entries := range in.DiscoveredHooks {
			var relEntries []config.HookEntry
			for _, entry := range entries {
				relScripts := make([]string, 0, len(entry.Scripts))
				for _, absPath := range entry.Scripts {
					rel, err := filepath.Rel(in.ConfigDir, absPath)
					if err != nil {
						return nil, fmt.Errorf("materializer hooks: computing relative path for %s: %w", absPath, err)
					}
					relScripts = append(relScripts, rel)
				}
				relEntries = append(relEntries, config.HookEntry{
					Matcher: entry.Matcher,
					Scripts: relScripts,
				})
			}
			merged[event] = relEntries
		}
		// Explicit config runs before discovered hooks for the same event
		// and must not silently discard user-authored discovered hooks.
		for event, entries := range effective.Claude.Hooks {
			if existing, ok := merged[event]; ok {
				merged[event] = append(entries, existing...)
			} else {
				merged[event] = entries
			}
		}
		effective.Claude.Hooks = merged
	}

	mctx := &MaterializeContext{
		Config:                in.Cfg,
		Effective:             effective,
		RepoName:              in.RepoName,
		RepoDir:               in.RepoDir,
		ConfigDir:             in.ConfigDir,
		DiscoveredEnv:         in.DiscoveredEnv,
		RepoIndex:             in.RepoIndex,
		SourceTuples:          in.SourceTuples,
		AllowPlaintextSecrets: in.AllowPlaintextSecrets,
		Stderr:                in.Stderr,
	}

	var written []string
	claudeOn := ClaudeEnabled(in.Cfg, in.RepoName)
	for _, m := range materializers {
		// Skip hooks and settings materializers when claude is disabled.
		if !claudeOn && (m.Name() == "hooks" || m.Name() == "settings") {
			continue
		}

		files, err := m.Materialize(mctx)
		if err != nil {
			return nil, fmt.Errorf("materializer %s for repo %s: %w", m.Name(), in.RepoName, err)
		}
		written = append(written, files...)
	}
	return written, nil
}

// FindRepoGroup resolves the group a repo belongs to by scanning the instance
// layout (<instanceRoot>/<group>/<repo>) two levels deep. The on-disk layout is
// the ground truth: niwa apply already cloned the repo into its group directory,
// regardless of how the group was determined (explicit override or group
// filter). Returns an error if the repo is not found under any group.
func FindRepoGroup(instanceRoot, repoName string) (string, error) {
	topEntries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance root %s: %w", instanceRoot, err)
	}
	for _, top := range topEntries {
		if !top.IsDir() || top.Name() == ".niwa" {
			continue
		}
		groupDir := filepath.Join(instanceRoot, top.Name())
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, sub := range subEntries {
			if sub.IsDir() && sub.Name() == repoName {
				return top.Name(), nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in workspace %s", repoName, instanceRoot)
}

// WorktreeApplyOptions carries the inputs ApplyToWorktree needs that are not
// derivable from the worktree path alone.
type WorktreeApplyOptions struct {
	// OverlayDir is the local clone path of the overlay repo when one is
	// active, used to append overlay content / resolve overlay-sourced repo
	// content. Empty when no overlay is active.
	OverlayDir string
	// GlobalConfigDir is the local global config clone, used to import
	// CLAUDE.global.md when present. Empty when no global config is active.
	GlobalConfigDir string
	// Materializers is the set of repo materializers to run against the
	// worktree. When nil, the default set is used (the same set the apply
	// pipeline wires).
	Materializers []Materializer
	// AllowPlaintextSecrets mirrors Applier.AllowPlaintextSecrets.
	AllowPlaintextSecrets bool
	// Stderr receives diagnostic warnings during materialization. When nil,
	// materializers fall back to os.Stderr.
	Stderr io.Writer
}

// ApplyToWorktree installs, into worktreePath, the same class of CLAUDE
// accessories a repo checkout receives from `niwa apply`, plus a worktree
// rules import (so the launched worktree sees workspace context) and a
// purpose/branch layer. It reuses the existing installers — InstallRepoContentTo
// and the shared runRepoMaterializers — rather than forking a parallel
// installer, so worktree and instance content cannot drift.
//
// It is idempotent: re-running overwrites the repo content, re-points the rules
// import (no duplicate @import lines), and replaces the worktree-context section
// rather than appending a second copy.
//
// instanceRoot is the workspace instance root; configDir is the snapshot/config
// directory whose content sources are resolved (the same configDir the apply
// pipeline uses). group is the repo's group; repo is the repo name; purpose and
// branch describe the worktree. purpose is treated strictly as content data —
// it is never interpolated into a filesystem path.
//
// Returns the list of files written.
func ApplyToWorktree(cfg *config.WorkspaceConfig, configDir, instanceRoot, worktreePath, group, repo, purpose, branch string, opts WorktreeApplyOptions) ([]string, error) {
	if !ClaudeEnabled(cfg, repo) {
		// Claude content is disabled for this repo; install only the
		// worktree-context layer so the worktree still records its purpose.
		return installWorktreeContextLayer(worktreePath, repo, purpose, branch)
	}

	var written []string

	// 1. Owning repo's content (CLAUDE.local.md + subdir content), targeted at
	//    the worktree root. Same function the instance apply path calls.
	result, err := InstallRepoContentTo(cfg, configDir, opts.OverlayDir, instanceRoot, worktreePath, group, repo)
	if err != nil {
		return nil, fmt.Errorf("installing repo content into worktree: %w", err)
	}
	written = append(written, result.WrittenFiles...)

	// 2. Repo materializers (settings, env, files, hooks) targeted at the
	//    worktree. Same shared loop the instance apply path uses.
	materializers := opts.Materializers
	if materializers == nil {
		materializers = defaultMaterializers(opts.Stderr)
	}
	discoveredHooks, _ := DiscoverHooks(configDir)
	wsEnvFile, repoEnvFiles, _ := DiscoverEnvFiles(configDir)
	relWsEnv := wsEnvFile
	if relWsEnv != "" {
		if r, err := filepath.Rel(configDir, relWsEnv); err == nil {
			relWsEnv = r
		}
	}
	repoIndex := map[string]string{repo: worktreePath}
	matFiles, err := runRepoMaterializers(materializers, repoMaterializeInputs{
		Cfg:             cfg,
		ConfigDir:       configDir,
		RepoName:        repo,
		RepoDir:         worktreePath,
		DiscoveredHooks: discoveredHooks,
		DiscoveredEnv: &DiscoveredEnv{
			WorkspaceFile: relWsEnv,
			RepoFiles:     repoEnvFiles,
		},
		RepoIndex:             repoIndex,
		SourceTuples:          map[string][]SourceEntry{},
		AllowPlaintextSecrets: opts.AllowPlaintextSecrets,
		Stderr:                opts.Stderr,
	})
	if err != nil {
		return nil, err
	}
	written = append(written, matFiles...)

	// 3. Worktree rules import: an absolute @import to the instance's
	//    workspace-context.md, plus overlay/global where present. Reuses the
	//    same write/append helpers the instance root uses.
	rulesFiles, err := installWorktreeRulesImport(instanceRoot, worktreePath, opts)
	if err != nil {
		return nil, err
	}
	written = append(written, rulesFiles...)

	// 4. Worktree-specific layer naming the purpose and branch.
	layerFiles, err := installWorktreeContextLayer(worktreePath, repo, purpose, branch)
	if err != nil {
		return nil, err
	}
	written = append(written, layerFiles...)

	return written, nil
}

// defaultMaterializers returns the same materializer set the apply pipeline
// wires (HooksMaterializer, SettingsMaterializer, EnvMaterializer,
// FilesMaterializer), so a worktree install matches a repo install.
func defaultMaterializers(stderr io.Writer) []Materializer {
	return []Materializer{
		&HooksMaterializer{},
		&SettingsMaterializer{},
		&EnvMaterializer{Stderr: stderr},
		&FilesMaterializer{Stderr: stderr},
	}
}

// installWorktreeRulesImport writes <worktree>/.claude/rules/worktree-imports.md
// with an absolute @import to the instance's workspace-context.md, then appends
// overlay/global imports when those files exist at the instance root. Uses the
// same writeWorkspaceRulesFile / appendToWorkspaceRulesFile helpers the instance
// root uses, so the worktree's import file has the identical shape.
func installWorktreeRulesImport(instanceRoot, worktreePath string, opts WorktreeApplyOptions) ([]string, error) {
	absInstance, err := filepath.Abs(instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving instance root: %w", err)
	}

	rulesPath := filepath.Join(worktreePath, worktreeRulesFile)

	contextPath := filepath.Join(absInstance, workspaceContextFile)
	if err := writeWorkspaceRulesFile(rulesPath, contextPath); err != nil {
		return nil, fmt.Errorf("writing worktree rules import: %w", err)
	}

	// Overlay/global imports, when those files exist at the instance root.
	overlayPath := filepath.Join(absInstance, overlayClaudeFile)
	if _, statErr := os.Stat(overlayPath); statErr == nil {
		if err := appendToWorkspaceRulesFile(rulesPath, overlayPath); err != nil {
			return nil, fmt.Errorf("adding overlay import to worktree rules: %w", err)
		}
	}
	globalPath := filepath.Join(absInstance, globalClaudeFile)
	if _, statErr := os.Stat(globalPath); statErr == nil {
		if err := appendToWorkspaceRulesFile(rulesPath, globalPath); err != nil {
			return nil, fmt.Errorf("adding global import to worktree rules: %w", err)
		}
	}

	return []string{rulesPath}, nil
}

// installWorktreeContextLayer writes the generated purpose/branch section to
// <worktree>/CLAUDE.local.md. The section is delimited by a stable heading so a
// re-apply replaces it in place rather than appending a duplicate (idempotent).
// purpose is interpolated only into file content, never a filesystem path.
//
// The target path is computed from worktreePath alone (CLAUDE.local.md at the
// worktree root) and verified to stay within the worktree via checkContainment,
// matching the containment discipline of the other content installers.
func installWorktreeContextLayer(worktreePath, repo, purpose, branch string) ([]string, error) {
	target := filepath.Join(worktreePath, "CLAUDE.local.md")
	if err := checkContainment(target, worktreePath); err != nil {
		return nil, fmt.Errorf("worktree context layer: %w", err)
	}

	section := fmt.Sprintf("%s\n\nThis is a niwa worktree of repo %q.\n\n- Purpose: %s\n- Branch: %s\n",
		worktreeContextHeading, repo, purpose, branch)

	existing, err := os.ReadFile(target)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading worktree CLAUDE.local.md: %w", err)
	}

	body := stripWorktreeContextSection(string(existing))
	if len(body) > 0 {
		// Separate prior content from the appended section with a blank line.
		for len(body) > 0 && (body[len(body)-1] == '\n') {
			body = body[:len(body)-1]
		}
		body += "\n\n"
	}
	body += section

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, fmt.Errorf("creating worktree dir: %w", err)
	}
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("writing worktree CLAUDE.local.md: %w", err)
	}
	return []string{target}, nil
}

// stripWorktreeContextSection removes a previously-appended worktree-context
// section (from worktreeContextHeading to end of file) so a re-apply replaces
// it rather than appending a duplicate. Content before the heading is preserved.
func stripWorktreeContextSection(content string) string {
	idx := strings.Index(content, worktreeContextHeading)
	if idx < 0 {
		return content
	}
	return content[:idx]
}
