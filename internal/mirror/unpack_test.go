package mirror

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// halfPack is a pack whose readable first half holds a small commit's
// objects and whose second half, cut off, holds one large blob.
type halfPack struct {
	store string   // an old store holding only the truncated pack
	small []string // every object of the small commit, its parent's included
	large string   // the large blob, past the cut
}

// newHalfPack builds, in an old store of "app" under f's .store/, one
// non-delta pack holding a small commit's objects and then a 256 KiB random
// blob, truncated halfway with its .idx beside it. git pack-objects writes
// commits, then trees, then blobs, each in the order given, so the large
// blob, listed last, is written last.
func newHalfPack(t *testing.T, f fixture) halfPack {
	t.Helper()
	work := filepath.Join(t.TempDir(), "half")
	testGit(t, filepath.Dir(work), "init", "--quiet", work)
	writeFile(t, filepath.Join(work, "small.txt"), "small\n")
	testGit(t, work, "add", "small.txt")
	testGit(t, work, "commit", "--quiet", "-m", "small")
	var hp halfPack
	for _, line := range strings.Split(testGit(t, work, "rev-list", "--objects", "HEAD"), "\n") {
		hp.small = append(hp.small, strings.Fields(line)[0])
	}
	big := make([]byte, 256<<10)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "large.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	hp.large = testGit(t, work, "hash-object", "-w", "large.bin")

	order := strings.Join(append(append([]string{}, hp.small...), hp.large), "\n") + "\n"
	base := filepath.Join(t.TempDir(), "pack")
	name := testGitStdin(t, work, order, "pack-objects", "--window=0", "-q", base)

	hp.store = filepath.Join(f.mdir(), ".store", "app-20200101T000000Z-0000aaaa")
	packDir := filepath.Join(hp.store, "objects", "pack")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(base + "-" + name + ".pack")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack-"+name+".pack"), data[:len(data)/2], 0o444); err != nil {
		t.Fatal(err)
	}
	idx, err := os.ReadFile(base + "-" + name + ".idx")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packDir, "pack-"+name+".idx"), idx, 0o444); err != nil {
		t.Fatal(err)
	}
	return hp
}

// newDst makes an empty bare repository to salvage into.
func newDst(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "dst.git")
	testGit(t, filepath.Dir(dst), "init", "--quiet", "--bare", dst)
	return dst
}

func reads(dst, id string) bool {
	_, err := gitTry(dst, "--git-dir="+dst, "cat-file", "-e", id)
	return err == nil
}

func TestSalvage_RecoversTheReadableHalfOfATruncatedPack(t *testing.T) {
	f := newFixture(t)
	if err := ensureLayout(f.home, f.root); err != nil {
		t.Fatal(err)
	}
	hp := newHalfPack(t, f)
	tmp := filepath.Join(f.mdir(), ".tmp")

	t.Run("the first half is recovered", func(t *testing.T) {
		dst := newDst(t)
		salvage(f.gitCtx(t), f.mdir(), "app", []string{hp.store}, dst)
		for _, id := range hp.small {
			if !reads(dst, id) {
				t.Errorf("object %s from before the cut doesn't read in the new store", id)
			}
		}
		if reads(dst, hp.large) {
			t.Fatalf("the blob past the cut reads in the new store")
		}
		if packs, _ := filepath.Glob(filepath.Join(dst, "objects", "pack", "*.pack")); len(packs) != 0 {
			t.Fatalf("the truncated pack was copied: %v", packs)
		}
		emptyDir(t, tmp)
	})

	t.Run("an interrupted recovery leaves nothing in .tmp", func(t *testing.T) {
		dst := newDst(t)
		ctx, cancel := context.WithCancel(f.gitCtx(t))
		defer cancel()
		ran := false
		gitHook = func(args []string) {
			if slices.Contains(args, "unpack-objects") {
				ran = true
				cancel()
			}
		}
		t.Cleanup(func() { gitHook = nil })
		salvage(ctx, f.mdir(), "app", []string{hp.store}, dst)
		if !ran {
			t.Fatal("the recovery never ran unpack-objects")
		}
		emptyDir(t, tmp)
	})

	t.Run("too little free space recovers nothing", func(t *testing.T) {
		dst := newDst(t)
		reportFree(t, 1, nil)
		calls := recordGit(t)
		salvage(f.gitCtx(t), f.mdir(), "app", []string{hp.store}, dst)
		for _, c := range calls() {
			if slices.Contains(c, "unpack-objects") {
				t.Fatalf("unpacked with no room: %q", c)
			}
		}
		if reads(dst, hp.small[0]) {
			t.Fatal("an object was recovered with no room for it")
		}
		emptyDir(t, tmp)
	})
}
