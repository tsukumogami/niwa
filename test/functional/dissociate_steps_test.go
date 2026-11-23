package functional

// Steps for features/dissociate.feature: niwa dissociate making a borrowing
// instance independent of the host mirrors, and niwa apply leaving the
// mirror of a repo turned off alone once nothing borrows from it. The
// borrowing instances come from niwa create in the test build and the
// reference-clone steps; nothing here writes an alternates file.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/cucumber/godog"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// asideName is the name niwa dissociate gives a clone's alternates file while
// it checks the clone.
const asideName = "alternates.niwa-dissociate"

func registerDissociateSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^every clone and worktree in instance "([^"]*)" reads all its history and fsck is clean$`, everyCloneAndWorktreeReadsAll)
	ctx.Step(`^instance "([^"]*)" has (\d+) worktrees?$`, instanceHasWorktrees)
	ctx.Step(`^no clone in instance "([^"]*)" has an alternates file left aside$`, noCloneHasAnAsideFile)
	ctx.Step(`^no clone in instance "([^"]*)" has a gc or maintenance key$`, noCloneHasAGcOrMaintenanceKey)
	ctx.Step(`^I remember the clone of "([^"]*)" in instance "([^"]*)"$`, iRememberTheClone)
	ctx.Step(`^the clone of "([^"]*)" in instance "([^"]*)" is byte-for-byte unchanged$`, theCloneIsUnchanged)
	ctx.Step(`^I remember the alternates files and pack directories of instance "([^"]*)"$`, iRememberTheAlternatesAndPacks)
	ctx.Step(`^the alternates files and pack directories of instance "([^"]*)" are unchanged$`, theAlternatesAndPacksAreUnchanged)
	ctx.Step(`^I run "([^"]*)" from workspace root while the instance lock of "([^"]*)" is held$`, iRunWhileTheInstanceLockIsHeld)
	ctx.Step(`^the state file of "([^"]*)" records its packs at (\d+) bytes each$`, theStateFileRecordsPacksAt)
	ctx.Step(`^the dissociate of the clone of "([^"]*)" in instance "([^"]*)" stopped with its alternates set aside$`, theDissociateStoppedWithTheFileAside)
	ctx.Step(`^the mirror log gained no line for "([^"]*)"$`, theMirrorLogGainedNoLineFor)
}

// dissociateScenario is what the dissociate steps remember.
type dissociateScenario struct {
	// clones holds each remembered clone's files, by clone directory.
	clones map[string]map[string]string
	// objects holds an instance's alternates files and pack directories, by
	// instance name.
	objects map[string]map[string]string
}

func dissociateState(ctx context.Context) (*testState, *dissociateScenario, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return nil, nil, err
	}
	if s.dissociate == nil {
		s.dissociate = &dissociateScenario{
			clones:  map[string]map[string]string{},
			objects: map[string]map[string]string{},
		}
	}
	return s, s.dissociate, nil
}

// instanceWorktrees lists the worktrees niwa worktree create made in inst.
func instanceWorktrees(s *testState, inst string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(s.workspaceRoot, inst, ".niwa", "worktrees"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, filepath.Join(s.workspaceRoot, inst, ".niwa", "worktrees", e.Name()))
		}
	}
	return out, nil
}

func instanceHasWorktrees(ctx context.Context, inst string, n int) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	wts, err := instanceWorktrees(s, inst)
	if err != nil {
		return err
	}
	if len(wts) != n {
		return fmt.Errorf("instance %s has worktrees %v, want %d", inst, wts, n)
	}
	return nil
}

// everyCloneAndWorktreeReadsAll runs git log --all and git fsck in every
// clone of inst and in every worktree of them.
func everyCloneAndWorktreeReadsAll(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	dirs := make([]string, 0, len(clones))
	for _, c := range clones {
		dirs = append(dirs, c)
	}
	wts, err := instanceWorktrees(s, inst)
	if err != nil {
		return err
	}
	dirs = append(dirs, wts...)
	for _, dir := range dirs {
		if out, err := fixtureGitWorkTree(dir, "log", "--all", "--oneline"); err != nil {
			return fmt.Errorf("git log --all in %s: %w\n%s", dir, err, out)
		}
		if out, err := fixtureGitWorkTree(dir, "fsck", "--no-progress"); err != nil {
			return fmt.Errorf("git fsck in %s: %w\n%s", dir, err, out)
		}
	}
	return nil
}

func noCloneHasAnAsideFile(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for _, c := range clones {
		if _, err := os.Lstat(filepath.Join(c, ".git", "objects", "info", asideName)); !os.IsNotExist(err) {
			return fmt.Errorf("%s has %s (%v)", c, asideName, err)
		}
	}
	return nil
}

// noCloneHasAGcOrMaintenanceKey is the config half of the no-shared-pack
// step, for an instance whose clones were repacked: a dissociated clone's
// own pack can hold exactly what a store's pack holds, and then the two
// packs have the same name.
func noCloneHasAGcOrMaintenanceKey(ctx context.Context, inst string) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	clones, err := instanceClones(s, inst)
	if err != nil {
		return err
	}
	for _, clone := range clones {
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

// snapshotTree reads every entry under dir: a regular file's bytes, a link's
// target, and each entry's mode.
func snapshotTree(dir string) (map[string]string, error) {
	snap := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		key := p + " " + info.Mode().String()
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			snap[key] = target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			snap[key] = string(data)
		default:
			snap[key] = ""
		}
		return nil
	})
	return snap, err
}

