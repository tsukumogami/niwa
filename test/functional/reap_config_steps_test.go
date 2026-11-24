package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"
)

// registerReapConfigSteps wires the steps the configuration-refusal
// scenarios use: keys set in a dispatch instance's clone to a script that
// writes a marker file, a filter planted between a configuration read and
// the status after it, a PATH holding planted git binaries, and the checks
// that no such program ran.
func registerReapConfigSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^repo "([^"]*)" of the dispatch instance sets "([^"]*)" to a marker-writing script$`, repoSetsKeyToAMarkerScript)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance sets "([^"]*)" to "([^"]*)"$`, repoSetsKeyTo)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance sets "([^"]*)" in its config\.worktree to a marker-writing script$`, repoSetsWorktreeKeyToAMarkerScript)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance selects filter x for every path and has a stat-dirty tracked file$`, repoSelectsFilterX)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance holds a signed commit in refs/stash$`, repoHoldsASignedStash)
	ctx.Step(`^a marker-writing filter x is planted between the (rule|re-read)'s configuration read and its status$`, aFilterIsPlantedBeforeStatus)
	ctx.Step(`^the planted filter is removed from repo "([^"]*)" of the dispatch instance$`, thePlantedFilterIsRemoved)
	ctx.Step(`^no marker-writing script ran$`, noMarkerScriptRan)
	ctx.Step(`^the host's git is older than 2\.45$`, theHostsGitIsOlderThan245)
	ctx.Step(`^repo "([^"]*)" of the dispatch instance is owned by a second uid$`, repoIsOwnedByASecondUID)
	ctx.Step(`^niwa runs with a PATH that starts with an empty entry and a directory linked into the workspace root, each holding a planted git, then a directory whose git records each call$`, niwaRunsWithAPlantedPATH)
	ctx.Step(`^no planted git ran$`, noPlantedGitRan)
	ctx.Step(`^the recording git ran$`, theRecordingGitRan)
}

// configMarkerDir is where the scenario's marker-writing scripts and their
// markers live: under the scenario's own temp directory, outside the
// workspace.
func configMarkerDir(s *testState) (string, error) {
	dir := filepath.Join(s.tmpDir, "config-markers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// markerScriptFor writes a script that creates a marker file named for
// label and passes its input through, as a filter does, and returns its
// path.
func markerScriptFor(s *testState, label string) (string, error) {
	dir, err := configMarkerDir(s)
	if err != nil {
		return "", err
	}
	name := strings.NewReplacer(".", "-", "/", "-", ":", "-").Replace(label)
	marker := filepath.Join(dir, "ran-"+name)
	script := filepath.Join(dir, name+".sh")
	body := "#!/bin/sh\ntouch '" + marker + "'\ncat\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		return "", err
	}
	return script, nil
}

// cloneGit runs git in the dispatch instance's clone of repo, outside niwa,
// with no global or system configuration, and with the fsmonitor and hooks
// turned off, so the scenario's own git calls never start a program the
// scenario just set.
func cloneGit(s *testState, repo string, args ...string) (string, error) {
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return "", err
	}
	clone := filepath.Join(inst, filepath.FromSlash(repo))
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.Command("git", argv...)
	cmd.Dir = clone
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %v in %s: %v\n%s", args, clone, err, stderr.String())
	}
	return strings.TrimSpace(string(out)), nil
}

func repoSetsKeyToAMarkerScript(ctx context.Context, repo, key string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	script, err := markerScriptFor(s, key)
	if err != nil {
		return ctx, err
	}
	_, err = cloneGit(s, repo, "config", key, script)
	return ctx, err
}

func repoSetsKeyTo(ctx context.Context, repo, key, value string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	_, err := cloneGit(s, repo, "config", key, value)
	return ctx, err
}

// repoSetsWorktreeKeyToAMarkerScript turns on extensions.worktreeConfig and
// sets key only in the clone's config.worktree.
func repoSetsWorktreeKeyToAMarkerScript(ctx context.Context, repo, key string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	script, err := markerScriptFor(s, key)
	if err != nil {
		return ctx, err
	}
	if _, err := cloneGit(s, repo, "config", "extensions.worktreeConfig", "true"); err != nil {
		return ctx, err
	}
	if _, err := cloneGit(s, repo, "config", "--worktree", key, script); err != nil {
		return ctx, err
	}
	local, err := cloneGit(s, repo, "config", "--local", "--list")
	if err != nil {
		return ctx, err
	}
	if strings.Contains(strings.ToLower(local), strings.ToLower(key)) {
		return ctx, fmt.Errorf("%s landed in the local configuration, not config.worktree", key)
	}
	return ctx, nil
}

