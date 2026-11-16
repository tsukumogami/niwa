package mirror

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// makeWritable lets a test damage a file git wrote read-only. The chmod moves
// the file's ctime, as any write would.
func makeWritable(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

func idxOf(pack string) string { return strings.TrimSuffix(pack, ".pack") + ".idx" }

func TestCheck_FaultsAndTheirRestore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, f fixture)
		want   Fault
	}{
		{"objects emptied", func(t *testing.T, f fixture) {
			objects := filepath.Join(f.store(t), "objects")
			if err := os.RemoveAll(objects); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(objects, 0o755); err != nil {
				t.Fatal(err)
			}
		}, FaultObjectsEmpty},
		{"pack deleted", func(t *testing.T, f fixture) {
			if err := os.Remove(f.packs(t)[0]); err != nil {
				t.Fatal(err)
			}
		}, FaultPackMissing},
		{"idx deleted", func(t *testing.T, f fixture) {
			if err := os.Remove(idxOf(f.packs(t)[0])); err != nil {
				t.Fatal(err)
			}
		}, FaultPackMissing},
		{"pack truncated", func(t *testing.T, f fixture) {
			pack := f.packs(t)[0]
			info, err := os.Stat(pack)
			if err != nil {
				t.Fatal(err)
			}
			makeWritable(t, pack)
			if err := os.Truncate(pack, info.Size()/2); err != nil {
				t.Fatal(err)
			}
		}, FaultPackChanged},
		{"pack overwritten mid-file at the same size", func(t *testing.T, f fixture) {
			pack := f.packs(t)[0]
			info, err := os.Stat(pack)
			if err != nil {
				t.Fatal(err)
			}
			makeWritable(t, pack)
			fh, err := os.OpenFile(pack, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			// Past the 12-byte header and before the 20-byte trailer.
			if _, err := fh.WriteAt(bytes.Repeat([]byte{0xff}, 8), (info.Size()-20)/2); err != nil {
				t.Fatal(err)
			}
			if err := fh.Close(); err != nil {
				t.Fatal(err)
			}
		}, FaultPackChanged},
		{"HEAD deleted", func(t *testing.T, f fixture) {
			if err := os.Remove(filepath.Join(f.store(t), "HEAD")); err != nil {
				t.Fatal(err)
			}
		}, FaultRepoUnreadable},
		{"config deleted", func(t *testing.T, f fixture) {
			if err := os.Remove(filepath.Join(f.store(t), "config")); err != nil {
				t.Fatal(err)
			}
		}, FaultRepoUnreadable},
		{"state file deleted", func(t *testing.T, f fixture) {
			if err := os.Remove(statePath(f.mdir(), "app")); err != nil {
				t.Fatal(err)
			}
		}, FaultMissing},
		{"state file names another store", func(t *testing.T, f fixture) {
			st, err := readState(f.mdir(), "app")
			if err != nil {
				t.Fatal(err)
			}
			st.Store = ".store/app-20200101T000000Z-00000000"
			data, err := json.Marshal(st)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statePath(f.mdir(), "app"), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}, FaultMissing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := built(t)
			// The build and the damage must land on different ctimes.
			time.Sleep(20 * time.Millisecond)
			before := f.linkTarget(t)
			tc.damage(t, f)
			if got := check(context.Background(), f.mdir(), "app"); got != tc.want {
				t.Fatalf("check = %q, want %q", got, tc.want)
			}
			res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
			if len(res) != 1 || res[0].Fault != tc.want || res[0].Outcome != OutcomeRestored || res[0].Err != nil {
				t.Fatalf("result = %+v, want %s/restored", res, tc.want)
			}
			if f.linkTarget(t) == before {
				t.Fatal("the restore left the link on the damaged store")
			}
			if got := check(context.Background(), f.mdir(), "app"); got != FaultNone {
				t.Fatalf("check after the restore = %q", got)
			}
			st, err := readState(f.mdir(), "app")
			if err != nil {
				t.Fatal(err)
			}
			if st.Store != f.linkTarget(t) || len(st.Packs) != len(f.packs(t)) {
				t.Fatalf("state after the restore = %+v, store holds %v", st, f.packs(t))
			}
			lines := f.logLines(t)
			last := lines[len(lines)-2:]
			for i, event := range []string{"check", "restore"} {
				if last[i]["event"] != event || last[i]["fault"] != string(tc.want) || last[i]["result"] != "restored" {
					t.Fatalf("log line %d = %v, want %s with %s/restored", i, last[i], event, tc.want)
				}
			}
		})
	}
}

