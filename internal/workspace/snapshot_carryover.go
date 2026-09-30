package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// SnapshotManifestFile lists every path the config source supplied to a
// snapshot, one slash-separated path relative to the config dir per line. It
// is written into staging right after extraction, before any local state is
// carried in, so it records exactly what the source supplied and nothing niwa
// or a user added afterwards.
//
// The next swap reads it back to tell the two apart. A path the manifest names
// was supplied by the source: the new snapshot decides whether it still
// exists. A path the manifest does not name was put there by someone else -- a
// session keeping notes at the workspace root, a hand-written script -- and
// the swap carries it into the new snapshot instead of deleting it.
//
// Being line-based, the manifest cannot represent a path containing a line
// break, and writeSnapshotManifest refuses one rather than record it wrongly:
// a source file named "x\nnotes.md" would otherwise put a "notes.md" line in
// the manifest, and the next refresh would delete a local notes.md as if the
// source had supplied it.
const SnapshotManifestFile = ".niwa-snapshot-manifest"

// carryOverReserved names top-level entries the generic carry-over never
// touches. The provenance marker and the manifest are rewritten for every
// snapshot; instance.json, dispatch-briefs/ and sessions/ each have their own
// preserve step with rules of its own (local wins on a name clash, the session
// store keeps its 0700 mode). .git is a legacy working tree's metadata, which
// the conversion to a snapshot exists to drop; copySubtree skips it for the
// same reason.
var carryOverReserved = map[string]bool{
	ProvenanceFile:        true,
	SnapshotManifestFile:  true,
	StateFile:             true,
	dispatchBriefsDirName: true,
	sessionsDirName:       true,
	".git":                true,
}

// isReservedTopLevel reports whether rel names one of carryOverReserved at the
// top of the config dir. The same names deeper down are ordinary paths.
func isReservedTopLevel(rel string) bool {
	return !strings.Contains(rel, "/") && carryOverReserved[rel]
}

