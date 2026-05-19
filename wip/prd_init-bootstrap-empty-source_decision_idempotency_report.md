<!-- decision:start id="init-bootstrap-idempotency" status="assumed" -->
### Decision: Behavior of `niwa init <name> --from owner/foo --bootstrap` when `<cwd>/<name>/` already exists

**Context**

The framing decision for `--bootstrap` chains init → create →
session-create as a single atomic command, with a per-step rollback
contract: init failure removes the workspace directory, create failure
leaves init's scaffold but removes the instance directory, session-create
failure leaves both intact and tells the user how to finish manually.
That contract makes three "partial state" sub-cases reachable on retry —
(b) full workspace from a prior successful bootstrap, (c) workspace
without an instance, and (d) instance without a worktree — on top of the
happy path (a, clean target).

Today's preflight at `internal/cli/init.go:411-453` refuses all three
non-clean sub-cases with sentinels (`ErrWorkspaceExists`,
`ErrNiwaDirectoryExists`, `ErrTargetDirExists`, `ErrRegistryNameInUse`)
and remediation hints written for the steady-state lifecycle:
"Use niwa apply to update the existing workspace," "Remove the .niwa/
directory and retry," "Pass --rebind to retarget the entry to this
directory." Those hints are correct in context for a user mid-workflow,
but they are actively misleading for a user whose first hands-on
exposure to niwa just failed partway through `--bootstrap`. The framing
decision is explicit that `--bootstrap` is "intro to niwa," which makes
the retry-after-failure message a load-bearing UX surface.

The decision lives on two axes: messaging quality (does the refusal
text match the bootstrap retry intent?) and recovery automation (does
bootstrap re-pick-up from the failed step?). `niwa destroy <name>`
already exists as the canonical "start over" verb — it removes both the
workspace directory and the registry entry in a single command — and
`niwa session create <repo> <purpose>` is exactly the third step of the
bootstrap chain, available standalone for sub-case (d).

**Assumptions**

- The rollback decision (parallel to this one) will leave at least
  sub-cases (c) and (d) reachable in practice. If wrong (full rollback
  on every failure): I3's bootstrap-specific text still helps in sub-case
  (b), so the assumption is non-load-bearing for the chosen option.

- Users are more likely to hit conflict during onboarding retries than
  after a successful bootstrap. If wrong: I3's "destroy and start over"
  guidance still applies in either direction, and the cost of being
  wrong is zero — the text reads the same regardless of how the user
  got into the conflict.

- `niwa destroy <name>` reliably cleans the registry entry across all
  three partial-state sub-cases. Confirmed by reading
  `internal/cli/destroy.go`: destroy classifies via the registry itself,
  not via workspace.toml parsing, so it can clean up even when the
  workspace.toml is missing or malformed.

- The minimal-blast-radius principle dominates the ergonomics-
  maximization principle for a v1 onboarding feature. If wrong: I2 or I4
  become more attractive; the chosen path (I3) does not foreclose
  layering I2-style resume on top later, since the pre-preflight
  injection point is the same.

**Chosen: I3 (Bootstrap-aware preflight with bootstrap-specific guidance)**

`niwa init <name> --from owner/foo --bootstrap` keeps today's refusal
behavior for all non-clean sub-cases but emits bootstrap-specific
`InitConflictError` text via a `--bootstrap`-gated branch at the call
site in `runInit`. The refusal text in all conflict sub-cases names
`niwa destroy <name>` as the start-over command and, where applicable,
`niwa session create <repo> bootstrap` as the resume-from-where-it-failed
shortcut:

- Sub-case (b) full workspace exists: "A niwa workspace named `<name>`
  already exists at `<path>`. To start over, run
  `niwa destroy <name>` and retry. To land in a fresh worktree
  without re-running init or create, run
  `niwa session create <repo> bootstrap`."
- Sub-case (c) `.niwa/` without `workspace.toml`: "A partial niwa state
  exists at `<path>/.niwa/`. To start over, run `niwa destroy <name>`
  and retry, or remove the `.niwa/` directory manually."
