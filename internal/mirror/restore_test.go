package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

func TestGitBaseConfig_TurnsOffBackgroundMaintenance(t *testing.T) {
	for _, kv := range []string{"maintenance.auto=false", "gc.auto=0", "core.fsmonitor=false", "protocol.ext.allow=never"} {
		if !slices.Contains(gitBaseConfig, kv) {
			t.Errorf("gitBaseConfig lacks %q: %v", kv, gitBaseConfig)
		}
	}
}

// setStageHook fails the restore step named stage until the test ends.
func setStageHook(t *testing.T, stage string) {
	t.Helper()
	stageHook = func(s string) error {
		if s == stage {
			return errors.New("injected failure at " + s)
		}
		return nil
	}
	t.Cleanup(func() { stageHook = nil })
}

// treeFiles maps each regular file under dir, relative to it, to its bytes.
// A link is recorded by its target.
func treeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			files[rel] = "link:" + target
		case d.Type().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[rel] = string(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// mirrorSnap is what a restore that fails must leave as it was.
type mirrorSnap struct {
	link   string // "" when there is no link
	state  []byte
	stores map[string]map[string]string // .store/ entry -> its files
}

func snapMirror(t *testing.T, f fixture) mirrorSnap {
	t.Helper()
	s := mirrorSnap{stores: map[string]map[string]string{}}
	s.link, _ = os.Readlink(StablePath(f.home, f.root, "app"))
	s.state, _ = os.ReadFile(statePath(f.mdir(), "app"))
	entries, _ := os.ReadDir(filepath.Join(f.mdir(), ".store"))
	for _, e := range entries {
		s.stores[e.Name()] = treeFiles(t, filepath.Join(f.mdir(), ".store", e.Name()))
	}
	return s
}

func (s mirrorSnap) requireSame(t *testing.T, f fixture) {
	t.Helper()
	now := snapMirror(t, f)
	if now.link != s.link {
		t.Fatalf("link = %q, want %q", now.link, s.link)
	}
	if !bytes.Equal(now.state, s.state) {
		t.Fatalf("state file changed:\n%s\nwas\n%s", now.state, s.state)
	}
	if !slices.Equal(slices.Sorted(maps.Keys(now.stores)), slices.Sorted(maps.Keys(s.stores))) {
		t.Fatalf(".store holds %v, want %v", slices.Sorted(maps.Keys(now.stores)), slices.Sorted(maps.Keys(s.stores)))
	}
	for name, files := range s.stores {
		if !maps.Equal(now.stores[name], files) {
			t.Fatalf("the files of store %s changed", name)
		}
	}
}

// requireNoLeftovers fails when a restore left a clone, a link or a state
// file under a temporary name.
func requireNoLeftovers(t *testing.T, f fixture) {
	t.Helper()
	emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
	for _, pattern := range []string{".link-*", filepath.Join(".meta", ".app.json-*")} {
		if found, _ := filepath.Glob(filepath.Join(f.mdir(), pattern)); len(found) != 0 {
			t.Fatalf("leftovers: %v", found)
		}
	}
}

// damageHEAD removes the linked store's HEAD, a repo-unreadable fault.
func damageHEAD(t *testing.T, f fixture) {
	t.Helper()
	if err := os.Remove(filepath.Join(f.store(t), "HEAD")); err != nil {
		t.Fatal(err)
	}
}

// truncatePack cuts the first pack of store to half, a pack-changed fault.
func truncatePack(t *testing.T, store string) {
	t.Helper()
	packs, _ := filepath.Glob(filepath.Join(store, "objects", "pack", "pack-*.pack"))
	if len(packs) == 0 {
		t.Fatalf("%s holds no pack", store)
	}
	info, err := os.Stat(packs[0])
	if err != nil {
		t.Fatal(err)
	}
	makeWritable(t, packs[0])
	if err := os.Truncate(packs[0], info.Size()/2); err != nil {
		t.Fatal(err)
	}
}

// hideRemote moves the fixture's remote away, so its URL names no remote,
// and returns a func that puts it back.
func hideRemote(t *testing.T, f fixture) func() {
	t.Helper()
	gone := f.remote + ".gone"
	if err := os.Rename(f.remote, gone); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Rename(gone, f.remote); err != nil {
			t.Fatal(err)
		}
	}
}

