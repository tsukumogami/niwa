package watch

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

// commitSHAPattern matches a full or abbreviated commit SHA. The PR head SHA
// comes from the GitHub API (platform-vouched), but it is still validated
// before it is passed to git so a malformed value cannot become an argument
// that git interprets as something other than an object name.
var commitSHAPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// gitHardeningConfig is the set of `git -c` flags that neuter every
// checkout-time program an attacker-authored tree could otherwise trigger. It
// is applied to BOTH the fetch and the checkout. Combined with the hardened
// environment (see hardenedGitEnv), it means populating the working tree runs
// no code the PR author controls.
var gitHardeningConfig = []string{
	"-c", "core.hooksPath=/dev/null", // repo hooks never run
	"-c", "core.attributesFile=/dev/null", // ignore any external attributes file
	"-c", "filter.lfs.smudge=", // no git-lfs smudge on checkout
	"-c", "filter.lfs.process=",
	"-c", "filter.lfs.required=false",
	"-c", "protocol.ext.allow=never", // no ext:: transport (the arbitrary-command vector)
	"-c", "core.fsmonitor=", // no fsmonitor program (empty reads as false)
	"-c", "core.sshCommand=" + pinnedSSHCommand, // no configured ssh program
}

// pinnedSSHCommand is the ssh program every hardened git run uses, so no
// core.sshCommand from any config can name another. The fetch environment carries
// no GIT_SSH or GIT_SSH_COMMAND, which would outrank it.
const pinnedSSHCommand = "ssh"

// Note on the file:: transport: it is NOT globally blocked because the primary
// fetch URL is niwa-controlled (the workspace repo's own clone URL, never an
// attacker-supplied value) and submodule recursion is disabled on every fetch
// (--no-recurse-submodules), so there is no attacker-supplied URL for git to
// follow. The dangerous transport is ext:: (runs arbitrary commands), which is
// blocked above.

// FetchPRHead fetches a specific commit SHA from remoteURL and lays its tree
// out as ordinary files under checkoutDir, WITHOUT running any checkout-time
// program the PR author could control (LFS smudge, custom filter drivers, repo
// hooks, submodule recursion). This runs in trusted niwa code, before the OS
// sandbox exists, so the hardening is load-bearing: a naive `git fetch` +
// checkout of attacker content can execute code and egress on its own.
//
// The exposure primitive is a filter-neutered checkout (not `git archive`,
// which honors `.gitattributes export-ignore` and would let an attacker hide a
// file from the reviewed tree). The agent then reads a faithful, ordinary file
// tree.
//
// checkoutDir may be an existing clone the review agent has written to (a
// continuation fetches into the review's own clone), so no git run here ever reads
// a git dir that existed before the call. Whatever is at checkoutDir is removed
// first without following symlinks: a .git directory with planted config, hooks or
// includes, a .git symlink or "gitdir:" file pointing elsewhere, or a checkoutDir
// that is itself a symlink. The fetch and checkout then go through a fresh git dir
// niwa creates outside checkoutDir, named on every git run with --git-dir and
// --work-tree, so git does no repository discovery in the clone. Once the checkout
// is done, that git dir is moved into checkoutDir/.git so the review can run git
// there; when it can't be moved (the temporary directory is on another
// filesystem), the clone is left without one. niwa runs no further git in the
// clone, so what the agent later writes to that .git never reaches niwa's process.
//
// Residual: while a review session is still running (today's continuation fetches
// before it stops the session), sandboxed Bash could swap checkoutDir for a symlink
// between its removal and the checkout. That can redirect where the PR's files are
// written, never what git reads as config or hooks.
//
// sha must be a validated commit SHA. token, when non-empty, authenticates the
// fetch to a private repo via an HTTP auth header (the fetch is trusted niwa code
// and legitimately needs repo read access; the token is passed to git, never into
// the contained session's environment). With no repo config read, no
// url.<base>.insteadOf can redirect the fetch, and the header with it, elsewhere.
func FetchPRHead(ctx context.Context, remoteURL, sha, checkoutDir, token string) error {
	if !commitSHAPattern.MatchString(sha) {
		return fmt.Errorf("fetch: refusing malformed commit SHA %q", sha)
	}
	if remoteURL == "" {
		return fmt.Errorf("fetch: empty remote URL")
	}
	// Git runs from a scratch directory, so the work tree must be absolute. Abs also
	// cleans the path: a trailing slash would make the removal below follow a
	// symlink at checkoutDir.
	checkoutDir, err := filepath.Abs(checkoutDir)
	if err != nil {
		return fmt.Errorf("fetch: resolving checkout dir: %w", err)
	}
	if err := resetCheckoutDir(checkoutDir); err != nil {
		return err
	}

	// A fetch-local HOME with no developer dotfiles, so no global/user gitconfig
	// (which could register an LFS or custom filter) is consulted. The fresh git
	// dir lives under it too, outside checkoutDir.
	gitHome, err := os.MkdirTemp("", "niwa-watch-fetch-home-")
	if err != nil {
		return fmt.Errorf("fetch: creating fetch-local HOME: %w", err)
	}
	defer os.RemoveAll(gitHome)
	// The auth header, when present, is injected via git's environment-variable
	// config mechanism (GIT_CONFIG_COUNT/KEY/VALUE) -- NOT a `-c` argv flag --
	// so the token never appears in the process command line (readable by any
	// local user via `ps` / /proc/<pid>/cmdline). The environment is only
	// readable by the process owner and root.
	env := hardenedGitEnv(gitHome, token)

	// 1. init an empty repo we control, outside checkoutDir (its config is clean).
	repo := filepath.Join(gitHome, "repo")
	if err := runGit(ctx, gitHome, env, "init", "--quiet", repo); err != nil {
		return fmt.Errorf("fetch: git init: %w", err)
	}
	gitDir := filepath.Join(repo, ".git")
	scope := []string{"--git-dir=" + gitDir, "--work-tree=" + checkoutDir}
	// 2. fetch the exact SHA as inert objects. --no-tags and
	//    --no-recurse-submodules keep the fetch from following anything beyond
	//    the requested object.
	fetchArgs := append(append([]string{}, scope...), hardenedFetchArgs(remoteURL, sha)...)
	if err := runGit(ctx, gitHome, env, fetchArgs...); err != nil {
		return fmt.Errorf("fetch: git fetch %s: %w", sha, err)
	}
	// 3. checkout the fetched SHA with filters neutered -- populates the working
	//    tree with raw blob contents and runs no smudge/filter/hook.
	checkoutArgs := append(append([]string{}, scope...), hardenedCheckoutArgs(sha)...)
	if err := runGit(ctx, gitHome, env, checkoutArgs...); err != nil {
		return fmt.Errorf("fetch: git checkout %s: %w", sha, err)
	}
	// 4. hand the clone its git dir, best effort (see above).
	dotGit := filepath.Join(checkoutDir, ".git")
	_ = os.RemoveAll(dotGit)
	_ = os.Rename(gitDir, dotGit)
	return nil
}

