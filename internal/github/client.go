// Package github provides an interface and implementation for querying GitHub repos.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// Repo represents a GitHub repository with metadata used for classification.
type Repo struct {
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	CloneURL   string `json:"clone_url"`
	SSHURL     string `json:"ssh_url"`
	Private    bool   `json:"private"`
}

// Client is the interface for querying GitHub repos.
type Client interface {
	ListRepos(ctx context.Context, org string) ([]Repo, error)
}

// APIClient is the real GitHub API client.
type APIClient struct {
	HTTPClient *http.Client
	Token      string
	BaseURL    string
}

// NewAPIClient creates a new GitHub API client. If token is empty, requests
// are unauthenticated (limited to public repos).
//
// The base URL defaults to https://api.github.com but can be overridden
// via the NIWA_GITHUB_API_URL environment variable. The override is
// intended primarily for tests against tarballFakeServer; production use
// is supported for self-hosted endpoints the user trusts (PRD R17,
// security model).
func NewAPIClient(token string) *APIClient {
	baseURL := os.Getenv("NIWA_GITHUB_API_URL")
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &APIClient{
		HTTPClient: http.DefaultClient,
		Token:      token,
		BaseURL:    baseURL,
	}
}

// ListRepos fetches all repos for the given org from the GitHub API.
// It paginates through all results.
func (c *APIClient) ListRepos(ctx context.Context, org string) ([]Repo, error) {
	var all []Repo
	page := 1

	for {
		url := fmt.Sprintf("%s/orgs/%s/repos?per_page=100&page=%d", c.BaseURL, org, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying GitHub: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub API returned status %d for org %q", resp.StatusCode, org)
		}

		var repos []Repo
		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding response: %w", err)
		}
		resp.Body.Close()

		if len(repos) == 0 {
			break
		}

		// Normalize visibility from the private field if the API doesn't set it.
		for i := range repos {
			normalizeRepoVisibility(&repos[i])
		}

		all = append(all, repos...)
		if len(repos) < 100 {
			break
		}
		page++
	}

	return all, nil
}

// normalizeRepoVisibility fills Repo.Visibility from Repo.Private when the
// API didn't set the string field. ListRepos and GetRepo share this helper
// so a single call site owns the bool→string mapping. The function never
// overwrites an existing non-empty Visibility — that preserves the
// historical ListRepos behavior byte-for-byte.
func normalizeRepoVisibility(r *Repo) {
	if r == nil || r.Visibility != "" {
		return
	}
	if r.Private {
		r.Visibility = "private"
	} else {
		r.Visibility = "public"
	}
}

// GetRepo fetches a single repo's metadata from GET /repos/{owner}/{repo}.
// On 200 it returns a populated *Repo with Visibility normalized from
// Private when the API omits the string. On non-2xx it returns
// *StatusError with the HTTP status code intact so callers can branch
// (e.g. 401/403/404/5xx → R17 cause classification).
func (c *APIClient) GetRepo(ctx context.Context, owner, repo string) (*Repo, error) {
	if owner == "" || repo == "" {
		return nil, errors.New("github: GetRepo requires owner and repo")
	}
	requestURL := fmt.Sprintf("%s/repos/%s/%s", c.BaseURL, owner, repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &StatusError{
			StatusCode: resp.StatusCode,
			URL:        requestURL,
			Message:    fmt.Sprintf("github: GetRepo returned %d for %s/%s", resp.StatusCode, owner, repo),
		}
	}

	var out Repo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	normalizeRepoVisibility(&out)
	return &out, nil
}

// ClassifyVisibilityCause maps a GetRepo error to the PRD R17 cause string
// the caller substitutes into the soft-fail `note:` line. The four causes
// are exhaustive per the PRD: any error that doesn't classify as
// auth/not-found/server-error falls through to "network error" (the
// catch-all for transport failures, context cancel, decode failures).
//
// Callers should print:
//
//	note: could not determine remote visibility (<cause>); defaulting to
//	[groups.public]. Edit .niwa/workspace.toml to change.
//
// then continue with bootstrap (R17 is a soft-fail).
func ClassifyVisibilityCause(err error) string {
	if err == nil {
		return ""
	}
	var se *StatusError
	if errors.As(err, &se) {
		switch {
		case se.StatusCode == http.StatusUnauthorized, se.StatusCode == http.StatusForbidden:
			return "authentication"
		case se.StatusCode == http.StatusNotFound:
			return "not found"
		case se.StatusCode >= 500 && se.StatusCode < 600:
			return "server error"
		}
	}
	return "network error"
}
