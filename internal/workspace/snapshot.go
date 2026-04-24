package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/testfault"
)

// SwapSnapshotAtomic promotes the staging directory to the canonical
// target path using a two-rename swap that satisfies PRD R12: at no
// point during the swap is the canonical path absent for more than a
// sub-microsecond rename window, and the previous snapshot is removed
// only after the new one is observable at the canonical path.
//
// Sequence:
//
//  1. Idempotent preflight cleanup: any leftover <target>.prev/ from
//     a previously-interrupted swap is removed.
//  2. testfault.Maybe("snapshot-swap") — fault injection seam.
//  3. If the canonical target exists, rename it to <target>.prev.
//  4. Rename staging → target.
//  5. fsync the parent directory to push the rename past metadata cache.
//  6. RemoveAll <target>.prev.
//
// If step 4 fails, the function attempts to roll <target>.prev back
// to the canonical path before returning the error, so an interrupted
// swap leaves the previous snapshot intact.
//
// The primitive is content-agnostic: it knows nothing about provenance
// markers, file contents, or the source tuple. Callers pre-populate
// staging before calling SwapSnapshotAtomic and clean up the staging
// directory on extraction errors.
func SwapSnapshotAtomic(target, staging string) error {
	if target == "" {
		return errors.New("swap: target path is empty")
	}
	if staging == "" {
		return errors.New("swap: staging path is empty")
	}
	if target == staging {
		return errors.New("swap: target and staging paths are identical")
	}

	prev := target + ".prev"

	// Step 1: idempotent preflight cleanup of stale .prev/.
	// Use os.Lstat-aware removal so a planted symlink can't trick the
	// cleanup into traversing outside the workspace.
	if err := safeRemoveAll(prev); err != nil {
		return fmt.Errorf("swap: preflight cleanup of %s: %w", prev, err)
	}

	// Step 2: fault injection.
	if err := testfault.Maybe("snapshot-swap"); err != nil {
		return fmt.Errorf("swap: %w", err)
	}

	// Step 3: rename canonical out of the way (only if it exists).
	targetExists := false
	if info, err := os.Lstat(target); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("swap: target %s exists but is not a directory", target)
		}
		targetExists = true
		if renameErr := os.Rename(target, prev); renameErr != nil {
			return fmt.Errorf("swap: rename %s to %s: %w", target, prev, renameErr)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("swap: stat %s: %w", target, err)
	}

	// Step 4: rename staging into place. On failure, roll prev back.
	if err := os.Rename(staging, target); err != nil {
		if targetExists {
			if rollbackErr := os.Rename(prev, target); rollbackErr != nil {
				return fmt.Errorf("swap: rename staging %s to %s: %w (rollback also failed: %v)",
					staging, target, err, rollbackErr)
			}
		}
		return fmt.Errorf("swap: rename %s to %s: %w", staging, target, err)
	}

	// Step 5: fsync the parent directory so the rename is durable.
	parent := filepath.Dir(target)
	if dir, err := os.Open(parent); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	// Step 6: remove the previous snapshot. Best-effort: a failure
	// here leaves an orphan that the next swap's preflight cleanup
	// will sweep up.
	if targetExists {
		_ = safeRemoveAll(prev)
	}

	return nil
}

// safeRemoveAll removes path, refusing to follow symlinks at the top
// level. If path is a symlink itself, only the link is removed (not
// the target). For a directory, RemoveAll handles the recursion;
// because RemoveAll itself doesn't follow symlinks during traversal,
// any symlinks inside the dir are removed without their targets.
func safeRemoveAll(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return os.Remove(path)
	}
	return os.RemoveAll(path)
}
