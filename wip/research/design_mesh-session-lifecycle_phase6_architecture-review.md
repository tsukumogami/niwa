# Architecture Review: DESIGN-mesh-session-lifecycle

Date: 2026-05-04
Reviewer: architecture-review agent

---

## 1. Is the architecture clear enough to implement? Are there implementation gaps?

The design is substantially clear. The four decisions compose coherently: file-based
session state (D3) feeds inbox derivation for both task routing (D1) and virtual ask
routing (D2), env-var propagation at daemon spawn binds the MCP server to the right
instance root (D2), and the shell wrapper extension is fully specified with exact
before/after templates (D4).

One gap that would stall an implementer: the design says `niwa_create_session` starts
the per-worktree daemon via `EnsureDaemonRunning` with additional env vars, but
`EnsureDaemonRunning` in `internal/workspace/daemon.go` (signature: `func
EnsureDaemonRunning(instanceRoot string) error`) has no parameter for extra env vars.
The design acknowledges this requires an extension but does not specify the new
signature. An implementer would need to decide whether to add a variadic env-slice
parameter, a functional options pattern, or a wrapper function — none of which is
resolved. Phase 2's deliverable list mentions "EnsureDaemonRunning extension — accept
additional env vars" but gives no sketch of the new signature or how to pass vars into
`cmd.Env` given that `EnsureDaemonRunning` currently builds the command internally
with no env override point. This is the most actionable gap.

A second gap: the worktree daemon started for a session must watch
`<worktreePath>/.niwa/roles/<repo>/inbox/`, but the daemon's watch loop is driven by
`--instance-root`. The current code in `mesh_watch.go` derives inbox paths from
`--instance-root` at startup. For a per-worktree daemon, `--instance-root` would be
the worktree path, which means the worktree must have a fully initialized `.niwa/`
subtree (roles, tasks dirs, daemon.pid, daemon.log) before the daemon starts. The
design does not specify whether `niwa_create_session` must call
`InstallChannelInfrastructure` (or equivalent) for the worktree, or whether a lighter
scaffold is sufficient. Without this, the daemon will fail to register its fsnotify
watch. The implementation plan must specify the worktree `.niwa/` scaffold step.

---

## 2. Missing components or interfaces — can EnsureDaemonRunning support this?

`EnsureDaemonRunning` cannot support the session use case without modification. The
current implementation calls:

```go
cmd := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+instanceRoot)
// no cmd.Env assignment — inherits parent env
```

For a per-worktree daemon, two new env vars must reach the spawned process:
`NIWA_MAIN_INSTANCE_ROOT` (the main workspace, so the worker's MCP server can find
`sessions.json` and the coordinator registry) and `NIWA_SESSION_ID` (so the MCP
server populates `s.sessionID` and enables virtual routing). Neither var can be passed
as a flag because `mesh watch` has no flag for them; they must go in `cmd.Env`.

The fix is straightforward — add an `extraEnv []string` parameter (or options struct)
and assign `cmd.Env = append(os.Environ(), extraEnv...)` — but the design should have
specified this. The existing call sites (`niwa apply`, `niwa create`) pass no extra
env; an additive `extraEnv` parameter with a nil/empty default preserves backward
compatibility without changing call-site signatures materially.

One additional interface issue: `WorkerMCPConfig` (in `channels.go`) bakes
`NIWA_INSTANCE_ROOT` and `NIWA_SESSION_ROLE` into the per-spawn worker MCP config.
For workers spawned by a per-worktree daemon, `NIWA_INSTANCE_ROOT` should be the
worktree path (so the worker's MCP server reads the worktree's task store and role
inbox), but `NIWA_MAIN_INSTANCE_ROOT` must also be available for virtual ask routing.
`WorkerMCPConfig` currently has no parameter for `mainInstanceRoot`. This means the
design needs to specify whether `NIWA_MAIN_INSTANCE_ROOT` is injected via the daemon's
inherited env (set when `EnsureDaemonRunning` starts the daemon) and therefore
propagated automatically to workers via `os.Environ()` in `spawnWorker`, or whether
`WorkerMCPConfig` needs a new field. The inherited-env approach is simpler and fits
the existing "last-wins override" pattern in `spawnWorker`; the design should state
this explicitly.

---

## 3. Are the implementation phases correctly sequenced? Can Phase 3 start before Phase 2?

The phase dependency graph is correct:
- Phase 1 (session state schema) is self-contained.
- Phase 2 (worktree lifecycle + daemon startup) depends on Phase 1.
- Phase 3 (delegate session routing) depends on Phase 1 for state reads; depends on
  Phase 2 for the daemon being running when a delegate call arrives.

