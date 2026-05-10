---
status: Proposed
problem: |
  Nine open issues filed since #92 describe a coherent set of failures in
  the niwa mesh subsystem (multi-agent coordination): coordinator routing
  is unreachable from session workers (#92, #109), worker sessions spawn
  with a fundamentally different Claude Code configuration than a user
  running `claude` directly in the same repo would (#108), the niwa-mesh
  skill leaks into consumer repos (#97), session and daemon health are
  not surfaced through the API (#110, #111), the daemon's `dangling` task
  classification is invisible at the API and unrecoverable (#112), and
  the `niwa_delegate` surface lacks queue-time precondition checks (#113)
  and a body-reuse primitive (#114). The shared root cause is that niwa
  delivers state through filesystem side-channels that the API layer
  doesn't read or that Claude Code's discovery doesn't surface from the
  session worktree CWD. The fixes interact, so a single coordinated
  design is required: the dangling lifecycle shape (#112) affects the
  redelegate primitive (#114), the worker config inheritance contract
  (#108) affects skill delivery (#97) and the required_skills check
  (#113), and every fix touches the niwa-mesh skill text.
decision: |
  The design commits to four decisions plus a set of mechanical
  follow-ons. (1) The worker Claude-config inheritance contract is
  "a worker spawned by niwa for role R sees exactly the same Claude
  Code configuration that a user running `claude` directly in role R's
  repo would see" — implemented by passing
  `--add-dir <workspaceRoot> --add-dir <repoPath> --setting-sources user,project,local`
  uniformly to every `claude -p` spawn, regardless of whether the
  worker is a main-instance or session worker. The flag set is
  empirically verified (see Verification Notes) and idempotent for
  main-instance workers. The per-repo niwa-mesh SKILL.md writes
  become unnecessary and are removed; defense-in-depth `.gitignore`
  entries close historical leaks. (2) Dangling tasks are converted
  from a filesystem-only quarantine to a real `state.json`
  transition: the daemon writes `abandoned` with
  `reason="taskstore_lost"`, so every read API surfaces a
  structurally consistent answer; `inbox/dangling/` stays as
  forensic-only. (3) `required_skills` lives inside the task body as
  a workspace convention; a queue-time `MISSING_SKILLS` gate catches
  typos and explicit-intent drift on top of the Decision 1
  inheritance contract. (4) A `roleRoot(role)` helper redirects
  coordinator-targeted role checks and inbox writes to
  `mainInstanceRoot`, so `niwa_ask` and `niwa_send_message` reach the
  live coordinator from session workers; auto-register coordinator
  from the four handlers a fan-out coordinator actually exercises.
  Plus mechanical follow-ons: typed daemon-spawn timeout (#110), a
  computed `daemon` sub-object on `niwa_list_sessions` (#111), a new
  `niwa_redelegate` primitive that reads the source envelope
  server-side and stamps `redelegated_from` (#114), and a niwa-mesh
  skill rewrite so the documented contract matches the runtime.
rationale: |
  The contract framing — "spawned worker matches a user's `claude` in
  the repo" — is the smallest mental model that covers every concern.
  The verified flag set
  (`--add-dir <workspaceRoot> <repoPath> --setting-sources user,project,local`)
  uses Claude Code primitives that are visible in `claude --help` and
  documented as the explicit-context channel for the `--bare` mode,
  so we are not betting on undocumented env vars or implicit
  filesystem walk-up. The flags are idempotent for main-instance
  spawns (same flag set on a CWD that already has standard discovery
  produces the same baseline output), so the same code path serves
  both spawn types — symmetry by construction. Reusing
  `TaskStateAbandoned` with a typed reason avoids growing the state
  alphabet. The body convention for `required_skills` keeps niwa's
  MCP wire schema small and forward-compatible as more workspace
  preconditions emerge, and it composes with `niwa_redelegate` for
  free because redelegate already reads the source body. The
  `roleRoot` helper extends the same `mainInstanceRoot` redirect
  `handleAsk` already establishes for `askRoot`. Each rejected
  alternative either keeps a filesystem side-channel that the
  empirical work traced as unreliable, splits the body convention
  across schema migrations, or introduces a synthetic filesystem dir
  whose only role would be to mislead readers and re-enable the
  ephemeral-coordinator spawn path PR #93 already removed.
---

# DESIGN: niwa mesh reliability

## Status

Proposed

## Context and Problem Statement

Nine open issues filed against the niwa repo since #92 cluster around
the mesh subsystem: routing, lifecycle, observability, recovery, and
worker bootstrap. Issues are #92, #97, #108, #109, #110, #111, #112,
#113, and #114.

The exploration that produced this design is preserved at
`wip/explore_niwa-mesh-reliability_findings.md` and the six per-lead
research files under `wip/research/explore_niwa-mesh-reliability_r1_lead-*.md`.
The Decision 1 mechanism in particular was validated by direct
experimentation; the recorded results are at
`wip/design_niwa-mesh-reliability_claude_config_experiments.md`.

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

- **The niwa repo's `.claude/` directory is not git-tracked.**
  `git ls-files` in `public/niwa` lists only `CLAUDE.md`. The entire
  `.claude/` tree (settings.local.json, hooks, skills,
  shirabe-extensions, scheduled_tasks.lock) is untracked. A fresh
  `git worktree add` of the niwa repo creates a worktree with NO
  `.claude/` directory at all — which is exactly the broken state
  #108 reports. The worker has CWD inside a directory that has no
  Claude configuration of its own, and falls back to whatever
  filesystem walk-up surfaces.

- **Worker spawn at `internal/cli/mesh_watch.go:908-1016`** invokes
  `claude -p` with only `--mcp-config=...`, `--strict-mcp-config`,
  and `--allowed-tools <list>`. No `--add-dir`, no
  `--setting-sources`, no `--settings`, no `--plugin-dir`. The
  worker's Claude-config discovery is left entirely to filesystem
  walk-up from CWD plus user-level `~/.claude.json`.

- **For session workers, walk-up does not surface the workspace
  plugin set.** Verified empirically (Verification Notes, Experiment
  D): from a worktree CWD under `<workspaceRoot>/.niwa/worktrees/<id>/`,
  Claude Code reaches all CLAUDE.md files in the chain, but the
  workspace's `enabledPlugins` from `<workspaceRoot>/.claude/settings.json`
  is not honored. shirabe and tsukumogami plugins do not load. The
  niwa-mesh skill at `<workspaceRoot>/.claude/skills/niwa-mesh/SKILL.md`
  is not visible. This reproduces #108.

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
relies on Claude Code's discovery to find — and Claude Code's
default discovery does not surface workspace plugins from a session
worktree. Each fix moves something from "filesystem convention" to
"explicit contract" — argv-flag config injection replaces CWD-walk
discovery, daemon-driven state transitions replace inbox quarantine,
a `roleRoot` helper replaces worktree-vs-main asymmetry, and a
synchronous timeout replaces silent fall-through.

## Decision Drivers

- **Workers inherit the same Claude config the repo's user would
  see.** A worker spawned by niwa for role R must, as its baseline,
  see exactly the same Claude Code configuration (settings,
  plugins, skills, hooks, marketplaces, CLAUDE.md chain) that a
  human user would see by running `claude` directly in role R's
  repo directory. This rule applies uniformly to main-instance and
  session workers — there is no design reason for the two paths to
  diverge.

- **Bring runtime and skill text back into lockstep.** The
  niwa-mesh skill is the canonical user-facing contract. Every fix
  that lands in isolation drifts the skill further from the truth.
  A coordinated design lets the skill update happen once.

- **Prefer surgical changes that reuse existing primitives.** Most
  of the infrastructure is already on disk: `daemon.pid`,
  `IsPIDAlive`, `lookupLiveCoordinator`, the flat task store, the
  five-state task lifecycle. The cleanest fixes are one-function
  changes that propagate signals already produced.

- **Leave room for future per-spawn customization without locking
  it in now.** The user contract for this design is the baseline
  ("same as `claude` in the repo"); future work may add per-spawn
  skills/hooks (either niwa-driven or coordinator-driven). The
  design must not preclude that future path. The mechanism chosen
  for Decision 1 (additional argv flags at spawn time) composes
  cleanly with future additional flags appended for per-spawn
  extras.

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

These were settled during exploration and validated against the
empirical work; they are constraints, not reopened in design:

- Treat the cluster as one design, not nine bugfixes.
- #92 and #109 collapse into a single fix.
- `Status` field stays single-writer; add an orthogonal `daemon`
  sub-object for #111.
- No new daemon heartbeat file; `daemon.pid` + `IsPIDAlive` is
  sufficient.
- `niwa_redelegate` is the single documented recovery path (no
  opt-in resurrect primitive).
- Decision 1's mechanism is empirically verified Claude Code argv
  flags, not a hypothesized environment variable. Verification
  results are in `wip/design_niwa-mesh-reliability_claude_config_experiments.md`.

## Considered Options

### Decision 1: Worker Claude-config inheritance contract

**Issues:** #108 (workers see no workspace plugins), #97 (skill
file committed by delegated agents into consumer repo PRs).

**The contract.** A worker spawned by niwa for role R sees, as its
baseline, exactly the same Claude Code configuration a human user
would see by running `claude` directly in role R's repo:

- Workspace settings (`<workspaceRoot>/.claude/settings.json`),
  including `enabledPlugins`, `extraKnownMarketplaces`, hooks
  declarations, and any other workspace-wide config.
- The repo's local settings (`<repoPath>/.claude/settings.local.json`),
  same field set.
- The `<workspaceRoot>/.claude/skills/` tree, including
  `niwa-mesh/SKILL.md`.
- The repo's `.claude/skills/` tree, plus repo `.claude/hooks/` and
  `.claude/shirabe-extensions/` (or any sibling tooling
  directories).
- The CLAUDE.md chain reachable from the repo working directory.
- The user-level config from `~/.claude/` and `~/.claude.json` —
  same HOME the coordinator runs under.

This applies uniformly to both spawn paths:

- **Main-instance worker** (CWD = `<workspaceRoot>/<group>/<repo>`).
- **Session worker** (CWD = `<workspaceRoot>/.niwa/worktrees/<repo>-<id>/`).

The two paths must produce the same baseline. There is no design
reason for them to diverge.

**The MCP server inheritance carve-out stays.** The existing
`--strict-mcp-config` argv (`mesh_watch.go:982-988`) was added
deliberately to scope MCP server inheritance, isolating workers
from `~/.claude.json`'s MCP server entries. That carve-out remains
explicit. The design treats it as the precedent for "everything
else inherits, MCP servers are scoped" — same shape, different
subject.

**What the empirical work (Verification Notes) settled.**
`CLAUDE_CONFIG_DIR` is not a documented Claude Code mechanism for
project-level config, and a filesystem mirror inside the worktree
is fragile and asymmetric. The actual primitives Claude Code
provides — visible in `claude --help` — are documented argv flags:
`--add-dir <directories...>`, `--setting-sources <sources>`,
`--settings <file-or-json>`, `--plugin-dir <path>`. The
`--bare` mode description names these as the "explicit context"
channel.

**These flags are not interchangeable.** Claude Code loads skills
via two distinct mechanisms, and the worker contract requires both
to be fed:

- **Plugin skills.** A plugin is referenced by an alias (e.g.
  `shirabe@shirabe`) inside `enabledPlugins` in `settings.json`.
  Claude Code resolves the alias against the user-level plugin
  store (`~/.claude.json`) and any marketplaces declared in
  `extraKnownMarketplaces`, fetches the plugin's content, and
  exposes its skills under the alias's namespace (`shirabe:plan`,
  `shirabe:design`, etc.). `--settings <file>` and
  `--setting-sources` feed this path — they tell Claude which
  settings layers and marketplaces to consult for plugin
  resolution.

- **Plain skills.** A plain skill is a `SKILL.md` file under
  `<project>/.claude/skills/<name>/`. Claude Code discovers it
  by scanning the `.claude/skills/` directory of project roots,
  where project roots come from CWD walk-up plus any explicitly
  added directories. niwa-mesh is delivered this way: it lives at
  `<workspaceRoot>/.claude/skills/niwa-mesh/SKILL.md` with no
  plugin manifest. `--add-dir <dir>` feeds this path — it puts
  `<dir>` in scope as a project root for plain-skill (and
  CLAUDE.md) discovery.

The empirical confirmation is in Experiment F (Verification Notes):
`--settings <ws-settings>` alone surfaces the workspace's
plugin-resolved skills (10 shirabe + 47 tsukumogami) but leaves
niwa-mesh invisible because the workspace's `.claude/skills/`
directory was never scanned. The verified flag set
(`--add-dir <ws> <repo> --setting-sources user,project,local`)
covers both load paths: `--add-dir` for plain skills and CLAUDE.md,
`--setting-sources user,project,local` for the layered settings
that drive plugin resolution. Choosing only one flag would silently
ship a worker that has either plugin skills or plain skills but
not both.

#### Chosen: Pass `--add-dir <workspaceRoot> --add-dir <repoPath> --setting-sources user,project,local` to every `claude -p` spawn

`spawnWorker` (`internal/cli/mesh_watch.go:908-1016`) appends three
new argv items to its `claude -p` invocation, in this order:

1. `--add-dir <workspaceRoot>` (`s.mainInstanceRoot` if non-empty,
   else `s.instanceRoot`).
2. `--add-dir <repoPath>` (the role's repo working directory,
   computed via existing `resolveRoleCWD` logic at
   `mesh_watch.go:2315-2342`).
3. `--setting-sources user,project,local`.

Both spawn paths use the same flag set. For main-instance workers
(CWD = `<repoPath>`), the flags are idempotent at the live-repo CWD
(verified: Experiments B and I produce identical results). For
session workers (CWD = worktree), the flags bring the workspace and
repo `.claude/` trees into Claude Code's discovery scope, matching
the live-repo baseline.

`InstallChannelInfrastructure` (`internal/workspace/channels.go:347-359`)
no longer writes `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for
non-coordinator roles. The instance-root copy at
`<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` (line 341)
remains; workers find it through `--add-dir <workspaceRoot>`.

Defense in depth: `internal/workspace/gitignore.go` is extended to
add `.claude/skills/niwa-mesh/` to each consumer repo's `.gitignore`
on apply, protecting against any historical commits and any future
regressions that re-introduce the per-repo write.

**Rationale.** The flag set is the smallest empirically verified
mechanism that satisfies the contract. It uses documented Claude
Code primitives (visible in `--help`), is idempotent for the
already-working main-instance case, and removes the niwa-mesh skill
leak by making the per-repo writes structurally unnecessary. It
introduces no new on-disk artifacts inside the worktree, no
filesystem mirror, no symlinks, and no env-variable contracts that
might silently change.

The flags also leave the future per-spawn customization path open.
Niwa or the coordinator can later append additional `--add-dir`,
`--plugin-dir`, or `--add-dir` arguments at spawn time to layer
extras on top of the baseline; the mechanism is additive.

#### Alternatives Considered

- **Filesystem mirror in `scaffoldWorktreeNiwa` (symlink or copy
  `<mainInstance>/.claude/` into the worktree).** Rejected because
  it re-introduces filesystem coupling exactly where the design is
  trying to remove it for #97 — a copied skill in
  `<worktree>/.claude/skills/` is one mis-configured `.gitignore`
  away from re-leaking. It also doesn't help the main-instance
  spawn path. Asymmetric mechanism is exactly what the user
  feedback ruled out.

- **`CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` env var.** Rejected
  because it is not a documented Claude Code mechanism for
  project-level config. Researching Claude Code's flag surface
  (`claude --help`) showed `CLAUDE_CONFIG_DIR` is not the channel
  for project context; it would have controlled user-level config
  location and is the wrong tool for this design.

- **`--settings <workspace-settings.json>` only (no `--add-dir`).**
  Rejected by direct measurement (Verification Notes, Experiment
  F): `--settings` honors the file's `enabledPlugins` but does not
  surface the workspace's `.claude/skills/niwa-mesh/SKILL.md` in
  the worker's skill list. `--settings` and `--add-dir` are
  complementary, not interchangeable.

- **Status quo (rely on Claude Code CWD-walk).** Rejected by direct
  measurement (Experiment D): from a niwa-canonical worktree path,
  walk-up reaches CLAUDE.md but does not surface the workspace
  plugin set. shirabe and tsukumogami plugin counts come back as
  zero, niwa-mesh is not visible. The status quo is the bug.

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

**Relationship to Decision 1's inheritance contract.** Once
Decision 1 commits workers to the baseline inheritance contract,
the value of `required_skills` shifts. Workers WILL have the
workspace plugin set as their baseline (Decision 1 guarantees it).
The gate's remaining utility is:

1. Catching typos in skill names in the body
   (`/shirabe:prd` vs. `/shirabe:rpd`) — fail fast at queue time
   instead of mid-task.
2. Coordinator declaring intent — "this body uses /shirabe:prd" —
   so audit logs and future tooling can read the requirement.
3. Detecting drift in cross-workspace redelegations — if a coordinator
   redelegates to a different session whose workspace happens not to
   have the plugin, the gate fails fast.
4. Preparing for the "future per-spawn customization" path — a
   future iteration could read `body.required_skills` to dynamically
   pass additional `--plugin-dir` flags at spawn time, layering
   per-task skills on top of the baseline.

The gate's role drops from "load-bearing prerequisite for delegation"
(its original framing under #113's narrative) to "declarative
assertion that catches drift and prepares for dynamic loading." The
placement debate is now about where to put a body-level intent
assertion, not where to put a runtime requirement.

Key assumptions:
- The manifest the gate reads is the same workspace `.claude/`
  tree Decision 1's flag injection delivers to workers. The gate is
  consistent with what the worker will actually see.
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
   manifest (read by enumerating `<workspaceRoot>/.claude/skills/`
   and the resolved enabledPlugins from the workspace settings —
   the same configuration Decision 1's flags deliver to workers).
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

The body convention also aligns with the future per-spawn
customization path. If niwa later reads `body.required_skills` at
spawn time and translates missing entries into additional
`--plugin-dir` arguments, the same body field doubles as the
queue-time gate input AND the spawn-time customization input.

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

- **Drop #113 from this design.** Considered explicitly given
  Decision 1's inheritance contract. Kept in scope because (a) the
  typo and intent-declaration value remains, (b) the body-convention
  cost is small (one unmarshal in one handler), and (c) the field
  is the natural input for the future per-spawn skill-augmentation
  path. The reduced urgency is acknowledged in the framing above;
  Phase 5 of the implementation approach reflects the lower
  priority by sequencing this gate after the inheritance contract
  lands.

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
Decision 1 establishes the worker config inheritance contract and
delivers it via verified Claude Code argv flags; that contract
removes the side-channel mechanism that fed both #108 and #97 and
gives Decision 3's gate a concrete manifest to read. Decision 4
makes the coordinator reachable from session workers (closing #92
and #109) via the same `mainInstanceRoot` redirect mechanism
already used elsewhere. Decision 2 converts a daemon-internal
observable (`dangling`) into a real state-machine transition that
every read API surfaces honestly, removing the
`{too_late, queued}` contradiction and making `niwa_redelegate`
the single documented recovery path. Decision 3 keeps the wire
schema small while wiring the new queue-time precondition (#113)
and ensuring it survives redelegation (#114) for free.

The secondary fixes (#110 typed timeout, #111 `daemon` sub-object,
#114 redelegate primitive, skill text rewrite) follow mechanically
from the four decisions and don't require independent design.

The future per-spawn customization path is not built by this
design, but it is not blocked either. Decision 1's argv-flag
mechanism is additive — niwa or the spawning coordinator can
append more `--add-dir`, `--plugin-dir`, or
`--append-system-prompt` arguments at spawn time without
re-architecting. Decision 3's body convention provides the
declarative input for any future per-spawn skill augmentation.

## Solution Architecture

### Overview

The mesh remains the same multi-agent coordination layer it is
today: coordinator delegates tasks to workers via niwa_* MCP
tools and per-role inboxes under
`<worktree>/.niwa/roles/<role>/`. The reliability pass changes
four things:

1. **Spawn-time config** flows programmatically into worker
   Claude Code processes via documented argv flags — so workers
   in both spawn paths see the same config a user would running
   `claude` directly in the repo.
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

### Worker config inheritance contract

The contract that Decision 1 commits to:

> A worker spawned by niwa for role R sees, as its baseline, the
> same Claude Code configuration a user would see by running
> `claude` directly in role R's repo directory.

Implemented by passing this fixed flag set on every `claude -p`
spawn:

```
claude -p \
  --mcp-config=<path>           (existing)
  --strict-mcp-config           (existing — MCP server scoping carve-out)
  --allowed-tools <list>        (existing)
  --add-dir <workspaceRoot>     (NEW)
  --add-dir <repoPath>          (NEW)
  --setting-sources user,project,local  (NEW)
  -p <prompt>
```

Where:
- `<workspaceRoot>` = `s.mainInstanceRoot` if non-empty (session
  worker), else `s.instanceRoot` (main-instance worker).
- `<repoPath>` = the role's repo working directory, computed via
  `resolveRoleCWD` (`mesh_watch.go:2315-2342`) — the same path
  that determines the worker's CWD for main-instance spawns.

Both spawn paths use the same flag set. For main-instance workers,
the flags are idempotent at the live-repo CWD (verified
empirically). For session workers, the flags bring the workspace
and repo `.claude/` trees into Claude Code's discovery scope.

What workers inherit, concretely:
- Workspace settings (`<workspaceRoot>/.claude/settings.json`),
  including `enabledPlugins` and `extraKnownMarketplaces`.
- The repo's local settings
  (`<repoPath>/.claude/settings.local.json`), same field set.
- Workspace skills (`<workspaceRoot>/.claude/skills/`), including
  `niwa-mesh/SKILL.md`.
- The repo's own skills, hooks, and any sibling tooling
  directories under `<repoPath>/.claude/`.
- The CLAUDE.md chain reachable from the repo (typically: workspace
  CLAUDE.md, intermediate group CLAUDE.md, repo CLAUDE.md).
- User-level `~/.claude/` and `~/.claude.json` (via inherited
  HOME), modulo the existing `--strict-mcp-config` carve-out for
  MCP servers.

What is NOT inherited (and is intentionally scoped):
- MCP servers from `~/.claude.json` — already scoped away by
  `--strict-mcp-config`.

### Components

**Modified components:**

- `internal/cli/mesh_watch.go`
  - `spawnWorker` (≈908-1016): append the three new flags
    (`--add-dir <workspaceRoot>`, `--add-dir <repoPath>`,
    `--setting-sources user,project,local`) to the `claude -p`
    argv. The values are computed from `s.mainInstanceRoot`/
    `s.instanceRoot` and the existing role-to-repo resolution.
  - `handleInboxEvent` (≈776-803): on missing state.json for a
    `task.delegate` envelope, transition state.json to
    `abandoned` with `reason="taskstore_lost"` (recreating the
    state.json stub if the task dir was wiped). The envelope
    still moves to `inbox/dangling/` as forensic-only.

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
  - Add a section that documents the worker config inheritance
    contract (what workers inherit, what is scoped via
    `--strict-mcp-config`, where to look for divergence).

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

Authorization: `kindDelegator` on `source_task_id`. Any source
state is permitted — `queued`, `running`, `completed`, `abandoned`,
or `cancelled`. The new envelope's `from` is the caller's role/PID;
`redelegated_from` points to the source. The source task's state is
unchanged by the call (an active source keeps running; a terminal
source stays terminal). The same `required_skills` gate runs against
the merged body.

The response always carries `source_state_at_fork` so the caller
can distinguish recovery flows (source was terminal) from active
forks (source was queued or running):

```json
{
  "task_id": "<new-task-id>",
  "redelegated_from": "<source-task-id>",
  "source_state_at_fork": "running"
}
```

When `source_state_at_fork` is `queued` or `running`, the caller has
forked active work — both the source and the new task may run to
completion in parallel. This is permitted (the same fan-out shape is
already available via N parallel `niwa_delegate` calls), but the
caller is expected to read the field and decide whether parallel
execution is safe for the body in question. Bodies with non-idempotent
external side effects (creating PRs, modifying shared state) need
explicit caller intent before forking.

The two corner cases:
- **`SOURCE_BODY_LOST`** — returned when source envelope.json is
  missing entirely (the rare `taskstore_lost` recreate-stub case).
  The caller can either re-supply the body via `body_overrides` or
  give up.
- **Source has terminated by the time the new task starts** — fine.
  The redelegated task is independent; it doesn't depend on the
  source's state at any point after the fork.

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

The field set is intentionally minimal — it answers "is this
session usable?" without requiring new daemon-side instrumentation.
Issue #111 also requested `last_claim_at`, `last_progress_at`, and
`watcher_count` for fleet-level observability of mesh activity and
resource utilization. Those fields require either a new daemon-side
heartbeat write path or fragile log parsing, and the "no new
daemon heartbeat" decision was settled during exploration. The
deferred scope is captured in #116 (`needs-prd`) so future
development starts from a requirements definition rather than
extending this design.

**`task.ask` / `task.delegate` vocabulary, documented:**

The skill's Message Vocabulary section is rewritten to list:
`task.delegate`, `task.ask`, `task.progress`, `task.completed`,
`task.abandoned`, `task.cancelled`. The dead
`question.ask`/`question.answer`/`status.update` entries are
removed (no handler dispatched them).

### Data Flow

**Worker spawn (post-fix), main-instance path:**

```
daemon receives task.delegate envelope, resolves role to <repoPath>
  -> spawnWorker constructs argv:
     claude -p
       --mcp-config=...                   (existing)
       --strict-mcp-config                (existing)
       --allowed-tools ...                (existing)
       --add-dir <workspaceRoot>          (NEW)
       --add-dir <repoPath>               (NEW)
       --setting-sources user,project,local  (NEW)
       <prompt>
  -> CWD = <repoPath>
  -> Claude Code surfaces the same config a user would see running
     `claude` in <repoPath>: workspace settings, repo settings.local,
     workspace and repo skills, niwa-mesh, hooks, marketplaces, the
     CLAUDE.md chain.
```

**Worker spawn (post-fix), session path:**

```
handleCreateSession
  -> EnsureDaemonRunning  (returns ErrDaemonSpawnTimeout on 500ms timeout)
     -> on success: daemon writes daemon.pid after fsnotify register
     -> on timeout: cleanupWorktree, rollback session state, errResult
  -> daemon spawns worker via spawnWorker — SAME argv as main-instance:
     claude -p
       --mcp-config=...
       --strict-mcp-config
       --allowed-tools ...
       --add-dir <workspaceRoot>          (= s.mainInstanceRoot)
       --add-dir <repoPath>               (resolveRoleCWD)
       --setting-sources user,project,local
       <prompt>
  -> CWD = <worktreePath>
  -> Claude Code surfaces the same config the main-instance worker
     would see: workspace + repo settings, skills, niwa-mesh, hooks,
     marketplaces. Plus the worktree's CLAUDE.md (which is the same
     as the repo's, since CLAUDE.md is git-tracked).
```

The two spawn paths produce the same baseline by construction — same
argv, same flag values for the same `<workspaceRoot>`/`<repoPath>`
pair.

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
phase is small enough to ship as one PR; `/plan` will turn this
into a milestone of issues.

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

This phase has no dependency on the worker config inheritance
contract; it ships first because it unblocks meaningful end-to-end
tests of the rest.

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

### Phase 3: Worker Claude-config inheritance contract

**Closes #108. Resolves #97 by elimination.**

Deliverables:
- `spawnWorker` (`internal/cli/mesh_watch.go:982-1009`) appends
  the three argv items in the order specified in Solution
  Architecture: `--add-dir <workspaceRoot>`,
  `--add-dir <repoPath>`, `--setting-sources user,project,local`.
- `InstallChannelInfrastructure` removes the per-repo skill write
  loop. Instance-root copy stays.
- `internal/workspace/gitignore.go` extended to add
  `.claude/skills/niwa-mesh/` to consumer repos' `.gitignore` on
  apply.
- Functional tests:
  1. A main-instance worker spawned by `niwa_delegate` sees
     identical workspace plugins, niwa-mesh skill, and
     CLAUDE.md chain as a user running `claude` in the repo.
  2. A session worker spawned by `niwa_create_session` +
     `niwa_delegate` sees the same config as the main-instance
     worker (symmetry test).
  3. Workspace-defined hooks (e.g., `PreToolUse`) fire inside
     the worker session.
  4. The niwa-mesh skill is invocable from the worker (e.g., the
     worker can call into mesh tooling described in
     `niwa-mesh/SKILL.md`).
  5. After `niwa apply`, no consumer repo working tree contains
     `.claude/skills/niwa-mesh/SKILL.md`.

Phase 3 unblocks Phase 5's `required_skills` gate (the gate's
manifest is the workspace `.claude/` tree this phase makes
authoritative).

### Phase 4: Task lifecycle truthfulness

**Closes #112.**

Deliverables:
- `handleInboxEvent` writes state.json transition to
  `abandoned` with `reason="taskstore_lost"`. Recreates state.json
  stub when it was missing entirely.
- Daemon-internal taskstore writer (shared with the MCP server's
  flock'd writer; do NOT introduce a parallel writer).
- `niwa_query_task`, `niwa_list_outbound_tasks`, `niwa_await_task`,
  `niwa_cancel_task`, `niwa_update_task` verify against the new
  abandoned-with-reason output. Existing terminal-state guards
  cover most of this; only the cancel path needs an early state
  guard added to remove the `{too_late, queued}` contradiction.
- Test fixture update: `TestHandleInboxEvent_DanglingEnvelope`
  (`mesh_watch_test.go:743-784`) verifies state.json transition.

Independent of Phase 3.

### Phase 5: Coordinator ergonomics

**Closes #113 and #114.** Lower priority than Phases 1–4 (per
Decision 3 framing — the gate's value-prop reduces under
Decision 1's inheritance contract).

Deliverables:
- `niwa_redelegate` MCP tool registration, `handleRedelegate`
  handler, `redelegated_from` envelope field.
- `required_skills` body peek and `MISSING_SKILLS` error code in
  `handleDelegate` and `handleRedelegate`. The manifest read at
  gate time enumerates `<workspaceRoot>/.claude/skills/` and
  resolves enabledPlugins from the workspace settings.
- Functional tests:
  1. Redelegate from `abandoned` source produces a new task with
     the source's body verbatim; the response carries
     `source_state_at_fork: "abandoned"`.
  2. Redelegate from a `taskstore_lost` source where envelope.json
     survived works.
  3. Redelegate where envelope.json is also missing returns
     `SOURCE_BODY_LOST`.
  4. Redelegate from a `running` source produces a new independent
     task, the source continues to run, and the response carries
     `source_state_at_fork: "running"`. Both tasks reach terminal
     states independently.
  5. Redelegate from a `queued` source produces a new task; both
     are claimable by their target sessions independently;
     response carries `source_state_at_fork: "queued"`.
  6. The `required_skills` gate fires for a typo and returns the
     correct `{missing, available}` shape.

Depends on Phase 3 (manifest source-of-truth) and Phase 4
(`abandoned` source state for redelegate from dangling tasks).

### Phase 6: Skill text and guides update

Deliverables:
- `buildSkillContent` rewrite per the contract audit in
  `wip/research/explore_niwa-mesh-reliability_r1_lead-mesh-skill-contract.md`.
- `docs/guides/sessions.md` update for the new `daemon`
  sub-object, `taskstore_lost` recovery, and the worker config
  inheritance contract.
- Diff-vs-fixture CI check for `buildSkillContent` output to
  catch future drift.

Depends on Phases 1–5 landing so the skill text reflects the
merged runtime.

### Sequencing summary

```
Phase 1 ──┐
Phase 2 ──┤
Phase 3 ──┼─► Phase 5 ──┐
Phase 4 ──┘              ├─► Phase 6
                Phase 4 ─┘
```

Phases 1, 2, and 4 are independent of everything else and can ship
in parallel. Phase 3 unblocks Phase 5. Phase 4 unblocks Phase 5
only for the dangling-source redelegate case. Phase 6 lands last
so the skill text reflects the merged runtime.

## Security Considerations

A dedicated security review of the design surfaced no high- or
medium-severity findings. The design preserves niwa's existing
authorization invariants and does not expand the trust boundary.
Five lower-severity considerations the implementer should know
about:

### Trust model is unchanged

niwa is single-tenant per workspace by design. The worker config
inheritance contract (Decision 1) formalizes the trust
relationship that already exists between `niwa apply` and the
workspace `.claude/` tree — it does not expand what's trusted.
Plugin alias resolution still goes through the user-level
`~/.claude.json` plugin store, which the user controls; niwa
never installs plugins on the user's behalf. The
`daemon: {alive, pid, started_at}` sub-object on
`niwa_list_sessions` exposes nothing that a same-UID process
can't already read from `/proc`.

### `--add-dir` and `--setting-sources` formalize existing trust

The two new argv flags Decision 1 adds (`--add-dir <workspaceRoot>`
plus `--add-dir <repoPath>` plus `--setting-sources user,project,local`)
make the worker's Claude-config trust scope explicit. The
workspace `.claude/` and repo `.claude/` trees are already
trusted by `niwa apply` (it writes them) and by the coordinator
(it runs in their discovery scope); the design extends the same
trust to workers, which run on the same user account on the same
machine. No new code-execution surface is introduced.

The carve-out `--strict-mcp-config` (existing) continues to scope
MCP server inheritance away from `~/.claude.json`. Decision 1
does not relax that carve-out; MCP servers reach workers only via
explicit `--mcp-config <path>`.

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

The new primitive reads the source envelope server-side and
reuses the body unless `body_overrides` is provided.
Time-sensitive material in the source body (short-lived tokens,
deadlines, references to ephemeral artifacts) travels forward
across redelegations. This is not a privilege escalation —
`kindDelegator` auth guarantees the new delegator is the same
role as the original, so they already had access to the body —
but it is a footgun.

Mitigation: the niwa-mesh skill documents that callers must pass
`body_overrides` to refresh time-sensitive fields when
redelegating. The `redelegated_from` envelope field provides an
audit chain so operators can identify and rotate any leaked
material.

### Daemon-driven `state.json` writes need flock discipline

Decision 2 expands the daemon's surface to include writing
`state.json` transitions. The existing taskstore writer in
`internal/mcp/taskstore.go` already provides flock'd
read-modify-write semantics; the daemon's transition path must
use the same helper rather than introducing a parallel writer.
This is a load-bearing implementation requirement, not a design
choice.

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
- Symmetric worker spawn paths. Main-instance and session workers
  receive the same flag set; the same argv code constructs the
  invocation; debugging one applies to debugging the other.
- The future per-spawn customization path is unblocked. The
  argv-flag mechanism composes cleanly with future additional
  `--add-dir`, `--plugin-dir`, and `--append-system-prompt`
  arguments at spawn time.

### Negative

- The Decision 1 verification surfaced one small unexplained gap:
  from a niwa-canonical worktree path with the new flag set, the
  worker shows shirabe skill count of 9 vs 11 from the live repo
  CWD. The functional contract (niwa-mesh visible, plugins
  loadable) holds in both cases; the count discrepancy does not
  affect any plugin's availability. The gap is documented as an
  open item in Verification Notes for implementation-time
  follow-up.
- Daemon now writes `state.json` transitions, expanding the
  daemon's surface. Today only the MCP server transitions state.
  The shared taskstore writer must enforce the same flock
  discipline; a regression here would corrupt state.
- The `required_skills` body convention loses audit-log fidelity
  (`extractArgKeys` only captures top-level wire keys). Failed
  gates are still logged via `error_code: MISSING_SKILLS`;
  successful assertions are not directly grep-able from arg_keys.
- Adding `maybeRegisterCoordinator` to four handlers means a
  coordinator that has never intentionally registered now does
  so on its first `niwa_delegate` call. This is the desired
  behavior, but it changes the registration timing.
- One role (`coordinator`) is now special in code rather than on
  disk. Reviewers grepping for `.niwa/roles/coordinator` in the
  worktree won't find it; the special-case in `roleRoot` is the
  only signal.

### Mitigations

- For the 9-vs-11 shirabe count gap: implementation Phase 3 must
  investigate the source (likely `<repoPath>/.claude/shirabe-extensions/`
  resolution under multi-`--add-dir`) and either produce full count
  parity or document the residual gap with concrete justification.
  If full parity requires a different flag combination, the design
  flexes — the contract is "same baseline behavior", not "same
  flag set". Phase 3 acceptance includes a symmetry test that
  compares main-instance and session-worker outputs at delegation
  time.
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
- For the code-side `coordinator` special-case: name the helper
  `roleRoot` rather than inlining the conditional, and add a
  Godoc comment that points at the precedent (`handleAsk`'s
  existing `askRoot` redirect at `server.go:817-819`). A
  reviewer who lands on the helper sees the rationale and the
  precedent in one place.

## Future Path: per-spawn customization

This design commits to the baseline contract only: "spawned
worker matches a user's `claude` in the repo". A follow-on (not
in scope, not blocked) extends the baseline with per-spawn
customizations:

- **Niwa-driven extras.** Niwa could define a per-role
  customization manifest (e.g.,
  `<workspaceRoot>/.niwa/spawn-extras/<role>.json`) listing
  additional `--plugin-dir`, `--add-dir`, or
  `--append-system-prompt` arguments to layer on every spawn for
  that role. `spawnWorker` reads the manifest at spawn time and
  appends the entries to the existing argv.

- **Coordinator-driven extras.** A future `niwa_delegate` or
  `niwa_redelegate` could accept an additional body field (e.g.,
  `body.spawn_extras: { add_dirs: [...], plugin_dirs: [...] }`)
  that the daemon translates into spawn-time argv entries for
  that one task. The body convention from Decision 3 already
  supports this shape.

- **Dynamic skill provisioning.** A future iteration could read
  `body.required_skills` at spawn time, check each entry against
  the workspace baseline, and dynamically pass `--plugin-dir
  <dynamically-fetched-skill-path>` to the worker if the skill
  is missing. This makes the gate from Decision 3 actively
  remediating rather than purely declarative.

The argv-flag mechanism Decision 1 commits to is additive — more
arguments can be appended at the same code site without
re-architecting. The body convention Decision 3 commits to is
forward-compatible — new body keys can carry per-task spawn
hints without changing the wire schema. Neither this design's
implementation nor its tests preclude any of the above paths.

## Verification Notes

Decision 1's mechanism was validated by direct experiment, not
inferred. The recorded results live at
`wip/design_niwa-mesh-reliability_claude_config_experiments.md`
(visible while the design is on its docs branch; cleaned at PR
merge per the project convention — but the key results are
summarized below for the design's permanent record).

### What was tested

The probe was a `claude -p` invocation that asked Claude to print a
JSON object describing its CWD, the count of available skill ids
starting with `shirabe:` and `tsukumogami:`, whether `niwa-mesh`
was visible in its skill list, and the count of CLAUDE.md /
CLAUDE.local.md files in its initial system context.

Each cell in the results table below is the output of one such
invocation; flags shown are passed in addition to
`--no-session-persistence --output-format json --max-budget-usd 0.50 --print`.

### Results

| ID | CWD | Flags added | shirabe | tsukumogami | niwa-mesh | CLAUDE.md |
|---|---|---|---|---|---|---|
| A | workspace root | (none) | 9 | 47 | true | 1 |
| B | live niwa repo | (none) | 11 | 47 | true | 3 |
| D | niwa-canonical worktree | (none) | 0 | 0 | **false** | 3 |
| F | niwa-canonical worktree | `--settings <ws-settings>.json` | 10 | 47 | **false** | 3 |
| G | niwa-canonical worktree | `--add-dir <ws> <repo> --setting-sources user,project,local` | 9 | 47 | true | 3 |
| I | live niwa repo | `--add-dir <ws> <repo> --setting-sources user,project,local` | 11 | 47 | true | 3 |

### Key conclusions

1. **The status quo is broken from a session worktree** (Experiment
   D: 0 shirabe, 0 tsukumogami, niwa-mesh not visible).
2. **`--settings <path>` alone is insufficient** (Experiment F:
   loads settings but niwa-mesh remains invisible).
3. **`--add-dir <workspaceRoot> <repoPath>` plus
   `--setting-sources user,project,local` works for both spawn
   paths** (Experiments G and I produce the same workable set;
   I matches the no-flags baseline B exactly).
4. **A 2-skill shirabe count gap** exists between worktree-with-flags
   (9) and live-repo (11). The functional contract holds in both
   cases (niwa-mesh visible, plugins loadable); the count
   discrepancy is documented as an open implementation-phase
   investigation.
5. **The niwa repo's `.claude/` is not git-tracked.** A fresh
   `git worktree add` creates a worktree with no `.claude/`; the
   workspace and repo `.claude/` trees must reach the worker via
   the new flag set, not via worktree-resident files.

## Source Issues

This design closes the following issues:

- #92 — niwa_ask to live coordinator (partly fixed, completes the
  chain via Decision 4)
- #97 — niwa-mesh skill file leaking into consumer repos
  (resolved by Decision 1's per-repo write removal)
- #108 — workers spawn without workspace plugins (resolved by
  Decision 1's inheritance contract)
- #109 — workers cannot reach coordinator UNKNOWN_ROLE (resolved
  by Decision 4)
- #110 — niwa_create_session must surface daemon-spawn failures
  (resolved by Phase 2's typed timeout)
- #111 — niwa_list_sessions must report daemon health (resolved
  by Phase 2's `daemon` sub-object)
- #112 — document the dangling classification and provide
  recovery (resolved by Decision 2 and `niwa_redelegate`)
- #113 — niwa_delegate should accept required_skills (resolved
  by Decision 3, with reduced urgency framing)
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
- `wip/design_niwa-mesh-reliability_claude_config_experiments.md`
