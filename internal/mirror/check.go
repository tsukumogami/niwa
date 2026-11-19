package mirror

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// looseDirPattern matches the fan-out directories that hold loose objects,
// and looseNamePattern a loose object's file name inside one: the rest of a
// SHA-1 in hex. The check and salvage both read them.
var (
	looseDirPattern  = regexp.MustCompile(`^[0-9a-f]{2}$`)
	looseNamePattern = regexp.MustCompile(`^[0-9a-f]{38}$`)
)

// check runs the offline check of one mirror and returns the first fault it
// finds. It reads local files only: the link, the state file, the store's
// objects and its HEAD. git runs only for a pack whose ctime moved and for
// HEAD, both against local files.
//
// The faults are checked in this order, so each damage names what's wrong
// rather than a symptom of it: the link and the state file first (missing),
// then whether the store was cloned from the URL the config gives now
// (url-changed), then whether the store holds any object at all
// (objects-empty), then each pack the manifest lists (pack-missing,
// pack-changed), and last whether the store is a repository git can read
// (repo-unreadable).
//
// Before anything else, the workspace key directory, mirrors/, .meta/ and
// .store/ must each be a real directory the user owns (layoutIsReal); a link
// at any of them is missing, and nothing is read through it.
func check(ctx context.Context, mdir string, r Repo) Fault {
	if !layoutIsReal(mdir) {
		return FaultMissing
	}
	stable := filepath.Join(mdir, r.ID)
	info, err := os.Lstat(stable)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return FaultMissing
	}
	target, err := os.Readlink(stable)
	if err != nil {
		return FaultMissing
	}
	// A store with no state file, or one recording another store, is one
	// niwa can't vouch for: it may be a store a restore never finished
	// recording, so it's rebuilt.
	rec, err := readState(mdir, r.ID)
	if err != nil || rec.Store != target {
		return FaultMissing
	}
	return checkStore(ctx, mdir, r, rec)
}

// urlChanged reports whether the config's URL for r is another remote than
// the one rec was cloned from. Both sides go through recordURL, so a URL that
// differs only in userinfo, query or fragment is the same remote. A repo with
// no URL (a caller that found none to give) is never compared.
func urlChanged(r Repo, rec state) bool {
	return r.URL != "" && recordURL(r.URL) != rec.URL
}

// checkStore checks the store rec records, as though the stable link named
// it: everything check looks at past the link itself. A restore also runs it
// on a recorded store that lost its link, to tell whether relinking it is
// enough.
func checkStore(ctx context.Context, mdir string, r Repo, rec state) Fault {
	if !validLinkTarget(r.ID, rec.Store) {
		return FaultMissing
	}
	store := filepath.Join(mdir, rec.Store)
	st, err := os.Lstat(store)
	if err != nil || !st.IsDir() || !ownedByUser(st) {
		return FaultMissing
	}
	if urlChanged(r, rec) {
		return FaultURLChanged
	}

	if !holdsObjects(store) {
		return FaultObjectsEmpty
	}
	if f := checkPacks(ctx, store, rec.Packs); f != FaultNone {
		return f
	}
	for _, name := range []string{"HEAD", "config"} {
		if fi, err := os.Lstat(filepath.Join(store, name)); err != nil || !fi.Mode().IsRegular() {
			return FaultRepoUnreadable
		}
	}
	// packed-refs may be absent, but one that's there must be a regular file:
	// git would follow a link there to whatever it names.
	if fi, err := os.Lstat(filepath.Join(store, "packed-refs")); err == nil && !fi.Mode().IsRegular() ||
		err != nil && !errors.Is(err, fs.ErrNotExist) {
		return FaultRepoUnreadable
	}
	if headCommitReadable(ctx, store) != nil {
		return FaultRepoUnreadable
	}
	return FaultNone
}

// holdsObjects reports whether store's objects/ is a directory with at least
// one file under objects/pack/ or one loose object under objects/<2 hex>/.
func holdsObjects(store string) bool {
	objects := filepath.Join(store, "objects")
	if fi, err := os.Lstat(objects); err != nil || !fi.IsDir() {
		return false
	}
	if hasRegularFile(filepath.Join(objects, "pack"), func(string) bool { return true }) {
		return true
	}
	entries, err := os.ReadDir(objects)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !looseDirPattern.MatchString(e.Name()) || !e.IsDir() {
			continue
		}
		if hasRegularFile(filepath.Join(objects, e.Name()), looseNamePattern.MatchString) {
			return true
		}
	}
	return false
}

// hasRegularFile reports whether dir is a directory holding a regular file
// whose name keep accepts.
func hasRegularFile(dir string, keep func(string) bool) bool {
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Type().IsRegular() && keep(e.Name()) {
			return true
		}
	}
	return false
}

// checkPacks compares each pack the manifest lists with the store. A pack or
// its .idx gone is pack-missing. A size or trailing checksum that differs is
// pack-changed. A moved ctime alone isn't: a chmod or a backup tool moves it
// without touching the bytes, so only a pack that also fails git verify-pack
// is pack-changed. Packs the manifest doesn't list are ignored.
func checkPacks(ctx context.Context, store string, packs []packEntry) Fault {
	dir := filepath.Join(store, "objects", "pack")
	for _, want := range packs {
		if !validPackName(want.Name) {
			// niwa never writes such a name, so the state file itself
			// can't be vouched for, as when it names another store.
			return FaultMissing
		}
		pack := filepath.Join(dir, want.Name)
		idx := strings.TrimSuffix(pack, ".pack") + ".idx"
		if fi, err := os.Lstat(idx); err != nil || !fi.Mode().IsRegular() {
			return FaultPackMissing
		}
		got, err := describePack(pack)
		if errors.Is(err, fs.ErrNotExist) {
			return FaultPackMissing
		}
		if err != nil || got.Size != want.Size || got.Trailer != want.Trailer {
			return FaultPackChanged
		}
		if got.CtimeNs != want.CtimeNs && verifyPack(ctx, idx) != nil {
			return FaultPackChanged
		}
	}
	return FaultNone
}

// packNamePattern is the name git gives a pack in a SHA-1 repository, the
// only kind of store here (see packTrailerLen and looseNamePattern).
var packNamePattern = regexp.MustCompile(`^pack-[0-9a-f]{40}\.pack$`)

// validPackName reports whether name is one a manifest may list. A name with
// a path separator in it would reach outside objects/pack/.
func validPackName(name string) bool { return packNamePattern.MatchString(name) }
