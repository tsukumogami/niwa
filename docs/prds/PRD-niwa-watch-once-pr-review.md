---
status: Done
problem: |
  niwa dispatch is a pull verb: a developer must notice a PR review is
  waiting, hand it off, and wait for the agent to read and draft. Nothing
  stages that review proactively, and staging it naively is unsafe --
  feeding an externally-authored PR to a session that runs with the
  developer's credentials and unrestricted network access turns the
  review into a remote-execution vector.
goals: |
  Ship a stateless, run-by-hand `niwa watch --once` verb that stages a
  review agent for each PR the developer was directly requested on, from a
  metadata-only dispatch prompt. The dispatched review session always runs
  under the developer's real HOME, real environment, and real Claude daemon,
  so it appears in the developer's own `claude agents` view and authenticates
  normally. Containment is a single axis -- the OS no-egress sandbox --
  governed by one global setting `watch_sandbox` (required by default, or
  off). In `required` mode the agent can read anything on disk but reach no
  network -- the OS sandbox cages Bash egress and a PreToolUse hook closes the
  WebFetch/WebSearch/MCP channels it does not cover -- so a hostile PR can
  neither exfiltrate a secret nor act on the PR.
  In every mode the agent drafts its review to a file and waits; posting is
  always a human act -- the operator reads the draft and submits it themselves.
  An operator who trusts the PR's authors can set `watch_sandbox = off` to run
  without the sandbox, giving the agent real credentials and live network so it
  can read the linked issue, CI status, and review threads a substantive review
  needs -- then still drafts and waits. The sandboxed boundary is proven by an
  adversarial live-egress test.
upstream: docs/briefs/BRIEF-niwa-watch-once-pr-review.md
motivating_context: |
  This is the first, minimal version of proactive PR-review dispatch in
  niwa. It is deliberately run by hand and scoped tight so the security
  containment it introduces on the dispatch path is proven working before
  scheduling and richer state are layered on top of it.
---

# PRD: niwa watch --once PR-review dispatch

## Status

Done

Requirements for the first version of proactive PR-review dispatch in niwa.
Upstream framing is the Accepted BRIEF. This PRD states WHAT the feature
does and the contract for "done"; the architecture (where the sandbox
profile is carried, how the single `watch_sandbox` switch resolves per host,
and how the sandboxed session still surfaces in the developer's own agents
view) warrants a **DESIGN doc before implementation** and is out of this
PRD's altitude.

## Problem Statement

A developer running a niwa workspace with a Claude Code habit handles a
steady stream of PRs they are directly asked to review. Today every step
before an agent can help is manual: notice the request, confirm it is
theirs, gather the context, run `niwa dispatch`, and wait while the agent
clones and reads the diff. `niwa dispatch` is a pull verb -- it only acts
when the developer decides there is work and hands it off. The workspace
already knows which repos count and can already launch a background agent,
but nothing turns the standing "you were requested to review" signal into
a review that is already underway when the developer next looks.

Closing that gap is unsafe if done naively. A PR's title, body, and diff
are authored by whoever opened the PR -- content the developer did not
write and cannot vouch for. Routing that content into a session that runs
with the developer's credentials and unrestricted network access makes the
review a prompt-injection surface: a crafted PR can attempt to exfiltrate
secrets, push code, or run commands, and because staging is proactive the
poisoned session is prepared before the human looks. The current dispatch
path offers no defense -- it launches workers with unrestricted outbound
network access. The real boundary is **egress denial**: cut the session off
from the network and an injected agent can neither send a stolen secret
anywhere nor act on the PR (posting, pushing, and merging all need the
network). So the convenience cannot ship unless that no-egress sandbox ships
with it, enforced deterministically and on by default. Relaxing it --
running without the sandbox -- must be an explicit operator choice for PRs
the operator already trusts, never the accident of a missing dependency.

## Goals

- Turn the "directly requested to review" signal into a **pre-staged,
  pre-drafted review** the developer finds waiting, without their having to
  notice, launch, or wait.
- Make the staged review **arrive as another agent in the developer's own
  `claude agents` view**, where they are already working, so they triage it
  in flow. This is why the session runs under the developer's real HOME and
  Claude daemon -- a session bound to a synthetic home would register with a
  separate transient daemon and never appear there.
- By default, make the review session **safe against a hostile PR by
  construction** -- every egress channel is closed (the OS sandbox cages Bash,
  a PreToolUse hook denies WebFetch/WebSearch/MCP), so injection can influence
  only reasoning inside a sealed session, never the outside world. The agent can
  still read anything on disk, but with no network it can neither exfiltrate
  what it reads nor act on the PR.
