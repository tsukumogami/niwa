package mirror

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// storesSchema is the store record's format version.
const storesSchema = 1

// storesMax bounds how much of a store record is read. A real one lists a
// handful of stores.
const storesMax = 1 << 20

// retireMinAge is how long a retired store waits before a sweep may remove
// it, and sweepEvery how long a mirror goes between sweeps. The age is a
// margin for git processes in a borrower that resolved the stable link to
// the old store before the swap, and for a create stalled between resolving
// the link and writing its alternates line. It never decides whether a store
// a line names can go: only the strict scan does.
const (
	retireMinAge = time.Hour
	sweepEvery   = time.Hour
)

// storesFile is a mirror's store record, .meta/<repo-id>.stores: one entry
// per store directory a restore made for the repo, so a store is removed by
// the exact name the record gives and never by a glob, and so salvage reads
// only stores cloned from the remote the config names now. The name has no
// .json suffix: Recorded reads every .meta/<x>.json as repo id <x>.
type storesFile struct {
	Schema int `json:"schema"`
	// LastSwept is when a sweep last ran its scan, UTC, RFC 3339; empty
	// before the first.
	LastSwept string       `json:"last_swept"`
	Stores    []storeEntry `json:"stores"`
}

// storeEntry is one store in the record.
type storeEntry struct {
	// Name is the store's directory name under .store/, without the
	// ".store/" prefix.
	Name string `json:"name"`
	// URL is the remote the store was cloned from, stripped as recordURL
	// strips it, and Identity is remoteIdentity of it.
	URL      string `json:"url"`
	Identity string `json:"identity"`
	// Linked says the stable link was ever about to name, or named, the
	// store. It's set before the swap, so a crash between the two keeps it
	// true. A store never linked can have had no borrower resolve it.
	Linked bool `json:"linked"`
	// RetiredAt is when a restore replaced the store, UTC, RFC 3339; empty
	// while it's the linked store or was never linked.
	RetiredAt string `json:"retired_at,omitempty"`
}

// The reasons a store line gives for removing or keeping a store.
const (
	reasonNeverLinked = "never-linked"
	reasonRetired     = "retired"
	reasonSpace       = "space"
	reasonInvalidName = "invalid-name"
	reasonNotAStore   = "not-a-store-directory"
	reasonScanFailed  = "scan-failed"
)

// storeEvent is one store removed or kept, for a store line.
type storeEvent struct{ event, name, reason string }

// storesPath is the store record of repoID.
func storesPath(mdir, repoID string) string {
	return filepath.Join(metaDir(mdir), repoID+".stores")
}

// readStores reads repoID's store record. It reports false when there is
// none to use: the file is missing, isn't a regular file (it's opened
// without following a link, as readState opens), is over storesMax, doesn't
// decode, or has another schema. Each entry's name is kept as written, even
// one that isn't a store name of repoID: the sweep and the teardown leave
// such a store on disk and log it, and dropping it here would hide it.
func readStores(mdir, repoID string) (storesFile, bool) {
	var rec storesFile
	f, err := openRegular(storesPath(mdir, repoID))
	if err != nil {
		return rec, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, storesMax+1))
	if err != nil || len(data) > storesMax {
		return rec, false
	}
	if err := json.Unmarshal(data, &rec); err != nil || rec.Schema != storesSchema {
		return storesFile{}, false
	}
	return rec, true
}

// storesOrSeed returns repoID's store record, or, when there is none, the
// record seedStores makes from the state file.
func storesOrSeed(mdir, repoID string) storesFile {
	if rec, ok := readStores(mdir, repoID); ok {
		return rec
	}
	return seedStores(mdir, repoID)
}