// writeSnapshotManifest records every path currently under staging. Call it
// after extraction and before anything else is written into staging. It fails,
// leaving the refresh to fail with it, when a supplied path contains a line
// break (see SnapshotManifestFile).
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
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.ContainsAny(rel, "\n\r") {
			return fmt.Errorf("the config source supplies a path containing a line break, %q; rename it in the source", rel)
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

// readSnapshotManifest returns the set of source-supplied paths recorded in
// configDir's manifest. ok is false when the snapshot predates the manifest.
func readSnapshotManifest(configDir string) (supplied map[string]bool, ok bool, err error) {
	f, err := os.Open(filepath.Join(configDir, SnapshotManifestFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()
	supplied = map[string]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			supplied[line] = true
		}
	}
	if err := sc.Err(); err != nil {
		return nil, false, err
	}
	return supplied, true, nil
}

// carryResult is what carryLocalPaths did.
type carryResult struct {
	// Carried lists the local paths copied into staging, relative and
	// slash-separated. A carried directory is listed once, not per entry.
	Carried []string
	// Skipped lists local entries that are not a regular file, directory or
	// symlink (a socket, a FIFO, a device). They can't be copied, so the swap
	// drops them; the caller reports them.
	Skipped []string
	// HaveManifest is whether the previous snapshot had a manifest to decide
	// by. Without one, Carried may include files the source deleted.
	HaveManifest bool
}

// carryLocalPaths copies every path under the current configDir that the
// config source did not supply into staging, so the swap that replaces
// configDir with staging removes only what the source stopped supplying.
//
// Which paths the source supplied comes from the previous snapshot's manifest.
// A snapshot written before the manifest existed has none; then the new
// snapshot's own contents are the only evidence. Every path the new snapshot
// lacks is carried, which can keep a file the source deleted in the same
// refresh, so the caller reports the carried paths in that case: keeping a
// stale file is recoverable by hand, and deleting a file nobody else has a copy
// of is not. A local file at a path the new snapshot supplies is replaced by
// the source's copy in that case, since nothing says it wasn't the source's.
//
// With a manifest, a local path the source newly supplies has two owners. The
// function refuses, naming every such path, rather than let either copy
// overwrite the other. The existing config dir is never modified: everything
// happens in staging.
func carryLocalPaths(configDir, staging string) (carryResult, error) {
	var res carryResult
	if info, statErr := os.Lstat(configDir); statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			// First materialization: nothing local exists yet.
			return res, nil
		}
		return res, statErr
	} else if !info.IsDir() {
		// SwapSnapshotAtomic refuses a non-directory target; let it say so.
		return res, nil
	}
	supplied, haveManifest, err := readSnapshotManifest(configDir)
	if err != nil {
		return res, fmt.Errorf("read snapshot manifest: %w", err)
	}
	res.HaveManifest = haveManifest

	type dirMode struct {
		path string
		perm os.FileMode
	}
	var (
		conflicts []string
		dirModes  []dirMode
	)
	carriedDirs := map[string]bool{}
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
		parentCarried := carriedDirs[filepath.ToSlash(filepath.Dir(rel))]

		if !parentCarried {
			if haveManifest && supplied[relSlash] {
				// The source's path. Its fate is whatever the new snapshot
				// says, but a local file may still live inside a source
				// directory, so keep walking.
				return nil
			}

			dst := filepath.Join(staging, rel)
			dstInfo, dstErr := os.Lstat(dst)
			switch {
			case dstErr == nil:
				if d.IsDir() && dstInfo.IsDir() {
					// Both sides have a directory here; carry its local
					// children one by one.
					return nil
				}
				return conflictOrYield(haveManifest, d, relSlash, &conflicts)
			case errors.Is(dstErr, syscall.ENOTDIR):
				// Staging has a file where this path's parent would be.
				return conflictOrYield(haveManifest, d, relSlash, &conflicts)
			case !errors.Is(dstErr, fs.ErrNotExist):
				return dstErr
			}
		}

		copied, err := copyLocalEntry(path, filepath.Join(staging, rel))
		if err != nil {
			return err
		}
		if !copied {
			res.Skipped = append(res.Skipped, relSlash)
			return nil
		}
		if d.IsDir() {
			carriedDirs[relSlash] = true
			if info, err := d.Info(); err == nil {
				dirModes = append(dirModes, dirMode{filepath.Join(staging, rel), info.Mode().Perm()})
			}
		}
		if !parentCarried {
			res.Carried = append(res.Carried, relSlash)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	if len(conflicts) > 0 {
		return res, fmt.Errorf("the config source now supplies %s, which already exist(s) locally and did not come from the source; move them out of %s and re-run",
			strings.Join(conflicts, ", "), configDir)
	}
	// Deepest first, so a read-only parent doesn't block its children. Done
	// only on success: the caller removes staging on failure, which a
	// read-only directory would block.
	for i := len(dirModes) - 1; i >= 0; i-- {
		if err := os.Chmod(dirModes[i].path, dirModes[i].perm); err != nil {
			return res, err
		}
	}
	return res, nil
}

// conflictOrYield handles a local path the new snapshot also has. With a
// manifest it is a conflict, recorded and not descended into. Without one the
// source's copy wins. Either way the local entry is not carried.
func conflictOrYield(haveManifest bool, d fs.DirEntry, relSlash string, conflicts *[]string) error {
	if haveManifest {
		*conflicts = append(*conflicts, relSlash)
	}
	if d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

// copyLocalEntry copies one entry from src to dst: a directory is created
// with its mode, a symlink is recreated as a symlink, and a regular file is
// streamed with its mode. It reports false, copying nothing, for anything
// else (a socket, a FIFO, a device). Unlike copySubtree it is not a guard
// against hostile source content: everything it copies was already in the
// config dir, and it only moves it to the same relative place in the next
// snapshot.
func copyLocalEntry(src, dst string) (bool, error) {
	info, err := os.Lstat(src)
	if err != nil {
		return false, err
	}
	mode := info.Mode()
	switch {
	case mode&os.ModeSymlink != 0:
		link, err := os.Readlink(src)
		if err != nil {
			return false, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, err
		}
		return true, os.Symlink(link, dst)
	case mode.IsDir():
		// Owner-writable while its children are copied in; a read-only
		// directory would otherwise refuse them. carryLocalPaths applies the
		// real mode once the walk is done.
		if err := os.MkdirAll(dst, mode.Perm()|0o700); err != nil {
			return false, err
		}
		return true, os.Chmod(dst, mode.Perm()|0o700)
	case mode.IsRegular():
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, err
		}
		return true, streamFile(src, dst, mode.Perm())
	default:
		return false, nil
	}
}

func streamFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}
