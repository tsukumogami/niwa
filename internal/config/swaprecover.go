package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// PrevSuffix names the directory a config snapshot swap moves the current
// snapshot to while the new one takes its place: <dir> becomes <dir>.prev,
// staging becomes <dir>, and <dir>.prev is removed.
const PrevSuffix = ".prev"

// restoreNoticeOut receives the line RestoreInterruptedSwap's callers print
// when they restore a snapshot. Tests point it elsewhere.
var restoreNoticeOut io.Writer = os.Stderr

// RestoreInterruptedSwap undoes a snapshot swap that stopped between its two
// renames: dir is missing and dir.prev is the only copy of the snapshot. It
// renames dir.prev back to dir and reports true. In every other state it
// changes nothing and reports false: dir exists (any dir.prev is then a
// leftover the swap removes itself), dir.prev is missing, or dir.prev is not a
// real directory (a symlink is never followed or moved).
//
// It never deletes anything, so calling it from a path that only reads, such
// as Discover, is safe. If another process is between its two renames at that
// moment, the restore makes that process's second rename fail and leaves the
// previous snapshot in place; nothing is lost either way.
func RestoreInterruptedSwap(dir string) (bool, error) {
	if _, err := os.Lstat(dir); err == nil {
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	prev := dir + PrevSuffix
	info, err := os.Lstat(prev)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if !info.IsDir() {
		return false, nil
	}
	if err := os.Rename(prev, dir); err != nil {
		return false, fmt.Errorf("restore %s from %s: %w", dir, prev, err)
	}
	return true, nil
}

// ReportRestoredSwap prints the notice for a snapshot RestoreInterruptedSwap
// put back.
func ReportRestoredSwap(dir string) {
	fmt.Fprintf(restoreNoticeOut, "niwa: restored %s from %s%s, left by a config refresh that was interrupted mid-swap\n",
		dir, dir, PrevSuffix)
}