// extraPack makes a pack holding a commit no fixture remote has and copies
// it, with its .idx, into dir. It returns the pack's file name.
func extraPack(t *testing.T, dir, content string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "extra")
	testGit(t, filepath.Dir(work), "init", "--quiet", work)
	if err := os.WriteFile(filepath.Join(work, "x.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", "x.txt")
	testGit(t, work, "commit", "--quiet", "-m", content)
	testGit(t, work, "repack", "-a", "-d", "-q")
	packs, _ := filepath.Glob(filepath.Join(work, ".git", "objects", "pack", "pack-*.pack"))
	if len(packs) != 1 {
		t.Fatalf("made %d packs, want 1", len(packs))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{packs[0], idxOf(packs[0])} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.Base(src)), data, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Base(packs[0])
}

func TestRestore_AFailureAtEachStepChangesNothing(t *testing.T) {
	for _, stage := range []string{stageClone, stageState, stageStore, stageLink, stageSwap} {
		t.Run(stage, func(t *testing.T) {
			f := built(t)
			damageHEAD(t, f)
			before := snapMirror(t, f)
			setStageHook(t, stage)

			res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
			if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil {
				t.Fatalf("result = %+v, want restore-failed with an error", res)
			}
			if !strings.Contains(res[0].Err.Error(), "injected failure at "+stage) {
				t.Fatalf("error %q is not the injected one", res[0].Err)
			}
			before.requireSame(t, f)
			requireNoLeftovers(t, f)
		})
	}
}

func TestRestore_AFailedStateRenameAfterTheSwapIsRebuiltNextTime(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	damageHEAD(t, f)
	setStageHook(t, stageRecord)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err == nil {
		t.Fatalf("result = %+v, want restored with an error", res)
	}
	if f.linkTarget(t) == old || !validLinkTarget("app", f.linkTarget(t)) {
		t.Fatalf("link = %q, want a new store", f.linkTarget(t))
	}

	stageHook = nil
	res = EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultMissing || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("next result = %+v, want missing/restored", res)
	}
	st, err := readState(f.mdir(), "app")
	if err != nil || st.Store != f.linkTarget(t) {
		t.Fatalf("state = %+v (%v), want it to name the link's target %q", st, err, f.linkTarget(t))
	}
	if stores := oldStores(f.mdir(), "app"); len(stores) != 1 {
		t.Fatalf(".store holds %v of app, want one", stores)
	}
	requireNoLeftovers(t, f)
}

func TestRestore_ClearsThisReposLeftoversUnderTheLock(t *testing.T) {
	f := built(t)
	damageHEAD(t, f)
	tmp, meta := filepath.Join(f.mdir(), ".tmp"), metaDir(f.mdir())
	writeFile(t, filepath.Join(tmp, "app-1234abcd", "objects", "x"), "partial clone")
	writeFile(t, filepath.Join(meta, ".app.json-1234abcd"), "{}")
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "keep")
	writeFile(t, outsideFile, "outside")
	if err := os.Symlink(outside, filepath.Join(tmp, "app-5678abcd")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tmp, "app-extra-1234abcd", "x"), "other repo")
	writeFile(t, filepath.Join(meta, ".app-extra.json-1234abcd"), "{}")

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	for _, gone := range []string{
		filepath.Join(tmp, "app-1234abcd"),
		filepath.Join(meta, ".app.json-1234abcd"),
		filepath.Join(tmp, "app-5678abcd"),
	} {
		if _, err := os.Lstat(gone); !os.IsNotExist(err) {
			t.Errorf("%s is still there (%v)", gone, err)
		}
	}
	for _, kept := range []string{filepath.Join(tmp, "app-extra-1234abcd", "x"), filepath.Join(meta, ".app-extra.json-1234abcd")} {
		if _, err := os.Lstat(kept); err != nil {
			t.Errorf("%s of another repo was removed (%v)", kept, err)
		}
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "outside" {
		t.Fatalf("the directory a leftover link named was touched: %q (%v)", data, err)
	}
}

