// Package hostdir names the host-level directories niwa keeps per workspace.
//
// Host-level state that niwa owns, and that is large or must keep its identity
// across a config refresh, lives under ~/.niwa/, in a directory keyed by the
// workspace root's canonical path. It never lives under the workspace root's
// .niwa/ or inside an instance. Every store that follows that rule (the git
// mirrors under mirrors/, the plugin releases under plugins/) takes its key
// from WorkspaceKey here, so the host has one layout.
//
// The package is a leaf beside internal/filelock: it imports no other niwa
// package, so internal/mirror, internal/pluginstore and internal/workspace can
// all use it.
package hostdir

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/tsukumogami/niwa/internal/filelock"
)

// KeptDirName is the directory under a workspace key directory where a
// finished instance is kept outside the workspace root.
const KeptDirName = "kept"

// WorkspaceKey names a workspace's directory under ~/.niwa: "w-" and the first
// 32 hex digits of the sha256 of the workspace root's canonical path, so the
// same root reached through a symlink or a trailing slash gets one key.
func WorkspaceKey(workspaceRoot string) string {
	sum := sha256.Sum256([]byte(filelock.CanonicalPath(workspaceRoot)))
	return "w-" + hex.EncodeToString(sum[:])[:32]
}

// WorkspaceDir is the workspace key directory, ~/.niwa/w-<key>.
func WorkspaceDir(home, workspaceRoot string) string {
	return filepath.Join(home, ".niwa", WorkspaceKey(workspaceRoot))
}

// OwnedByUser reports whether info belongs to the current user. It reads the
// unix owner, so the package builds for unix only, like the flock the stores
// rely on and every platform niwa releases for.
func OwnedByUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// EnsurePrivateDir makes dir a real directory the user owns with mode exactly
// 0700, creating it when missing and tightening a wider mode. The mode is set
// explicitly, never left to the umask. A symlink or anything else at dir is
// refused, never followed.
func EnsurePrivateDir(dir string) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !os.IsExist(err) {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	info, err := checkOwnedDir(dir)
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("setting the mode of %s: %w", dir, err)
		}
	}
	return nil
}

// CreatePrivateDir makes dir a real directory the user owns. A missing one is
// created with mode exactly 0700; an existing one keeps its mode, so a mode
// someone narrowed stays visible to the caller as a store it can't use rather
// than being loosened here. A symlink or anything else at dir is refused,
// never followed.
func CreatePrivateDir(dir string) error {
	created := true
	if err := os.Mkdir(dir, 0o700); err != nil {
		if !os.IsExist(err) {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		created = false
	}
	info, err := checkOwnedDir(dir)
	if err != nil {
		return err
	}
	if created && info.Mode().Perm() != 0o700 {
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("setting the mode of %s: %w", dir, err)
		}
	}
	return nil
}

// checkOwnedDir reads dir with Lstat and refuses anything but a real
// directory the user owns.
func checkOwnedDir(dir string) (os.FileInfo, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory (mode %s)", dir, info.Mode().Type())
	}
	if !OwnedByUser(info) {
		return nil, fmt.Errorf("%s is not owned by the current user", dir)
	}
	return info, nil
}

// InstanceDirs lists every instance of the workspace on disk: the real
// directories among the workspace root's children (its .niwa excepted) and,
// when it exists, among the entries of ~/.niwa/w-<key>/kept/. Entries are read
// with Lstat semantics, so a symlink or any other entry type is skipped and
// never followed. An instance doesn't need its .niwa/instance.json to count:
// one half-created, half-destroyed or half-moved into kept/ still holds links
// that must be seen.
//
// An error listing the root, or listing a kept/ directory that exists, is
// returned with whatever was listed before it; a caller that deletes on the
// strength of the answer deletes nothing then. The plugin store's sweep is
// that reader: on this error it deletes no release. A new place instances
// live is added here, so the sweep and repair both see their links.
func InstanceDirs(home, workspaceRoot string) ([]string, error) {
	var dirs []string
	rootEntries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("listing workspace root %s: %w", workspaceRoot, err)
	}
	for _, e := range rootEntries {
		if e.Name() == ".niwa" || !e.Type().IsDir() {
			continue
		}
		dirs = append(dirs, filepath.Join(workspaceRoot, e.Name()))
	}

	kept := filepath.Join(WorkspaceDir(home, workspaceRoot), KeptDirName)
	keptEntries, err := os.ReadDir(kept)
	switch {
	case err == nil:
		for _, e := range keptEntries {
			if !e.Type().IsDir() {
				continue
			}
			dirs = append(dirs, filepath.Join(kept, e.Name()))
		}
	case os.IsNotExist(err):
	default:
		sort.Strings(dirs)
		return dirs, fmt.Errorf("listing kept instances %s: %w", kept, err)
	}
	sort.Strings(dirs)
	return dirs, nil
}
