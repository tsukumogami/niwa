package mirror

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// seedOpts are the fixture's options as the borrowing create will pass them
// to Seed. The fixture's clone borrows nothing.
func (f fixture) seedOpts() Options {
	opts := f.opts()
	opts.Command = "create"
	return opts
}

// seeded returns a fixture whose mirror one Seed has built, with no clone
// borrowing from it.
func seeded(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t)
	if res := Seed(context.Background(), f.seedOpts(), f.repo()); res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("seeding the mirror: %+v", res)
	}
	return f
}

// requireNothingBuilt fails when anything of app's mirror is on disk: an
// entry at the stable path, a store, a leftover under a temporary name or a
// state file.
func requireNothingBuilt(t *testing.T, f fixture) {
	t.Helper()
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); !os.IsNotExist(err) {
		t.Fatalf("there's an entry at the stable path (%v)", err)
	}
	if stores := oldStores(f.mdir(), "app"); len(stores) != 0 {
		t.Fatalf(".store holds %v of app, want none", stores)
	}
	requireNoLeftovers(t, f)
	if _, err := os.Lstat(statePath(f.mdir(), "app")); !os.IsNotExist(err) {
		t.Fatalf("there's a state file for app (%v)", err)
	}
}

// requireRestored fails unless res is a usable restore of a missing mirror
// with no error.
func requireRestored(t *testing.T, what string, res Result) {
	t.Helper()
	if res.Fault != FaultMissing || res.Outcome != OutcomeRestored || res.Err != nil || !res.Usable() {
		t.Fatalf("%s = %+v, want missing/restored with no error", what, res)
	}
}

func TestSeed_RefusesAnIDOrURLThatCantTakeAMirror(t *testing.T) {
	f := newFixture(t)
	calls := recordGit(t)
	cases := []struct {
		repo   Repo
		stable string
	}{
		{Repo{ID: "", URL: f.repo().URL}, ""},
		{Repo{ID: ".meta", URL: f.repo().URL}, ""},
		{Repo{ID: "../app", URL: f.repo().URL}, ""},
		{Repo{ID: strings.Repeat("a", 101), URL: f.repo().URL}, ""},
		{Repo{ID: "app", URL: ""}, StablePath(f.home, f.root, "app")},
	}
	for _, c := range cases {
		res := Seed(context.Background(), f.seedOpts(), c.repo)
		if res.Fault != FaultMissing || res.Outcome != OutcomeRestoreFailed || res.Err == nil || res.Usable() {
			t.Errorf("Seed(%q, %q) = %+v, want missing/restore-failed with an error", c.repo.ID, c.repo.URL, res)
		}
		if res.StablePath != c.stable {
			t.Errorf("Seed(%q, %q) StablePath = %q, want %q", c.repo.ID, c.repo.URL, res.StablePath, c.stable)
		}
	}
	for _, pattern := range []string{filepath.Join(".niwa", "w-*"), filepath.Join(".niwa", "locks", "m-*.lock")} {
		if found, _ := filepath.Glob(filepath.Join(f.home, pattern)); len(found) != 0 {
			t.Fatalf("a refused Seed made %v", found)
		}
	}
	emptyDir(t, f.home)
	if got := calls(); len(got) != 0 {
		t.Fatalf("a refused Seed ran git %q", got)
	}
}

