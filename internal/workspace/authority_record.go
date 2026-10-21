package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tsukumogami/niwa/internal/authority"
)

// AuthorityRecordFile is the file, under an instance's .niwa directory, that
// holds the authority level the instance's dispatch resolved. Dispatch writes
// it right after provisioning and before it launches the worker, so a session
// that asks for its level before its session mapping exists, or an agent that
// sets no session id, can still find its dispatch's level. It lives inside the
// instance, so destroying the instance removes it.
const AuthorityRecordFile = "authority.json"

// AuthorityRecord is the content of an instance's authority record: the
// resolved level and the setting it came from, spelled the way the session
// mapping spells them.
type AuthorityRecord struct {
	Level  string `json:"authority_level"`
	Source string `json:"authority_source"`
}

// recordedAuthoritySources are the sources a dispatch can resolve a level
// from. "declaration" is deliberately absent: it names where a decision-point
// answer came from, never a dispatch's level.
var recordedAuthoritySources = map[authority.Source]bool{
	authority.SourceFlag:      true,
	authority.SourceMachine:   true,
	authority.SourceWorkspace: true,
	authority.SourceDefault:   true,
}

// Validate reports whether the record holds exactly one of the three level
// names and one of the four dispatch sources. Every reader checks this before
// it uses a recorded value: the record and the session mapping are writable by
// any same-user process, and a value that only looks like a level must never
// be read as one.
func (r AuthorityRecord) Validate() error {
	l, err := authority.Parse(r.Level)
	if err != nil || l == nil || l.String() != r.Level {
		return fmt.Errorf("recorded authority_level %q is not one of %v", r.Level, authority.Names())
	}
	if !recordedAuthoritySources[authority.Source(r.Source)] {
		return fmt.Errorf("recorded authority_source %q is not one of flag, machine, workspace, default", r.Source)
	}
	return nil
}

// AuthorityRecordPath is where instanceDir's authority record lives.
func AuthorityRecordPath(instanceDir string) string {
	return filepath.Join(instanceDir, StateDir, AuthorityRecordFile)
}

// WriteAuthorityRecord writes r to instanceDir's authority record with mode
// 0600, through a temporary file and a rename so a reader never sees half a
// record. It refuses a record that would not pass Validate.
func WriteAuthorityRecord(instanceDir string, r AuthorityRecord) error {
	if err := r.Validate(); err != nil {
		return err
	}
	target := AuthorityRecordPath(instanceDir)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(target), err)
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling authority record: %w", err)
	}
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing authority record: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("finalizing authority record: %w", err)
	}
	return nil
}

// ReadAuthorityRecord reads instanceDir's authority record. A missing record
// returns an error that satisfies errors.Is(err, fs.ErrNotExist), so a caller
// can tell "this instance has no record" apart from a broken one. A record
// that does not parse, or holds a value Validate rejects, is an error, never
// an empty record.
func ReadAuthorityRecord(instanceDir string) (AuthorityRecord, error) {
	path := AuthorityRecordPath(instanceDir)
	data, err := os.ReadFile(path)
	if err != nil {
		return AuthorityRecord{}, err
	}
	var r AuthorityRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return AuthorityRecord{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := r.Validate(); err != nil {
		return AuthorityRecord{}, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}
