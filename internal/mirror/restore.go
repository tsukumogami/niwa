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
// The order is what keeps a failure harmless: the clone lands in .tmp/, takes
// in what the old stores still hold intact, is renamed into .store/, and only
// then is a new link renamed over the stable path, the one atomic way to
// replace what borrowers read. The state file is written under a temporary
// name before the swap and renamed into place after it, so it never names a
// store the link doesn't. The old stores go last, by exact name, once nothing
// links to them. A failure before the swap removes what this restore made and
// leaves whatever sat at the stable path as it was.
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
	if check(ctx, mdir, r.ID) == FaultNone {
		return OutcomeHealthy, nil
	}
	stable := filepath.Join(mdir, r.ID)
	if info, err := os.Lstat(stable); err == nil && info.Mode()&os.ModeSymlink == 0 {
		// Something niwa didn't make sits where the link goes. A rename over
		// it would fail for a directory and destroy a file; leave it alone.
		return OutcomeRestoreFailed, fmt.Errorf("%s is not a link; left in place", stable)
	}

	// The old stores are listed before the new one exists, so the list can
	// never name it.
	olds := oldStores(mdir, r.ID)
	store, stateTmp, err := buildStore(ctx, mdir, r, olds)
	if err != nil {
		return OutcomeRestoreFailed, err
	}
	if err := swapLink(mdir, r.ID, store); err != nil {
		// Nothing links to the new store yet, so it goes, by its exact name.
		_ = os.RemoveAll(filepath.Join(mdir, ".store", store))
		_ = os.Remove(stateTmp)
		return OutcomeRestoreFailed, err
	}
	if err := os.Rename(stateTmp, statePath(mdir, r.ID)); err != nil {
		// The new store is linked and borrowers read it; only its record is
		// missing, so the next check finds no matching state file and
		// rebuilds, salvaging from this store. The old stores stay until then.
		_ = os.Remove(stateTmp)
		return OutcomeRestored, fmt.Errorf("recording the new store: %w", err)
	}
	_ = syncDir(metaDir(mdir))
	removeOldStores(mdir, r.ID, olds)
	return OutcomeRestored, nil
}

// buildStore clones r into a temporary directory, salvages into it from the
// old stores, writes its state file under a temporary name, and renames it
// into .store/. It returns the store's name and the state file's temporary
// path. On failure nothing it made is left behind.
func buildStore(ctx context.Context, mdir string, r Repo, olds []string) (string, string, error) {
	suffix, err := randHex()
	if err != nil {
		return "", "", err
	}
	tmp := filepath.Join(mdir, ".tmp", r.ID+"-"+suffix)
	if err := cloneMirror(ctx, r.URL, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("cloning the mirror: %w", err)
	}
	salvage(ctx, mdir, olds, tmp)

	name, err := newStoreName(r.ID, time.Now())
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	// The manifest is read in .tmp/: a rename of the store directory moves
	// none of the ctimes it records.
	packs, err := manifest(tmp)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("listing the new store's packs: %w", err)
	}
	stateTmp, err := writeStateTemp(mdir, state{
		Schema: stateSchema,
		RepoID: r.ID,
		URL:    recordURL(r.URL),
		Store:  filepath.Join(".store", name),
		Packs:  packs,
	})
	if err != nil {
		_ = os.RemoveAll(tmp)
		return "", "", err
	}
	if err := os.Rename(tmp, filepath.Join(mdir, ".store", name)); err != nil {
		_ = os.RemoveAll(tmp)
		_ = os.Remove(stateTmp)
		return "", "", fmt.Errorf("moving the new store into place: %w", err)
	}
	return name, stateTmp, nil
}

// removeOldStores removes the store directories a restore salvaged from, by
// the exact names it listed, skipping whatever the stable link names now.
// A failure leaves a directory behind for the next restore to salvage from
// and remove; it doesn't change the restore's outcome.
func removeOldStores(mdir, repoID string, olds []string) {
	current, _ := os.Readlink(filepath.Join(mdir, repoID))
	for _, name := range olds {
		if filepath.Join(".store", name) == current {
			continue
		}
		_ = os.RemoveAll(filepath.Join(mdir, ".store", name))
	}
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
