# Security Review: Init Command Design

## Scope

Review of `docs/designs/DESIGN-init-command.md` Security Considerations section against the Solution Architecture and existing codebase (`internal/config/`, `internal/workspace/`).

---

## 1. Attack Vectors Not Considered

### 1.1 TOCTOU race in pre-flight checks (Advisory)

The design specifies: "All checks run before any filesystem writes -- init either succeeds fully or changes nothing." However, between pre-flight checks and the actual filesystem writes (creating `.niwa/`, writing `workspace.toml`), another process could create `.niwa/` or place a symlink there. This is a standard time-of-check-time-of-use gap.

**Practical risk**: Low. Init is a user-interactive command, not a daemon. An attacker with write access to the user's working directory already has broader capabilities. The existing `Cloner.Clone` in `clone.go` has the same TOCTOU pattern (checks `.git` existence then clones), so this is consistent with codebase conventions.

**Verdict**: Advisory. Document as a known limitation but don't block on it.

### 1.2 Symlink attacks on `.niwa/` directory (Blocking)

The design does not address the case where `.niwa/` is a symlink pointing elsewhere. If an attacker (or a previous partial operation) places a symlink at `.niwa/` -> `/some/sensitive/path`, the pre-flight check "Check $PWD/.niwa/ -- refuse if exists" would catch it (symlinks are visible to `os.Stat`). However, Case 1 checks `.niwa/workspace.toml` first. If `.niwa` is a symlink to a directory that contains no `workspace.toml`, Case 3 triggers with the message "Remove .niwa/ manually." But the shallow clone (Mode C) uses `git clone --depth 1 <url> .niwa/` -- git refuses to clone into an existing directory, so the clone would fail safely.

For Mode A/B (scaffold), the code would call `os.MkdirAll` on `.niwa/` which would follow a symlink. But the pre-flight check for Case 3 (`.niwa/ exists but isn't recognized`) catches this first.

**Verdict**: The pre-flight detection order (Case 1 -> Case 3) provides adequate defense. The design should explicitly state that `.niwa/` existence checks use `os.Lstat` (not `os.Stat`) to distinguish symlinks from real directories and refuse on symlinks with a specific error. Currently unspecified. Advisory -- the current check order happens to be safe, but an `Lstat` requirement would make the invariant explicit.

### 1.3 Registry poisoning via name squatting (Blocking)

Mode B says: "check registry for `<name>`. If registered with a source, clone from that source." The global registry at `~/.config/niwa/config.toml` maps names to source repos. If an attacker can write to this file (e.g., via a malicious `niwa init <name> --from <org/repo>` in a shared environment, or via a compromised config repo that runs a post-checkout hook writing to the registry), they can redirect future `niwa init <name>` invocations to a malicious repo.

The design's git-hooks section acknowledges that `--from` is equivalent to `git clone` in trust level. But it doesn't address the transitive risk: a single trusted `--from` that runs a malicious hook could poison the registry for all future `init` calls.

**Mitigations to consider**:
- Log a visible message when a registry lookup redirects to a remote source: "Initializing from registered source: github.com/org/repo (registered on YYYY-MM-DD)"
- Consider adding a `--no-registry` flag to skip registry lookup for security-conscious users.

**Verdict**: Blocking. The design should acknowledge registry poisoning as a risk and specify that registry-sourced clones print the resolved source URL before cloning, giving the user a chance to abort.

### 1.4 Clone URL construction injection (Addressed but incomplete)

The design correctly identifies name validation (`[a-zA-Z0-9._-]+`) as preventing command injection in clone URLs. However, the `--from` flag accepts `<org/repo>` format, which gets split and interpolated into a URL. The design should specify:

1. That `--from` input is validated as exactly `<org>/<repo>` (one slash).
2. That both `org` and `repo` segments individually match `[a-zA-Z0-9._-]+`.
3. That the existing `validName` regex in `config.go:15` is reused (not duplicated).

Looking at the codebase, `config.go` already has `validName = regexp.MustCompile('^[a-zA-Z0-9._-]+$')`. The design should reference this explicitly.

**Verdict**: Advisory. The validation is described in intent but the spec should reference the existing `validName` regex and specify the `org/repo` split-then-validate approach to prevent a parallel validation pattern.

---

## 2. Sufficiency of Identified Mitigations

### 2.1 "Remote config trust" -- Partially sufficient

The design says "Users should only init from repos they trust" and mentions `--review` as future mitigation. This is honest but insufficient as a standalone mitigation. The `--review` flag is explicitly marked as future work, and the design acknowledges it can't mitigate git hooks anyway. The design should recommend that the first implementation print the clone URL and a brief trust notice (e.g., "Cloning config from https://github.com/org/repo -- ensure you trust this source") as a zero-cost awareness measure.

