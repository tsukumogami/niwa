package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SnapshotManifestFile lists every path the config source put into a snapshot,
// one slash-separated path relative to the config dir per line. It is written
// into staging right after extraction, before any local state is carried in,
// so it records exactly what upstream supplied and nothing niwa or a user
// added afterwards.
//
// The next swap reads it back to tell the two apart. A path the manifest names
// belongs to upstream: the new snapshot decides whether it still exists. A
// path the manifest does not name was put there by someone else -- a session
// keeping notes at the workspace root, a hand-written script -- and the swap
// carries it into the new snapshot instead of deleting it.
const SnapshotManifestFile = ".niwa-snapshot-manifest"

// carryOverReserved names top-level entries the generic carry-over never
// touches. The provenance marker and the manifest are rewritten for every
// snapshot; instance.json, dispatch-briefs/ and sessions/ each have their own
// preserve step with rules of its own (local wins on a name clash, the session
// store keeps its 0700 mode).
var carryOverReserved = map[string]bool{
	ProvenanceFile:        true,
	SnapshotManifestFile:  true,
	StateFile:             true,
	dispatchBriefsDirName: true,
	sessionsDirName:       true,
}

// isReservedTopLevel reports whether rel names one of carryOverReserved at the
// top of the config dir. The same names deeper down are ordinary paths.
func isReservedTopLevel(rel string) bool {
	return !strings.Contains(rel, "/") && carryOverReserved[rel]
}

// writeSnapshotManifest records every path currently under staging. Call it
// after extraction and before anything else is written into staging.
func writeSnapshotManifest(staging string) error {
	var paths []string
	err := filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == staging {
			return nil
		}
		rel, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if isReservedTopLevel(rel) {
			return nil
		}
		paths = append(paths, rel)
		return nil
	})
	if err != nil {
		return fmt.Errorf("list snapshot contents: %w", err)
	}
	sort.Strings(paths)
	var b strings.Builder
	for _, p := range paths {
		b.WriteString(p)
		b.WriteByte('\n')
	}
	dst := filepath.Join(staging, SnapshotManifestFile)
	if err := os.WriteFile(dst, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", dst, err)
	}
	return nil
}

// readSnapshotManifest returns the set of upstream-supplied paths recorded in
// configDir's manifest. ok is false when the snapshot predates the manifest.
func readSnapshotManifest(configDir string) (claimed map[string]bool, ok bool, err error) {
	f, err := os.Open(filepath.Join(configDir, SnapshotManifestFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	claimed = map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			claimed[line] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	return claimed, true, nil
}

// carryUnclaimedPaths copies every path under the current configDir that the
// config source did not supply into staging, so the swap that replaces
// configDir with staging removes only what upstream stopped supplying.
//
// Which paths are upstream's comes from the previous snapshot's manifest. A
// snapshot written before the manifest existed has none; then the new
// snapshot's own contents are the only evidence, and every path the new
// snapshot lacks is carried. That can keep a file upstream deleted in the
// same refresh, which is why the carried paths are reported in that case:
// keeping a stale file is recoverable by hand, and deleting a file nobody else
// has a copy of is not.
//
// With a manifest, a path that was never upstream's and that upstream now
// supplies is a clash, and the function refuses rather than let either copy
// overwrite the other. The existing snapshot is left untouched either way.
//
// Returns the carried paths, relative and slash-separated, in walk order, and
// whether the previous snapshot had a manifest to decide them by.
func carryUnclaimedPaths(configDir, staging string) (carried []string, haveManifest bool, err error) {
	if info, statErr := os.Lstat(configDir); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			// First materialization: nothing local exists yet.
			return nil, false, nil
		}
		return nil, false, statErr
	} else if !info.IsDir() {
		// SwapSnapshotAtomic refuses a non-directory target; let it say so.
		return nil, false, nil
	}
	claimed, haveManifest, err := readSnapshotManifest(configDir)
	if err != nil {
		return nil, false, fmt.Errorf("read snapshot manifest: %w", err)
	}
	err = filepath.WalkDir(configDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == configDir {
			return nil
		}
		rel, err := filepath.Rel(configDir, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		if isReservedTopLevel(relSlash) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		dst := filepath.Join(staging, rel)
		dstInfo, dstErr := os.Lstat(dst)
		inStaging := dstErr == nil
		if dstErr != nil && !errors.Is(dstErr, fs.ErrNotExist) {
			return dstErr
		}

		if haveManifest && claimed[relSlash] {
			// Upstream's path. Its fate is whatever the new snapshot says,
			// but a local file may still live inside an upstream directory.
			return nil
		}

		if inStaging {
			if d.IsDir() && dstInfo.IsDir() {
				// Both sides have a directory here; carry its local
				// children one by one.
				return nil
			}
			if !haveManifest {
				// No record of what upstream supplied before, and upstream
				// supplies it now: take upstream's copy.
				return nil
			}
			return fmt.Errorf("the config source now provides %s, which already exists locally and was not part of the previous snapshot; move it out of %s and re-run", relSlash, configDir)
		}

		if err := copyLocalEntry(path, dst); err != nil {
			return err
		}
		carried = append(carried, relSlash)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, haveManifest, err
	}
	return carried, haveManifest, nil
}

// copyLocalEntry copies a file, symlink or directory tree from src to dst,
// keeping modes and recreating symlinks as symlinks. Unlike copySubtree it is
// not a guard against hostile upstream content: everything it copies was
// already in the config dir, and it only moves it to the same relative place
// in the next snapshot.
func copyLocalEntry(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.IsDir():
			if err := os.MkdirAll(target, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(target, data, info.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(target, info.Mode().Perm())
		default:
			return fmt.Errorf("cannot carry %s across a config refresh: not a regular file, directory or symlink", path)
		}
	})
}
