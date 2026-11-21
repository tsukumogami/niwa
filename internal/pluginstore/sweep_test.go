package pluginstore

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// Commits whose hash order runs against the commit times the tests give
// them, so a newest-by-hash mistake shows.
const (
	sweepCommitA = "aaaa000000000000000000000000000000000001"
	sweepCommitB = "bbbb000000000000000000000000000000000002"
	sweepCommitC = "cccc000000000000000000000000000000000003"
)

var sweepT0 = time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)

// sweepFixture is a repair fixture whose fetch stamps each commit's archive
// with the commit time the test set for it.
type sweepFixture struct {
	*repairFixture
	mu    sync.Mutex
	times map[string]time.Time
}

func newSweepFixture(t *testing.T) *sweepFixture {
	t.Helper()
	return &sweepFixture{repairFixture: newRepairFixture(t), times: map[string]time.Time{}}
}

func (f *sweepFixture) store() Store {
	s := f.repairFixture.store()
	s.SweepLockWait = 200 * time.Millisecond
	s.Fetch = func(_ context.Context, _, _, commit string) (io.ReadCloser, error) {
		f.mu.Lock()
		at, ok := f.times[commit]
		f.mu.Unlock()
		if !ok {
			at = testCommitTime
		}
		return io.NopCloser(bytes.NewReader(archiveAt(f.t, commit, at))), nil
	}
	return s
}

// hold places r with every archive entry stamped at, so its mark records at
// as its commit time.
func (f *sweepFixture) hold(s Store, r Release, at time.Time) {
	f.t.Helper()
	f.mu.Lock()
	f.times[r.Commit] = at
	f.mu.Unlock()
	if _, cond, err := s.Ensure(context.Background(), r, "", nil, nil); err != nil || cond != CondNone {
		f.t.Fatalf("placing %s: %q %v", r.ID(), cond, err)
	}
}

// archiveAt is archiveFor with every entry stamped at.
func archiveAt(t *testing.T, commit string, at time.Time) []byte {
	t.Helper()
	wrapper := "Acme-skills-" + commit[:7]
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	entries := []struct {
		name, body string
	}{
		{wrapper + "/", ""},
		{wrapper + "/.claude-plugin/", ""},
		{wrapper + "/.claude-plugin/marketplace.json", `{"name":"skills","plugins":[{"name":"demo","source":"./plugins/demo"}]}`},
		{wrapper + "/plugins/demo/skills/greet/SKILL.md", "---\nname: greet\n---\n"},
	}
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: 0o644, ModTime: at, Typeflag: tar.TypeReg, Size: int64(len(e.body))}
		if strings.HasSuffix(e.name, "/") {
			hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeDir, 0o755, 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func skipAsRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("file modes don't bind root")
	}
}

// chmodFor sets path's mode for the rest of the test and puts back restore
// when it ends.
func chmodFor(t *testing.T, path string, mode, restore os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, restore) })
}

func ids(rs []Release) []string {
	out := []string{}
	for _, r := range rs {
		out = append(out, r.ID())
	}
	return out
}

func requireHeld(t *testing.T, s Store, r Release, want bool) {
	t.Helper()
	if held, cond := s.Held(r); held != want {
		t.Fatalf("Held(%s) = %v (%q), want %v", r.ID(), held, cond, want)
	}
}

func requireExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if got := err == nil; got != want {
		t.Fatalf("%s exists = %v (%v), want %v", path, got, err, want)
	}
}

// sweepOrFail runs Sweep and fails the test on an error.
func sweepOrFail(t *testing.T, s Store, scan func() ([]string, error)) SweepResult {
	t.Helper()
	res, err := s.Sweep(context.Background(), scan)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	return res
}

func TestSweepKeepsTheNewestByCommitTime(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	oldest := repairRelease(t, "skills", sweepCommitC)
	middle := repairRelease(t, "skills", sweepCommitB)
	newest := repairRelease(t, "skills", sweepCommitA)
	f.hold(s, oldest, sweepT0)
	f.hold(s, middle, sweepT0.Add(time.Hour))
	f.hold(s, newest, sweepT0.Add(2*time.Hour))

	res := sweepOrFail(t, s, nil)

	if got, want := ids(res.Removed), ids([]Release{newest, middle, oldest})[1:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed = %v, want %v", got, want)
	}
	requireHeld(t, s, newest, true)
	requireExists(t, releaseDir(s.pluginsDir(), oldest), false)
	requireExists(t, releaseDir(s.pluginsDir(), middle), false)
}

