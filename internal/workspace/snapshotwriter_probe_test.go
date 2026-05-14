package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/source"
)

// AC-D1: GitHub fresh materialization with rank-1 source layout extracts
// .niwa/ contents into the snapshot root and reports rank=1.
func TestMaterializeFromSource_GitHub_Rank1(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = team",
		"wrap/.niwa/CLAUDE.md":     "shared",
		"wrap/README.md":           "ignored at rank-1",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	rank, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource: %v", err)
	}
	if rank != 1 {
		t.Errorf("rank = %d, want 1", rank)
	}

	got, err := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	if string(got) != "name = team" {
		t.Errorf("snapshot content = %q, want %q", got, "name = team")
	}
	if _, err := os.Stat(filepath.Join(dir, "README.md")); err == nil {
		t.Error("rank-1 extraction should not bring in repo-root files like README.md")
	}

	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if prov.Subpath != ".niwa" {
		t.Errorf("provenance subpath = %q, want %q", prov.Subpath, ".niwa")
	}
}

// AC-D2: GitHub fresh materialization with rank-2 layout (legacy
// whole-repo) extracts the whole tree and reports rank=2.
func TestMaterializeFromSource_GitHub_Rank2(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = legacy",
		"wrap/CLAUDE.md":      "shared",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	rank, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource: %v", err)
	}
	if rank != 2 {
		t.Errorf("rank = %d, want 2", rank)
	}

	got, err := os.ReadFile(filepath.Join(dir, "workspace.toml"))
	if err != nil {
		t.Fatalf("read workspace.toml: %v", err)
	}
	if string(got) != "name = legacy" {
		t.Errorf("snapshot content = %q, want %q", got, "name = legacy")
	}

	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if prov.Subpath != "" {
		t.Errorf("rank-2 provenance subpath = %q, want empty", prov.Subpath)
	}
}

// AC-D3: Ambiguity — both rank-1 and rank-2 markers present in the
// same source returns AmbiguousMarkersError and writes no provenance.
func TestMaterializeFromSource_GitHub_Ambiguous(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = rank1",
		"wrap/workspace.toml":      "name = rank2",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	rank, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatalf("expected ambiguity error, got rank=%d err=nil", rank)
	}
	if !config.IsAmbiguousMarkers(err) {
		t.Errorf("error not classified as ambiguous: %v", err)
	}
}

// AC-D4: No marker found — returns NoMarkerError and writes nothing.
func TestMaterializeFromSource_GitHub_NoMarker(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/README.md": "no niwa config",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	_, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected NoMarkerError, got nil")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("error not classified as no-marker: %v", err)
	}
}

// AC-D5: Empty .niwa/ at the source root must NOT be treated as a
// valid rank-1 layout. Per PRD R6 / AC-D8, only the file presence
// (.niwa/workspace.toml) counts as a rank-1 marker.
func TestMaterializeFromSource_GitHub_EmptyNiwaDirIsNotRank1(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/.niwa/":    "",
		"wrap/README.md": "no niwa config",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	_, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected NoMarkerError when .niwa/ is empty, got nil")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("error not classified as no-marker: %v", err)
	}
}

// AC-D6: When src.Subpath is non-empty the probe is skipped and the
// explicit subpath flows through verbatim.
func TestMaterializeFromSource_GitHub_ExplicitSubpathBypassesProbe(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                "",
		"wrap/custom/":         "",
		"wrap/custom/file.txt": "explicit",
		"wrap/README.md":       "ignored",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo", Subpath: "custom"}
	rank, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource: %v", err)
	}
	if rank != 1 {
		t.Errorf("explicit-subpath rank = %d, want 1", rank)
	}

	got, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatalf("read file.txt: %v", err)
	}
	if string(got) != "explicit" {
		t.Errorf("file.txt = %q, want %q", got, "explicit")
	}

	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if prov.Subpath != "custom" {
		t.Errorf("provenance subpath = %q, want %q", prov.Subpath, "custom")
	}
}

// AC-D7: Network error on a fresh materialization aborts cleanly —
// no staging artifacts left behind, no snapshot written.
func TestMaterializeFromSource_GitHub_NetworkErrorAborts(t *testing.T) {
	fetcher := &fakeFetcher{fetchErr: errors.New("connection refused")}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"}
	_, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected error on network failure, got nil")
	}
	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("snapshot dir should not exist after fetch failure")
	}
	if _, statErr := os.Stat(dir + ".next"); statErr == nil {
		t.Error("staging dir should be cleaned up after fetch failure")
	}
}

