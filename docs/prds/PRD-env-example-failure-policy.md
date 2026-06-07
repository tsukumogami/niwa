---
status: Accepted
problem: |
  niwa's .env.example pre-pass has one response to a probable-secret
  detection -- abort apply with a non-zero exit -- and the only controls are
  coarse (declare the key, a fixed allowlist, a public-remote-only bypass
  flag, or disabling the scan). Placeholder values in .env.example routinely
  trip the entropy heuristic, so owners of example and demo environments hit
  false-positive failures they cannot proportionately dial down.
goals: |
  Make the failure response configurable. Probable-secret detections warn
  without blocking by default; owners opt into hard failures and set the
  fail-versus-warn response per detection category at user, project, and
  variable granularity, with the most specific setting winning. Strict
  blocking is recoverable exactly where an operator wants it.
upstream: docs/briefs/BRIEF-env-example-failure-policy.md
---

# PRD: env-example failure policy

## Status

Accepted

## Problem Statement

When niwa runs `apply`, the `.env.example` pre-pass classifies every value in
each repo's `.env.example` and treats a vendor-token-prefix match or an
above-threshold Shannon-entropy value as a probable secret. The only response is
to abort `apply` with a non-zero exit. `.env.example` files exist to hold
placeholder values, and realistic placeholders (UUID-like tokens, base64
samples, long hex strings) regularly clear the entropy threshold, so the file
whose purpose is examples is the one most likely to fail the apply.

The people affected are workspace owners and operators -- especially anyone
standing up example or demo environments. Their only escapes are coarse: declare
the key, match a small fixed allowlist, pass a per-invocation bypass that only
applies to repos with a public remote, or disable the scan entirely. There is no
"warn me but proceed", and no way to set the response differently for one
variable, one project, or one operator. The result is unpredictable failures on
data the author already knew was fake, with no proportionate control.

## Goals

- A detection no longer blocks `apply` on its own. By default it warns and the
  apply proceeds.
- Owners can opt into hard failures deliberately and precisely, choosing the
  response per detection category rather than all-or-nothing.
- The response is configurable at user, project, and variable granularity, with
  predictable most-specific-wins resolution, so an operator can restore strict
  blocking exactly where they want it.
- Existing workspaces keep working without config changes, picking up the new
  warn-by-default behavior automatically.

## User Stories

- As a developer applying a workspace with placeholder values in `.env.example`,
  I want the apply to succeed with a warning, so that fake data does not block my
  environment setup.
- As a security-conscious operator, I want to make vendor-token detections fail
  while entropy detections only warn, so that I block real-looking tokens without
  drowning in placeholder false positives.
- As a project maintainer, I want to set a stricter (or looser) policy for one
  workspace than my personal default, so that the project's risk profile governs
  its own applies.
- As a repo author, I want to mark one known-placeholder variable warn-only in
  the `.env.example` itself, so that the exemption travels with the repo.
- As an operator, I want my own per-variable setting to override a repo's inline
  exemption, so that a repo cannot force-exempt a value I want to block.
- As an operator who has opted into failures, I want a one-off override that lets
  a single apply proceed, so that I am not blocked in an emergency without editing
  config.

## Requirements

Functional:

- **R1.** When no failure policy resolves a detection to `fail`, the pre-pass
  emits a warning that names the key and the detection category and `apply`
  proceeds (warn-by-default). This replaces the current always-abort behavior.
- **R2.** The policy is keyed per detection category, with at least two
  categories resolved independently: vendor-token-prefix matches and
  entropy-threshold matches. Each resolves to `warn` or `fail`.
- **R3.** The policy is declarable at three levels: user (the personal global
  niwa config), project (the workspace config, both workspace-wide and per-repo),
  and variable (a specific environment key).
- **R4.** Resolution is most-specific-wins: a variable-level setting overrides a
  project-level setting, which overrides a user-level setting. Within the project
  level, a per-repo setting overrides a workspace-wide setting. A level that does
  not set a policy for a given category or variable inherits the next-broader
  level. When no level sets a policy, the effective response is `warn`.
- **R5.** A variable-level policy may be expressed two ways: an inline annotation
  on the variable in the repo's `.env.example`, and an explicit per-variable
  entry in the operator's workspace config. When both target the same variable,
  the workspace-config entry wins over the inline annotation.
- **R6.** A per-invocation override downgrades every `fail` outcome to `warn` for
  a single `apply`. This generalizes and replaces the existing
  public-remote-scoped bypass.
- **R7.** No detection behavior is conditioned on a repo's remote visibility. The
  previous public-remote special case and its visibility check are removed.
- **R8.** The existing whole-scan opt-out (disabling `.env.example` reading at the
  workspace or per-repo level) is preserved and independent of the failure
  policy. When scanning is disabled no detections run, so no warnings or failures
  are produced.
