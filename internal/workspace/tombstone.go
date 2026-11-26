package workspace

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The tombstone is an instance's last name before it is deleted. A pass
// that has checked an instance renames it to a tombstone in the same
// directory, checks it again there, and only then removes it, so a writer
// that arrives after the last check finds no path to write to: a path
// opened under the old name gets "no such file". A difference found on the
// tombstone renames it back.
//
// A tombstone is <root>/.niwa-tombstone-<instance>-<8 hex>. The name starts
// with ".niwa-", which EnumerateInstances skips, so a tombstone is never
// listed as an instance though it still holds .niwa/instance.json.

// TombstonePrefix starts every tombstone's name.
const TombstonePrefix = ".niwa-tombstone-"

// tombstoneSuffixLen is the length of a tombstone's random suffix in hex.
const tombstoneSuffixLen = 8

// ErrInstanceNameTaken is returned by UntombstoneInstance when the
// instance's own name is in use again, so the tombstone can't be renamed
// back.
var ErrInstanceNameTaken = errors.New("the instance's name is taken")

// randomTombstoneSuffix returns the tombstone's random suffix. A variable so
// a test can make two picks collide.
var randomTombstoneSuffix = func() (string, error) {
	b := make([]byte, tombstoneSuffixLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// TombstoneName returns the tombstone name for instance with suffix.
func TombstoneName(instance, suffix string) string {
	return TombstonePrefix + instance + "-" + suffix
}

// ParseTombstoneName returns the instance a tombstone's base name was made
// from. ok is false for any name that isn't exactly
// .niwa-tombstone-<instance>-<8 lowercase hex> with a usable instance name.
func ParseTombstoneName(base string) (instance string, ok bool) {
	rest, found := strings.CutPrefix(base, TombstonePrefix)
	if !found || len(rest) < tombstoneSuffixLen+2 {
		return "", false
	}
	cut := len(rest) - tombstoneSuffixLen - 1
	if rest[cut] != '-' || !isLowerHex(rest[cut+1:]) {
		return "", false
	}
	instance = rest[:cut]
	if checkInstanceName(instance) != nil {
		return "", false
	}
	return instance, true
}

func isLowerHex(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// checkInstanceName refuses a name that can't be an instance directory
// directly under the root: empty, a path, ".", "..", one EnumerateInstances
// would skip, or one ValidName rejects.
func checkInstanceName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("%q is not an instance name", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Errorf("instance name %q holds a path separator", name)
	case name == StateDir || strings.HasPrefix(name, StateDir+"-"):
		return fmt.Errorf("%q is a name niwa keeps for itself, not an instance", name)
	case !ValidName(name):
		return fmt.Errorf("instance name %q holds characters niwa refuses", name)
	}
	return nil
}

// realDirIn checks that path is a real directory, not a symlink, whose
// parent is dir as text. The check is made with Lstat, so a link is never
// followed.
func realDirIn(dir, path string) error {
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(dir) {
		return fmt.Errorf("%s is not directly under %s", path, dir)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		return fmt.Errorf("%s is a symlink", filepath.Base(path))
	case !info.IsDir():
		return fmt.Errorf("%s is not a directory", filepath.Base(path))
	}
	return nil
}

// TombstoneInstance renames <workspaceRoot>/<name> to a fresh tombstone in
// the same directory and returns the tombstone's path. The instance must be
// a real directory directly under the root, never a symlink. The rename
// never replaces anything already at the tombstone's name.
func TombstoneInstance(workspaceRoot, name string) (string, error) {
	if err := checkInstanceName(name); err != nil {
		return "", err
	}
	dir := filepath.Join(workspaceRoot, name)
	if err := realDirIn(workspaceRoot, dir); err != nil {
		return "", fmt.Errorf("refusing to tombstone the instance: %w", err)
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		suffix, err := randomTombstoneSuffix()
		if err != nil {
			return "", fmt.Errorf("picking a tombstone name: %w", err)
		}
		target := filepath.Join(workspaceRoot, TombstoneName(name, suffix))
		err = renameNoReplace(dir, target)
		if err == nil {
			return target, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("renaming the instance to its tombstone: %w", err)
		}
		lastErr = err
	}
	return "", fmt.Errorf("renaming the instance to its tombstone: no free name: %w", lastErr)
}

// UntombstoneInstance renames the tombstone at tombstonePath back to the
// instance name it was made from and returns the instance's path. The
// tombstone must be a real directory, never a symlink, directly under
// workspaceRoot. When the instance's name is in use again it returns an
// error wrapping ErrInstanceNameTaken and renames nothing.
func UntombstoneInstance(workspaceRoot, tombstonePath string) (string, error) {
	name, ok := ParseTombstoneName(filepath.Base(tombstonePath))
	if !ok {
		return "", fmt.Errorf("%s is not a tombstone's name", filepath.Base(tombstonePath))
	}
	if err := realDirIn(workspaceRoot, tombstonePath); err != nil {
		return "", fmt.Errorf("refusing to rename the tombstone back: %w", err)
	}
	target := filepath.Join(workspaceRoot, name)
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("renaming %s back: %w", filepath.Base(tombstonePath), ErrInstanceNameTaken)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("renaming %s back: %w", filepath.Base(tombstonePath), err)
	}
	if err := renameNoReplace(tombstonePath, target); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("renaming %s back: %w", filepath.Base(tombstonePath), ErrInstanceNameTaken)
		}
		return "", fmt.Errorf("renaming %s back: %w", filepath.Base(tombstonePath), err)
	}
	return target, nil
}

// ListTombstones returns the path of every entry directly under
// workspaceRoot whose name starts with TombstonePrefix, sorted, whatever its
// type: a caller Lstats each one before acting on it.
func ListTombstones(workspaceRoot string) ([]string, error) {
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("reading workspace root: %w", err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), TombstonePrefix) {
			out = append(out, filepath.Join(workspaceRoot, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// renameChecked renames from to to after checking nothing is at to. It is
// the fallback where the platform or the filesystem offers no rename that
// refuses to replace: the check and the rename are two steps, so a
// directory created at to between them (only an empty one; rename never
// replaces a non-empty directory) could still be replaced.
func renameChecked(from, to string) error {
	if _, err := os.Lstat(to); err == nil {
		return &fs.PathError{Op: "rename", Path: to, Err: fs.ErrExist}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Rename(from, to)
}
