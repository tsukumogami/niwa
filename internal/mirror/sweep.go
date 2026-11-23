package mirror

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// The removal rule here is stricter than the condition niwa#525 states,
// which asks only that no old store go before the restore's probe and
// recovery finish. A retired store is removed only after a strict scan of
// every clone on disk succeeds under the mirror's lock and no alternates line
// in it resolves into the store (PRD R9), and, outside the space-pressure
// path, only once the store has been retired for an hour. Outside the
// free-space path, the restore itself never removes a store that was ever
// linked, so the probe and recovery finish with every old store on disk (PRD
// R13). On that path, when the restore's free-space check would refuse, it
// runs the strict scan under its lock and removes retired stores that no
// alternates line pins, whatever their age, before it checks again. A nil or
// failing scan removes nothing.

// retiredFor reports whether e was retired at least minAge before now. A
// retired time that can't be read counts as old enough: the scan, not the
// age, is what decides whether a store can go.
func retiredFor(e storeEntry, now time.Time, minAge time.Duration) bool {
	if e.RetiredAt == "" {
		return false
	}
	at, err := time.Parse(time.RFC3339, e.RetiredAt)
	return err != nil || now.Sub(at) >= minAge
}

// sweepDue reports whether a sweep of rec should scan at now: some store has
// been retired for retireMinAge, and the last sweep is unrecorded or at
// least sweepEvery old. The hour between sweeps holds even when every scan
// fails, so an unreadable kept/ costs one scan an hour, not one per command.
func sweepDue(rec storesFile, now time.Time) bool {
	if last, err := time.Parse(time.RFC3339, rec.LastSwept); err == nil && now.Sub(last) < sweepEvery {
		return false
	}
	for _, e := range rec.Stores {
		if retiredFor(e, now, retireMinAge) {
			return true
		}
	}
	return false
}

// sweep removes, at the end of a call that wasn't busy, the retired stores
// of r that no clone on disk names, and returns the store lines to log. It
// reads the record without the lock first, so a mirror with nothing due
// costs one small read. When a sweep is due (sweepDue) it only tries the
// mirror's lock: a held lock means another command is restoring or
// refreshing the mirror, and the sweep is skipped with no line and no
// change. Under the lock it reads the record again and, with opts.Scan set,
// calls the scan and records the sweep's time whether or not the scan
// succeeded. A failed scan keeps every store and logs each due one as kept;
// otherwise removeUnpinned decides.
//
// Nothing is read or written unless the workspace key directory, mirrors/
// and .meta/ are real directories the user owns, so the record is never
// written through a link.
func sweep(opts Options, mdir string, r Repo, base logLine) []any {
	now := time.Now()
	if opts.Scan == nil || !ValidRepoID(r.ID) {
		return nil
	}
	for _, dir := range []string{filepath.Dir(mdir), mdir, metaDir(mdir)} {
		if !realOwnedDir(dir) {
			return nil
		}
	}
	if rec, ok := readStores(mdir, r.ID); !ok || !sweepDue(rec, now) {
		return nil
	}
	holder := fmt.Sprintf("niwa pid %d sweeping mirror %s", os.Getpid(), r.ID)
	release, err := filelock.TryFileLock(LockPath(opts.Home, opts.WorkspaceRoot, r.ID), holder)
	if err != nil {
		return nil
	}
	defer release()
	rec, ok := readStores(mdir, r.ID)
	if !ok || !sweepDue(rec, now) {
		return nil
	}

	clones, scanErr := opts.Scan()
	rec.LastSwept = stateTime(now)
	var events []storeEvent
	if scanErr != nil {
		for _, e := range rec.Stores {
			if retiredFor(e, now, retireMinAge) {
				events = append(events, storeEvent{eventKeep, e.Name, reasonScanFailed})
			}
		}
	} else {
		events, _ = removeUnpinned(mdir, r.ID, &rec, clones, retireMinAge, reasonRetired, now)
	}
	// A record that can't be written leaves the entries of removed stores,
	// which dropVanished prunes on a later write; nothing else depends on it.
	_ = writeStores(mdir, r.ID, rec)
	base.Time = logTime(now)
	return storeLines(base, events)
}

// removeUnpinned removes each store rec lists as retired at least minAge
// before now that no line of clones pins (hostdir.Pins), drops its entry and
// reports it with reason; it reports whether it removed any. clones must be
// the answer of a strict scan that succeeded, taken under the mirror's lock,
// which the caller holds.
//
// Each removal is by the exact name the record gives, joined under .store/
// only once it's a store name of repoID (validStoreName) and Lstat shows a
// real directory in a real .store/ (storeDirSafe); a name that fails either
// stays on disk and is logged as kept. No store the stable link or the state
// file names is ever a candidate. Every Pins call runs before the first
// removal, so an error from any of them removes nothing: each candidate is
// logged as kept then. A pinned store stays with no line, and a later sweep
// looks at it again.
func removeUnpinned(mdir, repoID string, rec *storesFile, clones []string, minAge time.Duration, reason string, now time.Time) ([]storeEvent, bool) {
	named := namedStores(mdir, repoID)
	var events []storeEvent
	var candidates []string
	for _, e := range rec.Stores {
		if !retiredFor(e, now, minAge) || named[e.Name] {
			continue
		}
		if !validStoreName(repoID, e.Name) {
			events = append(events, storeEvent{eventKeep, e.Name, reasonInvalidName})
			continue
		}
		if _, err := os.Lstat(filepath.Join(mdir, ".store", e.Name)); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if !storeDirSafe(mdir, e.Name) {
			events = append(events, storeEvent{eventKeep, e.Name, reasonNotAStore})
			continue
		}
		candidates = append(candidates, e.Name)
	}

	var unpinned []string
	for _, name := range candidates {
		pinned, err := hostdir.Pins(clones, filepath.Join(mdir, ".store", name))
		if err != nil {
			for _, name := range candidates {
				events = append(events, storeEvent{eventKeep, name, reasonScanFailed})
			}
			return events, false
		}
		if !pinned {
			unpinned = append(unpinned, name)
		}
	}

	removed := false
	for _, name := range unpinned {
		// A removal that fails part way keeps the entry, so a later sweep
		// finishes it.
		if err := removeStoreDir(filepath.Join(mdir, ".store", name)); err != nil {
			continue
		}
		rec.drop(name)
		removed = true
		events = append(events, storeEvent{eventRemove, name, reason})
	}
	rec.dropVanished(mdir, repoID)
	return events, removed
}
