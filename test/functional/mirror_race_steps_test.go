package functional

// Steps for the scenarios in features/mirror-repair.feature that race two
// commands on one broken mirror, make a restore fail and retry it, and relink
// a mirror whose link alone is gone. The race holds the mirror's lock the way
// another niwa command would, through niwa's own lock helper, so both
// commands find the mirror broken before either can restore it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// raceRun is one racing command's result.
type raceRun struct {
	instance, stderr string
	exitCode         int
}

func registerMirrorRaceSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I hold the mirror lock of "([^"]*)" while "([^"]*)" runs in instances "([^"]*)" and "([^"]*)" for (\d+) seconds$`, iHoldTheMirrorLockWhileTwoCommandsRun)
	ctx.Step(`^both racing commands exited 0$`, bothRacingCommandsExitedZero)
	ctx.Step(`^exactly one racing command printed the mirror warning "([^"]*)" and the other printed none$`, exactlyOneRacingCommandWarned)
	ctx.Step(`^the mirror log gained one restore of "([^"]*)" and a healthy check from the other command$`, theMirrorLogGainedOneRestoreAndAHealthyCheck)
	ctx.Step(`^I move the source repo "([^"]*)" away$`, iMoveTheSourceRepoAway)
	ctx.Step(`^I put the source repo "([^"]*)" back$`, iPutTheSourceRepoBack)
	ctx.Step(`^I remember the files of the store of "([^"]*)"$`, iRememberTheStoreFiles)
	ctx.Step(`^the store of "([^"]*)" holds the same files$`, theStoreHoldsTheSameFiles)
	ctx.Step(`^the hook's context carries the notice for "([^"]*)" with fault "([^"]*)" and result "([^"]*)"$`, theHookContextCarriesNoticeWith)
	ctx.Step(`^I delete only the stable link of "([^"]*)"$`, iDeleteOnlyTheStableLink)
	ctx.Step(`^I record the borrowers of "([^"]*)" on disk$`, iRecordTheBorrowersOnDisk)
	ctx.Step(`^the borrowers of "([^"]*)" are unchanged on disk$`, theBorrowersAreUnchangedOnDisk)
}

