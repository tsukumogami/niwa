package mirror

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// dissociateFixture is a workspace whose three repos (app, docs, lib) each
// have a remote and a seeded mirror, and an instance whose clones of them
// borrow through the stable path, so their own object stores hold nothing
// the mirror holds.
type dissociateFixture struct {
	home, root, instance string
	clones               []string
}

var dissociateRepos = []string{"app", "docs", "lib"}

func newDissociateFixture(t *testing.T) dissociateFixture {
	t.Helper()
	base := t.TempDir()
	f := dissociateFixture{
		home:     filepath.Join(base, "home"),
		root:     filepath.Join(base, "ws"),
		instance: filepath.Join(base, "ws", "inst"),
	}
	mkdir(t, f.home)
	for _, id := range dissociateRepos {
		remote := filepath.Join(base, id+".git")
		work := filepath.Join(base, "work-"+id)
		testGit(t, base, "init", "--quiet", "--bare", remote)
		testGit(t, base, "init", "--quiet", work)
		for _, name := range []string{"a.txt", "b.txt"} {
			writeFile(t, filepath.Join(work, name), id+" "+name+"\n")
			testGit(t, work, "add", name)
			testGit(t, work, "commit", "--quiet", "-m", name)
		}
		testGit(t, work, "push", "--quiet", remote, "HEAD:refs/heads/main")
		testGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

		opts := Options{Home: f.home, WorkspaceRoot: f.root, Command: "create"}
		if res := Seed(context.Background(), opts, Repo{ID: id, URL: "file://" + remote}); res.Outcome != OutcomeRestored || res.Err != nil {
			t.Fatalf("seeding the mirror of %s: %+v", id, res)
		}
		stable := StablePath(f.home, f.root, id)
		clone := filepath.Join(f.instance, "tools", id)
		mkdir(t, filepath.Dir(clone))
		testGit(t, base, "clone", "--quiet", "--reference", stable, "file://"+remote, clone)
		writeFile(t, filepath.Join(clone, ".git", "objects", "info", "alternates"), stable+"/objects\n")
		f.clones = append(f.clones, clone)
	}
	return f
}

func (f dissociateFixture) opts() DissociateOptions {
	return DissociateOptions{Home: f.home, WorkspaceRoot: f.root, Instance: f.instance, Clones: f.clones}
}

func (f dissociateFixture) run(t *testing.T) (DissociateReport, error) {
	t.Helper()
	return Dissociate(context.Background(), f.opts())
}

func (f dissociateFixture) deleteMirrors(t *testing.T) {
	t.Helper()
	if err := os.RemoveAll(mirrorsDir(f.home, f.root)); err != nil {
		t.Fatal(err)
	}
}

// requireBorrowsOnly checks that clone reads its history only through the
// mirror: its own object store holds no pack and no loose object.
func requireBorrowsOnly(t *testing.T, clone string) {
	t.Helper()
	objects := filepath.Join(clone, ".git", "objects")
	entries, err := os.ReadDir(objects)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == "info" {
			continue
		}
		inner, _ := os.ReadDir(filepath.Join(objects, e.Name()))
		if len(inner) != 0 {
			t.Fatalf("%s holds objects of its own in %s before the dissociate", clone, e.Name())
		}
	}
}

func requireReadsAll(t *testing.T, dir string) {
	t.Helper()
	testGit(t, dir, "log", "--all", "--oneline")
	testGit(t, dir, "fsck", "--no-progress")
}

func requireNoAlternatesFiles(t *testing.T, clone string) {
	t.Helper()
	for _, name := range []string{"alternates", hostdir.AlternatesAside} {
		if _, err := os.Lstat(filepath.Join(clone, ".git", "objects", "info", name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s still has %s (%v)", clone, name, err)
		}
	}
}

// treeSnapshot is every regular file and link under dir, by path, with its
// bytes (a link's target), and every directory's mode.
func treeSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
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
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func requireSameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatalf("%s: %d entries before, %d after", what, len(before), len(after))
	}
	for k, v := range before {
		if got, ok := after[k]; !ok || got != v {
			t.Fatalf("%s: %s changed", what, k)
		}
	}
}

func setDissociateStage(t *testing.T, hook func(stage, clone string) error) {
	t.Helper()
	orig := dissociateStage
	dissociateStage = hook
	t.Cleanup(func() { dissociateStage = orig })
}

