# /prd Scope: dispatch-permission-mode

## Problem Statement
Operators who declare `permissions = "bypass"` in a workspace's `workspace.toml`
expect every `niwa dispatch`-launched worker in that workspace to run
unattended. Since Claude Code 2.1.258, the only channel that still honors
`bypassPermissions` at launch is the `--permission-mode` CLI flag (or user/
managed settings) — the materialized project `settings.json` is ignored.
`niwa dispatch` only forwards `--permission-mode` when the operator types it
explicitly; nothing derives it from the workspace's declared posture, so
bypass-configured workspaces get workers that stall on unanswerable approval
prompts.

## Initial Scope

### In Scope
- Deriving `dispatchPermissionMode` (`internal/cli/dispatch.go`) from the
  workspace's declared `permissions` posture when the operator did not pass
  `--permission-mode` explicitly, before `buildDispatchPassthrough` is called.
- Reading the ALREADY-MATERIALIZED `.claude/settings.json`
  (`permissions.defaultMode`) via a widened `instanceSettings` struct in
  `internal/cli/dispatch_plugins.go`, the same mechanism `readInstanceSettings`
  already provides for the remote-control and keep-alive default-fill
  (dispatch.go:554), rather than re-deriving from `workspace.toml` directly —
  `runDispatch` does not have the effective `SettingsConfig` in scope where
  `buildDispatchPassthrough` is currently called (dispatch.go:534).
- Resolving the ordering gotcha: `buildDispatchPassthrough` (dispatch.go:534)
  currently runs BEFORE `readInstanceSettings` (dispatch.go:554). The
  materialized-settings read (or the derivation that depends on it) must move
  earlier, or `buildDispatchPassthrough` must be called later — model/slug
  resolution logic sits between the two lines and needs checking either way.
- Precedence: explicit `--permission-mode` always wins over the derived value.
- No-op for a workspace with no declared `permissions` posture, or `"ask"`.

### Out of Scope
- Changing `RootSettingsMaterializer`'s write path
  (`internal/workspace/materialize.go`) that already writes
  `permissions.defaultMode` into the instance's `settings.json` — unaffected.
- niwa#257 (`permissionsMapping["ask"] = "askPermissions"`, an invalid Claude
  Code value) — same map, unrelated root cause, tracked separately.
- Reviving `internal/workspace/permissions.go`'s `WorkerPermissionMode` — dead
  code (only caller was the removed mesh daemon; only remaining reference is
  its own test). Its fallback semantics (map every non-bypass case to
  `"acceptEdits"`) are also wrong for this feature: the requirement is to
  forward *nothing* when bypass isn't configured, not to force `acceptEdits`.
- Any change to `workspace.toml`'s `permissions` key itself.

## Research Leads
1. **Exact forwarding mechanics for Codex, the only other dispatch-capable
   agent** — RESOLVED during scoping (see Coverage Notes): `agentplan.LaunchFlags`
   maps Claude's `PermissionMode` to `--permission-mode` (value vocabulary:
   `bypassPermissions`, `acceptEdits`, etc.) but maps Codex's to `--sandbox`
   (a completely different value vocabulary — Codex sandbox modes, not
   `bypassPermissions`). The code comment at `internal/agentplan/dispatch.go`
   around the Codex `LaunchSpec` states niwa deliberately forwards nothing on
   this flag for Codex today, because `WorkdirGrantArgs`
   (`projects={...trust_level="trusted"}`) already establishes full trust for
   Codex workers through a different mechanism. Forwarding a literal
   `"bypassPermissions"` string via `--sandbox` would be wrong. This narrows
   the feature's real scope: the derivation almost certainly needs to be
   conditioned on the agent's flag spelling (only fires when
   `flags.PermissionMode == "--permission-mode"`, i.e., Claude's spelling) —
   this is a DESIGN-level question I flag explicitly with the finding above.
2. **`buildDispatchPassthrough` ordering fix** — needs a code read of
   dispatch.go:534-560 to decide the minimal-diff way to move the
   materialized-settings read earlier (or the passthrough build later)
   without disturbing model/slug resolution in between. Quick lead, resolved
   by direct code reading during Phase 3 drafting.
3. **Existing test patterns for `buildDispatchPassthrough` and
   `readInstanceSettings`** — `dispatch_remotecontrol_roundtrip_test.go` and
   `dispatch_plugins_remotecontrol_test.go` already exercise
   `readInstanceSettings`; these are the patterns a new test for this
   derivation should follow. Quick lead.

## Coverage Notes
- Who is affected: operators of `permissions = "bypass"` workspaces dispatching
  Claude Code workers. Non-Claude (Codex) dispatch is very likely unaffected by
  this feature per Research Lead 1's finding — Codex's own trust mechanism
  (`WorkdirGrantArgs`) already covers it, and forwarding a Claude-shaped value
  through Codex's `--sandbox` flag would be actively wrong.
- Current situation / what's missing: fully covered by the BRIEF.
- Why now: Claude Code 2.1.258 broke the previously-sufficient materialized-
  settings channel; this is a compatibility fix, not new functionality.
- Success criteria: matches the BRIEF's User Journeys — derived flag present
  for bypass-configured + no-explicit-flag; explicit flag wins; no-posture/`ask`
  workspaces see no change; a test asserts both the present and absent cases.
