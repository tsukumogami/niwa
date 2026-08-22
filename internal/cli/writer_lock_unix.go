//go:build unix

package cli

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// writerLockHeld reports whether another process currently holds the advisory
// lock at lockPath, and whether that question could be answered at all.
//
// The probe is an acquire-and-release rather than a read, because there is no
// portable way to ask about a BSD advisory lock without taking one: flock locks
// live in a different space from the POSIX record locks F_GETLK reports on, so
// F_GETLK sees nothing here. Taking the lock non-blocking and dropping it
// immediately is the whole mechanism -- success means nobody held it, and
// EWOULDBLOCK means somebody does.
//
// Three properties of the file this runs against, all measured against
// codex-cli 0.147.0, decide the shape of what follows:
//
// The file is absent unless a writer is attached. A clean exit unlinks it, so
// ENOENT is not a missing answer, it is the answer -- no writer. That is why
// the open below must not carry O_CREAT. A probe that creates the file would
// manufacture exactly the artifact a crashed worker leaves behind, and every
// later probe would then be reading this one's litter.
//
// The kernel releases the lock when a holder dies. A worker killed without
// unlinking leaves the file on disk with nothing holding it, so the acquire
// succeeds and it reads as no writer, which is the truth.
//
// The file is a fresh inode each time a writer attaches. Nothing here may be
// cached between probes: the path is opened again on every call, and the handle
// is closed before returning.
func writerLockHeld(lockPath string) (held bool, known bool) {
	if lockPath == "" {
		return false, false
	}

	f, err := os.OpenFile(lockPath, os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// The open cannot tell a missing lock file from a missing lock
			// directory -- both are ENOENT -- and the two mean opposite things.
			// A missing file is the answer: the agent unlinks it when the
			// writer detaches, so nobody is writing. A missing directory means
			// this build is looking in the wrong place, and reading that as
			// "nobody is writing" would retire the only gate standing between a
			// live worker and a destroyed working directory, silently, while
			// the two-gate design went on looking like it worked.
			//
			// So the directory is what gets checked, and only on this branch:
			// it is a structural fact about the store rather than a per-session
			// one, so nothing races it the way stat-then-open would race the
			// lock file's own unlink-and-recreate cycle. Absent, the question is
			// unanswerable, which everywhere else in this path means spare.
			if _, statErr := os.Stat(filepath.Dir(lockPath)); statErr != nil {
				return false, false
			}
			return false, true
		}
		// Anything else -- a permission error, a path that is not a file --
		// leaves the question unanswered, and a caller with no evidence must
		// not act as though it had some.
		return false, false
	}
	defer f.Close()

	switch err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		// Held for as briefly as it is possible to hold it. Releasing before
		// returning matters more than it looks: leaving it locked until the
		// process exits would make niwa the writer this check exists to detect.
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return false, true
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, true
	default:
		return false, false
	}
}