// resetCheckoutDir removes whatever is at checkoutDir, without following a symlink
// at it or anywhere under it, and creates it again as an empty directory. Creating
// it with Mkdir (not MkdirAll) fails if something took its place in between.
func resetCheckoutDir(checkoutDir string) error {
	if err := os.RemoveAll(checkoutDir); err != nil {
		return fmt.Errorf("fetch: clearing checkout dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(checkoutDir), 0o755); err != nil {
		return fmt.Errorf("fetch: creating checkout dir parent: %w", err)
	}
	if err := os.Mkdir(checkoutDir, 0o755); err != nil {
		return fmt.Errorf("fetch: creating checkout dir: %w", err)
	}
	return nil
}

// hardenedFetchArgs builds the git argv for the hardened fetch. It NEVER
// contains the auth token (that rides the environment, see hardenedGitEnv), so
// the token cannot leak via `ps` / /proc/<pid>/cmdline.
func hardenedFetchArgs(remoteURL, sha string) []string {
	args := append([]string{}, gitHardeningConfig...)
	return append(args,
		"fetch", "--no-tags", "--no-recurse-submodules", "--depth", "1", remoteURL, sha)
}

// hardenedCheckoutArgs builds the git argv for the filter-neutered checkout of sha.
func hardenedCheckoutArgs(sha string) []string {
	args := append([]string{}, gitHardeningConfig...)
	return append(args, "checkout", "--quiet", "--force", sha, "--")
}

// hardenedGitEnv builds the environment for a hardened git invocation: an
// isolated gitconfig (no system, a scratch HOME), LFS smudge skipped, and no
// interactive credential prompt. When token is non-empty, the HTTP auth header
// is injected via git's environment-variable config mechanism so it stays off
// the command line.
//
// The header is HTTP Basic with the token as the password (username
// "x-access-token"), which is what GitHub's git-over-HTTPS transport accepts --
// the same form GitHub Actions' checkout uses. A `Bearer` header is accepted by
// the REST API but rejected by the git transport (401), which then makes git
// fall back to a credential prompt and fail under GIT_TERMINAL_PROMPT=0; a
// malformed auth header is worse than none, since it also breaks the
// no-auth-needed public-repo case.
func hardenedGitEnv(gitHome, token string) []string {
	// Start from a minimal base rather than os.Environ() so no ambient
	// GIT_* / credential variables leak in.
	base := []string{
		"HOME=" + gitHome,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(gitHome, "nonexistent-gitconfig"),
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_TERMINAL_PROMPT=0",
	}
	if p := os.Getenv("PATH"); p != "" {
		base = append(base, "PATH="+p)
	}
	if token != "" {
		cred := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
		base = append(base,
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.extraheader",
			"GIT_CONFIG_VALUE_0=Authorization: Basic "+cred,
		)
	}
	return base
}

// runGit runs a git command in dir with the given environment.
func runGit(ctx context.Context, dir string, env []string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}
