// Package guardrail holds cross-cutting safety checks that run during
// apply but are not themselves part of the resolver, materializer, or
// config parser. Each guardrail lives in its own narrowly-scoped entry
// point so wiring them into apply.go is explicit: a new guardrail is a
// new call site, not a new bit on a shared struct.
package guardrail

import (
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/tsukumogami/niwa/internal/config"
)

// githubHTTPSRemote matches a GitHub HTTPS clone URL. The hostname is
// anchored to github.com only (no GHE, no case variants of other
// domains); the (?i) flag makes the match case-insensitive so that
// GitHub.com and GITHUB.COM both count. Optional `user@` credentials,
// optional `.git` suffix, and an optional trailing slash are all
// accepted because they're all valid clone URL shapes that `git clone`
// itself tolerates.
var githubHTTPSRemote = regexp.MustCompile(
	`(?i)^https?://([\w.-]+@)?github\.com/[\w.-]+/[\w.-]+(\.git)?/?$`,
)

// githubSSHRemote matches a GitHub SSH clone URL of the form
// `git@github.com:org/repo(.git)?`. Case-insensitive on the hostname
// only; `git@` is the canonical user for GitHub SSH clones, so we don't
// generalize the user portion.
var githubSSHRemote = regexp.MustCompile(
	`(?i)^git@github\.com:[\w.-]+/[\w.-]+(\.git)?$`,
)

// remoteLineFields returns the first two whitespace-separated fields
// from a `git remote -v` line. Each such line has the shape:
//
//	<name> <url> (fetch)
//	<name> <url> (push)
//
// name and url are returned; the (fetch)/(push) trailer is ignored. If
// the line has fewer than two fields it returns two empty strings.
func remoteLineFields(line string) (string, string) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", ""
	}
	return fields[0], fields[1]
}

// isGitHubPublicRemote reports whether url is a GitHub HTTPS or SSH
// clone URL on github.com (case-insensitive). GHE hosts like
// github.mycorp.com, gitlab.com, and bitbucket.org all return false —
// the v1 guardrail is explicitly GitHub-only (PRD deferred scope).
func isGitHubPublicRemote(url string) bool {
	if url == "" {
		return false
	}
	return githubHTTPSRemote.MatchString(url) || githubSSHRemote.MatchString(url)
}

// enumerateGitHubRemotes runs `git -C <configDir> remote -v` and
// returns the sorted, deduplicated list of unique URLs that match a
// public GitHub pattern. `git remote -v` lists every remote twice (one
// `(fetch)` line and one `(push)` line, often with identical URLs);
// this function collapses those duplicates before classifying.
//
// The second return value is true iff the git subprocess succeeded AND
// produced at least one parseable remote line. A false second return
// means "no git working tree (or no remotes at all)" and callers should
// skip the guardrail with a warning.
func enumerateGitHubRemotes(configDir string) (matches []string, haveGit bool) {
	cmd := exec.Command("git", "-C", configDir, "remote", "-v")
	// Silence git's own "fatal: not a git repository" stderr when
	// configDir is not a git tree. The guardrail emits its own
	// single-line "guardrail skipped" warning in that case; the raw
	// git error would be redundant noise in test output and in the
	// niwa apply log.
	cmd.Stderr = io.Discard
	out, err := cmd.Output()
	if err != nil {
		return nil, false
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, false
	}

	seen := map[string]struct{}{}
	for _, line := range strings.Split(trimmed, "\n") {
		_, url := remoteLineFields(line)
		if url == "" {
			continue
		}
		if _, dup := seen[url]; dup {
			continue
		}
		seen[url] = struct{}{}
		if isGitHubPublicRemote(url) {
			matches = append(matches, url)
		}
	}
	sort.Strings(matches)
	return matches, true
}

