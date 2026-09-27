package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests build real repositories: a bare "remote", the instance's clone
// of it, and a second clone standing in for the forge and for other people,
// which is where squash merges land and remote branches get deleted. The
// instance's clone is never fetched by the test after the merge, so every
// case also covers destroy refreshing the default branch itself.

// durableFixture is one instance with one repo, plus the "other" clone.
type durableFixture struct {
	t           *testing.T
	instanceDir string
	repo        string // the instance's clone
	bare        string // the remote
	other       string // a second clone of the remote
}

func newDurableFixture(t *testing.T) *durableFixture {
	t.Helper()
	instanceDir, repo := setupInstanceWithRepo(t, "alpha", "myrepo")
	f := &durableFixture{t: t, instanceDir: instanceDir, repo: repo, bare: repo + ".bare"}
	f.other = filepath.Join(t.TempDir(), "other")
	gitRun(t, filepath.Dir(f.other), "clone", "-q", f.bare, f.other)
	return f
}

// commit writes name=content in dir and commits it.
func (f *durableFixture) commit(dir, name, content, msg string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	gitRun(f.t, dir, "add", name)
	gitRun(f.t, dir, "commit", "-q", "-m", msg)
}

// squashMerge lands the remote branch on the remote's main as one new
// commit, the way a forge's squash merge does.
func (f *durableFixture) squashMerge(branch string) {
	f.t.Helper()
	gitRun(f.t, f.other, "fetch", "-q", "origin")
	gitRun(f.t, f.other, "checkout", "-q", "main")
	gitRun(f.t, f.other, "reset", "-q", "--hard", "origin/main")
	gitRun(f.t, f.other, "merge", "-q", "--squash", "origin/"+branch)
	gitRun(f.t, f.other, "commit", "-q", "-m", "squash "+branch)
	gitRun(f.t, f.other, "push", "-q", "origin", "main")
}

// deleteRemoteBranch deletes a branch on the remote, as a forge does after a
// merge or a person does to clean up.
func (f *durableFixture) deleteRemoteBranch(branch string) {
	f.t.Helper()
	gitRun(f.t, f.other, "push", "-q", "origin", "--delete", branch)
}

// pushToMainFromOther adds a commit to the remote's main from the other
// clone.
func (f *durableFixture) pushToMainFromOther(name, content, msg string) {
	f.t.Helper()
	gitRun(f.t, f.other, "fetch", "-q", "origin")
	gitRun(f.t, f.other, "checkout", "-q", "main")
	gitRun(f.t, f.other, "reset", "-q", "--hard", "origin/main")
	f.commit(f.other, name, content, msg)
	gitRun(f.t, f.other, "push", "-q", "origin", "main")
}

// asGitHub makes the instance clone's origin look like a github.com remote
// while still reaching the local bare repo, so the scan asks the forge about
// it. insteadOf rewrites the URL for every git operation; the configured
// remote.origin.url, which the scan reads to identify the repository, stays
// the GitHub one.
func (f *durableFixture) asGitHub() {
	f.t.Helper()
	const ghURL = "https://github.com/acme/widget.git"
	gitRun(f.t, f.repo, "config", "url."+f.bare+".insteadOf", ghURL)
	gitRun(f.t, f.repo, "remote", "set-url", "origin", ghURL)
}

func (f *durableFixture) scan(opts ...ScanOption) InstanceScan {
	f.t.Helper()
	opts = append([]ScanOption{WithNetworkTimeout(10 * time.Second)}, opts...)
	s, err := ScanInstance(f.instanceDir, opts...)
	if err != nil {
		f.t.Fatalf("ScanInstance: %v", err)
	}
	return s
}

// fakeForge answers HeadPR from a map, and records what it was asked.
type fakeForge struct {
	prs   map[string]HeadPR
	err   error
	asked []string
}