// repoSelectsFilterX makes every path in the clone select filter x through
// its info/attributes, which no status lists, and gives one tracked file a
// new modification time with its content unchanged, so a status there runs
// the filter's clean program when one is configured.
func repoSelectsFilterX(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	gitDir, err := cloneGit(s, repo, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return ctx, err
	}
	if err := os.MkdirAll(filepath.Join(gitDir, "info"), 0o755); err != nil {
		return ctx, err
	}
	if err := os.WriteFile(filepath.Join(gitDir, "info", "attributes"), []byte("* filter=x\n"), 0o644); err != nil {
		return ctx, err
	}
	files, err := cloneGit(s, repo, "ls-files")
	if err != nil {
		return ctx, err
	}
	first, _, _ := strings.Cut(files, "\n")
	if first == "" {
		return ctx, fmt.Errorf("repo %s tracks no file", repo)
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	later := time.Now().Add(time.Hour)
	return ctx, os.Chtimes(filepath.Join(inst, filepath.FromSlash(repo), first), later, later)
}

// repoHoldsASignedStash writes a commit carrying a gpgsig header and points
// refs/stash, with a reflog entry, at it, so a stash listing that checked
// signatures would run gpg.program.
func repoHoldsASignedStash(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	tree, err := cloneGit(s, repo, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return ctx, err
	}
	head, err := cloneGit(s, repo, "rev-parse", "HEAD")
	if err != nil {
		return ctx, err
	}
	body := "tree " + tree + "\nparent " + head + "\n" +
		"author t <t@example.com> 0 +0000\ncommitter t <t@example.com> 0 +0000\n" +
		"gpgsig -----BEGIN PGP SIGNATURE-----\n \n iQEzBAABCAAdFiEE\n -----END PGP SIGNATURE-----\n\nOn main: signed\n"
	dir, err := configMarkerDir(s)
	if err != nil {
		return ctx, err
	}
	obj := filepath.Join(dir, "signed-commit")
	if err := os.WriteFile(obj, []byte(body), 0o644); err != nil {
		return ctx, err
	}
	id, err := cloneGit(s, repo, "hash-object", "-t", "commit", "-w", obj)
	if err != nil {
		return ctx, err
	}
	_, err = cloneGit(s, repo, "update-ref", "--create-reflog", "-m", "On main: signed", "refs/stash", id)
	return ctx, err
}

// aFilterIsPlantedBeforeStatus has the test build's hook set filter.x.clean
// in the repository's configuration at the named point, between a
// configuration read and the status after it.
func aFilterIsPlantedBeforeStatus(ctx context.Context, stage string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	script, err := markerScriptFor(s, "filter.x.clean")
	if err != nil {
		return ctx, err
	}
	at := "rule"
	if stage == "re-read" {
		at = "reread"
	}
	s.envOverrides["NIWA_TEST_REMOVAL_PLANT_AT"] = at
	s.envOverrides["NIWA_TEST_REMOVAL_PLANT_CMD"] = `git config --file "$NIWA_PLANT_GIT_DIR/config" filter.x.clean '` + script + `'`
	return ctx, nil
}

func thePlantedFilterIsRemoved(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	_, err := cloneGit(s, repo, "config", "--unset", "filter.x.clean")
	return ctx, err
}

func noMarkerScriptRan(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	dir, err := configMarkerDir(s)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "ran-") {
			return fmt.Errorf("a program the repository's configuration names ran: %s\nstderr:\n%s", e.Name(), s.stderr)
		}
	}
	return nil
}

// theHostsGitIsOlderThan245 skips the scenario, with the reason printed, on
// a host whose git honors GIT_NO_LAZY_FETCH: the partial-clone keep is for
// an older git, and the unit tests cover it with a faked version.
func theHostsGitIsOlderThan245(ctx context.Context) (context.Context, error) {
	out, err := exec.Command("git", "version").Output()
	if err != nil {
		return ctx, err
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return ctx, fmt.Errorf("git version printed %q", out)
	}
	parts := strings.Split(fields[2], ".")
	if len(parts) >= 2 {
		major, err1 := strconv.Atoi(parts[0])
		minor, err2 := strconv.Atoi(parts[1])
		if err1 == nil && err2 == nil && (major > 2 || (major == 2 && minor >= 45)) {
			fmt.Fprintf(os.Stderr, "skipped: this host's git is %d.%d, which honors GIT_NO_LAZY_FETCH, so the partial-clone keep doesn't apply; the unit tests cover it with a faked version\n", major, minor)
			return ctx, godog.ErrSkip
		}
	}
	return ctx, nil
}

