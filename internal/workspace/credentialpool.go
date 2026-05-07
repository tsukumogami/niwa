package workspace

import (
	"context"

	"github.com/tsukumogami/niwa/internal/vault"
)

// Source classifies the authentication-source decision the
// credential pool recorded for one (kind, project) Lookup. The name
// reads as "which source authenticated this provider"; the four
// values cover both the case where a credential entry was supplied
// by a pool layer (local-file, vault) and the case where no entry
// was found and the apply will fall through to a backend's
// CLI-session credentials (cli-session) or ultimately fail to
// authenticate (none).
//
// Important: cli-session and none are NOT pool layers — they
// describe what happened when no pool entry was supplied. Code
// that means "the credential came from the pool's vault layer"
// must check `rec.Source == SourceVault` specifically; checking
// `rec.Source != SourceLocalFile` would over-include cli-session
// and none.
//
//   - SourceLocalFile  — entry came from ~/.config/niwa/provider-auth.toml.
//   - SourceVault      — entry came from the personal-overlay vault loader
//                        (machine-identity-vault-sync; wired in by I7).
//   - SourceCLISession — no pool entry was found; the backend will fall
//                        through to its CLI-session credentials
//                        (e.g., `infisical login`). Recorded as a
//                        tentative classification while the pool is
//                        building the audit log; I8 may upgrade the
//                        record to SourceNone if backend auth
//                        ultimately fails.
//   - SourceNone       — no source produced a usable credential.
//                        Set by I8 after a failed auth call; never
//                        produced by Lookup directly.
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
//
// Concurrency: CredentialPool is NOT goroutine-safe. Lookup mutates
// the audit slice on every call; concurrent Lookup calls would race
// on the append. The current production caller (injectProviderTokens
// in apply.go) is sequential — three call sites run one after the
// other, each iterating its registry's specs serially — so no
// synchronization is needed. A future fan-out across providers must
// either serialize Lookup calls or wrap them in a mutex.
type CredentialPool struct {
	fileEntries []ProviderAuthEntry
	vaultLoader *vaultCredLoader            // nil in I2 (and whenever opt-in is off).
	audit       []AuditRecord                // appended during Lookup calls; not goroutine-safe.
	cache       map[string]vaultLookupResult // populated by I7's vault-fetch path; unused in I2.
}

// vaultCredLoader is the credential-pool's connection to the personal-
// overlay vault provider. I2 declares the placeholder; I7 fleshes it
// out with Provider, ProviderName, and PathPrefix fields plus the
// lazy-fetch implementation in Lookup. The type stays unexported
// because only the workspace package constructs it: I7's
// openCredentialSyncProvider helper (also in this package) will
// build instances directly via struct literal, then pass them to
// NewCredentialPool. No other package should construct or compare
// vaultCredLoader values.
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
//
// Invariants every Lookup implementation MUST preserve (these
// stay true through I7's restructure and I8's source-upgrade work):
//
//  1. Exactly one AuditRecord is appended to p.audit per Lookup call.
//  2. AuditRecords are appended in call order; AuditLog returns them
//     in the same order.
//  3. The returned AuditRecord is a copy of what was appended
//     (callers may compare or render but should not assume mutating
//     the returned value mutates the log entry).
//  4. AuditRecord.Fallback is set to "vault:<provider-name>" if and
//     only if both layers had an entry for the same (kind, project)
//     and the file layer won; otherwise Fallback is empty.
//  5. AuditRecord.Source is exactly one of SourceLocalFile,
//     SourceVault, or SourceCLISession when produced by Lookup.
//     SourceNone is reserved for I8's post-failure upgrade and is
//     NEVER produced here.
//  6. AuditRecord.Provider is the vault provider name (or "" for
//     anonymous) when Source == SourceVault; "" otherwise.
//
// In I2 (file-only path), the algorithm is:
//
//  - Search the file layer via MatchProviderAuth (single source of
//    truth for matching rules).
//  - If matched, record SourceLocalFile and return the entry.
//  - Otherwise, record SourceCLISession — backends may fall through
//    to their CLI session for the (kind, project).
//
// I7 restructure note: the file-first short-circuit means the vault
// layer is never consulted in I2. DESIGN's full algorithm computes
// BOTH file and vault entries before deciding which wins, then
// records the loser as Fallback. I7 will replace the short-circuit
// with a compute-both join, preserving the invariants above.
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

