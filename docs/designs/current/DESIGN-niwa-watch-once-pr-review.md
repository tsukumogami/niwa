---
status: Current
upstream: docs/prds/PRD-niwa-watch-once-pr-review.md
problem: |
  niwa dispatch is a pull verb that launches a background worker inheriting
  the dispatcher's full environment and with no network containment. To
  stage PR reviews proactively, niwa must poll GitHub for directly-requested
  reviews, dispatch a review agent that reads an untrusted PR, and cut that
  agent off from the network -- none of which the current dispatch path
  provides.
decision: |
  Add a `niwa watch --once` verb that polls GitHub (`user-review-requested`),
  intersects with the workspace's repos, and for each new PR provisions an
  instance and launches a detached review agent. The dispatched session always
  runs under the developer's real HOME, real environment, and real Claude
  daemon, so it appears in the developer's own `claude agents` view and
  authenticates normally. Containment is a single axis -- the OS no-egress
  sandbox -- governed by one global switch `watch_sandbox` (required|off,
  default required). When required, niwa pre-fetches the PR head with the
  trusted CLI (outside the sandbox) and merges the OS no-egress sandbox profile
  into the instance's `.claude/settings.json`. Because `sandbox.enabled` cages
  only Bash subprocesses, the boundary is a combination: the OS sandbox over
  Bash egress, a PreToolUse hook denying the WebFetch/WebSearch/MCP channels it
  does not cover, and `--strict-mcp-config`. Together they leave the session no
  network. In every mode the agent drafts its review to a known file the
  operator posts from their own session; posting is always a human act. When
  `watch_sandbox = off` the agent runs an ordinary dispatch with real
  credentials and live network so it can read the linked issue, CI status, and
  review threads a substantive review needs, but it still only drafts and
  waits. A metadata-only prompt carries no author-controlled text in every mode.
rationale: |
  The security boundary is egress denial, not credential hiding. The session
  runs under the developer's real HOME and daemon precisely so it surfaces in
  the agents view (the feature's whole point -- the review arrives as another
  agent where the developer already works) and can refresh its subscription
  auth; a synthetic HOME broke both. In `required` mode the agent can read
  anything on disk but reach no network -- the OS sandbox cages Bash egress and
  a PreToolUse hook closes WebFetch/WebSearch/MCP -- so it can neither
  exfiltrate a secret nor act on the PR. The sandbox defaults on because the review reads untrusted
  content; the single switch lets an operator relax it deliberately for trusted
  sources rather than by accident. The sandbox profile rides niwa's existing
  settings-merge seam; pre-fetching the PR with trusted code lets a sandboxed
  agent run with a truly empty egress allowlist, so the boundary does not rest
  on the model. Posting is always a human act -- the agent drafts and waits in
  every mode -- so niwa needs no posting verb or credential; a post-guard keeps
  the trusted path from posting by accident but is not a boundary. Reusing the
  dispatch provisioning path keeps the change focused.
---

# DESIGN: niwa watch --once PR-review dispatch

## Status

Current

Technical design for the first version of proactive PR-review dispatch in
niwa, implementing the Accepted PRD.

**Amendment (containment model, sandbox capability, and provisioning).** A
feasibility investigation established that the OS sandbox's requirement is a
root-gated kernel capability that varies by host, not a missing package. A
later round of testing then established that **hiding the developer's
credentials was the wrong approach**: a synthetic HOME plus an env-var
allowlist removed the review session from the developer's own `claude agents`
view (it registered with a separate transient daemon) and broke Claude auth
(an isolated headless daemon cannot refresh subscription OAuth tokens, so the
contained session stalled). The realization: the OS no-egress sandbox was
always the real boundary, which makes credential-hiding both harmful and
unnecessary -- with no network, an extracted credential is useless. So the
earlier framing is superseded by **Decision 8**: the dispatched session always
runs under the developer's **real HOME, environment, and Claude daemon**, and
containment is a **single axis** -- the OS no-egress sandbox -- governed by one
global switch `watch_sandbox` (required|off). This replaces the previous two
switches (`watch_containment` on/off + `watch_sandbox` required/optional); the
synthetic HOME and the credential-scrub allowlist are removed (recorded as a
superseded alternative in Decision 3). The design keeps adaptive per-host
backend selection, unprivileged-by-default provisioning of `bwrap`/`socat` via
tsuku with an opt-in `niwa setup-sandbox` for the one privileged step on
hardened Linux, and `sandbox.failIfUnavailable` to close the harness
fail-open. Posting is always a human act (no `post` subcommand): the agent
drafts and waits in every mode and the operator posts from their own session; a
post-guard (a PreToolUse hook on `Bash` that denies `gh pr review`/`gh pr
comment`) keeps the trusted path from posting by accident but is not a boundary.
This expands the
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
platform-vouched identifiers; (3) by default (`watch_sandbox = required`) run
that agent under the developer's **real** environment and daemon -- so it
appears in the developer's own agents view and authenticates -- but with
**every egress channel closed** (the OS sandbox caging Bash subprocesses, a
PreToolUse hook denying WebFetch/WebSearch/MCP), so it can read the PR but
reach no network; (4)
have the agent draft its review for the developer to post from their own
session in **every** mode, while letting an operator turn the sandbox off
(`watch_sandbox = off`) for trusted sources so the agent runs with live
network and can read the surrounding context (linked issue, CI, review
threads) -- it still only drafts and waits; and (5) skip already-handled PRs.
When the sandbox is on, the enforcement is the crux, and it must hold at the
tool/OS layer, not rest on the model's judgment.

The relevant existing surface (grounding for the decisions below): the
dispatch flow and its `--detach` semantics in `internal/cli/dispatch.go`;
the full-environment launch in `internal/cli/dispatch_launcher.go`; niwa's
existing per-instance `.claude/settings.json` write/merge
(`InstallWorkspaceRootSettings`, `buildSettingsDoc`,
`MergeInstanceOverrides`); the GitHub client and token resolution
(`internal/github`, `internal/cli/token.go`); and workspace repo enumeration
(`config.Discover`, `WorkspaceConfig.Sources`).

