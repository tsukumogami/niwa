// Package removaltest holds the setup the swap scenarios share: a directory
// outside the instance whose listing, sizes and hashes are recorded, and a
// hook for the removal walk that swaps a directory for a link to it between
// the walk's read of the entry's type and its open. Any package's tests can
// use it; it imports nothing from niwa, so the removal package's own tests
// can too. The hook goes in with removal.SetBeforeOpenHook.
package removaltest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

// Outside is a directory outside the instance holding a marker file and a
// nested file, with what was there when it was made.
type Outside struct {
	Dir    string
	Marker string
	before string
}

// NewOutside makes the outside directory under a fresh temporary directory
// and records its listing, sizes and hashes.
func NewOutside(t testing.TB) *Outside {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	o := &Outside{Dir: filepath.Join(base, "outside")}
	o.Marker = filepath.Join(o.Dir, "MARKER")
	for name, body := range map[string]string{
		"MARKER":          "do not delete\n",
		"a/b/file.txt":    "outside file\n",
		"deep/a/b/x.txt":  "outside deep file\n",
		"other/notes.txt": "notes\n",
	} {
		p := filepath.Join(o.Dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	o.before = o.listing(t)
	return o
}

// listing is every entry under the directory with its type, size and, for a
// file, the hash of its content, one per line in path order.
func (o *Outside) listing(t testing.TB) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(o.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(o.Dir, p)
		info, err := d.Info()
		if err != nil {
			return err
		}
		line := fmt.Sprintf("%s %s %d", rel, info.Mode().Type(), info.Size())
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			line += " " + hex.EncodeToString(sum[:])
		}
		lines = append(lines, line)
		return nil
	})
	if err != nil {
		t.Fatalf("listing %s: %v", o.Dir, err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// AssertUnchanged fails the test unless the directory's listing, sizes and
// hashes are what they were when it was made and the marker is there.
func (o *Outside) AssertUnchanged(t testing.TB) {
	t.Helper()
	if _, err := os.Stat(o.Marker); err != nil {
		t.Errorf("the outside marker is gone: %v", err)
	}
	if got := o.listing(t); got != o.before {
		t.Errorf("the outside directory changed:\nbefore:\n%s\nafter:\n%s", o.before, got)
	}
}

// Swap swaps Target for a symlink to LinkTo the first time the walk is about
// to open Target, renaming the directory aside to Target+".swapped" in the
// same parent, so it stays inside the instance.
type Swap struct {
	Target, LinkTo string

	mu    sync.Mutex
	fired bool
	err   error
}

// NewSwap is a swap of target for a link to linkTo.
func NewSwap(target, linkTo string) *Swap {
	return &Swap{Target: filepath.Clean(target), LinkTo: linkTo}
}

// Hook is the walk's hook: it swaps on the first call naming Target.
func (s *Swap) Hook(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fired || filepath.Clean(path) != s.Target {
		return
	}
	s.fired = true
	if err := os.Rename(s.Target, s.Target+".swapped"); err != nil {
		s.err = err
		return
	}
	s.err = os.Symlink(s.LinkTo, s.Target)
}

// AssertFired fails the test unless the swap ran, and ran cleanly.
func (s *Swap) AssertFired(t testing.TB) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.fired {
		t.Errorf("the walk never reached %s, so nothing was swapped", s.Target)
	}
	if s.err != nil {
		t.Errorf("swapping %s: %v", s.Target, s.err)
	}
}
