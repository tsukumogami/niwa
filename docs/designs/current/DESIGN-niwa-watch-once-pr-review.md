---
status: Current
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
  instance and launches a detached review agent. Containment is governed by
  two nested global switches: `watch_containment` (on|off, default on) and,
  when on, `watch_sandbox` (required|optional, default required).
  With containment on, niwa pre-fetches the PR head with the trusted CLI
  (outside any sandbox), launches the agent with a credential-scrubbed
  environment (no GitHub token), a synthetic HOME, and a fail-closed
  permission mode, and merges the OS no-egress sandbox profile into the
  instance's `.claude/settings.json` when `watch_sandbox` calls for it. In
  every mode the agent drafts its review to a known file the operator posts
  from their own session; posting is always a human act. With containment off
  the agent runs an ordinary dispatch with the developer's real environment so
  it can read the linked issue, CI status, and review threads a substantive
  review needs, but it still only drafts and waits. A metadata-only prompt
  carries no author-controlled text in every mode.
rationale: |
  Containment stays on by default because the review reads untrusted PR
  content; the two switches let an operator relax it deliberately for trusted
  sources rather than by accident. The sandbox profile rides niwa's existing
  settings-merge seam; pre-fetching the PR with trusted code lets a contained
  agent run with a truly empty egress allowlist and no GitHub token, so the
  boundary does not rest on the model. Posting is always a human act -- the
  agent drafts and waits in every mode -- so niwa needs no posting verb or
  posting credential; a post-guard keeps the trusted uncontained path from
  posting by accident. Reusing the dispatch provisioning path keeps the change
  focused while adding the containment as net-new dispatch surface.
---

# DESIGN: niwa watch --once PR-review dispatch

## Status

Current

Technical design for the first version of proactive PR-review dispatch in
niwa, implementing the Accepted PRD.

**Amendment (containment model, sandbox capability, and provisioning).** A
feasibility investigation established that the OS sandbox's requirement is a
root-gated kernel capability that varies by host, not a missing package. The
earlier "sandbox works or refuse, Windows-only caveat" framing is superseded
by **Decision 8**: containment is now **optional**, expressed as two nested
global switches -- `watch_containment` (on|off) governing the credential
scrub + synthetic HOME + fail-closed bundle, and `watch_sandbox`
(required|optional) governing the OS no-egress cage. The design adds
adaptive per-host backend selection, unprivileged-by-default provisioning of
`bwrap`/`socat` via tsuku with an opt-in `niwa setup-sandbox` for the one
privileged step on hardened Linux, and `sandbox.failIfUnavailable` to close
the harness fail-open. Posting is always a human act (no `post` subcommand):
the agent drafts and waits in every mode and the operator posts from their own
session; a post-guard (`permissions.ask` on `gh pr review`/`gh pr comment`)
keeps the uncontained path from posting by accident. This expands the change
beyond a single niwa PR (a tsuku recipe and a `setup-sandbox` command join
it); the Implementation Approach and the downstream PLAN carry the added
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
platform-vouched identifiers; (3) by default (containment on) run that agent
under an enforced profile with an environment scrubbed to an explicit
allowlist, a synthetic HOME, a fail-closed permission mode, and -- when
`watch_sandbox` calls for it -- an OS no-egress sandbox with filesystem
writes scoped to the instance; (4) have the agent draft its review for the
developer to post from their own session in **every** mode, while letting an
operator turn containment off for trusted sources so the agent runs with real
credentials and can read the surrounding context (linked issue, CI, review
threads) -- it still only drafts and waits; and (5) skip already-handled PRs. When containment is on, the enforcement is the crux,
and it must hold at the tool/OS layer, not rest on the model's judgment.

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
- **Fail closed by default.** With the default switches (`watch_containment
  = on`, `watch_sandbox = required`), where the OS sandbox cannot be
  enforced the verb must refuse to dispatch rather than silently dispatch a
  session that is less contained than requested. Weakening containment is
  only ever an explicit operator setting, never a silent degradation.
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
  ordinary `niwa dispatch` path; the allowlist + synthetic HOME are used only
  when `watch_containment` is on. When `watch_containment` is off, the watch
  launch reuses the ordinary full-environment path (no allowlist, real
  `HOME`), because that mode is by definition an ordinary dispatch with the
  developer's real credentials -- the GitHub token included, so the agent can
  **read** the surrounding context a substantive review needs; it still only
  drafts and waits (Decision 6).

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

