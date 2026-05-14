package workspace

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/source"
	"github.com/tsukumogami/niwa/internal/testfault"
)

// FetchSubpathViaGitClone implements the non-GitHub branch of
// EnsureConfigSnapshot: shallow-clone src into a temp dir, resolve
// the HEAD commit oid, and copy <cloneDir>/<src.Subpath> into
// stagingDir. Returns the resolved oid so the caller can write it
// into the provenance marker.
//
// stagingDir must exist and be empty. The caller is responsible for
// writing the provenance marker into stagingDir and calling
// SwapSnapshotAtomic to promote it.
//
// Security discipline mirrors internal/github.ExtractSubpath:
//
//   - positive type allowlist — regular files and directories only;
//     symlinks, FIFOs, devices, and other non-regular entries are
//     silently skipped (lstat-based so the walk never follows a link).
//   - filename validation — reject any component containing NUL,
//     backslash, or "..".
//   - path containment — computed dest paths must stay inside
//     stagingDir (defense against crafted names that survive
//     validation but resolve outside the tree after Clean).
//   - subpath filter — only entries under <cloneDir>/<src.Subpath>/
//     are copied, matching PRD R4's whole-repo and subpath cases.
//
// `testfault.Maybe("fetch-fallback")` hook at entry for parity with
// the GitHub path's fault-injection seams.
func FetchSubpathViaGitClone(ctx context.Context, src source.Source, stagingDir string) (oid string, err error) {
	if err := testfault.Maybe("fetch-fallback"); err != nil {
		return "", fmt.Errorf("fallback: %w", err)
	}
	if stagingDir == "" {
		return "", errors.New("fallback: stagingDir is empty")
	}
	cloneURL, subpath, err := resolveFallbackCloneURL(src)
	if err != nil {
		return "", err
	}
	return cloneAndCopy(ctx, cloneURL, src.Ref, subpath, stagingDir)
}

// resolveFallbackCloneURL produces a (cloneURL, subpath) pair from a
// source. The default path synthesizes https:// from owner+repo. When
// the source's Host carries a "file://" raw URL (used by tests and
// anyone passing a bare cloneable URL), the URL is returned verbatim
// and the Owner/Repo fields are not consulted.
func resolveFallbackCloneURL(src source.Source) (cloneURL, subpath string, err error) {
	if strings.HasPrefix(src.Host, "file://") {
		return src.Host, src.Subpath, nil
	}
	if src.Owner == "" || src.Repo == "" {
		return "", "", errors.New("fallback: source missing owner or repo")
	}
	url, err := src.CloneURL("https")
	if err != nil {
		return "", "", fmt.Errorf("fallback: resolve clone URL: %w", err)
	}
	return url, src.Subpath, nil
}

// cloneAndCopy is the shared implementation: shallow-clone cloneURL
// (optionally pinned to ref), copy <clone>/<subpath> into stagingDir,
// and return the HEAD oid. Both the source-based and raw-URL entry
// points funnel through here so the security discipline applies once.
func cloneAndCopy(ctx context.Context, cloneURL, ref, subpath, stagingDir string) (oid string, err error) {
	tmp, oid, err := shallowCloneToTemp(ctx, cloneURL, ref)
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)

	if copyErr := copyFromCloneRoot(tmp, subpath, stagingDir, cloneURL); copyErr != nil {
		return "", copyErr
	}
	return oid, nil
}

