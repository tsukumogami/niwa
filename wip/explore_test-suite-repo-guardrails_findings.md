# Explore Findings: test-suite-repo-guardrails

Round 1, five leads, all closed. Sources: the five files under
`wip/research/explore_test-suite-repo-guardrails_r1_lead-*.md`, plus direct
read-only inspection of the live organization state.

## The five open questions, answered

### 1. Does branch protection exist on the affected default branches?

**No, not on the branch that mattered.** `refs/heads/main` on niwa carried
neither classic branch protection nor a ruleset. The 403 seen from the
originating session was a token-scope artifact: the fine-grained PAT in the
environment lacks administration scope, while the account's OAuth token reads
the API fine. Running `gh` with the PAT unset answers every protection query.

Live posture across the organization:

| Repo | Server-side protection today | Would it have stopped the incident? |
|---|---|---|
| niwa | none | no |
| .github | none | no |
| dot-niwa | none | no |
| tsuku | ruleset: deletion, non_fast_forward, copilot_code_review | **no** |
| shirabe | ruleset: required_linear_history, deletion, non_fast_forward | **no** |
| koto | classic: required check `validate`, enforce_admins **false** | **no** |
| 4 private repos | impossible on the current plan | n/a |

The three repos that look protected are not protected against *this*.
`non_fast_forward` blocks force-pushes; the incident push was a clean
fast-forward. `required_linear_history` objects to merge commits; the bad commit
was linear. And koto's required status check is bypassed outright because
`enforce_admins` is false and the pushing identity had admin permission.

Only three rule types actually reject a direct fast-forward push of a
never-checked commit: **`pull_request`**, **`required_status_checks`**, and the
ruleset **`update`** rule.

### 2. What is the right org-wide mechanism?

No single mechanism reaches every repo. Two hard constraints force a layered
answer:

- **The organization is on the GitHub Free plan.** Org-level rulesets require
  Team. Branch protection *and* rulesets on private repositories require Team.
  So the four private repos cannot carry any server-side push protection today,
  at any scope, for any amount of configuration effort.
- **A rule that blocks all direct pushes also blocks the release pipeline.**
  Every non-PR commit on `main` across koto, tsuku and shirabe is
  `github-actions[bot]` running release automation, and the shared release
  workflow pushes commits straight to the release branch
  (`shirabe/.github/workflows/release.yml:239`, `git push origin HEAD:<ref>`).
  That workflow already anticipates this: line 98 prints "If this repo has
  branch protection, pass a PAT via the token secret."

The bypass list is therefore load-bearing and must be chosen carefully. Giving
bypass to the **human owner** would have permitted the incident push exactly as
it happened, since that push used the owner's own credentials from a laptop.
Bypass must go to the automation identity only.

### 3. Is niwa's functional suite the only place a test can reach a real repo?

**Today, yes.** The occurrence counts in the brief overstated the spread. Read
site by site:

- **niwa** `test/functional/` is the only code in the organization that combines
  a fixed non-temp path inside a real checkout, an unguarded wipe-and-recreate,
  and git commands that rely on upward discovery. Nine call sites across four
  files share the defect, not the three that fired.
- **niwa's own unit tests** are all `t.TempDir()`-rooted and safe.
- **koto** has a godog suite of the same shape, but rooted at
  `os.MkdirTemp("", "koto-func-*")` — outside any checkout, so there is nothing
  to climb into. Safe today, and structurally one refactor away from not being.
- **shirabe** builds throwaway repos in temp dirs and passes explicit `-C`. One
  test runs `git status --porcelain` against the real checkout, but read-only.
- **tsuku** invokes git in no test at all. Its 15 `RemoveAll` hits are either the
  unrelated `RemoveAllVersions` domain method or temp-dir-rooted. Its functional
  suite already gives each scenario its own `MkdirTemp` home — the correct
  pattern, arrived at independently, one repo over from where it was needed.
- **dot-niwa, .github** have no test code.
- **Private repos**: temp-dir-rooted or marker-gated throughout; two have no test
  code at all.

This is the finding most in tension with the brief's framing, and the tension
resolves rather than persists. The *defect* is niwa-shaped. The *guarantee the
user asked for* — that this can never happen to any repo in the organization —
cannot be satisfied by fixing niwa, because it is a claim about tests that
nobody has written yet. A guarantee about future code has to live somewhere
other than the code it is making a promise about.

### 4. Can the guard fail closed in CI as well as locally?

Yes, and the distinction between prevention and detection is sharp.

**Prevention** (fires before the dangerous command runs, identical semantics
locally and in CI, no GitHub feature involved, so the Free-plan private-repo gap
does not apply):