### Decision 6 -- How a review gets posted

Posting is not a niwa verb, and it is **not the agent's act either**. In every
mode the dispatched agent drafts its review to a known file and waits;
**posting is always a human act** -- the operator reads the draft and submits
it from their own trusted session. The contained and uncontained modes differ
in **read context and credentials**, not in who posts. So there is no posting
step to design, credential to mint, or `post` subcommand to build.

- **Option 6A (chosen): the agent always drafts and waits; posting is a human
  act in every mode.** In both modes the session writes its review to the
  known location and halts -- it never posts, comments, approves, or pushes,
  and the developer posts the draft from their **own trusted session**. When
  `watch_containment` is on, the session also carries no GitHub token
  (Decision 3) and, under the OS sandbox, cannot egress (Decision 2), so it
  reads its pre-fetched clone and could not post even if the untrusted content
  told it to. When `watch_containment` is off, the session runs an ordinary
  dispatch with the developer's real environment (token included) **so it can
  read the linked issue, CI status, and review threads a substantive review
  needs** -- context the no-egress contained session cannot reach -- but it
  still only drafts and waits. Two things hold the uncontained path back from
  posting: the prompt tells the agent not to post, and the dispatched instance
  settings carry a **post-guard** -- an ask-approval rule (`permissions.ask`
  on `gh pr review` and `gh pr comment`) that requires operator approval
  before any submission. The post-guard is a **convenience /
  accident-prevention guard for the trusted uncontained path, NOT a security
  boundary**: command-string matching is not a security boundary (the OS
  sandbox is). It exists so a stray prompt-following cannot post under the
  operator's name without a click, and it is applied in every mode (harmless
  where the session has no credentials or no egress). Because niwa posts
  nothing, it exposes no `post` or `discard` subcommand; a staged session the
  developer no longer wants is dismissed from the Claude Code agents view.
- **Option 6B (rejected): a trusted `niwa watch post <handle>` subcommand**
  that reads the drafted review and posts it via the GitHub API with a
  narrowly-scoped credential niwa provisions and keeps out of the contained
  session. Rejected because it makes niwa a credential broker for a step the
  developer's own already-trusted session can do directly, and it adds a
  subcommand, a persisted staged-review record keyed to it, and the machinery
  to fix the review `event` in trusted code so a hostile draft cannot force an
  approval -- all of which the "the developer posts their own draft" model
  removes. The residual risk it was guarding (a malicious draft dictating an
  APPROVE) is instead handled by the developer reading the draft before
  posting.
- **Option 6C (rejected): print a ready-to-run `gh pr review` command** for
  the developer to paste. Rejected as a strictly worse version of 6A's
  contained path: it still assumes niwa assembles a post command, but adds a
  brittle copy-paste step and hard-codes the disposition, where letting the
  developer post from their own session leaves them in full control of what
  and how they post.
- **Option 6D (rejected): let the off-mode agent post directly.** An earlier
  framing had the uncontained agent review and post autonomously, keying "who
  posts" to the token's presence. Rejected because auto-posting under the
  operator's real identity on a PR the trigger may have auto-assigned (a
  teammate request, a CODEOWNERS auto-request, or external-fork triage -- see
  Decision 4 and Security Considerations) lets a prompt-injected agent act
  under the operator's name before any human looks. Drafting and waiting in
  every mode, plus the post-guard, keeps a human in the loop while still giving
  the uncontained agent the read credentials that make its draft substantive.

### Decision 7 -- Preflight resolution and fail-closed detection

The preflight resolves the two containment switches (defined in Decision 8)
and follows this authoritative matrix. It is the single source of truth for
what a dispatched session gets and for the one cell that refuses:

| `watch_containment` | `watch_sandbox` | The dispatched review session gets |
|---|---|---|
| on | required | Credential scrub + synthetic HOME + fail-closed, **and** the OS sandbox. If the sandbox cannot be enforced on the host, `watch --once` **refuses** to dispatch that review (do not dispatch). |
| on | optional | Credential scrub + synthetic HOME + fail-closed. The OS sandbox is added when available; otherwise the review proceeds **contained without it**. |
| off | (not consulted) | **Nothing** -- a normal `niwa dispatch` with the developer's real environment and credentials, as if they ran it themselves. |