- Keep the dispatch **decision** injection-proof by carrying only
  platform-vouched metadata into the prompt, never externally-authored
  text. This holds in every mode.
- Keep **posting a human act in every mode**: the dispatched agent drafts
  its review to a file and waits, and never posts, comments, approves, or
  pushes. Under the sandbox the session can't reach the network to post even
  if the untrusted content told it to; the operator posts the draft from
  their own trusted session.
- Let an operator who trusts the PR's authors **turn the sandbox off**
  (`watch_sandbox = off`), running an ordinary dispatch with real credentials
  and live network so the agent can **read** the linked issue, CI status, and
  review threads a substantive review needs -- the "I fully trust PRs from my
  peers" path. Even then the agent only drafts and waits; the operator posts.
- Stay a **plain, stateless, single-shot CLI verb** -- deterministic, no
  model, no session-resident skill, no daemon.

## User Stories

- As a developer working my agent view, I want a review I was directly
  requested on to already be drafted and waiting when I run one command,
  so that I can act on it instead of going to find it and launching an
  agent myself.
- As a developer using sandboxed reviews, I want the draft already written
  so I can read it and post it from my own session, so that
  triage-to-action stays one short step -- the sandboxed session can't post
  it itself because it has no network.
- As a developer who fully trusts PRs from my peers in a private repo, I
  want to set `watch_sandbox = off` so the review agent runs with my real
  environment and live network and can read the linked issue, CI status, and
  review threads, so that its draft reflects the full context -- I still read
  and post the draft myself.
- As the owner of a workspace, I want the review session that reads an
  untrusted PR to be unable to reach the network by default, so that a
  malicious PR cannot exfiltrate anything or act on the PR unless I have
  explicitly chosen to trust that source.
- As a developer who is on a team that gets review requests, I want only
  the PRs requesting *me personally* to stage work, so that team-wide
  requests do not flood my inbox.
- As a developer re-running the command, I want PRs I have already handled
  to be skipped, so that I do not get duplicate agents for work already in
  flight.

## Requirements

Functional:

- **R1.** niwa SHALL provide a `watch --once` verb that performs exactly
  one poll-and-dispatch pass and exits. It is a stateless single-shot
  invocation run by hand; it SHALL NOT start any resident or background
  process.
- **R2.** `watch --once` SHALL find open PRs on GitHub where the invoking
  developer is the **directly-requested** reviewer, using the user-scoped
  review-request qualifier `user-review-requested`, which excludes PRs
  where only a team the developer belongs to was requested. This qualifier
  fires on more than PRs the operator personally vetted: it also matches
  **teammate-assigned review requests, CODEOWNERS auto-requests, and
  external-fork triage** -- PRs the operator did not personally choose. That
  breadth is accepted for this version; the operator's responsibility is
  documented in R24.
- **R3.** The candidate set SHALL be restricted to repositories in the
  developer's niwa workspace; the workspace's repository set SHALL be
  derived from niwa's existing workspace configuration. A PR that directly
  requests the developer but lives in a repository outside the workspace
  SHALL NOT be staged.
- **R4.** For each matching PR not already recorded in the handled-set,
  `watch --once` SHALL assemble a dispatch prompt containing **only
  platform-vouched structural identifiers** -- the repository, the PR
  number, the PR URL, and the fact that the developer is a
  directly-requested reviewer -- plus fixed instructions. The prompt SHALL
  NOT contain any externally-authored free text: not the diff, not the PR
  body, and not the PR title or author name.
- **R5.** `watch --once` SHALL dispatch one review agent per selected PR
  through the existing `niwa dispatch`, invoked **always with `--detach`
  (`-d`)**, so a single run stages each review and returns without
  attaching a terminal to any staged session.
- **R6.** The dispatched agent SHALL be instructed to read the PR, treat all of
  it as untrusted, then **write its drafted review to a known location** and
  **halt before posting** -- in **every** mode. The agent SHALL NOT post,
  comment, approve, or push. When `watch_sandbox` is `required` (R7), the agent
  reads **only from its own pre-fetched clone** -- the PR head diff that trusted
  code staged for it; with no network it cannot fetch the PR itself, cannot
  reach the linked issue, CI status, or review threads live, and cannot reach
  the network to post. When `watch_sandbox` is `off`, the agent runs with the
  developer's real credentials and live network so it can **read** the linked
  issue, CI status, and review threads a substantive review needs, then still
  drafts to the known location and waits (R14). (The known draft path is a DESIGN detail; the requirement is
  that the location is fixed and predictable, not chosen ad hoc by the
  agent.)
