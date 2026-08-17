# Lead: How does tsukumogami already share CI and configuration across repos, and what would a shared guardrail cost to adopt and maintain?

## Existing cross-repo mechanisms

**Reusable GitHub Actions workflows, hosted almost entirely in `shirabe`.** Every `uses: tsukumogami/...` reference across the workspace resolves to one of two hosts:

- `tsukumogami/shirabe/.github/workflows/{pr-body,validate-docs,lifecycle,release,finalize-release}.yml` — the shared engine for PR-title/body conformance, doc-format validation, draft/ready lifecycle labeling, and the two-step release dance.
- `tsukumogami/koto/.github/workflows/check-template-freshness.yml` — one workflow, called only by `public/shirabe/.github/workflows/check-templates.yml:17`. So the pattern isn't strictly one-way: shirabe hosts most shared logic but also consumes one from koto, meaning "which repo hosts shared workflows" is answered by convention (whoever owns the underlying CLI/logic), not by a fixed rule.

Versioning is a deliberate split, stated explicitly in the caller comments (`public/shirabe/.github/workflows/pr-body.yml:13-16`, `validate-docs.yml:47-52`): in-org callers pin `@main` — "the tsukumogami adopters pin `@main` so the gate always runs the current engine without per-release pin bumps" — while an external consumer outside the org would use a release tag (`@vX.Y.Z`) for reproducibility. In practice this workspace mixes both: `pr-body.yml` and `validate-docs.yml` callers use `@main`; `release.yml`/`finalize-release.yml` callers pin release tags (`@v0.5.1` in tsuku, `@v0.2.0` in koto/niwa) because a release pipeline needs a stable contract, not a moving target.

Each reusable workflow embeds its own "Example caller" block as the adoption doc (see `pr-body.yml:3-16`, `validate-docs.yml:3-14`) — there's no separate onboarding doc; the workflow file itself is the spec. Both workflows also use `job.workflow_repository`/`job.workflow_sha` to check out the shirabe source at the exact ref the caller pinned, so the caller's binary always matches the workflow contract (`pr-body.yml:95-107`, `validate-docs.yml:45-57`).

