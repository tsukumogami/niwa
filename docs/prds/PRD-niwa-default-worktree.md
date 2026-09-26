---
schema: prd/v1
status: Done
problem: |
  In a niwa workspace, agent-initiated worktree creation can silently produce a
  second, competing bare worktree alongside niwa's managed one. The bare
  worktree lacks materialized secrets and context and is invisible to niwa, so
  the developer is left to reconcile orphaned worktrees, polluted git status, and
  branch collisions by hand. Nobody chose two systems; they just asked for a
  worktree.
goals: |
  Make niwa's managed worktree the single default worktree mechanism in a niwa
  workspace, installed by default on niwa apply, so agent-initiated worktrees are
  full niwa worktrees (one per task, known to niwa). Where the integration can't
  be honored, steer the agent to niwa's command rather than let it produce a
  silent degraded checkout.
upstream: docs/briefs/BRIEF-niwa-default-worktree.md
source_issue: 166
motivating_context: |
  A feasibility spike (docs/spikes/SPIKE-niwa-default-worktree.md) confirmed that
  agent-initiated worktree creation can be routed through niwa via per-repo
  worktree hooks, and that a per-repo install scope is required — a
  workspace-root-only install does not take effect for an agent operating inside a
  repo. This PRD captures the requirements now that feasibility and install scope
  are settled.
---

# PRD: niwa as the default worktree mechanism

## Status

Done

The downstream DESIGN owns the delegation mechanism (hook scripts, the input
adapter, removal reconciliation). This PRD owns the requirements and the
developer-facing contract.

## Problem Statement

niwa manages a multi-repo workspace and gives every managed worktree the same
environment a real checkout has: vault-resolved secrets, repo and workspace
context, and a session record it can list, attach to, and tear down. Claude Code
agents also create worktrees on their own — when a developer asks one to "work in
a worktree," or when the agent runs an isolated sub-task. Today those
agent-created worktrees are bare git checkouts placed inside the repo
(`.claude/worktrees/`), carrying none of what niwa materializes, and niwa has no
record they exist.

The result is two worktree systems for one repo, neither aware of the other. The
agent's bare checkout is a degraded environment — the secrets and context a
normal checkout has are missing, so the agent works without them and the failure
shows up late. Because niwa never recorded the bare worktree, the developer
untangles the divergence by hand: orphaned worktrees niwa's commands won't clean
up, an unexpected directory in `git status`, and branch collisions when both
systems reach for the same repo. The developer is the affected party, every time
an agent makes a worktree in a niwa workspace, and the cost is paid now on every
isolated task.

## Goals

- Agent-initiated worktree creation in a niwa workspace yields a full niwa
  worktree, with no separate competing bare checkout for the same task.
- The integration is on by default for every managed instance, with no
  per-developer manual setup.
- When the integration can't take effect, the developer gets an explicit redirect
  to niwa's worktree command rather than a silent degraded checkout.
- niwa stays the single system of record for worktrees: one worktree per task,
  all listed, all torn down cleanly.

## User Stories

- As a developer working with a Claude Code agent in a workspace repo, I want the
  agent's "work in a worktree" to give me a full niwa worktree, so that the
  isolated checkout has the same secrets and context as my real checkout without
  my doing anything special.
- As a developer, I want an agent's isolated sub-task to run in a niwa worktree
  even though I never asked for one, so that parallel agent work is faithful and
  leaves nothing orphaned.
- As a developer who runs niwa's worktree command directly, I want the agent's
  native tooling not to create a second worktree for the same task, so that the
  one worktree I made is the one the agent uses.
- As a developer on a host where the integration can't be honored, I want the
  agent steered to niwa's command instead of silently producing a degraded
  checkout, so that the worst case is an explicit redirect, not a quiet trap.
- As a developer with an unusual setup, I want to opt an instance out of the
  default, so that I can keep the agent's built-in worktree behavior when I have a
  reason to.

## Requirements

Functional:

- **R1.** In a niwa workspace, agent-initiated worktree creation — both the
  agent's interactive "work in a worktree" path and isolated sub-task worktrees —
  results in a niwa-managed worktree rather than a bare in-repo checkout.
