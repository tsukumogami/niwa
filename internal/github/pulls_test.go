package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurrentLogin_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"login":"octocat"}`)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	login, err := c.CurrentLogin(context.Background())
	if err != nil {
		t.Fatalf("CurrentLogin: %v", err)
	}
	if login != "octocat" {
		t.Errorf("login = %q, want octocat", login)
	}
}

func TestCurrentLogin_EmptyLoginIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"login":""}`)
	}))
	defer srv.Close()
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	if _, err := c.CurrentLogin(context.Background()); err == nil {
		t.Fatal("expected error on empty login")
	}
}

func TestSearchReviewRequestedPRs_ParsesAndFilters(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		_, _ = io.WriteString(w, `{"items":[
			{"number":42,"html_url":"https://github.com/acme/api/pull/42","created_at":"2026-01-02T00:00:00Z","repository_url":"https://api.github.com/repos/acme/api","pull_request":{"url":"x"}},
			{"number":7,"html_url":"https://github.com/acme/web/pull/7","created_at":"2026-01-01T00:00:00Z","repository_url":"https://api.github.com/repos/acme/web","pull_request":{"url":"y"}},
			{"number":99,"html_url":"https://github.com/acme/issue/issues/99","created_at":"2026-01-03T00:00:00Z","repository_url":"https://api.github.com/repos/acme/issue"}
		]}`)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	prs, err := c.SearchReviewRequestedPRs(context.Background(), "octocat")
	if err != nil {
		t.Fatalf("SearchReviewRequestedPRs: %v", err)
	}
	// The plain issue (no pull_request) is filtered out.
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2: %+v", len(prs), prs)
	}
	if prs[0].Owner != "acme" || prs[0].Repo != "api" || prs[0].Number != 42 {
		t.Errorf("pr0 = %+v", prs[0])
	}
	if prs[0].CreatedAt != "2026-01-02T00:00:00Z" {
		t.Errorf("pr0 CreatedAt = %q", prs[0].CreatedAt)
	}
	// The query uses the user-scoped qualifier so team-only requests are excluded.
	if !strings.Contains(gotQuery, "user-review-requested:octocat") {
		t.Errorf("query %q missing user-review-requested:octocat", gotQuery)
	}
	if !strings.Contains(gotQuery, "is:pr") || !strings.Contains(gotQuery, "is:open") {
		t.Errorf("query %q missing is:pr/is:open", gotQuery)
	}
}

func TestSearchReviewRequestedPRs_EmptyLogin(t *testing.T) {
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: "https://example.test"}
	if _, err := c.SearchReviewRequestedPRs(context.Background(), ""); err == nil {
		t.Fatal("expected error on empty login")
	}
}

func TestGetPullHead_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/api/pulls/42" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"head":{"sha":"abcdef1234567890","repo":{"clone_url":"https://github.com/forker/api.git"}}}`)
	}))
	defer srv.Close()
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	ph, err := c.GetPullHead(context.Background(), "acme", "api", 42)
	if err != nil {
		t.Fatalf("GetPullHead: %v", err)
	}
	if ph.SHA != "abcdef1234567890" {
		t.Errorf("sha = %q", ph.SHA)
	}
	if ph.CloneURL != "https://github.com/forker/api.git" {
		t.Errorf("cloneURL = %q", ph.CloneURL)
	}
}

func TestGetPullHead_InvalidCoords(t *testing.T) {
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: "https://example.test"}
	if _, err := c.GetPullHead(context.Background(), "", "api", 42); err == nil {
		t.Fatal("expected error on empty owner")
	}
}

func TestOwnerRepoFromAPIURL(t *testing.T) {
	cases := []struct {
		in          string
		owner, repo string
		ok          bool
	}{
		{"https://api.github.com/repos/acme/api", "acme", "api", true},
		{"https://ghe.example.com/api/v3/repos/org/sub-repo", "org", "sub-repo", true},
		{"https://api.github.com/repos/acme/", "", "", false},
		{"https://api.github.com/user", "", "", false},
		{"", "", "", false},
	}
	for _, tc := range cases {
		owner, repo, ok := ownerRepoFromAPIURL(tc.in)
		if ok != tc.ok || owner != tc.owner || repo != tc.repo {
			t.Errorf("ownerRepoFromAPIURL(%q) = (%q,%q,%v), want (%q,%q,%v)",
				tc.in, owner, repo, ok, tc.owner, tc.repo, tc.ok)
		}
	}
}