// shallowCloneToTemp performs `git clone --depth 1` (optionally with
// `--branch ref`) of cloneURL into a fresh temp directory, then runs
// `git rev-parse HEAD` inside the clone to record the resolved oid.
//
// Returns the temp directory path and the head oid; the caller is
// responsible for `os.RemoveAll(tmp)` after the clone is no longer
// needed. Errors are wrapped with the "fallback:" prefix so callers
// can surface them uniformly.
//
// Factored so the same clone seam can be reused by:
//
//   - cloneAndCopy (the explicit-subpath path);
//   - ProbeAndFetchSubpath (the discovery-mode path that probes the
//     clone root for marker files before copying).
//
// Both call sites share the clone semantics; only the post-clone
// disposition differs.
func shallowCloneToTemp(ctx context.Context, cloneURL, ref string) (tmp, oid string, err error) {
	tmp, err = os.MkdirTemp("", "niwa-fallback-")
	if err != nil {
		return "", "", fmt.Errorf("fallback: create temp dir: %w", err)
	}

	args := []string{"clone", "--depth", "1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, cloneURL, tmp)

	cmd := exec.CommandContext(ctx, "git", args...)
	if out, cloneErr := cmd.CombinedOutput(); cloneErr != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("fallback: git clone %s: %w\n%s", cloneURL, cloneErr, out)
	}

	revParse := exec.CommandContext(ctx, "git", "-C", tmp, "rev-parse", "HEAD")
	headBytes, revErr := revParse.Output()
	if revErr != nil {
		_ = os.RemoveAll(tmp)
		return "", "", fmt.Errorf("fallback: git rev-parse HEAD: %w", revErr)
	}
	return tmp, strings.TrimSpace(string(headBytes)), nil
}

// copyFromCloneRoot copies <tmp>/<subpath> into stagingDir, mirroring
// the existing cloneAndCopy disposition. Empty subpath copies the
// whole tree; a non-empty subpath that resolves to a regular file
// copies that file under its basename (PRD R4 single-file case); a
// non-empty subpath that resolves to a directory walks the subtree
// and copies regular files.
//
// cloneURL is used only for diagnostic messages — pass an empty
// string when no remote URL is available.
func copyFromCloneRoot(tmp, subpath, stagingDir, cloneURL string) error {

	// Identify the source root inside the clone: either the whole
	// tree (when Subpath is empty) or the named subdirectory/file.
	subpath = strings.Trim(subpath, "/")
	sourceRoot := tmp
	if subpath != "" {
		sourceRoot = filepath.Join(tmp, filepath.FromSlash(subpath))
	}

	info, err := os.Lstat(sourceRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("fallback: subpath %q not found in clone of %s", subpath, cloneURL)
		}
		return fmt.Errorf("fallback: stat subpath %q: %w", subpath, err)
	}

	if !info.IsDir() {
		// Single-file subpath (PRD R4 case): copy the file under its
		// basename into stagingDir.
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fallback: subpath %q is not a regular file or directory", subpath)
		}
		name := filepath.Base(sourceRoot)
		if err := validateRelName(name); err != nil {
			return fmt.Errorf("fallback: %w", err)
		}
		dest, err := fallbackSafeJoin(stagingDir, name)
		if err != nil {
			return fmt.Errorf("fallback: %w", err)
		}
		if err := copyRegularFile(sourceRoot, dest, info.Mode().Perm()); err != nil {
			return fmt.Errorf("fallback: copy %s: %w", sourceRoot, err)
		}
		return nil
	}

	// Directory subpath (whole-repo or subpath-is-dir): walk and copy
	// each regular file, mirroring directory structure.
	if err := copySubtree(sourceRoot, stagingDir); err != nil {
		return fmt.Errorf("fallback: copy subtree: %w", err)
	}

	return nil
}

