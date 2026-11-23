package mirror

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// lostFixture is a mirror with two real borrower clones, a and b. The
// remote gained a branch topic at commit topic, which a refresh brought into
// the mirror and each borrower fetched as origin/topic; both borrowers were
// then repacked against the mirror, and a got a local branch (keepRef) with
// one unpushed commit own on topic that adds mine.txt.
type lostFixture struct {
	fixture
	a, b      string
	topic     string
	topicFile string
	keepRef   string
	own       string
	ownBlob   string
}

// lostSetup says how newLostFixture builds the topic commit and the branch.
type lostSetup struct {
	topicFile string // the file the topic commit adds; "added-topic.txt" when empty
	keepRef   string // the local branch on topic; "refs/heads/keep" when empty
}

func (f lostFixture) opts() Options {
	return Options{Home: f.home, WorkspaceRoot: f.root, Instances: []string{f.a, f.b}, Command: "apply"}
}

func (f lostFixture) refreshOpts() Options {
	opts := f.opts()
	opts.Refresh = true
	return opts
}

func (f lostFixture) ensure(t *testing.T, opts Options) Result {
	t.Helper()
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 {
		t.Fatalf("EnsureHealthy returned %d results, want 1: %+v", len(res), res)
	}
	return res[0]
}

// pushFile commits one file named name to branch of the remote, starting
// from main, and returns the commit.
func pushFile(t *testing.T, remote, branch, name string) string {
	t.Helper()
	work := filepath.Join(t.TempDir(), "work")
	testGit(t, filepath.Dir(work), "clone", "--quiet", remote, work)
	testGit(t, work, "checkout", "--quiet", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(work, name), []byte("added on "+branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, work, "add", "--", name)
	testGit(t, work, "commit", "--quiet", "-m", "add on "+branch)
	testGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/"+branch)
	return testGit(t, work, "rev-parse", "HEAD")
}

func newLostFixture(t *testing.T, setup lostSetup) lostFixture {
	t.Helper()
	if setup.topicFile == "" {
		setup.topicFile = "added-topic.txt"
	}
	if setup.keepRef == "" {
		setup.keepRef = "refs/heads/keep"
	}
	f := lostFixture{fixture: newFixture(t), topicFile: setup.topicFile, keepRef: setup.keepRef}
	f.a = filepath.Join(f.root, "a", "tools", "app")
	f.b = filepath.Join(f.root, "b", "tools", "app")
	line := StablePath(f.home, f.root, "app") + "/objects\n"
	for _, clone := range []string{f.a, f.b} {
		if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
			t.Fatal(err)
		}
		testGit(t, filepath.Dir(clone), "clone", "--quiet", f.remote, clone)
		if err := os.WriteFile(filepath.Join(clone, ".git", "objects", "info", "alternates"), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if res := f.ensure(t, f.opts()); res.Outcome != OutcomeRestored || len(res.Unrecoverable) != 0 {
		t.Fatalf("building the mirror: %+v", res)
	}

	f.topic = pushFile(t, f.remote, "topic", f.topicFile)
	if res := f.ensure(t, f.refreshOpts()); res.Outcome != OutcomeHealthy || res.Err != nil {
		t.Fatalf("refreshing topic into the mirror: %+v", res)
	}
	for _, clone := range []string{f.a, f.b} {
		testGit(t, clone, "fetch", "--quiet", "origin")
		testGit(t, clone, "repack", "-a", "-d", "-l", "-q")
		if packs, _ := filepath.Glob(filepath.Join(clone, ".git", "objects", "pack", "*.pack")); len(packs) != 0 {
			if out := testGit(t, clone, "count-objects", "-v"); !strings.Contains(out, "in-pack: 0") {
				t.Fatalf("borrower %s still holds the mirror's objects:\n%s", clone, out)
			}
		}
	}

	// The unpushed commit, made without checking the branch out.
	f.ownBlob = testGitStdin(t, f.a, "mine\n", "hash-object", "-w", "--stdin")
	entries := testGit(t, f.a, "ls-tree", "-z", f.topic)
	tree := testGitStdin(t, f.a, entries+"100644 blob "+f.ownBlob+"\tmine.txt\x00", "mktree", "-z")
	f.own = testGit(t, f.a, "commit-tree", tree, "-p", f.topic, "-m", "mine")
	testGit(t, f.a, "update-ref", f.keepRef, f.own)
	return f
}

// dropTopic has the remote delete topic and every trace of it. With hidden
// set, it keeps the commit under refs/pull/1/head, which upload-pack hides,
// so the remote serves it by id only.
func (f lostFixture) dropTopic(t *testing.T, remote string, hidden bool) {
	t.Helper()
	if hidden {
		testGit(t, remote, "update-ref", "refs/pull/1/head", f.topic)
		testGit(t, remote, "config", "uploadpack.hideRefs", "refs/pull/")
	}
	testGit(t, remote, "update-ref", "-d", "refs/heads/topic")
	testGit(t, remote, "reflog", "expire", "--expire=now", "--all")
	testGit(t, remote, "gc", "--quiet", "--prune=now")
	_, err := gitTry(remote, "cat-file", "-e", f.topic)
	if hidden && err != nil {
		t.Fatalf("the remote lost the hidden commit: %v", err)
	}
	if !hidden && err == nil {
		t.Fatal("the remote still holds topic's commit; the case proves nothing")
	}
}

// deleteMirror removes the stable link and the store it names.
func (f lostFixture) deleteMirror(t *testing.T) {
	t.Helper()
	store := f.store(t)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(store); err != nil {
		t.Fatal(err)
	}
}

// borrowerSnap maps each borrower to every file under it, .git included.
func (f lostFixture) borrowerSnap(t *testing.T) map[string]map[string]string {
	t.Helper()
	return map[string]map[string]string{f.a: treeFiles(t, f.a), f.b: treeFiles(t, f.b)}
}

func (f lostFixture) requireBorrowersUnchanged(t *testing.T, before map[string]map[string]string) {
	t.Helper()
	for clone, files := range f.borrowerSnap(t) {
		was := before[clone]
		if !slices.Equal(slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(was))) {
			t.Fatalf("the files under %s changed:\nnow %v\nwas %v", clone, slices.Sorted(maps.Keys(files)), slices.Sorted(maps.Keys(was)))
		}
		for name, body := range was {
			if files[name] != body {
				t.Fatalf("%s in %s changed", name, clone)
			}
		}
	}
}

// requireReads fails unless every ref of clone reads to the root and fsck
// is clean.
func requireReads(t *testing.T, clone string) {
	t.Helper()
	if out, err := gitTry(clone, "rev-list", "--objects", "--all"); err != nil {
		t.Fatalf("rev-list --objects --all in %s: %v\n%s", clone, err, out)
	}
	if out, err := gitTry(clone, "fsck", "--full", "--no-dangling"); err != nil {
		t.Fatalf("fsck --full in %s: %v\n%s", clone, err, out)
	}
}

// setFetchHook replaces the fetch by id until the test ends.
func setFetchHook(t *testing.T, hook func(ctx context.Context, store, url string, ids []string) error) {
	t.Helper()
	fetchCommits = hook
	t.Cleanup(func() { fetchCommits = fetchByID })
}

// setLimits shortens the recovery's time limits until the test ends.
func setLimits(t *testing.T, perFetch, total time.Duration) {
	t.Helper()
	oldFetch, oldTotal := recoverFetchLimit, recoverTotalLimit
	recoverFetchLimit, recoverTotalLimit = perFetch, total
	t.Cleanup(func() { recoverFetchLimit, recoverTotalLimit = oldFetch, oldTotal })
}

func TestRecover_ABranchOnDeletedCommitsSurvivesWhileTheRemoteServesThem(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, true)
	f.deleteMirror(t)
	before := f.borrowerSnap(t)

	res := f.ensure(t, f.opts())
	if res.Fault != FaultMissing || res.Outcome != OutcomeRestored || res.Err != nil || len(res.Unrecoverable) != 0 {
		t.Fatalf("result = %+v, want missing/restored with nothing unrecoverable", res)
	}
	if got := keptRefs(t, f.fixture); !slices.Equal(got, []string{keptRefPrefix + f.topic + " " + f.topic}) {
		t.Fatalf("kept refs = %v, want only %s", got, keptRefPrefix+f.topic)
	}
	if got := f.storeRef(t, "refs/heads/topic"); got != "" {
		t.Fatalf("refs/heads/topic reached the new store at %s", got)
	}
	for _, clone := range []string{f.a, f.b} {
		requireReads(t, clone)
	}
	requireManifestMatches(t, f.fixture)
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if st.LastRefresh == "" || st.LastRefresh != st.LastDeepVerify {
		t.Fatalf("last_refresh %q, last_deep_verify %q: want both the build's", st.LastRefresh, st.LastDeepVerify)
	}
	f.requireBorrowersUnchanged(t, before)
}