The live-enforcement proof below applies only where the OS sandbox is in force
(the two `on` rows, when the sandbox is actually enabled on the host); the
credential scrub and fail-closed mode apply to **both** `on` rows; the `off`
row is uncontained by the operator's explicit choice and has no boundary to
verify.

  **When the OS sandbox is in force, the boundary is delegated to the harness
  -- so it must be *verified*, not assumed.** niwa has no OS sandbox of its own; egress denial and the
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
  - *Preflight (before any instance is created):* resolve `watch_containment`
    and `watch_sandbox`, then -- only when the OS sandbox is called for --
    actively probe that it can be created now, not merely that the OS is
    nominally supported, and select the backend for the host (see Decision 8
    for the per-host backends and the switch semantics). Concretely, on macOS
    the built-in `sandbox-exec` (Seatbelt) is available unprivileged; on Linux
    the probe checks the backend and dependency the harness requires (`bwrap`
    and `socat` on PATH) and functionally verifies a capability-bearing,
    network-isolable user namespace (`bwrap --unshare-net` succeeds -- it
    fails on a kernel that restricts unprivileged user namespaces). When the
    sandbox cannot be enforced, the matrix above decides: **refuse** under
    `watch_sandbox = required`, or proceed contained-without-sandbox under
    `optional`. Under `watch_containment = off` the probe is skipped entirely.
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

### Decision 8 -- The containment switches, sandbox capability across hosts, and provisioning

*Added after a feasibility investigation (see the Amendment note in Status).
The earlier framing -- "the sandbox works or the feature refuses" with a
Windows-only caveat, plus an `uncontained_policy` trichotomy -- was too
coarse. Containment is now optional and expressed as two nested switches.
Scope is real Linux and macOS hosts.*

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

- **8A -- Adaptive per-host backend.** When the OS sandbox is enabled, the
  preflight (Decision 7) detects the strongest enforceable backend and
  dispatches under it: Seatbelt on macOS, the `bwrap`+`socat` no-egress
  profile on capable Linux. When neither is available, the run does not
  silently weaken -- the `watch_sandbox` switch (8C) decides between refusing
  and proceeding contained-without-sandbox.
- **8B -- Provisioning, unprivileged by default.** `bwrap` and `socat` are
  declared as **Linux-only runtime dependencies** of niwa's tsuku recipe
  (both are installable as prebuilt bottles), so a normal `tsuku install`
  provides them with no `sudo`. They are needed only when the sandbox
  actually runs; macOS needs neither, and the refuse path needs neither. The
  one irreducible privileged step -- unlocking the kernel capability on a
  hardened Linux host -- is an **opt-in** `niwa setup-sandbox` command
  (install the AppArmor profile / set the sysctl) run once, never per
  dispatch. The default install requires no elevation; on macOS and permissive
  Linux nothing more is needed; on hardened Linux, under the default
  `watch_sandbox = required`, the feature refuses (8C) until `niwa
  setup-sandbox` is run. This keeps "standard install leaves a ready system"
  true wherever the kernel permits it, and reduces the hardened-Linux case to
  a single documented command rather than a multi-step manual install.