func (f *fakeForge) HeadPR(_ context.Context, owner, repo, branch string) (HeadPR, error) {
	f.asked = append(f.asked, owner+"/"+repo+":"+branch)
	if f.err != nil {
		return HeadPR{}, f.err
	}
	if pr, ok := f.prs[branch]; ok {
		return pr, nil
	}
	return HeadPR{State: HeadPRNone}, nil
}

func allLosses(s InstanceScan) []Loss {
	var out []Loss
	for _, r := range s.Repos {
		out = append(out, r.Losses...)
		if r.Skipped != "" {
			out = append(out, Loss{Kind: "skipped", Detail: r.Skipped})
		}
	}
	return out
}

func branchLosses(s InstanceScan, branch string) []Loss {
	var out []Loss
	for _, l := range allLosses(s) {
		if l.Branch == branch {
			out = append(out, l)
		}
	}
	return out
}

// requireClean fails unless the scan found nothing at all.
func requireClean(t *testing.T, s InstanceScan) {
	t.Helper()
	if s.HasLoss() {
		t.Fatalf("expected no findings, got %+v", allLosses(s))
	}
}

// requireBranchLoss fails unless branch has exactly one finding of kind whose
// detail contains every fragment. The finding is returned.
func requireBranchLoss(t *testing.T, s InstanceScan, branch string, kind LossKind, fragments ...string) Loss {
	t.Helper()
	ls := branchLosses(s, branch)
	if len(ls) != 1 {
		t.Fatalf("expected one finding for %s, got %+v (all: %+v)", branch, ls, allLosses(s))
	}
	l := ls[0]
	if l.Kind != kind {
		t.Errorf("finding for %s: kind %q, want %q (detail %q)", branch, l.Kind, kind, l.Detail)
	}
	for _, frag := range fragments {
		if !strings.Contains(l.Detail, frag) {
			t.Errorf("finding for %s: detail %q lacks %q", branch, l.Detail, frag)
		}
	}
	return l
}

// --- squash-merged work that is fully landed passes ---

// A session worktree branch (created with `worktree add -b`, so it has no
// upstream) pushed without -u and squash-merged. The old scan called this
// local-only and blocked.
func TestDurable_SquashMergedNoUpstreamPasses(t *testing.T) {
	f := newDurableFixture(t)
	wt := filepath.Join(f.instanceDir, ".niwa", "worktrees", "myrepo-s1")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.repo, "worktree", "add", "-q", "-b", "session/s1", wt)
	f.commit(wt, "a.txt", "a\n", "a")
	f.commit(wt, "b.txt", "b\n", "b")
	gitRun(t, wt, "push", "-q", "origin", "session/s1")
	f.squashMerge("session/s1")

	requireClean(t, f.scan())
}

// A branch cut from origin/main (so its upstream is origin/main, which
// autoSetupMerge does) pushed to its own remote branch, squash-merged, and
// the remote branch deleted, as a forge does after merging. The old scan
// saw it "ahead" of origin/main forever and blocked.
func TestDurable_SquashMergedUpstreamMainBranchDeletedPasses(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat", "--track", "origin/main")
	f.commit(f.repo, "f.txt", "one\n", "f1")
	f.commit(f.repo, "f.txt", "two\n", "f2")
	gitRun(t, f.repo, "push", "-q", "origin", "feat")
	f.squashMerge("feat")
	f.deleteRemoteBranch("feat")
	gitRun(t, f.repo, "checkout", "-q", "main")

	requireClean(t, f.scan())
}

// After the squash merge, main keeps moving and rewrites the very line the
// branch added. Merging the branch into main's current tip conflicts, so a
// tip-only content test would block; the scan still finds the squash commit
// on main's first-parent history and passes.
func TestDurable_SquashMergedThenMainEditsSameLinePasses(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "branch line\n", "f1")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.squashMerge("feat")
	f.deleteRemoteBranch("feat")
	f.pushToMainFromOther("f.txt", "rewritten on main\n", "rewrite")

	requireClean(t, f.scan())
}

