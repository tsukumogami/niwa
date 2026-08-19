package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
)

// marketplaceManifest is the manifest a marketplace tree carries, declaring one
// plugin and where its tree sits inside the marketplace.
const marketplaceManifest = `{"name":"tools","plugins":[{"name":"shirabe","source":"./plugins/shirabe"}]}`

// noStableRelease pins the release-resolution seam so these tests never reach
// the network for a tag, and the fetch lands on the default branch.
func noStableRelease(t *testing.T) {
	t.Helper()
	orig := resolveLatestStableRelease
	resolveLatestStableRelease = func(string) (string, bool) { return "", false }
	t.Cleanup(func() { resolveLatestStableRelease = orig })
}

// homelessMachine points HOME at an empty directory for the duration of t.
//
// It is the assertion that matters most in this file. The delivery this code
// replaces resolved a github-sourced marketplace out of Claude Code's
// user-global plugin directory, so on a machine with no Claude Code
// installation it found nothing and reported a fix that could not be carried
// out. Every resolution test below runs with a home that holds no .claude at
// all: if any path reached for one, the test would fail rather than quietly
// succeed on the developer's own machine.
func homelessMachine(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// marketplaceTarball builds the gzipped tarball a github-sourced marketplace
// fetch returns: the manifest, and one plugin tree with a skill in it.
func marketplaceTarball(t *testing.T) []byte {
	t.Helper()
	return makeFakeTarball(t, map[string]string{
		"tools-abc123/.claude-plugin/marketplace.json":                  marketplaceManifest,
		"tools-abc123/plugins/shirabe/.claude-plugin/plugin.json":       `{"name":"shirabe"}`,
		"tools-abc123/plugins/shirabe/skills/review/SKILL.md":           "review body\n",
		"tools-abc123/plugins/shirabe/skills/review/references/note.md": "note\n",
	})
}

func TestResolvePluginTreesFetchesAGithubMarketplaceIntoTheInstance(t *testing.T) {
	home := homelessMachine(t)
	noStableRelease(t)

	instance := t.TempDir()
	fetcher := &fakeFetcher{tarball: marketplaceTarball(t)}

	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}},
		Fetcher:      fetcher,
	})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if len(trees) != 1 {
		t.Fatalf("resolved %d trees, want one", len(trees))
	}

	want := filepath.Join(instance, ".niwa", "marketplaces", "tools", "plugins", "shirabe")
	if trees[0].Root != want {
		t.Errorf("plugin root = %s, want %s", trees[0].Root, want)
	}
	if _, err := os.Stat(filepath.Join(want, "skills", "review", "SKILL.md")); err != nil {
		t.Errorf("fetched tree is missing its skill: %v", err)
	}
	// The content is niwa's, inside the instance -- not in the home directory
	// another tool owns.
	if strings.HasPrefix(trees[0].Root, home) {
		t.Errorf("plugin root %s sits under the developer's home directory", trees[0].Root)
	}
}

func TestResolvePluginTreesReusesContentAlreadyFetched(t *testing.T) {
	homelessMachine(t)
	noStableRelease(t)

	instance := t.TempDir()
	in := PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}},
		Fetcher:      &fakeFetcher{tarball: marketplaceTarball(t)},
	}
	if _, missing := ResolvePluginTrees(context.Background(), in); len(missing) != 0 {
		t.Fatalf("first resolve reported %v", missing)
	}

	// A second apply must not go back to the network. A fetcher that refuses
	// every call proves the cached content is what answers.
	in.Fetcher = &fakeFetcher{fetchErr: os.ErrPermission}
	trees, missing := ResolvePluginTrees(context.Background(), in)
	if len(missing) != 0 || len(trees) != 1 {
		t.Fatalf("second resolve: trees %v, missing %v; want the fetched content reused", trees, missing)
	}
}

func TestResolvePluginTreesResolvesARepoSourcedMarketplace(t *testing.T) {
	homelessMachine(t)

	instance := t.TempDir()
	repo := filepath.Join(instance, "private", "tools")
	writeMarketplaceTree(t, repo)

	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools"},
		Marketplaces: config.MarketplaceConfigs{{Source: "repo:tools/.claude-plugin/marketplace.json"}},
		RepoIndex:    map[string]string{"tools": repo},
	})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if len(trees) != 1 || trees[0].Root != filepath.Join(repo, "plugins", "shirabe") {
		t.Fatalf("trees = %v, want the plugin tree inside the workspace's own clone", trees)
	}
}

func TestResolvePluginTreesReportsWhatItCannotResolve(t *testing.T) {
	homelessMachine(t)
	noStableRelease(t)

	instance := t.TempDir()
	_, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools", "orphan"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}},
		Fetcher:      &fakeFetcher{fetchErr: os.ErrPermission},
	})
	if len(missing) != 2 {
		t.Fatalf("missing = %v, want a report for each unresolved plugin", missing)
	}
	for _, m := range missing {
		if m.Plugin == "" || m.Reason == "" {
			t.Errorf("report %+v names neither the plugin nor the reason", m)
		}
		if !strings.Contains(m.String(), m.Plugin) {
			t.Errorf("rendered report %q does not name the plugin", m.String())
		}
	}
}

