// Package store keeps the last value each vault key resolved to, so a
// provisioning run can fall back on it when the vault can't be asked.
//
// There is one file per provider identity under Dir(). Its name is the
// SHA-256 of the identity's canonical form, so no identity field reaches
// a file name. Each identity also has an empty lock file that serialises
// Update across processes; Load takes no lock and relies on writers only
// ever renaming complete files into place.
//
// The store is trusted only when the current user owns it exclusively.
// A store directory or file that fails that check reads as empty and
// refuses writes. A file that can't be read or parsed, or that carries
// an unknown format version, also reads as empty, and the next Update
// replaces it.
//
// The package holds no state between calls. Values are plain text on
// disk; no error text ever carries one.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/vault"
)

// Entry is one stored key: the value it last resolved to, when, and the
// two halves of the vault.VersionToken the provider returned with it.
type Entry struct {
	Value        []byte    `json:"value"`
	ResolvedAt   time.Time `json:"resolved_at"`
	VersionToken string    `json:"version_token"`
	Provenance   string    `json:"provenance"`
}

// Update reports why it didn't write through exactly one of these
// sentinels, matched with errors.Is.
var (
	// ErrLockTimeout means another process held the identity's lock
	// for longer than the lock wait allows.
	ErrLockTimeout = errors.New("secret store lock wait timed out")

	// ErrInWorkTree means the store directory sits inside a git work
	// tree, so writing it could put secrets under version control.
	ErrInWorkTree = errors.New("secret store is inside a git work tree")

	// ErrUnwritable covers every other failed write: a store directory
	// or file that fails the ownership checks, a permission error, a
	// full disk, a failed rename.
	ErrUnwritable = errors.New("secret store is unwritable")
)

const (
	// formatVersion is the only data-file format this package reads.
	formatVersion = 1

	// maxDataFileSize caps what Load reads. A larger file is treated as
	// unreadable.
	maxDataFileSize = 1 << 20

	// lockTimeout and lockPoll bound Update's wait for the identity's
	// lock: a non-blocking attempt every lockPoll until lockTimeout.
	lockTimeout = 2 * time.Second
	lockPoll    = 20 * time.Millisecond
)

// Test hooks. Production code never sets them.
var (
	// testHookAfterRead runs inside Update right after the data file is
	// read, with the lock held.
	testHookAfterRead func()

	// testHookBeforeRename runs after the temp file is complete and
	// before it's renamed over the data file. A non-nil error aborts
	// the update there, leaving the temp file behind as a crash would.
	testHookBeforeRename func() error

	// testHookSkipLock makes Update proceed without taking the lock.
	testHookSkipLock bool

	// effectiveUID names the user the store must belong to.
	effectiveUID = os.Geteuid
)

// Dir returns the store directory: $XDG_STATE_HOME/niwa/secret-cache
// when XDG_STATE_HOME is an absolute path, otherwise
// $HOME/.local/state/niwa/secret-cache. It depends on nothing else, not
// the working directory, an instance, or niwa's configuration directory.
func Dir() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" || !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("locating the secret store: %w", err)
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "niwa", "secret-cache"), nil
}

// Load returns the entries stored for id in dir, without taking a lock.
// A missing store, and a data file that can't be read or parsed, has an
// unknown format version or names another identity, yield an empty map
// and a nil error. A store directory or data file that fails the
// ownership checks also yields an empty map, with an error wrapping
// ErrUnwritable that callers may ignore. The map is never nil.
func Load(dir string, id vault.Identity) (map[string]Entry, error) {
	empty := map[string]Entry{}
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return empty, nil
	}
	sd, err := openTrustedDir(dir)
	if err != nil {
		return empty, err
	}
	defer sd.close()

	stem, echo := fileStem(id)
	// A lock file that fails the checks makes the identity unwritable,
	// so it reads as empty too. Load looks at it but never opens it.
	if err := sd.checkLockFile(stem + ".lock"); err != nil {
		return empty, err
	}
	entries, err := sd.readData(stem+".json", echo)
	if err != nil {
		return empty, err
	}
	return entries, nil
}