func TestDissociate_FreesEveryCloneAndItsWorktree(t *testing.T) {
	f := newDissociateFixture(t)
	for _, c := range f.clones {
		requireBorrowsOnly(t, c)
	}
	worktree := filepath.Join(f.instance, ".niwa", "worktrees", "app-wt")
	mkdir(t, filepath.Dir(worktree))
	testGit(t, f.clones[0], "worktree", "add", "--quiet", "-b", "wt", worktree)

	rep, err := f.run(t)
	if err != nil {
		t.Fatalf("Dissociate: %v", err)
	}
	if want := []string{"tools/app", "tools/docs", "tools/lib"}; !slices.Equal(rep.Changed, want) {
		t.Fatalf("Changed = %v, want %v", rep.Changed, want)
	}
	if len(rep.Restored) != 0 || len(rep.Outside) != 0 {
		t.Fatalf("report = %+v, want only Changed", rep)
	}
	f.deleteMirrors(t)
	for _, c := range f.clones {
		requireNoAlternatesFiles(t, c)
		requireReadsAll(t, c)
		for _, line := range strings.Split(testGit(t, c, "config", "--local", "--list"), "\n") {
			if strings.HasPrefix(line, "gc.") || strings.HasPrefix(line, "maintenance.") {
				t.Fatalf("%s has the config key %q", c, line)
			}
		}
	}
	requireReadsAll(t, worktree)
}

func TestDissociate_ASecondRunHasNothingToChange(t *testing.T) {
	f := newDissociateFixture(t)
	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
	calls := recordGit(t)
	rep, err := f.run(t)
	if err != nil || len(rep.Changed) != 0 || len(rep.Restored) != 0 || len(rep.Outside) != 0 {
		t.Fatalf("second run = %+v, %v; want an empty report", rep, err)
	}
	if len(calls()) != 0 {
		t.Fatalf("a run with nothing to change ran git: %v", calls())
	}
}

func TestDissociate_LeavesACloneThatNeverBorrowedUnchanged(t *testing.T) {
	f := newDissociateFixture(t)
	full := filepath.Join(f.instance, "tools", "full")
	testGit(t, f.instance, "clone", "--quiet", "file://"+f.clones[0], full)
	if _, err := os.Lstat(filepath.Join(full, ".git", "objects", "info", "alternates")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the full clone has an alternates file (%v)", err)
	}
	f.clones = append(f.clones, full)
	before := treeSnapshot(t, full)

	rep, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(rep.Changed, "tools/full") {
		t.Fatalf("Changed lists the clone that never borrowed: %v", rep.Changed)
	}
	requireSameTree(t, "the clone that never borrowed", before, treeSnapshot(t, full))
}

func TestDissociate_RefusesShortFreeSpaceBeforeAnyChange(t *testing.T) {
	f := newDissociateFixture(t)
	var need uint64
	for _, id := range dissociateRepos {
		need += spaceNeeded(mirrorsDir(f.home, f.root), id)
	}
	if need == 0 {
		t.Fatal("the state files record no pack sizes")
	}
	before := map[string]map[string]string{}
	for _, c := range f.clones {
		before[c] = treeSnapshot(t, filepath.Join(c, ".git", "objects"))
	}
	reportFree(t, need-1, nil)
	calls := recordGit(t)

	_, err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "not enough free space") {
		t.Fatalf("Dissociate = %v, want a free-space refusal", err)
	}
	if len(calls()) != 0 {
		t.Fatalf("a refused run ran git: %v", calls())
	}
	for _, c := range f.clones {
		requireSameTree(t, c, before[c], treeSnapshot(t, filepath.Join(c, ".git", "objects")))
	}

	reportFree(t, need, nil)
	if _, err := f.run(t); err != nil {
		t.Fatalf("with exactly the space needed: %v", err)
	}
}

