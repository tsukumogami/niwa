package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
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
// whole one. It refuses a record with NewerVersion set and writes nothing.
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
