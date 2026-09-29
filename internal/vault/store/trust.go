package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// storeDir is a store directory that passed the trust checks. Every file
// operation goes through root, which is opened on the verified directory,
// so swapping the directory after the checks can't redirect a read or a
// write.
type storeDir struct {
	path string
	dir  *os.File
	root *os.Root
}

// openTrustedDir checks that path is a real directory (not a symlink)
// owned by the effective user, tightens group and other bits off it once
// ownership is confirmed, and opens a root on it. Any failed check
// returns an error wrapping ErrUnwritable.
func openTrustedDir(path string) (*storeDir, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnwritable, err)
	}
	if !fi.IsDir() {
		return nil, untrusted(path, "is not a directory")
	}
	if !ownedByEUID(fi) {
		return nil, untrusted(path, "is not owned by the current user")
	}

	dir, err := os.OpenFile(path, os.O_RDONLY|oNoFollow|oDirectory, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnwritable, err)
	}
	dfi, err := dir.Stat()
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("%w: %w", ErrUnwritable, err)
	}
	if !os.SameFile(fi, dfi) || !dfi.IsDir() || !ownedByEUID(dfi) {
		_ = dir.Close()
		return nil, untrusted(path, "changed while it was being checked")
	}
	if perm := dfi.Mode().Perm(); perm&0o077 != 0 {
		if err := dir.Chmod(perm & 0o700); err != nil {
			_ = dir.Close()
			return nil, fmt.Errorf("%w: tightening the mode of %s: %w", ErrUnwritable, path, err)
		}
	}

	// os.OpenRoot takes a path and follows symlinks, so it could land on
	// a directory swapped in since the checks above. Comparing it with the
	// descriptor those checks verified ties the root to that directory.
	root, err := os.OpenRoot(path)
	if err != nil {
		_ = dir.Close()
		return nil, fmt.Errorf("%w: %w", ErrUnwritable, err)
	}
	rfi, err := root.Stat(".")
	if err != nil || !os.SameFile(dfi, rfi) {
		_ = root.Close()
		_ = dir.Close()
		return nil, untrusted(path, "changed while it was being checked")
	}
	return &storeDir{path: path, dir: dir, root: root}, nil
}

func (sd *storeDir) close() {
	_ = sd.root.Close()
	_ = sd.dir.Close()
}

// readData returns the entries in the data file name. A missing file, or
// one that can't be opened, is too large, or doesn't decode as this
// identity's format-1 file, yields an empty map and no error. A file that
// fails the trust checks yields an error wrapping ErrUnwritable, and its
// contents, or a symlink's target, are never read.
func (sd *storeDir) readData(name string, echo map[string]string) (map[string]Entry, error) {
	empty := map[string]Entry{}
	f, err := sd.openChecked(name, os.O_RDONLY)
	if errors.Is(err, ErrUnwritable) {
		return nil, err
	}
	if err != nil {
		return empty, nil // missing or unreadable
	}
	defer f.Close()
	ffi, err := f.Stat()
	if err != nil {
		return empty, nil
	}
	if ffi.Size() > maxDataFileSize {
		return empty, nil
	}
	data, err := io.ReadAll(io.LimitReader(f, maxDataFileSize+1))
	if err != nil || len(data) > maxDataFileSize {
		return empty, nil
	}
	entries, ok := decode(data, echo)
	if !ok {
		return empty, nil
	}
	return entries, nil
}

// lock opens (creating it when missing) the lock file name and takes an
// exclusive flock on it, waiting at most lockTimeout. The returned
// function releases the lock.
func (sd *storeDir) lock(name string) (func(), error) {
	f, err := sd.openLockFile(name)
	if err != nil {
		return nil, err
	}
	if testHookSkipLock {
		return func() { _ = f.Close() }, nil
	}
	if err := acquireLock(f, lockTimeout, lockPoll); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", sd.path+"/"+name, err)
	}
	return func() {
		releaseLock(f)
		_ = f.Close()
	}, nil
}

// openLockFile opens the lock file name through openChecked, or creates
// it exclusively at 0600 when it doesn't exist. O_CREATE|O_EXCL never
// follows a symlink, so creation needs no further check.
func (sd *storeDir) openLockFile(name string) (*os.File, error) {
	for range 2 {
		f, err := sd.openChecked(name, os.O_RDWR)
		switch {
		case err == nil:
			return f, nil
		case errors.Is(err, ErrUnwritable):
			return nil, err
		case !errors.Is(err, fs.ErrNotExist):
			return nil, sd.unwritable("opening "+name, err)
		}
		f, err = sd.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue // created by another process since the check
		}
		if err != nil {
			return nil, sd.unwritable("creating "+name, err)
		}
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return nil, sd.unwritable("setting the mode of "+name, err)
		}
		return f, nil
	}
	return nil, untrusted(sd.path+"/"+name, "keeps changing")
}

// openChecked opens the existing store file name with flag, and only when
// it's a regular file owned by the effective user with no group or other
// bits. A file that fails those checks returns an error wrapping
// ErrUnwritable; any other failure (a missing file, a failed open or
// Fstat) returns the os error as is, so callers can tell "untrusted"
// from "absent or unreadable".
//
// O_NOFOLLOW alone doesn't keep a symlink out here: os.Root resolves a
// final-component symlink whose target stays inside the root even when
// the caller passes O_NOFOLLOW. The Lstat before the open is what refuses
// a symlink, and the SameFile check after it refuses one swapped in
// between the two. Both must stay.
func (sd *storeDir) openChecked(name string, flag int) (*os.File, error) {
	path := sd.path + "/" + name
	fi, err := sd.root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if reason := checkFile(fi); reason != "" {
		return nil, untrusted(path, reason)
	}
	f, err := sd.root.OpenFile(name, flag|oNoFollow, 0)
	if err != nil {
		return nil, err
	}
	ffi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !os.SameFile(fi, ffi) {
		_ = f.Close()
		return nil, untrusted(path, "changed while it was being checked")
	}
	if reason := checkFile(ffi); reason != "" {
		_ = f.Close()
		return nil, untrusted(path, reason)
	}
	return f, nil
}

// checkLockFile fails when the lock file name exists and fails the file
// checks. It only looks at the entry, never opens it.
func (sd *storeDir) checkLockFile(name string) error {
	fi, err := sd.root.Lstat(name)
	if err != nil {
		return nil
	}
	if reason := checkFile(fi); reason != "" {
		return untrusted(sd.path+"/"+name, reason)
	}
	return nil
}

// checkFile returns why fi can't be trusted as a store file, or "" when
// it can: it must be a regular file (so not a symlink) owned by the
// effective user with no group or other permission bits.
func checkFile(fi fs.FileInfo) string {
	switch {
	case !fi.Mode().IsRegular():
		return "is not a regular file"
	case !ownedByEUID(fi):
		return "is not owned by the current user"
	case fi.Mode().Perm()&0o077 != 0:
		return "is accessible to other users"
	}
	return ""
}

func ownedByEUID(fi fs.FileInfo) bool {
	uid, ok := fileOwner(fi)
	return ok && uid == effectiveUID()
}

func untrusted(path, reason string) error {
	return fmt.Errorf("%w: %s %s", ErrUnwritable, path, reason)
}

func (sd *storeDir) unwritable(what string, err error) error {
	return fmt.Errorf("%w: %s in %s: %w", ErrUnwritable, what, sd.path, err)
}
