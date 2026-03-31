// Package workspace orchestrates the apply pipeline: classification, cloning,
// and content installation.
package workspace

import (
	"fmt"
	"slices"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// ClassifiedRepo is a repo assigned to a group.
type ClassifiedRepo struct {
	Repo  github.Repo
	Group string
}

// Classify assigns each repo to a group based on group filters.
// Returns classified repos, a list of warnings for unmatched repos,
// and an error if any repo matches multiple groups.
func Classify(repos []github.Repo, groups map[string]config.GroupConfig) ([]ClassifiedRepo, []string, error) {
	var classified []ClassifiedRepo
	var warnings []string

	for _, repo := range repos {
		var matches []string

		for groupName, group := range groups {
			if matchesGroup(repo, group) {
				matches = append(matches, groupName)
			}
		}

		switch len(matches) {
		case 0:
			warnings = append(warnings, fmt.Sprintf("repo %q matches no group, skipping", repo.Name))
		case 1:
			classified = append(classified, ClassifiedRepo{
				Repo:  repo,
				Group: matches[0],
			})
		default:
			return nil, nil, fmt.Errorf("repo %q matches multiple groups: %v", repo.Name, matches)
		}
	}

	return classified, warnings, nil
}

func matchesGroup(repo github.Repo, group config.GroupConfig) bool {
	if group.Visibility != "" && group.Visibility == repo.Visibility {
		return true
	}

	if slices.Contains(group.Repos, repo.Name) {
		return true
	}

	return false
}

// InjectExplicitRepos adds repos from [repos] that have both url and group set
// but weren't discovered from any source. These are external repos explicitly
// declared in the config. They join the classified list and flow through the
// full pipeline.
func InjectExplicitRepos(
	classified []ClassifiedRepo,
	repos map[string]config.RepoOverride,
	groups map[string]config.GroupConfig,
) ([]ClassifiedRepo, []string, error) {
	// Build a set of already-classified repo names.
	existing := make(map[string]bool, len(classified))
	for _, cr := range classified {
		existing[cr.Repo.Name] = true
	}

	var warnings []string

	for name, override := range repos {
		// Only process entries with both url and group.
		if override.URL == "" || override.Group == "" {
			continue
		}

		// Skip if already discovered.
		if existing[name] {
			continue
		}

		// Validate group exists.
		if _, ok := groups[override.Group]; !ok {
			return nil, nil, fmt.Errorf("explicit repo %q: group %q is not defined in [groups]", name, override.Group)
		}

		// Infer visibility from the group config.
		groupCfg := groups[override.Group]
		visibility := groupCfg.Visibility
		if visibility == "" {
			visibility = "private"
		}

		classified = append(classified, ClassifiedRepo{
			Repo: github.Repo{
				Name:       name,
				CloneURL:   override.URL,
				SSHURL:     override.URL,
				Visibility: visibility,
				Private:    visibility == "private",
			},
			Group: override.Group,
		})
	}

	return classified, warnings, nil
}