The design states Phase 3 "depends on Phase 1 (state read), Phase 2 (daemon running
before delegate is called)." The dependency is functional, not strictly sequential for
implementation purposes. An implementer can write and unit-test Phase 3's
`handleDelegate` extension against fixture session state files (Phase 1 types) without
a live daemon. Phase 2 is required for end-to-end functional tests but not for unit
tests. This is fine — the design's stated dependency is accurate for functional
testing, not for coding order.

The one sequencing risk: Phase 5 depends on Phases 1–4 being complete, meaning the
skill update (`buildSkillContent()` and `niwaMCPToolNames`) cannot ship until all
three new MCP tool handlers are registered. This is correct — shipping tool names in
the skill before the handlers exist would cause tool calls to fail. The sequencing is
sound.

---

## 4. Inbox path derivation: is `repo` always the role name in a session worktree?

The design derives the worktree inbox path as:

```
<session.WorktreePath>/.niwa/roles/<session.Repo>/inbox/
```

This assumes the role name under `.niwa/roles/` in the worktree equals
`session.Repo`. Whether that holds depends on how the worktree's `.niwa/roles/`
directory is created.

In the main instance, `InstallChannelInfrastructure` calls `enumerateRoles`, which
derives role names from repo directory basenames (the two-level scan: group dir →
repo dir → `basename` = role name). For a repo whose directory is named `tsuku`, the
role is `tsuku` and its inbox is `.niwa/roles/tsuku/inbox/`. The `Repo` field in
`SessionLifecycleState` is meant to carry this identifier.

The risk: if a workspace uses `[channels.mesh.roles]` overrides that map a repo to a
different role name (e.g., `backend = "services/api"`), the role name in `.niwa/roles/`
is `backend` but `session.Repo` might be set to the repo basename (`api`). If
`niwa_create_session` sets `Repo` from the CLI argument (which a coordinator passes as
the repo name it wants work done on), it could use a value that does not match the
actual role name on disk.

The design does not specify whether `niwa_create_session` accepts `repo` as a role
name or a repo identifier, and whether validation checks that a matching role directory
exists before creating the session. The implementation should: (a) accept `repo` as a
role name (not a directory path), (b) validate that `.niwa/roles/<repo>/` exists in
the main instance before creating the worktree, and (c) document that `Repo` in the
session state is always a role name, not a filesystem basename. This is not a
correctness hole in the chosen architecture but is an under-specified input contract
that could produce a silently wrong inbox path.

---

## 5. Are there simpler alternatives that were overlooked?

The four decisions are well-reasoned and the chosen options (direct inbox write,
registry pre-check, per-session JSON, nested case arm) are each the simplest viable
option within their problem space. The rejected alternatives are correctly dismissed.

One area where a simpler approach exists that was not discussed: the worktree could
reuse the main instance's `.niwa/` directory rather than having its own. Specifically,
`<worktreePath>/.niwa/roles/<repo>/inbox/` could be a symlink pointing at the
corresponding role inbox in the main instance's `.niwa/roles/<repo>/inbox/`. This
would avoid the need to scaffold a separate `.niwa/` tree in each worktree and would
remove the need for a per-worktree daemon — the main daemon could pick up tasks for
session-targeted roles from its existing inbox watch. However, this collapses the
session isolation model: a session worktree's inbox would be indistinguishable from
a regular role inbox, and the daemon could not tell session tasks apart from
coordinator-dispatched tasks without session ID threading. The chosen per-worktree
daemon approach is the correct one; the symlink shortcut would undermine the
isolation guarantee. That said, the design could acknowledge and dismiss this in the
"Considered Options" section for completeness.

The shell wrapper duplication (identical `create)` and `session create)` blocks) could
be extracted to a shared shell function at this diff size, but the design acknowledges
this as a known trade-off and defers it correctly.

---

## Summary of Field Naming Inconsistency (D2 vs D3)

Decision 2's `resolveVirtualInbox` pseudocode references fields that do not exist in
Decision 3's `SessionLifecycleState` struct:

| D2 reference              | D3 field               | Status        |
|--------------------------|------------------------|---------------|
| `parentState.CoordinatorPID` | `CreatorPID`       | Name mismatch |
| `parentState.CoordinatorStartTime` | `CreatorStartTime` | Name mismatch |
| `parentState.InboxPath`   | (absent — derived)     | Field missing |
| `childState.InboxPath`    | (absent — derived)     | Field missing |

D2 also lists `inbox_path` as a required field in its "Implementation dependencies"
section, contradicting the D2 main body's own cross-validation note ("inbox path is
derived at runtime from WorktreePath + Repo") and D3's schema (no `inbox_path` field).
An implementer following D2's `resolveVirtualInbox` sketch would either add a field
that D3 explicitly rejected or derive the path and find the field references wrong.
The design document should reconcile these: remove `InboxPath` from D2's
pseudocode, replace `CoordinatorPID`/`CoordinatorStartTime` with `CreatorPID`/
`CreatorStartTime`, and delete the contradictory "Implementation dependencies" bullet.
