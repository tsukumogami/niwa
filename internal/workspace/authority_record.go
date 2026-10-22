package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/tsukumogami/niwa/internal/authority"
)

// AuthorityRecordFile is the authority record's name inside an instance's
// .niwa directory.
const AuthorityRecordFile = "authority.json"

// AuthorityRecordPath returns where an instance's authority record lives.
func AuthorityRecordPath(instancePath string) string {
	return filepath.Join(instancePath, StateDir, AuthorityRecordFile)
}

// ErrNewerVersionRecord refuses to write a record read from a later schema
// version: such a record is a read-side view holding nothing, and storing it
// would replace what a newer niwa wrote with less.
var ErrNewerVersionRecord = errors.New("refusing to write an authority record read from a newer schema version")

// WriteAuthorityRecord writes rec as the instance's authority record, mode
// 0600, through a temporary file opened O_EXCL|O_NOFOLLOW, synced, then
// renamed over the final path, so a reader sees either no record or the
// whole one. It refuses a record with NewerVersion set and writes nothing,
// since such a record is a read-side view that would replace what a newer
// niwa wrote with less. Instances are only built on Linux and macOS, where
// O_NOFOLLOW is available.
func WriteAuthorityRecord(instancePath string, rec authority.Record) error {
	if rec.NewerVersion != "" {
		return ErrNewerVersionRecord
	}
	data, err := rec.MarshalJSON()
	if err != nil {
		return fmt.Errorf("encoding the authority record: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Join(instancePath, StateDir)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Errorf("naming the authority record's temporary file: %w", err)
	}
	tmpPath := filepath.Join(dir, "."+AuthorityRecordFile+"."+hex.EncodeToString(suffix[:]))
	f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("creating the authority record: %w", err)
	}
	// Runs on every path; after a successful rename the temp name is gone
	// and both calls are no-ops.
	defer func() {
		f.Close()
		os.Remove(tmpPath)
	}()
	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("writing the authority record: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("flushing the authority record: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing the authority record: %w", err)
	}
	if err := os.Rename(tmpPath, AuthorityRecordPath(instancePath)); err != nil {
		return fmt.Errorf("placing the authority record: %w", err)
	}
	return nil
}

// AuthorityRecordError is a record that exists but can't be used. Its message
// names only the failure class: never the record's path, which a caller
// prints beside it when it wants to, and never anything read from the file,
// which any same-user process can write. Err keeps the underlying cause for
// errors.As (a *authority.MalformedRecordError among them) without printing
// it.
type AuthorityRecordError struct {
	Class string
	Err   error
}

func (e *AuthorityRecordError) Error() string { return "authority record " + e.Class }
func (e *AuthorityRecordError) Unwrap() error { return e.Err }

// The failure classes ReadAuthorityRecord reports.
const (
	RecordClassSymlink    = "is a symbolic link"
	RecordClassNotRegular = "is not a regular file"
	RecordClassTooLarge   = "is larger than 65536 bytes"
	RecordClassUnreadable = "cannot be read"
	RecordClassMalformed  = "is malformed"
)

// ReadAuthorityRecord reads an instance's authority record. found is false,
// with no error, when there is no record file, so a caller can tell an
// instance with no record (one dispatched before records existed, or never
// dispatched) from one whose record is invalid, which is an
// *AuthorityRecordError.
//
// The open uses O_NOFOLLOW, so a symlink at the record path is refused rather
// than followed, and O_NONBLOCK, so a FIFO planted there returns at once
// instead of blocking the caller until something writes to it; the handle is
// then checked to be a regular file. At most authority.MaxRecordBytes are
// read, which is the cap Resolve sizes every written record against, and the
// bytes go through authority.ParseRecord.
func ReadAuthorityRecord(instancePath string) (rec authority.Record, found bool, err error) {
	f, err := os.OpenFile(AuthorityRecordPath(instancePath), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			return authority.Record{}, false, nil
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.EMLINK):
			// ELOOP on Linux and macOS; FreeBSD reports EMLINK.
			return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassSymlink, Err: err}
		}
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassUnreadable, Err: err}
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassUnreadable, Err: err}
	}
	if !info.Mode().IsRegular() {
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassNotRegular}
	}
	data, err := io.ReadAll(io.LimitReader(f, authority.MaxRecordBytes+1))
	if err != nil {
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassUnreadable, Err: err}
	}
	if len(data) > authority.MaxRecordBytes {
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassTooLarge}
	}
	rec, err = authority.ParseRecord(data)
	if err != nil {
		return authority.Record{}, true, &AuthorityRecordError{Class: RecordClassMalformed, Err: err}
	}
	return rec, true, nil
}
