package functional

// Steps for the refresh scenario in features/mirror-repair.feature: a source
// repo that gains a branch and a commit and then drops both, and what the
// mirror must still hold afterwards. Like the steps beside them they read the
// store with git and the layout the design fixes rather than importing
// niwa's mirror package.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/cucumber/godog"
)

func registerMirrorRefreshSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I add a branch "([^"]*)" with one commit and one commit on main to source repo "([^"]*)"$`, iAddABranchAndACommitOnMain)
	ctx.Step(`^the mirror log gained only healthy check lines for "([^"]*)" from "([^"]*)" and "([^"]*)"$`, theMirrorLogGainedOnlyHealthyChecksFrom)
	ctx.Step(`^the mirror of "([^"]*)" has no branch "([^"]*)"$`, theMirrorHasNoBranch)
	ctx.Step(`^the mirror of "([^"]*)" holds the commits added to source repo "([^"]*)"$`, theMirrorHoldsTheAddedCommits)
	ctx.Step(`^the borrower of "([^"]*)" in instance "([^"]*)" has a local branch "([^"]*)" at the added topic commit$`, theBorrowerHasABranchAtTheTopicCommit)
	ctx.Step(`^source repo "([^"]*)" deletes the added branch, resets main back one commit and prunes both commits$`, theSourceRepoDropsTheAddedCommits)
	ctx.Step(`^a fresh clone of source repo "([^"]*)" holds neither added commit$`, aFreshCloneHoldsNeitherAddedCommit)
	ctx.Step(`^the mirror of "([^"]*)" keeps the commits source repo "([^"]*)" dropped$`, theMirrorKeepsTheDroppedCommits)
	ctx.Step(`^the store of "([^"]*)" holds the fixed retention config$`, theStoreHoldsTheFixedConfig)
	ctx.Step(`^the branch "([^"]*)" in the borrower of "([^"]*)" in instance "([^"]*)" still names the added topic commit$`, theBranchStillNamesTheTopicCommit)
}

// commitAndPush commits a new file named for msg in the work tree at work and
// pushes HEAD to branch.
func commitAndPush(work, branch, msg string) error {
	file := msg + ".txt"
	if err := os.WriteFile(filepath.Join(work, file), []byte(msg+"\n"), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"add", file},
		{"commit", "--quiet", "-m", msg},
		{"push", "--quiet", "origin", "HEAD:refs/heads/" + branch},
	} {
		if out, err := fixtureGitCommit(work, args...); err != nil {
			return fmt.Errorf("git %s: %w\n%s", args[0], err, out)
		}
	}
	return nil
}

// revParse is what rev names in the work tree at dir.
func revParse(dir, rev string) (string, error) {
	out, err := fixtureGitWorkTree(dir, "rev-parse", "--verify", rev)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s in %s: %w\n%s", rev, dir, err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func iAddABranchAndACommitOnMain(ctx context.Context, branch, name string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	work, err := os.MkdirTemp(s.gitServer.root, "added-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", s.repoURLs[name], work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", name, err, out)
	}
	if m.added.parent, err = revParse(work, "refs/remotes/origin/main"); err != nil {
		return ctx, err
	}
	if out, err := fixtureGitWorkTree(work, "checkout", "--quiet", "-B", branch, "refs/remotes/origin/main"); err != nil {
		return ctx, fmt.Errorf("git checkout %s: %w\n%s", branch, err, out)
	}
	if err := commitAndPush(work, branch, "added-"+branch); err != nil {
		return ctx, err
	}
	if m.added.topic, err = revParse(work, "HEAD"); err != nil {
		return ctx, err
	}
	if out, err := fixtureGitWorkTree(work, "checkout", "--quiet", "-B", "main", "refs/remotes/origin/main"); err != nil {
		return ctx, fmt.Errorf("git checkout main: %w\n%s", err, out)
	}
	if err := commitAndPush(work, "main", "added-main"); err != nil {
		return ctx, err
	}
	if m.added.tip, err = revParse(work, "HEAD"); err != nil {
		return ctx, err
	}
	m.added.branch = branch
	return ctx, nil
}

func theMirrorLogGainedOnlyHealthyChecksFrom(ctx context.Context, repoID, first, second string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	if len(lines) != 2 {
		return fmt.Errorf("the mirror log gained %d lines, want a healthy check from %s and one from %s: %v", len(lines), first, second, lines)
	}
	for i, command := range []string{first, second} {
		if err := requireLogLine(lines[i], command, repoID, "check", "", "healthy"); err != nil {
			return err
		}
	}
	return nil
}

// storeRef is what ref names in the mirror's linked store, or "" when the
// store has no such ref.
func storeRef(s *testState, repoID, ref string) (string, error) {
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return "", err
	}
	out, err := fixtureGitBare(store, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(out)), nil
}