**Adoption breadth today** (grep across every repo's `.github/workflows/`):
- `pr-body.yml`: 9 of 10 repos — every public repo plus every private repo (`tools`, `vision`, `coding-tools`, `dot-niwa-overlay`) except shirabe itself (the host self-calls via `./`). This is the widest-adopted shared workflow in the org and the strongest precedent for "adopt everywhere, even in repos with no code to protect."
- `validate-docs.yml`: 4 repos (tsuku, koto, niwa, private/vision) — narrower, gated on having a `docs/**` path worth validating.
- `lifecycle.yml`: 3 repos (`.github`, koto, niwa) — narrower still.
- `release.yml` / `finalize-release.yml`: 3 repos with real release pipelines (tsuku, koto, niwa).

**Design precedent for building a shared reusable workflow**: `public/shirabe/docs/designs/current/DESIGN-gha-doc-validation.md` documents exactly this pattern being built out — a Go CLI plus a `workflow_call` wrapper — explicitly to solve "no reusable GHA workflow... any downstream repo that wants validation must copy the script tree and keep it in sync manually" (line 44-45). It calls out a "Zero files to copy" driver (R13): "the entire validator lives in shirabe; downstream repos add one ~12-line caller file" (line 75-76). A guardrail against test suites reaching real repos fits this exact template: engine lives once in shirabe (or a new home), callers are a few lines each.

**`DESIGN-reusable-release-system.md`** is the other precedent for a deliberate, designed rollout of a shared mechanism across repos (not just organic copy-paste): "Four repos with different toolchains... each implement their own release workflow, duplicating the prepare-release dance and causing version drift bugs" — solved with two reusable workflows plus convention-based hook scripts (`.release/`) so repo-specific behavior stays local while the mutation-sensitive sequence (commit/tag/push) is centralized. That's the closest existing analogue to "one thing must never happen (destructive git push), centralize the safeguard, let each repo customize around it."

## Per-repo CI shape and adoption cost

| Repo | Visibility | Workflows (count) | Trigger shape | Runner | Consumes today | Adoption cost for a new guardrail workflow |
|---|---|---|---|---|---|---|
| tsuku | Public | 30 | push/PR on `**/*.go`, docs paths, release tags, nightly/weekly schedules | ubuntu + macos matrix | validate-docs, pr-body, release, finalize-release | Has real Go test suites; would slot a step into `test.yml`/`integration-tests.yml` or add a dedicated job |
| koto | Public | 11 | PR + push to main, release tags, daily bench cron | ubuntu, macos matrix (release) | validate-docs, pr-body, lifecycle, release, finalize-release, check-template-freshness (hosts it too) | Has functional tests that touch git (`test/functional/steps_test.go`, 459 lines) — a direct beneficiary, not just a copy |
| niwa | Public | 8 | PR (lifecycle/pr-body/validate-docs), push to main on `**/*.go`, release tags, manual live-egress gate | matrix + ubuntu | validate-docs, pr-body, lifecycle, release, finalize-release | This is the repo that caused the incident (functional-test git-clobber fixture) — the guardrail's primary target |
| shirabe | Public | 30 | PR + push to main (`build-and-test.yml`), plus ~20 narrow `check-*.yml` content gates | ubuntu | none (it's the host — self-calls via `./`) | Hosts the shared engine; adding a new guardrail here means adding one more reusable workflow file, same shape as the existing five |
| dot-niwa | Public | 1 | PR only | — | pr-body | Trivial: one `uses:` line is the entire CI surface today; a second `uses:` line is the entire adoption cost |
| .github | Public | 2 | PR only (lifecycle, pr-body) | — | pr-body, lifecycle | Same as dot-niwa: near-zero marginal file, no build step to slot into |
| tools (private) | Private | 6 | push/PR to main (`ci.yml`), plus push on `scripts/ci/**` or `workflows/**` paths (`sync-ci-scripts.yml`) | ubuntu | pr-body | Has a real `ci.yml`; adoption slots a step or job the same way as any public repo |
| coding-tools (private) | Private | 1 | PR only | — | pr-body | Same shape as dot-niwa/.github: single-line adoption |
| vision (private) | Private | 2 | PR only | — | pr-body, validate-docs | Two-line adoption, no build step |
| dot-niwa-overlay (private) | Private | 1 | PR only | — | pr-body | Same as coding-tools |

Six of the ten repos (dot-niwa, `.github`, coding-tools, vision, dot-niwa-overlay, and effectively shirabe-as-host) currently run **no build/test job at all** — their entire CI surface is one or two `pull_request`-triggered reusable-workflow calls. For those, adding a guardrail workflow is a single `uses:` line and a few seconds of runner time, not an integration into an existing pipeline.

## What the `.github` repo can and cannot enforce

`public/.github` currently carries: `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, `.github/ISSUE_TEMPLATE/{bug_report.yml,feature_request.yml,config.yml}`, `.github/PULL_REQUEST_TEMPLATE.md`, and an org `profile/README.md`. No `workflow-templates/` directory exists.

Precision on capability, because this is easy to get backwards:

- **Community health file fallback works.** GitHub falls back to the org `.github` repo's copy of `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `SECURITY.md`, and issue/PR templates for any repo that doesn't define its own. This is real, automatic, and requires no action per-repo.
- **Workflows do not fall back, and cannot be pushed onto other repos.** GitHub Actions only executes workflow files that live in the *consuming* repo's own `.github/workflows/` directory (or a `workflow_call` target it explicitly references with a `uses:` line). Putting a workflow file in `tsukumogami/.github/.github/workflows/` makes it run for the `.github` repo itself — exactly what's happening with `lifecycle.yml` and `validate-pr-body.yml` there today — and nothing else. There is no mechanism by which an org `.github` repo forces a check onto another repo's PRs.
- **`workflow-templates/` is opt-in UI sugar, not enforcement.** If `tsukumogami/.github` had a `workflow-templates/*.yml` + matching `*.properties.json`, every repo's "Actions" tab would list it as a suggested starting point ("Set up this workflow"). A human still has to click it, and it copies a workflow file into that repo — after which it's a normal, independently-editable file with no ongoing link back to `.github`. It cannot retroactively appear on existing PRs, cannot be made required, and a repo maintainer can decline it entirely. This workspace has never used the mechanism (no `workflow-templates/` directory exists), so there's no existing convention to build on here — it would be new territory if chosen.

Net: the only two levers that reach every repo without each repo opting in via a committed workflow file are (a) GitHub's community-health-file fallback (documentation only, not enforcement) and (b) branch protection / rulesets (blocked on private repos by the GitHub Free plan constraint already established). A CI-enforced guardrail against dangerous test behavior has to be a `uses:` line committed into each repo's own `.github/workflows/`, following exactly the `pr-body.yml` pattern — there's no shortcut through the `.github` repo.

## niwa's own file distribution — does it reach CI?

`public/niwa/docs/guides/file-distribution.md` describes three tables (`[files]`, `[instance.files]`, `[root.files]`) that copy files from the workspace's `.niwa/` config directory into managed repos, instance roots, or the workspace root. `public/dot-niwa/.niwa/workspace.toml:51-56` shows the only `[files]` entries actually in use today — five `.claude/shirabe-extensions/*.md` prompt fragments, not CI config.

This mechanism is explicitly scoped to **developer checkouts inside a niwa-managed workspace instance**, not CI and not arbitrary clones:

- `[files]` targets land in "each managed repo," rewritten with a `.local` infix so they match the repo's own `*.local*` gitignore pattern (file-distribution.md, "Why repos get `.local`..." section). A `.local`-suffixed file is by design **not committed**, so it can't reach a GitHub Actions runner, which checks out only what's in git.
- The doc's own Limitations section confirms the boundary: "Repo-subdirectory sessions are not covered... A file at the workspace root or instance root reaches sessions started *at* those levels, not sessions started *inside* a managed repo" — and there's no claim anywhere that instance/root files reach a repo cloned outside niwa (e.g. by CI, or by a contributor who just ran `git clone`).

So niwa's distribution mechanism is real and already the vehicle for pushing shared Claude Code config (skills, MCP config) into every instance, but it can't be the vehicle for the guardrail itself: CI runs against a plain `git clone` of the repo with no niwa instance present, and any guardrail that needs to run in CI must be a **committed, git-tracked file** — which is exactly what the reusable-workflow pattern already provides, and what `[files]`'s `.local` design deliberately avoids (workspace.toml's own `[files]` entries are IDE/agent config, never CI). niwa's distribution mechanism is the right tool for a local pre-commit-style hook a developer's Claude session might use, but not for the enforcement point the guardrail actually needs.

## Findings

1. The org already has exactly the mechanism this guardrail needs: a shirabe-hosted reusable `workflow_call` workflow, `@main`-pinned, adopted by a simple "Example caller" comment block copy-paste. `pr-body.yml` proves this scales to *all ten* repos including the ones with zero other CI.
2. The `.github` repo is not a lever here at all beyond documentation defaults — it cannot push a required check onto other repos, with or without `workflow-templates/`.
3. niwa's `[files]` distribution reaches developer instances, not CI, by design (the `.local` infix exists specifically to keep distributed files out of git, which disqualifies them from being a CI-visible guardrail).
4. Six of ten repos have no build pipeline to integrate with — for them, adoption cost is a single new workflow file with one `uses:` line, matching their existing `pr-body.yml`/`validate-docs.yml` adoption pattern exactly.
5. There is direct precedent for exactly this shape of problem: `DESIGN-reusable-release-system.md` centralizes a destructive, git-mutating sequence (commit/tag/push) into a shared reusable workflow specifically because "CI must own the commit-tag-push sequence" and local execution can't be trusted with it — the same reasoning applies to "test code must never be allowed to touch a real working tree's git state."

## Implications

- The natural home for a shared guardrail is `tsukumogami/shirabe/.github/workflows/`, following the `pr-body.yml`/`validate-docs.yml` template exactly: a `workflow_call` entry point, an "Example caller" comment as the adoption doc, checkout-the-caller + checkout-shirabe-at-pinned-ref steps, `@main` pinning for in-org callers.
- Because GitHub Free blocks org-level rulesets and blocks private-repo branch protection, this reusable-workflow-as-required-check is the only mechanism that reaches *all ten* repos uniformly, public and private alike — it doesn't depend on plan tier at all, only on each repo choosing to add the `uses:` line (and, ideally, marking it a required status check, which *is* available on Free for repos that enable branch protection on public repos, and can at least be a non-bypassable red X on private ones even without "required" status).
- Given `pr-body.yml`'s adoption pattern (universal, including zero-CI repos), the right default is "every repo adopts it," not "only repos with git-touching tests adopt it" — a repo with no dangerous test today is one functional-test fixture away from having one tomorrow, and the marginal cost per zero-CI repo is a few seconds of runner time.

## Surprises

- `pr-body.yml` is adopted even by repos with literally no other CI (`dot-niwa`, `coding-tools`, `dot-niwa-overlay`) — the org has already normalized "every repo, including config-only repos, carries at least one shared reusable-workflow check." That's a strong existing cultural precedent for making a new guardrail similarly universal rather than opt-in-by-risk-assessment.
- The hosting relationship isn't strictly shirabe-owns-everything: koto hosts `check-template-freshness.yml`, consumed by shirabe. So "which repo should host the new guardrail" is answered by "whoever owns the relevant logic," and for a git-safety guardrail, shirabe (already the shared-CI host, already the precedent-setter for the release system's destructive-git-op centralization) is the natural fit — but it isn't the *only* structurally valid choice.
- `koto/test/functional/steps_test.go` (459 lines) is a second repo with git-touching functional test code, not just niwa. The guardrail generalizing beyond niwa is not a hypothetical future concern — it already applies today.