- Sub-case (d) instance exists without worktree: same message as (b)
  — the user's correct action is one of the same two commands.
  (No new state-introspection branch is needed; the workspace.toml
  presence check already routes (d) into (b)'s message.)
- Symlink, file, or non-niwa directory at the path: keep today's
  generic message but append the destroy-suggestion variant when a
  registry entry for `<name>` also exists.
- Registry-collision case: replace the existing "Pass --rebind" text
  with bootstrap-aware text that names `niwa destroy <name>` as the
  primary remediation. `--rebind` is still mentioned as an advanced
  option for users who know what they want, but it is no longer the
  first suggestion.

The pre-preflight does no state introspection beyond what today's
preflight already does, and performs no destructive or recovery action.
The user is always in control of state transitions: either they destroy
explicitly, or they skip ahead with a named session-create command.

On the happy path (a, clean cwd, no registry collision) the
bootstrap-aware branch is a no-op and the chain proceeds normally.

**Rationale**

I3 is the only option that improves the failure-mode UX without taking
on either silent-resume risk (I2) or destructive-prompt risk (I4), and
without leaving the actively-misleading remediation text from the
non-bootstrap lifecycle in place (I1).

The framing decision establishes that `--bootstrap` is the user's first
exposure to niwa. A first-time user whose run failed and who re-types
the same command needs a message that matches their intent ("how do I
start over?"), not the steady-state lifecycle messages ("use niwa
apply," "use --rebind"). I3 produces exactly that message at the lowest
implementation cost of any option that fixes the text.

The strongest competing case is I2: if sub-cases (c) and (d) are common
(because rollback is intentionally stepwise to preserve user clones),
auto-resume removes a manual step. But I2 trades that ergonomic win for
a silent-state-recovery risk that is hard to bound: detection at a
distance ("does workspace.toml exist? Does the instance dir exist?")
can misidentify a contaminated or unrelated prior state, and the resume
path silently runs more commands against a workspace shape the user did
not intend. For an intro feature, refuse-with-good-guidance beats
silent-resume-might-be-wrong. The injection point I3 establishes is the
same one I2 would use, so this decision does not foreclose layering
resume logic later if the rollback decision and post-v1 data show (c)
and (d) are common in practice.

I4 carries the largest blast radius: an interactive "Destroy and
re-bootstrap? [y/N]" prompt on a v1 onboarding command means a fat-
finger keypress can wipe a workspace with unpushed work. The existing
`niwa destroy --force` flow already gates on unpushed work for exactly
this scenario; replicating that gate inside a bootstrap-time prompt
re-implements it on a less-tested code path. Asking the user to type
`niwa destroy <name>` themselves keeps the destructive action behind
the same command that has been hardened for the steady-state case.

I1 looks superficially cheap but leaves the load-bearing UX bug in
place. The cost gap between I1 and I3 is one branch in `runInit` and a
text-string variant — well below the threshold where keeping the worse
behavior is justified by implementation savings.

**Alternatives Considered**

- **I1 (Inherit today's preflight unchanged)**: Refuses (b)/(c)/(d)
  with today's text. Rejected because the existing remediation hints
  ("Use niwa apply," "use --rebind") were written for the steady-state
  lifecycle and read as nonsense to a user whose first bootstrap
  attempt just failed. Keeping them undermines the framing decision's
  "intro to niwa" purpose at zero implementation savings versus I3.

- **I2 (Bootstrap-aware preflight with resume)**: Detects sub-cases
  (c) and (d) and resumes from the failed lifecycle step. Rejected
  because silent resume against on-disk introspection is too high a
  blast-radius for a v1 onboarding feature: detection at a distance
  can misidentify contaminated or unrelated state, and the user's
  mental model ("--bootstrap is one command that does the whole
  thing") breaks down if the command sometimes does only the last
  step and sometimes all three with no in-band signal of which.
  The injection point I3 uses is the same one I2 would; layering
  resume on top later remains possible if (c)/(d) prove common.

- **I4 (Resume + interactive destroy prompt)**: I2 plus a TTY-gated
  destroy-and-retry prompt. Rejected because the destructive prompt
  is mis-located: niwa already has `niwa destroy <name> [--force]`
  with battle-tested unpushed-work gating, and replicating that gating
  inside a bootstrap-time prompt re-implements safety checks on a
  less-tested path. The non-TTY fallback to I3's text makes most of
  I4's surface area dead code in CI environments. The fat-finger
  blast radius alone disqualifies this option for v1.

**Consequences**

What gets easier:

- A user whose bootstrap attempt failed partway through and who
  re-types the same command sees a message that names exactly the
  two commands relevant to their situation: `niwa destroy <name>`
  (start over) and `niwa session create <repo> bootstrap` (skip
  ahead). No prior niwa knowledge required.
- The registry-collision path stops directing onboarding users at
  `--rebind`, which is an advanced flag with security implications
  the user has no way to reason about on their first run.
- `niwa destroy <name>` becomes the documented "reset to clean
  slate" verb for bootstrap, which is a discoverable lifecycle
  command users will need for many other reasons later.

What gets harder:

- The PRD must specify the exact text of the bootstrap-aware
  Detail+Suggestion blocks for each conflict sub-case, including
  the registry-collision variant. The text is the entire UX payoff
  of this decision and must be reviewed at PRD-acceptance time.
- A small amount of test surface is added: each conflict sub-case
  needs a functional test that verifies the bootstrap-aware text
  fires under `--bootstrap` and the legacy text fires without it.
- The pre-preflight branch is a new place where bootstrap and
  non-bootstrap behavior diverge. The PRD must specify it as a
  pure messaging branch (no behavior change beyond text) to keep
  the divergence auditable.
- Sub-case (d) (instance exists, worktree missing) requires the
  user to manually run the named `niwa session create` command
  rather than re-running `--bootstrap` and having it auto-resume.
  This is a documented one-liner, but it is one more keystroke
  than I2 would require. The trade-off is intentional: explicit
  user action over silent state recovery.
<!-- decision:end -->