func TestDissociate_SpaceSumsEachRepoOnceWithTheRestoreFloor(t *testing.T) {
	f := newDissociateFixture(t)
	mdir := mirrorsDir(f.home, f.root)
	// docs has no state file, so it counts as minFreeUnknown.
	if err := os.Remove(statePath(mdir, "docs")); err != nil {
		t.Fatal(err)
	}
	// A second clone of app in the instance counts app once.
	extra := filepath.Join(f.instance, "other", "app")
	mkdir(t, filepath.Dir(extra))
	testGit(t, f.instance, "clone", "--quiet", "--shared", f.clones[0], extra)
	writeFile(t, filepath.Join(extra, ".git", "objects", "info", "alternates"), StablePath(f.home, f.root, "app")+"/objects\n")
	f.clones = append(f.clones, extra)

	want := spaceNeeded(mdir, "app") + uint64(minFreeUnknown) + spaceNeeded(mdir, "lib")
	var got uint64
	orig := freeSpace
	freeSpace = func(dir string) (uint64, error) {
		if dir != f.instance {
			t.Errorf("free space read on %s, want the instance %s", dir, f.instance)
		}
		return 0, nil
	}
	t.Cleanup(func() { freeSpace = orig })
	_, err := f.run(t)
	if err == nil {
		t.Fatal("Dissociate with no free space passed")
	}
	if _, err := fmtSscanNeeded(err.Error(), &got); err != nil {
		t.Fatalf("parsing %q: %v", err.Error(), err)
	}
	if got != want {
		t.Fatalf("needed %d bytes, want %d (app and lib's recorded packs and 1 GiB for docs)", got, want)
	}
}

// fmtSscanNeeded reads the "<n> needed" figure out of a free-space refusal.
func fmtSscanNeeded(msg string, n *uint64) (int, error) {
	i := strings.LastIndex(msg, ", ")
	if i < 0 {
		return 0, errors.New("no figure")
	}
	var v uint64
	for _, r := range msg[i+2:] {
		if r < '0' || r > '9' {
			break
		}
		v = v*10 + uint64(r-'0')
	}
	*n = v
	return 1, nil
}

func TestDissociate_ACopyFailingForTheSecondCloneLeavesEveryCloneBorrowing(t *testing.T) {
	f := newDissociateFixture(t)
	before := map[string]string{}
	for _, c := range f.clones {
		data, err := os.ReadFile(filepath.Join(c, ".git", "objects", "info", "alternates"))
		if err != nil {
			t.Fatal(err)
		}
		before[c] = string(data)
	}
	var copied []string
	setDissociateStage(t, func(stage, clone string) error {
		if stage != "copy" {
			t.Errorf("stage %s reached after a copy failed", stage)
			return nil
		}
		copied = append(copied, clone)
		if clone == f.clones[1] {
			return errors.New("injected")
		}
		return nil
	})

	rep, err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "tools/docs") {
		t.Fatalf("Dissociate = %v, want a copy failure naming tools/docs", err)
	}
	if len(rep.Changed) != 0 {
		t.Fatalf("Changed = %v after a failed copy", rep.Changed)
	}
	if !slices.Equal(copied, f.clones[:2]) {
		t.Fatalf("copies started for %v, want the first two", copied)
	}
	for _, c := range f.clones {
		data, err := os.ReadFile(filepath.Join(c, ".git", "objects", "info", "alternates"))
		if err != nil || string(data) != before[c] {
			t.Fatalf("%s's alternates = %q, %v; want %q", c, data, err, before[c])
		}
		requireReadsAll(t, c)
	}
}

func TestDissociate_AFailedCheckPutsTheFileBackAndStops(t *testing.T) {
	f := newDissociateFixture(t)
	setDissociateStage(t, func(stage, clone string) error {
		if stage == "verify" && clone == f.clones[1] {
			// The copy into docs is undone: every pack it wrote is
			// removed, so git fsck can't read the history without the
			// mirror.
			packs, _ := filepath.Glob(filepath.Join(clone, ".git", "objects", "pack", "*"))
			for _, p := range packs {
				if err := os.Remove(p); err != nil {
					return err
				}
			}
		}
		return nil
	})

	rep, err := f.run(t)
	if err == nil || !strings.Contains(err.Error(), "tools/docs") || !strings.Contains(err.Error(), "put back") {
		t.Fatalf("Dissociate = %v, want a failed check naming tools/docs", err)
	}
	if !slices.Equal(rep.Changed, []string{"tools/app"}) {
		t.Fatalf("Changed = %v, want only tools/app", rep.Changed)
	}
	requireNoAlternatesFiles(t, f.clones[0])
	for _, c := range f.clones[1:] {
		if _, err := os.Stat(filepath.Join(c, ".git", "objects", "info", "alternates")); err != nil {
			t.Fatalf("%s lost its alternates file: %v", c, err)
		}
		if _, err := os.Lstat(filepath.Join(c, ".git", "objects", "info", hostdir.AlternatesAside)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s kept %s", c, hostdir.AlternatesAside)
		}
		requireReadsAll(t, c)
	}
}

