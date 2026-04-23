package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/tsukumogami/niwa/internal/testfault"
)

// RenameRedirect captures a 301 redirect chain that traversed a
// repository rename. Returned by FetchTarball when GitHub responds
// with a 301 for an old-name URL; the caller may surface this as a
// one-time `note:` per PRD R18.
type RenameRedirect struct {
	OldOwner string
	OldRepo  string
	NewOwner string
	NewRepo  string
}

// HeadCommit issues GET /repos/{owner}/{repo}/commits/{ref} with the
// Accept: application/vnd.github.sha header, returning the 40-byte
// commit oid as a plain string. Used for cheap drift checks per PRD
// R16.
//
// statusCode is returned alongside the body so the caller can
// distinguish 200, 304 (when If-None-Match matched), and error
// statuses without re-parsing the response.
func (c *APIClient) HeadCommit(ctx context.Context, owner, repo, ref, etag string) (oid, newETag string, statusCode int, err error) {
	if err := testfault.Maybe("head-commit"); err != nil {
		return "", "", 0, err
	}
	if owner == "" || repo == "" {
		return "", "", 0, errors.New("github: HeadCommit requires owner and repo")
	}
	if ref == "" {
		ref = "HEAD"
	}
	requestURL := fmt.Sprintf("%s/repos/%s/%s/commits/%s",
		strings.TrimRight(c.BaseURL, "/"), owner, repo, url.PathEscape(ref))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", "", 0, fmt.Errorf("github: building HeadCommit request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.sha")
	c.applyAuth(req)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", "", 0, fmt.Errorf("github: HeadCommit request failed: %w", err)
	}
	defer resp.Body.Close()

	newETag = resp.Header.Get("ETag")
	if resp.StatusCode == http.StatusNotModified {
		return "", newETag, resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", newETag, resp.StatusCode,
			fmt.Errorf("github: HeadCommit returned %d (verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return "", newETag, resp.StatusCode, fmt.Errorf("github: HeadCommit returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return "", newETag, resp.StatusCode, fmt.Errorf("github: reading HeadCommit body: %w", err)
	}
	return strings.TrimSpace(string(body)), newETag, resp.StatusCode, nil
}

// FetchTarball issues GET /repos/{owner}/{repo}/tarball/{ref}, sending
// If-None-Match: <etag> when etag is non-empty. Returns:
//
//   - body: the gzip-compressed tarball stream (caller must Close), or
//     nil for 304 / non-200 responses
//   - newETag: the response ETag (when present)
//   - statusCode: HTTP status (200, 304, 401, 403, 404, etc.)
//   - redirect: non-nil if the request followed a 301 from a renamed
//     repo (the caller surfaces this per PRD R18)
//   - err: non-nil only on transport/auth failures or unexpected status
func (c *APIClient) FetchTarball(ctx context.Context, owner, repo, ref, etag string) (
	body io.ReadCloser, newETag string, statusCode int, redirect *RenameRedirect, err error,
) {
	if err := testfault.Maybe("fetch-tarball"); err != nil {
		return nil, "", 0, nil, err
	}
	if owner == "" || repo == "" {
		return nil, "", 0, nil, errors.New("github: FetchTarball requires owner and repo")
	}
	if ref == "" {
		ref = "HEAD"
	}
	requestURL := fmt.Sprintf("%s/repos/%s/%s/tarball/%s",
		strings.TrimRight(c.BaseURL, "/"), owner, repo, url.PathEscape(ref))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, "", 0, nil, fmt.Errorf("github: building FetchTarball request: %w", err)
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	// Build a per-call client so CheckRedirect can capture the chain
	// without affecting other concurrent callers' redirect handling.
	chain := []*url.URL{}
	client := &http.Client{
		Transport: c.HTTPClient.Transport,
		Timeout:   c.HTTPClient.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			chain = append(chain, req.URL)
			if len(via) >= 10 {
				return errors.New("github: too many redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, nil, fmt.Errorf("github: FetchTarball request failed: %w", err)
	}

	newETag = resp.Header.Get("ETag")

	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return nil, newETag, resp.StatusCode, nil, nil
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		resp.Body.Close()
		return nil, newETag, resp.StatusCode, nil,
			fmt.Errorf("github: FetchTarball returned %d (verify GH_TOKEN scopes; fine-grained PATs need Contents: read, classic PATs need repo scope)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, newETag, resp.StatusCode, nil, fmt.Errorf("github: FetchTarball returned %d", resp.StatusCode)
	}

	// Detect repo rename by inspecting the redirect chain for any URL
	// whose /repos/{owner}/{repo}/ prefix differs from the request's.
	if rename := detectRename(requestURL, chain, owner, repo); rename != nil {
		redirect = rename
	}

	return resp.Body, newETag, resp.StatusCode, redirect, nil
}

// applyAuth attaches the bearer token (when set) to outgoing requests.
// The token is read once at construction; this never reads from disk
// or env per request.
func (c *APIClient) applyAuth(req *http.Request) {
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

// detectRename inspects the redirect chain for a /repos/{owner}/{repo}/...
// URL whose owner+repo differs from the initial request's. Returns nil
// when no rename was detected.
//
// The path shape is the discriminator: codeload redirects use a
// different path (e.g. /<owner>/<repo>/legacy.tar.gz/...) so they
// never match this prefix. This makes the detector work both against
// real api.github.com and against tarballFakeServer in tests, without
// hard-coding the host.
func detectRename(requestURL string, chain []*url.URL, origOwner, origRepo string) *RenameRedirect {
	for _, u := range chain {
		if u == nil {
			continue
		}
		// Path shape: /repos/{owner}/{repo}/...
		segments := strings.Split(strings.TrimPrefix(path.Clean(u.Path), "/"), "/")
		if len(segments) < 3 || segments[0] != "repos" {
			continue
		}
		newOwner, newRepo := segments[1], segments[2]
		if newOwner != origOwner || newRepo != origRepo {
			return &RenameRedirect{
				OldOwner: origOwner,
				OldRepo:  origRepo,
				NewOwner: newOwner,
				NewRepo:  newRepo,
			}
		}
	}
	return nil
}
