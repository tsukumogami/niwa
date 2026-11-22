package workspace

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/agent"
	"github.com/tsukumogami/niwa/internal/agentplan"
	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/fallbacknotice"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/pluginstore"
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

// scanFailure is a workspace whose root can't be listed, with an Applier
// whose reporter output is captured, the plugin inputs of one instance and
// the run's notices.
type scanFailure struct {
	a        *Applier
	in       PluginSkillsInputs
	out      *bytes.Buffer
	notices  *fallbacknotice.Collector
	root     string
	storeDir string
}

// newScanFailure makes the workspace and narrows its root to mode 0311, so
// it can be entered but not listed. withStore makes the plugin store's
// directory first.
func newScanFailure(t *testing.T, withStore bool) *scanFailure {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file modes don't bind root")
	}
	root := setupWorkspace(t, nil)
	home := t.TempDir()
	f := &scanFailure{
		in:      PluginSkillsInputs{InstanceRoot: filepath.Join(root, "one"), Home: home, Fetcher: &fakeFetcher{}},
		out:     &bytes.Buffer{},
		notices: fallbacknotice.New(nil),
		root:    root,
	}
	f.a = &Applier{Reporter: NewReporterWithTTY(f.out, false)}
	f.storeDir = pluginstore.Dir(home, root)
	if withStore {
		if err := os.MkdirAll(f.storeDir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(root, 0o311); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o755) })
	return f
}

// pass runs what runPipeline runs for the store: the deferred flush, the
// repair, and the sweep unless returnEarly.
func (f *scanFailure) pass(ctx context.Context, returnEarly bool) {
	defer f.a.flushPluginScanWarnings()
	f.a.repairPluginStore(ctx, f.in, f.notices)
	if returnEarly {
		return
	}
	f.a.sweepPluginStore(ctx, f.in, f.notices)
}

// repairWarnings counts repair's scan warning lines in the reporter output.
func (f *scanFailure) repairWarnings() int {
	return strings.Count(f.out.String(), "not every instance could be listed")
}

// An unlistable root is reported once. With a store, the sweep's skipped
// notice names the root and repair's warning is not printed; with no store,
// the sweep has nothing to do and repair's warning is printed once.
func TestAScanFailurePrintsOneLine(t *testing.T) {
	t.Run("with a store", func(t *testing.T) {
		f := newScanFailure(t, true)
		f.pass(context.Background(), false)
		reason, skipped := f.notices.SweepSkipped()
		if !skipped || !strings.Contains(reason, f.root) {
			t.Fatalf("SweepSkipped = %q, %v; want a reason naming %s", reason, skipped, f.root)
		}
		if n := f.repairWarnings(); n != 0 {
			t.Fatalf("repair's warning printed %d times beside the sweep's notice:\n%s", n, f.out.String())
		}
	})
	t.Run("with no store", func(t *testing.T) {
		f := newScanFailure(t, false)
		f.pass(context.Background(), false)
		if _, skipped := f.notices.SweepSkipped(); skipped {
			t.Fatal("a sweep with no store reported itself skipped")
		}
		if n := f.repairWarnings(); n != 1 {
			t.Fatalf("repair's warning printed %d times, want once:\n%s", n, f.out.String())
		}
		if _, err := os.Lstat(f.storeDir); !os.IsNotExist(err) {
			t.Fatalf("the sweep created the store (%v)", err)
		}
	})
}

// Every pass that repairs without reaching a sweep that runs still prints
// repair's warning once, and the deferred flush adds nothing after a sweep
// that took it.
func TestARepairWarningPrintsWhenNoSweepFollows(t *testing.T) {
	t.Run("a return before the sweep", func(t *testing.T) {
		f := newScanFailure(t, true)
		f.pass(context.Background(), true)
		if n := f.repairWarnings(); n != 1 {
			t.Fatalf("repair's warning printed %d times, want once:\n%s", n, f.out.String())
		}
		if _, skipped := f.notices.SweepSkipped(); skipped {
			t.Fatal("a sweep notice with no sweep run")
		}
	})
	t.Run("the store already swept", func(t *testing.T) {
		f := newScanFailure(t, true)
		f.a.pluginStoresSwept = map[string]bool{f.storeDir: true}
		f.pass(context.Background(), false)
		if n := f.repairWarnings(); n != 1 {
			t.Fatalf("repair's warning printed %d times, want once:\n%s", n, f.out.String())
		}
		if _, skipped := f.notices.SweepSkipped(); skipped {
			t.Fatal("a sweep notice from a sweep the guard skipped")
		}
	})
	t.Run("a sweep that errors", func(t *testing.T) {
		f := newScanFailure(t, true)
		f.pass(context.Background(), false)
		f.a.flushPluginScanWarnings()
		if n := f.repairWarnings(); n != 0 {
			t.Fatalf("repair's warning printed %d times beside the sweep's notice:\n%s", n, f.out.String())
		}
		if _, skipped := f.notices.SweepSkipped(); !skipped {
			t.Fatal("no sweep notice from a sweep that failed")
		}
		if n := strings.Count(f.notices.RenderText(), "sweep of unused releases was skipped"); n != 1 {
			t.Fatalf("%d sweep notices, want one:\n%s", n, f.notices.RenderText())
		}
	})
}

