//go:build niwa_functional

package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
	"github.com/tsukumogami/niwa/internal/testhook"
)

// testBuildCreate is one Applier.Create in the test build, where borrowing
// is on unless share_git_history turns it off, against a fresh workspace root
// and developer home, with what the command printed.
type testBuildCreate struct {
	home, workspaceRoot, instanceRoot string
	stderr                            string
	err                               error
}

func createInTestBuild(t *testing.T, sb borrowSandbox, extra string, names ...string) testBuildCreate {
	t.Helper()
	if len(names) == 0 {
		names = borrowRepos
	}
	base := t.TempDir()
	c := testBuildCreate{
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
	var repos []github.Repo
	for _, name := range names {
		remote := sb.remotes[name]
		if remote == "" {
			remote = sb.remotes["app"]
		}
		repos = append(repos, github.Repo{Name: name, Visibility: "public", CloneURL: "file://" + remote})
	}
	applier := NewApplier(&mockGitHubClient{repos: map[string][]github.Repo{"testorg": repos}})
	applier.DeveloperHome = c.home
	var out bytes.Buffer
	applier.Reporter = NewReporterWithTTY(&out, false)
	c.instanceRoot, c.err = applier.Create(context.Background(), res.Config, niwaDir, c.workspaceRoot, "inst")
	c.stderr = out.String()
	return c
}

func (c testBuildCreate) clone(name string) string {
	return filepath.Join(c.instanceRoot, "tools", name)
}

// requireBorrows fails unless name's clone holds the one stable-path line.
func (c testBuildCreate) requireBorrows(t *testing.T, name string) {
	t.Helper()
	data, err := os.ReadFile(alternatesOf(c.clone(name)))
	want := mirror.StablePath(c.home, c.workspaceRoot, name) + "/objects\n"
	if err != nil || string(data) != want {
		t.Fatalf("%s's alternates = %q (%v), want %q", name, data, err, want)
	}
}

// requireFull fails unless name's clone has no alternates file.
func (c testBuildCreate) requireFull(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(alternatesOf(c.clone(name))); !os.IsNotExist(err) {
		t.Fatalf("%s has an alternates file (%v), want a full clone", name, err)
	}
	gitOut(t, c.clone(name), "fsck", "--no-progress")
}

// hasMirror reports whether the workspace has any mirrors/ entry for name.
func (c testBuildCreate) hasMirror(name string) bool {
	_, err := os.Lstat(mirror.StablePath(c.home, c.workspaceRoot, name))
	return err == nil
}

func (c testBuildCreate) mirrorsDirExists() bool {
	_, err := os.Lstat(filepath.Join(hostdir.WorkspaceDir(c.home, c.workspaceRoot), "mirrors"))
	return err == nil
}

// The setting decides which new clones borrow: unset borrows everywhere; the
// workspace off makes full clones and no mirrors/ entry; the workspace off
// with one repo on borrows that repo only; the workspace on with one repo off
// makes that repo a full clone with no mirror and no notice.
func TestTestBuildCreate_TheSettingDecidesWhoBorrows(t *testing.T) {
	sb := newBorrowSandbox(t)

	c := createInTestBuild(t, sb, "")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireBorrows(t, "app")
	c.requireBorrows(t, "lib")
	if strings.Contains(c.stderr, "mirror") {
		t.Fatalf("a create that seeded and borrowed printed a mirror line:\n%s", c.stderr)
	}

	c = createInTestBuild(t, sb, "share_git_history = false\n")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireFull(t, "app")
	c.requireFull(t, "lib")
	if c.mirrorsDirExists() {
		t.Fatal("the workspace off made a mirrors/ entry")
	}

	c = createInTestBuild(t, sb, "share_git_history = false\n\n[repos.app]\nshare_git_history = true\n")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireBorrows(t, "app")
	c.requireFull(t, "lib")
	if c.hasMirror("lib") {
		t.Fatal("lib, off with the workspace, got a mirror")
	}

	c = createInTestBuild(t, sb, "\n[repos.lib]\nshare_git_history = false\n")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireBorrows(t, "app")
	c.requireFull(t, "lib")
	if c.hasMirror("lib") {
		t.Fatal("lib, turned off, got a mirror")
	}
	if strings.Contains(c.stderr, "lib") {
		t.Fatalf("lib, turned off, was named in the output:\n%s", c.stderr)
	}
}

// The borrow-clone fault makes the borrowing clone fail after git ran: the
// create still succeeds, the clone is a full one with nothing left beside it,
// and the warning names the repo and borrow-clone-failed.
func TestTestBuildCreate_TheBorrowCloneFaultClonesInFull(t *testing.T) {
	sb := newBorrowSandbox(t)
	t.Setenv("NIWA_TEST_FAULT", "error:injected@"+testhook.LabelBorrowClone)
	c := createInTestBuild(t, sb, "")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	for _, name := range borrowRepos {
		c.requireFull(t, name)
		requireOnlyEntryAmong(t, filepath.Join(c.instanceRoot, "tools"), name)
		want := "warning: " + fallbackWarning(name, fallbackBorrowCloneFailed)
		if !strings.Contains(c.stderr, want) {
			t.Errorf("the output lacks %q:\n%s", want, c.stderr)
		}
	}
}

// requireOnlyEntryAmong fails when dir holds an entry that is neither a repo
// of the sandbox nor niwa's own: a partial or temporary clone left behind.
func requireOnlyEntryAmong(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != name && !containsString(borrowRepos, e.Name()) {
			t.Fatalf("%s holds %s beside the clones", dir, e.Name())
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// A repo whose name fails the repo-id pattern is cloned in full, and the
// warning says "a repo whose name niwa can't mirror" in place of its name.
func TestTestBuildCreate_AnUnmirrorableNameClonesInFull(t *testing.T) {
	sb := newBorrowSandbox(t)
	bad := "_app"
	c := createInTestBuild(t, sb, "", bad, "lib")
	if c.err != nil {
		t.Fatalf("Create: %v", c.err)
	}
	c.requireFull(t, bad)
	c.requireBorrows(t, "lib")
	if !strings.Contains(c.stderr, "warning: a repo whose name niwa can't mirror was cloned in full instead of borrowing from the host mirror (invalid-repo-id)") {
		t.Fatalf("the output lacks the invalid-repo-id warning:\n%s", c.stderr)
	}
	if strings.Contains(c.stderr, bad) {
		t.Fatalf("the output names %q:\n%s", bad, c.stderr)
	}
}
