# Decision 5: Public-Repo Guardrail Placement and Detection

Where the R14/R30 public-repo plaintext-secret guardrail lives in niwa's
code layering, and how it detects a public GitHub remote. The PRD
requires: enumerate ALL git remotes (not just `origin`), match GitHub
HTTPS and SSH URL patterns, and — when a vault is configured and any
`*.secrets` value is not a `vault://` reference — block `niwa apply`
unless the user passes `--allow-plaintext-secrets`. Non-GitHub hosts are
explicitly out of scope in v1 (R14). The architect review (S-1) flagged
that git-remote classification must not leak into `internal/config/`.

## Options Evaluated

### Option 1: URL pattern match only (offline)

Enumerate remotes via `git remote -v` (shell-out, per R20's "no new Go
deps" constraint), match each fetch URL against a narrow set of GitHub
URL patterns (HTTPS `https://github.com/<owner>/<repo>[.git]`, SSH
`git@github.com:<owner>/<repo>[.git]`, `ssh://git@github.com/...`),
trigger the guardrail when any remote matches. No network call.

Shape:

- New package `internal/guardrail` with a narrowly-named entry point
  `CheckGitHubPublicRemoteSecrets(ctx, configDir, cfg, allowPlaintext) error`.
- Internal helper `classifyGitHubRemote(url string) (host, owner, repo, matched bool)`
  with unit tests for all URL permutations.
- `apply.go` calls the guardrail once per apply, after parse+resolve and
  BEFORE merge/materialize (see "Placement" below).

Trade-offs:

- Works offline, millisecond-scale cost, no rate limits, no auth.
- Consistent with how niwa already shells out for every other git
  operation (`internal/workspace/configsync.go:23`, `sync.go:41`,
  `clone.go:60`) — no new subprocess patterns.
- Cannot distinguish a public GitHub repo from a private repo hosted
  on github.com. It flags BOTH. That is the correct default posture:
  a config repo on github.com whose visibility is ambiguous should be
  treated as a commit risk until the team asserts otherwise. This
  matches R14's conservative intent ("refuse to proceed").
- False positives (private GitHub repos flagged) are addressable via
  `--allow-plaintext-secrets` (one-shot) or by migrating to vault refs
  — both are the right remediation regardless of visibility.

### Option 2: URL pattern match + unauthenticated GitHub API probe

Pattern-match to shortlist GitHub-matching remotes, then hit
`GET https://api.github.com/repos/<owner>/<repo>` unauthenticated per
match. 200 => public, 404 => private, other => treat as public (fail
closed). Already-used `internal/github` package can be extended.

Trade-offs:

- Accurate: the guardrail fires only on actually-public repos,
  eliminating the "private GitHub repo false positive."
- Requires network on every apply. Breaks offline applies (a flow
  niwa otherwise supports — `SyncConfigDir` is a no-op when there is no
  remote; applies still work).
- Unauthenticated github.com rate limit is 60 requests/hour per IP. A
  workspace with several remotes and a few applies per hour can exhaust
  it. Fail-closed on rate-limit means the guardrail becomes flaky.
- Authenticated would use the `gh` token niwa already resolves
  (`internal/cli/token.go:20`), but then the guardrail depends on the
  user being `gh auth`'d — a cross-cutting dependency the apply path
  does not currently have.
- The PRD's "milliseconds" expectation is hard to meet: a cold HTTP
  call over the network is tens to hundreds of ms per remote.
- The security value of distinguishing public from private on github.com
  is low relative to the cost: a private github.com repo is still a
  SaaS-hosted location where committed plaintext secrets are visible
  to repo collaborators and GitHub staff. Blocking plaintext there is
  defensible conservative default.

### Option 3: Explicit `public = true` in workspace.toml (no auto-detection)

The team's workspace.toml declares `[workspace] public = true` (or a
comparable field). Guardrail fires only when that flag is set. No git
enumeration.

Trade-offs:

- Simplest implementation; entirely inside `internal/config/` schema.
- Violates R14's normative requirement that detection "MUST enumerate
  ALL configured git remotes" — the PRD explicitly requires auto
  detection from git remotes, not a declared flag.
- Single point of human failure: a team forgets the flag, a plaintext
  secret ships to a public repo, the guardrail never fires.
- Architect review S-1's concern about keeping `internal/config/`
  ignorant of git is satisfied, but for the wrong reason — by deleting
  detection entirely.

### Option 4: Hybrid — pattern match as hard block, API probe as opt-in refinement

