package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tsukumogami/niwa/internal/authority"
	"github.com/tsukumogami/niwa/internal/config"
)

// The launch store keeps, for each worker `niwa dispatch` launched, what it
// launched the worker with: the permission mode, the session name, the model,
// the settings document and the environment niwa set. It exists so a worker
// can be brought back in the posture it was dispatched with after its
// instance is gone, which is why it lives beside the lineage store, outside
// every instance and outside the configuration directory: `niwa destroy`
// removes an instance and a refresh replaces the configuration directory, and
// neither may take a record with it.
//
// A record is kept for as long as Claude Code still holds the session's
// transcript, since without a transcript there is nothing to resume. The reap
// removes the rest (see the reap's launch-record pass).

// LaunchDirName is the launch store's directory under the workspace root.
const LaunchDirName = ".niwa-launch"

// LaunchRecordSchema is the schema every launch record carries.
const LaunchRecordSchema = "niwa.launch/v1"

// MaxLaunchRecordBytes is the most a launch record read takes from one file.
const MaxLaunchRecordBytes = 64 << 10

// LaunchRecord is what one dispatch launched a worker with.
type LaunchRecord struct {
	Schema       string `json:"schema"`
	DispatchID   string `json:"dispatch_id"`
	SessionID    string `json:"session_id"`
	Agent        string `json:"agent"`
	InstanceName string `json:"instance_name"`
	// PermissionMode is the mode the worker launched with, "" when none was
	// passed.
	PermissionMode string `json:"permission_mode,omitempty"`
	SessionName    string `json:"session_name,omitempty"`
	Model          string `json:"model,omitempty"`
	// Settings is the settings document passed to the agent's settings flag,
	// verbatim, "" when none was.
	Settings string `json:"settings,omitempty"`
	// Env holds the names and values niwa set in the worker's environment.
	Env       map[string]string `json:"env,omitempty"`
	WrittenAt time.Time         `json:"written_at"`
}

// LaunchDir returns the launch store's directory for a workspace root.
func LaunchDir(root string) string {
	return filepath.Join(root, LaunchDirName)
}

// errLaunchDispatchID refuses a dispatch id that isn't one before it can name
// a file.
var errLaunchDispatchID = errors.New("not a dispatch id")

func launchRecordPath(root, dispatchID string) (string, error) {
	if !authority.ValidDispatchID(dispatchID) {
		return "", errLaunchDispatchID
	}
	return filepath.Join(LaunchDir(root), dispatchID+".json"), nil
}

// recordableLaunchEnvName reports whether a launch environment name may be
// written into a record: one of the names niwa sets itself, or a name the
// session lists would accept, which excludes anything shaped like a
// credential.
func recordableLaunchEnvName(name string) bool {
	for _, own := range config.LaunchSettingsEnvNames {
		if name == own {
			return true
		}
	}
	return config.ValidateSessionEnvName(name) == nil
}

// WriteLaunchRecord writes rec under root, atomically, as a 0600 file in a
// 0700 directory. Environment entries whose names aren't recordable are left
// out rather than written. The dispatch id is validated before any path is
// built from it, and WrittenAt is stamped when zero.
func WriteLaunchRecord(root string, rec LaunchRecord) error {
	target, err := launchRecordPath(root, rec.DispatchID)
	if err != nil {
		return err
	}
	rec.Schema = LaunchRecordSchema
	if rec.WrittenAt.IsZero() {
		rec.WrittenAt = time.Now().UTC()
	}
	if len(rec.Env) > 0 {
		kept := make(map[string]string, len(rec.Env))
		for name, value := range rec.Env {
			if recordableLaunchEnvName(name) {
				kept[name] = value
			}
		}
		rec.Env = kept
	}

	dir := LaunchDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating launch store: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("securing launch store: %w", err)
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding launch record: %w", err)
	}
	tmp, err := os.CreateTemp(dir, rec.DispatchID+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing launch record: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("writing launch record: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing launch record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing launch record: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("finalizing launch record: %w", err)
	}
	return nil
}

// ReadLaunchRecord reads the record for dispatchID. It checks the schema and
// that the record names the dispatch id its file is named for; the fuller
// checks a resume makes before acting on a record are the resume's own.
func ReadLaunchRecord(root, dispatchID string) (LaunchRecord, error) {
	path, err := launchRecordPath(root, dispatchID)
	if err != nil {
		return LaunchRecord{}, err
	}
	return readLaunchRecordFile(path, dispatchID)
}

func readLaunchRecordFile(path, dispatchID string) (LaunchRecord, error) {
	f, err := os.Open(path)
	if err != nil {
		return LaunchRecord{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxLaunchRecordBytes+1))
	if err != nil {
		return LaunchRecord{}, err
	}
	if len(data) > MaxLaunchRecordBytes {
		return LaunchRecord{}, fmt.Errorf("launch record is larger than %d bytes", MaxLaunchRecordBytes)
	}
	var rec LaunchRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return LaunchRecord{}, fmt.Errorf("launch record does not parse: %w", err)
	}
	if rec.Schema != LaunchRecordSchema {
		return LaunchRecord{}, fmt.Errorf("launch record has schema %q, want %q", rec.Schema, LaunchRecordSchema)
	}
	if rec.DispatchID != dispatchID {
		return LaunchRecord{}, fmt.Errorf("launch record names dispatch %q in a file for %q", rec.DispatchID, dispatchID)
	}
	return rec, nil
}

// LaunchRecordEntry is one file in the launch store as ListLaunchRecords
// found it: the record, or why it couldn't be read.
type LaunchRecordEntry struct {
	// Name is the file's base name.
	Name   string
	Record LaunchRecord
	Err    error
}

// ListLaunchRecords lists the launch store under root, sorted by file name.
// A store that doesn't exist lists nothing. A file that doesn't parse, or
// whose name isn't a dispatch id, is listed with its error rather than left
// out, so a caller can say which file it skipped. Temporary files from an
// interrupted write are ignored.
func ListLaunchRecords(root string) ([]LaunchRecordEntry, error) {
	entries, err := os.ReadDir(LaunchDir(root))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LaunchRecordEntry
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || strings.HasSuffix(name, ".tmp") {
			continue
		}
		id, ok := strings.CutSuffix(name, ".json")
		if !ok || !authority.ValidDispatchID(id) {
			out = append(out, LaunchRecordEntry{Name: name, Err: errLaunchDispatchID})
			continue
		}
		rec, err := readLaunchRecordFile(filepath.Join(LaunchDir(root), name), id)
		out = append(out, LaunchRecordEntry{Name: name, Record: rec, Err: err})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// RemoveLaunchRecord removes the record for dispatchID. A record that is
// already gone is not an error.
func RemoveLaunchRecord(root, dispatchID string) error {
	path, err := launchRecordPath(root, dispatchID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
