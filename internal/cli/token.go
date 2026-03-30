package cli

import (
	"os"
	"os/exec"
	"strings"
)

// resolveGitHubToken returns a GitHub API token from the environment or by
// calling `gh auth token`. It checks GITHUB_TOKEN and GH_TOKEN env vars first,
// then falls back to the gh CLI. Returns empty string if no token is available.
func resolveGitHubToken() string {
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token
	}
	if token := os.Getenv("GH_TOKEN"); token != "" {
		return token
	}

	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
