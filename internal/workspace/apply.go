package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault/resolve"
)

// Applier orchestrates the apply pipeline.
type Applier struct {
	GitHubClient    github.Client
	Cloner          *Cloner
	Materializers   []Materializer
	NoPull          bool
	AllowDirty      bool
	GlobalConfigDir string // empty string means global config not registered

	// AllowMissingSecrets threads through to the vault resolver's
	// ResolveOptions.AllowMissing. When true, missing vault keys are
	// downgraded to empty MaybeSecret values with a stderr warning.
	// The CLI --allow-missing-secrets flag (Issue 10) populates this
	// field; default false preserves the strict-apply behavior.
	AllowMissingSecrets bool
}

// NewApplier creates an Applier with the given GitHub client.
func NewApplier(gh github.Client) *Applier {
	return &Applier{
		GitHubClient: gh,
		Cloner:       &Cloner{},
		Materializers: []Materializer{
			&HooksMaterializer{},
			&SettingsMaterializer{},
			&EnvMaterializer{},
			&FilesMaterializer{},
		},
	}
}

// DefaultMaxRepos is the threshold for auto-discovered repos per source.
// When an org returns more repos than this limit and the source has no
// explicit repos list, discovery fails with a clear error.
const DefaultMaxRepos = 10

// GlobalConfigOverrideFile is the filename for global config overrides in
// the global config repo.
const GlobalConfigOverrideFile = "niwa.toml"

// pipelineOpts configures shared pipeline behavior for Create vs Apply.
type pipelineOpts struct {
	existingState *InstanceState
	skipGlobal    bool
}

// pipelineResult holds the outputs of the shared pipeline.
type pipelineResult struct {
	classified   []ClassifiedRepo
	repoStates   map[string]RepoState
	managedFiles []ManagedFile
	warnings     []string
}

// Create creates a new workspace instance under workspaceRoot, runs the full
// pipeline (discover, classify, clone, install content), assigns an instance
// number, and writes fresh state. Returns the instance directory path.
func (a *Applier) Create(ctx context.Context, cfg *config.WorkspaceConfig, configDir, workspaceRoot string) (string, error) {
	now := time.Now()

	instanceRoot := filepath.Join(workspaceRoot, cfg.Workspace.Name)
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		return "", fmt.Errorf("creating instance directory: %w", err)
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState: nil,
	})
	if err != nil {
		return "", err
	}

	for _, w := range result.warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	instanceNumber, err := NextInstanceNumber(workspaceRoot)
	if err != nil {
		return "", fmt.Errorf("determining instance number: %w", err)
	}

	configName := cfg.Workspace.Name
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   cfg.Workspace.Name,
		InstanceNumber: instanceNumber,
		Root:           instanceRoot,
		Created:        now,
		LastApplied:    now,
		ManagedFiles:   result.managedFiles,
		Repos:          result.repoStates,
	}

	if err := SaveState(instanceRoot, state); err != nil {
		return "", fmt.Errorf("saving instance state: %w", err)
	}

	return instanceRoot, nil
}

// Apply runs the full apply pipeline on an existing instance: discover repos,
// classify, clone, install content, clean up removed repos, and update state.
func (a *Applier) Apply(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
	now := time.Now()

	// Load existing state (required for Apply).
	existingState, err := LoadState(instanceRoot)
	if err != nil {
		return fmt.Errorf("loading existing state: %w", err)
	}

	// Check drift on existing managed files before overwriting.
	for _, mf := range existingState.ManagedFiles {
		drift, err := CheckDrift(mf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not check drift for %s: %v\n", mf.Path, err)
			continue
		}
		if drift.Drifted() && !drift.FileRemoved {
			fmt.Fprintf(os.Stderr, "warning: managed file %s has been modified outside niwa\n", mf.Path)
		}
	}

	result, err := a.runPipeline(ctx, cfg, configDir, instanceRoot, now, &pipelineOpts{
		existingState: existingState,
		skipGlobal:    existingState.SkipGlobal,
	})
	if err != nil {
		return err
	}

	for _, w := range result.warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// Clean up managed files from previous state that are no longer produced.
	a.cleanRemovedFiles(existingState, result)

	// Clean up empty group directories for repos that were removed.
	a.cleanRemovedGroupDirs(existingState, result, instanceRoot)

	configName := cfg.Workspace.Name
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   cfg.Workspace.Name,
		InstanceNumber: existingState.InstanceNumber,
		Root:           instanceRoot,
		Created:        existingState.Created,
		LastApplied:    now,
		ManagedFiles:   result.managedFiles,
		Repos:          result.repoStates,
	}

	if err := SaveState(instanceRoot, state); err != nil {
		return fmt.Errorf("saving instance state: %w", err)
	}

	return nil
}