// refRecordingFetcher is a fakeFetcher that records the owner, repo and ref
// of every tarball request.
type refRecordingFetcher struct {
	*fakeFetcher
	fetched []string
}

func (f *refRecordingFetcher) FetchTarball(ctx context.Context, owner, repo, ref, etag string) (io.ReadCloser, string, int, *github.RenameRedirect, error) {
	f.fetched = append(f.fetched, owner+"/"+repo+"@"+ref)
	return f.fakeFetcher.FetchTarball(ctx, owner, repo, ref, etag)
}

// kitTarball is a second marketplace, kit, declaring the plugin kitplug. Its
// wrapper directory names the release commit (2222222...) because the store
// checks that.
func kitTarball(t *testing.T) []byte {
	t.Helper()
	return makeFakeTarball(t, map[string]string{
		"beta-kit-2222222/.claude-plugin/marketplace.json":              `{"name":"kit","plugins":[{"name":"kitplug","source":"./plugins/kitplug"}]}`,
		"beta-kit-2222222/plugins/kitplug/.claude-plugin/plugin.json":   `{"name":"kitplug"}`,
		"beta-kit-2222222/plugins/kitplug/skills/build/SKILL.md":        "build body\n",
		"beta-kit-2222222/plugins/kitplug/skills/build/references/a.md": "a\n",
	})
}

// toolsTarballAt is marketplaceTarball with the wrapper acme-tools-1111111. The
// wrapper names the commit because the store checks it.
func toolsTarballAt(t *testing.T) []byte {
	t.Helper()
	return makeFakeTarball(t, map[string]string{
		"acme-tools-1111111/.claude-plugin/marketplace.json":                  marketplaceManifest,
		"acme-tools-1111111/plugins/shirabe/.claude-plugin/plugin.json":       `{"name":"shirabe"}`,
		"acme-tools-1111111/plugins/shirabe/skills/review/SKILL.md":           "review body\n",
		"acme-tools-1111111/plugins/shirabe/skills/review/references/note.md": "note\n",
	})
}

// restoreWriteUnder makes every directory under dir writable again: the store seals release trees read-only, and this lets t.TempDir remove them.
func restoreWriteUnder(dir string) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(path, 0o755)
		}
		return nil
	})
}

