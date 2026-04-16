package vault

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// vaultScheme is the URI scheme recognised by ParseRef.
const vaultScheme = "vault"

// ParseRef parses a vault:// URI into a Ref. Accepted shapes:
//
//	vault://key                        // anonymous provider
//	vault://name/key                   // named provider
//	vault://key?required=false         // anonymous provider, optional
//	vault://name/key?required=false    // named provider, optional
//
// The only accepted query parameter is "required"; any other
// parameter is rejected with a descriptive error. An invalid boolean
// value for required is also rejected. required=true is accepted but
// is the default, so Ref.Optional is left false.
//
// ParseRef is strict: nested slashes beyond one separator, an empty
// key, or a non-vault scheme all return an error. The returned error
// is suitable for direct inclusion in user-facing messages; it never
// contains secret material because the input URI itself is non-
// secret (the key identifies, the stored value does not appear in
// the URI).
func ParseRef(uri string) (Ref, error) {
	if uri == "" {
		return Ref{}, fmt.Errorf("vault: empty ref")
	}

	// We check the scheme manually first because url.Parse is very
	// permissive with non-URI inputs and we want a crisp error for
	// the common typos (missing "vault://", wrong scheme).
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

	// net/url treats "vault://foo/bar" as Host="foo", Path="/bar"
	// and "vault://foo" as Host="foo", Path="". We rely on that
	// split but normalize aggressively so malformed inputs surface
	// as errors rather than silently resolving.
	host := parsed.Host
	path := strings.TrimPrefix(parsed.Path, "/")

	if parsed.User != nil {
		return Ref{}, fmt.Errorf("vault: ref %q must not contain userinfo", uri)
	}
	if parsed.Fragment != "" || strings.Contains(uri, "#") {
		return Ref{}, fmt.Errorf("vault: ref %q must not contain a fragment", uri)
	}

	var providerName, key string
	switch {
	case host == "" && path == "":
		return Ref{}, fmt.Errorf("vault: ref %q has empty key", uri)
	case host == "":
		// vault:///key — empty host, path holds key. Reject.
		return Ref{}, fmt.Errorf("vault: ref %q has empty provider segment", uri)
	case path == "":
		// vault://key — host holds the key; anonymous provider.
		providerName = ""
		key = host
	default:
		// vault://name/key — host is provider name, path is key.
		providerName = host
		key = path
	}

	// Nested slashes beyond one separator are rejected. The key
	// segment itself may not contain further slashes: the design
	// commits to flat keys (see DESIGN Decision 3 Ref shape).
	if strings.Contains(key, "/") {
		return Ref{}, fmt.Errorf("vault: ref %q has nested slashes in key segment", uri)
	}
	if key == "" {
		return Ref{}, fmt.Errorf("vault: ref %q has empty key", uri)
	}

	ref := Ref{ProviderName: providerName, Key: key}

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
