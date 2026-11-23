package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/github"
	"github.com/tsukumogami/niwa/internal/hostdir"
	"github.com/tsukumogami/niwa/internal/mirror"
)

// borrowFixture is a workspace root and developer home with a seeded mirror
// of each sandbox repo, and an instance directory new clones go into.
type borrowFixture struct {
	sb                                borrowSandbox
	home, workspaceRoot, instanceRoot string
}

func newBorrowFixture(t *testing.T) borrowFixture {
	t.Helper()
	base := t.TempDir()
	f := borrowFixture{
		sb:            newBorrowSandbox(t),
		home:          filepath.Join(base, "home"),
		workspaceRoot: filepath.Join(base, "ws"),
	}
	f.instanceRoot = filepath.Join(f.workspaceRoot, "inst")
	for _, d := range []string{f.home, filepath.Join(f.workspaceRoot, ".niwa"), f.instanceRoot} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range borrowRepos {
		res := mirror.Seed(context.Background(), f.mirrorOpts(), mirror.Repo{ID: name, URL: f.url(name)})
		if !res.Usable() {
			t.Fatalf("seeding the mirror of %s: outcome %s, %v", name, res.Outcome, res.Err)
		}
	}
	t.Cleanup(func() { borrowStage = nil })
	return f
}

func (f borrowFixture) url(name string) string { return "file://" + f.sb.remotes[name] }

func (f borrowFixture) mirrorOpts() mirror.Options {
	return mirror.Options{Home: f.home, WorkspaceRoot: f.workspaceRoot, Command: "create"}
}

func (f borrowFixture) stable(name string) string {
	return mirror.StablePath(f.home, f.workspaceRoot, name)
}

// store is the store directory the mirror of name links to now.
func (f borrowFixture) store(t *testing.T, name string) string {
	t.Helper()
	target, err := mirror.LinkedStore(f.home, f.workspaceRoot, name)
	if err != nil {
		t.Fatalf("reading the mirror link of %s: %v", name, err)
	}
	return filepath.Join(filepath.Dir(f.stable(name)), target)
}

// job is a clone job for name's new clone in the fixture's instance, borrowing
// from its mirror with the given lock wait.
func (f borrowFixture) job(name string, wait time.Duration) cloneJob {
	return cloneJob{
		cr:        ClassifiedRepo{Repo: github.Repo{Name: name}, Group: "tools"},
		cloneURL:  f.url(name),
		targetDir: filepath.Join(f.instanceRoot, "tools", name),
		borrow: borrowChoice{source: &borrowSource{
			repoID:        name,
			home:          f.home,
			workspaceRoot: f.workspaceRoot,
			stable:        f.stable(name),
			lockWait:      wait,
		}},
	}
}

func borrowApplier() *Applier {
	a := NewApplier(nil)
	a.Reporter = NewReporterWithTTY(io.Discard, false)
	return a
}

type cloneOutcome struct {
	cloned, borrowed bool
	fallback         string
	err              error
}

func runCloneNew(a *Applier, job cloneJob) cloneOutcome {
	cloned, _, borrowed, fallback, err := a.cloneNew(context.Background(), job, a.Reporter)
	return cloneOutcome{cloned, borrowed, fallback, err}
}

func alternatesOf(clone string) string {
	return filepath.Join(clone, ".git", "objects", "info", "alternates")
}

// requireFullClone fails unless clone is a readable clone with no
// alternates file and nothing but the clone itself in its group directory.
func requireFullClone(t *testing.T, clone string) {
	t.Helper()
	if _, err := os.Lstat(alternatesOf(clone)); !os.IsNotExist(err) {
		t.Fatalf("%s has an alternates file (%v), want a full clone", clone, err)
	}
	gitOut(t, clone, "fsck", "--no-progress")
	requireOnlyEntry(t, filepath.Dir(clone), filepath.Base(clone))
}