func TestSeed_BuildsTheFirstMirror(t *testing.T) {
	f := newFixture(t)
	if res := EnsureHealthy(context.Background(), f.seedOpts(), []Repo{f.repo()}); len(res) != 0 {
		t.Fatalf("EnsureHealthy with no borrower = %+v, want no result", res)
	}
	if found, _ := filepath.Glob(filepath.Join(f.home, ".niwa", "w-*")); len(found) != 0 {
		t.Fatalf("EnsureHealthy with no borrower made %v", found)
	}

	start := time.Now()
	res := Seed(context.Background(), f.seedOpts(), f.repo())
	requireRestored(t, "Seed", res)
	if res.LogErr != nil || res.Unrecoverable != nil {
		t.Fatalf("Seed = %+v, want no log error and nothing unrecoverable", res)
	}
	stable := StablePath(f.home, f.root, "app")
	if res.StablePath != stable || stable != filepath.Join(f.home, ".niwa", WorkspaceKey(f.root), "mirrors", "app") {
		t.Fatalf("StablePath = %q, want %q", res.StablePath, stable)
	}
	info, err := os.Lstat(stable)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the stable path is not a link: %v (%v)", info, err)
	}
	target := f.linkTarget(t)
	if !validLinkTarget("app", target) {
		t.Fatalf("link target %q is not a store name", target)
	}
	if stores := oldStores(f.mdir(), "app"); len(stores) != 1 || filepath.Join(".store", stores[0]) != target {
		t.Fatalf(".store holds %v of app, want only the linked %s", stores, target)
	}
	store := f.store(t)
	if _, err := gitTry(store, "--git-dir", store, "rev-parse", "--verify", "--quiet", "HEAD^{commit}"); err != nil {
		t.Fatalf("the store's HEAD doesn't resolve to a commit: %v", err)
	}
	if got, want := f.storeRef(t, "refs/heads/main"), testGit(t, f.remote, "rev-parse", "refs/heads/main"); got != want {
		t.Fatalf("the store's main = %q, want the remote's %q", got, want)
	}

	t.Run("config, state file and modes", func(t *testing.T) {
		if got, want := configList(t, store), wantConfig(f.repo().URL); !slices.Equal(got, want) {
			t.Fatalf("config =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		sinfo, err := os.Lstat(statePath(f.mdir(), "app"))
		if err != nil || !sinfo.Mode().IsRegular() || sinfo.Mode().Perm() != 0o600 {
			t.Fatalf("state file = %v (%v), want a 0600 regular file", sinfo, err)
		}
		st, err := readState(f.mdir(), "app")
		if err != nil {
			t.Fatal(err)
		}
		if st.Schema != 1 || st.RepoID != "app" || st.URL != recordURL(f.repo().URL) || st.Store != target {
			t.Fatalf("state = %+v, want schema 1, repo app, url %q, store %q", st, recordURL(f.repo().URL), target)
		}
		requireManifestMatches(t, f)
		if st.LastRefresh != st.LastDeepVerify {
			t.Fatalf("last_refresh %s != last_deep_verify %s", st.LastRefresh, st.LastDeepVerify)
		}
		notBefore(t, "last_refresh", st.LastRefresh, start)

		mdir := f.mdir()
		for _, dir := range []string{keyDir(f.home, f.root), mdir, metaDir(mdir), filepath.Join(mdir, ".store"), filepath.Join(mdir, ".tmp")} {
			if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
				t.Fatalf("%s = %v (%v), want a 0700 directory", dir, info, err)
			}
		}
		for _, file := range []string{LockPath(f.home, f.root, "app"), logPath(mdir)} {
			if info, err := os.Lstat(file); err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("%s = %v (%v), want mode 0600", file, info, err)
			}
		}
		requireNoLeftovers(t, f)
	})
}

