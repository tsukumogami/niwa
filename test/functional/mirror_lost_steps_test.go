package functional

// Steps for the lost-commit scenarios in features/mirror-repair.feature: a
// borrower branch built on a pushed commit that the remote later deletes,
// the mirror that kept the commit lost, and what a restore recovers or
// reports. Like the steps beside them they read the store with git and the
// layout the design fixes rather than importing niwa's mirror package.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

func registerMirrorLostSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I add a branch "([^"]*)" with one commit to source repo "([^"]*)"$`, iAddABranchWithOneCommit)
	ctx.Step(`^each borrower of "([^"]*)" has the remote-tracking branch "([^"]*)" at the added topic commit$`, eachBorrowerTracksTheTopic)
	ctx.Step(`^the borrower of "([^"]*)" in instance "([^"]*)" has a local branch "([^"]*)" with one unpushed commit on the added topic commit$`, theBorrowerHasAnUnpushedBranchOnTheTopic)
	ctx.Step(`^source repo "([^"]*)" keeps the added topic commit only under a hidden ref and deletes the added branch$`, theSourceRepoHidesTheTopic)
	ctx.Step(`^source repo "([^"]*)" deletes the added branch and prunes its commit$`, theSourceRepoDestroysTheTopic)
	ctx.Step(`^a fresh clone of source repo "([^"]*)" lacks the added topic commit$`, aFreshCloneLacksTheTopic)
	ctx.Step(`^the mirror of "([^"]*)" pins the added topic commit and has no branch for it$`, theMirrorPinsTheTopic)
	ctx.Step(`^the mirror of "([^"]*)" pins nothing$`, theMirrorPinsNothing)
	ctx.Step(`^the hook's context carries the notice for "([^"]*)" with fault "([^"]*)", result "([^"]*)" and (\d+) unrecoverable refs, naming none$`, theHookContextCountsUnrecoverable)
	ctx.Step(`^the mirror log then gained the unrecoverable lines for "([^"]*)" from command "([^"]*)"$`, theMirrorLogGainedTheUnrecoverableLines)
	ctx.Step(`^every other ref of each borrower of "([^"]*)" reads to the root$`, everyOtherRefReadsToTheRoot)
	ctx.Step(`^the unpushed commit on the branch and its own file still read in the borrower of "([^"]*)" in instance "([^"]*)"$`, theUnpushedBranchCommitStillReads)
	ctx.Step(`^the mirror log gained only a healthy check line for "([^"]*)" from "([^"]*)"$`, theMirrorLogGainedOnlyAHealthyCheck)
}

// lostOwnFile is the file the unpushed commit on the borrower's branch adds.
const lostOwnFile = "keep-own.txt"

func iAddABranchWithOneCommit(ctx context.Context, branch, name string) (context.Context, error) {
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
	m.added.branch = branch
	return ctx, nil
}

func eachBorrowerTracksTheTopic(ctx context.Context, repoID, tracking string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		got, err := revParse(clone, "refs/remotes/"+tracking)
		if err != nil {
			return err
		}
		if got != m.added.topic {
			return fmt.Errorf("%s in %s = %s, want the added topic commit %s", tracking, clone, got, m.added.topic)
		}
	}
	return nil
}

