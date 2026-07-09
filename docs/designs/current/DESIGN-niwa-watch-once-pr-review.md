---
status: Proposed
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

Proposed

Technical design for the first version of proactive PR-review dispatch in
niwa, implementing the Accepted PRD. Scoped to a single-PR change in the
niwa repo.

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
  (`GIT_LFS_SKIP_SMUDGE=1` and no `filter.lfs` invocation), submodule
  recursion off, `protocol.ext`/`protocol.file` disabled, and an isolated
  gitconfig (`GIT_CONFIG_NOSYSTEM=1`, `HOME` pointed away from the
  developer's gitconfig for the fetch); and expose the tree to the session
  by reading blobs by SHA / using a bare-style object store rather than a
  working-tree checkout that honors filters. The agent reads content but
  no checkout-time program runs.
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
  the very credential the env scrub removed. So the contained session runs
  with `HOME` set to a scratch directory inside its instance (no developer
  dotfiles), `SSH_AUTH_SOCK` and `GH_*`/`GITHUB_*` excluded, and the OS
  sandbox's filesystem policy denying reads outside the clone/instance in
  any case (R7 write-scoping generalized to reads of credential paths). The
  canary-absence test (PRD AC12) covers `GITHUB_TOKEN`, `GH_TOKEN`,
  `SSH_AUTH_SOCK`, and a planted `~/.netrc`/`~/.config/gh` sentinel, not
  just one env var.
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

- **Option 7A (chosen): preflight the containment, then re-verify per
  instance immediately before launch.** Two checkpoints, both fail-closed:
  - *Preflight (before any instance is created):* refuse on an unsupported
    platform (`GOOS == "windows"`) **and** actively probe that the OS
    sandbox can be created now -- not merely that the OS is nominally
    supported -- so a missing/broken sandbox binary or a kernel that denies
    the sandbox surfaces as a refusal rather than an uncontained launch.
  - *Post-merge re-verification (per instance, immediately before `claude
    --bg`):* because the containment lives in a *merged*
    `.claude/settings.json`, niwa re-reads the final merged document for
    that specific instance and asserts the sandbox stanza
    (`sandbox.enabled: true`, empty `allowedDomains`, fail-closed permission
    mode) is present and was not dropped or overridden by the merge. If the
    assertion fails, that PR is not launched (and not recorded handled).
  On either failure `watch --once` prints the reason to stderr and exits
  non-zero.
- **Option 7B (rejected): dispatch, then check.** Rejected because a session
  could reach a runnable state uncontained before the check fires; the
  preflight-plus-re-verify guarantees no uncontained session is ever
  launched.

## Decision Outcome

`niwa watch --once` is a new verb (`internal/cli/watch.go`) that runs one
poll-and-dispatch pass:

1. **Preflight (fail-closed).** Verify the OS sandbox can be enforced on
   this platform. If not, print the reason and exit non-zero (Decision 7).
2. **Poll.** Resolve the login and search GitHub for open PRs with
   `user-review-requested:<login>`; intersect the results with the
   workspace's repos (Decision 4).
3. **Dedup + bound.** Drop PRs already in the handled-set; order the rest
   oldest-request-first and take at most the per-run bound (default 3).
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
- **`internal/github` (extended).** A new client method,
  `SearchReviewRequestedPRs(ctx, login) ([]PRRef, error)`, wrapping
  `GET /search/issues`, plus `CurrentLogin(ctx) (string, error)` wrapping
  `GET /user`. `PRRef{Owner, Repo, Number, URL, RequestedAt}`. Auth reuses
  `resolveGitHubToken` and `NewAPIClient`.
- **Containment on the dispatch path (net-new dispatch surface).**
  - A containment-profile builder that produces the settings fragment
    (`sandbox.enabled: true`, `sandbox.network.allowedDomains: []`,
    fail-closed permission mode) merged into the instance settings via the
    existing merge seam.
  - An env-allowlist parameter threaded into the launch seam
    (`dispatchLaunch`), so the watch path launches with
    `cmd.Env = allowlisted(os.Environ())` instead of the full environment.
    The ordinary dispatch path is unchanged.
  - A trusted PR-head pre-fetch step run against the provisioned clone
    before launch.
- **Handled-set store.** Read/append helpers over
  `<workspaceRoot>/.niwa/watch-handled` (flat `owner/repo#number` lines).
- **Staged-review record.** At dispatch, niwa persists `{handle, owner,
  repo, number, url, draftPath}` alongside the existing `SessionMapping`
  so `post`/`discard` can resolve a handle to its PR and draft.

### The metadata-only prompt

Assembled by a pure function of the `PRRef` (satisfying the determinism
requirement): a fixed instruction template interpolating only `owner/repo`,
the PR number, and the PR URL. It instructs the agent to read the PR (title,
body, diff, linked issue, CI status) from its local clone at the pre-fetched
ref, treat all of it as untrusted, write its review to the known draft path,
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
  minus handled-set; order oldest-first; take <= bound
        |
  for each PR (trusted CLI, outside sandbox):
     provision instance (applier.Create)
     git fetch PR head into clone            <- untrusted content as data
     merge no-egress sandbox profile into instance .claude/settings.json
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

Phased so each step is independently testable:

1. **GitHub poll.** Add `CurrentLogin` and `SearchReviewRequestedPRs` to the
   client; unit-test against the existing fake server (`NIWA_GITHUB_API_URL`).
2. **Workspace intersection + dedup + bound.** Enumerate workspace repos,
   intersect, apply the handled-set and the oldest-first bound. Pure,
   table-testable logic.
3. **Containment surface.** Add the env-allowlist parameter to the launch
   seam (with synthetic `HOME`) and the containment-profile builder +
   settings merge + per-instance post-merge re-verification; add the
   hardened PR-head fetch (SHA, no filter-honoring checkout, LFS/hooks/
   submodules/ext-protocols disabled, isolated gitconfig). Unit-test the
   allowlist (subset + canary absence for `GITHUB_TOKEN`/`GH_TOKEN`/
   `SSH_AUTH_SOCK` and an on-disk `~/.netrc`/`~/.config/gh` sentinel), the
   merged settings document, and that a `.gitattributes`+`filter=lfs`
   fixture triggers no smudge during fetch.
4. **`watch --once` orchestration.** Wire poll -> select -> per-PR
   provision/fetch/merge/launch -> record. Fail-closed preflight and
   fail-loud error paths.
5. **Post/discard subcommands.** Resolve handle -> staged-review record;
   post via API from the trusted context; discard records handled.
6. **Adversarial test.** A hostile-PR fixture whose title/body/diff attempt
   egress/push/exfiltration; assert the outbound actions, executed directly
   in the session, are denied at the OS/tool layer (see Security
   Considerations for the test shape).

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

**Permission scope / network.** Containment is the OS-level sandbox
(`sandbox.enabled`, empty `sandbox.network.allowedDomains`, fail-closed
permission mode) applied via the instance settings merge and re-verified
per instance before launch (Decision 7). The empty allowlist blocks the
agent's *tool* egress (Bash and its subprocesses, alternate binaries,
write-then-run) at the OS layer; the Claude Code harness's own model-API
channel is separate and is what keeps the session runnable. Denial is
therefore the sandbox's, not the model's -- the adversarial test asserts
this by executing outbound actions directly in the session (PRD AC9/AC14).

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

