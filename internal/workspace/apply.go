package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
// install content.
func (a *Applier) Apply(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
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

	// Step 3: Create group directories and clone repos.
	for _, cr := range classified {
		groupDir := filepath.Join(instanceRoot, cr.Group)
		if err := os.MkdirAll(groupDir, 0o755); err != nil {
			return fmt.Errorf("creating group directory %s: %w", groupDir, err)
		}

		cloneURL := cr.Repo.SSHURL
		if cloneURL == "" {
			cloneURL = cr.Repo.CloneURL
		}

		targetDir := filepath.Join(groupDir, cr.Repo.Name)
		cloned, err := a.Cloner.Clone(ctx, cloneURL, targetDir)
		if err != nil {
			return fmt.Errorf("cloning repo %s: %w", cr.Repo.Name, err)
		}
		if cloned {
			fmt.Printf("cloned %s into %s\n", cr.Repo.Name, targetDir)
		} else {
			fmt.Printf("skipped %s (already exists)\n", cr.Repo.Name)
		}
	}

	// Step 4: Install workspace-level CLAUDE.md.
	if err := InstallWorkspaceContent(cfg, configDir, instanceRoot); err != nil {
		return fmt.Errorf("installing workspace content: %w", err)
	}

	// Step 5: Install group-level CLAUDE.md files.
	installedGroups := map[string]bool{}
	for _, cr := range classified {
		if installedGroups[cr.Group] {
			continue
		}
		installedGroups[cr.Group] = true

		if err := InstallGroupContent(cfg, configDir, instanceRoot, cr.Group); err != nil {
			return fmt.Errorf("installing group content for %q: %w", cr.Group, err)
		}
	}

	// Step 6: Install repo-level CLAUDE.local.md files (and subdirectories).
	for _, cr := range classified {
		contentWarnings, err := InstallRepoContent(cfg, configDir, instanceRoot, cr.Group, cr.Repo.Name)
		if err != nil {
			return fmt.Errorf("installing repo content for %q: %w", cr.Repo.Name, err)
		}
		for _, w := range contentWarnings {
			fmt.Fprintf(os.Stderr, "warning: %s\n", w)
		}
	}

	return nil
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
