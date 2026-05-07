package workspace

import (
	"context"
	"errors"
	"fmt"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
)

// CredentialSyncPathPrefix is the conventional namespace under which
// niwa stores credential bodies in the personal vault: keys live at
// CredentialSyncPathPrefix + "<kind>/<project>". Wired into
// vaultCredLoader.PathPrefix at apply time. Exported so tests can
// reference it without re-deriving the convention.
const CredentialSyncPathPrefix = "/niwa/provider-auth/"

// maxProviderAuthBodyBytes caps the byte length of a vault-fetched
// credential body before TOML unmarshal. The realistic body is
// ~200 bytes (a small TOML document with version, client_id,
// client_secret, optional api_url); the cap is generous (8 KiB)
// while still protecting against TOML-parser DoS via deeply-nested
// or pathologically-large bodies (DESIGN Security § "New surfaces").
const maxProviderAuthBodyBytes = 8 * 1024

// providerAuthBody is the wire shape of a credential body stored in
// the personal vault under CredentialSyncPathPrefix + "<kind>/<project>".
// PRD R7 schema: top-level version field plus the Infisical
// machine-identity fields. Future backends use parallel paths with
// their own body shapes; for I7's scope only Infisical is read.
type providerAuthBody struct {
	Version      string `toml:"version"`
	ClientID     string `toml:"client_id"`
	ClientSecret string `toml:"client_secret"`
	APIURL       string `toml:"api_url"`
}

// parseProviderAuthBody parses a vault-fetched credential body and
// returns a ProviderAuthEntry suitable for authentication. kind and
// project are passed in by the caller (they are the inputs that
// produced the vault key path) and propagated into the returned
// entry's Config.
//
// Errors carry the vault key path and the offending field name only
// — never the body bytes or any field value (PRD R18; AC-36
// sentinel-canary test enforces this).
func parseProviderAuthBody(kind, project string, raw []byte) (*ProviderAuthEntry, error) {
	if len(raw) > maxProviderAuthBodyBytes {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at "+CredentialSyncPathPrefix+"%s/%s exceeds %d bytes (%d) and will not be parsed",
			kind, project, maxProviderAuthBodyBytes, len(raw),
		)
	}
	var body providerAuthBody
	if err := toml.Unmarshal(raw, &body); err != nil {
		// Wrap the TOML parser's error WITHOUT including the body
		// bytes — toml.Unmarshal errors are content-free (offset
		// only), but we still scrub by surfacing only kind+project.
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at "+CredentialSyncPathPrefix+"%s/%s is malformed: %w",
			kind, project, sanitizeTOMLError(err),
		)
	}
	version := body.Version
	if version == "" {
		// PRD R8 backward-compat default: a body without a version
		// field is treated as version "1".
		version = "1"
	}
	if version != "1" {
		return nil, fmt.Errorf(
			"provider-auth body at "+CredentialSyncPathPrefix+"%s/%s has unsupported schema version %q; this niwa version supports v1. Upgrade niwa or update the vault entry.",
			kind, project, version,
		)
	}
	if body.ClientID == "" {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at "+CredentialSyncPathPrefix+"%s/%s is missing required field %q",
			kind, project, "client_id",
		)
	}
	if body.ClientSecret == "" {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at "+CredentialSyncPathPrefix+"%s/%s is missing required field %q",
			kind, project, "client_secret",
		)
	}
	cfg := map[string]any{
		"project":       project,
		"client_id":     body.ClientID,
		"client_secret": body.ClientSecret,
	}
	if body.APIURL != "" {
		cfg["api_url"] = body.APIURL
	}
	return &ProviderAuthEntry{
		Kind:   kind,
		Config: cfg,
	}, nil
}

