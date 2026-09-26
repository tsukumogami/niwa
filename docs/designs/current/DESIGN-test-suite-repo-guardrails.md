---
schema: design/v1
status: Current
problem: |
  niwa's functional-test fixture committed a developer's working tree onto the
  real repository's main branch and pushed it to GitHub. The suite's sandbox
  was a fixed path inside the checkout, wiped per scenario; two concurrent
  runs raced, one wiped the other's live clone, and git's upward repository
  discovery climbed into the real repo. Nothing server-side rejected the push.
  The organization needs this class of accident to be impossible, including
  for tests nobody has written yet.
decision: |
  Four guardrails, each closing a gap the others cannot reach. Fix the defect
  in niwa's suite (per-process temp sandbox, one bounds-checked git helper
  pinning GIT_DIR/GIT_WORK_TREE/GIT_CEILING_DIRECTORIES, a startup assertion,
  a Makefile flock). Make main unwritable except through a reviewed PR via a
  ruleset carrying the pull_request rule with an empty bypass list — applied
  today to the public repos whose release automation never pushes to the
  protected branch, and to the rest once their release push moves off a
  user-owned PAT onto a separable automation identity. Ship a shared CI tripwire
  as a shirabe-hosted workflow_call workflow every repo adopts, failing when a
  test step moves HEAD or dirties the checkout. Remove credentials from the
  CI blast radius with persist-credentials: false and least-privilege
  permissions on the test job.
rationale: |
  No single mechanism covers everything. The code fix removes the one known
  defect but cannot vouch for future tests. The ruleset would have stopped
  the incident regardless of the test bug, but the organization's GitHub Free
  plan blocks server-side protection on private repositories entirely, and
  on three public repos the release pipeline pushes to main under the
  owner's own PAT — the same actor as the incident push — so those repos
  cannot take the rule until the release identity is separable. The
  tripwire is plan-independent and needs no foreknowledge of how a future
  test misbehaves, but it detects after the fact rather than preventing. The
  credential hardening shrinks what a CI-side escape could do. Together the
  four cover local runs, CI runs, public repos, private repos, and code that
  does not exist yet.
---

# DESIGN: Test Suite Repo Guardrails

## Status

Current

## Context and Problem Statement

The incident is tsukumogami/niwa#249. niwa's functional suite rooted its
sandbox at a fixed path inside the real working tree —
`sandbox := filepath.Join(repoRoot, ".niwa-test")` at
`test/functional/suite_test.go:117` — and wiped it with `os.RemoveAll` once
per scenario, error discarded, with no lock. The fixture in
`test/functional/localrepo_test.go` created clone workdirs inside that sandbox
and ran `git add -A`, `git commit`, and `git push -u origin HEAD` with
`cmd.Dir` set but no `GIT_DIR` (`localrepo_test.go:90-104`), relying on git's
upward repository discovery to find the `.git` directory.

Two concurrent runs raced. One run's `RemoveAll` deleted the other's live
clone workdir mid-sequence; `MkdirAll` recreated the sandbox as a plain
directory with no `.git`. The surviving run's git commands, finding no
repository at their working directory, walked upward — past the vanished
workdir, past the sandbox — and discovered the real niwa checkout.
`git symbolic-ref HEAD refs/heads/main` repointed the real repository's HEAD,
`git add -A` staged the developer's entire working tree, `git commit` wrote it
onto `main`, and `git push` fast-forwarded GitHub's `main` to match. The push
was a clean fast-forward using the repo owner's own credentials. No force
flag, no PR, no CI run. Nothing rejected it.

Three facts shape the problem beyond the immediate bug:

- **The defect exists in exactly one place in the organization.** Every other
  git-touching test in every repo, public or private, already roots itself in
  a process-unique temp directory or passes explicit scoping. tsuku's
  functional suite independently arrived at the correct per-scenario
  `MkdirTemp` pattern, one repo over from where it was needed. But the
  guarantee being asked for — that no test suite in the organization can ever
  rewrite or push a real repository — is a claim about tests nobody has
  written yet. A guarantee about future code cannot live inside the code it
  is making a promise about. Fixing niwa is necessary and insufficient.
