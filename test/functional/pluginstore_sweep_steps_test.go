package functional

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cucumber/godog"
)

// The plugin-release-hold steps for the sweep of unused releases. A scenario
// holds several releases of one source by creating an instance on each, then
// takes instances off disk, moves one under the sandbox home's kept/, or
// makes part of the scan unreadable, and checks which <commit> directories
// the next command leaves in the store.

// pluginSweepScenario is what the sweep scenarios remember.
type pluginSweepScenario struct {
	// unreadable is the path a step made unreadable, and the forms the
	// sweep's line may name it by; unreadableMode is the mode it gets back.
	unreadable      string
	unreadableNames []string
	unreadableMode  os.FileMode
}

func registerPluginSweepSteps(ctx *godog.ScenarioContext) {
	ctx.Step(`^instances? "([^"]*)" (?:is|are) removed from disk$`, instancesAreRemovedFromDisk)
	ctx.Step(`^staging and trash entries are planted in the plugin store of workspace root "([^"]*)" for the releases "([^"]*)" of "([^"]*)"$`, stagingAndTrashArePlanted)
	ctx.Step(`^the plugin store of workspace root "([^"]*)" holds only the releases? "([^"]*)" of "([^"]*)"$`, theStoreHoldsOnlyTheReleases)
	ctx.Step(`^the plugin store of workspace root "([^"]*)" holds no staging or trash entry for the releases "([^"]*)" of "([^"]*)"$`, theStoreHoldsNoStagingOrTrashFor)
	ctx.Step(`^instance "([^"]*)" is moved under the sandbox home's kept/$`, instanceIsMovedUnderKept)
	ctx.Step(`^instance "([^"]*)" is (left as it is|stripped of its \.niwa/instance\.json)$`, instanceIsLeftOrStripped)
	ctx.Step(`^every marketplace's skill resolves in the kept instance "([^"]*)" at its root and in repo "([^"]*)"$`, everySkillResolvesInKeptInstance)
	ctx.Step(`^every marketplace's skill resolves in instance "([^"]*)" at its root and in repo "([^"]*)"$`, everySkillResolvesAtRootAndRepo)
	ctx.Step(`^I run (niwa create ws|niwa apply in instance ws-2|niwa dispatch|the SessionStart hook|niwa reset ws-2|niwa destroy ws-2) beside the kept instance$`, iRunBesideTheKeptInstance)
	ctx.Step(`^(the workspace root|the kept directory|the marketplaces of instance "[^"]*") cannot be read$`, thePathCannotBeRead)
	ctx.Step(`^its mode is restored$`, itsModeIsRestored)
	ctx.Step(`^the output reports the sweep skipped once, naming the path that could not be read, and no repair scan warning$`, theOutputReportsTheSweepSkippedOnce)
	ctx.Step(`^the plugin store of the removed workspace root is gone, and its key directory with it unless something else is in it$`, theStoreOfTheRemovedRootIsGone)
	ctx.Step(`^the plugin store of the removed workspace root is kept, and the output names its path$`, theStoreOfTheRemovedRootIsKept)
}

// keyDirOf is ~/.niwa/w-<key> of the workspace root, computed from its real
// path; for a root already removed, from its parent's real path.
func keyDirOf(s *testState, root string) (string, error) {
	dir := pluginRootDir(s, root)
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		parent, perr := filepath.EvalSymlinks(filepath.Dir(dir))
		if perr != nil {
			return "", fmt.Errorf("resolving %s: %w", dir, err)
		}
		real = filepath.Join(parent, filepath.Base(dir))
	}
	sum := sha256.Sum256([]byte(real))
	return filepath.Join(s.homeDir, ".niwa", "w-"+hex.EncodeToString(sum[:])[:32]), nil
}

// keptDirOf is the kept/ directory beside the suite root's store.
func keptDirOf(s *testState) (string, error) {
	key, err := keyDirOf(s, ".")
	if err != nil {
		return "", err
	}
	return filepath.Join(key, "kept"), nil
}

// releaseCommits resolves a comma-separated list of tags of slug to their
// commits.
func releaseCommits(ps *pluginStoreScenario, tags, slug string) (*pluginMarketplace, []string, error) {
	mk := ps.marketplaces[slug]
	if mk == nil {
		return nil, nil, fmt.Errorf("no published marketplace %s", slug)
	}
	var commits []string
	for _, tag := range strings.Split(tags, ",") {
		tag = strings.TrimSpace(tag)
		found := ""
		for _, r := range mk.releases {
			if r.tag == tag {
				found = r.commit
			}
		}
		if found == "" {
			return nil, nil, fmt.Errorf("%s has no release %s", slug, tag)
		}
		commits = append(commits, found)
	}
	return mk, commits, nil
}

// stagingPrefix is the name prefix of a release's staging and trash entries:
// the first 32 hex of the sha256 of its id, and a dash.
func stagingPrefix(mk *pluginMarketplace, commit string) string {
	sum := sha256.Sum256([]byte("github.com/" + mk.owner + "/" + mk.repo + "/" + commit))
	return hex.EncodeToString(sum[:])[:32] + "-"
}

