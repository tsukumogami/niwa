package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/pluginrecord"
)

const testManifest = ".claude-plugin/marketplace.json"

// newMarketplaceRemote creates a bare repository whose main branch carries a
// marketplace manifest named name, and returns its path.
func newMarketplaceRemote(t *testing.T, name string) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "tools.git")
	run(t, "git", "init", "--bare", "--initial-branch=main", bare)
	work := filepath.Join(base, "work")
	run(t, "git", "init", "--initial-branch=main", work)
	gitConfig(t, work)
	if err := os.MkdirAll(filepath.Join(work, ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, testManifest), `{"name":"`+name+`","plugins":[]}`)
	run(t, "git", "-C", work, "add", ".")
	run(t, "git", "-C", work, "commit", "-m", "initial")
	run(t, "git", "-C", work, "remote", "add", "origin", bare)
	run(t, "git", "-C", work, "push", "origin", "main")
	return bare
}

// pushMarketplaceCommit commits file on branch (created from main when new)
// and pushes it to bare, returning the new commit.
func pushMarketplaceCommit(t *testing.T, bare, branch, file string) string {
	t.Helper()
	work := t.TempDir()
	run(t, "git", "clone", "--quiet", bare, work)
	gitConfig(t, work)
	run(t, "git", "-C", work, "checkout", "-B", branch)
	writeFile(t, filepath.Join(work, file), file)
	run(t, "git", "-C", work, "add", ".")
	run(t, "git", "-C", work, "commit", "-m", "add "+file)
	run(t, "git", "-C", work, "push", "origin", branch)
	return gitRevParse(t, work, "HEAD")
}

func gitRevParse(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("git rev-parse %s in %s: %v", ref, dir, err)
	}
	return strings.TrimSpace(string(out))
}

// currentCommit is the commit the managed copy of repo is checked out at.
func currentCommit(t *testing.T, workspaceRoot, repo string) string {
	t.Helper()
	target, err := os.Readlink(managedMarketplaceCurrent(workspaceRoot, repo))
	if err != nil {
		t.Fatalf("reading the current link: %v", err)
	}
	return filepath.Base(target)
}

func checkoutNames(t *testing.T, workspaceRoot, repo string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(managedMarketplacesRoot(workspaceRoot), repo, managedCheckoutsDir))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func toolsMarketplace(url, ref string) []managedMarketplace {
	return []managedMarketplace{{repo: "tools", cloneURL: url, ref: ref, manifests: []string{testManifest}}}
}

