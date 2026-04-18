package vault_test

import (
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/vault"
)

// TestParseRefAnonymousNoPath covers the "vault://key" form under
// ParseAnonymous: empty ProviderName, empty Path, key in final segment.
func TestParseRefAnonymousNoPath(t *testing.T) {
	ref, err := vault.ParseRef("vault://api-token", vault.ParseAnonymous)
	if err != nil {
		t.Fatalf("ParseRef(vault://api-token) returned error: %v", err)
	}
	if ref.ProviderName != "" {
		t.Fatalf("ProviderName = %q, want empty", ref.ProviderName)
	}
	if ref.Path != "" {
		t.Fatalf("Path = %q, want empty", ref.Path)
	}
	if ref.Key != "api-token" {
		t.Fatalf("Key = %q, want %q", ref.Key, "api-token")
	}
	if ref.Optional {
		t.Fatalf("Optional = true, want false")
	}
}

// TestParseRefAnonymousWithPath covers vault://<folder>/<key> under
// ParseAnonymous. The leading segment becomes Ref.Path (with a leading
// slash); the final segment is Ref.Key. ProviderName stays empty.
func TestParseRefAnonymousWithPath(t *testing.T) {
	ref, err := vault.ParseRef("vault://codespar/ANTHROPIC_API_KEY", vault.ParseAnonymous)
	if err != nil {
		t.Fatalf("ParseRef returned error: %v", err)
	}
	if ref.ProviderName != "" {
		t.Fatalf("ProviderName = %q, want empty", ref.ProviderName)
	}
	if ref.Path != "/codespar" {
		t.Fatalf("Path = %q, want %q", ref.Path, "/codespar")
	}
	if ref.Key != "ANTHROPIC_API_KEY" {
		t.Fatalf("Key = %q, want %q", ref.Key, "ANTHROPIC_API_KEY")
	}
}

// TestParseRefAnonymousDeepPath covers multi-segment folder paths.
func TestParseRefAnonymousDeepPath(t *testing.T) {
	ref, err := vault.ParseRef("vault://a/b/c/d", vault.ParseAnonymous)
	if err != nil {
		t.Fatalf("ParseRef returned error: %v", err)
	}
	if ref.Path != "/a/b/c" {
		t.Fatalf("Path = %q, want %q", ref.Path, "/a/b/c")
	}
	if ref.Key != "d" {
		t.Fatalf("Key = %q, want %q", ref.Key, "d")
	}
}

// TestParseRefNamed covers the "vault://name/key" form under ParseNamed.
func TestParseRefNamed(t *testing.T) {
	ref, err := vault.ParseRef("vault://team-vault/db-password", vault.ParseNamed)
	if err != nil {
		t.Fatalf("ParseRef returned error: %v", err)
	}
	if ref.ProviderName != "team-vault" {
		t.Fatalf("ProviderName = %q, want %q", ref.ProviderName, "team-vault")
	}
	if ref.Path != "" {
		t.Fatalf("Path = %q, want empty", ref.Path)
	}
	if ref.Key != "db-password" {
		t.Fatalf("Key = %q, want %q", ref.Key, "db-password")
	}
	if ref.Optional {
		t.Fatalf("Optional = true, want false")
	}
}

// TestParseRefNamedRejectsBareKey asserts that ParseNamed requires the
// name/key form. A bare "vault://key" must fail with a shape-specific
// error so config validation can surface the right remediation.
func TestParseRefNamedRejectsBareKey(t *testing.T) {
	_, err := vault.ParseRef("vault://key", vault.ParseNamed)
	if err == nil {
		t.Fatal("expected error for bare-key URI in ParseNamed mode")
	}
	if !strings.Contains(err.Error(), "named form") {
		t.Fatalf("error should mention named form: %v", err)
	}
}

// TestParseRefNamedRejectsNestedSlashes asserts that ParseNamed
// continues to reject URIs with more than one slash — folder-path
// segments are only accepted under ParseAnonymous.
func TestParseRefNamedRejectsNestedSlashes(t *testing.T) {
	_, err := vault.ParseRef("vault://name/folder/key", vault.ParseNamed)
	if err == nil {
		t.Fatal("expected error for nested-slash URI in ParseNamed mode")
	}
	if !strings.Contains(err.Error(), "nested slashes") {
		t.Fatalf("error should mention nested slashes: %v", err)
	}
}

// TestParseRefRequiredFalse asserts that ?required=false sets
// Ref.Optional to true; any truthy value leaves it false. Covers both
// modes.
func TestParseRefRequiredFalse(t *testing.T) {
	cases := []struct {
		uri      string
		mode     vault.ParseMode
		wantOpt  bool
		wantName string
		wantPath string
		wantKey  string
	}{
		{"vault://api-token?required=false", vault.ParseAnonymous, true, "", "", "api-token"},
		{"vault://api-token?required=true", vault.ParseAnonymous, false, "", "", "api-token"},
		{"vault://folder/key?required=false", vault.ParseAnonymous, true, "", "/folder", "key"},
		{"vault://team/db?required=false", vault.ParseNamed, true, "team", "", "db"},
		{"vault://team/db?required=true", vault.ParseNamed, false, "team", "", "db"},
		{"vault://team/db?required=0", vault.ParseNamed, true, "team", "", "db"},
		{"vault://team/db?required=1", vault.ParseNamed, false, "team", "", "db"},
	}
	for _, c := range cases {
		t.Run(c.uri, func(t *testing.T) {
			ref, err := vault.ParseRef(c.uri, c.mode)
			if err != nil {
				t.Fatalf("ParseRef returned error: %v", err)
			}
			if ref.Optional != c.wantOpt {
				t.Fatalf("Optional = %v, want %v", ref.Optional, c.wantOpt)
			}
			if ref.ProviderName != c.wantName {
				t.Fatalf("ProviderName = %q, want %q", ref.ProviderName, c.wantName)
			}
			if ref.Path != c.wantPath {
				t.Fatalf("Path = %q, want %q", ref.Path, c.wantPath)
			}
			if ref.Key != c.wantKey {
				t.Fatalf("Key = %q, want %q", ref.Key, c.wantKey)
			}
		})
	}
}

// TestParseRefRejectsMalformed asserts that malformed URIs return
// descriptive errors under both modes.
func TestParseRefRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		mode vault.ParseMode
	}{
		{"empty anon", "", vault.ParseAnonymous},
		{"empty named", "", vault.ParseNamed},
		{"wrong scheme", "http://x", vault.ParseAnonymous},
		{"no scheme", "key", vault.ParseAnonymous},
		{"missing key anon", "vault://", vault.ParseAnonymous},
		{"missing key named", "vault://", vault.ParseNamed},
		{"empty provider with path", "vault:///key", vault.ParseAnonymous},
		{"empty segment", "vault://a//b", vault.ParseAnonymous},
		{"unknown query param", "vault://key?foo=bar", vault.ParseAnonymous},
		{"bogus required value", "vault://key?required=maybe", vault.ParseAnonymous},
		{"fragment", "vault://key#frag", vault.ParseAnonymous},
		{"userinfo", "vault://user:pw@name/key", vault.ParseNamed},
		{"repeated required", "vault://key?required=true&required=false", vault.ParseAnonymous},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := vault.ParseRef(c.uri, c.mode)
			if err == nil {
				t.Fatalf("ParseRef(%q) returned no error, want descriptive error", c.uri)
			}
			if !strings.Contains(err.Error(), "vault:") {
				t.Fatalf("error message should identify vault package: %v", err)
			}
		})
	}
}