// borrowerOnDisk reads, without running git, what a restore must leave alone
// in a borrower: its alternates, HEAD, index, packed refs, every loose ref,
// and every tracked file the snapshot listed. With the mirror's pack damaged
// git can't read the borrower at all, so this is the snapshot comparison a
// failed restore can still be held to.
func borrowerOnDisk(clone string, tracked map[string]string) (map[string]string, error) {
	files := map[string]string{}
	gitDir := filepath.Join(clone, ".git")
	for _, rel := range []string{"objects/info/alternates", "HEAD", "index", "packed-refs"} {
		data, err := os.ReadFile(filepath.Join(gitDir, rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		files[".git/"+rel] = string(data)
	}
	err := filepath.WalkDir(filepath.Join(gitDir, "refs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(clone, path)
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		return nil, err
	}
	for rel := range tracked {
		data, err := os.ReadFile(filepath.Join(clone, rel))
		if err != nil {
			return nil, err
		}
		files[rel] = string(data)
	}
	return files, nil
}

func iRecordTheBorrowersOnDisk(ctx context.Context, repoID string) (context.Context, error) {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return ctx, err
	}
	m.rawBorrowers = map[string]map[string]string{}
	for _, clone := range clones {
		snap, ok := m.snapshots[clone]
		if !ok {
			return ctx, fmt.Errorf("no snapshot of %s to take its tracked files from", clone)
		}
		if m.rawBorrowers[clone], err = borrowerOnDisk(clone, snap.files); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func theBorrowersAreUnchangedOnDisk(ctx context.Context, repoID string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := borrowersOf(m, repoID)
	if err != nil {
		return err
	}
	for _, clone := range clones {
		got, err := borrowerOnDisk(clone, m.snapshots[clone].files)
		if err != nil {
			return err
		}
		want := m.rawBorrowers[clone]
		for rel := range maps.Keys(want) {
			if got[rel] != want[rel] {
				return fmt.Errorf("%s in %s changed on disk", rel, clone)
			}
		}
		if len(got) != len(want) {
			return fmt.Errorf("%s holds %d of the recorded files, want %d", clone, len(got), len(want))
		}
	}
	return nil
}

// iHoldTheMirrorLockWhileTwoCommandsRun takes the mirror's lock, starts the
// command in both instances, and releases the lock once both have run for the
// given time, long enough for each to find the mirror broken and wait on the
// lock. It then waits for both to exit.
//
// The second command starts a second after the first. Every niwa apply first
// re-materializes the workspace's config snapshot at the root, and with the
// sandbox's file:// config source it always swaps the directory, so two
// applies started in the same instant can read the root config mid-swap.
// That race is the config snapshot's, not the mirror's; by the time the
// second starts, the first is past it and waiting on the mirror lock.
func iHoldTheMirrorLockWhileTwoCommandsRun(ctx context.Context, repoID, command, inst1, inst2 string, seconds int) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	release, err := filelock.TryFileLock(mirror.LockPath(s.homeDir, s.workspaceRoot, repoID), "functional test holder")
	if err != nil {
		return ctx, fmt.Errorf("taking the mirror lock: %w", err)
	}
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	type running struct {
		cmd    *exec.Cmd
		stderr *strings.Builder
		inst   string
	}
	var runs []running
	for i, inst := range []string{inst1, inst2} {
		if i > 0 {
			time.Sleep(time.Second)
		}
		args := strings.Fields(command)
		if len(args) > 0 && args[0] == "niwa" {
			args[0] = s.binPath
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = filepath.Join(s.workspaceRoot, inst)
		cmd.Env = s.buildEnv()
		stderr := &strings.Builder{}
		cmd.Stdout = &strings.Builder{}
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			for _, r := range runs {
				_ = r.cmd.Process.Kill()
				_ = r.cmd.Wait()
			}
			return ctx, fmt.Errorf("starting %q in %s: %w", command, inst, err)
		}
		runs = append(runs, running{cmd: cmd, stderr: stderr, inst: inst})
	}
	time.Sleep(time.Duration(seconds) * time.Second)
	release()
	released = true

	m.race = nil
	for _, r := range runs {
		code := 0
		if err := r.cmd.Wait(); err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				return ctx, fmt.Errorf("waiting for %q in %s: %w", command, r.inst, err)
			}
			code = exitErr.ExitCode()
		}
		m.race = append(m.race, raceRun{instance: r.inst, stderr: r.stderr.String(), exitCode: code})
	}
	return ctx, nil
}

func bothRacingCommandsExitedZero(ctx context.Context) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if len(m.race) != 2 {
		return fmt.Errorf("%d racing commands ran, want 2", len(m.race))
	}
	for _, r := range m.race {
		if r.exitCode != 0 {
			return fmt.Errorf("the command in %s exited %d:\n%s", r.instance, r.exitCode, r.stderr)
		}
	}
	return nil
}

func exactlyOneRacingCommandWarned(ctx context.Context, want string) error {
	_, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	warned := 0
	for _, r := range m.race {
		lines := mirrorWarnings(r.stderr)
		switch {
		case len(lines) == 0:
		case len(lines) == 1 && strings.TrimSpace(lines[0][strings.Index(lines[0], "warning: "):]) == "warning: "+want:
			warned++
		default:
			return fmt.Errorf("the command in %s printed mirror warnings %q, want none or exactly %q", r.instance, lines, "warning: "+want)
		}
	}
	if warned != 1 {
		return fmt.Errorf("%d racing commands printed %q, want exactly one", warned, want)
	}
	return nil
}

