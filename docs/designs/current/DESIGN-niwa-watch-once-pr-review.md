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
  outside the sandbox; the session runs with an empty egress allowlist.**
  The trusted CLI fetches the PR's head ref into the clone as part of
  setup (a `git fetch` of a ref is a data operation that does not execute
  PR content). The review session then reads local files and needs no
  network of its own, so `allowedDomains` is genuinely empty for the
  agent's tools.
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
  plus `PATH`/`HOME`/locale -- and nothing else. The GitHub token and every
  other inherited secret are absent.
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
- **Option 6B (rejected): print a ready-to-run `gh pr review` command** for
  the developer to paste. Rejected because it makes "post it" a copy step
  rather than one gesture and pushes credential handling onto the developer;
  the subcommand keeps the one-gesture promise while preserving the
  boundary.

### Decision 7 -- Fail-closed detection

- **Option 7A (chosen): preflight the containment before provisioning.**
  If the platform cannot enforce the OS sandbox (e.g. `GOOS == "windows"`)
  or the sandbox settings cannot be applied, `watch --once` refuses to
  dispatch, prints the reason to stderr, and exits non-zero -- before any
  instance is created.
- **Option 7B (rejected): dispatch, then check.** Rejected because a session
  could reach a runnable state uncontained before the check fires; the
  preflight guarantees no uncontained session is ever launched.

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
   seam and the containment-profile builder + settings merge; add the
   PR-head pre-fetch. Unit-test the allowlist (subset + canary absence) and
   the merged settings document.
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

*(Populated by the Phase 5 security review below.)*

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
  content (as data); it must fetch without running repository-supplied
  hooks.
- The empty egress allowlist means the agent cannot use `gh`/network tools;
  the prompt must direct it to the local clone, and some agent conveniences
  are unavailable in-session (acceptable, and the point).

### Mitigations

- Fail-closed preflight guarantees no uncontained session launches.
- Handled-set-on-success-only prevents a transient failure from suppressing
  a review.
- The adversarial test is part of "done," so the boundary is verified by the
  exact injection surface it defends.
