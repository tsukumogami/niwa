// Package reveal is the single plaintext-access surface for
// secret.Value. Its sole exported function, UnsafeReveal, returns
// the raw plaintext bytes of a Value.
//
// The deliberately unsafe-sounding name signals to code reviewers
// that every call site warrants scrutiny. The package lives in its
// own directory so a future go/analysis linter can allow-list
// legitimate callers — currently the workspace materializers and
// the vault provider implementations — and flag every other
// importer.
//
// DO NOT import this package from new code without explicit review.
// If you need to feed plaintext into a log, JSON payload, error
// message, or environment variable, the answer is almost always
// "don't": use the secret.Value directly, or surface an Origin for
// user-facing identifiers.
package reveal

import "github.com/tsukumogami/niwa/internal/secret"

// UnsafeReveal returns the plaintext bytes carried by v. The
// returned slice is the underlying buffer of the Value; callers MUST
// NOT retain or mutate it past the boundary of the short-lived
// operation that legitimately needs plaintext (e.g., writing an env
// file).
//
// For empty Values, UnsafeReveal returns nil.
func UnsafeReveal(v secret.Value) []byte {
	return secret.Bytes(v)
}