## Decision Drivers

- **The boundary must be deterministic.** Egress denial must be enforced by
  the OS sandbox, not by asking the model to behave. A control the model can
  talk its way around is not a control.
- **The dispatch decision must be injection-proof.** No author-controlled
  text (title, body, diff, author name) may enter the prompt that launches
  the session.
- **The boundary is egress, not credential hiding.** The review reads
  untrusted content, so the session must be unable to reach the network on
  **any** channel -- both Bash-subprocess egress (caged by the OS sandbox) and
  the non-Bash channels the OS sandbox does not cover, WebFetch/WebSearch/MCP
  (denied by a PreToolUse hook, with `--strict-mcp-config` as backup). Closing
  every egress channel stops both exfiltration and acting (posting, pushing,
  and merging all need the network). Hiding the on-disk credential is neither
  sufficient (a token is extractable and usable by any HTTP client) nor
  necessary once every channel is closed (an extracted credential is then
  useless), and it broke the agents-view integration and auth, so the design
  keeps the real environment and relies on egress denial.
- **Single-PR implementability.** The change should reuse niwa's dispatch
  provisioning and settings-merge machinery rather than build a parallel
  stack.
- **Fail closed by default.** With the default `watch_sandbox = required`,
  where the OS sandbox cannot be enforced the verb must refuse to dispatch
  rather than silently run an uncontained session. Turning the sandbox off is
  only ever an explicit operator setting, never a silent degradation.
- **No daemon.** `watch --once` is a stateless single-shot verb.

## Considered Options

### Decision 1 -- How the no-egress sandbox profile reaches the session

- **Option 1A (chosen): merge the profile into the instance's
  `.claude/settings.json`.** niwa already writes and merges a root
  `.claude/settings.json` into every provisioned instance. The watch path
  adds `sandbox.enabled`, an empty `sandbox.network.allowedDomains`, and
  `sandbox.failIfUnavailable` to that merged document before launch, alongside
  a PreToolUse egress-deny hook (matcher `WebFetch|WebSearch|mcp__`) that closes
  the non-Bash egress channels the OS sandbox does not cage.
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

  (Note: the empty allowlist restricts the agent's Bash-subprocess traffic;
  the PreToolUse egress-deny hook closes WebFetch/WebSearch/MCP. The Claude Code
  harness's own model-API channel is separate and is what lets the session run
  at all; see Security Considerations.)

### Decision 3 -- The dispatched environment (real HOME and daemon; synthetic HOME rejected)

