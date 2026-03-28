package workspace

import (
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

func TestClassifyByVisibility(t *testing.T) {
	repos := []github.Repo{
		{Name: "tsuku", Visibility: "public"},
		{Name: "koto", Visibility: "public"},
		{Name: "vision", Visibility: "private"},
	}

	groups := map[string]config.GroupConfig{
		"public":  {Visibility: "public"},
		"private": {Visibility: "private"},
	}

	classified, warnings, err := Classify(repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(classified) != 3 {
		t.Fatalf("classified count = %d, want 3", len(classified))
	}

	groupCounts := map[string]int{}
	for _, cr := range classified {
		groupCounts[cr.Group]++
	}
	if groupCounts["public"] != 2 {
		t.Errorf("public count = %d, want 2", groupCounts["public"])
	}
	if groupCounts["private"] != 1 {
		t.Errorf("private count = %d, want 1", groupCounts["private"])
	}
}

func TestClassifyExplicitRepoList(t *testing.T) {
	repos := []github.Repo{
		{Name: "terraform-modules", Visibility: "private"},
		{Name: "api-service", Visibility: "public"},
	}

	groups := map[string]config.GroupConfig{
		"infra":    {Repos: []string{"terraform-modules"}},
		"services": {Repos: []string{"api-service"}},
	}

	classified, warnings, err := Classify(repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %v", warnings)
	}
	if len(classified) != 2 {
		t.Fatalf("classified count = %d, want 2", len(classified))
	}
}

func TestClassifyNoMatch(t *testing.T) {
	repos := []github.Repo{
		{Name: "orphan", Visibility: "internal"},
	}

	groups := map[string]config.GroupConfig{
		"public": {Visibility: "public"},
	}

	classified, warnings, err := Classify(repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(classified) != 0 {
		t.Errorf("classified count = %d, want 0", len(classified))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings count = %d, want 1", len(warnings))
	}
}

func TestClassifyMultiMatchError(t *testing.T) {
	repos := []github.Repo{
		{Name: "ambiguous", Visibility: "public"},
	}

	groups := map[string]config.GroupConfig{
		"public": {Visibility: "public"},
		"also":   {Repos: []string{"ambiguous"}},
	}

	_, _, err := Classify(repos, groups)
	if err == nil {
		t.Fatal("expected error for multi-match, got nil")
	}
}
