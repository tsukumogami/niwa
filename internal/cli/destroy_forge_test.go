package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// forkForgeServer fakes GitHub for a repository acme/widget that is a fork of
// upstream/widget. Asked about its own pull requests, the fork has none: the
// PR for the branch lives, merged, in the parent. That is the answer a lookup
// against the fork gets, and it's wrong. Requests are recorded.
func forkForgeServer(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/repos/acme/widget":
			_, _ = io.WriteString(w, `{"fork": true, "parent": {"full_name": "upstream/widget"}}`)
		case "/repos/acme/widget/pulls":
			_, _ = io.WriteString(w, `[]`)
		case "/repos/upstream/widget/pulls":
			_, _ = io.WriteString(w, `[{"number": 5, "state": "closed", "merged_at": "2026-09-01T00:00:00Z", "head": {"ref": "feat", "sha": "x"}}]`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
}

// forkFixture builds an instance whose repo's origin is the GitHub fork
// acme/widget, reached through insteadOf at a local bare repo, with a branch
// feat pushed to it. It returns the instance dir, the clone, and a second
// clone of the remote.
func forkFixture(t *testing.T) (instanceDir, repo, other string) {
	t.Helper()
	root := destroyTestSetup(t, []string{"alpha"})
	instanceDir = filepath.Join(root, "alpha")
	repo = filepath.Join(instanceDir, "myrepo")
	scanInitGitRepoForCLITest(t, repo)
	bare := repo + ".bare"
	const ghURL = "https://github.com/acme/widget.git"
	gitRunForCLITest(t, repo, "config", "url."+bare+".insteadOf", ghURL)
	gitRunForCLITest(t, repo, "remote", "set-url", "origin", ghURL)

	gitRunForCLITest(t, repo, "checkout", "-q", "-b", "feat")
	if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRunForCLITest(t, repo, "add", "f.txt")
	gitRunForCLITest(t, repo, "commit", "-q", "-m", "work")
	gitRunForCLITest(t, repo, "push", "-q", "-u", "origin", "feat")
	gitRunForCLITest(t, repo, "checkout", "-q", "main")

	other = filepath.Join(t.TempDir(), "other")
	gitRunForCLITest(t, filepath.Dir(other), "clone", "-q", bare, other)
	return instanceDir, repo, other
}

func scanWithDestroyForge(t *testing.T, instanceDir, apiURL string) workspace.InstanceScan {
	t.Helper()
	t.Setenv("NIWA_GITHUB_API_URL", apiURL)
	t.Setenv("GITHUB_TOKEN", "test-token")
	s, err := workspace.ScanInstance(instanceDir,
		workspace.WithForge(newDestroyForge()), workspace.WithNetworkTimeout(10*time.Second))
	if err != nil {
		t.Fatalf("ScanInstance: %v", err)
	}
	return s
}

// A fork's branch is never passed on the strength of the fork's own (empty)
// PR list. Only the forge-free tests can pass it: ancestry or content on the
// default branch.
func TestDestroyForge_ForkRemoteNeverPassesOnForkLookup(t *testing.T) {
	t.Run("live branch, parent PR merged: blocked as unverified", func(t *testing.T) {
		srv, requests := forkForgeServer(t)
		instanceDir, _, _ := forkFixture(t)

		s := scanWithDestroyForge(t, instanceDir, srv.URL)
		var found *workspace.Loss
		for _, r := range s.Repos {
			for i, l := range r.Losses {
				if l.Branch == "feat" {
					found = &r.Losses[i]
				}
			}
		}
		if found == nil {
			t.Fatalf("expected feat to be blocked, got %+v", s.Repos)
		}
		if found.Kind != workspace.LossUnverified || !strings.Contains(found.Detail, "is a fork of upstream/widget") {
			t.Errorf("finding %+v, want unverified naming the fork", *found)
		}
		for _, p := range requests() {
			if p == "/repos/acme/widget/pulls" {
				t.Errorf("the fork's own PR list was consulted: %v", requests())
			}
		}
	})

	t.Run("squash-merged into the default branch: passes by content, forge not consulted", func(t *testing.T) {
		srv, requests := forkForgeServer(t)
		instanceDir, _, other := forkFixture(t)
		gitRunForCLITest(t, other, "merge", "-q", "--squash", "origin/feat")
		gitRunForCLITest(t, other, "commit", "-q", "-m", "squash feat")
		gitRunForCLITest(t, other, "push", "-q", "origin", "main")

		s := scanWithDestroyForge(t, instanceDir, srv.URL)
		if s.HasLoss() {
			t.Fatalf("expected no findings, got %+v", s.Repos)
		}
		if got := requests(); len(got) != 0 {
			t.Errorf("forge consulted for a branch the content test passes: %v", got)
		}
	})
}

func TestClassifyHeadPulls(t *testing.T) {
	merged := func(n int) github.PullByHead {
		return github.PullByHead{Number: n, State: "closed", MergedAt: "2026-09-01T00:00:00Z"}
	}
	closed := func(n int) github.PullByHead { return github.PullByHead{Number: n, State: "closed"} }
	open := func(n int) github.PullByHead { return github.PullByHead{Number: n, State: "open"} }

	cases := []struct {
		name  string
		pulls []github.PullByHead
		want  workspace.HeadPR
	}{
		{"none", nil, workspace.HeadPR{State: workspace.HeadPRNone}},
		{"open", []github.PullByHead{open(3)}, workspace.HeadPR{State: workspace.HeadPROpen, Number: 3}},
		{"merged", []github.PullByHead{merged(7)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 7}},
		{"closed", []github.PullByHead{closed(2)}, workspace.HeadPR{State: workspace.HeadPRClosed, Number: 2}},
		// A branch name reused after an earlier merge: the open PR keeps it alive.
		{"open beats merged", []github.PullByHead{merged(7), open(12)}, workspace.HeadPR{State: workspace.HeadPROpen, Number: 12}},
		{"merged beats closed", []github.PullByHead{closed(9), merged(4)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 4}},
		{"latest merged named", []github.PullByHead{merged(4), merged(8)}, workspace.HeadPR{State: workspace.HeadPRMerged, Number: 8}},
	}
	for _, c := range cases {
		if got := classifyHeadPulls(c.pulls); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
