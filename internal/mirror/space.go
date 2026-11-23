package mirror

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// checkFreeSpace refuses a clone of repoID when the file system holding mdir
// has less free than spaceNeeded: a clone that fills the disk would fail
// halfway and could starve everything else writing there. A free-space read
// that fails doesn't block the restore, since the clone itself still fails
// cleanly on a full disk.
func checkFreeSpace(mdir, repoID string) error {
	need := spaceNeeded(mdir, repoID)
	free, err := freeSpace(mdir)
	if err != nil {
		return nil
	}
	if free < need {
		return fmt.Errorf("not enough free space to restore the mirror: %d bytes free, %d needed", free, need)
	}
	return nil
}

// minFreeUnknown is the free space a restore wants when the state file
// records no pack sizes to size the clone by.
const minFreeUnknown = 1 << 30

// freeSpace reports the bytes an unprivileged user can still write on the
// file system holding dir. It's a variable so the tests can report any
// figure without filling a disk.
var freeSpace = func(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	// Bsize is uint32 on macOS and int64 on Linux; both convert.
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

// spaceNeeded is what a restore of repoID wants free before it clones: the
// sizes the state file records for its packs, which a new clone of the same
// remote roughly matches, or minFreeUnknown when no state file or no pack
// size is known.
func spaceNeeded(mdir, repoID string) uint64 {
	rec, err := readState(mdir, repoID)
	if err != nil {
		return minFreeUnknown
	}
	var sum uint64
	for _, p := range rec.Packs {
		if p.Size > 0 {
			sum += uint64(p.Size)
		}
	}
	if sum == 0 {
		return minFreeUnknown
	}
	return sum
}
