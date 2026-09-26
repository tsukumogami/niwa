package functional

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// worktree_teardown_steps_test.go holds the steps that
// features/worktree-teardown-by-session.feature needs and the suite did not
// already have. Everything else that feature uses -- the local git server, the
// config repo, `niwa init`, the fake claude, `niwa dispatch`, the mapping and
// instance assertions -- comes from the existing dispatch and ephemeral-session
// steps.
//
// What was missing is all of one shape: the suite could only address an
// instance by the literal directory name a scenario passed to `niwa create`,
// and a dispatch instance's name ends in a random hex token. The teardown
// scenarios drive `niwa worktree destroy <session id>` from the workspace root
// against worktrees that live inside *that* instance, so every step here works
// against the recorded dispatch instance (testState.lastDispatchInstancePath)
// instead of a name a feature file could spell.
//
// The three gaps the acceptance criteria name:
//   - creating a worktree inside the recorded dispatch instance;
//   - committing inside that worktree, so its branch is unmerged and the
//     kept-branch warning path runs;
//   - lifecycle, branch, repo and output assertions scoped to that instance.

// dispatchInstanceDir returns the dispatch instance the scenario is working
// against, falling back to a fresh scan when no step recorded one. The fallback
// exists because only two of the dispatch steps record the path; a scenario
// that reached the instance another way still gets the right directory.
func dispatchInstanceDir(s *testState) (string, error) {
	inst := s.lastDispatchInstancePath
	if inst == "" {
		inst = findDispatchInstance(s.workspaceRoot)
	}
	if inst == "" {
		return "", fmt.Errorf("no dispatch instance found under %s; dispatch one first", s.workspaceRoot)
	}
	s.lastDispatchInstancePath = inst
	return inst, nil
}

