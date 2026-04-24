package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProvenance_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := Provenance{
		SourceURL:      "tsukumogami/niwa:.niwa@main",
		Host:           "github.com",
		Owner:          "tsukumogami",
		Repo:           "niwa",
		Subpath:        ".niwa",
		Ref:            "main",
		ResolvedCommit: "abcdef0123456789",
		FetchedAt:      time.Date(2026, 4, 23, 10, 15, 0, 0, time.UTC),
		FetchMechanism: FetchMechanismGitHubTarball,
	}
	if err := WriteProvenance(dir, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !got.FetchedAt.Equal(want.FetchedAt) {
		t.Errorf("FetchedAt mismatch: got %v, want %v", got.FetchedAt, want.FetchedAt)
	}
	got.FetchedAt = want.FetchedAt
	if got != want {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", got, want)
	}
}

func TestProvenance_HumanReadable(t *testing.T) {
	dir := t.TempDir()
	p := Provenance{
		SourceURL:      "org/repo:.niwa@v1",
		Host:           "github.com",
		Owner:          "org",
		Repo:           "repo",
		Subpath:        ".niwa",
		Ref:            "v1",
		ResolvedCommit: "9f8e7d6c",
		FetchedAt:      time.Date(2026, 4, 23, 12, 0, 0, 0, time.UTC),
		FetchMechanism: FetchMechanismGitHubTarball,
	}
	if err := WriteProvenance(dir, p); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ProvenanceFile))
	if err != nil {
		t.Fatalf("read raw: %v", err)
	}
	out := string(data)
	for _, want := range []string{"source_url", "host", "owner", "repo", "subpath", "ref", "resolved_commit", "fetched_at", "fetch_mechanism", "github-tarball"} {
		if !strings.Contains(out, want) {
			t.Errorf("marker missing %q in output:\n%s", want, out)
		}
	}
}

func TestProvenance_RejectsMissingRequiredFields(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		p    Provenance
	}{
		{"missing source_url", Provenance{Owner: "o", Repo: "r", ResolvedCommit: "x", FetchedAt: time.Now(), FetchMechanism: FetchMechanismGitHubTarball}},
		{"missing owner", Provenance{SourceURL: "s", Repo: "r", ResolvedCommit: "x", FetchedAt: time.Now(), FetchMechanism: FetchMechanismGitHubTarball}},
		{"missing repo", Provenance{SourceURL: "s", Owner: "o", ResolvedCommit: "x", FetchedAt: time.Now(), FetchMechanism: FetchMechanismGitHubTarball}},
		{"missing resolved_commit", Provenance{SourceURL: "s", Owner: "o", Repo: "r", FetchedAt: time.Now(), FetchMechanism: FetchMechanismGitHubTarball}},
		{"missing fetched_at", Provenance{SourceURL: "s", Owner: "o", Repo: "r", ResolvedCommit: "x", FetchMechanism: FetchMechanismGitHubTarball}},
		{"missing fetch_mechanism", Provenance{SourceURL: "s", Owner: "o", Repo: "r", ResolvedCommit: "x", FetchedAt: time.Now()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteProvenance(dir, tc.p); err == nil {
				t.Errorf("expected error for %s", tc.name)
			}
		})
	}
}

func TestProvenance_MissingFileWrapsErrNotExist(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadProvenance(dir)
	if err == nil {
		t.Fatal("expected error for missing marker")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("error %v should wrap fs.ErrNotExist", err)
	}
}

func TestProvenance_MalformedFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProvenanceFile), []byte("this is not toml = oops!@#$"), 0o644); err != nil {
		t.Fatalf("write malformed: %v", err)
	}
	if _, err := ReadProvenance(dir); err == nil {
		t.Error("expected parse error")
	}
}