- **8C -- The two containment switches.** Containment is **optional**,
  expressed today as two niwa **global config settings** (each resolved on the
  usual `flag > config header > default` stack):
  - **`watch_containment`** (outer): `on` (default) or `off`. "Containment" is
    the no-secrets/no-auto-approve bundle -- the credential-scrubbed
    environment, the synthetic HOME, and the fail-closed permission mode
    (Decision 3). When `off`, none of it applies: the watch launch is an
    ordinary `niwa dispatch` with the developer's real environment and
    credentials, so the agent can read the linked issue, CI status, and review
    threads a substantive review needs -- but it still only drafts and waits
    (Decision 6).
  - **`watch_sandbox`** (inner, consulted **only** when containment is on):
    `required` (default) or `optional`. This is the OS no-egress network cage
    (bwrap+socat on Linux, Seatbelt on macOS). On a sandbox-capable host both
    values use the cage; the switch decides only the sandbox-incapable host:
    `required` refuses to dispatch, `optional` proceeds
    contained-without-cage. (`disabled` was removed -- deliberately turning the
    cage off on a capable host is pure downside for a read-only agent.) The
    preflight resolves both switches and follows the matrix in Decision 7.

  The default (`on` + `required`) is the strongest posture, so loosening is
  always an explicit operator choice, never an accident.

  **Future work -- granular policy.** Both switches are a **single global
  setting** today. Making the policy granular -- per repository, per PR
  author, or per team/org (e.g. "contained for external contributors, off for
  trusted teammates") -- is the natural next step this global switch is a
  first step toward. It is deliberately out of scope here: the config surface,
  the resolution precedence across scopes, and the per-PR author lookup are
  all deferred.

  **Future work -- two roadmap follow-ups (not built here).** Two items,
  named in the PRD and the positioning note below, would change the posture:
  (a) **automated author / fork / `author_association` filtering** to gate
  which PRs may run uncontained (today the operator is the gate -- see
  Security Considerations); and (b) an **MCP release-to-act gate**, a
  credential-free act path that lets a **contained** session post without being
  handed credentials, which is what would promote contained mode from
  informational to practical. Both are deferred.

  **Positioning -- contained mode is informational today; uncontained is the
  practical path.** Stated plainly: the contained/sandboxed session, with no
  egress, cannot reach the linked issue, CI status, or review threads a
  substantive review needs, so its draft is limited to what the diff shows. It
  is **informational, not yet practical for most reviews**. **Uncontained +
  operator-trust is the intended practical path** until the two roadmap
  follow-ups above land -- automated author/fork/association gating, and the
  MCP release-to-act gate that lets a contained session post without
  credentials. `watch_containment` still defaults to `on` (safe by default);
  the operator consciously opts into `off` for trusted repos. Each run
  **reports the posture** it is operating under -- one of "contained, sandbox
  in force", "contained, no sandbox", or "uncontained" -- so the operator
  always knows which contract is in force.

**Closing the fail-open at the harness layer.** Whenever the OS sandbox is
enabled, the dispatched instance settings set `sandbox.failIfUnavailable:
true` and `allowUnsandboxedCommands: false`, so Claude Code *refuses to run*
rather than silently disabling the sandbox and proceeding with a warning.
This, plus the niwa-side preflight (Decision 7), means a session is never
*less* contained than its resolved switches call for -- a session runs
uncontained only when `watch_containment = off`, and without the OS cage only
when `watch_sandbox` explicitly permits it.

## Decision Outcome

`niwa watch --once` is a new verb (`internal/cli/watch.go`) that runs one
poll-and-dispatch pass:

1. **Preflight (fail-closed).** Resolve `watch_containment` and
   `watch_sandbox` and follow the Decision 7 matrix. When the OS sandbox is
   called for, verify it can be enforced on this platform; if it cannot,
   refuse (print the reason, exit non-zero) under `watch_sandbox = required`
   or proceed contained-without-sandbox under `optional`. When
   `watch_containment = off`, skip the sandbox entirely.
2. **Poll.** Resolve the login and search GitHub for open PRs with
   `user-review-requested:<login>`; intersect the results with the
   workspace's repos (Decision 4).
3. **Dedup + bound.** Drop PRs already in the handled-set; order the rest
   by PR `created_at` (oldest first -- a deterministic key available from
   the single search) and take at most the per-run bound (default 3).
4. **Per selected PR:** provision an instance (reusing dispatch's
   provisioning); when containment is on, pre-fetch the PR head into the clone
   with the trusted CLI (Decision 2) and merge the OS no-egress sandbox
   profile into the instance's `.claude/settings.json` when `watch_sandbox`
   calls for it (Decision 1); write a metadata-only prompt (Decision
   `prompt`); launch `claude --bg` detached -- with an allowlisted environment
   and synthetic HOME when containment is on, or the ordinary full environment
   when it is off (Decision 3); persist the PR coordinates and draft path
   alongside the session mapping; record the PR in the handled-set only on
   success (Decision 5).
5. **Report.** For each staged review, print the **posture** it runs under --
   "contained, sandbox in force", "contained, no sandbox", or "uncontained".
   On an empty result, print "nothing to stage" and exit zero. On a poll or
   dispatch failure, print the error and exit non-zero without recording the
   affected PR as handled.

Posting is always a human act (Decision 6). In every mode the session drafts
to the known location and halts; the developer posts that draft from their own
trusted session. When containment is on the session holds no token and cannot
post; when off it holds real credentials to read the surrounding context but
is held back by the prompt and the post-guard. niwa has no `post` or `discard`
subcommand; a staged session is dismissed from the Claude Code agents view.

## Solution Architecture

### Components

- **`internal/cli/watch.go` (new).** Registers `watchCmd` with `--once` (no
  subcommands) via `init()` + `rootCmd.AddCommand`. Orchestrates the pass
  above.
- **Switch resolution + `niwa setup-sandbox` (new, Decision 8).** The
  preflight reads `watch_containment` and `watch_sandbox` from niwa config
  (each on the `flag > config header > default` stack; defaults `on` and
  `required`) and, when the OS sandbox is called for, resolves the per-host
  backend (Seatbelt / bwrap-socat / none). `niwa setup-sandbox` is the opt-in
  privileged command that unlocks the capability on hardened Linux (AppArmor
  profile or sysctl).
- **niwa tsuku recipe (packaging, Decision 8B).** A curated `recipes/n/niwa.toml`
  (shadowing today's auto-generated download recipe) declaring
  `runtime_dependencies = ["bubblewrap", "socat"]` scoped to Linux, plus a new
  `recipes/b/bubblewrap.toml` (homebrew-bottle action). macOS pulls neither.
- **Containment settings (extended).** The merged instance
  `.claude/settings.json` also sets `sandbox.failIfUnavailable: true` and
  `allowUnsandboxedCommands: false` so the harness refuses rather than silently
  disabling the sandbox.
- **Post-guard settings (every mode).** The merged instance
  `.claude/settings.json` also adds `permissions.ask` entries for `gh pr
  review` and `gh pr comment`, so any review/comment submission needs operator
  approval. This is written in **every** mode, including `watch_containment =
  off`. It is an accident-prevention guard for the trusted uncontained path,
  **not** a security boundary (command-string matching is not a boundary -- the
  OS sandbox is). See Decision 6.
- **`internal/github` (extended).** Two net-new client methods (the client
  has only `ListRepos`/`GetRepo` today): `CurrentLogin(ctx) (string, error)`
  wrapping `GET /user`; and `SearchReviewRequestedPRs(ctx, login) ([]PRRef,
  error)` wrapping `GET /search/issues`. No `CreateReview` method is needed --
  niwa posts nothing, and the agent posts nothing either (Decision 6): in
  every mode the agent drafts for the developer to post from their own session.
  `PRRef{Owner, Repo, Number, URL, CreatedAt}` -- `CreatedAt` is the
  PR's `created_at` from the search payload (the review-*request* time is not
  in that payload; ordering uses `created_at` as a deterministic, single-call
  proxy). Auth reuses `resolveGitHubToken` and `NewAPIClient` (for the poll,
  in trusted niwa code -- separate from the token the off-mode session
  inherits).
- **Containment on the dispatch path (net-new dispatch surface, active only
  when `watch_containment` is on).**
  - A containment-profile builder producing the settings fragment applied to
    the instance settings via the same merge helper `applier.Create` uses
    (`MergeInstanceOverrides` / the root-settings writer) -- watch is a
    *second writer* to `.claude/settings.json`, ordered Create -> watch-merge
    -> re-verify -> launch. The fail-closed permission settings are written
    whenever containment is on; the OS-cage stanza (`sandbox.enabled: true`,
    `sandbox.network.allowedDomains: []`, `sandbox.failIfUnavailable: true`)
    is written only when `watch_sandbox` is enabled for the run. The
    post-guard (`permissions.ask` on `gh pr review`/`gh pr comment`) is written
    in **every** mode, including `watch_containment = off`.
  - An env-allowlist on the launch seam. The launch func gains an options
    parameter, e.g. `dispatchLaunch(ctx, instanceDir, prompt, passthrough,
    LaunchOpts)` where `LaunchOpts{EnvOverride []string}`; `EnvOverride ==
    nil` preserves today's `cmd.Env = os.Environ()`, so both the ordinary
    dispatch path **and** the `watch_containment = off` path stay unchanged,
    and the contained watch path passes the allowlisted env (including the
    synthetic `HOME`).
  - A trusted, hardened PR-head fetch + filter-neutered checkout (Decision 2)
    run before launch **only when containment is on** (so the contained agent
    needs no network); when containment is off, the agent fetches the PR
    itself with its inherited credentials.
- **Handled-set store.** Read/append helpers over
  `<workspaceRoot>/.niwa/watch-handled` (flat `owner/repo#number` lines).
- **Staged-review record + handle.** The **handle is the dispatch session
  short id** shown in the agent view (the `shortID` from the existing
  `SessionMapping` capture). At dispatch niwa writes a small record
  `{handle, owner, repo, number, url, draftPath}` to
  `<workspaceRoot>/.niwa/watch/<handle>.json`. With no `post`/`discard`
  subcommand consuming it, the record is discoverability metadata: it lets the
  developer (or a thin `--json`-style report, if added later) map a staged
  session in the agent view back to its PR and draft path. `draftPath` is
  populated for **every** run, since the agent drafts and waits in both modes.

### The metadata-only prompt

Assembled by a pure function of the `PRRef` (satisfying the determinism
requirement): a fixed instruction template interpolating only `owner/repo`,
the PR number, and the PR URL. No title, body, diff, or author name is
interpolated. The template has two fixed variants, selected by
`watch_containment`:

- **Contained (on):** read the PR (title, body, diff, linked issue, CI
  status) from the filter-neutered local checkout of the pre-fetched PR head,
  treat all of it as untrusted, write the review to the known draft path, and
  **stop before posting** (the session has no credential to post with).
- **Uncontained (off):** read the PR from the host with the inherited
  credentials -- including the linked issue, CI status, and review threads --
  treat all of it as untrusted, write the review to the known draft path, and
  **stop before posting**. The agent does not post; the post-guard is a
  backstop if it tries.

Both variants are fixed strings chosen by trusted code, so which one is used
is never the untrusted content's decision.

### The known draft location (every mode)

niwa defines a fixed path in the instance (e.g.
`<instanceRoot>/watch-review-draft.md`) recorded in the staged-review record.
The agent writes there in **every** mode; the developer reads there before
posting from their own session.

### Data flow

```
watch --once
  resolve watch_containment (on|off), watch_sandbox (required|optional)
  preflight: on+required+sandbox-unenforceable --> stderr + non-zero exit (refuse)
        | otherwise proceed
  GET /user -> login
  GET /search/issues (user-review-requested:login, is:open, is:pr)
        |
  intersect with workspace repos (config.Discover)
        |
  minus handled-set; order by created_at (oldest first); take <= bound
        |
  for each PR:
     provision instance (applier.Create)
     if containment on:
        fetch PR head SHA + filter-neutered checkout  <- untrusted content as data
        if sandbox enabled: merge no-egress profile into .claude/settings.json;
                            re-verify merged sandbox stanza
        write contained prompt (draft + stop before posting)
        launch claude --bg --detach with allowlisted env + synthetic HOME  (NO GitHub token)
     if containment off:
        write uncontained prompt (read full context + draft + stop before posting)
        launch claude --bg --detach with ordinary env  (developer's real credentials)
                              + post-guard on gh pr review/comment
     persist staged-review record; append handled-set (on success)
        |
  agent view shows the staged session (posture reported)
        |
  every mode -> developer reads the draft, posts it from their own session
  (dismiss an unwanted staged session directly from the agents view)
```

## Implementation Approach

Phased so each step is independently testable. The handled-set and
staged-review record schemas are defined up front (step 0) since later steps
depend on them.

0. **State schemas.** Define the handled-set format (`owner/repo#number`
   lines) and the staged-review record (`{handle, owner, repo, number, url,
   draftPath}` at `.niwa/watch/<handle>.json`), with read/write helpers.
1. **GitHub poll.** Add `CurrentLogin` and `SearchReviewRequestedPRs` to the
   client; unit-test against the existing fake server (`NIWA_GITHUB_API_URL`).
   No review-posting method is added -- niwa posts nothing (Decision 6).
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
   unchanged, used for both ordinary dispatch and `watch_containment = off`)
   with the synthetic `HOME` for the contained path; the containment-profile
   builder + settings second-write via the existing merge helper + per-instance
   re-verification, with the OS-cage stanza written only when `watch_sandbox`
   is enabled. The post-guard (`permissions.ask` on `gh pr review`/`gh pr
   comment`) is written in every mode, contained or not. Unit-test the
   allowlist (subset + canary absence for
   `GITHUB_TOKEN`/`GH_TOKEN`/`SSH_AUTH_SOCK` and an on-disk
   `~/.netrc`/`~/.config/gh` sentinel), the post-guard entries, and the merged
   settings document.
