package pluginstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

const (
	heldCommit     = "1111111111111111111111111111111111111111"
	missingCommit  = "2222222222222222222222222222222222222222"
	unmarkedCommit = "3333333333333333333333333333333333333333"
)

// repairFixture is a workspace root with several instances and a store whose
// fetch serves a GitHub-shaped archive for whatever commit it is asked for,
// logging each request.
type repairFixture struct {
	home, root string
	mu         sync.Mutex
	fetches    []string
	fetchErr   error
	t          *testing.T
}

func newRepairFixture(t *testing.T) *repairFixture {
	t.Helper()
	base := t.TempDir()
	f := &repairFixture{home: filepath.Join(base, "home"), root: filepath.Join(base, "ws"), t: t}
	if err := os.MkdirAll(f.home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.root, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restoreWrite(base) })
	return f
}

func (f *repairFixture) store() Store {
	return Store{
		Home: f.home, WorkspaceRoot: f.root, LockWait: 10 * time.Second,
		Fetch: func(_ context.Context, owner, repo, commit string) (io.ReadCloser, error) {
			f.mu.Lock()
			f.fetches = append(f.fetches, owner+"/"+repo+"@"+commit)
			err := f.fetchErr
			f.mu.Unlock()
			if err != nil {
				return nil, err
			}
			return io.NopCloser(bytes.NewReader(archiveFor(f.t, commit))), nil
		},
	}
}

func (f *repairFixture) fetched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := append([]string(nil), f.fetches...)
	sort.Strings(out)
	return out
}

func (f *repairFixture) resetFetches() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches = nil
}

// instance makes <root>/<name>/.niwa/marketplaces and returns the instance
// directory.
func (f *repairFixture) instance(name string) string {
	f.t.Helper()
	inst := filepath.Join(f.root, name)
	if err := os.MkdirAll(filepath.Join(inst, ".niwa", MarketplacesDir), 0o755); err != nil {
		f.t.Fatal(err)
	}
	return inst
}

// linkTo points <inst>/.niwa/marketplaces/<name> at r's tree in this store.
func (f *repairFixture) linkTo(inst, name string, r Release) {
	f.t.Helper()
	link := filepath.Join(inst, ".niwa", MarketplacesDir, name)
	_ = os.Remove(link)
	if err := os.Symlink(TreePath(f.home, f.root, r), link); err != nil {
		f.t.Fatal(err)
	}
}

func repairRelease(t *testing.T, repo, commit string) Release {
	t.Helper()
	r, ok := NewRelease("acme", repo, commit, "", "")
	if !ok {
		t.Fatalf("release %s@%s not valid", repo, commit)
	}
	return r
}

func everythingReachable(context.Context, string, string, string) (bool, error) { return true, nil }

func declaredAcme() map[string]bool {
	return map[string]bool{"acme/held": true, "acme/missing": true, "acme/unmarked": true}
}

