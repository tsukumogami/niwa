package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/testfault"
)

// MaxDecompressedBytes caps the cumulative decompressed bytes
// extractor will write to disk. 500 MB matches the design's
// decompression-bomb defense; legitimate brain-repo subpaths are
// orders of magnitude smaller. The cap is per-extraction; a hostile
// gzipped tarball that would expand past this is rejected before any
// further bytes are written.
const MaxDecompressedBytes int64 = 500 * 1024 * 1024

// commonCaseInitialBufferSize pre-allocates the probe-and-extract
// buffer for the common case (a config-bearing subpath that
// decompresses to a few MB). Larger inputs grow the buffer
// geometrically up to the MaxDecompressedBytes cap. Pre-allocation
// reduces doubling overhead for the typical workload.
const commonCaseInitialBufferSize = 1 << 20 // 1 MB

// ExtractSubpath streams a gzipped GitHub tarball, filters entries to
// those under <wrapper>/<subpath>/ (or just <wrapper>/<subpath> for a
// single-file subpath per PRD R4), and writes them under dest with
// directory structure preserved. Subpath must be slash-separated and
// non-absolute; an empty subpath means "extract everything under the
// wrapper" (whole-repo case).
//
// The function enforces all seven security defenses from the design's
// Security Considerations section:
//
//  1. Positive type allowlist (TypeReg + TypeDir only).
//  2. Wrapper anchoring (first entry establishes the wrapper name;
//     every subsequent entry must begin with <wrapper>/).
//  3. Subpath filter (after wrapper-strip, must begin with subpath).
//  4. Path-containment check (cleaned destination must live under dest).
//  5. Filename validation (no NUL, no `..`, no leading `/`, no other
//     separator characters).
//  6. Decompression-bomb defense (cumulative byte cap via io.LimitReader
//     around the gzip stream; per-entry copy bounded by header.Size
//     against the remaining cumulative budget).
//  7. Failure leaves no partial state at the canonical path (the
//     caller stages into a fresh dir and the snapshot swap promotes
//     it only on success).
//
// Calls testfault.Maybe("extract-entry") once per entry processed so
// fault-injection scenarios can interrupt mid-extraction.
func ExtractSubpath(r io.Reader, subpath, dest string) error {
	if dest == "" {
		return errors.New("extractSubpath: dest is empty")
	}

	bytesBudget := MaxDecompressedBytes
	if truncate := testfault.TruncateAfter("fetch-tarball"); truncate >= 0 {
		// Honor the test-only stream cap when present.
		r = &cappedReader{src: r, remaining: truncate}
	}

	gz, err := gzip.NewReader(io.LimitReader(r, bytesBudget+1))
	if err != nil {
		return fmt.Errorf("extractSubpath: gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	return extractFromTarReader(tr, subpath, dest, bytesBudget)
}

// extractFromTarReader is the shared tar-walking loop used by both
// ExtractSubpath (which feeds it a gzip-backed reader) and
// ProbeAndExtractSubpath's pass-2 (which feeds it a bytes.NewReader
// wrapped in tar.NewReader from a pre-buffered decompressed stream).
//
// Walking the loop directly against a tar.Reader lets the caller choose
// the stream source while keeping all seven security defenses on a
// single code path. The bytesBudget argument is the remaining
// decompressed-byte budget for cumulative writes.
func extractFromTarReader(tr *tar.Reader, subpath, dest string, bytesBudget int64) error {
	subpath = strings.TrimPrefix(subpath, "/")
	subpath = strings.TrimSuffix(subpath, "/")

	var wrapper string
	var written int64

	for {
		if err := testfault.Maybe("extract-entry"); err != nil {
			return fmt.Errorf("extractSubpath: %w", err)
		}
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("extractSubpath: tar reader: %w", err)
		}

		// Defense 5: filename validation.
		if err := validateEntryName(hdr.Name); err != nil {
			return fmt.Errorf("extractSubpath: %w", err)
		}

		// Defense 1: positive type allowlist.
		if !isAllowedEntryType(hdr.Typeflag) {
			continue
		}

		// Defense 2: wrapper anchoring.
		if wrapper == "" {
			// First entry establishes the wrapper. GitHub's tarball
			// convention puts a single root directory at the top.
			parts := strings.SplitN(strings.TrimSuffix(hdr.Name, "/"), "/", 2)
			if parts[0] == "" {
				return fmt.Errorf("extractSubpath: first tar entry %q has empty wrapper", hdr.Name)
			}
			wrapper = parts[0]
		}
		if !strings.HasPrefix(hdr.Name, wrapper+"/") && hdr.Name != wrapper && hdr.Name != wrapper+"/" {
			return fmt.Errorf("extractSubpath: entry %q does not begin with wrapper %q", hdr.Name, wrapper)
		}

		// Strip wrapper prefix.
		rel := strings.TrimPrefix(hdr.Name, wrapper+"/")
		if rel == "" {
			// The wrapper directory itself; nothing to materialize.
			continue
		}

		// Defense 3: subpath filter.
		if subpath != "" {
			if rel != subpath && !strings.HasPrefix(rel, subpath+"/") {
				continue
			}
			if rel == subpath {
				rel = filepath.Base(subpath)
			} else {
				rel = strings.TrimPrefix(rel, subpath+"/")
			}
		}
		if rel == "" {
			continue
		}

		// Defense 4: path-containment check.
		target, err := safeJoin(dest, rel)
		if err != nil {
			return fmt.Errorf("extractSubpath: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("extractSubpath: mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("extractSubpath: mkdir parent of %s: %w", target, err)
			}

			// Defense 6: per-entry write bounded by remaining budget.
			remaining := bytesBudget - written
			if hdr.Size > remaining {
				return fmt.Errorf("extractSubpath: entry %s would exceed decompression-bomb cap (%d bytes)",
					hdr.Name, MaxDecompressedBytes)
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
			if err != nil {
				return fmt.Errorf("extractSubpath: create %s: %w", target, err)
			}
			n, err := io.CopyN(f, tr, hdr.Size)
			closeErr := f.Close()
			if err != nil && err != io.EOF {
				return fmt.Errorf("extractSubpath: write %s: %w", target, err)
			}
			if closeErr != nil {
				return fmt.Errorf("extractSubpath: close %s: %w", target, closeErr)
			}
			written += n
			if written > bytesBudget {
				return fmt.Errorf("extractSubpath: cumulative bytes exceeded decompression-bomb cap (%d bytes)",
					MaxDecompressedBytes)
			}
		}
	}

	if subpath != "" && written == 0 {
		// We never matched anything under the subpath. Caller may
		// surface this as a "subpath not found" diagnostic per PRD
		// AC-S5; we report it as an error to keep the swap pipeline
		// from promoting an empty snapshot.
		return fmt.Errorf("extractSubpath: subpath %q not found in tarball", subpath)
	}

	return nil
}

// ProbeAndExtractSubpath buffers the decompressed tarball, scans tar
// headers to record which markers exist at the source-root level,
// calls decider to resolve the rank, then re-iterates the buffered
// bytes to extract entries under the resolved subpath into dest.
//
// Three caps run in series and all share the MaxDecompressedBytes
// budget: Level A wraps the compressed input (unchanged from the
// pre-existing defense at the LimitReader on r); Level B wraps the
// decompressed output during buffer fill; Level C runs inside the
// pass-2 extract via the cumulative-bytes check in extractFromTarReader.
// If any of the three fires, the function returns the existing
// cap-exceeded diagnostic before any disk write completes.
//
// The function returns (resolvedSubpath, rank, notice, err). rank is
// 1 when the decider chose rank-1 (notice == nil), 2 when the decider
// chose rank-2 (notice != nil), and 0 on error. On any error path the
// function returns before pass-2 begins, leaving dest untouched.
func ProbeAndExtractSubpath(
	r io.Reader,
	markers config.MarkerSet,
	decider func(found, markers config.MarkerSet) (string, *config.DeprecationNotice, error),
	dest string,
) (resolvedSubpath string, rank int, notice *config.DeprecationNotice, err error) {
	if dest == "" {
		return "", 0, nil, errors.New("probeAndExtractSubpath: dest is empty")
	}
	if decider == nil {
		return "", 0, nil, errors.New("probeAndExtractSubpath: decider is nil")
	}

	bytesBudget := MaxDecompressedBytes
	if truncate := testfault.TruncateAfter("fetch-tarball"); truncate >= 0 {
		// Honor the test-only stream cap when present (Level A
		// fault-injection seam, same as ExtractSubpath).
		r = &cappedReader{src: r, remaining: truncate}
	}

	// Level A: cap on the compressed input. Unchanged from
	// ExtractSubpath's existing defense — keeps pathological compression
	// ratios from blowing past the budget before gzip even starts.
	gz, gzErr := gzip.NewReader(io.LimitReader(r, bytesBudget+1))
	if gzErr != nil {
		return "", 0, nil, fmt.Errorf("probeAndExtractSubpath: gzip reader: %w", gzErr)
	}
	defer gz.Close()

	// Level B (NEW): cap on the decompressed output during buffer
	// fill. Bounds in-memory growth regardless of how well-formed the
	// gzip stream is. Pre-allocate for the common config-bearing case
	// (~1 MB); the buffer grows geometrically beyond that up to the
	// cap.
	buf := bytes.NewBuffer(make([]byte, 0, commonCaseInitialBufferSize))
	if _, fillErr := io.Copy(buf, io.LimitReader(gz, bytesBudget+1)); fillErr != nil {
		return "", 0, nil, fmt.Errorf("probeAndExtractSubpath: read tarball: %w", fillErr)
	}
	if int64(buf.Len()) > bytesBudget {
		return "", 0, nil, fmt.Errorf("probeAndExtractSubpath: tarball exceeds decompression-bomb cap (%d bytes)",
			MaxDecompressedBytes)
	}

	// Pass 1: probe. Header-only scan; no disk writes.
	probeReader := tar.NewReader(bytes.NewReader(buf.Bytes()))
	found, probeErr := probeMarkersFromHeaders(probeReader, markers)
	if probeErr != nil {
		return "", 0, nil, fmt.Errorf("probeAndExtractSubpath: probe: %w", probeErr)
	}

	// Resolve rank via the injected decider.
	resolvedSubpath, notice, err = decider(found, markers)
	if err != nil {
		return "", 0, nil, err
	}

	// Derive rank from the decider's return values. rank-1 wins when
	// no notice is emitted; rank-2 emits a notice. Anything else means
	// the decider returned an error path we already handled above.
	if notice != nil {
		rank = 2
	} else {
		rank = 1
	}

	// Pass 2: extract. Fresh tar.Reader over the same buffered bytes.
	// extractFromTarReader's cumulative-bytes check (Level C) runs
	// unchanged.
	extractReader := tar.NewReader(bytes.NewReader(buf.Bytes()))
	if err := extractFromTarReader(extractReader, resolvedSubpath, dest, bytesBudget); err != nil {
		return "", 0, nil, err
	}

	return resolvedSubpath, rank, notice, nil
}

// probeMarkersFromHeaders walks the tar reader's headers and records
// which markers from the requested MarkerSet were observed at the
// source-root level. It writes nothing to disk and applies the same
// wrapper-anchoring, filename-validation, and type-allowlist checks
// the extract pass uses, so a marker entry that the extractor would
// reject (e.g., a symlink with the right name) does NOT get reported
// as "found."
//
// The returned MarkerSet has its Rank1Dir/Rank1File/Rank2Path fields
// set only for markers actually observed at the source root after
// wrapper-strip. Empty fields mean "not observed."
func probeMarkersFromHeaders(tr *tar.Reader, markers config.MarkerSet) (config.MarkerSet, error) {
	var found config.MarkerSet
	var wrapper string

	rank1Path := ""
	if markers.Rank1Dir != "" && markers.Rank1File != "" {
		rank1Path = markers.Rank1Dir + "/" + markers.Rank1File
	}

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return config.MarkerSet{}, fmt.Errorf("probeMarkersFromHeaders: tar reader: %w", err)
		}

		// Same defenses as the extract pass — see extractFromTarReader.
		if err := validateEntryName(hdr.Name); err != nil {
			return config.MarkerSet{}, fmt.Errorf("probeMarkersFromHeaders: %w", err)
		}
		if !isAllowedEntryType(hdr.Typeflag) {
			continue
		}
		if wrapper == "" {
			parts := strings.SplitN(strings.TrimSuffix(hdr.Name, "/"), "/", 2)
			if parts[0] == "" {
				return config.MarkerSet{}, fmt.Errorf("probeMarkersFromHeaders: first tar entry %q has empty wrapper", hdr.Name)
			}
			wrapper = parts[0]
		}
		if !strings.HasPrefix(hdr.Name, wrapper+"/") && hdr.Name != wrapper && hdr.Name != wrapper+"/" {
			return config.MarkerSet{}, fmt.Errorf("probeMarkersFromHeaders: entry %q does not begin with wrapper %q", hdr.Name, wrapper)
		}
		rel := strings.TrimPrefix(hdr.Name, wrapper+"/")

		// Only file (TypeReg) entries can satisfy a marker. Directories
		// don't carry the marker content even if their name matches —
		// this is the "empty .niwa/ is not rank-1" rule from PRD R6
		// expressed at the probe layer.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		if rank1Path != "" && rel == rank1Path {
			found.Rank1Dir = markers.Rank1Dir
			found.Rank1File = markers.Rank1File
		}
		if markers.Rank2Path != "" && rel == markers.Rank2Path {
			found.Rank2Path = markers.Rank2Path
		}
	}

	return found, nil
}

