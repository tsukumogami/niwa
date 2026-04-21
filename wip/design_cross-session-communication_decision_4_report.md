# Decision 4: ChannelMaterializer Integration

## Context

`runPipeline` in `internal/workspace/apply.go` runs a series of ordered steps to
provision a workspace instance. Step 4.5 writes `workspace-context.md` via
`InstallWorkspaceContext()`. Step 6.5 runs per-repo materializers
(`HooksMaterializer` → `SettingsMaterializer` → `EnvMaterializer` →
`FilesMaterializer`) once per classified repo.

The `ChannelMaterializer` needs to write both workspace-wide infrastructure (sessions
directory, `sessions.json`, `artifacts/` subdirectory, `.mcp.json`) and per-repo
artifacts (hook scripts for SessionStart and UserPromptSubmit). It also needs to append
a `## Channels` section to `workspace-context.md`, which is already written before any
materializer runs.

## Key assumptions

- The `## Channels` section in `workspace-context.md` does not need to vary by repo.
  R7 says role is derived at *registration* time from `NIWA_SESSION_ROLE`, the
  `[channels.mesh.roles]` table, or the repo path — not baked into workspace-context.md
  per-repo. The behavioral instructions (which tools exist, what the session should do)
  are workspace-wide constants. The `workspace-context.md` text directs Claude to "call
  `niwa session register` to receive your role" — role assignment is deferred to runtime,
  not burned into the file.
- Workspace-wide infrastructure (sessions dir, `.mcp.json`) must be written exactly once
  per apply, not once per repo.
- All written files must be returned and tracked in `InstanceState.ManagedFiles` so
  drift detection and `niwa destroy` clean up automatically.
- `niwa apply` must be idempotent: a second run must not duplicate `## Channels` in
  `workspace-context.md` or re-initialize `sessions.json` if it already exists.
- The `MaterializeContext` does not currently expose `instanceRoot`. It can be derived
  from `RepoDir` (which is `instanceRoot/<group>/<repo>`), but that is fragile across
  single-level and nested group paths and relies on a structural invariant that is not
  declared by the interface.
- Hook scripts for SessionStart and UserPromptSubmit are written per-repo (they land in
  `<repoDir>/.claude/hooks/`) and per-instance-root (via `InstallWorkspaceRootSettings`).
  `HooksMaterializer` already writes per-repo hook scripts from `EffectiveConfig.Claude.Hooks`.
  Channel hooks can be declared in config and carried through the same path — no separate
  per-repo write is required in `ChannelMaterializer` itself.

## Chosen: Option B — Separate workspace-level step in runPipeline

Add a new `runChannelMaterializer(ctx, cfg, instanceRoot, classified, writtenFiles)`
call at **step 4.75** in `runPipeline`, between the existing step 4.5
(`InstallWorkspaceContext`) and step 5 (group CLAUDE.md installation).

The function:

1. Checks whether `cfg.Channels` is non-empty. If not, returns immediately (no-op for
   workspaces without `[channels]`).
2. Creates `<instance-root>/.niwa/sessions/` and `artifacts/` subdirectory (idempotent
   via `os.MkdirAll`).
3. Creates `<instance-root>/.niwa/sessions/sessions.json` only if it does not already
   exist, so re-apply does not overwrite a populated registry.
4. Writes `<instance-root>/.claude/.mcp.json` with the `niwa` MCP server entry pointing
   to `niwa mcp-serve` and `NIWA_INSTANCE_ROOT` baked in.
5. Appends the `## Channels` section to `workspace-context.md` using an idempotent
   helper (check-then-append, same pattern as `ensureImportInCLAUDE`). The section
   contains: the sessions registry path, the available tool names
   (`niwa_check_messages`, `niwa_send_message`, `niwa_ask`, `niwa_wait`), the
   registration command, and behavioral instructions per R5 — without embedding a
   per-repo role (role is resolved at registration time by `niwa session register`).
6. Returns the list of written file paths so `runPipeline` appends them to
   `writtenFiles` for managed-files tracking.

Hook scripts for SessionStart and UserPromptSubmit are **not** written by this function.
They are declared in the `[channels.mesh]` config as standard hook entries (or injected
by the config parser when `[channels.mesh]` is present), and the existing
`HooksMaterializer` at step 6.5 copies them per-repo. This separates the channel
hook concern from the channel infrastructure concern cleanly: the materializer that
already writes hooks continues to write hooks; the new step handles only workspace-wide
channel infrastructure.

## Rationale

**Option B avoids the `instanceRoot` exposure problem.** Option A (per-repo
materializer) would require `MaterializeContext` to expose `instanceRoot`. Deriving it
from `RepoDir` (e.g., `filepath.Dir(filepath.Dir(repoDir))` when group depth is one)
is fragile: it bakes in an assumption about path depth that is nowhere contractually
guaranteed. Adding `instanceRoot` to `MaterializeContext` is a reasonable change, but it
expands an already-large struct for a field that only one materializer needs. Option B
sidesteps this entirely by running at a level where `instanceRoot` is already in scope
as a named parameter.

**The workspace-context.md append is correctly ordered.** Step 4.75 runs after step 4.5
writes `workspace-context.md` and before any per-repo content is installed. The append
can read and re-write the file using the same idempotent check-then-write pattern
already used by `ensureImportInCLAUDE`, so running `niwa apply` twice is safe.