// requireOnlyEntry fails unless dir holds exactly the entry name: no partial
// or temporary directory is left beside a clone.
func requireOnlyEntry(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if len(names) != 1 || names[0] != name {
		t.Fatalf("%s holds %v, want only %s", dir, names, name)
	}
	info, err := os.ReadDir(filepath.Join(dir, name, ".git", "objects", "info"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range info {
		if strings.HasPrefix(e.Name(), ".alternates-") {
			t.Fatalf("a temporary alternates file %s was left in %s", e.Name(), filepath.Join(dir, name))
		}
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

// A borrowing clone's alternates file holds exactly the stable path's line,
// not the resolved store, and the clone is otherwise the clone a full clone
// is: the same origin URL, refs, local config and hooks. It holds no pack a
// mirror store holds and no gc. or maintenance. key.
func TestBorrowingClone_WritesTheStableLineAndMatchesAFullClone(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	got := runCloneNew(borrowApplier(), job)
	if got.err != nil || !got.cloned || !got.borrowed || got.fallback != "" {
		t.Fatalf("cloneNew = %+v, want a borrowing clone", got)
	}
	clone := job.targetDir
	data, err := os.ReadFile(alternatesOf(clone))
	if err != nil {
		t.Fatal(err)
	}
	if want := f.stable("app") + "/objects\n"; string(data) != want {
		t.Fatalf("alternates = %q, want exactly %q", data, want)
	}
	if strings.Contains(string(data), ".store") {
		t.Fatalf("alternates %q names a store directly", data)
	}
	gitOut(t, clone, "fsck", "--no-progress")
	requireOnlyEntry(t, filepath.Dir(clone), "app")

	full := filepath.Join(t.TempDir(), "app")
	if _, err := (&Cloner{}).CloneWith(context.Background(), f.url("app"), full, CloneOptions{}, borrowApplier().Reporter); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"remote", "get-url", "origin"},
		{"for-each-ref"},
		{"config", "--local", "--list"},
	} {
		if b, w := gitOut(t, clone, args...), gitOut(t, full, args...); b != w {
			t.Errorf("git %v: borrowing clone gives\n%s\nfull clone gives\n%s", args, b, w)
		}
	}
	if b, w := dirNames(t, filepath.Join(clone, ".git", "hooks")), dirNames(t, filepath.Join(full, ".git", "hooks")); !slices.Equal(b, w) {
		t.Errorf("hooks: borrowing clone has %v, full clone %v", b, w)
	}
	for _, line := range strings.Split(gitOut(t, clone, "config", "--local", "--list"), "\n") {
		if strings.HasPrefix(line, "gc.") || strings.HasPrefix(line, "maintenance.") {
			t.Errorf("the borrowing clone has the config key %q", line)
		}
	}
	storePacks := dirNames(t, filepath.Join(f.store(t, "app"), "objects", "pack"))
	for _, p := range dirNames(t, filepath.Join(clone, ".git", "objects", "pack")) {
		if slices.Contains(storePacks, p) {
			t.Errorf("the borrowing clone holds %s, a pack the mirror store holds", p)
		}
	}
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

// The borrowing clone holds the mirror's lock from reading the stable link
// until its alternates line is on disk: a sweep, which only tries the lock,
// can't scan there, and a restore can't swap the store. Once git has
// written its line the strict scan already pins the store, and after the
// clone the strict walk lists it and Pins pins the store it names, so the
// store isn't removed.
func TestBorrowingClone_HoldsTheMirrorLockFromTheLinkToTheAlternatesLine(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	store := f.store(t, "app")
	lock := mirror.LockPath(f.home, f.workspaceRoot, "app")
	seen := map[string]bool{}
	borrowStage = func(stage string) {
		seen[stage] = true
		release, err := filelock.TryFileLock(lock, "test sweep")
		if err == nil {
			release()
			t.Errorf("at %s the mirror lock was free, so a sweep could scan and remove the store", stage)
		} else if !errors.Is(err, filelock.ErrLockHeld) {
			t.Errorf("at %s trying the mirror lock: %v", stage, err)
		}
		switch stage {
		case borrowStageLinked:
			// A restore that would swap the store gives up on the lock.
			if err := os.Remove(filepath.Join(filepath.Dir(f.stable("app")), ".meta", "app.json")); err != nil {
				t.Fatal(err)
			}
			opts := f.mirrorOpts()
			opts.LockWait = 20 * time.Millisecond
			for _, res := range mirror.EnsureHealthy(context.Background(), opts, []mirror.Repo{{ID: "app", URL: f.url("app")}}) {
				if res.Outcome != mirror.OutcomeBusy {
					t.Errorf("a restore while the clone held the lock: outcome %s, want busy", res.Outcome)
				}
			}
		case borrowStageCloned:
			// git's own line names the store, so a scan here pins it.
			requirePinned(t, f, store, job.targetDir)
		}
	}
	got := runCloneNew(borrowApplier(), job)
	borrowStage = nil
	if got.err != nil || !got.borrowed {
		t.Fatalf("cloneNew = %+v, want a borrowing clone", got)
	}
	for _, stage := range []string{borrowStageLinked, borrowStageCloned, borrowStageRewritten, borrowStageWritten} {
		if !seen[stage] {
			t.Errorf("the clone never reached %s", stage)
		}
	}
	requirePinned(t, f, store, job.targetDir)
	if f.store(t, "app") != store {
		t.Fatal("the mirror's link moved during the clone")
	}
}

// requirePinned fails unless the strict walk lists clone and Pins pins store.
func requirePinned(t *testing.T, f borrowFixture, store, clone string) {
	t.Helper()
	clones, err := hostdir.StrictClones(f.home, f.workspaceRoot)
	if err != nil {
		t.Fatalf("StrictClones: %v", err)
	}
	if !slices.Contains(clones, clone) {
		t.Fatalf("StrictClones = %v, want it to list %s", clones, clone)
	}
	pinned, err := hostdir.Pins(clones, store)
	if err != nil || !pinned {
		t.Fatalf("Pins(%s) = %t, %v; want the clone to pin it", store, pinned, err)
	}
}

// moveStore renames the mirror of name's linked store to the store name to
// and links the stable path to it, as a restore that swapped in a new store
// would leave it, and returns to.
func moveStore(t *testing.T, f borrowFixture, name, to string) string {
	t.Helper()
	mdir := filepath.Dir(f.stable(name))
	from, err := mirror.LinkedStore(f.home, f.workspaceRoot, name)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(mdir, from), filepath.Join(mdir, to)); err != nil {
		t.Fatal(err)
	}
	relinkMirror(t, f, name, to)
	return to
}

func relinkMirror(t *testing.T, f borrowFixture, name, target string) {
	t.Helper()
	if err := os.Remove(f.stable(name)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.stable(name)); err != nil {
		t.Fatal(err)
	}
}

// A link that names another store after the clone than before it gets one
// borrowing retry; a second change makes the clone a full one with reason
// store-changed.
func TestBorrowingClone_StoreChangedRetriesOnceThenClonesInFull(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	names := []string{".store/app-20990101T000000Z-0123abcd", ".store/app-20990101T000001Z-4567cdef"}
	links := 0
	borrowStage = func(stage string) {
		switch stage {
		case borrowStageLinked:
			links++
		case borrowStageWritten:
			moveStore(t, f, "app", names[links-1])
		}
	}
	got := runCloneNew(borrowApplier(), job)
	borrowStage = nil
	if links != 2 {
		t.Fatalf("the borrowing clone ran %d times, want 2 (one retry)", links)
	}
	if got.err != nil || !got.cloned || got.borrowed || got.fallback != fallbackStoreChanged {
		t.Fatalf("cloneNew = %+v, want a full clone with reason %s", got, fallbackStoreChanged)
	}
	requireFullClone(t, job.targetDir)
}

// A link that changes during the first clone only is borrowed from on the
// retry, through the stable path.
func TestBorrowingClone_StoreChangedOnceBorrowsOnTheRetry(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	links := 0
	borrowStage = func(stage string) {
		switch stage {
		case borrowStageLinked:
			links++
		case borrowStageWritten:
			if links == 1 {
				moveStore(t, f, "app", ".store/app-20990101T000000Z-0123abcd")
			}
		}
	}
	got := runCloneNew(borrowApplier(), job)
	borrowStage = nil
	if links != 2 {
		t.Fatalf("the borrowing clone ran %d times, want 2 (one retry)", links)
	}
	if got.err != nil || !got.borrowed || got.fallback != "" {
		t.Fatalf("cloneNew = %+v, want the retry to borrow", got)
	}
	data, err := os.ReadFile(alternatesOf(job.targetDir))
	if err != nil || string(data) != f.stable("app")+"/objects\n" {
		t.Fatalf("alternates = %q (%v), want the stable path's line", data, err)
	}
	requireOnlyEntry(t, filepath.Dir(job.targetDir), "app")
	gitOut(t, job.targetDir, "fsck", "--no-progress")
}

// A borrowing clone that leaves no alternates file, or whose rewritten line
// doesn't read back, is removed and redone in full with reason
// borrow-clone-failed.
func TestBorrowingClone_NoAlternatesOrABadReadBackClonesInFull(t *testing.T) {
	for name, at := range map[string]struct {
		stage string
		do    func(clone string) error
	}{
		"no alternates file": {borrowStageCloned, func(clone string) error { return os.Remove(alternatesOf(clone)) }},
		"read-back mismatch": {borrowStageRewritten, func(clone string) error {
			return os.WriteFile(alternatesOf(clone), []byte("/elsewhere/objects\n"), 0o644)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newBorrowFixture(t)
			job := f.job("app", 0)
			borrowStage = func(stage string) {
				if stage == at.stage {
					if err := at.do(job.targetDir); err != nil {
						t.Fatal(err)
					}
				}
			}
			got := runCloneNew(borrowApplier(), job)
			borrowStage = nil
			if got.err != nil || !got.cloned || got.borrowed || got.fallback != fallbackBorrowCloneFailed {
				t.Fatalf("cloneNew = %+v, want a full clone with reason %s", got, fallbackBorrowCloneFailed)
			}
			requireFullClone(t, job.targetDir)
		})
	}
}

// Another command holding the mirror's lock past the wait makes the clone a
// full one with reason mirror-busy, without waiting longer.
func TestBorrowingClone_AHeldLockClonesInFullAsMirrorBusy(t *testing.T) {
	f := newBorrowFixture(t)
	release, err := filelock.TryFileLock(mirror.LockPath(f.home, f.workspaceRoot, "app"), "another niwa command")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	job := f.job("app", 30*time.Millisecond)
	got := runCloneNew(borrowApplier(), job)
	if got.err != nil || !got.cloned || got.borrowed || got.fallback != fallbackMirrorBusy {
		t.Fatalf("cloneNew = %+v, want a full clone with reason %s", got, fallbackMirrorBusy)
	}
	requireFullClone(t, job.targetDir)
}

// A link that isn't one niwa would have written is never borrowed through.
func TestBorrowingClone_ALinkNiwaDidNotWriteClonesInFull(t *testing.T) {
	f := newBorrowFixture(t)
	elsewhere := t.TempDir()
	relinkMirror(t, f, "app", elsewhere)
	job := f.job("app", 0)
	got := runCloneNew(borrowApplier(), job)
	if got.err != nil || !got.cloned || got.borrowed || got.fallback != fallbackBorrowCloneFailed {
		t.Fatalf("cloneNew = %+v, want a full clone with reason %s", got, fallbackBorrowCloneFailed)
	}
	requireFullClone(t, job.targetDir)
}

// A repo that already has a clone is left exactly as it is: nothing borrows,
// nothing is cloned, and no reason is given.
func TestBorrowingClone_AnExistingCloneIsNeverConverted(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	if _, err := (&Cloner{}).CloneWith(context.Background(), f.url("app"), job.targetDir, CloneOptions{}, borrowApplier().Reporter); err != nil {
		t.Fatal(err)
	}
	job.noPull = true
	got := runCloneNew(borrowApplier(), job)
	if got.err != nil || got.cloned || got.borrowed || got.fallback != "" {
		t.Fatalf("cloneNew = %+v, want the existing clone left alone", got)
	}
	if _, err := os.Lstat(alternatesOf(job.targetDir)); !os.IsNotExist(err) {
		t.Fatalf("the existing clone gained an alternates file (%v)", err)
	}
}

// A repo ensureMirrors gave a reason to is cloned in full with that reason.
// A name that can't name a mirror isn't printed in the warning.
func TestCloneNew_AFallbackChoiceClonesInFullWithItsReason(t *testing.T) {
	f := newBorrowFixture(t)
	job := f.job("app", 0)
	job.borrow = borrowChoice{fallback: fallbackInvalidRepoID}
	got := runCloneNew(borrowApplier(), job)
	if got.err != nil || !got.cloned || got.borrowed || got.fallback != fallbackInvalidRepoID {
		t.Fatalf("cloneNew = %+v, want a full clone with reason %s", got, fallbackInvalidRepoID)
	}
	requireFullClone(t, job.targetDir)

	line := fallbackWarning("_bad name\x1b[2J", fallbackInvalidRepoID)
	if !strings.Contains(line, "a repo whose name niwa can't mirror") || strings.Contains(line, "bad name") {
		t.Fatalf("warning %q, want it to say \"a repo whose name niwa can't mirror\" in place of the name", line)
	}
	if line := fallbackWarning("app", fallbackSeedFailed); !strings.HasPrefix(line, "app ") || !strings.Contains(line, "("+fallbackSeedFailed+")") {
		t.Fatalf("warning %q, want it to name app and the reason", line)
	}
}