// sanitizeTOMLError returns a TOML parser error wrapped to ensure no
// body content escapes into the returned message. The BurntSushi/toml
// package's errors today reference position metadata only, but a
// future version that quoted offending tokens would be a leak. This
// helper short-circuits that risk.
func sanitizeTOMLError(err error) error {
	if err == nil {
		return nil
	}
	// Today's TOML errors are position-only. We deliberately do NOT
	// pass through arbitrary error text — instead, we surface a
	// short fixed message to be safe under any future toml-package
	// upgrade. The vault key path in the wrapping error is enough
	// for a user to locate the malformed body.
	return errors.New("TOML parse error")
}

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
// overlay credential-sync vault provider. The pool consults the
// loader lazily — only when a file lookup misses for a given
// (kind, project), and only once per pair per apply (memoized via
// CredentialPool.cache).
//
// PathPrefix carries the conventional namespace under which niwa
// stores credential bodies in the personal vault. For Infisical
// today: "/niwa/provider-auth/" — the per-pair key path is then
// PathPrefix + kind + "/" + project (PRD R7 schema convention).
//
// SelfKind and SelfProject identify the credential-sync provider
// itself: when injectProviderTokens iterates the global overlay's
// vault registry it WILL ask the pool to look up credentials for
// that provider's own (kind, project). Resolving against the
// credential-sync provider for its own credentials would be the
// PRD R9 chicken-and-egg cycle the static validators forbid; the
// pool's lookupVault refuses self-queries to enforce R9 dynamically
// alongside the parse-time validateCredentialSyncBootstrap{Pre,Post}Overlay
// gates. SelfKind/SelfProject are populated at apply.go's Step 0.4
// from the credential-sync ProviderSpec.
type vaultCredLoader struct {
	Provider     vault.Provider
	ProviderName string // "" for anonymous; AC-39 rendering handled by renderVaultProvider.
	PathPrefix   string
	SelfKind     string // (kind, project) of the credential-sync provider itself.
	SelfProject  string
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
// Invariants every Lookup implementation MUST preserve (PRD R4
// precedence):
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
// Algorithm:
//   - Compute the file entry via matchFileEntry.
//   - If a vault loader is wired, compute the vault entry via the
//     lazy-fetch path (memoized per-pair in p.cache so repeat
//     Lookups for the same pair don't double-fetch within one
//     apply — AC-31).
//   - Decide:
//     - File hit: SourceLocalFile, set Fallback when vault also had
//       an entry.
//     - File miss + vault hit: SourceVault.
//     - File miss + vault miss: SourceCLISession (tentative; I8 may
//       upgrade to SourceNone).
//
// Errors from the vault path (provider unreachable, body parse
// failure, body validation failure) are returned to the caller for
// classification by I8's R13 wiring; AC-39 anonymous-vault rendering
// is single-sourced through renderVaultProvider.
func (p *CredentialPool) Lookup(ctx context.Context, kind, project string) (*ProviderAuthEntry, AuditRecord, error) {
	rec := AuditRecord{Kind: kind, Project: project}
	fileEntry := matchFileEntry(p.fileEntries, kind, project)

	var vaultEntry *ProviderAuthEntry
	if p.vaultLoader != nil {
		entry, err := p.lookupVault(ctx, kind, project)
		if err != nil {
			// Surface vault-side errors to the caller. I8 will
			// classify them per PRD R13; the audit record is NOT
			// appended for an errored lookup because the apply will
			// abort and SaveState is never reached on error paths.
			return nil, rec, err
		}
		vaultEntry = entry
	}

	switch {
	case fileEntry != nil:
		rec.Source = SourceLocalFile
		if vaultEntry != nil {
			// AC-39: anonymous-aware rendering via renderVaultProvider.
			rec.Fallback = renderVaultProvider(p.vaultLoader.ProviderName)
		}
		p.audit = append(p.audit, rec)
		return fileEntry, rec, nil
	case vaultEntry != nil:
		rec.Source = SourceVault
		rec.Provider = p.vaultLoader.ProviderName
		p.audit = append(p.audit, rec)
		return vaultEntry, rec, nil
	default:
		rec.Source = SourceCLISession
		p.audit = append(p.audit, rec)
		return nil, rec, nil
	}
}

// lookupVault fetches the credential body for (kind, project) from
// the loader's vault provider and parses it into a
// ProviderAuthEntry. Memoized per-pair in p.cache so repeat Lookups
// for the same pair within one apply hit the cache, not the network
// (PRD R6 lazy fetch + AC-31 re-fetch-each-apply since the cache
// belongs to the per-apply CredentialPool).
//
// Return cases:
//   - (nil, nil) when the requested (kind, project) IS the
//     credential-sync provider's own (R9 dynamic self-lookup
//     guard; refuses to fire a Resolve call against the
//     credential-sync provider for its own credentials).
//   - (nil, nil) on ErrKeyNotFound (silent fallthrough; PRD R13.3).
//   - (entry, nil) on a successful fetch + parse.
//   - (nil, wrapped) on ErrProviderUnreachable (PRD R13.1/R13.2);
//     the wrap uses %w so I8's errors.Is dispatch matches the
//     vault sentinel — DO NOT change %w to %v in this path.
//   - (nil, parseErr) on body parse / validation failures
//     (PRD R13.4 / R13.5 / R13.7); the parseErr never contains
//     body bytes (PRD R18 / AC-36).
func (p *CredentialPool) lookupVault(ctx context.Context, kind, project string) (*ProviderAuthEntry, error) {
	// PRD R9 dynamic enforcement: refuse to resolve the
	// credential-sync provider's own (kind, project). injectProviderTokens
	// iterates the global overlay's vault registry — which contains the
	// credential-sync spec itself — so without this guard a Resolve call
	// against the credential-sync provider for its own credentials
	// would happen and feed an authenticateEntry chain that re-injects
	// a token into the very spec used to fetch it. The static R9 check
	// (validateCredentialSyncBootstrapPreOverlay) skips the syncSpec
	// slot from its scan, so this guard is the matching dynamic check.
	// Refusing the self-lookup behaves like ErrKeyNotFound: the audit
	// records SourceCLISession (the actual auth path the I6 open used)
	// and apply continues.
	if p.vaultLoader.SelfKind != "" && kind == p.vaultLoader.SelfKind && project == p.vaultLoader.SelfProject {
		return nil, nil
	}
	key := kind + "/" + project
	if cached, ok := p.cache[key]; ok {
		return cached.Entry, cached.Err
	}

	ref := vault.Ref{
		Path: p.vaultLoader.PathPrefix + kind,
		Key:  project,
	}
	sv, _, err := p.vaultLoader.Provider.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, vault.ErrKeyNotFound) {
			// PRD R13.3: silent fallthrough. Cache the absence so
			// repeat Lookups for the same pair don't re-query.
			p.cache[key] = vaultLookupResult{}
			return nil, nil
		}
		// Wrap network/auth errors so I8 can match them via
		// errors.Is at the apply-error classification site (R13.1/R13.2).
		wrapped := fmt.Errorf("fetching credential body for %s/%s from vault: %w", kind, project, err)
		p.cache[key] = vaultLookupResult{Err: wrapped}
		return nil, wrapped
	}

	// Plaintext access for parsing the credential body. This is a
	// deliberate new caller of secret.reveal.UnsafeReveal — the
	// package's own doc warns "DO NOT import this package from new
	// code without explicit review." The justification: the body is
	// a structured TOML envelope niwa itself wrote into the vault,
	// not user-payload secret data. The bytes flow only into
	// parseProviderAuthBody, which extracts the credential fields
	// into a ProviderAuthEntry (which then flows through the existing
	// secret-handling pipeline alongside file-sourced entries). The
	// raw bytes are never logged, never written to argv, never
	// surfaced in error messages (parseProviderAuthBody's errors
	// reference the vault path and field name only, never the
	// content — verified by the AC-36 sentinel-canary test).
	bodyBytes := reveal.UnsafeReveal(sv)
	entry, parseErr := parseProviderAuthBody(kind, project, bodyBytes)
	p.cache[key] = vaultLookupResult{Entry: entry, Err: parseErr}
	return entry, parseErr
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