func theMirrorHasNoBranch(ctx context.Context, repoID, branch string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	got, err := storeRef(s, repoID, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if got != "" {
		return fmt.Errorf("the mirror has refs/heads/%s at %s; nothing should have fetched it", branch, got)
	}
	return nil
}

// requireStoreRefs fails unless each ref in want names its commit in the
// mirror's store.
func requireStoreRefs(s *testState, repoID string, want map[string]string) error {
	for ref, commit := range want {
		got, err := storeRef(s, repoID, ref)
		if err != nil {
			return err
		}
		if got != commit {
			return fmt.Errorf("the mirror's %s = %q, want %s", ref, got, commit)
		}
	}
	return nil
}

func theMirrorHoldsTheAddedCommits(ctx context.Context, repoID, _ string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	return requireStoreRefs(s, repoID, map[string]string{
		"refs/heads/" + m.added.branch: m.added.topic,
		"refs/heads/main":              m.added.tip,
	})
}

// borrowerIn is the borrower of repoID in instance inst.
func borrowerIn(s *testState, m *mirrorScenario, repoID, inst string) (string, error) {
	for _, c := range m.borrowers[repoID] {
		if strings.HasPrefix(c, filepath.Join(s.workspaceRoot, inst)+string(filepath.Separator)) {
			return c, nil
		}
	}
	return "", fmt.Errorf("no borrower of %q in instance %q", repoID, inst)
}

func theBorrowerHasABranchAtTheTopicCommit(ctx context.Context, repoID, inst, branch string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return ctx, err
	}
	if out, err := fixtureGitWorkTree(clone, "branch", branch, m.added.topic); err != nil {
		return ctx, fmt.Errorf("git branch %s in %s: %w\n%s", branch, clone, err, out)
	}
	return ctx, nil
}

func theSourceRepoDropsTheAddedCommits(ctx context.Context, name string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	repo := sourceRepoPath(s, name)
	for _, args := range [][]string{
		{"update-ref", "-d", "refs/heads/" + m.added.branch},
		{"update-ref", "refs/heads/main", m.added.parent},
		{"reflog", "expire", "--expire=now", "--all"},
		{"gc", "--quiet", "--prune=now"},
	} {
		if out, err := fixtureGitBare(repo, args...); err != nil {
			return ctx, fmt.Errorf("git %s in %s: %w\n%s", args[0], repo, err, out)
		}
	}
	return ctx, nil
}

func aFreshCloneHoldsNeitherAddedCommit(ctx context.Context, name string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	work, err := os.MkdirTemp(s.gitServer.root, "fresh-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", "--no-local", s.repoURLs[name], work); err != nil {
		return fmt.Errorf("cloning %s: %w\n%s", name, err, out)
	}
	for _, id := range []string{m.added.topic, m.added.tip} {
		if _, err := fixtureGitWorkTree(work, "cat-file", "-e", id); err == nil {
			return fmt.Errorf("a fresh clone of %s still holds %s; the remote didn't drop it", name, id)
		}
	}
	return nil
}

func theMirrorKeepsTheDroppedCommits(ctx context.Context, repoID, _ string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if err := requireStoreRefs(s, repoID, map[string]string{
		"refs/heads/" + m.added.branch: m.added.topic,
		"refs/heads/main":              m.added.parent,
	}); err != nil {
		return err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return err
	}
	reflog, err := os.ReadFile(filepath.Join(store, "logs", "refs", "heads", "main"))
	if err != nil {
		return fmt.Errorf("the store keeps no reflog for main: %w", err)
	}
	if !strings.Contains(string(reflog), m.added.tip) {
		return fmt.Errorf("main's reflog doesn't name the dropped commit %s:\n%s", m.added.tip, reflog)
	}
	for _, id := range []string{m.added.topic, m.added.tip} {
		if out, err := fixtureGitBare(store, "cat-file", "-e", id); err != nil {
			return fmt.Errorf("the store can't read %s: %w\n%s", id, err, out)
		}
	}
	return nil
}

func theStoreHoldsTheFixedConfig(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return err
	}
	out, err := fixtureGit(store, "config", "--file", filepath.Join(store, "config"), "--list")
	if err != nil {
		return fmt.Errorf("listing the store's config: %w\n%s", err, out)
	}
	got := strings.Split(strings.TrimSpace(string(out)), "\n")
	sort.Strings(got)
	want := []string{
		"core.repositoryformatversion=0",
		"core.bare=true",
		"fetch.prune=false",
		"gc.auto=0",
		"maintenance.auto=false",
		"gc.pruneexpire=never",
		"core.logallrefupdates=always",
		"gc.reflogexpire=never",
		"gc.reflogexpireunreachable=never",
		"transfer.fsckobjects=true",
		"fetch.fsckobjects=true",
		"transfer.unpacklimit=1",
		"fetch.unpacklimit=1",
		"remote.origin.url=" + s.repoURLs[repoID],
		"remote.origin.fetch=+refs/*:refs/*",
	}
	sort.Strings(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("the store's config =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	return nil
}

func theBranchStillNamesTheTopicCommit(ctx context.Context, branch, repoID, inst string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return err
	}
	got, err := revParse(clone, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	if got != m.added.topic {
		return fmt.Errorf("%s in %s = %s, want %s", branch, clone, got, m.added.topic)
	}
	return nil
}
