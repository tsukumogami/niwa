package mirror

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// restoredOnce returns a fixture whose mirror was built and then restored
// once, so the record lists the first store as retired, and that store's
// link target.
func restoredOnce(t *testing.T) (fixture, string) {
	t.Helper()
	f := built(t)
	old := f.linkTarget(t)
	damageHEAD(t, f)
	if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("restore = %+v", res)
	}
	requireKeptRetired(t, f, old)
	return f, old
}

// ageRecord moves every retired_at and a recorded last_swept of the
// fixture's store record back by d.
func ageRecord(t *testing.T, f fixture, d time.Duration) {
	t.Helper()
	rec := recordOf(t, f)
	back := func(s string) string {
		at, err := time.Parse(time.RFC3339, s)
		if err != nil {
			t.Fatalf("record time %q: %v", s, err)
		}
		return stateTime(at.Add(-d))
	}
	for i := range rec.Stores {
		if rec.Stores[i].RetiredAt != "" {
			rec.Stores[i].RetiredAt = back(rec.Stores[i].RetiredAt)
		}
	}
	if rec.LastSwept != "" {
		rec.LastSwept = back(rec.LastSwept)
	}
	writeRecord(t, f, rec)
}

// countingScan is the strict walk of the fixture's workspace, counting its
// calls.
func countingScan(f fixture) (func() ([]string, error), *int) {
	n := new(int)
	return func() ([]string, error) {
		*n++
		return hostdir.StrictClones(f.home, f.root)
	}, n
}

// directBorrower makes a clone in a half-created instance whose alternates
// names store, a link target, directly, and returns it.
func directBorrower(t *testing.T, f fixture, instance, store string) string {
	t.Helper()
	clone := filepath.Join(f.root, instance, "tools", "app")
	writeFile(t, filepath.Join(clone, ".git", "objects", "info", "alternates"), filepath.Join(f.mdir(), store, "objects")+"\n")
	return clone
}

func requireStoreGone(t *testing.T, f fixture, store string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(f.mdir(), store)); !os.IsNotExist(err) {
		t.Fatalf("%s is still there (%v)", store, err)
	}
	if rec := recordOf(t, f); rec.index(filepath.Base(store)) >= 0 {
		t.Fatalf("the record still lists %s: %+v", store, rec.Stores)
	}
}

func requireStoreKept(t *testing.T, f fixture, store string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(f.mdir(), store, "objects")); err != nil {
		t.Fatalf("%s was removed (%v)", store, err)
	}
	entryOf(t, f, store)
}

func TestSweep_RemovesAnAgedUnpinnedRetiredStore(t *testing.T) {
	f, old := restoredOnce(t)
	current := f.linkTarget(t)
	ageRecord(t, f, 2*time.Hour)
	logged := len(f.logLines(t))

	opts := f.applyOpts()
	scan, calls := countingScan(f)
	opts.Scan = scan
	start := time.Now().Add(-time.Second)
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy || res[0].LogErr != nil {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if *calls != 1 {
		t.Fatalf("the scan ran %d times, want once", *calls)
	}
	requireStoreGone(t, f, old)
	requireStoreKept(t, f, current)
	if f.linkTarget(t) != current {
		t.Fatal("the sweep moved the link")
	}
	notBefore(t, "last_swept", recordOf(t, f).LastSwept, start)
	added := newLines(t, f, logged)
	if len(added) != 2 || added[0]["event"] != eventCheck {
		t.Fatalf("log gained %v, want the check line and one store line", added)
	}
	if l := added[1]; l["event"] != eventRemove || l["store"] != old || l["reason"] != reasonRetired || l["mirror"] != "app" || l["command"] != "apply" {
		t.Fatalf("store line = %v, want remove of %s for %s", l, old, reasonRetired)
	}
}

func TestSweep_LeavesAYoungRetiredStore(t *testing.T) {
	f, old := restoredOnce(t)
	ageRecord(t, f, 30*time.Minute)
	opts := f.applyOpts()
	scan, calls := countingScan(f)
	opts.Scan = scan
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if *calls != 0 {
		t.Fatalf("the scan ran %d times for a store retired half an hour ago, want none", *calls)
	}
	requireKeptRetired(t, f, old)
}

func TestSweep_RunsNoScanTwiceWithinTheHour(t *testing.T) {
	f, old := restoredOnce(t)
	// A clone naming the retired store directly pins it, so the first
	// sweep keeps it and it's still due.
	directBorrower(t, f, "half", old)
	ageRecord(t, f, 2*time.Hour)
	opts := f.applyOpts()
	scan, calls := countingScan(f)
	opts.Scan = scan
	for i := 0; i < 2; i++ {
		if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
			t.Fatalf("call %d = %+v, want healthy", i+1, res)
		}
	}
	if *calls != 1 {
		t.Fatalf("two calls within the hour ran the scan %d times, want once", *calls)
	}
	requireKeptRetired(t, f, old)
}