// runPipeline executes the shared pipeline steps: discover repos, classify,
// clone, and install content. It returns the pipeline results without writing
// state.
func (a *Applier) runPipeline(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string, now time.Time, opts *pipelineOpts) (*pipelineResult, error) {
	var writtenFiles []string
	var allWarnings []string

	// Step 1: Discover repos from all sources.
	allRepos, err := a.discoverAllRepos(ctx, cfg.Sources)
	if err != nil {
		return nil, err
	}

	// Step 2: Classify repos into groups.
	classified, classifyWarnings, err := Classify(allRepos, cfg.Groups)
	if err != nil {
		return nil, fmt.Errorf("classifying repos: %w", err)
	}
	allWarnings = append(allWarnings, classifyWarnings...)

	// Step 2.1: Inject explicit repos (url + group set, not from sources).
	classified, explicitWarnings, err := InjectExplicitRepos(classified, cfg.Repos, cfg.Groups)
	if err != nil {
		return nil, err
	}
	allWarnings = append(allWarnings, explicitWarnings...)

	// Step 2.5: Warn about unknown repo names in [repos] overrides.
	discoveredNames := make([]string, len(allRepos))
	for i, r := range allRepos {
		discoveredNames[i] = r.Name
	}
	known := KnownRepoNames(cfg, discoveredNames)
	allWarnings = append(allWarnings, WarnUnknownRepos(cfg, known)...)

	// Step 2a: Sync global config repo if registered and not skipped.
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		if syncErr := SyncConfigDir(a.GlobalConfigDir, a.AllowDirty); syncErr != nil {
			return nil, fmt.Errorf("syncing global config: %w", syncErr)
		}
	}

	// Steps 3a–3c: Parse the global config override, then run the
	// vault resolver stage against BOTH team (ws) and personal
	// (overlay) layers before the merge. The resolver must run
	// before merge so that each layer's provider bundle is built
	// from its own [vault] block only (file-local scoping, D-6 in
	// the vault-integration design). Resolving post-merge would
	// flatten provider declarations and make R12 collision
	// detection impossible.
	//
	// cfg remains the original for per-instance reads; effectiveCfg
	// carries the merge.
	effectiveCfg := cfg

	// Build a redactor for this apply invocation and attach it to
	// ctx so every secret.Errorf / Wrap call downstream scrubs
	// resolved values automatically.
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	var globalOverride *config.GlobalConfigOverride
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		overridePath := filepath.Join(a.GlobalConfigDir, GlobalConfigOverrideFile)
		data, readErr := os.ReadFile(overridePath)
		if readErr == nil {
			parsed, parseErr := config.ParseGlobalConfigOverride(data)
			if parseErr != nil {
				return nil, fmt.Errorf("parsing global config override: %w", parseErr)
			}
			globalOverride = parsed
		} else if !os.IsNotExist(readErr) {
			return nil, fmt.Errorf("reading global config override: %w", readErr)
		}
	}

	// Build provider bundles from each layer independently. Bundle
	// lifetime is scoped to this apply: defer CloseAll so providers
	// shut down cleanly even on error paths (R29 no-disk-cache).
	teamBundle, err := resolve.BuildBundle(ctx, nil, cfg.Vault, "workspace config")
	if err != nil {
		return nil, err
	}
	defer teamBundle.CloseAll()

	// BuildBundle is a safe no-op for a nil config.VaultRegistry, so
	// we always call it regardless of whether globalOverride is
	// present; that keeps the defer and the R12 check uniform.
	var overlayRegistry *config.VaultRegistry
	if globalOverride != nil {
		overlayRegistry = globalOverride.Global.Vault
	}
	personalBundle, err := resolve.BuildBundle(ctx, nil, overlayRegistry, "global overlay")
	if err != nil {
		return nil, err
	}
	defer personalBundle.CloseAll()

	// R12 enforcement: personal overlay MUST NOT redeclare any
	// provider name present in the team bundle. This is checked
	// here (not in the resolver) because only this call site has
	// both bundles in scope.
	if err := resolve.CheckProviderNameCollision(teamBundle, personalBundle); err != nil {
		return nil, err
	}

	// Resolve the team workspace config.
	resolvedCfg, err := resolve.ResolveWorkspace(ctx, cfg, resolve.ResolveOptions{
		AllowMissing: a.AllowMissingSecrets,
		TeamBundle:   teamBundle,
	})
	if err != nil {
		return nil, err
	}
	effectiveCfg = resolvedCfg

	// Resolve the personal overlay, then merge it into the team
	// workspace. The merge happens AFTER resolution so that R8
	// team_only enforcement in MergeGlobalOverride sees the
	// overlay's resolved MaybeSecret values, not pre-resolve URIs.
	if globalOverride != nil {
		resolvedOverride, err := resolve.ResolveGlobalOverride(ctx, globalOverride, resolve.ResolveOptions{
			AllowMissing:   a.AllowMissingSecrets,
			PersonalBundle: personalBundle,
		})
		if err != nil {
			return nil, err
		}
		flattened := ResolveGlobalOverride(resolvedOverride, cfg.Workspace.Name)
		merged, err := MergeGlobalOverride(resolvedCfg, flattened, a.GlobalConfigDir)
		if err != nil {
			return nil, err
		}
		effectiveCfg = merged
	}

	// Step 3: Create group directories and clone repos.
	repoStates := map[string]RepoState{}
	for _, cr := range classified {
		groupDir := filepath.Join(instanceRoot, cr.Group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return nil, fmt.Errorf("creating group directory %s: %w", groupDir, err)
		}

		cloneURL := RepoCloneURL(effectiveCfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL)
		branch := RepoCloneBranch(effectiveCfg, cr.Repo.Name)

		targetDir := filepath.Join(groupDir, cr.Repo.Name)
		cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
		if err != nil {
			return nil, fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
		}
		if cloned {
			fmt.Fprintf(os.Stderr, "cloned %s into %s\n", cr.Repo.Name, targetDir)
		} else if !a.NoPull {
			defaultBranch := DefaultBranch(effectiveCfg, cr.Repo.Name)
			result, syncErr := SyncRepo(ctx, targetDir, defaultBranch)
			switch result.Action {
			case "pulled":
				fmt.Fprintf(os.Stderr, "pulled %s (%d commits)\n", cr.Repo.Name, result.Commits)
			case "up-to-date":
				fmt.Fprintf(os.Stderr, "skipped %s (up to date)\n", cr.Repo.Name)
			case "fetch-failed":
				fmt.Fprintf(os.Stderr, "warning: could not fetch %s: %s\n", cr.Repo.Name, result.Reason)
			case "skipped":
				fmt.Fprintf(os.Stderr, "skipped %s (%s)\n", cr.Repo.Name, result.Reason)
			}
			if syncErr != nil {
				fmt.Fprintf(os.Stderr, "warning: sync failed for %s: %v\n", cr.Repo.Name, syncErr)
			}
		} else {
			fmt.Fprintf(os.Stderr, "skipped %s (already exists)\n", cr.Repo.Name)
		}

		repoStates[cr.Repo.Name] = RepoState{
			URL:    cloneURL,
			Cloned: cloned || repoAlreadyCloned(targetDir),
		}
	}

	// Step 4: Install workspace-level CLAUDE.md.
	wsFiles, err := InstallWorkspaceContent(effectiveCfg, configDir, instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("installing workspace content: %w", err)
	}
	writtenFiles = append(writtenFiles, wsFiles...)

	// Build repo name -> on-disk path index (used by marketplace resolution
	// and materializers).
	repoIndex := make(map[string]string, len(classified))
	for _, cr := range classified {
		repoIndex[cr.Repo.Name] = filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
	}

	// Step 4.5: Install workspace-root context and settings.
	// Context file uses @import for level-scoped visibility.
	// Settings uses settings.json (not .local) since instance root is non-git.
	ctxFiles, err := InstallWorkspaceContext(effectiveCfg, classified, instanceRoot)
	if err != nil {
		return nil, fmt.Errorf("installing workspace context: %w", err)
	}
	writtenFiles = append(writtenFiles, ctxFiles...)

	rootSettingsFiles, err := InstallWorkspaceRootSettings(effectiveCfg, configDir, instanceRoot, repoIndex)
	if err != nil {
		return nil, fmt.Errorf("installing workspace root settings: %w", err)
	}
	writtenFiles = append(writtenFiles, rootSettingsFiles...)

	// Step 5: Install group-level CLAUDE.md files.
	installedGroups := map[string]bool{}
	for _, cr := range classified {
		if installedGroups[cr.Group] {
			continue
		}
		installedGroups[cr.Group] = true

		groupFiles, err := InstallGroupContent(effectiveCfg, configDir, instanceRoot, cr.Group)
		if err != nil {
			return nil, fmt.Errorf("installing group content for %q: %w", cr.Group, err)
		}
		writtenFiles = append(writtenFiles, groupFiles...)
	}

	// Step 5c: Install global CLAUDE.md content if global config is active.
	if a.GlobalConfigDir != "" && !opts.skipGlobal {
		globalFiles, err := InstallGlobalClaudeContent(a.GlobalConfigDir, instanceRoot)
		if err != nil {
			return nil, fmt.Errorf("installing global claude content: %w", err)
		}
		writtenFiles = append(writtenFiles, globalFiles...)
	}

	// Step 6: Install repo-level CLAUDE.local.md files (and subdirectories).
	// Skip repos with claude = false.
	for _, cr := range classified {
		if !ClaudeEnabled(effectiveCfg, cr.Repo.Name) {
			fmt.Fprintf(os.Stderr, "skipped content for %s (claude = false)\n", cr.Repo.Name)
			continue
		}

		result, err := InstallRepoContent(effectiveCfg, configDir, instanceRoot, cr.Group, cr.Repo.Name)
		if err != nil {
			return nil, fmt.Errorf("installing repo content for %q: %w", cr.Repo.Name, err)
		}
		for _, w := range result.Warnings {
			allWarnings = append(allWarnings, w.String())
		}
		writtenFiles = append(writtenFiles, result.WrittenFiles...)
	}

	// Step 6.5: Run materializers (hooks, settings, env) for each repo.
	discoveredHooks, _ := DiscoverHooks(configDir)
	wsEnvFile, repoEnvFiles, _ := DiscoverEnvFiles(configDir)

	// Convert discovered env paths to relative (the env materializer joins
	// file paths with configDir, so they must be relative).
	relWsEnv := wsEnvFile
	if relWsEnv != "" {
		if r, err := filepath.Rel(configDir, relWsEnv); err == nil {
			relWsEnv = r
		}
	}

	discoveredEnv := &DiscoveredEnv{
		WorkspaceFile: relWsEnv,
		RepoFiles:     repoEnvFiles,
	}

	for _, cr := range classified {
		effective := MergeOverrides(effectiveCfg, cr.Repo.Name)

		// Merge discovered hooks as base, explicit config wins per event.
		if len(discoveredHooks) > 0 {
			merged := make(config.HooksConfig, len(discoveredHooks)+len(effective.Claude.Hooks))
			// Start with discovered hooks (converted to relative paths).
			for event, entries := range discoveredHooks {
				var relEntries []config.HookEntry
				for _, entry := range entries {
					relScripts := make([]string, 0, len(entry.Scripts))
					for _, absPath := range entry.Scripts {
						rel, err := filepath.Rel(configDir, absPath)
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
			// Explicit config overrides per event.
			for event, entries := range effective.Claude.Hooks {
				merged[event] = entries
			}
			effective.Claude.Hooks = merged
		}

		repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
		mctx := &MaterializeContext{
			Config:        effectiveCfg,
			Effective:     effective,
			RepoName:      cr.Repo.Name,
			RepoDir:       repoDir,
			ConfigDir:     configDir,
			DiscoveredEnv: discoveredEnv,
			RepoIndex:     repoIndex,
		}

		claudeOn := ClaudeEnabled(effectiveCfg, cr.Repo.Name)
		for _, m := range a.Materializers {
			// Skip hooks and settings materializers when claude is disabled.
			if !claudeOn && (m.Name() == "hooks" || m.Name() == "settings") {
				continue
			}

			files, err := m.Materialize(mctx)
			if err != nil {
				return nil, fmt.Errorf("materializer %s for repo %s: %w", m.Name(), cr.Repo.Name, err)
			}
			writtenFiles = append(writtenFiles, files...)
		}
	}

	// Step 6.75: Run repo-provided setup scripts.
	for _, cr := range classified {
		setupDir := ResolveSetupDir(effectiveCfg, cr.Repo.Name)
		repoDir := filepath.Join(instanceRoot, cr.Group, cr.Repo.Name)
		result := RunSetupScripts(repoDir, setupDir)

		if result.Disabled || result.Skipped {
			continue
		}

		for _, sr := range result.Scripts {
			if sr.Error != nil {
				fmt.Fprintf(os.Stderr, "warning: setup script %s/%s failed for %s: %v\n",
					setupDir, sr.Name, cr.Repo.Name, sr.Error)
			}
		}
	}

	// Step 7: Build managed files with hashes.
	managedFiles := make([]ManagedFile, 0, len(writtenFiles))
	for _, path := range writtenFiles {
		hash, err := HashFile(path)
		if err != nil {
			return nil, fmt.Errorf("hashing managed file %s: %w", path, err)
		}
		managedFiles = append(managedFiles, ManagedFile{
			Path:      path,
			Hash:      hash,
			Generated: now,
		})
	}

	return &pipelineResult{
		classified:   classified,
		repoStates:   repoStates,
		managedFiles: managedFiles,
		warnings:     allWarnings,
	}, nil
}

// cleanRemovedFiles deletes managed files from the previous state that are
// no longer present in the current pipeline result.
func (a *Applier) cleanRemovedFiles(existingState *InstanceState, result *pipelineResult) {
	currentFiles := make(map[string]bool, len(result.managedFiles))
	for _, mf := range result.managedFiles {
		currentFiles[mf.Path] = true
	}

	for _, mf := range existingState.ManagedFiles {
		if !currentFiles[mf.Path] {
			if err := os.Remove(mf.Path); err != nil && !os.IsNotExist(err) {
				fmt.Fprintf(os.Stderr, "warning: could not remove managed file %s: %v\n", mf.Path, err)
			}
		}
	}
}

// cleanRemovedGroupDirs removes empty group directories for repos that
// existed in the previous state but are no longer present.
func (a *Applier) cleanRemovedGroupDirs(existingState *InstanceState, result *pipelineResult, instanceRoot string) {
	// Build set of current group names.
	currentGroups := make(map[string]bool)
	for _, cr := range result.classified {
		currentGroups[cr.Group] = true
	}

	// Check previous classified repos by looking at managed file paths
	// to infer group directories. We check if group dirs that held repos
	// in the previous state are now empty.
	entries, err := os.ReadDir(instanceRoot)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == StateDir {
			continue
		}
		if currentGroups[name] {
			continue
		}
		groupDir := filepath.Join(instanceRoot, name)
		// Only remove if empty.
		subEntries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		if len(subEntries) == 0 {
			os.Remove(groupDir)
		}
	}
}

// repoAlreadyCloned checks if a directory has a .git marker, indicating
// it was previously cloned.
func repoAlreadyCloned(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// discoverAllRepos collects repos from all sources, enforcing per-source
// thresholds and detecting cross-source duplicate repo names.
func (a *Applier) discoverAllRepos(ctx context.Context, sources []config.SourceConfig) ([]github.Repo, error) {
	var allRepos []github.Repo
	seen := map[string]string{} // repo name -> source org (for duplicate detection)

	for _, source := range sources {
		repos, err := a.discoverRepos(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("discovering repos for org %q: %w", source.Org, err)
		}

		for _, r := range repos {
			if prevOrg, exists := seen[r.Name]; exists {
				return nil, fmt.Errorf(
					"duplicate repo name %q found in orgs %q and %q; rename or use explicit repos lists to resolve",
					r.Name, prevOrg, source.Org,
				)
			}
			seen[r.Name] = source.Org
		}

		allRepos = append(allRepos, repos...)
	}

	return allRepos, nil
}

func (a *Applier) discoverRepos(ctx context.Context, source config.SourceConfig) ([]github.Repo, error) {
	// If the source specifies explicit repos, build the list directly
	// without calling the GitHub API.
	if len(source.Repos) > 0 {
		repos := make([]github.Repo, len(source.Repos))
		for i, name := range source.Repos {
			repos[i] = github.Repo{
				Name:     name,
				SSHURL:   fmt.Sprintf("git@github.com:%s/%s.git", source.Org, name),
				CloneURL: fmt.Sprintf("https://github.com/%s/%s.git", source.Org, name),
			}
		}
		return repos, nil
	}

	// Auto-discover via API.
	repos, err := a.GitHubClient.ListRepos(ctx, source.Org)
	if err != nil {
		return nil, err
	}

	maxRepos := source.MaxRepos
	if maxRepos == 0 {
		maxRepos = DefaultMaxRepos
	}

	if len(repos) > maxRepos {
		return nil, fmt.Errorf(
			"org %q has %d repos, which exceeds the max_repos threshold of %d; "+
				"set max_repos to a higher value in [[sources]] or provide an explicit repos list",
			source.Org, len(repos), maxRepos,
		)
	}

	return repos, nil
}