// AuditTrail is the named slice type the pool returns from AuditLog.
// It carries the per-Lookup AuditRecord values plus an AsMap method
// that projects the slice into the InstanceState.AuthSources shape
// state.json persists. The named type lets I3/I4/I9 share a single
// rendering helper for the "<source>" / "<fallback>" strings.
type AuditTrail []AuditRecord

// AuditLog returns the AuditTrail the pool collected during this
// apply invocation, in Lookup-call order. I3 reads it (via AsMap)
// for state persistence; I9 reads it to emit per-pair stderr lines
// (R12).
func (p *CredentialPool) AuditLog() AuditTrail {
	return p.audit
}

// AsMap projects the AuditTrail into the InstanceState.AuthSources
// shape: a map keyed by "<kind>/<project>" with categorical-string
// values for Source and Fallback (PRD R11 / Decision 3).
//
// When the same (kind, project) pair appears multiple times in the
// trail (e.g., the same provider declared in both team and personal
// vault registries), the LAST record wins. This matches the apply
// pipeline's "later layer overrides earlier layer" semantics — the
// three injectProviderTokens callsites in apply.go (overlay layer at
// the existing line ~647, team layer at ~777, personal-global layer
// at ~781) run in that order, so the personal-overlay decision
// lands as the user's actual final state for that pair. The
// last-write-wins behavior pairs with Lookup's append-order
// invariant (see Lookup invariant #2) — together they guarantee
// AsMap's output reflects the apply's final decisions even if the
// apply pipeline reorders its three callsites.
func (a AuditTrail) AsMap() map[string]AuthSourceRecord {
	if len(a) == 0 {
		return nil
	}
	out := make(map[string]AuthSourceRecord, len(a))
	for _, rec := range a {
		key := rec.Kind + "/" + rec.Project
		out[key] = AuthSourceRecord{
			Source:   renderSource(rec),
			Fallback: rec.Fallback,
		}
	}
	return out
}

// renderSource translates an AuditRecord into the categorical-string
// form state.json (and, later, R12 stderr / `niwa status
// --audit-auth`) consume. The mapping:
//
//   - SourceLocalFile   → "local-file"
//   - SourceVault       → "vault:<provider-name>"  (or "vault:(anonymous)" when Provider == "")
//   - SourceCLISession  → "cli-session"
//   - SourceNone        → "none"
//   - any other Source  → "" (defensive; never produced by Lookup)
//
// Single-sourcing this rule means I9's R12 stderr emitter and I4's
// audit-auth renderer can call the same helper instead of
// re-implementing the vault-name/anonymous edge case (AC-39).
func renderSource(rec AuditRecord) string {
	switch rec.Source {
	case SourceLocalFile:
		return "local-file"
	case SourceVault:
		return renderVaultProvider(rec.Provider)
	case SourceCLISession:
		return "cli-session"
	case SourceNone:
		return "none"
	default:
		return ""
	}
}

// renderVaultProvider formats a vault provider name for the audit
// surfaces. Anonymous providers (Provider == "") render as
// "vault:(anonymous)" per PRD AC-39; named providers render as
// "vault:<name>". Never produces a bare "vault:" with a trailing
// colon and empty name.
//
// I7 will call this helper when populating AuditRecord.Fallback —
// the pool sets Fallback to renderVaultProvider(loader.ProviderName)
// when the file layer wins and the vault layer also had an entry —
// so the AC-39 anonymous-rendering rule applies symmetrically to
// the SOURCE and FALLBACK columns.
func renderVaultProvider(name string) string {
	if name == "" {
		return "vault:(anonymous)"
	}
	return "vault:" + name
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
