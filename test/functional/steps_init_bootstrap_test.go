package functional

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/cucumber/godog"
)

// aGitHubFakeIsConfigured spins up the per-scenario tarballFakeServer
// and points the niwa binary at it via NIWA_GITHUB_API_URL. The fake
// is closed in the suite After hook so scenarios that never use it
// pay only the cost of the empty struct.
//
// Subsequent steps configure tarballs, statuses, and metadata bodies
// through the godog step regexes registered in initializeScenario.
func aGitHubFakeIsConfigured(ctx context.Context) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if s.githubFake == nil {
		s.githubFake = newTarballFakeServer()
		s.envOverrides["NIWA_GITHUB_API_URL"] = s.githubFake.URL()
	}
	return ctx, nil
}

// theGitHubFakeServesAtRefWithWorkspaceMarker installs a tarball that
// contains a `.niwa/workspace.toml` marker file at the standard GitHub
// tarball-extraction layout. Use to drive Issue 4's happy-path AC: the
// materialize step finds a marker, so RunBootstrap is NOT triggered;
// scenarios that need NoMarker should call the *Empty step instead.
func theGitHubFakeServesAtRefWithWorkspaceMarker(ctx context.Context, slug, ref string) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake; call 'a GitHub fake is configured' first")
	}
	owner, repo, parseErr := splitOwnerRepo(slug)
	if parseErr != nil {
		return ctx, parseErr
	}
	wrapper := fmt.Sprintf("%s-%s-bootstrap", owner, repo)
	s.githubFake.SetTarball(owner, repo, ref, map[string]string{
		wrapper + "/":                     "",
		wrapper + "/.niwa/":               "",
		wrapper + "/.niwa/workspace.toml": "[workspace]\nname = \"" + repo + "\"\n",
	})
	s.githubFake.SetCommit(owner, repo, ref, "abcdef0123456789abcdef0123456789abcdef01")
	return ctx, nil
}

// theGitHubFakeServesAtRefEmpty installs a tarball that is structurally
// valid (one wrapper directory + a README) but does NOT contain a
// `.niwa/workspace.toml`. The materialize probe surfaces this as a
// *config.NoMarkerError so R13's NoMarker dispatch fires. This is the
// fixture for the TTY-Yes / TTY-No / non-TTY scenarios.
func theGitHubFakeServesAtRefEmpty(ctx context.Context, slug, ref string) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake; call 'a GitHub fake is configured' first")
	}
	owner, repo, parseErr := splitOwnerRepo(slug)
	if parseErr != nil {
		return ctx, parseErr
	}
	wrapper := fmt.Sprintf("%s-%s-bootstrap", owner, repo)
	s.githubFake.SetTarball(owner, repo, ref, map[string]string{
		wrapper + "/":          "",
		wrapper + "/README.md": "# " + repo + "\n",
	})
	s.githubFake.SetCommit(owner, repo, ref, "fedcba9876543210fedcba9876543210fedcba98")
	return ctx, nil
}

// theGitHubFakeReturnsStatusForAtRef configures the tarball / commits
// endpoint to return the supplied HTTP status code. Drives R10 / R11
// adjacent-failure-mode scenarios (401, 403, 404) without standing up a
// real network failure path.
func theGitHubFakeReturnsStatusForAtRef(ctx context.Context, status int, slug, ref string) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake; call 'a GitHub fake is configured' first")
	}
	owner, repo, parseErr := splitOwnerRepo(slug)
	if parseErr != nil {
		return ctx, parseErr
	}
	s.githubFake.SetStatus(owner, repo, ref, status)
	return ctx, nil
}

// theGitHubFakeReturnsStatusForRepoMetadata configures the bare
// /repos/{owner}/{repo} metadata endpoint to return the supplied HTTP
// status code. Visibility-lookup soft-fail scenarios (R17) need to be
// able to fail JUST the metadata endpoint while leaving the tarball
// fetch intact, so this is a distinct knob from
// theGitHubFakeReturnsStatusForAtRef.
func theGitHubFakeReturnsStatusForRepoMetadata(ctx context.Context, status int, slug string) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake; call 'a GitHub fake is configured' first")
	}
	owner, repo, parseErr := splitOwnerRepo(slug)
	if parseErr != nil {
		return ctx, parseErr
	}
	s.githubFake.SetRepoMetadataStatus(owner, repo, status)
	return ctx, nil
}

// theGitHubFakeServesRepoMetadataWithBody configures the bare
// /repos/{owner}/{repo} endpoint to return the supplied JSON body. Used
// by adversarial-fixture scenarios (Private:true + Visibility:"public"
// inversion, TOML-injection shaped visibility strings).
func theGitHubFakeServesRepoMetadataWithBody(ctx context.Context, slug string, body *godog.DocString) (context.Context, error) {
	s := getState(ctx)
	if s == nil || s.githubFake == nil {
		return ctx, fmt.Errorf("no GitHub fake; call 'a GitHub fake is configured' first")
	}
	owner, repo, parseErr := splitOwnerRepo(slug)
	if parseErr != nil {
		return ctx, parseErr
	}
	s.githubFake.SetRepoMetadata(owner, repo, body.Content)
	return ctx, nil
}

// splitOwnerRepo parses an owner/repo slug into its two components.
// Returns an error when the slug does not have exactly one slash. Used
// internally by every GitHub-fake step so feature files can use the
// canonical slug form niwa itself accepts.
func splitOwnerRepo(slug string) (string, string, error) {
	parts := strings.SplitN(slug, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid owner/repo slug %q", slug)
	}
	return parts[0], parts[1], nil
}

// itoaSafe stays exported only to silence the "unused import" complaint
// if some future change drops the strconv reference; godog wires strconv
// indirectly through other step files but keeping a literal use here
// keeps go vet quiet in isolation.
//
//nolint:unused // wired by build-tag-free paths
func itoaSafe(n int) string { return strconv.Itoa(n) }