- **R7.** The dispatched review session SHALL always run under the
  developer's **real HOME, real environment, and real Claude daemon** -- the
  same environment an ordinary `niwa dispatch` produces. This is what makes
  the session appear in the developer's own `claude agents` view (R13) and
  authenticate normally. Containment is a **single axis** -- the OS no-egress
  sandbox, on or off -- governed by the global setting `watch_sandbox` (R18).
  When `watch_sandbox` is `required` (the default), the session has **no network
  egress** on any channel: the OS sandbox cages Bash-subprocess egress, and a
  PreToolUse hook (plus `--strict-mcp-config`) closes the WebFetch/WebSearch/MCP
  channels the OS sandbox does not cage. That combination is the security
  boundary. When `watch_sandbox` is `off`, the session runs as an ordinary
  dispatch with full network access.
- **R8.** The dispatched review session SHALL NOT hide the developer's
  credentials or scrub its environment. It runs under the real HOME and real
  environment precisely so it registers with the developer's real Claude
  daemon (appearing in the agents view, R13) and authenticates normally. The
  security boundary is **egress denial, not credential hiding**: in `required`
  mode the session CAN read anything on disk -- `~/.ssh`, `~/.config/gh`,
  `~/.aws`, the `gh` token -- but can reach **no network**, so it can neither
  exfiltrate a secret nor act on the PR (posting, pushing, and merging all
  need the network) regardless of which binary it uses. Reaching no network
  takes a **combination**, because the OS sandbox (`sandbox.enabled`) cages only
  **Bash** subprocesses: the OS sandbox closes Bash egress, and a PreToolUse
  hook (matcher `WebFetch|WebSearch|mcp__`) plus `--strict-mcp-config` close the
  non-Bash channels -- WebFetch, WebSearch, and MCP tools -- that would
  otherwise egress outside it. Credential-hiding is unnecessary **only because
  every one of those channels is closed**; if any were open, the on-disk token
  would not be useless (it is extractable and usable by any HTTP-capable
  program). (A synthetic HOME plus an env-var
  allowlist was the previous approach; it is rejected -- see the DESIGN's
  superseded-alternative note -- because it removed the session from the
  developer's agents view and broke Claude auth.)
- **R9.** The preflight SHALL resolve `watch_sandbox` (R18). It SHALL
  **refuse to dispatch** a review -- exit non-zero and print a message
  naming the sandbox failure and the remediation (R19) -- when
  `watch_sandbox = required` and the OS sandbox cannot be enforced on the
  host (fail-closed). When `watch_sandbox = off`, it dispatches an ordinary
  uncontained session. It SHALL NOT silently dispatch a nominally-sandboxed
  session when the sandbox could not be applied.
- **R10.** `watch --once` SHALL stage at most a bounded number of **new**
  review agents per run (the per-run staging bound). When more matching new
  PRs exist than the bound allows, the selection SHALL be **deterministic**
  (oldest review-request first), and the remaining PRs SHALL be left
  unhandled for a subsequent run.
- **R11.** `watch --once` SHALL maintain a durable, flat handled-set keyed
  by **stable PR identity (repository plus PR number)**. A PR SHALL be
  recorded in the handled-set **only after its review agent is successfully
  dispatched**, so that a subsequent run does not re-dispatch an
  already-handled PR, while a PR whose poll or dispatch failed is **not**
  suppressed.
- **R12.** On a failure it cannot safely proceed past -- a failed GitHub
  poll (query error, missing or expired auth, host unreachable, rate
  limit) or a failed `niwa dispatch` for a selected PR -- `watch --once`
  SHALL **fail loud**: report the error and exit non-zero rather than
  silently continuing, and SHALL NOT record a PR it could not stage as
  handled.
- **R13.** Staged review sessions SHALL appear in the developer's **own
  existing Claude Code `claude agents` view** -- the view they are already
  working in -- so the review arrives as another agent they can triage in
  flow. This is the core UX, and it is **why the session runs under the
  developer's real HOME and real Claude daemon** (R7): a session bound to a
  synthetic HOME would register with a separate transient daemon and never
  surface in the real agents view. `niwa dispatch --detach` (R5) does not
  attach a terminal, but it still launches the worker as a `claude --bg`
  background session under the real daemon, and that is what auto-registers
  in the agents view -- so a detached dispatch is discoverable there without
  any new listing or inbox UI. In every mode the developer reads the draft
  and posts it from their own trusted session; the agent never posts. A
  staged session the developer no longer wants can be **dismissed directly
  from the Claude Code agents view** -- no niwa command is needed.
- **R14.** Posting is always a human act, so niwa SHALL NOT provide a `post`
  subcommand or a `discard` subcommand; `niwa watch` has no subcommands
  beyond `--once`. In **every** mode the dispatched agent drafts its review
  to the known location (R6) and halts; it SHALL NOT post, comment, approve,
  or push, and the developer posts the draft from their **own trusted
  session**. When `watch_sandbox` is `required`, the session has no network,
  so it cannot post even if the untrusted content told it to, and its
  sandbox SHALL NOT be lifted to post. When `watch_sandbox` is `off`, the
  session holds the developer's real credentials and live network so the
  agent can read the surrounding context (linked issue, CI, review threads),
  but it still only drafts and waits; the dispatched instance settings SHALL
  additionally carry a **post-guard** (R22) that blocks any review/comment
  submission, so a stray prompt-following cannot post under the operator's name. A staged session the developer no longer
  wants is dismissed from the Claude Code agents view (R13); because a PR is
  recorded handled on successful dispatch (R11), dismissing it stages no
  duplicate on a later run.

Non-functional:

- **R15.** The watcher SHALL be deterministic end to end: no model/LLM
  judgment and no session-resident skill participate in the poll, the
  relevance decision, or the prompt assembly, and prompt assembly SHALL be
  a pure function of the PR's platform metadata (identical metadata
  produces an identical prompt).