func instancesAreRemovedFromDisk(ctx context.Context, names string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	for _, name := range strings.Split(names, ",") {
		dir := filepath.Join(s.workspaceRoot, strings.TrimSpace(name))
		if _, err := os.Lstat(dir); err != nil {
			return ctx, fmt.Errorf("instance %s: %w", dir, err)
		}
		restoreWriteBits(dir)
		if err := os.RemoveAll(dir); err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

// stagingAndTrashArePlanted makes one entry in .tmp/ and one in .trash/ for
// each release, as a killed placement or removal leaves them.
func stagingAndTrashArePlanted(ctx context.Context, root, tags, slug string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	mk, commits, err := releaseCommits(ps, tags, slug)
	if err != nil {
		return ctx, err
	}
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return ctx, err
	}
	for _, sub := range []string{".tmp", ".trash"} {
		for _, commit := range commits {
			entry := filepath.Join(store, sub, stagingPrefix(mk, commit)+"0badf00d")
			if err := os.MkdirAll(filepath.Join(entry, "tree"), 0o700); err != nil {
				return ctx, err
			}
		}
	}
	return ctx, nil
}

// theStoreHoldsOnlyTheReleases checks the <commit> directories of slug in
// the store are exactly those of the listed releases, each with its
// held.json.
func theStoreHoldsOnlyTheReleases(ctx context.Context, root, tags, slug string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, want, err := releaseCommits(ps, tags, slug)
	if err != nil {
		return err
	}
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return err
	}
	repoDir := filepath.Join(store, "github.com", mk.owner, mk.repo)
	entries, err := os.ReadDir(repoDir)
	if err != nil {
		return err
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("%s holds %v, want exactly %v (%s)", repoDir, got, want, tags)
	}
	for _, commit := range want {
		if _, err := os.Lstat(filepath.Join(repoDir, commit, "held.json")); err != nil {
			return fmt.Errorf("%s of %s is not marked: %w", commit, slug, err)
		}
	}
	return nil
}

func theStoreHoldsNoStagingOrTrashFor(ctx context.Context, root, tags, slug string) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	mk, commits, err := releaseCommits(ps, tags, slug)
	if err != nil {
		return err
	}
	store, err := pluginStoreDirOf(s, root)
	if err != nil {
		return err
	}
	for _, sub := range []string{".tmp", ".trash"} {
		entries, err := os.ReadDir(filepath.Join(store, sub))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, commit := range commits {
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), stagingPrefix(mk, commit)) {
					return fmt.Errorf("%s/%s/%s of %s is left", store, sub, e.Name(), commit)
				}
			}
		}
	}
	return nil
}

// instanceIsMovedUnderKept moves the instance under the sandbox home's
// ~/.niwa/w-<key>/kept/, as a teardown that keeps an instance outside the
// root does. The instance's own absolute links into itself (its skill
// deliveries) are pointed at its new place; its marketplace links into the
// store are left exactly as niwa wrote them.
func instanceIsMovedUnderKept(ctx context.Context, name string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	kept, err := keptDirOf(s)
	if err != nil {
		return ctx, err
	}
	if err := os.MkdirAll(kept, 0o700); err != nil {
		return ctx, err
	}
	from := filepath.Join(s.workspaceRoot, name)
	to := filepath.Join(kept, name)
	if out, err := exec.Command("mv", from, to).CombinedOutput(); err != nil {
		return ctx, fmt.Errorf("mv %s %s: %w\n%s", from, to, err, out)
	}
	prefixes := []string{from}
	if real, err := filepath.EvalSymlinks(s.workspaceRoot); err == nil {
		prefixes = append(prefixes, filepath.Join(real, name))
	}
	err = filepath.WalkDir(to, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			return err
		}
		for _, prefix := range prefixes {
			if rest, ok := strings.CutPrefix(target, prefix+string(filepath.Separator)); ok {
				if err := os.Remove(p); err != nil {
					return err
				}
				return os.Symlink(filepath.Join(to, rest), p)
			}
		}
		return nil
	})
	return ctx, err
}

func instanceIsLeftOrStripped(ctx context.Context, name, how string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	if how == "left as it is" {
		return ctx, nil
	}
	return ctx, os.Remove(filepath.Join(s.workspaceRoot, name, ".niwa", "instance.json"))
}

func everySkillResolvesInKeptInstance(ctx context.Context, name, repoName string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	kept, err := keptDirOf(s)
	if err != nil {
		return ctx, err
	}
	return skillsResolveIn(ctx, filepath.Join(kept, name), repoName, "")
}

func everySkillResolvesAtRootAndRepo(ctx context.Context, name, repoName string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	return skillsResolveIn(ctx, filepath.Join(s.workspaceRoot, name), repoName, "")
}

