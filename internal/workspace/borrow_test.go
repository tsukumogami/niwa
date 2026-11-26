package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// borrowRepos are the repos the borrowing sandbox serves, each from its own
// bare remote.
var borrowRepos = []string{"app", "lib"}

// borrowSandbox is a set of bare remotes, one per borrowRepos entry, each
// with one commit on main.
type borrowSandbox struct {
	remotes map[string]string
}

func newBorrowSandbox(t *testing.T) borrowSandbox {
	t.Helper()
	base := t.TempDir()
	sb := borrowSandbox{remotes: map[string]string{}}
	for _, name := range borrowRepos {
		remote := filepath.Join(base, name+".git")
		work := filepath.Join(base, name+"-work")
		mirrorGit(t, base, "init", "--quiet", "--bare", remote)
		mirrorGit(t, base, "init", "--quiet", work)
		if err := os.WriteFile(filepath.Join(work, "README.md"), []byte(name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		mirrorGit(t, work, "add", "README.md")
		mirrorGit(t, work, "commit", "--quiet", "-m", "first")
		mirrorGit(t, work, "push", "--quiet", remote, "HEAD:refs/heads/main")
		mirrorGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
		sb.remotes[name] = remote
	}
	return sb
}

// borrowCreate is the outcome of one create against the sandbox.
type borrowCreate struct {
	home, workspaceRoot, instanceRoot string
	cfg                               *config.WorkspaceConfig
	err                               error
}

// create runs Applier.Create for a fresh workspace root and developer home,
// with the [workspace] and [repos.*] lines extra adds to the config. The
// clones are real: each repo is cloned from its bare remote. The root holds
// .niwa/workspace.toml, as every root niwa init makes does, so it counts as
// a workspace root (workspaceRootHoldsState).
func (sb borrowSandbox) create(t *testing.T, extra string) borrowCreate {
	t.Helper()
	return sb.createIn(t, extra, true)
}

// createIn is create, with rooted false making the layout one with no
// workspace root: the directory above the instance has a .niwa directory
// but no workspace.toml in it.
func (sb borrowSandbox) createIn(t *testing.T, extra string, rooted bool) borrowCreate {
	t.Helper()
	base := t.TempDir()
	c := borrowCreate{
		home:          filepath.Join(base, "home"),
		workspaceRoot: filepath.Join(base, "ws"),
	}
	niwaDir := filepath.Join(c.workspaceRoot, ".niwa")
	for _, d := range []string{c.home, niwaDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := "[workspace]\nname = \"borrow-ws\"\n" + extra + `
[[sources]]
org = "testorg"

[groups.tools]
visibility = "public"
`
	res, err := config.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parsing the sandbox config: %v\n%s", err, body)
	}
	c.cfg = res.Config
	if rooted {
		if err := os.WriteFile(filepath.Join(niwaDir, WorkspaceConfigFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c.instanceRoot, c.err = sb.applier(c.home).Create(context.Background(), c.cfg, niwaDir, c.workspaceRoot, "inst")
	return c
}

// applier is an Applier that serves the sandbox's repos from their bare
// remotes, with home as the developer home.
func (sb borrowSandbox) applier(home string) *Applier {
	var repos []github.Repo
	for _, name := range borrowRepos {
		repos = append(repos, github.Repo{Name: name, Visibility: "public", CloneURL: "file://" + sb.remotes[name]})
	}
	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{"testorg": repos}})
	applier.Cloner = &Cloner{}
	applier.DeveloperHome = home
	var out bytes.Buffer
	applier.Reporter = NewReporterWithTTY(&out, false)
	return applier
}

// clones maps each clone under the instance, by its path relative to the
// instance root, to the commit its HEAD names.
func (c borrowCreate) clones(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, clone := range hostdir.InstanceClones(c.instanceRoot) {
		rel, err := filepath.Rel(c.instanceRoot, clone)
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "rev-parse", "HEAD")
		cmd.Dir = clone
		head, err := cmd.Output()
		if err != nil {
			t.Fatalf("reading HEAD of %s: %v", clone, err)
		}
		out[rel] = strings.TrimSpace(string(head))
	}
	return out
}

// requireNoBorrowing fails when any clone of the create has an alternates
// file or the workspace's host directory has a mirrors/ entry.
func (c borrowCreate) requireNoBorrowing(t *testing.T) {
	t.Helper()
	clones := c.clones(t)
	if len(clones) != len(borrowRepos) {
		t.Fatalf("the create made clones %v, want one for each of %v", clones, borrowRepos)
	}
	for rel := range clones {
		alt := filepath.Join(c.instanceRoot, rel, ".git", "objects", "info", "alternates")
		if _, err := os.Lstat(alt); !os.IsNotExist(err) {
			t.Errorf("%s has an alternates file (%v), want a full clone", rel, err)
		}
	}
	mirrors := filepath.Join(hostdir.WorkspaceDir(c.home, c.workspaceRoot), "mirrors")
	if _, err := os.Lstat(mirrors); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(mirrors)
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		t.Errorf("the create made %s (%v, entries %v), want no mirrors entry", mirrors, err, names)
	}
}

// shareGitHistoryCombos are the nine combinations of a repo's and the
// workspace's share_git_history, each unset, true or false.
func shareGitHistoryCombos() []struct{ repo, ws *bool } {
	states := []*bool{nil, sghPtr(true), sghPtr(false)}
	var out []struct{ repo, ws *bool }
	for _, repo := range states {
		for _, ws := range states {
			out = append(out, struct{ repo, ws *bool }{repo, ws})
		}
	}
	return out
}

func comboConfig(repo, ws *bool) *config.WorkspaceConfig {
	return &config.WorkspaceConfig{
		Workspace: config.WorkspaceMeta{Name: "ws", ShareGitHistory: ws},
		Repos:     map[string]config.RepoOverride{"app": {ShareGitHistory: repo}},
	}
}
