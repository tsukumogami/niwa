# Panel Review: PRD Feasibility — Mesh Session Lifecycle

Five reviewers examined distinct code areas against PRD requirements R1–R21.
Findings are classified as **Breaking** (existing behavior changes), **Needs new
code** (additive, nothing existing breaks), or **Compatible as-is**.

## Breaking Changes

### B1 — `dotGitExists` returns false for worktree `.git` file pointer

**File:** `internal/workspace/snapshotwriter.go:95` (and `overlaysync.go:35`)

```go
return info.IsDir()  // git worktrees have .git as a file, not a directory
```

`EnsureConfigSnapshot` uses this to detect git-backed config dirs. In a
worktree, `.git` is a file pointer, so `dotGitExists` returns false and config
sync is silently skipped. No error; stale config.

**Fix:** `return err == nil` — one-liner. Same fix applies to `reset.go:148`
(`isClonedConfig`), which has the same `info.IsDir()` check and would refuse
`niwa reset` inside a session worktree.

**Acceptability:** Trivial to fix; should be fixed regardless of this feature.

---

### B2 — `EnumerateInstances` discovers session worktrees, causing `niwa apply` to operate on them

**File:** `internal/workspace/state.go:308`

`EnumerateInstances` scans immediate subdirectories of `workspaceRoot` for
`.niwa/instance.json`. Session worktrees have their own `.niwa/instance.json`
by design (per-worktree daemon model). If worktrees are placed alongside the
main instance at workspace root level, `EnumerateInstances` returns them, and
`niwa apply` attempts to apply config to them — directly violating R15.

**All callers are affected:**
- `internal/cli/apply.go:194` — apply pipeline
- `internal/workspace/scope.go:69` — cwd-path apply
- `internal/cli/status.go:107, 139` — status summary
- `internal/cli/completion.go:45, 149` — tab completion
- `internal/workspace/destroy.go:41` — destroy

**Fix options (mutually exclusive; requires design decision):**

1. **Layout separation (no code changes needed):** Place session worktrees
   under a dedicated subdirectory that `EnumerateInstances` never scans (e.g.,
   `<workspace>/<main-instance>/.niwa/worktrees/<session-id>/`). The function
   only scans one level deep from `workspaceRoot`; worktrees inside instance
   directories are invisible to it. This is the least invasive option.

2. **Marker field in `instance.json`:** Add `session_worktree: true` to
   `InstanceState`. `EnumerateInstances` skips entries with this flag. Requires
   a schema version bump and changes at every call site.

3. **Separate enumeration function:** Add `EnumerateSessionWorktrees`; leave
   `EnumerateInstances` unchanged. Every call site that should exclude worktrees
   stays on the existing function.

**Acceptability:** This is a structural blocker. The design doc must specify
worktree placement before implementation begins. Option 1 is recommended
because it requires no code changes to enumeration logic.

---

### B3 — `niwa_ask` cannot route from session-worktree workers to the coordinator

**Files:** `internal/mcp/server.go:604–706`, `internal/mcp/session_registry.go:57`

This is the most significant architectural break. Two independent failures:

**B3a — `isKnownRole` fails first** (`server.go:604`): before routing logic
runs, `handleAsk` checks `<s.instanceRoot>/.niwa/roles/coordinator/` for
existence. In a session worktree, `s.instanceRoot` is the worktree root, and
that directory does not exist. Call returns `UNKNOWN_ROLE`.

**B3b — `lookupLiveCoordinator` reads wrong registry** (`session_registry.go:57`):
even if B3a were patched, `lookupLiveCoordinator(s.instanceRoot)` reads
`<worktree_root>/.niwa/sessions/sessions.json`. The coordinator registered in
`<main_clone>/.niwa/sessions/sessions.json`. The lookup returns `("", false)`.

**B3c — `sendMessage` writes to unwatched inbox** (`server.go:608`): writes to
`<worktree_root>/.niwa/roles/coordinator/inbox/` — a directory no coordinator
watches.