- **The repos that look protected are not protected against this.** tsuku and
  shirabe carry rulesets with `non_fast_forward`, which blocks force-pushes
  and lets a clean fast-forward straight through. shirabe's
  `required_linear_history` objects to merge commits; the bad commit was
  linear. koto's classic branch protection has a required status check, but
  `enforce_admins` is false, and classic protection exempts admins by default
  — the pushing identity had admin permission, so the check never applied.
  Only three rule types reject a direct fast-forward push of a never-checked
  commit: `pull_request`, `required_status_checks`, and the ruleset `update`
  rule.
- **The organization is on the GitHub Free plan.** Org-level rulesets require
  Team. Branch protection and rulesets on private repositories require Team.
  So server-side protection can cover at most the six public repos, and the
  organization's private repositories cannot be protected server-side at all
  today. Any honest design has to cover that gap with something
  plan-independent rather than paper over it.

## Decision Drivers

- **Close the actual defect.** The niwa suite must be safe under concurrent
  runs, in the same checkout and across checkouts on one machine.
- **Cover tests that do not exist yet.** The next accident will not share this
  one's file paths, author string, or command sequence. Guards keyed to this
  incident's fingerprint are worthless.
- **Would it have stopped this incident?** Each guardrail is measured against
  the exact push that happened: a fast-forward of a linear commit to `main`
  over SSH by a repo admin, no PR, no CI.
- **Reach the private repositories.** The Free plan blocks all server-side
  protection there, so at least one guard must work without any GitHub
  feature.
- **Don't break the release pipeline.** The shared release workflow pushes
  version-bump commits directly to the release branch
  (`git push origin HEAD:<ref>`), and koto, tsuku, and shirabe exercise that
  path. A rule that blocks all direct pushes on those repos breaks their
  next release. The commit author on those pushes is `github-actions[bot]`,
  but that is cosmetic — the workflow sets the identity unconditionally, and
  the push authenticates as whatever token the caller supplies.
- **Identical semantics locally and in CI.** The incident happened on a
  developer machine. A guard that only exists in CI leaves the actual failure
  site uncovered.
- **Low adoption cost.** Six of the ten repos run no build pipeline at all;
  anything they adopt must cost one file with one `uses:` line.

## Considered Options

### Decision 1: How the niwa suite gets fixed

- **Relocate the sandbox and pin git's scope (chosen).** A per-process root
  from `os.MkdirTemp("", "niwa-func-")` allocated in `TestMain`, a fresh
  child directory per scenario instead of wiping a shared path, and one
  bounds-checked helper that pins `GIT_DIR`, `GIT_WORK_TREE`, and
  `GIT_CEILING_DIRECTORIES` on every fixture git call. This removes both
  halves of the mechanism: there is no shared path to race over, and even a
  raced git command cannot discover a repository outside the sandbox.
- **Keep the in-repo sandbox, add only a lock (rejected).** A `flock` around
  the test target serializes runs in one checkout, but the sandbox would
  still sit inside a real working tree, one bug away from the same escape,
  and a lock does nothing for two different checkouts. The suite also had a
  second fixed shared path — `/tmp/niwa-test-workspaces` at
  `suite_test.go:145` — shared across every checkout on the machine, which a
  repo-local lock cannot see. The lock is kept, but as one guard among
  several, not the fix.
- **A shared test-helper module other repos import (rejected).** Over-built.
  The defect exists only in niwa; koto, shirabe, and tsuku already
  independently use the correct pattern (temp-dir roots, explicit scoping),
  and half the repos have no test code at all. A library nobody else needs is
  maintenance surface without a beneficiary, and it would not protect the
  repos that never import it — which is the same future-code gap all over
  again.

### Decision 2: Which server-side rule, and who may bypass it

