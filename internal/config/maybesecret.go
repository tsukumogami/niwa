package config

import (
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// MaybeSecret is the sum type carried by every string slot that can
// hold either a plaintext value or a resolved vault secret. Exactly one
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
