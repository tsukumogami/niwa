package hostdir_test

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// walkLayout is a workspace root and home for the clone walk tests.
type walkLayout struct {
	base, home, root, kept string
}

func newWalkLayout(t *testing.T) walkLayout {
	t.Helper()
	base := t.TempDir()
	l := walkLayout{base: base, home: filepath.Join(base, "home"), root: filepath.Join(base, "ws")}
	l.kept = filepath.Join(hostdir.WorkspaceDir(l.home, l.root), hostdir.KeptDirName)
	mkdirAll(t, filepath.Join(l.root, ".niwa"))
	return l
}

func mkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	mkdirAll(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// clone makes a directory with a .git/objects/info/ under it, as a clone
// has, and returns it.
func clone(t *testing.T, dir string) string {
	t.Helper()
	mkdirAll(t, filepath.Join(dir, ".git", "objects", "info"))
	return dir
}

// chmodBack sets path's mode and puts 0755 back when the test ends, so the
// temporary directory can be removed.
func chmodBack(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func alternatesOf(c string) string { return filepath.Join(c, ".git", "objects", "info", "alternates") }

func TestClones_ListsEveryCloneOnDisk(t *testing.T) {
	l := newWalkLayout(t)
	writeFile(t, filepath.Join(l.root, "live", ".niwa", "instance.json"), "{}")
	live := clone(t, filepath.Join(l.root, "live", "tools", "app"))
	unnamed := clone(t, filepath.Join(l.root, "live", "other", "not-in-config"))
	half := clone(t, filepath.Join(l.root, "half", "tools", "app"))
	noGit := filepath.Join(l.root, "half", "tools", "no-git")
	mkdirAll(t, noGit)
	kept := clone(t, filepath.Join(l.kept, "old", "tools", "app"))
	// Skipped: the root's .niwa, dot-named groups and repos, and regular
	// files at the instance and group levels.
	clone(t, filepath.Join(l.root, ".niwa", "tools", "app"))
	clone(t, filepath.Join(l.root, "live", ".niwa", "worktrees", "app-1"))
	clone(t, filepath.Join(l.root, "live", "tools", ".hidden"))
	writeFile(t, filepath.Join(l.root, "CLAUDE.md"), "x")
	writeFile(t, filepath.Join(l.root, "live", "CLAUDE.md"), "x")
	writeFile(t, filepath.Join(l.root, "live", "tools", "notes.txt"), "x")

	want := []string{half, noGit, unnamed, live, kept}
	sort.Strings(want)
	if got := hostdir.Clones(l.home, l.root); !reflect.DeepEqual(got, want) {
		t.Fatalf("Clones = %v\nwant %v", got, want)
	}
	got, err := hostdir.StrictClones(l.home, l.root)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StrictClones = %v\nwant %v", got, want)
	}
}

func TestStrictClones_FailsWhereClonesDoesNot(t *testing.T) {
	type row struct {
		name   string
		asRoot bool // the row needs a permission check root bypasses
		spoil  func(t *testing.T, l walkLayout, c string)
	}
	setAlternates := func(name, body string) func(t *testing.T, l walkLayout, c string) {
		return func(t *testing.T, l walkLayout, c string) {
			writeFile(t, filepath.Join(c, ".git", "objects", "info", name), body)
		}
	}
	rows := []row{
		{"an unreadable root", true, func(t *testing.T, l walkLayout, c string) { chmodBack(t, l.root, 0o000) }},
		{"an unreadable kept directory", true, func(t *testing.T, l walkLayout, c string) {
			mkdirAll(t, filepath.Join(l.kept, "old"))
			chmodBack(t, l.kept, 0o000)
		}},
		{"an unreadable instance", true, func(t *testing.T, l walkLayout, c string) {
			chmodBack(t, filepath.Join(l.root, "live"), 0o000)
		}},
		{"an unreadable group", true, func(t *testing.T, l walkLayout, c string) {
			chmodBack(t, filepath.Join(l.root, "live", "tools"), 0o000)
		}},
		{"kept is a symlink", false, func(t *testing.T, l walkLayout, c string) {
			outside := filepath.Join(l.base, "outside-kept")
			mkdirAll(t, outside)
			mkdirAll(t, filepath.Dir(l.kept))
			if err := os.Symlink(outside, l.kept); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink to a directory at the instance level", false, func(t *testing.T, l walkLayout, c string) {
			outside := filepath.Join(l.base, "outside")
			mkdirAll(t, outside)
			if err := os.Symlink(outside, filepath.Join(l.root, "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlink to a directory at the group level", false, func(t *testing.T, l walkLayout, c string) {
			outside := filepath.Join(l.base, "outside")
			mkdirAll(t, outside)
			if err := os.Symlink(outside, filepath.Join(l.root, "live", "linked")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a fifo at the instance level", false, func(t *testing.T, l walkLayout, c string) {
			if err := unix.Mkfifo(filepath.Join(l.root, "pipe"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"a fifo at the group level", false, func(t *testing.T, l walkLayout, c string) {
			if err := unix.Mkfifo(filepath.Join(l.root, "live", "pipe"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{".git is a symlink", false, func(t *testing.T, l walkLayout, c string) {
			other := clone(t, filepath.Join(l.base, "elsewhere"))
			bad := filepath.Join(l.root, "live", "tools", "linked-git")
			mkdirAll(t, bad)
			if err := os.Symlink(filepath.Join(other, ".git"), filepath.Join(bad, ".git")); err != nil {
				t.Fatal(err)
			}
		}},
		{".git is a gitfile", false, func(t *testing.T, l walkLayout, c string) {
			writeFile(t, filepath.Join(l.root, "live", "tools", "worktree", ".git"), "gitdir: /elsewhere/.git/worktrees/x\n")
		}},
		{"a symlinked .git/objects", false, func(t *testing.T, l walkLayout, c string) {
			other := clone(t, filepath.Join(l.base, "elsewhere"))
			bad := filepath.Join(l.root, "live", "tools", "linked-objects", ".git")
			mkdirAll(t, bad)
			if err := os.Symlink(filepath.Join(other, ".git", "objects"), filepath.Join(bad, "objects")); err != nil {
				t.Fatal(err)
			}
		}},
		{"a symlinked .git/objects/info", false, func(t *testing.T, l walkLayout, c string) {
			other := clone(t, filepath.Join(l.base, "elsewhere"))
			bad := filepath.Join(l.root, "live", "tools", "linked-info", ".git", "objects")
			mkdirAll(t, bad)
			if err := os.Symlink(filepath.Join(other, ".git", "objects", "info"), filepath.Join(bad, "info")); err != nil {
				t.Fatal(err)
			}
		}},
		{"an alternates file that is a fifo", false, func(t *testing.T, l walkLayout, c string) {
			_ = os.Remove(alternatesOf(c))
			if err := unix.Mkfifo(alternatesOf(c), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"an alternates file that is a symlink", false, func(t *testing.T, l walkLayout, c string) {
			target := filepath.Join(l.base, "alternates-elsewhere")
			writeFile(t, target, "/x/objects\n")
			_ = os.Remove(alternatesOf(c))
			if err := os.Symlink(target, alternatesOf(c)); err != nil {
				t.Fatal(err)
			}
		}},
		{"an alternates file over 64 KiB", false, setAlternates("alternates", "/x/objects\n"+strings.Repeat("#", 64<<10))},
		{"a quoted line", false, setAlternates("alternates", "\"/x/objects\"\n")},
		{"a line with a NUL", false, setAlternates("alternates", "/x/obj\x00ects\n")},
		{"a line with a control byte", false, setAlternates("alternates", "/x/obj\x1bects\n")},
		{"a file that isn't UTF-8", false, setAlternates("alternates", "/x/obj\xffects\n")},
		{"a quoted line in a leftover alternates.niwa-dissociate", false, setAlternates("alternates.niwa-dissociate", "\"/x/objects\"\n")},
		{"a directory removed between listing and reading", false, func(t *testing.T, l walkLayout, c string) {
			group := filepath.Join(l.root, "live", "tools")
			t.Cleanup(hostdir.SetBeforeRead(func(dir string) {
				if dir == group {
					_ = os.RemoveAll(group)
				}
			}))
		}},
		{"a clone removed between listing and reading", false, func(t *testing.T, l walkLayout, c string) {
			t.Cleanup(hostdir.SetBeforeRead(func(dir string) {
				if dir == c {
					_ = os.RemoveAll(c)
				}
			}))
		}},
	}
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			if r.asRoot && os.Geteuid() == 0 {
				t.Skip("root reads a mode-000 directory")
			}
			l := newWalkLayout(t)
			c := clone(t, filepath.Join(l.root, "live", "tools", "app"))
			writeFile(t, alternatesOf(c), "# a comment\n\n/x/objects\n")
			if _, err := hostdir.StrictClones(l.home, l.root); err != nil {
				t.Fatalf("the layout before the change already fails: %v", err)
			}
			r.spoil(t, l, c)
			got, err := hostdir.StrictClones(l.home, l.root)
			if err == nil {
				t.Fatalf("StrictClones = %v with no error", got)
			}
			t.Logf("StrictClones: %v", err)
			// The lenient walk has no error to give: it skips what it can't
			// read.
			_ = hostdir.Clones(l.home, l.root)
		})
	}
}

func TestStrictKeptClones(t *testing.T) {
	l := newWalkLayout(t)
	clone(t, filepath.Join(l.root, "live", "tools", "app"))
	keyDir := hostdir.WorkspaceDir(l.home, l.root)

	got, err := hostdir.StrictKeptClones(keyDir)
	if err != nil || got != nil {
		t.Fatalf("StrictKeptClones with no kept/ = %v, %v; want nothing", got, err)
	}

	kept := clone(t, filepath.Join(l.kept, "old", "tools", "app"))
	got, err = hostdir.StrictKeptClones(keyDir)
	if err != nil || !reflect.DeepEqual(got, []string{kept}) {
		t.Fatalf("StrictKeptClones = %v, %v; want only %s", got, err, kept)
	}

	if os.Geteuid() != 0 {
		chmodBack(t, l.kept, 0o000)
		if got, err := hostdir.StrictKeptClones(keyDir); err == nil {
			t.Fatalf("an unreadable kept/ gave %v with no error", got)
		}
	}
}

// pinLayout is a mirrors directory with two stores and a stable link to the
// first, the way internal/mirror lays them out.
type pinLayout struct {
	base, stable, linked, retired string
}

func newPinLayout(t *testing.T) pinLayout {
	t.Helper()
	base := t.TempDir()
	mdir := filepath.Join(base, "mirrors")
	p := pinLayout{
		base:    base,
		stable:  filepath.Join(mdir, "app"),
		linked:  filepath.Join(mdir, ".store", "app-20260101T000000Z-0000000a"),
		retired: filepath.Join(mdir, ".store", "app-20250101T000000Z-0000000b"),
	}
	mkdirAll(t, filepath.Join(p.linked, "objects", "pack"))
	mkdirAll(t, filepath.Join(p.retired, "objects", "pack"))
	if err := os.Symlink(filepath.Join(".store", filepath.Base(p.linked)), p.stable); err != nil {
		t.Fatal(err)
	}
	return p
}

// borrower makes a clone whose file name (alternates or the dissociate copy)
// holds body.
func (p pinLayout) borrower(t *testing.T, name, file, body string) string {
	t.Helper()
	c := clone(t, filepath.Join(p.base, "ws", "inst", "tools", name))
	writeFile(t, filepath.Join(c, ".git", "objects", "info", file), body)
	return c
}

func requirePins(t *testing.T, clones []string, store string, want bool) {
	t.Helper()
	got, err := hostdir.Pins(clones, store)
	if err != nil {
		t.Fatalf("Pins(%v, %s): %v", clones, store, err)
	}
	if got != want {
		t.Fatalf("Pins(%v, %s) = %v, want %v", clones, store, got, want)
	}
}

func TestPins(t *testing.T) {
	t.Run("by os.SameFile on its objects through the stable link", func(t *testing.T) {
		p := newPinLayout(t)
		// Another link to the stable path: the line's spelling shares no
		// prefix with the store's, so only SameFile can match it.
		other := filepath.Join(p.base, "another-spelling")
		if err := os.Symlink(p.stable, other); err != nil {
			t.Fatal(err)
		}
		c := p.borrower(t, "a", "alternates", filepath.Join(other, "objects")+"\n")
		requirePins(t, []string{c}, p.linked, true)
		requirePins(t, []string{c}, p.retired, false)
	})
	t.Run("by a line naming the stable path", func(t *testing.T) {
		p := newPinLayout(t)
		c := p.borrower(t, "a", "alternates", filepath.Join(p.stable, "objects")+"\n")
		requirePins(t, []string{c}, p.linked, true)
		requirePins(t, []string{c}, p.retired, false)
	})
	t.Run("by a line naming the .store directory directly", func(t *testing.T) {
		p := newPinLayout(t)
		c := p.borrower(t, "a", "alternates", filepath.Join(p.retired, "objects")+"\n")
		requirePins(t, []string{c}, p.retired, true)
		requirePins(t, []string{c}, p.linked, false)
	})
	t.Run("by a relative line", func(t *testing.T) {
		p := newPinLayout(t)
		c := clone(t, filepath.Join(p.base, "ws", "inst", "tools", "a"))
		rel, err := filepath.Rel(filepath.Join(c, ".git", "objects"), filepath.Join(p.retired, "objects"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, alternatesOf(c), rel+"\n")
		requirePins(t, []string{c}, p.retired, true)
	})
	t.Run("by a leftover alternates.niwa-dissociate alone", func(t *testing.T) {
		p := newPinLayout(t)
		c := p.borrower(t, "a", "alternates.niwa-dissociate", filepath.Join(p.retired, "objects")+"\n")
		requirePins(t, []string{c}, p.retired, true)
	})
	t.Run("blank and comment lines, a missing file and a missing path pin nothing", func(t *testing.T) {
		p := newPinLayout(t)
		skipped := p.borrower(t, "a", "alternates",
			"\n   \n# "+filepath.Join(p.retired, "objects")+"\n"+filepath.Join(p.base, "gone", "objects")+"\n")
		none := clone(t, filepath.Join(p.base, "ws", "inst", "tools", "b"))
		requirePins(t, []string{skipped, none}, p.retired, false)
		requirePins(t, []string{skipped, none}, p.linked, false)
	})
	t.Run("a store that doesn't exist is pinned by nothing", func(t *testing.T) {
		p := newPinLayout(t)
		c := p.borrower(t, "a", "alternates", filepath.Join(p.retired, "objects")+"\n")
		requirePins(t, []string{c}, filepath.Join(filepath.Dir(p.retired), "app-20240101T000000Z-0000000c"), false)
	})
	t.Run("a line the strict read refuses is an error", func(t *testing.T) {
		p := newPinLayout(t)
		c := p.borrower(t, "a", "alternates", "\""+filepath.Join(p.retired, "objects")+"\"\n")
		if got, err := hostdir.Pins([]string{c}, p.retired); err == nil {
			t.Fatalf("Pins = %v with no error on a quoted line", got)
		}
	})
}
