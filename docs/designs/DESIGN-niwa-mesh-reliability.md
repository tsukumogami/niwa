---
status: Proposed
problem: |
  Nine open issues filed since #92 describe a coherent set of failures in
  the niwa mesh subsystem (multi-agent coordination): coordinator routing
  is unreachable from session workers (#92, #109), worker sessions spawn
  without workspace plugins (#108), the niwa-mesh skill leaks into
  consumer repos (#97), session and daemon health are not surfaced
  through the API (#110, #111), the daemon's `dangling` task
  classification is invisible at the API and unrecoverable (#112), and
  the `niwa_delegate` surface lacks queue-time precondition checks
  (#113) and a body-reuse primitive (#114). The shared root cause is
  that niwa stores or relies on filesystem state the API layer either
  doesn't read or relies on implicit discovery to find. The fixes
  interact, so a single coordinated design is required: the dangling
  lifecycle shape (#112) affects the redelegate primitive (#114), the
  plugin propagation mechanism (#108) affects skill delivery (#97) and
  the required_skills check (#113), and every fix touches the same
  niwa-mesh skill text.
decision: |
  Replace four filesystem-side-channel mechanisms with explicit API
  contracts. (1) Pass an explicit settings/config flag to `claude -p`
  in `spawnWorker` so workers find workspace plugins programmatically;
  remove per-repo skill writes so the niwa-mesh skill stops landing in
  PRs. (2) Make `dangling` a real `state.json` transition: the daemon
  writes `abandoned` with `reason="taskstore_lost"`, so every read API
  surfaces a structurally consistent answer; the `inbox/dangling/`
  subdir stays as forensic-only. (3) Place `required_skills` inside the
  task body as a workspace convention; gate at queue time with a new
  `MISSING_SKILLS` error code that returns `{missing, available}`
  before allocating a task ID. (4) Add a `roleRoot(role)` helper that
  redirects coordinator-targeted role checks and inbox writes to
  `mainInstanceRoot`, so `niwa_ask` and `niwa_send_message` reach the
  live coordinator from session workers; auto-register coordinator
  from the four handlers a fan-out coordinator actually exercises.
  Plus: typed daemon-spawn timeout for synchronous failure surfacing
  (#110), a computed `daemon` sub-object on `niwa_list_sessions`
  (#111), a new `niwa_redelegate` primitive that reads the source
  envelope server-side and stamps `redelegated_from` (#114), and a
  rewrite of the niwa-mesh skill so the documented contract matches
  the runtime.
rationale: |
  Every chosen option is the smallest change that lifts state niwa
  already has on disk into the API contract that callers can rely on.
  The argv-flag approach mirrors the existing `--mcp-config` /
  `--strict-mcp-config` precedent for explicit programmatic injection;
  reusing `TaskStateAbandoned` with a typed reason avoids growing the
  state alphabet; the body convention keeps niwa's MCP wire schema
  small and forward-compatible as more preconditions emerge; and the
  `roleRoot` helper extends the same `mainInstanceRoot` redirect
  `handleAsk` already establishes for `askRoot`. Each rejected
  alternative either keeps a filesystem side-channel that the
  research traced as the recurring root cause, splits the body
  convention across schema migrations, or introduces a synthetic
  filesystem dir whose only purpose would be to mislead readers and
  re-enable the ephemeral-coordinator spawn path PR #93 already
  removed.
---

# DESIGN: niwa mesh reliability

## Status

Proposed

## Context and Problem Statement

Nine open issues filed against the niwa repo since #92 cluster around
the mesh subsystem: routing, lifecycle, observability, and recovery.
Issues are #92, #97, #108, #109, #110, #111, #112, #113, and #114.
The exploration that produced this design lives in
`wip/explore_niwa-mesh-reliability_findings.md` and the six per-lead
research files under `wip/research/explore_niwa-mesh-reliability_r1_lead-*.md`.

### What was discovered

- **#92 is partly already done.** PR #93 wired live-coordinator
  routing in `handleAsk` (`internal/mcp/server.go:780-843`); the
  spawn-fabricate fallback is gone. Today's branch is "live
  coordinator → write `task.ask` and wait" or "no live coordinator →
  return `no_live_session` synchronously".

- **#92 and #109 are the same code-level bug.**
  `isKnownRole(args.To)` at `server.go:802` runs against
  `<worktreePath>/.niwa/roles/<role>/` for a session worker, but
  `scaffoldWorktreeNiwa` (`internal/mcp/handlers_session.go:80-108`)
  only creates the worker's own role dir, never `roles/coordinator/`.
  Workers fail at `UNKNOWN_ROLE` before the live-coordinator routing
  logic can run. PR #93 hoisted the lookup to `mainInstanceRoot` but
  did NOT hoist the role-existence precondition, and the same gap
  applies to `sendMessageWithID` whose inbox path also anchors to
  `s.instanceRoot` (`server.go:719`).

- **Coordinator auto-registration is fragile.**
  `maybeRegisterCoordinator` only fires from `niwa_check_messages`
  and `niwa_await_task`. A coordinator that uses only
  `niwa_delegate` + `niwa_query_task` never registers, so even with
  the precondition fixed, ask-routing can fall through to
  `no_live_session`.

- **Worker spawn at `internal/cli/mesh_watch.go:908-1016`** invokes
  `claude -p` with no `--plugin`, `--marketplace`, `--settings`, or
  `CLAUDE_CONFIG_DIR` flag. Plugin discovery relies entirely on
  Claude Code's CWD-walk, which empirically does not surface
  workspace plugins to session workers whose CWD is a worktree under
  `.niwa/worktrees/`. This is #108. Comment at `mesh_watch.go:959-965`
  shows the team already learned this lesson for MCP servers
  (`--strict-mcp-config`), but the same care is absent for plugins.

- **The niwa-mesh skill leak (#97) is unrelated to spawn.** It
  comes from `InstallChannelInfrastructure`
  (`internal/workspace/channels.go:347-359`) writing
  `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` into every
  non-coordinator role's working tree on every `niwa apply`. Eight or
  more byte-identical copies live in the workspace today. Workers
  `git add .` and the file ends up in PRs.

- **Spawn-success and daemon-liveness signals already exist on disk
  but aren't propagated.** The daemon writes `daemon.pid` only after
  fsnotify registration succeeds (`mesh_watch.go:283-287`).
  `EnsureDaemonRunning` (`internal/workspace/daemon.go:35-102`) polls
  for that signal for 500 ms but explicitly returns nil on timeout
  with the comment "Return nil so Create/Apply still succeed; the
  missing PID file is the observable failure signal." This is #110.
  `mcp.IsPIDAlive` (`internal/mcp/liveness.go:14-35`) already exists
  and is used elsewhere; `niwa_list_sessions`
  (`handlers_session.go:26-50`) does not consult it. This is #111.

- **`dangling` is not a state.** `validTaskStates`
  (`internal/mcp/types.go:171-189`) defines exactly five values:
  queued, running, completed, abandoned, cancelled. `dangling` is a
  filesystem quarantine in `<role>/inbox/dangling/` triggered by
  `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`) iff a
  `task.delegate` envelope's
  `<mainInstance>/.niwa/tasks/<id>/state.json` is missing. The API
  layer reads only state.json, so `niwa_query_task` and
  `niwa_list_outbound_tasks` report `state="queued"` for dangling
  envelopes; `niwa_cancel_task` returns the contradictory pair
  `{status:"too_late",current_state:"queued"}`. This is #112.

- **The task store is flat by task_id, not partitioned by state.**
  Every task lives at
  `<taskStoreRoot>/.niwa/tasks/<id>/{envelope,state}.json` for its
  entire lifetime — only the inbox **message** moves between
  subdirs. `niwa_redelegate` (#114) is therefore trivial:
  `ReadState(taskDirPath(...))` resolves the source regardless of
  inbox subdir. `required_skills` (#113) slots cleanly between the
  `UNKNOWN_ROLE` check and `createTaskEnvelope`
  (`handlers_task.go:130-141`).

- **The niwa-mesh skill claims six runtime behaviors that don't
  hold today.** The skill is generated by `buildSkillContent`
  (`internal/workspace/channels.go:682-833`). Six of nine first-class
  claims are broken (#92, #97, #108, #109, #110, #111, #112) and one
  is a missing primitive the skill assumes implicitly (#114). The
  Message Vocabulary section advertises `question.ask`,
  `question.answer`, and `status.update` types that no handler
  dispatches; the actually-routed `task.delegate` and `task.ask` are
  omitted. Workers taking the skill literally have always been
  silently ignored.

### The architectural pattern

Every concern in scope has the same shape: niwa stores or relies on
state in the filesystem that the API layer either doesn't read or
relies on implicit discovery to find. Each fix moves something from
"filesystem convention" to "explicit contract" — argv-flag plugin
injection replaces CWD-walk discovery, daemon-driven state
transitions replace inbox quarantine, a `roleRoot` helper replaces
worktree-vs-main asymmetry, and a synchronous timeout replaces
silent fall-through.

## Decision Drivers

- **Bring runtime and skill text back into lockstep.** The niwa-mesh
  skill is the canonical user-facing contract. Every fix that lands
  in isolation drifts the skill further from the truth. A
  coordinated design lets the skill update happen once.

- **Prefer surgical changes that reuse existing primitives.** Most
  of the infrastructure is already on disk: `daemon.pid`,
  `IsPIDAlive`, `lookupLiveCoordinator`, the flat task store, the
  five-state task lifecycle. The cleanest fixes are one-function
  changes that propagate signals already produced.

- **Keep `Status` single-writer.** Persisted
  `SessionLifecycleState.Status` is owned by the lifecycle code
  path (create writes "active", destroy writes "ended"). Avoid
  making `niwa_list_sessions` mutate it; add an orthogonal `daemon`
  sub-object instead. Same separation applies to task state vs.
  inbox classification.

- **Don't broaden the MCP surface unnecessarily.** Recovery paths
  should reuse existing primitives where possible. The only new
  tool is `niwa_redelegate` (#114), which the skill implicitly
  assumes today and which subsumes the rare cases that an opt-in
  resurrect would have served.

- **Preserve attribution semantics.** Redelegate must reset `from`
  to the caller's role/PID (so `kindDelegator` auth still works on
  subsequent calls) while preserving an audit chain to the source
  task via a new envelope field.

- **Public-repo contract.** The skill, the design doc, and any new
  guides must follow public-repo tone: clear to first-time
  contributors, no internal jargon, no competitor names.

## Decisions Already Made (during exploration)

These were settled during round 1 convergence and are constraints,
not reopened in design:

- Treat the cluster as one design, not nine bugfixes.
- #92 and #109 collapse into a single fix.
- `Status` field stays single-writer; add an orthogonal `daemon`
  sub-object for #111.
- No new daemon heartbeat file; `daemon.pid` + `IsPIDAlive` is
  sufficient.
- `niwa_redelegate` is the single documented recovery path (no
  opt-in resurrect primitive).

## Considered Options

### Decision 1: Worker discovery channel for plugins and the niwa-mesh skill

**Issues:** #108 (workers see no workspace plugins), #97 (skill file
committed by delegated agents into consumer repo PRs).

The two issues share a thematic root: niwa relies on side-channel
filesystem layout to communicate Claude Code config to workers,
instead of explicit programmatic config. Plugin discovery relies on
Claude Code's CWD-walk, which empirically does not surface workspace
plugins to session workers. The niwa-mesh skill is delivered by
copying its content into every non-coordinator repo's working tree
(`channels.go:347-359`), where worker agents `git add` it.

Key assumptions:
- Claude Code supports at least one of `--settings <path>`,
  `--add-dir <path>`, `--plugin <alias>@<marketplace>`, or
  `CLAUDE_CONFIG_DIR=<path>`. The exact spelling is empirically
  determined during implementation.
- Claude Code's skill discovery looks under `<configDir>/skills/`
  when `<configDir>` is supplied via flag or env.
- Squash-merge is the workspace's PR strategy, so historical
  `.claude/skills/niwa-mesh/SKILL.md` files in feature branches
  don't survive merge.

#### Chosen: Programmatic injection at spawn (argv flag, env fallback) plus removal of per-repo skill writes

`spawnWorker` (`internal/cli/mesh_watch.go:982-1009`) gains an
explicit Claude Code configuration flag pointing at the workspace
`.claude/` directory. The exact flag/env is whichever Claude Code
honors — preferring an argv flag (`--settings`, `--add-dir`, or
similar) for the same visibility and stability the existing
`--mcp-config`/`--strict-mcp-config` flags already have, with
`CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` as a runtime fallback
at the same code site if argv flags don't carry the right semantics.

The workspace root used is `s.mainInstanceRoot` when set (per-session
daemon spawns) or `s.instanceRoot` (main-instance daemon spawns).

`InstallChannelInfrastructure` (`internal/workspace/channels.go:347-359`)
no longer writes `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for
non-coordinator roles. The instance-root copy at
`<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` (line 341)
remains; workers find it via the same configuration channel that
delivers the plugin set.

Defense in depth: `internal/workspace/gitignore.go` is extended to
add `.claude/skills/niwa-mesh/` to each consumer repo's `.gitignore`
on apply, protecting against any historical commits and any future
regressions that re-introduce the per-repo write.

**Rationale.** Every existing pain point in the spawn pipeline
traces back to the side-channel pattern: plugins drop because
filesystem walk-up is unreliable, the niwa-mesh skill leaks because
it is delivered via working-tree files. `--strict-mcp-config` exists
specifically because the team already learned the lesson for MCP
servers. Continuing the filesystem pattern would extend the same
fragility instead of fixing the architecture; programmatic injection
brings plugin handling up to the same level of explicitness MCP
config already has.

#### Alternatives Considered

- **Filesystem mirror in `scaffoldWorktreeNiwa` (symlink or copy
  `<mainInstance>/.claude/` into the worktree).** Rejected because
  it only addresses session daemons; main-instance workers (CWD =
  `<workspaceRoot>/<group>/<repo>`) already have the workspace's
  settings.local.json via `SettingsMaterializer` and the empirical
  failure in #108 suggests Claude Code's plugin alias resolution
  fails even when settings ARE present. The mirror also re-introduces
  filesystem coupling exactly where the design is trying to remove
  it for #97 — a copied skill in `<worktree>/.claude/skills/` is one
  mis-configured `.gitignore` away from re-leaking.

- **Status quo (rely on Claude Code CWD-walk).** Rejected because the
  reproducer in #108 shows it does not work for session workers
  whose CWD sits under `.niwa/worktrees/`, and the user-reported
  plugin set in #108 contains zero workspace plugins, only the
  user's personal plugins from `~/.claude.json`. The status quo is
  the bug.

### Decision 2: Dangling task lifecycle shape

**Issue:** #112.

`dangling` is a daemon-only filesystem quarantine
(`inbox/<role>/dangling/`) triggered when a `task.delegate`
envelope's `state.json` is missing. The API layer reads state.json
and reports `state="queued"`, contradicting the filesystem reality.
`niwa_cancel_task` returns the inconsistent pair `{too_late,
queued}`. There is no operator path to recover. The state machine
in code defines exactly five states (queued, running, completed,
abandoned, cancelled).

Key assumptions:
- The daemon may legitimately write `state.json` transitions. The
  daemon already shares the `internal/mcp` package surface (types,
  authorization helpers); a shared `taskstore` helper that flocks
  and appends a `TransitionLogEntry` is acceptable.
- For the `taskstore_lost` case where state.json is missing
  entirely, the daemon authors only state.json (not envelope.json).
  `formatQueryResult` reads only state.json fields, so query / list
  / await / cancel / update all work without a fabricated envelope.
- `niwa_redelegate` (#114) handles the rare case where envelope.json
  is missing by returning a structured `SOURCE_BODY_LOST` error
  rather than crashing.

#### Chosen: Real `state.json` transition to `abandoned` with `reason="taskstore_lost"`

When `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`)
detects state.json is missing for a `task.delegate` envelope, the
daemon transitions state.json to `abandoned` with
`reason="taskstore_lost"` (re-using the existing
`TaskStateAbandoned` constant in `internal/mcp/types.go:173-200`,
not adding a sixth state). The envelope still moves to
`inbox/dangling/` as a forensic side-effect, but the rename is now
bookkeeping, not the primary signal.

Two sub-cases:

- **state.json missing entirely (taskstore_lost).** The daemon
  recreates a minimal stub (only state.json, with
  `state=abandoned`, `reason=taskstore_lost`, transition log
  seeded `unknown -> abandoned`). envelope.json stays missing.
- **state.json present at `state=queued` but inbox file is somehow
  in dangling/ (rare, hand-seeded).** The daemon transitions
  `queued -> abandoned` with the same reason annotation.

`niwa_query_task`, `niwa_list_outbound_tasks`, `niwa_await_task`,
`niwa_cancel_task`, and `niwa_update_task` then all surface
`state="abandoned"` consistently. `niwa_cancel_task` and
`niwa_update_task` keep their existing terminal-state guards;
`niwa_await_task` exits immediately via the existing terminal-state
short-circuit at `handlers_task.go:427-430`.

Operators recover via `niwa_redelegate` (#114), which already lists
`abandoned` as an allowed source state and reads the source
envelope from the flat task store. The `inbox/dangling/` subdir
becomes implementation detail.

**Rationale.** This is the only option that satisfies the API
truthfulness requirement without operator action and composes
cleanly with `niwa_redelegate` as the single documented recovery
path. It reuses existing primitives (`TaskStateAbandoned`, the
`state_transitions` log, the flock'd state.json writer), keeps the
state alphabet at five, and resolves the
`{too_late, queued}` contradiction without per-tool changes.

#### Alternatives Considered

- **Resurrect primitive only (keep dangling as quarantine; add
  `niwa_resurrect_task`).** Rejected because it doesn't satisfy the
  API truthfulness goal — until an operator runs the tool, every
  read API keeps lying. Resurrect requires the operator to first
  restore state.json by hand for the dominant `taskstore_lost`
  case, making the tool's marginal value over `mv` small. Also
  competes with redelegate for the documented recovery role.

- **Both real state AND resurrect primitive.** Rejected because
  resurrect's surface area shrinks to a corner case once the real
  state transition lands. A tool that exists for a corner case
  attracts misuse; documentation cost compounds.

- **Make dangling structurally impossible (refuse to claim, treat
  as fatal error).** Rejected because the trigger is exogenous to
  the daemon (manual `rm -rf .niwa/tasks/`, fresh checkout,
  partial workspace destroy, hand-seeded test fixtures). The
  daemon can't prevent the inconsistency at its source; refusing to
  classify just leaves orphan envelopes for `scanExistingInboxes`
  to retry on every restart. Also breaks redelegate composition
  by leaving state.json missing.

### Decision 3: `required_skills` placement

**Issue:** #113.

`niwa_delegate` accepts an opaque `body` plus structured fields
(`to`, `mode`, `expires_at`, `session_id`, `read_only`). Adding a
queue-time skill precondition needs a place for the input. Today's
convention is "body is opaque to niwa, body carries
workspace-specific fields"; the alternative is a top-level field on
`delegateArgs`.

Key assumptions:
- The manifest source-of-truth comes from Decision 1's argv-flag
  target: the workspace's `<workspaceRoot>/.claude/` plugin set.
  The gate reads from there.
- `niwa_redelegate` (#114) reads the source envelope server-side
  from `<taskStoreRoot>/.niwa/tasks/<id>/envelope.json`, so any
  body field naturally propagates through redelegation.

#### Chosen: Inside `body` (`body.required_skills`)

The MCP wire schema (`internal/mcp/server.go:264-279`) and
`delegateArgs` (`internal/mcp/handlers_task.go:46-55`) stay
unchanged. `handleDelegate` adds a peek between the existing
`UNKNOWN_ROLE` check and `createTaskEnvelope`:

1. `var peek struct { RequiredSkills []string \`json:"required_skills"\` }`
2. `_ = json.Unmarshal(args.Body, &peek)`
3. Intersect `peek.RequiredSkills` with the target session's
   manifest (read via Decision 1's mechanism).
4. On miss, return `errResultCode("MISSING_SKILLS", …)` with
   `{missing, available}` in the error body before any task ID is
   allocated.

The same gate runs inside `handleRedelegate`, before
`createTaskEnvelope`, against the merged body.

**Rationale.** Body is opaque to niwa today and that's load-bearing:
every workspace-specific knob lives inside it, and niwa's MCP
surface stays small and stable. `required_skills` is a
workspace-specific assertion about what the workpiece needs — same
category as instructions and tool hints. A top-level placement
splits the convention and forces a schema migration each time a
new precondition emerges (`required_marketplaces`,
`min_token_budget`, etc.). With a body convention, redelegate
propagates `required_skills` for free; with a top-level field,
redelegate would need explicit propagation logic or an
envelope-schema change.

The audit-log fidelity loss (`extractArgKeys` only captures
top-level wire keys) is bounded: `MISSING_SKILLS` failures still
log via `error_code`, so failed gates are observable. If
post-hoc "which calls asserted a skill requirement?" becomes a
recurring operator question, a non-breaking `body_top_keys`
extension to `AuditEntry` is the planned escape hatch.

#### Alternatives Considered

- **Top-level parameter on `delegateArgs`.** Rejected because it
  splits the body convention, scales the wire schema linearly with
  every new precondition, requires explicit redelegate propagation
  plumbing, and makes `delegateArgs` a grab-bag of routing plus
  workspace concerns.

### Decision 4: Coordinator role visibility for session workers

**Issues:** #92 (live-coordinator routing for `niwa_ask`), #109
(`UNKNOWN_ROLE` for `niwa_ask`/`niwa_send_message` when worker
targets `coordinator`).

`isKnownRole(role)` at `internal/mcp/server.go:768-778` does
`os.Stat(<s.instanceRoot>/.niwa/roles/<role>/)`. For session
workers, `s.instanceRoot` is the worktree, where
`scaffoldWorktreeNiwa` only creates the worker's own role dir.
PR #93 hoisted the live-coordinator lookup to `mainInstanceRoot`
but didn't hoist the role-existence precondition. The same gap
applies to `sendMessageWithID` whose inbox path also anchors to
`s.instanceRoot` (`server.go:719`).

Key assumptions:
- `s.mainInstanceRoot` is reliably populated for session worker
  MCP servers via `NIWA_MAIN_INSTANCE_ROOT` (set in
  `handleCreateSession`'s extraEnv at `handlers_session.go:212-215`).
- `lookupLiveCoordinator(<mainInstanceRoot>)` already returns the
  correct main-instance inbox path and prunes stale PIDs.
- No existing code path writes to
  `<worktree>/.niwa/roles/coordinator/inbox/`.

#### Chosen: Special-case via shared `roleRoot` helper

A new `roleRoot(role string) string` helper on `Server` returns
`s.mainInstanceRoot` when `role == "coordinator" && s.mainInstanceRoot != ""`,
else `s.instanceRoot`. Three call sites switch to it:

- `isKnownRole` (`server.go:768-778`)
- `sendMessageWithID`'s inbox path (`server.go:719`)
- `handleAsk`'s `askRoot` selection (`server.go:817-819`) — already
  encodes the same redirect inline; centralize it on the helper.

`scaffoldWorktreeNiwa` is untouched. The worktree's `.niwa/roles/`
keeps mirroring the worker's actual responsibilities (one role:
the worker's repo).

Auto-registration of coordinator extends to the four handlers a
fan-out coordinator actually exercises:

- `handleDelegate` (highest priority — the canonical fan-out call).
- `handleQueryTask` (the canonical poll call).
- `handleSendMessage` (initiating peer messaging).
- `handleListOutboundTasks` (alternative poll pattern).

`maybeRegisterCoordinator` is idempotent and cheap (short-circuits
on `s.role != "coordinator"` and writes only when no live entry
exists), so the cost of being generous with trigger sites is
negligible. The existing `handleCheckMessages` and `handleAwaitTask`
triggers stay.

**Rationale.** The decisive evidence is that
`sendMessageWithID` both calls `isKnownRole(args.To)` AND computes
`inboxDir := filepath.Join(s.instanceRoot, ".niwa", "roles", args.To, "inbox")`.
Whichever option is picked, the inbox path itself has to be
redirected for `coordinator` — the worktree's roles dir cannot be
the destination, because the daemon watching the worktree is the
worker's daemon, not the coordinator's. That redirect is the same
one-line decision `handleAsk` already encodes for `askRoot`.
Centralizing it via a small helper and reusing it from
`isKnownRole` lands the whole fix without ever creating a
directory.

#### Alternatives Considered

- **Synthetic `<worktree>/.niwa/roles/coordinator/inbox/` in
  `scaffoldWorktreeNiwa`.** Rejected because it doesn't reduce the
  required code work — `sendMessageWithID`'s inbox path still has
  to be redirected for messages to reach the live coordinator —
  and adds a directory whose only role in the system is to be
  misleading. The worktree's daemon enumerates `.niwa/roles/` in
  `registerInboxWatches` and would dutifully add a watch on the
  synthetic dir; any stray write into it (test fixture, future
  bug, manual reproduction) would either be silently swallowed
  (ordinary message) or trigger ephemeral-coordinator spawn
  (`task.delegate`) — exactly the deadlock scenario PR #93 was
  meant to eliminate.

## Decision Outcome

The four decisions compose into a single coherent reliability pass.
Decision 1 establishes a programmatic config channel from spawn-time
that delivers both the workspace plugin set and the niwa-mesh skill
(removing the per-repo write that fed #97 and the implicit-discovery
contract that fed #108). Decision 4 makes the coordinator reachable
from session workers (closing #92 and #109) via the same
`mainInstanceRoot` redirect mechanism already used elsewhere.
Decision 2 converts a daemon-internal observable (`dangling`) into
a real state-machine transition that every read API surfaces
honestly, removing the `{too_late, queued}` contradiction and
making `niwa_redelegate` the single documented recovery path.
Decision 3 keeps the wire schema small while wiring the new
queue-time precondition (#113) and ensuring it survives
redelegation (#114) for free.

The secondary fixes (#110 typed timeout, #111 `daemon` sub-object,
#114 redelegate primitive, skill text rewrite) follow mechanically
from the four decisions and don't require independent design.

## Solution Architecture

### Overview

The mesh remains the same multi-agent coordination layer it is
today: coordinator delegates tasks to workers via niwa_* MCP
tools and per-role inboxes under
`<worktree>/.niwa/roles/<role>/`. The reliability pass changes
four things:

1. **Spawn-time config** flows programmatically into worker
   Claude Code processes instead of through filesystem walk-up.
2. **State observability** reads from the same on-disk signals
   that already exist (daemon.pid + IsPIDAlive; state.json), but
   the API layer surfaces them.
3. **Routing** uses a uniform `roleRoot` helper for coordinator
   targets, so the live-coordinator branch in `handleAsk` is
   reachable from session workers and `niwa_send_message` honors
   the same redirect.
4. **Coordinator API** gains `niwa_redelegate` and a queue-time
   `required_skills` gate; the niwa-mesh skill text is rewritten
   to match the runtime.

### Components

**Modified components:**

- `internal/cli/mesh_watch.go`
  - `spawnWorker` (≈908-1016): add Claude Code config flag/env to
    the `claude -p` argv. Compute target workspace root from
    `s.mainInstanceRoot` if set else `s.instanceRoot`.
  - `handleInboxEvent` (≈776-803): on missing state.json for a
    `task.delegate` envelope, transition state.json to
    `abandoned` with `reason="taskstore_lost"` (recreating the
    state.json stub if the task dir was wiped). The envelope
    still moves to `inbox/dangling/` as forensic-only.
  - Auto-register triggers: no change here; the change is in MCP
    handlers below.

- `internal/workspace/daemon.go`
  - `EnsureDaemonRunning` (≈35-102): return typed
    `ErrDaemonSpawnTimeout` on the 500 ms timeout instead of nil.
    Existing pre-spawn errors keep their current return paths.

- `internal/workspace/channels.go`
  - `InstallChannelInfrastructure` (≈338-359): remove the
    per-repo skill write loop (lines 347-359 today). Keep only
    the instance-root copy at line 341.
  - `buildSkillContent` (≈682-833): rewrite to remove dead
    `question.ask`/`question.answer`/`status.update` vocabulary,
    add `task.delegate`/`task.ask` to message vocabulary,
    document `taskstore_lost` recovery via `niwa_redelegate`,
    document worker plugin availability semantics, fix the
    "Worker asks coordinator" pattern to reflect the new
    routing path.

- `internal/workspace/gitignore.go`
  - Extend the apply-time gitignore enforcement to add
    `.claude/skills/niwa-mesh/` to each consumer repo's
    `.gitignore`, defending against any historical commits and
    future regressions.

- `internal/mcp/server.go`
  - New `roleRoot(role string) string` helper on `Server`.
  - `isKnownRole` (≈768-778): consult `roleRoot(role)` instead
    of bare `s.instanceRoot`.
  - `sendMessageWithID` (≈695-742): consult `roleRoot(args.To)`
    when computing `inboxDir`.
  - `handleAsk` (≈780-843): use `roleRoot("coordinator")` for
    `askRoot` selection (refactor of the existing inline
    redirect at lines 817-819).
  - Tool registrations: add `niwa_redelegate` (≈264-279 area).

- `internal/mcp/handlers_task.go`
  - `handleDelegate` (≈111-165): insert `required_skills` peek
    and gate between `UNKNOWN_ROLE` check (line 130-133) and
    `createTaskEnvelope` (line 141). New `MISSING_SKILLS` error
    code returns `{missing, available}`.
  - New `handleRedelegate`: reads the source via
    `ReadState(taskDirPath(...))`, validates source state is
    non-active, merges body overrides, runs the same
    `required_skills` gate, and re-enters `createTaskEnvelope`
    with `from` reset to caller. Adds `redelegated_from` to the
    new envelope.
  - `maybeRegisterCoordinator` calls added to: `handleDelegate`,
    `handleQueryTask`, `handleSendMessage`,
    `handleListOutboundTasks` (entry point).

- `internal/mcp/handlers_session.go`
  - `handleCreateSession` (≈146-228): handle
    `ErrDaemonSpawnTimeout` returned by `daemonStarter` —
    rollback the worktree (existing `cleanupWorktree` defer
    pattern at lines 194-208), the branch, and the session-state
    file; return `errResult` with `IsError: true` and a
    structured error code.
  - `handleListSessions` (≈26-50): enrich each
    `SessionLifecycleState` row with a computed `daemon`
    sub-object: `{alive, pid, started_at}`. Probe via
    `<worktreePath>/.niwa/daemon.pid` + `mcp.IsPIDAlive`. The
    persisted `Status` field is unchanged.

- `internal/mcp/types.go`
  - `TaskEnvelope` (≈206-224): add `RedelegatedFrom string \`json:"redelegated_from,omitempty"\``.
  - No new task-state constant; reuse `TaskStateAbandoned`.

- `docs/guides/sessions.md`
  - Add a section on the `daemon` sub-object returned by
    `niwa_list_sessions` (parallel to existing
    `daemon_warning` docs at ~222-225).
  - Add a section on `taskstore_lost` recovery via
    `niwa_redelegate` (parallel to existing "When the session
    daemon crashes" at ~258-281).
  - Remove the `dangling` filesystem detail from any
    user-facing prose; it's implementation-only now.

**Unchanged components (notable):**

- The flat task store layout
  (`<taskStoreRoot>/.niwa/tasks/<id>/{envelope,state}.json`).
- The five-state task lifecycle alphabet.
- The fsnotify-based inbox watch loop.
- The `--mcp-config`/`--strict-mcp-config` argv contract for
  workers.
- The existing role registration in `sessions.json` and
  `lookupLiveCoordinator`.

### Key Interfaces

**New MCP tool — `niwa_redelegate`:**

```
niwa_redelegate(
  source_task_id: string [required],     // Task to re-fire
  to:            string,                  // Override source.to.role
  session_id:    string,                  // Override session
  read_only:     boolean,                 // Override routing
  body_overrides: object,                 // Shallow-merge into source.body
  mode:          "async" | "sync",        // Default async
  expires_at:    string,                  // RFC3339
)
```

Authorization: `kindDelegator` on `source_task_id`. Source state
must be in `{abandoned, cancelled, completed}`; redelegate from
`queued` or `running` returns a structured error (cancel first).
The new envelope's `from` is the caller's role/PID; `redelegated_from`
points to the source. The same `required_skills` gate runs against
the merged body.

**New error code — `MISSING_SKILLS`:**

```
{
  "error_code": "MISSING_SKILLS",
  "missing": ["shirabe:prd"],
  "available": ["superpowers:tdd", "init", "review", ...]
}
```

Returned synchronously from `niwa_delegate` and `niwa_redelegate`
when `body.required_skills` contains entries not present in the
target session's plugin manifest. No task ID is allocated.

**New error code — `DAEMON_SPAWN_TIMEOUT`:**

Returned synchronously from `niwa_create_session` when
`EnsureDaemonRunning` times out (500 ms) waiting for `daemon.pid`.
The session state file, branch, and worktree are rolled back
before the response. Existing pre-spawn errors keep their current
codes.

**Extended `niwa_list_sessions` shape:**

Each session entry gains a `daemon` sub-object:

```json
{
  "session_id": "ab12cd34",
  "status": "active",
  "daemon": {
    "alive": true,
    "pid": 12345,
    "started_at": "2026-05-09T10:00:00Z"
  },
  "...existing fields"
}
```

`status` keeps its lifecycle-marker meaning. `daemon.alive` is
the runtime health probe.

**`task.ask` / `task.delegate` vocabulary, documented:**

The skill's Message Vocabulary section is rewritten to list:
`task.delegate`, `task.ask`, `task.progress`, `task.completed`,
`task.abandoned`, `task.cancelled`. The dead
`question.ask`/`question.answer`/`status.update` entries are
removed (no handler dispatched them).

### Data Flow

**Worker spawn (post-fix):**

```
handleCreateSession
  -> EnsureDaemonRunning  (returns ErrDaemonSpawnTimeout on 500ms timeout)
     -> on success: daemon writes daemon.pid after fsnotify register
     -> on timeout: cleanupWorktree, rollback session state, errResult
  -> daemon spawns worker via spawnWorker
     -> claude -p
          --mcp-config=...           (existing)
          --strict-mcp-config        (existing)
          --settings=<workspaceRoot>/.claude/settings.json   (NEW)
          [or CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude in env]
        => Claude Code resolves enabledPlugins, finds skills/niwa-mesh/
           via the workspace .claude/, no per-repo files needed
```

**Worker asks coordinator (post-fix):**

```
worker calls niwa_ask(to="coordinator", body=...)
  -> isKnownRole("coordinator") consults roleRoot("coordinator") = mainInstanceRoot
     => stat <mainInstance>/.niwa/roles/coordinator/  -> exists, pass
  -> handleAsk uses askRoot = mainInstanceRoot
  -> lookupLiveCoordinator(<mainInstance>/.niwa/sessions/sessions.json)
     => coordinator was auto-registered on its first niwa_delegate or
        niwa_query_task call, so sessions.json has a live entry
  -> writeAskNotification writes task.ask envelope to
     <mainInstance>/.niwa/roles/coordinator/inbox/
  -> coordinator's watcher dispatches task.ask via existing
     questionWaiters / niwa_check_messages
  -> coordinator answers via niwa_finish_task(ask_task_id)
  -> answer routes back to worker via the existing awaitWaiters
     dispatch in watcher.go
```

**Task lifecycle including taskstore_lost (post-fix):**

```
queued -> running -> completed | abandoned | cancelled
                                    |
                                    +-- abandoned with reason="taskstore_lost"
                                        (daemon-driven, when state.json is missing)
```

The `inbox/dangling/` subdir is forensic-only. Operators never see
it via the API; `niwa_query_task` returns
`{state: "abandoned", reason: "taskstore_lost"}` and the caller
recovers via `niwa_redelegate(source_task_id)`.

## Implementation Approach

The phases are sequenced by their build-order dependencies. Each
phase is small enough to ship as one PR; `/plan` will turn this into
a milestone of issues.

### Phase 1: Coordinator routing repair

**Closes #92 and #109.**

Deliverables:
- New `roleRoot(role string) string` helper on `Server`.
- `isKnownRole`, `sendMessageWithID` inbox path, `handleAsk`
  `askRoot` switched to use `roleRoot`.
- `maybeRegisterCoordinator` called from `handleDelegate`,
  `handleQueryTask`, `handleSendMessage`, `handleListOutboundTasks`.
- Functional test: a worker session can `niwa_ask(to="coordinator")`
  and reach a live coordinator via the existing `task.ask` flow.
  Same for `niwa_send_message`.
- `@critical` Gherkin scenario in `test/functional/features/`
  per the niwa testing convention.

This phase has no dependency on plugin propagation; it ships first
because it unblocks meaningful end-to-end tests of the rest.

### Phase 2: Daemon health propagation

**Closes #110 and #111.**

Deliverables:
- `EnsureDaemonRunning` returns typed `ErrDaemonSpawnTimeout` on
  the 500 ms timeout.
- `handleCreateSession` rolls back worktree, branch, state on
  that error class; returns `errResult`.
- `handleListSessions` enriches each row with the computed
  `daemon: {alive, pid, started_at}` sub-object via
  `mcp.IsPIDAlive`.
- `docs/guides/sessions.md` updated with both surfaces.

Independent of Phase 1; can ship in parallel.

### Phase 3: Worker plugin and skill discovery

**Closes #108. Resolves #97 by elimination.**

Deliverables:
- `spawnWorker` adds Claude Code configuration flag/env to
  `claude -p` argv. Empirical verification of which flag
  Claude Code honors lands during this phase.
- `InstallChannelInfrastructure` removes the per-repo skill
  write loop. Instance-root copy stays.
- `internal/workspace/gitignore.go` extended to add
  `.claude/skills/niwa-mesh/` to consumer repos' `.gitignore`.
- Functional test: a worker spawned by `niwa_delegate` can
  invoke a workspace-level skill (e.g. `/shirabe:prd`).

Phase 3 unblocks Phase 5's `required_skills` gate (because the
gate needs a manifest source-of-truth that this phase establishes).

### Phase 4: Task lifecycle truthfulness

**Closes #112.**

Deliverables:
- `handleInboxEvent` writes state.json transition to
  `abandoned` with `reason="taskstore_lost"`. Recreates state.json
  stub when it was missing entirely.
- Daemon-internal taskstore writer (shared with the MCP server's
  flock'd writer).
- `niwa_query_task`, `niwa_list_outbound_tasks`, `niwa_await_task`,
  `niwa_cancel_task`, `niwa_update_task` verify against the new
  abandoned-with-reason output. Existing terminal-state guards
  cover most of this; only the cancel path needs an early state
  guard added to remove the `{too_late, queued}` contradiction.
- Test fixture update: `TestHandleInboxEvent_DanglingEnvelope`
  (`mesh_watch_test.go:743-784`) verifies state.json transition.

Independent of Phase 3.

### Phase 5: Coordinator ergonomics

**Closes #113 and #114.**

Deliverables:
- `niwa_redelegate` MCP tool registration, `handleRedelegate`
  handler, `redelegated_from` envelope field.
- `required_skills` body peek and `MISSING_SKILLS` error code in
  `handleDelegate` and `handleRedelegate`.
- Functional test: redelegate from `abandoned` source produces a
  new task with the source's body verbatim; redelegate from a
  `taskstore_lost` source where envelope.json survived works;
  redelegate where envelope.json is also missing returns
  `SOURCE_BODY_LOST`.

Depends on Phase 3 (manifest source-of-truth) and Phase 4
(`abandoned` source state for redelegate from dangling tasks).

### Phase 6: Skill text and guides update

Deliverables:
- `buildSkillContent` rewrite per the contract audit in
  `wip/research/explore_niwa-mesh-reliability_r1_lead-mesh-skill-contract.md`.
- `docs/guides/sessions.md` update for the new `daemon`
  sub-object and `taskstore_lost` recovery.
- Diff-vs-fixture CI check for `buildSkillContent` output to
  catch future drift.

Depends on Phases 1–5 landing so the skill describes truthful
behavior.

### Sequencing summary

```
Phase 1 ──┐
Phase 2 ──┤
Phase 3 ──┼─► Phase 5 ──┐
Phase 4 ──┘              ├─► Phase 6
                Phase 4 ─┘
```

Phases 1 and 2 are independent of everything else and can ship
first in parallel. Phase 3 unblocks Phase 5. Phase 4 unblocks
Phase 5 only for the dangling-source redelegate case. Phase 6
lands last so the skill text reflects the merged runtime.

## Security Considerations

A dedicated security review of this design surfaced no high- or
medium-severity findings. The design preserves niwa's existing
authorization invariants and does not expand the trust boundary.
Three lower-severity considerations the implementer should know
about:

### Trust model is unchanged

niwa is single-tenant per workspace by design. The new
`--settings <workspaceRoot>/.claude/settings.json` argv flag
formalizes the trust relationship that already exists between
`niwa apply` and the workspace `.claude/` tree — it does not
expand what's trusted. Plugin alias resolution still goes through
the user-level `~/.claude.json` plugin store, which the user
controls; niwa never installs plugins on the user's behalf. The
`daemon: {alive, pid, started_at}` sub-object on
`niwa_list_sessions` exposes nothing that a same-UID process
can't already read from `/proc`.

### `roleRoot` redirect is literal-string-gated

The `roleRoot(role string) string` helper returns
`s.mainInstanceRoot` only when `role == "coordinator"` (literal
equality). It cannot be used to write into the main instance's
roles dir for any other role. Workers cannot extend the redirect
to non-coordinator targets through anything they control. The
existing daemon ownership predicate (`daemonOwnsInboxFile` only
claims `task.delegate` files) is unaffected, so PR #93's closure
of the ephemeral-coordinator-spawn deadlock stays intact.

### `niwa_redelegate` propagates the body verbatim

The new primitive reads the source envelope server-side and reuses
the body unless `body_overrides` is provided. Time-sensitive
material in the source body (short-lived tokens, deadlines,
references to ephemeral artifacts) travels forward across
redelegations. This is not a privilege escalation — `kindDelegator`
auth guarantees the new delegator is the same role as the original,
so they already had access to the body — but it is a footgun.

Mitigation: the niwa-mesh skill documents that callers must pass
`body_overrides` to refresh time-sensitive fields when redelegating.
The `redelegated_from` envelope field provides an audit chain so
operators can identify and rotate any leaked material. The body
content is at rest under the same filesystem permissions as the
source envelope (no new on-disk surface).

### Daemon-driven `state.json` writes need flock discipline

Decision 2 expands the daemon's surface to include writing
`state.json` transitions. The existing taskstore writer in
`internal/mcp/taskstore.go` already provides flock'd
read-modify-write semantics; the daemon's transition path must use
the same helper rather than introducing a parallel writer. This is
a load-bearing implementation requirement, not a design choice.

Mitigation: implementation must reuse the existing `UpdateState`
helper. Tests under `mesh_watch_test.go` should extend the
existing `TestHandleInboxEvent_DanglingEnvelope` fixture to cover
concurrent MCP tool calls on dangling-classified tasks (e.g.,
`niwa_query_task` racing with the daemon's transition).

### `required_skills` audit log fidelity

The body-convention placement (Decision 3) means
`extractArgKeys` does not surface `required_skills` in `arg_keys`
for successful gate assertions. Failed assertions remain visible
via `error_code: MISSING_SKILLS`. This is consistent with how
every other body field is treated today, but reduces the
auditability of "which calls successfully asserted a skill
requirement?".

Mitigation: out-of-scope for this design. If post-hoc
auditability becomes a recurring operator question, a
non-breaking `body_top_keys` extension to `AuditEntry` is the
planned escape hatch (captures sorted top-level keys of body
without values, preserving the existing no-values invariant).

## Consequences

### Positive

- The niwa-mesh skill becomes truthful again. Workers that take
  the documented patterns literally now succeed instead of being
  silently ignored.
- API responses become structurally consistent for dangling-class
  tasks. Operator scripts that branch on `state` no longer have
  to special-case the inbox filesystem layout.
- The skill no longer leaks into consumer PRs. PR diffs reflect
  only the work product, not coordination tooling internals.
- Coordinator escalation works: workers blocked on a
  not-satisfiable constraint can ask the coordinator instead of
  abandoning silently or improvising.
- Spawn failures are visible synchronously. Coordinators no
  longer queue work onto dead sessions.
- `niwa_redelegate` makes recovery cheap. Dangling, abandoned, or
  cancelled tasks can be re-fired without rewriting bodies.
- Smaller MCP wire schema than the alternative (top-level
  `required_skills` would have grown the schema linearly with
  every new precondition).
- Backward-compatible: existing fields don't change shape;
  additions are additive (`daemon` sub-object,
  `redelegated_from` envelope field, new error codes).

### Negative

- Decision 1 carries an empirical assumption about Claude Code
  flag/env support. If neither argv nor env honors the workspace
  `.claude/` redirect, the design needs to fall back to a
  filesystem mirror in `scaffoldWorktreeNiwa` for the session
  case and accept that main-instance workers need a separate fix
  (likely involving plugin-store population in the worker's
  user-level config).
- Daemon now writes `state.json` transitions, expanding the
  daemon's surface. Today only the MCP server transitions state.
  The shared taskstore writer must enforce the same flock
  discipline; a regression here would corrupt state.
- The `required_skills` body convention loses audit-log fidelity
  (`extractArgKeys` only captures top-level wire keys). Failed
  gates are still logged via `error_code: MISSING_SKILLS`;
  successful assertions are not directly grep-able from arg_keys.
- Adding `maybeRegisterCoordinator` to four handlers means a
  coordinator that has never intentionally registered now does so
  on its first `niwa_delegate` call. This is the desired
  behavior, but it changes the registration timing (no longer
  tied to `await_task`/`check_messages`).
- One role (`coordinator`) is now special in code rather than on
  disk. Reviewers grepping for `.niwa/roles/coordinator` in the
  worktree won't find it; the special-case in `roleRoot` is the
  only signal.

### Mitigations

- For the Claude Code flag/env assumption: the implementation
  experiments with `--settings` first, falls back to
  `CLAUDE_CONFIG_DIR` env at the same code site, and treats the
  filesystem-mirror path as an explicit Phase 3 follow-up issue
  if both fail.
- For the daemon-writes-state.json risk: reuse the existing
  `taskstore.go` flock'd writer rather than introducing a
  parallel writer; cover the new write path with the existing
  `mesh_watch_test.go` fixture pattern
  (`TestHandleInboxEvent_DanglingEnvelope` at line 743).
- For audit-log fidelity loss: keep `error_code: MISSING_SKILLS`
  on the failed-gate path. If post-hoc auditability of asserted
  preconditions becomes a real operator question, extend
  `AuditEntry` with a non-breaking `body_top_keys` capture in a
  follow-up.
- For the registration-timing change: document the expanded
  trigger set in the niwa-mesh skill so operators reading the
  skill understand when registration happens. The existing
  `sessions.json` audit shows registration time, so timing
  changes are observable.
- For the code-side special-case: name the helper `roleRoot`
  rather than inlining the `if role == "coordinator"`
  conditional, and add a Godoc comment that points at the
  precedent (`handleAsk`'s existing `askRoot` redirect at
  `server.go:817-819`). A reviewer who lands on the helper sees
  the rationale and the precedent in one place.

## Source Issues

This design closes the following issues:

- #92 — niwa_ask to live coordinator (partly fixed, completes the
  chain via Decision 4)
- #97 — niwa-mesh skill file leaking into consumer repos
  (resolved by Decision 1's per-repo write removal)
- #108 — workers spawn without workspace plugins (resolved by
  Decision 1)
- #109 — workers cannot reach coordinator UNKNOWN_ROLE (resolved
  by Decision 4)
- #110 — niwa_create_session must surface daemon-spawn failures
  (resolved by Phase 2's typed timeout)
- #111 — niwa_list_sessions must report daemon health (resolved
  by Phase 2's `daemon` sub-object)
- #112 — document the dangling classification and provide
  recovery (resolved by Decision 2 and `niwa_redelegate`)
- #113 — niwa_delegate should accept required_skills (resolved
  by Decision 3)
- #114 — add niwa_redelegate primitive (resolved by Phase 5)

## Exploration Artifacts

- `wip/explore_niwa-mesh-reliability_scope.md`
- `wip/explore_niwa-mesh-reliability_findings.md`
- `wip/explore_niwa-mesh-reliability_decisions.md`
- `wip/explore_niwa-mesh-reliability_crystallize.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-coordinator-routing.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-worker-spawn-environment.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-session-daemon-health.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-task-lifecycle-dangling.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-delegate-api-extensions.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-mesh-skill-contract.md`
- `wip/design_niwa-mesh-reliability_summary.md`
- `wip/design_niwa-mesh-reliability_coordination.json`
- `wip/design_niwa-mesh-reliability_decision_1_report.md`
- `wip/design_niwa-mesh-reliability_decision_2_report.md`
- `wip/design_niwa-mesh-reliability_decision_3_report.md`
- `wip/design_niwa-mesh-reliability_decision_4_report.md`