- **R2.** niwa installs the worktree integration by default for every managed
  workspace instance, applied and refreshed on every `niwa apply`, with no
  per-developer manual setup.
- **R3.** niwa installs the integration at the scope required for it to take
  effect for an agent operating inside a repo, so the integration fires for
  agent worktree actions taken from within each workspace repo.
- **R4.** `niwa worktree create` exposes the created worktree's absolute path in a
  machine-readable form suitable for programmatic callers, so the integration can
  return the path to the agent as the session working directory.
- **R5.** When the integration is active, a single agent task produces exactly one
  worktree: the agent's native worktree path does not also create a separate bare
  checkout for that task.
- **R6.** Worktree teardown triggered by the agent is reconciled with niwa's
  lifecycle so no orphaned worktree directory or stale session record remains;
  niwa remains the system of record for active worktrees.
- **R7.** When the agent harness does not honor niwa's installed worktree
  integration — for example an older Claude Code version, or a harness without
  worktree-integration support — the agent does not silently produce a degraded
  bare worktree: instead it is steered to `niwa worktree create`. This
  harness-does-not-honor condition is the "fallback mode" referenced below.
- **R8.** Fallback mode is surfaced to the developer rather than silent: niwa
  discloses when an instance is operating in fallback mode (on the `niwa apply`
  output and via niwa's one-time-notice mechanism), and the agent's redirect to
  `niwa worktree create` is explicit. The mechanism by which niwa determines that
  the integration is not honored is deferred to DESIGN; this PRD requires only
  that the fallback be detectable and disclosed.
- **R9.** A developer can opt a workspace instance out of the integration at init
  time, persisted in instance state (consistent with the existing `--skip-global`
  and `--no-overlay` opt-outs). When opted out, niwa does not install the
  integration and the agent's built-in worktree behavior is left in place. The
  opt-out is reversible by re-initializing the instance without the opt-out flag;
  the next `niwa apply` then installs the integration.
- **R10.** When a delegated worktree is created but secrets cannot be fully
  resolved (for example a transient vault outage), creation continues
  (warn-and-continue) and the degradation is surfaced to the developer, not
  applied silently.

Non-functional:

- **R11.** Installing and refreshing the integration is idempotent and safe to
  re-run on every `niwa apply` and every `niwa worktree apply`; re-runs do not
  duplicate or corrupt installed content.
- **R12.** The integration requires no interactive shell or TTY to install — it
  applies through the normal non-interactive `niwa apply` path.
- **R13.** The feature targets Claude Code agents. Other agent harnesses are out
  of scope and must not be required for the feature to work.

## Acceptance Criteria

- [ ] After `niwa apply`, asking a Claude Code agent in a workspace repo to work
  in a worktree produces a niwa-managed worktree that (a) appears in
  `niwa worktree list`, (b) contains the materialized secret env file(s) the
  instance defines, and (c) contains the worktree's workspace/repo context files
  (the `.claude/rules/worktree-imports.md` import and repo CLAUDE content) — and
  no bare `.claude/worktrees/` checkout is created for that task.
- [ ] An isolated agent sub-task (worktree-isolated subagent) likewise runs in a
  niwa worktree with the same observables as above, not a bare checkout.
- [ ] A fresh workspace instance that was not opted out receives the integration
  on its first `niwa apply` with no manual per-developer setup; a repo added to
  the workspace later receives the integration on the next `niwa apply`.
- [ ] The integration is installed at per-repo scope: the agent worktree action
  fires the integration when taken from inside a repo, and a workspace-root-only
  install (no per-repo install) demonstrably does not fire for the same action.
- [ ] `niwa worktree create` emits the created worktree's absolute path in a
  documented machine-readable form a non-author can parse without scraping
  human-prose lines.
- [ ] Re-running `niwa apply` (and `niwa worktree apply`) leaves the installed
  integration unchanged and functional, with no duplicated entries.
- [ ] The integration installs through a non-interactive `niwa apply` with no TTY
  attached.
- [ ] With the harness forced into a state that does not honor the integration
  (e.g. an unsupported/older harness version), an agent worktree action is steered
  to `niwa worktree create`, no degraded bare worktree is produced, and niwa
  discloses fallback mode on the `niwa apply` output (a reviewer can grep the
  apply output / one-time-notice for the disclosure).
- [ ] An instance initialized with the opt-out flag does not receive the
  integration; the agent's built-in worktree behavior remains in place. Re-running
  init without the flag, then `niwa apply`, installs the integration.
- [ ] When secrets cannot be resolved during delegated worktree creation, creation
  completes and a warning naming the unresolved secrets is surfaced to the
  developer (not silent).
- [ ] After an agent tears down a delegated worktree, `niwa worktree list` shows
  no orphaned entry and the working tree has no leftover worktree directory.
- [ ] The feature does not require any agent harness other than Claude Code: with
  only Claude Code present, all of the above hold.

## Decisions and Trade-offs

- **Fallback is visible, not silent (closes brief Open Question 1).** When an
  instance can't honor the integration, niwa discloses the fallback (following the
  existing one-time-notice convention) and the agent's redirect to
  `niwa worktree create` is explicit. Alternative considered: a silent fallback.
  Rejected because silent degradation is the exact failure this feature exists to
  remove — a quiet fallback would re-introduce it.