func TestSeed_FirstBuildRunsOneCloneAndNoProbe(t *testing.T) {
	f := newFixture(t)
	before := treeFiles(t, f.clone)
	calls := recordGit(t)
	var probes atomic.Int32
	probeHook = func(string) { probes.Add(1) }
	t.Cleanup(func() { probeHook = nil })

	requireRestored(t, "Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
	got := calls()
	if len(got) != 1 {
		t.Fatalf("the first build ran git %q, want exactly one clone", got)
	}
	tmpPath := regexp.MustCompile(`^` + regexp.QuoteMeta(filepath.Join(f.mdir(), ".tmp")+"/") + `app-[0-9a-f]{8}$`)
	clone := got[0]
	want := []string{"clone", "--mirror", "--template=", "--quiet", "--", f.repo().URL}
	if len(clone) != len(want)+1 || !slices.Equal(clone[:len(want)], want) || !tmpPath.MatchString(clone[len(want)]) {
		t.Fatalf("the first build ran git %q, want %q and a .tmp/app-<8 hex> path", clone, want)
	}
	for _, sub := range []string{"for-each-ref", "rev-list", "cat-file", "verify-pack", "fetch", "rev-parse"} {
		if slices.Contains(clone, sub) {
			t.Fatalf("the first build ran git %s", sub)
		}
	}
	if n := probes.Load(); n != 0 {
		t.Fatalf("the probe ran %d times, want never", n)
	}
	if !maps.Equal(treeFiles(t, f.clone), before) {
		t.Fatal("Seed changed the clone passed in Options.Instances")
	}
	if _, err := os.Lstat(filepath.Join(f.clone, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("the clone has an alternates file (%v)", err)
	}
}

func TestSeed_LogsACheckAndARestore(t *testing.T) {
	for _, refresh := range []bool{false, true} {
		name := "without refresh"
		if refresh {
			name = "with refresh"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			opts := f.seedOpts()
			opts.Refresh = refresh
			res := Seed(context.Background(), opts, f.repo())
			requireRestored(t, "Seed", res)
			lines := f.logLines(t)
			requireEvents(t, lines, [3]string{"check", "missing", "restored"}, [3]string{"restore", "missing", "restored"})
			for i, l := range lines {
				if l["command"] != "create" {
					t.Fatalf("log line %d command = %v, want create", i, l["command"])
				}
			}
			if got, want := lines[0]["check_ms"], float64(res.CheckDuration.Milliseconds()); got != want {
				t.Fatalf("check_ms = %v, want %v", got, want)
			}
			if _, ok := lines[1]["took_ms"]; !ok {
				t.Fatalf("the restore line %v has no took_ms", lines[1])
			}
		})
	}

	t.Run("a log that can't be written", func(t *testing.T) {
		f := newFixture(t)
		blocker := logPath(f.mdir())
		mkdir(t, blocker)
		res := Seed(context.Background(), f.seedOpts(), f.repo())
		if res.Outcome != OutcomeRestored || res.Err != nil || res.LogErr == nil {
			t.Fatalf("Seed = %+v, want restored with no error and LogErr set", res)
		}
		if info, err := os.Lstat(blocker); err != nil || !info.IsDir() {
			t.Fatalf("the directory at the log path is gone: %v (%v)", info, err)
		}
	})
}

func TestSeed_OnASeededMirrorReturnsWhatACheckDoes(t *testing.T) {
	t.Run("healthy", func(t *testing.T) {
		f := seeded(t)
		target := f.linkTarget(t)
		state := stateBytes(t, f)
		logged := len(f.logLines(t))
		calls := recordGit(t)
		res := Seed(context.Background(), f.seedOpts(), f.repo())
		gitHook = nil
		if res.Fault != FaultNone || res.Outcome != OutcomeHealthy || res.Err != nil {
			t.Fatalf("Seed = %+v, want healthy", res)
		}
		revParse := []string{"--git-dir=" + filepath.Join(f.mdir(), target), "rev-parse", "--verify", "--quiet", "HEAD^{commit}"}
		if got := calls(); len(got) != 1 || !slices.Equal(got[0], revParse) {
			t.Fatalf("a healthy Seed ran git %q, want exactly %q", got, revParse)
		}
		if f.linkTarget(t) != target || string(stateBytes(t, f)) != string(state) {
			t.Fatal("a healthy Seed changed the link or the state file")
		}
		requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"})
	})

	t.Run("refreshed", func(t *testing.T) {
		f := seeded(t)
		commit := pushCommit(t, f, "main", "second")
		logged := len(f.logLines(t))
		opts := f.seedOpts()
		opts.Refresh = true
		res := Seed(context.Background(), opts, f.repo())
		if res.Fault != FaultNone || res.Outcome != OutcomeHealthy || res.Err != nil {
			t.Fatalf("Seed = %+v, want healthy", res)
		}
		if !f.storeHas(t, commit) {
			t.Fatalf("the store lacks the remote's new commit %s", commit)
		}
		requireEvents(t, newLines(t, f, logged), [3]string{"check", "", "healthy"}, [3]string{"refresh", "", "healthy"})
	})

	t.Run("pack missing", func(t *testing.T) {
		f := seeded(t)
		old := f.linkTarget(t)
		packs := f.packs(t)
		if len(packs) != 1 {
			t.Fatalf("the seeded store holds %d packs, want 1", len(packs))
		}
		if err := os.Remove(packs[0]); err != nil {
			t.Fatal(err)
		}
		res := Seed(context.Background(), f.seedOpts(), f.repo())
		if res.Fault != FaultPackMissing || res.Outcome != OutcomeRestored || res.Err != nil {
			t.Fatalf("Seed = %+v, want pack-missing/restored", res)
		}
		if got := f.linkTarget(t); got == old || !validLinkTarget("app", got) {
			t.Fatalf("link = %q, want a new store, not %q", got, old)
		}
	})
}

func TestSeed_HoldsTheLockThroughTheBuild(t *testing.T) {
	f := newFixture(t)
	lock := LockPath(f.home, f.root, "app")
	var tried error
	stageHook = func(s string) error {
		if s == stageClone {
			release, err := filelock.TryFileLock(lock, "test probe")
			if err == nil {
				release()
			}
			tried = err
		}
		return nil
	}
	t.Cleanup(func() { stageHook = nil })

	if res := Seed(context.Background(), f.seedOpts(), f.repo()); res.Outcome != OutcomeRestored {
		t.Fatalf("Seed = %+v, want restored", res)
	}
	if !errors.Is(tried, filelock.ErrLockHeld) {
		t.Fatalf("TryFileLock during the clone = %v, want ErrLockHeld", tried)
	}
	release, err := filelock.TryFileLock(lock, "test after")
	if err != nil {
		t.Fatalf("the lock is still held after Seed: %v", err)
	}
	release()
}

func TestSeed_BusyWhileAnotherCommandHoldsTheLock(t *testing.T) {
	f := newFixture(t)
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "other command")
	if err != nil {
		t.Fatal(err)
	}
	calls := recordGit(t)
	opts := f.seedOpts()
	opts.LockWait = time.Second
	res := Seed(context.Background(), opts, f.repo())
	if res.Fault != FaultMissing || res.Outcome != OutcomeBusy || !errors.Is(res.Err, filelock.ErrLockHeld) || res.Usable() {
		release()
		t.Fatalf("Seed = %+v, want missing/busy with ErrLockHeld", res)
	}
	requireNothingBuilt(t, f)
	requireEvents(t, f.logLines(t), [3]string{"check", "missing", "busy"}, [3]string{"restore", "missing", "busy"})
	if got := calls(); len(got) != 0 {
		release()
		t.Fatalf("a busy Seed ran git %q", got)
	}

	release()
	requireRestored(t, "Seed after the lock is released", Seed(context.Background(), opts, f.repo()))
}

