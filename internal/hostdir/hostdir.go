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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"

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

// alternatesMax bounds how much of a clone's alternates file the strict read
// takes. A real one holds a line or two; a larger one isn't niwa's, and the
// strict read fails on it rather than read part of it.
const alternatesMax = 64 << 10

// alternatesFiles are the files under a clone's .git/objects/info/ that name
// object directories the clone reads from: the alternates file, and the copy
// a dissociate leaves while it runs, which still names what the clone
// borrowed until the dissociate finishes.
var alternatesFiles = []string{"alternates", "alternates.niwa-dissociate"}

// beforeRead, when a test sets it, runs with each directory the strict walk
// is about to read, once the directory above it has listed it. A test
// removes the directory there to show that one which vanished between
// listing and reading fails the walk. It's nil outside the tests.
var beforeRead func(dir string)

// Clones is the lenient clone walk: every real directory two levels under
// each instance InstanceDirs lists (<instance>/<group>/<repo>), sorted and
// each once. Names starting with a dot (.niwa, .claude) are skipped at the
// group and repo levels, and so is anything that isn't a real directory by
// Lstat. It needs no state file and no config, so a half-created instance
// and a repo the config no longer names both count (PRD R7).
//
// Anything it can't read is skipped, so its answer can be short. That makes
// it fit for finding the clones a check, a restore or a probe looks at, and
// unfit to authorize removing anything: only StrictClones may do that.
func Clones(home, workspaceRoot string) []string {
	var clones []string
	for _, inst := range lenientInstances(home, workspaceRoot) {
		clones = append(clones, InstanceClones(inst)...)
	}
	return sortedUnique(clones)
}

// lenientInstances lists the instances Clones walks: those InstanceDirs
// lists, except that a root that can't be listed still leaves kept/'s
// instances to walk, and the other way round.
func lenientInstances(home, workspaceRoot string) []string {
	var dirs []string
	if entries, err := os.ReadDir(workspaceRoot); err == nil {
		for _, e := range entries {
			if e.Name() != ".niwa" && e.Type().IsDir() {
				dirs = append(dirs, filepath.Join(workspaceRoot, e.Name()))
			}
		}
	}
	kept := filepath.Join(WorkspaceDir(home, workspaceRoot), KeptDirName)
	if entries, err := os.ReadDir(kept); err == nil {
		for _, e := range entries {
			if e.Type().IsDir() {
				dirs = append(dirs, filepath.Join(kept, e.Name()))
			}
		}
	}
	return dirs
}

// InstanceClones is the lenient walk of one instance: its real directories
// two levels down (<group>/<repo>), names starting with a dot skipped at
// both levels and anything unreadable skipped, sorted. A caller whose own
// instance must be in the answer whatever the root's listing gave adds this.
func InstanceClones(instance string) []string {
	var clones []string
	groups, err := os.ReadDir(instance)
	if err != nil {
		return nil
	}
	for _, g := range groups {
		if !g.Type().IsDir() || strings.HasPrefix(g.Name(), ".") {
			continue
		}
		repos, err := os.ReadDir(filepath.Join(instance, g.Name()))
		if err != nil {
			continue
		}
		for _, r := range repos {
			if r.Type().IsDir() && !strings.HasPrefix(r.Name(), ".") {
				clones = append(clones, filepath.Join(instance, g.Name(), r.Name()))
			}
		}
	}
	return sortedUnique(clones)
}

// StrictClones is the strict clone walk, the only one whose answer may
// authorize removing a mirror store (PRD R7, R9). It lists the same clones as
// Clones, over the workspace root (its .niwa excepted) and
// ~/.niwa/w-<key>/kept/, and returns an error, never a partial list, when
// anything on the way can't be read for certain:
//
//   - the root, kept/, an instance or a group directory can't be listed, or
//     is gone by the time it's read after the level above listed it;
//   - kept/ is a symlink, or an instance or group entry is a symlink to a
//     directory or something other than a directory, a regular file or a
//     symlink (a fifo, a socket, a device). A regular file there is skipped:
//     niwa writes CLAUDE.md and files like it at both levels;
//   - a clone's .git is a symlink or a gitfile, or its .git/objects or
//     .git/objects/info is a symlink;
//   - an alternates file, or a leftover alternates.niwa-dissociate, fails
//     the strict read (see Pins).
//
// A missing kept/, a clone with no .git and a clone with no alternates file
// are fine: they name nothing.
func StrictClones(home, workspaceRoot string) ([]string, error) {
	instances, err := strictList(workspaceRoot, func(name string) bool { return name == ".niwa" })
	if err != nil {
		return nil, err
	}
	kept, err := strictKeptInstances(filepath.Join(WorkspaceDir(home, workspaceRoot), KeptDirName))
	if err != nil {
		return nil, err
	}
	return strictClonesOf(append(instances, kept...))
}