- **R9.** A `fail` outcome aborts `apply` with a non-zero exit; a `warn` outcome
  prints the same diagnostic and `apply` proceeds. Both diagnostics name the key
  and the detection category.

Non-functional:

- **R10.** Diagnostics (warnings and errors) never include the value, any
  fragment of the value, or the raw entropy score -- only the key name and the
  category or rule name.
- **R11.** A workspace or personal config that declares no failure policy applies
  without error and yields warn-by-default behavior. No config migration is
  required to adopt the new default.

## Acceptance Criteria

- [ ] With no policy configured, applying a workspace whose repo's `.env.example`
  has an undeclared high-entropy value exits 0 and prints a warning naming the key.
- [ ] With no policy configured, an undeclared vendor-token value (e.g. an
  `sk_live_`-prefixed value) likewise warns and `apply` exits 0.
- [ ] Setting the entropy category to `fail` at the user level makes `apply` exit
  non-zero on a high-entropy value, while a vendor-token-only value still warns.
- [ ] A project-level policy overrides the user-level policy for that workspace's
  repos.
- [ ] A per-repo policy overrides the workspace-wide policy within the same
  workspace.
- [ ] A variable marked warn-only via inline annotation warns while a project
  `fail` policy still fails other keys in the same file.
- [ ] A workspace-config per-variable entry overrides an inline annotation for the
  same key (operator wins).
- [ ] The per-invocation override downgrades all `fail` outcomes to warnings for
  that single apply, and `apply` exits 0.
- [ ] After the change, no behavior differs based on whether the repo has a public
  or private remote.
- [ ] Disabling the scan for a repo or workspace produces no warnings or failures
  for that scope.
- [ ] No warning or error output contains the value bytes, a value fragment, or
  the numeric entropy score.
- [ ] A detection resolved to `fail` aborts `apply` with a non-zero exit and a
  diagnostic naming the key and category; a detection resolved to `warn` prints
  the same diagnostic and `apply` exits 0.
- [ ] With a policy set only at the user level and nothing at the project or
  variable level, a repo's detection is resolved using the user-level policy
  (inheritance fall-through).
- [ ] An existing workspace config with no failure policy declared applies without
  error and exhibits warn-by-default behavior.

## Out of Scope

- Changes to the detection heuristics themselves -- the entropy threshold value
  and the blocklist/allowlist contents are unchanged. This PRD governs the
  response to a detection, not how a detection is made.
- Conditioning policy on remote visibility. It is removed and deliberately not
  reintroduced as a configurable axis.
- Replacing the whole-scan on/off control. It remains the way to stop scanning
  entirely; this work adds graduated responses between that and a hard fail.
- Secret handling beyond the `.env.example` pre-pass: runtime scanning of real
  environment files at materialization, and vault-backed secret storage.

## Decisions and Trade-offs

These record the user-facing decisions settled in the upstream BRIEF plus the
requirements-level choices made here, so downstream design does not re-litigate
them.

- **Warn by default (was fail).** Uniform opt-in: no detection blocks by default,
  including vendor-token matches. Alternative considered: keep a public-remote
  vendor-token hard-fail floor on by default. Rejected for a predictable, uniform
  default; strict blocking is recoverable via config. Trade-off: this is a
  behavior change (see Known Limitations).
- **Per-category granularity.** The policy distinguishes vendor-token matches from
  entropy matches so they can be set independently, because the motivating pain is
  entropy false positives while vendor-token matches are precise. A single global
  warn/fail switch was rejected as too blunt.
- **Three levels mapped to existing config layers.** User = personal global niwa
  config; project = workspace config (workspace-wide and per-repo, matching the
  existing `read_env_example` layering); variable = a specific key.
- **Variable level has two sources, operator wins.** Inline annotation keeps the
  exemption with the repo; the workspace-config entry lets the operator override.
  Operator-wins is the trust boundary so a scanned repo cannot force-exempt a
  value the operator wants blocked.
- **Bypass generalized.** The existing per-invocation bypass becomes a
  downgrade-all-failures-to-warnings override, no longer scoped to public remotes.
- **Remote-visibility special case removed.** Implied by uniform opt-in; the
  public-remote branch and its bypass-gating go away.

## Known Limitations

- **Behavior change on upgrade.** After this lands, an `apply` that previously
  hard-failed on a probable secret will succeed with a warning unless the operator
  has opted into failing. Operators relying on the old fail-by-default behavior
  must add a failing policy at the level they want.
- **Inline exemptions are repo-controlled.** A repo author can mark a value
  warn-only inline, including a value that is actually a real secret. The
  operator-config-wins rule is the mitigation, but absent an operator override the
  inline annotation is trusted.