func TestRecover_ABranchOnDestroyedCommitsIsReportedUnrecoverable(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, false)
	f.deleteMirror(t)
	before := f.borrowerSnap(t)
	logged := len(f.logLines(t))

	res := f.ensure(t, f.opts())
	if res.Fault != FaultMissing || res.Outcome != OutcomeRestored || res.Err != nil || !res.Usable() {
		t.Fatalf("result = %+v, want missing/restored", res)
	}
	want := []LostRef{
		{Instance: f.a, Repo: "app", Ref: "refs/heads/keep", MissingCommit: f.topic,
			OwnCommits: []string{f.own}, LostFiles: []LostFile{{Path: "added-topic.txt"}}},
		{Instance: f.a, Repo: "app", Ref: "refs/remotes/origin/topic", MissingCommit: f.topic, RemoteTracking: true},
		{Instance: f.b, Repo: "app", Ref: "refs/remotes/origin/topic", MissingCommit: f.topic, RemoteTracking: true},
	}
	if len(res.Unrecoverable) != len(want) {
		t.Fatalf("unrecoverable = %+v, want %+v", res.Unrecoverable, want)
	}
	for i, w := range want {
		got := res.Unrecoverable[i]
		if got.Instance != w.Instance || got.Repo != w.Repo || got.Ref != w.Ref || got.MissingCommit != w.MissingCommit ||
			got.RemoteTracking != w.RemoteTracking || !slices.Equal(got.OwnCommits, w.OwnCommits) || !slices.Equal(got.LostFiles, w.LostFiles) {
			t.Fatalf("unrecoverable[%d] = %+v, want %+v", i, got, w)
		}
	}
	if got := keptRefs(t, f.fixture); len(got) != 0 {
		t.Fatalf("kept refs = %v, want none", got)
	}
	for _, clone := range []string{f.a, f.b} {
		for _, ref := range []string{"refs/heads/main", "refs/remotes/origin/main", "HEAD"} {
			if out, err := gitTry(clone, "rev-list", "--objects", ref); err != nil {
				t.Fatalf("%s in %s doesn't read to the root: %v\n%s", ref, clone, err, out)
			}
		}
	}
	for _, id := range []string{f.own, f.ownBlob} {
		if _, err := gitTry(f.a, "cat-file", "-e", id); err != nil {
			t.Fatalf("%s no longer reads in borrower a: %v", id, err)
		}
	}
	f.requireBorrowersUnchanged(t, before)

	lines := newLines(t, f.fixture, logged)
	requireEvents(t, lines,
		[3]string{"check", "missing", "restored"},
		[3]string{"restore", "missing", "restored"},
		[3]string{"unrecoverable", "missing", "restored"},
		[3]string{"unrecoverable", "missing", "restored"},
		[3]string{"unrecoverable", "missing", "restored"})
	newKeys := []string{"instance", "ref", "missing_commit", "remote_tracking", "own_commits", "lost_files"}
	for _, l := range lines[:2] {
		for _, k := range newKeys {
			if _, ok := l[k]; ok {
				t.Fatalf("%s line %v carries %s", l["event"], l, k)
			}
		}
	}
	for i, l := range lines[2:] {
		w := res.Unrecoverable[i]
		own, _ := l["own_commits"].([]any)
		files, _ := l["lost_files"].([]any)
		if l["instance"] != w.Instance || l["ref"] != w.Ref || l["missing_commit"] != w.MissingCommit ||
			l["remote_tracking"] != w.RemoteTracking || len(own) != len(w.OwnCommits) || len(files) != len(w.LostFiles) {
			t.Fatalf("unrecoverable line %v doesn't match %+v", l, w)
		}
		if len(own) == 1 && own[0] != w.OwnCommits[0] {
			t.Fatalf("own_commits %v, want %v", own, w.OwnCommits)
		}
		if len(files) == 1 {
			file, _ := files[0].(map[string]any)
			if file["path"] != w.LostFiles[0].Path || file["in_working_tree"] != w.LostFiles[0].InWorkingTree {
				t.Fatalf("lost_files %v, want %+v", files, w.LostFiles)
			}
		}
	}

	// The next command finds the mirror healthy and doesn't report again.
	target := f.linkTarget(t)
	logged = len(f.logLines(t))
	again := f.ensure(t, f.opts())
	if again.Outcome != OutcomeHealthy || len(again.Unrecoverable) != 0 {
		t.Fatalf("second result = %+v, want healthy", again)
	}
	requireEvents(t, newLines(t, f.fixture, logged), [3]string{"check", "", "healthy"})
	if f.linkTarget(t) != target {
		t.Fatal("the second call moved the link")
	}
	f.requireBorrowersUnchanged(t, before)
}