// iCreateAWorktreeInTheDispatchInstance runs `niwa worktree create <repo>
// "<purpose>"` with the recorded dispatch instance as the working directory,
// and stores the worktree id and path the success line reports.
//
// It is the dispatch-instance twin of iCallCreateWorktree, which can only
// address an instance by a literal name. It stores into the same two state
// fields, so every existing "last worktree" assertion keeps working here.
func iCreateAWorktreeInTheDispatchInstance(ctx context.Context, repo, purpose string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceDir(s)
	if err != nil {
		return ctx, err
	}
	if err := runNiwa(s, inst, fmt.Sprintf("niwa worktree create %s %q", repo, purpose)); err != nil {
		return ctx, fmt.Errorf("niwa worktree create: %w", err)
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa worktree create exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	m := sessionCreateRE.FindStringSubmatch(s.stdout)
	if m == nil {
		return ctx, fmt.Errorf("could not parse worktree create output: %q", s.stdout)
	}
	s.lastSessionID = strings.TrimSpace(m[1])
	s.lastSessionWorktreePath = strings.TrimSpace(m[2])
	return ctx, nil
}

// iCommitAChangeInTheLastWorktree writes relPath into the last worktree and
// commits it, which is what leaves the worktree's branch unmerged: destroy runs
// `git branch -d`, git refuses to delete a branch carrying commits the repo's
// checked-out branch does not have, and the kept-branch warning is produced.
//
// The suite could already make a worktree DIRTY (an uncommitted file, which
// destroy refuses outright) but not CLEAN-AND-AHEAD, which is the state the
// warning path needs: teardown proceeds, the directory goes, the branch stays.
//
// It shells out through fixtureGit rather than fixtureGitCommit because a git
// worktree's `.git` is a file, not a directory, so the GIT_DIR pinning the
// commit helper does would point git at a file. Discovery from inside the
// worktree resolves the right repository, and the sandbox ceiling fixtureGit
// sets is what keeps that discovery from ever walking out of the sandbox. The
// suite's author identity is passed as -c overrides instead, so the commit is
// still attributable to the suite and still independent of the developer's
// ~/.gitconfig.
func iCommitAChangeInTheLastWorktree(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	if err := os.WriteFile(full, []byte("committed work\n"), 0o600); err != nil {
		return fmt.Errorf("writing %q in worktree %s: %w", relPath, s.lastSessionWorktreePath, err)
	}
	if out, err := fixtureGit(s.lastSessionWorktreePath, "add", "--", relPath); err != nil {
		return fmt.Errorf("git add %q in worktree %s: %w\n%s", relPath, s.lastSessionWorktreePath, err, out)
	}
	out, err := fixtureGit(s.lastSessionWorktreePath,
		"-c", "user.name=niwa-test",
		"-c", "user.email=niwa-test@example.com",
		"commit", "-m", "worktree work in progress")
	if err != nil {
		return fmt.Errorf("git commit in worktree %s: %w\n%s", s.lastSessionWorktreePath, err, out)
	}
	return nil
}

// theSessionMappingRecordsHandle asserts the workspace-root mapping for a
// session records exactly the given handle.
//
// It is what makes the handle scenario distinct rather than a second spelling
// of the id scenario. The handle a Claude dispatch records is the name of the
// job directory the worker wrote, which is not the session id; this step pins
// that the value the scenario then passes to destroy is that recorded handle,
// so a reader can see which of the resolver's three match arms is under test.
func theSessionMappingRecordsHandle(ctx context.Context, sessionID, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "sessions", sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading session mapping %s: %w", path, err)
	}
	var m struct {
		Handle string `json:"handle"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return fmt.Errorf("parsing session mapping %s: %w", path, err)
	}
	if m.Handle != want {
		return fmt.Errorf("session mapping %s records handle %q, want %q", path, m.Handle, want)
	}
	return nil
}

// theOutputHasExactlyOneDestroyedLineForTheLastWorktree asserts stdout carries
// exactly one `session: destroyed ...` line and that it is the enriched,
// session-resolved form naming the last worktree's id, its repo and its path.
//
// Both halves matter and neither existing step covers either. "Contains the
// line" would pass if teardown had destroyed a second worktree as well, and the
// bare `session: destroyed <id>` line a worktree-id target prints is a
// substring of the enriched one, so a contains-check could not tell the two
// output contracts apart.
func theOutputHasExactlyOneDestroyedLineForTheLastWorktree(ctx context.Context, repo string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" || s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree recorded; create a worktree first")
	}
	var destroyed []string
	for _, line := range strings.Split(s.stdout, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "session: destroyed ") {
			destroyed = append(destroyed, strings.TrimSpace(line))
		}
	}
	if len(destroyed) != 1 {
		return fmt.Errorf("stdout has %d destroyed lines, want exactly 1:\n%s", len(destroyed), s.stdout)
	}
	want := fmt.Sprintf("session: destroyed %s (%s) at %s",
		s.lastSessionID, repo, s.lastSessionWorktreePath)
	if destroyed[0] != want {
		return fmt.Errorf("destroyed line is %q, want %q", destroyed[0], want)
	}
	return nil
}

// theErrorOutputHasExactlyOneLineContaining counts the stderr lines carrying a
// substring and requires exactly one.
//
// The unmerged-branch scenario needs the count, not the presence: one worktree
// kept means one warning, and a teardown that warned twice about the same
// branch would satisfy "stderr contains" while breaking the outcome contract.
func theErrorOutputHasExactlyOneLineContaining(ctx context.Context, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	n := 0
	for _, line := range strings.Split(s.stderr, "\n") {
		if strings.Contains(line, want) {
			n++
		}
	}
	if n != 1 {
		return fmt.Errorf("stderr has %d lines containing %q, want exactly 1:\n%s", n, want, s.stderr)
	}
	return nil
}

// theLastWorktreeRecordIsEndedInTheDispatchInstance asserts the lifecycle
// record of the last worktree, inside the recorded dispatch instance, reached
// the ended status.
//
// The existing status steps read the lifecycle store under a literally named
// instance directory, which a dispatch instance does not have.
func theLastWorktreeRecordIsEndedInTheDispatchInstance(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no worktree recorded; create a worktree first")
	}
	inst, err := dispatchInstanceDir(s)
	if err != nil {
		return err
	}
	state, err := worktree.ReadSessionLifecycleState(filepath.Join(inst, ".niwa", "sessions"), s.lastSessionID)
	if err != nil {
		return fmt.Errorf("reading lifecycle record for %s in %s: %w", s.lastSessionID, inst, err)
	}
	if state.Status != worktree.SessionStatusEnded {
		return fmt.Errorf("lifecycle record %s in %s has status %q, want %q",
			s.lastSessionID, inst, state.Status, worktree.SessionStatusEnded)
	}
	return nil
}

// lastWorktreeBranchInDispatchRepo resolves the clone of repo inside the
// recorded dispatch instance and the branch name the last worktree was created
// on, which is the pair both branch assertions below need.
func lastWorktreeBranchInDispatchRepo(s *testState, repo string) (repoPath, branch string, err error) {
	if s.lastSessionID == "" {
		return "", "", fmt.Errorf("no worktree recorded; create a worktree first")
	}
	inst, err := dispatchInstanceDir(s)
	if err != nil {
		return "", "", err
	}
	repoPath, err = findRepoPathInInstance(inst, repo)
	if err != nil {
		return "", "", err
	}
	return repoPath, "session/" + s.lastSessionID, nil
}

// theLastWorktreeBranchExistsInDispatchRepo asserts the worktree's branch is
// still in the clone -- the unmerged case, where teardown keeps the branch and
// warns rather than discarding commits.
func theLastWorktreeBranchExistsInDispatchRepo(ctx context.Context, repo string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	repoPath, branch, err := lastWorktreeBranchInDispatchRepo(s, repo)
	if err != nil {
		return err
	}
	if out, err := fixtureGit(repoPath, "rev-parse", "--verify", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("branch %q should still exist in %s: %w\n%s", branch, repoPath, err, out)
	}
	return nil
}

// theLastWorktreeBranchDoesNotExistInDispatchRepo asserts the worktree's branch
// is gone -- the merged case, where `git branch -d` succeeds and teardown is
// complete.
func theLastWorktreeBranchDoesNotExistInDispatchRepo(ctx context.Context, repo string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	repoPath, branch, err := lastWorktreeBranchInDispatchRepo(s, repo)
	if err != nil {
		return err
	}
	if out, err := fixtureGit(repoPath, "rev-parse", "--verify", "refs/heads/"+branch); err == nil {
		return fmt.Errorf("branch %q still exists in %s after teardown:\n%s", branch, repoPath, out)
	}
	return nil
}

// theRepoStillExistsInTheDispatchInstance asserts the cloned repository inside
// the recorded dispatch instance is still a git checkout after a teardown.
//
// It is one of the three survival assertions every destroy scenario makes.
// Teardown by session removes worktrees and nothing else: not the mapping
// (asserted with the existing mapping step), not the instance (the existing
// dispatch-instance step), and not the clones, which is this one. The existing
// repo-exists step takes a literal instance name, and it only stats a
// directory; this checks the clone is still a repository, since an emptied
// directory would pass a stat.
func theRepoStillExistsInTheDispatchInstance(ctx context.Context, repo string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceDir(s)
	if err != nil {
		return err
	}
	repoPath, err := findRepoPathInInstance(inst, repo)
	if err != nil {
		return fmt.Errorf("clone of %q should still exist in dispatch instance %s: %w", repo, inst, err)
	}
	if out, err := fixtureGit(repoPath, "rev-parse", "--is-inside-work-tree"); err != nil {
		return fmt.Errorf("clone of %q at %s is no longer a git work tree: %w\n%s", repo, repoPath, err, out)
	}
	return nil
}

// registerWorktreeTeardownSteps wires the destroy-by-session steps into the
// scenario context. Called from initializeScenario.
func registerWorktreeTeardownSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I create a worktree for repo "([^"]*)" with purpose "([^"]*)" in the dispatch instance$`, iCreateAWorktreeInTheDispatchInstance)
	ctx.Step(`^I commit a change "([^"]*)" in the last worktree$`, iCommitAChangeInTheLastWorktree)
	ctx.Step(`^the session mapping for session "([^"]*)" records handle "([^"]*)"$`, theSessionMappingRecordsHandle)
	ctx.Step(`^the output has exactly one destroyed line for the last worktree in repo "([^"]*)"$`, theOutputHasExactlyOneDestroyedLineForTheLastWorktree)
	ctx.Step(`^the error output has exactly one line containing "([^"]*)"$`, theErrorOutputHasExactlyOneLineContaining)
	ctx.Step(`^the last worktree record is ended in the dispatch instance$`, theLastWorktreeRecordIsEndedInTheDispatchInstance)
	ctx.Step(`^the last worktree branch exists in repo "([^"]*)" of the dispatch instance$`, theLastWorktreeBranchExistsInDispatchRepo)
	ctx.Step(`^the last worktree branch does not exist in repo "([^"]*)" of the dispatch instance$`, theLastWorktreeBranchDoesNotExistInDispatchRepo)
	ctx.Step(`^the repo "([^"]*)" still exists in the dispatch instance$`, theRepoStillExistsInTheDispatchInstance)
}