// isAllowedEntryType reports whether the tar header's type flag is on
// the positive allowlist (defense 1). Factored so the probe pass and
// the extract pass cannot drift in their type acceptance — divergence
// here would silently allow a marker (e.g., a symlinked
// workspace.toml) to be detected by the probe even though the extract
// pass would skip it.
func isAllowedEntryType(typeflag byte) bool {
	return typeflag == tar.TypeReg || typeflag == tar.TypeDir
}

// validateEntryName enforces filename safety rules: no NUL, no `..`
// segments, no leading `/`, no embedded separators beyond the expected
// `/`. POSIX-style separators only (no Windows backslashes).
func validateEntryName(name string) error {
	if name == "" {
		return errors.New("entry name is empty")
	}
	if strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("entry name %q contains NUL byte", name)
	}
	if strings.HasPrefix(name, "/") {
		return fmt.Errorf("entry name %q is absolute", name)
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("entry name %q contains backslash", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." {
			return fmt.Errorf("entry name %q contains `..` segment", name)
		}
	}
	return nil
}

// safeJoin returns filepath.Join(dest, rel) only when the cleaned
// result is contained within dest. Defends against path-traversal
// attacks via crafted entries that survive validateEntryName but
// resolve outside dest after Clean (e.g., on case-insensitive
// filesystems).
func safeJoin(dest, rel string) (string, error) {
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

// cappedReader is the test-only seam that stops returning bytes after
// `remaining` go through it. Used to simulate truncated tarballs in
// fault-injection scenarios.
type cappedReader struct {
	src       io.Reader
	remaining int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.src.Read(p)
	c.remaining -= int64(n)
	return n, err
}