func TestCheck_MovedCtimeAndUnlistedPackAreNoFault(t *testing.T) {
	f := built(t)
	target := f.linkTarget(t)
	time.Sleep(20 * time.Millisecond)
	pack := f.packs(t)[0]
	recorded, err := describePack(pack)
	if err != nil {
		t.Fatal(err)
	}
	makeWritable(t, pack)
	moved, err := describePack(pack)
	if err != nil {
		t.Fatal(err)
	}
	if moved.CtimeNs == recorded.CtimeNs {
		t.Fatal("the chmod did not move the pack's ctime; the case proves nothing")
	}

	// An extra pack the manifest doesn't list: a copy under another name.
	extra := filepath.Join(filepath.Dir(pack), "pack-"+strings.Repeat("ab", 20)+".pack")
	for src, dst := range map[string]string{pack: extra, idxOf(pack): idxOf(extra)} {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o444); err != nil {
			t.Fatal(err)
		}
	}

	if got := check(context.Background(), f.mdir(), "app"); got != FaultNone {
		t.Fatalf("check = %q, want no fault", got)
	}
	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeHealthy {
		t.Fatalf("result = %+v, want healthy", res)
	}
	if f.linkTarget(t) != target {
		t.Fatal("the link moved")
	}
}

// oldStoreFixture builds, under .store/, an old store of "app" that nothing
// links to, holding: an intact pack, a truncated pack, an .idx whose pack is
// gone, a loose object that hashes to its name, one that doesn't, a link
// named like a pack, and planted config, hooks, info, alternates and
// packed-refs. It returns the store's path and the names salvage must keep
// and must drop.
func oldStoreFixture(t *testing.T, f fixture) (store string, keep, drop []string) {
	t.Helper()
	work := filepath.Join(t.TempDir(), "old")
	testGit(t, filepath.Dir(work), "init", "--quiet", work)
	var packs []string
	for i, name := range []string{"one", "two", "three"} {
		if err := os.WriteFile(filepath.Join(work, name), []byte(strings.Repeat(name, 100+i)), 0o644); err != nil {
			t.Fatal(err)
		}
		testGit(t, work, "add", name)
		testGit(t, work, "commit", "--quiet", "-m", name)
		testGit(t, work, "repack", "-d", "-q")
		all, _ := filepath.Glob(filepath.Join(work, ".git", "objects", "pack", "*.pack"))
		for _, p := range all {
			seen := false
			for _, q := range packs {
				seen = seen || q == p
			}
			if !seen {
				packs = append(packs, p)
			}
		}
	}
	if len(packs) != 3 {
		t.Fatalf("made %d packs, want 3", len(packs))
	}

	store = filepath.Join(f.mdir(), ".store", "app-20200101T000000Z-0badc0de")
	objects := filepath.Join(store, "objects")
	if err := os.MkdirAll(filepath.Join(objects, "pack"), 0o755); err != nil {
		t.Fatal(err)
	}
	copyFile := func(src, dst string) {
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, data, 0o444); err != nil {
			t.Fatal(err)
		}
	}
	dstPack := func(p string) string { return filepath.Join(objects, "pack", filepath.Base(p)) }

	// Intact.
	copyFile(packs[0], dstPack(packs[0]))
	copyFile(idxOf(packs[0]), idxOf(dstPack(packs[0])))
	keep = append(keep, filepath.Join("objects", "pack", filepath.Base(packs[0])))
	// Truncated.
	data, err := os.ReadFile(packs[1])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dstPack(packs[1]), data[:len(data)/2], 0o444); err != nil {
		t.Fatal(err)
	}
	copyFile(idxOf(packs[1]), idxOf(dstPack(packs[1])))
	drop = append(drop, filepath.Join("objects", "pack", filepath.Base(packs[1])))
	// An .idx whose .pack is gone.
	copyFile(idxOf(packs[2]), idxOf(dstPack(packs[2])))
	drop = append(drop, filepath.Join("objects", "pack", filepath.Base(idxOf(packs[2]))))

	// Loose objects: one that hashes to its name, and the same bytes under
	// a name they don't hash to.
	blob := []byte("blob 6\x00salvag")
	var z bytes.Buffer
	zw := zlib.NewWriter(&z)
	if _, err := zw.Write(blob); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	id := testGitStdin(t, work, "salvag", "hash-object", "--stdin")
	if err := os.MkdirAll(filepath.Join(objects, id[:2]), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, id[:2], id[2:]), z.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	keep = append(keep, filepath.Join("objects", id[:2], id[2:]))
	badName := strings.Repeat("e", 38)
	if err := os.MkdirAll(filepath.Join(objects, "ee"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(objects, "ee", badName), z.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	drop = append(drop, filepath.Join("objects", "ee", badName))

	// A link named like a pack, pointing at the intact pack outside.
	linkName := "pack-" + strings.Repeat("cd", 20) + ".pack"
	if err := os.Symlink(packs[0], filepath.Join(objects, "pack", linkName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(idxOf(packs[0]), filepath.Join(objects, "pack", idxOf(linkName))); err != nil {
		t.Fatal(err)
	}
	drop = append(drop, filepath.Join("objects", "pack", linkName))

	// Planted files that are not objects.
	for rel, body := range map[string]string{
		"config":                  "[core]\n\tbare = true\n[niwa]\n\tplanted = yes\n",
		"HEAD":                    "ref: refs/heads/main\n",
		"hooks/post-checkout":     "#!/bin/sh\necho planted\n",
		"info/planted":            "planted\n",
		"objects/info/alternates": "/planted/objects\n",
		"packed-refs":             "0000000000000000000000000000000000000000 refs/heads/planted\n",
	} {
		path := filepath.Join(store, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	drop = append(drop, filepath.Join("hooks", "post-checkout"), filepath.Join("info", "planted"), filepath.Join("objects", "info", "alternates"))
	return store, keep, drop
}

// testGitStdin runs a fixture git command with input on stdin.
func testGitStdin(t *testing.T, dir, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func TestRestore_SalvagesOnlyIntactObjects(t *testing.T) {
	f := newFixture(t)
	f.borrow(t)
	if err := ensureLayout(f.home, f.root); err != nil {
		t.Fatal(err)
	}
	old, keep, drop := oldStoreFixture(t, f)

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	store := f.store(t)
	for _, rel := range keep {
		info, err := os.Lstat(filepath.Join(store, rel))
		if err != nil || !info.Mode().IsRegular() {
			t.Errorf("the new store lacks %s (%v)", rel, err)
		}
	}
	for _, rel := range drop {
		if _, err := os.Lstat(filepath.Join(store, rel)); err == nil {
			t.Errorf("the new store holds %s", rel)
		}
	}
	for _, rel := range keep {
		if strings.HasSuffix(rel, ".pack") {
			if err := verifyPack(context.Background(), idxOf(filepath.Join(store, rel))); err != nil {
				t.Errorf("the salvaged pack fails verify-pack: %v", err)
			}
		}
	}
	cfg, err := os.ReadFile(filepath.Join(store, "config"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "planted") {
		t.Error("the planted config key reached the new store")
	}
	if refs, err := os.ReadFile(filepath.Join(store, "packed-refs")); err == nil && strings.Contains(string(refs), "planted") {
		t.Error("the planted packed-refs reached the new store")
	}
	if _, err := os.Lstat(old); !os.IsNotExist(err) {
		t.Errorf("the old store is still there (%v)", err)
	}
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Packs) != len(f.packs(t)) {
		t.Fatalf("manifest lists %d packs, the store holds %d", len(st.Packs), len(f.packs(t)))
	}
}

func TestRestore_RemovesOnlyThisRepoStores(t *testing.T) {
	f := built(t)
	unused := filepath.Join(f.mdir(), ".store", "app-20200101T000000Z-00000000")
	if err := os.MkdirAll(unused, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(f.mdir(), ".store", "app-extra-20200101T000000Z-00000000")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	otherFile := filepath.Join(other, "keep")
	if err := os.WriteFile(otherFile, []byte("other repo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.store(t), "HEAD")); err != nil {
		t.Fatal(err)
	}

	res := EnsureHealthy(context.Background(), f.applyOpts(), []Repo{f.repo()})
	if len(res) != 1 || res[0].Outcome != OutcomeRestored {
		t.Fatalf("result = %+v, want restored", res)
	}
	stores := oldStores(f.mdir(), "app")
	st, err := readState(f.mdir(), "app")
	if err != nil {
		t.Fatal(err)
	}
	if len(stores) != 1 || filepath.Join(".store", stores[0]) != f.linkTarget(t) || st.Store != f.linkTarget(t) {
		t.Fatalf(".store holds %v of app, link %q, state %q; want only the linked one", stores, f.linkTarget(t), st.Store)
	}
	if data, err := os.ReadFile(otherFile); err != nil || string(data) != "other repo" {
		t.Fatalf("the app-extra store changed: %q (%v)", data, err)
	}
}