// --- work that could be lost blocks ---

// Squash-merged, then another commit made locally and never pushed. The
// remote branch is gone, and the new commit isn't in the squash.
func TestDurable_SquashMergedPlusUnpushedCommitBlocks(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "one\n", "f1")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.squashMerge("feat")
	f.deleteRemoteBranch("feat")
	f.commit(f.repo, "late.txt", "late\n", "after the merge")

	requireBranchLoss(t, f.scan(), "feat", LossUnpushedCommits,
		"not on origin/main", "origin/feat no longer exists")
}

// Squash-merged, then a commit pushed to the still-live head branch after the
// merge. The commit is on a remote, but only on a branch whose PR merged, so
// it disappears when that branch is cleaned up.
func TestDurable_PostMergePushToMergedBranchBlocks(t *testing.T) {
	f := newDurableFixture(t)
	f.asGitHub()
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "one\n", "f1")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.squashMerge("feat")
	f.commit(f.repo, "late.txt", "late\n", "after the merge")
	gitRun(t, f.repo, "push", "-q", "origin", "feat")

	forge := &fakeForge{prs: map[string]HeadPR{"feat": {State: HeadPRMerged, Number: 7}}}
	requireBranchLoss(t, f.scan(WithForge(forge)), "feat", LossUnlanded,
		"1 commit not on origin/main", "PR #7 merged", "origin/feat is due for deletion")
	if len(forge.asked) == 0 || forge.asked[0] != "acme/widget:feat" {
		t.Errorf("forge asked %v, want acme/widget:feat", forge.asked)
	}
}

// Pushed to a remote branch, never merged, and the remote branch deleted.
// The commits exist nowhere but this clone. The old scan passed this: after a
// prune the upstream reads "[gone]" rather than "ahead".
func TestDurable_PushedThenRemoteBranchDeletedBlocks(t *testing.T) {
	for _, prune := range []bool{true, false} {
		name := "stale-tracking-ref"
		if prune {
			name = "pruned"
		}
		t.Run(name, func(t *testing.T) {
			f := newDurableFixture(t)
			gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
			f.commit(f.repo, "f.txt", "work\n", "work")
			gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
			f.deleteRemoteBranch("feat")
			if prune {
				gitRun(t, f.repo, "fetch", "-q", "--prune", "origin")
			}
			gitRun(t, f.repo, "checkout", "-q", "main")

			requireBranchLoss(t, f.scan(), "feat", LossUnpushedCommits,
				"1 commit not on origin/main", "origin/feat no longer exists")
		})
	}
}

// The coordinator's tightening: a live remote branch vouched for by the forge
// only covers the commits it actually holds. Local commits beyond its tip are
// unpushed.
func TestDurable_LocalCommitsBeyondLiveRemoteBranchBlock(t *testing.T) {
	f := newDurableFixture(t)
	f.asGitHub()
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "one\n", "f1")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.commit(f.repo, "g.txt", "two\n", "f2")
	f.commit(f.repo, "h.txt", "three\n", "f3")

	forge := &fakeForge{prs: map[string]HeadPR{"feat": {State: HeadPROpen, Number: 3}}}
	requireBranchLoss(t, f.scan(WithForge(forge)), "feat", LossUnpushedCommits,
		"3 commits not on origin/main", "2 commits beyond origin/feat not pushed")
}

// --- the forge can't vouch: conservative, and says so ---