// theBorrowerHasAnUnpushedBranchOnTheTopic makes the unpushed commit in a
// scratch clone of the source repo, on the topic commit, and fetches it into
// the borrower as the branch, so the borrower's checkout, index and working
// tree are never touched. The borrower is then repacked against the mirror
// again, so it holds the unpushed commit's objects and nothing the mirror
// holds.
func theBorrowerHasAnUnpushedBranchOnTheTopic(ctx context.Context, repoID, inst, branch string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return ctx, err
	}
	work, err := os.MkdirTemp(s.gitServer.root, "unpushed-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", s.repoURLs[repoID], work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", repoID, err, out)
	}
	if out, err := fixtureGitWorkTree(work, "checkout", "--quiet", "-B", branch, m.added.topic); err != nil {
		return ctx, fmt.Errorf("git checkout %s: %w\n%s", branch, err, out)
	}
	if err := os.WriteFile(filepath.Join(work, lostOwnFile), []byte("work on "+branch+" nobody pushed\n"), 0o644); err != nil {
		return ctx, err
	}
	for _, args := range [][]string{{"add", lostOwnFile}, {"commit", "--quiet", "-m", "unpushed work on " + branch}} {
		if out, err := fixtureGitCommit(work, args...); err != nil {
			return ctx, fmt.Errorf("git %s: %w\n%s", args[0], err, out)
		}
	}
	commit, err := revParse(work, "HEAD")
	if err != nil {
		return ctx, err
	}
	blob, err := revParse(work, "HEAD:"+lostOwnFile)
	if err != nil {
		return ctx, err
	}
	for _, args := range [][]string{
		{"fetch", "--quiet", "--no-tags", work, "refs/heads/" + branch + ":refs/heads/" + branch},
		{"repack", "-a", "-d", "-l", "-q"},
	} {
		if out, err := fixtureGitWorkTree(clone, args...); err != nil {
			return ctx, fmt.Errorf("git %s in %s: %w\n%s", args[0], clone, err, out)
		}
	}
	if got, err := revParse(clone, "refs/heads/"+branch); err != nil || got != commit {
		return ctx, fmt.Errorf("%s in %s = %q (%v), want %s", branch, clone, got, err, commit)
	}
	m.lost.clone, m.lost.branch, m.lost.commit, m.lost.blob = clone, branch, commit, blob
	return ctx, nil
}

// dropTheTopic has the source repo delete the added branch and expire and
// prune everything only it reached. With hidden set, the commit is kept under
// refs/pull/1/head first, a ref upload-pack hides, as a forge keeps a pull
// request's commits: the remote then serves it by id and advertises nothing.
func dropTheTopic(ctx context.Context, name string, hidden bool) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	repo := sourceRepoPath(s, name)
	var steps [][]string
	if hidden {
		steps = append(steps,
			[]string{"update-ref", "refs/pull/1/head", m.added.topic},
			[]string{"config", "uploadpack.hideRefs", "refs/pull/"})
	}
	steps = append(steps,
		[]string{"update-ref", "-d", "refs/heads/" + m.added.branch},
		[]string{"reflog", "expire", "--expire=now", "--all"},
		[]string{"gc", "--quiet", "--prune=now"})
	for _, args := range steps {
		if out, err := fixtureGitBare(repo, args...); err != nil {
			return ctx, fmt.Errorf("git %s in %s: %w\n%s", args[0], repo, err, out)
		}
	}
	_, err = fixtureGitBare(repo, "cat-file", "-e", m.added.topic)
	if hidden && err != nil {
		return ctx, fmt.Errorf("the source repo lost the commit it should keep hidden: %w", err)
	}
	if !hidden && err == nil {
		return ctx, fmt.Errorf("the source repo still holds %s after the prune", m.added.topic)
	}
	return ctx, nil
}

func theSourceRepoHidesTheTopic(ctx context.Context, name string) (context.Context, error) {
	return dropTheTopic(ctx, name, true)
}

func theSourceRepoDestroysTheTopic(ctx context.Context, name string) (context.Context, error) {
	return dropTheTopic(ctx, name, false)
}

func aFreshCloneLacksTheTopic(ctx context.Context, name string) error {
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
	if _, err := fixtureGitWorkTree(work, "cat-file", "-e", m.added.topic); err == nil {
		return fmt.Errorf("a fresh clone of %s still holds %s", name, m.added.topic)
	}
	return nil
}

// keptRefsOf lists the store's refs under refs/niwa/kept/, each as
// "<ref> <object>".
func keptRefsOf(s *testState, repoID string) ([]string, error) {
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return nil, err
	}
	out, err := fixtureGitBare(store, "for-each-ref", "--format=%(refname) %(objectname)", "refs/niwa/kept/")
	if err != nil {
		return nil, fmt.Errorf("listing the store's kept refs: %w\n%s", err, out)
	}
	return strings.Fields(strings.ReplaceAll(strings.TrimSpace(string(out)), " ", "=")), nil
}

