package removal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Base is where the paths a record names resolve: the unit's instance, and
// its job directory for a path that starts with "job:".
type Base struct {
	Instance string
	JobDir   string
}

// ResolveRecorded resolves a path a record names against base and returns
// the path on disk. The path must be relative and clean: not empty, not
// absolute, with no "..", "." or empty component, no NUL and no backslash.
// One trailing "/" is allowed, the way Verdict.Paths writes a directory. A
// path that starts with "job:" resolves against the unit's job directory and
// nothing else.
//
// Every component is checked from the base down with Lstat, so a symlink at
// any component, the last one included, or a component on another device
// than the base fails. A component that doesn't exist fails with an error
// that wraps fs.ErrNotExist, so a caller can tell a missing path from a
// refused one. Every error starts with the path as given.
func ResolveRecorded(base Base, p string) (string, error) {
	switch {
	case p == "":
		return "", fmt.Errorf("%q: the path is empty", p)
	case strings.ContainsRune(p, 0):
		return "", fmt.Errorf("%q: the path holds a NUL", p)
	case strings.Contains(p, `\`):
		return "", fmt.Errorf("%q: the path holds a backslash", p)
	}
	root, rel := base.Instance, p
	if r, ok := strings.CutPrefix(p, jobPrefix); ok {
		if base.JobDir == "" {
			return "", fmt.Errorf("%q: the unit has no job directory", p)
		}
		root, rel = base.JobDir, r
	}
	if root == "" {
		return "", fmt.Errorf("%q: there is no base to resolve it against", p)
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%q: the path is absolute", p)
	}
	clean := strings.TrimSuffix(rel, "/")
	if clean == "" {
		return "", fmt.Errorf("%q: the path names no file", p)
	}
	parts := strings.Split(clean, "/")
	for _, part := range parts {
		switch part {
		case "":
			return "", fmt.Errorf("%q: the path has an empty component", p)
		case ".", "..":
			return "", fmt.Errorf("%q: the path has a %q component", p, part)
		}
	}

	// The base itself is the caller's: a link at or above it is followed,
	// and its device is the one every component must share.
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("%q: its base can't be read: %s", p, errText(err))
	}
	rootDev, ok := deviceOf(rootInfo)
	if !ok {
		return "", fmt.Errorf("%q: its base's device can't be read", p)
	}
	cur := root
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		shown := strings.Join(parts[:i+1], "/")
		info, err := os.Lstat(cur)
		switch {
		case err == nil:
		case os.IsNotExist(err):
			return "", fmt.Errorf("%q: %s does not exist: %w", p, shown, fs.ErrNotExist)
		default:
			return "", fmt.Errorf("%q: %s: %s", p, shown, errText(err))
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%q: %s is a symlink", p, shown)
		}
		if dev, ok := deviceOf(info); !ok || dev != rootDev {
			return "", fmt.Errorf("%q: %s is on another device", p, shown)
		}
	}
	return filepath.Join(root, filepath.FromSlash(clean)), nil
}

// deviceOf is the device a file lives on.
func deviceOf(info fs.FileInfo) (uint64, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
