package vault

import (
	"context"

	"github.com/tsukumogami/niwa/internal/secret"
)

// ScrubStderr runs raw through the redaction machinery and returns
// the scrubbed string. It is the canonical entry point for surfacing
// captured provider-CLI stderr into error messages: callers MUST
// route raw stderr through ScrubStderr before interpolating it into
// any error string, log line, or user-facing output.
//
// ScrubStderr applies two layers in sequence:
//
//  1. The Redactor attached to ctx via secret.WithRedactor, if any.
//     This catches fragments registered earlier in the resolve-call
//     lifecycle — e.g., values successfully fetched for other keys
//     before the failing one.
//
//  2. A fresh Redactor seeded with every Value in known. This catches
//     fragments that may appear in the stderr even though the Value
//     was not registered on the context redactor (e.g., an auth-
//     failure stderr that echoes part of a supplied token).
//
// Both layers are no-ops for empty input or when no fragments apply.
// ScrubStderr never returns raw stderr unmodified without first
// running it through at least the known-fragments pass.
//
// ScrubStderr does NOT wrap or return an error — it returns a
// scrubbed string for the caller to interpolate into a secret.Errorf
// (or similar) message. The ctx parameter is consulted solely for
// its attached Redactor; cancellation is not checked.
func ScrubStderr(ctx context.Context, raw []byte, known ...secret.Value) string {
	if len(raw) == 0 {
		return ""
	}
	out := string(raw)

	// Layer 1: context-scoped redactor, if present.
	if r := secret.RedactorFrom(ctx); r != nil {
		out = r.Scrub(out)
	}

	// Layer 2: a fresh redactor seeded with the known-fragments
	// deny-list. A fresh redactor avoids accidentally mutating the
	// context's redactor with fragments the caller only wanted to
	// apply here.
	if len(known) > 0 {
		local := secret.NewRedactor()
		for _, v := range known {
			local.RegisterValue(v)
		}
		out = local.Scrub(out)
	}

	return out
}