func TestDurable_LiveRemoteBranchForgeAnswers(t *testing.T) {
	setup := func(t *testing.T, github bool) *durableFixture {
		f := newDurableFixture(t)
		if github {
			f.asGitHub()
		}
		gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
		f.commit(f.repo, "f.txt", "in review\n", "f1")
		gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
		return f
	}

	t.Run("open PR passes", func(t *testing.T) {
		f := setup(t, true)
		requireClean(t, f.scan(WithForge(&fakeForge{prs: map[string]HeadPR{"feat": {State: HeadPROpen, Number: 5}}})))
	})
	t.Run("no PR passes", func(t *testing.T) {
		f := setup(t, true)
		requireClean(t, f.scan(WithForge(&fakeForge{})))
	})
	t.Run("closed PR blocks", func(t *testing.T) {
		f := setup(t, true)
		requireBranchLoss(t, f.scan(WithForge(&fakeForge{prs: map[string]HeadPR{"feat": {State: HeadPRClosed, Number: 9}}})),
			"feat", LossUnlanded, "PR #9 was closed without merging")
	})
	t.Run("forge error blocks as unverified", func(t *testing.T) {
		f := setup(t, true)
		requireBranchLoss(t, f.scan(WithForge(&fakeForge{err: errors.New("dial tcp: no route to host")})),
			"feat", LossUnverified, "checking its PR state on GitHub failed", "no route to host")
	})
	t.Run("no forge client blocks as unverified", func(t *testing.T) {
		f := setup(t, true)
		requireBranchLoss(t, f.scan(), "feat", LossUnverified, "no GitHub client was available")
	})
	t.Run("non-GitHub remote blocks as unverified", func(t *testing.T) {
		f := setup(t, false)
		forge := &fakeForge{}
		requireBranchLoss(t, f.scan(WithForge(forge)), "feat", LossUnverified, "isn't a GitHub remote")
		if len(forge.asked) != 0 {
			t.Errorf("forge was asked about a non-GitHub remote: %v", forge.asked)
		}
	})
}

// The remote can't be reached at all. Nothing the scan would need can be
// checked; the branch blocks and the finding names the cause.
func TestDurable_UnreachableRemoteBlocks(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "work\n", "work")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	gitRun(t, f.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))

	requireBranchLoss(t, f.scan(WithForge(&fakeForge{})), "feat", LossUnverified, "couldn't reach origin")
}

// The remote branch moved on from another clone, so its tip isn't in this
// clone and ancestry can't be checked. Conservative.
func TestDurable_RemoteBranchAdvancedElsewhereIsUnverified(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "mine\n", "mine")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.commit(f.repo, "g.txt", "local only\n", "local")
	gitRun(t, f.other, "fetch", "-q", "origin")
	gitRun(t, f.other, "checkout", "-q", "-b", "feat", "origin/feat")
	f.commit(f.other, "h.txt", "theirs\n", "theirs")
	gitRun(t, f.other, "push", "-q", "origin", "feat")

	requireBranchLoss(t, f.scan(WithForge(&fakeForge{})), "feat", LossUnverified, "hasn't fetched")
}

// --- working-tree state stays protected ---

// A dirty linked worktree blocks even when every branch is landed.
func TestDurable_DirtyLinkedWorktreeBlocks(t *testing.T) {
	f := newDurableFixture(t)
	wt := filepath.Join(f.instanceDir, ".niwa", "worktrees", "myrepo-s1")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.repo, "worktree", "add", "-q", "-b", "session/s1", wt)
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := f.scan()
	found := false
	for _, l := range allLosses(s) {
		if l.Kind == LossWorkingTreeDirty && l.Path != "" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a dirty finding in the linked worktree, got %+v", allLosses(s))
	}
}