- `GIT_CEILING_DIRECTORIES` — verified experimentally to stop upward discovery
  dead. Verified gotchas: it does not blind git to the ceiling directory
  itself, only to traversal past it; and **relative paths are silently ignored**,
  which makes a misconfiguration a no-op rather than an error.
- Explicit `GIT_DIR`/`GIT_WORK_TREE` — stronger per call, weaker as a net,
  because it only protects call sites that remember to use it. That is precisely
  how the incident happened.
- A startup assertion that the sandbox root has no `.git` ancestor — one check,
  one place, covers call sites that do not exist yet.

**Detection** (fires after, shortens the blast radius rather than preventing):

- A CI tripwire comparing `git rev-parse HEAD` and `git status --porcelain`
  before and after the test step. This is the only mechanism that needs no
  foreknowledge of *how* a future test misbehaves. Note it must use
  `rev-parse`, not `symbolic-ref`: `actions/checkout` leaves a detached HEAD, so
  a `symbolic-ref` guard would be permanently red.

**Rejected — fingerprint, not mechanism.** Pre-push hooks keyed to
`niwa-test <niwa-test@example.com>`, a global `core.hooksPath`, and PR-commit
identity scanning all fail the "a new test written tomorrow" bar, because they
assume the next accident carries the same author string. Two further defects:
hooks are not distributed by `git clone` (verified: a fresh clone has only
`.sample` files and no `hooksPath` setting), so CI and every fresh agent
checkout start disarmed; and a PR-commit scanner never sees a push that skips
PR review, which is exactly the shape of what happened.

**One amplifier worth closing.** `actions/checkout` persists credentials in
`.git/config` by default, and niwa's CI runs `make test-functional` with no
`permissions:` block. An escape inside CI would therefore have a writable token
sitting right there. `persist-credentials: false` plus `permissions: contents:
read` on the test job removes the credential from the blast radius.

### 5. One PR per repo, or niwa plus a follow-up?

The organization already has the distribution mechanism this needs and has
already normalized universal adoption: `pr-body.yml`, a shirabe-hosted
`workflow_call` workflow, is adopted by **all ten repos**, including the four
that have no other CI at all. Adoption cost for a repo with no build pipeline is
a single file with one `uses:` line.

So: one PR per repo, but they are not equal in size. niwa's is a real code
change; shirabe's adds the shared workflow; the rest are a few lines each.

## What this converges on

A layered guardrail, because no layer covers everything:

1. **Fix the defect in niwa.** Per-process sandbox allocated in `TestMain` via
   `os.MkdirTemp("", "niwa-func-")`, a fresh child per scenario instead of
   wiping a shared fixed path, `workspaceRoot` folded inside it (which closes
   the `/tmp/niwa-test-workspaces` cross-checkout hazard for free), one
   bounds-checked `fixtureGit` helper pinning `GIT_DIR`/`GIT_WORK_TREE`/
   `GIT_CEILING_DIRECTORIES` routed through all nine call sites, a `flock` in the
   Makefile, and no more discarded `RemoveAll` errors.
2. **Make the branch unwritable by anything but a reviewed PR**, on the six
   public repos where the plan permits it. A ruleset with the `pull_request`
   rule, bypass granted to the release automation identity and to nothing
   else. This is the layer that would have stopped the incident regardless of
   the test bug, and it keeps working for tests nobody has written yet.
3. **Ship the generic tripwire as a shared reusable workflow** that every repo
   adopts, including the private four that cannot have protection. This is the
   layer that covers the plan gap and the future-test gap.
4. **Take the credentials out of the CI blast radius** with
   `persist-credentials: false` and least-privilege `permissions:`.

Rejected, with reasons: a shared *test helper module* other repos import (nobody
else needs it; koto, shirabe and tsuku already independently use the right
pattern); identity-based push rejection (fingerprint, not mechanism, and blind
to the direct-push shape); relying on the `.github` repo to distribute a check
(it cannot — an org `.github` repo supplies fallback community-health files and
opt-in workflow *templates*, and can force nothing onto another repo's PRs);
niwa's own `[files]` distribution (deliberately writes `.local`-suffixed,
gitignored files, so it never reaches a CI runner).

Deferred to a follow-up issue, not silently dropped: upgrading to GitHub Team
(~$4/user/month) to unlock org-level rulesets and private-repo protection is the
only way to close layer 2's gap on the private repos, and it is a spending
decision that is not ours to take. koto's fixed `/tmp/koto-test-*` sentinel
files are a real but unrelated hygiene nit.

## Decision: Crystallize