func TestSweepBreaksACommitTimeTieOnTheHash(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	lower := repairRelease(t, "skills", sweepCommitA)
	greater := repairRelease(t, "skills", sweepCommitB)
	f.hold(s, lower, sweepT0)
	f.hold(s, greater, sweepT0)

	res := sweepOrFail(t, s, nil)

	if got, want := ids(res.Removed), ids([]Release{lower}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed = %v, want %v", got, want)
	}
	requireHeld(t, s, greater, true)
}

// The newest release survives with no instance linking it, and an older one
// an instance links survives too; only the unlinked older one goes.
func TestSweepKeepsTheNewestWhenNothingLinksIt(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	linked := repairRelease(t, "skills", sweepCommitA)
	unused := repairRelease(t, "skills", sweepCommitB)
	newest := repairRelease(t, "skills", sweepCommitC)
	f.hold(s, linked, sweepT0)
	f.hold(s, unused, sweepT0.Add(time.Hour))
	f.hold(s, newest, sweepT0.Add(2*time.Hour))
	f.linkTo(f.instance("one"), "skills", linked)

	res := sweepOrFail(t, s, nil)

	if got, want := ids(res.Removed), ids([]Release{unused}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed = %v, want %v", got, want)
	}
	requireHeld(t, s, newest, true)
	requireHeld(t, s, linked, true)
}

// A newer <commit> directory that fails Held, one with a wrong-schema mark and
// one with no manifest, is neither the newest nor a candidate: the older held
// release nothing links survives as the newest, and both bad ones stay.
func TestSweepNeverCountsAnUnheldReleaseAsNewest(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	older := repairRelease(t, "skills", sweepCommitA)
	wrongSchema := repairRelease(t, "skills", sweepCommitB)
	noManifest := repairRelease(t, "skills", sweepCommitC)
	f.hold(s, older, sweepT0)
	f.hold(s, wrongSchema, sweepT0.Add(time.Hour))
	f.hold(s, noManifest, sweepT0.Add(2*time.Hour))

	mark, err := s.ReadMark(wrongSchema)
	if err != nil {
		t.Fatal(err)
	}
	mark.Schema = markSchema + 1
	data, err := json.Marshal(mark)
	if err != nil {
		t.Fatal(err)
	}
	markPath := filepath.Join(releaseDir(s.pluginsDir(), wrongSchema), markFileName)
	if err := os.Remove(markPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markPath, data, 0o444); err != nil {
		t.Fatal(err)
	}
	manifestDir := filepath.Join(TreePath(f.home, f.root, noManifest), ".claude-plugin")
	withWrite(t, manifestDir, func() {
		if err := os.Remove(filepath.Join(manifestDir, "marketplace.json")); err != nil {
			t.Fatal(err)
		}
	})
	requireHeld(t, s, wrongSchema, false)
	requireHeld(t, s, noManifest, false)

	res := sweepOrFail(t, s, nil)

	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing", ids(res.Removed))
	}
	requireHeld(t, s, older, true)
	requireExists(t, releaseDir(s.pluginsDir(), wrongSchema), true)
	requireExists(t, releaseDir(s.pluginsDir(), noManifest), true)
}

func TestSweepSkipsACandidateWhoseLockIsHeld(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old := repairRelease(t, "skills", sweepCommitA)
	newest := repairRelease(t, "skills", sweepCommitB)
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))

	unlock, err := filelock.TryFileLock(LockPath(f.home, f.root, old), "another command")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	res := sweepOrFail(t, s, nil)

	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v while the lock was held", ids(res.Removed))
	}
	if got, want := ids(res.Busy), ids([]Release{old}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Busy = %v, want %v", got, want)
	}
	requireHeld(t, s, old, true)

	// Once the lock is let go, the next sweep deletes it.
	unlock()
	res = sweepOrFail(t, s, nil)
	if got, want := ids(res.Removed), ids([]Release{old}); !reflect.DeepEqual(got, want) {
		t.Fatalf("second Removed = %v, want %v", got, want)
	}
}

// A link that appears between the first scan and the rescan under the lock
// keeps the release.
func TestSweepKeepsACandidateLinkedBeforeTheRescan(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old := repairRelease(t, "skills", sweepCommitA)
	newest := repairRelease(t, "skills", sweepCommitB)
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))
	inst := f.instance("one")

	calls := 0
	scan := func() ([]string, error) {
		calls++
		if calls == 2 {
			f.linkTo(inst, "skills", old)
		}
		return hostdir.InstanceDirs(f.home, f.root)
	}
	res := sweepOrFail(t, s, scan)

	if calls != 2 {
		t.Fatalf("scanned %d times, want a first scan and one rescan", calls)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing", ids(res.Removed))
	}
	requireHeld(t, s, old, true)
}