// iRunBesideTheKeptInstance runs one provisioning or removal command of the
// workspace while an instance is kept under kept/. A command that makes an
// instance goes through the second-instance step, which checks that it made
// exactly one.
func iRunBesideTheKeptInstance(ctx context.Context, command string) (context.Context, error) {
	s, _, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	switch command {
	case "niwa create ws", "niwa dispatch", "the SessionStart hook":
		return iMakeTheSecondInstanceWith(ctx, command)
	case "niwa apply in instance ws-2":
		return ctx, runNiwa(s, filepath.Join(s.workspaceRoot, "ws-2"), "niwa apply")
	case "niwa reset ws-2":
		return ctx, runNiwa(s, s.workspaceRoot, "niwa reset ws-2 --force")
	case "niwa destroy ws-2":
		return ctx, runNiwa(s, s.workspaceRoot, "niwa destroy ws-2 --force")
	}
	return ctx, fmt.Errorf("unknown command %q", command)
}

// thePathCannotBeRead narrows one part of what the sweep scans: the root to
// 0311 (entered but not listed), the kept/ directory to 000 (made first when
// missing), or an instance's .niwa/marketplaces to 000. The mode comes back
// when the scenario ends, or earlier with "its mode is restored".
func thePathCannotBeRead(ctx context.Context, what string) (context.Context, error) {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	var path string
	mode, restore := os.FileMode(0), os.FileMode(0o755)
	switch {
	case what == "the workspace root":
		path, mode = s.workspaceRoot, 0o311
	case what == "the kept directory":
		if path, err = keptDirOf(s); err != nil {
			return ctx, err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return ctx, err
		}
		restore = 0o700
	default:
		name := strings.TrimSuffix(strings.TrimPrefix(what, `the marketplaces of instance "`), `"`)
		path = filepath.Join(s.workspaceRoot, name, ".niwa", "marketplaces")
	}
	names := []string{path}
	if real, err := filepath.EvalSymlinks(path); err == nil && real != path {
		names = append(names, real)
	}
	if err := os.Chmod(path, mode); err != nil {
		return ctx, err
	}
	ps.sweep = pluginSweepScenario{unreadable: path, unreadableNames: names, unreadableMode: restore}
	s.cleanups = append(s.cleanups, func() { _ = os.Chmod(path, restore) })
	return ctx, nil
}

func itsModeIsRestored(ctx context.Context) (context.Context, error) {
	_, ps, err := pluginState(ctx)
	if err != nil {
		return ctx, err
	}
	if ps.sweep.unreadable == "" {
		return ctx, fmt.Errorf("no path was made unreadable")
	}
	return ctx, os.Chmod(ps.sweep.unreadable, ps.sweep.unreadableMode)
}

// theOutputReportsTheSweepSkippedOnce checks the last command's output for
// exactly one line about the failed scan: the sweep's skipped line, naming
// the unreadable path. Repair's own "not every instance could be listed"
// line must not be printed beside it.
func theOutputReportsTheSweepSkippedOnce(ctx context.Context) error {
	s, ps, err := pluginState(ctx)
	if err != nil {
		return err
	}
	out := commandOutput(s)
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "sweep of unused releases") || strings.Contains(line, "could be listed") || strings.Contains(line, "permission denied") {
			lines = append(lines, line)
		}
	}
	if len(lines) != 1 {
		return fmt.Errorf("%d lines about the failed scan, want 1:\n%s", len(lines), out)
	}
	line := lines[0]
	if !strings.Contains(line, "the plugin store's sweep of unused releases was skipped: ") || !strings.Contains(line, "nothing was deleted") {
		return fmt.Errorf("the line is not the sweep's skipped line:\n%s", line)
	}
	named := false
	for _, name := range ps.sweep.unreadableNames {
		named = named || strings.Contains(line, name)
	}
	if !named {
		return fmt.Errorf("the line names none of %v:\n%s", ps.sweep.unreadableNames, line)
	}
	return nil
}

// theStoreOfTheRemovedRootIsGone checks, after the suite root was destroyed,
// that its plugin store is gone and that its key directory is gone too
// unless it still holds something besides the store.
func theStoreOfTheRemovedRootIsGone(ctx context.Context) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	key, err := keyDirOf(s, ".")
	if err != nil {
		return err
	}
	store := filepath.Join(key, "plugins")
	if _, err := os.Lstat(store); !os.IsNotExist(err) {
		return fmt.Errorf("%s survived the workspace (%v)", store, err)
	}
	entries, err := os.ReadDir(key)
	switch {
	case os.IsNotExist(err):
		return nil
	case err != nil:
		return err
	case len(entries) == 0:
		return fmt.Errorf("%s is left empty", key)
	}
	return nil
}

// theStoreOfTheRemovedRootIsKept checks the store survived the workspace and
// the output said where it is.
func theStoreOfTheRemovedRootIsKept(ctx context.Context) error {
	s, _, err := pluginState(ctx)
	if err != nil {
		return err
	}
	key, err := keyDirOf(s, ".")
	if err != nil {
		return err
	}
	store := filepath.Join(key, "plugins")
	if info, err := os.Lstat(store); err != nil || !info.IsDir() {
		return fmt.Errorf("%s did not survive (%v)", store, err)
	}
	want := "kept the plugin store at " + store + ": kept instances still link into it"
	if out := commandOutput(s); !strings.Contains(out, want) {
		return fmt.Errorf("the output does not say %q:\n%s", want, out)
	}
	return nil
}
