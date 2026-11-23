package mirror

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// keptScan is a scan that finds clones, as the strict walk of kept/ would.
func keptScan(clones ...string) func() ([]string, error) {
	return func() ([]string, error) { return clones, nil }
}

// keptBorrower makes a clone outside the workspace root, as a kept instance
// is, whose alternates holds line.
func keptBorrower(t *testing.T, line string) string {
	t.Helper()
	clone := filepath.Join(t.TempDir(), "kept", "old", "tools", "app")
	writeFile(t, filepath.Join(clone, ".git", "objects", "info", "alternates"), line+"\n")
	return clone
}

func teardown(t *testing.T, f fixture, scan func() ([]string, error)) string {
	t.Helper()
	var out bytes.Buffer
	NewTeardown(f.home, f.root).Remove(scan, &out)
	return out.String()
}

func requireMirrorKept(t *testing.T, f fixture, stores ...string) {
	t.Helper()
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); err != nil {
		t.Fatalf("the stable link was removed (%v)", err)
	}
	for _, path := range []string{statePath(f.mdir(), "app"), storesPath(f.mdir(), "app")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s was removed (%v)", path, err)
		}
	}
	for _, store := range stores {
		if _, err := os.Lstat(filepath.Join(f.mdir(), store, "objects")); err != nil {
			t.Fatalf("store %s was removed (%v)", store, err)
		}
	}
}

func TestTeardown_RemovesAMirrorNoKeptInstanceBorrows(t *testing.T) {
	f, old := restoredOnce(t)
	if out := teardown(t, f, keptScan()); out != "" {
		t.Fatalf("printed %q, want nothing", out)
	}
	if _, err := os.Lstat(f.mdir()); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(f.mdir())
		t.Fatalf("mirrors/ survived with %v (%v); the retired store was %s", entries, err, old)
	}
}

func TestTeardown_KeepsAMirrorAKeptInstanceBorrows(t *testing.T) {
	for _, c := range []struct {
		name string
		line func(f fixture, retired string) string
	}{
		{"through the stable path", func(f fixture, _ string) string {
			return filepath.Join(StablePath(f.home, f.root, "app"), "objects")
		}},
		{"by a retired store directly", func(f fixture, retired string) string {
			return filepath.Join(f.mdir(), retired, "objects")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, retired := restoredOnce(t)
			current := f.linkTarget(t)
			clone := keptBorrower(t, c.line(f, retired))
			out := teardown(t, f, keptScan(clone))
			want := "kept the mirror at " + StablePath(f.home, f.root, "app") + ": a kept instance borrows from it"
			if !strings.Contains(out, want) {
				t.Fatalf("printed %q, want %q", out, want)
			}
			requireMirrorKept(t, f, retired, current)
		})
	}
	t.Run("through the stable path with the link gone", func(t *testing.T) {
		f := built(t)
		store := f.linkTarget(t)
		if err := os.Remove(StablePath(f.home, f.root, "app")); err != nil {
			t.Fatal(err)
		}
		clone := keptBorrower(t, filepath.Join(StablePath(f.home, f.root, "app"), "objects"))
		if out := teardown(t, f, keptScan(clone)); !strings.Contains(out, "a kept instance borrows from it") {
			t.Fatalf("printed %q, want the mirror kept for its borrower", out)
		}
		if _, err := os.Lstat(filepath.Join(f.mdir(), store)); err != nil {
			t.Fatalf("the store was removed (%v)", err)
		}
	})
}

func TestTeardown_KeepsAMirrorWhoseLockIsHeld(t *testing.T) {
	f, retired := restoredOnce(t)
	release, err := filelock.TryFileLock(LockPath(f.home, f.root, "app"), "another command")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	scanned := false
	out := teardown(t, f, func() ([]string, error) { scanned = true; return nil, nil })
	want := "kept the mirror at " + StablePath(f.home, f.root, "app") + ": busy, another niwa command holds its lock"
	if !strings.Contains(out, want) {
		t.Fatalf("printed %q, want %q", out, want)
	}
	if scanned {
		t.Fatal("the scan ran with the lock held by another command")
	}
	requireMirrorKept(t, f, retired, f.linkTarget(t))
}

func TestTeardown_KeepsAMirrorWhenKeptCantBeRead(t *testing.T) {
	f, retired := restoredOnce(t)
	out := teardown(t, f, func() ([]string, error) { return nil, errors.New("kept/ can't be read") })
	want := "kept the mirror at " + StablePath(f.home, f.root, "app") + ": kept instances couldn't be read"
	if !strings.Contains(out, want) {
		t.Fatalf("printed %q, want %q", out, want)
	}
	requireMirrorKept(t, f, retired, f.linkTarget(t))
}

func TestTeardown_LeavesAStoreNoRecordNames(t *testing.T) {
	f := built(t)
	unrecorded := filepath.Join(f.mdir(), ".store", "app-20200101T000000Z-0000dead")
	writeFile(t, filepath.Join(unrecorded, "objects", "keep"), "keep")
	out := teardown(t, f, keptScan())
	if want := "left " + unrecorded + ": no mirror record names it"; !strings.Contains(out, want) {
		t.Fatalf("printed %q, want %q", out, want)
	}
	if _, err := os.Lstat(filepath.Join(unrecorded, "objects", "keep")); err != nil {
		t.Fatalf("the unrecorded store was removed (%v)", err)
	}
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); !os.IsNotExist(err) {
		t.Fatalf("the recorded mirror stayed (%v)", err)
	}
}

func TestTeardown_AppAndAppExtraDoNotCollide(t *testing.T) {
	f := built(t)
	appStore := f.linkTarget(t)
	extra := Repo{ID: "app-extra", URL: f.repo().URL}
	if res := Seed(context.Background(), f.applyOpts(), extra); res.Outcome != OutcomeRestored {
		t.Fatalf("seeding app-extra = %+v", res)
	}
	extraStable := StablePath(f.home, f.root, "app-extra")
	extraStore, err := os.Readlink(extraStable)
	if err != nil {
		t.Fatal(err)
	}
	clone := keptBorrower(t, filepath.Join(extraStable, "objects"))

	out := teardown(t, f, keptScan(clone))
	if want := "kept the mirror at " + extraStable + ": a kept instance borrows from it"; !strings.Contains(out, want) {
		t.Fatalf("printed %q, want %q", out, want)
	}
	if strings.Contains(out, StablePath(f.home, f.root, "app")+":") {
		t.Fatalf("printed %q, want nothing about app", out)
	}
	if _, err := os.Lstat(filepath.Join(f.mdir(), appStore)); !os.IsNotExist(err) {
		t.Fatalf("app's store stayed (%v)", err)
	}
	if _, err := os.Lstat(StablePath(f.home, f.root, "app")); !os.IsNotExist(err) {
		t.Fatalf("app's link stayed (%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(f.mdir(), extraStore, "objects")); err != nil {
		t.Fatalf("app-extra's store was removed with app's (%v)", err)
	}
	for _, path := range []string{statePath(f.mdir(), "app-extra"), storesPath(f.mdir(), "app-extra")} {
		if _, err := os.Lstat(path); err != nil {
			t.Fatalf("%s was removed (%v)", path, err)
		}
	}
}

func TestTeardown_NoMirrorsDirectoryDoesNothing(t *testing.T) {
	f := newFixture(t)
	if out := teardown(t, f, keptScan()); out != "" {
		t.Fatalf("printed %q, want nothing", out)
	}
	emptyDir(t, f.home)
}