### 2.2 "Git hooks execute during clone" -- Sufficient

The analysis is correct: `git clone` runs hooks, this is inherent to git, and `--review` can't help. The design correctly positions this as equivalent to running `git clone` directly. No additional mitigation needed beyond what git itself provides.

One note: `git clone --depth 1` with `--config core.hooksPath=/dev/null` could disable hooks, but this would break legitimate config repos that rely on hooks. The design's decision to not add this is correct.

### 2.3 "Name validation as security invariant" -- Sufficient with caveat

The regex `[a-zA-Z0-9._-]+` is adequate for preventing shell injection and URL manipulation. The design correctly flags that changes to this charset require security review.

**Caveat**: The existing `clone.go:38` constructs the git command via `exec.CommandContext(ctx, "git", args...)` with separate arguments, which avoids shell interpretation entirely. As long as the implementation continues to use `exec.Command` (not `exec.Command("sh", "-c", ...)`) and passes the URL as a single argument, the name validation is defense-in-depth rather than the sole barrier. The design should note that clone URLs must never be passed through a shell.

### 2.4 "Filesystem safety" -- Sufficient

Pre-flight checks prevent overwrites. Init writes only to `.niwa/` and `claude/` within the current directory. No symlink-following writes to arbitrary paths (given the advisory in 1.2 is addressed).

---

## 3. "Not Applicable" Justification Review

### 3.1 "No secret exposure" -- Correctly scoped out

The design states init doesn't handle secrets. This is accurate: init creates a config scaffold or clones a config repo. Neither operation involves credentials beyond what git itself manages (SSH keys, credential helpers). The registry file at `~/.config/niwa/config.toml` contains only names, sources, and paths -- no tokens.

However, there's an implicit assumption worth making explicit: the cloned config repo itself should not contain secrets. If a team stores API keys in their workspace.toml (which the schema doesn't support, but TOML allows extra fields), those would be cloned to disk. This is a user responsibility, not an init command responsibility. Correctly out of scope.

### 3.2 Implicit "not applicable": denial of service

Not discussed. A malicious or very large config repo could consume disk space during `git clone --depth 1`. This is inherent to git clone and not specific to niwa. Correctly omitted.

### 3.3 Implicit "not applicable": config repo content injection into AI behavior

The design mentions that "cloned workspace.toml and content files direct file writes and shape AI agent behavior." This is the highest-impact risk and deserves more attention than a single sentence. A malicious config repo could:

- Set `content_dir` to point to a directory containing prompt injection content
- Define content entries that install malicious CLAUDE.md files into repos
- Configure hooks (when implemented) that execute arbitrary commands

The existing `validateContentSource` in `config.go:173` prevents path traversal in content sources, which is good. But the init command's trust boundary is the config repo itself -- if you trust the repo, you trust its content. The design's one-sentence treatment is adequate as a security consideration, but it should cross-reference the existing path traversal validation as evidence that downstream apply operations are defended.

**Verdict**: Not blocking. The trust model (trust the config repo) is sound and consistent with git's own model. The downstream validations exist in the codebase. A cross-reference would strengthen the design.

---

## 4. Residual Risk Assessment

### 4.1 Should be escalated: Registry poisoning (from 1.3)

A compromised config repo's git hooks can modify `~/.config/niwa/config.toml`, redirecting future workspace inits to attacker-controlled repos. This is a privilege escalation within the niwa trust model: a one-time trust decision (cloning repo X) silently becomes a persistent trust delegation (all future `init <name>` goes to repo X, even if the user didn't intend that).

**Recommended mitigation**: Print the resolved source URL when initializing from registry. This is low-cost and makes the trust chain visible.

### 4.2 Acceptable residual risk: Git hook execution

Inherent to git. Users who run `niwa init --from` should have the same trust posture as running `git clone`. No escalation needed.

### 4.3 Acceptable residual risk: Malicious scaffold content

The scaffold template is a Go string constant (per the design). No user-controlled input enters the template content. No risk here.

---

## Summary of Findings

| # | Finding | Severity | Section |
|---|---------|----------|---------|
| 1 | TOCTOU race in pre-flight checks | Advisory | 1.1 |
| 2 | Symlink handling on `.niwa/` unspecified -- should require `Lstat` | Advisory | 1.2 |
| 3 | Registry poisoning via git hooks: resolved URL not shown to user | Blocking | 1.3 |
| 4 | `--from` input validation should reference existing `validName` regex | Advisory | 1.4 |
| 5 | Should print trust notice with clone URL before cloning | Advisory | 2.1 |
| 6 | Clone URLs must use exec.Command args, never shell interpolation | Advisory | 2.3 |
| 7 | Cross-reference path traversal validation in content source handling | Advisory | 3.3 |

**Blocking: 1** (registry poisoning visibility)
**Advisory: 6**
