package claudetrust

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// rawObject is one JSON object level of the config file. Values stay as the
// exact bytes they were read as, so a rewrite can't change a value niwa didn't
// mean to touch: a large integer keeps its digits and an escaped string keeps
// its escapes.
type rawObject map[string]json.RawMessage

// rawTrue is the flag value niwa writes.
var rawTrue = json.RawMessage("true")

// beforeRename, when set, runs after the temp file is written and synced and
// before it is renamed over the target. It is a seam for tests that inject a
// failure at the last moment; production leaves it nil.
var beforeRename func(tmpPath string) error

// configDoc is a classified config file, loaded under the lock.
type configDoc struct {
	// target is where a write goes: the config path, or the file it links to.
	target string
	// exists is false when there was no file; a write then creates one.
	exists bool
	// mode is the permission a write keeps: the file's own, or 0600 for a new
	// file.
	mode os.FileMode
	top  rawObject
}

// loadConfig resolves and classifies the config file at path. Call it with the
// lock held: a symlinked path is resolved here, after the lock, so a link
// swapped before the lock was taken can't redirect the write. Every error is a
// reason to leave the file alone, worded to follow "left <file> unchanged:".
func loadConfig(path string) (*configDoc, error) {
	doc := &configDoc{target: path, mode: 0o600}
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		doc.top = rawObject{}
		return doc, nil
	}
	if err != nil {
		return nil, fmt.Errorf("the file can't be inspected: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, fmt.Errorf("the symlink can't be resolved: %w", err)
		}
		if info, err = os.Stat(target); err != nil {
			return nil, fmt.Errorf("the symlink target %s can't be inspected: %w", target, err)
		}
		doc.target = target
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", doc.target)
	}
	doc.exists = true
	doc.mode = info.Mode().Perm()

	data, err := os.ReadFile(doc.target)
	if err != nil {
		return nil, fmt.Errorf("the file is unreadable: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		doc.top = rawObject{}
		return doc, nil
	}
	if !json.Valid(data) {
		return nil, fmt.Errorf("the file is not valid JSON")
	}
	top, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("the top level is not a JSON object")
	}
	doc.top = top
	return doc, nil
}

// entry returns the projects object (empty when absent) and the entry at key
// (nil when absent). A projects value or entry that isn't a JSON object is an
// error: it is never replaced.
func (d *configDoc) entry(key string) (rawObject, rawObject, error) {
	projects := rawObject{}
	if raw, ok := d.top["projects"]; ok {
		p, err := decodeObject(raw)
		if err != nil {
			return nil, nil, fmt.Errorf(`"projects" is not a JSON object`)
		}
		projects = p
	}
	raw, ok := projects[key]
	if !ok {
		return projects, nil, nil
	}
	e, err := decodeObject(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("the entry for %q is not a JSON object", key)
	}
	return projects, e, nil
}

// setEntry stores entry at projects[key] and projects in the document.
func (d *configDoc) setEntry(projects rawObject, key string, entry rawObject) error {
	raw, err := marshalRaw(entry)
	if err != nil {
		return err
	}
	projects[key] = raw
	return d.setProjects(projects)
}

// setProjects stores projects in the document.
func (d *configDoc) setProjects(projects rawObject) error {
	raw, err := marshalRaw(projects)
	if err != nil {
		return err
	}
	d.top["projects"] = raw
	return nil
}

// write encodes the document the way Claude Code writes it (two-space indent,
// no HTML escaping) and replaces the target atomically.
func (d *configDoc) write() error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(d.top); err != nil {
		return fmt.Errorf("encoding the config: %w", err)
	}
	return writeAtomic(d.target, buf.Bytes(), d.mode)
}

// flagValue reads the trust flag from entry: false when absent. A value that
// isn't a JSON boolean is an unexpected shape, and an error.
func flagValue(entry rawObject) (bool, error) {
	raw, ok := entry[trustFlag]
	if !ok {
		return false, nil
	}
	switch string(bytes.TrimSpace(raw)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return false, fmt.Errorf("%s is %s, not a boolean", trustFlag, raw)
}

// decodeObject decodes raw as one JSON object level. json.Unmarshal accepts
// null into a map without error, so the leading byte is checked first.
func decodeObject(raw []byte) (rawObject, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("not a JSON object")
	}
	var obj rawObject
	if err := json.Unmarshal(trimmed, &obj); err != nil {
		return nil, err
	}
	if obj == nil {
		obj = rawObject{}
	}
	return obj, nil
}

// marshalRaw encodes v compactly with HTML escaping off, for nesting into an
// outer level that is indented when the whole document is written.
func marshalRaw(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding the config: %w", err)
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// writeAtomic replaces target with data: a temp file beside it (an atomic
// rename needs the same filesystem), private from the moment it exists,
// fsynced so the bytes are on disk before the rename publishes them, given
// mode, then renamed over target. The target is never opened for writing, so
// an interruption at any point leaves the previous file whole.
func writeAtomic(target string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(target), ".claude.json.niwa-*.tmp")
	if err != nil {
		return fmt.Errorf("creating a temp file beside %s: %w", target, err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("restricting the temp file for %s: %w", target, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the temp file for %s: %w", target, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing the temp file for %s: %w", target, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing the temp file for %s: %w", target, err)
	}
	if beforeRename != nil {
		if err := beforeRename(tmpName); err != nil {
			return err
		}
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("setting the mode on the temp file for %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("replacing %s: %w", target, err)
	}
	cleanup = false
	return nil
}