// With share_plugin_releases off, a marketplace the instance does not hold yet
// is fetched into the instance at its unresolved ref, and an existing link into
// the store is still kept and Ensured against it.
func TestSharingOffFetchesANewMarketplaceIntoTheInstanceAndKeepsALink(t *testing.T) {
	const (
		toolsCommit = "1111111111111111111111111111111111111111"
		kitCommit   = "2222222222222222222222222222222222222222"
	)

	for _, sharingOff := range []bool{true, false} {
		name := "sharing off"
		if !sharingOff {
			name = "sharing on"
		}
		t.Run(name, func(t *testing.T) {
			home := homelessMachine(t)
			t.Cleanup(func() { restoreWriteUnder(home) })
			noStableRelease(t)
			root := setupWorkspace(t, nil)
			instance := filepath.Join(root, "one")
			contentRoot := filepath.Join(instance, ".niwa", "marketplaces")
			if err := os.MkdirAll(contentRoot, 0o755); err != nil {
				t.Fatal(err)
			}
			marketplaces := config.MarketplaceConfigs{{Source: "acme/tools"}, {Source: "beta/kit"}}

			// Place kit's release with sharing on and link the instance to
			// it, then point the link at the same release under another
			// home, so a relink by the run below shows.
			setup := PluginSkillsInputs{
				InstanceRoot: instance,
				Marketplaces: marketplaces,
				Fetcher:      &fakeFetcher{tarball: kitTarball(t)},
				Home:         home,
			}
			store, ok := pluginStoreFor(setup)
			if !ok {
				t.Fatal("no plugin store for an instance under a workspace root")
			}
			kit, valid := pluginstore.NewRelease("beta", "kit", kitCommit, "HEAD", "")
			if !valid {
				t.Fatal("kit's release is not one the store can hold")
			}
			kitLink := filepath.Join(contentRoot, "kit")
			if _, cond, err := store.Ensure(context.Background(), kit, kitLink, nil, nil); err != nil || cond != pluginstore.CondNone {
				t.Fatalf("placing kit: cond %q, err %v", cond, err)
			}
			if err := os.Remove(kitLink); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(pluginstore.TreePath(t.TempDir(), root, kit), kitLink); err != nil {
				t.Fatal(err)
			}

			fetcher := &refRecordingFetcher{fakeFetcher: &fakeFetcher{tarball: toolsTarballAt(t), commitOID: toolsCommit}}
			trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
				InstanceRoot: instance,
				Plugins:      []string{"shirabe@tools", "kitplug@kit"},
				Marketplaces: marketplaces,
				Fetcher:      fetcher,
				Home:         home,
				SharingOff:   sharingOff,
			})
			if len(missing) != 0 {
				t.Fatalf("missing = %v, want none", missing)
			}
			if len(trees) != 2 {
				t.Fatalf("resolved %v, want two trees", trees)
			}

			tools := filepath.Join(contentRoot, "tools")
			info, err := os.Lstat(tools)
			if err != nil {
				t.Fatalf("tools: %v", err)
			}
			acmeInStore := filepath.Join(pluginstore.Dir(home, root), pluginstore.Forge, "acme")
			if sharingOff {
				if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
					t.Fatalf("tools is %v, want a real directory", info.Mode())
				}
				if _, err := os.Stat(marketplaceManifestPath(tools)); err != nil {
					t.Fatalf("tools holds no manifest: %v", err)
				}
				if want := []string{"acme/tools@HEAD"}; strings.Join(fetcher.fetched, ",") != strings.Join(want, ",") {
					t.Fatalf("tarball requests = %v, want %v", fetcher.fetched, want)
				}
				if fetcher.headCalls != 0 {
					t.Fatalf("resolved a commit %d times, want none", fetcher.headCalls)
				}
				if _, err := os.Lstat(acmeInStore); !os.IsNotExist(err) {
					t.Fatalf("the store holds something for acme/tools (lstat err %v)", err)
				}
			} else {
				if info.Mode()&os.ModeSymlink == 0 {
					t.Fatalf("tools is %v, want a link into the store", info.Mode())
				}
				target, err := os.Readlink(tools)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(target, acmeInStore+string(filepath.Separator)) {
					t.Fatalf("tools links to %s, want a release under %s", target, acmeInStore)
				}
			}

			kitInfo, err := os.Lstat(kitLink)
			if err != nil {
				t.Fatalf("kit: %v", err)
			}
			if kitInfo.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("kit is %v, want it still a link", kitInfo.Mode())
			}
			target, err := os.Readlink(kitLink)
			if err != nil {
				t.Fatal(err)
			}
			if want := pluginstore.TreePath(home, root, kit); target != want {
				t.Fatalf("kit links to %s, want %s (Ensured against this store)", target, want)
			}
		})
	}
}

// sourceFetcher serves each "owner/repo" its own commit and tarball, and
// records the owner, repo and ref of every tarball request.
type sourceFetcher struct {
	commits  map[string]string
	tarballs map[string][]byte
	fetched  []string
}

func (f *sourceFetcher) HeadCommit(_ context.Context, owner, repo, _, _ string) (string, string, int, error) {
	commit, ok := f.commits[owner+"/"+repo]
	if !ok {
		return "", "", 404, nil
	}
	return commit, "", 200, nil
}

func (f *sourceFetcher) FetchTarball(_ context.Context, owner, repo, ref, _ string) (io.ReadCloser, string, int, *github.RenameRedirect, error) {
	f.fetched = append(f.fetched, owner+"/"+repo+"@"+ref)
	body, ok := f.tarballs[owner+"/"+repo]
	if !ok {
		return nil, "", 404, nil, nil
	}
	return io.NopCloser(bytes.NewReader(body)), "", 200, nil, nil
}

