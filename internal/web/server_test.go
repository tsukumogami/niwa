package web

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNew_BindsToLoopback(t *testing.T) {
	srv, ln, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "127.0.0.1" {
		t.Errorf("listener bound to %q, want 127.0.0.1 (NFR4 demands loopback-only)", host)
	}
}

func TestNew_EphemeralPortWhenZero(t *testing.T) {
	_, ln, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	_, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if portStr == "0" {
		t.Errorf("expected non-zero ephemeral port, got %q", portStr)
	}
}

// TestNew_RoutesWired confirms New() registers the hierarchical F5 GET
// routes with real handlers (not 501 stubs). The exhaustive behavioural
// coverage of each route lives in handlers_test.go; this test only
// checks that the wiring exists.
func TestNew_RoutesWired(t *testing.T) {
	srv, ln, err := New(context.Background(), Config{
		Port: 0,
		// Empty instance list — the surface still boots and serves the
		// workspaces-index / 404 paths; per-instance route coverage
		// lives in handlers_test.go where the fixture is richer.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	// Disable redirect following so we can observe the 302 from GET /.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	for _, tc := range []struct {
		path string
		want int
	}{
		{"/", http.StatusFound},                                                  // 302 to /workspaces/
		{"/workspaces/", http.StatusOK},                                          // empty workspaces index renders 200
		{"/workspaces/missing/", http.StatusNotFound},                            // unknown workspace → 404
		{"/workspaces/missing/inst/changes/", http.StatusNotFound},               // unknown instance → 404
		{"/workspaces/missing/inst/changes/not-a-uuid", http.StatusNotFound},     // bogus id (also unknown instance) → 404
	} {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := client.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s: %v", tc.path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("GET %s: status = %d, want %d", tc.path, resp.StatusCode, tc.want)
			}
		})
	}
}

func TestCORSStrip_RejectsCrossOrigin(t *testing.T) {
	srv, ln, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/workspaces/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (cross-origin rejected)", resp.StatusCode)
	}
}

func TestNoCORSHeadersInResponses(t *testing.T) {
	srv, ln, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/workspaces/")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	for k := range resp.Header {
		if strings.HasPrefix(strings.ToLower(k), "access-control-") {
			t.Errorf("server emitted CORS header %q (must not)", k)
		}
	}
}

func TestBearerMiddleware_MissingHeaderReturns401(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mw := BearerMiddleware("secret-token")
	ts := httptest.NewServer(mw(stub))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/anything")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (no Authorization header)", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer ") {
		t.Errorf("WWW-Authenticate = %q, want 'Bearer ...'", got)
	}
}

func TestBearerMiddleware_WrongTokenReturns401(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mw := BearerMiddleware("secret-token")
	ts := httptest.NewServer(mw(stub))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/anything", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (wrong token)", resp.StatusCode)
	}
}

func TestBearerMiddleware_CorrectTokenAdmitsRequest(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mw := BearerMiddleware("secret-token")
	ts := httptest.NewServer(mw(stub))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/anything", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 (correct token)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want 'ok'", body)
	}
}

// TestBearerMiddleware_CookieTokenRejected confirms the explicit
// non-acceptance of cookies as a credential source. A request whose
// "Authorization" cookie carries a valid token is still rejected
// because BearerMiddleware reads only from the Authorization header.
func TestBearerMiddleware_CookieTokenRejected(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mw := BearerMiddleware("secret-token")
	ts := httptest.NewServer(mw(stub))
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/anything", nil)
	req.AddCookie(&http.Cookie{Name: "Authorization", Value: "Bearer secret-token"})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (cookies must not authenticate)", resp.StatusCode)
	}
}

// TestBearerMiddleware_QueryParamRejected confirms tokens in query
// strings are not accepted as a credential source — they'd leak to
// upstream proxies and referrer headers.
func TestBearerMiddleware_QueryParamRejected(t *testing.T) {
	stub := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mw := BearerMiddleware("secret-token")
	ts := httptest.NewServer(mw(stub))
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/anything?token=secret-token")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (query params must not authenticate)", resp.StatusCode)
	}
}
