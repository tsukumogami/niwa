// Package pluginrecord owns every read and write of Claude Code's global
// plugin install registry at ~/.claude/plugins/installed_plugins.json.
//
// niwa does not own this file — live Claude Code sessions read and write
// it concurrently, and niwa cannot make them honor a lock. Every mutation
// therefore flows through one audited path with one safety model: a
// preserve-unknowns document model that round-trips fields and keys niwa
// does not model, atomic temp-and-rename writes that never truncate the
// target, and timestamped rotated backups taken before the first write.
//
// This file provides the registry I/O core: locate, load, save, and
// backup. The removal predicates and the Prune entry point live in a
// separate file in this package.
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
	"time"
)

// registryRelPath is the registry's location relative to the user's home
// directory (or an injected base directory in tests). The path is fixed
// and never derived from user input or config, so there is no
// path-injection surface.
const registryRelPath = ".claude/plugins/installed_plugins.json"

// backupSuffix is inserted between the registry filename and the RFC3339
// timestamp to form a backup sibling's name.
const backupSuffix = ".niwa-bak."

// defaultBackupRetention is how many timestamped backups Prune retains;
// older ones are pruned after each new backup.
const defaultBackupRetention = 5

// defaultFileMode is the permission used for a freshly created registry
// or a backup whose source mode could not be determined.
const defaultFileMode fs.FileMode = 0o644

// ErrMalformed is returned (wrapped) when the on-disk registry is present
// but is not valid JSON. Callers should leave the file untouched and
// report rather than overwrite a file they cannot parse.
var ErrMalformed = errors.New("pluginrecord: malformed registry")

// Registry is an in-memory, preserve-unknowns view of the installed
// plugins registry. The top-level document is held as an ordered list of
// raw key/value pairs so unmodelled top-level keys and their order
// survive a load/save cycle byte-stably; the "plugins" object is the only
// key parsed into structured records.
type Registry struct {
	// path is the resolved on-disk location of the registry.
	path string

	// present reports whether the file existed at load time. An absent
	// registry loads as an empty, not-present Registry and saving it is a
	// no-op until records are added.
	present bool

	// topLevel preserves every top-level key/value pair in document order.
	// The entry whose key is "plugins" is replaced at save time with the
	// re-marshalled records; every other entry is re-emitted verbatim.
	topLevel []rawField

	// pluginsIndex is the position of the "plugins" key within topLevel,
	// or -1 if the document had no "plugins" key.
	pluginsIndex int

	// Plugins maps a plugin key to its ordered list of install records.
	Plugins map[string][]Record
}

// rawField is one top-level key/value pair, preserved verbatim for keys
// niwa does not model.
type rawField struct {
	Key   string
	Value json.RawMessage
}

// Record is a single install record. niwa reads Scope, ProjectPath, and
// InstallPath to classify a record for removal; every other field is
// retained verbatim in raw and re-emitted on save so niwa never drops a
// field it does not model.
type Record struct {
	Scope       string
	ProjectPath string
	InstallPath string

	// raw is the complete original object for this record, used to
	// re-marshal unmodelled fields untouched.
	raw json.RawMessage
}

// recordModel is the subset of fields niwa parses out of a record.
type recordModel struct {
	Scope       string `json:"scope"`
	ProjectPath string `json:"projectPath"`
	InstallPath string `json:"installPath"`
}

// Option configures how a registry path is resolved.
type Option func(*config)

type config struct {
	baseDir string
}

// WithBaseDir overrides the home directory used to locate the registry.
// It exists for tests, which point the package at a t.TempDir instead of
// the real ~/.claude.
func WithBaseDir(dir string) Option {
	return func(c *config) { c.baseDir = dir }
}

// Locate resolves the registry path. With no options it joins
// os.UserHomeDir with the fixed relative path; WithBaseDir substitutes a
// caller-provided base directory.
func Locate(opts ...Option) (string, error) {
	var c config
	for _, opt := range opts {
		opt(&c)
	}
	base := c.baseDir
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("pluginrecord: locate home: %w", err)
		}
		base = home
	}
	return filepath.Join(base, registryRelPath), nil
}

// Load reads and parses the registry. A missing file loads as an empty,
// not-present Registry with no error. A present but unparseable file
// returns an error wrapping ErrMalformed and leaves the file untouched.
func Load(opts ...Option) (*Registry, error) {
	path, err := Locate(opts...)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Registry{
			path:         path,
			present:      false,
			pluginsIndex: -1,
			Plugins:      map[string][]Record{},
		}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("pluginrecord: read registry: %w", err)
	}

	reg, err := parse(data)
	if err != nil {
		return nil, err
	}
	reg.path = path
	reg.present = true
	return reg, nil
}