// requireSweepRefuses checks that Sweep fails naming path and deletes
// nothing.
func requireSweepRefuses(t *testing.T, s Store, path string, releases ...Release) {
	t.Helper()
	res, err := s.Sweep(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("Sweep error = %v, want one naming %s", err, path)
	}
	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v after a failed scan", ids(res.Removed))
	}
	for _, r := range releases {
		requireHeld(t, s, r, true)
	}
}

// twoReleases holds an older and a newer release of one source with nothing
// linking the older, so a working sweep would delete it.
func twoReleases(t *testing.T, f *sweepFixture, s Store) (Release, Release) {
	t.Helper()
	old := repairRelease(t, "skills", sweepCommitA)
	newest := repairRelease(t, "skills", sweepCommitB)
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))
	return old, newest
}

func TestSweepDeletesNothingWhenTheRootCannotBeListed(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	chmodFor(t, f.root, 0o311, 0o755)

	requireSweepRefuses(t, s, f.root, old, newest)
}

func TestSweepDeletesNothingWhenKeptCannotBeListed(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	kept := filepath.Join(hostdir.WorkspaceDir(f.home, f.root), hostdir.KeptDirName)
	if err := os.Mkdir(kept, 0o700); err != nil {
		t.Fatal(err)
	}
	chmodFor(t, kept, 0, 0o700)

	requireSweepRefuses(t, s, kept, old, newest)
}

func TestSweepDeletesNothingWhenMarketplacesCannotBeRead(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	marketplaces := filepath.Join(f.instance("one"), ".niwa", MarketplacesDir)
	chmodFor(t, marketplaces, 0, 0o755)

	requireSweepRefuses(t, s, marketplaces, old, newest)
}

func TestSweepDeletesNothingWhenPluginsCannotBeListed(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	chmodFor(t, s.pluginsDir(), 0, 0o700)

	requireSweepRefuses(t, s, s.pluginsDir())
	if err := os.Chmod(s.pluginsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	requireHeld(t, s, old, true)
	requireHeld(t, s, newest, true)
}

// An older release whose held.json can't be read is never a candidate.
func TestSweepKeepsAReleaseWithAnUnreadableMark(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	markPath := filepath.Join(releaseDir(s.pluginsDir(), old), markFileName)
	chmodFor(t, markPath, 0, 0o444)

	res := sweepOrFail(t, s, nil)

	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing", ids(res.Removed))
	}
	requireExists(t, markPath, true)
	requireHeld(t, s, newest, true)
}

// A delete that leaves the repo directory empty (here the newest release goes
// out of band while the sweep holds the candidate's lock) removes the empty
// repo and owner directories with it, and never the levels above them.
func TestSweepLeavesNoEmptyOwnerOrRepoDirectory(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old := repairRelease(t, "skills", sweepCommitA)
	newest := repairRelease(t, "skills", sweepCommitB)
	other := repairRelease(t, "tools", sweepCommitC)
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))
	ownerDir := filepath.Join(s.pluginsDir(), Forge, "acme")

	calls := 0
	scan := func() ([]string, error) {
		calls++
		if calls == 2 {
			removeTree(releaseDir(s.pluginsDir(), newest))
		}
		return hostdir.InstanceDirs(f.home, f.root)
	}
	res := sweepOrFail(t, s, scan)

	if got, want := ids(res.Removed), ids([]Release{old}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed = %v, want %v", got, want)
	}
	requireExists(t, filepath.Join(ownerDir, "skills"), false)
	requireExists(t, ownerDir, false)
	requireExists(t, filepath.Join(s.pluginsDir(), Forge), true)

	// An owner directory still holding another source stays.
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))
	f.hold(s, other, sweepT0)
	calls = 0
	sweepOrFail(t, s, scan)
	requireExists(t, filepath.Join(ownerDir, "skills"), false)
	requireExists(t, ownerDir, true)
	requireHeld(t, s, other, true)
}

