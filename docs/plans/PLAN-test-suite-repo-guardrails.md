---
schema: plan/v1
status: Draft
execution_mode: multi-pr
upstream: docs/designs/current/DESIGN-test-suite-repo-guardrails.md
issue_count: 5
---

# PLAN: test suite repo guardrails

## Status

Draft

## Scope Summary

Implement `docs/designs/current/DESIGN-test-suite-repo-guardrails.md`: make it
impossible for a test suite to rewrite or push a real repository, across the
organization rather than at the one call site that escaped.

The design settles on four layers because no single one covers everything. The
code fix removes the one known defect but cannot vouch for tests nobody has
written. A `pull_request` ruleset would have stopped the incident regardless of
the test bug, but the GitHub Free plan blocks server-side protection on private
repositories outright, and on three public repos the release pipeline pushes to
`main` under a user-owned PAT — the same actor as the incident push — so those
repos cannot take the rule until the release identity is separable. The CI
tripwire needs no foreknowledge of how a future test misbehaves, but detects
rather than prevents. The credential hardening shrinks what a CI-side escape
could reach.

Closes the code half of tsukumogami/niwa#249.

## Decomposition Strategy

**One PR per repository, plus one configuration change that is not a diff.**

The sequencing constraint that shapes this plan: a repository cannot adopt a
shared reusable workflow with `uses: tsukumogami/shirabe/...@main` until that
workflow exists on shirabe's `main`. Adopter PRs opened before then would
reference a workflow that does not resolve, and would be red through no fault
of their own. Since this work opens PRs and does not merge them, every adopter
PR is deferred to a follow-up that becomes actionable the moment issue 2 lands.

That constraint is why issue 1 carries niwa's tripwire **inline** rather than as
a caller. niwa is the repository that was actually clobbered; making it wait for
a cross-repo merge before it gets any CI-side check would be the wrong trade. The
follow-up that adopts the shared workflow replaces the inline steps with the
`uses:` line, which is a smaller and more reviewable change than the reverse
order would produce.

## Issue Outlines

### Issue 1 — niwa: stop the functional suite from reaching the real repository

**Repo:** niwa. **PR:** self-contained. **Depends on:** nothing.

Layer 1 plus layer 4, in the repository where the incident happened.

- `test/functional/suite_test.go`: add `TestMain`, allocating one process-wide
  sandbox with `os.MkdirTemp("", "niwa-func-")` and removing it after `m.Run()`
  unless `NIWA_TEST_KEEP_SANDBOX` is set. Log the cleanup error rather than
  discarding it.
- Add a startup assertion in `TestMain`: the sandbox root must have no `.git`
  ancestor. It cannot fire against the current allocation, which is the point —
  it fires if someone later re-parents the sandbox into a checkout.
- Replace the per-scenario wipe-and-recreate of `<repo>/.niwa-test` with
  `os.MkdirTemp(processSandboxRoot, "scenario-*")`. Nothing shared, nothing to
  race over.
- Fold `workspaceRoot` into the scenario sandbox, retiring the machine-global
  `/tmp/niwa-test-workspaces` path that is shared across different checkouts.
- Add one bounds-checked `fixtureGit` helper that refuses a directory outside
  the sandbox and pins `GIT_DIR`, `GIT_WORK_TREE`, `GIT_CEILING_DIRECTORIES`,
  `GIT_CONFIG_GLOBAL` and `GIT_CONFIG_SYSTEM`. Route every fixture git
  invocation through it — `localrepo_test.go`, `session_steps_test.go`'s
  `runGitInDir`, `steps_workspace_config_sources_test.go`, and
  `worktree_delegation_steps_test.go`. Note `git -C` is not a containment
  boundary, so `-C` call sites need this as much as the `cmd.Dir` ones did.
- `Makefile`: `flock` the functional targets so two runs in one checkout fail
  fast instead of interleaving. Drop the now-dead `rm -rf .niwa-test`.
- `.github/workflows/test.yml`: `persist-credentials: false` on checkout and
  `permissions: contents: read`, so a CI-side escape finds no writable token.
  Add inline before/after `git rev-parse HEAD` and `git status --porcelain`
  checks around the functional-test step. Use `rev-parse`, not `symbolic-ref` —
  `actions/checkout` leaves a detached HEAD, so a `symbolic-ref` guard would be
  permanently red.
