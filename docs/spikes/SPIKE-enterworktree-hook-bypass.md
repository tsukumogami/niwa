---
status: Complete
question: |
  Why does Claude Code's EnterWorktree tool create a native git worktree that niwa
  cannot see -- instead of delegating to niwa's installed WorktreeCreate hook -- when
  an agent isolates inside a nested-git niwa workspace, and which direction should
  close the gap?
timebox: "1 session: live reproduction on the installed harness + source trace; fix decision deferred to a design revision"
---

# SPIKE: EnterWorktree bypasses niwa's worktree hook in a nested-git workspace

## Status

Complete

Answered by live reproduction on the installed harness (Claude Code 2.1.215) plus a
source trace. **Key result:** this is not a workspace-config mistake -- it is a
Claude Code harness-behavior change that falsified a load-bearing assumption in the
shipped worktree-delegation design (`docs/designs/current/DESIGN-niwa-default-worktree.md`,
Decision 4). On 2.1.215 niwa installs a `WorktreeCreate` hook that can never fire and
never trips its own fallback, silently re-introducing the bare worktree the feature
existed to remove. The fix is a design-level correction (see Recommendation); three
small validation questions (Q1-Q3) are handed to that design.

## Question

In a niwa workspace, `niwa apply` installs a Claude Code `WorktreeCreate` /
`WorktreeRemove` hook so that an agent's native "work in a worktree" action produces a
niwa-managed worktree (with secrets, workspace context, and a session record) rather
than a bare in-repo checkout. In practice, when an agent isolates while working inside
a nested repo, it gets a plain native `git worktree` under
`<repo>/.claude/worktrees/<name>` and niwa has no record of it. Why is the hook
bypassed, and which direction should close the gap?

## Context

The worktree-delegation feature (issue #167, landed 2026-06-20) rests on a feasibility
spike (`docs/spikes/SPIKE-niwa-default-worktree.md`) that exercised the hook against
Claude Code v2.1.183 and concluded the per-repo `.claude/settings.local.json` hook
fires from inside a git repo and replaces native worktree creation (that spike's
Experiment C). The design (`docs/designs/current/DESIGN-niwa-default-worktree.md`)
installs the hook per-repo and adds an apply-time `claude --version` support probe:
at/above the known-good baseline it installs the hook; below it, it installs a
`permissions.deny: ["EnterWorktree","ExitWorktree"]` + steer-to-`niwa worktree create`
fallback (Decision 4). The two are mutually exclusive.

A niwa workspace root is deliberately not a git repository -- each managed repo is
nested one level down. The apparent intent was that isolation would flow through the
hook. The observed behavior contradicts that, and this spike establishes why.

## Approach

1. Reproduce the native-vs-hook trigger on the installed harness, from both a non-git
   directory and inside a nested repo, and record the exact deciding factor.
2. Trace the niwa side: where the hook is emitted, what `niwa worktree from-hook`
   would have done, and where the support probe decides hook-vs-fallback.
3. Compare the installed harness version against the design's baseline and against the
   documented `EnterWorktree` behavior to explain the discrepancy with the spike.
4. Map the alternatives with trade-offs and code-surface effort; recommend a direction.

## Findings

### 1. The deciding factor is an upward git-repo discovery walk from the live cwd

`EnterWorktree` chooses its path by walking up from the live session working directory
(which tracks the shell, not a fixed session root) looking for an enclosing git
repository:

- **Enclosing repo found** -> it creates a native `git worktree` under
  `<repo>/.claude/worktrees/<name>` on a new branch and never consults the hook.
- **No enclosing repo found** -> it looks for a `WorktreeCreate` hook in that cwd's
  settings scope. If one is configured, it delegates; if not, it errors.

Reproduced live on Claude Code 2.1.215, both ways:

- cwd inside a nested repo -> a native worktree was created (its `.git` file points to
  `<repo>/.git/worktrees/<name>`; `git worktree list` shows it `locked`), and
  `niwa worktree list` shows zero sessions. This matches the originally observed
  behavior exactly.
- cwd at the true non-git workspace root -> `Cannot create a worktree: not in a git
  repository and no WorktreeCreate hooks are configured.`

Because an agent working in a workspace is almost always cwd'd inside a repo (that is
where the code is), the enclosing-repo branch is the common case, and the hook is never
reached.

