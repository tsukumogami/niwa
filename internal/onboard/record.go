// The R20 record: the one piece of state the wizard persists across
// runs, at ~/.config/niwa/ (sibling to config.toml and the personal-
// overlay clone dir -- config.GlobalConfigPath's directory). It names
// the most recently minted client-secret id for a given (kind, project)
// pair. Its only consumer is best-effort revocation (R20): a
// superseding re-run revokes the prior id, and a mint-then-verify
// success followed by a store failure revokes the just-minted id. It
// participates in zero resume/skip decisions (Decision 1) -- Detect and
// the credential-sync read topology own that question entirely; this
// file only ever answers "what was minted last time," which no
// observable-state probe can reconstruct.
package onboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// mintRecord is the R20 record's on-disk shape: a flat, non-extensible
// struct holding only the non-secret client-secret id.
//
// The design sketch describes the shape as "{secret_id, recoverable}
// or equivalent." This implementation omits a separate boolean
// "recoverable" field: "not recoverable" (R20's third bullet, AC-35b)
// is fully and unambiguously represented by the absence of a readable
// record for the (kind, project) pair -- no file, or one that fails to
// parse -- so a dedicated flag would only ever hold one value in every
// code path this package writes. There is no scenario in this pipeline
// where a record is persisted while deliberately marking its own id
// unusable.
type mintRecord struct {
	SecretID string `json:"secret_id"`
}

// recordDir returns the directory the R20 record lives in: the parent
// of config.GlobalConfigPath() ("~/.config/niwa" in production,
// respecting XDG_CONFIG_HOME), the same root the operator-local
// pointer file already resolves against. Reusing GlobalConfigPath's
// resolution (rather than re-deriving XDG_CONFIG_HOME/HOME lookup
// here) keeps exactly one place that knows the operator-local config
// root.
func recordDir() (string, error) {
	cfgPath, err := config.GlobalConfigPath()
	if err != nil {
		return "", fmt.Errorf("resolving R20 record directory: %w", err)
	}
	return filepath.Dir(cfgPath), nil
}

// recordFileName renders the on-disk filename for a (kind, project)
// pair's mint record. Both segments are config-sourced identifiers,
// never response bodies; path separators are replaced defensively so a
// hostile kind/project value cannot escape dir, even though kind is
// always one of a small compiled-in backend set and project is
// expected to be a UUID.
func recordFileName(kind, project string) string {
	sanitize := func(s string) string {
		s = strings.ReplaceAll(s, "/", "_")
		s = strings.ReplaceAll(s, string(filepath.Separator), "_")
		return s
	}
	return fmt.Sprintf("onboard-mint-record-%s-%s.json", sanitize(kind), sanitize(project))
}

// readMintRecord reads the R20 record for (kind, project) from dir. A
// missing file, one that fails to parse, or one with an empty
// secret_id is reported as found=false with a nil error -- all three
// are "not recoverable" in R20's sense (AC-35b), and a corrupt local
// file must never crash the wizard or block a fresh mint. A genuine
// I/O error (permission denied, etc.) is returned as err alongside
// found=false so the caller can surface a warning while still treating
// the prior id as unrecoverable and proceeding.
func readMintRecord(dir, kind, project string) (rec mintRecord, found bool, err error) {
	path := filepath.Join(dir, recordFileName(kind, project))
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return mintRecord{}, false, nil
		}
		return mintRecord{}, false, fmt.Errorf("reading R20 record %s: %w", path, readErr)
	}
	var parsed mintRecord
	if jsonErr := json.Unmarshal(data, &parsed); jsonErr != nil || parsed.SecretID == "" {
		// Malformed or empty: unrecoverable, but not fatal -- no error
		// propagated, matching the missing-file case.
		return mintRecord{}, false, nil
	}
	return parsed, true, nil
}

// writeMintRecord persists rec for (kind, project) in dir, at mode
// 0600 via the open-in-dir-then-rename discipline shared with the
// config-authoring writes (atomicWriteFile, config_authoring.go) -- no
// in-place truncate+rewrite, so a reader never observes a partial
// file.
func writeMintRecord(dir, kind, project string, rec mintRecord) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshalling R20 record: %w", err)
	}
	path := filepath.Join(dir, recordFileName(kind, project))
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing R20 record %s: %w", path, err)
	}
	return nil
}