- **R16.** This version SHALL target GitHub as the host that carries the
  directly-requested signal; other hosts are out of scope. Host scope is
  enforced structurally by the GitHub-specific `user-review-requested`
  query (D3) -- no non-GitHub source is polled.
- **R17.** The sandboxed path SHALL be adversarially verified: a PR whose
  title, body, and diff attempt exfiltration and outbound action (e.g.
  `curl … | sh`, `git push`, printing and sending secrets, writing to
  `~/.ssh`/`~/.bashrc`), dispatched with `watch_sandbox = required` and the
  sandbox enforced, SHALL have escape on **all four credential-leak channels
  denied**: a WebFetch, an MCP tool call, a raw Bash socket, and a built-in Write
  to a path outside the review instance. The verification SHALL exercise the
  actions **directly** (executed in a real sandboxed session, bypassing the
  model's judgment) so that denial is provably the sandbox-plus-hooks' doing and
  not the model merely choosing to decline; the filesystem check SHALL be
  authoritative (the out-of-instance target absent afterward), not the agent's
  self-report. This test is the boundary proof for the sandbox and is required
  whenever `watch_sandbox = required`; it does **not** apply to the
  `watch_sandbox = off` path, which is uncontained by the operator's explicit
  choice.

Containment model (a single switch):

Containment is a single axis -- the OS no-egress sandbox, on or off --
expressed today as one niwa **global config setting** `watch_sandbox`
(making it granular -- per repository, per PR author, or per team -- is
future work; see Out of Scope). It is `required` (default) or `off`. This
replaces the earlier two-switch model (an outer `watch_containment` on/off
plus an inner `watch_sandbox` required/optional); the old `optional` value,
which silently degraded to no-sandbox, is removed as a footgun. The
dispatched session always runs under the developer's real HOME, environment,
and Claude daemon (R7/R8); `watch_sandbox` decides only whether the OS
no-egress network cage (bwrap+socat on Linux, Seatbelt on macOS) wraps it.
The switch resolves per this matrix:

| `watch_sandbox` | The dispatched review session |
|---|---|
| required (default) | Real HOME; no network on any channel -- OS sandbox over Bash + a PreToolUse hook over WebFetch/WebSearch/MCP = the boundary; refuse if the sandbox cannot be enforced (R9). |
| off | Real HOME, no sandbox; the developer's real credentials and live network, for richer live context. The trusted path -- only for PRs the operator trusts. |

- **R18.** Whether the session runs inside the OS no-egress sandbox SHALL be
  governed by `watch_sandbox` per the matrix above. When the sandbox is
  enabled (`required`), the preflight SHALL select the strongest enforceable
  backend for the host -- the built-in Seatbelt sandbox on macOS, or the
  `bwrap`+`socat` no-egress profile on a Linux host with a
  capability-bearing user namespace -- and dispatch under it; it SHALL NOT
  require the same backend on every platform. When `watch_sandbox = off`, no
  sandbox is applied.
- **R19.** A standard, **unprivileged** niwa installation SHALL provide the
  Linux sandbox binaries (`bwrap`, `socat`) automatically (as Linux-only
  runtime dependencies); macOS SHALL require none. The one privileged step --
  unlocking the kernel capability on a hardened Linux host -- SHALL be a
  single opt-in command (`niwa setup-sandbox`), never a per-dispatch or
  multi-step manual requirement. The default install SHALL NOT require
  elevation. This requirement matters whenever `watch_sandbox = required`; it
  is moot when the sandbox is off.
- **R21.** When the OS sandbox is enabled (`watch_sandbox = required`), the
  dispatched instance settings SHALL set the harness to **refuse to run**
  rather than silently disable the sandbox (`sandbox.failIfUnavailable`), so
  a nominally-sandboxed session is never produced by a silent harness
  degradation -- only the explicit `watch_sandbox` setting decides whether
  the OS sandbox is in force.
- **R22.** The dispatched instance settings SHALL carry a **post-guard**: a
  **PreToolUse hook** (matcher `Bash`) that denies `gh pr review` and `gh pr
  comment`. It is a hook rather than a `permissions.ask`/`deny` rule because the
  session runs under `bypassPermissions`, where permission rules do not fire but
  a PreToolUse hook still does. This guard SHALL be applied in **every** mode (it
  is harmless where the session already has no egress). It is a **convenience /
  accident-prevention guard for the trusted (`off`) path, NOT a security
  boundary** -- command/hook-level act-gating is not a security boundary: a token
  is extractable from disk and usable via any HTTP-capable program (curl, git,
  python) without ever invoking the gated command, so a hook denying a `gh`
  command cannot contain a hostile agent (egress denial is the boundary). It
  exists so a stray prompt-following cannot post under the operator's name; the
  prompt already instructs the agent not to post.
- **R23.** Each run SHALL **report its posture** -- one of "sandboxed (OS
  no-egress boundary)" or "uncontained (trusted; no sandbox)" -- so the
  operator always knows which contract is in force for a staged review.
