package infisical

// Integration tests for the Infisical backend. These call the real
// `infisical` CLI and require a live Infisical project with pre-seeded
// secrets.
//
// Gating: tests skip when INFISICAL_TEST_PROJECT_ID is unset. CI sets
// this env var alongside INFISICAL_TOKEN (a Machine Identity access
// token) so these tests run against the same project without a
// browser-based login.
//
// Required project setup (one-time):
//
//   Project ID: set in INFISICAL_TEST_PROJECT_ID
//   Environment: dev (default)
//   Path: /
//   Secrets:
//     NIWA_TEST_API_KEY      = "test-api-key-value-12345"
//     NIWA_TEST_DB_PASSWORD  = "test-db-password-67890"
//     NIWA_TEST_WEBHOOK_URL  = "https://example.com/hook"

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/secret/reveal"
	"github.com/tsukumogami/niwa/internal/vault"
)

const testProjectEnvVar = "INFISICAL_TEST_PROJECT_ID"

func skipUnlessIntegration(t *testing.T) string {
	t.Helper()
	projectID := os.Getenv(testProjectEnvVar)
	if projectID == "" {
		t.Skipf("skipping: %s not set (integration test requires a live Infisical project)", testProjectEnvVar)
	}
	return projectID
}

// openTestProvider constructs a real Infisical provider using the
// default commander (real subprocess) against the test project.
func openTestProvider(t *testing.T, projectID string) vault.Provider {
	t.Helper()
	f := &Factory{}
	p, err := f.Open(context.Background(), vault.ProviderConfig{
		"project": projectID,
		"env":     "dev",
		"path":    "/",
		"name":    "integration-test",
	})
	if err != nil {
		t.Fatalf("Factory.Open: %v", err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestIntegration_ResolveKnownSecret(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	val, token, err := p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err != nil {
		t.Fatalf("Resolve NIWA_TEST_API_KEY: %v", err)
	}

	plaintext := string(reveal.UnsafeReveal(val))
	if plaintext != "test-api-key-value-12345" {
		t.Errorf("expected %q, got %q", "test-api-key-value-12345", plaintext)
	}

	// VersionToken should be non-empty (the synthesised SHA-256 digest).
	if token.Token == "" {
		t.Error("VersionToken.Token is empty")
	}
	if token.Provenance == "" {
		t.Error("VersionToken.Provenance is empty")
	}

	// Provenance should be an audit-log URL containing the project ID.
	if !containsSubstring(token.Provenance, projectID) {
		t.Errorf("Provenance %q does not contain project ID %q", token.Provenance, projectID)
	}
}

func TestIntegration_ResolveMultipleSecrets(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	keys := []string{"NIWA_TEST_API_KEY", "NIWA_TEST_DB_PASSWORD", "NIWA_TEST_WEBHOOK_URL"}
	expected := map[string]string{
		"NIWA_TEST_API_KEY":     "test-api-key-value-12345",
		"NIWA_TEST_DB_PASSWORD": "test-db-password-67890",
		"NIWA_TEST_WEBHOOK_URL": "https://example.com/hook",
	}

	for _, key := range keys {
		val, _, err := p.Resolve(context.Background(), vault.Ref{Key: key})
		if err != nil {
			t.Errorf("Resolve %s: %v", key, err)
			continue
		}
		got := string(reveal.UnsafeReveal(val))
		if got != expected[key] {
			t.Errorf("Resolve %s: expected %q, got %q", key, expected[key], got)
		}
	}
}

func TestIntegration_ResolveBatch(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	batch, ok := p.(vault.BatchResolver)
	if !ok {
		t.Fatal("Infisical provider does not implement BatchResolver")
	}

	refs := []vault.Ref{
		{Key: "NIWA_TEST_API_KEY"},
		{Key: "NIWA_TEST_DB_PASSWORD"},
		{Key: "NONEXISTENT_KEY_12345"},
	}

	results, err := batch.ResolveBatch(context.Background(), refs)
	if err != nil {
		t.Fatalf("ResolveBatch: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}

	// First two should succeed.
	for i, expected := range []string{"test-api-key-value-12345", "test-db-password-67890"} {
		if results[i].Err != nil {
			t.Errorf("result[%d] unexpected error: %v", i, results[i].Err)
			continue
		}
		got := string(reveal.UnsafeReveal(results[i].Value))
		if got != expected {
			t.Errorf("result[%d]: expected %q, got %q", i, expected, got)
		}
	}

	// Third should be ErrKeyNotFound.
	if results[2].Err == nil {
		t.Error("result[2] expected ErrKeyNotFound, got nil")
	}
}

func TestIntegration_ResolveMissingKeyReturnsErrKeyNotFound(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	_, _, err := p.Resolve(context.Background(), vault.Ref{Key: "DEFINITELY_NOT_A_REAL_SECRET_KEY"})
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}

	// The error should mention the key.
	if !containsSubstring(err.Error(), "DEFINITELY_NOT_A_REAL_SECRET_KEY") {
		t.Errorf("error %q does not mention the missing key", err.Error())
	}
}

func TestIntegration_RedactionSurvivesRealCLI(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	val, _, err := p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The Value's String() must never reveal plaintext.
	s := val.String()
	if s != "***" {
		t.Errorf("String() = %q, want %q", s, "***")
	}

	// Verify the Value IS a real secret (not zero).
	if val.IsEmpty() {
		t.Error("Value is empty after resolving a real secret")
	}

	// Verify origin metadata is populated.
	origin := val.Origin()
	if origin.ProviderName != "integration-test" {
		t.Errorf("Origin.ProviderName = %q, want %q", origin.ProviderName, "integration-test")
	}
	if origin.Key != "NIWA_TEST_API_KEY" {
		t.Errorf("Origin.Key = %q, want %q", origin.Key, "NIWA_TEST_API_KEY")
	}
}

func TestIntegration_SecretValueRedactedInError(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	val, _, err := p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	plaintext := reveal.UnsafeReveal(val)

	// Build an inner error that "accidentally" includes the plaintext.
	inner := fmt.Errorf("oops the secret is %s and that is bad", string(plaintext))

	// Wrap it with the resolved value so the Error uses its redactor.
	// This exercises the full R22 redaction chain with a real
	// Infisical-resolved value.
	wrapped := secret.Wrap(inner, val)

	// The wrapped error's message must NOT contain the plaintext.
	errMsg := wrapped.Error()
	if containsSubstring(errMsg, string(plaintext)) {
		t.Errorf("error message contains plaintext %q:\n  %s", string(plaintext), errMsg)
	}
	// But it should contain the redacted placeholder.
	if !containsSubstring(errMsg, "***") {
		t.Errorf("error message does not contain redacted placeholder:\n  %s", errMsg)
	}
}

func TestIntegration_CachesAcrossMultipleResolves(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	p := openTestProvider(t, projectID)

	// First resolve triggers the subprocess.
	_, token1, err := p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err != nil {
		t.Fatalf("first Resolve: %v", err)
	}

	// Second resolve should hit cache (same token).
	_, token2, err := p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_DB_PASSWORD"})
	if err != nil {
		t.Fatalf("second Resolve: %v", err)
	}

	if token1.Token != token2.Token {
		t.Errorf("tokens differ across cached resolves: %q vs %q", token1.Token, token2.Token)
	}
}

func TestIntegration_ClosePreventsFurtherResolves(t *testing.T) {
	projectID := skipUnlessIntegration(t)
	f := &Factory{}
	p, err := f.Open(context.Background(), vault.ProviderConfig{
		"project": projectID,
		"env":     "dev",
		"path":    "/",
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Resolve once to load the cache.
	_, _, err = p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err != nil {
		t.Fatalf("Resolve before Close: %v", err)
	}

	// Close the provider.
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Resolve after Close should fail.
	_, _, err = p.Resolve(context.Background(), vault.Ref{Key: "NIWA_TEST_API_KEY"})
	if err == nil {
		t.Fatal("expected error after Close, got nil")
	}
}

func containsSubstring(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) && (s == substr || len(s) > 0 && stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
