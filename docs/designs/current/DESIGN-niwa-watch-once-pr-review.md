---
status: Planned
upstream: docs/prds/PRD-niwa-watch-once-pr-review.md
problem: |
  niwa dispatch is a pull verb that launches a background worker inheriting
  the dispatcher's full environment and with no network containment. To
  stage PR reviews proactively, niwa must poll GitHub for directly-requested
  reviews, dispatch a review agent that reads an untrusted PR, and do so
  under an enforced no-egress + credential-scoped profile -- none of which
  the current dispatch path provides.
decision: |
  Add a `niwa watch --once` verb that polls GitHub (`user-review-requested`),
  intersects with the workspace's repos, and for each new PR provisions an
  instance, pre-fetches the PR head with the trusted CLI (outside any
  sandbox), merges a no-egress sandbox profile into the instance's
  `.claude/settings.json`, and launches the review agent detached with an
  allowlisted environment that excludes the GitHub token. A metadata-only
  prompt carries no author-controlled text. Posting is a separate trusted
  subcommand (`niwa watch post`) that runs outside the sandbox on the
  developer-approved draft.
rationale: |
  The sandbox profile rides niwa's existing settings-merge seam; pre-fetching
  the PR with trusted code lets the agent run with a truly empty egress
  allowlist (the agent's tools reach nothing), so the GitHub token is never
  needed in-session and the boundary does not rest on the model. Reusing the
  dispatch provisioning path keeps the change to one PR while adding the
  containment as net-new dispatch surface.
---

# DESIGN: niwa watch --once PR-review dispatch

## Status

Planned

Technical design for the first version of proactive PR-review dispatch in
niwa, implementing the Accepted PRD.

**Amendment (sandbox capability, provisioning, and fallback policy).** A
feasibility investigation established that the OS sandbox's requirement is a
root-gated kernel capability that varies by host, not a missing package. The
earlier "sandbox works or refuse, Windows-only caveat" framing is superseded
by **Decision 8**: adaptive per-host enforcement tiers, unprivileged-by-default
provisioning of `bwrap`/`socat` via tsuku with an opt-in `niwa setup-sandbox`
for the one privileged step on hardened Linux, an operator-owned
`uncontained_policy` (default `refuse`) governing the no-tier fallback, and
`sandbox.failIfUnavailable` to close the harness fail-open. This expands the
change beyond a single niwa PR (a tsuku recipe and a `setup-sandbox` command
join it); the Implementation Approach and the downstream PLAN carry the added
work.

## Context and Problem Statement

`niwa dispatch` provisions a fresh instance and launches a `claude --bg`
worker for a hand-written task. Two properties of that path make it unsafe
to point at externally-authored content: the worker inherits the
dispatcher's full environment (`internal/cli/dispatch_launcher.go` sets
`cmd.Env = os.Environ()` with no filtering), and niwa applies no network or
process isolation to the worker (the OS-level sandbox does not exist in the
codebase today). A PR's title, body, and diff are authored by whoever opened
the PR, so a review session that reads them runs untrusted content with the
developer's credentials and unrestricted egress.

The feature must: (1) find open PRs where the developer is the
directly-requested reviewer, restricted to the workspace's repos; (2)
dispatch one review agent per new PR from a prompt that carries only
platform-vouched identifiers; (3) run that agent under an enforced profile
with no network egress, filesystem writes scoped to its clone, a fail-closed
permission mode, and an environment scrubbed to an explicit allowlist; (4)
let the developer post the drafted review with a trusted action that never
lifts the session's containment; and (5) skip already-handled PRs. The
enforcement is the crux, and it must hold at the tool/OS layer, not rest on
the model's judgment.

The relevant existing surface (grounding for the decisions below): the
dispatch flow and its `--detach` semantics in `internal/cli/dispatch.go`;
the full-environment launch in `internal/cli/dispatch_launcher.go`; niwa's
existing per-instance `.claude/settings.json` write/merge
(`InstallWorkspaceRootSettings`, `buildSettingsDoc`,
`MergeInstanceOverrides`); the GitHub client and token resolution
(`internal/github`, `internal/cli/token.go`); and workspace repo enumeration
(`config.Discover`, `WorkspaceConfig.Sources`).

## Decision Drivers

- **The boundary must be deterministic.** Egress denial and credential
  scoping must be enforced by the OS sandbox and the process environment,
  not by asking the model to behave. A control the model can talk its way
  around is not a control.
- **The dispatch decision must be injection-proof.** No author-controlled
  text (title, body, diff, author name) may enter the prompt that launches
  the session.
- **Minimal blast radius on the credential axis.** The review task is
  read-only; the session should hold no more than the model channel needs.
  The GitHub token (which can push and post) must not be reachable from the
  session that read the hostile PR.