### 2. The hook is installed where it can never fire, and absent where it would

niwa emits the `WorktreeCreate` / `WorktreeRemove` hook into every nested repo's
`.claude/settings.local.json` (`internal/workspace/materialize.go:698-708`, threaded
per repo at `internal/workspace/apply.go:1442`). It never emits it into the non-git
workspace-root `.claude/settings.json` -- that document is built separately and
receives only the SessionStart hook (`internal/workspace/root_materializer.go:241-260`).
So the hook is present exactly where the native path preempts it (inside repos), and
absent at the one cwd where `EnterWorktree` would actually consult it (the non-git
root). It is dead on both branches.

### 3. Root cause: a falsified monotonic-support assumption (harness regression)

The installed harness is Claude Code 2.1.215; the design's known-good baseline is
2.1.183 (`internal/workspace/harness_compat.go:17`). The support probe is monotonic --
"at or above baseline => supported" (`harness_compat.go:56`) -- so 2.1.215 reports
supported, niwa installs the per-repo hook, and the deny+steer fallback (which only
fires when unsupported) never triggers.

But the harness behavior changed after 2.1.183. The feasibility spike's Experiment C
observed the per-repo hook firing from inside a git repo on v2.1.183; on 2.1.215 the
current `EnterWorktree` contract is the opposite -- the hook is consulted only when no
enclosing git repo is found, and inside a repo the native path is taken unconditionally.
The design encoded the now-false assumption verbatim in Decision 4: "an unsupported
harness stays unsupported across applies"
(`docs/designs/current/DESIGN-niwa-default-worktree.md:150`). The probe only models the
below-baseline direction of "unsupported"; it has no way to express "above baseline but
support was later removed." `harness_compat.go` has been frozen since the feature landed.

Two ironies worth carrying into the design:

- Decision 4 rejected "lazy post-hoc detection (observe whether the hook fired)" because
  it "only triggers after a user already got a bare worktree" (`DESIGN-...:160-162`) --
  which is exactly the failure now occurring under the version-pin approach it chose
  instead. Post-hoc detection is the one method that would have caught this.
- The design's stated mitigation for release coupling -- "update the constant in a patch
  if hook behavior changes" (`DESIGN-...` Consequences) -- only covers a raised floor,
  not a ceiling; it cannot express "hook works in [2.1.183, X) but breaks at >= X."

### 4. This silently violates the accepted PRD

