package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/github"
)

// fakeInspectFetcher implements sourceInspectFetcher with a
// pre-built tarball.
type fakeInspectFetcher struct {
	tarball []byte
	status  int
	err     error
}

func (f *fakeInspectFetcher) FetchTarball(ctx context.Context, owner, repo, ref, etag string) (io.ReadCloser, string, int, *github.RenameRedirect, error) {
	if f.err != nil {
		return nil, "", 0, nil, f.err
	}
	status := f.status
	if status == 0 {
		status = 200
	}
	return io.NopCloser(bytes.NewReader(f.tarball)), "", status, nil, nil
}

// makeTarball wraps the tar.go test helper format used elsewhere:
// keys ending in "/" are dir entries; other keys are regular files.
func makeTarball(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	for name := range entries {
		if !strings.HasSuffix(name, "/") {
			continue
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range entries {
		if strings.HasSuffix(name, "/") {
			continue
		}
		data := []byte(body)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
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

// runInspect installs a fake fetcher and exit handler, then runs
// the source inspect command with the given args. Returns stdout,
// captured exit code (0 if no exit), and any RunE error.
func runInspect(t *testing.T, tarball []byte, args ...string) (string, int, error) {
	t.Helper()
	origFetcher := newInspectFetcher
	origExit := inspectExit
	t.Cleanup(func() {
		newInspectFetcher = origFetcher
		inspectExit = origExit
		// Reset --json so flag state doesn't leak between tests.
		sourceInspectCmd.Flags().Set("json", "false")
	})
	newInspectFetcher = func() sourceInspectFetcher {
		return &fakeInspectFetcher{tarball: tarball, status: 200}
	}
	exitCode := 0
	inspectExit = func(c int) { exitCode = c }

	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"source", "inspect"}, args...))
	err := rootCmd.Execute()
	return buf.String(), exitCode, err
}

func TestSourceInspect_SlugParseFailure(t *testing.T) {
	_, _, err := runInspect(t, nil, "not-a-valid-slug")
	if err == nil {
		t.Fatal("expected error on invalid slug, got nil")
	}
}

func TestSourceInspect_Rank1Text(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = team",
	})
	out, exit, err := runInspect(t, tarball, "org/repo")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit != 0 {
		t.Errorf("expected exit code 0 on rank-1, got %d", exit)
	}
	for _, want := range []string{"Source: org/repo", "rank:    1", ".niwa"} {
		if !strings.Contains(out, want) {
			t.Errorf("text output missing %q\noutput:\n%s", want, out)
		}
	}
}

func TestSourceInspect_Rank1JSON(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = team",
	})
	out, exit, err := runInspect(t, tarball, "org/repo", "--json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit != 0 {
		t.Errorf("expected exit 0, got %d", exit)
	}
	var result inspectResult
	if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if result.SchemaVersion != 1 {
		t.Errorf("schema_version = %d, want 1", result.SchemaVersion)
	}
	if result.Resolved == nil {
		t.Fatalf("Resolved is nil; result = %+v", result)
	}
	if result.Resolved.Rank != 1 {
		t.Errorf("rank = %d, want 1", result.Resolved.Rank)
	}
	if result.Resolved.Deprecated {
		t.Error("rank-1 should not be deprecated")
	}
	if result.Resolved.Subpath != ".niwa" {
		t.Errorf("subpath = %q, want .niwa", result.Resolved.Subpath)
	}
}

func TestSourceInspect_Rank2JSON(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = legacy",
	})
	out, exit, err := runInspect(t, tarball, "org/repo", "--json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit != 0 {
		t.Errorf("expected exit 0, got %d", exit)
	}
	var result inspectResult
	if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if result.Resolved == nil {
		t.Fatalf("Resolved is nil; result = %+v", result)
	}
	if result.Resolved.Rank != 2 {
		t.Errorf("rank = %d, want 2", result.Resolved.Rank)
	}
	if !result.Resolved.Deprecated {
		t.Error("rank-2 must be deprecated=true")
	}
	if result.Resolved.MigrationHint == "" {
		t.Error("rank-2 must include non-empty migration_hint")
	}
}

func TestSourceInspect_AmbiguityJSON(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = rank1",
		"wrap/workspace.toml":      "name = rank2",
	})
	out, exit, err := runInspect(t, tarball, "org/repo", "--json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit == 0 {
		t.Error("expected non-zero exit on ambiguity")
	}
	var result inspectResult
	if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if result.Error == nil {
		t.Fatal("expected error object, got nil")
	}
	if result.Error.Code != "ambiguous" {
		t.Errorf("error.code = %q, want %q", result.Error.Code, "ambiguous")
	}
	if result.Resolved != nil {
		t.Error("ambiguous result must not have a resolved object")
	}
}

func TestSourceInspect_NoMarkerJSON(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/README.md": "no niwa config",
	})
	out, exit, err := runInspect(t, tarball, "org/repo", "--json")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit == 0 {
		t.Error("expected non-zero exit on no-marker")
	}
	var result inspectResult
	if jerr := json.Unmarshal([]byte(out), &result); jerr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jerr, out)
	}
	if result.Error == nil || result.Error.Code != "no_marker" {
		t.Errorf("error.code = %+v, want no_marker", result.Error)
	}
	if result.Resolved != nil {
		t.Error("no-marker result must not have a resolved object")
	}
}

func TestSourceInspect_SchemaVersionPinned(t *testing.T) {
	tarball := makeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = legacy",
	})
	out, _, _ := runInspect(t, tarball, "org/repo", "--json")
	if !strings.Contains(out, `"schema_version": 1`) {
		t.Errorf("schema_version: 1 not present in JSON output:\n%s", out)
	}
}