- **Option 3A (chosen): the session runs under the developer's real
  environment, real HOME, and real Claude daemon** -- the same `cmd.Env =
  os.Environ()` and real `HOME` the ordinary dispatch path uses, in **every**
  mode. This is not a convenience; it is load-bearing for two reasons. First,
  the review is meant to arrive as **another agent in the developer's own
  `claude agents` view**, where they are already working, so they triage it in
  flow (R13). A session registers in that view only if it runs under the
  developer's real Claude daemon; a session bound to a synthetic HOME
  registers with a separate transient daemon and never appears there. Second,
  the daemon must **refresh subscription OAuth tokens** to keep the session
  alive; an isolated headless daemon under a synthetic HOME cannot, so the
  session stalls on auth. Running under the real environment fixes both.

  The security boundary is therefore **egress denial, not credential hiding**
  (see Security Considerations). The session CAN read anything on disk --
  `~/.ssh`, `~/.config/gh`, `~/.aws`, the `gh` token -- but reaches **no
  network**, so it can neither exfiltrate a secret nor act on the PR (posting,
  pushing, and merging all need the network) regardless of which binary it uses.
  Reaching no network takes a **combination**, because the OS sandbox
  (`sandbox.enabled`) cages only **Bash** subprocesses: the OS sandbox closes
  Bash egress, and a PreToolUse hook (matcher `WebFetch|WebSearch|mcp__`) plus
  `--strict-mcp-config` close the non-Bash channels -- WebFetch, WebSearch, and
  MCP tools -- that would otherwise egress outside the sandbox. (Under real HOME
  + `bypassPermissions` the session carries the developer's full tool fleet, so
  the OS sandbox alone would leave a credential-exfil hole: read the `gh` token
  from disk, send it via `WebFetch` or a send-capable MCP server.) Hiding the
  credential adds nothing **only because every one of those channels is
  closed**: a token is extractable from disk and usable by any HTTP-capable
  program (curl, git, python) or by `WebFetch`/MCP, so if any egress channel
  were open the extracted credential would not be useless. Command-level gating
  alone is not a boundary either (Decision 6).

  **Writable region.** The agent must write its drafted review; the draft
  lands at `<instanceRoot>/watch-review-draft.md` in the instance directory
  (which contains the clone), outside the clone's working tree so it does not
  pollute the reviewed repo. Writes are confined to the instance the same way
  egress is confined to nothing. The OS sandbox restricts **Bash** writes to the
  instance, but the built-in `Write`/`Edit`/`NotebookEdit` tools -- like
  WebFetch/MCP -- run OUTSIDE it (through the permission system, which
  `bypassPermissions` skips), so a **filesystem-guard PreToolUse hook** (matcher
  `Write|Edit|NotebookEdit`) adjudicates them: a write that resolves inside the
  instance is allowed, one that resolves outside is denied. Without it an injected
  agent could write `~/.ssh/authorized_keys`, `~/.bashrc`, or a `~/.gitconfig`
  `core.hooksPath` and gain persistence or code execution from merely reading an
  untrusted PR. The deny is **hard, not an operator prompt**: a review-drafting
  agent has no legitimate out-of-instance write (its only writes are the draft and
  clone-local files), and a hard deny is provably fail-closed and symmetric with
  the network egress deny. An operator-approval (`ask`) refinement -- surfacing the
  write for a human to approve in the agents view -- was investigated and found
  **fail-open** under the session's `bypassPermissions` mode: a PreToolUse hook's
  `permissionDecision: "ask"` is not honored there (verified -- the hook fires and
  emits a valid ask, yet the write lands), so it cannot be used without a
  permission-posture redesign that runs the session outside `bypassPermissions`.
  That upgrade is tracked as future work (niwa#201); the hard deny is the
  fail-closed default until it lands.
- **Option 3B (rejected, previously chosen): a credential-scrubbed env
  allowlist plus a synthetic HOME.** An earlier version of this design
  scrubbed `cmd.Env` to an explicit allowlist (model auth + `PATH`/locale) and
  pointed `HOME` at a scratch directory with no developer dotfiles, to keep
  the GitHub token and on-disk credentials (`~/.config/gh/hosts.yml`,
  `~/.netrc`, `~/.ssh/`) out of the session. Testing rejected it for two
  independent reasons: (1) the synthetic HOME made the session register with a
  **separate transient daemon**, so it vanished from the developer's own
  `claude agents` view -- defeating the feature's whole point, since the review
  is supposed to arrive as another agent in the view the developer is already
  working in; and (2) the isolated headless daemon **could not refresh
  subscription OAuth tokens**, so the contained session stalled on auth. The
  approach was also unnecessary: egress denial was always the real boundary --
  the OS sandbox over Bash plus the PreToolUse hook over the non-Bash channels
  it does not cage -- and with every egress channel closed an extracted
  credential is useless. So credential-hiding is both harmful (breaks
  agents-view and auth) and
  redundant, and is removed. The env allowlist and synthetic HOME are gone;
  the session inherits the real environment in every mode.

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
  after a PR's review agent is successfully dispatched.
- **Option 5B (rejected): a structured store (SQLite / JSON with per-PR
  state).** Rejected as premature -- the only question this version asks is
  "handled or not," and richer dedup/expiry state is explicitly deferred.

### Decision 6 -- How a review gets posted

Posting is not a niwa verb, and it is **not the agent's act either**. In every
mode the dispatched agent drafts its review to a known file and waits;
**posting is always a human act** -- the operator reads the draft and submits
it from their own trusted session. The sandboxed and uncontained modes differ
in **network and live read context**, not in who posts. So there is no posting
step to design, credential to mint, or `post` subcommand to build.

- **Option 6A (chosen): the agent always drafts and waits; posting is a human
  act in every mode.** In both modes the session writes its review to the
  known location and halts -- it never posts, comments, approves, or pushes,
  and the developer posts the draft from their **own trusted session**. When
  `watch_sandbox = required`, the session runs under the OS sandbox and has
  **no network** (Decision 2), so it reads its pre-fetched clone and could not
  post even if the untrusted content told it to. When `watch_sandbox = off`,
  the session runs an ordinary dispatch with the developer's real environment
  and live network **so it can read the linked issue, CI status, and review
  threads a substantive review needs** -- context the no-egress path cannot
  reach -- but it still only drafts and waits. Two things hold the `off` path
  back from posting: the prompt tells the agent not to post, and the
  dispatched instance settings carry a **post-guard** -- a PreToolUse hook
  (matcher `Bash`) that denies `gh pr review` and `gh pr comment`. It is a hook
  rather than a `permissions.ask`/`deny` rule because the session runs under
  `bypassPermissions`, where permission rules do not fire but a PreToolUse hook
  still does (verified live). The post-guard is a **convenience /
  accident-prevention guard for the trusted path, NOT a security boundary**:
  command/hook-level act-gating is not a boundary, because a token is
  extractable from disk and usable via any HTTP-capable program (curl, git,
  python) without ever invoking the gated command (egress denial is the
  boundary). It exists so a stray prompt-following cannot post under the
  operator's name, and it is applied in every mode (harmless where the session
  already has no egress). Because niwa posts nothing, it exposes no
  `post` or `discard` subcommand; a staged session the developer no longer
  wants is dismissed from the Claude Code agents view.
- **Option 6B (rejected): a trusted `niwa watch post <handle>` subcommand**
  that reads the drafted review and posts it via the GitHub API with a
  narrowly-scoped credential niwa provisions and keeps out of the sandboxed
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
  sandboxed path: it still assumes niwa assembles a post command, but adds a
  brittle copy-paste step and hard-codes the disposition, where letting the
  developer post from their own session leaves them in full control of what
  and how they post.
- **Option 6D (rejected): let the off-mode agent post directly.** An earlier
  framing had the uncontained agent review and post autonomously. Rejected
  because auto-posting under the operator's real identity on a PR the trigger
  may have auto-assigned (a teammate request, a CODEOWNERS auto-request, or
  external-fork triage -- see Decision 4 and Security Considerations) lets a
  prompt-injected agent act under the operator's name before any human looks.
  Drafting and waiting in every mode, plus the post-guard, keeps a human in
  the loop while still giving the `off`-mode agent the real credentials and
  live network that make its draft substantive.

### Decision 7 -- Preflight resolution and fail-closed detection

The preflight resolves the single containment switch (defined in Decision 8)
and follows this authoritative matrix. It is the single source of truth for
what a dispatched session gets and for the one cell that refuses:

| `watch_sandbox` | The dispatched review session gets |
|---|---|
| required (default) | Real HOME + real environment + real daemon, with **every egress channel closed** -- the OS sandbox over Bash + a PreToolUse hook over WebFetch/WebSearch/MCP (with `--strict-mcp-config`) = the boundary. If the sandbox cannot be enforced on the host, `watch --once` **refuses** to dispatch that review (do not dispatch). |
| off | Real HOME + real environment + real daemon, **no** sandbox -- an ordinary `niwa dispatch` with the developer's real credentials and live network, as if they ran it themselves. The trusted path. |