## Open Questions

- Should the guardrail be a `workflow_call` step other workflows call (matching the `pr-body.yml`/`validate-docs.yml` shape), or a `pre-commit`/git-hook style local check distributed via niwa's `[files]` table for defense-in-depth before a push even happens? The two aren't mutually exclusive — niwa's `.local` file distribution could carry a local pre-push hook as a first line of defense, with the shirabe reusable workflow as the CI-side backstop that can't be skipped by an un-hooked clone.
- Does the guardrail need repo-side configuration (e.g. an allowlist of directories a test sandbox may write to), and if so, does it follow the release system's `.release/`-style convention-based hook pattern, or take `workflow_call` inputs like `validate-docs.yml`'s `custom-statuses`?

## Summary

The org already has a proven, universally-adopted distribution mechanism for exactly this kind of guardrail: shirabe-hosted `workflow_call` reusable workflows, `@main`-pinned, adopted via a copy-pasted "Example caller" block — `pr-body.yml` alone reaches all ten repos including the ones with zero other CI, so adoption cost for a new guardrail is a one-line `uses:` addition per repo, not a redesign. The `.github` repo and niwa's `[files]` distribution both looked like plausible alternatives but neither can reach CI: `.github` cannot force a check onto another repo (only supplies fallback docs), and niwa's distributed files are deliberately kept out of git via the `.local` infix, so they never reach a CI runner. The closest structural precedent, `DESIGN-reusable-release-system.md`, already centralizes a destructive git-mutation sequence into a shared reusable workflow for the same underlying reason — trusting every repo to reimplement a dangerous operation safely doesn't scale, so the operation gets one guarded implementation everyone calls.
