---
schema: design/v1
status: Planned
upstream: docs/prds/PRD-ephemeral-session-instances.md
problem: |
  Claude Code background sessions dispatched from a niwa workspace root share one
  working tree, so parallel agents collide. The PRD requires each dispatched session
  to run in its own ephemeral instance, created on start and torn down on end, with
  a reaper backstop -- and the spike fixed three constraints: teardown must key on a
  session->instance mapping (not cwd), SessionEnd is best-effort, and no native hook
  field distinguishes the coordinator from a worker.
decision: |
  Install workspace-root SessionStart/SessionEnd hooks (a new root-materialization
  surface) that delegate to a new `niwa session from-hook` subcommand. On start it
  detects a dispatched background-job session, runs `niwa create --json`, records a
  session->instance mapping under the root's `.niwa/sessions/`, and injects the
  instance context + a cd instruction. On end it looks the instance up by session id
  and `niwa destroy --force`s it. A `niwa reap` sweep reclaims orphans via
  `niwa list --json` plus a per-instance liveness marker. `niwa refresh` regenerates
  the root-managed config; `niwa init` installs it by default.
rationale: |
  The spike proved this end-to-end with no Agent SDK. A hook-backed Go subcommand
  mirrors the proven `niwa worktree from-hook` precedent, keeps logic testable, and
  avoids brittle shell. Keying teardown on a mapping is forced by SessionEnd's cwd
  being the launch root; the reaper is forced by SessionEnd being best-effort; the
  background-job guard is the one discriminator the spike surfaced.
---

# DESIGN: one ephemeral niwa instance per Claude Code session

## Status

Planned

This design owns the mechanism: the root-hook materialization surface, the
`niwa session from-hook` subcommand, the session->instance mapping store, the
reaper, the supporting `--json` / liveness primitives, and the `niwa refresh`
command. The upstream PRD owns the requirements (R1-R12); this design cites them
and does not re-open them.

## Context and Problem Statement

niwa creates multiple ephemeral instances of a workspace (`niwa create` ->
`tsuku`, `tsuku-2`, ...), each a full clone with its own `.niwa/instance.json`.
Claude Code's `claude agents` dispatches background sessions that inherit the
launch directory's cwd, so sessions fanned out from the workspace root share one
tree and collide. The PRD requires each dispatched session to run in its own
ephemeral instance, provisioned on `SessionStart` and reclaimed on `SessionEnd`,
with a reaper so orphans are bounded.

The spike (docs/spikes/SPIKE-ephemeral-session-instances.md) fixed the constraints
this design must honor:

- A session's cwd cannot be set at dispatch and a hook cannot relocate it, but
  `SessionStart` fires for dispatched sessions, inherits the launch cwd, and can
  inject `additionalContext`. The agent enters the instance with a Bash `cd`.
- `SessionEnd`'s reported cwd is the launch root, not the instance -- so teardown
  must resolve the instance from a `session_id` mapping, never from cwd.
- `SessionEnd` is best-effort (one of three observed sessions fired none) -- so a
  reaper is mandatory.
- The coordinator and workers are indistinguishable by `source`/`agent_type`; the
  one signal the spike surfaced is that dispatched workers run as background jobs
  (a `~/.claude/jobs/<id>` profile) where a foreground coordinator does not.

niwa already owns the analogous surface one level down: per-repo
`WorktreeCreate`/`WorktreeRemove` hooks delegating to `niwa worktree from-hook`,
a `.niwa/sessions/<id>.json` worktree-session store, a `.niwa/attach.state` PID
sentinel for liveness, and non-interactive `niwa destroy --force`. This design
lifts that established pattern from the worktree level to the instance level.

## Decision Drivers

- **Mapping, not cwd.** Teardown correctness depends on resolving the instance by
  session id (PRD R2, R4); cwd is the launch root and would target the workspace.
- **Best-effort SessionEnd.** "No orphans" can only be guaranteed by a reaper
  (R5), not by the end hook.
- **No native worker discriminator.** The guard (R6) must be engineered from a
  signal niwa controls, not read from `source`/`agent_type`.
