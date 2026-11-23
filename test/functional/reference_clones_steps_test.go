package functional

// Steps for features/reference-clones.feature: new clones that borrow from
// the host mirror through its stable path, the fallback to a full clone and
// its warning, and the setting that turns borrowing off. The steps that say
// whether a clone borrows are meant for other features too: a scenario that
// needs borrowers calls "the clones of ... were made borrowing by niwa
// create" in place of writing alternates files itself.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// referenceHookSessionID is the session the SessionStart hook rows provision
// for; its instance is "<workspace>-" and the id's first twelve characters.
const referenceHookSessionID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"

func registerReferenceCloneSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the clone of "([^"]*)" in instance "([^"]*)" borrows from the mirror of "([^"]*)"$`, theCloneBorrowsFromTheMirror)
	ctx.Step(`^the clone of "([^"]*)" in instance "([^"]*)" does not borrow$`, theCloneDoesNotBorrow)
	ctx.Step(`^the clones of "([^"]*)" in instances "([^"]*)" and "([^"]*)" were made borrowing by niwa create from the mirror of "([^"]*)"$`, theClonesWereMadeBorrowing)
	ctx.Step(`^every clone in instance "([^"]*)" borrows from its mirror$`, everyCloneInInstanceBorrows)
	ctx.Step(`^no clone in instance "([^"]*)" borrows$`, noCloneInInstanceBorrows)
	ctx.Step(`^every clone in every instance borrows from its mirror$`, everyCloneInEveryInstanceBorrows)
	ctx.Step(`^the error output warns that "([^"]*)" was cloned in full with reason "([^"]*)"$`, theErrorOutputWarnsClonedInFull)
	ctx.Step(`^the error output names no clone made in full$`, theErrorOutputNamesNoFullClone)
	ctx.Step(`^the error output says nothing about "([^"]*)"$`, theErrorOutputSaysNothingAbout)
	ctx.Step(`^no mirror of "([^"]*)" exists$`, noMirrorOfExists)
	ctx.Step(`^the clones of "([^"]*)" in instances "([^"]*)" and "([^"]*)" have the same origin, refs, local config and hooks$`, theClonesMatch)
	ctx.Step(`^nothing but the clones is left in the group "([^"]*)" of instance "([^"]*)"$`, nothingButTheClonesIsLeft)
	ctx.Step(`^the error output shows the restore of "([^"]*)" before the instance was created$`, theRestoreCameBeforeTheCreate)
	ctx.Step(`^I remove instance "([^"]*)" from disk$`, iRemoveInstanceFromDisk)
	ctx.Step(`^I remove every trace of the mirror of "([^"]*)"$`, iRemoveEveryTraceOfTheMirror)
	ctx.Step(`^the mirror of "([^"]*)" can't be seeded$`, theMirrorCantBeSeeded)
	ctx.Step(`^I run "([^"]*)" from workspace root (\d+) times at once$`, iRunTimesAtOnce)
	ctx.Step(`^every create that ran at once exited 0$`, everyConcurrentCreateExitedZero)
	ctx.Step(`^each of "([^"]*)" has one stable path and one store$`, eachHasOneStablePathAndOneStore)
	ctx.Step(`^every clone of the creates that ran at once borrows or is named as cloned in full in that create's output$`, everyConcurrentCloneBorrowsOrIsNamed)
	ctx.Step(`^a SessionStart hook that provisions runs while the mirror lock of "([^"]*)" is held$`, aProvisioningHookRunsWhileTheLockIsHeld)
	ctx.Step(`^the hook took at least (\d+) seconds$`, theHookTookAtLeast)
	ctx.Step(`^the hook's context says "([^"]*)" was cloned in full with reason "([^"]*)"$`, theHookContextSaysClonedInFull)
	ctx.Step(`^the clone of "([^"]*)" in the hook's instance does not borrow$`, theCloneInTheHookInstanceDoesNotBorrow)
	ctx.Step(`^I run niwa dispatch from workspace root$`, iRunNiwaDispatchForReferenceClones)
	ctx.Step(`^I run a SessionStart hook that provisions$`, iRunAProvisioningHook)
	ctx.Step(`^no clone in instance "([^"]*)" holds a pack a mirror store holds or a gc or maintenance key$`, noCloneHoldsAMirrorPack)
	ctx.Step(`^every clone in instance "([^"]*)" reads its default branch and fsck is clean$`, everyCloneReadsAndFsckIsClean)
	ctx.Step(`^(niwa apply after a repo is added to the config|niwa dispatch|a SessionStart hook) provisions a new clone$`, aCommandProvisionsANewClone)
	ctx.Step(`^the workspace holds (\d+) instances with (\d+) clones in all$`, theWorkspaceHoldsInstancesAndClones)
	ctx.Step(`^the error output names no clone made in full other than "([^"]*)"$`, theErrorOutputNamesNoOtherFullClone)
	ctx.Step(`^git fetches "([^"]*)" from source repo "([^"]*)"$`, gitFetchesFromSourceRepo)
	ctx.Step(`^every clone of workspace "([^"]*)" borrows from its mirror$`, everyCloneOfWorkspaceBorrows)
}