// StrictKeptClones is the strict walk over keyDir/kept/ alone: once a
// workspace root is gone, the kept instances are the only ones left that can
// borrow from its mirrors. A missing kept/ lists nothing.
func StrictKeptClones(keyDir string) ([]string, error) {
	instances, err := strictKeptInstances(filepath.Join(keyDir, KeptDirName))
	if err != nil {
		return nil, err
	}
	return strictClonesOf(instances)
}

// strictKeptInstances lists the instances in a kept/ directory for the strict
// walk. A missing kept/ has none; a kept/ that is a symlink is an error,
// since the walk follows no link.
func strictKeptInstances(kept string) ([]string, error) {
	info, err := os.Lstat(kept)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspecting kept instances %s: %w", kept, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("kept instances %s is not a directory (mode %s)", kept, info.Mode().Type())
	}
	return strictList(kept, func(string) bool { return false })
}

// strictClonesOf runs the strict walk's two levels under each instance and
// checks each clone it finds.
func strictClonesOf(instances []string) ([]string, error) {
	dotted := func(name string) bool { return strings.HasPrefix(name, ".") }
	var clones []string
	for _, inst := range instances {
		groups, err := strictList(inst, dotted)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			repos, err := strictRepos(g)
			if err != nil {
				return nil, err
			}
			for _, clone := range repos {
				if err := strictClone(clone); err != nil {
					return nil, err
				}
				clones = append(clones, clone)
			}
		}
	}
	return sortedUnique(clones), nil
}

// strictList lists the directories in dir for the strict walk, an instance
// or a group level: skip names an entry to pass over. A symlink to a
// directory, or an entry that is none of a directory, a regular file or a
// symlink, is an error; a regular file and a symlink to anything else are
// skipped.
func strictList(dir string, skip func(string) bool) ([]string, error) {
	if beforeRead != nil {
		beforeRead(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", dir, err)
	}
	var dirs []string
	for _, e := range entries {
		if skip(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		switch t := e.Type(); {
		case t.IsDir():
			dirs = append(dirs, path)
		case t.IsRegular():
		case t&fs.ModeSymlink != 0:
			info, err := os.Stat(path)
			switch {
			case err == nil && info.IsDir():
				return nil, fmt.Errorf("%s is a symlink to a directory, which the walk doesn't follow", path)
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return nil, fmt.Errorf("inspecting %s: %w", path, err)
			}
		default:
			return nil, fmt.Errorf("%s is not a directory, a regular file or a symlink (mode %s)", path, t)
		}
	}
	return dirs, nil
}

// strictRepos lists the clone directories of one group for the strict walk:
// its real directories whose names don't start with a dot. Anything else at
// that level is skipped, as in Clones.
func strictRepos(group string) ([]string, error) {
	if beforeRead != nil {
		beforeRead(group)
	}
	entries, err := os.ReadDir(group)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", group, err)
	}
	var repos []string
	for _, e := range entries {
		if e.Type().IsDir() && !strings.HasPrefix(e.Name(), ".") {
			repos = append(repos, filepath.Join(group, e.Name()))
		}
	}
	return repos, nil
}

// strictClone checks one clone for the strict walk: a .git that is a real
// directory or absent, real directories or nothing at .git/objects and
// .git/objects/info, and every alternates file there readable strictly. A
// symlink at any of them would make what the clone reads depend on a path
// the walk can't vouch for.
func strictClone(clone string) error {
	if beforeRead != nil {
		beforeRead(clone)
	}
	if _, err := os.Lstat(clone); err != nil {
		return fmt.Errorf("inspecting %s: %w", clone, err)
	}
	gitDir := filepath.Join(clone, ".git")
	for _, dir := range []string{gitDir, filepath.Join(gitDir, "objects"), filepath.Join(gitDir, "objects", "info")} {
		info, err := os.Lstat(dir)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return nil
		case err != nil:
			return fmt.Errorf("inspecting %s: %w", dir, err)
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink", dir)
		case dir == gitDir && info.Mode().IsRegular():
			return fmt.Errorf("%s is a gitfile, whose object directory the walk can't vouch for", dir)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory (mode %s)", dir, info.Mode().Type())
		}
	}
	for _, name := range alternatesFiles {
		if _, err := readAlternatesStrict(filepath.Join(gitDir, "objects", "info", name)); err != nil {
			return err
		}
	}
	return nil
}