func theMirrorPinsTheTopic(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	kept, err := keptRefsOf(s, repoID)
	if err != nil {
		return err
	}
	if want := []string{"refs/niwa/kept/" + m.added.topic + "=" + m.added.topic}; !slices.Equal(kept, want) {
		return fmt.Errorf("the mirror's kept refs = %v, want %v", kept, want)
	}
	return theMirrorHasNoBranch(ctx, repoID, m.added.branch)
}

func theMirrorPinsNothing(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	kept, err := keptRefsOf(s, repoID)
	if err != nil {
		return err
	}
	if len(kept) != 0 {
		return fmt.Errorf("the mirror pins %v, want nothing", kept)
	}
	return nil
}

func theHookContextCountsUnrecoverable(ctx context.Context, repoID, fault, result string, n int) error {
	if err := theHookContextCarriesNoticeWith(ctx, repoID, fault, result); err != nil {
		return err
	}
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	text, err := hookContext(s)
	if err != nil {
		return err
	}
	if want := fmt.Sprintf(" Except %d refs in those clones, which are unrecoverable", n); !strings.Contains(text, want) {
		return fmt.Errorf("the notice doesn't count %d unrecoverable refs:\n%s", n, text)
	}
	if !strings.Contains(text, "The mirror log names them.") {
		return fmt.Errorf("the notice doesn't say the mirror log names them:\n%s", text)
	}
	for _, name := range []string{"refs/", m.added.branch + ".txt", m.lost.branch, m.added.topic, filepath.Join("tools", repoID)} {
		if strings.Contains(text, name) {
			return fmt.Errorf("the notice names %q:\n%s", name, text)
		}
	}
	return nil
}

// theMirrorLogGainedTheUnrecoverableLines checks the lines a restore that
// left refs lost logs: a check and a restore line with fault missing and
// result restored, then one unrecoverable line for the lost branch in myws
// and one for the stale origin/<topic> in each borrower.
func theMirrorLogGainedTheUnrecoverableLines(ctx context.Context, repoID, command string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	if len(lines) != 5 {
		return fmt.Errorf("the mirror log gained %d lines, want a check, a restore and three unrecoverable lines: %v", len(lines), lines)
	}
	if err := requireLogLine(lines[0], command, repoID, "check", "missing", "restored"); err != nil {
		return err
	}
	if err := requireLogLine(lines[1], command, repoID, "restore", "missing", "restored"); err != nil {
		return err
	}
	newKeys := []string{"instance", "ref", "missing_commit", "remote_tracking", "own_commits", "lost_files"}
	for _, l := range lines[:2] {
		for _, k := range newKeys {
			if _, ok := l[k]; ok {
				return fmt.Errorf("the %s line %v carries %q", l["event"], l, k)
			}
		}
	}
	myws := string(filepath.Separator) + filepath.Join("myws", "tools", repoID)
	myws2 := string(filepath.Separator) + filepath.Join("myws-2", "tools", repoID)
	var tracking []string
	for i, l := range lines[2:] {
		if err := requireLogLine(l, command, repoID, "unrecoverable", "missing", "restored"); err != nil {
			return err
		}
		for _, k := range newKeys {
			if _, ok := l[k]; !ok {
				return fmt.Errorf("unrecoverable line %v has no %q", l, k)
			}
		}
		instance, _ := l["instance"].(string)
		own, _ := l["own_commits"].([]any)
		files, _ := l["lost_files"].([]any)
		if l["missing_commit"] != m.added.topic {
			return fmt.Errorf("unrecoverable line %v names missing commit %v, want %s", l, l["missing_commit"], m.added.topic)
		}
		if i == 0 {
			if l["ref"] != "refs/heads/"+m.lost.branch || !strings.HasSuffix(instance, myws) || l["remote_tracking"] != false {
				return fmt.Errorf("the first unrecoverable line %v, want refs/heads/%s in the myws clone", l, m.lost.branch)
			}
			if len(own) != 1 || own[0] != m.lost.commit {
				return fmt.Errorf("own_commits %v, want [%s]", own, m.lost.commit)
			}
			if len(files) != 1 {
				return fmt.Errorf("lost_files %v, want only added-%s.txt", files, m.added.branch)
			}
			file, _ := files[0].(map[string]any)
			if file["path"] != "added-"+m.added.branch+".txt" || file["in_working_tree"] != false {
				return fmt.Errorf("lost_files %v, want added-%s.txt not in the working tree", files, m.added.branch)
			}
			continue
		}
		if l["ref"] != "refs/remotes/origin/"+m.added.branch || l["remote_tracking"] != true || len(own) != 0 || len(files) != 0 {
			return fmt.Errorf("unrecoverable line %v, want the stale refs/remotes/origin/%s with no own commits or files", l, m.added.branch)
		}
		tracking = append(tracking, instance)
	}
	if len(tracking) != 2 || !slices.ContainsFunc(tracking, func(p string) bool { return strings.HasSuffix(p, myws) }) ||
		!slices.ContainsFunc(tracking, func(p string) bool { return strings.HasSuffix(p, myws2) }) {
		return fmt.Errorf("the stale origin/%s lines name %v, want the myws and myws-2 clones", m.added.branch, tracking)
	}
	return nil
}