5. **Switch resolution + `watch --once` orchestration.** Read
   `watch_containment`/`watch_sandbox` (flag > config > default). Wire
   preflight -> poll -> select -> per-PR provision -> (contained:
   fetch/merge/re-verify/launch-allowlisted | off: launch-ordinary) -> record.
   Fail-closed preflight (refuse only in the on+required+unenforceable cell)
   and fail-loud error paths.
6. **Containment-off path.** The uncontained variant: an ordinary dispatch
   (nil `EnvOverride`, no sandbox stanza, no pre-fetch) with the uncontained
   prompt, so the agent reads the surrounding context and drafts. Verify it
   inherits the developer's environment, still writes a draft and halts before
   posting, and that the post-guard gates `gh pr review`/`gh pr comment`.
7. **Adversarial / live-enforcement test (the boundary proof, sandboxed path
   only).** A hostile-PR fixture whose title/body/diff attempt egress, push,
   and exfiltration, dispatched with containment on and the OS sandbox
   enforced. From inside the running contained session (bypassing the model),
   the test attempts **real outbound network** -- both a connection to a
   domain and a **raw socket to a literal IP** -- and a write outside the
   instance, and asserts each fails at the OS layer (connection blocked /
   EPERM). This live attempt, not a settings-file assertion, is what proves
   empty-allowlist = deny-all and that the sandbox is actually enforcing for
   the `--bg --detach` launch; it gates release. It does not apply to the
   `watch_containment = off` path, which is uncontained by design.

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