- **Single-PR implementability.** The change should reuse niwa's dispatch
  provisioning and settings-merge machinery rather than build a parallel
  stack.
- **Fail closed.** Where containment cannot be enforced, the verb must
  refuse to dispatch rather than dispatch uncontained.
- **No daemon.** `watch --once` is a stateless single-shot verb.

## Considered Options

### Decision 1 -- How the no-egress sandbox profile reaches the session

- **Option 1A (chosen): merge the profile into the instance's
  `.claude/settings.json`.** niwa already writes and merges a root
  `.claude/settings.json` into every provisioned instance. The watch path
  adds `sandbox.enabled`, an empty `sandbox.network.allowedDomains`, and
  fail-closed permission settings to that merged document before launch.
- **Option 1B (rejected): pass the profile via the `--settings <json>`
  flag** to `claude --bg`. Rejected because dispatch already injects
  `--settings` for remote control, so a second producer would collide on
  that single flag; settings that must persist for the instance's life
  belong in the instance's settings file, which is the documented merge
  point, not a launch-time flag.

### Decision 2 -- How the untrusted PR content reaches the clone under no egress

- **Option 2A (chosen): niwa pre-fetches the PR head during provisioning,
  outside the sandbox, as inert data; the session runs with an empty egress
  allowlist.** The trusted CLI fetches the PR's head **commit SHA** and
  makes its tree available to the session **without a filter-honoring
  checkout**, then the review session reads local files and needs no network
  of its own, so `allowedDomains` is genuinely empty for the agent's tools.

  This fetch is itself an attack surface and must be hardened, because it
  runs in trusted niwa code *before* the sandbox boundary exists. A naive
  `git fetch` + checkout of attacker content can execute code and egress on
  its own: a `.gitattributes` marking a path `filter=lfs` triggers a
  `git-lfs` smudge (arbitrary fetch to an attacker-named URL), custom filter
  drivers route through the developer's ambient gitconfig, `core.hooksPath`
  / repo hooks can run, and submodule recursion fetches attacker URLs. The
  fetch step therefore MUST: fetch a specific commit SHA (not follow refs
  arbitrarily); run with hooks disabled, LFS smudge disabled
  (`GIT_LFS_SKIP_SMUDGE=1`, `filter.lfs.smudge`/`.process` unset), submodule
  recursion off, `protocol.ext`/`protocol.file` disabled, an empty
  `core.attributesFile`, and an isolated gitconfig (`GIT_CONFIG_NOSYSTEM=1`,
  `HOME` pointed away from the developer's gitconfig for the fetch).

  **Exposure primitive (chosen concretely):** a **filter-neutered checkout**
  of the fetched SHA into a working tree the agent reads as ordinary files
  -- with the git settings above making the checkout populate raw blob
  contents (LFS pointer files, no smudge, no filter/hook execution). This is
  preferred over `git archive` (which honors `.gitattributes export-ignore`,
  letting an attacker hide a malicious file from the reviewed tree while it
  stays in the merged PR) and over a bare object store the agent would have
  to `git show` (which contradicts the "read the local checkout" prompt).
  The checkout gives the agent a faithful, ordinary file tree and runs no
  checkout-time program. The prompt and data flow refer to this checkout.
- **Option 2B (rejected): allow `github.com` egress in the session** so the
  agent can clone/fetch the PR itself. Rejected because an allowed host is
  an exfiltration channel: an injected agent could push to an
  attacker-controlled ref or use the host as a data sink. Keeping the
  allowlist empty and moving the fetch to trusted code removes that channel
  entirely.

  (Note: the empty allowlist restricts the agent's *tool* traffic. The
  Claude Code harness's own model-API channel is separate and is what lets
  the session run at all; see Security Considerations.)

### Decision 3 -- How the dispatched environment is scoped

- **Option 3A (chosen): build `cmd.Env` from an explicit allowlist** for the
  watch launch path -- the Claude/Anthropic auth the model channel needs
  plus `PATH`/locale -- and a **synthetic `HOME`**, not the developer's.
  The GitHub token and every other inherited secret are absent from the
  environment.

  Scrubbing the environment is necessary but not sufficient: the GitHub
  credential commonly lives on *disk* under the developer's home directory
  (`~/.config/gh/hosts.yml`, `~/.netrc`, `~/.ssh/` plus a forwarded
  `SSH_AUTH_SOCK`). Handing the session the real `HOME` would reintroduce
  the very credential the env scrub removed. The **primary guard is a
  synthetic `HOME`** -- set to a scratch directory inside the instance with
  no developer dotfiles -- so the credential files are simply not present at
  the paths tools look for; this is an allowlist-shaped (fail-closed) guard,
  not a path denylist (which would share the fail-open weakness Option 3B
  rejects). `SSH_AUTH_SOCK` and `GH_*`/`GITHUB_*` are also excluded from the
  env. The sandbox's filesystem policy still applies, but note it cannot
  literally "deny all reads outside the clone" -- the session needs
  `/usr/bin`, shared libraries, and locale data to run; the guarantee is
  that *developer credentials* are unreachable (synthetic HOME), not that
  the filesystem is empty.

  The one secret the session legitimately holds is the **model API
  credential** (it must reach the model to function). That is acceptable
  only because the sandbox holds: with no tool egress, the session cannot
  exfiltrate even the credential it carries. If the sandbox were bypassed,
  this credential would be exposed -- another reason the sandbox enforcement
  must be *verified live*, not assumed (see Security Considerations).

  The canary-absence test (PRD AC12) covers `GITHUB_TOKEN`, `GH_TOKEN`,
  `SSH_AUTH_SOCK`, and a planted `~/.netrc`/`~/.config/gh` sentinel under the
  real home, not just one env var.

  **Writable region.** The agent must write its drafted review, so the
  sandbox's write scope is the **instance directory** (which contains the
  clone), and the draft lands at `<instanceRoot>/watch-review-draft.md` --
  outside the clone's working tree so it does not pollute the reviewed repo.
  (This reconciles the draft location with the write scope; R7's
  "writes scoped to its clone" is realized as "scoped to its instance.")
