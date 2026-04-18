package vault

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// vaultScheme is the URI scheme recognised by ParseRef.
const vaultScheme = "vault"

// ParseMode tells ParseRef which grammar the URI is expected to follow.
// The caller supplies the mode because the URI shape (anonymous vs
// named) cannot be inferred from the URI alone — it follows the
// provider-declaration shape of the file the URI appears in.
type ParseMode int

const (
	// ParseAnonymous parses URIs of the form
	// vault://[<path-segments.../>]<key>[?required=<bool>]. Leading
	// segments (zero or more) populate Ref.Path as "/seg1/seg2/..."
	// (leading slash, no trailing slash). The final segment is Ref.Key.
	// Ref.ProviderName is always empty. Nested slashes are permitted.
	ParseAnonymous ParseMode = iota

	// ParseNamed parses URIs of the form
	// vault://<name>/<key>[?required=<bool>]. The first segment is
	// Ref.ProviderName; the second is Ref.Key. Ref.Path is always
	// empty. Nested slashes beyond the single name/key separator are
	// rejected — named-provider URIs with folder paths are not
	// supported in this iteration.
	ParseNamed
)

// ParseRef parses a vault:// URI into a Ref using the given mode. The
// accepted shape depends on mode:
//
//   ParseAnonymous:
//     vault://key                               → Key="key"
//     vault://folder/key                        → Path="/folder", Key="key"
//     vault://folder/sub/key                    → Path="/folder/sub", Key="key"
//     vault://key?required=false                → Key="key", Optional=true
//
//   ParseNamed:
//     vault://name/key                          → ProviderName="name", Key="key"
//     vault://name/key?required=false           → …, Optional=true
//     vault://key                               → error (named requires name/key)
//     vault://name/folder/key                   → error (nested slashes rejected)
//
// The only accepted query parameter is "required"; any other
// parameter is rejected. required=true is accepted but is the default,
// so Ref.Optional is left false in that case.
//
// See DESIGN-vault-integration.md Decision 7 for the rationale
// behind the two grammars and the per-resolve Path contract.
func ParseRef(uri string, mode ParseMode) (Ref, error) {
	if uri == "" {
		return Ref{}, fmt.Errorf("vault: empty ref")
	}

	// Check the scheme manually first because url.Parse is permissive
	// with non-URI inputs and we want a crisp error for common typos
	// (missing "vault://", wrong scheme).
	if !strings.HasPrefix(uri, vaultScheme+"://") {
		return Ref{}, fmt.Errorf("vault: ref %q must start with %q", uri, vaultScheme+"://")
	}

	parsed, err := url.Parse(uri)
	if err != nil {
		return Ref{}, fmt.Errorf("vault: ref %q is not a valid URI: %w", uri, err)
	}
	if parsed.Scheme != vaultScheme {
		return Ref{}, fmt.Errorf("vault: ref %q has scheme %q, want %q", uri, parsed.Scheme, vaultScheme)
	}
	if parsed.User != nil {
		return Ref{}, fmt.Errorf("vault: ref %q must not contain userinfo", uri)
	}
	if parsed.Fragment != "" || strings.Contains(uri, "#") {
		return Ref{}, fmt.Errorf("vault: ref %q must not contain a fragment", uri)
	}

	// net/url treats "vault://foo/bar" as Host="foo", Path="/bar" and
	// "vault://foo" as Host="foo", Path="". Reassemble so we have a
	// single "segments" view for downstream mode-specific handling.
	host := parsed.Host
	pathAfterHost := strings.TrimPrefix(parsed.Path, "/")

	// Reject vault:///key (empty host). That shape is reserved; callers
	// should write vault://key (anonymous) or vault://name/key (named).
	if host == "" {
		if pathAfterHost == "" {
			return Ref{}, fmt.Errorf("vault: ref %q has empty key", uri)
		}
		return Ref{}, fmt.Errorf("vault: ref %q has empty provider segment", uri)
	}

	// Assemble the full path (segments after "vault://") for mode-specific
	// parsing. "vault://a/b/c" → segments ["a", "b", "c"].
	var segments []string
	if pathAfterHost == "" {
		segments = []string{host}
	} else {
		segments = append([]string{host}, strings.Split(pathAfterHost, "/")...)
	}
	// Reject any empty segment (e.g. "vault://a//b") since it almost
	// always indicates a user-authoring bug and makes path-join
	// semantics ambiguous.
	for _, s := range segments {
		if s == "" {
			return Ref{}, fmt.Errorf("vault: ref %q has an empty segment", uri)
		}
	}

	var ref Ref
	switch mode {
	case ParseAnonymous:
		// Anonymous form: leading segments are the folder path; final
		// segment is the key. A single-segment URI is just vault://key
		// with no path.
		ref.Key = segments[len(segments)-1]
		if len(segments) > 1 {
			ref.Path = "/" + strings.Join(segments[:len(segments)-1], "/")
		}
	case ParseNamed:
		// Named form: exactly two segments (name + key). Fewer or more
		// is an error.
		if len(segments) == 1 {
			return Ref{}, fmt.Errorf(
				"vault: ref %q must use named form vault://<name>/<key> in this file",
				uri,
			)
		}
		if len(segments) > 2 {
			return Ref{}, fmt.Errorf(
				"vault: ref %q has nested slashes; named URIs accept only vault://<name>/<key>",
				uri,
			)
		}
		ref.ProviderName = segments[0]
		ref.Key = segments[1]
	default:
		return Ref{}, fmt.Errorf("vault: ParseRef called with unknown mode %d", mode)
	}

	if ref.Key == "" {
		return Ref{}, fmt.Errorf("vault: ref %q has empty key", uri)
	}

	// Query parameter handling. Only "required" is accepted.
	rawQuery := parsed.RawQuery
	if rawQuery != "" {
		q, qerr := url.ParseQuery(rawQuery)
		if qerr != nil {
			return Ref{}, fmt.Errorf("vault: ref %q has invalid query: %w", uri, qerr)
		}
		for param, values := range q {
			if param != "required" {
				return Ref{}, fmt.Errorf("vault: ref %q has unknown query parameter %q", uri, param)
			}
			if len(values) != 1 {
				return Ref{}, fmt.Errorf("vault: ref %q repeats query parameter %q", uri, param)
			}
			reqVal, berr := strconv.ParseBool(values[0])
			if berr != nil {
				return Ref{}, fmt.Errorf("vault: ref %q has invalid required value %q: %w", uri, values[0], berr)
			}
			ref.Optional = !reqVal
		}
	}

	return ref, nil
}
