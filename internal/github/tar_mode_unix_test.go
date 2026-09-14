//go:build unix

package github

import (
	"archive/tar"
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// These two tests own the assertions that a umask could otherwise make
// true for free, and they own them by setting the umask themselves.
//
// The problem they exist to solve: os.OpenFile's mode argument is masked
// by the process umask, so an extractor that gets the mode entirely
// wrong can still produce a file whose bits happen to satisfy a loose
// assertion on the runner that ran it. TestExtractSubpath_PreservesExecBit
// stays umask-agnostic and only asks whether the exec bit is set, which
// is safe because a umask can only clear bits and no realistic umask
// clears owner-exec. Everything sharper than that lives here.
//
// withUmask sets the process umask for the duration of one test and
// restores whatever was there before.
//
// READ THIS BEFORE ADDING t.Parallel ANYWHERE IN PACKAGE github. The
// umask is process-global, not per-goroutine, so this helper is only
// sound while test functions in this package run one at a time. Under a
// parallel test the window between these two calls overlaps whatever
// else is running: files those tests create get this umask instead of
// their own, and -- worse for the tests below -- the mode they assert
// stops being attributable to the extractor. They would not go red.
// They would go quietly meaningless, still passing, still looking like
// coverage. That is the same defect class this whole change exists to
// fix, so it is not left to a comment: TestNoParallelTestsInPackage
// below fails if anyone adds the call.
//
// If you need parallelism here, move these tests to their own package
// rather than weakening what they assert.
func withUmask(t *testing.T, mask int) {
	t.Helper()
	previous := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(previous) })
}

// TestNoParallelTestsInPackage enforces the precondition withUmask
// depends on, by parsing this package's own test sources and failing on
// any real t.Parallel call.
//
// A comment asking people not to do something is a check that cannot
// fail. This can. It parses rather than greps so that the word appearing
// in prose -- as it does in withUmask's comment, several times -- is not
// a false positive: go/parser discards comments, and only a genuine call
// expression is matched.
//
// Test binaries run with the working directory set to the package
// source directory, so the files are readable from here.
func TestNoParallelTestsInPackage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	fset := token.NewFileSet()
	checked := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		checked++

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Parallel" || len(call.Args) != 0 {
				return true
			}
			t.Errorf("%s: t.Parallel() at %s -- the umask tests in this file "+
				"assert a process-global value and stop meaning anything once "+
				"tests here run concurrently. Read withUmask's comment: move "+
				"those tests to their own package rather than leaving them "+
				"passing and unable to fail.", name, fset.Position(sel.Sel.Pos()))
			return true
		})
	}

	// A guard that silently checked nothing would be the same mistake it
	// exists to prevent.
	if checked == 0 {
		t.Fatal("found no _test.go files to check; this guard is not running")
	}
}

// tarballWithModes builds a gzipped tarball whose single wrapper holds
// one regular file per entry in modes, each named by its key.
func tarballWithModes(t *testing.T, modes map[string]int64) []byte {
	t.Helper()
	entries := []probeTestEntry{
		{Header: &tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir}},
		{Header: &tar.Header{Name: "wrap/cfg/", Mode: 0o755, Typeflag: tar.TypeDir}},
	}
	body := []byte("#!/bin/sh\ntrue\n")
	for name, mode := range modes {
		entries = append(entries, probeTestEntry{
			Header: &tar.Header{
				Name:     "wrap/cfg/" + name,
				Mode:     mode,
				Size:     int64(len(body)),
				Typeflag: tar.TypeReg,
			},
			Body: body,
		})
	}
	return buildTarballWithHeaders(t, entries)
}

// TestExtractSubpath_ChmodSurvivesRestrictiveUmask pins the chmod that
// follows the write. Without it the extractor still passes the right
// mode to os.OpenFile, and on a runner with a permissive umask the file
// still comes out executable -- so deleting the chmod is invisible to
// every assertion that only asks "is the exec bit set?".
//
// Under umask 077 the two diverge: OpenFile alone yields 0700, and only
// the chmod gets back to the 0755 the archive asked for. Asserting the
// whole mode is legitimate here precisely because the test controls the
// umask that would otherwise make the number unpredictable.
func TestExtractSubpath_ChmodSurvivesRestrictiveUmask(t *testing.T) {
	withUmask(t, 0o077)

	raw := tarballWithModes(t, map[string]int64{"hook.sh": 0o755})
	dest := t.TempDir()
	if err := ExtractSubpath(bytes.NewReader(raw), "cfg", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	info, err := os.Stat(filepath.Join(dest, "hook.sh"))
	if err != nil {
		t.Fatalf("stat hook: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("hook.sh mode = %04o, want 0755 (umask 077 was in force; "+
			"0700 means the chmod after the write is missing)", got)
	}
}

// TestExtractSubpath_NarrowsHostileModes pins what filePerm refuses to
// reproduce. It runs under umask 0 so that nothing but filePerm can
// clear a bit: under the more usual 022 the group- and other-write
// cases would pass whether or not the mask exists, which is the trap
// this file was written to avoid.
//
// Every mode below is one a tarball can carry but git cannot record, so
// none of them can arrive from a legitimate GitHub source. That is the
// point -- the extractor reads an archive fetched over the network, and
// the header is as attacker-controlled as the entry name.
func TestExtractSubpath_NarrowsHostileModes(t *testing.T) {
	withUmask(t, 0)

	cases := []struct {
		name string
		mode int64
		want os.FileMode
		why  string
	}{
		{"world-writable-exec", 0o777, 0o755, "group and other write must be cleared"},
		{"world-writable-plain", 0o666, 0o644, "group and other write must be cleared"},
		{"setuid", 0o4755, 0o755, "setuid must not survive extraction"},
		{"setgid", 0o2755, 0o755, "setgid must not survive extraction"},
		{"sticky", 0o1777, 0o755, "the sticky bit must not survive extraction"},
		{"honoured-exec", 0o755, 0o755, "a plain executable must come through unchanged"},
		{"honoured-plain", 0o644, 0o644, "a plain file must come through unchanged"},
		{"owner-private", 0o600, 0o600, "a mode narrower than the default is left narrow"},
		{"absent", 0, 0o644, "an omitted mode falls back to the non-executable default"},
		{"nonsense", 0o022, 0o644, "a mode the owner cannot read falls back to the default"},
	}

	modes := make(map[string]int64, len(cases))
	for _, c := range cases {
		modes[c.name] = c.mode
	}

	raw := tarballWithModes(t, modes)
	dest := t.TempDir()
	if err := ExtractSubpath(bytes.NewReader(raw), "cfg", dest); err != nil {
		t.Fatalf("extract: %v", err)
	}

	for _, c := range cases {
		info, err := os.Stat(filepath.Join(dest, c.name))
		if err != nil {
			t.Errorf("%s: stat: %v", c.name, err)
			continue
		}
		// Mode(), not Mode().Perm(): Perm() masks setuid and setgid
		// away, which would make the two cases that matter most here
		// pass no matter what the extractor did.
		if got := info.Mode(); got != c.want {
			t.Errorf("header mode %04o extracted as %v, want %v -- %s",
				c.mode, got, c.want, c.why)
		}
	}
}