func TestEnsureHealthy_AHealthyCheckClearsLeftoversWhenTheLockIsFree(t *testing.T) {
	f := built(t)
	target := f.linkTarget(t)
	state, err := os.ReadFile(statePath(f.mdir(), "app"))
	if err != nil {
		t.Fatal(err)
	}
	tmpLeft := filepath.Join(f.mdir(), ".tmp", "app-0badc0de")
	metaLeft := filepath.Join(metaDir(f.mdir()), ".app.json-0badc0de")
	plant := func() {
		writeFile(t, filepath.Join(tmpLeft, "objects", "x"), "partial clone")
		writeFile(t, metaLeft, "{}")
	}

	plant()
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if f.linkTarget(t) != target {
		t.Fatal("a healthy check moved the link")
	}
	if now, _ := os.ReadFile(statePath(f.mdir(), "app")); !bytes.Equal(now, state) {
		t.Fatal("a healthy check changed the state file")
	}
	emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
	entries, err := os.ReadDir(metaDir(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != "app.json,mirror.log" {
		t.Fatalf(".meta holds %v, want only app.json and mirror.log", names)
	}

	plant()
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "test holder")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	start := time.Now()
	res = EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if took := time.Since(start); took >= time.Second {
		t.Fatalf("a healthy check with the lock held took %s, want under a second", took)
	}
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result with the lock held = %+v, want healthy", res)
	}
	for _, kept := range []string{tmpLeft, metaLeft} {
		if _, err := os.Lstat(kept); err != nil {
			t.Fatalf("%s was cleared while another command held the lock (%v)", kept, err)
		}
	}
}

func TestRestore_RelinksTheRecordedStoreWithNoNetwork(t *testing.T) {
	f := built(t)
	target := f.linkTarget(t)
	before := snapMirror(t, f)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	hideRemote(t, f)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultMissing || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("result = %+v, want missing/restored", res)
	}
	if f.linkTarget(t) != target {
		t.Fatalf("link = %q, want the recorded store %q", f.linkTarget(t), target)
	}
	before.requireSame(t, f)
	requireNoLeftovers(t, f)
}

func TestRestore_NoRelinkToAStoreThatFailsItsCheckOrCameFromAnotherRemote(t *testing.T) {
	for _, tc := range []struct {
		name   string
		spoil  func(t *testing.T, f fixture)
		remote bool
	}{
		{"truncated pack, remote reachable", func(t *testing.T, f fixture) { truncatePack(t, f.store(t)) }, true},
		{"truncated pack, remote gone", func(t *testing.T, f fixture) { truncatePack(t, f.store(t)) }, false},
		{"another remote recorded, remote reachable", rewriteStateURL, true},
		{"another remote recorded, remote gone", rewriteStateURL, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := built(t)
			target := f.linkTarget(t)
			tc.spoil(t, f)
			if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
				t.Fatal(err)
			}
			if !tc.remote {
				hideRemote(t, f)
			}
			before := snapMirror(t, f)

			res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
			if len(res) != 1 || res[0].Fault != FaultMissing {
				t.Fatalf("result = %+v, want fault missing", res)
			}
			if !tc.remote {
				if res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil {
					t.Fatalf("result = %+v, want restore-failed", res)
				}
				before.requireSame(t, f)
				requireNoLeftovers(t, f)
				return
			}
			if res[0].Outcome != OutcomeRestored {
				t.Fatalf("result = %+v, want restored", res)
			}
			if got := f.linkTarget(t); got == target || !validLinkTarget("app", got) {
				t.Fatalf("link = %q, want a newly cloned store, not %q", got, target)
			}
		})
	}
}

