package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tsukumogami/niwa/internal/watch"
)

// A review instance the removal rule kept, followed through the passes after
// it. The handled set is the only thing that stops a pass staging a PR again,
// and nothing clears it when the kept instance is destroyed, so a new stage
// waits for the PR's head to move. The test mutates package globals and the
// working directory, so it doesn't run in parallel.

// keptRestageGitHub serves what a pass asks GitHub for: the login, the
// review-requested search naming acme/widget#7, and the pull lookup, whose
// head the test moves. Any other request fails the test.
type keptRestageGitHub struct {
	mu   sync.Mutex
	head string
	url  string
}

func newKeptRestageGitHub(t *testing.T, head string) *keptRestageGitHub {
	t.Helper()
	g := &keptRestageGitHub{head: head}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/user":
			fmt.Fprint(w, `{"login":"reviewer"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/search/issues":
			repoURL := g.url + "/repos/acme/widget"
			fmt.Fprintf(w, `{"total_count":1,"items":[{"number":7,"html_url":%q,"created_at":"2026-01-01T00:00:00Z","repository_url":%q,"pull_request":{"url":%q}}]}`,
				watchCharPRURL, repoURL, repoURL+"/pulls/7")
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/widget/pulls/7":
			g.mu.Lock()
			sha := g.head
			g.mu.Unlock()
			fmt.Fprintf(w, `{"head":{"sha":%q,"repo":{"clone_url":%q}}}`, sha, watchCharCloneURL)
		default:
			t.Errorf("unexpected GitHub request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	g.url = srv.URL
	return g
}

func (g *keptRestageGitHub) setHead(sha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.head = sha
}

// readHandledFile returns the handled set file's bytes.
func readHandledFile(t *testing.T, root string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".niwa", "watch-handled"))
	if err != nil {
		t.Fatalf("reading the handled set: %v", err)
	}
	return string(b)
}

func assertReviewInstanceStaged(t *testing.T, root, inst string) {
	t.Helper()
	assertInstanceListed(t, root, inst)
	if _, err := os.Stat(filepath.Join(inst, watch.DefaultCloneRelDir, ".git")); err != nil {
		t.Errorf("pr-clone's git directory isn't there: %v", err)
	}
	assertFileUnchanged(t, filepath.Join(inst, watch.DefaultDraftRelPath), keptRestageDraft)
}

const keptRestageDraft = "the review agent's draft\n"

// TestRunWatchOnce_AKeptReviewInstanceIsStagedAgainOnlyOnceItsHeadMoves pins
// four passes over one PR. Pass 1 stages a review instance at head A and its
// record save fails, so the rollback asks the removal rule, which keeps the
// instance for pr-clone's commit and records head A as handled. Pass 2 at the
// same head stages nothing. `niwa destroy --force` then removes the kept
// instance and leaves the handled set byte for byte as it was, so a pass at
// head A after the destroy still stages nothing. Pass 3, once the head moves
// to B, stages a new instance and moves the handled entry to B.
func TestRunWatchOnce_AKeptReviewInstanceIsStagedAgainOnlyOnceItsHeadMoves(t *testing.T) {
	root, _ := setupWatchCharEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	installWatchCharFakes(t)
	destroyInstanceFunc = realDestroyInstance // installWatchCharFakes restores the original
	claudeListingOnPath(t)

	appendWorkspaceConfig(t, root, "\n[[sources]]\norg = \"acme\"\n")
	writeTestFile(t, filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "niwa", "config.toml"),
		"[global]\nwatch_sandbox = \"off\"\n")
	gh := newKeptRestageGitHub(t, watchCharHeadSHA)
	t.Setenv("NIWA_GITHUB_API_URL", gh.url)
	t.Setenv("GITHUB_TOKEN", "test-token")

	var provisioned []string
	prevProvision := provisionInstanceFunc
	provisionInstanceFunc = func(ctx context.Context, r, cwd, namePrefix, sep string, n int) (provisionResult, error) {
		res, err := prevProvision(ctx, r, cwd, namePrefix, sep, n)
		if err == nil {
			provisioned = append(provisioned, res.Path)
		}
		return res, err
	}
	t.Cleanup(func() { provisionInstanceFunc = prevProvision })
	var fetched []string
	fetchPRHeadFunc = func(_ context.Context, _, sha, checkoutDir, _ string) error { // installWatchCharFakes restores the original
		fetched = append(fetched, sha)
		fakePRClone(t, checkoutDir)
		return nil
	}
	launches := 0
	dispatchLaunch = func(_ context.Context, req launchRequest) error { // installWatchCharFakes restores the original
		launches++
		writeTestFile(t, filepath.Join(req.InstanceDir, watch.DefaultDraftRelPath), keptRestageDraft)
		return nil
	}
	saved := recordSaves(t, 1)
	chdir(t, root)

	pass := func() (string, string, error) {
		t.Helper()
		cmd, out, errOut := newWatchCharCmd()
		err := runWatchOnce(cmd, nil)
		return out.String(), errOut.String(), err
	}
	assertCounts := func(step string, wantProvisions, wantLaunches, wantSaves int, wantFetched ...string) {
		t.Helper()
		if len(provisioned) != wantProvisions || launches != wantLaunches || len(*saved) != wantSaves ||
			strings.Join(fetched, " ") != strings.Join(wantFetched, " ") {
			t.Fatalf("%s: provisions %q, launches %d, saves %d, fetches %q; want %d provisions, %d launches, %d saves, fetches %q",
				step, provisioned, launches, len(*saved), fetched, wantProvisions, wantLaunches, wantSaves, wantFetched)
		}
	}
	const handledHeader = "# niwa-watch-state v2 semantics=level\n"
	handledAtA := handledHeader + "acme/widget#7@" + watchCharHeadSHA + "\n"
	handledAtB := handledHeader + "acme/widget#7@" + watchPassHeadB + "\n"
	const handle = "watch_acme_widget_7"

	// Pass 1 at head A: the stage fails at the record save and the rule keeps
	// the instance.
	stdout, stderr, err := pass()
	if err == nil || err.Error() != "niwa watch: staging acme/widget#7: planted record write failure" {
		t.Fatalf("pass 1 returned %v; want the planted record write failure\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	assertCounts("pass 1", 1, 1, 1, watchCharHeadSHA)
	kept := provisioned[0]
	for _, want := range []string{
		"niwa: kept " + filepath.Base(kept) + ": repository check: pr-clone: clone URL unreachable: no clone URL for this repository\n",
		"niwa watch: kept the failed stage's instance for acme/widget#7; its PR head " + watchCharHeadSHA + " won't be staged again until it moves\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("pass 1 stderr does not carry %q:\n%s", want, stderr)
		}
	}
	assertReviewInstanceStaged(t, root, kept)
	if _, err := watch.LoadStagedRecord(root, handle); err == nil {
		t.Error("pass 1 left a staged record; its save failed")
	}
	if got := readHandledFile(t, root); got != handledAtA {
		t.Fatalf("handled set after pass 1:\n%s\nwant:\n%s", got, handledAtA)
	}

	// Pass 2 at the same head: nothing to stage.
	stdout, stderr, err = pass()
	if err != nil {
		t.Fatalf("pass 2: %v\nstderr:\n%s", err, stderr)
	}
	assertStdoutLine(t, stdout, "niwa watch: nothing to stage")
	if stderr != "" {
		t.Errorf("pass 2 stderr: %q, want none", stderr)
	}
	assertCounts("pass 2", 1, 1, 1, watchCharHeadSHA)
	assertReviewInstanceStaged(t, root, kept)
	if got := readHandledFile(t, root); got != handledAtA {
		t.Fatalf("handled set after pass 2:\n%s\nwant:\n%s", got, handledAtA)
	}

	// niwa destroy --force of the kept instance: the directory goes, the
	// handled set stays as it was.
	before := readHandledFile(t, root)
	setDestroyFlags(t, true, false, "")
	cmd, out, errOut := newWatchCharCmd()
	if err := runDestroy(cmd, []string{filepath.Base(kept)}); err != nil {
		t.Fatalf("destroy --force %s: %v\nstderr:\n%s", filepath.Base(kept), err, errOut.String())
	}
	assertStdoutLine(t, out.String(), "Destroyed instance: "+kept)
	if _, err := os.Lstat(kept); !os.IsNotExist(err) {
		t.Fatalf("the kept instance %s is still there after destroy --force (lstat %v)", kept, err)
	}
	if got := readHandledFile(t, root); got != before || got != handledAtA {
		t.Fatalf("handled set after destroy:\n%s\nwant it unchanged:\n%s", got, before)
	}

	// A pass at head A after the destroy still stages nothing: the destroy
	// doesn't re-arm staging.
	stdout, stderr, err = pass()
	if err != nil {
		t.Fatalf("pass after the destroy: %v\nstderr:\n%s", err, stderr)
	}
	assertStdoutLine(t, stdout, "niwa watch: nothing to stage")
	if stderr != "" {
		t.Errorf("pass after the destroy stderr: %q, want none", stderr)
	}
	assertCounts("pass after the destroy", 1, 1, 1, watchCharHeadSHA)
	if got := readHandledFile(t, root); got != handledAtA {
		t.Fatalf("handled set after the pass following the destroy:\n%s\nwant:\n%s", got, handledAtA)
	}

	// Pass 3 once the head moves to B: a fresh stage in a new instance.
	gh.setHead(watchPassHeadB)
	stdout, stderr, err = pass()
	if err != nil {
		t.Fatalf("pass 3: %v\nstderr:\n%s", err, stderr)
	}
	assertStdoutLine(t, stdout, "niwa watch: staged review for acme/widget#7 (handle watch_acme_widget_7)")
	assertCounts("pass 3", 2, 2, 2, watchCharHeadSHA, watchPassHeadB)
	fresh := provisioned[1]
	if fresh == kept {
		t.Fatalf("pass 3 staged into the destroyed instance's path %s", kept)
	}
	assertReviewInstanceStaged(t, root, fresh)
	if _, err := os.Lstat(kept); !os.IsNotExist(err) {
		t.Errorf("the destroyed instance %s is back after pass 3 (lstat %v)", kept, err)
	}
	rec, err := watch.LoadStagedRecord(root, handle)
	if err != nil {
		t.Fatalf("pass 3 saved no staged record: %v", err)
	}
	if rec.DispatchedSHA != watchPassHeadB || rec.InstancePath != fresh {
		t.Errorf("staged record at %s for %s; want head %s and instance %s", rec.DispatchedSHA, rec.InstancePath, watchPassHeadB, fresh)
	}
	if got := readHandledFile(t, root); got != handledAtB {
		t.Errorf("handled set after pass 3:\n%s\nwant:\n%s", got, handledAtB)
	}
}
