package mirror

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/tsukumogami/niwa/internal/filelock"
	"github.com/tsukumogami/niwa/internal/hostdir"
)

// Teardown removes a destroyed workspace's mirrors that no kept instance
// borrows from (PRD R10). Its paths and the workspace key the mirror locks
// hash are worked out by NewTeardown while the workspace root still exists,
// since the key comes from the root's canonical path; Remove runs once the
// root is gone.
type Teardown struct {
	home, key, keyDir, mdir string
}

// NewTeardown records where workspaceRoot's mirrors and locks are. Call it
// before the root is removed.
func NewTeardown(home, workspaceRoot string) Teardown {
	return Teardown{
		home:   home,
		key:    WorkspaceKey(workspaceRoot),
		keyDir: keyDir(home, workspaceRoot),
		mdir:   mirrorsDir(home, workspaceRoot),
	}
}

// Remove decides, mirror by mirror, whether the destroyed workspace's
// mirrors go, writing each decision it keeps a mirror for, and each thing it
// leaves, to out (nil writes nothing). It never fails: the workspace root is
// already gone, so nothing here can make the destroy undo itself.
//
// For each repo id the mirrors directory records (recordedIn), in sorted
// order, it only tries the mirror's lock and keeps the mirror when another
// command holds it ("busy"). Under the lock it calls scan, the strict walk of
// the kept instances, the only ones left once the root is gone, and keeps the
// mirror when the scan fails ("kept instances couldn't be read") or when a
// kept clone's alternates resolves into a store the record or the state file
// names, or names the stable path's objects though the link is gone ("a kept
// instance borrows from it"). Otherwise it removes the link (only a link),
// each store the record and the state file name, by exact name and only once
// it's a store name of the id in a real directory in a real .store/ (as the
// sweep validates), then the state file, the record and the id's leftovers.
//
// A .store/ entry no record or state file names is left and reported, never
// removed: removing by a "<id>-*" glob would take repo "app-extra"'s stores
// with "app"'s. With no mirror kept and nothing left, the mirror log goes and
// then .meta/, .store/, .tmp/ and mirrors/ themselves, each only if empty.
func (td Teardown) Remove(scan func() ([]string, error), out io.Writer) {
	say := func(format string, args ...any) {
		if out != nil {
			fmt.Fprintf(out, format+"\n", args...)
		}
	}
	if _, err := os.Lstat(td.mdir); errors.Is(err, os.ErrNotExist) {
		return
	}
	if !realOwnedDir(td.keyDir) || !realOwnedDir(td.mdir) {
		say("left %s: not a directory niwa made", td.mdir)
		return
	}
	meta := metaDir(td.mdir)
	metaReal := realOwnedDir(meta)

	keptAny := false
	named := map[string]bool{}
	for _, id := range recordedIn(td.mdir, nil) {
		stores := storesNamedFor(td.mdir, id, metaReal)
		for _, name := range stores {
			named[name] = true
		}
		if !td.removeOne(id, stores, metaReal, scan, say) {
			keptAny = true
		}
	}

	if entries, err := os.ReadDir(filepath.Join(td.mdir, ".store")); err == nil {
		for _, e := range entries {
			if !named[e.Name()] {
				say("left %s: no mirror record names it", filepath.Join(td.mdir, ".store", e.Name()))
				keptAny = true
			}
		}
	}
	if keptAny {
		return
	}
	if metaReal {
		log := logPath(td.mdir)
		_ = os.Remove(log)
		_ = os.Remove(log + ".1")
	}
	// Each fails, harmlessly, unless the directory is empty.
	for _, dir := range []string{meta, filepath.Join(td.mdir, ".store"), filepath.Join(td.mdir, ".tmp"), td.mdir} {
		_ = os.Remove(dir)
	}
}

// removeOne is Remove's decision for one repo id whose stores are stores. It
// reports whether the mirror is gone; false means it was kept, or something
// of it was left, and said so.
func (td Teardown) removeOne(id string, stores []string, metaReal bool, scan func() ([]string, error), say func(string, ...any)) bool {
	stable := filepath.Join(td.mdir, id)
	holder := fmt.Sprintf("niwa pid %d removing mirror %s", os.Getpid(), id)
	release, err := filelock.TryFileLock(lockPathFor(td.home, td.key, id), holder)
	if errors.Is(err, filelock.ErrLockHeld) {
		say("kept the mirror at %s: busy, another niwa command holds its lock", stable)
		return false
	}
	if err != nil {
		say("kept the mirror at %s: its lock couldn't be taken", stable)
		return false
	}
	defer release()

	clones, err := scan()
	if err != nil {
		say("kept the mirror at %s: kept instances couldn't be read", stable)
		return false
	}
	for _, name := range stores {
		pinned, err := hostdir.Pins(clones, filepath.Join(td.mdir, ".store", name))
		if err != nil {
			say("kept the mirror at %s: kept instances couldn't be read", stable)
			return false
		}
		if pinned {
			say("kept the mirror at %s: a kept instance borrows from it", stable)
			return false
		}
	}
	if hasBorrower(clones, stable) {
		say("kept the mirror at %s: a kept instance borrows from it", stable)
		return false
	}

	gone := true
	if info, err := os.Lstat(stable); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			_ = os.Remove(stable)
		} else {
			say("left %s: not a link niwa made", stable)
			gone = false
		}
	}
	for _, name := range stores {
		path := filepath.Join(td.mdir, ".store", name)
		if !storeDirSafe(td.mdir, name) {
			if _, err := os.Lstat(path); err == nil {
				say("left %s: not a store directory niwa made", path)
				gone = false
			}
			continue
		}
		if err := removeStoreDir(path); err != nil {
			say("left %s: %v", path, err)
			gone = false
		}
	}
	if metaReal {
		_ = os.Remove(statePath(td.mdir, id))
		_ = os.Remove(storesPath(td.mdir, id))
	} else {
		gone = false
	}
	clearLeftovers(td.mdir, id)
	return gone
}

// storesNamedFor lists, sorted and each once, the stores of id the record
// and the state file name, kept only when the name is a store name of id
// (validStoreName), so none can be joined into a path outside .store/. With
// .meta/ not a real directory, nothing in it is read and none is named.
func storesNamedFor(mdir, id string, metaReal bool) []string {
	if !metaReal {
		return nil
	}
	seen := map[string]bool{}
	if rec, ok := readStores(mdir, id); ok {
		for _, e := range rec.Stores {
			if validStoreName(id, e.Name) {
				seen[e.Name] = true
			}
		}
	}
	if st, err := readState(mdir, id); err == nil && validLinkTarget(id, st.Store) {
		seen[filepath.Base(st.Store)] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