func TestRecover_TheFetchRunsUnderTheLockAfterTheSwap(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, true)
	f.deleteMirror(t)

	calls := 0
	setFetchHook(t, func(ctx context.Context, store, url string, ids []string) error {
		calls++
		target, err := os.Readlink(StablePath(f.home, f.root, "app"))
		if err != nil {
			t.Errorf("the hook ran with no link: %v", err)
		}
		st, err := readState(f.mdir(), "app")
		if err != nil || st.Store != target {
			t.Errorf("the hook ran before the state file recorded %s (%+v, %v)", target, st, err)
		}
		release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "test")
		if err == nil {
			release()
			t.Error("the mirror's lock was free while the fetch ran")
		} else if !errors.Is(err, filelock.ErrLockHeld) {
			t.Errorf("TryFileLock = %v, want ErrLockHeld", err)
		}
		return fetchByID(ctx, store, url, ids)
	})
	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || len(res.Unrecoverable) != 0 || calls == 0 {
		t.Fatalf("result = %+v after %d fetches, want restored with the commit fetched", res, calls)
	}
}

// requireHeld reports, with t.Errorf since it runs inside the probe and fetch
// hooks, unless store is still on disk and the store record lists it as
// linked and not retired. when names the hook in the report.
func requireHeld(t *testing.T, f fixture, store, when string) {
	t.Helper()
	name := filepath.Base(store)
	if _, err := os.Lstat(filepath.Join(f.mdir(), ".store", name, "objects")); err != nil {
		t.Errorf("%s: the replaced store %s is gone (%v)", when, name, err)
	}
	rec, ok := readStores(f.mdir(), "app")
	if !ok {
		t.Errorf("%s: there's no readable store record", when)
		return
	}
	i := rec.index(name)
	if i < 0 {
		t.Errorf("%s: the store record lists %+v, not %s", when, rec.Stores, name)
		return
	}
	if e := rec.Stores[i]; !e.Linked || e.RetiredAt != "" {
		t.Errorf("%s: the record's entry for %s = %+v, want linked and not yet retired", when, name, e)
	}
}

