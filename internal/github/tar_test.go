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
