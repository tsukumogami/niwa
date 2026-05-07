package workspace

import (
	"context"

	"github.com/tsukumogami/niwa/internal/vault"
)

// Source identifies which layer of the credential pool supplied a
// particular machine-identity entry.
//
//   - SourceLocalFile  — entry came from ~/.config/niwa/provider-auth.toml.
//   - SourceVault      — entry came from the personal-overlay vault loader
//                        (machine-identity-vault-sync; wired in by I7).
//   - SourceCLISession — no entry was found in the pool; the backend will
//                        fall through to its CLI-session credentials
//                        (e.g., `infisical login`). Recorded as a
//                        tentative classification while the pool is
//                        constructing the audit; downstream phases (I8)
//                        may upgrade to SourceNone if backend auth
//                        ultimately fails.
//   - SourceNone       — no source produced a usable credential.
type Source string

const (
	SourceLocalFile  Source = "local-file"
	SourceVault      Source = "vault"
	SourceCLISession Source = "cli-session"
	SourceNone       Source = "none"
)

// AuditRecord captures the credential-source decision for a single
// (kind, project) lookup during one apply invocation. The pool
// appends one record per Lookup call. AuditRecord values are
// categorical strings only — they never carry credential bytes
// (PRD R18).
//
// Fallback is set when the file layer wins and the vault layer also
// had an entry: it carries `vault:<provider-name>` so audit surfaces
// can show the user that they have a per-machine override active
// (PRD R11 FALLBACK column).
type AuditRecord struct {
	Kind     string
	Project  string
	Source   Source
	Provider string // vault provider name when Source == SourceVault; "" otherwise.
	Fallback string // "vault:<name>" when local file overrode a vault entry; "" otherwise.
}

// CredentialPool joins the (eager) local-file credential layer with
// an (optional, lazy) vault loader. In I2 the loader is never wired,
// so Lookup is a thin façade over MatchProviderAuth that records
// audit decisions. I7 will plug in the vaultLoader and exercise the
// lazy-fetch path; the public surface stays the same.
type CredentialPool struct {
	fileEntries []ProviderAuthEntry
	vaultLoader *vaultCredLoader            // nil in I2 (and whenever opt-in is off).
	audit       []AuditRecord                // appended during Lookup calls.
	cache       map[string]vaultLookupResult // populated by I7's vault-fetch path; unused in I2.
}

// vaultCredLoader is the credential-pool's connection to the personal-
// overlay vault provider. I2 declares the placeholder; I7 fleshes it
// out with Provider/ProviderName/PathPrefix fields and the lazy-
// fetch implementation in Lookup.
type vaultCredLoader struct {
	// Fields are intentionally absent in I2 — the type exists only so
	// CredentialPool.vaultLoader has a stable type and so I7 can fill
	// in the implementation without touching NewCredentialPool's
	// exported signature.
}

// vaultLookupResult is the per-(kind, project) memoization entry for
// the vault-fetch cache. I2 declares the placeholder; I7 populates
// it with the parsed entry plus any fetch/parse error.
type vaultLookupResult struct {
	Entry *ProviderAuthEntry
	Err   error
}

// NewCredentialPool builds a CredentialPool from an eager file layer
// and an optional vault loader. The loader argument is accepted in I2
// for forward compatibility with I7; passing a non-nil loader is a
// no-op until I7 wires the lazy lookup.
func NewCredentialPool(file []ProviderAuthEntry, loader *vaultCredLoader) *CredentialPool {
	return &CredentialPool{
		fileEntries: file,
		vaultLoader: loader,
		audit:       nil,
		cache:       map[string]vaultLookupResult{},
	}
}

// Lookup returns the winning credential entry for (kind, project)
// plus the AuditRecord that the pool appended for this decision.
// In I2 (file-only path), the algorithm is:
//
//  1. Search the file layer via MatchProviderAuth (single source of
//     truth for matching rules).
//  2. If matched, record SourceLocalFile and return the entry.
//  3. Otherwise, record a tentative SourceCLISession — backends may
//     fall through to their CLI session for the (kind, project).
//
// I7 restructure note: the file-first short-circuit above means
// the vault layer is never consulted in I2. DESIGN's full algorithm
// computes BOTH the file entry AND the vault entry first, then
// decides which wins (file > vault) and records the loser as
// AuditRecord.Fallback. I7 will need to flip this Lookup body to
// the compute-both shape (not just paste a vault-fetch branch in
// front of the file-hit return). Mechanically: replace the
// short-circuit returns with a join over fileEntry + vaultEntry,
// set rec.Fallback when both are populated, append the audit
// record once, and return.
//
// The error return is reserved for I7's vault-fetch errors; I2
// always returns nil for the error.
func (p *CredentialPool) Lookup(ctx context.Context, kind, project string) (*ProviderAuthEntry, AuditRecord, error) {
	rec := AuditRecord{Kind: kind, Project: project}
	if entry := matchFileEntry(p.fileEntries, kind, project); entry != nil {
		rec.Source = SourceLocalFile
		// I7: replaces this branch with a Fallback-aware version
		// that consults the vault loader before deciding.
		p.audit = append(p.audit, rec)
		return entry, rec, nil
	}
	// I7 restructures this entire function (see doc comment above).
	// In I2 (loader nil), we fall through to the cli-session record.
	rec.Source = SourceCLISession
	p.audit = append(p.audit, rec)
	return nil, rec, nil
}

// AuditLog returns the slice of AuditRecord values the pool collected
// during this apply invocation, in Lookup-call order. I3 will read
// the slice (and likely add an AsMap helper alongside) for state
// persistence; I9 will read it to emit per-pair stderr lines (R12).
func (p *CredentialPool) AuditLog() []AuditRecord {
	return p.audit
}

// matchFileEntry synthesizes a vault.ProviderSpec for (kind, project)
// and reuses MatchProviderAuth from providerauth.go so the matching
// rule stays single-sourced across the pool's file path and the
// existing direct-call path. The synthesized spec carries the
// canonical "project" key in Config because that's what
// MatchProviderAuth consults for the Infisical match.
func matchFileEntry(entries []ProviderAuthEntry, kind, project string) *ProviderAuthEntry {
	if len(entries) == 0 {
		return nil
	}
	spec := vault.ProviderSpec{
		Kind: kind,
		Config: vault.ProviderConfig{
			"project": project,
		},
	}
	return MatchProviderAuth(spec, entries)
}