- **Option 3B (rejected): denylist known-sensitive variables** out of
  `os.Environ()`. Rejected because a denylist is unbounded and fails open:
  any secret the list does not anticipate leaks through. An allowlist fails
  closed.

  The existing `cmd.Env = os.Environ()` behavior is left unchanged for the
  ordinary `niwa dispatch` path; the allowlist is a new parameter used only
  by the contained watch launch.

### Decision 4 -- The poll query and the workspace intersection

- **Option 4A (chosen): one GitHub search, then intersect.** Resolve the
  authenticated login (`GET /user`), issue
  `GET /search/issues?q=is:pr+is:open+user-review-requested:<login>`, and
  intersect the returned `owner/repo` set with the workspace's repos
  (from `config.Discover` -> `Sources`/`Repos`). `user-review-requested`
  excludes team-only requests by construction.
- **Option 4B (rejected): query each workspace repo's pulls endpoint and
  filter by requested reviewer.** Rejected as O(repos) API calls and more
  client surface, for the same result the single search yields.

### Decision 5 -- Where the handled-set lives

- **Option 5A (chosen): a flat file under the workspace's `.niwa/`
  directory**, one stable id per line (`owner/repo#number`), written only
  after a PR's review agent is successfully dispatched under containment.
- **Option 5B (rejected): a structured store (SQLite / JSON with per-PR
  state).** Rejected as premature -- the only question this version asks is
  "handled or not," and richer dedup/expiry state is explicitly deferred.

### Decision 6 -- The post-on-approval affordance

- **Option 6A (chosen): a trusted `niwa watch post <handle>` subcommand**
  (with `niwa watch discard <handle>`) that runs in the dispatcher's
  trusted context, reads the drafted review from the known location and the
  PR coordinates niwa persisted at dispatch, and posts via the GitHub API
  with the dispatcher's token. The token lives only in this trusted path,
  never in the contained session.

  The draft is authored by the untrusted session, so the post step treats
  it as **inert data, never as control**: the review `event` (the field
  that would make a review APPROVE / REQUEST_CHANGES / COMMENT) is fixed by
  trusted code -- it is **not** read from the draft -- and defaults to a
  non-approving `COMMENT`, so a malicious draft cannot dictate an approval;
  a stronger disposition, if offered, is an explicit developer choice on the
  `post` command, not something the draft can set. The draft body is passed
  as an opaque API body. Before reading, niwa validates that the recorded
  `draftPath` resolves inside the instance root (no traversal) and that the
  handle maps to a known staged-review record.
- **Option 6B (rejected): print a ready-to-run `gh pr review` command** for
  the developer to paste. Rejected because it makes "post it" a copy step
  rather than one gesture and pushes credential handling onto the developer;
  the subcommand keeps the one-gesture promise while preserving the
  boundary.

### Decision 7 -- Fail-closed detection

  **The boundary is delegated to the harness -- so it must be *verified*,
  not assumed.** niwa has no OS sandbox of its own; egress denial and the
  fail-closed permission mode are enforced by the Claude Code harness's
  `sandbox.*` settings. The design therefore depends on a harness version
  that (a) supports `sandbox.enabled` + `sandbox.network.allowedDomains`,
  (b) treats an **empty** `allowedDomains` as *deny-all* (not allow-all),
  and (c) actually creates and enforces the sandbox for a `--bg --detach`
  launch. These are assumptions, and a silent inversion (empty read as
  allow-all, or the sandbox failing to start and the session running
  uncontained with only a warning) would pass every settings-shaped check.
  So verification is layered and fail-closed:

