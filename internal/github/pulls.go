package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// PRRef identifies a pull request the invoking user was directly requested to
// review. Every field is platform-vouched metadata: an attacker who opens a PR
// controls only the PR number (an integer) and the repository it lives in --
// which is further constrained to the workspace's own repos downstream. No
// author-authored free text (title, body, diff, author name) is carried here,
// so a PRRef is safe to interpolate into a dispatch prompt.
type PRRef struct {
	Owner     string
	Repo      string
	Number    int
	URL       string
	CreatedAt string // PR created_at; a deterministic ordering key from the search payload
}

// CurrentLogin returns the login of the authenticated user (GET /user). The
// watcher needs it to build the user-scoped review-request query, because
// GitHub's search only supports the @me shorthand for review-requested, not
// for the user-review-requested qualifier this feature keys on.
func (c *APIClient) CurrentLogin(ctx context.Context) (string, error) {
	url := c.BaseURL + "/user"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("querying GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d for /user", resp.StatusCode)
	}

	var body struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}
	if body.Login == "" {
		return "", fmt.Errorf("GitHub /user returned an empty login")
	}
	return body.Login, nil
}

// searchIssuesResponse is the subset of the /search/issues payload the watcher
// reads. repository_url has the shape
// "<base>/repos/<owner>/<repo>"; owner/repo are parsed from it.
type searchIssuesResponse struct {
	Items []struct {
		Number        int    `json:"number"`
		HTMLURL       string `json:"html_url"`
		CreatedAt     string `json:"created_at"`
		RepositoryURL string `json:"repository_url"`
		PullRequest   *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	} `json:"items"`
}

// SearchReviewRequestedPRs returns the open PRs where login is the
// directly-requested reviewer, using the user-scoped user-review-requested
// qualifier so team-only requests are excluded by construction. Results are
// paginated. Only structural identifiers are returned (see PRRef).
func (c *APIClient) SearchReviewRequestedPRs(ctx context.Context, login string) ([]PRRef, error) {
	if login == "" {
		return nil, fmt.Errorf("SearchReviewRequestedPRs: empty login")
	}
	query := fmt.Sprintf("is:pr is:open user-review-requested:%s", login)

	var all []PRRef
	page := 1
	for {
		reqURL := fmt.Sprintf("%s/search/issues?q=%s&per_page=100&page=%d",
			c.BaseURL, url.QueryEscape(query), page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}
		c.applyAuth(req)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("querying GitHub search: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("GitHub search returned status %d", resp.StatusCode)
		}

		var body searchIssuesResponse
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("decoding search response: %w", err)
		}
		resp.Body.Close()

		if len(body.Items) == 0 {
			break
		}
		for _, it := range body.Items {
			// Only real PRs (pull_request present) with a parseable repo.
			if it.PullRequest == nil {
				continue
			}
			owner, repo, ok := ownerRepoFromAPIURL(it.RepositoryURL)
			if !ok {
				continue
			}
			all = append(all, PRRef{
				Owner:     owner,
				Repo:      repo,
				Number:    it.Number,
				URL:       it.HTMLURL,
				CreatedAt: it.CreatedAt,
			})
		}
		if len(body.Items) < 100 {
			break
		}
		page++
	}
	return all, nil
}

// PullHead identifies the head commit of a PR: the exact SHA (so the fetch pins
// it against a force-push race) and the clone URL of the repo the head lives in
// (the fork, for a cross-repo PR).
type PullHead struct {
	SHA      string
	CloneURL string
}

// GetPullHead returns the head commit SHA and clone URL for a PR
// (GET /repos/{owner}/{repo}/pulls/{number}). The SHA is what the hardened
// fetch pins.
func (c *APIClient) GetPullHead(ctx context.Context, owner, repo string, number int) (PullHead, error) {
	var ph PullHead
	if owner == "" || repo == "" || number <= 0 {
		return ph, fmt.Errorf("GetPullHead: invalid PR coordinates %q/%q#%d", owner, repo, number)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d", c.BaseURL, owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ph, fmt.Errorf("creating request: %w", err)
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return ph, fmt.Errorf("querying PR: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ph, fmt.Errorf("GitHub PR GET returned status %d", resp.StatusCode)
	}
	var body struct {
		Head struct {
			SHA  string `json:"sha"`
			Repo *struct {
				CloneURL string `json:"clone_url"`
			} `json:"repo"`
		} `json:"head"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return ph, fmt.Errorf("decoding PR: %w", err)
	}
	if body.Head.SHA == "" {
		return ph, fmt.Errorf("PR %s/%s#%d has no head sha", owner, repo, number)
	}
	ph.SHA = body.Head.SHA
	if body.Head.Repo != nil {
		ph.CloneURL = body.Head.Repo.CloneURL
	}
	return ph, nil
}

// CreateReview posts a review to a pull request. event is supplied by the
// caller (trusted niwa code), never derived from body; body is treated as an
// opaque payload. This is the trusted post step that runs outside the contained
// review session.
func (c *APIClient) CreateReview(ctx context.Context, owner, repo string, number int, body, event string) error {
	if owner == "" || repo == "" || number <= 0 {
		return fmt.Errorf("CreateReview: invalid PR coordinates %q/%q#%d", owner, repo, number)
	}
	if event == "" {
		event = "COMMENT"
	}
	payload, err := json.Marshal(map[string]string{"body": body, "event": event})
	if err != nil {
		return fmt.Errorf("encoding review payload: %w", err)
	}
	url := fmt.Sprintf("%s/repos/%s/%s/pulls/%d/reviews", c.BaseURL, owner, repo, number)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("posting review: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("GitHub review POST returned status %d", resp.StatusCode)
	}
	return nil
}

// ownerRepoFromAPIURL parses "<base>/repos/<owner>/<repo>" into owner, repo.
func ownerRepoFromAPIURL(apiURL string) (owner, repo string, ok bool) {
	idx := strings.Index(apiURL, "/repos/")
	if idx < 0 {
		return "", "", false
	}
	rest := apiURL[idx+len("/repos/"):]
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
