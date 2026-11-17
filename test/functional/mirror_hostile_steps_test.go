package functional

// Steps for the hostile-mirror scenario in features/mirror-repair.feature: a
// hook planted in the mirror's store and a store config that names a hook
// directory and an fsmonitor script, each of which writes a marker if git
// ever runs it. Like the steps beside them they reach the store through the
// layout the design fixes rather than importing niwa's mirror package.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cucumber/godog"
)

func registerMirrorHostileSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^I plant a reference-transaction hook that writes a marker in the store of "([^"]*)"$`, iPlantAHookInTheStore)
	ctx.Step(`^I write core\.hooksPath naming the marker hook's directory and core\.fsmonitor naming a marker script into the store config of "([^"]*)"$`, iWriteAHostileStoreConfig)
	ctx.Step(`^I add one commit on main to source repo "([^"]*)"$`, iAddOneCommitOnMain)
	ctx.Step(`^the mirror of "([^"]*)" holds the commit added on main$`, theMirrorHoldsTheCommitAddedOnMain)
	ctx.Step(`^no hook marker exists$`, noHookMarkerExists)
	ctx.Step(`^the sandbox home's empty-hooks directory is empty with mode 0700$`, theEmptyHooksDirectoryIsEmpty)
}

// hostileDir holds the scenario's hook copies, scripts and markers, beside
// the source repos and outside the sandbox home.
func hostileDir(s *testState) string { return filepath.Join(s.gitServer.root, "hostile") }

// hookMarker is what the planted reference-transaction hook writes, and
// fsmonitorMarker what the planted fsmonitor script writes.
func hookMarker(s *testState) string      { return filepath.Join(hostileDir(s), "hook-ran") }
func fsmonitorMarker(s *testState) string { return filepath.Join(hostileDir(s), "fsmonitor-ran") }

// writeMarkerScript writes an executable shell script at path that appends
// a line to marker.
func writeMarkerScript(path, marker string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho ran >> '"+marker+"'\n"), 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

func iPlantAHookInTheStore(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	return ctx, writeMarkerScript(filepath.Join(store, "hooks", "reference-transaction"), hookMarker(s))
}

func iWriteAHostileStoreConfig(ctx context.Context, repoID string) (context.Context, error) {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	store, err := mirrorStore(s, repoID)
	if err != nil {
		return ctx, err
	}
	hooks := filepath.Join(hostileDir(s), "hooks")
	if err := writeMarkerScript(filepath.Join(hooks, "reference-transaction"), hookMarker(s)); err != nil {
		return ctx, err
	}
	script := filepath.Join(hostileDir(s), "fsmonitor.sh")
	if err := writeMarkerScript(script, fsmonitorMarker(s)); err != nil {
		return ctx, err
	}
	config := filepath.Join(store, "config")
	for _, kv := range [][2]string{{"core.hooksPath", hooks}, {"core.fsmonitor", script}} {
		if out, err := fixtureGit(store, "config", "--file", config, kv[0], kv[1]); err != nil {
			return ctx, fmt.Errorf("writing %s into the store's config: %w\n%s", kv[0], err, out)
		}
	}
	return ctx, nil
}

func iAddOneCommitOnMain(ctx context.Context, name string) (context.Context, error) {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return ctx, err
	}
	work, err := os.MkdirTemp(s.gitServer.root, "added-again-*")
	if err != nil {
		return ctx, err
	}
	defer os.RemoveAll(work)
	if out, err := fixtureGit(s.gitServer.root, "clone", "--quiet", s.repoURLs[name], work); err != nil {
		return ctx, fmt.Errorf("cloning %s: %w\n%s", name, err, out)
	}
	if out, err := fixtureGitWorkTree(work, "checkout", "--quiet", "-B", "main", "refs/remotes/origin/main"); err != nil {
		return ctx, fmt.Errorf("git checkout main: %w\n%s", err, out)
	}
	if err := commitAndPush(work, "main", "added-again"); err != nil {
		return ctx, err
	}
	if m.added.tip, err = revParse(work, "HEAD"); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func theMirrorHoldsTheCommitAddedOnMain(ctx context.Context, repoID string) error {
	s, m, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	if m.added.tip == "" {
		return fmt.Errorf("no commit was added on main")
	}
	return requireStoreRefs(s, repoID, map[string]string{"refs/heads/main": m.added.tip})
}

func noHookMarkerExists(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	for _, marker := range []string{hookMarker(s), fsmonitorMarker(s)} {
		if _, err := os.Lstat(marker); !os.IsNotExist(err) {
			return fmt.Errorf("marker %s exists (%v): a planted hook or script ran", marker, err)
		}
	}
	return nil
}

func theEmptyHooksDirectoryIsEmpty(ctx context.Context) error {
	s, _, err := mirrorState(ctx)
	if err != nil {
		return err
	}
	dir := filepath.Join(s.homeDir, ".niwa", "empty-hooks")
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("the empty-hooks directory: %w", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		return fmt.Errorf("%s has mode %s, want a 0700 directory", dir, info.Mode())
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("%s holds %d entries, want none", dir, len(entries))
	}
	return nil
}