- `docs/guides/functional-testing.md`: document the new sandbox model, the keep
  flag, and the rule that fixture git calls go through the helper.

**Acceptance:** `gofmt -l .` empty; `go vet ./...` clean; unit suite green;
`make test-functional` green; `grep -n 'exec.Command("git"' test/functional/*.go`
returns only the helper; after a full functional run `git status --porcelain` is
empty and `HEAD` is unchanged.

### Issue 2 — shirabe: a shared repository-safety tripwire

**Repo:** shirabe. **PR:** self-contained. **Depends on:** nothing.

A `workflow_call` reusable workflow following the established `pr-body.yml`
template — the pattern already adopted by every repository in the organization,
including the ones with no other CI. It records `git rev-parse HEAD` and
`git status --porcelain` around a caller-supplied test command and fails the job
if either changed.

shirabe adopts it itself via `./.github/workflows/...`, which is what keeps this
PR green and self-contained rather than depending on a merge.

The workflow's "Example caller" comment block is the adoption doc, matching the
convention the other shared workflows use.

**Acceptance:** shirabe's own CI green with the workflow self-adopted; the
example caller block present; a deliberate planted change proves the check
fails when the checkout is dirtied.

### Issue 3 — branch protection where the release flow permits it

**Repo:** none — this is a configuration change, applied through the API.
**Depends on:** nothing.

Create a ruleset on `refs/heads/main` carrying the `pull_request` rule with an
empty `bypass_actors` list, on **niwa**, **dot-niwa**, and **.github**.

These three are safe today and verified so: niwa has 143 commits on `main` with
zero `chore(release):` commits, so the version-bump path has never run there,
and its last direct push was 2026-04-28; `.github` has been PR-only since
2026-04-20; dot-niwa has one direct commit, its initial one. An empty bypass
list is deliberate — it blocks even an org owner, which classic branch
protection does not do by default.

**koto, tsuku and shirabe are explicitly excluded** and must not be given this
rule in this change. Their release automation pushes version-bump commits
straight to `main` using a PAT owned by a human account, so an empty bypass list
would break the next release, and a bypass naming that account would have
permitted the incident push exactly as it happened. Issue 5 is the follow-up.

Because this is not a diff, it needs an explicit record of what changed and how
to undo it: the ruleset id, the exact JSON applied, and the single API call that
deletes it.

**Acceptance:** each ruleset readable back with the expected rule and an empty
bypass list; a dry-run push attempt to `main` rejected; the revert command
recorded.

### Issue 4 — adopt the shared tripwire across the organization

**Repos:** niwa, koto, tsuku, dot-niwa, .github, and the organization's private
repositories. **Depends on:** issue 2 merged.

One caller file per repository, `@main`-pinned, matching the convention. For
niwa this replaces the inline steps issue 1 added.

This is the only layer that reaches the private repositories, which cannot carry
any server-side protection on the current plan. Deferred rather than done here
because a caller cannot be green before the callee exists on `main`.

### Issue 5 — separate the release push identity

**Repos:** koto, tsuku, shirabe. **Depends on:** nothing, but blocks extending
issue 3.

Move the release workflow's push off the user-owned `RELEASE_PAT` onto an
identity a ruleset can name without also naming a human — a GitHub App
installation token or a dedicated machine account. Then extend issue 3's ruleset
to those three with that identity as the sole bypass actor.

Carries real release-breaking risk and touches the shared release workflow, so
it is deliberately not folded into this change.

## Implementation Sequence

Issues 1, 2 and 3 are independent and land in parallel. Issue 4 unblocks when 2
merges. Issue 5 is independent, and extending issue 3's coverage waits on it.

Order by value delivered: issue 3 first — it is the only layer that would have
stopped the incident on its own, and it is a configuration call that takes
minutes. Then issue 1, which removes the defect. Then issue 2, which sets up the
generic net. Issues 4 and 5 are follow-ups filed with their blockers named.

## Out of Scope

- Upgrading to GitHub Team to unlock org-level rulesets and private-repository
  protection. It is the only way to close issue 3's gap on the private repos,
  and it is a spending decision. Filed as a follow-up.
- koto's fixed `/tmp/koto-test-*` sentinel files: a real fixed-shared-path
  collision risk, unrelated to repository escape.
- niwa's `worktree_delegation_steps_test.go` builds a repo path without the
  `.git`-existence check its sibling helper performs. Worth aligning; not this
  change.
