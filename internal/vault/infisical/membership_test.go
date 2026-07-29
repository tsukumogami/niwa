package infisical

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

func TestReadProjectMembership_Granted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/projects/proj-1/memberships/identities/ident-1") {
			t.Errorf("path = %q, want suffix .../projects/proj-1/memberships/identities/ident-1", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer op-bearer-token-value" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityMembership": map[string]any{
				"roles": []map[string]string{{"role": "member"}},
			},
		})
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	granted, err := ReadProjectMembership(ctx, srv.URL, bearer, "proj-1", "ident-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !granted {
		t.Error("granted = false, want true")
	}
}

func TestReadProjectMembership_NoRolesNotGranted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"identityMembership": map[string]any{"roles": []map[string]string{}},
		})
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	granted, err := ReadProjectMembership(ctx, srv.URL, bearer, "proj-1", "ident-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if granted {
		t.Error("granted = true, want false (empty roles array)")
	}
}

func TestReadProjectMembership_NotFoundNotGranted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	granted, err := ReadProjectMembership(ctx, srv.URL, bearer, "proj-1", "ident-1")
	if err != nil {
		t.Fatalf("a 404 must not be an error -- it is the 'not yet granted' signal: %v", err)
	}
	if granted {
		t.Error("granted = true, want false on 404")
	}
}

func TestReadProjectMembership_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	_, err := ReadProjectMembership(ctx, srv.URL, bearer, "proj-1", "ident-1")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want wrapping ErrUnauthorized", err)
	}
}

func TestReadProjectMembership_Malformed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json`))
	}))
	defer srv.Close()

	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	if _, err := ReadProjectMembership(ctx, srv.URL, bearer, "proj-1", "ident-1"); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestReadProjectMembership_RequiresProjectIDAndIdentityID(t *testing.T) {
	ctx := testCtxWithRedactor()
	bearer := secret.New([]byte("op-bearer-token-value"), secret.Origin{})

	if _, err := ReadProjectMembership(ctx, "https://example.invalid", bearer, "", "ident-1"); err == nil {
		t.Error("expected error for empty projectID")
	}
	if _, err := ReadProjectMembership(ctx, "https://example.invalid", bearer, "proj-1", ""); err == nil {
		t.Error("expected error for empty identityID")
	}
}