// Update changes what's stored for id in dir under the identity's lock.
// It reads the current file (missing or bad counts as empty), removes
// every key when evictAll is set, then the keys in evictKeys, then
// stores puts, leaving every other key as it was. When that changes
// nothing, the file isn't rewritten. Otherwise the new contents are
// written to a temp file and renamed over the data file.
//
// Update creates dir (0700) when it's missing, unless dir sits inside a
// git work tree. Every error wraps ErrLockTimeout, ErrInWorkTree or
// ErrUnwritable.
func Update(dir string, id vault.Identity, puts map[string]Entry, evictKeys []string, evictAll bool) error {
	if InWorkTree(dir) {
		return fmt.Errorf("%w: %s", ErrInWorkTree, dir)
	}
	if err := createDir(dir); err != nil {
		return err
	}
	sd, err := openTrustedDir(dir)
	if err != nil {
		return err
	}
	defer sd.close()

	stem, echo := fileStem(id)
	unlock, err := sd.lock(stem + ".lock")
	if err != nil {
		return err
	}
	defer unlock()

	dataName := stem + ".json"
	current, err := sd.readData(dataName, echo)
	if err != nil {
		return err
	}
	if testHookAfterRead != nil {
		testHookAfterRead()
	}

	next, changed := merge(current, puts, evictKeys, evictAll)
	if !changed {
		return nil
	}

	tmpPrefix := "." + dataName + ".tmp-"
	sd.removeLeftovers(tmpPrefix)

	data, err := json.MarshalIndent(dataFile{
		FormatVersion: formatVersion,
		Identity:      echo,
		Keys:          next,
	}, "", "  ")
	if err != nil {
		return sd.unwritable("encoding "+dataName, err)
	}
	return sd.replace(dataName, tmpPrefix, append(data, '\n'))
}

// dataFile is the on-disk form of one identity's entries.
type dataFile struct {
	FormatVersion int               `json:"format_version"`
	Identity      map[string]string `json:"identity"`
	Keys          map[string]Entry  `json:"keys"`
}

// fileStem returns the lower-case hex SHA-256 that names id's files and
// the identity echo its data file carries. Both come from
// vault.NormalizeIdentity, so every spelling of one identity shares a
// file.
func fileStem(id vault.Identity) (string, map[string]string) {
	n := vault.NormalizeIdentity(id)
	fields := []string{n.Kind, n.APIDomain, n.ProjectID, n.Environment, n.FolderPath}
	// Encoding the fields as a JSON array keeps a separator inside one
	// field from making two identities collide.
	canonical, err := json.Marshal(fields)
	if err != nil {
		panic(fmt.Sprintf("store: encoding identity: %v", err)) // []string always encodes
	}
	sum := sha256.Sum256(canonical)
	echo := map[string]string{
		"kind":        n.Kind,
		"api_domain":  n.APIDomain,
		"project_id":  n.ProjectID,
		"environment": n.Environment,
		"folder_path": n.FolderPath,
	}
	return hex.EncodeToString(sum[:]), echo
}

// decode parses a data file, returning ok false for anything Load must
// treat as empty: invalid JSON, another format version, or an identity
// echo that doesn't match the one the file name was derived from.
func decode(data []byte, echo map[string]string) (map[string]Entry, bool) {
	var f dataFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, false
	}
	if f.FormatVersion != formatVersion || !sameEcho(f.Identity, echo) {
		return nil, false
	}
	entries := make(map[string]Entry, len(f.Keys))
	for k, e := range f.Keys {
		e.ResolvedAt = e.ResolvedAt.UTC()
		entries[k] = e
	}
	return entries, true
}