// AC-D8: Provenance records the resolved subpath, not the input
// src.Subpath. Verified for the rank-1 case where src.Subpath="" and
// the resolved subpath is ".niwa".
func TestMaterializeFromSource_GitHub_ProvenanceRecordsResolvedSubpath(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = team",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), ".niwa")
	src := source.Source{Owner: "org", Repo: "repo"} // Subpath intentionally empty
	if _, err := MaterializeFromSource(context.Background(), src, "org/repo", dir, config.TeamConfigMarkerSet(), fetcher, nil); err != nil {
		t.Fatalf("MaterializeFromSource: %v", err)
	}

	prov, err := ReadProvenance(dir)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	if prov.Subpath != ".niwa" {
		t.Errorf("provenance subpath = %q, want %q (must be resolved, not input)", prov.Subpath, ".niwa")
	}
}

// AC-V1: Overlay fresh materialization with rank-1 layout
// (.niwa/workspace-overlay.toml) extracts .niwa/ contents and reports
// rank=1 via the overlay marker set.
func TestMaterializeFromSource_Overlay_Rank1(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                             "",
		"wrap/.niwa/":                       "",
		"wrap/.niwa/workspace-overlay.toml": "name = personal",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles"}
	rank, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource overlay: %v", err)
	}
	if rank != 1 {
		t.Errorf("overlay rank = %d, want 1", rank)
	}

	got, err := os.ReadFile(filepath.Join(dir, "workspace-overlay.toml"))
	if err != nil {
		t.Fatalf("read workspace-overlay.toml: %v", err)
	}
	if string(got) != "name = personal" {
		t.Errorf("overlay content = %q, want %q", got, "name = personal")
	}
}

// AC-V2: Overlay fresh materialization with rank-2 layout
// (workspace-overlay.toml at source root) extracts whole tree and
// reports rank=2.
func TestMaterializeFromSource_Overlay_Rank2(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                       "",
		"wrap/workspace-overlay.toml": "name = legacy",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles"}
	rank, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource overlay: %v", err)
	}
	if rank != 2 {
		t.Errorf("overlay rank = %d, want 2", rank)
	}
}

// AC-V3: Overlay ambiguity — both rank-1 and rank-2 overlay markers
// present returns AmbiguousMarkersError.
func TestMaterializeFromSource_Overlay_Ambiguous(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                             "",
		"wrap/.niwa/":                       "",
		"wrap/.niwa/workspace-overlay.toml": "name = rank1",
		"wrap/workspace-overlay.toml":       "name = rank2",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles"}
	_, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected ambiguity error for overlay, got nil")
	}
	if !config.IsAmbiguousMarkers(err) {
		t.Errorf("error not classified as ambiguous: %v", err)
	}
}

// AC-V4: Overlay no-marker — neither overlay marker found yields
// NoMarkerError. The caller is responsible for converting this into
// a silent-skip per R35/R11.
func TestMaterializeFromSource_Overlay_NoMarker(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "team-config, not overlay",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles"}
	_, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected NoMarkerError for overlay without overlay marker, got nil")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("error not classified as no-marker: %v", err)
	}
}

// AC-V5: Team-config marker (.niwa/workspace.toml) at an overlay
// source does NOT satisfy the overlay marker set — overlay
// materialization fails with no-marker.
func TestMaterializeFromSource_Overlay_TeamConfigMarkerDoesNotSatisfyOverlay(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "team-config marker",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles"}
	_, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err == nil {
		t.Fatal("expected NoMarkerError: team-config marker should not satisfy overlay marker set")
	}
	if !config.IsNoMarker(err) {
		t.Errorf("error not classified as no-marker: %v", err)
	}
}

// AC-V6: Overlay sources with explicit subpaths skip the probe and
// flow verbatim, same as team-config sources.
func TestMaterializeFromSource_Overlay_ExplicitSubpathBypassesProbe(t *testing.T) {
	tarball := makeFakeTarball(t, map[string]string{
		"wrap/":             "",
		"wrap/p/":           "",
		"wrap/p/keep.txt":   "explicit",
		"wrap/elsewhere.md": "ignored",
	})
	fetcher := &fakeFetcher{tarball: tarball, commitOID: "abc"}

	dir := filepath.Join(t.TempDir(), "overlay")
	src := source.Source{Owner: "user", Repo: "dotfiles", Subpath: "p"}
	rank, err := MaterializeFromSource(context.Background(), src, "user/dotfiles", dir, config.OverlayMarkerSet(), fetcher, nil)
	if err != nil {
		t.Fatalf("MaterializeFromSource overlay with subpath: %v", err)
	}
	if rank != 1 {
		t.Errorf("explicit-subpath overlay rank = %d, want 1", rank)
	}

	got, err := os.ReadFile(filepath.Join(dir, "keep.txt"))
	if err != nil {
		t.Fatalf("read keep.txt: %v", err)
	}
	if string(got) != "explicit" {
		t.Errorf("file.txt = %q, want %q", got, "explicit")
	}
}