- **Testability.** Logic belongs in a Go subcommand with unit tests, mirroring
  `niwa worktree from-hook`, not in shell embedded in `settings.json`.
- **The root becomes managed config.** Hosting hooks at the root forces a
  non-destructive regenerate path (R7, R8).
- **Untrusted hook input.** `session_id` and other stdin fields are interpolated
  into paths and commands and must be validated before use.

## Considered Options

### Decision 1 — root-hook materialization surface and the refresh command (R7, R8)

The session hooks must live in the workspace root's `.claude/settings.json`, which
is not a managed surface today (niwa materializes per-instance and per-repo
content, not the root). Options: (a) hand-edited root settings -- rejected, manual
hook editing is the setup this feature removes; (b) fold root materialization into
`niwa apply` -- rejected, `apply` is instance-scoped convergence and the root is
not an instance; (c) a dedicated root-materialization step run by `niwa init` and
re-runnable via a new `niwa refresh` command. **Chosen: (c).** `niwa init`
materializes the root `.claude/settings.json` (and any future root-managed files)
from niwa's embedded templates; `niwa refresh` regenerates the same root-managed
set idempotently on an already-initialized workspace, touching no instance and
destroying nothing. `refresh` reuses the existing content-materializer hashing so
it is drift-aware and a no-op when the root is already current.

### Decision 2 — the provisioning subcommand (R1, R3)

Options: embed `niwa create` plus JSON assembly directly in a shell hook
(rejected -- brittle parsing, untestable) versus a Go subcommand the hook calls.
**Chosen:** a new `niwa session from-hook` subcommand, mirroring `niwa worktree
from-hook`. The root hook is a one-line `command` entry piping stdin to
`niwa session from-hook`. The subcommand reads the hook JSON on stdin, branches on
`hook_event_name` (SessionStart vs SessionEnd), and owns all logic: guard
evaluation, `niwa create`, mapping writes, context injection, and teardown. Hook
config is data; behavior is compiled and unit-tested.

### Decision 3 — the coordinator-vs-worker guard (R6)

The hard constraint: coordinator and workers both present `source:startup`,
`agent_type:claude`, both rooted at the launch dir. Options considered:
(a) match on `source`/`agent_type` -- rejected, no field discriminates;
(b) provision for every root session including the coordinator -- rejected,
spurious coordinator instances are the exact waste the PRD calls out;
(c) an env marker workers carry -- rejected, dispatched workers inherit the
coordinator's environment, so an env set before `claude agents` reaches both.
**Chosen: a three-part guard.** (1) **Opt-in master switch:** provisioning is
inert unless the workspace root is in ephemeral-session mode (a root state flag,
default off), so by default no session is touched -- this single switch satisfies
both "ordinary sessions untouched" (R6) and the opt-out (R12). (2) **Background-job
detection:** within ephemeral mode, provision only when the session is a dispatched
background job, detected from the background-job profile Claude Code sets for
Agent-View workers (the `~/.claude/jobs/<id>` signal the spike surfaced) and not
present for a foreground coordinator. (3) **Re-entrancy no-op:** the subcommand
no-ops if its launch cwd already resolves inside a niwa instance
(`DiscoverInstance` succeeds), so a worker that itself dispatches sub-sessions does
not nest. The exact field exposing background-job-ness is verified during
implementation (an acceptance criterion); if it proves unavailable, the master
switch still bounds blast radius to opt-in workspaces and the reaper still reclaims
any coordinator instance.

### Decision 4 — context delivery and instance entry (R3)

The spike showed a mid-session `cd` moves only the Bash tool cwd and does not
reload `CLAUDE.md`. Options: rely on the agent re-rooting (rejected -- `cd` does
not re-root) versus inject. **Chosen: inject.** The SessionStart branch emits
`{"hookSpecificOutput":{"hookEventName":"SessionStart","additionalContext": ...}}`
carrying (a) the created instance's path, (b) the instance's root `CLAUDE.md`
content so the agent operates under the instance's guidance without a re-root, and
(c) an explicit instruction to `cd` into the instance before any work. The path is
also exported via the existing `NIWA_INSTANCE_ROOT` convention for tools that read
it.