func sameSnapshot(what string, before, after map[string]string) error {
	for k, v := range before {
		if got, ok := after[k]; !ok {
			return fmt.Errorf("%s: %s is gone", what, k)
		} else if got != v {
			return fmt.Errorf("%s: %s changed", what, k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			return fmt.Errorf("%s: %s is new", what, k)
		}
	}
	return nil
}

func iRememberTheClone(ctx context.Context, groupRepo, inst string) (context.Context, error) {
	s, d, err := dissociateState(ctx)
	if err != nil {
		return ctx, err
	}
	clone := filepath.Join(s.workspaceRoot, inst, groupRepo)
	snap, err := snapshotTree(clone)
	if err != nil {
		return ctx, err
	}
	d.clones[clone] = snap
	return ctx, nil
}

func theCloneIsUnchanged(ctx context.Context, groupRepo, inst string) error {
	s, d, err := dissociateState(ctx)
	if err != nil {
		return err
	}
	clone := filepath.Join(s.workspaceRoot, inst, groupRepo)
	before, ok := d.clones[clone]
	if !ok {
		return fmt.Errorf("the clone %s wasn't remembered", clone)
	}
	after, err := snapshotTree(clone)
	if err != nil {
		return err
	}
	return sameSnapshot(clone, before, after)
}

// objectsSnapshot is every clone's alternates file and leftover file, by
// bytes, and its pack directory's files, by bytes.
func objectsSnapshot(s *testState, inst string) (map[string]string, error) {
	clones, err := instanceClones(s, inst)
	if err != nil {
		return nil, err
	}
	snap := map[string]string{}
	for _, clone := range clones {
		info := filepath.Join(clone, ".git", "objects", "info")
		for _, name := range []string{"alternates", asideName} {
			data, err := os.ReadFile(filepath.Join(info, name))
			switch {
			case err == nil:
				snap[filepath.Join(info, name)] = string(data)
			case !os.IsNotExist(err):
				return nil, err
			}
		}
		pack, err := snapshotTree(filepath.Join(clone, ".git", "objects", "pack"))
		if err != nil {
			return nil, err
		}
		for k, v := range pack {
			snap[k] = v
		}
	}
	return snap, nil
}

func iRememberTheAlternatesAndPacks(ctx context.Context, inst string) (context.Context, error) {
	s, d, err := dissociateState(ctx)
	if err != nil {
		return ctx, err
	}
	snap, err := objectsSnapshot(s, inst)
	if err != nil {
		return ctx, err
	}
	d.objects[inst] = snap
	return ctx, nil
}

func theAlternatesAndPacksAreUnchanged(ctx context.Context, inst string) error {
	s, d, err := dissociateState(ctx)
	if err != nil {
		return err
	}
	before, ok := d.objects[inst]
	if !ok {
		return fmt.Errorf("the alternates files and packs of %s weren't remembered", inst)
	}
	after, err := objectsSnapshot(s, inst)
	if err != nil {
		return err
	}
	return sameSnapshot("instance "+inst, before, after)
}

// iRunWhileTheInstanceLockIsHeld runs command from the workspace root with
// the instance's lock held the way another niwa command holds it.
func iRunWhileTheInstanceLockIsHeld(ctx context.Context, command, inst string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	release, err := filelock.TryFileLock(workspace.InstanceLockPath(s.homeDir, filepath.Join(s.workspaceRoot, inst)), "niwa destroy (pid 4242)")
	if err != nil {
		return ctx, fmt.Errorf("taking the instance lock: %w", err)
	}
	defer release()
	return ctx, runNiwa(s, s.workspaceRoot, command)
}

// theStateFileRecordsPacksAt rewrites the pack sizes repoID's state file
// records, so the free-space check sees a mirror larger than any disk. It's
// the mirror's own record, read by niwa as the size of the history a
// dissociate copies; no alternates file is touched.
func theStateFileRecordsPacksAt(ctx context.Context, repoID string, size int64) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	dir, err := mirrorsDirOf(s)
	if err != nil {
		return ctx, err
	}
	path := filepath.Join(dir, ".meta", repoID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ctx, err
	}
	var st map[string]any
	if err := json.Unmarshal(data, &st); err != nil {
		return ctx, err
	}
	packs, ok := st["packs"].([]any)
	if !ok || len(packs) == 0 {
		return ctx, fmt.Errorf("the state file of %s records no packs", repoID)
	}
	for _, p := range packs {
		entry, ok := p.(map[string]any)
		if !ok {
			return ctx, fmt.Errorf("the state file of %s has a pack entry %v", repoID, p)
		}
		entry["size"] = size
	}
	out, err := json.Marshal(st)
	if err != nil {
		return ctx, err
	}
	return ctx, os.WriteFile(path, append(out, '\n'), 0o600)
}

// theDissociateStoppedWithTheFileAside leaves the clone as a dissociate
// killed right after it set the clone's alternates file aside, before its
// objects were copied: niwa's own alternates file, renamed, with nothing
// written to it.
func theDissociateStoppedWithTheFileAside(ctx context.Context, groupRepo, inst string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	info := filepath.Join(s.workspaceRoot, inst, groupRepo, ".git", "objects", "info")
	return ctx, os.Rename(filepath.Join(info, "alternates"), filepath.Join(info, asideName))
}

// theMirrorLogGainedNoLineFor checks that nothing logged a line for repoID
// since the count: no check, no restore, no refresh.
func theMirrorLogGainedNoLineFor(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	path, err := mirrorLogPath(s)
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if m.logLines == 0 {
			return nil
		}
		return fmt.Errorf("the mirror log is gone; it held %d lines at the count", m.logLines)
	}
	lines, err := newLogLines(s, m)
	if err != nil {
		return err
	}
	for _, line := range lines {
		if line["mirror"] == repoID {
			return fmt.Errorf("the mirror log gained a line for %s: %v", repoID, line)
		}
	}
	return nil
}