**Impact:** Every `niwa_ask` from a session-worktree worker silently fails. R19
("workers in session worktrees can reach the live coordinator") is unmet.

**Fix:** The session-worktree daemon must know its parent (main) instance root
and propagate it separately for coordinator routing, distinct from the worktree
root used for task/inbox paths. Two viable mechanisms:

- Bake a `NIWA_MAIN_INSTANCE_ROOT` env var into the per-worktree worker MCP
  config at daemon spawn time. The `handleAsk` coordinator lookup reads this
  env var when present.
- Symlink `<worktree>/.niwa/sessions/sessions.json` → `<main>/.niwa/sessions/sessions.json`
  at worktree creation time (simpler, but couples the file layout).

**Acceptability:** Must be resolved in the design doc. The daemon needs a new
concept of "parent instance root" for cross-instance routing.

---

## Needs New Code (additive, no existing behavior breaks)

### N1 — `resolveRoleCWD` does not resolve worktree paths

**File:** `internal/cli/mesh_watch.go:2211`

Worker CWD is resolved by scanning depth-2 subdirectories of `instanceRoot`.
Session worktrees at a separate path are not found; the daemon falls back to
`instanceRoot` as the worker CWD. Session workers run in the wrong directory.

Must be fixed to support R2 (worker spawns in session's worktree).

---

### N2 — `resumeSessionID` is only set on the stall-watchdog retry path

**File:** `internal/cli/mesh_watch.go:1669, 1716–1721`

`inboxEvent.resumeSessionID` is populated only during `retrySpawn` (stall
recovery for the same task). For R10 (new task resumes prior task's Claude
session), `resumeSessionID` must also be populated on the initial
`handleInboxEvent` path when the task carries a `session_id`. New code path
required; existing behavior is unchanged for tasks without `session_id`.

**Note:** R11 (capture the first worker's Claude session ID) is already solved
— `registerSessionID()` at `server.go:933` captures `CLAUDE_SESSION_ID` from
env into `state.json.worker.claude_session_id` at worker startup. No change
needed there.

---

### N3 — No warning field for R12 resume fallback

**File:** `internal/mcp/types.go:265–281` (`TaskState`)

R12 requires niwa to "record a warning in session state" when JSONL is missing
and the worker falls back to a fresh spawn. No such field exists. A new field
(e.g., `ResumeFallbackReason string`) must be added and surfaced in
`formatQueryResult`; otherwise the field is written but never read (state
contract violation).

---

### N4 — Session registry cannot model multiple sessions per repo

**File:** `internal/mcp/session_registry.go:20`, `internal/mcp/types.go:102–111`

`WriteSessionEntry` enforces one-live-entry-per-role. The PRD's session model
requires multiple concurrent sessions for the same repo. The coordinator routing
registry (`SessionEntry`) is a different concept from the PRD's session lifecycle
store. The implementation needs either a new file (e.g., `mesh-sessions.json`)
or ID-keyed writes rather than role-keyed writes — with the constraint that a
parallel schema (second file, separate writer) risks state-contract drift.

`SessionEntry` also has no `Status` field for `active`/`pending_merge`/`ended`/
`abandoned` (R6). `ClaudeSessionID` exists in the struct but is never populated
by the in-process `maybeRegisterCoordinator` path (only by the CLI
`session_register` command).

---

### N5 — Three new MCP tools require standard boilerplate

**File:** `internal/mcp/server.go:174` (toolsList), `server.go:311` (callTool)

`niwa_create_session`, `niwa_list_sessions`, `niwa_end_session` each need a
`toolDef` entry and a `case` in the switch. Purely additive. The `niwa_delegate`
schema needs a new optional `session_id` field (`handlers_task.go:45`) — backward-
compatible because existing callers omitting it get `""`, which preserves the
current fresh-spawn behavior (R13).

Whether these coordinator-only tools should appear in `ClaudeAllowedTools`
(`allowed_tools.go`) for worker spawns is unspecified in the PRD. They likely
should not be in the worker allowlist.

---

### N6 — Process execution location: MCP handlers vs. daemon

**Advisory (not a breaking change; structural risk if unaddressed)**

The current architecture routes all process execution through the mesh watch
daemon: `niwa_delegate` writes a task envelope; the daemon claims and spawns
the worker. R1 (`git worktree add`, per-worktree daemon start) and R4/R5
(`git worktree remove`, git inspection) propose doing this from MCP handlers —
transient per-session subprocesses with no supervisor, no restart policy, and
no lifecycle tie to the workspace.

This doesn't break anything today, but it creates a second process-spawning
site. Any future change to worktree teardown must be maintained in two places.
The safe design has `niwa_create_session` write a session record and signal the
daemon to perform the actual worktree and daemon setup — consistent with the
existing delegate → daemon → spawn pattern.

---

### N7 — `EnumerateRepos` surfaces session worktrees in `niwa go -r` completion

**File:** `internal/workspace/state.go:343`

`EnumerateRepos` scans two levels deep (instance → groups → repos). If session
worktrees are placed inside group directories, they would appear in `niwa go -r`
tab-completion and resolution. No correctness impact for apply or sync; UX issue.
Filterable by naming convention or by checking the session registry.

---

## CWD / `--resume` Alignment Constraint

**File:** `internal/cli/mesh_watch.go:1728` (`checkSessionFileIntegrity`)

Claude Code stores JSONL files at `~/.claude/projects/<base64(cwd)>/<session_id>.jsonl`.
`--resume <id>` only finds the file if the new worker's `cwd` matches the
original session's `cwd` exactly.

For R10 (same role, same worktree): CWDs match; `--resume` works.

For cross-role sessions (coordinator → repo agent): CWDs differ; the JSONL is
not found; the daemon silently falls back to R12 behavior (fresh spawn) without
surfacing the required warning. This is an inherent constraint of Claude Code's
session storage design — niwa cannot change it.

**Impact on PRD:** R10 is guaranteed only within a single role in a single
worktree. If the PRD envisions cross-role session continuity, it must
acknowledge this as a known limitation or be descoped.

---

## Summary

| # | Type | Location | One-liner? | Design required? |
|---|------|----------|-----------|-----------------|
| B1 | Breaking | `snapshotwriter.go:95`, `reset.go:148` | Yes | No |
| B2 | Breaking | `state.go:308` (EnumerateInstances) | No | Yes — worktree placement |
| B3 | Breaking | `server.go:604–706`, `session_registry.go:57` | No | Yes — two-root daemon model |
| N1 | New code | `mesh_watch.go:2211` (resolveRoleCWD) | No | No |
| N2 | New code | `mesh_watch.go:1669` (resumeSessionID) | No | No |
| N3 | New code | `types.go:265` (TaskState) | No | No |
| N4 | New code | `session_registry.go:20`, `types.go:102` | No | Yes — registry design |
| N5 | New code | `server.go:174, 311` (tool registration) | No | No |
| N6 | Advisory | MCP handler vs. daemon execution | — | Recommended |
| N7 | New code | `state.go:343` (EnumerateRepos) | No | No |

### Bottom line

The PRD is implementable. Three breaking changes require active mitigation
before the design doc is written:

1. **B1** is a one-liner; fix it early.
2. **B2** is resolved by a layout decision: place session worktrees inside the
   instance directory (e.g., under `.niwa/worktrees/`) rather than at workspace
   root level — then `EnumerateInstances` never sees them and no code changes
   are needed to enumeration logic.
3. **B3** is the hardest: the session-worktree daemon needs to carry its parent
   instance root and propagate it for coordinator routing. This is a new concept
   for the daemon and should be the primary focus of the design doc.

No breaking changes affect existing workspaces that don't use sessions.
All new-code items are additive and backward-compatible.
