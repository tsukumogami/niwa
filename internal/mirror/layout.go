// Package mirror keeps the host's per-workspace git mirrors usable.
//
// A mirror is a bare repo that instance clones (borrowers) read objects from
// through their alternates file. Each one sits behind a stable path,
//
//	~/.niwa/w-<workspace key>/mirrors/<repo-id>
//
// a symlink to a store directory beside it under .store/, recorded with its
// pack manifest in .meta/<repo-id>.json. Borrowers name the
// stable path, never the store, so a restore that builds a new store and
// renames a new link over the old one replaces the mirror atomically without
// touching any borrower.
//
// The package imports internal/filelock and never internal/workspace; the
// callers pass in the repos, their URLs and the instance clone paths.
package mirror

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// repoIDPattern is what a repo id must look like to get a mirror. No id
// starts with a dot, so none can take niwa's own names under mirrors/
// (.meta, .store, .tmp, .link-*). A repo whose name doesn't match is never
// mirrored and clones in full.
var repoIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

// storeStampLayout is the UTC stamp in a store directory's name.
const storeStampLayout = "20060102T150405Z"

// ValidRepoID reports whether id may name a mirror.
func ValidRepoID(id string) bool {
	return repoIDPattern.MatchString(id)
}

// WorkspaceKey names a workspace's directory under ~/.niwa: "w-" and the first
// 32 hex digits of the sha256 of the workspace root's canonical path, so the
// same root reached through a symlink or a trailing slash gets one key.
func WorkspaceKey(workspaceRoot string) string {
	sum := sha256.Sum256([]byte(filelock.CanonicalPath(workspaceRoot)))
	return "w-" + hex.EncodeToString(sum[:])[:32]
}

// keyDir is the workspace key directory, ~/.niwa/w-<key>.
func keyDir(home, workspaceRoot string) string {
	return filepath.Join(home, ".niwa", WorkspaceKey(workspaceRoot))
}

// mirrorsDir holds every mirror of the workspace and niwa's own entries.
func mirrorsDir(home, workspaceRoot string) string {
	return filepath.Join(keyDir(home, workspaceRoot), "mirrors")
}

// StablePath is the path borrowers name: their alternates line is
// StablePath(...) + "/objects". It stays the same across restores.
func StablePath(home, workspaceRoot, repoID string) string {
	return filepath.Join(mirrorsDir(home, workspaceRoot), repoID)
}

// LockPath is the per-mirror lock in niwa's lock directory. The name hashes
// the workspace key and the repo id rather than the stable path, because
// CanonicalPath resolves symlinks and would give each store its own lock.
func LockPath(home, workspaceRoot, repoID string) string {
	sum := sha256.Sum256([]byte(WorkspaceKey(workspaceRoot) + "/" + repoID))
	return filepath.Join(home, ".niwa", "locks", "m-"+hex.EncodeToString(sum[:])[:32]+".lock")
}

