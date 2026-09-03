---
schema: prd/v1
status: Draft
problem: |
  Operators who declare `permissions = "bypass"` in a workspace's
  `workspace.toml` expect every Claude Code worker `niwa dispatch` launches
  into that workspace to run unattended. Since Claude Code 2.1.258, the
  materialized project `.claude/settings.json` no longer carries that intent
  through — only the invoking user's own non-project settings, or the
  `--permission-mode` CLI flag, are honored. `niwa dispatch` forwards
  `--permission-mode` only when an operator types it explicitly; nothing
  derives it from the workspace's declared posture, so a bypass-configured
  workspace's worker silently falls back to the operator's own global default
  and stalls headless on approvals nobody can answer.
goals: |
  A worker dispatched into a `permissions = "bypass"` workspace runs
  unattended, without an operator having to learn a new flag or change how
  they invoke `niwa dispatch`. An explicit `--permission-mode` still wins
  over the derived value, and a workspace with no declared posture (or
  `"ask"`) is unaffected. The fix is internal to niwa's dispatch pipeline.
upstream: docs/briefs/BRIEF-dispatch-permission-mode.md
source_issue: 276
---

## Status

Draft

## Problem Statement

An operator who declares `permissions = "bypass"` in a workspace's
`workspace.toml` is stating a trust decision for every worker that runs
inside it: dispatched sessions should act without a human approving each
tool call. Until Claude Code 2.1.258, writing that decision into the
instance's materialized `.claude/settings.json` as
`permissions.defaultMode: "bypassPermissions"` was enough — the launched
worker picked it up automatically.

Claude Code 2.1.258 changed which channels honor `defaultMode:
"bypassPermissions"`: a value set in a project's `.claude/settings.json` or
`.claude/settings.local.json` is now ignored, treated the same as `"auto"`.
Only two channels still work — the user's own (non-project) settings, or the
`--permission-mode` CLI flag passed at launch. `niwa dispatch` already has a
`--permission-mode` flag (`internal/cli/dispatch.go:28`) and already forwards
it to the launched worker via `buildDispatchPassthrough`
(`internal/cli/dispatch.go:944`), but only when an operator types it
explicitly on the `niwa dispatch` invocation. Nothing derives it from the
workspace's own declared `permissions` posture.

A workspace configured for `bypass` therefore gets a worker that silently
starts in whatever mode the *invoking operator's own global* Claude Code
settings default to. (The workspace's `permissions = "bypass"` declaration
and the instance's materialized `permissions.defaultMode: "bypassPermissions"`
are the same trust decision at two altitudes: `RootSettingsMaterializer`
writes the latter from the former today, at instance-creation time. The
requirements below read the already-materialized instance-level value —
see R5 — rather than re-reading the workspace-level declaration.) Because dispatched workers run headless, every tool
call that mode gates on approval — including messages a coordinator session
sends the worker after launch, which arrive as tool calls too — simply
stalls. Nothing signals the operator that the cause is a Claude Code
permission-channel change rather than a bug in their workspace config.

## Goals

A worker dispatched into a `permissions = "bypass"` workspace runs
unattended: its tool calls execute without a human in the loop, and a
coordinator's messages to it are acted on rather than stuck behind an
unanswerable approval prompt. An operator gets this by doing nothing
differently — the same `workspace.toml` declaration and the same `niwa
dispatch` invocation they already use. An explicit `--permission-mode` still
wins over the derived value, and a workspace with no declared posture (or
`"ask"`) sees no behavioral change.

## User Stories

**US1.** As an operator whose workspace declares `permissions = "bypass"`, I
want `niwa dispatch "<task>"` (no `--permission-mode` flag) to launch a
worker that can act without stalling on approval, so that unattended
dispatch actually behaves unattended.

