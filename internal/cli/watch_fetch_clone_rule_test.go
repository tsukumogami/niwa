package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/watch"
)

// The failed-stage rollback's removal rule over a pr-clone the real
// watch.FetchPRHead wrote, not the fakePRClone stand-in. The PR's head is
// fetched from a local source repository through a file:// clone URL a fake
// pull lookup serves, so the clone has the shape a real stage leaves: shallow,
// HEAD detached at the PR's head, no branch and no remote. The tests mutate
// package globals, so none of them runs in parallel.

const (
	cloneRuleReadme = "widget\n"
	cloneRuleChange = "the PR's change\n"
)

// cloneRuleSource makes the repository the PR is fetched from: two commits,
// so a depth-1 fetch of the tip is really shallow, and allowAnySHA1InWant so
// the fetch of an exact sha is accepted. It returns the source and its tip.
func cloneRuleSource(t *testing.T) (src, tip string) {
	t.Helper()
	src = filepath.Join(canonicalTempDir(t), "source")
	passGit(t, filepath.Dir(src), "init", "-q", "-b", "main", src)
	passGit(t, src, "config", "uploadpack.allowAnySHA1InWant", "true")
	writeTestFile(t, filepath.Join(src, "README.md"), cloneRuleReadme)
	passGit(t, src, "add", "README.md")
	passGit(t, src, "commit", "-q", "-m", "base")
	writeTestFile(t, filepath.Join(src, "new.txt"), cloneRuleChange)
	passGit(t, src, "add", "new.txt")
	passGit(t, src, "commit", "-q", "-m", "the PR's change")
	return src, strings.TrimSpace(passGitOut(t, src, "rev-parse", "HEAD"))
}