**Permission scope / network -- and the delegated dependency.** The analysis
below covers the default posture, `watch_containment = on` with the OS
sandbox enabled (`watch_sandbox = required`, or `optional` and available); an
operator who sets `watch_containment = off` has explicitly opted out of it for
a trusted source, and `watch_sandbox = optional`-unavailable keeps the
credential scrub and fail-closed mode but not the OS cage. When the OS
sandbox is enabled it is applied via the instance settings merge
(`sandbox.enabled`, empty `sandbox.network.allowedDomains`, fail-closed
permission mode) and re-verified per instance before launch (Decision 7).
**niwa does not implement the sandbox; it delegates to the Claude Code
harness.** The whole boundary therefore rests on the harness
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

**Credential scoping.** When `watch_containment` is on, the session
environment is an allowlist (model auth + `PATH`/locale + a synthetic
`HOME`); the GitHub token, `SSH_AUTH_SOCK`, and `GH_*`/`GITHUB_*` are absent,
and the synthetic `HOME` plus the sandbox's filesystem policy keep on-disk
credentials (`~/.config/gh`, `~/.netrc`, `~/.ssh`) out of reach (Decision 3).
Posting never happens inside this session: it holds no token, so the
developer posts the draft from their **own trusted session** and the drafting
session's containment is never lifted (Decision 6). Because niwa mints no
posting credential and runs no post step, there is no scoped token to leak
and no in-code review `event` to protect -- the developer reading the draft
before posting is the checkpoint that a hostile draft cannot force an
unwanted approval. When `watch_containment` is off, the session deliberately
inherits the developer's real credentials so it can read the linked issue, CI
status, and review threads a substantive review needs -- but it still only
drafts and waits. Posting stays a human act: the prompt tells the agent not to
post, and the post-guard (`permissions.ask` on `gh pr review`/`gh pr comment`)
gates any submission behind operator approval. That guard is an
accident-prevention convenience, **not** a security boundary -- command-string
matching is not a boundary; it exists so a stray prompt-following cannot post
under the operator's name without a click.

