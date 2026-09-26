package config

import (
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// UnresolvedCause classifies why a declared key ended up with no
// value. The set is deliberately closed: only shortfalls appear here.
// Configuration defects (a malformed reference, an unsupported
// reference version, an undecodable body, a missing required field)
// stay hard errors inside the resolver, because degrading them would
// make the tolerant default swallow configuration typos.
type UnresolvedCause string

const (
	// CauseKeyNotFound means a provider was configured, was reachable,
	// and reported that it does not hold the key. This is the one cause
	// that keeps a required key fatal.
	CauseKeyNotFound UnresolvedCause = "key-not-found"

	// CauseProviderUnreachable means a provider was configured but
	// could not be contacted: auth failure, network error, expired
	// session.
	CauseProviderUnreachable UnresolvedCause = "provider-unreachable"

	// CauseClientNotInstalled narrows CauseProviderUnreachable to the
	// case where the backend's client binary is absent from the host.
	// It is separate because the remedy differs: install the client,
	// rather than repair credentials or connectivity.
	CauseClientNotInstalled UnresolvedCause = "client-not-installed"

	// CauseUndeclaredProvider means the reference named a provider that
	// the merged configuration does not declare. It is NOT a
	// key-not-found: no provider was ever asked, so nothing establishes
	// that a reachable backend lacks the key.
	CauseUndeclaredProvider UnresolvedCause = "undeclared-provider"
)

// RequirementLevel is a key's placement in the required / recommended
// / optional sub-tables of the workspace configuration. It is a
// property of the declaration, not of a vault:// reference — the
// per-reference "?required=false" opt-out is a separate mechanism and
// never produces an Unresolved mark at all.
type RequirementLevel string

const (
	// LevelUnspecified is the zero value: the key appears in no
	// requirement sub-table. Settings keys, which have no sub-tables,
	// always carry this level.
	LevelUnspecified RequirementLevel = ""

	// LevelRequired mirrors the `*.required` sub-table.
	LevelRequired RequirementLevel = "required"

	// LevelRecommended mirrors the `*.recommended` sub-table.
	LevelRecommended RequirementLevel = "recommended"

	// LevelOptional mirrors the `*.optional` sub-table.
	LevelOptional RequirementLevel = "optional"
)

// Unresolved records why a MaybeSecret carries no value. It holds no
// secret material — only the key's declared metadata and the shape of
// the failure — so it is safe to render in a report or write into a
// generated file.
//
// An Unresolved is treated as immutable once attached. Config merging
// and deep-copying share the pointer rather than cloning the struct,
// which is what lets the mark inherit last-layer-wins for free: a
// later layer supplying a real value simply replaces the whole
// MaybeSecret, mark and all.
type Unresolved struct {
	// Cause is why the value is absent.
	Cause UnresolvedCause

	// Level is the key's declared requirement level, or
	// LevelUnspecified when it appears in no requirement sub-table.
	Level RequirementLevel

	// Description is the author-supplied text from the requirement
	// sub-table. Empty when the key is not declared there. This is
	// free text from a configuration file: treat it as untrusted when
	// rendering or writing it anywhere.
	Description string

	// ProviderKind is the backend kind of the provider that was asked
	// (for example "infisical"). Empty when no provider was reached —
	// notably for CauseUndeclaredProvider, where none was ever found.
	ProviderKind string
}

// MaybeSecret is the sum type carried by every string slot that can
// hold either a plaintext value or a resolved vault secret. At most one
// of Plain or Secret is populated at any given time:
//
//   - The parser produces MaybeSecret{Plain: "..."} from every TOML
//     string it decodes into a MaybeSecret slot. Values that begin with
//     "vault://" remain in Plain until the resolver sees them.
//   - The resolver (Issue 4) replaces MaybeSecret{Plain: "vault://..."}
//     with MaybeSecret{Secret: v, Token: t} after calling the configured
//     provider. Callers that need the plaintext bytes reach the Secret
//     via secret/reveal.UnsafeReveal; everything else uses String.
//
// Zero-value semantics: MaybeSecret{} is an "empty non-secret" and is
// equivalent to the field being absent. The parser never produces a
// zero MaybeSecret; it either populates Plain or leaves the containing
// map entry out entirely.
//
// A third state exists after resolution: neither Plain nor Secret is
// populated AND Unresolved is non-nil, meaning "this key has no value,
// and here is why". Only the resolver produces that state. Code that
// treats an empty value as absent stays correct without change; code
// that needs to tell a deliberate empty from a shortfall consults
// Unresolved.
type MaybeSecret struct {
	// Plain holds the literal TOML string as read by the parser. For
	// plaintext values this is the final value. For vault:// references
	// this is the URI until the resolver replaces it.
	Plain string

	// Secret carries the resolved plaintext wrapped in secret.Value.
	// Empty until the resolver runs.
	Secret secret.Value

	// Token carries the provider-opaque version identifier set by the
	// resolver alongside Secret. Empty until the resolver runs.
	Token vault.VersionToken

	// Unresolved is nil for every value that carries one, and non-nil
	// only for a value the resolver could not supply. It is metadata
	// about the absence, never part of the value: String and
	// MarshalText ignore it entirely, so the redaction promise those
	// two methods carry is unaffected.
	Unresolved *Unresolved
}

// IsUnresolved reports whether this slot carries a mark explaining why
// it has no value. A deliberate empty — an absent declaration, or a
// reference whose author opted out of resolution failure — returns
// false, which is what keeps the per-reference opt-out silent.
func (m MaybeSecret) IsUnresolved() bool {
	return m.Unresolved != nil
}

// IsSecret reports whether this slot carries resolved secret material.
// A parser-produced plaintext returns false even when its Plain value
// happens to begin with "vault://" — classification by the resolver is
// what promotes a value to secret status.
func (m MaybeSecret) IsSecret() bool {
	return !m.Secret.IsEmpty()
}

// String returns the redacted placeholder for secret values and the
// literal plaintext for non-secret values. String never emits plaintext
// that was resolved from a vault; the resolver-populated Secret field
// always redacts to "***".
//
// This method exists so that code which logs or formats configuration
// values does not need to branch on IsSecret.
//
// A marked (unresolved) value renders exactly as a zero value does:
// the empty string. The mark is not part of the value's textual form.
// Consumers that must show it read Unresolved directly rather than
// widening what this method may emit.
func (m MaybeSecret) String() string {
	if m.IsSecret() {
		return m.Secret.String()
	}
	return m.Plain
}

// UnmarshalText implements encoding.TextUnmarshaler so that TOML string
// values decode directly into MaybeSecret{Plain: "..."}. BurntSushi/toml
// dispatches primitive strings to TextUnmarshaler when the target type
// implements it.
//
// The parser does not reject vault:// URIs here because some slots (for
// example, [env.secrets] values) explicitly accept them. Slot-specific
// validation runs in a post-parse pass once the containing structure is
// known; see validate_vault_refs.go.
func (m *MaybeSecret) UnmarshalText(text []byte) error {
	m.Plain = string(text)
	return nil
}

// MarshalText implements encoding.TextMarshaler, emitting the redacted
// form for secret values and the literal Plain value otherwise. This
// keeps round-trips through encoding/json or other TextMarshaler-aware
// serializers from leaking plaintext.
func (m MaybeSecret) MarshalText() ([]byte, error) {
	return []byte(m.String()), nil
}