// parse decodes the registry bytes into the preserve-unknowns model.
func parse(data []byte) (*Registry, error) {
	// An empty file is treated as an empty document, not malformed.
	if len(bytes.TrimSpace(data)) == 0 {
		return &Registry{pluginsIndex: -1, Plugins: map[string][]Record{}}, nil
	}

	// Decode the top level preserving key order. json.Decoder over the
	// object tokens gives us order; a plain map would lose it.
	fields, err := decodeOrderedObject(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}

	reg := &Registry{
		topLevel:     fields,
		pluginsIndex: -1,
		Plugins:      map[string][]Record{},
	}

	for i, f := range fields {
		if f.Key != "plugins" {
			continue
		}
		reg.pluginsIndex = i
		plugins, err := parsePlugins(f.Value)
		if err != nil {
			return nil, err
		}
		reg.Plugins = plugins
	}

	return reg, nil
}

// parsePlugins decodes the "plugins" object into structured records while
// retaining each record's raw bytes.
func parsePlugins(raw json.RawMessage) (map[string][]Record, error) {
	// A null or absent plugins value is an empty set.
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return map[string][]Record{}, nil
	}

	var rawByKey map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawByKey); err != nil {
		return nil, fmt.Errorf("%w: plugins: %v", ErrMalformed, err)
	}

	out := make(map[string][]Record, len(rawByKey))
	for key, listRaw := range rawByKey {
		var rawRecords []json.RawMessage
		if err := json.Unmarshal(listRaw, &rawRecords); err != nil {
			return nil, fmt.Errorf("%w: plugin %q records: %v", ErrMalformed, key, err)
		}
		records := make([]Record, 0, len(rawRecords))
		for _, rr := range rawRecords {
			var m recordModel
			if err := json.Unmarshal(rr, &m); err != nil {
				return nil, fmt.Errorf("%w: plugin %q record: %v", ErrMalformed, key, err)
			}
			records = append(records, Record{
				Scope:       m.Scope,
				ProjectPath: m.ProjectPath,
				InstallPath: m.InstallPath,
				raw:         append(json.RawMessage(nil), rr...),
			})
		}
		out[key] = records
	}
	return out, nil
}

// decodeOrderedObject decodes a JSON object preserving the order of its
// top-level keys.
func decodeOrderedObject(data []byte) ([]rawField, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected JSON object at top level")
	}

	var fields []rawField
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key")
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
		fields = append(fields, rawField{Key: key, Value: val})
	}

	// Consume the closing brace and confirm no trailing content.
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("unexpected trailing content after top-level object")
	}
	return fields, nil
}

