package removal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Every file the rule reads under a unit goes through one reader: opened
// without following a link and without blocking on a FIFO, checked on its
// descriptor to be a regular file owned by the current user and no larger
// than its cap, and read no further than the cap. Anything else in a
// record's place is refused, and a refused record keeps the unit.

// currentUID is the user every file the rule reads must belong to. A
// variable so a test can make a file another owner's without root.
var currentUID = os.Getuid

// The caps on the records the rule reads.
const (
	// maxRecordBytes bounds a small record: the mark, discard.json,
	// pids/*.json and unit.json.
	maxRecordBytes = 64 << 10
	// maxKeepBytes bounds keep.json, which carries a handoff brief.
	maxKeepBytes = 1 << 20
	// maxCacheTagBytes bounds the read of a CACHEDIR.TAG.
	maxCacheTagBytes = 4096
)

// readOpenFlags opens a record for reading: never through a link at its
// last component, never waiting on a FIFO or a device.
const readOpenFlags = unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC

// dirOpenFlags opens a directory the rule reads records relative to.
const dirOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// readCapped reads the file at path through the shared reader.
func readCapped(path string, max int64) ([]byte, error) {
	fd, err := retryEINTR(func() (int, error) { return unix.Open(path, readOpenFlags, 0) })
	if err != nil {
		return nil, openError(filepath.Base(path), err)
	}
	return readFD(fd, filepath.Base(path), max)
}

// readCappedAt reads the file name, relative to the directory open at
// dirfd, through the shared reader.
func readCappedAt(dirfd int, name string, max int64) ([]byte, error) {
	fd, err := retryEINTR(func() (int, error) { return unix.Openat(dirfd, name, readOpenFlags, 0) })
	if err != nil {
		return nil, openError(filepath.Base(name), err)
	}
	return readFD(fd, filepath.Base(name), max)
}

// openError names why a record couldn't be opened. O_NOFOLLOW fails with
// ELOOP on a link, which is named as one.
func openError(label string, err error) error {
	if errors.Is(err, unix.ELOOP) {
		return fmt.Errorf("%s is a symlink", label)
	}
	return &fs.PathError{Op: "open", Path: label, Err: err}
}

// readFD checks the open file fd and reads at most max bytes of it, then
// closes it. label names the file in errors.
func readFD(fd int, label string, max int64) ([]byte, error) {
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, &fs.PathError{Op: "fstat", Path: label, Err: err}
	}
	if err := ownedRegular(label, &st); err != nil {
		return nil, err
	}
	if st.Size > max {
		return nil, fmt.Errorf("%s is larger than %d bytes", label, max)
	}
	var out bytes.Buffer
	buf := make([]byte, 32<<10)
	for {
		n, err := unix.Read(fd, buf)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, &fs.PathError{Op: "read", Path: label, Err: err}
		}
		if n == 0 {
			break
		}
		out.Write(buf[:n])
		if int64(out.Len()) > max {
			return nil, fmt.Errorf("%s is larger than %d bytes", label, max)
		}
	}
	return out.Bytes(), nil
}

// ownedRegular refuses anything but a regular file owned by the current
// user.
func ownedRegular(label string, st *unix.Stat_t) error {
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if int64(st.Uid) != int64(currentUID()) {
		return fmt.Errorf("%s is not owned by the current user", label)
	}
	return nil
}

// ownedDir refuses anything but a directory owned by the current user, and
// names a link as one.
func ownedDir(label string, st *unix.Stat_t) error {
	switch uint32(st.Mode) & unix.S_IFMT {
	case unix.S_IFDIR:
	case unix.S_IFLNK:
		return fmt.Errorf("%s is a symlink", label)
	default:
		return fmt.Errorf("%s is not a directory", label)
	}
	if int64(st.Uid) != int64(currentUID()) {
		return fmt.Errorf("%s is not owned by the current user", label)
	}
	return nil
}

// sameFile reports whether two stats name the same file.
func sameFile(a, b *unix.Stat_t) bool {
	return uint64(a.Dev) == uint64(b.Dev) && uint64(a.Ino) == uint64(b.Ino)
}

func retryEINTR(open func() (int, error)) (int, error) {
	for {
		fd, err := open()
		if !errors.Is(err, unix.EINTR) {
			return fd, err
		}
	}
}

// niwaDirName is the instance's own record directory.
const niwaDirName = ".niwa"

// errNiwaNotOpen is what a niwaDir that was never opened answers.
var errNiwaNotOpen = errors.New(".niwa was not opened")

// niwaDir is an instance's .niwa directory, held open so every read the
// rule makes under it is made relative to the directory it checked and not
// to whatever the path names by the time of the read.
type niwaDir struct {
	fd     int
	opened bool
	// absent is true when the instance has no .niwa at all.
	absent bool
	// err is why .niwa can't be used: it is a link, not a directory,
	// another user's, or changed between the check and the open.
	err error
}

