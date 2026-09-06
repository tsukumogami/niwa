# /scope Handoff: worktree-setup-scripts

## Provenance

Written by `/explore` on 2026-09-06 from
`wip/explore_worktree-setup-scripts_crystallize.md`. Research files:
`wip/explore_worktree-setup-scripts_findings.md` and
`wip/research/explore_worktree-setup-scripts_r1_lead-*.md` (four leads: hook
points, design record, cost and correctness, precedent and blast radius).

One discover-converge round, run against `origin/main` = `d25ad4d`. The
exploration narrowed twice along the way. It started from "should `RunSetupScripts`
also run on worktrees" and moved off that once `worktree-hooks/` turned out to
already execute scripts against worktrees, which reframed the gap as
repo-authored-vs-config-authored provenance rather than data-vs-execution. It then
moved off "turn it on for every repo" once the only first-party setup script in the
workspace turned out to be one that would be harmed by running per-worktree.

## Problem Statement

`niwa apply` runs a repo's own `scripts/setup/` against the clone it materializes,
and nothing runs them against a worktree. A worktree therefore arrives with the
repo's tracked source and none of its dependency tree — no `node_modules`, no
`.venv` — while receiving every other thing a clone gets. The failure is not merely
inconvenient, it is illegible: the second-order symptom reported from the field was
a vite error naming a package's `exports` field, which reads as a broken package and
points nowhere near provisioning. The gap is undeclared rather than deliberate:
`DESIGN-worktree-command-parity.md` enumerates setup scripts in the pipeline it set
out to mirror, states "gaps should be deliberate", marks exactly one gap deliberate
(`attach`/`detach`), and never mentions setup scripts again except to claim the
result needs "no manual setup for agents launched there".

## Scope Boundary

### In scope

- Repo-provided `scripts/setup/` reaching worktrees, on some set of the surfaces
  that call `ApplyToWorktree`.
- Which surfaces: `worktree create`, `worktree apply`, the agent-facing
  `WorktreeCreate` hook, and Step 6.6's per-apply fan-out are four distinct answers
  with different cost profiles.
- The opt-in/opt-out switch and where it lives in config, given that
  `setup_dir = ""` governs the clone run and cannot be reused.
- What a setup script may assume about its working directory, published as a
  contract rather than left implicit.
- Failure posture for a half-provisioned worktree, including the `from-hook`
  rollback interaction.
- Narrowing the `docs/guides/worktree.md` sentence promising `worktree create`
  needs "no network access".

### Out of scope

- Any change to `RunSetupScripts`' existing clone behavior. It needs none: it never
  touches git and is already parameterized on `repoDir`.
- Codex directory trust, which is the only other cell missing from the
  clone/worktree parity table. Separate owner, separate issue.
- The two silent-divergence bugs found alongside (shell auto-cd, unconsumed
  worktree-hook events). Filed separately; neither blocks this.

## Decisions Already Settled

- **The gap is real and should be closed in niwa.** Settled on
  `DESIGN-worktree-command-parity.md`'s own standard (undeclared gap) and on
  `DESIGN-niwa-default-worktree.md` Decision 9, which closed a strictly weaker,
  *latent* clone/worktree divergence on principle.
- **The data-vs-execution objection is dead.** `runWorktreeHooks` already executes
  scripts in worktrees on create and apply. The live distinction is provenance.
- **`worktree-hooks/` alone is not the answer**, though it is a real lever and is
  what the field workaround already uses. It lives in the config repo and fires for
  every repo, so per-repo provisioning becomes an `NIWA_WORKTREE_REPO` switch in a
  foreign repository, duplicating what each repo's own `scripts/setup/` already does
  for its clone.
- **The default must be off, per repo.** The only first-party setup script in this
  workspace writes to the instance root and would be silently miscompiled into a dead
  path if run per-worktree, at exit 0.
- **Failure must be data, not error.** If a setup failure counted as a create
  failure, the `from-hook` path's rollback would destroy every agent worktree in a
  repo with one flaky script.
- **The offline promise gets narrowed, not designed around.** It is an unenforced doc
  sentence already inaccurate for the sibling clone path.

## Coverage Notes

The exploration deliberately did not settle:

- **Which surfaces run it.** Create-only avoids Step 6.6's N-worktrees-per-apply
  multiplication; every-apply matches the existing worktree-hook behavior and the
  idempotency driver. Both are defensible and the cost data does not decide it.
- **How a script learns where it is.** `worktree-hooks` scripts already get
  `NIWA_WORKTREE_PATH`/`_REPO`/`_PURPOSE`/`_BRANCH`; `RunSetupScripts` sets no
  `cmd.Env` at all, and `DESIGN-post-clone-scripts.md`'s security section reasons
  explicitly about that absence. Reusing the existing four-variable shape is the
  obvious move but changes a property that design relied on.