// Marshal renders the registry back to JSON, preserving top-level key
// order and every unmodelled field. The "plugins" object is rebuilt from
// the structured records (each record re-emitted from its retained raw
// bytes), which is how removals take effect while leaving untouched
// records byte-stable.
func (r *Registry) Marshal() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')

	fields := r.topLevel
	// A registry built in memory (no prior document) still needs a
	// "plugins" key emitted.
	emittedPlugins := false

	for i, f := range fields {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(f.Key)
		if err != nil {
			return nil, fmt.Errorf("pluginrecord: marshal key: %w", err)
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')

		if i == r.pluginsIndex {
			pluginsJSON, err := r.marshalPlugins()
			if err != nil {
				return nil, err
			}
			buf.Write(pluginsJSON)
			emittedPlugins = true
		} else {
			buf.Write(f.Value)
		}
	}

	if !emittedPlugins {
		if len(fields) > 0 {
			buf.WriteByte(',')
		}
		buf.WriteString(`"plugins":`)
		pluginsJSON, err := r.marshalPlugins()
		if err != nil {
			return nil, err
		}
		buf.Write(pluginsJSON)
	}

	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// marshalPlugins re-emits the plugins object. Keys are sorted so output is
// deterministic; each record is emitted from its retained raw bytes, so a
// record niwa never touched round-trips byte-for-byte.
func (r *Registry) marshalPlugins() ([]byte, error) {
	keys := make([]string, 0, len(r.Plugins))
	for k := range r.Plugins {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(k)
		if err != nil {
			return nil, fmt.Errorf("pluginrecord: marshal plugin key: %w", err)
		}
		buf.Write(keyJSON)
		buf.WriteString(":[")
		for j, rec := range r.Plugins[k] {
			if j > 0 {
				buf.WriteByte(',')
			}
			buf.Write(rec.raw)
		}
		buf.WriteByte(']')
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Save writes the registry atomically: a temp file in the same directory
// created O_CREATE|O_EXCL, written, fsynced, then os.Rename over the
// target. The target is never truncated, so an interrupted write leaves
// either the prior file or the fully-written new one.
func (r *Registry) Save() error {
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	return atomicWrite(r.path, data, r.fileMode())
}

// fileMode returns the mode to use for a freshly created registry. If the
// file exists, its current mode is preserved; otherwise the default.
func (r *Registry) fileMode() fs.FileMode {
	if info, err := os.Stat(r.path); err == nil {
		return info.Mode().Perm()
	}
	return defaultFileMode
}

// atomicWrite writes data to path via a same-directory O_EXCL temp file
// renamed over the target.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("pluginrecord: ensure registry dir: %w", err)
	}

	tmp, err := createExclTemp(dir, filepath.Base(path), mode)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if anything below fails before the rename.
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pluginrecord: write temp registry: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("pluginrecord: sync temp registry: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pluginrecord: close temp registry: %w", err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("pluginrecord: rename temp registry: %w", err)
	}
	cleanup = false
	return nil
}

// createExclTemp creates a uniquely named temp file in dir with O_EXCL so
// a pre-planted symlink at the chosen name cannot redirect the write.
func createExclTemp(dir, base string, mode fs.FileMode) (*os.File, error) {
	for attempt := 0; attempt < 10000; attempt++ {
		name := filepath.Join(dir, fmt.Sprintf(".%s.niwa-tmp.%d.%d", base, os.Getpid(), time.Now().UnixNano()+int64(attempt)))
		f, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err == nil {
			return f, nil
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return nil, fmt.Errorf("pluginrecord: create temp registry: %w", err)
	}
	return nil, fmt.Errorf("pluginrecord: create temp registry: exhausted unique names")
}

// Backup takes a non-clobbering snapshot of the current registry file as a
// timestamped sibling installed_plugins.json.niwa-bak.<RFC3339>, created
// O_CREATE|O_EXCL with the source file's mode, then retains only the most
// recent retain backups. An absent source is a no-op (no file to back up).
// It returns the path of the backup written, or "" when the source was
// absent.
func (r *Registry) Backup(retain int) (string, error) {
	if retain <= 0 {
		retain = defaultBackupRetention
	}

	info, err := os.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("pluginrecord: stat registry for backup: %w", err)
	}

	data, err := os.ReadFile(r.path)
	if err != nil {
		return "", fmt.Errorf("pluginrecord: read registry for backup: %w", err)
	}

	backupPath, err := writeExclBackup(r.path, data, info.Mode().Perm())
	if err != nil {
		return "", err
	}

	if err := rotateBackups(r.path, retain); err != nil {
		return backupPath, err
	}
	return backupPath, nil
}

// writeExclBackup writes data to a timestamped sibling created O_EXCL with
// the given mode. If a backup with this timestamp already exists (sub-
// second repeated calls), it retries with a uniqueness counter so the
// snapshot is never silently dropped.
func writeExclBackup(srcPath string, data []byte, mode fs.FileMode) (string, error) {
	dir := filepath.Dir(srcPath)
	base := filepath.Base(srcPath)
	ts := time.Now().Format(time.RFC3339)

	for attempt := 0; attempt < 1000; attempt++ {
		name := base + backupSuffix + ts
		if attempt > 0 {
			name = fmt.Sprintf("%s%s%s.%d", base, backupSuffix, ts, attempt)
		}
		full := filepath.Join(dir, name)
		f, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("pluginrecord: create backup: %w", err)
		}
		if _, err := f.Write(data); err != nil {
			_ = f.Close()
			_ = os.Remove(full)
			return "", fmt.Errorf("pluginrecord: write backup: %w", err)
		}
		if err := f.Close(); err != nil {
			return "", fmt.Errorf("pluginrecord: close backup: %w", err)
		}
		return full, nil
	}
	return "", fmt.Errorf("pluginrecord: create backup: exhausted unique names")
}

// rotateBackups removes the oldest backups so at most retain remain.
// Backups are ordered by name; the RFC3339 timestamp embedded in each name
// sorts lexicographically in chronological order.
func rotateBackups(srcPath string, retain int) error {
	dir := filepath.Dir(srcPath)
	prefix := filepath.Base(srcPath) + backupSuffix

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("pluginrecord: list backups: %w", err)
	}

	var backups []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if len(name) > len(prefix) && name[:len(prefix)] == prefix {
			backups = append(backups, name)
		}
	}
	if len(backups) <= retain {
		return nil
	}

	sort.Strings(backups)
	toRemove := backups[:len(backups)-retain]
	for _, name := range toRemove {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("pluginrecord: rotate backups: %w", err)
		}
	}
	return nil
}
