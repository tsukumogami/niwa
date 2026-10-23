package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/lineage"
)

// LineageDirName is the lineage store's directory under the workspace root.
// It sits beside the instances and outside the workspace configuration
// directory, so neither a reap nor a configuration refresh touches it.
const LineageDirName = ".niwa-lineage"

// MaxLineageEntryBytes is the most a lineage read takes from one file.
const MaxLineageEntryBytes = 8 << 10

// LineageDir returns the lineage store's directory for a workspace root.
func LineageDir(root string) string {
	return filepath.Join(root, LineageDirName)
}

// ErrLineageEntryExists refuses a create for a dispatch id that already has
// an entry; the existing entry is left as it was.
var ErrLineageEntryExists = errors.New("a lineage entry already exists for this dispatch id")

// errLineageDispatchID refuses a dispatch id that isn't one before it can
// name a file.
var errLineageDispatchID = errors.New("not a dispatch id")

// lineageEntryPath joins a checked dispatch id into the store's path. Every
// store function that takes a dispatch id goes through it, whatever record
// the id came from.
func lineageEntryPath(root, dispatchID string) (string, error) {
	if !authority.ValidDispatchID(dispatchID) {
		return "", errLineageDispatchID
	}
	return filepath.Join(LineageDir(root), dispatchID+".json"), nil
}

// lineageDirChmod tightens the store's directory through an open handle. It
// is a variable so a test can make the tightening fail.
var lineageDirChmod = func(f *os.File, mode os.FileMode) error { return f.Chmod(mode) }

// prepareLineageDir readies the store's directory for a write: it creates it
// at 0700 when it is missing, refuses a path that is a symbolic link or not a
// directory, and sets the directory to 0700 through a handle opened with
// O_NOFOLLOW, so a directory left wider by hand is tightened before an entry
// goes into it. A failure to tighten (another user owns it, say) refuses the
// write. It runs on every write, so the first write in each process tightens
// the directory; only writes call it, so show and list leave the mode alone.
func prepareLineageDir(root string) (string, error) {
	dir := LineageDir(root)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("creating the lineage store: %w", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", fmt.Errorf("checking the lineage store: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("the lineage store %s is a symbolic link; niwa won't write through it", dir)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("the lineage store %s is not a directory", dir)
	}
	f, err := os.OpenFile(dir, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", fmt.Errorf("opening the lineage store: %w", err)
	}
	defer f.Close()
	if err := lineageDirChmod(f, 0o700); err != nil {
		return "", fmt.Errorf("making the lineage store %s private: %w", dir, err)
	}
	return dir, nil
}

// placeLineageFile writes data to a dot-prefixed temporary file at 0600 in
// dir, syncs it, and renames it to final, so a reader sees either the old
// file (or none) or the whole new one. The temporary is removed on every
// failure.
func placeLineageFile(dir, dispatchID, final string, data []byte) error {
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("naming the lineage entry's temporary file: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+dispatchID+".json."+hex.EncodeToString(suffix[:]))
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("creating the lineage entry: %w", err)
	}
	// Runs on every path; after a successful rename the temp name is gone
	// and both calls are no-ops.
	defer func() {
		f.Close()
		os.Remove(tmpPath)
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing the lineage entry: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing the lineage entry: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the lineage entry: %w", err)
	}
	if err := os.Rename(tmpPath, final); err != nil {
		return fmt.Errorf("placing the lineage entry: %w", err)
	}
	return nil
}

// CreateLineageEntry writes e as a new entry in the workspace root's lineage
// store, readying the store's directory first (prepareLineageDir). The entry
// is written to a temporary file at 0600 and renamed into place. A dispatch
// id that already has an entry is refused with ErrLineageEntryExists and the
// existing entry is not touched.
func CreateLineageEntry(root string, e lineage.Entry) error {
	final, err := lineageEntryPath(root, e.DispatchID)
	if err != nil {
		return fmt.Errorf("writing the lineage entry: %w", err)
	}
	data, err := lineage.Marshal(e)
	if err != nil {
		return fmt.Errorf("encoding the lineage entry: %w", err)
	}
	data = append(data, '\n')

	dir, err := prepareLineageDir(root)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(final); err == nil {
		return ErrLineageEntryExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking for an existing lineage entry: %w", err)
	}
	return placeLineageFile(dir, e.DispatchID, final, data)
}