// The store a restore replaces stays on disk, recorded as linked and not
// retired, until the probe of the borrowers and the recovery by id are over;
// only then is it retired, and only a later sweep removes it. The order isn't
// visible from the outside: the probe reads the new store and the fetch the
// remote, and a restore's store lines are logged after its restore line
// whenever the store went. So the hooks look at the old store while the
// probe and the fetch run.
func TestRecover_TheReplacedStoreOutlivesTheProbeAndRecovery(t *testing.T) {
	t.Run("rebuild", func(t *testing.T) {
		f := newLostFixture(t, lostSetup{})
		old := f.linkTarget(t)
		damageHEAD(t, f.fixture)
		probes := 0
		setProbeHook(t, func(string) {
			probes++
			if target, err := os.Readlink(StablePath(f.home, f.root, "app")); err != nil || target == old {
				t.Errorf("the probe ran before the swap: link %q (%v), old store %s", target, err, old)
			}
			requireHeld(t, f.fixture, old, "as the probe started")
		})
		res := f.ensure(t, f.opts())
		if res.Outcome != OutcomeRestored || res.Err != nil || len(res.Unrecoverable) != 0 || probes != 1 {
			t.Fatalf("result = %+v after %d probes, want restored with one probe and nothing lost", res, probes)
		}
		requireKeptRetired(t, f.fixture, old)
	})

	t.Run("rebuild whose probe finds a lost ref", func(t *testing.T) {
		f := newLostFixture(t, lostSetup{})
		old := f.linkTarget(t)
		damageHEAD(t, f.fixture)
		missing := randomID(t)
		writeRef(t, f.b, "refs/heads/lost", missing)
		probes, fetches := 0, 0
		setProbeHook(t, func(string) {
			probes++
			requireHeld(t, f.fixture, old, "as the probe started")
		})
		setFetchHook(t, func(_ context.Context, _, _ string, ids []string) error {
			fetches++
			if !slices.Contains(ids, missing) {
				t.Errorf("the fetch asked for %v, not the lost ref's %s", ids, missing)
			}
			requireHeld(t, f.fixture, old, "while the recovery fetched")
			return nil
		})
		res := f.ensure(t, f.opts())
		if res.Outcome != OutcomeRestored || res.Err != nil || probes != 1 || fetches == 0 {
			t.Fatalf("result = %+v after %d probes and %d fetches, want restored with one probe and a fetch", res, probes, fetches)
		}
		if len(res.Unrecoverable) != 1 || res.Unrecoverable[0].Instance != f.b ||
			res.Unrecoverable[0].Ref != "refs/heads/lost" || res.Unrecoverable[0].MissingCommit != missing {
			t.Fatalf("unrecoverable = %+v, want refs/heads/lost in %s on %s", res.Unrecoverable, f.b, missing)
		}
		requireKeptRetired(t, f.fixture, old)
	})

	t.Run("relink", func(t *testing.T) {
		// A restore whose last record write failed leaves the store it
		// replaced linked and not retired beside the new one; a relink of the
		// new one retires the old one, after its probe.
		f := newLostFixture(t, lostSetup{})
		old := f.linkTarget(t)
		damageHEAD(t, f.fixture)
		if res := f.ensure(t, f.opts()); res.Outcome != OutcomeRestored || res.Err != nil {
			t.Fatalf("first restore = %+v", res)
		}
		current := f.linkTarget(t)
		rec := recordOf(t, f.fixture)
		rec.Stores[rec.index(filepath.Base(old))].RetiredAt = ""
		writeRecord(t, f.fixture, rec)
		if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
			t.Fatal(err)
		}

		probes := 0
		setProbeHook(t, func(string) {
			probes++
			requireHeld(t, f.fixture, old, "as the relink's probe started")
		})
		res := f.ensure(t, f.opts())
		if res.Outcome != OutcomeRestored || res.Err != nil || len(res.Unrecoverable) != 0 || probes != 1 {
			t.Fatalf("result = %+v after %d probes, want a relink with one probe and nothing lost", res, probes)
		}
		if got := f.linkTarget(t); got != current {
			t.Fatalf("link = %q, want a relink to %q", got, current)
		}
		requireKeptRetired(t, f.fixture, old)
	})
}

func TestRecover_ARestoreThatFailsBeforeTheSwapProbesNothing(t *testing.T) {
	for _, stage := range []string{stageClone, stageState, stageStore, stageLink, stageSwap} {
		t.Run(stage, func(t *testing.T) {
			f := newLostFixture(t, lostSetup{})
			f.dropTopic(t, f.remote, true)
			f.deleteMirror(t)
			probes, fetches := 0, 0
			probeHook = func(string) { probes++ }
			t.Cleanup(func() { probeHook = nil })
			setFetchHook(t, func(context.Context, string, string, []string) error {
				fetches++
				return nil
			})
			setStageHook(t, stage)
			res := f.ensure(t, f.opts())
			if res.Outcome != OutcomeRestoreFailed || len(res.Unrecoverable) != 0 {
				t.Fatalf("result = %+v, want restore-failed", res)
			}
			if probes != 0 || fetches != 0 {
				t.Fatalf("a restore failed at %s probed %d times and fetched %d times", stage, probes, fetches)
			}
		})
	}
}