// everyOtherRefReadsToTheRoot walks every ref of each borrower but the lost
// branch and the stale remote-tracking refs back to the root commit.
func everyOtherRefReadsToTheRoot(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	skip := []string{"refs/heads/" + m.lost.branch, "refs/remotes/origin/" + m.added.branch}
	for _, clone := range clones {
		refs, err := fixtureGitWorkTree(clone, "for-each-ref", "--format=%(refname)")
		if err != nil {
			return fmt.Errorf("git for-each-ref in %s: %w\n%s", clone, err, refs)
		}
		walked := 0
		for _, ref := range append(strings.Fields(string(refs)), "HEAD") {
			if slices.Contains(skip, ref) {
				continue
			}
			if out, err := fixtureGitWorkTree(clone, "rev-list", "--objects", ref); err != nil {
				return fmt.Errorf("git rev-list --objects %s in %s: %w\n%s", ref, clone, err, out)
			}
			root, err := fixtureGitWorkTree(clone, "rev-list", "--max-parents=0", ref)
			if err != nil {
				return fmt.Errorf("git rev-list %s in %s: %w\n%s", ref, clone, err, root)
			}
			if got, want := strings.TrimSpace(string(root)), m.rootCommits[repoID]; got != want {
				return fmt.Errorf("%s in %s reaches root %q, want %q", ref, clone, got, want)
			}
			walked++
		}
		if walked == 0 {
			return fmt.Errorf("borrower %s has no other ref to walk", clone)
		}
	}
	return nil
}

func theUnpushedBranchCommitStillReads(ctx context.Context, repoID, inst string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clone, err := borrowerIn(s, m, repoID, inst)
	if err != nil {
		return err
	}
	got, err := revParse(clone, "refs/heads/"+m.lost.branch)
	if err != nil {
		return err
	}
	if got != m.lost.commit {
		return fmt.Errorf("%s in %s = %s, want %s", m.lost.branch, clone, got, m.lost.commit)
	}
	for _, id := range []string{m.lost.commit, m.lost.blob} {
		if out, err := fixtureGitWorkTree(clone, "cat-file", "-e", id); err != nil {
			return fmt.Errorf("%s no longer reads in %s: %w\n%s", id, clone, err, out)
		}
	}
	return nil
}

func theMirrorLogGainedOnlyAHealthyCheck(ctx context.Context, repoID, command string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	if len(lines) != 1 {
		return fmt.Errorf("the mirror log gained %d lines, want one healthy check line: %v", len(lines), lines)
	}
	return requireLogLine(lines[0], command, repoID, "check", "", "healthy")
}
