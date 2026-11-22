package mirror

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// recordOf reads the fixture's store record, failing the test when there is
// none to read.
func recordOf(t testing.TB, f fixture) storesFile {
	t.Helper()
	rec, ok := readStores(f.mdir(), "app")
	if !ok {
		t.Fatalf("there's no readable store record at %s", storesPath(f.mdir(), "app"))
	}
	return rec
}

// entryOf returns the record's entry for store, given as a link target
// (".store/<name>") or a bare name, failing when there is none.
func entryOf(t testing.TB, f fixture, store string) storeEntry {
	t.Helper()
	rec := recordOf(t, f)
	name := filepath.Base(store)
	if i := rec.index(name); i >= 0 {
		return rec.Stores[i]
	}
	t.Fatalf("the store record lists %+v, not %s", rec.Stores, name)
	return storeEntry{}
}

// requireKeptRetired fails unless store is still on disk and the record
// lists it as linked once and retired since.
func requireKeptRetired(t testing.TB, f fixture, store string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(f.mdir(), ".store", filepath.Base(store), "objects")); err != nil {
		t.Fatalf("the replaced store %s is gone (%v)", store, err)
	}
	if e := entryOf(t, f, store); !e.Linked || e.RetiredAt == "" {
		t.Fatalf("the record's entry for %s = %+v, want linked and retired", store, e)
	}
}

// writeRecord writes rec as the fixture's store record.
func writeRecord(t testing.TB, f fixture, rec storesFile) {
	t.Helper()
	if err := writeStores(f.mdir(), "app", rec); err != nil {
		t.Fatal(err)
	}
}

// storeLinesOf returns the store lines among lines, in order.
func storeLinesOf(lines []map[string]any) []map[string]any {
	var out []map[string]any
	for _, l := range lines {
		if l["event"] == eventRemove || l["event"] == eventKeep {
			out = append(out, l)
		}
	}
	return out
}