// randomID is a full-length commit id no repository here holds.
func randomID(t *testing.T) string {
	t.Helper()
	var b [20]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

// writeRef writes a loose ref straight into a clone, so it can name an
// object the clone doesn't hold, which update-ref refuses.
func writeRef(t *testing.T, clone, ref, id string) {
	t.Helper()
	path := filepath.Join(clone, ".git", filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRecover_FetchesAtMost32IDsInTwoRounds(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.deleteMirror(t)
	var missing []string
	for i := range 40 {
		id := randomID(t)
		missing = append(missing, id)
		writeRef(t, f.b, fmt.Sprintf("refs/heads/lost-%02d", i), id)
	}
	setLimits(t, time.Second, 5*time.Second)
	var calls [][]string
	setFetchHook(t, func(_ context.Context, _, _ string, ids []string) error {
		calls = append(calls, slices.Clone(ids))
		return nil
	})

	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("result = %+v, want restored", res)
	}
	seen := map[string]bool{}
	for _, ids := range calls {
		for _, id := range ids {
			seen[id] = true
		}
	}
	if len(seen) > 32 || len(calls) > 2 || len(calls) == 0 {
		t.Fatalf("%d fetches asked for %d distinct ids, want at most 2 fetches and 32 ids", len(calls), len(seen))
	}
	lost := map[string]bool{}
	for _, l := range res.Unrecoverable {
		if l.Instance == f.b && strings.HasPrefix(l.Ref, "refs/heads/lost-") {
			lost[l.MissingCommit] = true
		}
	}
	for _, id := range missing {
		if !lost[id] {
			t.Fatalf("the ref on %s wasn't fetched and isn't reported", id)
		}
	}
}

func TestRecover_AFailedBatchIsRetriedOneIDAtATime(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	// Three commits the remote keeps under hidden refs, and two it never had.
	var served []string
	for i := range 3 {
		id := pushFile(t, f.remote, fmt.Sprintf("pr-%d", i), fmt.Sprintf("pr-%d.txt", i))
		testGit(t, f.remote, "update-ref", fmt.Sprintf("refs/pull/%d/head", i+1), id)
		testGit(t, f.remote, "update-ref", "-d", fmt.Sprintf("refs/heads/pr-%d", i))
		served = append(served, id)
	}
	testGit(t, f.remote, "config", "uploadpack.hideRefs", "refs/pull/")
	never := []string{randomID(t), randomID(t)}
	for i, id := range append(slices.Clone(served), never...) {
		writeRef(t, f.b, fmt.Sprintf("refs/heads/pr-%d", i), id)
	}
	f.deleteMirror(t)

	var calls [][]string
	setFetchHook(t, func(ctx context.Context, store, url string, ids []string) error {
		calls = append(calls, slices.Clone(ids))
		if len(ids) > 1 {
			return errors.New("injected batch failure")
		}
		return fetchByID(ctx, store, url, ids)
	})
	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("result = %+v, want restored", res)
	}
	singles := 0
	for _, ids := range calls {
		if len(ids) == 1 {
			singles++
		}
	}
	if singles == 0 {
		t.Fatalf("the failed batch wasn't retried one id at a time: %v", calls)
	}
	kept := keptRefs(t, f.fixture)
	for _, id := range served {
		if !slices.Contains(kept, keptRefPrefix+id+" "+id) {
			t.Fatalf("served commit %s isn't pinned: %v", id, kept)
		}
	}
	var reported []string
	for _, l := range res.Unrecoverable {
		if l.Instance == f.b && strings.HasPrefix(l.Ref, "refs/heads/pr-") {
			reported = append(reported, l.MissingCommit)
		}
	}
	slices.Sort(reported)
	wantReported := slices.Sorted(slices.Values(never))
	if !slices.Equal(reported, wantReported) {
		t.Fatalf("reported %v, want only the commits the remote never had %v", reported, wantReported)
	}
}

func TestRecover_AFetchThatBlocksIsCancelledAtItsLimit(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	for i := range 4 {
		writeRef(t, f.b, fmt.Sprintf("refs/heads/slow-%d", i), randomID(t))
	}
	f.deleteMirror(t)
	perFetch, total := 400*time.Millisecond, time.Second
	setLimits(t, perFetch, total)

	var mu sync.Mutex
	var first time.Time
	setFetchHook(t, func(ctx context.Context, _, _ string, _ []string) error {
		start := time.Now()
		mu.Lock()
		if first.IsZero() {
			first = start
		}
		mu.Unlock()
		<-ctx.Done()
		if took := time.Since(start); took > perFetch+500*time.Millisecond {
			t.Errorf("a fetch ran %v, past its limit %v", took, perFetch)
		}
		return ctx.Err()
	})
	res := f.ensure(t, f.opts())
	done := time.Now()
	if res.Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	if first.IsZero() {
		t.Fatal("the hook was never called")
	}
	if took := done.Sub(first); took > total+time.Second {
		t.Fatalf("the recovery took %v after its first fetch, past %v and a second", took, total)
	}
}

func TestFetchByID_TheCommandLine(t *testing.T) {
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, true)
	f.deleteMirror(t)

	shim := t.TempDir()
	argv := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$NIWA_TEST_ARGV\"; done\n" +
		"printf '%s\\n' '--end--' >> \"$NIWA_TEST_ARGV\"\nexec " + realGit + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(shim, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIWA_TEST_ARGV", argv)
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))

	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || len(res.Unrecoverable) != 0 {
		t.Fatalf("result = %+v, want restored with nothing lost", res)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	fetches := 0
	for _, call := range strings.Split(string(data), "--end--\n") {
		args := strings.Split(strings.TrimSuffix(call, "\n"), "\n")
		if !slices.Contains(args, "--no-write-fetch-head") || !slices.Contains(args, "--no-tags") {
			continue
		}
		fetches++
		if !slices.Contains(args, "protocol.version=2") {
			t.Fatalf("the fetch by id doesn't set protocol.version=2: %q", args)
		}
		dash := slices.Index(args, "--")
		if dash < 0 || dash+2 >= len(args) || args[dash+1] != "file://"+f.remote {
			t.Fatalf("the fetch by id doesn't give the config's URL after --: %q", args)
		}
		for _, id := range args[dash+2:] {
			if !commitIDPattern.MatchString(id) {
				t.Fatalf("the fetch by id passed %q after the URL", id)
			}
		}
	}
	if fetches == 0 {
		t.Fatalf("no fetch by id ran:\n%s", data)
	}

	store := f.store(t)
	if _, err := os.Lstat(filepath.Join(store, "FETCH_HEAD")); !os.IsNotExist(err) {
		t.Fatalf("the recovery wrote FETCH_HEAD (%v)", err)
	}
	refs := strings.Split(testGit(t, store, "--git-dir", store, "for-each-ref", "--format=%(refname)"), "\n")
	if want := []string{"refs/heads/main", keptRefPrefix + f.topic}; !slices.Equal(refs, want) {
		t.Fatalf("store refs = %v, want %v", refs, want)
	}
	for rel, body := range treeFiles(t, filepath.Join(store, "logs")) {
		if strings.Contains(body, f.remote) {
			t.Fatalf("logs/%s in the store names the remote's URL", rel)
		}
	}

	for _, bad := range []string{"--upload-pack=touch /tmp/x", "abc", strings.Repeat("g", 40)} {
		if err := fetchByID(context.Background(), store, "file://"+f.remote, []string{bad}); err == nil {
			t.Fatalf("fetchByID accepted %q", bad)
		}
	}
}

