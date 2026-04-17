// Package secret provides an opaque Value type for carrying sensitive
// material through niwa without risking accidental disclosure via any
// of Go's standard emission paths (formatters, JSON/text marshalers,
// gob encoding, error interpolation).
//
// The package implements Decision 2 of the vault-integration design:
//
//   - Value is a struct with private plaintext bytes. Every standard
//     Go emission path returns a redacted placeholder ("***").
//   - Error wraps any error whose chain may contain secret fragments;
//     its Error() output is scrubbed by a per-context Redactor before
//     being returned to callers.
//   - Redactor accumulates known secret fragments and performs
//     longest-first substring replacement. Fragments shorter than six
//     bytes are refused (they collide too often with ordinary log
//     text to be safely replaced).
//
// Plaintext bytes are accessible only through internal/secret/reveal's
// UnsafeReveal. That deliberately unsafe-sounding name lives in its
// own sub-package so a future linter can allow-list legitimate callers
// (materializers, vault providers) and flag everything else.
package secret

import (
	"encoding/json"
	"errors"
	"fmt"
)

// errGobRefused is returned by Value.GobEncode. gob encoding is
// refused outright because the wire format has no hook analogous to
// MarshalJSON/MarshalText that we can use to emit a redacted
// placeholder without also leaking type metadata about the Value.
var errGobRefused = errors.New("secret.Value: gob encoding is refused to prevent plaintext leakage")

// Origin carries non-secret metadata about where a Value came from.
// It is safe to log, serialize, or display. No plaintext ever lives
// on Origin; populate only provider-side identifiers.
type Origin struct {
	// ProviderName is the user-facing provider name, e.g., the
	// anonymous singular "" or a named provider like "team-vault".
	ProviderName string
	// Key is the path portion of the vault reference (the part
	// after "vault://[name]/"). Treated as non-secret: it's the
	// lookup key, not the stored value.
	Key string
	// VersionToken is the provider-side opaque version identifier
	// (e.g., a commit SHA, audit-log token, or revision ID). Used
	// for rotation diagnostics. Empty when the provider does not
	// expose versions.
	VersionToken string
}

// Value is an opaque holder for a single secret's plaintext bytes.
//
// The zero value (Value{}) is a legal empty secret: IsEmpty returns
// true, and all formatter paths still redact to "***".
//
// All methods that Go's fmt, encoding/json, encoding, and encoding/gob
// packages consult return redacted output. Plaintext bytes are only
// accessible through internal/secret/reveal.UnsafeReveal. This
// preserves R22's redact-logs invariant across every Go emission path
// we can control: %s/%v/%+v/%q/%#v, MarshalJSON, MarshalText, and a
// refusing GobEncode. It does NOT stop deliberate misuse such as
// unsafe pointer reads; the goal is to make accidental leakage
// impossible and intentional plaintext access grep-able.
type Value struct {
	b      []byte
	origin Origin
}

// New constructs a Value carrying the given plaintext bytes and
// origin metadata. New copies the input slice so callers may reuse
// or zero their buffer after the call returns.
func New(plaintext []byte, origin Origin) Value {
	if len(plaintext) == 0 {
		return Value{origin: origin}
	}
	buf := make([]byte, len(plaintext))
	copy(buf, plaintext)
	return Value{b: buf, origin: origin}
}

// IsEmpty reports whether the Value carries no plaintext bytes. The
// zero Value is empty.
func (v Value) IsEmpty() bool {
	return len(v.b) == 0
}

// Origin returns the non-secret metadata associated with the Value.
// The returned Origin may be freely logged or serialized.
func (v Value) Origin() Origin {
	return v.origin
}

// String returns the redacted placeholder "***".
//
// fmt's %s and %v verbs call String on types that implement
// fmt.Stringer and do not also implement fmt.Formatter. Value
// implements both, so String is primarily a safety net for callers
// that invoke it directly.
func (v Value) String() string {
	return "***"
}

// GoString returns the redacted placeholder used by the %#v verb.
func (v Value) GoString() string {
	return "secret.Value(***)"
}

// Format implements fmt.Formatter. Every verb — %s, %v, %+v, %q,
// %#v, and any other — emits a redacted placeholder. Width and
// precision flags are honored against the placeholder, not against
// the plaintext.
//
// We implement Format directly (rather than relying on Stringer)
// because %q and %#v do not consult String/GoString in the obvious
// way: %q of a Stringer double-quotes its String output, but callers
// occasionally reach into the underlying type via reflection or
// wrapper types. A direct Format implementation intercepts all
// standard verbs unconditionally.
func (v Value) Format(s fmt.State, verb rune) {
	var placeholder string
	switch {
	case verb == 'v' && s.Flag('#'):
		placeholder = "secret.Value(***)"
	case verb == 'q':
		placeholder = `"***"`
	default:
		placeholder = "***"
	}
	_, _ = fmt.Fprint(s, placeholder)
}

// MarshalJSON implements json.Marshaler, emitting the quoted
// placeholder "\"***\"". Returned bytes are valid JSON.
func (v Value) MarshalJSON() ([]byte, error) {
	return json.Marshal("***")
}

// MarshalText implements encoding.TextMarshaler, emitting the
// unquoted placeholder "***".
func (v Value) MarshalText() ([]byte, error) {
	return []byte("***"), nil
}

// GobEncode implements gob.GobEncoder by refusing to encode. gob
// provides no hook to emit a redacted stand-in without also writing
// type metadata, so the safest behavior is to fail loudly.
func (v Value) GobEncode() ([]byte, error) {
	return nil, errGobRefused
}

// bytes returns the underlying plaintext. This package-private
// helper is used by the error-wrapping machinery when it registers
// fragments on a Redactor.
func (v Value) bytes() []byte {
	return v.b
}

// Bytes returns the underlying plaintext bytes of v. It exists
// solely so internal/secret/reveal can expose UnsafeReveal without
// giving every importer of internal/secret the same power.
//
// This function is NOT part of the public API contract of this
// package and should not be called outside internal/secret/reveal.
// The sub-package split is the grep-able review surface; this
// export is the plumbing that makes it work.
func Bytes(v Value) []byte {
	return v.b
}
