//go:build darwin

package workspace

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames from to to and fails with an fs.ErrExist error
// when anything is at to, in one step: renamex_np with RENAME_EXCL. A
// filesystem without the flag gets renameChecked.
func renameNoReplace(from, to string) error {
	err := unix.RenamexNp(from, to, unix.RENAME_EXCL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EINVAL):
		return renameChecked(from, to)
	}
	return &fs.PathError{Op: "rename", Path: from, Err: err}
}
