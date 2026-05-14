package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
)

// readTarReaderFromTarball decompresses tarball and returns the
// tar.Reader for direct ProbeMarkers calls.
func readTarReaderFromTarball(t *testing.T, tarball []byte) *tar.Reader {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(gz); err != nil {
		t.Fatal(err)
	}
	return tar.NewReader(bytes.NewReader(buf.Bytes()))
}

func TestProbeMarkers_Rank1(t *testing.T) {
	tarball := buildTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = team",
		"wrap/README.md":           "ignored",
	})
	tr := readTarReaderFromTarball(t, tarball)

	found, err := ProbeMarkers(tr, config.TeamConfigMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if !found.HasRank1() {
		t.Errorf("expected rank-1 markers found, got %+v", found)
	}
	if found.HasRank2() {
		t.Errorf("did not expect rank-2 marker found at root, got %+v", found)
	}
}

func TestProbeMarkers_Rank2(t *testing.T) {
	tarball := buildTarball(t, map[string]string{
		"wrap/":               "",
		"wrap/workspace.toml": "name = legacy",
	})
	tr := readTarReaderFromTarball(t, tarball)

	found, err := ProbeMarkers(tr, config.TeamConfigMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if found.HasRank1() {
		t.Errorf("did not expect rank-1, got %+v", found)
	}
	if !found.HasRank2() {
		t.Errorf("expected rank-2 marker found, got %+v", found)
	}
}

func TestProbeMarkers_Both(t *testing.T) {
	tarball := buildTarball(t, map[string]string{
		"wrap/":                    "",
		"wrap/.niwa/":              "",
		"wrap/.niwa/workspace.toml": "name = rank1",
		"wrap/workspace.toml":      "name = rank2",
	})
	tr := readTarReaderFromTarball(t, tarball)

	found, err := ProbeMarkers(tr, config.TeamConfigMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if !found.HasRank1() || !found.HasRank2() {
		t.Errorf("expected both rank-1 and rank-2 found, got %+v", found)
	}
}

func TestProbeMarkers_Neither(t *testing.T) {
	tarball := buildTarball(t, map[string]string{
		"wrap/":          "",
		"wrap/README.md": "no niwa config",
	})
	tr := readTarReaderFromTarball(t, tarball)

	found, err := ProbeMarkers(tr, config.TeamConfigMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if found.HasRank1() || found.HasRank2() {
		t.Errorf("expected no markers found, got %+v", found)
	}
}

func TestProbeMarkers_SymlinkMarkerRejected(t *testing.T) {
	// Build a tarball where workspace.toml at the root is a symlink,
	// not a regular file. ProbeMarkers shares the extract pass's
	// type-allowlist defense, so the symlink entry must not be
	// counted as a rank-2 marker.
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "wrap/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	// symlink-typed entry with marker-matching name.
	if err := tw.WriteHeader(&tar.Header{
		Name:     "wrap/workspace.toml",
		Typeflag: tar.TypeSymlink,
		Linkname: "elsewhere.toml",
	}); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()

	tr := readTarReaderFromTarball(t, raw.Bytes())
	found, err := ProbeMarkers(tr, config.TeamConfigMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if found.HasRank2() {
		t.Errorf("symlink-typed workspace.toml must NOT count as rank-2; got %+v", found)
	}
}

func TestProbeMarkers_OverlayMarkerSet(t *testing.T) {
	tarball := buildTarball(t, map[string]string{
		"wrap/":                             "",
		"wrap/.niwa/":                       "",
		"wrap/.niwa/workspace-overlay.toml": "name = personal",
	})
	tr := readTarReaderFromTarball(t, tarball)

	found, err := ProbeMarkers(tr, config.OverlayMarkerSet())
	if err != nil {
		t.Fatalf("ProbeMarkers: %v", err)
	}
	if !found.HasRank1() {
		t.Errorf("expected rank-1 overlay markers found, got %+v", found)
	}
}