### Decision 5 — the session->instance mapping store (R2)

Options: a new bespoke store versus reusing niwa's existing session-state
location. **Chosen: reuse.** The mapping is written at the workspace root under
`.niwa/sessions/<session_id>.json` (the same directory family niwa already uses for
worktree sessions), recording `session_id`, `instance_name`, `instance_path`,
`transcript_path`, `created`, and an `ephemeral: true` marker. `session_id` is
validated against the Claude session-id format (UUID) before being used as a path
component. This store is the single source of truth for teardown and the reaper.

### Decision 6 — teardown and the reaper (R4, R5, R10, R11)

**Teardown (SessionEnd branch):** resolve the instance by `session_id` through the
Decision-5 store (never cwd), confirm the mapping is marked `ephemeral: true`, run
`niwa destroy --force <instance>`, and delete the mapping entry. A SessionEnd for a
session with no mapping (non-worker, or already reaped) is a clean no-op.
**Reaper (`niwa reap`):** enumerate instances via `niwa list --json` (R10),
join against the mapping store, and reclaim any instance whose backing session is
no longer live. Liveness (R11) uses the per-instance marker plus the mapping's
`transcript_path`: a session is dead if its transcript has not been modified within
a TTL and/or its background job is gone. `niwa reap` only ever destroys instances
marked `ephemeral: true` with a dead session -- it never touches developer
instances. `reap` runs on demand and is also invoked opportunistically at the start
of `niwa create` so fan-out self-bounds.

### Decision 7 — machine-readable create and list (R9, R10)

`niwa create` gains `--json` emitting `{name, number, path}` for the created
instance, so the provisioning hook consumes the path without parsing the human
summary or re-deriving the name (the spike noted today's path discovery is
inference-only). `niwa list --json` (a public enumeration over the existing
internal `EnumerateInstances`) emits each instance's name, path, and ephemeral
marker for the reaper. Both are additive output modes; existing human output is
unchanged.

## Decision Outcome

A new root-materialization surface installs two workspace-root hooks that both
delegate to `niwa session from-hook`. On SessionStart, the subcommand applies the
three-part guard, runs `niwa create --json`, writes a
`.niwa/sessions/<session_id>.json` mapping, and injects the instance's context plus
a cd instruction. The agent works inside the instance. On SessionEnd, the
subcommand resolves the instance by session id and `niwa destroy --force`s it.
`niwa reap` backstops orphans by joining `niwa list --json` against the mapping and
a liveness marker, destroying only dead ephemeral instances. `niwa init` installs
the root config by default; `niwa refresh` regenerates it idempotently. `niwa
create --json` and `niwa list --json` give the hook and reaper machine-readable
surfaces.

This keeps niwa the system of record (every ephemeral instance is mapped, listed,
and reclaimable), removes per-session manual setup, and bounds orphans even when
SessionEnd never fires.

## Solution Architecture

Components (new unless noted):

- **Root materializer** -- emits the workspace-root `.claude/settings.json` with
  the SessionStart and SessionEnd hook entries (each a `command` piping stdin to
  `niwa session from-hook`) and the ephemeral-mode flag. Run by `niwa init` and
  `niwa refresh`; drift-aware via the existing content-materializer hashing.
- **`niwa session from-hook`** -- the hook entry point. Reads hook JSON on stdin,
  validates `session_id`, branches on `hook_event_name`:
  - *SessionStart:* guard (mode on? background-job? not already inside an
    instance?) -> `niwa create --json` -> write mapping -> emit `additionalContext`
    JSON (instance path + instance `CLAUDE.md` + cd instruction).
  - *SessionEnd:* resolve mapping by `session_id` -> if `ephemeral` -> `niwa destroy
    --force` -> delete mapping. No mapping -> no-op.
- **Mapping store** -- `.niwa/sessions/<session_id>.json` at the workspace root.
- **`niwa reap`** -- enumerate (`list --json`) + join mapping + liveness check ->
  `niwa destroy --force` dead ephemeral instances. Also invoked at `niwa create`
  start.
