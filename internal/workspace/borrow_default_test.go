package workspace

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// borrowEnabled is the share_git_history cascade, with nothing else deciding
// it, for every combination a user can write and for a nil config.
func TestBorrowEnabled_IsTheCascade(t *testing.T) {
	for _, c := range shareGitHistoryCombos() {
		cfg := comboConfig(c.repo, c.ws)
		for _, repo := range []string{"app", "unnamed"} {
			if got, want := borrowEnabled(cfg, repo), cfg.ShareGitHistoryEnabled(repo); got != want {
				t.Errorf("borrowEnabled(%s) with repo=%s workspace=%s = %t, want the cascade's %t",
					repo, sghState(c.repo), sghState(c.ws), got, want)
			}
		}
	}
	if !borrowEnabled(nil, "app") {
		t.Error("borrowEnabled with a nil config is false, want the cascade's default on")
	}
}

// requireBorrowsByDefault fails unless every clone of the create holds
// exactly one alternates line, the repo's stable path followed by /objects,
// and that stable path exists.
func (c borrowCreate) requireBorrowsByDefault(t *testing.T) {
	t.Helper()
	clones := c.clones(t)
	if len(clones) != len(borrowRepos) {
		t.Fatalf("the create made clones %v, want one for each of %v", clones, borrowRepos)
	}
	for _, repo := range borrowRepos {
		clone := filepath.Join(c.instanceRoot, "tools", repo)
		stable := mirror.StablePath(c.home, c.workspaceRoot, repo)
		want := filepath.Join(stable, "objects") + "\n"
		data, err := os.ReadFile(alternatesOf(clone))
		if err != nil || string(data) != want {
			t.Errorf("%s's alternates = %q (%v), want the one line %q", repo, data, err, want)
		}
		if _, err := os.Stat(stable); err != nil {
			t.Errorf("%s's stable path %s doesn't exist: %v", repo, stable, err)
		}
	}
}

// A create where a workspace root exists borrows every repo from its host
// mirror, both with share_git_history set nowhere and with it set to true at
// both levels.
func TestCreate_BorrowsByDefaultWhereAWorkspaceRootExists(t *testing.T) {
	sb := newBorrowSandbox(t)
	for name, extra := range map[string]string{
		"unset": "",
		"on at both levels": "share_git_history = true\n\n" +
			"[repos.app]\nshare_git_history = true\n\n" +
			"[repos.lib]\nshare_git_history = true\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := sb.create(t, extra)
			if c.err != nil {
				t.Fatalf("Create: %v", c.err)
			}
			for _, repo := range borrowRepos {
				if !borrowEnabled(c.cfg, repo) {
					t.Errorf("borrowEnabled(%s) is false", repo)
				}
			}
			c.requireBorrowsByDefault(t)
		})
	}
}

// A create in a layout with no workspace root makes full clones and no
// mirrors/ directory, whatever share_git_history says.
func TestCreate_NoWorkspaceRootMakesFullClones(t *testing.T) {
	sb := newBorrowSandbox(t)
	for name, extra := range map[string]string{
		"unset":             "",
		"on at both levels": "share_git_history = true\n\n[repos.app]\nshare_git_history = true\n",
	} {
		t.Run(name, func(t *testing.T) {
			c := sb.createIn(t, extra, false)
			if c.err != nil {
				t.Fatalf("Create: %v", c.err)
			}
			c.requireNoBorrowing(t)
		})
	}
}

// An instance whose clones were made in full before borrowing was on keeps
// them full: an apply with no setting at either level adds no alternates
// file to a clone that already exists.
func TestApply_ClonesMadeBeforeTheTurnOnStayFull(t *testing.T) {
	sb := newBorrowSandbox(t)
	// The workspace off makes the clones a release build made before the
	// turn-on: full, with no mirror.
	c := sb.create(t, "share_git_history = false\n")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireNoBorrowing(t)
	before := c.clones(t)

	res, err := config.Parse([]byte("[workspace]\nname = \"borrow-ws\"\n\n[[sources]]\norg = \"testorg\"\n\n[groups.tools]\nvisibility = \"public\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !borrowEnabled(res.Config, "app") {
		t.Fatal("the apply's config doesn't have borrowing on, so the test proves nothing")
	}
	niwaDir := filepath.Join(c.workspaceRoot, ".niwa")
	if err := sb.applier(c.home).Apply(context.Background(), res.Config, niwaDir, c.instanceRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got := c.clones(t); !reflect.DeepEqual(got, before) {
		t.Fatalf("clones after the apply = %v, want %v as before it", got, before)
	}
	for rel := range before {
		alt := filepath.Join(c.instanceRoot, rel, ".git", "objects", "info", "alternates")
		if _, err := os.Lstat(alt); !os.IsNotExist(err) {
			t.Errorf("%s has an alternates file after the apply (%v), want the full clone it was", rel, err)
		}
	}
}
