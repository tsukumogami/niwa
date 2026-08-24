package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
)

// rootDeliveryAgents runs f for every enumerated agent's gated producer at an
// instance root. The tests below never name an agent for the same reason the
// pipeline does not: which agent receives a root delivery is the declaration
// table's answer, and a test that named one would assert the answer it wanted
// instead of the one the table gives.
func rootDeliveryAgents(instanceRoot string, f func(producer agentplan.Producer)) {
	for _, ag := range agent.All() {
		f(agentplan.For(ag).Gated(true))
	}
}

// TestRootDeliveryLeavesAnEnclosingRepositoryAlone is the property behind the
// root plan's missing ExcludeAs.
//
// Workspaces are routinely prepared inside somebody else's checkout, and the
// helper that records niwa's ignore coverage inside a working tree searches
// upward for the enclosing repository. Handing it an instance root would write
// niwa's managed block into the exclude file of a repository that merely
// contains the instance -- coverage niwa was never asked for, in a tree it does
// not own. What the instance root needs is written at the root itself, by
// EnsureInstanceGitignore.
//
// The test puts an instance root inside a real repository and runs both root
// deliveries, then asserts the repository's exclude file is exactly as it was.
func TestRootDeliveryLeavesAnEnclosingRepositoryAlone(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}

	outer := t.TempDir()
	if out, err := exec.Command("git", "-C", outer, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	excludePath := filepath.Join(outer, ".git", "info", "exclude")
	before, _ := os.ReadFile(excludePath)

	instanceRoot := filepath.Join(outer, "instance")
	if err := os.MkdirAll(instanceRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	pluginRoot := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(filepath.Join(pluginRoot, "skills", "demo-skill"), 0o755); err != nil {
		t.Fatal(err)
	}

	rootDeliveryAgents(instanceRoot, func(producer agentplan.Producer) {
		m := &RootSkillsMaterializer{
			Plugins:  []agentplan.PluginTree{{Name: "demo", Root: pluginRoot}},
			Producer: producer,
		}
		if _, err := m.Materialize(&MaterializeContext{RepoDir: instanceRoot}); err != nil {
			t.Fatalf("root skills delivery: %v", err)
		}
		if _, err := (codexNiwaPluginProcedure{}).Deliver(procedureInput{
			InstanceRoot: instanceRoot,
			Producer:     producer,
		}); err != nil {
			t.Fatalf("niwa plugin delivery: %v", err)
		}
	})

	after, _ := os.ReadFile(excludePath)
	if string(after) != string(before) {
		t.Errorf("a root delivery rewrote the enclosing repository's exclude file:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if strings.Contains(string(after), "niwa managed") {
		t.Error("the enclosing repository's exclude file carries niwa's managed block")
	}
}

// TestNiwaPluginTreeCoexistsWithAMarketplaceNamedNiwa is the site-collision
// property, asserted rather than argued.
//
// niwa's own plugin is delivered under a fixed name, and a workspace is free to
// configure a marketplace under that same name. Had the embedded tree been
// extracted into the marketplace content directory, one of the two would have
// found the other's bytes where it expected its own. They live under different
// parents instead, so the collision cannot be constructed -- which is what this
// test tries and fails to do.
func TestNiwaPluginTreeCoexistsWithAMarketplaceNamedNiwa(t *testing.T) {
	instanceRoot := t.TempDir()

	// A configured marketplace whose registration name is the same name niwa's
	// own plugin takes, already fetched into this instance.
	marketplaceRoot := filepath.Join(marketplaceContentRoot(instanceRoot), agentplan.NiwaPluginTreeName)
	if err := os.MkdirAll(filepath.Join(marketplaceRoot, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"plugins":[{"name":"demo","source":"./demo"}]}`
	if err := os.WriteFile(marketplaceManifestPath(marketplaceRoot), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(marketplaceRoot, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}

	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instanceRoot,
		Plugins:      []string{"demo@" + agentplan.NiwaPluginTreeName},
		Marketplaces: config.MarketplaceConfigs{{Source: "someorg/" + agentplan.NiwaPluginTreeName}},
	})
	if len(missing) > 0 {
		t.Fatalf("the configured marketplace did not resolve: %v", missing)
	}
	if len(trees) != 1 || trees[0].Root != filepath.Join(marketplaceRoot, "demo") {
		t.Fatalf("resolved trees = %v, want the plugin under %s", trees, marketplaceRoot)
	}

	source, err := ensureNiwaPluginTree(instanceRoot)
	if err != nil {
		t.Fatalf("materializing the niwa plugin tree: %v", err)
	}
	if source == marketplaceRoot {
		t.Fatal("niwa's own tree was extracted over the configured marketplace")
	}
	if _, err := os.Stat(filepath.Join(source, niwaPluginManifestFile)); err != nil {
		t.Errorf("niwa's own tree is not at %s: %v", source, err)
	}

	// The configured marketplace is untouched: same manifest, same declared
	// plugin, still resolving to the same tree.
	got, err := os.ReadFile(marketplaceManifestPath(marketplaceRoot))
	if err != nil || string(got) != manifest {
		t.Errorf("the configured marketplace's manifest changed: %q, %v", got, err)
	}
}

// TestNiwaPluginTreeIsIdempotentAndPathStable covers the two properties the
// symlink into the skills directory rests on: a tree already at the embedded
// version is left alone, and a replacement lands back at the same path rather
// than at a new one a prior link would not name.
func TestNiwaPluginTreeIsIdempotentAndPathStable(t *testing.T) {
	instanceRoot := t.TempDir()

	first, err := ensureNiwaPluginTree(instanceRoot)
	if err != nil {
		t.Fatalf("first materialization: %v", err)
	}
	marker := filepath.Join(first, ".not-niwas")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := ensureNiwaPluginTree(instanceRoot)
	if err != nil {
		t.Fatalf("second materialization: %v", err)
	}
	if second != first {
		t.Errorf("the tree moved from %s to %s; a symlink to the first would now dangle", first, second)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("a tree already at the embedded version was rewritten")
	}

	// A tree at a different version is replaced, and replaced in place.
	if err := os.WriteFile(filepath.Join(first, niwaPluginManifestFile), []byte(`{"version":"0.0.0-stale"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	third, err := ensureNiwaPluginTree(instanceRoot)
	if err != nil {
		t.Fatalf("replacing a stale tree: %v", err)
	}
	if third != first {
		t.Errorf("the replacement landed at %s rather than back at %s", third, first)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the stale tree survived its replacement")
	}
	if _, err := os.Stat(first + ".staging"); err == nil {
		t.Error("the staging directory outlived the promotion")
	}
}

// TestRootSkillsDeliveryFailsRatherThanSkippingAnUnreadableDirectory is half of
// the loud-failure posture: where the contract declares the capability
// implemented, a root delivery that cannot be made fails rather than leaving a
// session quietly short of skills. The other half is the pipeline's, which
// names the capability in the error -- see the apply-level test below.
func TestRootSkillsDeliveryFailsRatherThanSkippingAnUnreadableDirectory(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	instanceRoot := t.TempDir()
	blocked := 0
	rootDeliveryAgents(instanceRoot, func(producer agentplan.Producer) {
		dir := skillsDirFor(t, producer, instanceRoot)
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		blocked++

		m := &RootSkillsMaterializer{Producer: producer}
		if _, err := m.Materialize(&MaterializeContext{RepoDir: instanceRoot}); err == nil {
			t.Error("an unreadable skills directory was delivered into silently")
		}
	})
	if blocked == 0 {
		t.Skip("no enumerated agent reads root skills from a delivered tree")
	}
}

// TestNiwaPluginDeliveryFailsOnAnUnwritableInstance is the same posture for the
// procedure-routed half: the tree has to reach disk before anything can link to
// it, and an extraction that cannot happen is not something to warn about and
// carry on from.
func TestNiwaPluginDeliveryFailsOnAnUnwritableInstance(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	instanceRoot := t.TempDir()
	if err := os.Chmod(instanceRoot, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(instanceRoot, 0o755) })

	rootDeliveryAgents(instanceRoot, func(producer agentplan.Producer) {
		if _, err := (codexNiwaPluginProcedure{}).Deliver(procedureInput{
			InstanceRoot: instanceRoot,
			Producer:     producer,
		}); err == nil {
			t.Error("a tree that could not be extracted was reported as delivered")
		}
	})
}

// TestApplyFailsWhenARootDeliveryCannotBeMade is the pipeline half of the
// posture: the failure reaches the user as an error naming the capability, so
// what went undelivered is in the message rather than inferred from it.
func TestApplyFailsWhenARootDeliveryCannotBeMade(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}

	tmpDir := t.TempDir()
	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "blocked"

[[sources]]
org = "testorg"

[groups.all]
visibility = "public"
`
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(filepath.Join(niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	instanceRoot := filepath.Join(tmpDir, "blocked")
	repoDir := filepath.Join(instanceRoot, "all", "repo1")
	if err := os.MkdirAll(filepath.Join(repoDir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Block every root skills directory an enumerated agent reads from, so the
	// delivery fails wherever the table says it is made.
	blocked := 0
	rootDeliveryAgents(instanceRoot, func(producer agentplan.Producer) {
		dir := skillsDirFor(t, producer, instanceRoot)
		if dir == "" {
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o000); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() { os.Chmod(dir, 0o755) })
		blocked++
	})
	if blocked == 0 {
		t.Skip("no enumerated agent reads root skills from a delivered tree")
	}

	applier := NewApplier(&mockGitHubClient{
		repos: map[string][]github.Repo{
			"testorg": {{Name: "repo1", Visibility: "public", SSHURL: "git@github.com:testorg/repo1.git"}},
		},
	})
	applier.Cloner = &Cloner{}

	_, err = applier.Create(context.Background(), loaded.Config, niwaDir, tmpDir, loaded.Config.Workspace.Name)
	if err == nil {
		t.Fatal("create succeeded with an undeliverable root skills directory")
	}
	if !strings.Contains(err.Error(), agentplan.RootProjectSkills.String()) {
		t.Errorf("error does not name the capability: %v", err)
	}
}

// skillsDirFor asks the producer where this agent's root-delivered trees
// belong. Empty means this agent takes no delivered trees, which is a statement
// about how its skills arrive rather than about whether it has any -- and the
// tests above skip it rather than inventing a directory for it.
//
// The path is read from the producer rather than written out here for the
// reason the whole delivery is shaped this way: a literal would be this package
// deciding one agent's layout, which is the decision the declaration table
// exists to hold.
//
// It reads the reconcile spec rather than the plan because the two answer
// different questions. The plan says what is delivered, which is gated on the
// capability's own declaration; the spec says which directory the delivery owns,
// which is a fact about the agent's layout and is answered whether or not the
// row has flipped. The failure these tests provoke is in that directory either
// way.
func skillsDirFor(t *testing.T, producer agentplan.Producer, dir string) string {
	t.Helper()
	return producer.RootSkillsReconcileSpec(agentplan.RootSkillsInputs{Dir: dir}).Dir
}