func TestSweepRemovesStagingAndTrashOfADeletedRelease(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old, newest := twoReleases(t, f, s)
	pdir := s.pluginsDir()
	var planted []string
	for _, dir := range []string{filepath.Join(pdir, tmpDirName), filepath.Join(pdir, trashDirName)} {
		if err := hostdir.CreatePrivateDir(dir); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(dir, old.hash()+"-0badf00d")
		if err := os.MkdirAll(filepath.Join(entry, "tree"), 0o700); err != nil {
			t.Fatal(err)
		}
		planted = append(planted, entry)
	}
	keep := filepath.Join(pdir, tmpDirName, newest.hash()+"-0badf00d")
	if err := os.Mkdir(keep, 0o700); err != nil {
		t.Fatal(err)
	}

	res := sweepOrFail(t, s, nil)

	if got, want := ids(res.Removed), ids([]Release{old}); !reflect.DeepEqual(got, want) {
		t.Fatalf("Removed = %v, want %v", got, want)
	}
	for _, p := range planted {
		requireExists(t, p, false)
	}
	requireExists(t, keep, true)
	entries, err := os.ReadDir(filepath.Join(pdir, trashDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf(".trash holds %d entries after the sweep", len(entries))
	}
}

// A link whose target names the release through another home's store path
// counts as a use.
func TestSweepCountsALinkThroughAnotherHome(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old, _ := twoReleases(t, f, s)
	link := filepath.Join(f.instance("moved"), ".niwa", MarketplacesDir, "skills")
	if err := os.Symlink(TreePath(filepath.Join(f.home, "elsewhere"), f.root, old), link); err != nil {
		t.Fatal(err)
	}

	res := sweepOrFail(t, s, nil)

	if len(res.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing", ids(res.Removed))
	}
	requireHeld(t, s, old, true)
}

func TestSweepWithNoStoreCreatesNothing(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	f.instance("one")

	res := sweepOrFail(t, s, nil)

	if len(res.Removed)+len(res.Busy) != 0 {
		t.Fatalf("result %+v from a store that doesn't exist", res)
	}
	requireExists(t, filepath.Join(f.home, ".niwa"), false)
}

// A release restored by repair at an old commit keeps the commit time it had,
// so it doesn't push a newer unused release out as a candidate.
func TestRefetchKeepsTheCommitTime(t *testing.T) {
	f := newSweepFixture(t)
	s := f.store()
	old := repairRelease(t, "skills", sweepCommitA)
	newest := repairRelease(t, "skills", sweepCommitB)
	f.hold(s, old, sweepT0)
	f.hold(s, newest, sweepT0.Add(time.Hour))
	f.linkTo(f.instance("one"), "skills", old)
	before, err := s.ReadMark(old)
	if err != nil {
		t.Fatal(err)
	}

	removeTree(releaseDir(s.pluginsDir(), old))
	res := s.Repair(context.Background(), map[string]bool{"acme/skills": true}, everythingReachable)
	if len(res.Restored) != 1 {
		t.Fatalf("Repair = %+v, want the old release restored", res)
	}
	after, err := s.ReadMark(old)
	if err != nil {
		t.Fatal(err)
	}
	if after.CommitTime != before.CommitTime || after.CommitTime != sweepT0.Format(time.RFC3339) {
		t.Fatalf("commit_time = %s after the refetch, was %s", after.CommitTime, before.CommitTime)
	}

	swept := sweepOrFail(t, s, nil)
	if len(swept.Removed) != 0 {
		t.Fatalf("Removed = %v, want nothing", ids(swept.Removed))
	}
	requireHeld(t, s, old, true)
	requireHeld(t, s, newest, true)
}

// Linked passes over an instance whose marketplaces can't be read and returns
// no error for it; the strict scan fails on the same instance.
func TestLinkedStaysLenient(t *testing.T) {
	skipAsRoot(t)
	f := newSweepFixture(t)
	s := f.store()
	r := repairRelease(t, "skills", sweepCommitA)
	one := f.instance("one")
	f.linkTo(one, "skills", r)
	unreadable := filepath.Join(f.instance("two"), ".niwa", MarketplacesDir)
	chmodFor(t, unreadable, 0, 0o755)

	linked, err := s.Linked()
	if err != nil {
		t.Fatalf("Linked: %v", err)
	}
	if len(linked) != 1 || linked[0].Release.ID() != r.ID() || !reflect.DeepEqual(linked[0].Instances, []string{one}) {
		t.Fatalf("Linked = %+v", linked)
	}

	instances, err := hostdir.InstanceDirs(f.home, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanLinks(instances, true); err == nil || !strings.Contains(err.Error(), unreadable) {
		t.Fatalf("strict scan error = %v, want one naming %s", err, unreadable)
	}
}
