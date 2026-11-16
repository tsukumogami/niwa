package mirror

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// restore rebuilds a broken mirror from r.URL and swaps it in. It runs under
// the mirror's lock and checks again once it holds it, so a second command
// that found the same fault while the first was restoring finds the mirror
// healthy and does nothing.
//
// The order is what keeps a failure harmless: the clone lands in .tmp/, is
// renamed into .store/, and only then is a new link renamed over the stable
// path, the one atomic way to replace what borrowers read. A failure at any
// point removes what this restore made, by exact name, and leaves whatever
// sat at the stable path before as it was.
func restore(ctx context.Context, opts Options, r Repo) (Outcome, error) {
	if r.URL == "" {
		return OutcomeRestoreFailed, errors.New("the workspace config gives no URL to restore from")
	}
	if err := ensureLayout(opts.Home, opts.WorkspaceRoot); err != nil {
		return OutcomeRestoreFailed, err
	}

	wait := opts.LockWait
	if wait <= 0 {
		wait = DefaultLockWait
	}
	holder := fmt.Sprintf("niwa pid %d restoring mirror %s", os.Getpid(), r.ID)
	release, err := filelock.WaitFileLock(ctx, LockPath(opts.Home, opts.WorkspaceRoot, r.ID), holder, wait)
	if errors.Is(err, filelock.ErrLockHeld) {
		return OutcomeBusy, err
	}
	if err != nil {
		return OutcomeRestoreFailed, err
	}
	defer release()

	mdir := mirrorsDir(opts.Home, opts.WorkspaceRoot)
	if check(mdir, r.ID) == FaultNone {
		return OutcomeHealthy, nil
	}
	stable := filepath.Join(mdir, r.ID)
	if info, err := os.Lstat(stable); err == nil && info.Mode()&os.ModeSymlink == 0 {
		// Something niwa didn't make sits where the link goes. A rename over
		// it would fail for a directory and destroy a file; leave it alone.
		return OutcomeRestoreFailed, fmt.Errorf("%s is not a link; left in place", stable)
	}

	store, err := buildStore(ctx, mdir, r)
	if err != nil {
		return OutcomeRestoreFailed, err
	}
	if err := swapLink(mdir, r.ID, store); err != nil {
		// Nothing links to the new store yet, so it goes, by its exact name.
		_ = os.RemoveAll(filepath.Join(mdir, ".store", store))
		return OutcomeRestoreFailed, err
	}
	return OutcomeRestored, nil
}

// buildStore clones r into a temporary directory and renames it into .store/,
// returning the store's name. On failure nothing it made is left behind.
func buildStore(ctx context.Context, mdir string, r Repo) (string, error) {
	suffix, err := randHex()
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(mdir, ".tmp", r.ID+"-"+suffix)
	if err := cloneMirror(ctx, r.URL, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("cloning the mirror: %w", err)
	}
	name, err := newStoreName(r.ID, time.Now())
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", err
	}
	if err := os.Rename(tmp, filepath.Join(mdir, ".store", name)); err != nil {
		_ = os.RemoveAll(tmp)
		return "", fmt.Errorf("moving the new store into place: %w", err)
	}
	return name, nil
}

// swapLink points the stable path at .store/<store>: a new link is made under
// a temporary name (Symlink never replaces an existing entry) and renamed over
// the stable path, then the directory is synced so the swap survives a crash.
// The sync is best effort: once the rename has landed the link names the new
// store, so an error after it must not make the caller remove that store.
func swapLink(mdir, repoID, store string) error {
	suffix, err := randHex()
	if err != nil {
		return err
	}
	tmpLink := filepath.Join(mdir, ".link-"+suffix)
	if err := os.Symlink(filepath.Join(".store", store), tmpLink); err != nil {
		return fmt.Errorf("making the new link: %w", err)
	}
	if err := os.Rename(tmpLink, filepath.Join(mdir, repoID)); err != nil {
		_ = os.Remove(tmpLink)
		return fmt.Errorf("swapping the new link into place: %w", err)
	}
	_ = syncDir(mdir)
	return nil
}