- **Option 7A (chosen): preflight, re-verify the effective settings, and
  prove enforcement live.** Three checkpoints:
  - *Preflight (before any instance is created):* actively probe that the OS
    sandbox can be created now -- not merely that the OS is nominally
    supported -- and select the enforcement tier for the host (see Decision 8
    for the capability tiers and the operator-owned policy that governs what
    happens when no tier is available). Concretely, on macOS the built-in
    `sandbox-exec` (Seatbelt) is available unprivileged; on Linux the probe
    checks the backend and dependency the harness requires (`bwrap` and
    `socat` on PATH) and functionally verifies a capability-bearing,
    network-isolable user namespace (`bwrap --unshare-net` succeeds -- it
    fails on a kernel that restricts unprivileged user namespaces). When no
    enforceable tier exists, the configured fallback policy decides between
    refusing and proceeding (Decision 8); the default is to refuse.
  - *Effective-settings re-verification (per instance, before launch):* the
    containment lives in a *merged* `.claude/settings.json`, and the harness
    applies its own downstream merge (managed/enterprise settings, and the
    `--settings` flag `niwa dispatch` already injects for remote control at
    high precedence). niwa must both re-read the merged instance file to
    confirm the sandbox stanza survived *and* ensure its own `--settings`
    injection does not relax `sandbox.*`. Where niwa cannot observe the
    harness's final merge, it must not silently assume the stanza wins.
  - *Live enforcement proof (the real check):* settings presence does not
    prove the sandbox is enforcing. The adversarial acceptance test
    (Implementation step 6) attempts **actual egress** from inside the
    session -- both a domain connection and a **raw socket to a literal IP**
    -- and asserts both fail. This live attempt is what distinguishes
    deny-all from a silently-inverted allow-all and a proxy-only egress path
    (where a raw socket could escape) from a default-deny network namespace.
  On any preflight/re-verify failure `watch --once` prints the reason to
  stderr and exits non-zero; the live proof gates release, not each run.
- **Option 7B (rejected): dispatch, then check.** Rejected because a session
  could reach a runnable state uncontained before the check fires; the
  preflight-plus-re-verify-plus-live-proof layering guarantees no
  uncontained session is silently trusted.

### Decision 8 -- Sandbox capability across hosts, provisioning, and the fallback policy

*Added after a feasibility investigation (see the Amendment note in Status).
The earlier framing -- "the sandbox works or the feature refuses" with a
Windows-only caveat -- was too coarse. Scope is real Linux and macOS hosts.*

**The empirical capability picture.** The Claude Code sandbox's requirements
vary by host, and the difference is a **root-gated kernel capability**, not a
missing package:

- **macOS** -- the sandbox uses the built-in `sandbox-exec` (Seatbelt),
  available to an unprivileged user. No extra binaries, no elevation; it works
  after a normal install.
- **Linux, permissive kernel** -- with unprivileged user namespaces enabled
  and no LSM restriction, `bwrap` + `socat` run unprivileged and the sandbox
  works out of the box.
- **Linux, hardened kernel** (e.g. Ubuntu 24.04's
  `apparmor_restrict_unprivileged_userns=1`) -- the unprivileged user
  namespace is created capability-*less*, so `bwrap` dies configuring the
  netns loopback (`RTM_NEWADDR: Operation not permitted`) and refuses to
  start. The fix -- an AppArmor profile for the `bwrap` binary, the sysctl set
  to 0, or setuid `bwrap` -- is **root-only** and cannot be granted by any
  unprivileged install.

**Decision (three parts):**

- **8A -- Adaptive enforcement tier.** The preflight (Decision 7) detects the
  strongest enforceable boundary and dispatches under it: Seatbelt on macOS,
  the `bwrap`+`socat` no-egress profile on capable Linux. When neither is
  available, the run does not silently weaken -- it consults the fallback
  policy (8C).
- **8B -- Provisioning, unprivileged by default.** `bwrap` and `socat` are
  declared as **Linux-only runtime dependencies** of niwa's tsuku recipe
  (both are installable as prebuilt bottles), so a normal `tsuku install`
  provides them with no `sudo`. They are needed only when the sandbox
  actually runs; macOS needs neither, and the refuse path needs neither. The
  one irreducible privileged step -- unlocking the kernel capability on a
  hardened Linux host -- is an **opt-in** `niwa setup-sandbox` command
  (install the AppArmor profile / set the sysctl) run once, never per
  dispatch. The default install requires no elevation; on macOS and permissive
  Linux nothing more is needed; on hardened Linux the feature refuses (8C)
  until `niwa setup-sandbox` is run. This keeps "standard install leaves a
  ready system" true wherever the kernel permits it, and reduces the
  hardened-Linux case to a single documented command rather than a
  multi-step manual install.
