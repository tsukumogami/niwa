package workspace

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tsukumogami/niwa/internal/config"
	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

func TestLoadProviderAuth_ValidFile(t *testing.T) {
	dir := t.TempDir()
	content := `
[[providers]]
kind          = "infisical"
project       = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
client_id     = "11111111-2222-3333-4444-555555555555"
client_secret = "abcdef01"
api_url       = "https://app.infisical.com/api"

[[providers]]
kind      = "onepassword"
vault     = "Engineering"
token     = "ops_fake"
`
	path := filepath.Join(dir, providerAuthFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, err := LoadProviderAuth(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(entries))
	}

	// First entry: Infisical.
	if entries[0].Kind != "infisical" {
		t.Errorf("entries[0].Kind = %q, want %q", entries[0].Kind, "infisical")
	}
	if entries[0].Config["project"] != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("entries[0].Config[project] = %v", entries[0].Config["project"])
	}
	if entries[0].Config["client_id"] != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("entries[0].Config[client_id] = %v", entries[0].Config["client_id"])
	}
	if entries[0].Config["client_secret"] != "abcdef01" {
		t.Errorf("entries[0].Config[client_secret] = %v", entries[0].Config["client_secret"])
	}
	if entries[0].Config["api_url"] != "https://app.infisical.com/api" {
		t.Errorf("entries[0].Config[api_url] = %v", entries[0].Config["api_url"])
	}

	// Second entry: 1Password.
	if entries[1].Kind != "onepassword" {
		t.Errorf("entries[1].Kind = %q, want %q", entries[1].Kind, "onepassword")
	}
	if entries[1].Config["vault"] != "Engineering" {
		t.Errorf("entries[1].Config[vault] = %v", entries[1].Config["vault"])
	}
}

func TestLoadProviderAuth_FileAbsent(t *testing.T) {
	dir := t.TempDir()
	entries, err := LoadProviderAuth(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("want nil entries for absent file, got %v", entries)
	}
}

func TestLoadProviderAuth_WrongPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, providerAuthFile)
	if err := os.WriteFile(path, []byte("[[providers]]\nkind = \"infisical\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProviderAuth(dir)
	if err == nil {
		t.Fatal("expected error for wrong permissions, got nil")
	}
	if got := err.Error(); !containsAll(got, "permissions", "0644", "0600") {
		t.Errorf("error = %q; want mention of permissions, 0644, and 0600", got)
	}
}

func TestLoadProviderAuth_MissingKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, providerAuthFile)
	content := `
[[providers]]
project = "some-uuid"
client_id = "abc"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProviderAuth(dir)
	if err == nil {
		t.Fatal("expected error for missing kind, got nil")
	}
	if got := err.Error(); !containsAll(got, "kind") {
		t.Errorf("error = %q; want mention of kind", got)
	}
}

func TestLoadProviderAuth_EmptyKind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, providerAuthFile)
	content := `
[[providers]]
kind = ""
project = "some-uuid"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProviderAuth(dir)
	if err == nil {
		t.Fatal("expected error for empty kind, got nil")
	}
	if got := err.Error(); !containsAll(got, "non-empty") {
		t.Errorf("error = %q; want mention of non-empty", got)
	}
}

func TestLoadProviderAuth_MalformedTOML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, providerAuthFile)
	if err := os.WriteFile(path, []byte("this is not valid TOML [["), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadProviderAuth(dir)
	if err == nil {
		t.Fatal("expected error for malformed TOML, got nil")
	}
}