// ProbeAndFetchSubpath is the discovery-mode entry point for the
// non-GitHub fallback path. Symmetric with
// internal/github.ProbeAndExtractSubpath:
//
//  1. Resolves the clone URL and shallow-clones into a temp dir.
//  2. Runs probeAndResolveCloneRoot to scan the clone's top level for
//     marker files in `markers` and asks decider to resolve the rank.
//  3. Copies the resolved subpath into stagingDir.
//
// Returns the resolved subpath (the value the caller writes to the
// provenance marker), the rank (1 or 2; 0 on error), the optional
// deprecation notice for rank-2 (callers route it through the
// workspace disclosure helper), and the HEAD oid.
//
// All seven security defenses from copyFromCloneRoot apply unchanged
// on pass-2. Empty `.niwa/` at the source root does NOT count as
// rank-1 — probeAndResolveCloneRoot's os.Stat against the rank-1
// FILE path (not the directory) encodes PRD R6 at this layer.
//
// On any error path the function returns before the copy begins,
// leaving stagingDir untouched (PRD R5 atomic-failure invariant).
// The temp clone is always removed via defer.
func ProbeAndFetchSubpath(
	ctx context.Context,
	src source.Source,
	markers config.MarkerSet,
	decider func(found, markers config.MarkerSet) (string, *config.DeprecationNotice, error),
	stagingDir string,
) (resolvedSubpath string, rank int, notice *config.DeprecationNotice, oid string, err error) {
	if err := testfault.Maybe("fetch-fallback"); err != nil {
		return "", 0, nil, "", fmt.Errorf("fallback: %w", err)
	}
	if stagingDir == "" {
		return "", 0, nil, "", errors.New("fallback: stagingDir is empty")
	}
	if decider == nil {
		return "", 0, nil, "", errors.New("fallback: decider is nil")
	}

	cloneURL, _, urlErr := resolveFallbackCloneURL(src)
	if urlErr != nil {
		return "", 0, nil, "", urlErr
	}

	tmp, oid, cloneErr := shallowCloneToTemp(ctx, cloneURL, src.Ref)
	if cloneErr != nil {
		return "", 0, nil, "", cloneErr
	}
	defer os.RemoveAll(tmp)

	resolvedSubpath, notice, probeErr := probeAndResolveCloneRoot(tmp, markers, decider)
	if probeErr != nil {
		return "", 0, nil, "", probeErr
	}
	if notice != nil {
		rank = 2
	} else {
		rank = 1
	}

	if copyErr := copyFromCloneRoot(tmp, resolvedSubpath, stagingDir, cloneURL); copyErr != nil {
		return "", 0, nil, "", copyErr
	}
	return resolvedSubpath, rank, notice, oid, nil
}

// probeAndResolveCloneRoot inspects the top level of a shallow clone
// directory for the rank-1 and rank-2 markers from `markers`,
// populates a `found` MarkerSet from the os.Stat results, and calls
// decider to resolve the rank.
//
// The probe checks `<cloneDir>/<Rank1Dir>/<Rank1File>` (rank-1) and
// `<cloneDir>/<Rank2Path>` (rank-2) — both as files, not directories.
// PRD R6 / AC-D8 (empty `.niwa/` does NOT count as rank-1) is encoded
// naturally: if the directory exists but the marker file inside does
// not, the rank-1 stat returns ErrNotExist and HasRank1 stays false.
// Non-regular file types (symlinks, FIFOs, etc.) at the marker paths
// are rejected via the Mode().IsRegular() check, matching the type-
// allowlist discipline from the GitHub path.
//
// Returns the decider's verdict ((subpath, notice, err)). Callers
// derive rank from the notice (nil → rank-1, non-nil → rank-2).
func probeAndResolveCloneRoot(
	cloneDir string,
	markers config.MarkerSet,
	decider func(found, markers config.MarkerSet) (string, *config.DeprecationNotice, error),
) (subpath string, notice *config.DeprecationNotice, err error) {
	var found config.MarkerSet

	// Rank-1 probe: <cloneDir>/<Rank1Dir>/<Rank1File> must exist as a
	// regular file.
	if markers.Rank1Dir != "" && markers.Rank1File != "" {
		rank1Path := filepath.Join(cloneDir, markers.Rank1Dir, markers.Rank1File)
		if info, statErr := os.Lstat(rank1Path); statErr == nil && info.Mode().IsRegular() {
			found.Rank1Dir = markers.Rank1Dir
			found.Rank1File = markers.Rank1File
		}
	}

	// Rank-2 probe: <cloneDir>/<Rank2Path> must exist as a regular file.
	if markers.Rank2Path != "" {
		rank2Path := filepath.Join(cloneDir, markers.Rank2Path)
		if info, statErr := os.Lstat(rank2Path); statErr == nil && info.Mode().IsRegular() {
			found.Rank2Path = markers.Rank2Path
		}
	}

	return decider(found, markers)
}