- **A ruleset carrying the `pull_request` rule with an empty bypass list,
  applied where the release pipeline permits (chosen).** `pull_request`
  requires every change to land via a reviewed PR, which categorically
  excludes direct pushes to the target ref. Rulesets have the right default:
  an empty bypass list blocks everyone, including org owners — verified
  against the live API, which reports `current_user_can_bypass: "never"` for
  an org-owner account against a ruleset with `bypass_actors: []`. Classic
  protection defaults the other way (admins exempt unless `enforce_admins`
  is flipped on), which is exactly why koto's existing protection failed to
  matter.

  The obvious shape — bypass for "the release automation identity" — fell
  apart under inspection, and the constraint it exposed is load-bearing.
  Release commits on `main` are authored by `github-actions[bot]`, but the
  shared release workflow sets that identity unconditionally
  (`git config user.name "github-actions[bot]"` in shirabe's `release.yml`),
  so the author string says nothing about who pushed. The push itself
  authenticates with the caller-supplied `token` secret, falling back to
  `GITHUB_TOKEN` only when none is set — and niwa and koto both pass a
  `RELEASE_PAT` (niwa's `.github/workflows/prepare-release.yml:27-28`). A
  PAT authenticates as its human owner. So the release push and the
  incident push are the same actor, and a ruleset cannot separate them by
  bypass list: granting the owner bypass to keep releases working would
  have permitted the incident exactly as it happened, and granting bypass
  to the GitHub Actions app instead would block the release flow in every
  repo that passes a PAT.

  The resolution: an empty bypass list on the repos where nothing breaks —
  niwa has 143 commits on `main` and zero `chore(release):` commits (the
  version-bump path has never been exercised there), and dot-niwa and
  .github have no release workflow at all. Those three take the rule today,
  and niwa is the repo that was actually clobbered. koto, tsuku, and
  shirabe wait behind a named follow-up: moving the release push onto an
  identity a ruleset can distinguish from a human — a GitHub App or machine
  account. That the human owner must never hold bypass is not softened by
  any of this; it is the reason the constraint bites.
- **`non_fast_forward` or `required_linear_history` (rejected).** Already
  present on tsuku and shirabe, and silent on this failure mode. The bad push
  was a clean fast-forward of a linear commit — the exact shape these rules
  wave through.
- **`required_status_checks` as the primary rule (rejected as primary, open
  as a later addition).** It would have blocked the push — a commit that
  never ran CI has no check recorded for its SHA, so the ref cannot move —
  but that is a side effect of how check runs are scoped, not the rule's
  intent, and it requires a CI check to exist first. `pull_request` states
  the actual policy ("changes go through review") and needs no CI
  prerequisite.
- **The ruleset `update` rule (rejected).** Blocks every non-bypass push,
  which would have worked, but it adds only a bypass-list gate, not a review
  requirement. `pull_request` gives the same block plus the process the
  organization actually wants.

### Decision 3: What catches a future test that escapes anyway

- **A CI tripwire on repository state (chosen).** Record `git rev-parse HEAD`
  and `git status --porcelain` before the test step, compare after, fail the
  job on any difference. This is the one mechanism that needs no
  foreknowledge of how a future test misbehaves — it watches the symptom
  (the checkout changed) rather than any particular cause. It must compare
  `rev-parse HEAD`, not `symbolic-ref HEAD`: `actions/checkout` leaves a
  detached HEAD, so a `symbolic-ref` guard would fail on every stock
  checkout and be permanently red.
- **Identity-based push rejection (rejected).** A pre-push hook or CI scan
  keyed to the `niwa-test <niwa-test@example.com>` author string catches the
  fingerprint, not the mechanism — a test written tomorrow with a different
  identity sails through. Two further defects, both verified: git hooks are
  not distributed by `git clone` (a fresh clone has only `.sample` files and
  no `hooksPath` setting), so CI runners and every fresh checkout start
  disarmed; and a PR-commit scanner never sees a push that skips PR review,
  which is exactly the shape of what happened.

### Decision 4: How the tripwire reaches every repo

- **A shirabe-hosted `workflow_call` reusable workflow (chosen).** The
  organization already distributes shared CI this way: shirabe's `pr-body`
  workflow is adopted by all ten repos, including the four with no other CI,
  via a single caller file with one `uses:` line, `@main`-pinned, with the
  workflow's own "Example caller" comment block as the adoption doc. The
  tripwire follows that template exactly. Because it is plain CI, it reaches
  the private repositories that no server-side rule can.
- **Distributing from the org `.github` repository (rejected).** It cannot.
  An org `.github` repo supplies fallback community-health files and opt-in
  workflow templates; workflows do not fall back, and nothing in that repo
  can force a check onto another repo's PRs. A template still requires a
  human in each repo to click, copy, and keep the copy current.
- **niwa's own `[files]` distribution (rejected).** By design it writes
  `.local`-suffixed, gitignored files into developer checkouts inside a
  niwa-managed workspace. A CI runner checks out only what is in git, so a
  deliberately-uncommitted file never reaches it.

### Decision 5: Closing the private-repository gap for real

- **Upgrade to GitHub Team (deferred, recorded as a follow-up).** Team
  unlocks org-level rulesets — one rule configured once, covering every repo
  public and private — and is the only way to give the private repositories
  server-side protection. It is a spending decision (about $4 per user per
  month), so it is filed as a follow-up rather than taken inside this
  design. Until then, the private repositories are covered by the tripwire
  and by the fact that none of them currently contains the defect.

## Decision Outcome

Four guardrails, one per gap:

1. **Fix niwa's functional suite.** Per-process sandbox from
   `os.MkdirTemp("", "niwa-func-")` allocated in `TestMain`; a fresh child
   directory per scenario; `workspaceRoot` folded inside the sandbox, which
   also removes the machine-global `/tmp/niwa-test-workspaces` path; one
   bounds-checked `fixtureGit` helper pinning `GIT_DIR`, `GIT_WORK_TREE`,
   and `GIT_CEILING_DIRECTORIES`, routed through all nine fixture git call
   sites; a `flock` around the functional-test Make targets; `RemoveAll`
   errors logged instead of discarded; and a startup assertion that the
   sandbox root has no `.git` ancestor. This is prevention: it fires before
   any dangerous command runs, works identically on a laptop and in CI, and
   depends on no GitHub feature.
2. **Make `main` unwritable except through a reviewed PR**: a ruleset
   carrying the `pull_request` rule with an empty bypass list, applied today
   to niwa, dot-niwa, and .github — the public repos whose release
   automation never pushes to the protected branch — and to koto, tsuku,
   and shirabe once their release push moves off a user-owned PAT onto a
   separable automation identity. This is the one layer that would have
   stopped the incident on its own, regardless of the test bug, and it
   keeps working for tests nobody has written yet. It lands immediately on
   niwa, the repo that was actually clobbered.
3. **A shared CI tripwire**, hosted in shirabe as a `workflow_call` reusable
   workflow and adopted by every repo with one caller file. It fails the job
   when the test step moves HEAD or dirties the checkout. This is detection,
   not prevention — it fires after the fact — but it is the only layer that
   reaches the private repositories and the only one that generalizes to
   failure shapes nobody has imagined.
4. **Take credentials out of the CI blast radius.** `persist-credentials:
   false` on checkout and a least-privilege `permissions:` block
   (`contents: read`) on the job that runs the functional suite, so an
   escape inside CI finds no writable token sitting in `.git/config`.

## Solution Architecture

### Layer 1: the niwa suite

`test/functional/suite_test.go` gains a `TestMain` (the package has none
today; `TestFeatures` is its only test function, so adding one is clean):

- `TestMain` allocates `processSandboxRoot` via
  `os.MkdirTemp("", "niwa-func-")` before `m.Run()` and removes it after,
  logging rather than discarding the cleanup error. Setting
  `NIWA_TEST_KEEP_SANDBOX` skips cleanup and prints the path, replacing the
  old sandbox's "easy to inspect on failure" rationale.
- `TestMain` also runs the startup assertion: with the working directory set
  to the new sandbox root, `git rev-parse --show-toplevel` must fail. If it
  succeeds, the sandbox sits inside some real repository and the suite
  refuses to run. One check, one place, covering call sites that do not
  exist yet.
- The godog `Before` hook replaces the wipe-and-recreate of a fixed path
  (`suite_test.go:117-119`) with `os.MkdirTemp(processSandboxRoot,
  "scenario-*")` — a fresh, unique directory per scenario, nothing shared,
  nothing to race over, no `RemoveAll` in the hot path at all.
- `workspaceRoot` becomes a child of the scenario sandbox instead of the
  fixed `/tmp/niwa-test-workspaces` path (`suite_test.go:145-147`). niwa's
  `CheckInitConflicts` only walks upward looking for niwa-managed ancestors,
  which a fresh temp directory does not have, so the relocation is safe.

Every fixture git invocation routes through one helper:

```go
func fixtureGit(sandboxRoot, dir string, args ...string) (string, error)
```

The helper refuses to run if `dir` resolves outside `sandboxRoot` (a caller
passing a stale or escaped path fails loudly instead of silently targeting
whatever git discovers), and pins the environment:
`GIT_DIR=<dir>/.git`, `GIT_WORK_TREE=<dir>`, and
`GIT_CEILING_DIRECTORIES=<parent of sandboxRoot>` as an absolute path.

The nine call sites it replaces span four files: the clone-workdir
`add`/`commit`/`push` sequence and the two `symbolic-ref` calls in
`test/functional/localrepo_test.go`, the shared `runGitInDir` helper at
`test/functional/session_steps_test.go:696` (the natural chokepoint for its
roughly six step-function callers), the init/config/push loops in
`test/functional/steps_workspace_config_sources_test.go:60-86`, and the
worktree listing at `test/functional/worktree_delegation_steps_test.go:278`.
A key finding motivates routing all of them, not just the three lines that
fired: `git -C <dir>` is not a containment boundary. It sets the starting
directory for git's normal upward discovery, exactly like `cmd.Dir`, so
every `-C` call site carries the identical escape property. `git init
--bare` and `git clone` calls do not operate on an ambient repository and
need only the bounds check, via a thin sibling that skips the env pinning.

Two verified `GIT_CEILING_DIRECTORIES` gotchas shape this design. First, the
ceiling does not blind git to the ceiling directory itself, only to
traversal past it — which is fine here, since a fixture always sits below
the sandbox root, but worth knowing. Second, relative paths are silently
ignored: a misconfigured ceiling is a no-op, not an error, and the suite
would keep passing with the guard disabled. That silent-failure mode is why
the startup assertion exists alongside the ceiling rather than instead of
it — a second lock on the same door, one that fails loudly.

The Makefile wraps the functional-test targets in `flock` on a repo-local
lock file, serializing concurrent invocations in one checkout. The
cross-checkout race is closed by the sandbox relocation itself (every
process gets a unique root), so the two guards compose to cover both shapes.
The now-dead `rm -rf .niwa-test` cleanup lines drop out of the Makefile.

Nothing else moves: no scenario file or step function hardcodes the sandbox
path — every consumer reads it from `testState` fields populated in the
`Before` hook — and CI invokes `make test-functional` with no path
assumptions. `docs/guides/functional-testing.md` gets updated to describe
the new layout.

### Layer 2: the ruleset

niwa, dot-niwa, and .github each get a ruleset targeting the default branch
with the `pull_request` rule and `bypass_actors: []`. Nobody bypasses — not
the owner, not automation — because nothing on these repos pushes to the
protected branch outside a PR: dot-niwa and .github have no release
workflow, and niwa's version-bump path has never been exercised (143
commits on `main`, zero `chore(release):` commits).

koto, tsuku, and shirabe do exercise that path: the shared release workflow
pushes version-bump commits straight to `main`, authenticated by the
caller-supplied `RELEASE_PAT` — a token owned by the same person whose
credentials made the incident push. A bypass wide enough to admit that
release push admits the incident push too, so these three repos cannot
take the rule until the release push authenticates as something a ruleset
can distinguish from a human: a GitHub App or a machine account. That
migration is a named follow-up — real work, with release-breaking risk if
rushed — and until it lands, these repos are covered by layers 3 and 4.
When they do adopt the rule, koto's classic branch protection is superseded
rather than patched, avoiding the `enforce_admins` default-open trap, and
tsuku's and shirabe's existing rulesets gain the `pull_request` rule
alongside their current rules.

This layer is configuration applied to live repositories, not a diff, so the
implementation plan must state how it is applied (the rulesets API), how it
is verified (query `current_user_can_bypass` and attempt-free inspection of
the resulting ruleset), and how it is reverted (delete the ruleset by id).

### Layer 3: the tripwire

A new reusable workflow in shirabe, following the `pr-body` template: a
`workflow_call` trigger, an "Example caller" comment block as the adoption
doc, in-org callers pinned `@main`. Shape of the check, wrapped immediately
around the caller-supplied test command rather than the whole job (so steps
that legitimately produce diffs, like tidy checks, are not caught):

```
before=$(git rev-parse HEAD)
<test command>
after=$(git rev-parse HEAD)
[ "$before" = "$after" ] || fail "test step moved HEAD"
[ -z "$(git status --porcelain)" ] || fail "test step dirtied the checkout"
```

`rev-parse`, not `symbolic-ref`, because `actions/checkout` checks out a
detached SHA and a symbolic-ref guard would be red on every run. Every repo
in the organization adopts it with one caller file — for the six repos that
currently run no build pipeline, that one file is the entire adoption cost,
matching how `pr-body` already reached all ten repos.

This layer is honestly a detector. By the time the comparison runs, the
dangerous command has already executed; what the tripwire buys is a loud red
job instead of a silent success, and coverage for the private repositories
and for failure shapes no prevention layer anticipated. It is the backstop,
not the fix.

### Layer 4: CI credential hygiene

niwa's test workflow sets `persist-credentials: false` on the
`actions/checkout` step of the job running the functional suite, and gives
that job `permissions: contents: read`. By default `actions/checkout` writes
its token into `.git/config`, and niwa's workflow currently declares no
`permissions:` block — so a test escaping inside CI would find a writable
token exactly where a `git push` looks for one. This layer removes that
amplifier. The incident itself happened on a developer machine, where this
layer does nothing; it exists because the same suite runs in CI on every PR,
and the CI copy of the accident would otherwise be strictly worse.

## Implementation Approach

Five work items, one PR each, ordered so the highest-value protection lands
first and nothing waits on a review that has not happened:

1. **niwa: fix the functional suite** (layer 1). The `TestMain` sandbox,
   per-scenario children, `workspaceRoot` relocation, `fixtureGit` routed
   through all nine call sites, the startup assertion, the Makefile `flock`,
   and the doc update. Closes the code half of tsukumogami/niwa#249.
2. **niwa: harden the CI blast radius** (layer 4). Two small workflow edits,
   independent of everything else.
3. **shirabe: add the tripwire workflow** (layer 3, host side). One new
   reusable workflow following the `pr-body` template, its "Example caller"
   block serving as the adoption doc.
4. **Every repo: adopt the tripwire** (layer 3, caller side). One caller
   file per repo, `@main`-pinned, including the private repositories and the
   repos with no other CI.
5. **Rulesets on niwa, dot-niwa, and .github** (layer 2). Applied via the
   rulesets API with an empty bypass list; verified by reading back the
   ruleset and confirming `current_user_can_bypass` reports no bypass;
   reverted, if needed, by deleting the ruleset. The only item that would
   have prevented the incident by itself, and the only one that is
   configuration rather than a diff.

Recorded as follow-ups, not silently dropped: moving the release push on
koto, tsuku, and shirabe from the owner's `RELEASE_PAT` onto a GitHub App or
machine account so those repos can take the ruleset — blocked on standing up
that identity without breaking a release; the GitHub Team upgrade
(Decision 5); koto's fixed `/tmp/koto-test-*` sentinel files, a real but
unrelated fixed-shared-path hygiene issue; and aligning
`worktree_delegation_steps_test.go`'s direct path construction with the
`.git`-existence check its sibling helper already performs.

## Security Considerations

- **Prevention versus detection, stated plainly.** Layers 1 and 2 prevent:
  the sandbox and ceiling make the dangerous git command fail before it
  runs, and the ruleset makes the push bounce at the server. Layers 3 and 4
  mitigate: the tripwire turns a silent escape into a loud CI failure after
  the commands already ran, and the credential hygiene shrinks what those
  commands could have reached. The design does not claim the tripwire stops
  anything — it shortens the blast radius and removes silence.
- **The bypass list is the security boundary of layer 2.** Granting bypass
  to the human owner would reopen the exact hole this design closes — the
  incident push carried the owner's own credentials — and today the release
  PAT *is* the human owner, which is why three repos wait for a separable
  automation identity rather than getting a bypass entry. The verification
  step in work item 5 exists to prove, not assume, that nobody can bypass.
- **Fail-closed behavior of the guards.** `fixtureGit`'s bounds check turns
  a fixture bug into an immediate error instead of an ambient-repo
  operation. The startup assertion turns a mislocated sandbox into a refusal
  to run. The one silent failure mode found — `GIT_CEILING_DIRECTORIES`
  ignoring relative paths without complaint — is covered by pinning the
  ceiling to an absolute path in one place and by the assertion backstop.
- **CI token exposure.** Without layer 4, any code execution inside the test
  job (a test bug or a compromised dependency alike) can read a
  push-capable token from `.git/config`. With `persist-credentials: false`
  and `contents: read`, the same execution finds no credential and holds no
  write permission. This also keeps layer 3 honest: a tripwire that fails a
  job is worth more when the job could not have pushed anything anyway.
- **Residual risk.** The private repositories remain without server-side
  protection until the plan upgrade, and koto, tsuku, and shirabe remain
  without it until the release-identity migration; their coverage meanwhile
  is the tripwire, the credential hardening, and the verified absence of
  the defect pattern in their test code today. A developer-machine escape
  in some future suite would be stopped by layer 2 only on the repos
  carrying the ruleset — that is the honest edge of this design, and the
  two recorded follow-ups are the path to closing it.

## Consequences

### Positive

- The known defect is removed at the mechanism level, not patched at the
  three lines that fired: no shared paths, no reliance on upward discovery,
  and every present and future fixture git call bounded by one helper.
- The incident becomes impossible on the repos carrying the ruleset —
  starting with niwa, where it happened — regardless of any test bug: a
  direct push to `main` bounces at the server, even from an admin, even
  from an org owner.
- Tests that do not exist yet are covered twice — the ruleset rejects their
  pushes on public repos, and the tripwire exposes their side effects
  everywhere, private repositories included.
- Adoption cost matches the organization's established pattern: repos with
  no CI gain one file, and the shared logic lives in one place.

### Negative

- Direct pushes to `main` stop working for everyone, including the owner,
  on the repos carrying the ruleset. Every change now goes through a PR.
  This is the point, but it is a real workflow change on repos that
  previously allowed quick direct pushes.
- koto, tsuku, and shirabe get no server-side protection from this design:
  their release push authenticates as the owner's PAT, and no bypass list
  can admit it without also admitting the incident push. Mitigation: the
  release-identity migration is a named follow-up with its blocker stated,
  and those repos carry layers 3 and 4 meanwhile.
- The tripwire adds a small amount of runner time to every adopting repo,
  including repos with no test step that could misbehave today. Accepted:
  the marginal cost is seconds, and "no dangerous test today" is one
  fixture away from being false.
- Serialized functional-test runs via `flock` mean a second concurrent
  `make test-functional` in the same checkout waits instead of running.
  Accepted: concurrent runs in one checkout are exactly what caused the
  incident.

### Mitigations

- `NIWA_TEST_KEEP_SANDBOX` preserves the old sandbox's debuggability now
  that test artifacts live in a temp directory instead of beside the binary.
- The startup assertion backstops the ceiling's silent relative-path no-op,
  so a misconfiguration fails the suite loudly instead of disarming the
  guard invisibly.
- Work item 5's read-back verification and stated revert path keep the
  live-configuration change auditable and reversible.
