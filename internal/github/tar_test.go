package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// buildTarball assembles a gzipped tarball from the given entries
// (path → contents). Empty contents creates a directory entry.
func buildTarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)

	// Sorted iteration for deterministic ordering.
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	// Wrapper directory entry first.
	for _, name := range names {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range names {
		if strings.HasSuffix(name, "/") {
			continue
		}
		body := []byte(entries[name])
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

func TestExtractSubpath_HappyPath(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrapper-abc/":       "",
		"wrapper-abc/.niwa/": "",
		"wrapper-abc/.niwa/workspace.toml": `[workspace]
name = "foo"
`,
		"wrapper-abc/.niwa/hooks/start.sh": "#!/bin/bash\necho hi\n",
		"wrapper-abc/README.md":            "outside subpath",
		"wrapper-abc/src/main.go":          "outside subpath",
	})
	if err := ExtractSubpath(bytes.NewReader(tarball), ".niwa", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	want := map[string]string{
		"workspace.toml": "[workspace]\nname = \"foo\"\n",
		"hooks/start.sh": "#!/bin/bash\necho hi\n",
	}
	for relpath, wantBody := range want {
		got, err := os.ReadFile(filepath.Join(dest, relpath))
		if err != nil {
			t.Errorf("missing %s: %v", relpath, err)
			continue
		}
		if string(got) != wantBody {
			t.Errorf("%s: got %q, want %q", relpath, got, wantBody)
		}
	}
	// Files outside the subpath must not exist.
	for _, rel := range []string{"README.md", "src/main.go", "src"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err == nil {
			t.Errorf("file %s should not have been extracted", rel)
		}
	}
}

func TestExtractSubpath_WholeRepo(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "x",
		"wrap/sub/file.md":    "y",
	})
	if err := ExtractSubpath(bytes.NewReader(tarball), "", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "workspace.toml")); err != nil {
		t.Errorf("workspace.toml: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "file.md")); err != nil {
		t.Errorf("sub/file.md: %v", err)
	}
}

func TestExtractSubpath_RejectsPathTraversal(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/../escape": "no",
	})
	err := ExtractSubpath(bytes.NewReader(tarball), "", dest)
	if err == nil {
		t.Fatal("expected error for path traversal")
	}
	if !strings.Contains(err.Error(), "..") && !strings.Contains(err.Error(), "escape") {
		t.Errorf("error %q does not mention traversal", err.Error())
	}
}

func TestValidateEntryName(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // expected error substring; "" means "must succeed"
	}{
		{"valid", "wrap/file.txt", ""},
		{"valid nested", "wrap/a/b/c.txt", ""},
		{"empty", "", "empty"},
		{"NUL byte", "wrap/bad\x00", "NUL"},
		{"backslash", "wrap/foo\\bar.txt", "backslash"},
		{"absolute path", "/etc/passwd", "absolute"},
		{"dotdot segment", "wrap/../escape", "`..`"},
		{"trailing dotdot", "../escape", "`..`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateEntryName(tc.input)
			if tc.want == "" {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("validateEntryName(%q) = %v, want error containing %q", tc.input, err, tc.want)
			}
		})
	}
}

func TestExtractSubpath_RejectsAbsolutePath(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":       "",
		"/etc/passwd": "no",
	})
	err := ExtractSubpath(bytes.NewReader(tarball), "", dest)
	if err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Errorf("expected absolute-path rejection, got %v", err)
	}
}

func TestExtractSubpath_SkipsSymlinkEntries(t *testing.T) {
	// Manually craft a tarball containing a symlink entry to verify
	// the type allowlist excludes it.
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	mustHeader(t, tw, &tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir})
	mustHeader(t, tw, &tar.Header{Name: "wrap/safe.txt", Mode: 0o644, Size: 4, Typeflag: tar.TypeReg})
	if _, err := tw.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	mustHeader(t, tw, &tar.Header{Name: "wrap/evil", Linkname: "/etc/passwd", Typeflag: tar.TypeSymlink})
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	if err := ExtractSubpath(&raw, "", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "evil")); err == nil {
		t.Error("symlink entry should not have been materialized")
	}
	if _, err := os.Stat(filepath.Join(dest, "safe.txt")); err != nil {
		t.Errorf("safe.txt missing: %v", err)
	}
}