// A repo configured to hide untracked files from `git status` still has them
// reported.
func TestDurable_UntrackedFilesHiddenByConfigStillBlock(t *testing.T) {
	f := newDurableFixture(t)
	gitRun(t, f.repo, "config", "status.showUntrackedFiles", "no")
	if err := os.WriteFile(filepath.Join(f.repo, "notes.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := f.scan()
	for _, l := range allLosses(s) {
		if l.Kind == LossUntracked {
			return
		}
	}
	t.Fatalf("expected an untracked finding, got %+v", allLosses(s))
}

// A stash made in a linked worktree is reported once: stashes belong to the
// repo, not to a working tree.
func TestDurable_StashReportedOnce(t *testing.T) {
	f := newDurableFixture(t)
	wt := filepath.Join(f.instanceDir, ".niwa", "worktrees", "myrepo-s1")
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, f.repo, "worktree", "add", "-q", "-b", "session/s1", wt)
	if err := os.WriteFile(filepath.Join(wt, "README.md"), []byte("stash me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, wt, "stash", "push", "-q")

	n := 0
	for _, l := range allLosses(f.scan()) {
		if l.Kind == LossStash {
			n++
			if l.Detail != "1 stash entries" {
				t.Errorf("stash detail %q", l.Detail)
			}
		}
	}
	if n != 1 {
		t.Fatalf("expected exactly one stash finding, got %d", n)
	}
}

// A git failure is a finding, not a silent pass.
func TestDurable_GitErrorBlocks(t *testing.T) {
	f := newDurableFixture(t)
	// A HEAD that isn't a valid ref makes git refuse the repository.
	if err := os.WriteFile(filepath.Join(f.repo, ".git", "HEAD"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Every check that runs git fails here; require the working-tree status
	// check's own finding, since that's the one that used to be dropped.
	s := f.scan()
	for _, l := range allLosses(s) {
		if l.Kind == LossScanError && strings.Contains(l.Detail, "git status failed") {
			return
		}
	}
	t.Fatalf("expected a scan-error finding from git status, got %+v", allLosses(s))
}

// Without merge-tree (git older than 2.38) a squash merge can't be
// recognised. The branch blocks, and the finding says why rather than
// presenting the work as simply unmerged.
func TestDurable_OldGitCantRecogniseSquashAndSaysSo(t *testing.T) {
	orig := supportsMergeTree
	supportsMergeTree = func() bool { return false }
	t.Cleanup(func() { supportsMergeTree = orig })

	f := newDurableFixture(t)
	gitRun(t, f.repo, "checkout", "-q", "-b", "feat")
	f.commit(f.repo, "f.txt", "one\n", "f1")
	gitRun(t, f.repo, "push", "-q", "-u", "origin", "feat")
	f.squashMerge("feat")
	f.deleteRemoteBranch("feat")

	requireBranchLoss(t, f.scan(), "feat", LossUnpushedCommits, "git is older than 2.38")
}

// --- pieces ---

func TestParseGitVersion(t *testing.T) {
	cases := []struct {
		in           string
		major, minor int
		ok           bool
	}{
		{"git version 2.43.0\n", 2, 43, true},
		{"git version 2.39.3 (Apple Git-146)\n", 2, 39, true},
		{"git version 2.34.1", 2, 34, true},
		{"nonsense", 0, 0, false},
	}
	for _, c := range cases {
		major, minor, ok := parseGitVersion(c.in)
		if major != c.major || minor != c.minor || ok != c.ok {
			t.Errorf("parseGitVersion(%q) = %d, %d, %v; want %d, %d, %v", c.in, major, minor, ok, c.major, c.minor, c.ok)
		}
	}
}

func TestGithubOwnerRepo(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"https://github.com/acme/widget.git", "acme", "widget", true},
		{"git@github.com:acme/widget.git", "acme", "widget", true},
		{"ssh://git@github.com/acme/widget", "acme", "widget", true},
		{"https://gitlab.com/acme/widget.git", "", "", false},
		{"git@gitlab.example.com:acme/widget.git", "", "", false},
		{"/srv/git/widget.git", "", "", false},
		{"file:///srv/git/widget.git", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		owner, repo, ok := githubOwnerRepo(c.in)
		if owner != c.owner || repo != c.repo || ok != c.ok {
			t.Errorf("githubOwnerRepo(%q) = %q, %q, %v; want %q, %q, %v", c.in, owner, repo, ok, c.owner, c.repo, c.ok)
		}
	}
}