// storeNamePattern matches the name of a store directory of repoID and of no
// other repo: "<id>-<UTC stamp>-<8 hex>". A plain "<id>-*" glob would also
// match a store of a repo named "<id>-something".
func storeNamePattern(repoID string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(repoID) + `-[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
}

// newStoreName names a new store directory for repoID.
func newStoreName(repoID string, now time.Time) (string, error) {
	suffix, err := randHex()
	if err != nil {
		return "", err
	}
	return repoID + "-" + now.UTC().Format(storeStampLayout) + "-" + suffix, nil
}

// validLinkTarget reports whether target, read from the stable path's link, is
// one niwa would have written: relative, ".store/" and a store name of repoID,
// with nothing that could step outside mirrors/.
func validLinkTarget(repoID, target string) bool {
	dir, name := filepath.Split(target)
	return dir == ".store/" && storeNamePattern(repoID).MatchString(name)
}

// randHex returns 8 random hex digits for temporary and store names.
func randHex() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// ownedByUser reports whether info belongs to the current user. It reads the
// unix owner, so the package builds for unix only, like the flock it relies on
// and every platform niwa releases for.
func ownedByUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// ensurePrivateDir makes dir a real directory the user owns with mode exactly
// 0700, creating it when missing and tightening a wider mode. The mode is set
// explicitly, never left to the umask. A symlink or anything else at dir is
// refused, never followed.
func ensurePrivateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory (mode %s)", dir, info.Mode().Type())
	}
	if !ownedByUser(info) {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("setting the mode of %s: %w", dir, err)
		}
	}
	return nil
}

// createPrivateDir makes dir a real directory the user owns, creating it with
// mode exactly 0700 when missing and leaving an existing directory's mode as
// it is. The mode of a new directory is set explicitly, never left to the
// umask. A symlink or anything else at dir is refused, never followed.
//
// It's for a level the mirror shares, the workspace key directory: the plugin
// store keeps its plugins/ there too and reads a narrowed mode on it as the
// store being unusable, so the mirror never changes that mode.
func createPrivateDir(dir string) error {
	created := true
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		created = false
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory (mode %s)", dir, info.Mode().Type())
	}
	if !ownedByUser(info) {
		return fmt.Errorf("%s is not owned by the current user", dir)
	}
	if created && info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("setting the mode of %s: %w", dir, err)
		}
	}
	return nil
}

// ensureLayout creates the workspace key directory and the mirrors/ entries a
// restore writes into. The mirrors/ entries are the mirror's alone and are
// each made 0700 (ensurePrivateDir). The key directory is shared, so it's
// created 0700 when missing and otherwise keeps its mode (createPrivateDir):
// one narrowed so far that a missing mirrors/ can't be made fails here, and
// the restore reports it. ~/.niwa itself is created if missing but otherwise
// left as it is: other niwa state lives there too.
func ensureLayout(home, workspaceRoot string) error {
	if err := os.MkdirAll(filepath.Join(home, ".niwa"), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Join(home, ".niwa"), err)
	}
	if err := createPrivateDir(keyDir(home, workspaceRoot)); err != nil {
		return err
	}
	mdir := mirrorsDir(home, workspaceRoot)
	for _, dir := range []string{
		mdir,
		metaDir(mdir),
		filepath.Join(mdir, ".store"),
		filepath.Join(mdir, ".tmp"),
	} {
		if err := ensurePrivateDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// emptyHooksDir is the directory every git call names as core.hooksPath,
// ~/.niwa/empty-hooks: real, empty, the user's own and 0700, so no hook runs.
func emptyHooksDir(home string) string {
	return filepath.Join(home, ".niwa", "empty-hooks")
}

// realOwnedDir reports whether path is, by Lstat, a directory the user owns.
// A link to a directory isn't one.
func realOwnedDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && ownedByUser(info)
}

// tighten sets path's mode to perm when it's a directory (wantDir) or a
// regular file the user owns and has another mode. It opens path without
// following a link and without blocking on a fifo, then reads the type and
// owner from the open file and changes the mode through it, so nothing is
// ever changed through a link and a fifo, a socket or a device is left
// alone. Only the last path component is guarded: the caller checks the
// directories above it. It creates nothing, and a failure changes nothing.
func tighten(path string, perm os.FileMode, wantDir bool) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !ownedByUser(info) {
		return
	}
	if wantDir && !info.IsDir() || !wantDir && !info.Mode().IsRegular() {
		return
	}
	if info.Mode().Perm() != perm {
		_ = f.Chmod(perm)
	}
}

// checkEmptyHooks returns an error naming dir unless it's a real directory
// the user owns, with mode exactly 0700 and no entry in it. It opens dir
// without following a link and only reads: nothing in it is removed,
// followed or moved.
func checkEmptyHooks(dir string) error {
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("the empty hooks directory %s is not a real directory: %w", dir, err)
	}
	defer f.Close()
	info, err := f.Stat()
	switch {
	case err != nil:
		return fmt.Errorf("inspecting the empty hooks directory %s: %w", dir, err)
	case !info.IsDir():
		return fmt.Errorf("the empty hooks directory %s is not a directory", dir)
	case !ownedByUser(info):
		return fmt.Errorf("the empty hooks directory %s is not owned by the current user", dir)
	case info.Mode().Perm() != 0o700:
		return fmt.Errorf("the empty hooks directory %s has mode %04o, want 0700", dir, info.Mode().Perm())
	}
	if _, err := f.Readdirnames(1); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("the empty hooks directory %s is not empty", dir)
		}
		return fmt.Errorf("reading the empty hooks directory %s: %w", dir, err)
	}
	return nil
}

// prepareEmptyHooks makes ~/.niwa/empty-hooks a real directory the user owns
// with mode 0700, creating it when missing and tightening a wider mode, and
// then checks it's empty (checkEmptyHooks). It never removes, renames or
// follows anything: a link, a file or a planted entry there is left as it is
// and reported, and gitRun then refuses every git call.
func prepareEmptyHooks(home string) error {
	niwa := filepath.Join(home, ".niwa")
	if err := os.MkdirAll(niwa, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", niwa, err)
	}
	dir := emptyHooksDir(home)
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	tighten(dir, 0o700, true)
	return checkEmptyHooks(dir)
}

// layoutIsReal reports whether the workspace key directory, mirrors/, .meta/
// and .store/ are each a real directory the user owns. When one isn't, the
// check reports the mirror missing and the restore's ensureLayout refuses
// it, so nothing is read or written through a link there.
func layoutIsReal(mdir string) bool {
	for _, dir := range []string{filepath.Dir(mdir), mdir, metaDir(mdir), filepath.Join(mdir, ".store")} {
		if !realOwnedDir(dir) {
			return false
		}
	}
	return true
}

// tightenModes puts the modes of niwa's own entries for one mirror back:
// mirrors/, .meta/, .store/, .tmp/, ~/.niwa/locks/ and ~/.niwa/empty-hooks/
// to 0700, and the state file, the mirror log, mirror.log.1 and the mirror's
// lock file to 0600 (see tighten). The workspace key directory above
// mirrors/ is shared with the plugin store, so its mode is left as it is
// (see createPrivateDir). It creates nothing. Each entry is tightened only
// when every directory above it, up to ~/.niwa, is real, since tighten
// guards only the last path component: through a linked .meta/ it would
// change a file elsewhere.
func tightenModes(home, workspaceRoot, repoID string) {
	key := keyDir(home, workspaceRoot)
	if realOwnedDir(key) {
		mdir := mirrorsDir(home, workspaceRoot)
		if realOwnedDir(mdir) {
			tighten(mdir, 0o700, true)
			for _, dir := range []string{metaDir(mdir), filepath.Join(mdir, ".store"), filepath.Join(mdir, ".tmp")} {
				if realOwnedDir(dir) {
					tighten(dir, 0o700, true)
				}
			}
			if realOwnedDir(metaDir(mdir)) {
				log := logPath(mdir)
				for _, file := range []string{statePath(mdir, repoID), log, log + ".1"} {
					tighten(file, 0o600, false)
				}
			}
		}
	}
	lock := LockPath(home, workspaceRoot, repoID)
	if realOwnedDir(filepath.Dir(lock)) {
		tighten(filepath.Dir(lock), 0o700, true)
		tighten(lock, 0o600, false)
	}
	if hooks := emptyHooksDir(home); realOwnedDir(hooks) {
		tighten(hooks, 0o700, true)
	}
}

// syncDir flushes a directory's entries, so a rename in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