func TestRecover_ARelinkWithEveryRefReadableFetchesNothing(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	target := f.linkTarget(t)
	state := stateBytes(t, f.fixture)
	if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatal(err)
	}
	fetches := 0
	setFetchHook(t, func(context.Context, string, string, []string) error {
		fetches++
		return nil
	})
	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || f.linkTarget(t) != target || len(res.Unrecoverable) != 0 {
		t.Fatalf("result = %+v, link %q; want a relink to %q", res, f.linkTarget(t), target)
	}
	if fetches != 0 {
		t.Fatalf("the relink fetched %d times", fetches)
	}
	if got := keptRefs(t, f.fixture); len(got) != 0 {
		t.Fatalf("kept refs = %v, want none", got)
	}
	if got := stateBytes(t, f.fixture); string(got) != string(state) {
		t.Fatalf("the state file changed:\n%s\nwas\n%s", got, state)
	}
}

func TestRecover_AURLChangedRestoreProbesTheNewRemote(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(fmt.Sprintf("hidden=%v", hidden), func(t *testing.T) {
			f := newLostFixture(t, lostSetup{})
			second := filepath.Join(t.TempDir(), "second.git")
			testGit(t, filepath.Dir(second), "clone", "--quiet", "--bare", f.remote, second)
			f.dropTopic(t, second, hidden)

			r := Repo{ID: "app", URL: "file://" + second}
			res := EnsureHealthy(context.Background(), f.opts(), []Repo{r})
			if len(res) != 1 || res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored {
				t.Fatalf("result = %+v, want url-changed/restored", res)
			}
			var keep bool
			for _, l := range res[0].Unrecoverable {
				if l.Instance == f.a && l.Ref == "refs/heads/keep" && l.MissingCommit == f.topic {
					keep = true
				}
			}
			if hidden {
				if len(res[0].Unrecoverable) != 0 {
					t.Fatalf("unrecoverable = %+v, want none", res[0].Unrecoverable)
				}
				if got := keptRefs(t, f.fixture); !slices.Equal(got, []string{keptRefPrefix + f.topic + " " + f.topic}) {
					t.Fatalf("kept refs = %v, want only %s", got, keptRefPrefix+f.topic)
				}
				requireReads(t, f.a)
			} else if !keep {
				t.Fatalf("unrecoverable = %+v, want keep in a on %s", res[0].Unrecoverable, f.topic)
			}
		})
	}
}

func TestRecover_ADeepVerifyRebuildProbesUnderTheRefreshLock(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	f.dropTopic(t, f.remote, false)

	// The pack the refresh fetched topic in holds it alone; rot it so only
	// the deep verify sees it.
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	var topicPack string
	for _, p := range st.Packs {
		out := testGit(t, f.mdir(), "verify-pack", "-v", idxOf(filepath.Join(f.store(t), "objects", "pack", p.Name)))
		if strings.Contains(out, f.topic) {
			topicPack = filepath.Join(f.store(t), "objects", "pack", p.Name)
		}
	}
	if topicPack == "" {
		t.Fatal("no pack of the store holds topic's commit")
	}
	info, err := os.Stat(topicPack)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	makeWritable(t, topicPack)
	fh, err := os.OpenFile(topicPack, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteAt([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}, (info.Size()-20)/2); err != nil {
		t.Fatal(err)
	}
	if err := fh.Close(); err != nil {
		t.Fatal(err)
	}
	now, err := describePack(topicPack)
	if err != nil {
		t.Fatal(err)
	}
	editState(t, f.fixture, func(st *state) {
		for i := range st.Packs {
			if st.Packs[i].Name == filepath.Base(topicPack) {
				st.Packs[i].CtimeNs = now.CtimeNs
			}
		}
		st.LastDeepVerify = stateTime(time.Now().Add(-25 * time.Hour))
	})

	locked := false
	setFetchHook(t, func(ctx context.Context, store, url string, ids []string) error {
		release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "test")
		if err == nil {
			release()
		} else {
			locked = errors.Is(err, filelock.ErrLockHeld)
		}
		return fetchByID(ctx, store, url, ids)
	})
	logged := len(f.logLines(t))
	res := f.ensure(t, f.refreshOpts())
	if res.Fault != FaultPackChanged || res.Outcome != OutcomeRestored || res.Err != nil {
		t.Fatalf("result = %+v, want pack-changed/restored", res)
	}
	if !locked {
		t.Fatal("the rebuild's probe didn't run under the refresh's lock")
	}
	var keep bool
	for _, l := range res.Unrecoverable {
		if l.Instance == f.a && l.Ref == "refs/heads/keep" && l.MissingCommit == f.topic {
			keep = true
		}
	}
	if !keep {
		t.Fatalf("unrecoverable = %+v, want keep in a", res.Unrecoverable)
	}
	lines := newLines(t, f.fixture, logged)
	if len(lines) < 3 || lines[0]["event"] != "check" || lines[1]["event"] != "restore" || lines[2]["event"] != "unrecoverable" {
		t.Fatalf("log gained %v, want check, restore, then unrecoverable lines", lines)
	}
	for _, l := range lines[1:] {
		if l["fault"] != string(FaultPackChanged) || l["result"] != string(OutcomeRestored) {
			t.Fatalf("line %v, want pack-changed/restored", l)
		}
	}
}