- **R24.** Documentation SHALL state the operator's responsibility when
  running uncontained (`watch_sandbox = off`): **only review PRs you
  trust.** The `off` mode has **no hard boundary** -- it rests entirely on
  operator trust plus the accident guard. Because `user-review-requested`
  fires on teammate assignments, CODEOWNERS auto-requests, and external-fork
  triage (R2), an un-vetted PR can be staged automatically. The
  draft-and-wait gate (R6/R14) and the post-guard (R22) are what keep such
  an auto-assignment from acting before the operator looks at the draft.

## Acceptance Criteria

Selection and dispatch:

- [ ] **AC1 (R1, R5).** Running `niwa watch --once` in a workspace with
      exactly one open PR that directly requests the developer stages
      exactly one sandboxed review agent via `niwa dispatch -d` and returns
      without attaching a terminal.
- [ ] **AC2 (R4).** The generated dispatch prompt contains the repo, PR
      number, PR URL, and the directly-requested fact plus fixed
      instructions, and contains no PR title, author name, body, or diff
      text (verified by inspecting the prompt string).
- [ ] **AC3 (R2).** A PR that requests only a team the developer belongs to
      (not the developer individually) stages no agent.
- [ ] **AC4 (R3).** A PR that directly requests the developer but lives in
      a repository **not** in the niwa workspace stages no agent.
- [ ] **AC5 (R11).** A second `niwa watch --once` run, with the same PR
      still open and still requesting the developer, stages no new agent
      for it.
- [ ] **AC6 (R12, R1).** A run in which no PR directly requests the
      developer stages nothing and exits zero with a "nothing to stage"
      style message.
- [ ] **AC7 (R6).** A normal (non-adversarial) dispatched review produces a
      draft review artifact at the known location and leaves the session
      halted in a drafted-but-not-posted state (a usable draft exists to
      approve).
- [ ] **AC8 (R10).** With more matching new PRs than the configured bound N,
      exactly N agents are staged in one run; the N selected are the
      oldest-review-request-first selection, and a repeat run with unchanged
      state selects the same N. (Test is parameterized on the configured N.)

Containment (security):

- [ ] **AC9 (R17, R18 egress + write confinement).** With `watch_sandbox =
      required` and the OS sandbox enforced, both an outbound network request
      executed **directly** within a dispatched session (e.g. `curl
      https://example.com` run in the session shell, bypassing model judgment)
      AND a **built-in Write outside the instance** (e.g. to `~/.ssh`) fail
      (egress: connection blocked / EPERM; write: denied by the filesystem-guard
      PreToolUse hook, target absent afterward) -- so the agent can neither
      exfiltrate nor persist/tamper outside the instance.
