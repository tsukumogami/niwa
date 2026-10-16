// This file extends the pluginrecord package to reconcile Claude Code's
// global marketplace registry at ~/.claude/plugins/known_marketplaces.json.
//
// niwa writes each marketplace's source and auto-update policy into each repo's
// project settings (extraKnownMarketplaces), but Claude Code does not overwrite
// an already-registered marketplace's entry in its global registry from those
// project settings. So a marketplace niwa registered earlier keeps a stale
// autoUpdate value after niwa's config flips it, and a directory-source
// marketplace keeps pointing at whichever directory registered it.
// ReconcileMarketplaces closes both gaps for the marketplaces niwa manages --
// the same self-healing posture the dangling-record Prune already applies to
// installed_plugins.json.
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

// ReconcileReport describes the outcome of a reconcile call.
type ReconcileReport struct {
	// Updated lists the marketplace names whose autoUpdate value changed.
	Updated []string
	// Repointed lists the marketplace names whose directory source was
	// rewritten to the wanted path.
	Repointed []string
	// BackupPath is the timestamped backup written before the change, or "".
	BackupPath string
}

// MarketplaceWant is what niwa wants one registered marketplace to look like.
type MarketplaceWant struct {
	// AutoUpdate is the wanted autoUpdate value.
	AutoUpdate bool
	// Path, when non-empty, is the directory the marketplace must be
	// registered from: its source becomes {"source": "directory", "path":
	// Path} and its installLocation becomes Path. Empty leaves the source
	// alone, which is what a github marketplace wants.
	Path string
}

// ReconcileAutoUpdate sets autoUpdate to the desired value for each named
// marketplace that already exists in known_marketplaces.json. It is
// ReconcileMarketplaces with no path to rewrite.
func ReconcileAutoUpdate(desired map[string]bool, opts ...Option) (ReconcileReport, error) {
	wants := make(map[string]MarketplaceWant, len(desired))
	for name, autoUpdate := range desired {
		wants[name] = MarketplaceWant{AutoUpdate: autoUpdate}
	}
	return ReconcileMarketplaces(wants, opts...)
}

// ReconcileMarketplaces brings each named marketplace that ALREADY EXISTS in
// known_marketplaces.json to its wanted autoUpdate value and, when the want
// carries a Path, to that directory source. It never adds a marketplace
// (plugin pre-warming, or a Claude Code session reading project settings,
// registers it) and never touches marketplaces absent from desired. An absent registry file
// is a no-op; a malformed file returns an error wrapping ErrMalformed and
// leaves the file untouched. Every other entry, every other field and the
// file's key order are preserved; a backup is written before the first change,
// and the new content replaces the file by an atomic rename.
func ReconcileMarketplaces(desired map[string]MarketplaceWant, opts ...Option) (ReconcileReport, error) {
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

	var updated, repointed []string
	for i := range top {
		want, ok := desired[top[i].Key]
		if !ok {
			continue
		}
		// An entry that is not an editable JSON object fails both setters
		// below; it is left untouched rather than risk corrupting an
		// unexpected shape.
		val := top[i].Value
		if want.Path != "" {
			newVal, changed, err := setEntryDirectory(val, want.Path)
			if err != nil {
				continue
			}
			if changed {
				val = newVal
				repointed = append(repointed, top[i].Key)
			}
		}
		newVal, changed, err := setEntryAutoUpdate(val, want.AutoUpdate)
		if err != nil {
			continue
		}
		if changed {
			val = newVal
			updated = append(updated, top[i].Key)
		}
		top[i].Value = val
	}
	if len(updated) == 0 && len(repointed) == 0 {
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
	sort.Strings(repointed)
	rep.Updated = updated
	rep.Repointed = repointed
	rep.BackupPath = backupPath
	return rep, nil
}

// RegisteredMarketplaces returns the names of the marketplaces registered in
// known_marketplaces.json. An absent registry file yields an empty set; a
// malformed one returns an error wrapping ErrMalformed. It only reads.
func RegisteredMarketplaces(opts ...Option) (map[string]bool, error) {
	path, err := LocateMarketplaces(opts...)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	top, err := decodeOrderedObject(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	names := make(map[string]bool, len(top))
	for _, f := range top {
		names[f.Key] = true
	}
	return names, nil
}

// setEntryDirectory returns the entry with its source set to a directory
// source at dir and its installLocation set to dir, preserving every other
// field and the field order. The source object is replaced whole, so fields a
// different source kind carried (a github repo or ref) don't linger beside the
// directory path. It reports whether a change was made; an entry that is not a
// JSON object returns an error.
func setEntryDirectory(raw json.RawMessage, dir string) (json.RawMessage, bool, error) {
	fields, err := decodeOrderedObject(raw)
	if err != nil {
		return nil, false, err
	}
	dirRaw, err := marshalString(dir)
	if err != nil {
		return nil, false, err
	}
	srcRaw := marshalOrderedObject([]rawField{
		{Key: "source", Value: json.RawMessage(`"directory"`)},
		{Key: "path", Value: dirRaw},
	})

	changed := false
	set := func(key string, value json.RawMessage, current func(json.RawMessage) bool) {
		for i := range fields {
			if fields[i].Key != key {
				continue
			}
			if !current(fields[i].Value) {
				fields[i].Value = value
				changed = true
			}
			return
		}
		fields = append(fields, rawField{Key: key, Value: value})
		changed = true
	}
	set("source", srcRaw, func(old json.RawMessage) bool {
		var src map[string]any
		return json.Unmarshal(old, &src) == nil && len(src) == 2 &&
			src["source"] == "directory" && src["path"] == dir
	})
	set("installLocation", dirRaw, func(old json.RawMessage) bool {
		var s string
		return json.Unmarshal(old, &s) == nil && s == dir
	})
	if !changed {
		return raw, false, nil
	}
	return marshalOrderedObject(fields), true, nil
}

// marshalString encodes s as a JSON string without HTML escaping, so a path
// holding & or < is written the way Claude Code writes it.
func marshalString(s string) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
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
