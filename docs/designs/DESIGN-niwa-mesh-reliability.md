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
  doesn't read or relies on implicit discovery to find. Bringing the
  runtime contract back into alignment with the documented niwa-mesh
  skill needs one coordinated design rather than nine independent fixes,
  because the choices interact: the dangling lifecycle shape (#112)
  affects the redelegate primitive (#114), the plugin propagation
  mechanism (#108) affects the skill delivery path (#97) and the
  required_skills check (#113), and every fix touches the same
  user-facing skill text.
---

# DESIGN: niwa mesh reliability

## Status

Proposed

## Context and Problem Statement

### What prompted the exploration

Nine open issues filed against the niwa repo since #92 cluster around
the mesh subsystem: routing, lifecycle, observability, and recovery.
Issues are #92, #97, #108, #109, #110, #111, #112, #113, and #114.

### What was discovered

The exploration produced six research files at
`wip/research/explore_niwa-mesh-reliability_r1_lead-*.md` and a
synthesis at `wip/explore_niwa-mesh-reliability_findings.md`. Key
findings, with citations:

- **#92 is partly already done.** PR #93 wired live-coordinator routing
  in `handleAsk` (`internal/mcp/server.go:780-843`); the spawn-fabricate
  fallback is gone. Today's branch is "live coordinator → write
  `task.ask` and wait" or "no live coordinator → return
  `no_live_session` synchronously".

- **#92 and #109 are the same code-level bug.** `isKnownRole(args.To)`
  at `server.go:802` runs against `<worktreePath>/.niwa/roles/<role>/`
  for a session worker, but `scaffoldWorktreeNiwa`
  (`internal/mcp/handlers_session.go:80-108`) only creates the worker's
  own role dir, never `roles/coordinator/`. Workers fail at
  `UNKNOWN_ROLE` before the live-coordinator routing logic can run.
  PR #93 hoisted the lookup to `mainInstanceRoot` but did NOT hoist
  the role-existence precondition.

- **Coordinator auto-registration is fragile.**
  `maybeRegisterCoordinator` only fires from `niwa_check_messages` and
  `niwa_await_task`. A coordinator that uses only
  `niwa_delegate` + `niwa_query_task` never registers, so even with
  the precondition fixed, ask-routing can fall through to
  `no_live_session`.

- **Worker spawn at `internal/cli/mesh_watch.go:908-1016`** invokes
  `claude -p` with no `--plugin`, `--marketplace`, `--settings`, or
  `CLAUDE_CONFIG_DIR` flag. Plugin discovery relies entirely on Claude
  Code's CWD-walk, which empirically does not surface workspace
  plugins to session workers whose CWD is a worktree under
  `.niwa/worktrees/`. This is #108.

- **The niwa-mesh skill leak (#97) is unrelated to spawn.** It comes
  from `InstallChannelInfrastructure`
  (`internal/workspace/channels.go:347-359`) writing
  `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` into every
  non-coordinator role's working tree on every `niwa apply`. Eight or
  more byte-identical copies live in the workspace today. Workers
  `git add .` and the file ends up in PRs.

- **Spawn-success and daemon-liveness signals already exist on disk
  but aren't propagated.** The daemon writes `daemon.pid` only after
  fsnotify registration succeeds (`mesh_watch.go:283-287`).
  `EnsureDaemonRunning` (`internal/workspace/daemon.go:35-102`) polls
  for that signal for 500 ms but explicitly returns nil on timeout —
  the failure path for #110 is silent because the result is discarded.
  `mcp.IsPIDAlive` (`internal/mcp/liveness.go:14-35`) already exists
  and is used elsewhere; `niwa_list_sessions`
  (`handlers_session.go:26-50`) does not consult it. This is #111.

- **`dangling` is not a state.** `validTaskStates`
  (`internal/mcp/types.go:171-189`) defines exactly five values:
  queued, running, completed, abandoned, cancelled. `dangling` is a
  filesystem quarantine in `<role>/inbox/dangling/` triggered by
  `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`) iff a
  `task.delegate` envelope's `<mainInstance>/.niwa/tasks/<id>/state.json`
  is missing. Under normal flow this is structurally impossible
  because `createTaskEnvelope`
  (`handlers_task.go:177-258`) writes state.json before the inbox
  message. Stickiness is deterministic, not stateful: fsnotify
  doesn't watch `dangling/`, and the same Stat check re-classifies
  the file if it's moved back. The API layer reads only state.json,
  so `niwa_query_task` and `niwa_list_outbound_tasks` report
  `state="queued"` for dangling envelopes; `niwa_cancel_task` returns
  the contradictory pair `{status:"too_late",current_state:"queued"}`
  because it only renames `inbox/<id>.json` and treats ENOENT as
  "daemon already claimed". This is #112.

- **The task store is flat by task_id, not partitioned by state.**
  Every task lives at `<taskStoreRoot>/.niwa/tasks/<id>/{envelope,state}.json`
  for its entire lifetime — only the inbox **message** moves between
  subdirs. `niwa_redelegate` (#114) is therefore trivial:
  `ReadState(taskDirPath(...))` resolves the source regardless of
  inbox subdir. `required_skills` (#113) slots cleanly between the
  `UNKNOWN_ROLE` check and `createTaskEnvelope`
  (`handlers_task.go:130-141`).

- **The niwa-mesh skill claims six runtime behaviors that don't
  hold today.** The skill is generated by `buildSkillContent`
  (`internal/workspace/channels.go:682-833`). Of nine first-class
  claims, six are broken (#92, #97, #108, #109, #110, #111, #112) and
  one is a missing primitive the skill assumes implicitly (#114).
  The Message Vocabulary section also advertises `question.ask`,
  `question.answer`, and `status.update` types that no handler
  dispatches; the actually-routed `task.delegate` and `task.ask` are
  omitted. Workers taking the skill literally have always been
  silently ignored.

### What architectural decisions remain open

1. **Plugin propagation mechanism (#108).** Three candidate injection
   points: argv flag to `claude -p`, env var
   (`CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude`), or filesystem mirror
   in `scaffoldWorktreeNiwa` (symlink/copy
   `<mainInstance>/.claude/settings.json` into
   `<worktree>/.claude/settings.local.json`). Trade-offs differ on
   robustness to Claude Code discovery behavior, ergonomics for
   future plugin upgrades, and how it composes with the skill-delivery
   change for #97.

2. **Dangling-task lifecycle (#112).** Two design options:
   (A) make `dangling` a real state.json state, daemon transitions
   on quarantine, all read APIs surface the truth;
   (B) add an opt-in `niwa_resurrect_task(task_id)` primitive that
   reverts the rename. The two are not mutually exclusive — (A) gives
   API truthfulness; (B) gives recovery. Either alone leaves a gap;
   together they restore the documented contract.

3. **Skill delivery path (#97).** Either deliver via `CLAUDE.local.md`
   injection (already used for workspace context) or rely on the
   instance-root copy alone with Claude Code discovery configured to
   find it. Couples to decision 1 (plugin propagation mechanism)
   because the same Claude Code discovery semantics control both.

4. **`required_skills` placement (#113).** Inside `body` (matches the
   existing "opaque body" convention) or as a top-level `delegateArgs`
   field (better audit-log fidelity but splits the body convention).

5. **Worker `coordinator` role visibility (#109 fix shape).** Either
   special-case `to == "coordinator"` in `isKnownRole` to consult
   `mainInstanceRoot`, OR have `scaffoldWorktreeNiwa` create a
   synthetic `<worktree>/.niwa/roles/coordinator/inbox/` so existing
   precondition checks pass uniformly.

## Decision Drivers

- **Bring runtime and skill text back into lockstep.** The niwa-mesh
  skill is the canonical user-facing contract. Every fix that lands in
  isolation drifts the skill further from the truth. A coordinated
  design lets the skill update happen once.

- **Prefer surgical changes that reuse existing primitives.** Most of
  the infrastructure is already on disk: `daemon.pid`, `IsPIDAlive`,
  `lookupLiveCoordinator`, the flat task store. The cleanest fixes are
  one-function changes that propagate signals already produced.

- **Keep `Status` single-writer.** Persisted
  `SessionLifecycleState.Status` is owned by the lifecycle code path
  (create writes "active", destroy writes "ended"). Avoid making
  `niwa_list_sessions` mutate it; add an orthogonal `daemon`
  sub-object instead. Same separation applies to task state vs. inbox
  classification.

- **Don't broaden the MCP surface unnecessarily.** Recovery tools
  (resurrect) are operator paths; a worker should never need them.
  Prefer CLI-only or restrict via authorization (`kindDelegator`).

- **Preserve attribution semantics.** Redelegate must reset `from` to
  the caller's role/PID (so `kindDelegator` auth still works on
  subsequent calls) while preserving an audit chain to the source
  task via a new envelope field.

- **Public-repo contract.** The skill, the design doc, and any new
  guides must follow public-repo tone: clear to first-time
  contributors, no internal jargon, no competitor names.

## Decisions Already Made (during exploration)

These were settled during round 1 convergence and should be treated as
constraints, not reopened in the design phases.

- **Treat the cluster as one design, not nine bugfixes.** Reason:
  shared root causes, shared modules, shared user-facing surfaces.
  Splitting forces the skill text to drift across nine PRs.
- **One round of research is sufficient.** Reason: leads returned
  tight, file-cited findings with no contradictions.
- **#92 and #109 collapse into a single fix.** Reason: same code-level
  precondition mismatch.
- **Status field stays single-writer; add an orthogonal `daemon`
  sub-object for #111.** Reason: avoids write race with destroy and
  preserves the lifecycle-marker meaning.
- **No new daemon heartbeat file.** Reason: `daemon.pid` +
  `IsPIDAlive` already provides PID-recycle-safe liveness.
- **`niwa_redelegate` accepts dangling source tasks.** Reason: covers
  the case where resurrect's precondition (state.json intact) doesn't
  hold; together with whichever option wins for #112 this restores
  full recovery coverage.

## Considered Options

(Populated by /shirabe:design from the open architectural decisions
listed above.)

## Decision Outcome

(Populated by /shirabe:design after running per-decision evaluation.)

## Solution Architecture

(Populated by /shirabe:design.)

## Implementation Approach

(Populated by /shirabe:design. The exploration suggests a natural
sequencing along the lines of:
1. Coordinator routing repair: `isKnownRole` hoist + auto-register
   from `niwa_delegate`. Closes #92 and #109.
2. Daemon health propagation: `EnsureDaemonRunning` typed timeout +
   computed `daemon` sub-object. Closes #110 and #111.
3. Plugin propagation: chosen mechanism from decision 1. Closes #108
   and unblocks #113.
4. Skill delivery: chosen mechanism from decision 3. Closes #97 and
   composes with item 3.
5. Task lifecycle truthfulness: dangling-state shape from decision 2,
   plus required APIs. Closes #112.
6. Coordinator ergonomics: `required_skills` gate (#113) and
   `niwa_redelegate` primitive (#114). Both depend on items 3 and 5.
7. Skill text and `docs/guides/sessions.md` updates: amend message
   vocabulary, add dangling paragraph, add worker-skills paragraph,
   cross-link guides. Lands with the final implementation issues.)

## Security Considerations

(Populated by /shirabe:design. Areas to consider include: skill content
delivery path must not allow arbitrary file writes into consumer repos;
the `redelegated_from` envelope chain must not let a redelegator
elevate their authority over a task they didn't originally own; the
`required_skills` check must read the manifest from a trusted root,
not from a worker-controllable path.)

## Consequences

(Populated by /shirabe:design.)

## Source Issues

This design closes the following issues:

- #92 — niwa_ask to live coordinator (partly fixed, completes the chain)
- #97 — niwa-mesh skill file leaking into consumer repos
- #108 — workers spawn without workspace plugins
- #109 — workers cannot reach coordinator (UNKNOWN_ROLE)
- #110 — niwa_create_session must surface daemon-spawn failures
- #111 — niwa_list_sessions must report daemon health
- #112 — document the dangling classification and provide recovery
- #113 — niwa_delegate should accept required_skills
- #114 — add niwa_redelegate primitive

Exploration artifacts:
- `wip/explore_niwa-mesh-reliability_scope.md`
- `wip/explore_niwa-mesh-reliability_findings.md`
- `wip/explore_niwa-mesh-reliability_decisions.md`
- `wip/explore_niwa-mesh-reliability_crystallize.md`
- `wip/research/explore_niwa-mesh-reliability_r1_lead-*.md`
