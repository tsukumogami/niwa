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
	fi, err := sd.root.Lstat(name)
	if err != nil {
		return empty, nil
	}
	if reason := checkFile(fi); reason != "" {
		return nil, untrusted(sd.path+"/"+name, reason)
	}
	f, err := sd.root.OpenFile(name, os.O_RDONLY|oNoFollow, 0)
	if err != nil {
		return empty, nil
	}
	defer f.Close()
	ffi, err := f.Stat()
	if err != nil {
		return empty, nil
	}
	if !os.SameFile(fi, ffi) {
		return nil, untrusted(sd.path+"/"+name, "changed while it was being checked")
	}
	if reason := checkFile(ffi); reason != "" {
		return nil, untrusted(sd.path+"/"+name, reason)
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

// openLockFile opens the lock file name without following a symlink,
// creating it exclusively at 0600 when it doesn't exist. An existing
// lock file must pass the same checks as a data file.
func (sd *storeDir) openLockFile(name string) (*os.File, error) {
	path := sd.path + "/" + name
	for range 2 {
		fi, err := sd.root.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			f, err := sd.root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
			if errors.Is(err, fs.ErrExist) {
				continue // created by another process since the Lstat
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
		if err != nil {
			return nil, sd.unwritable("checking "+name, err)
		}
		if reason := checkFile(fi); reason != "" {
			return nil, untrusted(path, reason)
		}
		f, err := sd.root.OpenFile(name, os.O_RDWR|oNoFollow, 0)
		if err != nil {
			return nil, sd.unwritable("opening "+name, err)
		}
		ffi, err := f.Stat()
		if err != nil {
			_ = f.Close()
			return nil, sd.unwritable("checking "+name, err)
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
	return nil, untrusted(path, "keeps changing")
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
