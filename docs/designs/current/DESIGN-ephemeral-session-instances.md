---
schema: design/v1
status: Current
upstream: docs/prds/PRD-ephemeral-session-instances.md
problem: |
  Claude Code background sessions dispatched from a niwa workspace root share one
  working tree, so parallel agents collide. The PRD requires each dispatched session
  to run in its own ephemeral instance, created on start and torn down on end, with
  a reaper backstop -- and the spike fixed three constraints: teardown must key on a
  session->instance mapping (not cwd), SessionEnd is best-effort, and no native hook
  field distinguishes the coordinator from a worker.
decision: |
  Install a workspace-root SessionStart hook (a new root-materialization
  surface) that delegates to a new `niwa instance from-hook` subcommand (a
  SessionEnd hook was originally installed for teardown but is no longer, per the
  Decision 6 revision). On start it
  detects a dispatched background-job session, runs `niwa create --json`, records a
  session->instance mapping under the root's `.niwa/sessions/`, and injects the
  instance context + a cd instruction. Teardown is delete-only: an instance is
  reclaimed solely by the `niwa reap` sweep, which keys liveness on the session's
  Claude Code job entry being PRESENT (the session is live, including idle-but-
  resumable) versus GONE (the developer deleted the session). `niwa init` installs the
  root-managed config (hooks, permission posture, CLAUDE.md); context-aware
  `niwa apply` refreshes it -- converging the subtree at the current scope (root,
  instance, or worktree), with `--no-cascade` to cap heavy root ops.
rationale: |
  The spike proved this end-to-end with no Agent SDK. A hook-backed Go subcommand
  mirrors the proven `niwa worktree from-hook` precedent, keeps logic testable, and
  avoids brittle shell. Keying teardown on a mapping is forced by SessionEnd's cwd
  being the launch root; the reaper is the SINGLE teardown path because the
  Agent-View session lifecycle has an idle-but-resumable state that SessionEnd
  cannot distinguish from deletion, so liveness keys on the job entry being present
  (live) vs gone (deleted); the background-job guard keys on the confirmed
  `template: "bg"` job-state marker.
---

# DESIGN: one ephemeral niwa instance per Claude Code session

## Status

Current

This design owns the mechanism: the root materialization surface, the
`niwa instance from-hook` subcommand, the session->instance mapping store, the
reaper, the supporting `--json` / liveness primitives, and the context-aware
`niwa apply` refresh model. The upstream PRD owns the requirements (R1-R14); this
design cites them and does not re-open them.

## Context and Problem Statement

niwa creates multiple ephemeral instances of a workspace (`niwa create` ->
`tsuku`, `tsuku-2`, ...), each a full clone with its own `.niwa/instance.json`.
Claude Code's `claude agents` dispatches background sessions that inherit the
launch directory's cwd, so sessions fanned out from the workspace root share one
tree and collide. The PRD requires each dispatched session to run in its own
ephemeral instance, provisioned on `SessionStart` and reclaimed once the session
is gone (originally framed as "on `SessionEnd`"; revised to delete-only,
reaper-driven teardown in Decision 6), with a reaper so orphans are bounded.

The spike (docs/spikes/SPIKE-ephemeral-session-instances.md) fixed the constraints
this design must honor:

- A session's cwd cannot be set at dispatch and a hook cannot relocate it, but
  `SessionStart` fires for dispatched sessions, inherits the launch cwd, and can
  inject `additionalContext`. The agent enters the instance with a Bash `cd`.
- `SessionEnd`'s reported cwd is the launch root, not the instance -- so teardown
  must resolve the instance from a `session_id` mapping, never from cwd.
- `SessionEnd` is best-effort (one of three observed sessions fired none) -- so a
  reaper is mandatory.
- The coordinator and workers are indistinguishable by `source`/`agent_type`, but a
  dispatched worker's job state at `~/.claude/jobs/<session-id>/state.json` carries
  `template: "bg"` where an interactive session carries `template: "claude"` -- the
  confirmed discriminator (see Decision 3).

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
- **The root becomes managed config.** Hosting the hooks, permission posture, and a
  root `CLAUDE.md` forces a non-destructive, scope-aware refresh path (R7, R8, R13,
  R14).
- **Untrusted hook input.** `session_id` and other stdin fields are interpolated
  into paths and commands and must be validated before use.

## Considered Options

### Decision 1 — root materialization surface and context-aware `niwa apply` (R7, R8, R13, R14)

