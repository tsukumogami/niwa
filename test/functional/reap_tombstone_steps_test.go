package functional

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cucumber/godog"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// registerReapTombstoneSteps wires the steps the re-read and tombstone
// scenarios use: an on-reap command that changes the unit after the re-read,
// a lock held by the test process, a tombstone left by an interrupted pass,
// and two reaps started together.
func registerReapTombstoneSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^the workspace configuration declares the on-reap command:$`, theWorkspaceConfigurationDeclaresTheOnReapCommand)
	ctx.Step(`^the file "([^"]*)" still exists in the dispatch instance$`, theFileStillExistsInTheDispatchInstance)
	ctx.Step(`^no tombstone remains in the workspace root$`, noTombstoneRemains)
	ctx.Step(`^the instance lock of the dispatch instance is held by the test$`, theInstanceLockIsHeldByTheTest)
	ctx.Step(`^the tombstone lock is held shared by the test$`, theTombstoneLockIsHeldSharedByTheTest)
	ctx.Step(`^the test releases its locks$`, theTestReleasesItsLocks)
	ctx.Step(`^the dispatch instance is renamed to a tombstone$`, theDispatchInstanceIsRenamedToATombstone)
	ctx.Step(`^the tombstone still exists$`, theTombstoneStillExists)
	ctx.Step(`^the dispatch instance's own name is free$`, theDispatchInstancesOwnNameIsFree)
	ctx.Step(`^every dispatch session is deleted from the Agent View$`, everyDispatchSessionIsDeleted)
	ctx.Step(`^I run two niwa reaps together from the workspace root$`, iRunTwoNiwaReapsTogether)
	ctx.Step(`^both reaps exited 0 and printed no error or warning$`, bothReapsExitedCleanly)
}

// tombstoneState is what these steps keep per scenario: the locks the test
// holds and the tombstone it made.
type tombstoneState struct {
	releases  []func()
	tombstone string
}

var tombstoneStates sync.Map // *testState -> *tombstoneState

func tombstoneStateOf(s *testState) *tombstoneState {
	v, _ := tombstoneStates.LoadOrStore(s, &tombstoneState{})
	return v.(*tombstoneState)
}

func theWorkspaceConfigurationDeclaresTheOnReapCommand(ctx context.Context, doc *godog.DocString) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	path := filepath.Join(s.workspaceRoot, ".niwa", "workspace.toml")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(f, "\n[session.on_reap]\ncommand = %q\n", strings.TrimSpace(doc.Content)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func theFileStillExistsInTheDispatchInstance(ctx context.Context, rel string) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(inst, filepath.FromSlash(rel))); err != nil {
		return fmt.Errorf("%s in the dispatch instance: %v\nstderr:\n%s", rel, err, s.stderr)
	}
	return nil
}

func noTombstoneRemains(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	tombs, err := workspace.ListTombstones(s.workspaceRoot)
	if err != nil {
		return err
	}
	if len(tombs) != 0 {
		return fmt.Errorf("tombstones remain: %v\nstderr:\n%s", tombs, s.stderr)
	}
	return nil
}

func theInstanceLockIsHeldByTheTest(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	release, err := workspace.TryFileLock(workspace.InstanceLockPath(s.homeDir, inst), "functional test (pid 1)")
	if err != nil {
		return fmt.Errorf("taking the instance lock: %w", err)
	}
	ts := tombstoneStateOf(s)
	ts.releases = append(ts.releases, release)
	return nil
}

func theTombstoneLockIsHeldSharedByTheTest(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	release, err := workspace.TryFileLockShared(workspace.TombstoneLockPath(s.homeDir, s.workspaceRoot))
	if err != nil {
		return fmt.Errorf("taking the tombstone lock shared: %w", err)
	}
	ts := tombstoneStateOf(s)
	ts.releases = append(ts.releases, release)
	return nil
}

func theTestReleasesItsLocks(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	ts := tombstoneStateOf(s)
	for _, release := range ts.releases {
		release()
	}
	ts.releases = nil
	return nil
}

func theDispatchInstanceIsRenamedToATombstone(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	tomb, err := workspace.TombstoneInstance(s.workspaceRoot, filepath.Base(inst))
	if err != nil {
		return err
	}
	tombstoneStateOf(s).tombstone = tomb
	return nil
}

func theTombstoneStillExists(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	tomb := tombstoneStateOf(s).tombstone
	if tomb == "" {
		return fmt.Errorf("no tombstone was made")
	}
	if _, err := os.Stat(filepath.Join(tomb, ".niwa", "instance.json")); err != nil {
		return fmt.Errorf("the tombstone %s is gone or changed: %v\nstderr:\n%s", tomb, err, s.stderr)
	}
	return nil
}

func theDispatchInstancesOwnNameIsFree(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	inst, err := dispatchInstanceForStep(s)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(inst); !os.IsNotExist(err) {
		return fmt.Errorf("something is at the dispatch instance's own name %s (%v)\nstderr:\n%s", inst, err, s.stderr)
	}
	return nil
}

func everyDispatchSessionIsDeleted(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	dir := filepath.Join(s.workspaceRoot, ".niwa", "sessions")
	deleted := 0
	for _, name := range sessionMappings(s) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		if m := mappingSession.FindSubmatch(data); m != nil {
			if _, err := theDispatchSessionIsDeleted(ctx, string(m[1])); err != nil {
				return ctx, err
			}
			deleted++
		}
	}
	if deleted == 0 {
		return ctx, fmt.Errorf("no session mapping to delete a session for")
	}
	return ctx, nil
}

// twoReaps holds the output of the last two reaps started together.
type twoReaps struct {
	codes   [2]int
	stderrs [2]string
}

var twoReapsOf sync.Map // *testState -> *twoReaps

func iRunTwoNiwaReapsTogether(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	var r twoReaps
	var cmds [2]*exec.Cmd
	var errs [2]strings.Builder
	for i := range cmds {
		cmd := exec.Command(s.binPath, "reap")
		cmd.Dir = s.workspaceRoot
		cmd.Env = s.buildEnv()
		cmd.Stderr = &errs[i]
		cmds[i] = cmd
	}
	for i, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("starting reap %d: %w", i+1, err)
		}
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				return fmt.Errorf("reap %d: %w", i+1, err)
			}
			r.codes[i] = exitErr.ExitCode()
		}
		r.stderrs[i] = errs[i].String()
	}
	twoReapsOf.Store(s, &r)
	s.stderr = r.stderrs[0] + r.stderrs[1]
	return nil
}

func bothReapsExitedCleanly(ctx context.Context) error {
	s := getState(ctx)
	if s == nil {
		return fmt.Errorf("no test state")
	}
	v, ok := twoReapsOf.Load(s)
	if !ok {
		return fmt.Errorf("no two reaps were run")
	}
	r := v.(*twoReaps)
	for i := range r.codes {
		if r.codes[i] != 0 {
			return fmt.Errorf("reap %d exited %d\nstderr:\n%s", i+1, r.codes[i], r.stderrs[i])
		}
		for _, line := range strings.Split(r.stderrs[i], "\n") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "error") || strings.Contains(lower, "warning") || strings.Contains(lower, "changed during the pass") {
				return fmt.Errorf("reap %d printed %q\nstderr:\n%s", i+1, line, r.stderrs[i])
			}
		}
	}
	return nil
}
