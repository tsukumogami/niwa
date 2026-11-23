package cli

import (
	"slices"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

func TestCloneURLListFromConfig(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Sources: []config.SourceConfig{{Org: "acme", Repos: []string{"api"}}, {Org: "beta"}},
		Repos:   map[string]config.RepoOverride{"web": {URL: "https://example.com/web.git"}},
	}
	got := cloneURLListFromConfig(cfg)
	want := []string{"git@github.com:acme/api.git", "https://example.com/web.git"}
	if !slices.Equal(got, want) {
		t.Fatalf("clone URLs %q, want %q", got, want)
	}
	if got := cloneURLListFromConfig(nil); got != nil {
		t.Fatalf("no configuration gave %q", got)
	}
}

func TestRemovalPolicyCarriesProofRemotes(t *testing.T) {
	cfg := &config.WorkspaceConfig{
		Repos: map[string]config.RepoOverride{"web": {URL: "https://example.com/web.git"}},
	}
	cfg.Session.Removal.ProofRemotes = []string{"https://github.com/dana/"}
	p := removalPolicy(cfg, nil)
	if !slices.Equal(p.ProofRemotes, []string{"https://github.com/dana/"}) {
		t.Errorf("proof remotes %q", p.ProofRemotes)
	}
	if !slices.Equal(p.CloneURLs, []string{"https://example.com/web.git"}) {
		t.Errorf("clone URLs %q", p.CloneURLs)
	}
	cfg.Session.Removal.ProofRemotes[0] = "changed"
	if p.ProofRemotes[0] != "https://github.com/dana/" {
		t.Error("the policy shares the configuration's slice")
	}
}
