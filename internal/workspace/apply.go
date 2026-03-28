package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// Applier orchestrates the apply pipeline.
type Applier struct {
	GitHubClient github.Client
	Cloner       *Cloner
}

// NewApplier creates an Applier with the given GitHub client.
func NewApplier(gh github.Client) *Applier {
	return &Applier{
		GitHubClient: gh,
		Cloner:       &Cloner{},
	}
}

// DefaultMaxRepos is the threshold for auto-discovered repos per source.
// When an org returns more repos than this limit and the source has no
// explicit repos list, discovery fails with a clear error.
const DefaultMaxRepos = 10

// Apply runs the full apply pipeline: discover repos, classify, clone, and
// install content. It writes .niwa/instance.json to track state.
func (a *Applier) Apply(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
	now := time.Now()
	var writtenFiles []string

	// Load existing state if present (for drift detection and preserving created time).
	existingState, _ := LoadState(instanceRoot)

	// Step 1: Discover repos from all sources.
	allRepos, err := a.discoverAllRepos(ctx, cfg.Sources)
	if err != nil {
		return err
	}

	// Step 2: Classify repos into groups.
	classified, warnings, err := Classify(allRepos, cfg.Groups)
	if err != nil {
		return fmt.Errorf("classifying repos: %w", err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// Step 2.5: Warn about unknown repo names in [repos] overrides.
	discoveredNames := make([]string, len(allRepos))
	for i, r := range allRepos {
		discoveredNames[i] = r.Name
	}
	known := KnownRepoNames(cfg, discoveredNames)
	for _, w := range WarnUnknownRepos(cfg, known) {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	// Step 3: Create group directories and clone repos.
	repoStates := map[string]RepoState{}
	for _, cr := range classified {
		groupDir := filepath.Join(instanceRoot, cr.Group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return fmt.Errorf("creating group directory %s: %w", groupDir, err)
		}

		cloneURL := RepoCloneURL(cfg, cr.Repo.Name, cr.Repo.SSHURL, cr.Repo.CloneURL)
		branch := RepoCloneBranch(cfg, cr.Repo.Name)

		targetDir := filepath.Join(groupDir, cr.Repo.Name)
		cloned, err := a.Cloner.CloneWithBranch(ctx, cloneURL, targetDir, branch)
		if err != nil {
			return fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
		}
		if cloned {
			fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
		} else {
			fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)
		}

		repoStates[cr.Repo.Name] = RepoState{
			URL:    cloneURL,
			Cloned: cloned || repoAlreadyCloned(targetDir),
		}
	}

	// Check drift on existing managed files before overwriting.
	if existingState != nil {
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
	}

	// Step 4: Install workspace-level CLAUDE.md.
	wsFiles, err := InstallWorkspaceContent(cfg, configDir, instanceRoot)
	if err != nil {
		return fmt.Errorf("installing workspace content: %w", err)
	}
	writtenFiles = append(writtenFiles, wsFiles...)

	// Step 5: Install group-level CLAUDE.md files.
	installedGroups := map[string]bool{}
	for _, cr := range classified {
		if installedGroups[cr.Group] {
			continue
		}
		installedGroups[cr.Group] = true

		groupFiles, err := InstallGroupContent(cfg, configDir, instanceRoot, cr.Group)
		if err != nil {
			return fmt.Errorf("installing group content for %q: %w", cr.Group, err)
		}
		writtenFiles = append(writtenFiles, groupFiles...)
	}

	// Step 6: Install repo-level CLAUDE.local.md files (and subdirectories).
	// Skip repos with claude = false.
	for _, cr := range classified {
		if !ClaudeEnabled(cfg, cr.Repo.Name) {
			fmt.Printf("skipped content for %s (claude = false)\n", cr.Repo.Name)
			continue
		}

		result, err := InstallRepoContent(cfg, configDir, instanceRoot, cr.Group, cr.Repo.Name)
		if err != nil {
			return fmt.Errorf("installing repo content for %q: %w", cr.Repo.Name, err)
		}
		for _, w := range result.Warnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
		writtenFiles = append(writtenFiles, result.WrittenFiles...)
	}

	// Step 7: Build managed files with hashes.
	managedFiles := make([]ManagedFile, 0, len(writtenFiles))
	for _, path := range writtenFiles {
		hash, err := HashFile(path)
		if err != nil {
			return fmt.Errorf("hashing managed file %s: %w", path, err)
		}
		managedFiles = append(managedFiles, ManagedFile{
			Path:      path,
			Hash:      hash,
			Generated: now,
		})
	}

	// Step 8: Write instance state.
	created := now
	instanceNumber := 1
	if existingState != nil {
		created = existingState.Created
		instanceNumber = existingState.InstanceNumber
	}

	configName := cfg.Workspace.Name
	state := &InstanceState{
		SchemaVersion:  SchemaVersion,
		ConfigName:     &configName,
		InstanceName:   cfg.Workspace.Name,
		InstanceNumber: instanceNumber,
		Root:           instanceRoot,
		Created:        created,
		LastApplied:    now,
		ManagedFiles:   managedFiles,
		Repos:          repoStates,
	}

	if err := SaveState(instanceRoot, state); err != nil {
		return fmt.Errorf("saving instance state: %w", err)
	}

	return nil
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