func TestRecover_HostileNamesAreCutToPrintableASCII(t *testing.T) {
	long := "refs/heads/" + strings.Repeat("abcdefghi/", 28) + "tail-of-a-long-branch-name"
	if len(long) < 300 {
		t.Fatalf("the long ref is %d bytes, want 300 or more", len(long))
	}
	f := newLostFixture(t, lostSetup{topicFile: "esc-\x1b[31m-\nnewline.txt", keepRef: long})
	f.dropTopic(t, f.remote, false)
	f.deleteMirror(t)
	logged := len(f.logLines(t))

	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	printable := func(s string) bool {
		for i := 0; i < len(s); i++ {
			if s[i] < 0x20 || s[i] >= 0x7f {
				return false
			}
		}
		return len(s) <= nameMax
	}
	var branch *LostRef
	for i, l := range res.Unrecoverable {
		if !printable(l.Ref) || !printable(l.Instance) {
			t.Fatalf("lost ref names aren't cut: %+v", l)
		}
		for _, file := range l.LostFiles {
			if !printable(file.Path) {
				t.Fatalf("lost file %q isn't cut", file.Path)
			}
		}
		if !l.RemoteTracking {
			branch = &res.Unrecoverable[i]
		}
	}
	if branch == nil || len(branch.LostFiles) != 1 || branch.Ref != long[:min(len(long), nameMax)] {
		t.Fatalf("the long branch = %+v, want its ref cut to %d bytes with one lost file", branch, nameMax)
	}
	if want := `esc-\x1b[31m-\x0anewline.txt`; branch.LostFiles[0].Path != want {
		t.Fatalf("lost file = %q, want %q", branch.LostFiles[0].Path, want)
	}
	data, err := os.ReadFile(logPath(f.mdir()))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if got := len(lines) - logged; got != 2+len(res.Unrecoverable) {
		t.Fatalf("the log gained %d lines, want %d", got, 2+len(res.Unrecoverable))
	}
	if strings.ContainsRune(string(data), 0x1b) {
		t.Fatal("the log holds a raw ESC byte")
	}
	f.logLines(t) // each line is one JSON object
}