// copySubtree walks src recursively and copies every regular file
// and directory into dest, preserving the relative layout. Non-
// regular entries (symlinks, FIFOs, devices) are silently skipped
// so a hostile repo can't smuggle links that escape the staging
// dir on promotion. Skipping a `.git` directory is the one hard-
// coded policy: we never want the clone's metadata in the snapshot.
func copySubtree(src, dest string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == src {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("relativize %s: %w", path, err)
		}
		// Skip the repo's own `.git` metadata so the snapshot stays
		// clean regardless of clone implementation.
		relSlash := filepath.ToSlash(rel)
		first := strings.SplitN(relSlash, "/", 2)[0]
		if first == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		for _, seg := range strings.Split(relSlash, "/") {
			if err := validateRelName(seg); err != nil {
				return err
			}
		}

		target, err := fallbackSafeJoin(dest, filepath.FromSlash(relSlash))
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}

		switch {
		case info.IsDir():
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case info.Mode().IsRegular():
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			if err := copyRegularFile(path, target, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			// Symlinks, FIFOs, devices — skip. Mirrors ExtractSubpath's
			// positive type allowlist.
		}
		return nil
	})
}

// copyRegularFile copies a regular file using io.Copy with an
// explicit byte cap. The cap matches the tarball extractor's
// decompression-bomb defense so the fallback path has equivalent
// resource-exhaustion bounds.
func copyRegularFile(srcPath, destPath string, mode os.FileMode) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", srcPath, err)
	}
	defer in.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	// Same 500 MB per-snapshot cap as ExtractSubpath. A single file
	// that exceeds this is rejected; filepath.WalkDir's visitor does
	// not enforce cumulative budget but individual files are bounded.
	const maxBytes = 500 * 1024 * 1024
	_, copyErr := io.Copy(out, io.LimitReader(in, maxBytes+1))
	closeErr := out.Close()
	if copyErr != nil {
		return fmt.Errorf("write %s: %w", destPath, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", destPath, closeErr)
	}
	// Reject oversize files that tripped the LimitReader.
	info, err := os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("stat %s: %w", destPath, err)
	}
	if info.Size() > maxBytes {
		_ = os.Remove(destPath)
		return fmt.Errorf("%s exceeds max size (%d bytes)", destPath, maxBytes)
	}
	return nil
}

// validateRelName enforces the same filename safety rules as
// internal/github.validateEntryName, applied to each path segment.
func validateRelName(seg string) error {
	if seg == "" {
		return errors.New("empty path segment")
	}
	if strings.ContainsRune(seg, '\x00') {
		return fmt.Errorf("path segment %q contains NUL byte", seg)
	}
	if strings.ContainsRune(seg, '\\') {
		return fmt.Errorf("path segment %q contains backslash", seg)
	}
	if seg == ".." {
		return fmt.Errorf("path segment %q traverses parent", seg)
	}
	return nil
}

// fallbackSafeJoin mirrors internal/github.safeJoin: the cleaned
// destination must live within dest so a crafted entry name that
// survives validateRelName can't resolve outside the snapshot.
func fallbackSafeJoin(dest, rel string) (string, error) {
	cleanedDest, err := filepath.Abs(dest)
	if err != nil {
		return "", fmt.Errorf("safeJoin: abs(dest): %w", err)
	}
	target := filepath.Clean(filepath.Join(cleanedDest, rel))
	cleanedDest = filepath.Clean(cleanedDest)
	if target != cleanedDest && !strings.HasPrefix(target, cleanedDest+string(os.PathSeparator)) {
		return "", fmt.Errorf("entry %q escapes dest %q", rel, cleanedDest)
	}
	return target, nil
}
