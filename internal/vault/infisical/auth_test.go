package infisical

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

func TestAuthenticate_Success(t *testing.T) {
	wantToken := "eyJhbGciOiJSUzI1NiJ9.test-jwt-payload.signature"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, universalAuthPath) {
			t.Errorf("path = %q, want suffix %q", r.URL.Path, universalAuthPath)
		}

		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decoding request body: %v", err)
		}
		if body["clientId"] != "test-client-id" {
			t.Errorf("clientId = %q, want test-client-id", body["clientId"])
		}
		if body["clientSecret"] != "test-client-secret-value" {
			t.Errorf("clientSecret = %q, want test-client-secret-value", body["clientSecret"])
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"accessToken": wantToken,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	entry := map[string]any{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret-value",
		"api_url":       srv.URL,
	}

	token, err := Authenticate(ctx, entry)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != wantToken {
		t.Errorf("token = %q, want %q", token, wantToken)
	}
}

func TestAuthenticate_DefaultAPIURL(t *testing.T) {
	// Verify that when api_url is absent, the default URL is used.
	// We don't actually make the request (it would fail), but we
	// can verify the function doesn't panic and uses the default.
	ctx := context.Background()
	entry := map[string]any{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret-value",
		// No api_url -- should default to https://app.infisical.com/api
	}

	// This will fail with a connection error, but the error message
	// should reference the default URL.
	_, err := Authenticate(ctx, entry)
	if err == nil {
		t.Fatal("expected error (cannot reach default URL), got nil")
	}
	// The error should indicate an HTTP failure, not a missing field error.
	errStr := err.Error()
	if strings.Contains(errStr, "missing required field") {
		t.Errorf("unexpected missing-field error: %v", err)
	}
}

func TestAuthenticate_AuthFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message": "invalid credentials"}`))
	}))
	defer srv.Close()

	ctx := context.Background()
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	entry := map[string]any{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret-value",
		"api_url":       srv.URL,
	}

	_, err := Authenticate(ctx, entry)
	if err == nil {
		t.Fatal("expected error for 401 response, got nil")
	}
	errStr := err.Error()
	if !strings.Contains(errStr, "401") {
		t.Errorf("error = %q; want mention of 401", errStr)
	}
}

func TestAuthenticate_ClientSecretNotInError(t *testing.T) {
	// Server echoes the client_secret in the error response body.
	theSecret := "super-secret-client-secret-value"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		// Echo the client_secret in the response (some APIs do this).
		fmt.Fprintf(w, `{"error": "invalid client_secret: %s"}`, theSecret)
	}))
	defer srv.Close()

	ctx := context.Background()
	redactor := secret.NewRedactor()
	ctx = secret.WithRedactor(ctx, redactor)

	entry := map[string]any{
		"client_id":     "test-client-id",
		"client_secret": theSecret,
		"api_url":       srv.URL,
	}

	_, err := Authenticate(ctx, entry)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	errStr := err.Error()
	if strings.Contains(errStr, theSecret) {
		t.Errorf("error message contains client_secret %q; must be scrubbed.\nFull error: %s", theSecret, errStr)
	}
	// The scrubbed placeholder should appear instead.
	if !strings.Contains(errStr, "***") {
		t.Errorf("error = %q; expected scrubbed placeholder '***'", errStr)
	}
}

func TestAuthenticate_MissingClientID(t *testing.T) {
	ctx := context.Background()
	entry := map[string]any{
		"client_secret": "test-secret",
	}

	_, err := Authenticate(ctx, entry)
	if err == nil {
		t.Fatal("expected error for missing client_id, got nil")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error = %q; want mention of client_id", err.Error())
	}
}

func TestAuthenticate_MissingClientSecret(t *testing.T) {
	ctx := context.Background()
	entry := map[string]any{
		"client_id": "test-id",
	}

	_, err := Authenticate(ctx, entry)
	if err == nil {
		t.Fatal("expected error for missing client_secret, got nil")
	}
	if !strings.Contains(err.Error(), "client_secret") {
		t.Errorf("error = %q; want mention of client_secret", err.Error())
	}
}
