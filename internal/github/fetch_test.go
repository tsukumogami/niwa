package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func urlParse(s string) (*url.URL, error) { return url.Parse(s) }

func TestNewAPIClient_BaseURLOverride(t *testing.T) {
	t.Setenv("NIWA_GITHUB_API_URL", "https://example.test/api")
	c := NewAPIClient("test-token")
	if c.BaseURL != "https://example.test/api" {
		t.Errorf("BaseURL = %q, want override", c.BaseURL)
	}
	if c.Token != "test-token" {
		t.Errorf("Token = %q, want test-token", c.Token)
	}
}

func TestNewAPIClient_DefaultBaseURL(t *testing.T) {
	t.Setenv("NIWA_GITHUB_API_URL", "")
	c := NewAPIClient("")
	if c.BaseURL != "https://api.github.com" {
		t.Errorf("BaseURL = %q, want default", c.BaseURL)
	}
}

func TestHeadCommit_ReturnsOID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/foo/bar/commits/main" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github.sha" {
			t.Errorf("Accept header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("ETag", `"abc123"`)
		_, _ = w.Write([]byte("9f8e7d6c5b4a3210\n"))
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, Token: "test-token", BaseURL: srv.URL}
	oid, etag, status, err := c.HeadCommit(context.Background(), "foo", "bar", "main", "")
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	if oid != "9f8e7d6c5b4a3210" {
		t.Errorf("oid = %q", oid)
	}
	if etag != `"abc123"` {
		t.Errorf("etag = %q", etag)
	}
}

func TestHeadCommit_NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"abc123"` {
			t.Errorf("If-None-Match = %q", got)
		}
		w.Header().Set("ETag", `"abc123"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	oid, etag, status, err := c.HeadCommit(context.Background(), "foo", "bar", "main", `"abc123"`)
	if err != nil {
		t.Fatalf("HeadCommit: %v", err)
	}
	if status != http.StatusNotModified {
		t.Errorf("status = %d", status)
	}
	if oid != "" {
		t.Errorf("oid should be empty on 304, got %q", oid)
	}
	if etag != `"abc123"` {
		t.Errorf("etag = %q", etag)
	}
}

func TestHeadCommit_AuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, _, status, err := c.HeadCommit(context.Background(), "foo", "bar", "main", "")
	if err == nil {
		t.Fatal("expected error on 401")
	}
	if status != http.StatusUnauthorized {
		t.Errorf("status = %d", status)
	}
	if !strings.Contains(err.Error(), "PAT") || !strings.Contains(err.Error(), "scope") {
		t.Errorf("error message should mention PAT and scope: %v", err)
	}
	// Token must NOT appear in the error message.
	if strings.Contains(err.Error(), "test-token") {
		t.Errorf("token leaked in error message: %v", err)
	}
}

func TestFetchTarball_Streams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/foo/bar/tarball/main" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q", got)
		}
		w.Header().Set("ETag", `"tarball-etag"`)
		_, _ = w.Write([]byte("fake-tarball-bytes"))
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, Token: "test-token", BaseURL: srv.URL}
	body, etag, status, redirect, err := c.FetchTarball(context.Background(), "foo", "bar", "main", "")
	if err != nil {
		t.Fatalf("FetchTarball: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d", status)
	}
	if redirect != nil {
		t.Errorf("unexpected redirect: %+v", redirect)
	}
	if etag != `"tarball-etag"` {
		t.Errorf("etag = %q", etag)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(got) != "fake-tarball-bytes" {
		t.Errorf("body = %q", got)
	}
}

func TestFetchTarball_NotModified(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("If-None-Match"); got != `"prior"` {
			t.Errorf("If-None-Match = %q", got)
		}
		w.Header().Set("ETag", `"prior"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	body, etag, status, redirect, err := c.FetchTarball(context.Background(), "foo", "bar", "main", `"prior"`)
	if err != nil {
		t.Fatalf("FetchTarball: %v", err)
	}
	if status != http.StatusNotModified {
		t.Errorf("status = %d", status)
	}
	if body != nil {
		t.Error("body should be nil on 304")
	}
	if etag != `"prior"` {
		t.Errorf("etag = %q", etag)
	}
	if redirect != nil {
		t.Errorf("unexpected redirect: %+v", redirect)
	}
}

func TestFetchTarball_RenameRedirect(t *testing.T) {
	// Two-server dance: hit /repos/oldorg/oldrepo/... → 301 to
	// /repos/neworg/newrepo/... → 200. detectRename should observe
	// the chain and return RenameRedirect.
	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/repos/oldorg/oldrepo/tarball/main", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srvURL+"/repos/neworg/newrepo/tarball/main", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/repos/neworg/newrepo/tarball/main", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("renamed-tarball"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	// Override the server URL to look like api.github.com so detectRename
	// recognises it.
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	// We'll test detectRename directly since the host check requires a
	// specific hostname, and altering hostname through httptest is
	// involved. Verify FetchTarball succeeds end-to-end first.
	body, _, _, _, err := c.FetchTarball(context.Background(), "oldorg", "oldrepo", "main", "")
	if err != nil {
		t.Fatalf("FetchTarball: %v", err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != "renamed-tarball" {
		t.Errorf("body = %q", got)
	}
}

func TestDetectRename(t *testing.T) {
	apiURL, _ := urlParse("https://api.github.com/repos/neworg/newrepo/tarball/main")
	got := detectRename("https://api.github.com/repos/oldorg/oldrepo/tarball/main", []*url.URL{apiURL}, "oldorg", "oldrepo")
	if got == nil {
		t.Fatal("expected rename detection")
	}
	if got.OldOwner != "oldorg" || got.OldRepo != "oldrepo" {
		t.Errorf("old = %s/%s", got.OldOwner, got.OldRepo)
	}
	if got.NewOwner != "neworg" || got.NewRepo != "newrepo" {
		t.Errorf("new = %s/%s", got.NewOwner, got.NewRepo)
	}
}

func TestDetectRename_NoChange(t *testing.T) {
	apiURL, _ := urlParse("https://api.github.com/repos/oldorg/oldrepo/tarball/main")
	if got := detectRename("https://api.github.com/repos/oldorg/oldrepo/tarball/main", []*url.URL{apiURL}, "oldorg", "oldrepo"); got != nil {
		t.Errorf("unexpected rename: %+v", got)
	}
}

func TestDetectRename_NonGitHubHost(t *testing.T) {
	codeloadURL, _ := urlParse("https://codeload.github.com/oldorg/oldrepo/legacy.tar.gz/main")
	if got := detectRename("https://api.github.com/repos/oldorg/oldrepo/tarball/main", []*url.URL{codeloadURL}, "oldorg", "oldrepo"); got != nil {
		t.Errorf("non-API host should not trigger rename detection: %+v", got)
	}
}