// gitFetchesFromSourceRepo points url at the local source repo for every git
// process niwa starts, its mirror's network calls included, through a
// url.<base>.insteadOf entry in GIT_CONFIG_COUNT.
func gitFetchesFromSourceRepo(ctx context.Context, url, source string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	local, ok := s.repoURLs[source]
	if !ok {
		return ctx, fmt.Errorf("no source repo %q", source)
	}
	s.envOverrides["GIT_CONFIG_COUNT"] = "1"
	s.envOverrides["GIT_CONFIG_KEY_0"] = "url." + local + ".insteadOf"
	s.envOverrides["GIT_CONFIG_VALUE_0"] = url
	return ctx, nil
}

// everyCloneOfWorkspaceBorrows checks every clone of the workspace that niwa
// init made under the workspace root's directory name: each holds the one
// line of its mirror's stable path, under the key of that workspace.
func everyCloneOfWorkspaceBorrows(ctx context.Context, name string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	root := filepath.Join(s.workspaceRoot, name)
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "*", ".git"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("workspace %s holds no clones", root)
	}
	ws := *s
	ws.workspaceRoot = root
	for _, m := range matches {
		clone := filepath.Dir(m)
		if err := cloneBorrowsFrom(&ws, clone, filepath.Base(clone)); err != nil {
			return err
		}
	}
	return nil
}

// aCommandProvisionsANewClone runs the outline row's command. The apply row
// adds the repo docs to the workspace config first, so the apply clones it
// into the existing instance; the other rows provision a new instance.
func aCommandProvisionsANewClone(ctx context.Context, command string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	switch command {
	case "niwa apply after a repo is added to the config":
		body := "[workspace]\nname = \"myws\"\n\n[groups.tools]\n"
		for _, repo := range []string{"app", "lib", "docs"} {
			body += fmt.Sprintf("\n[repos.%s]\nurl = \"{repo:%s}\"\ngroup = \"tools\"\n", repo, repo)
		}
		if ctx, err = theConfigRepoIsForcePushedTo(ctx, "myws", body); err != nil {
			return ctx, err
		}
		return ctx, runNiwa(s, s.workspaceRoot, "niwa apply")
	case "niwa dispatch":
		return iRunNiwaDispatchForReferenceClones(ctx)
	case "a SessionStart hook":
		return iRunAProvisioningHook(ctx)
	}
	return ctx, fmt.Errorf("unknown command %q", command)
}

func theWorkspaceHoldsInstancesAndClones(ctx context.Context, instances, clones int) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	insts, err := borrowingInstances(s)
	if err != nil {
		return err
	}
	if len(insts) != instances {
		return fmt.Errorf("the workspace holds instances %v, want %d", insts, instances)
	}
	n := 0
	for _, inst := range insts {
		c, err := instanceClones(s, inst)
		if err != nil {
			return err
		}
		n += len(c)
	}
	if n != clones {
		return fmt.Errorf("the workspace's instances hold %d clones, want %d", n, clones)
	}
	return nil
}

func theErrorOutputNamesNoOtherFullClone(ctx context.Context, repo string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(s.stderr, "\n") {
		if strings.Contains(line, "cloned in full") && !strings.Contains(line, "warning: "+repo+" was cloned in full") {
			return fmt.Errorf("stderr names another clone made in full: %q", line)
		}
	}
	return nil
}

