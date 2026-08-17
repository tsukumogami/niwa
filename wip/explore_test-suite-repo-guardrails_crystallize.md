# Crystallize: test-suite-repo-guardrails

## Outcome

**A chain, entering at `/scope`**, terminating in a PLAN that `/execute` drives.

## Why not the terminal outcomes

- **Not file-an-issue.** The work spans several repositories and has a real
  design question in it (which rule, which bypass, which layer covers which
  gap). It is not one deliverable for one person.
- **Not a spike.** Nothing here is a feasibility question. Every mechanism was
  verified during exploration — `GIT_CEILING_DIRECTORIES` semantics
  experimentally, ruleset semantics against the live API and GitHub's docs, plan
  limits against the org's billing state.
- **Not a decision record.** The choice is not between named alternatives; it is
  a layered design where each layer covers a gap the others cannot reach. The
  rejected options are recorded in the findings, which is enough.
- **Not a competitive landscape.** Wrong shape, and this is a public repo.

## Why `/scope` and not `/charter`

This is one bounded piece of work with a settled problem statement and no
strategic bet to justify. Tactical chain.

## Scope compression

The BRIEF and PRD hops carry no weight here: the problem statement is already
written and agreed (niwa#249 plus the RCA), and the requirement is a single
sentence the user already stated — no test suite in the organization can rewrite
or push a real repository. The chain should enter at DESIGN, which is where the
one real question lives (the layering and the bypass list), and terminate in a
PLAN.

## What the PLAN must cover

Five work items, one PR each, ordered so the highest-value protection lands
first and nothing depends on a review that has not happened:

1. **niwa: fix the functional suite.** Per-process sandbox in `TestMain`,
   per-scenario child, `workspaceRoot` folded inside, one bounds-checked
   `fixtureGit` helper routed through all nine call sites, `flock` in the
   Makefile, no discarded `RemoveAll` errors, and a startup assertion that the
   sandbox has no `.git` ancestor. Closes niwa#249's code half.
2. **niwa: harden the CI blast radius.** `persist-credentials: false` and
   least-privilege `permissions:` on the job that runs the functional suite.
3. **shirabe: add the shared tripwire workflow.** A `workflow_call` reusable
   workflow that fails when a test step moves HEAD or dirties the checkout,
   following the `pr-body.yml` template exactly, with its "Example caller" block
   as the adoption doc.
4. **Every repo: adopt the tripwire.** One caller file each, `@main`-pinned,
   matching the established convention. Reaches the private repos, which no
   server-side rule can.
5. **Branch protection across the public repos.** A ruleset carrying the
   `pull_request` rule, bypass granted to the release automation identity and
   nothing else — verified necessary, because the shared release workflow pushes
   commits directly to the release branch, and verified sufficient, because an
   empty-bypass ruleset blocks even an org-owner account.

Item 5 is a configuration change to live repositories rather than a diff, so the
PLAN must say plainly how it is applied, how it is verified, and how it is
reverted. It is also the only item that would have prevented the incident on its
own.

## Follow-ups, recorded not dropped

- Upgrading to GitHub Team to unlock org-level rulesets and private-repo
  protection, which is the only way to close item 5's gap on the private
  repositories. A spending decision, so it is filed, not taken.
- koto's fixed `/tmp/koto-test-*` sentinel files — a genuine fixed-shared-path
  collision risk, unrelated to repository escape.
- niwa's `worktree_delegation_steps_test.go` builds a repo path without the
  `.git`-existence check its sibling helper performs. Worth aligning.
