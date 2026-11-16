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
	"fmt"
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

// ensureLayout creates the workspace key directory and the mirrors/ entries a
// restore writes into, each 0700. ~/.niwa itself is created if missing but
// otherwise left as it is: other niwa state lives there too.
func ensureLayout(home, workspaceRoot string) error {
	if err := os.MkdirAll(filepath.Join(home, ".niwa"), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Join(home, ".niwa"), err)
	}
	mdir := mirrorsDir(home, workspaceRoot)
	for _, dir := range []string{
		keyDir(home, workspaceRoot),
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

// syncDir flushes a directory's entries, so a rename in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
