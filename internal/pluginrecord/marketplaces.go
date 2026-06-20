// This file extends the pluginrecord package to reconcile Claude Code's
// global marketplace registry at ~/.claude/plugins/known_marketplaces.json.
//
// niwa writes the per-marketplace auto-update policy into each repo's project
// settings (extraKnownMarketplaces), but Claude Code does not overwrite an
// already-registered marketplace's entry in its global registry from those
// project settings. So a marketplace niwa registered earlier with
// autoUpdate=true keeps that stale value even after niwa's config flips it to
// false. ReconcileAutoUpdate closes that gap by updating the global registry's
// autoUpdate flag for the marketplaces niwa manages — the same self-healing
// posture the dangling-record Prune already applies to installed_plugins.json.
package pluginrecord

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const knownMarketplacesRelPath = ".claude/plugins/known_marketplaces.json"

// marketplaceBackupRetain caps how many timestamped backups of the marketplace
// registry are kept, matching the installed_plugins backup retention.
const marketplaceBackupRetain = 5

// LocateMarketplaces resolves the path to known_marketplaces.json from
// os.UserHomeDir plus the fixed relative path; WithBaseDir substitutes a test
// directory.
func LocateMarketplaces(opts ...Option) (string, error) {
	c := &config{}
	for _, o := range opts {
		o(c)
	}
	base := c.baseDir
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = home
	}
	return filepath.Join(base, knownMarketplacesRelPath), nil
}

// ReconcileReport describes the outcome of a ReconcileAutoUpdate call.
type ReconcileReport struct {
	// Updated lists the marketplace names whose autoUpdate value changed.
	Updated []string
	// BackupPath is the timestamped backup written before the change, or "".
	BackupPath string
}

// ReconcileAutoUpdate sets autoUpdate to the desired value for each named
// marketplace that ALREADY EXISTS in known_marketplaces.json. It never adds a
// marketplace (Claude Code owns registration) and never touches marketplaces
// absent from desired. An absent registry file is a no-op; a malformed file
// returns an error wrapping ErrMalformed and leaves the file untouched. All
// other fields and the file's key order are preserved; a backup is written
// before the first change.
func ReconcileAutoUpdate(desired map[string]bool, opts ...Option) (ReconcileReport, error) {
	var rep ReconcileReport
	if len(desired) == 0 {
		return rep, nil
	}
	path, err := LocateMarketplaces(opts...)
	if err != nil {
		return rep, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return rep, nil
	}
	if err != nil {
		return rep, err
	}

	top, err := decodeOrderedObject(data)
	if err != nil {
		return rep, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	var updated []string
	for i := range top {
		want, ok := desired[top[i].Key]
		if !ok {
			continue
		}
		newVal, changed, err := setEntryAutoUpdate(top[i].Value, want)
		if err != nil {
			// Entry is not an editable JSON object; leave it untouched
			// rather than risk corrupting an unexpected shape.
			continue
		}
		if changed {
			top[i].Value = newVal
			updated = append(updated, top[i].Key)
		}
	}
	if len(updated) == 0 {
		return rep, nil
	}

	pretty, err := indentOrderedObject(top)
	if err != nil {
		return rep, err
	}

	mode := defaultFileMode
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}

	backupPath, err := writeExclBackup(path, data, mode)
	if err != nil {
		return rep, err
	}
	if err := rotateBackups(path, marketplaceBackupRetain); err != nil {
		return rep, err
	}
	if err := atomicWrite(path, pretty, mode); err != nil {
		return rep, err
	}

	sort.Strings(updated)
	rep.Updated = updated
	rep.BackupPath = backupPath
	return rep, nil
}

// setEntryAutoUpdate returns the entry with its autoUpdate field set to want,
// preserving all other fields and their order. It reports whether a change was
// made. An entry that is not a JSON object returns an error.
func setEntryAutoUpdate(raw json.RawMessage, want bool) (json.RawMessage, bool, error) {
	fields, err := decodeOrderedObject(raw)
	if err != nil {
		return nil, false, err
	}
	wantRaw := json.RawMessage("false")
	if want {
		wantRaw = json.RawMessage("true")
	}
	for i := range fields {
		if fields[i].Key == "autoUpdate" {
			if strings.TrimSpace(string(fields[i].Value)) == string(wantRaw) {
				return raw, false, nil
			}
			fields[i].Value = wantRaw
			return marshalOrderedObject(fields), true, nil
		}
	}
	fields = append(fields, rawField{Key: "autoUpdate", Value: wantRaw})
	return marshalOrderedObject(fields), true, nil
}

// marshalOrderedObject renders ordered fields as a compact JSON object,
// preserving field order and each field's raw value verbatim.
func marshalOrderedObject(fields []rawField) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, _ := json.Marshal(f.Key)
		buf.Write(keyJSON)
		buf.WriteByte(':')
		buf.Write(f.Value)
	}
	buf.WriteByte('}')
	return buf.Bytes()
}

// indentOrderedObject renders ordered fields as a 2-space indented JSON object
// with a trailing newline, matching the style Claude Code writes.
func indentOrderedObject(fields []rawField) ([]byte, error) {
	compact := marshalOrderedObject(fields)
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return nil, err
	}
	pretty.WriteByte('\n')
	return pretty.Bytes(), nil
}