// openNiwaDir checks <instance>/.niwa with Lstat as a real directory owned
// by the user, opens it with O_DIRECTORY|O_NOFOLLOW, and checks the
// descriptor names the directory the Lstat saw.
func openNiwaDir(instance string) niwaDir {
	p := filepath.Join(instance, niwaDirName)
	var lst unix.Stat_t
	if err := unix.Lstat(p, &lst); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return niwaDir{absent: true}
		}
		return niwaDir{err: fmt.Errorf("%s: %v", niwaDirName, err)}
	}
	if err := ownedDir(niwaDirName, &lst); err != nil {
		return niwaDir{err: err}
	}
	fd, err := retryEINTR(func() (int, error) { return unix.Open(p, dirOpenFlags, 0) })
	if err != nil {
		return niwaDir{err: openError(niwaDirName, err)}
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return niwaDir{err: &fs.PathError{Op: "fstat", Path: niwaDirName, Err: err}}
	}
	if !sameFile(&lst, &st) {
		unix.Close(fd)
		return niwaDir{err: fmt.Errorf("%s changed while it was opened", niwaDirName)}
	}
	return niwaDir{fd: fd, opened: true}
}

// usable is nil when reads can be made under the directory, and otherwise
// why they can't.
func (d *niwaDir) usable() error {
	switch {
	case d.err != nil:
		return d.err
	case d.absent:
		return fmt.Errorf("%s: %w", niwaDirName, fs.ErrNotExist)
	case !d.opened:
		return errNiwaNotOpen
	}
	return nil
}

// read reads the record name under .niwa through the shared reader.
func (d *niwaDir) read(name string, max int64) ([]byte, error) {
	if err := d.usable(); err != nil {
		return nil, err
	}
	return readCappedAt(d.fd, name, max)
}

// present reports whether anything is at name under .niwa, without
// following a link. An error means the rule can't tell, and comes with
// present true.
func (d *niwaDir) present(name string) (bool, error) {
	if err := d.usable(); err != nil {
		return true, err
	}
	var st unix.Stat_t
	err := unix.Fstatat(d.fd, name, &st, unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	}
	return true, &fs.PathError{Op: "stat", Path: name, Err: err}
}

// checkDir checks name under .niwa, when it exists, is a real directory
// owned by the user, and that opening it without following a link reaches
// the directory the check saw. A name that doesn't exist passes. Nothing
// under the directory is read.
func (d *niwaDir) checkDir(name string) error {
	if err := d.usable(); err != nil {
		return err
	}
	label := niwaDirName + "/" + name
	var lst unix.Stat_t
	if err := unix.Fstatat(d.fd, name, &lst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("%s: %v", label, err)
	}
	if err := ownedDir(label, &lst); err != nil {
		return err
	}
	fd, err := retryEINTR(func() (int, error) { return unix.Openat(d.fd, name, dirOpenFlags, 0) })
	if err != nil {
		return openError(label, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("%s: %v", label, err)
	}
	if !sameFile(&lst, &st) {
		return fmt.Errorf("%s changed while it was opened", label)
	}
	return nil
}

// close releases the descriptor.
func (d *niwaDir) close() {
	if d.opened {
		unix.Close(d.fd)
		d.opened = false
	}
}

// decodeRecord decodes a record strictly into v: its schema field must be
// schema, every field must be one v declares, and nothing may follow the
// value.
func decodeRecord(b []byte, schema string, v any) error {
	// The schema is read first, leniently and from the first value only,
	// so a record of another schema is named as one rather than as a
	// field this schema doesn't know.
	var head struct {
		Schema string `json:"schema"`
	}
	if err := json.NewDecoder(bytes.NewReader(b)).Decode(&head); err != nil {
		return fmt.Errorf("the record does not parse: %v", err)
	}
	if head.Schema != schema {
		return fmt.Errorf("unknown schema name %q", head.Schema)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("the record does not parse: %v", err)
	}
	if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return errors.New("trailing data after the record")
	}
	return nil
}

// readRecordAt reads the record name under .niwa through the shared reader
// with its cap and decodes it strictly.
func readRecordAt(d *niwaDir, name string, max int64, schema string, v any) error {
	b, err := d.read(name, max)
	if err != nil {
		return err
	}
	return decodeRecord(b, schema, v)
}

// checkRecords is the records check. The records later checks read live
// under .niwa, so a .niwa the rule can't use keeps the unit with it named,
// and so does a .niwa/pids that is a link, not a directory, or another
// user's. Nothing under .niwa/pids is read here.
func checkRecords(_ context.Context, _ Unit, _ Policy, s *evalState) []Check {
	if s.niwa.absent {
		return nil
	}
	if err := s.niwa.usable(); err != nil {
		return []Check{{Paths: []string{niwaDirName}, Evidence: "records could not be read: " + errText(err)}}
	}
	if err := s.niwa.checkDir("pids"); err != nil {
		return []Check{{Paths: []string{niwaDirName + "/pids"}, Evidence: "records could not be read: " + errText(err)}}
	}
	return nil
}