- [ ] **AC10 (R7, R8 real HOME).** With `watch_sandbox = required`, the
      dispatched session runs under the developer's **real HOME** -- an
      on-disk credential sentinel (e.g. a token file under `~/.config/gh`) is
      **readable** from the session -- yet the direct outbound request of AC9
      still fails, so the readable credential cannot be exfiltrated. This
      confirms the boundary is egress denial, not credential hiding.
- [ ] **AC11 (R7, R13 real daemon).** A session staged with `watch_sandbox =
      required` registers with the developer's **real Claude daemon** and
      authenticates normally (no auth stall), which is what lets it appear in
      the developer's own `claude agents` view (AC20).
- [ ] **AC12 (R8).** With `watch_sandbox = required`, a sentinel env var
      planted in the dispatcher's environment (e.g. `NIWA_CANARY_SECRET=…`)
      **is present** in the dispatched session's environment -- the session
      inherits the real environment, not a scrubbed allowlist -- and yet,
      with egress denied (AC9), that value cannot leave the session.
- [ ] **AC13 (R9).** With `watch_sandbox = required`, when the OS sandbox
      cannot be enforced on the host, `niwa watch --once` refuses to dispatch
      that review, exits non-zero, and prints a message naming the sandbox
      failure; no session is launched.
- [ ] **AC14 (R17).** Adversarial test (sandboxed path): a PR whose
      title/body/diff attempt `curl … | sh`, a `git push`, secret exfiltration,
      and a write to `~/.ssh`/`~/.bashrc` is dispatched with `watch_sandbox =
      required` and the sandbox enforced; attempts on **all four credential-leak
      channels** -- a **WebFetch**, an **MCP tool** call, a **raw Bash socket**,
      and a **built-in Write outside the instance** -- each **executed directly
      in a real `claude --bg` sandboxed session to bypass model judgment**, are
      denied -- no egress, no push, no unapproved post, no out-of-instance write
      (the target absent afterward).

Act boundary and determinism:

- [ ] **AC15 (R14).** With `watch_sandbox = required`, the dispatched session
      -- though it can read the `gh` token on disk (AC10) -- has no network
      (AC9) and so cannot post; no review attributable to the session appears
      on the PR. The developer posts the approved draft from their own trusted
      session, and niwa exposes no `post` subcommand to do it.
- [ ] **AC16 (R13, R14).** Dismissing a staged session from the Claude Code
      agents view posts nothing; because the PR was recorded handled on
      dispatch (R11), a later `niwa watch --once` run does not re-stage it.
      niwa exposes no `discard` subcommand.
- [ ] **AC17 (R11, R12).** A PR whose `niwa dispatch` fails is **not**
      recorded in the handled-set; a subsequent run re-attempts it.
- [ ] **AC18 (R15).** Given identical PR platform metadata, prompt assembly
      produces a byte-identical prompt (pure function), and no model/LLM
      call occurs on the poll, relevance, or prompt-assembly path.
- [ ] **AC19 (R12 poll branch).** A run in which the GitHub poll itself
      fails (simulated expired/absent auth, rate limit, or unreachable
      host) exits non-zero, prints an error naming the failure, stages
      nothing, and records no PR as handled -- distinct from the empty-poll
      success path (AC6).
