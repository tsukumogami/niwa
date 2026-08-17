# Explore Scope: test-suite-repo-guardrails

## Visibility

Public

## Execution Mode

auto (dispatched background run; max-rounds=3)

## Core Question

A functional-test fixture committed a developer's working tree onto `main` and
pushed it to GitHub, because the suite's sandbox lives inside the real working
tree and the fixture's git calls rely on upward repository discovery. The root
cause is settled. What is undecided is the shape of the guardrail: what stops a
test suite from reaching a real repository, and what form of that guardrail
reaches every repository in the organization rather than only the one call site
that escaped.

## Context

- Incident filed as tsukumogami/niwa#249 (label: `bug`). RCA readable at
  `git show b579cbe:wip/research/rca_functional-suite-repo-clobber.md`.
- The defect is latent, not new: every dangerous line is byte-identical across
  recent history. Two concurrent suite runs in one checkout could have triggered
  it at any point.
- `@critical` is fully exposed — the run that did the damage *was*
  `make test-functional-critical`, so gating CI on the critical subset is not
  protection.
- The bad push was a plain **fast-forward**. No force was involved and nothing
  rejected it. Any guardrail evaluated against "would this have blocked the
  push" must be tested against a fast-forward, not against a force-push.
- Already established before this exploration began (verified directly, not
  assumed):
  - `refs/heads/main` on niwa had **no branch protection and no ruleset**. The
    403 seen earlier was a token-scope artifact, not evidence of protection.
  - The organization is on the **GitHub Free** plan. That means org-level
    rulesets are unavailable (needs Team), and branch protection / rulesets on
    **private** repositories are unavailable (needs Pro/Team). Only the six
    public repos can be protected server-side today.
  - Current posture: niwa, `.github`, dot-niwa have nothing; tsuku has a ruleset
    (deletion, non_fast_forward, copilot_code_review); shirabe has a ruleset
    (required_linear_history, deletion, non_fast_forward); koto has classic
    protection with a required `validate` status check.
  - `non_fast_forward` blocks force-pushes. It does **not** block the
    fast-forward push that caused this incident.
- Deliverable 1 (restoring `main` to `230c5c9`) is complete and verified. This
  exploration covers Deliverable 2 only.

## In Scope

- The niwa functional suite's sandbox allocation, fixture git invocation, and
  Makefile concurrency behaviour.
- A mechanism that generalizes across the organization's repositories, weighed
  on how completely it removes the failure mode against what it costs to keep.
- Whether the guardrail can fail closed in CI as well as on a developer machine.
- Sequencing: one PR per repo, or one PR in niwa plus follow-ups.

## Out of Scope

- PR #248's dual-agent workspace feature, and the other six open PRs. Not ours
  to advance, revise, or merge.
- Rewriting history on any branch beyond the single authorized `main` restore.
- Anything under `wip/` on `docs/dual-agent-workspace` — readable, not
  resurrectable.
- Running `make test-functional` in a shared checkout before the sandbox fix
  lands. That is what caused the incident.

## Research Leads

1. **Which server-side rule would actually have blocked this push, and what is
   reachable on the organization's current plan?** (lead-server-side-protection)
   The incident push was a fast-forward by a non-admin-scoped token. Ruleset
   rules differ sharply in what they stop. Establish precisely which rule type
   rejects a direct fast-forward push to a default branch, whether it can be
   applied to all six public repos, what happens to the four private repos that
   cannot carry protection on the Free plan, and what an upgrade would cost.
   This decides whether protection is the whole answer, part of it, or a
   backstop.

2. **Is niwa's functional suite the only place in the organization where a test
   can reach a real repository?** (lead-org-danger-survey)
   A survey counted occurrences, not danger: niwa 18 files with
   `exec.Command("git"` in tests and 13 with `RemoveAll`; koto 1 and 1; tsuku 0
   and 15. Read the actual call sites and judge which can escape into an
   enclosing repository. Distinguish "writes inside a temp dir" from "runs git
   with an inherited working directory". The answer decides whether the fix is
   niwa-shaped or org-shaped.

3. **What is the correct code shape for the niwa suite fix, and does it hold
   against the failure that occurred?** (lead-niwa-suite-fix)
   The RCA proposes a per-process sandbox in `TestMain`, a `fixtureGit` helper
   that bounds-checks its directory and sets `GIT_DIR`/`GIT_WORK_TREE`, a
   Makefile lock, and not discarding the `RemoveAll` error. Enumerate every
   fixture git call site, every fixed shared path, and every place a scenario
   assumes the sandbox is at a known location. Identify what breaks if the
   sandbox moves outside the repository.

4. **What guard can fail closed in CI and on a developer machine at the same
   time, without a server-side rule?** (lead-failclosed-guard)
   A local-only guard protects developers but not an agent or a CI job in a
   fresh checkout; a CI-only guard is the reverse. Investigate what is
   available: `GIT_CEILING_DIRECTORIES`, a test-time assertion that the working
   tree is unchanged, a CI step that fails when a test run dirties the checkout
   or moves HEAD, detection of the `niwa-test <niwa-test@example.com>` commit
   identity, and pre-push hooks. Judge each on whether it catches the
   *mechanism* or only the *fingerprint*.

5. **How does this organization already share CI and configuration across
   repositories, and what would a shared guardrail cost to adopt and maintain?**
   (lead-cross-repo-distribution)
   niwa's `validate-docs.yml` pins
   `tsukumogami/shirabe/.github/workflows/validate-docs.yml@main`, so a
   cross-repo reusable-workflow pattern already exists and is already trusted.
   Establish how widely it is used, what the `.github` repository currently
   carries, whether niwa distributes files to repos through its own config, and
   what the realistic maintenance burden of a shared check is for repos that
   carry no git-touching test code at all.