- **Windows.** The OS sandbox is unavailable on Windows; the design fails
  closed there (refuses to dispatch) rather than running uncontained. Windows
  self-hosters get no staged reviews until a later version addresses it.
- **Proxy TLS termination / domain-fronting.** The sandbox's egress proxy
  may not TLS-terminate by default, leaving a narrow SNI-evasion seam. With
  an empty allowlist the exposure is limited (there is no allowed host to
  front through for the agent's tools), but it is recorded, not closed.
- **Model-channel cost.** The always-available model channel means a
  hostile PR can still consume model tokens (a cost/DoS vector, not an
  exfiltration one). The per-run staging bound and the handled-set limit the
  blast radius; richer cost controls are deferred.
- **Draft text.** The drafted review text is authored by the untrusted
  session and could contain attacker-influenced prose. The approving
  developer reads it before the trusted step posts it -- the human is the
  trust checkpoint for the draft's *content*, while containment covers the
  session's *actions*.

## Consequences

### Positive

- The dispatch decision is injection-proof by construction: no
  author-controlled text enters the prompt.
- The review session cannot egress (empty allowlist) and cannot act with
  the developer's GitHub credentials (token absent from its env), so a
  hostile PR is contained at the OS/process layer rather than by the model.
- Credential scoping and the sandbox profile land as reusable dispatch
  surface a later contained-dispatch feature can adopt.
- The change is a single niwa PR that reuses existing provisioning and
  settings-merge machinery.

### Negative / costs

- Net-new GitHub client surface (search + user), a containment path, and
  two subcommands make this a sizeable single PR.
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