- **8C -- Operator-owned fallback policy.** What happens when no enforceable
  tier exists is a **policy decision the operator owns**, not a hard-coded
  refusal. A durable setting `[watch] uncontained_policy` (resolved on the
  usual `flag > config header > default` stack) takes one of:
  `refuse` (the safe default -- fail closed), `warn` (dispatch, but emit and
  record a prominent warning so the operator knowingly accepts the weaker
  bar), or `allow` (dispatch without the warning, for a standing informed
  decision). "Uncontained" means only the *OS-level egress denial* is absent;
  the metadata-only prompt, the credential-scrubbed environment, and the
  human review-before-post gate all still apply. The default stays `refuse`
  so loosening is always an explicit opt-out, never an accident.

**Closing the fail-open at the harness layer.** Independently of the tier, the
dispatched instance settings set `sandbox.failIfUnavailable: true` and
`allowUnsandboxedCommands: false`, so Claude Code *refuses to run* rather than
silently disabling the sandbox and proceeding with a warning. This, plus the
niwa-side preflight (Decision 7) and the `uncontained_policy` gate, means an
uncontained session is only ever produced by an explicit operator choice.

## Decision Outcome

`niwa watch --once` is a new verb (`internal/cli/watch.go`) that runs one
poll-and-dispatch pass:

1. **Preflight (fail-closed).** Verify the OS sandbox can be enforced on
   this platform. If not, print the reason and exit non-zero (Decision 7).
2. **Poll.** Resolve the login and search GitHub for open PRs with
   `user-review-requested:<login>`; intersect the results with the
   workspace's repos (Decision 4).
3. **Dedup + bound.** Drop PRs already in the handled-set; order the rest
   by PR `created_at` (oldest first -- a deterministic key available from
   the single search) and take at most the per-run bound (default 3).
4. **Per selected PR:** provision an instance (reusing dispatch's
   provisioning); pre-fetch the PR head into the clone with the trusted CLI
   (Decision 2); merge the no-egress sandbox profile into the instance's
   `.claude/settings.json` (Decision 1); write a metadata-only prompt
   (Decision `prompt`); launch `claude --bg` detached with an allowlisted
   environment (Decision 3); persist the PR coordinates and draft path
   alongside the session mapping; record the PR in the handled-set only on
   success (Decision 5).
5. **Report.** On an empty result, print "nothing to stage" and exit zero.
   On a poll or dispatch failure, print the error and exit non-zero without
   recording the affected PR as handled.

Posting is out-of-band: `niwa watch post <handle>` reads the approved draft
and posts it via the GitHub API from the trusted context (Decision 6);
`niwa watch discard <handle>` records the PR handled and posts nothing.

## Solution Architecture

### Components

- **`internal/cli/watch.go` (new).** Registers `watchCmd` with `--once` and
  the `post`/`discard` subcommands via `init()` + `rootCmd.AddCommand`.
  Orchestrates the pass above.
- **Capability tiering + `niwa setup-sandbox` (new, Decision 8).** The
  preflight resolves the enforcement tier (Seatbelt / bwrap-socat / none) and
  consults `uncontained_policy`; `niwa setup-sandbox` is the opt-in privileged
  command that unlocks the capability on hardened Linux (AppArmor profile or
  sysctl). The `uncontained_policy` value is read from niwa config on the
  `flag > config header > default(refuse)` stack.
