package vault_test

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// TestParseRefAnonymous covers the "vault://key" form: empty
// ProviderName, key in host segment, no Optional.
func TestParseRefAnonymous(t *testing.T) {
	ref, err := vault.ParseRef("vault://api-token")
	if err != nil {
		t.Fatalf("ParseRef(vault://api-token) returned error: %v", err)
	}
	if ref.ProviderName != "" {
		t.Fatalf("ProviderName = %q, want empty", ref.ProviderName)
	}
	if ref.Key != "api-token" {
		t.Fatalf("Key = %q, want %q", ref.Key, "api-token")
	}
	if ref.Optional {
		t.Fatalf("Optional = true, want false")
	}
}

// TestParseRefNamed covers the "vault://name/key" form.
func TestParseRefNamed(t *testing.T) {
	ref, err := vault.ParseRef("vault://team-vault/db-password")
	if err != nil {
		t.Fatalf("ParseRef returned error: %v", err)
	}
	if ref.ProviderName != "team-vault" {
		t.Fatalf("ProviderName = %q, want %q", ref.ProviderName, "team-vault")
	}
	if ref.Key != "db-password" {
		t.Fatalf("Key = %q, want %q", ref.Key, "db-password")
	}
	if ref.Optional {
		t.Fatalf("Optional = true, want false")
	}
}

// TestParseRefRequiredFalse asserts AC: ?required=false sets
// Ref.Optional to true; anything else leaves it false.
func TestParseRefRequiredFalse(t *testing.T) {
	cases := []struct {
		uri      string
		wantOpt  bool
		wantName string
		wantKey  string
	}{
		{"vault://api-token?required=false", true, "", "api-token"},
		{"vault://api-token?required=true", false, "", "api-token"},
		{"vault://team/db?required=false", true, "team", "db"},
		{"vault://team/db?required=true", false, "team", "db"},
		{"vault://team/db?required=0", true, "team", "db"},
		{"vault://team/db?required=1", false, "team", "db"},
	}
	for _, c := range cases {
		t.Run(c.uri, func(t *testing.T) {
			ref, err := vault.ParseRef(c.uri)
			if err != nil {
				t.Fatalf("ParseRef returned error: %v", err)
			}
			if ref.Optional != c.wantOpt {
				t.Fatalf("Optional = %v, want %v", ref.Optional, c.wantOpt)
			}
			if ref.ProviderName != c.wantName {
				t.Fatalf("ProviderName = %q, want %q", ref.ProviderName, c.wantName)
			}
			if ref.Key != c.wantKey {
				t.Fatalf("Key = %q, want %q", ref.Key, c.wantKey)
			}
		})
	}
}

// TestParseRefRoundTripAC is the AC-named test: ParseRef round-trips
// vault://key and vault://name/key?required=false; rejects malformed
// URIs.
func TestParseRefRoundTripAC(t *testing.T) {
	ref1, err := vault.ParseRef("vault://key")
	if err != nil {
		t.Fatalf("ParseRef(vault://key) returned error: %v", err)
	}
	if ref1.ProviderName != "" || ref1.Key != "key" || ref1.Optional {
		t.Fatalf("vault://key did not parse as anonymous required key: %+v", ref1)
	}

	ref2, err := vault.ParseRef("vault://name/key?required=false")
	if err != nil {
		t.Fatalf("ParseRef(vault://name/key?required=false) returned error: %v", err)
	}
	if ref2.ProviderName != "name" || ref2.Key != "key" || !ref2.Optional {
		t.Fatalf("vault://name/key?required=false did not parse as optional: %+v", ref2)
	}
}

// TestParseRefRejectsMalformed asserts AC: malformed URIs return
// descriptive errors. Each case exercises a distinct rejection
// branch in the parser.
func TestParseRefRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"empty", ""},
		{"wrong scheme", "http://x"},
		{"no scheme", "key"},
		{"missing key", "vault://"},
		{"nested slashes", "vault://name/key/sub"},
		{"empty provider with path", "vault:///key"},
		{"unknown query param", "vault://key?foo=bar"},
		{"bogus required value", "vault://key?required=maybe"},
		{"fragment", "vault://key#frag"},
		{"userinfo", "vault://user:pw@name/key"},
		{"repeated required", "vault://key?required=true&required=false"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := vault.ParseRef(c.uri)
			if err == nil {
				t.Fatalf("ParseRef(%q) returned no error, want descriptive error", c.uri)
			}
			if !strings.Contains(err.Error(), "vault:") {
				t.Fatalf("error message should identify vault package: %v", err)
			}
		})
	}
}