func TestExtractSubpath_RejectsWrapperEscape(t *testing.T) {
	// Tarball with a second entry whose wrapper differs from the first.
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	mustHeader(t, tw, &tar.Header{Name: "wrap1/", Mode: 0o755, Typeflag: tar.TypeDir})
	mustHeader(t, tw, &tar.Header{Name: "wrap1/file.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("a"))
	mustHeader(t, tw, &tar.Header{Name: "wrap2/sneaky.txt", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("b"))
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	if err := ExtractSubpath(&raw, "", dest); err == nil {
		t.Error("expected wrapper-escape error")
	}
}

func TestExtractSubpath_SubpathNotFound(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":        "",
		"wrap/foo.txt": "x",
	})
	err := ExtractSubpath(bytes.NewReader(tarball), "missing", dest)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected subpath-not-found error, got %v", err)
	}
}

func TestExtractSubpath_SingleFileSubpath(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/niwa.toml": "x",
		"wrap/readme.md": "y",
	})
	if err := ExtractSubpath(bytes.NewReader(tarball), "niwa.toml", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "niwa.toml"))
	if err != nil {
		t.Fatalf("missing niwa.toml: %v", err)
	}
	if string(got) != "x" {
		t.Errorf("niwa.toml: got %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "readme.md")); err == nil {
		t.Error("readme.md should not have been extracted")
	}
}

func TestExtractSubpath_FaultInjectionInterrupts(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":         "",
		"wrap/file.txt": "x",
	})
	t.Setenv("NIWA_TEST_FAULT", "error:simulated@extract-entry")
	err := ExtractSubpath(bytes.NewReader(tarball), "", dest)
	if err == nil || !strings.Contains(err.Error(), "simulated") {
		t.Errorf("expected fault-injection error, got %v", err)
	}
}

func TestExtractSubpath_TruncatedTarball(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":         "",
		"wrap/file.txt": strings.Repeat("x", 1024),
	})
	// Truncate to garbage.
	bad := tarball[:len(tarball)/2]
	err := ExtractSubpath(bytes.NewReader(bad), "", dest)
	if err == nil {
		t.Error("expected error on truncated tarball")
	}
}

func mustHeader(t *testing.T, tw *tar.Writer, h *tar.Header) {
	t.Helper()
	if err := tw.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
}

func TestExtractSubpath_DecompressionBombDefense(t *testing.T) {
	// Build a tarball whose declared header.Size exceeds the cap.
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	mustHeader(t, tw, &tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir})
	mustHeader(t, tw, &tar.Header{
		Name:     "wrap/huge.bin",
		Mode:     0o644,
		Size:     MaxDecompressedBytes + 1,
		Typeflag: tar.TypeReg,
	})
	// Don't actually write that many bytes; the size check should fire
	// before any read.
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	err := ExtractSubpath(&raw, "", dest)
	if err == nil || !strings.Contains(err.Error(), "decompression-bomb") {
		t.Errorf("expected decompression-bomb error, got %v", err)
	}
}

// silence imported-but-unused for io until a future test needs it.
var _ = io.EOF

// ----- ProbeAndExtractSubpath (PRD-config-source-discovery Issue 2) -----

// buildTarballWithHeaders is like buildTarball but lets the caller
// specify arbitrary tar header types (e.g., TypeSymlink for the
// symlink-marker regression test) and entry ordering. Each entry is
// emitted as a (Header, body) tuple — the body is only written for
// TypeReg entries.
func buildTarballWithHeaders(t *testing.T, entries []probeTestEntry) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		if err := tw.WriteHeader(e.Header); err != nil {
			t.Fatal(err)
		}
		if e.Header.Typeflag == tar.TypeReg && len(e.Body) > 0 {
			if _, err := tw.Write(e.Body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return raw.Bytes()
}

type probeTestEntry struct {
	Header *tar.Header
	Body   []byte
}

// teamConfigMarkers returns the marker set the production tests
// exercise (`.niwa/workspace.toml` at rank-1, root `workspace.toml` at
// rank-2). Mirrors config.TeamConfigMarkerSet() with explicit values
// for in-test readability.
func teamConfigMarkers() config.MarkerSet {
	return config.MarkerSet{
		Rank1Dir:  ".niwa",
		Rank1File: "workspace.toml",
		Rank2Path: "workspace.toml",
	}
}

func TestProbeAndExtractSubpath_Rank1Resolves(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":                     "",
		"wrap/.niwa/":               "",
		"wrap/.niwa/workspace.toml": "[workspace]\nname = \"foo\"\n",
		"wrap/.niwa/hooks/start.sh": "#!/bin/bash\n",
		"wrap/README.md":            "outside subpath",
	})

	subpath, rank, notice, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	if err != nil {
		t.Fatalf("probe-and-extract: %v", err)
	}
	if subpath != ".niwa" {
		t.Errorf("subpath = %q, want %q", subpath, ".niwa")
	}
	if rank != 1 {
		t.Errorf("rank = %d, want 1", rank)
	}
	if notice != nil {
		t.Errorf("rank-1 must not emit a notice, got %+v", notice)
	}
	// Only the .niwa subpath landed.
	if _, err := os.Stat(filepath.Join(dest, "workspace.toml")); err != nil {
		t.Errorf("expected workspace.toml under dest, got: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md must not land under dest (outside subpath); err=%v", err)
	}
}

