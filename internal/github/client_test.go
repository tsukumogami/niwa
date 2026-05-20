package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetRepo_OK_PublicVisibilityFromBool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/foo/bar" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization header = %q", got)
		}
		w.WriteHeader(http.StatusOK)
		// API does not always set "visibility"; ListRepos historically
		// fills it from "private". GetRepo should share that helper.
		_, _ = w.Write([]byte(`{"name":"bar","private":false,"clone_url":"https://example.test/foo/bar.git","ssh_url":"git@example.test:foo/bar.git"}`))
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, Token: "test-token", BaseURL: srv.URL}
	r, err := c.GetRepo(context.Background(), "foo", "bar")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Name != "bar" {
		t.Errorf("Name = %q, want bar", r.Name)
	}
	if r.Private {
		t.Errorf("Private = true, want false")
	}
	if r.Visibility != "public" {
		t.Errorf("Visibility = %q, want public (derived from private=false)", r.Visibility)
	}
	if r.CloneURL == "" || r.SSHURL == "" {
		t.Errorf("clone urls not decoded: %+v", r)
	}
}

func TestGetRepo_OK_PrivateVisibilityFromBool(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"name":"secret","private":true}`))
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.GetRepo(context.Background(), "foo", "secret")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if !r.Private {
		t.Errorf("Private = false, want true")
	}
	if r.Visibility != "private" {
		t.Errorf("Visibility = %q, want private (derived from private=true)", r.Visibility)
	}
}

func TestGetRepo_NotFound_ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	r, err := c.GetRepo(context.Background(), "foo", "bar")
	if r != nil {
		t.Fatalf("expected nil *Repo on error, got %+v", r)
	}
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", se.StatusCode)
	}
	if se.URL == "" {
		t.Errorf("URL field empty on StatusError")
	}
	if se.Message == "" {
		t.Errorf("Message field empty on StatusError")
	}
}

func TestGetRepo_Unauthorized_ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.GetRepo(context.Background(), "foo", "bar")
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want 401", se.StatusCode)
	}
}

func TestGetRepo_ServerError_ReturnsStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	_, err := c.GetRepo(context.Background(), "foo", "bar")
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %T: %v", err, err)
	}
	if se.StatusCode != http.StatusInternalServerError {
		t.Errorf("StatusCode = %d, want 500", se.StatusCode)
	}
}

func TestGetRepo_RequiresOwnerAndRepo(t *testing.T) {
	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: "https://example.test"}
	if _, err := c.GetRepo(context.Background(), "", "bar"); err == nil {
		t.Error("expected error for empty owner")
	}
	if _, err := c.GetRepo(context.Background(), "foo", ""); err == nil {
		t.Error("expected error for empty repo")
	}
}

func TestNormalizeRepoVisibility(t *testing.T) {
	tests := []struct {
		name string
		in   Repo
		want string
	}{
		{"empty+private fills private", Repo{Private: true}, "private"},
		{"empty+public fills public", Repo{Private: false}, "public"},
		{"already-public preserved", Repo{Private: true, Visibility: "public"}, "public"},
		{"already-private preserved", Repo{Private: false, Visibility: "private"}, "private"},
		{"already-internal preserved", Repo{Private: false, Visibility: "internal"}, "internal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.in
			normalizeRepoVisibility(&r)
			if r.Visibility != tt.want {
				t.Errorf("Visibility = %q, want %q", r.Visibility, tt.want)
			}
		})
	}
}

func TestNormalizeRepoVisibility_NilSafe(t *testing.T) {
	// Should not panic.
	normalizeRepoVisibility(nil)
}

func TestGetRepoAndListRepos_ShareNormalizer(t *testing.T) {
	// Spin up a fake server that serves both endpoints with parallel
	// payloads — GetRepo for "foo/bar" and a one-page ListRepos for "foo"
	// containing the same repo shape. After both calls the Visibility
	// strings should match: the bool→string mapping is funneled through
	// normalizeRepoVisibility for both paths.
	body := `{"name":"bar","private":true}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/foo/bar":
			_, _ = w.Write([]byte(body))
		case "/orgs/foo/repos":
			_, _ = w.Write([]byte("[" + body + "]"))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	repo, err := c.GetRepo(context.Background(), "foo", "bar")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	list, err := c.ListRepos(context.Background(), "foo")
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListRepos returned %d repos, want 1", len(list))
	}
	if repo.Visibility != list[0].Visibility {
		t.Errorf("visibility mismatch between GetRepo (%q) and ListRepos (%q)", repo.Visibility, list[0].Visibility)
	}
	if repo.Visibility != "private" {
		t.Errorf("expected private (derived from private=true), got %q", repo.Visibility)
	}
}

func TestClassifyVisibilityCause(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil → empty", nil, ""},
		{"401 → authentication", &StatusError{StatusCode: 401}, "authentication"},
		{"403 → authentication", &StatusError{StatusCode: 403}, "authentication"},
		{"404 → not found", &StatusError{StatusCode: 404}, "not found"},
		{"500 → server error", &StatusError{StatusCode: 500}, "server error"},
		{"503 → server error", &StatusError{StatusCode: 503}, "server error"},
		{"transport error → network error", errors.New("dial tcp: connection refused"), "network error"},
		{"unknown status (418) falls through to network", &StatusError{StatusCode: 418}, "network error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyVisibilityCause(tt.err); got != tt.want {
				t.Errorf("ClassifyVisibilityCause(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