// offendingKeys returns the sorted list of plaintext secret-table keys
// in cfg. An entry is "offending" when:
//
//   - it lives in a *.secrets table (never in *.vars — the whole point
//     of the vars/secrets split is that vars values aren't secret-class);
//   - its MaybeSecret has a non-empty Plain AND has not been promoted
//     to a resolved secret (IsSecret() == false). A resolved vault-ref
//     has IsSecret() == true and is skipped.
//
// The keys returned are env var names only; no values are included in
// the output. This is deliberate (PRD R22: diagnostics never contain
// secret bytes).
//
// All four [env.secrets] and [claude.env.secrets] locations in the
// schema are walked: workspace-level, per-repo overrides, and the
// instance override. Description sub-tables (.required, .recommended,
// .optional) are never walked — those are metadata, not values.
func offendingKeys(cfg *config.WorkspaceConfig) []string {
	if cfg == nil {
		return nil
	}
	keys := map[string]struct{}{}

	walk := func(t config.EnvVarsTable) {
		for k, v := range t.Values {
			if v.IsSecret() {
				continue
			}
			if v.Plain == "" {
				continue
			}
			keys[k] = struct{}{}
		}
	}

	walk(cfg.Env.Secrets)
	walk(cfg.Claude.Env.Secrets)

	for _, repo := range cfg.Repos {
		walk(repo.Env.Secrets)
		if repo.Claude != nil {
			walk(repo.Claude.Env.Secrets)
		}
	}

	walk(cfg.Instance.Env.Secrets)
	if cfg.Instance.Claude != nil {
		walk(cfg.Instance.Claude.Env.Secrets)
	}

	if len(keys) == 0 {
		return nil
	}
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CheckGitHubPublicRemoteSecrets enforces PRD R14/R30: a workspace
// config repo with a public GitHub remote must not carry plaintext
// values in its *.secrets tables. The function enumerates every git
// remote in configDir, matches them against the GitHub HTTPS/SSH
// patterns (case-insensitive on hostname; github.com only), and if any
// match AND cfg carries plaintext secrets:
//
//   - When allowPlaintextSecrets is true: emits a loud stderr warning
//     naming the offending remotes and keys, then returns nil so apply
//     proceeds. The flag is one-shot by contract — no state is written,
//     so the next apply re-runs the check.
//   - When allowPlaintextSecrets is false: returns a structured error
//     naming the offending remotes and keys. The error message points
//     at vault://refs as the migration path and at
//     --allow-plaintext-secrets as the escape hatch.
//
// If configDir is not a git working tree (or has no remotes at all),
// the guardrail cannot meaningfully run and emits a single-line stderr
// warning, returning nil. This is the documented offline/unclonable
// path; it is NOT silent, so users who expected the check can see it
// was skipped.
//
// Non-GitHub remotes (GHE, GitLab, Bitbucket) are not flagged — v1
// scope is strictly github.com. The regex deliberately rejects
// github.mycorp.com; future guardrails can extend the match set without
// changing this entry point's signature.
//
// No secret values are ever emitted by this function (PRD R22). Error
// and warning text includes remote URLs and env-var KEY names only.
func CheckGitHubPublicRemoteSecrets(
	configDir string,
	cfg *config.WorkspaceConfig,
	allowPlaintextSecrets bool,
	stderr io.Writer,
) error {
	remotes, haveGit := enumerateGitHubRemotes(configDir)
	if !haveGit {
		fmt.Fprintln(stderr,
			"warning: no git remotes detected; public-repo guardrail skipped")
		return nil
	}
	if len(remotes) == 0 {
		return nil
	}

	keys := offendingKeys(cfg)
	if len(keys) == 0 {
		return nil
	}

	if allowPlaintextSecrets {
		fmt.Fprintf(stderr,
			"warning: proceeding with plaintext secrets despite public-GitHub "+
				"remote(s) %s. Offending keys: %s. "+
				"--allow-plaintext-secrets is one-shot — next `niwa apply` "+
				"will re-check.\n",
			strings.Join(remotes, ", "),
			strings.Join(keys, ", "),
		)
		return nil
	}

	var b strings.Builder
	b.WriteString("plaintext secrets in a public-remote config repo are blocked.\n")
	b.WriteString("niwa refuses to apply because ALL of the following are true:\n")
	fmt.Fprintf(&b, "  - the workspace config repo has a public-GitHub remote: %s\n",
		strings.Join(remotes, ", "))
	b.WriteString("  - [env.secrets] (or [claude.env.secrets]) contains plaintext values:\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "      - %s\n", k)
	}
	b.WriteString("\n")
	b.WriteString("Move these values into a vault-backed reference (e.g., `vault://<key>`)\n")
	b.WriteString("and re-run `niwa apply`. If you intentionally need to keep plaintext\n")
	b.WriteString("for one exceptional run, pass `--allow-plaintext-secrets` (one-shot;\n")
	b.WriteString("not remembered across invocations). See niwa status --audit-secrets\n")
	b.WriteString("to enumerate all plaintext values in the workspace.")
	return fmt.Errorf("%s", b.String())
}