// theMirrorLogGainedOneRestoreAndAHealthyCheck reads the lines both racing
// commands logged: the one that restored logs a check and a restore line,
// both missing and restored; the one that waited and then found the mirror
// restored logs only its check line, with the fault it saw and healthy.
func theMirrorLogGainedOneRestoreAndAHealthyCheck(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	var restores, restoredChecks, healthyChecks int
	for _, line := range lines {
		if line["mirror"] != repoID {
			continue
		}
		var event string
		switch {
		case line["event"] == "restore" && line["result"] == "restored":
			event, restores = "restore", restores+1
		case line["event"] == "check" && line["result"] == "restored":
			event, restoredChecks = "check", restoredChecks+1
		case line["event"] == "check" && line["result"] == "healthy":
			event, healthyChecks = "check", healthyChecks+1
		default:
			return fmt.Errorf("unexpected log line %v", line)
		}
		result, _ := line["result"].(string)
		if err := requireLogLine(line, "apply", repoID, event, "missing", result); err != nil {
			return err
		}
	}
	if restores != 1 || restoredChecks != 1 || healthyChecks != 1 {
		return fmt.Errorf("the log gained %d restore lines, %d restored checks and %d healthy checks, want one each: %v", restores, restoredChecks, healthyChecks, lines)
	}
	return nil
}

// sourceRepoPath is where the local git server keeps a source repo.
func sourceRepoPath(s *testState, name string) string {
	return filepath.Join(s.gitServer.root, name+".git")
}

func iMoveTheSourceRepoAway(ctx context.Context, name string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	path := sourceRepoPath(s, name)
	return ctx, os.Rename(path, path+".away")
}

func iPutTheSourceRepoBack(ctx context.Context, name string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	path := sourceRepoPath(s, name)
	return ctx, os.Rename(path+".away", path)
}

// storeFiles maps each file under the linked store, relative to it, to its
// bytes.
func storeFiles(s *testState, repoID string) (map[string]string, error) {
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return nil, err
	}
	files := map[string]string{}
	err = filepath.WalkDir(store, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(store, path)
		files[rel] = string(data)
		return nil
	})
	return files, err
}

func iRememberTheStoreFiles(ctx context.Context, repoID string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	m.storeFiles, err = storeFiles(s, repoID)
	return ctx, err
}

func theStoreHoldsTheSameFiles(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	got, err := storeFiles(s, repoID)
	if err != nil {
		return err
	}
	if !maps.Equal(got, m.storeFiles) {
		var names []string
		for name := range maps.Keys(got) {
			names = append(names, name)
		}
		return fmt.Errorf("the store's files changed; it now holds %v", names)
	}
	return nil
}

// theHookContextCarriesNoticeWith decodes the hook's stdout and finds the
// notice for repoID with the given fault and result exactly once, naming the
// sandbox's mirror log.
func theHookContextCarriesNoticeWith(ctx context.Context, repoID, fault, result string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	var inj struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	dec := json.NewDecoder(strings.NewReader(s.stdout))
	if err := dec.Decode(&inj); err != nil {
		return fmt.Errorf("the hook's stdout is not a JSON object: %w\n%s", err, s.stdout)
	}
	if dec.More() {
		return fmt.Errorf("the hook's stdout holds more than one JSON object:\n%s", s.stdout)
	}
	if inj.HookSpecificOutput.HookEventName != "SessionStart" {
		return fmt.Errorf("hookEventName = %q, want SessionStart", inj.HookSpecificOutput.HookEventName)
	}
	text := inj.HookSpecificOutput.AdditionalContext
	head := fmt.Sprintf("niwa mirror %s: fault %s, result %s.", repoID, fault, result)
	if n := strings.Count(text, head); n != 1 {
		return fmt.Errorf("the context carries %q %d times, want once:\n%s", head, n, text)
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return err
	}
	if !strings.HasSuffix(text, "Mirror log: "+filepath.Join(dir, ".meta", "mirror.log")) {
		return fmt.Errorf("the notice doesn't name the sandbox's mirror log:\n%s", text)
	}
	return nil
}

// iDeleteOnlyTheStableLink removes the link at the stable path and leaves
// its store and the state file in place.
func iDeleteOnlyTheStableLink(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	if _, err := mirrorStore(s, repoID); err != nil {
		return ctx, err
	}
	stable, err := mirrorStablePath(s, repoID)
	if err != nil {
		return ctx, err
	}
	return ctx, os.Remove(stable)
}