**US2.** As an operator who wants one dispatched worker to run more
cautiously than my workspace's default, I want `niwa dispatch "<task>"
--permission-mode acceptEdits` to override the workspace's declared posture,
so that an explicit choice always wins.

**US3.** As an operator whose workspace declares no permission posture (or
`"ask"`), I want `niwa dispatch` to behave exactly as it does today, so that
this feature introduces no surprise for workspaces that never opted into
bypass.

**US4.** As an operator who dispatches Codex workers, I want this feature to
leave Codex dispatch untouched, so that a value meaningless to Codex's
`--sandbox` flag is never forwarded on my behalf.

## Requirements

**R1 (functional).** When `niwa dispatch` launches a Claude Code worker into
a workspace whose materialized instance settings
(`<instance>/.claude/settings.json`) declare `permissions.defaultMode:
"bypassPermissions"`, and the operator did not pass `--permission-mode` on
the `niwa dispatch` invocation, the launched worker's argv SHALL include
`--permission-mode bypassPermissions`.

**R2 (functional).** An operator-supplied `--permission-mode` value on the
`niwa dispatch` invocation SHALL always reach the worker as typed, taking
precedence over any workspace-derived value.

**R3 (functional).** When the materialized instance settings declare no
`permissions.defaultMode`, or a value other than `"bypassPermissions"`
(including `"ask"`'s current mapping), no `--permission-mode` flag SHALL be
forwarded from this derivation path — unchanged from today's behavior.

**R4 (functional, scope-limiting).** The derivation SHALL fire only when the
launched agent's permission-equivalent flag is Claude Code's spelling
(`agentplan.LaunchFlags.PermissionMode == "--permission-mode"`). It SHALL
NOT forward a derived value through any other agent's permission-equivalent
flag (concretely: Codex's `--sandbox`, whose value vocabulary is unrelated
and which already receives an equivalent trust posture through
`WorkdirGrantArgs`).

**R5 (non-functional, implementation constraint).** The derivation SHALL
read `permissions.defaultMode` from the instance's already-materialized
`.claude/settings.json` (via the existing `readInstanceSettings` mechanism,
widened to project the `permissions` key), not by re-deriving the value from
`workspace.toml` in the CLI layer — `runDispatch` does not have the
effective `SettingsConfig` in scope at the point `buildDispatchPassthrough`
is currently called.

**R6 (non-functional, ordering constraint).** The materialized-settings read
this derivation depends on SHALL occur before `buildDispatchPassthrough` is
called (`internal/cli/dispatch.go:534`), so the derived value is available
to be included in the argv `buildDispatchPassthrough` builds on the same
invocation. The existing read at `internal/cli/dispatch.go:554` (consumed by
the remote-control and keep-alive default-fill logic) MAY be consolidated
into this earlier read rather than duplicated. If consolidated, the
remote-control and keep-alive behaviors this read already feeds SHALL remain
unchanged.

**R7 (functional, error handling).** When the materialized-settings read
fails — the file is absent, unreadable, or not valid JSON — the derivation
SHALL behave exactly as R3 specifies for "no posture declared": no
`--permission-mode` flag is forwarded from this path. A settings-read
failure SHALL NOT fail the `niwa dispatch` invocation itself, consistent
with `readInstanceSettings`'s existing error contract (callers already treat
any read error as "nothing to act on," per its remote-control and keep-alive
consumers).

## Acceptance Criteria

- [ ] AC1: Dispatching a Claude Code worker into a workspace whose
  materialized `.claude/settings.json` has `permissions.defaultMode:
  "bypassPermissions"`, with no `--permission-mode` passed to `niwa
  dispatch`, results in the launched worker's argv containing
  `--permission-mode bypassPermissions`.
- [ ] AC2: An explicit `--permission-mode` passed to `niwa dispatch` is what
  reaches the worker, regardless of the workspace's declared posture.
- [ ] AC3: A workspace whose materialized settings have no
  `permissions.defaultMode`, or a value other than `"bypassPermissions"`,
  results in no `--permission-mode` flag being forwarded from this
  derivation path.
- [ ] AC4: Dispatching a Codex worker never receives a derived value through
  its `--sandbox` flag from this feature, regardless of the workspace's
  declared permission posture.
- [ ] AC5: A test covering the derivation (or its caller, `runDispatch` /
  `buildDispatchPassthrough`) asserts the derived flag is present for a
  bypass-configured Claude Code dispatch and absent for each of: no posture
  declared, `"ask"` posture, a Codex dispatch, and an explicit
  `--permission-mode` already supplied.
- [ ] AC6: A test constructs a fixture where the workspace's
  `workspace.toml` `permissions` value and the materialized
  `.claude/settings.json` `permissions.defaultMode` value disagree (e.g.
  `workspace.toml` unset or `"ask"` while the materialized settings say
  `"bypassPermissions"`, and the reverse), and asserts the derived flag
  tracks the materialized settings value in both directions — proving the
  derivation reads materialized settings, not `workspace.toml` (R5).
- [ ] AC7: A materialized `.claude/settings.json` that is absent, unreadable,
  or not valid JSON results in no `--permission-mode` flag being forwarded
  from this derivation path, and the `niwa dispatch` invocation does not
  fail because of it (R7).
- [ ] AC8: After consolidating the settings read per R6, the existing
  remote-control default-fill and keep-alive arming behaviors exercised by
  `dispatch_remotecontrol_roundtrip_test.go` and
  `dispatch_keepalive_roundtrip_test.go` are unchanged.

## Out of Scope

- Changing how `RootSettingsMaterializer` writes `permissions.defaultMode`
  into an instance's `.claude/settings.json`
  (`internal/workspace/materialize.go`) — that write path is unaffected;
  this feature adds a second, still-honored read of the same declared trust
  decision.
- niwa#257 (`permissionsMapping["ask"] = "askPermissions"`, an invalid
  Claude Code value in the same materialization map) — unrelated root
  cause, predates the 2.1.258 channel change, tracked separately.
- Reviving or reusing `internal/workspace/permissions.go`'s
  `WorkerPermissionMode` — dead code (its only caller, the mesh daemon, was
  removed; only its own test still references it), and its fallback
  semantics (mapping every non-bypass case to `"acceptEdits"`) contradict R3.
- Deriving or forwarding any value through Codex's `--sandbox` flag on this
  workspace's behalf (see R4 and the Decisions and Trade-offs section below).
- Any change to how a workspace declares or validates its `permissions` key
  in `workspace.toml` itself.

## Decisions and Trade-offs

**Read materialized settings, not `workspace.toml` directly.** Considered
re-deriving the posture from `workspace.toml`'s `permissions` key at the
point `buildDispatchPassthrough` is called. Rejected: `runDispatch` does not
have the effective `SettingsConfig` in scope there, and
`internal/cli/dispatch.go` already has a proven mechanism —
`readInstanceSettings`, reading `<instance>/.claude/settings.json` — used
identically for the remote-control and keep-alive default-fill logic a few
lines later. Reusing it (widened with a `Permissions.DefaultMode` field on
`instanceSettings`) is a smaller, more consistent change than adding a
second posture-resolution path.

**Move the settings read earlier, don't duplicate it.** The existing
`readInstanceSettings` call sits at `dispatch.go:554`, after
`buildDispatchPassthrough` at `:534`. Nothing between those two lines
depends on the passthrough argv's content, so the read (or a version of it)
moves ahead of `:534`. The two existing consumers (remote-control,
keep-alive) can share the earlier read rather than the CLI reading the same
file twice per dispatch.

**Scope the derivation to Claude Code's flag spelling only, not "any agent's
equivalent."** The original issue framing anticipated forwarding "the
equivalent flag for a non-Claude agent, per `agentplan.LaunchFlags`."
Codebase research found this doesn't hold for niwa's only other
dispatch-capable agent, Codex: its `LaunchFlags.PermissionMode` maps to
`--sandbox`, a value vocabulary unrelated to `bypassPermissions`, and a code
comment in `internal/agentplan/dispatch.go` states niwa deliberately
forwards nothing on that flag today because `WorkdirGrantArgs`
(`trust_level="trusted"`) already grants Codex workers full trust through a
separate mechanism. Forwarding a derived `"bypassPermissions"` string
through `--sandbox` would be a regression, not a fix — so R4 scopes the
derivation to fire only when the agent's permission flag is Claude's
spelling.

## References

- niwa#276 — the GitHub issue this PRD specifies.
- niwa#257 — the neighboring, unrelated `permissionsMapping["ask"]` bug,
  named here only to keep it out of scope.