- **Opt-out is an init-time instance flag, not a config toggle (closes brief Open
  Question 2).** The opt-out follows niwa's established pattern for
  apply-logic toggles (`--skip-global`, `--no-overlay`): set at init, persisted in
  instance state, carried forward on every apply. Alternatives considered: a
  declarative `[instance]` toggle in workspace.toml (rejected — that section is for
  config merges that materialize into output, not control-flow toggles) and no
  opt-out at all (rejected — an escape hatch is needed for unusual setups).
- **Secret resolution stays warn-and-continue, but surfaced (R10).** Delegated
  worktree creation keeps the current `AllowMissingSecrets` tolerance so a
  transient vault outage doesn't block an agent mid-task. Alternative considered:
  strict hard-fail on missing secrets (rejected — blocking the agent on a
  transient outage is worse than a surfaced, recoverable degradation). Silent
  warn-and-continue was also rejected for the same reason as the fallback: no
  silent degradation.
- **niwa worktree create must expose a machine-readable path (R4).** Today it
  prints only a human-oriented `session: created <id> at <path>` line. The
  integration needs the path programmatically, so a stable machine-readable form
  is a requirement; the exact form (a `--json` mode or a documented stable line)
  is a design choice.

## Known Limitations

- The integration depends on the agent harness honoring a per-repo worktree
  integration. On a host or agent version that doesn't, the feature degrades to
  the R7/R8 fallback (explicit redirect) rather than transparent delegation.
- Agent-side worktree removal is best-effort and may not block on niwa's teardown
  guards; niwa remaining the system of record (R6) is what keeps this safe. The
  reconciliation details are downstream design.
- Two creation attempts racing for the same branch (an agent and a developer at
  once) can still collide at the git layer; serializing or arbitrating that race
  is a design concern, not guaranteed by this PRD.

## Out of Scope

- The delegation mechanism's technical design — how agent worktree creation is
  routed into niwa, the input adaptation, and removal reconciliation. Downstream
  DESIGN.
- What a niwa worktree materializes and how the secret pipeline resolves values.
  That pipeline exists and is unchanged here.
- The internals of niwa's worktree lifecycle commands (create, destroy, attach,
  list) beyond R4's path-output requirement.
- Agent harnesses other than Claude Code.
- Cross-branch or cross-machine worktree resume.
- Cleaning up bare worktrees that already exist from before this feature was
  installed (pre-existing orphans). The feature prevents new divergence; migrating
  away from worktrees created under the old behavior is separate work.

## References

- docs/briefs/BRIEF-niwa-default-worktree.md — the framing this PRD operationalizes.
- docs/spikes/SPIKE-niwa-default-worktree.md — feasibility and install-scope findings.
- tsukumogami/niwa#166 — the enhancement issue that motivated the work.