- **Whether one per-repo switch is enough**, given that a single `scripts/setup/`
  can hold both a `node_modules` install (must run per-tree) and a git-hooks
  installer (must not, since hooks live in the shared `git-common-dir`).
- **Whether the `WorktreeCreate` hook entry should gain a timeout** regardless.
  It currently carries none while the SessionStart entries set 180s deliberately.
- **Whether `SetupResult` needs a target-directory field** so a warning naming
  `<repo>-<sid>` is intelligible. `RunSetupScripts` derives its display name from
  `filepath.Base(repoDir)`, which on a worktree is `<repo>-<sid>`, not `<repo>`.

## Upstream Observations

`docs/designs/current/DESIGN-post-clone-scripts.md` is the setup-script design.
Its Decision B settles the exit-code question for the clone case with an argument
that transfers directly — the shell wrapper's `cd` is gated on exit 0, so a fatal
setup failure strands the operator outside the directory they need to enter to fix
the script — and it specifies a deferred `setup_policy = "warn" | "fail"` key on the
same config structs, explicitly so that adding it later is additive. Its
idempotency contract exists only as a decision-driver bullet and appears in no
user-facing documentation at all.

`docs/designs/current/DESIGN-worktree-command-parity.md` is the design that created
`ApplyToWorktree` and the document the gap actually belongs to.

`docs/designs/current/DESIGN-niwa-default-worktree.md` Decision 9 is the precedent
for closing a clone/worktree divergence.

`docs/guides/worktree.md` documents `worktree-hooks/` and carries the network
promise that needs narrowing.

No ROADMAP found; nothing to pass on `--upstream`.

## Framing-Shift Answer

**Pre-supplied answer:** yes, the framing shifted.

**Evidence:** the exploration opened on "should `RunSetupScripts` run on worktrees",
which frames the gap as data-sync versus execution. Two findings moved it. First,
`runWorktreeHooks` already executes scripts against every worktree on create and
apply, and its own source comment calls itself "Analog of the instance setup-script
run" — so the boundary is not execution, it is provenance, and the missing cell is
specifically repo-authored scripts. Second, the naive fix is silently wrong on the
one first-party setup script that exists here, which turns the problem from "run the
scripts" into "publish the contract those scripts run under, then run them where it
holds". The success criterion moved with it: not "a worktree has `node_modules`" but
"a worktree is provisioned exactly where the repo says it should be, and visibly not
where it says it shouldn't".

## Shape Signals

### Architectural alternatives left open

- **Insert inside `ApplyToWorktree` beside `runWorktreeHooks`
  (`worktree_content.go:712`).** The only site holding `cfg`, `repo`, `group`,
  `instanceRoot` and `worktreePath` at once, same package as `RunSetupScripts`, and
  reaches all five callers with one insertion — including Step 6.6's fan-out, so no
  enumeration needs extracting. Cost: it fires on all five, so an expensive script
  runs once per live worktree per apply unless gated.
- **A new step in `apply.go` after Step 6.75.** Best inputs — the pipeline's
  `Reporter` and `redactor` are already in scope, and setup-as-data is the
  established posture there. Cost: covers only apply, does nothing for
  `worktree create` where a bare worktree is actually born, and needs
  `refreshWorktreeEnvs`' inline four-guard enumeration (`apply.go:2394-2440`)
  extracted first.
- **Extend `worktree-hooks/` to a repo-scoped tier.** Reuses a mechanism that
  already exports worktree context and already runs on create and apply. Cost: a
  second discovery surface and a second event vocabulary, and it inherits that
  mechanism's fatal failure posture, which is the wrong one here.
- **Do nothing in code; document `worktree-hooks/` as the answer.** Real, cheap, and
  what the field workaround already does. Cost: per-repo knowledge migrates into a
  foreign repo's switch statement and the repo-local convention stops working at
  exactly the point it is most useful.

### Complexity signals

- Six coupled decisions, not one: hook point, surface set, opt-in shape, failure
  posture, script environment contract, and whether to publish the idempotency
  contract. Settling any one without the others produces a defensible-looking wrong
  answer.
- `ApplyToWorktree` has 25 call sites in tests and 2 in production code, across 8
  test files including `internal/workspace/characterization_test.go` and
  `test/functional/worktree_delegation_steps_test.go`.
- Three existing failure postures on the worktree path already disagree with each
  other (create retains, `from-hook` rolls back, Step 6.6 warns and continues), and
  a fourth mechanism in the same function is fatal where its stated analog is not.
- The correctness hazard is silent by construction: the failure mode found here
  exits 0 and prints nothing.
- Cost is bimodal and ecosystem-determined — 0.29s versus ~13s and 341 MB per tree
  — so no single default is right for a mixed workspace.
