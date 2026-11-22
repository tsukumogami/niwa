package removal

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func newScratchFixture(t *testing.T) (home, root string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home, root = filepath.Join(base, "home"), filepath.Join(base, "ws")
	for _, d := range []string{home, root} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home, root
}

func TestScratchIsFreshAndPrivate(t *testing.T) {
	home, root := newScratchFixture(t)
	a, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir == b.Dir {
		t.Fatal("two passes share a scratch directory")
	}
	for _, d := range []string{a.Dir, filepath.Dir(a.Dir), filepath.Join(a.Dir, scratchNet)} {
		info, err := os.Lstat(d)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Errorf("%s has mode %o, want 700", d, info.Mode().Perm())
		}
	}
	a.Remove()
	if _, err := os.Lstat(a.Dir); !os.IsNotExist(err) {
		t.Fatalf("scratch directory survived Remove: %v", err)
	}
	b.Remove()
}

func TestScratchRefusesAWrongModeOrALink(t *testing.T) {
	home, root := newScratchFixture(t)
	s, err := NewScratch(home, root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Remove()

	if err := os.Chmod(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Use(scratchNet); err == nil {
		t.Error("Use accepted a pass directory with mode 755")
	}
	if err := os.Chmod(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Dir(s.Dir), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Use(scratchNet); err == nil {
		t.Error("Use accepted a scratch root with mode 750")
	}
	if err := os.Chmod(filepath.Dir(s.Dir), 0o700); err != nil {
		t.Fatal(err)
	}

	net := filepath.Join(s.Dir, scratchNet)
	if err := os.Remove(net); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), net); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Use(scratchNet); err == nil {
		t.Error("Use accepted a link in place of its empty directory")
	}
}

func TestAFailedScratchCheckKeepsAndRunsNothingFromIt(t *testing.T) {
	f := newFixture(t)
	p := f.policy(t)
	if err := os.Chmod(p.Env.Scratch.Dir, 0o777); err != nil {
		t.Fatal(err)
	}
	v := Evaluate(context.Background(), f.unit(), p)
	first, _ := v.First()
	if v.Kind != Keep || first.Name != CheckRepository {
		t.Fatalf("verdict %s %+v; want the repository check to keep", v.Kind, v.Checks)
	}
}

func TestAPassWithoutAScratchDirectoryStillReapsAUnitWithNoRepository(t *testing.T) {
	_, root := newScratchFixture(t)
	instance := filepath.Join(root, "ws-1")
	if err := os.MkdirAll(filepath.Join(instance, ".niwa"), 0o755); err != nil {
		t.Fatal(err)
	}
	e := NewEnv(root, "relative-home", os.Getenv("PATH"))
	if e.Scratch != nil || e.ScratchErr == nil {
		t.Fatal("a scratch directory was made under a relative home")
	}
	v := Evaluate(context.Background(), Unit{InstancePath: instance, Liveness: LivenessGone}, Policy{Env: e})
	if v.Kind != Reap {
		t.Fatalf("verdict %s %+v; want reap", v.Kind, v.Checks)
	}
}