**Trigger scope and operator responsibility.** The
`user-review-requested:<login>` qualifier (Decision 4) fires on more than PRs
the operator personally chose to review: it also matches **teammate-assigned
review requests, CODEOWNERS auto-requests, and external-fork triage** -- PRs
the operator did not vet. This breadth is accepted for this version. When
running **uncontained**, the operator's responsibility is therefore: **only
review PRs you trust.** The draft-and-wait gate (the agent posts nothing in any
mode) and the post-guard are what keep an un-vetted auto-assignment from acting
under the operator's name before the operator looks at the draft. Automated
author/fork/`author_association` gating (Decision 8 future work) would move this
gate from operator discipline into code; it is not built here.

**Data exposure.** The persisted state (handled-set of `owner/repo#number`,
staged-review records of PR coordinates + draft path) is low-sensitivity
workspace metadata at rest under `.niwa/`; it contains no secrets. The PR's
content does reach the model API by design (the review has to read it);
that is inherent to reviewing and is the same trust posture as any Claude
Code session reading a repo.

**Accepted residual risks.**

- **Hardened Linux needs a one-time privileged setup.** On a kernel that
  restricts unprivileged user namespaces (e.g. Ubuntu 24.04), the OS sandbox
  cannot run until `niwa setup-sandbox` is run once (Decision 8). Until then,
  under the default `watch_sandbox = required` the feature refuses to stage
  those reviews; `optional` stages them contained-without-cage. macOS and
  permissive Linux need no elevation. This
  residual is a property of the OS security model, not a niwa limitation --
  the capability is root-gated and not user-installable.