func TestSweep_SkippedWhileTheLockIsHeld(t *testing.T) {
	f, old := restoredOnce(t)
	ageRecord(t, f, 2*time.Hour)
	record, err := os.ReadFile(storesPath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "another command")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	logged := len(f.logLines(t))

	opts := f.applyOpts()
	scan, calls := countingScan(f)
	opts.Scan = scan
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if *calls != 0 {
		t.Fatalf("the scan ran %d times under another command's lock, want none", *calls)
	}
	requireKeptRetired(t, f, old)
	if now, _ := os.ReadFile(storesPath(f.mdir(), "app")); !bytes.Equal(now, record) {
		t.Fatalf("the record changed while the lock was held:\n%s\nwas\n%s", now, record)
	}
	if lines := storeLinesOf(newLines(t, f, logged)); len(lines) != 0 {
		t.Fatalf("the skipped sweep logged %v", lines)
	}
}

func TestSweep_NilOrFailingScanRemovesNothing(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		f, old := restoredOnce(t)
		ageRecord(t, f, 2*time.Hour)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
			t.Fatalf("result = %+v, want healthy", res)
		}
		requireKeptRetired(t, f, old)
	})
	t.Run("failing", func(t *testing.T) {
		f, old := restoredOnce(t)
		ageRecord(t, f, 2*time.Hour)
		logged := len(f.logLines(t))
		opts := f.applyOpts()
		opts.Scan = func() ([]string, error) { return nil, errors.New("kept/ can't be read") }
		start := time.Now().Add(-time.Second)
		if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
			t.Fatalf("result = %+v, want healthy", res)
		}
		requireKeptRetired(t, f, old)
		notBefore(t, "last_swept", recordOf(t, f).LastSwept, start)
		lines := storeLinesOf(newLines(t, f, logged))
		if len(lines) != 1 || lines[0]["event"] != eventKeep || lines[0]["store"] != old || lines[0]["reason"] != reasonScanFailed {
			t.Fatalf("store lines = %v, want one keep of %s for %s", lines, old, reasonScanFailed)
		}
	})
}

func TestSweep_LeavesAnInvalidNameAndLogsIt(t *testing.T) {
	f, _ := restoredOnce(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), "outside")
	otherRepo := "other-20200101T000000Z-0000000d"
	escape := "../app-20200101T000000Z-0000000e"
	linked := "app-20200101T000000Z-0000000f"
	for _, dir := range []string{
		filepath.Join(f.mdir(), ".store", otherRepo),
		filepath.Join(f.mdir(), ".store", escape),
	} {
		writeFile(t, filepath.Join(dir, "objects", "keep"), "keep")
	}
	if err := os.Symlink(outside, filepath.Join(f.mdir(), ".store", linked)); err != nil {
		t.Fatal(err)
	}
	rec := recordOf(t, f)
	retired := stateTime(time.Now().Add(-2 * time.Hour))
	for _, name := range []string{otherRepo, escape, linked} {
		rec.put(storeEntry{Name: name, URL: recordURL(f.repo().URL), Identity: remoteIdentity(f.repo().URL), Linked: true, RetiredAt: retired})
	}
	writeRecord(t, f, rec)
	logged := len(f.logLines(t))

	opts := f.applyOpts()
	scan, _ := countingScan(f)
	opts.Scan = scan
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result = %+v, want healthy", res)
	}
	for _, path := range []string{
		filepath.Join(f.mdir(), ".store", otherRepo, "objects", "keep"),
		filepath.Join(f.mdir(), ".store", escape, "objects", "keep"),
		filepath.Join(outside, "keep"),
	} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s was removed (%v)", path, err)
		}
	}
	if info, err := os.Lstat(filepath.Join(f.mdir(), ".store", linked)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the linked store entry = %v (%v), want the link left", info, err)
	}
	want := map[string]string{
		".store/" + otherRepo: reasonInvalidName,
		".store/" + escape:    reasonInvalidName,
		".store/" + linked:    reasonNotAStore,
	}
	got := map[string]string{}
	for _, l := range storeLinesOf(newLines(t, f, logged)) {
		if l["event"] != eventKeep {
			t.Fatalf("store line %v, want only keep lines", l)
		}
		got[l["store"].(string)] = l["reason"].(string)
	}
	for store, reason := range want {
		if got[store] != reason {
			t.Fatalf("store lines = %v, want %s kept for %s", got, store, reason)
		}
	}
	after := recordOf(t, f)
	for _, name := range []string{otherRepo, escape, linked} {
		if after.index(name) < 0 {
			t.Fatalf("the record dropped %q: %+v", name, after.Stores)
		}
	}
}

