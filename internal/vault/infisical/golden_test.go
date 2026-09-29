package infisical

// Characterization of the universal-auth login failure, recorded before the
// vault-offline work bounds and classifies it. Rewrite the fixture only on
// purpose, with
//
//	go test ./internal/vault/infisical/ -run Golden -update

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tsukumogami/niwa/internal/secret"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden fixtures under testdata/golden from the current code")

// TestGoldenUniversalAuthFailure pins today's error for a universal-auth
// login the server refuses. The server echoes the client secret back in its
// body, so the fixture also records that the secret is scrubbed from the
// error.
func TestGoldenUniversalAuthFailure(t *testing.T) {
	const clientSecret = "golden-secret-marker-client-secret"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"statusCode":401,"message":"Invalid credentials for client secret %s","error":"UnauthorizedError"}`, clientSecret)
	}))
	defer srv.Close()

	// Authenticate registers the client secret on the context's redactor
	// before calling authenticateHTTP; do the same here.
	redactor := secret.NewRedactor()
	redactor.Register([]byte(clientSecret))
	ctx := secret.WithRedactor(context.Background(), redactor)

	token, err := authenticateHTTP(ctx, srv.URL, "golden-client-id", clientSecret)
	if token != "" {
		t.Fatalf("token = %q, want empty on a refused login", token)
	}

	var sb strings.Builder
	sb.WriteString("scenario: universal-auth-http-401\nerror:\n")
	if err == nil {
		sb.WriteString("<nil>\n")
	} else {
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	got := strings.ReplaceAll(sb.String(), srv.URL, "<SERVER>")
	if strings.Contains(got, clientSecret) {
		t.Fatalf("fixture would contain the client secret")
	}

	path := filepath.Join("testdata", "golden", "universal-auth-http-401.golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing fixture %s; record it with -update", path)
	}
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != got {
		t.Errorf("fixture %s differs from the current output\n--- want\n%s--- got\n%s", path, want, got)
	}
}