Default behavior is Option 1 (URL pattern match is the hard block).
Advanced users can opt into Option 2 (API probe) via a flag or config
setting that narrows the hard block to verified-public remotes only.
E.g., `--confirm-public-via-api` or `[workspace] public_remote_detection = "api"`.

Trade-offs:

- Preserves the conservative default of Option 1.
- Adds a user-facing knob that is hard to name, hard to document, and
  rarely used. The population of users who hit a false positive on a
  private github.com repo AND who know to reach for this flag is tiny.
- For the small population that does hit false positives, the existing
  escape hatch `--allow-plaintext-secrets` already exists and is one
  shot per invocation. Adding a second opt-out mechanism confuses the
  threat model: which flag does what? When do I use which?
- More code surface to maintain (HTTP path, rate-limit handling, token
  threading) for low incremental security value.

### Option 5: Pattern match + `gh` CLI probe

Pattern match to filter GitHub-matching remotes, then shell out to
`gh repo view <owner>/<repo> --json visibility --jq .visibility`.
Authenticated via the user's existing `gh` session (niwa already uses
`gh auth token`).

Trade-offs:

- Accuracy of Option 2 without rate-limit fragility (authenticated
  requests have a 5000/hr limit).
- Requires `gh` to be installed and authed. niwa uses `gh auth token`
  for source enumeration (`internal/cli/token.go:20`), but the token
  resolver treats failure gracefully (falls back to unauthenticated).
  A guardrail that hard-requires `gh` makes a cross-cutting runtime
  dependency a blocker for apply on a git repo that niwa was happy to
  clone via `git clone` without `gh`.
- `gh repo view` is slower than pattern match (subprocess spawn +
  HTTP). Multiple remotes means multiple sequential `gh` calls unless
  we parallelize, which adds complexity.
- Same conceptual issue as Option 2: distinguishing public from private
  on github.com is low-value given the threat model — any committed
  plaintext in a SaaS-hosted repo is a liability.

## Chosen

**Option 1: URL pattern match only, in a new `internal/guardrail` package,
with a narrowly-named entry point `CheckGitHubPublicRemoteSecrets`.**

The guardrail lives in `internal/guardrail/githubpublic.go`. It
enumerates remotes by running `git -C <configDir> remote -v` (shell
out, no Go git library per R20), matches each unique fetch URL against
a set of GitHub-only URL regexes, and — if any match — enumerates the
merged team config's `[env.secrets]` and `[claude.env.secrets]` tables
for non-`vault://` values. If the intersection is non-empty and no
`--allow-plaintext-secrets` flag is set, apply fails with a structured
error listing the offending keys. The guardrail is invoked from
`apply.go:runPipeline` AFTER parse and vault resolution but BEFORE
merge — see "Placement" below.

## Rationale

Option 1 is the only option that is both cheap enough to run on every
apply (milliseconds, no network) and faithful to R14's requirement to
enumerate all configured git remotes. The "false positive on private
github.com repos" concern is the right default posture: committed
plaintext in a github.com repo is a commit risk regardless of
visibility state, and the `--allow-plaintext-secrets` one-shot escape
hatch (R30) already handles the rare case where a user knowingly
accepts the risk. Options 2 and 5 add network latency and a cross-
cutting dependency (rate-limited or `gh`-required) for a distinction
that does not meaningfully change the threat model. Option 3 violates
R14's explicit "MUST enumerate ALL configured git remotes" requirement.
Option 4 adds user-facing surface area for a knob that the existing
one-shot override already covers.

## Rejected

- **Option 2 (API probe)** — rejected: breaks offline applies, costs
  tens-to-hundreds of ms per remote, and unauthenticated rate limits
  (60/hr) make the guardrail flaky on a hot workspace. The accuracy
  gain (distinguishing private github.com repos) doesn't justify the
  cost given the threat model.
- **Option 3 (explicit `public = true` flag)** — rejected: violates
  R14's normative "MUST enumerate ALL configured git remotes" language
  and creates a single point of human failure.
- **Option 4 (hybrid with opt-in API probe)** — rejected: adds a
  second opt-out mechanism that overlaps with `--allow-plaintext-secrets`
  without meaningfully improving security. Two escape hatches for the
  same invariant is a documentation and support burden with no clear
  decision tree for users.
- **Option 5 (`gh repo view` probe)** — rejected: introduces a hard
  runtime dependency on `gh` for a check that niwa today runs without
  it, and adds subprocess spawn latency for low incremental value. The
  conservative default of Option 1 is the right posture.

