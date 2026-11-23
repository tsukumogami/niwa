package removal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestScratchRemovesOnlyWhatAnInterruptedPassLeft(t *testing.T) {
	home, root := newScratchFixture(t)
	live, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Remove()
	scratchRoot := filepath.Dir(live.Dir)
	old := time.Now().Add(-time.Hour)

	// What an interrupted pass left: its directory and a store in it, no
	// lock held.
	left := filepath.Join(scratchRoot, "0000000000000001")
	writeFile(t, filepath.Join(left, "store-1", "objects", "x"), "x")
	for _, d := range []string{left, filepath.Join(left, "store-1")} {
		if err := os.Chmod(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// A directory with another mode, and one too new to judge.
	wrongMode := filepath.Join(scratchRoot, "0000000000000002")
	young := filepath.Join(scratchRoot, "0000000000000003")
	for _, d := range []string{wrongMode, young} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(wrongMode, 0o755); err != nil {
		t.Fatal(err)
	}
	// A link to a directory elsewhere, which must be neither followed nor
	// emptied.
	target := t.TempDir()
	writeFile(t, filepath.Join(target, "keep"), "k")
	link := filepath.Join(scratchRoot, "0000000000000004")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{left, wrongMode, live.Dir} {
		if err := os.Chtimes(d, old, old); err != nil {
			t.Fatal(err)
		}
	}

	next, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Remove()

	if _, err := os.Lstat(left); !os.IsNotExist(err) {
		t.Errorf("an interrupted pass's directory survived: %v", err)
	}
	for _, d := range []string{live.Dir, wrongMode, young, link, filepath.Join(target, "keep")} {
		if _, err := os.Lstat(d); err != nil {
			t.Errorf("%s was removed: %v", d, err)
		}
	}
}

func TestScratchHoldsItsDirectoryLocked(t *testing.T) {
	home, root := newScratchFixture(t)
	s, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockDir(s.Dir); err == nil {
		t.Fatal("a live pass's directory could be locked again")
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, scratchHelperRead)); err != nil {
		t.Fatalf("no directory for the credential-helper read: %v", err)
	}
	s.Remove()
	if _, err := os.Lstat(s.Dir); !os.IsNotExist(err) {
		t.Fatalf("pass directory survived Remove: %v", err)
	}
}