// TestRecover_OwnCommitsAndLostFilesAreCappedAt100: a branch "many" in
// borrower a holds 150 commits of its own on a pushed commit "wide" that
// added 150 files; the remote then destroys wide. Its LostRef lists the first
// 100 commits the walk read, tip first, and 100 lost files, and its
// unrecoverable line carries the same 100 commits.
func TestRecover_OwnCommitsAndLostFilesAreCappedAt100(t *testing.T) {
	f := newLostFixture(t, lostSetup{})
	work := filepath.Join(t.TempDir(), "wide")
	testGit(t, filepath.Dir(work), "clone", "--quiet", f.remote, work)
	testGit(t, work, "checkout", "--quiet", "-B", "wide", "origin/main")
	for i := 0; i < 150; i++ {
		if err := os.WriteFile(filepath.Join(work, fmt.Sprintf("wide-%03d.txt", i)), []byte(fmt.Sprintf("wide %d\n", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	testGit(t, work, "add", ".")
	testGit(t, work, "commit", "--quiet", "-m", "wide")
	testGit(t, work, "push", "--quiet", "origin", "HEAD:refs/heads/wide")
	wide := testGit(t, work, "rev-parse", "HEAD")
	if res := f.ensure(t, f.refreshOpts()); res.Outcome != OutcomeHealthy || res.Err != nil {
		t.Fatalf("refreshing wide into the mirror: %+v", res)
	}
	testGit(t, f.a, "fetch", "--quiet", "origin")
	testGit(t, f.a, "repack", "-a", "-d", "-l", "-q")

	// 150 commits of the borrower's own on wide, each on the one before,
	// sharing one tree the borrower holds: wide's files, whose blobs only the
	// mirror holds, plus a file of its own.
	blob := testGitStdin(t, f.a, "mine\n", "hash-object", "-w", "--stdin")
	entries := testGit(t, f.a, "ls-tree", "-z", wide)
	tree := testGitStdin(t, f.a, entries+"100644 blob "+blob+"\tmine.txt\x00", "mktree", "-z")
	parent := wide
	var own []string
	for i := 0; i < 150; i++ {
		parent = testGit(t, f.a, "commit-tree", tree, "-p", parent, "-m", fmt.Sprintf("own %d", i))
		own = append(own, parent)
	}
	testGit(t, f.a, "update-ref", "refs/heads/many", parent)

	testGit(t, f.remote, "update-ref", "-d", "refs/heads/wide")
	f.dropTopic(t, f.remote, false)
	if _, err := gitTry(f.remote, "cat-file", "-e", wide); err == nil {
		t.Fatal("the remote still holds wide; the case proves nothing")
	}
	f.deleteMirror(t)
	logged := len(f.logLines(t))

	res := f.ensure(t, f.opts())
	if res.Outcome != OutcomeRestored || res.LogErr != nil {
		t.Fatalf("result = %+v, want restored", res)
	}
	var many *LostRef
	for i := range res.Unrecoverable {
		if res.Unrecoverable[i].Ref == "refs/heads/many" {
			many = &res.Unrecoverable[i]
		}
	}
	if many == nil || many.MissingCommit != wide {
		t.Fatalf("unrecoverable = %+v, want refs/heads/many missing %s", res.Unrecoverable, wide)
	}
	walked := slices.Clone(own)
	slices.Reverse(walked)
	if !slices.Equal(many.OwnCommits, walked[:100]) {
		t.Fatalf("own commits = %d ids, want the first 100 the walk read, tip first", len(many.OwnCommits))
	}
	if len(many.LostFiles) != 100 {
		t.Fatalf("lost files = %d, want 100", len(many.LostFiles))
	}

	var line map[string]any
	for _, l := range newLines(t, f.fixture, logged) {
		if l["event"] == "unrecoverable" && l["ref"] == "refs/heads/many" {
			line = l
		}
	}
	if line == nil {
		t.Fatal("no unrecoverable line for refs/heads/many")
	}
	logged100, _ := line["own_commits"].([]any)
	if len(logged100) != 100 {
		t.Fatalf("the line lists %d own commits, want 100", len(logged100))
	}
	for i, id := range logged100 {
		if id != many.OwnCommits[i] {
			t.Fatalf("own_commits[%d] = %v, want %s", i, id, many.OwnCommits[i])
		}
	}
	if files, _ := line["lost_files"].([]any); len(files) != 100 {
		t.Fatalf("the line lists %d lost files, want 100", len(files))
	}
}

func TestCleanName(t *testing.T) {
	cases := map[string]string{
		"refs/heads/plain":       "refs/heads/plain",
		"a\x1bb\nc":              `a\x1bb\x0ac`,
		`back\slash`:             `back\x5cslash`,
		"caf\xc3\xa9":            `caf\xc3\xa9`,
		strings.Repeat("x", 300): strings.Repeat("x", nameMax),
	}
	for in, want := range cases {
		if got := cleanName(in); got != want {
			t.Errorf("cleanName(%q) = %q, want %q", in, got, want)
		}
	}
	// An escape that would cross the cap is left out whole.
	if got := cleanName(strings.Repeat("x", nameMax-2) + "\x1b"); got != strings.Repeat("x", nameMax-2) {
		t.Errorf("cleanName cut an escape in half: %q", got)
	}
}

func TestResultNotice_CountsUnrecoverableRefs(t *testing.T) {
	stable := "/h/.niwa/w-0123/mirrors/app"
	log := "/h/.niwa/w-0123/mirrors/.meta/mirror.log"
	lost := []LostRef{
		{Instance: "/ws/a/tools/app", Repo: "app", Ref: "\x1b[31mx\nforged", MissingCommit: strings.Repeat("a", 40)},
		{Instance: "/ws/a/tools/app", Repo: "app", Ref: "refs/remotes/origin/topic", MissingCommit: strings.Repeat("a", 40), RemoteTracking: true},
		{Instance: "/ws/b/tools/app", Repo: "app", Ref: "refs/remotes/origin/topic", MissingCommit: strings.Repeat("a", 40), RemoteTracking: true,
			LostFiles: []LostFile{{Path: "secret-name.txt"}}},
	}
	res := Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored, Unrecoverable: lost}
	want := "niwa mirror app: fault missing, result restored. It was rebuilt from the remote, and the clones that borrow from it read normally." +
		" Except 3 refs in those clones, which are unrecoverable: they were built on commits the remote no longer serves. The mirror log names them." +
		" Mirror log: " + log
	if got := res.Notice(); got != want {
		t.Fatalf("Notice() =\n%q\nwant\n%q", got, want)
	}
	for _, name := range []string{"forged", "/ws/", "origin/topic", strings.Repeat("a", 40), "secret-name"} {
		if strings.Contains(res.Notice(), name) {
			t.Fatalf("Notice() names %q", name)
		}
	}
	plain := Result{RepoID: "app", StablePath: stable, Fault: FaultMissing, Outcome: OutcomeRestored}
	if got, want := plain.Notice(), "niwa mirror app: fault missing, result restored. It was rebuilt from the remote, and the clones that borrow from it read normally. Mirror log: "+log; got != want {
		t.Fatalf("Notice() with none lost =\n%q\nwant\n%q", got, want)
	}
	if got := (Result{RepoID: "app", StablePath: stable, Outcome: OutcomeHealthy}).Notice(); got != "" {
		t.Fatalf("healthy Notice() = %q, want empty", got)
	}
}