func TestSyncManagedMarketplaces_DefaultBranchThenNewCommit(t *testing.T) {
	bare := newMarketplaceRemote(t, "tsukumogami")
	ws := t.TempDir()
	ctx := context.Background()

	if w := syncManagedMarketplaces(ctx, ws, toolsMarketplace(bare, "")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	first := gitRevParse(t, bare, "main")
	if got := currentCommit(t, ws, "tools"); got != first {
		t.Fatalf("current = %s, want main %s", got, first)
	}
	if _, err := os.Stat(filepath.Join(managedMarketplaceCurrent(ws, "tools"), testManifest)); err != nil {
		t.Fatalf("manifest not reachable through current: %v", err)
	}

	second := pushMarketplaceCommit(t, bare, "main", "two.txt")
	if w := syncManagedMarketplaces(ctx, ws, toolsMarketplace(bare, "")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	if got := currentCommit(t, ws, "tools"); got != second {
		t.Fatalf("current = %s, want new main %s", got, second)
	}
	// The checkout just replaced stays for sessions still reading it.
	if got := checkoutNames(t, ws, "tools"); len(got) != 2 {
		t.Fatalf("checkouts = %v, want the new one and the one it replaced", got)
	}

	third := pushMarketplaceCommit(t, bare, "main", "three.txt")
	if w := syncManagedMarketplaces(ctx, ws, toolsMarketplace(bare, "")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	got := checkoutNames(t, ws, "tools")
	if len(got) != 2 || strings.Contains(strings.Join(got, ","), first) {
		t.Fatalf("checkouts = %v, want %s and %s only", got, second, third)
	}
}

func TestSyncManagedMarketplaces_ConfiguredRefHonoured(t *testing.T) {
	bare := newMarketplaceRemote(t, "tsukumogami")
	feature := pushMarketplaceCommit(t, bare, "feature", "feature.txt")
	ws := t.TempDir()

	if w := syncManagedMarketplaces(context.Background(), ws, toolsMarketplace(bare, "feature")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	if got := currentCommit(t, ws, "tools"); got != feature {
		t.Fatalf("current = %s, want feature %s", got, feature)
	}

	// A tag and a commit resolve the same way.
	run(t, "git", "-C", bare, "tag", "v1.0.0", "main")
	main := gitRevParse(t, bare, "main")
	if w := syncManagedMarketplaces(context.Background(), ws, toolsMarketplace(bare, "v1.0.0")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	if got := currentCommit(t, ws, "tools"); got != main {
		t.Fatalf("current = %s, want tag v1.0.0 at %s", got, main)
	}
}

func TestSyncManagedMarketplaces_FailureKeepsLastGoodCheckout(t *testing.T) {
	bare := newMarketplaceRemote(t, "tsukumogami")
	ws := t.TempDir()
	ctx := context.Background()

	if w := syncManagedMarketplaces(ctx, ws, toolsMarketplace(bare, "")); len(w) != 0 {
		t.Fatalf("unexpected warnings: %v", w)
	}
	good := currentCommit(t, ws, "tools")

	// A branch whose head dropped the manifest.
	work := t.TempDir()
	run(t, "git", "clone", "--quiet", bare, work)
	gitConfig(t, work)
	run(t, "git", "-C", work, "checkout", "-b", "broken")
	run(t, "git", "-C", work, "rm", "--quiet", testManifest)
	run(t, "git", "-C", work, "commit", "-m", "drop the manifest")
	run(t, "git", "-C", work, "push", "origin", "broken")

	for name, ms := range map[string][]managedMarketplace{
		"unreachable remote":   toolsMarketplace(filepath.Join(t.TempDir(), "gone.git"), ""),
		"unknown ref":          toolsMarketplace(bare, "no-such-branch"),
		"ref without manifest": toolsMarketplace(bare, "broken"),
	} {
		t.Run(name, func(t *testing.T) {
			w := syncManagedMarketplaces(ctx, ws, ms)
			if len(w) != 1 || !strings.Contains(w[0], "keeping the copy already checked out") {
				t.Fatalf("warnings = %v, want one saying the last checkout is kept", w)
			}
			if got := currentCommit(t, ws, "tools"); got != good {
				t.Fatalf("current moved to %s after a failed update, want %s", got, good)
			}
		})
	}
}

func TestSyncManagedMarketplaces_FirstFailureLeavesNothingToRegister(t *testing.T) {
	ws := t.TempDir()
	w := syncManagedMarketplaces(context.Background(), ws, toolsMarketplace(filepath.Join(t.TempDir(), "gone.git"), ""))
	if len(w) != 1 || !strings.Contains(w[0], "not registered until") {
		t.Fatalf("warnings = %v, want one saying nothing is registered yet", w)
	}

	instanceClone := filepath.Join(t.TempDir(), "tools")
	index := managedMarketplaceIndex(ws, map[string]string{"tools": instanceClone})
	if index["tools"] != instanceClone {
		t.Fatalf("index[tools] = %q, want the instance clone %q while no managed copy exists", index["tools"], instanceClone)
	}

	var called bool
	a := &Applier{
		Reporter: NewReporter(&bytes.Buffer{}),
		reconcileMarketplaces: func(desired map[string]pluginrecord.MarketplaceWant) (pluginrecord.ReconcileReport, error) {
			called = true
			for name, want := range desired {
				if want.Path != "" {
					t.Errorf("desired[%s].Path = %q, want no rewrite without a managed copy", name, want.Path)
				}
			}
			return pluginrecord.ReconcileReport{}, nil
		},
	}
	writeToolsManifest(t, instanceClone, "tsukumogami")
	cfg := &config.WorkspaceConfig{Claude: config.ClaudeConfig{
		Marketplaces: config.MarketplaceConfigs{{Source: "repo:tools/" + testManifest}},
	}}
	a.reconcileMarketplaceRegistry(cfg, index, ws)
	if !called {
		t.Fatal("reconcile seam was not called")
	}
}

// marketplacePipeline is a workspace whose tools repo is a repo: marketplace,
// cloned from a local remote, with Claude Code's registry in a scratch HOME.
type marketplacePipeline struct {
	ws, niwaDir, home, registry, bare string
	cfg                               *config.WorkspaceConfig
}

func newMarketplacePipeline(t *testing.T) *marketplacePipeline {
	t.Helper()
	p := &marketplacePipeline{ws: t.TempDir(), home: t.TempDir()}
	t.Setenv("HOME", p.home)
	p.bare = newMarketplaceRemote(t, "tsukumogami")

	p.niwaDir = filepath.Join(p.ws, ".niwa")
	if err := os.MkdirAll(p.niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configTOML := `
[workspace]
name = "mkt"

[[sources]]
org = "testorg"

[groups.private]
visibility = "private"

[repos.tools]
url = "` + p.bare + `"

[claude]
marketplaces = ["repo:tools/.claude-plugin/marketplace.json"]
plugins = ["tsukumogami@tsukumogami"]
`
	if err := os.WriteFile(filepath.Join(p.niwaDir, "workspace.toml"), []byte(configTOML), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := config.Load(filepath.Join(p.niwaDir, "workspace.toml"))
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}
	p.cfg = result.Config

	p.registry = filepath.Join(p.home, ".claude", "plugins", "known_marketplaces.json")
	if err := os.MkdirAll(filepath.Dir(p.registry), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *marketplacePipeline) applier(t *testing.T) *Applier {
	t.Helper()
	a := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{
		"testorg": {{Name: "tools", Visibility: "private"}},
	}})
	a.DeveloperHome = p.home
	a.Reporter = NewReporterWithTTY(&bytes.Buffer{}, false)
	a.prunePluginRecords = nil
	a.reconcileMarketplaces = func(desired map[string]pluginrecord.MarketplaceWant) (pluginrecord.ReconcileReport, error) {
		return reconcileMarketplacesAt(p.home, desired)
	}
	return a
}

// seedRegistry writes a registry whose tsukumogami entry points at dir, beside
// an unrelated entry in Claude Code's own formatting.
func (p *marketplacePipeline) seedRegistry(t *testing.T, dir string) {
	t.Helper()
	body := `{
  "shirabe": {
    "source": {
      "source": "github",
      "repo": "tsukumogami/shirabe"
    },
    "installLocation": "/elsewhere/shirabe",
    "lastUpdated": "2026-10-01T00:00:00.000Z",
    "autoUpdate": true
  },
  "tsukumogami": {
    "source": {
      "source": "directory",
      "path": "` + dir + `"
    },
    "installLocation": "` + dir + `",
    "lastUpdated": "2026-10-01T00:00:00.000Z",
    "autoUpdate": false
  }
}
`
	if err := os.WriteFile(p.registry, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (p *marketplacePipeline) entry(t *testing.T) (path, installLocation string, raw map[string]json.RawMessage) {
	t.Helper()
	data, err := os.ReadFile(p.registry)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatalf("registry is not valid JSON: %v", err)
	}
	var e struct {
		Source struct {
			Path string `json:"path"`
		} `json:"source"`
		InstallLocation string `json:"installLocation"`
	}
	if err := json.Unmarshal(top["tsukumogami"], &e); err != nil {
		t.Fatal(err)
	}
	return e.Source.Path, e.InstallLocation, top
}

func (p *marketplacePipeline) assertRegisteredFromManagedCopy(t *testing.T) {
	t.Helper()
	want := managedMarketplaceCurrent(p.ws, "tools")
	path, install, top := p.entry(t)
	if path != want || install != want {
		t.Fatalf("tsukumogami entry = path %q, installLocation %q; want both %q", path, install, want)
	}
	if _, err := os.Stat(filepath.Join(path, testManifest)); err != nil {
		t.Fatalf("registered path has no manifest: %v", err)
	}
	wantShirabe := `{
    "source": {
      "source": "github",
      "repo": "tsukumogami/shirabe"
    },
    "installLocation": "/elsewhere/shirabe",
    "lastUpdated": "2026-10-01T00:00:00.000Z",
    "autoUpdate": true
  }`
	if string(top["shirabe"]) != wantShirabe {
		t.Fatalf("unrelated entry changed:\n%s", top["shirabe"])
	}
}

// TestManagedMarketplace_CreateApplyDestroy walks the sequence that moved the
// global entry: each create pointed it at the new instance's clone, apply left
// a stale entry alone, and destroying the instance that registered last left
// it dangling.
func TestManagedMarketplace_CreateApplyDestroy(t *testing.T) {
	p := newMarketplacePipeline(t)
	ctx := context.Background()

	first, err := p.applier(t).Create(ctx, p.cfg, p.niwaDir, p.ws, "mkt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Claude Code registered the marketplace from an instance's clone, the
	// way it does from that instance's settings before this change.
	p.seedRegistry(t, filepath.Join(first, "private", "tools"))

	second, err := p.applier(t).Create(ctx, p.cfg, p.niwaDir, p.ws, "mkt-2")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	p.assertRegisteredFromManagedCopy(t)

	// Every instance's settings name the managed copy too.
	for _, inst := range []string{first, second} {
		data, err := os.ReadFile(filepath.Join(inst, ".claude", "settings.json"))
		if err != nil {
			t.Fatal(err)
		}
		var settings struct {
			Extra map[string]struct {
				Source struct {
					Path string `json:"path"`
				} `json:"source"`
			} `json:"extraKnownMarketplaces"`
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if got := settings.Extra["tsukumogami"].Source.Path; got != managedMarketplaceCurrent(p.ws, "tools") {
			t.Errorf("%s settings name %q, want the managed copy", inst, got)
		}
	}

	// An entry repointed at an instance (a create by an older niwa, or a
	// session's own registration) is rewritten by the next apply.
	p.seedRegistry(t, filepath.Join(second, "private", "tools"))
	if err := p.applier(t).Apply(ctx, p.cfg, p.niwaDir, second); err != nil {
		t.Fatalf("apply: %v", err)
	}
	p.assertRegisteredFromManagedCopy(t)

	// Destroying the instance that registered last leaves the entry valid.
	if err := DestroyInstance(second, WithPluginRecordBaseDir(p.home)); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	p.assertRegisteredFromManagedCopy(t)
}

// TestManagedMarketplace_ApplyFollowsNewCommits asserts an apply moves the
// managed copy to the remote's new head without moving the registered path.
func TestManagedMarketplace_ApplyFollowsNewCommits(t *testing.T) {
	p := newMarketplacePipeline(t)
	ctx := context.Background()

	inst, err := p.applier(t).Create(ctx, p.cfg, p.niwaDir, p.ws, "mkt")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	p.seedRegistry(t, managedMarketplaceCurrent(p.ws, "tools"))

	next := pushMarketplaceCommit(t, p.bare, "main", "next.txt")
	if err := p.applier(t).Apply(ctx, p.cfg, p.niwaDir, inst); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := currentCommit(t, p.ws, "tools"); got != next {
		t.Fatalf("managed copy at %s, want %s", got, next)
	}
	p.assertRegisteredFromManagedCopy(t)
}