## Placement in Code Layering

**Package:** `internal/guardrail/` (new). Narrow name.

**Files:**

- `internal/guardrail/githubpublic.go` — the entry point
  `CheckGitHubPublicRemoteSecrets(configDir string, cfg *config.WorkspaceConfig, allowPlaintext bool) error`
  and URL classifier `isGitHubRemote(url string) bool` (unexported).
- `internal/guardrail/githubpublic_test.go` — table-driven URL pattern
  tests (HTTPS with/without `.git`, SSH `git@` form, `ssh://git@`
  form, hosts other than github.com, enterprise github variants,
  malformed inputs).

**Imports:**

- `internal/config` — to read `cfg.Env.Secrets`, `cfg.Claude.Env.Secrets`
  (read only; no git types pushed into config).
- Standard library only for `git remote -v`: `os/exec`, `regexp`,
  `strings`.
- Does NOT import `internal/github` (no API client needed).
- Does NOT import `internal/workspace` (would introduce a cycle; the
  guardrail is called FROM workspace).

**Architectural contract (S-1 compliance):**

- `internal/config/` does not import `internal/guardrail`. The config
  package stays ignorant of git, matching architect review S-1.
- `internal/guardrail/` depends only on `internal/config` types; it
  does not import workspace types.
- `internal/workspace/apply.go` is the sole caller. It owns the
  ordering decision (when to fire the guardrail relative to pipeline
  stages).

**Integration point in `apply.go`:**

`runPipeline` adds one new step immediately after vault resolution
(Decision 1) and BEFORE `MergeGlobalOverride`:

```
// Step 2b (new): public-repo plaintext-secret guardrail (R14/R30).
if err := guardrail.CheckGitHubPublicRemoteSecrets(
    configDir, resolvedCfg, a.AllowPlaintextSecrets,
); err != nil {
    return nil, err
}
```

The `Applier` struct gains a new `AllowPlaintextSecrets bool` field,
wired from a `--allow-plaintext-secrets` cobra flag in
`internal/cli/apply.go`. The flag is NOT persisted; it is a pure
per-invocation boolean, satisfying R30's "strictly one-shot"
requirement.

## Detection Algorithm

Executed once per `niwa apply` invocation:

1. **Enumerate remotes.** Run `git -C <configDir> remote -v` via
   `exec.Command`. Parse output line by line; collect unique fetch
   URLs (the `(fetch)` suffixed rows). If `git` exits non-zero (not a
   git repo, git not installed), return nil — no remotes means no
   guardrail trigger. This matches `SyncConfigDir`'s tolerant
   posture (`configsync.go:19`).
2. **Classify each URL.** For each unique fetch URL, apply the GitHub
   URL patterns:
   - HTTPS: `^https://(?:[^@]+@)?github\.com/([^/]+)/([^/.]+?)(?:\.git)?/?$`
   - SSH (scp-like): `^git@github\.com:([^/]+)/([^/.]+?)(?:\.git)?/?$`
   - SSH (URL): `^ssh://git@github\.com/([^/]+)/([^/.]+?)(?:\.git)?/?$`
   Hosts other than exactly `github.com` do NOT match (Enterprise
   hosts like `github.mycorp.com` are explicitly deferred, per R14
   Out of Scope). If none match, return nil — no GitHub remote
   detected.