func TestMatchProviderAuth_InfisicalByProject(t *testing.T) {
	entries := []ProviderAuthEntry{
		{Kind: "infisical", Config: map[string]any{"project": "proj-aaa", "client_id": "id-a"}},
		{Kind: "infisical", Config: map[string]any{"project": "proj-bbb", "client_id": "id-b"}},
		{Kind: "onepassword", Config: map[string]any{"vault": "Engineering"}},
	}

	// Match: same kind and project.
	spec := vault.ProviderSpec{
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "proj-bbb"},
	}
	match := MatchProviderAuth(spec, entries)
	if match == nil {
		t.Fatal("expected match, got nil")
	}
	if match.Config["client_id"] != "id-b" {
		t.Errorf("matched wrong entry: client_id = %v, want id-b", match.Config["client_id"])
	}

	// No match: same kind, different project.
	spec2 := vault.ProviderSpec{
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "proj-zzz"},
	}
	if m := MatchProviderAuth(spec2, entries); m != nil {
		t.Errorf("expected no match, got %v", m)
	}

	// No match: different kind.
	spec3 := vault.ProviderSpec{
		Kind:   "sops",
		Config: vault.ProviderConfig{"project": "proj-aaa"},
	}
	if m := MatchProviderAuth(spec3, entries); m != nil {
		t.Errorf("expected no match for sops, got %v", m)
	}

	// No match: onepassword kind (no matching logic yet).
	spec4 := vault.ProviderSpec{
		Kind:   "onepassword",
		Config: vault.ProviderConfig{"vault": "Engineering"},
	}
	if m := MatchProviderAuth(spec4, entries); m != nil {
		t.Errorf("expected no match for onepassword (not yet implemented), got %v", m)
	}
}

func TestMatchProviderAuth_EmptyEntries(t *testing.T) {
	spec := vault.ProviderSpec{
		Kind:   "infisical",
		Config: vault.ProviderConfig{"project": "proj-aaa"},
	}
	if m := MatchProviderAuth(spec, nil); m != nil {
		t.Errorf("expected nil for empty entries, got %v", m)
	}
}

func TestInjectProviderTokens_EndToEnd(t *testing.T) {
	wantToken := "eyJ-injected-jwt-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"accessToken": wantToken,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	entries := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "proj-match",
				"client_id":     "test-id",
				"client_secret": "test-secret-value",
				"api_url":       srv.URL,
			},
		},
	}

	// Test with anonymous provider.
	vr := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "proj-match"},
		},
	}

	if err := injectProviderTokens(ctx, entries, vr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := vr.Provider.Config["token"].(string)
	if !ok || got != wantToken {
		t.Errorf("Config[token] = %q, want %q", got, wantToken)
	}
}

func TestInjectProviderTokens_NamedProvider(t *testing.T) {
	wantToken := "eyJ-named-jwt"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"accessToken": wantToken,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	entries := []ProviderAuthEntry{
		{
			Kind: "infisical",
			Config: map[string]any{
				"project":       "proj-named",
				"client_id":     "named-id",
				"client_secret": "named-secret-value",
				"api_url":       srv.URL,
			},
		},
	}

	vr := &config.VaultRegistry{
		Providers: map[string]config.VaultProviderConfig{
			"team": {
				Kind:   "infisical",
				Config: map[string]any{"project": "proj-named"},
			},
			"other": {
				Kind:   "infisical",
				Config: map[string]any{"project": "proj-no-match"},
			},
		},
	}

	if err := injectProviderTokens(ctx, entries, vr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "team" should have a token injected.
	teamCfg := vr.Providers["team"]
	got, ok := teamCfg.Config["token"].(string)
	if !ok || got != wantToken {
		t.Errorf("team Config[token] = %q, want %q", got, wantToken)
	}

	// "other" should NOT have a token.
	otherCfg := vr.Providers["other"]
	if _, ok := otherCfg.Config["token"]; ok {
		t.Errorf("other Config[token] should not be set, but got %v", otherCfg.Config["token"])
	}
}

func TestInjectProviderTokens_NilRegistry(t *testing.T) {
	ctx := context.Background()
	entries := []ProviderAuthEntry{
		{Kind: "infisical", Config: map[string]any{"project": "p"}},
	}
	// Should be a no-op, not a panic.
	if err := injectProviderTokens(ctx, entries, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInjectProviderTokens_EmptyEntries(t *testing.T) {
	ctx := context.Background()
	vr := &config.VaultRegistry{
		Provider: &config.VaultProviderConfig{
			Kind:   "infisical",
			Config: map[string]any{"project": "p"},
		},
	}
	// Should be a no-op.
	if err := injectProviderTokens(ctx, nil, vr); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := vr.Provider.Config["token"]; ok {
		t.Error("token should not be set with empty entries")
	}
}

// containsAll reports whether s contains all of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
