package infisical

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

func testCtxWithRedactor() context.Context {
	return secret.WithRedactor(context.Background(), secret.NewRedactor())
}

func TestReadIdentity_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/auth/universal-auth/identities/ident-123") {
			t.Errorf("path = %q, want suffix .../identities/ident-123", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer op-bearer-token-value" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityUniversalAuth": map[string]string{"clientId": "client-abc"},
		})
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	clientID, err := ReadIdentity(ctx, srv.URL, bearer, "ident-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if clientID != "client-abc" {
		t.Errorf("clientID = %q, want client-abc", clientID)
	}
}

func TestReadIdentity_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	_, err := ReadIdentity(ctx, srv.URL, bearer, "ident-123")
	if !errors.Is(err, ErrIdentityNotFound) {
		t.Fatalf("err = %v, want wrapping ErrIdentityNotFound", err)
	}
}

func TestReadIdentity_Unauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		}))

		ctx := testCtxWithRedactor()
		bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

		_, err := ReadIdentity(ctx, srv.URL, bearer, "ident-123")
		if !errors.Is(err, ErrUnauthorized) {
			t.Errorf("status %d: err = %v, want wrapping ErrUnauthorized", status, err)
		}
		srv.Close()
	}
}

func TestReadIdentity_ResponseBodyScrubbedOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message": "op-bearer-token-value leaked in error"}`))
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	_, err := ReadIdentity(ctx, srv.URL, bearer, "ident-123")
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "op-bearer-token-value") {
		t.Fatalf("error leaked bearer token: %v", err)
	}
}

func TestMintClientSecret_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/auth/universal-auth/identities/ident-123/client-secrets") {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"clientSecret":     "minted-secret-value-xyz",
			"clientSecretData": map[string]string{"id": "secret-id-001"},
		})
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	clientSecret, secretID, err := MintClientSecret(ctx, srv.URL, bearer, "ident-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if secretID != "secret-id-001" {
		t.Errorf("secretID = %q, want secret-id-001", secretID)
	}
	if clientSecret.IsEmpty() {
		t.Error("clientSecret is empty, want minted value")
	}
	if clientSecret.String() != "***" {
		t.Errorf("clientSecret.String() = %q, want redacted placeholder", clientSecret.String())
	}
}

func TestMintClientSecret_RegistersSecretOnRedactorImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"clientSecret":     "minted-secret-value-xyz",
			"clientSecretData": map[string]string{"id": "secret-id-001"},
		})
	}))
	defer srv.Close()

	redactor := secret.NewRedactor()
	ctx := secret.WithRedactor(context.Background(), redactor)
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	_, _, err := MintClientSecret(ctx, srv.URL, bearer, "ident-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	scrubbed := redactor.Scrub("leaked: minted-secret-value-xyz")
	if strings.Contains(scrubbed, "minted-secret-value-xyz") {
		t.Fatalf("redactor did not scrub the minted secret: %q", scrubbed)
	}
}

func TestMintClientSecret_Rejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message": "plan does not allow additional client secrets"}`))
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	_, _, err := MintClientSecret(ctx, srv.URL, bearer, "ident-123")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestRevokeClientSecret_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/auth/universal-auth/identities/ident-123/client-secrets/secret-id-001/revoke") {
			t.Errorf("path = %q, want suffix .../revoke", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	if err := RevokeClientSecret(ctx, srv.URL, bearer, "ident-123", "secret-id-001"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRevokeClientSecret_HostileSecretIDIsEscaped(t *testing.T) {
	// The hostile secretID carries "/" and ".." path-structuring
	// characters. What matters is the bytes actually placed on the
	// wire (the request line niwa's own client sends): url.PathEscape
	// must have turned every "/" into "%2F" there, so the request
	// target is a single opaque path segment rather than a sequence
	// that could redirect the request to a different endpoint. We
	// assert against r.RequestURI (the raw request-target string),
	// not r.URL.Path, since the latter is Go's server-side convenience
	// decoding and would misleadingly show the un-escaped form even
	// though the wire bytes were safely escaped.
	var gotRequestURI string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})
	hostile := "../../other-identity/secrets"

	_ = RevokeClientSecret(ctx, srv.URL, bearer, "ident-123", hostile)
	if !strings.Contains(gotRequestURI, "%2F") {
		t.Fatalf("hostile secretID's slashes were not percent-escaped on the wire, request-URI = %q", gotRequestURI)
	}
	if strings.Contains(gotRequestURI, "/../") {
		t.Fatalf("hostile secretID left an unescaped path-traversal segment on the wire, request-URI = %q", gotRequestURI)
	}
}

func TestRevokeClientSecret_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	err := RevokeClientSecret(ctx, srv.URL, bearer, "ident-123", "secret-id-001")
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestReadEnvironmentSecrets_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v4/secrets") {
			t.Errorf("path = %q, want suffix /v4/secrets", r.URL.Path)
		}
		q := r.URL.Query()
		if q.Get("projectId") != "proj-1" || q.Get("environment") != "dev" || q.Get("secretPath") != "/" {
			t.Errorf("query = %v, want projectId=proj-1 environment=dev secretPath=/", q)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer minted-access-token" {
			t.Errorf("Authorization header = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"secrets": []}`))
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	token := secret.New([]byte("minted-access-token"), secret.Origin{})

	if err := ReadEnvironmentSecrets(ctx, srv.URL, token, "proj-1", "dev", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadEnvironmentSecrets_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	token := secret.New([]byte("minted-access-token"), secret.Origin{})

	err := ReadEnvironmentSecrets(ctx, srv.URL, token, "proj-1", "dev", "/")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want wrapping ErrUnauthorized", err)
	}
}