// readAlternatesStrict reads the lines of the alternates file at path that
// name an object directory. A missing file names nothing. It opens the file
// without following a symlink and without blocking on a fifo, and checks the
// open file, so anything but a regular file is an error, and so is a file
// over alternatesMax, one that isn't valid UTF-8, a line holding a NUL or
// any other control character, and a quoted line (git unquotes those; this
// read doesn't, so it can't say what one names). Blank lines and lines
// starting with # are skipped, as git skips them.
func readAlternatesStrict(path string) ([]string, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspecting %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file (mode %s)", path, info.Mode().Type())
	}
	data, err := io.ReadAll(io.LimitReader(f, alternatesMax+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) > alternatesMax {
		return nil, fmt.Errorf("%s is over %d bytes", path, alternatesMax)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%s is not valid UTF-8", path)
	}
	var lines []string
	for n, line := range strings.Split(string(data), "\n") {
		if strings.ContainsFunc(line, isControl) {
			return nil, fmt.Errorf("line %d of %s holds a control character", n+1, path)
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, `"`) {
			return nil, fmt.Errorf("line %d of %s is quoted", n+1, path)
		}
		lines = append(lines, line)
	}
	return lines, nil
}

// isControl reports whether r is a C0 or C1 control character or DEL.
func isControl(r rune) bool {
	return r < 0x20 || r >= 0x7f && r <= 0x9f
}

// Pins reports whether any of clones reads objects from the store directory
// at store, by any line of its alternates file or of a leftover
// alternates.niwa-dissociate, so the store must stay (PRD R9). Each file is
// read strictly (readAlternatesStrict). A relative line is joined to the
// clone's .git/objects, as git resolves it, and every line then has its
// symlinks resolved, so a line naming the stable path pins the store the
// link names now, and one naming a .store/ directory directly pins that
// store. A line pins the store when what it resolves to is the store's
// objects directory by os.SameFile, or lies inside the store's resolved
// directory. Nothing compares paths as strings, so two spellings of one
// directory (macOS's /var and /private/var) match.
//
// A line resolving to nothing that exists names nothing. Any other error,
// from a file or from resolving a line, is returned, and the caller removes
// nothing on it. A store that doesn't exist is pinned by nothing.
func Pins(clones []string, store string) (bool, error) {
	storeReal, err := filepath.EvalSymlinks(store)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("resolving %s: %w", store, err)
	}
	objects, err := os.Stat(filepath.Join(storeReal, "objects"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, fmt.Errorf("inspecting %s: %w", filepath.Join(storeReal, "objects"), err)
	}
	for _, clone := range clones {
		cloneObjects := filepath.Join(clone, ".git", "objects")
		for _, name := range alternatesFiles {
			lines, err := readAlternatesStrict(filepath.Join(cloneObjects, "info", name))
			if err != nil {
				return false, err
			}
			for _, line := range lines {
				pinned, err := linePins(cloneObjects, line, storeReal, objects)
				if err != nil || pinned {
					return pinned, err
				}
			}
		}
	}
	return false, nil
}

// linePins reports whether one alternates line of a clone whose object
// directory is cloneObjects resolves into the store at storeReal, whose
// objects directory is objects (nil when it has none). The line is tried as
// git reads it and, when that differs, with surrounding space trimmed, so a
// stray space can only add a pin.
func linePins(cloneObjects, line, storeReal string, objects os.FileInfo) (bool, error) {
	forms := []string{line}
	if trimmed := strings.TrimSpace(line); trimmed != line {
		forms = append(forms, trimmed)
	}
	for _, p := range forms {
		if !filepath.IsAbs(p) {
			p = filepath.Join(cloneObjects, p)
		}
		resolved, err := filepath.EvalSymlinks(p)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return false, fmt.Errorf("resolving alternates line %q: %w", line, err)
		}
		if objects != nil {
			if info, err := os.Stat(resolved); err == nil && os.SameFile(info, objects) {
				return true, nil
			}
		}
		if rel, err := filepath.Rel(storeReal, resolved); err == nil &&
			rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true, nil
		}
	}
	return false, nil
}

// sortedUnique sorts paths and drops repeats.
func sortedUnique(paths []string) []string {
	sort.Strings(paths)
	out := paths[:0]
	for i, p := range paths {
		if i == 0 || p != paths[i-1] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
