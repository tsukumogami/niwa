package store

import (
	"os"
	"path/filepath"
)

// InWorkTree reports whether dir sits inside a git work tree, so the
// store must not be written there. It looks for a .git entry (a
// directory, or the file a linked worktree has) at every level from dir
// to the filesystem root, twice: over the cleaned path as given, which
// need not exist yet, and over the symlink-resolved path of its deepest
// existing ancestor. It runs no subprocess.
//
// A home directory tracked through a bare repository has no .git entry
// and isn't detected.
func InWorkTree(dir string) bool {
	clean, err := filepath.Abs(dir)
	if err != nil {
		clean = filepath.Clean(dir)
	}
	if gitAbove(clean) {
		return true
	}
	for p := clean; ; p = filepath.Dir(p) {
		if _, err := os.Lstat(p); err == nil {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				return gitAbove(resolved)
			}
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}

// gitAbove reports whether p or any directory above it holds a .git
// entry of any type.
func gitAbove(p string) bool {
	for {
		if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}