- **niwa tsuku recipe (packaging, Decision 8B).** A curated `recipes/n/niwa.toml`
  (shadowing today's auto-generated download recipe) declaring
  `runtime_dependencies = ["bubblewrap", "socat"]` scoped to Linux, plus a new
  `recipes/b/bubblewrap.toml` (homebrew-bottle action). macOS pulls neither.
- **Containment settings (extended).** The merged instance
  `.claude/settings.json` also sets `sandbox.failIfUnavailable: true` and
  `allowUnsandboxedCommands: false` so the harness refuses rather than silently
  disabling the sandbox.
- **`internal/github` (extended).** Three net-new client methods (the
  client has only `ListRepos`/`GetRepo` today): `CurrentLogin(ctx)
  (string, error)` wrapping `GET /user`; `SearchReviewRequestedPRs(ctx,
  login) ([]PRRef, error)` wrapping `GET /search/issues`; and
  `CreateReview(ctx, owner, repo, number int, body, event string) error`
  wrapping `POST /repos/{owner}/{repo}/pulls/{number}/reviews` for the
  trusted post step, where `event` is supplied by trusted niwa code (default
  `COMMENT`) and never read from the draft. `PRRef{Owner, Repo, Number, URL,
  CreatedAt}` -- `CreatedAt` is the PR's `created_at` from the search
  payload (the review-*request* time is not in that payload; ordering uses
  `created_at` as a deterministic, single-call proxy). Auth reuses
  `resolveGitHubToken` and `NewAPIClient`.
- **Containment on the dispatch path (net-new dispatch surface).**
  - A containment-profile builder producing the sandbox settings fragment
    (`sandbox.enabled: true`, `sandbox.network.allowedDomains: []`,
    fail-closed permission mode), applied to the instance settings via the
    same merge helper `applier.Create` uses (`MergeInstanceOverrides` /
    the root-settings writer) -- watch is a *second writer* to
    `.claude/settings.json`, ordered Create -> watch-merge -> re-verify ->
    launch.
  - An env-allowlist on the launch seam. The launch func gains an options
    parameter, e.g. `dispatchLaunch(ctx, instanceDir, prompt, passthrough,
    LaunchOpts)` where `LaunchOpts{EnvOverride []string}`; `EnvOverride ==
    nil` preserves today's `cmd.Env = os.Environ()` so the ordinary dispatch
    path is unchanged, and the watch path passes the allowlisted env
    (including the synthetic `HOME`).
  - A trusted, hardened PR-head fetch + filter-neutered checkout (Decision
    2) run before launch.
- **Handled-set store.** Read/append helpers over
  `<workspaceRoot>/.niwa/watch-handled` (flat `owner/repo#number` lines).
- **Staged-review record + handle.** The **handle is the dispatch session
  short id** shown in the agent view (the `shortID` from the existing
  `SessionMapping` capture). At dispatch niwa writes a small record
  `{handle, owner, repo, number, url, draftPath}` to
  `<workspaceRoot>/.niwa/watch/<handle>.json`; `post <handle>` /
  `discard <handle>` load it to resolve the PR and draft. The record schema
  and store are an explicit early deliverable (the post/discard phase
  depends on them).

### The metadata-only prompt

Assembled by a pure function of the `PRRef` (satisfying the determinism
requirement): a fixed instruction template interpolating only `owner/repo`,
the PR number, and the PR URL. It instructs the agent to read the PR (title,
body, diff, linked issue, CI status) from its filter-neutered local checkout
of the pre-fetched PR head, treat all of it as untrusted, write its review to
the known draft path,
and stop before posting. No title, body, diff, or author name is
interpolated.

### The known draft location

niwa defines a fixed path in the instance (e.g.
`<instanceRoot>/watch-review-draft.md`) recorded in the staged-review
record. The agent writes there; `niwa watch post` reads there.

### Data flow

```
watch --once
  preflight sandbox  --(unsupported)--> stderr + non-zero exit
        | ok
  GET /user -> login
  GET /search/issues (user-review-requested:login, is:open, is:pr)
        |
  intersect with workspace repos (config.Discover)
        |
  minus handled-set; order by created_at (oldest first); take <= bound
        |
  for each PR (trusted CLI, outside sandbox):
     provision instance (applier.Create)
     fetch PR head SHA + filter-neutered checkout   <- untrusted content as data
     merge no-egress sandbox profile into instance .claude/settings.json
     re-verify merged sandbox stanza
     write metadata-only prompt
     launch claude --bg  --detach  with allowlisted env  (NO GitHub token)
     persist staged-review record; append handled-set (on success)
        |
  agent view shows the staged session; developer reads the draft
        |
  niwa watch post <handle>  (trusted, outside sandbox)
     read draft + PR coords -> POST /repos/{o}/{r}/pulls/{n}/reviews (dispatcher token)
  niwa watch discard <handle> -> record handled, post nothing
```

## Implementation Approach

Phased so each step is independently testable. The handled-set and
staged-review record schemas are defined up front (step 0) since later steps
depend on them.

0. **State schemas.** Define the handled-set format (`owner/repo#number`
   lines) and the staged-review record (`{handle, owner, repo, number, url,
   draftPath}` at `.niwa/watch/<handle>.json`), with read/write helpers.
1. **GitHub poll.** Add `CurrentLogin`, `SearchReviewRequestedPRs`, and
   `CreateReview` to the client; unit-test against the existing fake server
   (`NIWA_GITHUB_API_URL`).
2. **Workspace intersection + dedup + bound.** Enumerate workspace repos,
   intersect, apply the handled-set and the `created_at`-ordered bound.
   Pure, table-testable logic.
3. **Hardened PR-head fetch (isolated -- the sharpest risk).** Fetch by SHA
   + filter-neutered checkout (LFS smudge / hooks / submodule recursion /
   ext-protocols disabled, empty `core.attributesFile`, isolated gitconfig).
   Test against fixtures: a `.gitattributes`+`filter=lfs` PR triggers no
   smudge and no egress during fetch; an `export-ignore`-marked malicious
   file is still present in the checked-out tree (not hidden).
4. **Containment surface.** Add the `LaunchOpts{EnvOverride}` seam (nil =
   unchanged) with the synthetic `HOME`; the containment-profile builder +
   settings second-write via the existing merge helper + per-instance
   re-verification. Unit-test the allowlist (subset + canary absence for
   `GITHUB_TOKEN`/`GH_TOKEN`/`SSH_AUTH_SOCK` and an on-disk
   `~/.netrc`/`~/.config/gh` sentinel) and the merged settings document.
5. **`watch --once` orchestration.** Wire preflight -> poll -> select ->
   per-PR provision/fetch/merge/re-verify/launch -> record. Fail-closed
   preflight and fail-loud error paths.
6. **Post/discard subcommands.** Resolve handle -> staged-review record;
   post via `CreateReview` with the `event` fixed in code; discard records
   handled.
7. **Adversarial / live-enforcement test (the boundary proof).** A hostile-PR
   fixture whose title/body/diff attempt egress, push, and exfiltration.
   From inside the running contained session (bypassing the model), the test
   attempts **real outbound network** -- both a connection to a domain and a
   **raw socket to a literal IP** -- and a write outside the instance, and
   asserts each fails at the OS layer (connection blocked / EPERM). This live
   attempt, not a settings-file assertion, is what proves empty-allowlist =
   deny-all and that the sandbox is actually enforcing for the `--bg
   --detach` launch; it gates release.

## Security Considerations

This feature exists to contain a remote-execution vector, so security is the
design's center, not an appendix. A party who opens a PR requesting the
developer's review is an untrusted author whose title/body/diff reach a
session running with the developer's authority. The boundary must be
deterministic.

**External artifact handling.** The launch prompt carries only
platform-vouched identifiers (repo, PR number, URL) -- no author-controlled
text -- so the dispatch decision cannot be injected. The untrusted PR
content is fetched by trusted niwa code as **inert data** (by commit SHA,
no filter-honoring checkout, with LFS smudge, hooks, submodule recursion,
and `protocol.ext`/`protocol.file` disabled and an isolated gitconfig), so
fetching cannot execute checkout-time programs or egress on the attacker's
behalf (see Decision 2). Inside the session the content is read but never
treated as instructions; the session cannot act on it because it has no
egress and no privileged credentials.

**Permission scope / network -- and the delegated dependency.** Containment
is the OS-level sandbox (`sandbox.enabled`, empty
`sandbox.network.allowedDomains`, fail-closed permission mode) applied via
the instance settings merge and re-verified per instance before launch
(Decision 7). **niwa does not implement the sandbox; it delegates to the
Claude Code harness.** The whole boundary therefore rests on the harness
(a) treating an empty allowlist as deny-all, (b) actually creating and
enforcing the sandbox for the `--bg --detach` launch, and (c) not letting a
downstream settings merge (managed settings, or the `--settings` remote-
control flag `niwa dispatch` injects) relax `sandbox.*`. These are
assumptions a settings-shaped check cannot confirm -- a silent inversion
would pass every such check -- so the design **proves enforcement live**:
the release-gating adversarial test attempts real egress (a domain
connection and a raw socket to a literal IP) and an out-of-instance write
from inside the session, and requires each to fail at the OS layer (Decision
7, Implementation step 7; PRD AC9/AC14). Denial is thus verified to be the
sandbox's, not the model's. (The harness's own model-API endpoint remains
reachable -- that channel is what runs the session -- so "empty allowlist"
means "no *attacker-useful* egress," not "no packets at all"; it is not an
exfiltration sink.)

**Credential scoping.** The session environment is an allowlist (model auth
+ `PATH`/locale + a synthetic `HOME`); the GitHub token, `SSH_AUTH_SOCK`,
and `GH_*`/`GITHUB_*` are absent, and the synthetic `HOME` plus the
sandbox's filesystem policy keep on-disk credentials (`~/.config/gh`,
`~/.netrc`, `~/.ssh`) out of reach (Decision 3). The posting credential
lives only in the out-of-band trusted `post` step; it never enters the
contained session. The act boundary holds: the drafting session's
containment is never lifted, and posting happens in a separate trusted
context on a developer-approved draft, with the review disposition
(`event`) fixed by trusted code so a hostile draft cannot force an approval
(Decision 6).

