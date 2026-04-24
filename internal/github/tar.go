package github

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/testfault"
)

// MaxDecompressedBytes caps the cumulative decompressed bytes
// extractor will write to disk. 500 MB matches the design's
// decompression-bomb defense; legitimate brain-repo subpaths are
// orders of magnitude smaller. The cap is per-extraction; a hostile
// gzipped tarball that would expand past this is rejected before any
// further bytes are written.
const MaxDecompressedBytes int64 = 500 * 1024 * 1024

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
	subpath = strings.TrimPrefix(subpath, "/")
	subpath = strings.TrimSuffix(subpath, "/")

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
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeDir {
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