func TestSeed_TwoRacingSeedsBuildOnce(t *testing.T) {
	for i := range 10 {
		f := newFixture(t)
		calls := recordGit(t)
		var results [2]Result
		var wg sync.WaitGroup
		begin := make(chan struct{})
		for g := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-begin
				results[g] = Seed(context.Background(), f.seedOpts(), f.repo())
			}()
		}
		close(begin)
		wg.Wait()
		gitHook = nil

		outcomes := map[Outcome]int{}
		for _, r := range results {
			if !r.Usable() || r.Err != nil {
				t.Fatalf("round %d: result %+v, want usable with no error", i, r)
			}
			outcomes[r.Outcome]++
		}
		if outcomes[OutcomeRestored] != 1 || outcomes[OutcomeHealthy] != 1 {
			t.Fatalf("round %d: outcomes %v, want one restored and one healthy", i, outcomes)
		}
		restores := 0
		for _, l := range f.logLines(t) {
			if l["event"] == "restore" {
				restores++
			}
		}
		if restores != 1 {
			t.Fatalf("round %d: the log holds %d restore lines, want 1", i, restores)
		}
		stores := oldStores(f.mdir(), "app")
		st, err := readState(f.mdir(), "app")
		if err != nil {
			t.Fatal(err)
		}
		if len(stores) != 1 || filepath.Join(".store", stores[0]) != f.linkTarget(t) || st.Store != f.linkTarget(t) {
			t.Fatalf("round %d: .store holds %v, link %q, state %q, want one store named by both", i, stores, f.linkTarget(t), st.Store)
		}
		clones := 0
		for _, c := range calls() {
			if len(c) > 0 && c[0] == "clone" {
				clones++
			}
		}
		if clones != 1 {
			t.Fatalf("round %d: %d clones across both calls, want 1", i, clones)
		}
	}
}