func TestDissociate_FinishesALeftoverThatPassesTheCheck(t *testing.T) {
	f := newDissociateFixture(t)
	app := f.clones[0]
	// An earlier run copied app's objects and set its file aside, then
	// stopped before it removed the file.
	testGit(t, app, "repack", "-a", "-d", "-q")
	info := filepath.Join(app, ".git", "objects", "info")
	if err := os.Rename(filepath.Join(info, "alternates"), filepath.Join(info, hostdir.AlternatesAside)); err != nil {
		t.Fatal(err)
	}

	rep, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tools/app", "tools/docs", "tools/lib"}; !slices.Equal(rep.Changed, want) {
		t.Fatalf("Changed = %v, want %v", rep.Changed, want)
	}
	if len(rep.Restored) != 0 {
		t.Fatalf("Restored = %v, want none", rep.Restored)
	}
	f.deleteMirrors(t)
	for _, c := range f.clones {
		requireNoAlternatesFiles(t, c)
		requireReadsAll(t, c)
	}
}

func TestDissociate_PutsBackALeftoverThatFailsTheCheck(t *testing.T) {
	f := newDissociateFixture(t)
	app := f.clones[0]
	info := filepath.Join(app, ".git", "objects", "info")
	// An earlier run set app's file aside before its objects were copied.
	if err := os.Rename(filepath.Join(info, "alternates"), filepath.Join(info, hostdir.AlternatesAside)); err != nil {
		t.Fatal(err)
	}
	// Every copy fails, so the run stops right after the leftover is
	// finished and its result is what's on disk.
	setDissociateStage(t, func(stage, clone string) error {
		if stage == "copy" {
			return errors.New("injected")
		}
		return nil
	})

	rep, err := f.run(t)
	if err == nil {
		t.Fatal("Dissociate passed with every copy failing")
	}
	if !slices.Equal(rep.Restored, []string{"tools/app"}) || len(rep.Changed) != 0 {
		t.Fatalf("report = %+v, want tools/app restored and nothing changed", rep)
	}
	data, err := os.ReadFile(filepath.Join(info, "alternates"))
	if err != nil || string(data) != StablePath(f.home, f.root, "app")+"/objects\n" {
		t.Fatalf("app's alternates = %q, %v; want the stable line back", data, err)
	}
	if _, err := os.Lstat(filepath.Join(info, hostdir.AlternatesAside)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("app kept %s", hostdir.AlternatesAside)
	}
	requireReadsAll(t, app)

	// With the copies working again, the next run frees it.
	setDissociateStage(t, nil)
	rep, err = f.run(t)
	if err != nil || !slices.Contains(rep.Changed, "tools/app") {
		t.Fatalf("next run = %+v, %v; want tools/app changed", rep, err)
	}
	f.deleteMirrors(t)
	requireReadsAll(t, app)
}

func TestDissociate_LeavesACloneBorrowingFromOutsideAsItIs(t *testing.T) {
	f := newDissociateFixture(t)
	elsewhere := filepath.Join(t.TempDir(), "elsewhere.git")
	testGit(t, f.instance, "clone", "--quiet", "--bare", f.clones[0], elsewhere)
	outside := filepath.Join(f.instance, "tools", "outside")
	testGit(t, f.instance, "clone", "--quiet", "--shared", elsewhere, outside)
	altPath := filepath.Join(outside, ".git", "objects", "info", "alternates")
	writeFile(t, altPath, filepath.Join(elsewhere, "objects")+"\n")
	f.clones = append(f.clones, outside)
	before := treeSnapshot(t, outside)
	calls := recordGit(t)

	rep, err := f.run(t)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Outside) != 1 || rep.Outside[0].Clone != "tools/outside" || rep.Outside[0].Line != filepath.Join(elsewhere, "objects") {
		t.Fatalf("Outside = %+v, want tools/outside and its line", rep.Outside)
	}
	if slices.Contains(rep.Changed, "tools/outside") || len(rep.Changed) != 3 {
		t.Fatalf("Changed = %v, want the three mirror borrowers only", rep.Changed)
	}
	requireSameTree(t, "the clone borrowing from outside", before, treeSnapshot(t, outside))
	for _, call := range calls() {
		if slices.Contains(call, "--git-dir="+filepath.Join(outside, ".git")) {
			t.Fatalf("git ran in the clone borrowing from outside: %v", call)
		}
	}
}

