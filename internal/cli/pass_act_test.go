package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/removal"
	"github.com/tsukumogami/niwa/internal/workspace"
)

// realRemoval replaces the fixture's destroy stand-in with the pass's real
// removal of the tombstone, recording the instance each removal was for.
func realRemoval(t *testing.T) *[]string {
	t.Helper()
	var removed []string
	prev := removeTombstoneFunc
	removeTombstoneFunc = func(p *workspace.DestroyPlan, tomb string) error {
		removed = append(removed, p.Dir())
		return p.Remove(tomb)
	}
	t.Cleanup(func() { removeTombstoneFunc = prev })
	return &removed
}

// mappingBytes reads a session mapping file as written.
func mappingBytes(t *testing.T, root, sid string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".niwa", "sessions", sid+".json"))
	if err != nil {
		t.Fatalf("reading the mapping for %s: %v", sid, err)
	}
	return b
}

func assertNoTombstone(t *testing.T, root string) {
	t.Helper()
	tombs, err := workspace.ListTombstones(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tombs) != 0 {
		t.Fatalf("tombstones left: %v", tombs)
	}
}

// keptAtActing runs one acting pass and checks the unit was kept with a
// line holding want, nothing was removed, no tombstone is left, and the
// mapping is byte for byte what it was.
func keptAtActing(t *testing.T, f passFixture, want string) {
	t.Helper()
	before := mappingBytes(t, f.root, passSessionID)
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 0 || len(*removed) != 0 {
		t.Fatalf("reaped %d, removed %v; want the unit kept\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	if len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Reason, want) {
		t.Fatalf("kept %+v; want one unit kept with %q\nstderr:\n%s", res.Kept, want, f.stderr.String())
	}
	if !strings.Contains(f.stderr.String(), "niwa: kept test-ws-pass: ") || !strings.Contains(f.stderr.String(), want) {
		t.Fatalf("no kept line holding %q:\n%s", want, f.stderr.String())
	}
	if _, err := os.Stat(filepath.Join(f.inst, ".niwa", "instance.json")); err != nil {
		t.Fatalf("the instance isn't at its own path: %v", err)
	}
	assertNoTombstone(t, f.root)
	if after := mappingBytes(t, f.root, passSessionID); !bytes.Equal(after, before) {
		t.Fatalf("the mapping changed:\n%s\n->\n%s", before, after)
	}
}

// A file written between the rule and the re-read (here by the gate) keeps
// the unit at the re-read, with the path named and nothing renamed.
func TestPassAct_ReReadKeepsAFileWrittenAfterTheRule(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	late := filepath.Join(f.clone, "late.txt")
	appendBeforeRemoveConfig(t, f.root, fmt.Sprintf(`command = "echo late > '%s'"`, late))
	keptAtActing(t, f, "changed during the pass: tools/app/late.txt")
	if _, err := os.Stat(late); err != nil {
		t.Fatalf("the late file is gone: %v", err)
	}
}

// A file written after the re-read (here by the on-reap command) is found
// on the tombstone: the tombstone is renamed back, the unit kept with the
// path named, and the file is still under <root>/<instance>.
func TestPassAct_TombstoneCheckRenamesBackOnAFileWrittenAfterTheReRead(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	appendOnReapConfig(t, f.root, `command = "echo late > \"$NIWA_INSTANCE_PATH/tools/app/late.txt\""`)
	keptAtActing(t, f, "changed during the pass: tools/app/late.txt")
	if _, err := os.Stat(filepath.Join(f.clone, "late.txt")); err != nil {
		t.Fatalf("the late file isn't under the instance: %v", err)
	}
}

// A commit made after the re-read leaves git status clean; the ref snapshot
// finds it on the tombstone and names the ref.
func TestPassAct_TombstoneCheckNamesARefMovedAfterTheReRead(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passGit(t, f.clone, "checkout", "-q", "-B", "main", "origin/main")
	appendOnReapConfig(t, f.root, `command = "git -C \"$NIWA_INSTANCE_PATH/tools/app\" -c user.name=t -c user.email=t@example.com commit -q --allow-empty -m late"`)
	keptAtActing(t, f, "changed during the pass: ref refs/heads/main in tools/app")
}

// A linked worktree inside the instance names its admin directory by an
// absolute path the rename moves, so until worktrees are removed before the
// rename the unit keeps at the tombstone check.
func TestPassAct_LinkedWorktreeKeepsAtTheTombstoneCheck(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	passGit(t, f.clone, "checkout", "-q", "-B", "main", "origin/main")
	passGit(t, f.clone, "worktree", "add", "-q", filepath.Join(f.inst, "wt"), "-b", "wt-branch", "main")
	passGit(t, f.clone, "push", "-q", "origin", "wt-branch")
	keptAtActing(t, f, "a linked worktree names its admin directory by a path the rename moved")
}

// A clean unit: the on-reap command runs while the instance is still there,
// the tombstone is removed, and the mapping is deleted after the removal.
func TestPassAct_CleanUnitRunsOnReapBeforeTheRemovalAndSettlesAfter(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	log := filepath.Join(t.TempDir(), "on-reap.log")
	appendOnReapConfig(t, f.root, fmt.Sprintf(`command = "test -f \"$NIWA_INSTANCE_PATH/.niwa/instance.json\" && echo present >> '%s'"`, log))
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 || (*removed)[0] != f.inst {
		t.Fatalf("reaped %d, removed %v; want the instance removed\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	if b, err := os.ReadFile(log); err != nil || string(b) != "present\n" {
		t.Fatalf("on-reap log = %q (%v); want the command run once while the instance was there", b, err)
	}
	if exists(f.inst) {
		t.Fatal("the instance is still there")
	}
	assertNoTombstone(t, f.root)
	if _, err := workspace.ReadSessionMapping(f.root, passSessionID); err == nil {
		t.Fatal("the mapping is still there after a successful on-reap run")
	}
}

// A failed on-reap run isn't a refusal: the unit is removed and the mapping
// is rewritten with its failure count raised.
func TestPassAct_FailedOnReapStillRemovesAndCountsTheFailure(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	appendOnReapConfig(t, f.root, `command = "exit 3"`)
	removed := realRemoval(t)
	res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Reaped != 1 || len(*removed) != 1 {
		t.Fatalf("reaped %d, removed %v; want the instance removed\nstderr:\n%s", res.Reaped, *removed, f.stderr.String())
	}
	if !strings.Contains(f.stderr.String(), "niwa: warning: on-reap for session "+passSessionID+": exited with status 3") {
		t.Fatalf("no on-reap warning:\n%s", f.stderr.String())
	}
	m, err := workspace.ReadSessionMapping(f.root, passSessionID)
	if err != nil || m.OnReapFailures != 1 {
		t.Fatalf("mapping %+v (%v); want it kept with one failure counted", m, err)
	}
}

// The tombstone lock taken exclusively by another holder keeps the unit
// with the reason named and nothing renamed; a shared holder doesn't, since
// passes share it.
func TestPassAct_TombstoneLock(t *testing.T) {
	t.Run("held exclusively", func(t *testing.T) {
		f := newPassFixture(t)
		home := lockTestHome(t)
		release, err := workspace.TryFileLock(workspace.TombstoneLockPath(home, f.root), lockTestHolder)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		keptAtActing(t, f, "the tombstone lock is held")
		if !strings.Contains(f.stderr.String(), lockTestHolder) {
			t.Fatalf("the kept line doesn't name the holder:\n%s", f.stderr.String())
		}
	})
	t.Run("held shared by another pass", func(t *testing.T) {
		f := newPassFixture(t)
		home := lockTestHome(t)
		release, err := workspace.TryFileLockShared(workspace.TombstoneLockPath(home, f.root))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		removed := realRemoval(t)
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if res.Reaped != 1 || len(*removed) != 1 {
			t.Fatalf("reaped %d; want the unit removed beside another shared holder\nstderr:\n%s", res.Reaped, f.stderr.String())
		}
	})
}

// A symlink planted at a lock path makes that lock fail, keeps the unit and
// leaves the link's target unchanged: at the tombstone lock and at the
// instance lock alike.
func TestPassAct_SymlinkedLockFilesKeepTheUnit(t *testing.T) {
	plant := func(t *testing.T, lockPath string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Dir(lockPath), 0o700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(t.TempDir(), "target")
		if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, lockPath); err != nil {
			t.Fatal(err)
		}
		return target
	}
	check := func(t *testing.T, target string) {
		t.Helper()
		if b, err := os.ReadFile(target); err != nil || string(b) != "keep" {
			t.Fatalf("the link's target = %q (%v); want it unchanged", b, err)
		}
	}
	t.Run("tombstone lock", func(t *testing.T) {
		f := newPassFixture(t)
		home := lockTestHome(t)
		target := plant(t, workspace.TombstoneLockPath(home, f.root))
		keptAtActing(t, f, "could not take the tombstone lock")
		check(t, target)
	})
	t.Run("instance lock", func(t *testing.T) {
		f := newPassFixture(t)
		home := lockTestHome(t)
		target := plant(t, workspace.InstanceLockPath(home, f.inst))
		removed := realRemoval(t)
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if res.Reaped != 0 || len(*removed) != 0 || !exists(f.inst) {
			t.Fatalf("reaped %d; want the unit kept on its lock\nstderr:\n%s", res.Reaped, f.stderr.String())
		}
		if !strings.Contains(f.stderr.String(), "niwa: kept test-ws-pass: could not take its instance lock") {
			t.Fatalf("no kept line for the lock:\n%s", f.stderr.String())
		}
		check(t, target)
	})
}

// A tombstone an interrupted pass left, with no holder of the tombstone
// lock, is renamed back and judged as usual; one the lock's shared holder
// owns is left alone; one whose name is taken is kept and listed; a
// symlink at a tombstone's name is skipped and its target untouched.
func TestPassAct_Recovery(t *testing.T) {
	t.Run("renamed back and judged", func(t *testing.T) {
		f := newPassFixture(t)
		lockTestHome(t)
		f.commitUnpushed(t, "left-behind")
		tomb, err := workspace.TombstoneInstance(f.root, filepath.Base(f.inst))
		if err != nil {
			t.Fatal(err)
		}
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if exists(tomb) || !exists(f.inst) {
			t.Fatalf("tombstone %v, instance %v; want it renamed back", exists(tomb), exists(f.inst))
		}
		if len(res.Kept) != 1 || !strings.Contains(res.Kept[0].Check.Evidence, "branch left-behind") {
			t.Fatalf("kept %+v; want the renamed-back unit judged and kept on its branch", res.Kept)
		}
	})
	t.Run("a live pass holds it", func(t *testing.T) {
		f := newPassFixture(t)
		home := lockTestHome(t)
		tomb, err := workspace.TombstoneInstance(f.root, filepath.Base(f.inst))
		if err != nil {
			t.Fatal(err)
		}
		release, err := workspace.TryFileLockShared(workspace.TombstoneLockPath(home, f.root))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if !exists(tomb) || exists(f.inst) || res.Reaped != 0 || len(*f.destroyed) != 0 {
			t.Fatalf("tombstone %v, instance %v, reaped %d; want the tombstone left alone", exists(tomb), exists(f.inst), res.Reaped)
		}
	})
	t.Run("its name is taken", func(t *testing.T) {
		f := newPassFixture(t)
		lockTestHome(t)
		tomb, err := workspace.TombstoneInstance(f.root, filepath.Base(f.inst))
		if err != nil {
			t.Fatal(err)
		}
		makeReapInstance(t, f.root, filepath.Base(f.inst))
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if !exists(tomb) {
			t.Fatal("the tombstone was moved or removed")
		}
		found := false
		for _, k := range res.Kept {
			if k.Name == filepath.Base(tomb) && strings.Contains(k.Reason, "name is taken") {
				found = true
			}
		}
		if !found || !strings.Contains(f.stderr.String(), "niwa: kept "+filepath.Base(tomb)+": ") {
			t.Fatalf("kept %+v; want the tombstone listed\nstderr:\n%s", res.Kept, f.stderr.String())
		}
	})
	t.Run("a symlink at a tombstone's name", func(t *testing.T) {
		f := newPassFixture(t)
		lockTestHome(t)
		target := makeReapInstance(t, t.TempDir(), "elsewhere")
		link := filepath.Join(f.root, workspace.TombstoneName("elsewhere", "0011aabb"))
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("the link moved: %v", err)
		}
		if exists(filepath.Join(f.root, "elsewhere")) {
			t.Fatal("the link was renamed back to an instance name")
		}
		if _, err := os.Stat(filepath.Join(target, ".niwa", "instance.json")); err != nil {
			t.Fatalf("the link's target was touched: %v", err)
		}
		found := false
		for _, k := range res.Kept {
			if strings.Contains(k.Reason, "symlink") {
				found = true
			}
		}
		if !found {
			t.Fatalf("kept %+v; want the symlinked tombstone listed", res.Kept)
		}
	})
	t.Run("the verdict-only mode leaves it", func(t *testing.T) {
		f := newPassFixture(t)
		lockTestHome(t)
		tomb, err := workspace.TombstoneInstance(f.root, filepath.Base(f.inst))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := runPass(context.Background(), f.root, passVerdictOnly, passScope{JobsDir: f.jobsDir, Now: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if !exists(tomb) || exists(f.inst) {
			t.Fatal("the verdict-only mode moved the tombstone")
		}
	})
}

// backstopUnit makes an unmapped dispatch-named instance under the fixture's
// root, aged past the backstop's TTL by its pending marker, holding a clone
// of the fixture's remote on main.
func backstopUnit(t *testing.T, f passFixture, name string) (inst, clone string) {
	t.Helper()
	inst = makeReapInstance(t, f.root, name)
	clone = filepath.Join(inst, "tools", "app")
	remote := strings.TrimPrefix(passConfigURL(t, f), "file://")
	passGit(t, f.root, "clone", "-q", "file://"+remote, clone)
	passGit(t, clone, "checkout", "-q", "-B", "main", "origin/main")
	writeDispatchMarkerAt(t, inst, time.Now().Add(-2*dispatchBackstopTTL))
	return inst, clone
}

// passConfigURL reads the clone URL of the fixture's repository.
func passConfigURL(t *testing.T, f passFixture) string {
	t.Helper()
	cfg, err := loadWorkspaceConfig(f.root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Repos["app"].URL
}

// The backstop's candidates go through the rule: one holding an unpushed
// commit is kept with its first keeping check, one carrying the retain
// marker is kept by the hold check with the recorded reason, and a clean
// one is removed through the tombstone.
func TestPassAct_BackstopGoesThroughTheRule(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	unpushed, unpushedClone := backstopUnit(t, f, "test-ws+-0000bb01")
	passGit(t, unpushedClone, "checkout", "-q", "-b", "never-pushed")
	passGit(t, unpushedClone, "commit", "-q", "--allow-empty", "-m", "work")
	held, _ := backstopUnit(t, f, "test-ws+-0000bb02")
	const reason = "a codex turn finished here but no session record was found"
	if err := writeDispatchRetainMarker(held, reason); err != nil {
		t.Fatal(err)
	}
	clean, _ := backstopUnit(t, f, "test-ws+-0000bb03")
	removed := realRemoval(t)

	n, err := reapBackstop(f.root, f.jobsDir, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	out := f.stderr.String()
	if n != 1 || len(*removed) != 1 || (*removed)[0] != clean || exists(clean) {
		t.Fatalf("reaped %d, removed %v; want only the clean candidate removed\nstderr:\n%s", n, *removed, out)
	}
	if !exists(unpushed) || !strings.Contains(out, "niwa: kept test-ws+-0000bb01: repository check: ") || !strings.Contains(out, "branch never-pushed") {
		t.Fatalf("want the unpushed candidate kept with its repository check:\n%s", out)
	}
	if !exists(held) || !strings.Contains(out, "niwa: kept test-ws+-0000bb02: hold check: held by .niwa/dispatch-retain: "+reason) {
		t.Fatalf("want the retained candidate kept by the hold check with its reason:\n%s", out)
	}
	assertNoTombstone(t, f.root)
}

// The backstop's age read goes through the shared reader: a pending marker
// that is a link isn't followed, and the age falls back to the directory's
// own mtime. An instance whose directory is a link is never a candidate.
func TestPassAct_BackstopAgeRead(t *testing.T) {
	f := newPassFixture(t)
	now := time.Now()

	young := makeReapInstance(t, f.root, "test-ws+-0000cc01")
	oldMarker := filepath.Join(t.TempDir(), "old-marker")
	if err := os.WriteFile(oldMarker, []byte(now.Add(-2*dispatchBackstopTTL).UTC().Format(time.RFC3339)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(oldMarker, filepath.Join(young, dispatchPendingMarker)); err != nil {
		t.Fatal(err)
	}
	// The directory is young, so only a followed link could make it old.
	touchInstanceMtime(t, young, now)
	if ts, ok := dispatchInstanceAge(young); !ok || now.Sub(ts) >= dispatchBackstopTTL {
		t.Fatalf("age %v, %v; want the directory's own young mtime", ts, ok)
	}

	elsewhere := makeReapInstance(t, t.TempDir(), "test-ws+-0000cc02")
	writeDispatchMarkerAt(t, elsewhere, now.Add(-2*dispatchBackstopTTL))
	link := filepath.Join(f.root, "test-ws+-0000cc03")
	if err := os.Symlink(elsewhere, link); err != nil {
		t.Fatal(err)
	}
	if _, ok := dispatchInstanceAge(link); ok {
		t.Fatal("an instance whose directory is a link has an age")
	}
	targets, _, err := selectBackstopTargets(f.root, f.jobsDir, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if tg.InstancePath == young || tg.InstancePath == link {
			t.Fatalf("candidate %s; want neither the young nor the linked instance", tg.InstancePath)
		}
	}
	if removalMarker := removal.DispatchPendingMarker; removalMarker != dispatchPendingMarker {
		t.Fatalf("removal reads %s but dispatch writes %s", removalMarker, dispatchPendingMarker)
	}
}

// syncWriter serializes writes from passes running at once.
type syncWriter struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

// Two passes started together over the same workspace remove each clean
// unit once, neither reports an error, and no tombstone is left.
func TestPassAct_OverlappingPassesRemoveEachUnitOnce(t *testing.T) {
	f := newPassFixture(t)
	lockTestHome(t)
	second := makeReapInstance(t, f.root, "test-ws-second")
	mapEphemeral(t, f.root, reapDeadSessionID, second, true)
	var out syncWriter
	prevStderr := reapStderr
	reapStderr = &out
	t.Cleanup(func() { reapStderr = prevStderr })
	var mu sync.Mutex
	var removed []string
	prev := removeTombstoneFunc
	removeTombstoneFunc = func(p *workspace.DestroyPlan, tomb string) error {
		mu.Lock()
		removed = append(removed, p.Dir())
		mu.Unlock()
		return p.Remove(tomb)
	}
	t.Cleanup(func() { removeTombstoneFunc = prev })

	var wg sync.WaitGroup
	var errs [2]error
	var reaped [2]int
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := runPass(context.Background(), f.root, passActAll, passScope{JobsDir: f.jobsDir, Now: time.Now()})
			errs[i], reaped[i] = err, res.Reaped
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("pass %d: %v", i+1, err)
		}
	}
	if reaped[0]+reaped[1] != 2 || len(removed) != 2 || removed[0] == removed[1] {
		t.Fatalf("reaped %v, removed %v; want each unit removed once\nstderr:\n%s", reaped, removed, out.String())
	}
	if exists(f.inst) || exists(second) {
		t.Fatal("a unit is still there")
	}
	assertNoTombstone(t, f.root)
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "warning") || strings.Contains(line, "changed during the pass") || strings.Contains(line, "tombstone") {
			t.Fatalf("a pass reported %q", line)
		}
	}
}