The session hooks, the permission posture (`permissions.defaultMode`), and a
workspace-root `CLAUDE.md` must live in the *workspace root* -- not a managed surface
today (niwa materializes the instance root via `InstallWorkspaceRootSettings` and
per-repo dirs, but not the workspace root above the instances). The settings ride the
same `buildSettingsDoc` path that already emits the hooks and the permissions block
together, and the root `CLAUDE.md` reuses the existing workspace-context content at
workspace altitude -- so none of this is a separate mechanism, it is the same config
landing at a new location. The root `CLAUDE.md` matters because a session launched at
the root loads its `CLAUDE.md` at startup; today it gets none, so the coordinator and
any root session start with no workspace orientation.

Two questions: where it lands first, and how it is refreshed. **Landing:** `niwa
init` materializes the workspace-root config by default.

**Refresh -- context-aware `apply`.** Rather than a dedicated refresh verb, `apply`
is made context-aware. niwa already classifies cwd (`cwd_classify.go`) as workspace
root / inside-instance / outside; this design adds a fourth discrimination,
**inside-worktree**, giving three managed scopes -- workspace root, instance,
worktree. `apply` converges the **subtree rooted at the current scope** and never
climbs above it or touches siblings:

- at the **workspace root**: the root-managed config and vault, then every instance
  (the existing instance-scoped `apply`) and each instance's worktrees;
