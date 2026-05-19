# Phase 5 Security Review — init bootstrap from empty source

Scope: `DESIGN-init-bootstrap-empty-source.md` (Proposed). New surface:
`internal/github.StatusError`, `internal/github.APIClient.GetRepo`,
`internal/workspace/scaffold.go:ScaffoldFromSource`,
`internal/workspace/bootstrap.go:RunBootstrap`, and the
`--bootstrap` / `--no-bootstrap` flag pair plus classifier seam in
`internal/cli/init.go`.

## Dimension Analysis

### 1. External artifact handling — APPLIES YES

Bootstrap runs `git init` + `git remote add origin <cloneURL>` +
`git fetch --depth 1 origin HEAD` against a user-supplied remote. Three
sub-risks:

**1a. Remote-helper / URL-handler exploitation via `--from`.**
`workspace.ResolveCloneURL` short-circuits when the input "looks like a
URL" (contains `://`, starts with `git@`, or starts with `/` with two
slashes) and passes the raw string through to git unchanged. git
supports remote helpers (`ext::`, `transport::`) that execute arbitrary
commands. Modern git releases default `protocol.allow` to deny these
unsafe transports, so a hostile `--from "ext::sh -c 'rm -rf ~' #"`
should fail at fetch time rather than execute. Still, the bootstrap
path is the first niwa entry point that runs `git fetch` against a
URL the user just typed *because they want to adopt that repo* — the
intent gate (`--bootstrap`) is exactly the kind of "I trust this URL"
signal an attacker might forge in a paste-from-the-internet attack.

  - Severity: low (relies on git's default protocol allowlist;
    `git -c protocol.allow=user` would be needed to weaken it).
  - Mitigation: the design already constrains v1 to GitHub sources
    ("Bootstrap path checks the source host: GitHub → proceed;
    non-GitHub → refuse"). If that host check happens BEFORE git ever
    sees the URL, the remote-helper vector is closed in v1. The design
    should make this ordering explicit — host check first, then git
    invocations.

**1b. URL passed positionally to git, not via stdin.**
`exec.CommandContext("git", args...)` is used throughout the codebase
(see `clone.go:63`, `sync.go:67`), passing arguments as separate
elements with no shell. So command injection via shell metacharacters
is not in play. The only attack surface is git's own URL parsing.

  - Severity: not applicable — Go's exec.Command bypasses the shell.

**1c. Scaffolded content influenced by remote data.**
The scaffold body is templated from `ScaffoldOptions{Name, Org,
Visibility}`. `Name` comes from the positional arg (validated by
`workspace.ValidateInitName`), `Org` comes from the `--from` slug
(validated by `source.Parse`), and `Visibility` comes from
`GetRepo(...).Private` normalized to `"public"`/`"private"` via the
existing `ListRepos` path. The `private` bool cannot be poisoned to
inject TOML — only those two literal strings reach the template.

  - Severity: not applicable — all three inputs are bound to a closed
    set or pre-validated grammar before they reach the TOML template.
  - One residual: confirm `ScaffoldFromSource` uses the `private` bool
    path, NOT the raw `visibility` JSON string. The design language is
    "reusing the `private` bool → `Visibility` normalization that
    `ListRepos` already does," which is correct; the implementation
    must keep that contract.

**1d. Git fetch tree contents.**
The fetched tree is checked out to `<workspaceRoot>/`. niwa only writes
new files under `.niwa/`; it does not execute anything in the tree.
The classic "malicious file in cloned repo" risk applies only if a
later step (a hook, a code generator) reads tree content. Bootstrap
itself doesn't.

  - Severity: not applicable for this design; flagged for downstream
    callers that might assume the post-bootstrap tree is trusted.

### 2. Permission scope — APPLIES YES

**2a. Privilege escalation.** No new sudo path, no new privileged
syscalls, no new filesystem locations outside `<cwd>/<name>/` and the
already-existing global config. Same trust boundary as today's
`modeClone`. Not a concern.

**2b. Partial-failure on-disk state.**
The defer at `init.go:221-225` rolls back the workspace directory on
failure today; the design says `workspaceCreated = false` disarms it
just before `RunBootstrap` runs, so a panic or non-error exit inside
RunBootstrap leaves the directory behind. The design's `RunBootstrap`
contract says "Idempotent on partial failure (cleans up `.git/` if
init fails; leaves clean state if commit succeeded)" — but does not
specify what happens between those endpoints (after `git fetch`
succeeds but before `git commit` runs). A failure there would leave a
partially-initialized `<cwd>/<name>/` with `.git/`, a feature branch,
and possibly a half-written `.niwa/workspace.toml` — but no rollback,
because the defer was already disarmed.

  - Severity: low (user-visible mess, not security-relevant).
  - Mitigation: either (a) keep the cleanup defer armed until the
    commit succeeds, then disarm; or (b) add a `RunBootstrap`-internal
    cleanup that runs on any error after the directory contents start
    changing. The design should document which it picks.