// TestRepairFetchesOnlyWhatIsNotHeld holds one release, leaves one missing
// and one present without its held.json, links all three from instances on
// disk, and checks that Repair fetches only the two that aren't held and
// names each restored release with the instances that link it.
func TestRepairFetchesOnlyWhatIsNotHeld(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	held := repairRelease(t, "held", heldCommit)
	missing := repairRelease(t, "missing", missingCommit)
	unmarked := repairRelease(t, "unmarked", unmarkedCommit)

	for _, r := range []Release{held, unmarked} {
		if _, cond, err := s.Ensure(context.Background(), r, "", nil, nil); err != nil || cond != CondNone {
			t.Fatalf("placing %s: %q %v", r.ID(), cond, err)
		}
	}
	if err := os.Remove(filepath.Join(releaseDir(s.pluginsDir(), unmarked), markFileName)); err != nil {
		t.Fatal(err)
	}
	f.resetFetches()

	one, two, three := f.instance("one"), f.instance("two"), f.instance("three")
	f.linkTo(one, "held", held)
	f.linkTo(one, "missing", missing)
	f.linkTo(two, "missing", missing)
	f.linkTo(two, "unmarked", unmarked)
	f.linkTo(three, "held", held)

	res := s.Repair(context.Background(), declaredAcme(), everythingReachable)

	if got, want := f.fetched(), []string{"acme/missing@" + missingCommit, "acme/unmarked@" + unmarkedCommit}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fetches = %v, want %v", got, want)
	}
	want := []RepairEntry{
		{Release: missing, Instances: []string{one, two}},
		{Release: unmarked, Instances: []string{two}},
	}
	if !sameEntries(res.Restored, want) {
		t.Fatalf("Restored = %+v, want %+v", res.Restored, want)
	}
	if len(res.Refused)+len(res.Undeclared)+len(res.Failed) != 0 || res.ScanErr != nil {
		t.Fatalf("unexpected result %+v", res)
	}
	for _, r := range []Release{held, missing, unmarked} {
		if ok, cond := s.Held(r); !ok || cond != CondNone {
			t.Fatalf("%s not held after Repair (%q)", r.ID(), cond)
		}
	}
	if got := missing.Name(); got != "github.com/acme/missing@"+missingCommit {
		t.Fatalf("Name = %q", got)
	}

	// A second pass has nothing to do.
	f.resetFetches()
	res = s.Repair(context.Background(), declaredAcme(), everythingReachable)
	if len(f.fetched()) != 0 || len(res.Restored) != 0 {
		t.Fatalf("second Repair fetched %v, restored %+v", f.fetched(), res.Restored)
	}
}

// sameEntries compares releases by id and instances, ignoring Ref and Tag.
func sameEntries(got, want []RepairEntry) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i].Release.ID() != want[i].Release.ID() || !reflect.DeepEqual(got[i].Instances, want[i].Instances) {
			return false
		}
	}
	return true
}

func TestRepairRefusesACommitOutsideTheSourceHistory(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	r := repairRelease(t, "missing", missingCommit)
	inst := f.instance("one")
	f.linkTo(inst, "missing", r)
	link := filepath.Join(inst, ".niwa", MarketplacesDir, "missing")
	before, _ := os.Readlink(link)

	notInHistory := func(context.Context, string, string, string) (bool, error) { return false, nil }
	res := s.Repair(context.Background(), declaredAcme(), notInHistory)

	if len(f.fetched()) != 0 {
		t.Fatalf("fetched %v for a refused release", f.fetched())
	}
	if len(res.Refused) != 1 || res.Refused[0].Release.ID() != r.ID() || res.Refused[0].Reason == "" {
		t.Fatalf("Refused = %+v", res.Refused)
	}
	if _, err := os.Lstat(releaseDir(s.pluginsDir(), r)); !os.IsNotExist(err) {
		t.Fatalf("release directory exists after a refusal (%v)", err)
	}
	if after, _ := os.Readlink(link); after != before {
		t.Fatalf("link changed: %s -> %s", before, after)
	}
}

func TestRepairLeavesAnUndeclaredSourceAlone(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	r := repairRelease(t, "gone", missingCommit)
	inst := f.instance("one")
	f.linkTo(inst, "gone", r)

	res := s.Repair(context.Background(), declaredAcme(), everythingReachable)

	if len(f.fetched()) != 0 {
		t.Fatalf("fetched %v for an undeclared source", f.fetched())
	}
	if len(res.Undeclared) != 1 || res.Undeclared[0].Release.ID() != r.ID() || !reflect.DeepEqual(res.Undeclared[0].Instances, []string{inst}) {
		t.Fatalf("Undeclared = %+v", res.Undeclared)
	}
}

