//go:build unix

package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// countingGit puts a git stub first on PATH that records every call, and
// returns the file it records to.
func countingGit(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "git-calls")
	script := "#!/bin/sh\necho \"$@\" >> '" + log + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func TestWorkTreeGuard(t *testing.T) {
	cases := map[string]func(t *testing.T, tmp string) string{
		".git directory above": func(t *testing.T, tmp string) string {
			mkdir(t, filepath.Join(tmp, "repo", ".git"))
			return filepath.Join(tmp, "repo", "state", "niwa", "secret-cache")
		},
		".git file above (linked worktree)": func(t *testing.T, tmp string) string {
			mkdir(t, filepath.Join(tmp, "wt"))
			if err := os.WriteFile(filepath.Join(tmp, "wt", ".git"), []byte("gitdir: /elsewhere\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(tmp, "wt", "niwa", "secret-cache")
		},
		"symlink into a work tree": func(t *testing.T, tmp string) string {
			mkdir(t, filepath.Join(tmp, "repo", ".git"))
			mkdir(t, filepath.Join(tmp, "repo", "sub"))
			if err := os.Symlink(filepath.Join(tmp, "repo", "sub"), filepath.Join(tmp, "link")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(tmp, "link", "niwa", "secret-cache")
		},
		// Only the walk over the path as configured catches this one: the
		// symlink leads out of the work tree, so the resolved path has no
		// .git above it.
		"configured path in a work tree, symlink out": func(t *testing.T, tmp string) string {
			mkdir(t, filepath.Join(tmp, "repo", ".git"))
			mkdir(t, filepath.Join(tmp, "outside"))
			if err := os.Symlink(filepath.Join(tmp, "outside"), filepath.Join(tmp, "repo", "link")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(tmp, "repo", "link", "secret-cache")
		},
		"symlink into a .git directory": func(t *testing.T, tmp string) string {
			mkdir(t, filepath.Join(tmp, "repo", ".git", "modules"))
			if err := os.Symlink(filepath.Join(tmp, "repo", ".git", "modules"), filepath.Join(tmp, "link")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(tmp, "link", "secret-cache")
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			isolate(t)
			calls := countingGit(t)
			tmp := t.TempDir()
			dir := setup(t, tmp)

			if !InWorkTree(dir) {
				t.Fatalf("InWorkTree(%s) = false, want true", dir)
			}
			err := Update(dir, testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false)
			if !errors.Is(err, ErrInWorkTree) {
				t.Fatalf("err = %v, want one wrapping ErrInWorkTree", err)
			}
			if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("Update created %s inside the work tree", dir)
			}
			if _, err := os.Lstat(filepath.Join(tmp, "repo", "state")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("Update created the store's parents inside the work tree")
			}
			if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
				b, _ := os.ReadFile(calls)
				t.Fatalf("the walk ran git: %s", b)
			}
		})
	}
}

func TestWorkTreeGuardAllowsPlainDirectories(t *testing.T) {
	dir := isolate(t)
	calls := countingGit(t)
	if InWorkTree(dir) {
		t.Skipf("the test's temp directory %s sits in a work tree", dir)
	}
	mustUpdate(t, dir, testIdentity(), map[string]Entry{"K": entry("v", time.Now(), "t")}, nil, false)
	if _, err := os.Stat(calls); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the walk ran git")
	}
}

func mkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o700); err != nil {
		t.Fatal(err)
	}
}