// referenceScenario is what these steps remember between steps.
type referenceScenario struct {
	concurrent []raceRun
}

func referenceState(ctx context.Context) (*testState, *referenceScenario, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if s.reference == nil {
		s.reference = &referenceScenario{}
	}
	return s, s.reference, nil
}

// cloneBorrowsFrom checks that clone's alternates file holds exactly one
// line, the mirror's stable path and "/objects", that the stable path is the
// link niwa writes into its store and not the store itself, and that git
// reads the clone.
func cloneBorrowsFrom(s *testState, clone, repoID string) error {
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return err
	}
	if _, err := mirrorStore(s, repoID); err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(clone, ".git", "objects", "info", "alternates"))
	if err != nil {
		return fmt.Errorf("%s has no alternates file: %w", clone, err)
	}
	if want := stable + "/objects\n"; string(data) != want {
		return fmt.Errorf("%s's alternates holds %q, want exactly %q", clone, data, want)
	}
	if out, err := fixtureGitWorkTree(clone, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		return fmt.Errorf("git can't read HEAD in %s: %w\n%s", clone, err, out)
	}
	return nil
}

// cloneIsFull checks that clone is a clone with no alternates file.
func cloneIsFull(clone string) error {
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		return fmt.Errorf("%s is not a clone: %w", clone, err)
	}
	alt := filepath.Join(clone, ".git", "objects", "info", "alternates")
	if _, err := os.Lstat(alt); !os.IsNotExist(err) {
		data, _ := os.ReadFile(alt)
		return fmt.Errorf("%s has an alternates file (%q), want a full clone", clone, data)
	}
	return nil
}

func theCloneBorrowsFromTheMirror(ctx context.Context, groupRepo, inst, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	return cloneBorrowsFrom(s, filepath.Join(s.workspaceRoot, inst, groupRepo), repoID)
}

func theCloneDoesNotBorrow(ctx context.Context, groupRepo, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	return cloneIsFull(filepath.Join(s.workspaceRoot, inst, groupRepo))
}

// theClonesWereMadeBorrowing is how the mirror features get their borrowers:
// it checks that niwa create made both clones borrow from the mirror, and
// records them as its borrowers for the mirror steps that follow.
func theClonesWereMadeBorrowing(ctx context.Context, groupRepo, inst1, inst2, repoID string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	for _, inst := range []string{inst1, inst2} {
		clone := filepath.Join(s.workspaceRoot, inst, groupRepo)
		if err := cloneBorrowsFrom(s, clone, repoID); err != nil {
			return ctx, err
		}
		m.borrowers[repoID] = append(m.borrowers[repoID], clone)
	}
	return ctx, nil
}

// instanceClones lists every clone two levels under inst, keyed by repo name.
func instanceClones(s *testState, inst string) (map[string]string, error) {
	matches, err := filepath.Glob(filepath.Join(s.workspaceRoot, inst, "*", "*", ".git"))
	if err != nil {
		return nil, err
	}
	clones := map[string]string{}
	for _, m := range matches {
		clone := filepath.Dir(m)
		clones[filepath.Base(clone)] = clone
	}
	if len(clones) == 0 {
		return nil, fmt.Errorf("instance %s holds no clones", inst)
	}
	return clones, nil
}

func everyCloneInInstanceBorrows(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for name, clone := range clones {
		if err := cloneBorrowsFrom(s, clone, name); err != nil {
			return err
		}
	}
	return nil
}

func noCloneInInstanceBorrows(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		if err := cloneIsFull(clone); err != nil {
			return err
		}
	}
	return nil
}