The live-enforcement proof below applies only to the `required` row when the
sandbox is actually in force on the host; the `off` row is uncontained by the
operator's explicit choice and has no boundary to verify.

  **When the OS sandbox is in force, the boundary is delegated to the harness
  -- so it must be *verified*, not assumed.** niwa has no OS sandbox of its
  own; Bash-subprocess egress denial is enforced by the Claude Code harness's
  `sandbox.*` settings, and the non-Bash channels (WebFetch/WebSearch/MCP) are
  denied by a PreToolUse hook the harness runs. The design therefore depends on
  a harness version that (a) supports `sandbox.enabled` +
  `sandbox.network.allowedDomains`, (b) treats an **empty** `allowedDomains` as
  *deny-all* (not allow-all), (c) actually creates and enforces the sandbox for
  a `--bg --detach` launch, and (d) fires the PreToolUse hook under
  `bypassPermissions`. These are assumptions, and a silent inversion (empty read
  as allow-all, the sandbox failing to start and the session running with only a
  warning, or the hook not firing) would pass every settings-shaped check. So
  verification is layered and fail-closed:

- **Option 7A (chosen): preflight, re-verify the effective settings, and
  prove enforcement live.** Three checkpoints:
  - *Preflight (before any instance is created):* resolve `watch_sandbox`,
    then -- only when it is `required` -- actively probe that the sandbox can
    be created now, not merely that the OS is nominally supported, and select
    the backend for the host (see Decision 8 for the per-host backends).
    Concretely, on macOS the built-in `sandbox-exec` (Seatbelt) is available
    unprivileged; on Linux the probe checks the backend and dependency the
    harness requires (`bwrap` and `socat` on PATH) and functionally verifies a
    capability-bearing, network-isolable user namespace (`bwrap --unshare-net`
    succeeds -- it fails on a kernel that restricts unprivileged user
    namespaces). When the sandbox cannot be enforced under `required`, the run
    **refuses**. Under `watch_sandbox = off` the probe is skipped entirely.
  - *Effective-settings re-verification (per instance, before launch):* the
    sandbox stanza lives in a *merged* `.claude/settings.json`, and the harness
    applies its own downstream merge (managed/enterprise settings, and the
    `--settings` flag `niwa dispatch` already injects for remote control at
    high precedence). niwa must both re-read the merged instance file to
    confirm the sandbox stanza survived *and* ensure its own `--settings`
    injection does not relax `sandbox.*`. Where niwa cannot observe the
    harness's final merge, it must not silently assume the stanza wins.
  - *Live enforcement proof (the real check):* settings presence does not
    prove enforcement. The adversarial acceptance test (Implementation step 7)
    attempts **actual escape on all four credential-leak channels** from inside a
    real `claude --bg` sandboxed session -- a **WebFetch**, an **MCP tool** call,
    a **raw Bash socket to a literal IP**, and a **built-in Write to a path
    outside the instance** -- and asserts each fails. The raw Bash socket
    distinguishes deny-all from a silently-inverted allow-all and a proxy-only
    egress path (where a raw socket could escape) from a default-deny network
    namespace; the WebFetch and MCP attempts prove the egress-deny PreToolUse hook
    closes the network channels the OS sandbox does not cage; the out-of-instance
    Write proves the filesystem-guard PreToolUse hook closes the write channel it
    does not cage (checked authoritatively -- the out-of-instance file must be
    absent afterward, not merely the agent's self-report).
  On any preflight/re-verify failure `watch --once` prints the reason to
  stderr and exits non-zero; the live proof gates release, not each run.
- **Option 7B (rejected): dispatch, then check.** Rejected because a session
  could reach a runnable state uncontained before the check fires; the
  preflight-plus-re-verify-plus-live-proof layering guarantees no
  uncontained session is silently trusted.

### Decision 8 -- The containment switch, sandbox capability across hosts, and provisioning

*Added after a feasibility investigation and a later round of testing (see the
Amendment note in Status). The earlier framing -- "the sandbox works or the
feature refuses" with a Windows-only caveat, then an `uncontained_policy`
trichotomy, then a two-switch `watch_containment` + `watch_sandbox` model with
a synthetic HOME -- was superseded. Containment is now a single axis: the OS
no-egress sandbox, on or off, over a session that always runs under the
developer's real environment. Scope is real Linux and macOS hosts.*

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

- **8A -- Adaptive per-host backend.** When the OS sandbox is enabled
  (`watch_sandbox = required`), the preflight (Decision 7) detects the
  strongest enforceable backend and dispatches under it: Seatbelt on macOS,
  the `bwrap`+`socat` no-egress profile on capable Linux. When neither is
  available, the run does not silently weaken -- under `watch_sandbox =
  required` it refuses (8C).
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
- **8C -- The single containment switch.** The dispatched session always runs
  under the developer's **real HOME, environment, and Claude daemon**
  (Decision 3) -- that is what makes it appear in the developer's own `claude
  agents` view and authenticate. Containment is a single axis: the OS
  no-egress sandbox, on or off, expressed today as one niwa **global config
  setting** `watch_sandbox`, resolved on the usual `flag > config header >
  default` stack:
  - **`required`** (default): run the session inside the OS no-egress network
    cage (bwrap+socat on Linux, Seatbelt on macOS) over Bash, plus a PreToolUse
    hook denying WebFetch/WebSearch/MCP and `--strict-mcp-config` -- together the
    security boundary. On a sandbox-incapable host, **refuse** to dispatch
    (fail-closed); `niwa
    setup-sandbox` unlocks it on hardened Linux, or the operator explicitly
    sets `off`.
  - **`off`**: no sandbox. The session runs with real credentials and live
    network for richer live context (linked issue, CI, review threads). This
    is the trusted path -- only for PRs the operator trusts, and it has **no
    hard boundary**: it rests on operator trust plus the accident guard
    (post-guard, Decision 6).

  This replaces the earlier two-switch model (`watch_containment` on/off +
  `watch_sandbox` required/optional). The old `optional` value, which silently
  degraded to no-sandbox on a capable-but-unprovisioned host, is removed as a
  footgun; and `watch_containment` is gone because the session no longer hides
  credentials at all -- egress denial, not credential hiding, is the boundary.
  The default (`required`) is the strongest posture, so turning the sandbox
  off is always an explicit operator choice, never an accident. The preflight
  resolves the switch and follows the matrix in Decision 7.

  **Future work -- granular policy.** `watch_sandbox` is a **single global
  setting** today. Making the policy granular -- per repository, per PR author,
  or per team/org (e.g. "required for external contributors, off for trusted
  teammates") -- is the natural next step this global switch is a first step
  toward. It is deliberately out of scope here: the config surface, the
  resolution precedence across scopes, and the per-PR author lookup are all
  deferred.

  **Future work -- two roadmap follow-ups (not built here).** Two items,
  named in the PRD and the positioning note below, would change the posture:
  (a) **automated author / fork / `author_association` filtering** to gate
  which PRs may run uncontained (today the operator is the gate -- see
  Security Considerations); and (b) an **MCP release-to-act gate**, a
  credential-free act path that lets a **sandboxed** session post without
  lifting the boundary, which is what would promote sandboxed mode from
  limited to fully practical. Both are deferred. A **vetted, egress-free MCP
  allowlist for sandbox mode** -- letting the sandboxed session use specific
  read-only MCP servers that cannot themselves egress, widening what a no-egress
  review can see without opening a network channel -- is a further possible
  enhancement, also not built now.

  **Positioning -- sandboxed mode is usable but limited; uncontained gives
  full context.** Stated plainly: the sandboxed session appears in the
  developer's agents view and authenticates normally, so it is **usable** --
  but with no egress it cannot reach the linked issue, CI status, or review
  threads a substantive review needs, so its draft is limited to what the diff
  shows. **Uncontained (`watch_sandbox = off`) + operator-trust is the
  fuller-context path** until the two roadmap follow-ups above land --
  automated author/fork/association gating, and the MCP release-to-act gate
  that lets a sandboxed session post without lifting the boundary.
  `watch_sandbox` still defaults to `required` (safe by default); the operator
  consciously opts into `off` for trusted repos. Each run **reports the
  posture** it is operating under -- "sandboxed (OS no-egress boundary)" or
  "uncontained (trusted; no sandbox)" -- so the operator always knows which
  contract is in force.

**Closing the fail-open at the harness layer.** Whenever the OS sandbox is
enabled (`watch_sandbox = required`), the dispatched instance settings set
`sandbox.failIfUnavailable: true` and `allowUnsandboxedCommands: false`, so
Claude Code *refuses to run* rather than silently disabling the sandbox and
proceeding with a warning. This, plus the niwa-side preflight (Decision 7),
means a session runs without the OS cage only when `watch_sandbox = off` --
never by a silent degradation.

## Decision Outcome

`niwa watch --once` is a new verb (`internal/cli/watch.go`) that runs one
poll-and-dispatch pass:

1. **Preflight (fail-closed).** Resolve `watch_sandbox` and follow the
   Decision 7 matrix. When it is `required`, verify the sandbox can be
   enforced on this platform; if it cannot, refuse (print the reason, exit
   non-zero). When `off`, skip the sandbox entirely.
2. **Poll.** Resolve the login and search GitHub for open PRs with
   `user-review-requested:<login>`; intersect the results with the
   workspace's repos (Decision 4).
3. **Dedup + bound.** Drop PRs already in the handled-set; order the rest
   by PR `created_at` (oldest first -- a deterministic key available from
   the single search) and take at most the per-run bound (default 3).
4. **Per selected PR:** provision an instance (reusing dispatch's
   provisioning); when `watch_sandbox = required`, pre-fetch the PR head into
   the clone with the trusted CLI (Decision 2) and merge the OS no-egress
   sandbox profile plus the PreToolUse egress-deny hook into the instance's
   `.claude/settings.json` (Decision 1), dispatching with `--strict-mcp-config`;
   write a metadata-only prompt (Decision `prompt`); launch `claude --bg`
   detached under the developer's **real environment and daemon in every
   mode** (Decision 3); persist the PR coordinates and draft path alongside
   the session mapping; record the PR in the handled-set only on success
   (Decision 5).
5. **Report.** For each staged review, print the **posture** it runs under --
   "sandboxed (OS no-egress boundary)" or "uncontained (trusted; no sandbox)".
   On an empty result, print "nothing to stage" and exit zero. On a poll or
   dispatch failure, print the error and exit non-zero without recording the
   affected PR as handled.

Posting is always a human act (Decision 6). In every mode the session drafts
to the known location and halts; the developer posts that draft from their own
trusted session. When `watch_sandbox = required` the session has no network and
cannot post; when `off` it holds real credentials and live network to read the
surrounding context but is held back by the prompt and the post-guard. niwa has
no `post` or `discard` subcommand; a staged session is dismissed from the
Claude Code agents view.

## Solution Architecture

### Components

- **`internal/cli/watch.go` (new).** Registers `watchCmd` with `--once` (no
  subcommands) via `init()` + `rootCmd.AddCommand`. Orchestrates the pass
  above.
- **Switch resolution + `niwa setup-sandbox` (new, Decision 8).** The
  preflight reads `watch_sandbox` from niwa config (on the `flag > config
  header > default` stack; default `required`) and, when it is `required`,
  resolves the per-host backend (Seatbelt / bwrap-socat / none). `niwa
  setup-sandbox` is the opt-in privileged command that unlocks the capability
  on hardened Linux (AppArmor profile or sysctl).
- **niwa tsuku recipe (packaging, Decision 8B).** A curated `recipes/n/niwa.toml`
  (shadowing today's auto-generated download recipe) declaring
  `runtime_dependencies = ["bubblewrap", "socat"]` scoped to Linux, plus a new
  `recipes/b/bubblewrap.toml` (homebrew-bottle action). macOS pulls neither.
- **Sandbox settings (extended).** When the OS sandbox is enabled, the merged
  instance `.claude/settings.json` also sets `sandbox.failIfUnavailable: true`
  and `allowUnsandboxedCommands: false` so the harness refuses rather than
  silently disabling the sandbox.
- **Egress-deny hook settings (`required` only).** Because `sandbox.enabled`
  cages only Bash subprocesses, the merged instance `.claude/settings.json`
  also adds a **PreToolUse hook** (matcher `WebFetch|WebSearch|mcp__`) that
  denies those non-Bash egress channels, and the dispatch runs with
  `--strict-mcp-config` to limit MCP server loading. Together with the OS
  sandbox this closes every egress channel. Written only when `watch_sandbox =
  required`.
- **Filesystem-guard hook settings (`required` only).** Because `sandbox.enabled`
  restricts only Bash writes, the built-in `Write`/`Edit`/`NotebookEdit` tools
  (which run through the permission system that `bypassPermissions` skips) could
  otherwise write outside the instance. The merged instance
  `.claude/settings.json` adds a **PreToolUse hook** (matcher
  `Write|Edit|NotebookEdit`) that delegates to `niwa watch guard-fs`: it resolves
  the target path against the instance root and exits 0 (inside -> allow) or 2
  (outside -> deny), with the hook wrapper mapping any non-zero or failed-to-run
  outcome to a block (fail-closed). This is the filesystem counterpart to the
  egress-deny hook -- a hard deny, since a review-drafting agent has no legitimate
  out-of-instance write. Written only when `watch_sandbox = required`.
- **Post-guard settings (every mode).** The merged instance
  `.claude/settings.json` also adds a **PreToolUse hook** (matcher `Bash`) that
  denies `gh pr review` and `gh pr comment`, so a review/comment submission from
  inside the session is blocked. It is a hook rather than a `permissions.ask`
  rule because the session runs under `bypassPermissions`, where permission
  rules are inert but a PreToolUse hook still fires. This is written in
  **every** mode, including `watch_sandbox = off`. It is an accident-prevention
  guard for the trusted path, **not** a security boundary (command/hook-level
  act-gating is not a boundary -- egress denial is). See Decision 6.
- **`internal/github` (extended).** Two net-new client methods (the client
  has only `ListRepos`/`GetRepo` today): `CurrentLogin(ctx) (string, error)`
  wrapping `GET /user`; and `SearchReviewRequestedPRs(ctx, login) ([]PRRef,
  error)` wrapping `GET /search/issues`. No `CreateReview` method is needed --
  niwa posts nothing, and the agent posts nothing either (Decision 6): in
  every mode the agent drafts for the developer to post from their own session.
  `PRRef{Owner, Repo, Number, URL, CreatedAt}` -- `CreatedAt` is the
  PR's `created_at` from the search payload (the review-*request* time is not
  in that payload; ordering uses `created_at` as a deterministic, single-call
  proxy). Auth reuses `resolveGitHubToken` and `NewAPIClient` for the poll (in
  trusted niwa code -- the poll runs regardless of mode; it is separate from
  the credentials the dispatched session itself carries).
- **Sandbox surface on the dispatch path (net-new dispatch surface, active
  only when `watch_sandbox = required`).**
  - A sandbox-profile builder producing the settings fragment applied to
    the instance settings via the same merge helper `applier.Create` uses
    (`MergeInstanceOverrides` / the root-settings writer) -- watch is a
    *second writer* to `.claude/settings.json`, ordered Create -> watch-merge
    -> re-verify -> launch. The OS-cage stanza (`sandbox.enabled: true`,
    `sandbox.network.allowedDomains: []`, `sandbox.failIfUnavailable: true`)
    is written only when `watch_sandbox = required`, alongside the PreToolUse
    egress-deny hook (matcher `WebFetch|WebSearch|mcp__`) and the
    `--strict-mcp-config` dispatch flag that together close the non-Bash egress
    channels the OS cage does not cover. The post-guard (a PreToolUse hook on
    `Bash` denying `gh pr review`/`gh pr comment`) is written in **every** mode,
    including `off`.
  - **No env override.** The launch keeps today's `cmd.Env = os.Environ()`
    and the developer's real `HOME` in **every** mode, so the session runs
    under the real Claude daemon (appearing in the agents view) and
    authenticates. There is no allowlist and no synthetic HOME -- the earlier
    plan for an `EnvOverride` seam is dropped (Decision 3). Egress denial,
    applied by the sandbox stanza, is the boundary.
  - A trusted, hardened PR-head fetch + filter-neutered checkout (Decision 2)
    run before launch **only when `watch_sandbox = required`** (so the
    sandboxed agent, with no network, has the PR to read); when `off`, the
    agent fetches the PR itself with its inherited credentials.
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
`watch_sandbox`:

- **Sandboxed (`required`):** read the PR **from the filter-neutered local
  checkout of the pre-fetched PR head** -- the PR head diff and in-repo files
  only; with no network the session cannot reach the linked issue, CI status, or
  review threads live. Treat all of it as untrusted, write the review to the
  known draft path, and **stop before posting** (the session has no network to
  post with).
- **Uncontained (`off`):** read the PR from the host with the inherited
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
  resolve watch_sandbox (required|off)
  preflight: required + sandbox-unenforceable --> stderr + non-zero exit (refuse)
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
     if watch_sandbox required:
        fetch PR head SHA + filter-neutered checkout  <- untrusted content as data
        merge no-egress profile + egress-deny hook (WebFetch/WebSearch/MCP) into
           .claude/settings.json; re-verify; dispatch with --strict-mcp-config
        write sandboxed prompt (read PR head diff only; draft + stop before posting)
     if watch_sandbox off:
        write uncontained prompt (read full context + draft + stop before posting)
     launch claude --bg --detach under the developer's REAL env + HOME + daemon
                              + post-guard hook (Bash: gh pr review/comment)
     persist staged-review record; append handled-set (on success)
        |
  agent view (the developer's own) shows the staged session (posture reported)
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
4. **Sandbox surface.** The sandbox-profile builder + settings second-write
   via the existing merge helper + per-instance re-verification, with the
   OS-cage stanza and the PreToolUse egress-deny hook (matcher
   `WebFetch|WebSearch|mcp__`) plus the `--strict-mcp-config` dispatch flag
   applied only when `watch_sandbox = required`. The post-guard (a PreToolUse
   hook on `Bash` denying `gh pr review`/`gh pr comment`) is written in every
   mode. No env override and no synthetic HOME -- the launch keeps the real
   environment and daemon in every mode (Decision 3). Unit-test the post-guard
   and egress-deny hook entries and the merged settings document (sandbox stanza
   + egress-deny hook present under `required`, absent under `off`; post-guard
   present in both).
5. **Switch resolution + `watch --once` orchestration.** Read `watch_sandbox`
   (flag > config > default). Wire preflight -> poll -> select -> per-PR
   provision -> (required: fetch/merge/re-verify/launch | off:
   launch-ordinary), always launching under the real environment -> record.
   Fail-closed preflight (refuse only in the `required`-and-unenforceable
   cell) and fail-loud error paths.
6. **Sandbox-off path.** The uncontained variant: an ordinary dispatch (no
   sandbox stanza, no pre-fetch) with the uncontained prompt, so the agent
   reads the surrounding context and drafts. Verify it inherits the
   developer's environment, still writes a draft and halts before posting, and
   that the post-guard hook denies `gh pr review`/`gh pr comment`.
7. **Adversarial / live-enforcement test (the boundary proof, sandboxed path
   only).** A hostile-PR fixture whose title/body/diff attempt egress, push,
   and exfiltration, dispatched with `watch_sandbox = required` and the sandbox
   enforced. From inside a **real `claude --bg` sandboxed session** (bypassing
   the model), the test attempts **real outbound network on all three egress
   channels** -- a **WebFetch**, an **MCP tool** call, and a **raw Bash socket
   to a literal IP** -- and a **write outside the instance** (e.g. to `~/.ssh`),
   and asserts each fails (connection blocked / EPERM; write denied). This live
   attempt, not a settings-file assertion, is what proves empty-allowlist =
   deny-all for Bash, that the PreToolUse hook closes WebFetch/WebSearch/MCP,
   that writes are confined to the instance, and that the boundary is actually
   enforcing for the `--bg --detach` launch; it gates release. It does not apply
   to the `watch_sandbox = off` path, which is uncontained by design.

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
treated as instructions; under the sandbox the session cannot act on it
because it has no egress.

**Permission scope / network -- and the delegated dependency.** The analysis
below covers the default posture, `watch_sandbox = required` with the OS
sandbox enabled; an operator who sets `watch_sandbox = off` has explicitly
opted out of it for a trusted source. When the OS sandbox is enabled it is
applied via the instance settings merge (`sandbox.enabled`, empty
`sandbox.network.allowedDomains`, `sandbox.failIfUnavailable`) and re-verified
per instance before launch (Decision 7). But `sandbox.enabled` cages only
**Bash** subprocesses; `WebFetch`, `WebSearch`, and MCP tools make network
calls **outside** it, so the merge also adds a **PreToolUse hook** (matcher
`WebFetch|WebSearch|mcp__`) denying those channels, and the dispatch runs with
`--strict-mcp-config`. **niwa does not implement any of this; it delegates to
the Claude Code harness.** The whole boundary therefore rests on the harness
(a) treating an empty allowlist as deny-all, (b) actually creating and enforcing
the sandbox for the `--bg --detach` launch, (c) not letting a downstream
settings merge (managed settings, or the `--settings` remote-control flag `niwa
dispatch` injects) relax `sandbox.*`, and (d) firing the PreToolUse hook under
`bypassPermissions` (where `permissions.ask`/`deny` rules do not fire, but the
hook does -- verified live). These are assumptions a settings-shaped check
cannot confirm -- a silent inversion would pass every such check -- so the design
**proves enforcement live**: the release-gating adversarial test attempts real
egress on all three channels from inside the session (a WebFetch, an MCP tool
call, and a raw Bash socket to a literal IP) and requires each to fail (Decision
7, Implementation step 7; PRD AC9/AC14). Denial is thus verified to be the
sandbox-plus-hook's doing, not the model's. (The harness's own model-API
endpoint remains reachable -- that channel is what runs the session -- so "no
egress" means "no *attacker-useful* egress," not "no packets at all"; it is not
an exfiltration sink.)

**Egress is the boundary, not credential hiding.** The dispatched session runs
under the developer's **real HOME and environment** in every mode -- the `gh`
token, `~/.ssh`, `~/.config/gh`, and `~/.aws` are all present and readable.
That is deliberate: the session must run under the developer's real Claude
daemon to appear in their own `claude agents` view (R13) and to refresh its
subscription auth; a synthetic HOME broke both (Decision 3). Security does not
come from hiding those credentials -- it comes from **egress denial**, and that
denial is a **combination**, because the OS sandbox (`sandbox.enabled`) cages
only **Bash** subprocesses. `WebFetch`, `WebSearch`, and MCP tools egress
outside the sandbox, and under real HOME + `bypassPermissions` the session
carries the developer's full tool fleet -- so the OS sandbox alone would leave a
real credential-exfil hole (read the `gh` token from disk, send it via
`WebFetch` or a send-capable MCP server). The boundary for `required` mode is
therefore three parts: (1) the OS sandbox (`sandbox.enabled`, empty
`sandbox.network.allowedDomains`, `sandbox.failIfUnavailable`,
`allowUnsandboxedCommands: false`) cages all Bash egress and confines Bash
writes to the instance; (2) a **PreToolUse hook** (matcher
`WebFetch|WebSearch|mcp__`) denies the non-Bash egress channels the sandbox does
not cage -- it is a hook, not a `permissions.ask`/`deny` rule, because the
session runs under `bypassPermissions`, where permission rules are inert but a
PreToolUse hook still fires (verified live); and (3) `--strict-mcp-config` on the
sandboxed dispatch reduces MCP server loading (belt-and-suspenders alongside the
hook). Only with all three closed does the session reach **no network**, so it
can neither exfiltrate a secret nor act on the PR: posting, pushing, and merging
all need the network. Hiding the token adds nothing **only because every egress
channel is closed** -- if any were open, an extracted credential would **not** be
useless, since a token is extractable from disk and usable by any HTTP-capable
program (curl, git, python) or by `WebFetch`/MCP. That same fact is why
**command/hook-level act-gating is explicitly not a boundary on its own**: a
`permissions.ask` rule, a `gh` wrapper, and the post-guard can all be bypassed
by an agent that never invokes the gated command, so they are
accident-prevention, not containment. The OS sandbox also confines the session's
filesystem writes to the instance, so a hostile PR cannot persist or tamper
outside it (writing `~/.ssh/authorized_keys` or `~/.bashrc`) even though those
paths are readable -- the adversarial release-gate test asserts the egress denial
on all three channels and an out-of-instance write denial. Posting never happens
inside the sandboxed session because it has no network, so the developer posts
the draft from their **own trusted session** and the sandbox is never lifted
(Decision 6). When `watch_sandbox = off`, the session has both
real credentials and live network, so it can read the surrounding context
(linked issue, CI, review threads) a substantive review needs -- but it still
only drafts and waits. Posting stays a human act: the prompt tells the agent
not to post, and the post-guard -- a PreToolUse hook on `Bash` denying `gh pr
review`/`gh pr comment`, applied in both modes -- blocks any submission. That
guard is an accident-prevention convenience, **not** a boundary; the `off` path
has **no hard boundary** and rests on operator trust (only review PRs you
trust).

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
  those reviews; an operator can set `watch_sandbox = off` to stage them
  without the sandbox, at their own risk. macOS and permissive Linux need no
  elevation. This residual is a property of the OS security model, not a niwa
  limitation -- the capability is root-gated and not user-installable.
- **Windows.** The OS sandbox is unavailable on Windows; under the default
  `watch_sandbox = required` the design fails closed there (refuses to
  dispatch) rather than running a session less contained than requested.
  Windows self-hosters get no sandboxed staged reviews until a later version
  addresses it (they can opt into `watch_sandbox = off` at their own risk).
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
  *content*, while the sandbox (when on) covers the session's *actions*. This
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
- By default (`watch_sandbox = required`), the review session cannot egress on
  any channel -- the OS sandbox cages Bash, the PreToolUse hook denies
  WebFetch/WebSearch/MCP, and `--strict-mcp-config` limits MCP loading -- so a
  hostile PR is contained: it can read the developer's credentials but can
  neither exfiltrate them nor act on the PR, because acting needs the network
  the boundary denies.
- The staged review arrives as another agent in the developer's own `claude
  agents` view (it runs under the real HOME and daemon) and authenticates
  normally, so it is triaged in flow rather than hidden in a separate surface.
- Posting stays a human act in every mode instead of needing its own verb and
  scoped credential: the agent drafts and waits, so niwa never brokers a
  posting credential and there is no auto-post to reason about under injection.
- The sandbox profile lands as reusable dispatch surface a later
  contained-dispatch feature can adopt.
- The verb reuses existing provisioning and settings-merge machinery.
- The feature runs unprivileged out of the box on macOS and permissive Linux;
  the only elevation is a single opt-in `niwa setup-sandbox` on hardened
  Linux, and the single switch lets the operator -- not the code -- decide when
  to relax the sandbox for a trusted source.

### Negative / costs

- Net-new GitHub client surface (search + user) and the sandbox path make
  the niwa verb itself sizeable; the Decision-8 amendment adds a tsuku recipe,
  a `setup-sandbox` command, and the config switch, so the effort now spans
  niwa **and** a tsuku-recipe change -- it is no longer a single niwa PR.
  (Dropping the `post`/`discard` subcommands and the env-scrub/synthetic-HOME
  machinery trims some of this back.)
- The switch adds operator-facing surface (one config knob, a setup command,
  and per-host behavior to document); a wrong default here would be a security
  regression, so the default is the strongest posture (`required`) and the
  fail-open is closed at both the niwa and harness layers.
- Pre-fetching the PR head means niwa's trusted code touches untrusted repo
  content; getting this wrong (a filter-honoring checkout, LFS smudge, repo
  hooks, submodule recursion) would execute attacker code *outside* the
  sandbox. Decision 2 constrains the fetch to inert-data handling to close
  that, but it is the sharpest implementation risk and carries a dedicated
  test.
- The empty egress allowlist means the agent cannot use `gh`/network tools;
  the prompt must direct it to the local clone, and some agent conveniences
  are unavailable in-session (acceptable, and the point).
- With `watch_sandbox = required` and no egress, the session cannot reach the
  linked issue, CI status, or review threads, so a sandboxed draft is limited
  to what the diff shows -- sandboxed mode is **usable but limited**.
  Uncontained + operator-trust is the fuller-context path until the two
  roadmap follow-ups land (Decision 8 future work).

### Mitigations

- Fail-closed preflight guarantees no session launches without the sandbox
  when `watch_sandbox = required`.
- Handled-set-on-success-only prevents a transient failure from suppressing
  a review.
- The adversarial test is part of "done," so the boundary is verified by the
  exact injection surface it defends.
