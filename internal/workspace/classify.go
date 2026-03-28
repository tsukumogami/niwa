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
	// Check explicit repo list first.
	if slices.Contains(group.Repos, repo.Name) {
		return true
	}

	// Check visibility filter.
	if group.Visibility != "" && group.Visibility == repo.Visibility {
		return true
	}

	return false
}