// skipAsRoot skips a test that relies on a mode the owner can't get past.
func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file modes don't bind root")
	}
}

// Two marketplaces falling back from one unwritable store each get their own
// copy at the resolved commit, and the run records one notice naming the
// store, its condition and both marketplaces. The store's narrowed mode is
// left as it was and nothing is written in it.
func TestStoreFallbackRecordsOneNoticePerStore(t *testing.T) {
	skipAsRoot(t)
	const (
		toolsCommit = "1111111111111111111111111111111111111111"
		kitCommit   = "2222222222222222222222222222222222222222"
	)
	noStableRelease(t)
	root := setupWorkspace(t, nil)
	instance := filepath.Join(root, "one")
	home := t.TempDir()
	storeDir := pluginstore.Dir(home, root)
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreWriteUnder(home) })

	fetcher := &sourceFetcher{
		commits:  map[string]string{"acme/tools": toolsCommit, "beta/kit": kitCommit},
		tarballs: map[string][]byte{"acme/tools": toolsTarballAt(t), "beta/kit": kitTarball(t)},
	}
	notices := fallbacknotice.New(nil)
	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"shirabe@tools", "kitplug@kit"},
		Marketplaces: config.MarketplaceConfigs{{Source: "acme/tools"}, {Source: "beta/kit"}},
		Fetcher:      fetcher,
		Home:         home,
		Notices:      notices,
	})
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
	if len(trees) != 2 {
		t.Fatalf("resolved %v, want two trees", trees)
	}

	for _, name := range []string{"tools", "kit"} {
		dest := filepath.Join(instance, ".niwa", "marketplaces", name)
		info, err := os.Lstat(dest)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("%s: %v, mode %v; want a real directory", name, err, info.Mode())
		}
		if _, err := os.Stat(marketplaceManifestPath(dest)); err != nil {
			t.Fatalf("%s holds no manifest: %v", name, err)
		}
	}
	if got, want := strings.Join(fetcher.fetched, ","), "acme/tools@"+toolsCommit+",beta/kit@"+kitCommit; got != want {
		t.Fatalf("tarball requests = %s, want %s (each at its resolved commit)", got, want)
	}

	notes := notices.StoreFallbacks()
	if len(notes) != 1 {
		t.Fatalf("StoreFallbacks = %+v, want one note", notes)
	}
	if n := notes[0]; n.Store != storeDir || n.Condition != string(pluginstore.CondUnwritable) || strings.Join(n.Marketplaces, ",") != "kit,tools" {
		t.Fatalf("StoreFallbacks()[0] = %+v, want %s, %q and both marketplaces", n, storeDir, pluginstore.CondUnwritable)
	}

	info, err := os.Lstat(storeDir)
	if err != nil || info.Mode().Perm() != 0o500 {
		t.Fatalf("plugins/: %v, mode %v; want it left at 0500", err, info.Mode().Perm())
	}
	if entries, err := os.ReadDir(storeDir); err != nil || len(entries) != 0 {
		t.Fatalf("plugins/ holds %d entries (%v), want none", len(entries), err)
	}
}

// A marketplace falling back from an unwritable store whose own fetch then
// fails is reported missing with that fetch's error, and the run records no
// store fallback notice: the instance never got a copy of its own.
func TestStoreFallbackRecordsNoNoticeWhenItsOwnFetchFails(t *testing.T) {
	skipAsRoot(t)
	const kitCommit = "2222222222222222222222222222222222222222"
	noStableRelease(t)
	root := setupWorkspace(t, nil)
	instance := filepath.Join(root, "one")
	home := t.TempDir()
	storeDir := pluginstore.Dir(home, root)
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreWriteUnder(home) })

	// The commit resolves, but no tarball is served, so the fallback fetch
	// gets a 404.
	fetcher := &sourceFetcher{commits: map[string]string{"beta/kit": kitCommit}}
	notices := fallbacknotice.New(nil)
	trees, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"kitplug@kit"},
		Marketplaces: config.MarketplaceConfigs{{Source: "beta/kit"}},
		Fetcher:      fetcher,
		Home:         home,
		Notices:      notices,
	})
	if len(trees) != 0 {
		t.Fatalf("resolved %v, want none", trees)
	}
	if len(missing) != 1 || !strings.Contains(missing[0].Reason, `marketplace "kit" did not resolve`) || !strings.Contains(missing[0].Reason, "GitHub returned 404") {
		t.Fatalf("missing = %v, want one reporting kit's failed fetch", missing)
	}
	if got, want := strings.Join(fetcher.fetched, ","), "beta/kit@"+kitCommit; got != want {
		t.Fatalf("tarball requests = %s, want %s (the fallback fetch at the resolved commit)", got, want)
	}
	if notes := notices.StoreFallbacks(); len(notes) != 0 {
		t.Fatalf("StoreFallbacks = %+v, want none when the fallback fetch failed", notes)
	}
}