3. **Short-circuit on allow flag.** If any URL matched AND
   `allowPlaintext == true`, emit a structured warning to stderr
   ("warning: --allow-plaintext-secrets bypassing R14 guardrail; this
   flag is one-shot and will be re-checked on next apply") and return
   nil. R30 requires the flag be visible, not silent.
4. **Walk `*.secrets` tables.** Enumerate
   `cfg.Env.Secrets` and `cfg.Claude.Env.Secrets` (these are
   `map[string]config.MaybeSecret` after Decision 1's resolver ran).
   Collect every key whose value is `{Plain: s}` where `s` does NOT
   start with `vault://`. A `{Secret: ...}` value indicates the
   resolver already replaced a `vault://` URI — not a plaintext leak.
5. **Decide.** If step 4's offending-key set is empty, return nil. If
   non-empty, return an error (see "Error Message Example") naming
   every offending key and the matched remote(s).

Ordering relative to other pipeline stages:

- Runs AFTER vault resolution (Decision 1), so the resolver has
  already converted `vault://` Plains into `{Secret: ...}` values.
  This makes step 4's classification a trivial type-switch.
- Runs BEFORE merge, so the team config's `*.secrets` tables are still
  separable from the personal overlay's. The guardrail checks the
  team config only (R14 scope: "in the team config"). Personal overlay
  plaintext is a local-machine concern, not a public-commit concern.
- Runs BEFORE clone/materialize, so no filesystem writes happen if
  the guardrail fails. The apply is cheap to abort.

Total wall-clock cost: one `git remote -v` subprocess (~5-10ms cold,
faster warm) plus regex matching on a handful of URLs plus a map walk.
Well within the "milliseconds" budget.

## Error Message Example

```
error: plaintext secrets detected in a workspace config with a public GitHub remote

    The following keys in [env.secrets] and [claude.env.secrets] hold plaintext
    values, and at least one configured git remote of this workspace config
    points to a public GitHub repository. Committing plaintext secrets to a
    public repo is a leak risk and is blocked by default.

    Offending keys:
      [env.secrets]
        ANTHROPIC_API_KEY
        OPENAI_API_KEY
      [claude.env.secrets]
        GITHUB_TOKEN

    Matched remote(s):
      upstream  https://github.com/example-org/workspace-config.git (fetch)

    Remediation:
      1. Move each plaintext secret to a vault provider and reference it via
         vault://<provider>/<key> (preferred). Run `niwa status --audit-secrets`
         to see every plaintext secret across the merged config.
      2. If the matched remote is actually private (GitHub flags by URL pattern
         and does not probe visibility), or if you accept the risk for this
         single apply, re-run with:
             niwa apply --allow-plaintext-secrets
         The flag is one-shot: every subsequent `niwa apply` re-evaluates the
         guardrail.

    See docs/guardrails/public-repo.md for the full threat model.
```

The error must NOT print secret values (R22 redaction applies — niwa
prints keys only). The remediation block names both the preferred fix
(migrate to vault refs) and the one-shot override. The "one-shot"
language matches R30's strict wording so users don't expect persistent
behavior.

## Open Items for Phase 3 Cross-Validation

Assumptions this decision bakes in, to verify against other decisions:

1. **Decision 1 pipeline ordering.** This decision places the guardrail
   AFTER resolver, BEFORE merge. Decision 1 (pipeline ordering) commits
   to `parse -> per-file-resolve -> merge -> materialize`. The guardrail
   slots in between resolve and merge; verify that Decision 1's Open
   Items #2 (vars/secrets distinction consumed by guardrail, not
   resolver) and #3 (MaybeSecret field type carries vault URIs) hold
   under this ordering. If the resolver moves, the guardrail's step 4
   classification (Plain vs Secret) changes shape.
2. **Decision 6 shadow-detection integration.** If shadow detection runs
   before merge, there is a coordination question about which pre-merge
   invariant fires first (guardrail on team config, then shadow on
   overlay? or interleaved?). This decision assumes they are
   independent passes with a fixed order: guardrail first (team-config-
   only), shadow second (team vs overlay). Verify with Decision 6.
3. **Decision 2 (provider interface) and `Resolver.Close` lifecycle.**
   The guardrail runs after Resolve but does NOT consume providers. If
   providers are closed eagerly after Resolve completes, the guardrail
   is unaffected. If providers are held open through merge+materialize,
   same — the guardrail doesn't touch them. No coupling expected; flag
   if provider lifecycle leaks into the guardrail's call site.
4. **Where `--allow-plaintext-secrets` is defined.** This decision
   assumes the cobra flag lives in `internal/cli/apply.go` and is
   wired through `Applier.AllowPlaintextSecrets`. If the CLI decision
   adopts a different flag-threading pattern (e.g., a command-wide
   options struct), this wiring changes but the guardrail's signature
   does not.
5. **Enterprise GitHub hosts (v1.1 scope).** This decision hard-codes
   `github.com` as the only matched host. If a future decision expands
   to GitHub Enterprise Server, the pattern list grows but the package
   location and contract are unchanged. Flag in Phase 5 security
   review that "non-github.com hosts silently pass" is an explicit v1
   limitation, called out in the package doc comment and in
   `docs/guardrails/public-repo.md`.
6. **Config-schema Decision (vars/secrets split, R33/D-10).** This
   decision assumes `cfg.Env.Secrets` and `cfg.Claude.Env.Secrets` are
   distinct map fields from their `.Vars` counterparts, so the
   guardrail can walk just the secret-bearing tables without re-
   classifying. Verify the config-schema decision produces this shape
   (it is also Decision 1 Open Item #2).