// newCloneRulePullServer serves GET /repos/acme/widget/pulls/7 with the given
// head sha and clone URL. Any other request fails the test.
func newCloneRulePullServer(t *testing.T, sha, cloneURL string) *github.APIClient {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/widget/pulls/7" {
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"head":{"sha":%q,"repo":{"clone_url":%q}}}`, sha, cloneURL)
	}))
	t.Cleanup(srv.Close)
	return &github.APIClient{HTTPClient: srv.Client(), BaseURL: srv.URL}
}

// installCloneRuleFakes sets up the watch seams with the real fetch and the
// real destroy behind the rollback, and a claude on PATH that names no live
// session.
func installCloneRuleFakes(t *testing.T) (string, *watchCharFakes) {
	t.Helper()
	root, _ := setupWatchCharEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fakes := installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)
	fetchPRHeadFunc = watch.FetchPRHead // installWatchCharFakes restores the original
	return root, fakes
}

// cloneGitFacts is what git says about a fetched pr-clone. It's read without
// git status, which can refresh the index.
type cloneGitFacts struct {
	shallowFile, headFile, refs, remotes, isShallow string
}

func readCloneGitFacts(t *testing.T, clone string) cloneGitFacts {
	t.Helper()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(clone, ".git", name))
		if err != nil {
			t.Errorf("reading pr-clone/.git/%s: %v", name, err)
		}
		return string(b)
	}
	return cloneGitFacts{
		shallowFile: read("shallow"),
		headFile:    read("HEAD"),
		refs:        passGitOut(t, clone, "for-each-ref"),
		remotes:     passGitOut(t, clone, "remote"),
		isShallow:   passGitOut(t, clone, "rev-parse", "--is-shallow-repository"),
	}
}

func assertHandledHead(t *testing.T, root, want string) {
	t.Helper()
	handled, err := watch.LoadHandledSet(root)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := handled[watch.HandledIdentity(watchCharPR.Owner, watchCharPR.Repo, watchCharPR.Number)]
	if want == "" {
		if ok {
			t.Errorf("handled set records %q for the PR; want no entry, so the next pass stages it again", got)
		}
		return
	}
	if got != want {
		t.Errorf("handled set records %q for the PR, want its head %s", got, want)
	}
}

// TestWatchCloneRule_ARealShallowCloneKeepsTheFailedStage pins the clone
// FetchPRHead leaves (shallow at the PR's head, HEAD detached there, no ref
// and no remote) and that the rule keeps it on the repository check: the clone
// has no clone URL to prove its commit against, so the check keeps before it
// reads any object, and the shallow history never matters.
func TestWatchCloneRule_ARealShallowCloneKeepsTheFailedStage(t *testing.T) {
	root, fakes := installCloneRuleFakes(t)
	src, tip := cloneRuleSource(t)
	var before cloneGitFacts
	launches := 0
	dispatchLaunch = func(_ context.Context, req launchRequest) error { // installWatchCharFakes restores the original
		launches++
		before = readCloneGitFacts(t, filepath.Join(req.InstanceDir, watch.DefaultCloneRelDir))
		return errors.New("launch boom")
	}
	client := newCloneRulePullServer(t, tip, "file://"+src)
	cmd, stdout, stderr := newWatchCharCmd()

	err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
	if err == nil || err.Error() != "launching review agent: launch boom" {
		t.Fatalf("stageReview returned %v; want the launch error\nstderr:\n%s", err, stderr.String())
	}
	if launches != 1 {
		t.Fatalf("launches %d, want 1", launches)
	}
	want := cloneGitFacts{
		shallowFile: tip + "\n",
		headFile:    tip + "\n",
		refs:        "",
		remotes:     "",
		isShallow:   "true\n",
	}
	if before != want {
		t.Errorf("the fetched clone before the rule: %+v\nwant: %+v", before, want)
	}

	inst := fakes.provisioned.path
	clone := filepath.Join(inst, watch.DefaultCloneRelDir)
	if stdout.String() != "" {
		t.Errorf("stdout: %q, want none", stdout.String())
	}
	for _, line := range []string{
		"niwa: kept " + filepath.Base(inst) + ": repository check: pr-clone: clone URL unreachable: no clone URL for this repository\n",
		"niwa watch: kept the failed stage's instance for acme/widget#7; its PR head " + tip + " won't be staged again until it moves\n",
	} {
		if !strings.Contains(stderr.String(), line) {
			t.Errorf("stderr does not carry %q:\n%s", line, stderr.String())
		}
	}
	assertInstanceListed(t, root, inst)
	assertFileUnchanged(t, filepath.Join(clone, "README.md"), cloneRuleReadme)
	assertFileUnchanged(t, filepath.Join(clone, "new.txt"), cloneRuleChange)
	if after := readCloneGitFacts(t, clone); after != want {
		t.Errorf("the kept clone after the rule: %+v\nwant: %+v", after, want)
	}
	assertHandledHead(t, root, tip)
}

// TestWatchCloneRule_APRCloneWithNoGitDirKeepsOnItsFiles pins the fallback
// when pr-clone has no .git: FetchPRHead's last step renames its git dir into
// pr-clone best effort, and a rename across filesystems fails silently. In a
// test the scratch dir and the instance share $TMPDIR, so the rename always
// succeeds; the launch stub removes pr-clone/.git to stand in for the failure.
// With no repository there, the outside check keeps the instance on the PR's
// files and nothing is removed.
func TestWatchCloneRule_APRCloneWithNoGitDirKeepsOnItsFiles(t *testing.T) {
	root, fakes := installCloneRuleFakes(t)
	src, tip := cloneRuleSource(t)
	dispatchLaunch = func(_ context.Context, req launchRequest) error { // installWatchCharFakes restores the original
		dotGit := filepath.Join(req.InstanceDir, watch.DefaultCloneRelDir, ".git")
		if fi, err := os.Stat(dotGit); err != nil || !fi.IsDir() {
			t.Errorf("the real fetch left no pr-clone/.git directory (stat %v)", err)
		}
		if err := os.RemoveAll(dotGit); err != nil {
			t.Fatalf("removing pr-clone/.git: %v", err)
		}
		return errors.New("launch boom")
	}
	client := newCloneRulePullServer(t, tip, "file://"+src)
	cmd, stdout, stderr := newWatchCharCmd()

	err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
	if err == nil || err.Error() != "launching review agent: launch boom" {
		t.Fatalf("stageReview returned %v; want the launch error\nstderr:\n%s", err, stderr.String())
	}
	inst := fakes.provisioned.path
	clone := filepath.Join(inst, watch.DefaultCloneRelDir)
	if stdout.String() != "" {
		t.Errorf("stdout: %q, want none", stdout.String())
	}
	for _, want := range []string{
		"niwa: kept " + filepath.Base(inst) + ": outside check: files outside every repository: ",
		"pr-clone/README.md",
		"pr-clone/new.txt",
		"niwa watch: kept the failed stage's instance for acme/widget#7; its PR head " + tip + " won't be staged again until it moves\n",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr does not carry %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), "repository check") {
		t.Errorf("stderr names the repository check with no repository in the instance:\n%s", stderr.String())
	}
	assertInstanceListed(t, root, inst)
	assertFileUnchanged(t, filepath.Join(clone, "README.md"), cloneRuleReadme)
	assertFileUnchanged(t, filepath.Join(clone, "new.txt"), cloneRuleChange)
	if _, err := os.Stat(filepath.Join(inst, ".claude", "settings.json")); err != nil {
		t.Errorf("the review settings are gone from the kept instance: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(clone, ".git")); !os.IsNotExist(err) {
		t.Errorf("pr-clone/.git is back after the rollback (lstat %v)", err)
	}
	assertHandledHead(t, root, tip)
}

// TestWatchCloneRule_AFetchThatFailsLeavesAnEmptyCloneAndNoInstance pins the
// deliberate go-on with nothing to lose: a fetch of a sha the source doesn't
// hold fails before checkout and leaves pr-clone empty. Everything else in the
// instance is niwa's own state, so the rule passes it and the rollback
// destroys it. The PR isn't recorded handled, so a later pass stages it again.
func TestWatchCloneRule_AFetchThatFailsLeavesAnEmptyCloneAndNoInstance(t *testing.T) {
	root, fakes := installCloneRuleFakes(t)
	src, _ := cloneRuleSource(t)
	launches := 0
	dispatchLaunch = func(context.Context, launchRequest) error { // installWatchCharFakes restores the original
		launches++
		return errors.New("launch boom")
	}
	const missing = "fedcba9876543210fedcba9876543210fedcba98"
	cloneEntries := -1
	fetchPRHeadFunc = func(ctx context.Context, remoteURL, sha, checkoutDir, token string) error { // installWatchCharFakes restores the original
		ferr := watch.FetchPRHead(ctx, remoteURL, sha, checkoutDir, token)
		entries, err := os.ReadDir(checkoutDir)
		if err != nil {
			t.Errorf("reading pr-clone after the failed fetch: %v", err)
		}
		cloneEntries = len(entries)
		return ferr
	}
	client := newCloneRulePullServer(t, missing, "file://"+src)
	cmd, stdout, stderr := newWatchCharCmd()

	err := stageReview(cmd, root, root, "", client, watchCharPR, reviewPlan{})
	if err == nil || !strings.HasPrefix(err.Error(), "fetching PR head: fetch: git fetch "+missing+": ") {
		t.Fatalf("stageReview returned %v; want the fetch error for %s", err, missing)
	}
	if cloneEntries != 0 {
		t.Errorf("pr-clone held %d entries after the failed fetch, want it empty", cloneEntries)
	}
	if launches != 0 {
		t.Errorf("launches %d after a failed fetch, want 0", launches)
	}
	if stdout.String() != "" || stderr.String() != "" {
		t.Errorf("stdout %q, stderr %q; want neither, the rule keeps nothing", stdout.String(), stderr.String())
	}
	assertInstanceGone(t, fakes.provisioned.path)
	assertHandledHead(t, root, "")
}