- at an **instance**: that instance and its worktrees;
- at a **worktree**: that worktree alone. Worktree-scope `apply` delegates to the
  upstream inherit primitive (tsukumogami/niwa#168): the worktree path inherits the
  instance's already-materialized environment rather than resolving secrets on the
  worktree path.

`apply --no-cascade` at the workspace root refreshes only the root-managed config
and does not re-converge the instances beneath it -- its purpose is picking up a
hook/permission/CLAUDE.md edit at the root without paying for a full reconvergence
of every instance. It has no effect at an instance or a worktree: under the inherit
model (tsukumogami/niwa#168) a worktree is a derived view of its instance and refreshes
together with it, so it is not an independently skippable scope, and a worktree is a
leaf with nothing below it. Adding the worktree scope refines today's behavior, where
`apply` from anywhere inside an instance converges the whole instance; this is an
intentional, pre-1.0 change for a uniform "converge my subtree" model.
**Chosen:** init lands it, context-aware `apply` (+ `--no-cascade`) refreshes it.
Rejected: a `niwa refresh` verb (the root is just another `apply` scope, and a second
verb would drift) and a root-only `--root-only` flag (`--no-cascade` is the name we
keep, though the heavy-op guarantee it provides is root-scoped). `apply` stays
content-idempotent via the existing content-materializer hashing, so it produces the
same bytes where everything is already current, and it never destroys (that is
`niwa destroy`).

### Decision 2 — the provisioning subcommand (R1, R3)

Options: embed `niwa create` plus JSON assembly directly in a shell hook
(rejected -- brittle parsing, untestable) versus a Go subcommand the hook calls.
**Chosen:** a new `niwa instance from-hook` subcommand, mirroring the existing
`niwa worktree from-hook` precedent. The root hook is a one-line `command` entry
piping stdin to `niwa instance from-hook`. The subcommand reads the hook JSON on
stdin, branches on `hook_event_name` (SessionStart vs SessionEnd), and owns all
logic: guard evaluation, `niwa create`, mapping writes, context injection, and
teardown. Hook config is data; behavior is compiled and unit-tested.

**Naming -- avoid the `session` collision.** This is `niwa instance from-hook`, not
`niwa session from-hook`, deliberately. niwa already ships a per-repo worktree hook
command (`internal/cli/session_from_hook_cmd.go`) and in niwa "session" already means
a worktree's lifecycle. This feature operates at the instance level on Claude
SessionStart/SessionEnd events; naming it `instance from-hook` keeps it disjoint from
the per-repo worktree command and from the overloaded "session" term. The two
commands share nothing but the `from-hook` suffix convention.

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
detection (confirmed signal):** within ephemeral mode, provision only when the
session is a dispatched background job. The discriminator is empirically confirmed:
Claude Code records each session's job state at `~/.claude/jobs/<session-id>/state.json`
with a `template` field whose value is `"bg"` for a dispatched background worker and
`"claude"` for an interactive/foreground session. The subcommand correlates its hook
`session_id` to that job dir (the dir name is the session-id prefix; the full
`sessionId` inside `state.json` confirms the match) and provisions only when
`template == "bg"`. Note: the `CLAUDE_JOB_DIR` env var is NOT reliably set in every
session, so the guard locates the job dir by session id rather than trusting the env
var. (3) **Re-entrancy no-op:** the subcommand no-ops if its launch cwd already
resolves inside a niwa instance (`DiscoverInstance` succeeds), so a worker that itself
dispatches sub-sessions does not nest.

`~/.claude/jobs/.../state.json` is an undocumented internal file, so the `template`
read is a stability risk if Claude Code changes the format; the opt-in master switch
bounds the blast radius to workspaces that chose it, and the reaper reclaims any
instance a misfire creates, so a format change degrades to wasted clones, not
corrupted developer instances.

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

**Instance naming.** The instance is named from the `session_id`, via
`niwa create --name <session-id-prefix>` using at least the first 12 hex characters
of the UUID (niwa applies no charset/length validation to the `--name` suffix and a
UUID is filesystem-safe; 12+ chars keeps collisions negligible while the job dir's
own 8-char prefix shows shorter is used elsewhere). This is deliberate: at
`SessionStart` the session has not yet processed its first user prompt, so no
topic-derived slug exists -- the empirically-captured SessionStart stdin carried
`session_id`, `transcript_path`, `cwd`, `agent_type`, `source`, and `model`, but no
title or topic. `session_id` is the only stable, unique handle available at create
time, and naming from it both gives a collision-free name and dodges the
`NextInstanceNumber` race that an unnamed concurrent `niwa create` would hit. A
human-friendly alias (e.g. derived from the first `UserPromptSubmit` once the topic
is known) MAY be recorded as an optional `label` field on the mapping later, but the
on-disk instance directory is never renamed -- renaming mid-session would break the
running session's cwd, so durable identity stays the `session_id` and any slug is
cosmetic metadata.

### Decision 6 — teardown and the reaper (R4, R5, R10, R11)

> **Revision (2026-06-27) — delete-only teardown.** This decision was revised
> after the shipped behavior reaped instances the moment their session finished a
> task or went idle. The original rule (quoted at the end of this decision)
> collapsed three distinct session conditions into "dead". The Agent-View
> background-session lifecycle has a state the original design never modeled --
> **idle-but-resumable** -- and BOTH teardown paths misread it as termination. The
> corrected contract keys liveness on **job-entry presence alone** and tears down
> **only on explicit session deletion**. See the spike's empirical findings
> (docs/spikes/SPIKE-ephemeral-session-instances.md, "Job-entry lifecycle across
> done/idle/delete").

**The idle-but-resumable state.** A dispatched background session in the Agent View
moves through more than two states. Besides "running" and "gone" it can be
**finished/idle/suspended yet still listed and fully resumable**: it has stamped a
terminal `state` and a `firstTerminalAt` in its job state, but the developer can
re-open it in `claude agents` and it keeps working. Empirically (spike, verified
2026-06-27) such sessions kept their `~/.claude/jobs/<id>/` entry for hours -- and
across a real dispatched worker, the entry disappeared only when the developer
explicitly deleted the session (Ctrl+X twice in `claude agents`), with the last
recorded state still `done`/`firstTerminalAt`-stamped. So:

- **job-entry present** is a faithful proxy for "the session still exists in the
  Agent View" (running OR idle-but-resumable), and
- **job-entry gone** is a faithful proxy for "the developer deleted the session."

There is no documented hook event or `SessionEnd` `reason` value that uniquely
means "deleted": Claude Code fires `SessionEnd` with `reason: resume` on
idle-suspend, `clear` on `/clear`, `logout`, `prompt_input_exit`,
`bypass_permissions_disabled`, or `other` -- none of which mean the session is
gone. Deletion is observable *only* as the job entry disappearing.

**Teardown -- delete-only, reaper-driven.** Tear down an ephemeral instance only
when its backing session is deleted. The reaper (`niwa reap`) is the SINGLE
teardown path, keyed on the entry-gone proxy. A session that finishes its task,
goes idle, or is suspended keeps its instance, so the developer can resume it into
the same isolated clone. This is the human's explicit choice: only-on-delete, over
"deletion OR truly-dead" and over "keep-idle with a long TTL."

**The liveness rule (R11) -- entry-present.** A session is **live** ⇔ its Claude
Code job entry at `~/.claude/jobs/<session-id>/state.json` exists (and its recorded
`sessionId`, when present, matches). It is **dead** only when the entry is gone.
The reaper reclaims an instance only when it is marked `ephemeral: true` AND its
session is dead by this rule. The three conditions the original rule also treated
as dead -- a terminal `state`, a stamped `firstTerminalAt`, and an `updatedAt`
older than a 30-minute TTL -- are **dropped**: every one of them is true of a live
idle-but-resumable session, so every one of them produced the reported bug.
Consequently `jobLivenessTTL` and `terminalJobStates` (job_state.go) and the
`state` / `updatedAt` / `firstTerminalAt` fields of the read struct become unused
and are removed; `template` (the SessionStart guard) and `cwd` (the dispatch
identity-capture path) still have readers and stay. Job-entry presence remains
strictly more reliable than transcript mtime, which is never consulted.

**SessionEnd is no longer a teardown trigger.** The original `SessionEnd` branch
resolved the instance by `session_id` and force-destroyed it. Because `SessionEnd`
fires on idle-suspend/`resume`/`clear` -- never uniquely on delete -- that branch
tore instances down while their sessions were still resumable (the secondary cause
of the bug, alongside the reaper's TTL/terminal misfire). The branch becomes a
no-op: it may log, but it never destroys. The workspace-root materializer
correspondingly stops installing the `SessionEnd` hook entry, so a freshly
materialized workspace carries only the `SessionStart` provisioning hook. The
handler-level no-op is the load-bearing safety, because a workspace whose
`settings.json` was materialized before this fix still carries the `SessionEnd`
entry until it re-applies, and that stale entry must not destroy. SessionStart
provisioning is unchanged.

**Reaper mechanics (unchanged except the liveness rule).** `niwa reap` still
enumerates instances via `niwa list --json` (R10), joins them against the mapping
store, and only ever force-destroys instances marked `ephemeral: true` with a dead
session; it never touches a developer instance and never reaps on a marker or a TTL
alone. It runs on demand and opportunistically at the start of `niwa create`, so
fan-out self-bounds. The separate **dispatch backstop** (`selectBackstopTargets`,
keyed on the dispatch instance NAME plus `dispatchBackstopTTL` for UNMAPPED orphan
instances -- Decision 4) is untouched: it handles the SIGKILL-before-mapping case,
a different concern from session liveness, and keeps its own TTL constant.

**Residual -- the ~1-hour supervisor process-stop.** The Agent View stops a
finished background session's supervisor process after roughly an hour. Whether
that stop also removes the `~/.claude/jobs/<id>/` entry was not observed in the
spike. If it does, the pure entry-present rule would let the reaper reclaim an
instance that is still resumable -- a window much narrower than, but the same shape
as, the bug being fixed. The chosen contract **accepts** this (option a): the
human explicitly asked for only-on-delete teardown, and an instance reclaimed an
hour after its session went idle is a far smaller surprise than one reclaimed the
instant a task finished. A generous long-TTL backstop (e.g. 24 h, distinct from the
removed 30-minute liveness TTL) is recorded as an **optional follow-up** (option b)
for truly abandoned instances, not part of this fix. Settling the residual needs an
empirical check of the entry's fate across the process-stop, which the spike did
not run.

> **Original (superseded) reasoning, retained for the audit trail:**
> *Teardown (SessionEnd branch):* resolve the instance by `session_id` through the
> Decision-5 store, confirm `ephemeral: true`, run `niwa destroy --force`, delete
> the mapping. *Reaper liveness:* a session is dead when its job entry is gone, OR
> its job `state` is terminal, OR `updatedAt` is older than a TTL. -- This
> collapsed the idle-but-resumable state into "dead" and is the direct source of
> the reaped-on-completion / reaped-on-idle bug.

### Decision 7 — machine-readable create and list (R9, R10)

`niwa create` gains `--json` emitting `{name, number, path}` for the created
instance, so the provisioning hook consumes the path without parsing the human
summary or re-deriving the name (the spike noted today's path discovery is
inference-only). `niwa list --json` (a public enumeration over the existing
internal `EnumerateInstances`) emits each instance's name, path, and ephemeral
marker for the reaper. Both are additive output modes; existing human output is
unchanged.

## Decision Outcome

A new root-materialization surface installs a workspace-root SessionStart hook
delegating to `niwa instance from-hook`. On SessionStart, the subcommand applies the
three-part guard, runs `niwa create --json`, writes a
`.niwa/sessions/<session_id>.json` mapping, and injects the instance's context plus
a cd instruction. The agent works inside the instance. SessionEnd is NOT a teardown
trigger (it fires on idle-suspend, not uniquely on delete), so its hook is no longer
installed and its handler branch is a no-op. `niwa reap` is the single teardown
path: it joins `niwa list --json` against the mapping and reclaims an ephemeral
instance only once its session's job entry is gone -- the proxy for the developer
deleting the session -- so completed, idle, and suspended sessions keep their
instances. `niwa init` installs
the root config (hooks and permission posture) by default; `niwa apply` from the
workspace root refreshes it and cascades into instances and worktrees. `niwa
create --json` and `niwa list --json` give the hook and reaper machine-readable
surfaces. Instances are named from `session_id` (the only stable handle available at
SessionStart), with an optional friendly alias recorded later.

This keeps niwa the system of record (every ephemeral instance is mapped, listed,
and reclaimable), removes per-session manual setup, keeps a finished-but-resumable
session's instance alive until the developer deletes the session, and reclaims it
on the next reap once that delete removes the job entry.

## Solution Architecture

Components (new unless noted):

- **Root materializer** -- emits the workspace-root managed config: `.claude/settings.json`
  (via the shared `buildSettingsDoc`) with the SessionStart hook entry (a `command`
  piping stdin to `niwa instance from-hook`; the SessionEnd entry is no longer
  installed, per the Decision 6 revision), the permission
  posture (`permissions.defaultMode`), and the ephemeral-mode flag, plus a
  workspace-root `CLAUDE.md` (workspace-context content at root altitude). Run by
  `niwa init` and by context-aware `niwa apply`; drift-aware via the existing
  content-materializer hashing.
- **Context-aware `niwa apply`** -- extends `cwd_classify` with an inside-worktree
  scope (workspace root / instance / worktree) and converges the subtree at the
  current scope: root -> root config + vault + every instance + their worktrees;
  instance -> that instance + its worktrees; worktree -> that worktree. At worktree
  scope, `apply` delegates to the upstream inherit primitive (tsukumogami/niwa#168) --
  the worktree path inherits the instance's already-materialized environment and does
  not resolve secrets on the worktree path. Never climbs above the current scope.
  `--no-cascade` caps it at the current node (e.g. root config only).
- **`niwa instance from-hook`** -- the hook entry point. Reads hook JSON on stdin,
  validates `session_id`, branches on `hook_event_name`:
  - *SessionStart:* guard (ephemeral mode on? job `template == "bg"`? not already
    inside an instance?) -> `niwa create --json --name <session-id-prefix>` -> write
    mapping -> emit `additionalContext` JSON (instance path + instance `CLAUDE.md` +
    cd instruction).
  - *SessionEnd:* no-op (the branch may log but never destroys). It fires on
    idle-suspend/`resume`/`clear`, not uniquely on delete, so it is not a teardown
    signal -- the reaper owns teardown (Decision 6 revision).
- **Mapping store** -- `.niwa/sessions/<session_id>.json` at the workspace root.
- **`niwa reap`** -- enumerate (`list --json`) + join mapping + liveness check
  (job entry present = live, gone = deleted) -> `niwa destroy --force` only
  instances whose session is deleted. The SINGLE teardown path. Also invoked at
  `niwa create` start.
- **`niwa create --json` / `niwa list --json`** -- additive machine-readable output.

End-to-end flow:

1. Developer runs `claude agents` at a workspace root in ephemeral mode and
   dispatches a worker.
2. Worker `SessionStart` -> hook -> `niwa instance from-hook` passes the guard ->
   `niwa create --json` clones an instance -> mapping written -> context injected.
3. Agent `cd`s into the instance and works there in isolation.
4. Worker finishes its task / goes idle / is suspended -> the instance is KEPT
   (the session is still listed and resumable in the Agent View). `SessionEnd` may
   fire (`reason: resume`, etc.) but is a no-op.
5. Developer deletes the session (Ctrl+X twice in `claude agents`) -> its
   `~/.claude/jobs/<id>/` entry disappears -> the next `niwa reap` (on demand or at
   the next `niwa create`) sees the entry gone and reclaims the instance, deleting
   the mapping.

## Implementation Approach

- Add `--json` to `create` (and a `list` command with `--json`) over the existing
  `EnumerateInstances` / applier path; emit `{name, number, path}` /
  per-instance records.
- Add `niwa instance from-hook` (a new subcommand, NOT the existing per-repo
  `session_from_hook_cmd.go`) with a SessionStart provisioning branch and a
  SessionEnd no-op branch. The guard reads `~/.claude/jobs/<session-id>/state.json`
  for `template == "bg"`; unit-test the guard matrix (mode off / `template != "bg"`
  / already-inside-instance / worker), the mapping read/write, the injection JSON,
  and that SessionEnd never destroys (it is a no-op), with table tests (mirror the
  existing `from-hook` worktree tests). Teardown lives in the reaper, not here.
- Add the mapping store helpers (write/read/delete, `session_id` validation) beside
  the existing session-state code.
- Add `niwa reap` and wire an opportunistic call into `create`.
- Add the root materializer (reusing `buildSettingsDoc`, emitting hooks + permission
  posture, plus a root `CLAUDE.md`); make `niwa init` install the root config by
  default with an opt-out flag persisted in root state via the existing
  `LoadState`/`SaveState` plumbing (the same additive-field pattern as
  `ConfigNameOverride`).
- Extend `cwd_classify` with an inside-worktree scope and make `niwa apply`
  context-aware: this touches `internal/workspace/scope.go` (`ResolveApplyScope`, a
  new worktree scope mode) and updates the existing `scope_test.go` cases
  (`TestResolveApplyScope_SingleFromInstance` / `_SingleFromNestedDir`) plus the
  `apply` help text, which document today's converge-the-whole-instance behavior.
  Converge the subtree at the current scope, add `--no-cascade` at the root to refresh
  the root-managed config without re-converging the instances beneath it (a no-op at
  instance and worktree scope). Worktree-scope `apply` delegates to the upstream inherit primitive
  (tsukumogami/niwa#168) -- the worktree path inherits the instance's
  already-materialized environment instead of resolving secrets on the worktree path.
  Unit-test each scope (root / instance / worktree) and the `--no-cascade` cap.
- Add `@critical` functional Gherkin scenarios: provision-on-start; an
  idle/completed session KEEPS its instance (the regression guard for this fix);
  and a reaper-reclaims-orphan scenario where the instance is reclaimed only once
  the job entry is gone (the delete proxy) -- using the offline `localGitServer`
  helper.

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
- niwa gains a root-managed-config surface (`niwa init` lands it; context-aware
  `niwa apply` refreshes it) it did not have, plus `--json` output modes that are
  independently useful.
- `niwa apply` becomes a uniform subtree-convergence operation across three scopes
  (root / instance / worktree) with a `--no-cascade` cap. This refines today's
  behavior -- `apply` inside an instance no longer always converges the whole
  instance; a worktree is now its own scope -- an intentional, pre-1.0 semantics
  change documented in the guide (Issue 10).
- A root-level bypass-permissions posture applies to every session launched at the
  root, not only dispatched workers (settings resolve at launch and cannot be scoped
  per session). This is wider than per-instance bypass; the opt-in ephemeral mode
  bounds it to workspaces that chose it.
- Instance build cost (a full clone per session) is unchanged and accepted; fan-out
  of N agents is N clones.
- The background-job discriminator is confirmed (`template: "bg"` in the session's
  job state), so no feasibility unknown remains; its only residual risk is that the
  job-state file is undocumented and could change format, which the master switch +
  reaper bound to wasted clones rather than corruption.
- Teardown is delete-only and reaper-driven: an instance survives task completion,
  idle, and suspension (so its session can be resumed into the same clone) and is
  reclaimed only after the developer deletes the session, on the next reap sweep.
  An instance therefore intentionally outlives a finished-but-resumable session.
- Residual: if the ~1-hour Agent-View supervisor process-stop also removes the job
  entry, the entry-present rule would reclaim a still-resumable instance after that
  stop. Accepted as part of the only-on-delete contract; an optional 24 h backstop
  is noted as a follow-up (Decision 6).

## References

- docs/prds/PRD-ephemeral-session-instances.md -- the requirements this design
  implements (R1-R12).
- docs/spikes/SPIKE-ephemeral-session-instances.md -- the feasibility findings and
  the three load-bearing constraints (mapping-not-cwd, best-effort SessionEnd, no
  native worker discriminator), plus the "Job-entry lifecycle across
  done/idle/delete" findings that ground the Decision 6 revision (entry-present
  liveness, delete-only teardown).
- docs/guides/worktree.md -- the per-repo `niwa worktree from-hook` precedent this
  design lifts to the instance level.
- tsukumogami/niwa#168 / #170 -- the worktree-vs-apply overlay-vault asymmetry (#170)
  was superseded upstream by #168, which has the worktree path inherit the instance's
  already-materialized environment instead of resolving secrets. Worktree-scope `apply`
  delegates to that inherit primitive.