// borrowingInstances lists the instance directories under the workspace root.
func borrowingInstances(s *testState) ([]string, error) {
	entries, err := os.ReadDir(s.workspaceRoot)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func everyCloneInEveryInstanceBorrows(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	instances, err := borrowingInstances(s)
	if err != nil {
		return err
	}
	if len(instances) == 0 {
		return fmt.Errorf("the workspace holds no instance")
	}
	for _, inst := range instances {
		if err := everyCloneInInstanceBorrows(ctx, inst); err != nil {
			return err
		}
	}
	return nil
}

// fullCloneLine is the warning a create prints for repo cloned in full.
func fullCloneLine(repo, reason string) string {
	return "warning: " + repo + " was cloned in full instead of borrowing from the host mirror (" + reason + ")"
}

func theErrorOutputWarnsClonedInFull(ctx context.Context, repo, reason string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if n := strings.Count(s.stderr, fullCloneLine(repo, reason)); n != 1 {
		return fmt.Errorf("stderr carries %q %d times, want once:\n%s", fullCloneLine(repo, reason), n, s.stderr)
	}
	return nil
}

func theErrorOutputNamesNoFullClone(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if strings.Contains(s.stderr, "cloned in full") {
		return fmt.Errorf("stderr names a clone made in full:\n%s", s.stderr)
	}
	return nil
}

func theErrorOutputSaysNothingAbout(ctx context.Context, repo string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(s.stderr, "\n") {
		if strings.Contains(line, "warning:") && strings.Contains(line, repo) {
			return fmt.Errorf("stderr warns about %s: %q", repo, line)
		}
	}
	return nil
}

// noMirrorOfExists checks that the mirrors directory holds nothing for repoID:
// no stable path, no store, no state file and no store record.
func noMirrorOfExists(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	for _, pattern := range []string{
		filepath.Join(dir, repoID),
		filepath.Join(dir, ".store", repoID+"-*"),
		filepath.Join(dir, ".meta", repoID+".*"),
	} {
		found, err := filepath.Glob(pattern)
		if err != nil {
			return err
		}
		if len(found) != 0 {
			return fmt.Errorf("the mirror of %s has %v on disk", repoID, found)
		}
	}
	return nil
}

func theClonesMatch(ctx context.Context, groupRepo, inst1, inst2 string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	a := filepath.Join(s.workspaceRoot, inst1, groupRepo)
	b := filepath.Join(s.workspaceRoot, inst2, groupRepo)
	for _, args := range [][]string{
		{"rev-parse", "HEAD"},
		{"remote", "get-url", "origin"},
		{"for-each-ref"},
		{"config", "--local", "--list"},
	} {
		outA, err := fixtureGitWorkTree(a, args...)
		if err != nil {
			return fmt.Errorf("git %v in %s: %w\n%s", args, a, err, outA)
		}
		outB, err := fixtureGitWorkTree(b, args...)
		if err != nil {
			return fmt.Errorf("git %v in %s: %w\n%s", args, b, err, outB)
		}
		if string(outA) != string(outB) {
			return fmt.Errorf("git %v differs:\n%s:\n%s\n%s:\n%s", args, a, outA, b, outB)
		}
	}
	hooksA, err := fileNames(filepath.Join(a, ".git", "hooks"))
	if err != nil {
		return err
	}
	hooksB, err := fileNames(filepath.Join(b, ".git", "hooks"))
	if err != nil {
		return err
	}
	if !slices.Equal(hooksA, hooksB) {
		return fmt.Errorf("hooks differ: %s has %v, %s has %v", a, hooksA, b, hooksB)
	}
	return nil
}

func fileNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// nothingButTheClonesIsLeft checks that the group directory holds only real
// clones, and that no clone's objects/info holds a temporary alternates file.
func nothingButTheClonesIsLeft(ctx context.Context, group, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.workspaceRoot, inst, group)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		clone := filepath.Join(dir, e.Name())
		if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
			return fmt.Errorf("%s is a directory beside the clones that isn't one", clone)
		}
		names, err := fileNames(filepath.Join(clone, ".git", "objects", "info"))
		if err != nil {
			return err
		}
		for _, n := range names {
			if strings.HasPrefix(n, ".") {
				return fmt.Errorf("%s holds the temporary file %s", clone, n)
			}
		}
	}
	return nil
}

// theRestoreCameBeforeTheCreate checks that the restore's warning comes
// before the line reporting the instance created, which follows the clones.
func theRestoreCameBeforeTheCreate(ctx context.Context, repoID string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	restore := strings.Index(s.stderr, "warning: mirror "+repoID+": fault missing, result restored")
	created := strings.Index(s.stderr, "created ")
	if restore < 0 || created < 0 || restore > created {
		return fmt.Errorf("stderr doesn't show the restore of %s before the created line:\n%s", repoID, s.stderr)
	}
	return nil
}