Under 2.1.215 the agent gets a bare in-repo checkout niwa cannot see. Against
`docs/prds/PRD-niwa-default-worktree.md`: R1 (agent creation must yield a niwa
worktree), R5 (one worktree per task, no separate bare checkout), R7 (a
harness-does-not-honor condition must steer to `niwa worktree create`, not silently
degrade), and R8 (fallback must be surfaced) are all violated, along with the
single-system-of-record goal. R10 (secret resolution inside a delegated worktree) is
moot because delegation never happens; R9 (the init-time opt-out) is unaffected. This
is precisely the silent-degradation failure the feature was built to remove. No open
issue tracks it (the nearest, #170, is an unrelated `niwa worktree create` bug).

### 5. What `niwa worktree from-hook` would have done, and a useful asymmetry

Had it been reached, `niwa worktree from-hook` (WorktreeCreate) reads the hook's stdin
JSON, resolves the owning repo from `cwd`, creates a niwa-managed worktree under
`<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` on a niwa-chosen branch, installs
secrets + workspace context, writes a session-lifecycle state file, and echoes the
path back to Claude. Note the path asymmetry: niwa worktrees live under
`.niwa/worktrees/*`, native ones under `.claude/worktrees/*`. They are distinguishable,
which matters for any post-hoc reconciliation option.

### 6. Alternatives and code-surface effort

| Option | What it does | Effort | Key trade-off |
|--------|--------------|--------|---------------|
| A. Workflow guidance only | Tell agents to isolate from the non-git root | trivial | Doubly broken: errors at the true root (no hook there) and any `cd` into a repo flips it to native. Non-functional alone. |
| B. Install the hook at the root | Also emit the hook into the non-git workspace/instance root settings | small (code) | Efficacy narrow and unconfirmed: agents are steered into the instance/repo, so a root hook is often not the discovery scope. Needs Q2 confirmed. |
| C. `niwa worktree adopt` | Register a pre-existing native worktree as a niwa session after the fact | medium | Content-install is already path-agnostic and idempotent, but session creation always runs `git worktree add`, so adopt needs a new command + repo/branch readback + tolerating worktrees outside `.niwa/worktrees/`. Reactive. |
| D. Fix the probe -> deny+steer | Detect the regressed harness; fall back to the deny+steer the design already builds per-repo | small | Gives up transparent delegation (agent is blocked and redirected to `niwa worktree create`), but restores R7/R8 correctness cheaply because the deny machinery already exists (`materialize.go:601-606`). Needs Q1 confirmed. |
| E. Non-monotonic detection | Make support a range or empirical, not a version floor | small (ceiling) / small-medium (post-hoc) / large (apply-time self-test) | Ceiling is brittle (chase every release); post-hoc reflects reality but is reactive; a real self-test has no existing harness to spawn/inspect. |
| F. Accept native worktrees | Scope niwa's lifecycle to root-level ephemeral instances; drop the hook expectation and the now-misleading wiring | low | Abandons "one mechanism, not two" (PRD R1/R5); interactive in-repo isolation stays niwa-invisible. Plausible only because background-worker isolation already uses ephemeral instance clones, not worktrees. |

The options compose rather than compete: the realistic shape is D+E for correctness now,
then a design decision on the detection method and whether B (or C) can restore true
delegation.

## Recommendation

**Go: treat this as a correctness regression and route to a revision of
`docs/designs/current/DESIGN-niwa-default-worktree.md` (Decisions 4 and 6).** The
recommended direction is layered:

1. **Restore correctness first (small).** Make harness detection non-monotonic so
   2.1.215 is recognized as not honoring the in-repo hook, which flips niwa to the
   deny+steer fallback it already installs per-repo. This satisfies PRD R7/R8 (no
   silent bare worktree; disclosed fallback) with minimal new code -- it re-triggers
   existing machinery rather than building new machinery.
2. **Choose a detection method (design decision).** Weigh a version ceiling (cheap,
   brittle) against post-hoc detection (reactive but reflects reality -- worth
   reconsidering now that the pure version pin has failed once) or a hybrid. Replace or
   augment the version-floor probe.
3. **Decide the delegation model (larger design question).** Determine whether
   transparent delegation is still achievable at all under current `EnterWorktree`
   semantics. If yes, pursue an instance/workspace-root hook install (Option B), gated
   on Q2 and a workflow that keeps an isolating agent's cwd at the non-git root. If not,
   accept deny+steer as the primary mode (a deliberate scope reduction from the shipped
   intent) and optionally add `niwa worktree adopt` (Option C) to reconcile native
   worktrees that slip through.

### Validation questions for the design revision

- **Q1.** Does the per-repo `permissions.deny:["EnterWorktree","ExitWorktree"]` actually
  block `EnterWorktree` when the cwd is inside the repo on 2.1.215? (Very likely, since
  an in-repo cwd resolves that repo's settings; untested here.)
- **Q2.** Does a `WorktreeCreate` hook installed at the non-git workspace-root /
  instance-root `settings.json` fire when `EnterWorktree` is invoked with cwd at that
  non-git root? (The root-cwd error message suggests root-scope settings are consulted;
  needs a positive test.)
- **Q3.** Exactly which Claude Code version in (2.1.183, 2.1.215] changed the behavior?
  (Needed to set any version ceiling.)

These are small, targeted validations -- not blockers on the direction. No further
open-ended investigation is needed before the design revision.