**2c. Git author/committer.** The design doesn't say. `git commit -m
"..."` without `--author` uses the user's normal git identity from
`user.name` / `user.email`. This is correct (no spoofing) but worth
calling out explicitly: niwa SHOULD NOT pass `--author` or set
`GIT_AUTHOR_*` / `GIT_COMMITTER_*` env vars when invoking commit. The
commit on the bootstrap branch is morally authored by the user, and
should reflect their identity for audit trails.

  - Severity: low if implemented as plain `git commit -m`; not
    applicable.

### 3. Supply chain or dependency trust — APPLIES YES, narrow

**3a. Git hooks from the cloned remote.** A `.git/hooks/` directory
inside a fetched-into-existing-dir repo is taken from the local
filesystem state, not the remote — `git fetch` does not transfer
hooks. `git clone` would copy `<template>/hooks/` from the local git
template dir into the new `.git/hooks/`, not from the remote. So the
bootstrap path does NOT pick up hostile hooks from the remote.

  - Severity: not applicable.

**3b. Scaffolded TOML wires no plugins / hooks / vault.** Decision 4
locks the scaffold to `[workspace]` + `[[sources]]` +
`[groups.<vis>]` plus commented examples. No `[claude.hooks]`, no
`[claude.env.secrets]`, no `[claude.plugins]`, no
`[claude.marketplaces]`. The first `niwa apply` against this workspace
will not execute anything the user hasn't subsequently uncommented and
configured.

  - Severity: not applicable.

### 4. Data exposure — APPLIES YES, low

**4a. Token handling.** Bootstrap calls `GetRepo` via the existing
`resolveGitHubToken()` path, same as `ListRepos`. The token is sent to
the GitHub API host (`NIWA_GITHUB_API_URL` or `api.github.com`) as a
bearer header. No change to the existing token-trust model. The token
is not written to the scaffolded TOML or any other on-disk artifact.

  - Severity: not applicable.

**4b. Stderr success message.** The design specifies a WARNING-style
block naming `workspace path`, `bootstrap branch name`, and next-step
commands. The path is the user's own `<cwd>/<name>/`; the branch name
is fixed `niwa-bootstrap`. No secrets, no tokens, no system data leak
into stderr.

  - Severity: not applicable.

**4c. Registry entry.** `SourceURL` = the `--from` slug, `Root` = the
absolute workspace path — same shape as today's clone path.

  - Severity: not applicable.

**4d. Side-channel via the `NIWA_GITHUB_API_URL` override.**
`internal/github/client.go:41-50` honors `NIWA_GITHUB_API_URL` to
redirect API traffic. A user who has set this env var (intentionally
or via shell config copy-paste) will direct `GetRepo` to a different
host along with their bearer token. This is existing behavior, not
introduced by this design.

  - Severity: existing risk, not introduced here. Flag for future
    hardening (e.g., warn at niwa startup if the override is set).

### 5. Confirmation UX and the explicit-intent gate — APPLIES YES

**5a. Confused-deputy via piped stdin.** The TTY-gated prompt uses
`cli.ReadConfirmation` which reads from `os.Stdin`. `IsStdinTTY()`
checks `term.IsTerminal(os.Stdin.Fd())` before prompting. A script
that pipes `yes | niwa init ...` will NOT reach the prompt because
the piped stdin is not a TTY — the non-TTY-without-flag branch fails
fast with a hint. The design follows the destroy.go template here.

  - Severity: not applicable. The TTY gate closes this vector by
    design.

**5b. Classifier ordering exploit.** The seam at `init.go:265` runs
`AmbiguousMarkers → NoMarker → 401/403 → 404 → generic`. An attacker
controlling the remote at the moment of probe could in principle race
between empty/private/missing, but: (i) the user has already supplied
the slug, so the remote identity is fixed; (ii) only `NoMarker` plus
`--bootstrap` triggers a scaffold + commit. A remote that flips from
"has marker" to "has no marker" between two probes wouldn't help the
attacker — niwa would still scaffold the user's chosen directory and
commit on a feature branch named `niwa-bootstrap` that the user must
manually push.

  - Severity: not applicable.

**5c. Default-Y prompt with piped input that DOES look like a TTY.**
Edge case: an attacker who can MITM the user's terminal multiplexer
or a tool like `expect` could in principle drive the TTY prompt. This
is outside niwa's threat model (full local terminal control implies
full user-account compromise).

  - Severity: not applicable.

### 6. Command injection in git invocations — APPLIES YES, low

The bootstrap orchestrator calls git multiple times (`git init`,
`git remote add origin <url>`, `git fetch --depth 1 origin HEAD`,
`git checkout -b niwa-bootstrap FETCH_HEAD`, `git add .niwa/`,
`git commit -m "..."`). All use `exec.CommandContext` (per the
existing pattern in `clone.go:63`), passing arguments as separate
elements — no shell, no interpolation. The fixed branch name
(`niwa-bootstrap`) and fixed commit message
(`"Initial niwa workspace config"`) are not user-derived.

  - Severity: not applicable for the niwa-controlled args. The only
    user-derived string flowing into git is `<cloneURL>`, which is
    addressed in 1a/1b above.

### 7. Branch-name conflicts / race conditions — APPLIES YES, low

**7a. `niwa-bootstrap` collision.** If the source remote (against all
expectation, given the v1 scope of "empty-source") already has a
branch named `niwa-bootstrap` after the depth-1 fetch, the
`git checkout -b niwa-bootstrap FETCH_HEAD` step fails because `-b`
refuses to create an existing branch. This is a UX issue, not a
security issue — the user sees a git error and bootstrap aborts with
the workspace directory partially populated (see 2b).

  - Severity: not applicable as a security concern; flagged as an
    operational edge case the orchestrator should handle.

**7b. TOCTOU on `<cwd>/<name>/`.** The pre-flight in
`init.go:411-453` uses `os.Lstat` to gate on whether the path exists,
then `os.Mkdir` (NOT `MkdirAll`) at `init.go:217` to create it. The
existing comment notes this closes "the symlink-TOCTOU window between
the Lstat pre-gate and creation." Bootstrap inherits this protection
and does not weaken it.

  - Severity: not applicable.

### 8. Source-host validation ordering — APPLIES YES (raised as a new dimension)

The design states "Bootstrap path checks the source host: GitHub →
proceed; non-GitHub → refuse with v1-supports-GitHub-sources-only."
For this check to close the remote-helper vector in dimension 1a, it
must run BEFORE `git init` / `git remote add` / `git fetch`. The
data-flow diagram puts visibility lookup AFTER `git fetch`, which
suggests the host check might also be late. The design should make
the ordering explicit: host check → clone-URL resolution → git
operations.

  - Severity: low-to-moderate (depends on git's `protocol.allow`
    defaults on the user's system).
  - Recommendation: document the host-check-first ordering in
    Solution Architecture or the Phase 4 deliverable list.

## Recommended Outcome

**Outcome 2 — document considerations.**

The design is structurally sound from a security standpoint. The new
attack surface is bounded: a typed error type, a single-repo metadata
fetch (same trust path as existing `ListRepos`), a sibling scaffold
function, and an orchestrator that calls git via the established
`exec.CommandContext` pattern. The TTY-gated prompt + explicit
`--bootstrap` flag close the confused-deputy vector by construction,
and the scaffold's input set (validated `Name`, validated `Org`, and
`private` bool → enum string) keeps remote-controlled data out of the
generated TOML.

Three small additions to the document would tighten the story without
changing the architecture:

1. **Host-check ordering.** State in Solution Architecture (and
   reaffirm in Phase 4) that the "v1 supports GitHub sources only"
   host check runs BEFORE `git init` / `git remote add` / `git fetch`.
   This closes the remote-helper vector for non-GitHub URLs even if a
   future git release weakens `protocol.allow` defaults.
2. **Partial-failure cleanup contract.** The `RunBootstrap` docstring
   already says "idempotent on partial failure" for the init and
   commit endpoints. Extend it to specify what happens if `fetch`,
   `checkout`, or `scaffold` fails — either keep the workspace-dir
   defer armed until commit succeeds (and disarm only then), or have
   `RunBootstrap` clean up its own intermediate state on every error
   path. Pick one and document it.
3. **Git identity invariant.** Note explicitly that `git commit` runs
   without `--author` and without overriding `GIT_AUTHOR_*` /
   `GIT_COMMITTER_*` env vars, so the bootstrap commit reflects the
   user's normal git identity and produces a truthful audit trail.

None of these warrant blocking the design or reworking the four-phase
implementation plan. They are clarifications that protect already-good
choices from drifting during implementation.

## Summary

The init-bootstrap design adds bounded new surface (a typed GitHub
error, a single-repo metadata fetch, a sibling scaffold, and a
git-orchestrating bootstrap function) and keeps all remote-controlled
data out of the generated TOML by binding inputs to a closed set
(validated name, validated org slug, `private` bool → enum string).
The TTY-gated prompt plus explicit `--bootstrap` flag close the
confused-deputy vector by construction. Three small documentation
clarifications would tighten the story: explicit host-check ordering
before git invocations, an explicit partial-failure cleanup contract
in `RunBootstrap`, and an invariant that the bootstrap commit uses the
user's normal git identity.