func TestDissociate_AnAmbiguousCloneChangesNothing(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, info string){
		"both files": func(t *testing.T, info string) {
			data, err := os.ReadFile(filepath.Join(info, "alternates"))
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(info, hostdir.AlternatesAside), string(data))
		},
		"a quoted line": func(t *testing.T, info string) {
			writeFile(t, filepath.Join(info, "alternates"), "\"/some/where\"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newDissociateFixture(t)
			setup(t, filepath.Join(f.clones[1], ".git", "objects", "info"))
			before := map[string]map[string]string{}
			for _, c := range f.clones {
				before[c] = treeSnapshot(t, c)
			}
			calls := recordGit(t)
			if _, err := f.run(t); err == nil || !strings.Contains(err.Error(), "nothing was changed") {
				t.Fatalf("Dissociate = %v, want a refusal with nothing changed", err)
			}
			if len(calls()) != 0 {
				t.Fatalf("a refused run ran git: %v", calls())
			}
			for _, c := range f.clones {
				requireSameTree(t, c, before[c], treeSnapshot(t, c))
			}
		})
	}
}

func TestDissociate_GitRunsScrubbedAndWritesNoConfig(t *testing.T) {
	f := newDissociateFixture(t)
	t.Setenv("GIT_ALTERNATE_OBJECT_DIRECTORIES", filepath.Join(t.TempDir(), "objects"))
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "elsewhere"))
	var procs []gitProc
	orig := gitProcHook
	gitProcHook = func(p gitProc) { procs = append(procs, p) }
	t.Cleanup(func() { gitProcHook = orig })

	if _, err := f.run(t); err != nil {
		t.Fatal(err)
	}
	// The fixture's own git calls below must not see them.
	os.Unsetenv("GIT_ALTERNATE_OBJECT_DIRECTORIES")
	os.Unsetenv("GIT_DIR")
	subs := map[string]int{}
	for _, p := range procs {
		for _, want := range []string{"gc.auto=0", "maintenance.auto=false", "core.fsmonitor=false", "core.hooksPath=" + emptyHooksDir(f.home)} {
			if !slices.Contains(p.Config, want) {
				t.Fatalf("git %v ran without -c %s", p.Args, want)
			}
		}
		if len(p.Inherited) != 0 {
			t.Fatalf("git %v kept %v of the parent's environment", p.Args, p.Inherited)
		}
		for _, a := range p.Args {
			if !strings.HasPrefix(a, "-") {
				subs[a]++
				break
			}
		}
	}
	if subs["repack"] != 3 || subs["fsck"] != 3 || len(subs) != 2 {
		t.Fatalf("git subcommands = %v, want three repacks and three fscks", subs)
	}
	for _, c := range f.clones {
		for _, line := range strings.Split(testGit(t, c, "config", "--local", "--list"), "\n") {
			if strings.HasPrefix(line, "gc.") || strings.HasPrefix(line, "maintenance.") {
				t.Fatalf("%s has the config key %q", c, line)
			}
		}
	}
}

func TestDissociate_TheFaultLabelDoesNothingInAReleaseBuild(t *testing.T) {
	f := newDissociateFixture(t)
	t.Setenv("NIWA_TEST_FAULT", "error:injected@dissociate-copy,error:injected@dissociate-copy:tools/docs")
	if _, err := f.run(t); err != nil {
		t.Fatalf("the fault label fired outside the test build: %v", err)
	}
}

func TestBorrowsFrom(t *testing.T) {
	f := newFixture(t)
	stable := StablePath(f.home, f.root, "app")
	info := filepath.Join(f.clone, ".git", "objects", "info")
	if BorrowsFrom([]string{f.clone}, f.home, f.root, "app") {
		t.Fatal("a clone with no alternates file borrows")
	}
	writeFile(t, filepath.Join(info, "alternates"), filepath.Join(f.mdir(), ".store", "app-1-ab", "objects")+"\n")
	if BorrowsFrom([]string{f.clone}, f.home, f.root, "app") {
		t.Fatal("a line naming a store directly counts as borrowing through the stable path")
	}
	writeFile(t, filepath.Join(info, "alternates"), stable+"/objects\n")
	if !BorrowsFrom([]string{f.clone}, f.home, f.root, "app") {
		t.Fatal("the stable line doesn't count")
	}
	if BorrowsFrom([]string{f.clone}, f.home, f.root, "lib") {
		t.Fatal("app's line counts for lib")
	}
	if err := os.Rename(filepath.Join(info, "alternates"), filepath.Join(info, hostdir.AlternatesAside)); err != nil {
		t.Fatal(err)
	}
	if !BorrowsFrom([]string{f.clone}, f.home, f.root, "app") {
		t.Fatal("the stable line in a file a dissociate set aside doesn't count")
	}
}