func TestSweep_LeavesEveryStoreWhenStoreIsALink(t *testing.T) {
	f, old := restoredOnce(t)
	// .store/ itself replaced by a link to a copy: nothing under it may be
	// removed through the link.
	store := filepath.Join(f.mdir(), ".store")
	moved := filepath.Join(t.TempDir(), "store-copy")
	if err := os.Rename(store, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, store); err != nil {
		t.Fatal(err)
	}
	ageRecord(t, f, 2*time.Hour)
	logged := len(f.logLines(t))

	opts := f.applyOpts()
	scan, _ := countingScan(f)
	opts.Scan = scan
	_ = EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if _, err := os.Lstat(filepath.Join(moved, filepath.Base(old), "objects")); err != nil {
		t.Fatalf("the retired store was removed through the linked .store (%v)", err)
	}
	lines := storeLinesOf(newLines(t, f, logged))
	if len(lines) != 1 || lines[0]["event"] != eventKeep || lines[0]["store"] != old || lines[0]["reason"] != reasonNotAStore {
		t.Fatalf("store lines = %v, want one keep of %s for %s", lines, old, reasonNotAStore)
	}
}

func TestSweep_KeepsAStoreAPinNamesDirectly(t *testing.T) {
	f := built(t)
	first := f.linkTarget(t)
	direct := directBorrower(t, f, "half", first)
	opts := f.applyOpts()
	opts.Instances = append(append([]string{}, opts.Instances...), direct)
	scan, _ := countingScan(f)
	opts.Scan = scan

	// Through a restore.
	damageHEAD(t, f)
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("restore = %+v", res)
	}
	second := f.linkTarget(t)
	requireKeptRetired(t, f, first)

	// Through a rebuild for a changed remote.
	rewriteStateURL(t, f)
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored {
		t.Fatalf("url-changed rebuild = %+v", res)
	}
	requireKeptRetired(t, f, first)
	requireKeptRetired(t, f, second)

	// Through a sweep with the record aged past an hour: the store the
	// clone names directly stays, the one nothing names goes.
	ageRecord(t, f, 2*time.Hour)
	if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("sweep call = %+v, want healthy", res)
	}
	requireKeptRetired(t, f, first)
	requireStoreGone(t, f, second)
	if packs, _ := filepath.Glob(filepath.Join(f.mdir(), first, "objects", "pack", "pack-*.pack")); len(packs) == 0 {
		t.Fatalf("the store the clone names lost its packs")
	}
}

// freeWhile reports too little free space while path exists and room once
// it's gone, until the test ends.
func freeWhile(t *testing.T, path string, need uint64) {
	t.Helper()
	orig := freeSpace
	freeSpace = func(string) (uint64, error) {
		if _, err := os.Lstat(path); err == nil {
			return need - 1, nil
		}
		return need, nil
	}
	t.Cleanup(func() { freeSpace = orig })
}

func TestRestore_SpacePressureRemovesUnpinnedRetiredStores(t *testing.T) {
	// Two retired stores, both younger than an hour: the first nothing
	// names, the second a clone names directly.
	setup := func(t *testing.T) (fixture, Options, string, string) {
		f, unpinned := restoredOnce(t)
		pinned := f.linkTarget(t)
		direct := directBorrower(t, f, "half", pinned)
		damageHEAD(t, f)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
			t.Fatalf("second restore = %+v", res)
		}
		requireKeptRetired(t, f, unpinned)
		requireKeptRetired(t, f, pinned)
		opts := f.applyOpts()
		opts.Instances = append(append([]string{}, opts.Instances...), direct)
		damageHEAD(t, f)
		return f, opts, unpinned, pinned
	}

	t.Run("with a scan", func(t *testing.T) {
		f, opts, unpinned, pinned := setup(t)
		freeWhile(t, filepath.Join(f.mdir(), unpinned), spaceNeeded(f.mdir(), "app"))
		scan, calls := countingScan(f)
		opts.Scan = scan
		logged := len(f.logLines(t))
		res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
			t.Fatalf("restore = %+v, want restored", res)
		}
		if *calls != 1 {
			t.Fatalf("the scan ran %d times, want once", *calls)
		}
		requireStoreGone(t, f, unpinned)
		requireKeptRetired(t, f, pinned)
		lines := storeLinesOf(newLines(t, f, logged))
		if len(lines) != 1 || lines[0]["event"] != eventRemove || lines[0]["store"] != unpinned || lines[0]["reason"] != reasonSpace {
			t.Fatalf("store lines = %v, want one remove of %s for %s", lines, unpinned, reasonSpace)
		}
	})
	t.Run("with no scan", func(t *testing.T) {
		f, opts, unpinned, pinned := setup(t)
		freeWhile(t, filepath.Join(f.mdir(), unpinned), spaceNeeded(f.mdir(), "app"))
		before := snapMirror(t, f)
		res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil {
			t.Fatalf("restore = %+v, want restore-failed for free space", res)
		}
		before.requireSame(t, f)
		requireKeptRetired(t, f, unpinned)
		requireKeptRetired(t, f, pinned)
	})
	t.Run("with a failing scan", func(t *testing.T) {
		f, opts, unpinned, _ := setup(t)
		freeWhile(t, filepath.Join(f.mdir(), unpinned), spaceNeeded(f.mdir(), "app"))
		opts.Scan = func() ([]string, error) { return nil, errors.New("kept/ can't be read") }
		before := snapMirror(t, f)
		if res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed {
			t.Fatalf("restore = %+v, want restore-failed for free space", res)
		}
		before.requireSame(t, f)
	})
}