- **`niwa create --json` / `niwa list --json`** -- additive machine-readable output.
- **`niwa refresh`** -- idempotent regeneration of root-managed files.

End-to-end flow:

1. Developer runs `claude agents` at a workspace root in ephemeral mode and
   dispatches a worker.
2. Worker `SessionStart` -> hook -> `niwa session from-hook` passes the guard ->
   `niwa create --json` clones an instance -> mapping written -> context injected.
3. Agent `cd`s into the instance and works there in isolation.
4. Worker ends -> `SessionEnd` -> hook resolves the instance by session id ->
   `niwa destroy --force` -> mapping deleted.
5. If step 4 never fires (crash/kill), the next `niwa reap` (on demand or at the
   next `niwa create`) reclaims the orphan via the liveness check.

## Implementation Approach

- Add `--json` to `create` (and a `list` command with `--json`) over the existing
  `EnumerateInstances` / applier path; emit `{name, number, path}` /
  per-instance records.
- Add `niwa session from-hook` with SessionStart/SessionEnd branches; unit-test the
  guard matrix, the mapping read/write, the injection JSON, and the
  resolve-by-session-id teardown with table tests (mirror
  `internal/cli/...from-hook` worktree tests).
- Add the mapping store helpers (write/read/delete, `session_id` validation) beside
  the existing session-state code.
- Add `niwa reap` and wire an opportunistic call into `create`.
- Add the root materializer and `niwa refresh`; make `niwa init` install the root
  config by default with an opt-out flag persisted in root state.
- Add a `@critical` functional Gherkin scenario covering provision-on-start /
  teardown-on-end and a reaper-reclaims-orphan scenario, using the offline
  `localGitServer` helper.

## Security Considerations

- **Untrusted hook input.** `session_id` and other stdin fields are attacker-shaped
  in principle and are interpolated into file paths and command arguments. The
  subcommand validates `session_id` against the UUID format before using it as a
  path component and never passes raw stdin into a shell; `niwa create`/`destroy`
  are invoked via argument vectors, not string-built command lines.
- **Destroy blast radius.** Teardown and `niwa reap` destroy with `--force`, which
  skips the uncommitted-work guard. Both are constrained to instances carrying the
  `ephemeral: true` mapping marker with a confirmed-dead session; a developer's
  normal instance has no such marker and is never a target. The reaper never
  force-destroys solely on a TTL without the ephemeral marker.
- **Guard failure is fail-safe-ish.** If background-job detection misfires, the
  opt-in master switch bounds provisioning to workspaces the developer explicitly
  enabled, and a wrongly-created coordinator instance is itself an ephemeral
  instance the reaper reclaims -- the failure wastes a clone, it does not corrupt a
  developer instance.
- **Public-repo visibility.** No private content; the feature is generic Claude
  Code + niwa wiring.

## Consequences

- Developers fan out agents from the root and each runs isolated, with no manual
  per-session create/destroy and no growing orphan pile.
- niwa gains a root-managed-config surface (`niwa init` + `niwa refresh`) it did not
  have, plus `--json` output modes that are independently useful.
- Instance build cost (a full clone per session) is unchanged and accepted; fan-out
  of N agents is N clones.
- The background-job discriminator is the one residual feasibility detail; it is
  pinned by an acceptance criterion, and the master switch + reaper bound the
  damage if it needs a different signal.
- Teardown on clean exit remains best-effort; the reaper is the guarantee, so an
  instance can outlive its session until the next sweep.

## References

- docs/prds/PRD-ephemeral-session-instances.md -- the requirements this design
  implements (R1-R12).
- docs/spikes/SPIKE-ephemeral-session-instances.md -- the feasibility findings and
  the three load-bearing constraints (mapping-not-cwd, best-effort SessionEnd, no
  native worker discriminator).
- docs/guides/worktree.md -- the per-repo `niwa worktree from-hook` precedent this
  design lifts to the instance level.
