package removal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// scratchDirName is the scratch directory under a workspace's key directory.
const scratchDirName = "scratch"

// scratchNet is the empty directory a network call in no repository runs
// from.
const scratchNet = "net"

// Scratch is one pass's scratch directory,
// ~/.niwa/w-<key>/scratch/<pass>/, made fresh with mode 0700 and removed
// when the pass ends. Nothing runs from it, or from a directory under it,
// until it and its parent pass the owner and mode check, which is made
// before each use rather than once: the agent runs as the person, so a
// check at creation says nothing about the directory seconds later.
type Scratch struct {
	// Dir is this pass's directory.
	Dir string
	// root is the scratch directory Dir sits in.
	root string
}

// NewScratch makes a fresh pass directory under home's workspace key
// directory for root, with its empty directories.
func NewScratch(home, root string) (*Scratch, error) {
	if home == "" || !filepath.IsAbs(home) {
		return nil, errors.New("no absolute home directory")
	}
	niwaDir := filepath.Join(home, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", niwaDir, err)
	}
	keyDir := hostdir.WorkspaceDir(home, root)
	if err := hostdir.CreatePrivateDir(keyDir); err != nil {
		return nil, err
	}
	scratchRoot := filepath.Join(keyDir, scratchDirName)
	if err := hostdir.CreatePrivateDir(scratchRoot); err != nil {
		return nil, err
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, fmt.Errorf("naming the pass directory: %w", err)
	}
	s := &Scratch{Dir: filepath.Join(scratchRoot, hex.EncodeToString(b[:])), root: scratchRoot}
	// Mkdir rather than MkdirAll: the pass directory must be new, never one
	// found already at the path.
	if err := os.Mkdir(s.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", s.Dir, err)
	}
	for _, sub := range []string{scratchNet} {
		if err := os.Mkdir(filepath.Join(s.Dir, sub), 0o700); err != nil {
			s.Remove()
			return nil, fmt.Errorf("creating %s: %w", filepath.Join(s.Dir, sub), err)
		}
	}
	// The mode is set explicitly, since a umask can only narrow it and a
	// narrowed one would fail every later check.
	for _, d := range []string{s.Dir, filepath.Join(s.Dir, scratchNet)} {
		if err := os.Chmod(d, 0o700); err != nil {
			s.Remove()
			return nil, fmt.Errorf("setting the mode of %s: %w", d, err)
		}
	}
	if err := s.check(); err != nil {
		s.Remove()
		return nil, err
	}
	return s, nil
}

// Use checks the pass directory, its parent and the named empty directory
// under it, owner, mode and that it is still empty, and returns that
// directory's path. An entry found there, a .git among them, is something
// niwa didn't put there, and a program run from the directory could read
// it.
func (s *Scratch) Use(name string) (string, error) {
	if err := s.check(); err != nil {
		return "", err
	}
	dir := filepath.Join(s.Dir, name)
	if err := checkPrivateDir(dir); err != nil {
		return "", err
	}
	if err := checkEmptyDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

// checkEmptyDir refuses a directory that holds any entry.
func checkEmptyDir(dir string) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return fmt.Errorf("scratch directory check: %w", err)
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if len(names) > 0 {
		return fmt.Errorf("scratch directory check: %s is not empty", dir)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("scratch directory check: %w", err)
	}
	return nil
}

// check is the owner and mode check on the pass directory and its parent.
func (s *Scratch) check() error {
	for _, d := range []string{s.root, s.Dir} {
		if err := checkPrivateDir(d); err != nil {
			return err
		}
	}
	return nil
}

// checkPrivateDir refuses anything at dir but a real directory, not a link,
// owned by the person, with mode exactly 0700.
func checkPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("scratch directory check: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("scratch directory check: %s is not a directory", dir)
	}
	if !hostdir.OwnedByUser(info) {
		return fmt.Errorf("scratch directory check: %s is not owned by the current user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("scratch directory check: %s has mode %o, not 700", dir, info.Mode().Perm())
	}
	return nil
}

// Remove deletes the pass directory. Nothing in it is anyone's work: it
// holds only the empty directories programs ran from.
func (s *Scratch) Remove() {
	if s == nil {
		return
	}
	// The directory may have been narrowed or swapped; RemoveAll doesn't
	// follow a link at the top, and a failure here leaves a directory for a
	// later pass rather than an error for this one.
	_ = os.RemoveAll(s.Dir)
}