// TestResolvePluginTreesDoesNoNetworkWorkWithoutAFetcher covers the worktree
// path, which re-delivers from what an instance apply already fetched.
func TestResolvePluginTreesDoesNoNetworkWorkWithoutAFetcher(t *testing.T) {
	homelessMachine(t)

	instance := t.TempDir()
	cached := filepath.Join(instance, ".niwa", "marketplaces", "tools")
	writeMarketplaceTree(t, cached)

	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}},
	})
	if len(missing) != 0 || len(trees) != 1 {
		t.Fatalf("trees = %v, missing = %v; want the already-fetched content", trees, missing)
	}
}

func TestInstallRepoSkillsDeliversTheTreesAndTheirExcludeCoverage(t *testing.T) {
	homelessMachine(t)

	instance := t.TempDir()
	marketplace := filepath.Join(instance, ".niwa", "marketplaces", "tools")
	writeMarketplaceTree(t, marketplace)
	repo := filepath.Join(instance, "public", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}},
	})
	if len(missing) != 0 {
		t.Fatalf("missing = %v", missing)
	}

	result, err := InstallRepoSkills(repo, trees, agentplan.For(agent.AgentCodex))
	if err != nil {
		t.Fatalf("InstallRepoSkills: %v", err)
	}
	if len(result.Delivered) != 1 {
		t.Fatalf("delivered %v, want one tree", result.Delivered)
	}
	// The whole plugin tree arrives, which is what makes the skill inside it
	// resolve as <plugin>:<skill> rather than as a loose directory.
	if _, err := os.Stat(filepath.Join(repo, ".codex", "skills", "shirabe", "skills", "review", "SKILL.md")); err != nil {
		t.Errorf("delivered tree is missing its skill: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".codex", "skills", "shirabe", ".claude-plugin", "plugin.json")); err != nil {
		t.Errorf("delivered tree is missing the manifest the namespace derives from: %v", err)
	}
	if len(result.Excludes) != 1 || result.Excludes[0] != ".codex/skills/shirabe" {
		t.Errorf("excludes = %v, want the delivered name", result.Excludes)
	}

	// An agent whose skills do not arrive as delivered trees gets nothing, and
	// nothing is left behind for it.
	claude, err := InstallRepoSkills(repo, trees, agentplan.For(agent.AgentClaude))
	if err != nil {
		t.Fatalf("InstallRepoSkills for the other agent: %v", err)
	}
	if len(claude.Delivered) != 0 {
		t.Errorf("delivered %v for an agent that takes no trees", claude.Delivered)
	}
}

func TestInstallRepoSkillsPrunesADeconfiguredPlugin(t *testing.T) {
	homelessMachine(t)

	instance := t.TempDir()
	marketplace := filepath.Join(instance, ".niwa", "marketplaces", "tools")
	writeMarketplaceTree(t, marketplace)
	repo := filepath.Join(instance, "public", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}

	trees := []agentplan.PluginTree{
		{Name: "shirabe", Root: filepath.Join(marketplace, "plugins", "shirabe")},
		{Name: "retired", Root: filepath.Join(marketplace, "plugins", "shirabe")},
	}
	if _, err := InstallRepoSkills(repo, trees, agentplan.For(agent.AgentCodex)); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	retired := filepath.Join(repo, ".codex", "skills", "retired")
	if _, err := os.Lstat(retired); err != nil {
		t.Fatalf("first delivery did not write %s: %v", retired, err)
	}

	// The configuration drops one plugin. Its delivery has to go with it, or
	// its skills keep reaching every session forever.
	if _, err := InstallRepoSkills(repo, trees[:1], agentplan.For(agent.AgentCodex)); err != nil {
		t.Fatalf("second delivery: %v", err)
	}
	if _, err := os.Lstat(retired); !os.IsNotExist(err) {
		t.Errorf("de-configured delivery survived (lstat err %v)", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".codex", "skills", "shirabe")); err != nil {
		t.Errorf("the still-configured delivery was pruned too: %v", err)
	}
}

func TestInstallRepoSkillsLeavesSomethingItDidNotDeliver(t *testing.T) {
	homelessMachine(t)

	repo := t.TempDir()
	theirs := filepath.Join(repo, ".codex", "skills", "handwritten")
	if err := os.MkdirAll(theirs, 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(theirs, "SKILL.md")
	if err := os.WriteFile(kept, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := InstallRepoSkills(repo, nil, agentplan.For(agent.AgentCodex))
	if err != nil {
		t.Fatalf("InstallRepoSkills: %v", err)
	}
	if _, statErr := os.Stat(kept); statErr != nil {
		t.Errorf("the reconciliation removed content niwa did not deliver: %v", statErr)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], theirs) {
		t.Errorf("warnings = %v, want one naming %s", result.Warnings, theirs)
	}
}

// writeMarketplaceTree writes a marketplace at root: its manifest, and the one
// plugin tree the manifest declares.
func writeMarketplaceTree(t *testing.T, root string) {
	t.Helper()
	files := map[string]string{
		filepath.Join(root, ".claude-plugin", "marketplace.json"):                  marketplaceManifest,
		filepath.Join(root, "plugins", "shirabe", ".claude-plugin", "plugin.json"): `{"name":"shirabe"}`,
		filepath.Join(root, "plugins", "shirabe", "skills", "review", "SKILL.md"):  "review body\n",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
