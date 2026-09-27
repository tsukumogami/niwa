package onboard

import (
	"strings"
	"testing"
)

func TestSanitize_ControlBytesEscaped(t *testing.T) {
	cases := map[string]string{
		"\x1b[31mred\x1b[0m": `\x1b[31mred\x1b[0m`,
		"line1\r\nline2":     `line1\x0d\x0aline2`,
		"tab\there":          `tab\x09here`,
	}
	for in, want := range cases {
		got := Sanitize(in)
		if got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitize_PrintablePassthrough(t *testing.T) {
	in := "identity-name_123 (env: prod)"
	if got := Sanitize(in); got != in {
		t.Errorf("Sanitize(%q) = %q, want unchanged", in, got)
	}
}

func TestSanitize_PrintableNonASCIIPassthrough(t *testing.T) {
	// Sanitize itself does not homoglyph-normalize -- that's
	// SanitizeHost's job. A printable non-ASCII rune outside a
	// hostname context passes through unchanged.
	in := "café"
	if got := Sanitize(in); got != in {
		t.Errorf("Sanitize(%q) = %q, want unchanged", in, got)
	}
}

func TestSanitizeHost_ASCIIPassthrough(t *testing.T) {
	in := "app.infisical.com"
	if got := SanitizeHost(in); got != in {
		t.Errorf("SanitizeHost(%q) = %q, want unchanged", in, got)
	}
}

func TestSanitizeHost_AlreadyPunycodePassthrough(t *testing.T) {
	in := "xn--mnchen-3ya.de"
	if got := SanitizeHost(in); got != in {
		t.Errorf("SanitizeHost(%q) = %q, want unchanged", in, got)
	}
}

func TestSanitizeHost_PunycodeNormalizesNonASCIILabel(t *testing.T) {
	got := SanitizeHost("münchen.de")
	want := "xn--mnchen-3ya.de"
	if got != want {
		t.Errorf("SanitizeHost(münchen.de) = %q, want %q", got, want)
	}
}

func TestSanitizeHost_HomoglyphIsVisiblyPunycoded(t *testing.T) {
	// U+043E CYRILLIC SMALL LETTER O standing in for Latin "o" --
	// visually near-indistinguishable from "google.com" in most fonts.
	homoglyph := "gоogle.com"
	got := SanitizeHost(homoglyph)
	if got == homoglyph {
		t.Fatalf("SanitizeHost did not transform the homoglyph host")
	}
	if got == "google.com" {
		t.Fatalf("SanitizeHost collapsed the homoglyph into the real domain -- must stay visibly distinct")
	}
	labels := strings.Split(got, ".")
	if len(labels) == 0 || !strings.HasPrefix(labels[0], "xn--") {
		t.Errorf("SanitizeHost(%q) = %q, want first label prefixed xn--", homoglyph, got)
	}
}

func TestSanitizeHost_PortPreserved(t *testing.T) {
	got := SanitizeHost("münchen.de:8443")
	want := "xn--mnchen-3ya.de:8443"
	if got != want {
		t.Errorf("SanitizeHost with port = %q, want %q", got, want)
	}
}

func TestSanitizeHost_IPv4Passthrough(t *testing.T) {
	in := "127.0.0.1"
	if got := SanitizeHost(in); got != in {
		t.Errorf("SanitizeHost(%q) = %q, want unchanged", in, got)
	}
}

func TestSanitizeURL_SanitizesHostInFullURL(t *testing.T) {
	got := SanitizeURL("https://münchen.de/api/v1/foo")
	want := "https://xn--mnchen-3ya.de/api/v1/foo"
	if got != want {
		t.Errorf("SanitizeURL = %q, want %q", got, want)
	}
}

func TestSanitizeURL_HomoglyphHostSurvivesControlBytesElsewhereInTheURL(t *testing.T) {
	// net/url.Parse rejects any string carrying a raw control byte
	// anywhere in it -- not just in the host -- so a homoglyph host
	// paired with an incidental control byte in the path must not fall
	// through to a no-host-awareness fallback and lose punycode
	// protection. QA-found regression.
	cyrillicHomoglyph := "gооgle.com" // U+043E Cyrillic о x2, standing in for Latin o
	raw := "https://" + cyrillicHomoglyph + "/path\r\x1b[2Ksegment"

	got := SanitizeURL(raw)

	if strings.Contains(got, cyrillicHomoglyph) {
		t.Fatalf("SanitizeURL(%q) = %q, still contains the raw homoglyph host -- punycode protection was lost", raw, got)
	}
	if !strings.Contains(got, "xn--") {
		t.Fatalf("SanitizeURL(%q) = %q, want the homoglyph host rendered as its xn-- form", raw, got)
	}
	if strings.ContainsAny(got, "\r\x1b") {
		t.Fatalf("SanitizeURL(%q) = %q, still contains a raw control byte", raw, got)
	}
}

func TestSanitizeURL_FallsBackOnUnparseable(t *testing.T) {
	in := "not\x1ba-url"
	got := SanitizeURL(in)
	if got != Sanitize(in) {
		t.Errorf("SanitizeURL(%q) = %q, want Sanitize fallback %q", in, got, Sanitize(in))
	}
}

func TestPunycodeEncode_KnownVector(t *testing.T) {
	got, err := punycodeEncode([]rune("münchen"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "mnchen-3ya"
	if got != want {
		t.Errorf("punycodeEncode(münchen) = %q, want %q", got, want)
	}
}

func TestPunycodeEncode_AllASCIIInputGetsTrailingDelimiter(t *testing.T) {
	// Not a round trip -- the raw Bootstring primitive always appends
	// the "-" delimiter after any basic-code-point run, even when
	// there's nothing non-ASCII left to encode after it. This only
	// exercises that low-level behavior directly; toASCIILabel never
	// calls punycodeEncode for an all-ASCII label in the first place
	// (see TestSanitizeHost_ASCIIPassthrough), so callers never see
	// this trailing dash.
	got, err := punycodeEncode([]rune("plain"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "plain-" {
		t.Errorf("punycodeEncode(plain) = %q, want plain-", got)
	}
}
