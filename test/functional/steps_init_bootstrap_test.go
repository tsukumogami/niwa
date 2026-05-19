package functional

import (
	"context"
	"fmt"
	"os/exec"
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
		wrapper + "/":                       "",
		wrapper + "/.niwa/":                 "",
		wrapper + "/.niwa/workspace.toml":   "[workspace]\nname = \"" + repo + "\"\n",
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

// iRunUnderPTYWithInput drives the niwa binary under util-linux
// `script -q -c <cmd> /dev/null`, which allocates a real pty and
// connects it to the child's stdin/stdout. This is the test seam for
// R13 TTY-Y / TTY-N scenarios that need the binary's IsStdinTTY()
// check to return true. Input lines are joined with "\n" and fed via
// a temp file the wrapper redirects in.
//
// `script` is the POSIX util-linux command; it ships on every Linux
// CI image and on macOS via Homebrew. Adding a Go pty library
// (github.com/creack/pty) was considered and rejected to avoid a new
// dependency.
func iRunUnderPTYWithInput(ctx context.Context, command, input string) (context.Context, error) {
	s := getState(ctx)
	if s == nil {
		return ctx, fmt.Errorf("no test state")
	}
	if _, err := exec.LookPath("script"); err != nil {
		return ctx, fmt.Errorf("util-linux `script` not on PATH; cannot drive PTY scenario: %w", err)
	}

	// Substitute {repo:<name>} placeholders for symmetry with iRunFromWorkspaceRoot.
	for repoName, repoURL := range s.repoURLs {
		command = strings.ReplaceAll(command, "{repo:"+repoName+"}", repoURL)
	}

	args := strings.Fields(command)
	if len(args) > 0 && args[0] == "niwa" {
		args[0] = s.binPath
	}

	// Build the inner command. We change directory to workspaceRoot
	// then exec the binary so it inherits the pty `script` allocated.
	// Without `exec`, an intermediate bash would steal the pty and the
	// child's IsStdinTTY check would observe a pipe.
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	innerCmd := "cd " + shellQuote(s.workspaceRoot) + " && exec " + strings.Join(quoted, " ")

	// `script -q -c <cmd> /dev/null` runs cmd under a pty and writes
	// the terminal-output transcript to /dev/null. We capture the
	// child's output via the script process's stdout (which mirrors
	// the pty master side). The child's stdin is fed by writing to
	// script's own stdin via cmd.Stdin — script forwards stdin bytes
	// to the pty so the child sees them as terminal input.
	cmd := exec.CommandContext(ctx, "script", "-q", "-c", innerCmd, "/dev/null")
	cmd.Env = s.buildEnv()
	// Convert "\n" escapes (so feature files can write `y\n`).
	rawInput := strings.ReplaceAll(input, `\n`, "\n")
	cmd.Stdin = strings.NewReader(rawInput)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()
	s.stdout = stdout.String()
	// util-linux `script` interleaves stdout and stderr on its single
	// PTY surface; the child's stderr is mirrored on stdout under PTY.
	// Treat the combined output as both for assertion purposes — both
	// fields contain the same bytes so any "error output contains"
	// step sees the prompt + Detail+Suggestion text.
	s.stderr = stdout.String() + stderr.String()
	s.shellPwd = ""
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			s.exitCode = exitErr.ExitCode()
			return ctx, nil
		}
		return ctx, fmt.Errorf("pty run failed: %w; stderr: %s", runErr, s.stderr)
	}
	s.exitCode = 0
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

// shellQuote escapes s for use inside a `bash -c` string. Single-quotes
// are doubled with the standard `'\''` trick. Used by iRunUnderPTYWithInput
// to thread arbitrary command strings through `script -c` safely.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// itoaSafe stays exported only to silence the "unused import" complaint
// if some future change drops the strconv reference; godog wires strconv
// indirectly through other step files but keeping a literal use here
// keeps go vet quiet in isolation.
//
//nolint:unused // wired by build-tag-free paths
func itoaSafe(n int) string { return strconv.Itoa(n) }
