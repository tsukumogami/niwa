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
		"main":    {Visibility: "public", Repos: []string{"special"}},
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

// --- InjectExplicitRepos tests ---

func TestInjectExplicitRepos(t *testing.T) {
	classified := []ClassifiedRepo{
		{Repo: github.Repo{Name: "existing"}, Group: "public"},
	}
	repos := map[string]config.RepoOverride{
		"external": {URL: "git@github.com:other-org/external.git", Group: "private"},
	}
	groups := map[string]config.GroupConfig{
		"public":  {Visibility: "public"},
		"private": {Visibility: "private"},
	}

	result, _, err := InjectExplicitRepos(classified, repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
	found := false
	for _, cr := range result {
		if cr.Repo.Name == "external" && cr.Group == "private" {
			found = true
		}
	}
	if !found {
		t.Error("expected external repo in private group")
	}
}

func TestInjectExplicitReposSkipDiscovered(t *testing.T) {
	classified := []ClassifiedRepo{
		{Repo: github.Repo{Name: "myrepo"}, Group: "public"},
	}
	repos := map[string]config.RepoOverride{
		"myrepo": {URL: "git@github.com:other/myrepo.git", Group: "public"},
	}
	groups := map[string]config.GroupConfig{"public": {Visibility: "public"}}

	result, _, err := InjectExplicitRepos(classified, repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1, got %d", len(result))
	}
}

func TestInjectExplicitReposInvalidGroup(t *testing.T) {
	repos := map[string]config.RepoOverride{
		"ext": {URL: "git@github.com:other/ext.git", Group: "nonexistent"},
	}
	groups := map[string]config.GroupConfig{"public": {Visibility: "public"}}

	_, _, err := InjectExplicitRepos(nil, repos, groups)
	if err == nil {
		t.Fatal("expected error for invalid group")
	}
}

func TestInjectExplicitReposIgnoresWithoutGroup(t *testing.T) {
	repos := map[string]config.RepoOverride{
		"override": {URL: "git@github.com:other/repo.git"},
	}
	groups := map[string]config.GroupConfig{"public": {Visibility: "public"}}

	result, _, err := InjectExplicitRepos(nil, repos, groups)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("expected 0, got %d", len(result))
	}
}