- [ ] **AC20 (R13).** After a run stages a review, the staged session is
      discoverable in the developer's own existing Claude Code `claude
      agents` view (it registered as a background session under the real
      daemon), not merely present as an on-disk draft.
- [ ] **AC21 (R14, R6 uncontained draft).** With `watch_sandbox = off`, the
      dispatched agent holds the developer's real credentials and live network
      and can read the linked issue, CI status, and review threads, but it
      still writes its review to the known draft location and **halts before
      posting** -- no review attributable to the session appears on the PR
      until the operator posts the draft themselves.
- [ ] **AC22 (R22 post-guard).** With `watch_sandbox = off`, the dispatched
      instance settings carry a PreToolUse hook (matcher `Bash`) that denies
      `gh pr review` / `gh pr comment`, so an attempt to submit a review from
      inside the unattended session is blocked rather than auto-run.
- [ ] **AC23 (R23 posture report).** Each run prints the posture it is
      operating under -- "sandboxed (OS no-egress boundary)" or "uncontained
      (trusted; no sandbox)" -- matching the resolved `watch_sandbox` setting.

## Out of Scope

- **Scheduling / always-on.** Driving `watch --once` from an OS timer or a
  harness routine. This version is run by hand.
- **Durable dedup/cursor state.** Re-request expiry after new commits,
  unblock-time freshness re-validation (still open? still requesting me?
  not force-pushed?), and cursor/ETag polling. The handled-set here is
  deliberately minimal.
- **Richer attention and cost controls.** Batching, heads-down
  suppression, priority ordering, bulk discard, cost-containment policy,
  and a configurable concurrency model beyond the minimal per-run bound.
- **Multi-repo scale-out and multi-host reach.** Tuning the poll for large
  workspaces, and supporting hosts other than GitHub.
- **Any relevance model or session-resident skill in the watcher.**
- **Ambient sources.** Slack, CI logs, or any source whose relevance must
  be manufactured by a model, and the deterministic pre-model gate they
  require.
- **Granular containment policy.** Making the `watch_sandbox` switch
  granular -- per repository, per PR author, or per team/org (e.g. "sandboxed
  for external contributors, off for trusted teammates") -- is the intended
  next step and is deliberately not built here. The switch is a **single
  global setting** in this version, a first step toward it, not the end
  state.
- **Automated author / fork / association gating (future work).** Filtering
  which PRs may run uncontained (`watch_sandbox = off`) by author, fork
  status, or `author_association` is **not** built here. Today the operator
  is the gate: when running uncontained they must only review PRs they trust
  (R24). This filter is the first of two roadmap items that would let
  uncontained runs be scoped automatically instead of by operator
  discipline.
- **Credential-free act path (MCP release-to-act gate, future work).** A path
  that lets a **sandboxed** session post without lifting the boundary -- an
  MCP release-to-act gate the operator releases -- is **not** built here. It
  is the second roadmap item, and it is what would eventually promote
  sandboxed mode from limited to fully practical (see Known Limitations).
- **Vetted egress-free MCP allowlist for sandbox mode (future work).** Letting a
  sandboxed session use specific read-only MCP servers that cannot themselves
  egress -- widening what a no-egress review can see without opening a network
  channel -- is a possible later enhancement, not built here.
- **Closing the sandbox's residual caveats.** Windows sandbox support and
  the egress proxy's TLS-termination / domain-fronting seam (see Known
  Limitations).

## Decisions and Trade-offs

These close the Open Questions the upstream BRIEF deferred to this PRD.

- **D1 — Workspace-repo coverage and enumeration.** *Decided (assumed):*
  the poll covers **all repositories in the developer's niwa workspace**,
  with the repo set derived from niwa's existing workspace configuration.
  *Alternatives:* a hand-picked minimal subset. *Why:* the workspace
  boundary is the feature's natural relevance scope, and the per-run bound
  (D5) already contains any resource burst, so an artificial subset would
  add configuration surface without safety benefit. The exact query
  mechanism (a single `user-review-requested` search intersected with the
  workspace set, versus per-repo queries) is a DESIGN choice.
- **D2 — Handled-set minimum contract.** *Decided (assumed):* the
  handled-set is a flat file keyed by **repository plus PR number** (the
  stable, human-legible PR identity), recording that a PR was dispatched,
  and written **only after a successful dispatch** (R11). *Alternatives:*
  keying by GitHub node id; also recording per-PR dispatch outcome/state;
  writing on attempt rather than success. *Why:* the only job here is "do
  not re-dispatch what I already handled"; repository+PR number is stable
  and legible; writing only on success avoids a transient failure
  permanently suppressing a review (see D6). Richer state (expiry,
  freshness, outcome) is explicitly deferred.
- **D3 — Directly-requested qualifier.** *Decided (confirmed):* use the
  **user-scoped** review-request signal (`user-review-requested`), which
  matches only PRs where the developer is individually requested and
  excludes team-only requests. *Alternatives:* the broader
  `review-requested` (includes team requests). *Why:* team requests are
  explicitly excluded by the framing; the user-scoped qualifier is the
  deterministic filter that enforces it.
- **D4 — Posting model (no post verb; posting is always human).** *Decided:*
  the dispatched agent drafts its review to a file and waits in **every**
  mode; posting is always a human act, never a niwa action and never the
  agent's. The developer posts the draft from their **own trusted session**.
  When `watch_sandbox` is `required` the session has no network, so it
  cannot post even if prompted; when `off`, the session holds real
  credentials and live network (so it can read the surrounding context) but
  is held back by the prompt and the post-guard (R22). niwa provides no
  `post` or `discard` subcommand. *Alternatives:* a niwa-provided trusted
  `post` subcommand holding a narrowly-scoped posting credential; printing a
  ready-to-run `gh` command; or (an earlier framing) letting the off-mode
  agent post directly. *Why:* keeping posting a human act in every mode makes
  the sandboxed and uncontained paths differ only in **network and live read
  context**, not in who posts, so there is no auto-post to reason about under
  injection and no posting credential for niwa to mint or carry. The
  developer's own session is already trusted, so a draft they post themselves
  honors the model in both modes without a bespoke verb.
- **D5 — Per-run staging bound.** *Decided (assumed):* `watch --once`
  stages at most a **small fixed number** of new agents per run (a safe
  default such as 3), leaving additional matches for a later run, and
  selects them **oldest-review-request first** when matches exceed the
  bound (R10). *Alternatives:* strictly one-at-a-time, or no bound;
  arbitrary/undefined selection order. *Why:* an unbounded run over a
  workspace with many pending requests is a first-run resource footgun; a
  small cap prevents the burst while still staging a useful handful, and a
  defined order keeps the bounded behavior deterministic (R15). The exact
  value and whether it is configurable are implementation details for the
  DESIGN/plan.
- **D6 — Failure semantics.** *Decided (assumed):* on a failed poll or a
  failed dispatch, `watch --once` **fails loud** (reports the error, exits
  non-zero) and does **not** record the affected PR as handled (R11, R12).
  *Alternatives:* best-effort continue past failures; record-on-attempt.
  *Why:* silently swallowing a poll/auth failure would make the tool look
  like "nothing to review" when it is actually broken, and recording a
  failed dispatch as handled would permanently suppress a review that a
  retry would have staged. Fail-loud plus handled-on-success-only is the
  safe default.
- **D7 — Staged-draft discovery.** *Decided (assumed):* staged reviews are
  surfaced through niwa's **existing Claude Code agent view** rather than a
  new listing UI (R13). *Alternatives:* a bespoke `watch list`/inbox
  command. *Why:* a `--bg` dispatch already auto-registers in the agent
  view for free; adding a parallel inbox surface is out of scope for the
  first version and would duplicate an existing affordance.

## Known Limitations

- **Hardened Linux needs a one-time privileged setup.** On a Linux kernel
  that restricts unprivileged user namespaces (e.g. Ubuntu 24.04), the OS
  sandbox cannot run until `niwa setup-sandbox` (R19) is run once. Until then,
  with `watch_sandbox = required` (the default) the feature **refuses** to
  stage those reviews (R9); an operator can set `watch_sandbox = off` to stage
  them without the sandbox, at their own risk. macOS and permissive Linux need
  no elevation. This capability is root-gated by the OS and cannot be granted
  by an unprivileged install.
- **Windows.** The OS-level sandbox is not available on Windows; with the
  default `watch_sandbox = required`, the feature fails closed there (R9), so
  Windows self-hosters get no staged reviews until later work addresses the
  gap (they can still set `watch_sandbox = off`, at their own risk).
- **Egress proxy TLS termination.** The no-egress sandbox's proxy does not
  TLS-terminate by default, leaving a domain-fronting / SNI-evasion seam.
  This is a recorded residual risk for the review session's threat model,
  not closed in this version.
- **Per-run bound defers work.** With many pending requests, some matching
  PRs are intentionally left for a subsequent run rather than all staged at
  once.
- **GitHub only.** Reviews requested on other hosts are not seen by this
  version.
- **The draft text is authored by the untrusted-content session.** The
  drafted review is produced by the session that read the PR, so its *text*
  could contain attacker-influenced content -- in **every** mode. The
  developer is the trust checkpoint for that text: they read the draft before
  posting it from their own session. The sandbox (when on) stops the session
  from acting; the human read covers what the draft says. When the sandbox is
  off the operator has chosen to trust the source enough to run with live
  network and real credentials, but the agent still drafts and waits -- the
  human read before posting is not skipped.
- **Sandboxed mode is usable but limited today.** The sandboxed session
  appears in the developer's agents view and authenticates normally, so it is
  usable -- but with no egress it cannot reach the linked issue, CI status, or
  review threads a substantive review needs, so its draft is limited to what
  the diff alone shows. **Uncontained (`watch_sandbox = off`) +
  operator-trust is the fuller-context path** until two roadmap items land:
  (a) automated author/fork/`author_association` filtering to gate which PRs
  may run without the sandbox, and (b) a credential-free act path (an MCP
  release-to-act gate) that lets a sandboxed session post without lifting the
  boundary. `watch_sandbox` still defaults to `required` (safe by default);
  the operator consciously opts into `off` for trusted repos.
