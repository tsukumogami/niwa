---
status: In Progress
problem: |
  niwa dispatch is a pull verb: a developer must notice a PR review is
  waiting, hand it off, and wait for the agent to read and draft. Nothing
  stages that review proactively, and staging it naively is unsafe --
  feeding an externally-authored PR to a session that runs with the
  developer's credentials and unrestricted network access turns the
  review into a remote-execution vector.
goals: |
  Ship a stateless, run-by-hand `niwa watch --once` verb that stages a
  contained review agent for each PR the developer was directly requested
  on, from a metadata-only dispatch prompt, and lets the developer post or
  discard the drafted review with a single trusted gesture -- with the
  review session contained (no egress, scrubbed credentials) so a hostile
  PR cannot exfiltrate or act, proven by an adversarial test.
upstream: docs/briefs/BRIEF-niwa-watch-once-pr-review.md
motivating_context: |
  This is the first, minimal version of proactive PR-review dispatch in
  niwa. It is deliberately run by hand and scoped tight so the security
  containment it introduces on the dispatch path is proven working before
  scheduling and richer state are layered on top of it.
---

# PRD: niwa watch --once PR-review dispatch

## Status

In Progress

Requirements for the first version of proactive PR-review dispatch in niwa.
Upstream framing is the Accepted BRIEF. This PRD states WHAT the feature
does and the contract for "done"; the architecture (where the containment
profile is carried, the environment-scrub mechanism, how the posting
credential is provisioned) warrants a **DESIGN doc before implementation**
and is out of this PRD's altitude.

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
path offers no defense -- it launches workers carrying the dispatcher's
full environment and with no restriction on outbound network access. The
convenience cannot ship unless the containment that makes an
untrusted-content review safe ships with it, enforced deterministically.

## Goals

- Turn the "directly requested to review" signal into a **pre-staged,
  contained, pre-drafted review** the developer finds waiting, without
  their having to notice, launch, or wait.
- Make the review session **safe against a hostile PR by construction** --
  no network egress and no inherited secrets -- so injection can influence
  only reasoning inside a sealed session, never the outside world.
- Keep the dispatch **decision** injection-proof by carrying only
  platform-vouched metadata into the prompt, never externally-authored
  text.
- Let the developer **act** on a staged review (post or discard) in one
  trusted gesture, without ever lifting the review session's containment.
- Stay a **plain, stateless, single-shot CLI verb** -- deterministic, no
  model, no session-resident skill, no daemon.

## User Stories

- As a developer working my agent view, I want a review I was directly
  requested on to already be drafted and waiting when I run one command,
  so that I can act on it instead of going to find it and launching an
  agent myself.
- As a developer, I want to post an approved review with a single gesture
  and discard an unwanted one just as easily, so that triage-to-action is
  one step.
- As the owner of a workspace, I want the review session that reads an
  untrusted PR to be unable to reach the network or act with my
  credentials, so that a malicious PR cannot turn my convenience into a
  breach.
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
  where only a team the developer belongs to was requested.
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
- **R6.** The dispatched agent SHALL be instructed to read the PR (title,
  body, diff, linked issue, CI status) **in its own clone** and to treat
  all of it as untrusted, then write its drafted review to a **known
  location** the invoking developer and the trusted post step can both
  find, and halt before posting. (The exact path is a DESIGN detail; the
  requirement is that the location is fixed and predictable, not chosen ad
  hoc by the agent.)
- **R7.** The dispatched review session SHALL run under enforced
  containment with three properties: **no network egress**, filesystem
  writes **scoped to its clone**, and a **fail-closed permission mode** (an
  action that would otherwise prompt for approval is denied, not
  auto-allowed, in the unattended session).
- **R8.** The dispatched review session SHALL carry only an **explicit,
  minimal allowlist** of environment variables -- the set the read-only
  review task needs. No other variable from the dispatcher's environment
  SHALL be present in the session; in particular, secrets the review task
  does not need SHALL be absent. (The exact allowlist contents are a DESIGN
  detail; the requirement is that the mechanism is allowlist-based, not a
  best-effort scrub of a denylist.)
- **R9.** If the enforced containment (R7 and R8) cannot be applied to the
  dispatched instance -- the OS sandbox is absent or unsupported for any
  reason -- `watch --once` SHALL **refuse to dispatch** that review, exit
  non-zero, and print a message naming the containment failure, rather than
  dispatching it uncontained.
- **R10.** `watch --once` SHALL stage at most a bounded number of **new**
  review agents per run (the per-run staging bound). When more matching new
  PRs exist than the bound allows, the selection SHALL be **deterministic**
  (oldest review-request first), and the remaining PRs SHALL be left
  unhandled for a subsequent run.
- **R11.** `watch --once` SHALL maintain a durable, flat handled-set keyed
  by **stable PR identity (repository plus PR number)**. A PR SHALL be
  recorded in the handled-set **only after its review agent is successfully
  dispatched under enforced containment**, so that a subsequent run does
  not re-dispatch an already-handled PR, while a PR whose poll or dispatch
  failed is **not** suppressed.
- **R12.** On a failure it cannot safely proceed past -- a failed GitHub
  poll (query error, missing or expired auth, host unreachable, rate
  limit) or a failed `niwa dispatch` for a selected PR -- `watch --once`
  SHALL **fail loud**: report the error and exit non-zero rather than
  silently continuing, and SHALL NOT record a PR it could not stage as
  handled.
- **R13.** Staged review sessions SHALL be surfaced through the developer's
  **existing Claude Code agent view**. `niwa dispatch --detach` (R5) does
  not attach a terminal, but it still launches the worker as a `claude
  --bg` background session, and that background session is what
  auto-registers in the agent view -- so a detached dispatch is
  discoverable there without any new listing or inbox UI. The developer
  reads the draft and invokes post or discard from that surface.