// seedStores is the record of a mirror that has none, such as one v0.43.0
// built: the store the state file names, as the linked store, cloned from
// the state file's URL. With no readable state file, or one naming no store
// of repoID, it's an empty record.
func seedStores(mdir, repoID string) storesFile {
	rec := storesFile{Schema: storesSchema}
	st, err := readState(mdir, repoID)
	if err != nil || !validLinkTarget(repoID, st.Store) {
		return rec
	}
	rec.Stores = []storeEntry{{
		Name:     filepath.Base(st.Store),
		URL:      recordURL(st.URL),
		Identity: remoteIdentity(st.URL),
		Linked:   true,
	}}
	return rec
}

// writeStores writes rec as repoID's store record: to a temporary name in
// .meta/, .<id>.stores-<8 hex>, created exclusively with mode 0600 and
// synced, then renamed over the record, and .meta/ synced so the rename
// survives a crash. The rename replaces whatever is at the record's path, a
// link included, and never writes through it. A crash leaves at worst the
// temporary file, which clearLeftovers removes under the lock.
func writeStores(mdir, repoID string, rec storesFile) error {
	if err := atStage(stageStores); err != nil {
		return fmt.Errorf("writing the store record: %w", err)
	}
	rec.Schema = storesSchema
	data, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	suffix, err := randHex()
	if err != nil {
		return err
	}
	tmp := filepath.Join(metaDir(mdir), "."+repoID+".stores-"+suffix)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("writing the store record: %w", err)
	}
	_, werr := f.Write(append(data, '\n'))
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(tmp, storesPath(mdir, repoID))
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("writing the store record: %w", werr)
	}
	_ = syncDir(metaDir(mdir))
	return nil
}

// storesSnapshot is a store record's bytes as they were, so a restore that
// fails before its swap can put them back.
type storesSnapshot struct {
	data   []byte
	exists bool
}

// snapStores reads repoID's store record's bytes. A record that can't be
// read as a regular file counts as absent.
func snapStores(mdir, repoID string) storesSnapshot {
	f, err := openRegular(storesPath(mdir, repoID))
	if err != nil {
		return storesSnapshot{}
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, storesMax+1))
	if err != nil {
		return storesSnapshot{}
	}
	return storesSnapshot{data: data, exists: true}
}

// putBack writes the snapshot's bytes back as repoID's record, or removes
// the record when there was none. It's best effort: the record it leaves
// otherwise names one store more than exists, which dropVanished prunes.
func (s storesSnapshot) putBack(mdir, repoID string) {
	path := storesPath(mdir, repoID)
	if !s.exists {
		_ = os.Remove(path)
		return
	}
	suffix, err := randHex()
	if err != nil {
		return
	}
	tmp := filepath.Join(metaDir(mdir), "."+repoID+".stores-"+suffix)
	if err := os.WriteFile(tmp, s.data, 0o600); err != nil {
		_ = os.Remove(tmp)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return
	}
	_ = syncDir(metaDir(mdir))
}

// validStoreName reports whether name may be joined into a path under
// .store/ to remove: repoID is one a mirror may have, and name is a store
// name of that id and no other ("<id>-<UTC stamp>-<8 hex>"), so it holds no
// separator and no "..".
func validStoreName(repoID, name string) bool {
	return ValidRepoID(repoID) && storeNamePattern(repoID).MatchString(name)
}

// storeDirSafe reports whether .store/ is a real directory the user owns and
// .store/<name> is, by Lstat, a real directory the user owns in it, so
// removing it removes that directory and nothing a link names.
func storeDirSafe(mdir, name string) bool {
	return realOwnedDir(filepath.Join(mdir, ".store")) && realOwnedDir(filepath.Join(mdir, ".store", name))
}

// index returns the position of the entry named name, or -1.
func (rec *storesFile) index(name string) int {
	for i, e := range rec.Stores {
		if e.Name == name {
			return i
		}
	}
	return -1
}

// put adds e, or replaces the entry of the same name.
func (rec *storesFile) put(e storeEntry) {
	if i := rec.index(e.Name); i >= 0 {
		rec.Stores[i] = e
		return
	}
	rec.Stores = append(rec.Stores, e)
}

