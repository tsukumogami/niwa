package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tsukumogami/niwa/internal/worktree"
)

// session_steps_test.go holds step definitions for the preserved
// session-lifecycle CLI (`niwa session create` / `destroy` / `list` /
// `detach`) and the attach-availability scenarios. These exercise the
// worktree/session lifecycle directly through the compiled binary; the
// agent-facing MCP surface was removed, so every step drives the CLI.

// iSetUpSingleRepoChanneledWorkspace creates one bare source repo named
// "app" under group "apps" plus a config repo. After `niwa create` the
// instance layout is `<instance>/apps/app/`. Use to exercise the simplest
// topology that provisions a session-capable workspace.
func iSetUpSingleRepoChanneledWorkspace(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, err := s.gitServer.SourceRepo("app")
	if err != nil {
		return ctx, fmt.Errorf("creating source repo %q: %w", "app", err)
	}
	s.repoURLs["app"] = url

	body := fmt.Sprintf(`[workspace]
name = %q

[groups.apps]

[repos.app]
url = %q
group = "apps"
`, name, url)
	cfgURL, err := s.gitServer.ConfigRepo(name, body)
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = cfgURL
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+cfgURL); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iSetUpSingleRepoChanneledWorkspaceWithContent is iSetUpSingleRepoChanneledWorkspace
// plus a repo content layer: the config repo ships content/repos/app.md and the
// workspace.toml declares content_dir + [claude.content.repos.app].source so
// `niwa apply` installs the repo's CLAUDE.local.md. Used to exercise that a
// worktree gets the same repo content a checkout does.
func iSetUpSingleRepoChanneledWorkspaceWithContent(ctx context.Context, name string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	url, err := s.gitServer.SourceRepo("app")
	if err != nil {
		return ctx, fmt.Errorf("creating source repo %q: %w", "app", err)
	}
	s.repoURLs["app"] = url

	const appContent = "# app repo content\n\nThis CLAUDE.local.md came from the app repo content layer.\n"
	body := fmt.Sprintf(`[workspace]
name = %q
content_dir = "content"

[groups.apps]

[repos.app]
url = %q
group = "apps"

[claude.content.repos.app]
source = "repos/app.md"
`, name, url)

	cfgURL, err := s.gitServer.ConfigRepoFiles(name, map[string]string{
		".niwa/workspace.toml":       body,
		".niwa/content/repos/app.md": appContent,
	})
	if err != nil {
		return ctx, fmt.Errorf("creating config repo %q: %w", name, err)
	}
	s.repoURLs[name] = cfgURL
	if err := runNiwa(s, s.workspaceRoot, "niwa init --from "+cfgURL); err != nil {
		return ctx, err
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa init exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// theFileExistsInLastWorktree asserts relPath exists inside the worktree created
// by the previous worktree-create step.
func theFileExistsInLastWorktree(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	if _, err := os.Stat(full); err != nil {
		return fmt.Errorf("expected file %q in worktree %s: %w", relPath, s.lastSessionWorktreePath, err)
	}
	return nil
}

// theFileInLastWorktreeContains asserts relPath inside the last worktree
// contains text.
func theFileInLastWorktreeContains(ctx context.Context, relPath, text string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("reading %q in worktree %s: %w", relPath, s.lastSessionWorktreePath, err)
	}
	if !strings.Contains(string(data), text) {
		return fmt.Errorf("file %q does not contain %q\ncontent:\n%s", full, text, string(data))
	}
	return nil
}

// sessionCreateRE parses the `niwa session create` success line:
//
//	session: created <id> at <path>
var sessionCreateRE = regexp.MustCompile(`session: created (\S+) at (.+)`)

// iCallCreateSession runs `niwa session create <repo> <purpose>` from the
// instance root and stores the session_id + worktree path parsed from the
// success line.
func iCallCreateSession(ctx context.Context, repo, purpose, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa session create %s %q", repo, purpose)
	if err := runNiwa(s, instRoot, cmd); err != nil {
		return ctx, fmt.Errorf("niwa session create: %w", err)
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa session create exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	m := sessionCreateRE.FindStringSubmatch(s.stdout)
	if m == nil {
		return ctx, fmt.Errorf("could not parse session create output: %q", s.stdout)
	}
	s.lastSessionID = strings.TrimSpace(m[1])
	s.lastSessionWorktreePath = strings.TrimSpace(m[2])
	return ctx, nil
}

// iCallCreateWorktree runs `niwa worktree create <repo> <purpose>` (the
// canonical command name) from the instance root and stores the session_id +
// worktree path parsed from the success line. Mirrors iCallCreateSession but
// exercises the renamed command rather than the deprecated alias.
func iCallCreateWorktree(ctx context.Context, repo, purpose, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa worktree create %s %q", repo, purpose)
	if err := runNiwa(s, instRoot, cmd); err != nil {
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

// iCallApplyWorktree runs `niwa worktree apply <session-id>` (the canonical
// command) for the session created by a preceding create step, re-syncing the
// worktree's CLAUDE content idempotently. It resolves the session id from the
// state stored by the create step (mirroring how an operator copies the id out
// of create's output) and asserts a clean exit.
func iCallApplyWorktree(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; create a worktree first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa worktree apply %s", s.lastSessionID)
	if err := runNiwa(s, instRoot, cmd); err != nil {
		return ctx, fmt.Errorf("niwa worktree apply: %w", err)
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa worktree apply exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iCallApplySessionAlias runs `niwa session apply <session-id>` (the deprecated
// alias) for the last session, exercising the alias resolution + deprecation
// notice on the apply verb.
func iCallApplySessionAlias(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; create a worktree first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa session apply %s", s.lastSessionID)
	if err := runNiwa(s, instRoot, cmd); err != nil {
		return ctx, fmt.Errorf("niwa session apply: %w", err)
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa session apply exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iSnapshotLastWorktreeFile records the current bytes of relPath inside the
// last worktree so a later step can assert the content was not changed by an
// idempotent re-run.
func iSnapshotLastWorktreeFile(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("snapshotting %q in worktree %s: %w", relPath, s.lastSessionWorktreePath, err)
	}
	if s.worktreeFileSnapshots == nil {
		s.worktreeFileSnapshots = map[string]string{}
	}
	s.worktreeFileSnapshots[relPath] = string(data)
	return nil
}

// theLastWorktreeFileIsUnchanged asserts relPath inside the last worktree still
// matches the snapshot taken earlier (the idempotency assertion: a second apply
// produced no spurious change).
func theLastWorktreeFileIsUnchanged(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored; create a worktree first")
	}
	want, ok := s.worktreeFileSnapshots[relPath]
	if !ok {
		return fmt.Errorf("no snapshot recorded for %q; snapshot it before asserting", relPath)
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("reading %q in worktree %s: %w", relPath, s.lastSessionWorktreePath, err)
	}
	if string(data) != want {
		return fmt.Errorf("file %q changed after idempotent re-run\nbefore:\n%s\nafter:\n%s", full, want, string(data))
	}
	return nil
}

// theLastCommandStderrContainsDeprecationNotice asserts that the previous
// command printed the `niwa session` deprecation notice to stderr. Used to
// pin the alias contract: invoking via `niwa session` must still work but
// must warn.
func theLastCommandStderrContainsDeprecationNotice(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	const want = `"niwa session" is deprecated; use "niwa worktree"`
	if !strings.Contains(s.stderr, want) {
		return fmt.Errorf("stderr does not contain deprecation notice %q:\nstderr:\n%s", want, s.stderr)
	}
	return nil
}

// iCallDestroySession runs `niwa session destroy <id> --force` for the
// session created by the previous step.
func iCallDestroySession(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; create a session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa session destroy %s --force", s.lastSessionID)
	if err := runNiwa(s, instRoot, cmd); err != nil {
		return ctx, fmt.Errorf("niwa session destroy: %w", err)
	}
	if s.exitCode != 0 {
		return ctx, fmt.Errorf("niwa session destroy exit=%d\nstdout:\n%s\nstderr:\n%s",
			s.exitCode, s.stdout, s.stderr)
	}
	return ctx, nil
}

// iCallDestroySessionWithoutForce runs `niwa session destroy <id>` (no
// --force). When a live attach lock is held the command exits non-zero and
// prints the SESSION_ATTACHED guard message on stderr; callers assert
// against that text via `the last MCP response contains code "..."`.
func iCallDestroySessionWithoutForce(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; create a session first")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa session destroy %s", s.lastSessionID)
	if err := runNiwa(s, instRoot, cmd); err != nil {
		return ctx, fmt.Errorf("niwa session destroy: %w", err)
	}
	return ctx, nil
}

// theLastMCPResponseContainsCode asserts that the last command's combined
// stdout+stderr contains the given token. Retained under its historical
// name so the session_attach feature text is unchanged; the underlying
// surface is now the CLI's stderr rather than an MCP error payload.
func theLastMCPResponseContainsCode(ctx context.Context, code string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	combined := s.stdout + s.stderr
	if !strings.Contains(combined, code) {
		return fmt.Errorf("response does not contain %q:\nstdout:\n%s\nstderr:\n%s", code, s.stdout, s.stderr)
	}
	return nil
}

// theSessionIsActiveInInstance asserts the session state file has status="active".
func theSessionIsActiveInInstance(ctx context.Context, instance string) error {
	return assertSessionStatus(ctx, instance, worktree.SessionStatusActive)
}

// theSessionIsEndedInInstance asserts the session state file has status="ended".
func theSessionIsEndedInInstance(ctx context.Context, instance string) error {
	return assertSessionStatus(ctx, instance, worktree.SessionStatusEnded)
}

func assertSessionStatus(ctx context.Context, instance string, want string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	sessionsDir := filepath.Join(s.workspaceRoot, instance, ".niwa", "sessions")
	state, err := worktree.ReadSessionLifecycleState(sessionsDir, s.lastSessionID)
	if err != nil {
		return fmt.Errorf("ReadSessionLifecycleState: %w", err)
	}
	if state.Status != want {
		return fmt.Errorf("session status = %q, want %q", state.Status, want)
	}
	return nil
}

// theSessionWorktreeExistsInInstance asserts the worktree directory was created.
func theSessionWorktreeExistsInInstance(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	if fi, err := os.Stat(s.lastSessionWorktreePath); err != nil {
		return fmt.Errorf("session worktree missing at %s: %w", s.lastSessionWorktreePath, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", s.lastSessionWorktreePath)
	}
	return nil
}

// theSessionScaffoldDirExistsInWorktree asserts that relPath exists inside the
// session's worktree directory.
func theSessionScaffoldDirExistsInWorktree(ctx context.Context, relPath string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	full := filepath.Join(s.lastSessionWorktreePath, relPath)
	if fi, err := os.Stat(full); err != nil {
		return fmt.Errorf("scaffold dir %q missing: %w", relPath, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", full)
	}
	return nil
}

// iRunNiwaGoWithLastSessionID runs "niwa go <repo> <session-id>" using the
// session ID stored by a preceding create step.
func iRunNiwaGoWithLastSessionID(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; call a create session step first")
	}
	return ctx, runNiwa(s, s.homeDir, "niwa go "+repo+" "+s.lastSessionID)
}

// theResponseFileContainsLastSessionWorktreePath verifies that the response
// file contains the worktree path stored by a preceding create step.
func theResponseFileContainsLastSessionWorktreePath(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no session worktree path stored; call a create session step first")
	}
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return fmt.Errorf("NIWA_RESPONSE_FILE not set in this scenario")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading response file %s: %w", path, err)
	}
	want := s.lastSessionWorktreePath + "\n"
	got := string(data)
	if got != want {
		return fmt.Errorf("response file content mismatch:\n  want: %q\n  got:  %q", want, got)
	}
	return nil
}

// theResponseFileContainsPathUnderWorktreesDir verifies that the response
// file written by niwa session create contains a path under the instance's
// .niwa/worktrees/ directory.
func theResponseFileContainsPathUnderWorktreesDir(ctx context.Context, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path, ok := s.envOverrides["NIWA_RESPONSE_FILE"]
	if !ok {
		return fmt.Errorf("NIWA_RESPONSE_FILE not set in this scenario")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading response file %s: %w", path, err)
	}
	got := strings.TrimRight(string(data), "\n")
	instRoot := filepath.Join(s.workspaceRoot, instance)
	worktreesDir := filepath.Join(instRoot, ".niwa", "worktrees")
	if !strings.HasPrefix(got, worktreesDir) {
		return fmt.Errorf("response file path %q is not under worktrees dir %q", got, worktreesDir)
	}
	if fi, err := os.Stat(got); err != nil {
		return fmt.Errorf("response file path %q does not exist: %w", got, err)
	} else if !fi.IsDir() {
		return fmt.Errorf("response file path %q is not a directory", got)
	}
	return nil
}

// aSessionLifecycleStateExistsForRepo verifies that at least one session
// state file exists for the named repo with the expected status.
func aSessionLifecycleStateExistsForRepo(ctx context.Context, repo, status, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	sessionsDir := filepath.Join(instRoot, ".niwa", "sessions")
	all, err := worktree.ListSessionLifecycleStates(sessionsDir)
	if err != nil {
		return fmt.Errorf("listing session states: %w", err)
	}
	for _, st := range all {
		if st.Repo == repo && st.Status == status {
			return nil
		}
	}
	return fmt.Errorf("no session with repo=%q status=%q found in %s; got %d sessions",
		repo, status, sessionsDir, len(all))
}

func theMainCloneIsOnBranch(ctx context.Context, repoName, instance, branch string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	out, err := runGitInDir(repoPath, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("git branch --show-current: %w", err)
	}
	got := strings.TrimSpace(out)
	if got != branch {
		return fmt.Errorf("repo %q is on branch %q, want %q", repoName, got, branch)
	}
	return nil
}

func theSessionBranchExistsInRepo(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	branchRef := "session/" + s.lastSessionID
	if _, err := runGitInDir(repoPath, "rev-parse", "--verify", branchRef); err != nil {
		return fmt.Errorf("branch %q does not exist in repo %q: %w", branchRef, repoName, err)
	}
	return nil
}

func theSessionBranchDoesNotExistInRepo(ctx context.Context, repoName, instance string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return fmt.Errorf("no session_id stored")
	}
	instRoot := filepath.Join(s.workspaceRoot, instance)
	repoPath, err := findRepoPathInInstance(instRoot, repoName)
	if err != nil {
		return err
	}
	branchRef := "session/" + s.lastSessionID
	if _, err := runGitInDir(repoPath, "rev-parse", "--verify", branchRef); err == nil {
		return fmt.Errorf("branch %q still exists in repo %q after expected deletion", branchRef, repoName)
	}
	return nil
}

func theSessionWorktreeDirectoryDoesNotExist(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return fmt.Errorf("no worktree path stored")
	}
	if _, err := os.Stat(s.lastSessionWorktreePath); err == nil {
		return fmt.Errorf("session worktree directory still exists at %s", s.lastSessionWorktreePath)
	}
	return nil
}

// findRepoPathInInstance scans instanceRoot two levels deep for a directory
// named repoName that contains a .git entry.
func findRepoPathInInstance(instanceRoot, repoName string) (string, error) {
	groups, err := os.ReadDir(instanceRoot)
	if err != nil {
		return "", fmt.Errorf("reading instance root %s: %w", instanceRoot, err)
	}
	for _, g := range groups {
		if !g.IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		groupDir := filepath.Join(instanceRoot, g.Name())
		entries, err := os.ReadDir(groupDir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() != repoName {
				continue
			}
			candidate := filepath.Join(groupDir, e.Name())
			if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("repo %q not found in instance %s", repoName, instanceRoot)
}

// runGitInDir runs a git command in dir and returns combined stdout/stderr.
func runGitInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// iSeedLiveAttachSentinelForLastSession writes a <worktree>/.niwa/attach.state
// JSON file that points at the test process's own PID + start_time. Used by
// the @critical session_attach scenarios to make a session look "attached"
// without actually invoking `niwa session attach` (which would require a
// real claude binary).
func iSeedLiveAttachSentinelForLastSession(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionWorktreePath == "" {
		return ctx, fmt.Errorf("no worktree path stored; create a session first")
	}
	myPID := os.Getpid()
	myStart, _ := worktree.PIDStartTime(myPID)
	state := worktree.AttachState{
		V:              1,
		OwnerPID:       myPID,
		OwnerStartTime: myStart,
		StartedAt:      "2026-05-10T14:32:11Z",
		LockPath:       ".niwa/attach.lock",
	}
	if err := worktree.WriteAttachState(s.lastSessionWorktreePath, state); err != nil {
		return ctx, fmt.Errorf("writing attach state: %w", err)
	}
	return ctx, nil
}

// iRunSessionDetachForLastSessionInInstance runs `niwa session detach <id>`
// against the most recently created session, executed from the given
// instance root.
func iRunSessionDetachForLastSessionInInstance(ctx context.Context, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.lastSessionID == "" {
		return ctx, fmt.Errorf("no session_id stored; create a session first")
	}
	cwd := filepath.Join(s.workspaceRoot, instance)
	cmd := fmt.Sprintf("niwa session detach %s", s.lastSessionID)
	return ctx, runNiwa(s, cwd, cmd)
}

// iRunFromChanneledInstance runs a niwa command with cwd =
// <workspaceRoot>/<instance>. The single-repo channeled workspace fixture
// places the instance directly under workspaceRoot (no extra workspace-
// name folder), so the niwa binary resolves the instance root via its
// walk-up logic.
func iRunFromChanneledInstance(ctx context.Context, command, instance string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	cwd := filepath.Join(s.workspaceRoot, instance)
	return ctx, runNiwa(s, cwd, command)
}