func iRemoveInstanceFromDisk(ctx context.Context, inst string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	dir := filepath.Join(s.workspaceRoot, inst)
	if _, err := os.Stat(filepath.Join(dir, ".niwa")); err != nil {
		return ctx, fmt.Errorf("%s is not an instance: %w", dir, err)
	}
	return ctx, os.RemoveAll(dir)
}

// iRemoveEveryTraceOfTheMirror removes the stable path, every store of
// repoID, its state file and its store record, as on a host that never had
// the mirror.
func iRemoveEveryTraceOfTheMirror(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return ctx, err
	}
	if err := os.Remove(filepath.Join(dir, repoID)); err != nil {
		return ctx, err
	}
	for _, pattern := range []string{filepath.Join(dir, ".store", repoID+"-*"), filepath.Join(dir, ".meta", repoID+".*")} {
		found, err := filepath.Glob(pattern)
		if err != nil {
			return ctx, err
		}
		for _, f := range found {
			if err := os.RemoveAll(f); err != nil {
				return ctx, err
			}
		}
	}
	return ctx, noMirrorOfExists(ctx, repoID)
}

// theMirrorCantBeSeeded puts a directory holding a file where repoID's store
// record goes, so a first build of its mirror fails when it writes the
// record, while the repo's remote still serves a clone.
func theMirrorCantBeSeeded(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return ctx, err
	}
	record := filepath.Join(dir, ".meta", repoID+".stores")
	if err := os.MkdirAll(record, 0o700); err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(filepath.Join(record, "blocker"), []byte("x\n"), 0o600)
}

