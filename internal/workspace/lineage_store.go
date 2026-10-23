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

// lineageEntryPath joins a checked dispatch id into the store's path.
func lineageEntryPath(root, dispatchID string) (string, error) {
	if !authority.ValidDispatchID(dispatchID) {
		return "", errLineageDispatchID
	}
	return filepath.Join(LineageDir(root), dispatchID+".json"), nil
}

// CreateLineageEntry writes e as a new entry in the workspace root's lineage
// store, creating the store's directory at 0700 when it is missing. The entry
// is written to a dot-prefixed temporary file at 0600, synced, and renamed
// into place, so a reader sees either no entry or the whole one. A dispatch
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

	dir := LineageDir(root)
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("creating the lineage store: %w", err)
	}
	if _, err := os.Lstat(final); err == nil {
		return ErrLineageEntryExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking for an existing lineage entry: %w", err)
	}

	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("naming the lineage entry's temporary file: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+e.DispatchID+".json."+hex.EncodeToString(suffix[:]))
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

// LineageClassTooLarge is the class for an entry file over the read cap.
var LineageClassTooLarge = "is larger than " + strconv.Itoa(MaxLineageEntryBytes) + " bytes"

// ReadLineageEntry reads a dispatch's entry from the workspace root's store.
// found is false, with no error, when there is no entry. The open uses
// O_NOFOLLOW and O_NONBLOCK, the handle must be a regular file, and at most
// MaxLineageEntryBytes are read, as ReadAuthorityRecord does; a file that
// fails any of that, doesn't parse, or names another dispatch is a
// *LineageEntryError.
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
		return StoredLineageEntry{}, true, &LineageEntryError{Class: "names another dispatch"}
	}
	return StoredLineageEntry{Raw: data, Parsed: parsed}, true, nil
}