// drop removes the entry named name.
func (rec *storesFile) drop(name string) {
	if i := rec.index(name); i >= 0 {
		rec.Stores = append(rec.Stores[:i], rec.Stores[i+1:]...)
	}
}

// retireReplaced marks every linked entry other than current retired at
// now, keeping a retired time already set, and makes current's entry the
// linked one, never retired.
func (rec *storesFile) retireReplaced(current string, now time.Time) {
	for i := range rec.Stores {
		e := &rec.Stores[i]
		switch {
		case e.Name == current:
			e.Linked = true
			e.RetiredAt = ""
		case e.Linked && e.RetiredAt == "":
			e.RetiredAt = stateTime(now)
		}
	}
}

// dropVanished removes the entries whose store directory is gone, by Lstat,
// and which neither the stable link nor the state file names. An entry
// whose directory can't be looked at is kept.
func (rec *storesFile) dropVanished(mdir, repoID string) {
	named := namedStores(mdir, repoID)
	kept := rec.Stores[:0]
	for _, e := range rec.Stores {
		gone := false
		if validStoreName(repoID, e.Name) && !named[e.Name] {
			_, err := os.Lstat(filepath.Join(mdir, ".store", e.Name))
			gone = errors.Is(err, fs.ErrNotExist)
		}
		if !gone {
			kept = append(kept, e)
		}
	}
	rec.Stores = kept
}

// namedStores returns the store names the stable link and the state file
// name now: the stores a removal must never touch.
func namedStores(mdir, repoID string) map[string]bool {
	named := map[string]bool{}
	if target, err := os.Readlink(filepath.Join(mdir, repoID)); err == nil && validLinkTarget(repoID, target) {
		named[filepath.Base(target)] = true
	}
	if st, err := readState(mdir, repoID); err == nil && validLinkTarget(repoID, st.Store) {
		named[filepath.Base(st.Store)] = true
	}
	return named
}

// removeNeverLinked removes, by exact name, each store whose entry says the
// link never named it, and drops its entry. The caller holds the mirror's
// lock, so no restore is between writing such an entry and the swap: the
// store is left from one that crashed, and no clone can have resolved the
// link to it, so it goes with no scan. A name that isn't a store name of
// repoID stays on disk and in the record, and so does a directory that isn't
// a real one in a real .store/; each is reported.
func (rec *storesFile) removeNeverLinked(mdir, repoID string) []storeEvent {
	named := namedStores(mdir, repoID)
	var events []storeEvent
	var gone []string
	for _, e := range rec.Stores {
		if e.Linked || named[e.Name] {
			continue
		}
		if !validStoreName(repoID, e.Name) {
			events = append(events, storeEvent{eventKeep, e.Name, reasonInvalidName})
			continue
		}
		path := filepath.Join(mdir, ".store", e.Name)
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			gone = append(gone, e.Name)
			continue
		}
		if !storeDirSafe(mdir, e.Name) {
			events = append(events, storeEvent{eventKeep, e.Name, reasonNotAStore})
			continue
		}
		if err := removeStoreDir(path); err != nil {
			continue
		}
		gone = append(gone, e.Name)
		events = append(events, storeEvent{eventRemove, e.Name, reasonNeverLinked})
	}
	for _, name := range gone {
		rec.drop(name)
	}
	return events
}

// removeStoreDir removes a store directory a caller validated, by its exact
// path. git writes objects read-only, which RemoveAll removes anyway since
// the directories above them are writable.
func removeStoreDir(path string) error {
	return os.RemoveAll(path)
}

// storeLines builds one store line per event from base, the line of the
// call that made them, whose time, command and mirror they carry.
func storeLines(base logLine, events []storeEvent) []any {
	var lines []any
	for _, ev := range events {
		l := logLine{Time: base.Time, Command: base.Command, Mirror: base.Mirror, Event: ev.event}
		lines = append(lines, storeLine{logLine: l, Store: ".store/" + ev.name, Reason: ev.reason})
	}
	return lines
}
