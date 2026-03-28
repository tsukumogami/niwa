// Package github provides an interface and implementation for querying GitHub repos.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
func NewAPIClient(token string) *APIClient {
	return &APIClient{
		HTTPClient: http.DefaultClient,
		Token:      token,
		BaseURL:    "https://api.github.com",
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
			if repos[i].Visibility == "" {
				if repos[i].Private {
					repos[i].Visibility = "private"
				} else {
					repos[i].Visibility = "public"
				}
			}
		}

		all = append(all, repos...)
		if len(repos) < 100 {
			break
		}
		page++
	}

	return all, nil
}
