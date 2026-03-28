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

// Apply runs the full apply pipeline: discover repos, classify, clone, and
// install content.
func (a *Applier) Apply(ctx context.Context, cfg *config.WorkspaceConfig, configDir, instanceRoot string) error {
	// Step 1: Discover repos from all sources.
	var allRepos []github.Repo
	for _, source := range cfg.Sources {
		repos, err := a.discoverRepos(ctx, source)
		if err != nil {
			return fmt.Errorf("discovering repos for org %q: %w", source.Org, err)
		}
		allRepos = append(allRepos, repos...)
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

	return nil
}

func (a *Applier) discoverRepos(ctx context.Context, source config.SourceConfig) ([]github.Repo, error) {
	return a.GitHubClient.ListRepos(ctx, source.Org)
}
