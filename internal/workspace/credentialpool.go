package workspace

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
)

// CredentialSyncPathPrefix is the conventional namespace under which
// niwa stores credential bodies in the personal vault: each kind
// gets one folder at CredentialSyncPathPrefix + "<kind>", and the
// per-project credential body lives at the key "p-<project>" inside
// that folder. Wired into vaultCredLoader.PathPrefix at apply time.
// Exported so tests can reference it without re-deriving the
// convention.
//
// The "p-" key prefix exists because Infisical (and likely other
// backends) reject secret keys whose first character is a digit;
// roughly 37.5% of UUIDv4 values fall into that bucket. Prefixing the
// KEY (not the path) sidesteps the validation while keeping the path
// shape clean for human inspection in the vault UI.
const CredentialSyncPathPrefix = "/niwa/provider-auth/"

// credentialSyncProjectKeyPrefix is prepended to the project UUID to
// form the vault secret key. Exported indirectly via the rendered
// path strings so error messages and documentation can show users
// the exact key they need to set, e.g.,
// "/niwa/provider-auth/infisical/p-<uuid>".
const credentialSyncProjectKeyPrefix = "p-"

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
	// keyedPath renders the user-visible path/key combination for
	// diagnostic strings: ".../<kind>/p-<project>". Users look this
	// path up directly in their vault UI, so the rendered prefix
	// MUST match the actual lookup key (see lookupVault).
	keyedPath := CredentialSyncPathPrefix + kind + "/" + credentialSyncProjectKeyPrefix + project
	if len(raw) > maxProviderAuthBodyBytes {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at %s exceeds %d bytes (%d) and will not be parsed",
			keyedPath, maxProviderAuthBodyBytes, len(raw),
		)
	}
	var body providerAuthBody
	if err := toml.Unmarshal(raw, &body); err != nil {
		// Wrap the TOML parser's error WITHOUT including the body
		// bytes — toml.Unmarshal errors are content-free (offset
		// only), but we still scrub by surfacing only the path.
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at %s is malformed: %w",
			keyedPath, sanitizeTOMLError(err),
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
			"provider-auth body at %s has unsupported schema version %q; this niwa version supports v1. Upgrade niwa or update the vault entry.",
			keyedPath, version,
		)
	}
	if body.ClientID == "" {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at %s is missing required field %q",
			keyedPath, "client_id",
		)
	}
	if body.ClientSecret == "" {
		return nil, fmt.Errorf(
			"vault-sourced provider-auth body at %s is missing required field %q",
			keyedPath, "client_secret",
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

// vaultUnreachableError is the typed error lookupVault returns when
// the personal vault provider can't be reached
// (vault.ErrProviderUnreachable). Apply-time wiring uses errors.As
// to recover the (kind, project) pair plus the vault provider name
// for PRD R13.1's aggregated warning text. Unwrap returns the
// fmt.Errorf wrap so errors.Is(err, vault.ErrProviderUnreachable)
// keeps matching for any downstream consumer.
type vaultUnreachableError struct {
	Kind         string
	Project      string
	ProviderName string // "" for anonymous; render via renderVaultProvider.
	err          error  // the fmt.Errorf %w-wrap; Unwrap returns it.
}

func (e *vaultUnreachableError) Error() string { return e.err.Error() }
func (e *vaultUnreachableError) Unwrap() error { return e.err }

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
	// vaultUnreachable accumulates one observation per distinct
	// (kind, project, providerName) triple seen across all
	// injectProviderTokens calls in this apply. apply.go's
	// post-injection R13.1 warning emitter walks the slice once
	// and emits one stderr line per observation. Deduplicated on
	// append.
	vaultUnreachable []vaultUnreachableError
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
			// PRD R13.1: a vault-unreachable error is recoverable
			// when the file layer covers this pair (PRD AC-7 + AC-16:
			// row shows local-file, apply exits 0, single aggregated
			// warning emitted apply-side). When the file does NOT
			// cover the pair, surface the typed error so
			// injectProviderTokens can soften (continue iterating)
			// and the apply falls through to the backend's CLI
			// session per R13.1/R13.2.
			//
			// Other vault errors (R13.4/5/6/7) propagate hard with
			// no audit row — apply will abort, SaveState is never
			// reached.
			var vue *vaultUnreachableError
			if errors.As(err, &vue) {
				if fileEntry != nil {
					// File wins; the vault-unreachable observation
					// has already been recorded on the pool by
					// lookupVault. Do NOT set rec.Fallback here:
					// PRD R11's FALLBACK semantics describe a vault
					// entry the user successfully fetched but
					// chose to override locally. An UNREACHABLE
					// vault has no entry to be a fallback for.
					rec.Source = SourceLocalFile
					p.audit = append(p.audit, rec)
					return fileEntry, rec, nil
				}
				rec.Source = SourceCLISession
				p.audit = append(p.audit, rec)
				return nil, rec, err
			}
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

	// Infisical and likely other backends reject secret keys whose
	// first character is a digit (~37.5% of UUIDv4 values), so the
	// project UUID is prepended with "p-" before being used as the
	// vault Key. The Path is unchanged. User-facing diagnostics in
	// parseProviderAuthBody render the same prefixed key so the
	// path the user sees in an error message matches what they need
	// to set in their vault.
	ref := vault.Ref{
		Path: p.vaultLoader.PathPrefix + kind,
		Key:  credentialSyncProjectKeyPrefix + project,
	}
	sv, _, err := p.vaultLoader.Provider.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, vault.ErrKeyNotFound) {
			// PRD R13.3: silent fallthrough. Cache the absence so
			// repeat Lookups for the same pair don't re-query.
			p.cache[key] = vaultLookupResult{}
			return nil, nil
		}
		// Wrap network/auth errors so apply-error classification
		// (errors.Is) keeps matching the vault sentinel.
		wrapped := fmt.Errorf("fetching credential body for %s/%s from vault: %w", kind, project, err)
		// PRD R13.1/R13.2: when the underlying error is
		// vault.ErrProviderUnreachable, surface a typed value the
		// apply orchestrator can errors.As to recover the
		// (kind, project) + provider-name tuple for the aggregated
		// warning. Other vault errors stay as the bare wrap.
		if errors.Is(err, vault.ErrProviderUnreachable) {
			vue := &vaultUnreachableError{
				Kind:         kind,
				Project:      project,
				ProviderName: p.vaultLoader.ProviderName,
				err:          wrapped,
			}
			p.recordVaultUnreachable(vue)
			p.cache[key] = vaultLookupResult{Err: vue}
			return nil, vue
		}
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

// recordVaultUnreachable appends one observation to the pool's
// vault-unreachable buffer, deduplicated on
// (Kind, Project, ProviderName). Repeat observations for the same
// triple are merged so the aggregated PRD R13.1 warning never
// names the same provider twice for the same pair.
func (p *CredentialPool) recordVaultUnreachable(e *vaultUnreachableError) {
	if e == nil {
		return
	}
	for _, existing := range p.vaultUnreachable {
		if existing.Kind == e.Kind && existing.Project == e.Project && existing.ProviderName == e.ProviderName {
			return
		}
	}
	p.vaultUnreachable = append(p.vaultUnreachable, *e)
}

// VaultUnreachableObservations returns the deduplicated typed
// vault-unreachable observations the pool recorded during this
// apply, in append order. Apply-time wiring walks the slice once
// after the third injectProviderTokens call to emit the aggregated
// PRD R13.1 warning.
func (p *CredentialPool) VaultUnreachableObservations() []vaultUnreachableError {
	return p.vaultUnreachable
}

// HasFileFallback reports whether the pool's file layer can
// satisfy a vault provider's (kind, project) — i.e., whether a
// provider-auth.toml entry exists for that pair. Exposed as a
// stable API for callers that need to inspect fallback coverage
// without running a full Lookup. Today's only direct caller is
// integration-test code; injectProviderTokens itself relies on
// the pool's compute-both algorithm to prefer the file entry on
// vault-unreachable, so it doesn't query HasFileFallback directly.
//
// Note: a "file fallback" answers only the local-file question.
// The backend's own CLI-session fallback (PRD R13.1's "or a
// working CLI session") is implicit: when no file fallback exists
// and no token is injected, the backend's later universal-auth
// call falls through to its CLI session and either succeeds (R13.1)
// or fails (R13.2 — apply exits non-zero from the backend's own
// error path).
func (p *CredentialPool) HasFileFallback(kind, project string) bool {
	return matchFileEntry(p.fileEntries, kind, project) != nil
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

// EmitR12Lines walks the AuditTrail and emits one apply-time stderr
// line per credential-source decision worth logging, per PRD R12 +
// AC-13 / AC-14 / AC-15. The emit rules:
//
//   - Source == SourceVault → "auth: <kind>/<project> source=vault:personal-overlay"
//     (or "vault:personal-overlay(<name>)" when the provider has a
//     name; see renderVaultProvider for the rendering rule).
//   - Source == SourceLocalFile && Fallback != "" →
//     "auth: <kind>/<project> source=local-file fallback=<fallback>"
//     (the user has a per-machine override active over a vault entry).
//   - Source == SourceLocalFile && Fallback == "" → no line (no
//     vault-sync activity to surface).
//   - Source == SourceCLISession → no line (the apply silently fell
//     through to the backend's CLI session; no per-pair signal
//     needed).
//   - Source == SourceNone → no line (apply will fail at the backend
//     auth call, which already produces its own diagnostic).
//
// When the same (kind, project) appears multiple times in the trail
// (e.g., the same provider declared in both team and personal vault
// registries), the LAST record wins — matching AsMap's last-write-wins
// rule. Lines are emitted in deterministic KIND, PROJECT order so
// snapshot tests stay stable.
//
// Reporter is the apply pipeline's stderr surface. Lines flow through
// Reporter.Log so they share spinner-handling, TTY-detection, and
// any future redaction wrappers with the rest of the apply output.
func (a AuditTrail) EmitR12Lines(reporter *Reporter) {
	if reporter == nil || len(a) == 0 {
		return
	}
	// Last-write-wins per (kind, project).
	type key struct{ kind, project string }
	final := make(map[key]AuditRecord, len(a))
	for _, rec := range a {
		final[key{rec.Kind, rec.Project}] = rec
	}
	// Stable emit order: KIND ascending, then PROJECT ascending.
	rows := make([]AuditRecord, 0, len(final))
	for _, rec := range final {
		rows = append(rows, rec)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].Project < rows[j].Project
	})
	for _, rec := range rows {
		switch rec.Source {
		case SourceVault:
			reporter.Log("auth: %s/%s source=%s",
				rec.Kind, rec.Project, renderVaultProvider(rec.Provider))
		case SourceLocalFile:
			if rec.Fallback != "" {
				reporter.Log("auth: %s/%s source=local-file fallback=%s",
					rec.Kind, rec.Project, rec.Fallback)
			}
		}
	}
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
//   - SourceVault       → "vault:personal-overlay"  (anonymous form; or "vault:personal-overlay(<name>)" when named)
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
// surfaces (R11 audit table SOURCE/FALLBACK columns and R12 stderr
// emission).
//
// The label has two parts: a fixed source identifier
// ("personal-overlay") that names where credential-sync sources live,
// and an optional disambiguator (the provider's name, when set) for
// users who eventually configure multiple credential-sync providers
// in the same overlay.
//
// Renderings:
//   - Anonymous personal-overlay vault (the only credential-sync
//     shape Alt 2 currently allows): "vault:personal-overlay".
//   - Named personal-overlay vault (reserved for a future extension
//     where named providers may serve as credential-sync sources):
//     "vault:personal-overlay(<name>)".
//
// The source label is consistent regardless of name because, under
// the Alt 2 design, every credential-sync source lives in the
// personal overlay — that's the consent property of declaring
// `[global.vault.provider]`. Names disambiguate among siblings; they
// never identify a different origin.
//
// Never emits a bare "vault:" with a trailing colon.
func renderVaultProvider(name string) string {
	const sourceLabel = "vault:personal-overlay"
	if name == "" {
		return sourceLabel
	}
	return sourceLabel + "(" + name + ")"
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
