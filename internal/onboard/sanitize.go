package onboard

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"unicode"
)

// Sanitize strips or escapes control and non-printable bytes from s
// before it reaches a terminal, so a hostile config- or
// response-sourced value (an identity name, an environment slug, a
// guided-instruction token) can't emit ANSI cursor moves, a carriage
// return that redraws a previous line, or any other non-printable
// sequence. Every C0 control byte and DEL is escaped as \xHH; any
// other non-printable rune (per unicode.IsPrint) is escaped as \uHHHH
// or, for runes outside the Basic Multilingual Plane, \UHHHHHHHH.
// Printable characters, including printable non-ASCII characters,
// pass through unchanged -- SanitizeHost handles the separate
// homoglyph concern for hostnames specifically.
//
// This is applied by the prompt kit (Confirm, Select, Pause) to every
// prompt it displays, and should also be applied by any other
// guided-instruction output the wizard prints directly.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, "\\x%02x", r)
		case !unicode.IsPrint(r):
			if r > 0xffff {
				fmt.Fprintf(&b, "\\U%08x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// SanitizeHost renders host (optionally "host:port") in
// ASCII/punycode-normalized form: each dot-separated label containing
// a non-ASCII rune is Punycode-encoded and prefixed with "xn--", so a
// homoglyph domain (a Cyrillic "о" standing in for a Latin "o") is
// visible as its unambiguous xn-- form rather than passing as
// legitimate. A label that is already all-ASCII -- including an
// already-punycode "xn--..." label -- is left unchanged.
//
// Falls back to Sanitize(host) for input that parses as a literal IP
// address (nothing to punycode-normalize there) or that this
// implementation cannot encode.
func SanitizeHost(host string) string {
	h, port, err := net.SplitHostPort(host)
	if err != nil {
		h, port = host, ""
	}

	trimmed := strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if net.ParseIP(trimmed) != nil {
		return Sanitize(host)
	}

	labels := strings.Split(h, ".")
	for i, label := range labels {
		labels[i] = toASCIILabel(label)
	}
	out := strings.Join(labels, ".")
	if port != "" {
		out = out + ":" + port
	}
	return Sanitize(out)
}

// SanitizeURL formats a URL for terminal display: Sanitize handles
// control/non-printable bytes across the whole string, and
// SanitizeHost additionally punycode-normalizes the host component, so
// a homoglyph host is visible rather than passing as legitimate. Falls
// back to Sanitize(raw) when raw doesn't parse as a URL with a host --
// callers should not assume the result round-trips through url.Parse.
func SanitizeURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return Sanitize(raw)
	}
	u.Host = SanitizeHost(u.Host)
	return Sanitize(u.String())
}

// toASCIILabel converts one dot-separated hostname label to its
// ASCII/punycode form. A label that is already all-ASCII is returned
// unchanged. A label containing any non-ASCII rune is Punycode-encoded
// and prefixed with "xn--".
func toASCIILabel(label string) string {
	isASCII := true
	for _, r := range label {
		if r >= 0x80 {
			isASCII = false
			break
		}
	}
	if isASCII {
		return label
	}

	encoded, err := punycodeEncode([]rune(label))
	if err != nil {
		// This implementation could not encode the label -- fall back
		// to a plainly-visible escape rather than silently passing the
		// raw homoglyph through unrendered.
		return Sanitize(label)
	}
	return "xn--" + encoded
}