// rewriteStateURL records another remote in the state file.
func rewriteStateURL(t *testing.T, f fixture) {
	t.Helper()
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	st.URL = "file:///elsewhere/other.git"
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(f.mdir(), "app"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// reportFree replaces the free-space read until the test ends.
func reportFree(t *testing.T, free uint64, err error) {
	t.Helper()
	orig := freeSpace
	freeSpace = func(string) (uint64, error) { return free, err }
	t.Cleanup(func() { freeSpace = orig })
}

func TestRestore_RefusesToCloneWithoutTheFreeSpace(t *testing.T) {
	t.Run("sized by the state file", func(t *testing.T) {
		f := built(t)
		st, err := readState(f.mdir(), "app")
		if err != nil {
			t.Fatal(err)
		}
		var need uint64
		for _, p := range st.Packs {
			need += uint64(p.Size)
		}
		damageHEAD(t, f)
		before := snapMirror(t, f)

		reportFree(t, need-1, nil)
		res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "free space") {
			t.Fatalf("result = %+v, want restore-failed naming free space", res)
		}
		before.requireSame(t, f)
		requireNoLeftovers(t, f)

		reportFree(t, need, nil)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
			t.Fatalf("result with room = %+v, want restored", res)
		}
	})
	t.Run("no state file", func(t *testing.T) {
		f := newFixture(t)
		f.borrow(t)
		reportFree(t, minFreeUnknown-1, nil)
		res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
		if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || !strings.Contains(res[0].Err.Error(), "free space") {
			t.Fatalf("result under 1 GiB = %+v, want restore-failed naming free space", res)
		}
		emptyDir(t, filepath.Join(f.mdir(), ".tmp"))
		emptyDir(t, filepath.Join(f.mdir(), ".store"))

		reportFree(t, minFreeUnknown, nil)
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
			t.Fatalf("result at 1 GiB = %+v, want restored", res)
		}
	})
	t.Run("a failed read", func(t *testing.T) {
		f := newFixture(t)
		f.borrow(t)
		reportFree(t, 0, errors.New("statfs failed"))
		if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeRestored {
			t.Fatalf("result = %+v, want restored", res)
		}
	})
}

func TestCheck_URLChanged(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	onlyOld := extraPack(t, filepath.Join(f.store(t), "objects", "pack"), "only the old store")
	rewriteStateURL(t, f)

	if got := check(context.Background(), f.mdir(), f.repo()); got != FaultURLChanged {
		t.Fatalf("check = %q, want url-changed", got)
	}
	if got := check(context.Background(), f.mdir(), Repo{ID: "app"}); got != FaultNone {
		t.Fatalf("check with no URL = %q, want no fault", got)
	}

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("result = %+v, want url-changed/restored", res)
	}
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != recordURL(f.repo().URL) {
		t.Fatalf("state url = %q, want %q", st.URL, recordURL(f.repo().URL))
	}
	if f.linkTarget(t) == old {
		t.Fatal("the link still names the old store")
	}
	if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", onlyOld)); !os.IsNotExist(err) {
		t.Fatalf("a pack only the old store held reached the new one (%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(f.mdir(), old)); !os.IsNotExist(err) {
		t.Fatalf("the old store is still there (%v)", err)
	}
}