func sameEcho(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if got, ok := a[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// merge applies evictAll, then evictKeys, then puts to a copy of
// current, and reports whether the result differs from it.
func merge(current map[string]Entry, puts map[string]Entry, evictKeys []string, evictAll bool) (map[string]Entry, bool) {
	next := make(map[string]Entry, len(current)+len(puts))
	changed := false
	if evictAll {
		changed = len(current) > 0
	} else {
		for k, e := range current {
			next[k] = e
		}
		for _, k := range evictKeys {
			if _, ok := next[k]; ok {
				delete(next, k)
				changed = true
			}
		}
	}
	for k, e := range puts {
		e.ResolvedAt = e.ResolvedAt.UTC()
		if old, ok := next[k]; !ok || !sameEntry(old, e) {
			changed = true
		}
		next[k] = e
	}
	// A put that restores exactly what an eviction removed is no change.
	if changed && sameEntries(current, next) {
		changed = false
	}
	return next, changed
}

func sameEntry(a, b Entry) bool {
	return string(a.Value) == string(b.Value) && a.ResolvedAt.Equal(b.ResolvedAt) &&
		a.VersionToken == b.VersionToken && a.Provenance == b.Provenance
}

func sameEntries(a, b map[string]Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for k, e := range a {
		if other, ok := b[k]; !ok || !sameEntry(e, other) {
			return false
		}
	}
	return true
}

// createDir makes dir and any missing parents at 0700. A directory this
// call creates is set to exactly 0700 whatever the umask; an existing
// one is left for openTrustedDir to check.
func createDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("%w: creating %s: %w", ErrUnwritable, filepath.Dir(dir), err)
	}
	err := os.Mkdir(dir, 0o700)
	switch {
	case err == nil:
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("%w: setting the mode of %s: %w", ErrUnwritable, dir, err)
		}
	case !errors.Is(err, os.ErrExist):
		return fmt.Errorf("%w: creating %s: %w", ErrUnwritable, dir, err)
	}
	return nil
}

// removeLeftovers deletes temp files an earlier update left behind when
// it died before its rename. Failures are ignored: a leftover costs
// only disk space.
func (sd *storeDir) removeLeftovers(prefix string) {
	d, err := sd.root.Open(".")
	if err != nil {
		return
	}
	names, err := d.Readdirnames(-1)
	_ = d.Close()
	if err != nil {
		return
	}
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			_ = sd.root.Remove(name)
		}
	}
}

// replace writes data to a new temp file next to name and renames it
// over name, so a reader sees either the old file or the new one.
func (sd *storeDir) replace(name, tmpPrefix string, data []byte) error {
	tmpName, f, err := sd.createTemp(tmpPrefix)
	if err != nil {
		return sd.unwritable("creating a temp file", err)
	}
	fail := func(what string, err error) error {
		_ = f.Close()
		_ = sd.root.Remove(tmpName)
		return sd.unwritable(what, err)
	}
	if _, err := f.Write(data); err != nil {
		return fail("writing "+tmpName, err)
	}
	if err := f.Sync(); err != nil {
		return fail("syncing "+tmpName, err)
	}
	if err := f.Close(); err != nil {
		_ = sd.root.Remove(tmpName)
		return sd.unwritable("closing "+tmpName, err)
	}
	if err := sd.root.Chmod(tmpName, 0o600); err != nil {
		_ = sd.root.Remove(tmpName)
		return sd.unwritable("setting the mode of "+tmpName, err)
	}
	if testHookBeforeRename != nil {
		if err := testHookBeforeRename(); err != nil {
			return sd.unwritable("before renaming "+tmpName, err)
		}
	}
	if err := sd.root.Rename(tmpName, name); err != nil {
		_ = sd.root.Remove(tmpName)
		return sd.unwritable("renaming "+tmpName, err)
	}
	// Make the rename durable. A failure here doesn't undo it.
	_ = sd.dir.Sync()
	return nil
}

// createTemp creates a new file named prefix plus a random suffix,
// exclusively and at 0600.
func (sd *storeDir) createTemp(prefix string) (string, *os.File, error) {
	for range 10 {
		var b [8]byte
		if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
			return "", nil, err
		}
		name := prefix + hex.EncodeToString(b[:])
		f, err := sd.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|oNoFollow, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, f, nil
	}
	return "", nil, errors.New("no unused temp file name")
}