**No duplication of hook-writing logic.** Option C splits into two materializers, which
means maintaining two types that must cooperate. Option A would need to guard the
workspace-wide writes with a "first repo only" check. Option B avoids both by keeping
hook-writing in `HooksMaterializer` and infrastructure-writing in a focused function
that has no interest in per-repo structure.

**Follows the existing pattern for workspace-level steps.** `InstallWorkspaceContext`,
`InstallOverlayClaudeContent`, `InstallWorkspaceRootSettings`, and
`InstallGlobalClaudeContent` are all free functions called sequentially from
`runPipeline` with `instanceRoot` as an argument. Adding `runChannelMaterializer` (or
`InstallChannelInfrastructure` by the naming convention) is a natural extension of this
pattern. It is immediately legible to anyone reading `runPipeline`.

**`sessions.json` is not overwritten on re-apply.** The guard "create only if absent"
means the session registry survives `niwa apply` without data loss. This satisfies the
idempotency requirement without any state-comparison logic.

**The `## Channels` section role placeholder works at runtime.** R7 defers role
assignment to `niwa session register`. The workspace-context.md section can say "run
`niwa session register --repo <repo>` to receive your role" without per-repo
specialization. This is correct: the behavioral instructions (which tools to call, when
to use `niwa_ask` vs. `niwa_send_message`) are identical for all sessions; only the role
value differs, and it is resolved at registration time from the environment.

## Rejected alternatives

### Option A: ChannelMaterializer as a per-repo materializer at step 6.5

A `ChannelMaterializer` struct implementing the `Materializer` interface would fit
structurally into the existing materializer slice. However:

- It needs `instanceRoot` to write `.mcp.json` and the sessions directory, but
  `MaterializeContext` does not expose it. Deriving it from `RepoDir` requires
  path-depth arithmetic that is not contractually guaranteed.
- Workspace-wide writes (sessions dir, `.mcp.json`) must be guarded with a
  "first-repo-or-already-exists" pattern, introducing state into what should be a
  stateless per-repo call.
- The `workspace-context.md` append at step 6.5 writes to a file two levels above
  `repoDir`, which `checkContainment` would reject if enforced, and which is
  architecturally inconsistent: materializers are expected to operate on their
  `RepoDir`, not on the instance root.

### Option C: Split into ChannelInfraMaterializer + ChannelHooksMaterializer

Two types, one running at step 4.75 and one at step 6.5. Cleaner in theory, but:

- `ChannelHooksMaterializer` at step 6.5 would write hook scripts that are
  conceptually identical to what `HooksMaterializer` already writes. The hook scripts
  for SessionStart and UserPromptSubmit are standard hook entries; they should travel
  through the standard hook pipeline.
- Two materializers must agree on naming, not conflict on paths, and be registered
  in the right order. That coordination overhead is not justified when the hook concern
  is already handled by `HooksMaterializer`.
- `ChannelInfraMaterializer` at step 4.75 is effectively Option B but wrapped in an
  interface that adds no value for a function called once.

### Option D: Extend workspace_context.go to include channels section at step 4.5

Modifying `generateWorkspaceContext` to accept channels config and write the
`## Channels` section during the initial write at step 4.5 is appealing for locality,
but:

- The `## Channels` section includes the sessions registry path, which is derived from
  `instanceRoot`. `generateWorkspaceContext` already takes `instanceRoot` indirectly
  (it computes paths), so this is solvable, but it conflates two concerns: workspace
  topology context (repos, groups) and channel behavioral instructions.
- All other channel infrastructure (`.mcp.json`, sessions directory) still needs a
  separate step. The single benefit — co-locating workspace-context.md writes — does
  not justify splitting channel provisioning across two pipeline positions.
- As channels evolve (e.g., new section content), changes to `generateWorkspaceContext`
  become harder to isolate. A separate `InstallChannelInfrastructure` function keeps
  channel logic co-located and independently testable.

## Consequences

- Positive: `instanceRoot` stays out of `MaterializeContext`, keeping the interface
  stable for the existing materializers.
- Positive: Hook scripts for channel events flow through `HooksMaterializer` unchanged.
  No duplication of hook-writing logic.
- Positive: `workspace-context.md` is written once at step 4.5, then amended at step
  4.75 in a single idempotent append — the file is in its final state before any
  per-repo content is installed.
- Positive: The new function follows the naming and call-site pattern of
  `InstallWorkspaceContext`, `InstallWorkspaceRootSettings`, etc., making `runPipeline`
  readable without new concepts.
- Positive: `sessions.json` is never overwritten on re-apply; the registry survives
  intact across multiple `niwa apply` calls.
- Negative: Hook entries for SessionStart and UserPromptSubmit must be declared in (or
  injected into) `cfg.Channels` / `cfg.Claude.Hooks` so `HooksMaterializer` picks them
  up. The config wiring between `[channels.mesh]` and hook entries is an extra
  integration step that must be implemented and tested.
- Negative: `runPipeline` gains another function call. This is consistent with the
  existing structure but does grow the function length slightly.
- Mitigation: The hook-injection coupling can be resolved during config parsing: when
  `cfg.Channels` is non-empty, the parser (or the top of `runPipeline` before step 4.5)
  synthesizes the SessionStart and UserPromptSubmit `HookEntry` values into
  `cfg.Claude.Hooks`, so downstream materializers see them as normal configured hooks.
  This keeps the hook wiring in one place.
