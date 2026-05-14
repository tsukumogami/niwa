package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscover(t *testing.T) {
	// Create a temp workspace structure:
	// tmpdir/
	//   .niwa/
	//     workspace.toml
	//   instance/
	//     public/
	//       somerepo/
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(niwaDir, "workspace.toml")
	if err := os.WriteFile(configFile, []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	deepDir := filepath.Join(tmpDir, "instance", "public", "somerepo")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Discover from deep nested directory should find the config.
	path, dir, err := Discover(deepDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantPath := configFile
	if path != wantPath {
		t.Errorf("configPath = %q, want %q", path, wantPath)
	}

	wantDir := niwaDir
	if dir != wantDir {
		t.Errorf("configDir = %q, want %q", dir, wantDir)
	}
}

func TestDiscoverFromConfigDir(t *testing.T) {
	tmpDir := t.TempDir()

	niwaDir := filepath.Join(tmpDir, ".niwa")
	if err := os.MkdirAll(niwaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(niwaDir, "workspace.toml"), []byte("[workspace]\nname = \"test\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Discover from the workspace root itself.
	path, _, err := Discover(tmpDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != filepath.Join(niwaDir, "workspace.toml") {
		t.Errorf("unexpected path: %s", path)
	}
}

func TestDiscoverNotFound(t *testing.T) {
	tmpDir := t.TempDir()

	_, _, err := Discover(tmpDir)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ----- MarkerSet, RankDecider, and factories (PRD-config-source-discovery) -----

func TestTeamConfigMarkerSet_Values(t *testing.T) {
	m := TeamConfigMarkerSet()
	if m.Rank1Dir != ".niwa" {
		t.Errorf("Rank1Dir = %q, want %q", m.Rank1Dir, ".niwa")
	}
	if m.Rank1File != "workspace.toml" {
		t.Errorf("Rank1File = %q, want %q", m.Rank1File, "workspace.toml")
	}
	if m.Rank2Path != "workspace.toml" {
		t.Errorf("Rank2Path = %q, want %q", m.Rank2Path, "workspace.toml")
	}
}

func TestOverlayMarkerSet_Values(t *testing.T) {
	m := OverlayMarkerSet()
	if m.Rank1Dir != ".niwa" {
		t.Errorf("Rank1Dir = %q, want %q", m.Rank1Dir, ".niwa")
	}
	if m.Rank1File != "workspace-overlay.toml" {
		t.Errorf("Rank1File = %q, want %q", m.Rank1File, "workspace-overlay.toml")
	}
	if m.Rank2Path != "workspace-overlay.toml" {
		t.Errorf("Rank2Path = %q, want %q", m.Rank2Path, "workspace-overlay.toml")
	}
}

func TestMarkerSet_HasRank1(t *testing.T) {
	cases := []struct {
		name  string
		found MarkerSet
		want  bool
	}{
		{"both Rank1Dir and Rank1File set", MarkerSet{Rank1Dir: ".niwa", Rank1File: "workspace.toml"}, true},
		{"only Rank1Dir set (empty .niwa/ — PRD R6)", MarkerSet{Rank1Dir: ".niwa"}, false},
		{"only Rank1File set", MarkerSet{Rank1File: "workspace.toml"}, false},
		{"both empty", MarkerSet{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.found.HasRank1(); got != tc.want {
				t.Errorf("HasRank1() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarkerSet_HasRank2(t *testing.T) {
	cases := []struct {
		name  string
		found MarkerSet
		want  bool
	}{
		{"Rank2Path set", MarkerSet{Rank2Path: "workspace.toml"}, true},
		{"Rank2Path empty", MarkerSet{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.found.HasRank2(); got != tc.want {
				t.Errorf("HasRank2() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRankDecider_Rank1Only(t *testing.T) {
	markers := TeamConfigMarkerSet()
	found := MarkerSet{Rank1Dir: ".niwa", Rank1File: "workspace.toml"}

	subpath, notice, err := RankDecider(found, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notice != nil {
		t.Errorf("rank-1 must not emit a deprecation notice, got %+v", notice)
	}
	if subpath != ".niwa" {
		t.Errorf("subpath = %q, want %q", subpath, ".niwa")
	}
}

func TestRankDecider_Rank2Only_Accepted(t *testing.T) {
	markers := TeamConfigMarkerSet()
	found := MarkerSet{Rank2Path: "workspace.toml"}

	subpath, notice, err := RankDecider(found, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q, want empty (whole-repo)", subpath)
	}
	if notice == nil {
		t.Fatal("rank-2 must emit a deprecation notice")
	}
	if notice.Rank != 2 {
		t.Errorf("notice.Rank = %d, want 2", notice.Rank)
	}
	if notice.Markers != markers {
		t.Errorf("notice.Markers = %+v, want %+v", notice.Markers, markers)
	}
}

func TestRankDecider_BothRanks_Rank1Wins(t *testing.T) {
	markers := TeamConfigMarkerSet()
	found := MarkerSet{
		Rank1Dir:  ".niwa",
		Rank1File: "workspace.toml",
		Rank2Path: "workspace.toml",
	}

	subpath, notice, err := RankDecider(found, markers)
	if err != nil {
		t.Fatalf("rank-1 + rank-2 must resolve to rank-1 wins, got error: %v", err)
	}
	if subpath != ".niwa" {
		t.Errorf("subpath = %q, want %q", subpath, ".niwa")
	}
	if notice != nil {
		t.Errorf("rank-1-wins must not emit a deprecation notice, got %+v", notice)
	}
}

func TestRankDecider_NoMarkers(t *testing.T) {
	markers := TeamConfigMarkerSet()
	subpath, notice, err := RankDecider(MarkerSet{}, markers)
	if err == nil {
		t.Fatal("no-marker case must return an error")
	}
	if !IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError, got %T: %v", err, err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q on error, want empty", subpath)
	}
	if notice != nil {
		t.Errorf("notice = %+v on error, want nil", notice)
	}
}

func TestRankDecider_EmptyNiwaDirectory(t *testing.T) {
	// Probe observed `.niwa/` but no `workspace.toml` inside it; plus a root
	// `workspace.toml` is also present. Per PRD R6, the empty `.niwa/` does
	// NOT count as a rank-1 match — discovery resolves to rank-2.
	markers := TeamConfigMarkerSet()
	found := MarkerSet{
		Rank1Dir:  ".niwa",
		Rank1File: "", // probe saw the directory but not the file inside it
		Rank2Path: "workspace.toml",
	}

	subpath, notice, err := RankDecider(found, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q, want empty (rank-2 resolved)", subpath)
	}
	if notice == nil {
		t.Fatal("rank-2 resolution must emit a deprecation notice")
	}
	if notice.Rank != 2 {
		t.Errorf("notice.Rank = %d, want 2", notice.Rank)
	}
}

func TestRankDecider_Rank2Only_NotAccepted(t *testing.T) {
	// Forward-compat path: the rank-2 deprecated branch in RankDecider is
	// gated by `rankTwoAccepted = true`. The PRD-config-source-discovery R15
	// follow-up release flips that to false and deletes the BEGIN/END
	// bracketed branch. This test exercises the flipped path now so that
	// release is a one-function mechanical edit.
	prev := rankTwoAcceptedTestHook
	off := false
	rankTwoAcceptedTestHook = &off
	t.Cleanup(func() { rankTwoAcceptedTestHook = prev })

	markers := TeamConfigMarkerSet()
	found := MarkerSet{Rank2Path: "workspace.toml"}

	subpath, notice, err := RankDecider(found, markers)
	if err == nil {
		t.Fatal("rank-2 with the guard off must return a no-marker error")
	}
	if !IsNoMarker(err) {
		t.Errorf("expected *NoMarkerError, got %T: %v", err, err)
	}
	if subpath != "" {
		t.Errorf("subpath = %q on error, want empty", subpath)
	}
	if notice != nil {
		t.Errorf("notice = %+v on error, want nil", notice)
	}
}

func TestRankDecider_OverlayMarkers(t *testing.T) {
	// Overlay marker set drives the same decision logic with different
	// filenames. Rank-1 produces the overlay subpath; rank-2 produces a
	// notice with the overlay's markers attached.
	markers := OverlayMarkerSet()

	rank1Found := MarkerSet{Rank1Dir: ".niwa", Rank1File: "workspace-overlay.toml"}
	subpath, notice, err := RankDecider(rank1Found, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subpath != ".niwa" {
		t.Errorf("overlay rank-1 subpath = %q, want %q", subpath, ".niwa")
	}
	if notice != nil {
		t.Error("overlay rank-1 must not emit a notice")
	}

	rank2Found := MarkerSet{Rank2Path: "workspace-overlay.toml"}
	subpath, notice, err = RankDecider(rank2Found, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if subpath != "" {
		t.Errorf("overlay rank-2 subpath = %q, want empty", subpath)
	}
	if notice == nil || notice.Rank != 2 {
		t.Errorf("overlay rank-2 must emit a rank-2 notice, got %+v", notice)
	}
}

func TestErrorTypes_Diagnostic(t *testing.T) {
	// Smoke test: error messages contain the slug-style identifiers that
	// the CLI / disclosure layer will surface to users.
	markers := TeamConfigMarkerSet()
	nm := &NoMarkerError{Markers: markers}
	if msg := nm.Error(); msg == "" {
		t.Error("NoMarkerError.Error() must produce a non-empty message")
	}

	am := &AmbiguousMarkersError{Markers: markers}
	if msg := am.Error(); msg == "" {
		t.Error("AmbiguousMarkersError.Error() must produce a non-empty message")
	}

	if !IsNoMarker(nm) {
		t.Error("IsNoMarker must return true for *NoMarkerError")
	}
	if !IsAmbiguousMarkers(am) {
		t.Error("IsAmbiguousMarkers must return true for *AmbiguousMarkersError")
	}
}