func TestRepairReportsAFailedFetchAndLeavesNothingHeld(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	r := repairRelease(t, "missing", missingCommit)
	f.linkTo(f.instance("one"), "missing", r)
	f.fetchErr = errors.New("connection reset")

	res := s.Repair(context.Background(), declaredAcme(), everythingReachable)

	if len(res.Failed) != 1 || res.Failed[0].Release.ID() != r.ID() || res.Failed[0].Reason == "" {
		t.Fatalf("Failed = %+v", res.Failed)
	}
	dir := releaseDir(s.pluginsDir(), r)
	for _, p := range []string{dir, filepath.Join(dir, markFileName)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a failed fetch (%v)", p, err)
		}
	}

	// The next pass, with the fetch working again, restores it.
	f.fetchErr = nil
	f.resetFetches()
	res = s.Repair(context.Background(), declaredAcme(), everythingReachable)
	if len(res.Restored) != 1 || len(f.fetched()) != 1 {
		t.Fatalf("retry restored %+v with fetches %v", res.Restored, f.fetched())
	}
}

// TestLinkedReadsOnlyReleaseLinks checks what the scan counts: a release link
// into this store, and one into another home's store (an instance moved
// here), whose release is held here too. A relative link, a real directory
// and an instance whose marketplaces directory is itself a link are passed
// over.
func TestLinkedReadsOnlyReleaseLinks(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	r := repairRelease(t, "missing", missingCommit)
	moved := repairRelease(t, "held", heldCommit)

	one := f.instance("one")
	f.linkTo(one, "missing", r)
	marketplaces := filepath.Join(one, ".niwa", MarketplacesDir)
	if err := os.Symlink(TreePath(filepath.Join(f.home, "elsewhere"), f.root, moved), filepath.Join(marketplaces, "other-home")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../relative/tree", filepath.Join(marketplaces, "relative")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(marketplaces, "real", ".claude-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}

	two := filepath.Join(f.root, "two")
	if err := os.MkdirAll(filepath.Join(two, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(marketplaces, filepath.Join(two, ".niwa", MarketplacesDir)); err != nil {
		t.Fatal(err)
	}

	linked, err := s.Linked()
	if err != nil {
		t.Fatal(err)
	}
	want := []RepairEntry{
		{Release: moved, Instances: []string{one}},
		{Release: r, Instances: []string{one}},
	}
	var got []RepairEntry
	for _, l := range linked {
		got = append(got, RepairEntry{Release: l.Release, Instances: l.Instances})
	}
	if !sameEntries(got, want) {
		t.Fatalf("Linked = %+v, want %+v", linked, want)
	}

	// The moved instance's release is held in this store; its link is left
	// for that instance's own next command to relink.
	res := s.Repair(context.Background(), declaredAcme(), everythingReachable)
	if len(res.Restored) != 2 {
		t.Fatalf("Restored = %+v, want both releases", res.Restored)
	}
	if ok, _ := s.Held(moved); !ok {
		t.Fatal("the moved instance's release is not held here")
	}
	if target, _ := os.Readlink(filepath.Join(marketplaces, "other-home")); target != TreePath(filepath.Join(f.home, "elsewhere"), f.root, moved) {
		t.Fatalf("Repair rewrote a link in an instance: %s", target)
	}
}

// TestLinkedCountsKeptInstances checks that an instance kept under
// ~/.niwa/w-<key>/kept is scanned like one under the root.
func TestLinkedCountsKeptInstances(t *testing.T) {
	f := newRepairFixture(t)
	s := f.store()
	r := repairRelease(t, "missing", missingCommit)
	kept := filepath.Join(hostdir.WorkspaceDir(f.home, f.root), hostdir.KeptDirName, "old")
	if err := os.MkdirAll(filepath.Join(kept, ".niwa", MarketplacesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(TreePath(f.home, f.root, r), filepath.Join(kept, ".niwa", MarketplacesDir, "missing")); err != nil {
		t.Fatal(err)
	}

	linked, err := s.Linked()
	if err != nil {
		t.Fatal(err)
	}
	if len(linked) != 1 || !reflect.DeepEqual(linked[0].Instances, []string{kept}) {
		t.Fatalf("Linked = %+v", linked)
	}
}
