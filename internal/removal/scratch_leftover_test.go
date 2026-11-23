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

func TestScratchRemovesOnlyPassNamedDirectories(t *testing.T) {
	home, root := newScratchFixture(t)
	first, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	scratchRoot := filepath.Dir(first.Dir)
	first.Remove()
	old := time.Now().Add(-time.Hour)
	// Private, old and unlocked, but not named as a pass names its
	// directory: never removed.
	other := filepath.Join(scratchRoot, "not-a-pass")
	writeFile(t, filepath.Join(other, "x"), "x")
	if err := os.Chmod(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(other, old, old); err != nil {
		t.Fatal(err)
	}
	next, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer next.Remove()
	if _, err := os.Lstat(filepath.Join(other, "x")); err != nil {
		t.Fatalf("a directory not named as a pass's was removed: %v", err)
	}
}

func TestScratchNeverRemovesThroughALinkedRoot(t *testing.T) {
	home, root := newScratchFixture(t)
	s, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()
	// The scratch root swapped for a link to a directory holding something
	// that looks like a leftover pass directory.
	elsewhere := t.TempDir()
	victim := filepath.Join(elsewhere, "0000000000000009")
	writeFile(t, filepath.Join(victim, "x"), "x")
	old := time.Now().Add(-time.Hour)
	if err := os.Chmod(victim, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(victim, old, old); err != nil {
		t.Fatal(err)
	}
	moved := s.root + ".moved"
	if err := os.Rename(s.root, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, s.root); err != nil {
		t.Fatal(err)
	}
	s.removeLeftovers()
	if _, err := os.Lstat(filepath.Join(victim, "x")); err != nil {
		t.Fatalf("leftover removal followed a linked scratch root: %v", err)
	}
	if err := os.Remove(s.root); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(moved, s.root); err != nil {
		t.Fatal(err)
	}
}

func TestStoreLocalUseComparesItsConfig(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	st, err := p.Env.newStore()
	if err != nil {
		t.Fatal(err)
	}
	defer st.remove()
	if _, err := st.use(); err != nil {
		t.Fatalf("a fresh store failed its check: %v", err)
	}
	writeFile(t, filepath.Join(st.dir, "config"), string(storeConfig)+"[merge \"x\"]\n\tdriver = touch /tmp/x\n")
	if _, err := st.use(); err == nil {
		t.Fatal("a local call ran against a store whose config was rewritten")
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