- **Windows.** The OS sandbox is unavailable on Windows; under the default
  `watch_containment = on` + `watch_sandbox = required` the design fails
  closed there (refuses to dispatch) rather than running a session less
  contained than requested. Windows self-hosters get no contained staged
  reviews until a later version addresses it (they can opt into
  `watch_sandbox = optional` or `watch_containment = off` at their own risk).
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
  authored by the session that read the PR and could carry attacker-influenced
  prose, in **every** mode. The developer reads it before posting it from
  their own session -- the human is the trust checkpoint for the draft's
  *content*, while containment (when on) covers the session's *actions*. This
  assumption is load-bearing and specifically means the draft must NOT be
  auto-ingested by another agent, or piped straight into a post, without a
  human read: nothing in niwa posts the draft automatically (there is no
  `post` verb), and the agent does not post in any mode (the prompt plus the
  post-guard hold it back), but a future automated consumer of the draft would
  reopen this channel.

## Consequences

### Positive

- The dispatch decision is injection-proof by construction: no
  author-controlled text enters the prompt. The workspace intersection
  strengthens this -- only known-good `owner/repo` identifiers from the
  developer's own workspace reach the prompt, so the sole attacker-chosen
  value is the PR number (an integer), leaving no room to inject text via
  the identifiers themselves.
- By default (containment on), the review session cannot egress (empty
  allowlist) and cannot act with the developer's GitHub credentials (token
  absent from its env), so a hostile PR is contained at the OS/process layer
  rather than by the model.
- Posting stays a human act in every mode instead of needing its own verb and
  scoped credential: the agent drafts and waits, so niwa never brokers a
  posting credential and there is no auto-post to reason about under injection.
- Credential scoping and the sandbox profile land as reusable dispatch
  surface a later contained-dispatch feature can adopt.
- The verb reuses existing provisioning and settings-merge machinery.
- The feature runs unprivileged out of the box on macOS and permissive Linux;
  the only elevation is a single opt-in `niwa setup-sandbox` on hardened
  Linux, and the two switches let the operator -- not the code -- decide when
  to relax containment for a trusted source.

### Negative / costs

- Net-new GitHub client surface (search + user) and the containment path make
  the niwa verb itself sizeable; the Decision-8 amendment adds a tsuku recipe,
  a `setup-sandbox` command, and the two config switches, so the effort now
  spans niwa **and** a tsuku-recipe change -- it is no longer a single niwa
  PR. (Dropping the `post`/`discard` subcommands trims some of this back.)
- The two switches add operator-facing surface (two config knobs, a setup
  command, and per-host behavior to document); a wrong default here would be a
  security regression, so the defaults are the strongest posture (`on` +
  `required`) and the fail-open is closed at both the niwa and harness layers.
- Pre-fetching the PR head means niwa's trusted code touches untrusted repo
  content; getting this wrong (a filter-honoring checkout, LFS smudge, repo
  hooks, submodule recursion) would execute attacker code *outside* the
  sandbox. Decision 2 constrains the fetch to inert-data handling to close
  that, but it is the sharpest implementation risk and carries a dedicated
  test.
- The empty egress allowlist means the agent cannot use `gh`/network tools;
  the prompt must direct it to the local clone, and some agent conveniences
  are unavailable in-session (acceptable, and the point).
- With containment on and no egress, the session cannot reach the linked
  issue, CI status, or review threads, so a contained draft is limited to what
  the diff shows -- contained mode is **informational, not yet practical for
  most reviews**. Uncontained + operator-trust is the practical path until the
  two roadmap follow-ups land (Decision 8 future work).

### Mitigations

- Fail-closed preflight guarantees no uncontained session launches.
- Handled-set-on-success-only prevents a transient failure from suppressing
  a review.
- The adversarial test is part of "done," so the boundary is verified by the
  exact injection surface it defends.