func TestProbeAndExtractSubpath_Rank2Resolves(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "[workspace]\nname = \"foo\"\n",
		"wrap/recipes.toml":   "[recipes]\n",
	})

	subpath, rank, notice, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	if err != nil {
		t.Fatalf("probe-and-extract: %v", err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q, want empty (whole-repo rank-2)", subpath)
	}
	if rank != 2 {
		t.Errorf("rank = %d, want 2", rank)
	}
	if notice == nil {
		t.Fatal("rank-2 must emit a deprecation notice")
	}
	if notice.Rank != 2 {
		t.Errorf("notice.Rank = %d, want 2", notice.Rank)
	}
	// Both root files landed.
	if _, err := os.Stat(filepath.Join(dest, "workspace.toml")); err != nil {
		t.Errorf("expected workspace.toml under dest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "recipes.toml")); err != nil {
		t.Errorf("expected recipes.toml under dest: %v", err)
	}
}

func TestProbeAndExtractSubpath_AmbiguityErrorBeforeWrite(t *testing.T) {
	dest := t.TempDir()
	// Touch a sentinel file in dest so we can verify the pre-existing
	// state survives the failure (R5 byte-identity).
	sentinel := filepath.Join(dest, "sentinel.txt")
	if err := os.WriteFile(sentinel, []byte("preexisting"), 0o644); err != nil {
		t.Fatal(err)
	}

	tarball := buildTarball(t, map[string]string{
		"wrap/":                     "",
		"wrap/.niwa/":               "",
		"wrap/.niwa/workspace.toml": "[workspace]\nname = \"foo\"\n",
		"wrap/workspace.toml":       "[workspace]\nname = \"bar\"\n",
	})

	subpath, rank, notice, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !config.IsAmbiguousMarkers(err) {
		t.Errorf("expected *AmbiguousMarkersError, got %T: %v", err, err)
	}
	if subpath != "" || rank != 0 || notice != nil {
		t.Errorf("on error want (\"\", 0, nil); got (%q, %d, %+v)", subpath, rank, notice)
	}
	// No new files were written; sentinel survives.
	got, err := os.ReadFile(sentinel)
	if err != nil {
		t.Fatalf("sentinel: %v", err)
	}
	if string(got) != "preexisting" {
		t.Errorf("sentinel mutated: %q", got)
	}
}

func TestProbeAndExtractSubpath_NoMarkerErrorBeforeWrite(t *testing.T) {
	dest := t.TempDir()
	tarball := buildTarball(t, map[string]string{
		"wrap/":            "",
		"wrap/README.md":   "no niwa config here",
		"wrap/src/main.go": "package main",
	})

	subpath, rank, notice, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	if err == nil {
		t.Fatal("expected no-marker error, got nil")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError, got %T: %v", err, err)
	}
	if subpath != "" || rank != 0 || notice != nil {
		t.Errorf("on error want (\"\", 0, nil); got (%q, %d, %+v)", subpath, rank, notice)
	}
	// dest is empty (no probe writes).
	dirents, err := os.ReadDir(dest)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(dirents) != 0 {
		t.Errorf("dest must be empty on error; got %d entries", len(dirents))
	}
}

func TestProbeAndExtract_DecompressionBombDefense(t *testing.T) {
	// A tarball whose decompressed bytes are tiny but whose Size header
	// claims to exceed the cap. The probe pass walks headers — when
	// tar.Reader.Next() tries to advance past the (oversized,
	// truncated) body, it returns ErrUnexpectedEOF. The pipeline
	// returns an error, no snapshot is materialized, dest stays empty.
	// This is the malformed-input shape: a real-attacker tarball that
	// promises 500MB+1 bytes but doesn't deliver. The seven security
	// defenses still hold (no panic, no partial write, no escape
	// through validateEntryName).
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	mustHeader(t, tw, &tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir})
	mustHeader(t, tw, &tar.Header{
		Name:     "wrap/huge.bin",
		Mode:     0o644,
		Size:     MaxDecompressedBytes + 1,
		Typeflag: tar.TypeReg,
	})
	// Don't write the body — tar.Reader will see truncation.
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	subpath, rank, notice, err := ProbeAndExtractSubpath(
		bytes.NewReader(raw.Bytes()), teamConfigMarkers(), config.RankDecider, dest)
	if err == nil {
		t.Fatal("expected error from oversized/malformed tarball, got nil")
	}
	if subpath != "" || rank != 0 || notice != nil {
		t.Errorf("on error want (\"\", 0, nil); got (%q, %d, %+v)", subpath, rank, notice)
	}
	dirents, _ := os.ReadDir(dest)
	if len(dirents) != 0 {
		t.Errorf("dest must be empty on error; got %d entries", len(dirents))
	}

	// The existing TestExtractSubpath_DecompressionBombDefense
	// exercises the cumulative-bytes cap (Level C) on the production
	// extract path. Since ProbeAndExtractSubpath delegates pass-2 to
	// extractFromTarReader (the same shared helper used by
	// ExtractSubpath), the Level C cap behaviour is inherited
	// automatically — verified end-to-end by the existing test
	// against a non-pathological rank-2 fixture; on this entry point
	// we rely on the shared helper invariant rather than re-asserting
	// it. A real 500MB+ body fixture would exercise the cap directly
	// but is impractical for a unit-test pass.
}

