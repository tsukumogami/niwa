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
	srv, ln, _, err := New(context.Background(), Config{Port: 0})
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
	_, ln, _, err := New(context.Background(), Config{Port: 0})
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

func TestNew_StubRoutesReturn501(t *testing.T) {
	srv, ln, _, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	for _, path := range []string{"/", "/changes/", "/changes/abc"} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusNotImplemented {
				t.Errorf("status = %d, want 501 (F5 stub)", resp.StatusCode)
			}
		})
	}
}

func TestCORSStrip_RejectsCrossOrigin(t *testing.T) {
	srv, ln, _, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/changes/", nil)
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
	srv, ln, _, err := New(context.Background(), Config{Port: 0})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ln.Close()
	defer srv.Close()

	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/changes/")
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