// repoIsOwnedByASecondUID gives the clone to another uid, which only root
// can do. Without root the scenario is skipped with the reason printed; the
// unit tests cover git's ownership refusal through a faked git either way.
func repoIsOwnedByASecondUID(ctx context.Context, repo string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if os.Geteuid() != 0 {
		fmt.Fprintf(os.Stderr, "skipped: a repository owned by a second uid can't be made without root; the unit tests cover git's ownership refusal through a faked git\n")
		return ctx, godog.ErrSkip
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return ctx, err
	}
	clone := filepath.Join(inst, filepath.FromSlash(repo))
	if out, err := exec.Command("chown", "-R", "1:1", clone).CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("chown %s: %v\n%s", clone, err, out)
	}
	return ctx, nil
}

// plantedGitDirs are where the PATH scenario's planted git binaries and
// recording git live: the workspace root (the empty entry resolves to the
// directory niwa runs in), a directory inside the workspace root reached by
// a link from outside it, and a directory outside it.
type plantedGitDirs struct {
	markers, recordLog string
}

func plantedGitPaths(s *testState) (plantedGitDirs, error) {
	dir, err := configMarkerDir(s)
	if err != nil {
		return plantedGitDirs{}, err
	}
	return plantedGitDirs{markers: dir, recordLog: filepath.Join(dir, "recording-git.log")}, nil
}

// niwaRunsWithAPlantedPATH builds the PATH niwa runs with: an empty entry
// (the directory niwa runs from, the workspace root, holds a planted git),
// a link from outside the workspace root to a directory inside it holding
// another, then a directory outside the root whose git records each call
// and runs the real git, then the PATH niwa ran with before.
func niwaRunsWithAPlantedPATH(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	real, err := exec.LookPath("git")
	if err != nil {
		return ctx, err
	}
	p, err := plantedGitPaths(s)
	if err != nil {
		return ctx, err
	}
	planted := func(dir, label string) error {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		body := "#!/bin/sh\ntouch '" + filepath.Join(p.markers, "ran-planted-git-"+label) + "'\nexit 1\n"
		return os.WriteFile(filepath.Join(dir, "git"), []byte(body), 0o755)
	}
	if err := planted(s.workspaceRoot, "empty-entry"); err != nil {
		return ctx, err
	}
	inside := filepath.Join(s.workspaceRoot, "planted-bin")
	if err := planted(inside, "linked-entry"); err != nil {
		return ctx, err
	}
	link := filepath.Join(s.tmpDir, "linked-bin")
	if err := os.Symlink(inside, link); err != nil {
		return ctx, err
	}
	recording := filepath.Join(s.tmpDir, "recording-bin")
	if err := os.MkdirAll(recording, 0o755); err != nil {
		return ctx, err
	}
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + p.recordLog + "'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(recording, "git"), []byte(body), 0o755); err != nil {
		return ctx, err
	}
	for _, d := range []string{link, recording} {
		if rel, err := filepath.Rel(s.workspaceRoot, d); err == nil && !strings.HasPrefix(rel, "..") {
			return ctx, fmt.Errorf("%s sits inside the workspace root; the scenario needs it outside", d)
		}
	}
	before := ""
	for _, kv := range s.buildEnv() {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			before = v
		}
	}
	s.envOverrides["PATH"] = strings.Join([]string{"", link, recording, before}, string(os.PathListSeparator))
	return ctx, nil
}

func noPlantedGitRan(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	p, err := plantedGitPaths(s)
	if err != nil {
		return err
	}
	for _, label := range []string{"empty-entry", "linked-entry"} {
		if _, err := os.Stat(filepath.Join(p.markers, "ran-planted-git-"+label)); err == nil {
			return fmt.Errorf("the git planted in the %s ran\nstderr:\n%s", label, s.stderr)
		}
	}
	return nil
}

func theRecordingGitRan(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	p, err := plantedGitPaths(s)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(p.recordLog)
	if err != nil || strings.TrimSpace(string(data)) == "" {
		return fmt.Errorf("the git on the first entry that survives the filter never ran: %v\nstderr:\n%s", err, s.stderr)
	}
	return nil
}