func TestSeed_AFailureLeavesNothingAndTheNextSeedBuilds(t *testing.T) {
	for _, stage := range []string{stageClone, stageState, stageStore, stageLink, stageSwap} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			setStageHook(t, stage)
			res := Seed(context.Background(), f.seedOpts(), f.repo())
			if res.Outcome != OutcomeRestoreFailed || res.Err == nil || res.Usable() {
				t.Fatalf("Seed = %+v, want restore-failed with an error", res)
			}
			requireNothingBuilt(t, f)
			stageHook = nil
			requireRestored(t, "the next Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
		})
	}

	t.Run("no repo at the URL", func(t *testing.T) {
		f := newFixture(t)
		absent := Repo{ID: "app", URL: "file://" + filepath.Join(t.TempDir(), "absent.git")}
		res := Seed(context.Background(), f.seedOpts(), absent)
		if res.Outcome != OutcomeRestoreFailed || res.Err == nil || res.Usable() {
			t.Fatalf("Seed = %+v, want restore-failed with an error", res)
		}
		requireNothingBuilt(t, f)
		requireRestored(t, "the next Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
	})

	t.Run("free space", func(t *testing.T) {
		f := newFixture(t)
		reportFree(t, minFreeUnknown-1, nil)
		calls := recordGit(t)
		res := Seed(context.Background(), f.seedOpts(), f.repo())
		gitHook = nil
		if res.Outcome != OutcomeRestoreFailed || res.Err == nil || res.Usable() || !strings.Contains(res.Err.Error(), "free space") {
			t.Fatalf("Seed = %+v, want restore-failed naming free space", res)
		}
		for _, c := range calls() {
			if len(c) > 0 && c[0] == "clone" {
				t.Fatalf("Seed cloned without the free space: %q", c)
			}
		}
		requireNothingBuilt(t, f)
		reportFree(t, minFreeUnknown, nil)
		requireRestored(t, "the next Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
	})

	t.Run(stageRecord, func(t *testing.T) {
		f := newFixture(t)
		setStageHook(t, stageRecord)
		res := Seed(context.Background(), f.seedOpts(), f.repo())
		if res.Outcome != OutcomeRestored || res.Err == nil {
			t.Fatalf("Seed = %+v, want restored with an error", res)
		}
		if !validLinkTarget("app", f.linkTarget(t)) {
			t.Fatalf("link = %q, want the new store", f.linkTarget(t))
		}
		if _, err := os.Lstat(statePath(f.mdir(), "app")); !os.IsNotExist(err) {
			t.Fatalf("there's a state file for app (%v)", err)
		}

		stageHook = nil
		next := EnsureHealthy(context.Background(), f.seedOpts(), []Repo{f.repo()})
		if len(next) != 1 {
			t.Fatalf("EnsureHealthy returned %d results, want 1", len(next))
		}
		requireRestored(t, "the next EnsureHealthy", next[0])
		st, err := readState(f.mdir(), "app")
		if err != nil || st.Store != f.linkTarget(t) {
			t.Fatalf("state = %+v (%v), want it to name the link's target %q", st, err, f.linkTarget(t))
		}
		if stores := oldStores(f.mdir(), "app"); len(stores) != 1 {
			t.Fatalf(".store holds %v of app, want one", stores)
		}
		requireNoLeftovers(t, f)
	})
}

func TestSeed_ReplacesALinkPointingOutside(t *testing.T) {
	f := newFixture(t)
	outside := t.TempDir()
	marker := filepath.Join(outside, "HEAD")
	if err := os.WriteFile(marker, []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mkdir(t, f.mdir())
	if err := os.Symlink(outside, StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}

	requireRestored(t, "Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
	if !validLinkTarget("app", f.linkTarget(t)) {
		t.Fatalf("link target after Seed = %q", f.linkTarget(t))
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "ref: refs/heads/main\n" {
		t.Fatalf("the outside directory was touched: %q (%v)", data, err)
	}
}

// TestSeed_ASeededMirrorIsRestoredUnderItsBorrower runs the contract the
// borrowing create relies on: seed, borrow through the stable path, lose the
// mirror, and have EnsureHealthy put it back with the borrower reading again.
func TestSeed_ASeededMirrorIsRestoredUnderItsBorrower(t *testing.T) {
	f := newFixture(t)
	requireRestored(t, "Seed", Seed(context.Background(), f.seedOpts(), f.repo()))
	stable := StablePath(f.home, f.root, "app")

	borrower := filepath.Join(t.TempDir(), "borrower")
	testGit(t, filepath.Dir(borrower), "clone", "--quiet", f.remote, borrower)
	alternates := filepath.Join(borrower, ".git", "objects", "info", "alternates")
	if err := os.WriteFile(alternates, []byte(stable+"/objects\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, borrower, "repack", "-a", "-d", "-l", "-q")
	testGit(t, borrower, "prune-packed")
	counts := strings.Split(testGit(t, borrower, "count-objects", "-v"), "\n")
	for _, want := range []string{"count: 0", "in-pack: 0"} {
		if !slices.Contains(counts, want) {
			t.Fatalf("count-objects -v = %q, want %q", counts, want)
		}
	}
	testGit(t, borrower, "fsck", "--full")
	altBytes, err := os.ReadFile(alternates)
	if err != nil {
		t.Fatal(err)
	}

	store := f.store(t)
	if err := os.Remove(stable); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}

	opts := f.seedOpts()
	opts.Instances = []string{borrower}
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 {
		t.Fatalf("EnsureHealthy returned %d results, want 1", len(res))
	}
	requireRestored(t, "EnsureHealthy", res[0])
	if len(res[0].Unrecoverable) != 0 {
		t.Fatalf("the restore left %v unrecoverable", res[0].Unrecoverable)
	}
	if now, err := os.ReadFile(alternates); err != nil || string(now) != string(altBytes) {
		t.Fatalf("the borrower's alternates changed: %q (%v)", now, err)
	}
	testGit(t, borrower, "rev-list", "--objects", "--all")
	testGit(t, borrower, "fsck", "--full")
}
