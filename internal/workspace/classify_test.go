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
		"group-a": {Visibility: "public"},
		"group-b": {Visibility: "public"},
	}

	_, _, err := Classify(repos, groups)
	if err == nil {
		t.Fatal("expected error for multi-match, got nil")
	}
}

func TestClassifyByExplicitRepos(t *testing.T) {
	repos := []github.Repo{
		{Name: "tsuku", Visibility: "public"},
		{Name: "vision", Visibility: "private"},
		{Name: "koto", Visibility: "public"},
	}

	groups := map[string]config.GroupConfig{
		"core":    {Repos: []string{"tsuku", "koto"}},
		"private": {Repos: []string{"vision"}},
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
	if groupCounts["core"] != 2 {
		t.Errorf("core count = %d, want 2", groupCounts["core"])
	}
	if groupCounts["private"] != 1 {
		t.Errorf("private count = %d, want 1", groupCounts["private"])
	}
}

func TestClassifyMixedVisibilityAndRepos(t *testing.T) {
	repos := []github.Repo{
		{Name: "tsuku", Visibility: "public"},
		{Name: "vision", Visibility: "private"},
		{Name: "special", Visibility: "internal"},
	}

	// "special" has internal visibility (not matched by visibility filter)
	// but is explicitly listed in repos, so it should match.
	groups := map[string]config.GroupConfig{
		"main": {Visibility: "public", Repos: []string{"special"}},
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

	byName := map[string]string{}
	for _, cr := range classified {
		byName[cr.Repo.Name] = cr.Group
	}
	if byName["tsuku"] != "main" {
		t.Errorf("tsuku group = %q, want %q", byName["tsuku"], "main")
	}
	if byName["special"] != "main" {
		t.Errorf("special group = %q, want %q", byName["special"], "main")
	}
	if byName["vision"] != "private" {
		t.Errorf("vision group = %q, want %q", byName["vision"], "private")
	}
}

func TestClassifyMultiMatchWithRepos(t *testing.T) {
	repos := []github.Repo{
		{Name: "tsuku", Visibility: "public"},
	}

	// tsuku matches group-a by visibility and group-b by explicit repo list.
	groups := map[string]config.GroupConfig{
		"group-a": {Visibility: "public"},
		"group-b": {Repos: []string{"tsuku"}},
	}

	_, _, err := Classify(repos, groups)
	if err == nil {
		t.Fatal("expected error for multi-match, got nil")
	}
}

func TestClassifyNoMatchWithRepos(t *testing.T) {
	repos := []github.Repo{
		{Name: "unlisted", Visibility: "internal"},
	}

	groups := map[string]config.GroupConfig{
		"core": {Repos: []string{"tsuku", "koto"}},
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
