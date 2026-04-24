package workspace

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"
)

// FetchMechanism enumerates the strategies the snapshot writer may
// have used to materialize the snapshot. Recorded in the provenance
// marker for later inspection.
const (
	FetchMechanismGitHubTarball = "github-tarball"
	FetchMechanismGitClone      = "git-clone-fallback"
)

// Provenance is the on-disk source-identity surface for a snapshot.
// Written into the snapshot directory as ProvenanceFile (TOML); read
// by `niwa status`, drift detection, `niwa reset`, and the
// plaintext-secrets guardrail.
//
// The struct field set is the public contract for the marker; new
// fields added here change the on-disk schema.
type Provenance struct {
	SourceURL      string    `toml:"source_url"`
	Host           string    `toml:"host"`
	Owner          string    `toml:"owner"`
	Repo           string    `toml:"repo"`
	Subpath        string    `toml:"subpath"`
	Ref            string    `toml:"ref"`
	ResolvedCommit string    `toml:"resolved_commit"`
	FetchedAt      time.Time `toml:"fetched_at"`
	FetchMechanism string    `toml:"fetch_mechanism"`
}

// WriteProvenance writes the marker as snapshotDir/ProvenanceFile.
// snapshotDir must already exist.
func WriteProvenance(snapshotDir string, p Provenance) error {
	if p.SourceURL == "" {
		return errors.New("provenance: source_url is required")
	}
	if p.Owner == "" || p.Repo == "" {
		return errors.New("provenance: owner and repo are required")
	}
	if p.ResolvedCommit == "" {
		return errors.New("provenance: resolved_commit is required")
	}
	if p.FetchedAt.IsZero() {
		return errors.New("provenance: fetched_at is required")
	}
	if p.FetchMechanism == "" {
		return errors.New("provenance: fetch_mechanism is required")
	}

	path := filepath.Join(snapshotDir, ProvenanceFile)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("provenance: open %s: %w", path, err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(p); err != nil {
		return fmt.Errorf("provenance: encode: %w", err)
	}
	return nil
}

// ReadProvenance reads snapshotDir/ProvenanceFile. Returns
// (Provenance{}, fs.ErrNotExist) wrapped in a context-bearing error
// when the file is missing — callers who treat absence as
// user-authored config (PRD R30, R31) should check with errors.Is.
func ReadProvenance(snapshotDir string) (Provenance, error) {
	path := filepath.Join(snapshotDir, ProvenanceFile)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Provenance{}, fmt.Errorf("provenance: %s: %w", path, err)
		}
		return Provenance{}, fmt.Errorf("provenance: read %s: %w", path, err)
	}
	var p Provenance
	if err := toml.Unmarshal(data, &p); err != nil {
		return Provenance{}, fmt.Errorf("provenance: parse %s: %w", path, err)
	}
	return p, nil
}
