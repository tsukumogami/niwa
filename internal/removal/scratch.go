package removal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/tsukumogami/niwa/internal/hostdir"
)

// scratchDirName is the scratch directory under a workspace's key directory.
const scratchDirName = "scratch"

// scratchNet is the empty directory a network call runs from, in no
// repository or against a scratch store.
const scratchNet = "net"

// scratchHelperRead is the empty directory the credential-helper read runs
// from.
const scratchHelperRead = "helper-read"

// scratchClaude is the empty directory every claude call on the removal path
// runs from, so no instance's project settings or hooks apply to it.
const scratchClaude = "claude"

// scratchVersion is the empty directory the pass's one read of git's
// version runs from.
const scratchVersion = "version"

// leftoverAge is how long a pass directory must have gone unchanged before
// another pass takes it for one an interrupted pass left. A younger one may
// belong to a pass that made it and hasn't locked it yet.
var leftoverAge = 2 * time.Minute

// Scratch is one pass's scratch directory,
// ~/.niwa/w-<key>/scratch/<pass>/, made fresh with mode 0700 and removed
// when the pass ends. Nothing runs from it, or from a directory under it,
// until it and its parent pass the owner and mode check, which is made
// before each use rather than once: the agent runs as the person, so a
// check at creation says nothing about the directory seconds later.
//
// The pass holds its directory under an exclusive flock for as long as it
// runs, so another pass can tell a live pass's directory from one an
// interrupted pass left, and removes only the latter.
type Scratch struct {
	// Dir is this pass's directory.
	Dir string
	// root is the scratch directory Dir sits in.
	root string
	// chain is every directory from below the home directory down to root's
	// parent: each must be a real directory the person owns.
	chain []string
	// lock is Dir, open and flocked while the pass runs; nil for a Scratch
	// made by hand.
	lock *os.File

	mu       sync.Mutex
	storeSeq int
}

// NewScratch makes a fresh pass directory under home's workspace key
// directory for root, with its empty directories, and removes any pass
// directory an interrupted pass left there.
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
	s := &Scratch{
		Dir:   filepath.Join(scratchRoot, hex.EncodeToString(b[:])),
		root:  scratchRoot,
		chain: []string{niwaDir, keyDir},
	}
	// Mkdir rather than MkdirAll: the pass directory must be new, never one
	// found already at the path.
	if err := os.Mkdir(s.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating %s: %w", s.Dir, err)
	}
	lock, err := lockDir(s.Dir)
	if err != nil {
		_ = os.RemoveAll(s.Dir)
		return nil, fmt.Errorf("locking %s: %w", s.Dir, err)
	}
	s.lock = lock
	subs := []string{scratchNet, scratchHelperRead, scratchClaude, scratchVersion}
	for _, sub := range subs {
		if err := os.Mkdir(filepath.Join(s.Dir, sub), 0o700); err != nil {
			s.Remove()
			return nil, fmt.Errorf("creating %s: %w", filepath.Join(s.Dir, sub), err)
		}
	}
	// The mode is set explicitly, since a umask can only narrow it and a
	// narrowed one would fail every later check.
	for _, d := range append([]string{s.Dir}, joinAll(s.Dir, subs)...) {
		if err := os.Chmod(d, 0o700); err != nil {
			s.Remove()
			return nil, fmt.Errorf("setting the mode of %s: %w", d, err)
		}
	}
	if err := s.check(); err != nil {
		s.Remove()
		return nil, err
	}
	s.removeLeftovers()
	return s, nil
}

func joinAll(dir string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(dir, n)
	}
	return out
}

// lockDir opens dir, refusing a link, and takes an exclusive flock on it
// without waiting. The lock lasts until the returned file is closed.
func lockDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// removeLeftovers removes every pass directory in the scratch root that an
// interrupted pass left: one that passes the owner and mode check, hasn't
// changed for leftoverAge, and whose flock can be taken, which no live pass
// would allow. Its scratch stores go with it. A directory that fails any of
// these is left for a later pass, and nothing here fails this one.
func (s *Scratch) removeLeftovers() {
	// The root is opened without following a link and must be the one the
	// pass checked; one swapped for a link since is left alone.
	rootInfo, err := os.Lstat(s.root)
	if err != nil || !rootInfo.IsDir() {
		return
	}
	fd, err := unix.Open(s.root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	f := os.NewFile(uintptr(fd), s.root)
	opened, err := f.Stat()
	if err != nil || !os.SameFile(opened, rootInfo) {
		f.Close()
		return
	}
	names, _ := f.Readdirnames(-1)
	f.Close()
	for _, name := range names {
		p := filepath.Join(s.root, name)
		if p == s.Dir || !isPassName(name) {
			continue
		}
		s.removeLeftover(p, rootInfo)
	}
}

// isPassName reports whether name is how a pass names its directory: 16
// lowercase hex digits.
func isPassName(name string) bool {
	if len(name) != 16 {
		return false
	}
	for _, c := range name {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *Scratch) removeLeftover(p string, rootInfo os.FileInfo) {
	if err := checkPrivateDir(p); err != nil {
		return
	}
	info, err := os.Lstat(p)
	if err != nil || time.Since(info.ModTime()) < leftoverAge {
		return
	}
	lock, err := lockDir(p)
	if err != nil {
		// A live pass holds it, or it is no longer a directory.
		return
	}
	defer lock.Close()
	// The directory opened must be the one checked; a swap in between is
	// left alone.
	if held, err := lock.Stat(); err != nil || !os.SameFile(held, info) {
		return
	}
	// The root is checked again right before the removal, which walks the
	// path: still a real directory, the one listed.
	if now, err := os.Lstat(s.root); err != nil || !os.SameFile(now, rootInfo) {
		return
	}
	// RemoveAll never follows a link, at the top or below it.
	_ = os.RemoveAll(p)
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

// check is the owner and mode check on the pass directory and its parent,
// and the owner check on every directory from below the home directory down
// to them: none may be a link.
func (s *Scratch) check() error {
	for _, d := range s.chain {
		info, err := os.Lstat(d)
		switch {
		case err != nil:
			return fmt.Errorf("scratch directory check: %w", err)
		case !info.IsDir():
			return fmt.Errorf("scratch directory check: %s is not a directory", d)
		case !hostdir.OwnedByUser(info):
			return fmt.Errorf("scratch directory check: %s is not owned by the current user", d)
		}
	}
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

// Remove deletes the pass directory, then lets its lock go. Nothing in it is
// anyone's work: it holds only the empty directories programs ran from and
// scratch stores.
func (s *Scratch) Remove() {
	if s == nil {
		return
	}
	// The directory may have been narrowed or swapped; RemoveAll doesn't
	// follow a link at the top, and a failure here leaves a directory for a
	// later pass rather than an error for this one.
	_ = os.RemoveAll(s.Dir)
	if s.lock != nil {
		s.lock.Close()
		s.lock = nil
	}
}