- **R14.** Posting an approved review SHALL be performed by a **separate
  trusted action** that runs **outside** the contained review session,
  operates on the draft the developer approved, and holds a credential
  scoped to nothing beyond posting that review. The review session's
  containment SHALL NOT be lifted to post, and the posting credential
  SHALL NOT enter the contained session's environment. Discarding a staged
  review SHALL post nothing and SHALL record the PR as handled.

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
- **R17.** The feature SHALL be adversarially verified: a PR whose title,
  body, and diff attempt exfiltration and outbound action (e.g.
  `curl … | sh`, `git push`, printing and sending secrets) SHALL have
  those outbound actions **denied at the tool/OS layer**. The verification
  SHALL exercise the outbound actions **directly** (executed in the
  session, bypassing the model's judgment) so that denial is provably the
  sandbox's doing and not the model merely choosing to decline.

## Acceptance Criteria

Selection and dispatch:

- [ ] **AC1 (R1, R5).** Running `niwa watch --once` in a workspace with
      exactly one open PR that directly requests the developer stages
      exactly one contained review agent via `niwa dispatch -d` and returns
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

- [ ] **AC9 (R7 egress).** An outbound network request executed **directly**
      within a dispatched session (e.g. `curl https://example.com` run in
      the session shell, bypassing model judgment) fails at the OS/tool
      layer (connection blocked / EPERM / proxy refusal).
- [ ] **AC10 (R7 writes).** A filesystem write executed directly within the
      session to a path outside its clone is denied.
- [ ] **AC11 (R7 fail-closed).** A tool action that Claude Code would
      normally gate behind an approval prompt (for example, a command not
      on any allow-list), attempted in the unattended session, is **denied**
      rather than auto-approved -- confirming the session runs in a
      fail-closed mode, not an auto-allow mode.
- [ ] **AC12 (R8).** A sentinel secret planted in the dispatcher's
      environment (e.g. `NIWA_CANARY_SECRET=…`) is **absent** from the
      dispatched session's environment, and the session's environment is a
      subset of the defined allowlist.
- [ ] **AC13 (R9).** When the enforced containment cannot be applied,
      `niwa watch --once` refuses to dispatch, exits non-zero, and prints a
      message naming the containment failure; no uncontained review session
      is launched.
- [ ] **AC14 (R17).** Adversarial test: a PR whose title/body/diff attempt
      `curl … | sh`, a `git push`, and secret exfiltration is dispatched
      under the profile; each outbound action, **executed directly in the
      session to bypass model judgment**, is denied at the OS/tool layer --
      no egress, no push, no unapproved post.

Act boundary and determinism:

- [ ] **AC15 (R14).** Approving a staged review posts it through the trusted
      action running outside the contained session. The dispatched session
      holds no posting-scoped credential (AC12 baseline) and, with egress
      denied (AC9), cannot post; no review attributable to the session
      appears on the PR between dispatch and explicit approval.
- [ ] **AC16 (R14).** Discarding a staged review posts nothing and records
      the PR as handled (a later run does not re-stage it).
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
      discoverable in the existing Claude Code agent view (it registered as
      a background session), not merely present as an on-disk draft.
- [ ] **AC21 (R14 post success).** Invoking the trusted post action outside
      the contained session on a developer-approved draft successfully
      posts that review to the PR on the host.

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
- **Un-caging the review agent to post.** Lifting the drafting session's
  containment on unblock, or handing it a posting credential, so the same
  agent that read the PR can post. This is a rejected alternative, not
  deferred work.
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
  and written **only after a successful contained dispatch** (R11).
  *Alternatives:* keying by GitHub node id; also recording per-PR dispatch
  outcome/state; writing on attempt rather than success. *Why:* the only
  job here is "do not re-dispatch what I already handled"; repository+PR
  number is stable and legible; writing only on success avoids a transient
  failure permanently suppressing a review (see D6). Richer state (expiry,
  freshness, outcome) is explicitly deferred.
- **D3 — Directly-requested qualifier.** *Decided (confirmed):* use the
  **user-scoped** review-request signal (`user-review-requested`), which
  matches only PRs where the developer is individually requested and
  excludes team-only requests. *Alternatives:* the broader
  `review-requested` (includes team requests). *Why:* team requests are
  explicitly excluded by the framing; the user-scoped qualifier is the
  deterministic filter that enforces it.
- **D4 — Trusted post step.** *Decided (assumed):* posting is a
  **niwa-provided trusted action** the developer invokes to post the
  approved draft; it runs outside the contained session with a credential
  scoped to posting only. *Alternatives:* the developer posts manually
  through GitHub. *Why:* a trusted one-gesture post keeps the
  triage-to-action loop real while honoring the containment invariant. The
  exact affordance (a niwa subcommand versus a printed ready-to-run
  command versus another host-side gesture), where the trusted step runs,
  and how its narrowly-scoped credential is provisioned and kept out of the
  contained environment are **DESIGN** decisions.
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

- **Windows and the sandbox.** The OS-level sandbox that enforces
  no-egress is not available on Windows. Per R9 the feature fails closed
  there (it refuses to dispatch rather than dispatching uncontained), so
  Windows self-hosters get no staged reviews until later work addresses
  the gap.
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
  drafted review is produced by the contained session that read the
  hostile PR, so its *text* could contain attacker-influenced content. The
  approving developer is the trust checkpoint for that text: they read the
  draft before the trusted step posts it. Containment stops the session
  from acting; the human gate covers what the draft says. Automatic posting
  without that human read is deliberately not offered.
