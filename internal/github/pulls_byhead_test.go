package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListPullsByHead_SendsOwnerPrefixedHeadAndParses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/widget/pulls" {
			t.Errorf("path %q", r.URL.Path)
		}
		q := r.URL.Query()
		if got := q.Get("head"); got != "acme:fix/thing" {
			t.Errorf("head = %q, want acme:fix/thing (GitHub ignores a head filter without the owner)", got)
		}
		if got := q.Get("state"); got != "all" {
			t.Errorf("state = %q, want all", got)
		}
		_, _ = io.WriteString(w, `[
			{"number": 12, "state": "closed", "merged_at": "2026-09-01T00:00:00Z", "head": {"ref": "fix/thing", "sha": "aaa"}},
			{"number": 4, "state": "closed", "merged_at": null, "head": {"ref": "fix/thing", "sha": "bbb"}},
			{"number": 99, "state": "open", "merged_at": null, "head": {"ref": "some-other-branch", "sha": "ccc"}}
		]`)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	pulls, err := c.ListPullsByHead(context.Background(), "acme", "widget", "fix/thing")
	if err != nil {
		t.Fatalf("ListPullsByHead: %v", err)
	}
	// #99 has another head: an ignored filter must not leak it in.
	if len(pulls) != 2 {
		t.Fatalf("got %d pulls, want 2: %+v", len(pulls), pulls)
	}
	if pulls[0].Number != 12 || pulls[0].MergedAt == "" || pulls[0].HeadSHA != "aaa" {
		t.Errorf("first pull %+v", pulls[0])
	}
	if pulls[1].Number != 4 || pulls[1].MergedAt != "" || pulls[1].State != "closed" {
		t.Errorf("second pull %+v", pulls[1])
	}
}

func TestListPullsByHead_NonOKIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &APIClient{HTTPClient: http.DefaultClient, BaseURL: srv.URL}
	if _, err := c.ListPullsByHead(context.Background(), "acme", "widget", "b"); err == nil {
		t.Fatal("expected an error for a 404")
	}
}