// RemoveLineageEntry removes a dispatch's entry. An entry that isn't there
// is not an error.
func RemoveLineageEntry(root, dispatchID string) error {
	path, err := lineageEntryPath(root, dispatchID)
	if err != nil {
		return fmt.Errorf("removing the lineage entry: %w", err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing the lineage entry: %w", err)
	}
	return nil
}

// StoredLineageEntry is one entry read from the store: its bytes as stored
// and what they parsed to.
type StoredLineageEntry struct {
	Raw    []byte
	Parsed lineage.Parsed
}

// LineageEntryError is an entry file that exists but can't be used. Like
// AuthorityRecordError, its message names only the failure class, never a
// value read from the file.
type LineageEntryError struct {
	Class string
	Err   error
}

func (e *LineageEntryError) Error() string { return "lineage entry " + e.Class }
func (e *LineageEntryError) Unwrap() error { return e.Err }

// The lineage-only failure classes, beside the RecordClass ones.
var (
	// LineageClassTooLarge is the class for an entry file over the read cap.
	LineageClassTooLarge = "is larger than " + strconv.Itoa(MaxLineageEntryBytes) + " bytes"
	// LineageClassWideMode is the class for an entry file whose mode grants
	// access to anyone but its owner.
	LineageClassWideMode = "grants access beyond its owner"
	// LineageClassOtherDispatch is the class for an entry file whose
	// dispatch_id is not the one its name carries.
	LineageClassOtherDispatch = "names another dispatch"
)

// ReadLineageEntry reads a dispatch's entry from the workspace root's store.
// found is false, with no error, when there is no entry. The open uses
// O_NOFOLLOW and O_NONBLOCK, the handle must be a regular file whose mode
// grants nothing beyond its owner, and at most MaxLineageEntryBytes are read,
// as ReadAuthorityRecord does; a file that fails any of that, doesn't parse
// (lineage.Parse re-validates every known field and refuses a duplicate
// member), or names another dispatch is a *LineageEntryError. It never
// changes the store.
func ReadLineageEntry(root, dispatchID string) (entry StoredLineageEntry, found bool, err error) {
	path, err := lineageEntryPath(root, dispatchID)
	if err != nil {
		return StoredLineageEntry{}, false, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return StoredLineageEntry{}, false, nil
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.EMLINK):
			return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassSymlink, Err: err}
		}
		return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassUnreadable, Err: err}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassUnreadable, Err: err}
	}
	if !info.Mode().IsRegular() {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassNotRegular}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: LineageClassWideMode}
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxLineageEntryBytes+1))
	if err != nil {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassUnreadable, Err: err}
	}
	if len(data) > MaxLineageEntryBytes {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: LineageClassTooLarge}
	}
	parsed, err := lineage.Parse(data)
	if err != nil {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: RecordClassMalformed, Err: err}
	}
	if parsed.Entry.DispatchID != dispatchID {
		return StoredLineageEntry{}, true, &LineageEntryError{Class: LineageClassOtherDispatch}
	}
	return StoredLineageEntry{Raw: data, Parsed: parsed}, true, nil
}

// LineageListResult is one selected file of the store: its dispatch id, and
// either the entry read from it (a later version included, as
// Entry.Parsed.LaterVersion) or why it couldn't be read.
type LineageListResult struct {
	DispatchID string
	Entry      StoredLineageEntry
	Err        error
}

// ListLineageEntries reads every entry in the workspace root's store. Files
// are selected by name alone: `<dispatch id>.json`, so the dot-prefixed
// temporaries and every other name are skipped, and whether the content
// parses never decides it. Each selected file gives one result, read as
// ReadLineageEntry reads it, so one that isn't a regular file within the cap
// is reported as a per-file error. The results are in file-name order. A
// store that doesn't exist lists nothing; an error is returned only when the
// store's directory exists and can't be read.
func ListLineageEntries(root string) ([]LineageListResult, error) {
	names, err := os.ReadDir(LineageDir(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading the lineage store: %w", err)
	}
	var out []LineageListResult
	for _, de := range names {
		id, ok := strings.CutSuffix(de.Name(), ".json")
		if !ok || !authority.ValidDispatchID(id) {
			continue
		}
		stored, found, err := ReadLineageEntry(root, id)
		if err == nil && !found {
			// Gone between the listing and the read.
			continue
		}
		out = append(out, LineageListResult{DispatchID: id, Entry: stored, Err: err})
	}
	return out, nil
}
