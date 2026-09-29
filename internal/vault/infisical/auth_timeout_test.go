package infisical

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tsukumogami/niwa/internal/secret"
	"github.com/tsukumogami/niwa/internal/vault"
)

// silentServer accepts every connection and never answers, until the
// test ends.
func silentServer(t *testing.T) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})
	return srv
}

func loginEntry(apiURL string) map[string]any {
	return map[string]any{
		"client_id":     "timeout-client-id",
		"client_secret": "timeout-client-secret",
		"api_url":       apiURL,
	}
}

// R8, fast variant: with the bound shortened, a login to a server that
// never answers fails with an error naming the universal-auth login,
// the server and the bound.
func TestAuthenticate_LoginTimesOut(t *testing.T) {
	quietOverride(t, "200ms")
	srv := silentServer(t)

	start := time.Now()
	token, err := Authenticate(context.Background(), loginEntry(srv.URL))
	elapsed := time.Since(start)

	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
	want := "infisical: universal-auth login to " + srv.URL + " timed out after 200ms"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if elapsed > 5*time.Second {
		t.Errorf("login returned after %s, want shortly after the 200ms bound", elapsed)
	}
}

// R8: a login timeout takes the same path out of Authenticate as any
// other failed login (an HTTP 500 here). Both are scrubbed secret
// errors that don't read as "provider unreachable", so the provider
// auth layer stops provisioning for both, as it does today.
func TestAuthenticate_TimeoutFollowsLoginErrorPath(t *testing.T) {
	quietOverride(t, "200ms")
	silent := silentServer(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer failing.Close()

	_, timeoutErr := Authenticate(context.Background(), loginEntry(silent.URL))
	_, http500Err := Authenticate(context.Background(), loginEntry(failing.URL))

	for name, err := range map[string]error{"timeout": timeoutErr, "HTTP 500": http500Err} {
		if err == nil {
			t.Fatalf("%s: Authenticate returned no error", name)
		}
		var se *secret.Error
		if !errors.As(err, &se) {
			t.Errorf("%s: error %T is not a *secret.Error", name, err)
		}
		if errors.Is(err, vault.ErrProviderUnreachable) {
			t.Errorf("%s: error wraps ErrProviderUnreachable, which would soften it", name)
		}
	}
	if !strings.Contains(http500Err.Error(), "HTTP 500") {
		t.Errorf("HTTP 500 error changed: %v", http500Err)
	}
}

// A caller's own cancellation of the login is not reported as a
// timeout.
func TestAuthenticate_CallerCancellationIsNotATimeout(t *testing.T) {
	srv := silentServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	_, err := Authenticate(ctx, loginEntry(srv.URL))
	if err == nil {
		t.Fatal("cancelled login returned no error")
	}
	if strings.Contains(err.Error(), "timed out") {
		t.Errorf("caller cancellation reported as a timeout: %v", err)
	}
}

// R8 with the real bound: a login to a server that never answers
// fails within 35 s.
func TestAuthenticate_LoginRealBound(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: waits for the real 30s login bound")
	}
	t.Parallel()
	if os.Getenv(testTimeoutEnv) != "" {
		t.Skipf("%s is set in the environment; this test needs the real bound", testTimeoutEnv)
	}
	srv := silentServer(t)

	start := time.Now()
	_, err := Authenticate(context.Background(), loginEntry(srv.URL))
	elapsed := time.Since(start)

	want := "infisical: universal-auth login to " + srv.URL + " timed out after 30s"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if elapsed > 35*time.Second {
		t.Errorf("login returned after %s, want within 35s", elapsed)
	}
}

// The login bound lives on the request, not on the shared client, so
// the onboarding calls that share HTTPClient stay unbounded.
func TestHTTPClientHasNoTimeout(t *testing.T) {
	if HTTPClient.Timeout != 0 {
		t.Errorf("HTTPClient.Timeout = %s, want 0", HTTPClient.Timeout)
	}
}