func TestRestore_RecordsEveryStore(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	damageHEAD(t, f)
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v", res)
	}
	current := f.linkTarget(t)
	if current == old {
		t.Fatal("the link still names the old store")
	}

	path := storesPath(f.mdir(), "app")
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("store record = %v (%v), want a 0600 file", info, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Schema int              `json:"schema"`
		Stores []map[string]any `json:"stores"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Schema != storesSchema || len(raw.Stores) != 2 {
		t.Fatalf("store record = %s, want schema %d and two stores", data, storesSchema)
	}
	byName := map[string]map[string]any{}
	for _, e := range raw.Stores {
		byName[e["name"].(string)] = e
	}
	for _, store := range []string{old, current} {
		e, ok := byName[filepath.Base(store)]
		if !ok {
			t.Fatalf("store record %s has no entry named %s (without .store/)", data, filepath.Base(store))
		}
		if e["url"] != recordURL(f.repo().URL) || e["identity"] != remoteIdentity(f.repo().URL) || e["linked"] != true {
			t.Fatalf("entry %v, want url %q, identity %q and linked", e, recordURL(f.repo().URL), remoteIdentity(f.repo().URL))
		}
		if _, err := os.Lstat(filepath.Join(f.mdir(), store, "objects")); err != nil {
			t.Fatalf("store %s is gone after the restore (%v)", store, err)
		}
	}
	if _, ok := byName[filepath.Base(old)]["retired_at"]; !ok {
		t.Fatalf("the replaced store's entry %v has no retired_at", byName[filepath.Base(old)])
	}
	if _, ok := byName[filepath.Base(current)]["retired_at"]; ok {
		t.Fatalf("the linked store's entry %v has a retired_at", byName[filepath.Base(current)])
	}
}

func TestRestore_RemovesANeverLinkedStoreWithNoScan(t *testing.T) {
	f := built(t)
	name := "app-20200101T000000Z-0000000c"
	crashed := filepath.Join(f.mdir(), ".store", name)
	writeFile(t, filepath.Join(crashed, "objects", "pack", "x"), "a store a crashed restore renamed in")
	rec := recordOf(t, f)
	rec.put(storeEntry{Name: name, URL: recordURL(f.repo().URL), Identity: remoteIdentity(f.repo().URL)})
	writeRecord(t, f, rec)
	logged := len(f.logLines(t))

	opts := f.applyOpts()
	opts.Scan = func() ([]string, error) {
		t.Error("the scan ran: a never-linked store needs none")
		return nil, errors.New("no scan here")
	}
	damageHEAD(t, f)
	res := EnsureHealthy(context.Background(), opts, []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v", res)
	}
	if _, err := os.Lstat(crashed); !os.IsNotExist(err) {
		t.Fatalf("the never-linked store is still there (%v)", err)
	}
	if after := recordOf(t, f); after.index(name) >= 0 {
		t.Fatalf("the record still lists the never-linked store: %+v", after.Stores)
	}
	lines := storeLinesOf(newLines(t, f, logged))
	if len(lines) != 1 || lines[0]["event"] != eventRemove || lines[0]["store"] != ".store/"+name || lines[0]["reason"] != reasonNeverLinked {
		t.Fatalf("store lines = %v, want one remove of .store/%s for %s", lines, name, reasonNeverLinked)
	}
}

func TestRestore_SeedsTheRecordFromAV043StateFile(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	onlyOld := extraPack(t, filepath.Join(f.store(t), "objects", "pack"), "only the old store")
	// v0.43.0 wrote no record and no identity in the state file.
	if err := os.Remove(storesPath(f.mdir(), "app")); err != nil {
		t.Fatal(err)
	}
	writeOldStateURL(t, f, f.repo().URL)
	damageHEAD(t, f)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v", res)
	}
	seeded := entryOf(t, f, old)
	if seeded.URL != recordURL(f.repo().URL) || seeded.Identity != remoteIdentity(f.repo().URL) {
		t.Fatalf("seeded entry = %+v, want the state file's URL and its identity", seeded)
	}
	requireKeptRetired(t, f, old)
	if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", onlyOld)); err != nil {
		t.Fatalf("the seeded store wasn't salvaged from: %v", err)
	}
	if from, _ := salvagedFrom(t, restoreLine(t, f)); !slices.Equal(from, []string{old}) {
		t.Fatalf("salvaged_from = %v, want [%s]", from, old)
	}
}

func TestRestore_AFailureBeforeTheSwapLeavesTheRecord(t *testing.T) {
	type tc struct {
		name   string
		hook   func() func(string) error
		record bool
	}
	failAt := func(stage string, n int) func() func(string) error {
		return func() func(string) error {
			seen := 0
			return func(s string) error {
				if s != stage {
					return nil
				}
				if seen++; seen == n {
					return errors.New("injected failure at " + s)
				}
				return nil
			}
		}
	}
	var cases []tc
	for _, record := range []bool{true, false} {
		for _, stage := range []string{stageClone, stageState, stageStore, stageLink, stageSwap} {
			cases = append(cases, tc{stage, failAt(stage, 1), record})
		}
		cases = append(cases,
			tc{"stores, the never-linked entry", failAt(stageStores, 1), record},
			tc{"stores, the linked entry", failAt(stageStores, 2), record})
	}
	for _, c := range cases {
		name := c.name + ", with a record"
		if !c.record {
			name = c.name + ", with none"
		}
		t.Run(name, func(t *testing.T) {
			f := built(t)
			if !c.record {
				if err := os.Remove(storesPath(f.mdir(), "app")); err != nil {
					t.Fatal(err)
				}
			}
			damageHEAD(t, f)
			before := snapMirror(t, f)
			stageHook = c.hook()
			t.Cleanup(func() { stageHook = nil })

			res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
			if len(res) != 1 || res[0].Outcome != OutcomeRestoreFailed || res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "injected failure") {
				t.Fatalf("result = %+v, want restore-failed with the injected error", res)
			}
			before.requireSame(t, f)
			_, err := os.Lstat(storesPath(f.mdir(), "app"))
			if c.record && err != nil {
				t.Fatalf("the record is gone (%v)", err)
			}
			if !c.record && !os.IsNotExist(err) {
				t.Fatalf("a record was left where there was none (%v)", err)
			}
			requireNoLeftovers(t, f)
		})
	}
}

func TestHostile_ALinkedRecordIsReplacedNotFollowed(t *testing.T) {
	f := built(t)
	old := f.linkTarget(t)
	outside := filepath.Join(t.TempDir(), "outside.stores")
	body := []byte("not niwa's\n")
	if err := os.WriteFile(outside, body, 0o644); err != nil {
		t.Fatal(err)
	}
	path := storesPath(f.mdir(), "app")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	damageHEAD(t, f)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore = %+v", res)
	}
	if info, err := os.Lstat(path); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("store record = %v (%v), want a 0600 regular file in place of the link", info, err)
	}
	if data, err := os.ReadFile(outside); err != nil || !bytes.Equal(data, body) {
		t.Fatalf("the file the link named changed: %q (%v)", data, err)
	}
	if info, err := os.Stat(outside); err != nil || info.Mode().Perm() != 0o644 {
		t.Fatalf("the file the link named had its mode changed: %v (%v)", info, err)
	}
	// The linked record was read as none, so the state file seeded it.
	requireKeptRetired(t, f, old)
}

// After a url-changed rebuild the old remote's store is kept, retired, under
// its own identity. A later restore of the new remote salvages nothing from
// it, nor from a store directory no record names, and its restore line
// names only the store it did salvage from.
func TestRestore_NoSalvageFromAnotherRemoteStoreOrAnUnrecordedOne(t *testing.T) {
	f := built(t)
	oldRemoteStore := f.linkTarget(t)
	onlyOld := extraPack(t, filepath.Join(f.store(t), "objects", "pack"), "only the old remote's store")
	other := filepath.Join(filepath.Dir(f.remote), "other.git")
	testGit(t, filepath.Dir(f.remote), "clone", "--quiet", "--bare", f.remote, other)
	repo := Repo{ID: "app", URL: "file://" + other}

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{repo})
	if len(res) != 1 || res[0].Fault != FaultURLChanged || res[0].Outcome != OutcomeRestored {
		t.Fatalf("url-changed rebuild = %+v", res)
	}
	requireKeptRetired(t, f, oldRemoteStore)
	if e := entryOf(t, f, oldRemoteStore); e.Identity != remoteIdentity(f.repo().URL) {
		t.Fatalf("the old remote's entry = %+v, want its own identity %q", e, remoteIdentity(f.repo().URL))
	}
	newRemoteStore := f.linkTarget(t)

	unrecorded := filepath.Join(f.mdir(), ".store", "app-20200101T000000Z-0000beef")
	onlyUnrecorded := extraPack(t, filepath.Join(unrecorded, "objects", "pack"), "only the unrecorded store")

	damageHEAD(t, f)
	res = EnsureHealthy(context.Background(), f.applyOpts(), []Repo{repo})
	if len(res) != 1 || res[0].Fault != FaultRepoUnreadable || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
		t.Fatalf("restore of the new remote = %+v", res)
	}
	for name, pack := range map[string]string{"the old remote's store": onlyOld, "the unrecorded store": onlyUnrecorded} {
		if _, err := os.Lstat(filepath.Join(f.store(t), "objects", "pack", pack)); !os.IsNotExist(err) {
			t.Fatalf("a pack only %s held reached the new store (%v)", name, err)
		}
	}
	if from, _ := salvagedFrom(t, restoreLine(t, f)); !slices.Equal(from, []string{newRemoteStore}) {
		t.Fatalf("salvaged_from = %v, want only %s", from, newRemoteStore)
	}
	if _, err := os.Lstat(filepath.Join(unrecorded, "objects", "pack", onlyUnrecorded)); err != nil {
		t.Fatalf("the unrecorded store was touched (%v)", err)
	}
}

func TestHostile_ALoosenedRecordModeIsTightened(t *testing.T) {
	f := built(t)
	path := storesPath(f.mdir(), "app")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()}); len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("check = %+v, want healthy", res)
	}
	if info, err := os.Lstat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("store record = %v (%v), want mode 0600", info, err)
	}
}