func TestCheck_URLDifferingOnlyInSecretsIsNoFault(t *testing.T) {
	f := built(t)
	if got := check(context.Background(), f.mdir(), Repo{ID: "app", URL: f.repo().URL + "?x=1#f"}); got != FaultNone {
		t.Fatalf("check with a query and fragment = %q, want no fault", got)
	}
	rec := state{URL: "https://host/o/r.git"}
	if urlChanged(Repo{ID: "app", URL: "https://user:tok@host/o/r.git?x=1#f"}, rec) {
		t.Fatal("userinfo, query and fragment read as another remote")
	}
	if !urlChanged(Repo{ID: "app", URL: "https://host/o/other.git"}, rec) {
		t.Fatal("another path read as the same remote")
	}
	if urlChanged(Repo{ID: "app"}, rec) {
		t.Fatal("an empty URL was compared")
	}
}

// copyTree copies the regular files and directories under src to dst,
// keeping each file's mode.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
}

// realDirAtStable replaces the stable link with a real directory holding a
// copy of the linked store, one pack only it holds and a file "keep". It
// returns the extra pack's name.
func realDirAtStable(t *testing.T, f fixture) string {
	t.Helper()
	stable := StablePath(f.home, f.root, "app")
	store := f.store(t)
	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	copyTree(t, store, stable)
	pack := extraPack(t, filepath.Join(stable, "objects", "pack"), "only the real directory")
	writeFile(t, filepath.Join(stable, "keep"), "mine")
	return pack
}

func TestRestore_MovesARealDirectoryAsideAndKeepsIt(t *testing.T) {
	f := built(t)
	only := realDirAtStable(t, f)
	stable := StablePath(f.home, f.root, "app")
	files := treeFiles(t, stable)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Fault != FaultMissing || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("result = %+v, want missing/restored", res)
	}
	if !validLinkTarget("app", f.linkTarget(t)) {
		t.Fatalf("link = %q, want a store", f.linkTarget(t))
	}
	if err := verifyPack(context.Background(), idxOf(filepath.Join(f.store(t), "objects", "pack", only))); err != nil {
		t.Fatalf("the new store lacks the pack only the directory held: %v", err)
	}
	moved, _ := filepath.Glob(filepath.Join(f.mdir(), ".store", "app-*.moved"))
	if len(moved) != 1 || !storeNamePattern("app").MatchString(strings.TrimSuffix(filepath.Base(moved[0]), ".moved")) {
		t.Fatalf("moved directories = %v, want one .store/app-<stamp>-<rand>.moved", moved)
	}
	if !maps.Equal(treeFiles(t, moved[0]), files) {
		t.Fatal("the moved directory's files changed")
	}

	// A second restore neither reads nor removes it: a pack added to it now
	// never reaches the next store.
	later := extraPack(t, filepath.Join(moved[0], "objects", "pack"), "added after the move")
	files = treeFiles(t, moved[0])
	damageHEAD(t, f)
	res = EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("second result = %+v, want restored", res)
	}
	if !maps.Equal(treeFiles(t, moved[0]), files) {
		t.Fatal("the second restore changed the moved directory")
	}
	if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", later)); !os.IsNotExist(err) {
		t.Fatalf("the second restore salvaged from the moved directory (%v)", err)
	}
	if got := Recorded(f.home, f.root, nil); strings.Join(got, ",") != "app" {
		t.Fatalf("Recorded = %v, want only app", got)
	}
}

func TestRestore_AFailedCloneLeavesTheRealDirectoryInPlace(t *testing.T) {
	f := built(t)
	realDirAtStable(t, f)
	stable := StablePath(f.home, f.root, "app")
	files := treeFiles(t, stable)
	hideRemote(t, f)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed {
		t.Fatalf("result = %+v, want restore-failed", res)
	}
	if info, err := os.Lstat(stable); err != nil || !info.IsDir() {
		t.Fatalf("the stable path is no longer the directory (%v)", err)
	}
	if !maps.Equal(treeFiles(t, stable), files) {
		t.Fatal("the directory at the stable path changed")
	}
	if moved, _ := filepath.Glob(filepath.Join(f.mdir(), ".store", "*.moved")); len(moved) != 0 {
		t.Fatalf("moved directories after a failed clone: %v", moved)
	}
	requireNoLeftovers(t, f)
}