**Data exposure.** The persisted state (handled-set of `owner/repo#number`,
staged-review records of PR coordinates + draft path) is low-sensitivity
workspace metadata at rest under `.niwa/`; it contains no secrets. The PR's
content does reach the model API by design (the review has to read it);
that is inherent to reviewing and is the same trust posture as any Claude
Code session reading a repo.

**Accepted residual risks.**

- **Hardened Linux needs a one-time privileged setup.** On a kernel that
  restricts unprivileged user namespaces (e.g. Ubuntu 24.04), the sandbox
  cannot run until `niwa setup-sandbox` is run once (Decision 8); until then
  the feature follows `uncontained_policy` (default: refuse). macOS and
  permissive Linux need no elevation. This residual is a property of the OS
  security model, not a niwa limitation -- the capability is root-gated and
  not user-installable.
- **Windows.** The OS sandbox is unavailable on Windows; the design fails
  closed there (refuses to dispatch) rather than running uncontained. Windows
  self-hosters get no staged reviews until a later version addresses it.
- **Egress mechanism (proxy vs network namespace).** If the harness enforces
  egress with a default-deny network namespace, raw sockets are blocked and
  the residual is only a narrow SNI/domain-fronting seam through any allowed
  host. If it enforces egress proxy-only, a subprocess opening a raw socket
  to an arbitrary IP could escape entirely -- a hole, not a seam. The live
  raw-socket test (Implementation step 7) is what surfaces which regime is in
  force; the feature requires the deny-all-network posture and treats a
  passing raw-socket escape as a release blocker, not an accepted residual.