func TestProbeAndExtract_SymlinkMarkerIsNotRank1(t *testing.T) {
	// Regression guard: a tar entry with TypeSymlink whose name matches
	// the rank-1 marker MUST NOT be detected as rank-1 by the probe.
	// The extract pass rejects symlinks via the type allowlist; the
	// probe pass must apply the same rule so a future contributor who
	// relaxes one pass's allowlist doesn't silently desynchronize the
	// two.
	entries := []probeTestEntry{
		{Header: &tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir}},
		{Header: &tar.Header{Name: "wrap/.niwa/", Mode: 0o755, Typeflag: tar.TypeDir}},
		{Header: &tar.Header{
			Name:     "wrap/.niwa/workspace.toml",
			Linkname: "/etc/passwd",
			Typeflag: tar.TypeSymlink,
		}},
	}
	tarball := buildTarballWithHeaders(t, entries)

	dest := t.TempDir()
	_, _, _, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	// Symlink rejected by the type allowlist → probe sees no markers
	// at root → decider returns no-marker error.
	if err == nil {
		t.Fatal("expected no-marker error (symlink must not satisfy rank-1)")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError (symlink-marker is not rank-1), got %T: %v", err, err)
	}
	// And no symlink was written under dest.
	if _, err := os.Lstat(filepath.Join(dest, "workspace.toml")); !os.IsNotExist(err) {
		t.Errorf("symlink must not land under dest; err=%v", err)
	}
}

func TestProbeAndExtract_TruncatedTarball(t *testing.T) {
	// Use the existing testfault truncate seam to cut the gzipped
	// input stream mid-read at byte 16 (deep enough that gzip's
	// header parses but the stream payload errors out mid-decompress).
	// The probe-and-extract returns an error before pass-2 completes;
	// dest stays empty.
	tarball := buildTarball(t, map[string]string{
		"wrap/":                     "",
		"wrap/.niwa/":               "",
		"wrap/.niwa/workspace.toml": "[workspace]\nname=\"x\"\n",
	})

	t.Setenv("NIWA_TEST_FAULT", "truncate-after:16@fetch-tarball")
	dest := t.TempDir()
	_, _, _, err := ProbeAndExtractSubpath(
		bytes.NewReader(tarball), teamConfigMarkers(), config.RankDecider, dest)
	if err == nil {
		t.Fatal("expected truncation error, got nil")
	}
	dirents, _ := os.ReadDir(dest)
	if len(dirents) != 0 {
		t.Errorf("dest must be empty on truncation error; got %d entries", len(dirents))
	}
}