// An instance that already links into a store that can't be used is kept on
// its link and reported through the missing plugin, with no store fallback
// notice: it was not given a copy of its own.
func TestKeptLinkRecordsNoStoreFallback(t *testing.T) {
	skipAsRoot(t)
	const kitCommit = "2222222222222222222222222222222222222222"
	noStableRelease(t)
	root := setupWorkspace(t, nil)
	instance := filepath.Join(root, "one")
	contentRoot := filepath.Join(instance, ".niwa", "marketplaces")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Cleanup(func() { restoreWriteUnder(home) })
	marketplaces := config.MarketplaceConfigs{{Source: "beta/kit"}}

	setup := PluginSkillsInputs{InstanceRoot: instance, Marketplaces: marketplaces, Fetcher: &fakeFetcher{tarball: kitTarball(t)}, Home: home}
	store, ok := pluginStoreFor(setup)
	if !ok {
		t.Fatal("no plugin store for an instance under a workspace root")
	}
	kit, valid := pluginstore.NewRelease("beta", "kit", kitCommit, "HEAD", "")
	if !valid {
		t.Fatal("kit's release is not one the store can hold")
	}
	kitLink := filepath.Join(contentRoot, "kit")
	if _, cond, err := store.Ensure(context.Background(), kit, kitLink, nil, nil); err != nil || cond != pluginstore.CondNone {
		t.Fatalf("placing kit: cond %q, err %v", cond, err)
	}
	storeDir := pluginstore.Dir(home, root)
	if err := os.Chmod(storeDir, 0); err != nil {
		t.Fatal(err)
	}

	notices := fallbacknotice.New(nil)
	_, missing := ResolvePluginTrees(context.Background(), PluginSkillsInputs{
		InstanceRoot: instance,
		Plugins:      []string{"kitplug@kit"},
		Marketplaces: marketplaces,
		Fetcher:      &fakeFetcher{tarball: kitTarball(t), commitOID: kitCommit},
		Home:         home,
		Notices:      notices,
	})
	if notes := notices.StoreFallbacks(); len(notes) != 0 {
		t.Fatalf("StoreFallbacks = %+v, want none for a kept link", notes)
	}
	if len(missing) != 1 || !strings.Contains(missing[0].Reason, storeDir) || !strings.Contains(missing[0].Reason, string(pluginstore.CondUnreadable)) {
		t.Fatalf("missing = %v, want one naming %s as %q", missing, storeDir, pluginstore.CondUnreadable)
	}
	if info, err := os.Lstat(kitLink); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("kit: %v; want it still a link", err)
	}
}

// A sweep stopped because the store can't be listed names the store and its
// condition in the notice.
func TestSweepStoppedNoticeNamesTheStoreCondition(t *testing.T) {
	skipAsRoot(t)
	root := setupWorkspace(t, nil)
	home := t.TempDir()
	storeDir := pluginstore.Dir(home, root)
	if err := os.MkdirAll(storeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(storeDir, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreWriteUnder(home) })

	a := &Applier{Reporter: NewReporterWithTTY(&bytes.Buffer{}, false)}
	notices := fallbacknotice.New(nil)
	a.sweepPluginStore(context.Background(), PluginSkillsInputs{InstanceRoot: filepath.Join(root, "one"), Home: home}, notices)

	reason, skipped := notices.SweepSkipped()
	if want := "the plugin store at " + storeDir + " is unreadable: "; !skipped || !strings.HasPrefix(reason, want) {
		t.Fatalf("SweepSkipped = %q, %v; want a reason starting %q", reason, skipped, want)
	}
	if !strings.Contains(reason, storeDir) {
		t.Fatalf("SweepSkipped = %q, want the path named", reason)
	}
}