- **Model-channel cost.** The always-available model channel means a
  hostile PR can still consume model tokens (a cost/DoS vector, not an
  exfiltration one). The per-run staging bound and the handled-set limit the
  blast radius; richer cost controls are deferred.
- **Draft text is a second-order channel.** The drafted review text is
  authored by the untrusted session and could carry attacker-influenced
  prose. The approving developer reads it before the trusted step posts it
  -- the human is the trust checkpoint for the draft's *content*, while
  containment covers the session's *actions*. This assumption is load-bearing
  and specifically means the draft must NOT be auto-ingested by another agent
  without a human read; the `post` step already treats the draft as inert
  data (event fixed in code, body opaque, path validated), but a future
  automated consumer of the draft would reopen this channel.

## Consequences

### Positive

- The dispatch decision is injection-proof by construction: no
  author-controlled text enters the prompt. The workspace intersection
  strengthens this -- only known-good `owner/repo` identifiers from the
  developer's own workspace reach the prompt, so the sole attacker-chosen
  value is the PR number (an integer), leaving no room to inject text via
  the identifiers themselves.
- The review session cannot egress (empty allowlist) and cannot act with
  the developer's GitHub credentials (token absent from its env), so a
  hostile PR is contained at the OS/process layer rather than by the model.
- Credential scoping and the sandbox profile land as reusable dispatch
  surface a later contained-dispatch feature can adopt.
- The verb reuses existing provisioning and settings-merge machinery.
- The feature runs unprivileged out of the box on macOS and permissive Linux;
  the only elevation is a single opt-in `niwa setup-sandbox` on hardened
  Linux, and the operator -- not the code -- chooses the no-tier fallback.

### Negative / costs

- Net-new GitHub client surface (search + user), a containment path, and two
  subcommands make the niwa verb itself sizeable; the Decision-8 amendment
  adds a tsuku recipe, a `setup-sandbox` command, and a config policy, so the
  effort now spans niwa **and** a tsuku-recipe change -- it is no longer a
  single niwa PR.
- The capability tiering and `uncontained_policy` add operator-facing surface
  (a config knob, a setup command, and per-host behavior to document); a wrong
  default here would be a security regression, so the default is `refuse` and
  the fail-open is closed at both the niwa and harness layers.
- Pre-fetching the PR head means niwa's trusted code touches untrusted repo
  content; getting this wrong (a filter-honoring checkout, LFS smudge, repo
  hooks, submodule recursion) would execute attacker code *outside* the
  sandbox. Decision 2 constrains the fetch to inert-data handling to close
  that, but it is the sharpest implementation risk and carries a dedicated
  test.
- The empty egress allowlist means the agent cannot use `gh`/network tools;
  the prompt must direct it to the local clone, and some agent conveniences
  are unavailable in-session (acceptable, and the point).

### Mitigations

- Fail-closed preflight guarantees no uncontained session launches.
- Handled-set-on-success-only prevents a transient failure from suppressing
  a review.
- The adversarial test is part of "done," so the boundary is verified by the
  exact injection surface it defends.