// iRunTimesAtOnce starts n copies of command from the workspace root at the
// same moment and waits for all of them. The command's {n} is replaced by
// each copy's number, so each can name its own instance.
func iRunTimesAtOnce(ctx context.Context, command string, n int) (context.Context, error) {
	s, r, err := referenceState(ctx)
	if err != nil {
		return ctx, err
	}
	r.concurrent = make([]raceRun, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		line := strings.ReplaceAll(command, "{n}", fmt.Sprint(i+1))
		args := strings.Fields(line)
		if args[0] == "niwa" {
			args[0] = s.binPath
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = s.workspaceRoot
		cmd.Env = s.buildEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := cmd.Run()
			r.concurrent[i] = raceRun{instance: line, stderr: stderr.String()}
			if exitErr, ok := err.(*exec.ExitError); ok {
				r.concurrent[i].exitCode = exitErr.ExitCode()
			} else if err != nil {
				errs[i] = err
			}
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func everyConcurrentCreateExitedZero(ctx context.Context) error {
	_, r, err := referenceState(ctx)
	if err != nil {
		return err
	}
	for _, run := range r.concurrent {
		if run.exitCode != 0 {
			return fmt.Errorf("%q exited %d:\n%s", run.instance, run.exitCode, run.stderr)
		}
	}
	return nil
}

func eachHasOneStablePathAndOneStore(ctx context.Context, list string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	for _, repoID := range strings.Split(list, ",") {
		repoID = strings.TrimSpace(repoID)
		if _, err := mirrorStore(s, repoID); err != nil {
			return err
		}
		stores, err := filepath.Glob(filepath.Join(dir, ".store", repoID+"-*"))
		if err != nil {
			return err
		}
		if len(stores) != 1 {
			return fmt.Errorf("%s has %d stores, want one: %v", repoID, len(stores), stores)
		}
	}
	return nil
}

// everyConcurrentCloneBorrowsOrIsNamed checks each clone of every instance
// the concurrent creates made: it borrows, or its create's stderr names it
// as cloned in full.
func everyConcurrentCloneBorrowsOrIsNamed(ctx context.Context) error {
	s, r, err := referenceState(ctx)
	if err != nil {
		return err
	}
	for _, run := range r.concurrent {
		name := run.instance[strings.LastIndex(run.instance, " ")+1:]
		inst := "myws+" + name
		clones, err := instanceClones(s, inst)
		if err != nil {
			return err
		}
		for repo, clone := range clones {
			if cloneBorrowsFrom(s, clone, repo) == nil {
				continue
			}
			if cloneIsFull(clone) != nil || !strings.Contains(run.stderr, "warning: "+repo+" was cloned in full") {
				return fmt.Errorf("%s neither borrows nor is named as cloned in full in its create's output:\n%s", clone, run.stderr)
			}
		}
	}
	return nil
}

// aProvisioningHookRunsWhileTheLockIsHeld holds repoID's mirror lock through
// niwa's own lock helper while a SessionStart hook for a background session
// provisions an instance.
func aProvisioningHookRunsWhileTheLockIsHeld(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	release, err := filelock.TryFileLock(mirror.LockPath(s.homeDir, s.workspaceRoot, repoID), "functional test holder")
	if err != nil {
		return ctx, fmt.Errorf("taking the mirror lock: %w", err)
	}
	defer release()
	return iRunAProvisioningHook(ctx)
}

func iRunAProvisioningHook(ctx context.Context) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	if ctx, err = aBackgroundJobStateForSession(ctx, referenceHookSessionID); err != nil {
		return ctx, err
	}
	start := time.Now()
	ctx, err = iPipeASessionStartHookForSession(ctx, referenceHookSessionID)
	s.lastRunDuration = time.Since(start)
	return ctx, err
}

func theHookTookAtLeast(ctx context.Context, seconds int) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if s.lastRunDuration < time.Duration(seconds)*time.Second {
		return fmt.Errorf("the hook took %s, want at least %ds", s.lastRunDuration, seconds)
	}
	return nil
}

func theHookContextSaysClonedInFull(ctx context.Context, repo, reason string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	var inj struct {
		HookSpecificOutput struct {
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(s.stdout), &inj); err != nil {
		return fmt.Errorf("the hook's stdout is not a JSON object: %w\n%s", err, s.stdout)
	}
	want := strings.TrimPrefix(fullCloneLine(repo, reason), "warning: ")
	if n := strings.Count(inj.HookSpecificOutput.AdditionalContext, want); n != 1 {
		return fmt.Errorf("the hook's context carries %q %d times, want once:\n%s", want, n, inj.HookSpecificOutput.AdditionalContext)
	}
	return nil
}

func theCloneInTheHookInstanceDoesNotBorrow(ctx context.Context, groupRepo string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	return cloneIsFull(filepath.Join(s.workspaceRoot, "myws-"+referenceHookSessionID[:12], groupRepo))
}

func iRunNiwaDispatchForReferenceClones(ctx context.Context) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	if ctx, err = aFakeClaudeForDispatchWithSession(ctx, "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"); err != nil {
		return ctx, err
	}
	return ctx, runNiwa(s, s.workspaceRoot, "niwa dispatch borrow-task --detach")
}

// mirrorStorePacks returns the name of every file in the pack directory of
// every store of the workspace's mirrors.
func mirrorStorePacks(s *testState) (map[string]bool, error) {
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return nil, err
	}
	storePacks, err := filepath.Glob(filepath.Join(dir, ".store", "*", "objects", "pack", "*"))
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, p := range storePacks {
		held[filepath.Base(p)] = true
	}
	return held, nil
}

// noCloneHoldsAMirrorPack checks every clone of inst against every store of
// the workspace's mirrors: no pack file name in the clone is one a store
// holds, and the clone's local config has no gc. or maintenance. key.
func noCloneHoldsAMirrorPack(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	held, err := mirrorStorePacks(s)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		packs, err := fileNames(filepath.Join(clone, ".git", "objects", "pack"))
		if err != nil {
			return err
		}
		for _, p := range packs {
			if held[p] {
				return fmt.Errorf("%s holds %s, a pack a mirror store holds", clone, p)
			}
		}
		out, err := fixtureGitWorkTree(clone, "config", "--local", "--list")
		if err != nil {
			return fmt.Errorf("git config --local --list in %s: %w\n%s", clone, err, out)
		}
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "gc.") || strings.HasPrefix(line, "maintenance.") {
				return fmt.Errorf("%s has the config key %q", clone, line)
			}
		}
	}
	return nil
}

func everyCloneReadsAndFsckIsClean(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		if out, err := fixtureGitWorkTree(clone, "fsck", "--no-progress"); err != nil {
			return fmt.Errorf("git fsck in %s: %w\n%s", clone, err, out)
		}
		if out, err := fixtureGitWorkTree(clone, "log", "--oneline", "HEAD"); err != nil {
			return fmt.Errorf("git log in %s: %w\n%s", clone, err, out)
		}
	}
	return nil
}
