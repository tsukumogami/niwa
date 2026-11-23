//go:build linux

package workspace

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to and fails with an fs.ErrExist error
// when anything is at to, in one step: renameat2 with RENAME_NOREPLACE. A
// kernel or filesystem without the flag gets renameChecked.
func renameNoReplace(from, to string) error {
	err := unix.Renameat2(unix.AT_FDCWD, from, unix.AT_FDCWD, to, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		return renameChecked(from, to)
	}
	return &fs.PathError{Op: "rename", Path: from, Err: err}
}
