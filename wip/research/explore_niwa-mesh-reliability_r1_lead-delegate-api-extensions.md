# Lead: What does niwa_delegate accept today, and what schema and precondition-check changes would required_skills and niwa_redelegate introduce?

## Findings

### 1. Current `niwa_delegate` MCP shape

**Tool registration:** `internal/mcp/server.go:264-279` (the `toolDef` for `niwa_delegate`) declares the wire schema:

- `to` (string, required) — target role
- `body` (object, required) — opaque task payload
- `mode` (string, optional) — `"async"` (default) or `"sync"`
- `expires_at` (string, optional, RFC3339) — task expiry deadline
- `session_id` (string, optional, 8 lowercase hex) — route into a session worktree
- `read_only` (boolean, optional) — bypass `SESSION_REQUIRED` for non-mutating tasks

The `Required` array is exactly `["to","body"]`.

**Go struct:** `delegateArgs` at `internal/mcp/handlers_task.go:46-55` mirrors that schema 1:1.

**Dispatcher:** `internal/mcp/server.go:419-424` JSON-decodes into `delegateArgs` and forwards to `s.handleDelegate(args)`.

### 2. Current handler validation pipeline

`handleDelegate` in `internal/mcp/handlers_task.go:111-165`. The order of checks:

1. `to == ""` → plain `errResult` (`handlers_task.go:112-114`).
2. `body` empty or `"null"` → plain `errResult` (`handlers_task.go:115-117`).
3. `mode` defaults to `async`; values other than `async`/`sync` → `errResultCode("BAD_PAYLOAD", …)` (`handlers_task.go:118-124`).
4. `session_id == "" && !read_only` → `errResultCode("SESSION_REQUIRED", …)` (`handlers_task.go:125-129`). **This is the only structured precondition gate today.**
5. `!s.isKnownRole(to)` → `errResultCode("UNKNOWN_ROLE", …)` (`handlers_task.go:130-133`). `isKnownRole` (server.go:768-778) is a pure `os.Stat` of `<instanceRoot>/.niwa/roles/<role>/` — no plugin/skill manifest is consulted.
6. Process-environment guards: `instanceRoot != ""` and `s.role != ""` (`handlers_task.go:134-139`).
7. Then `createTaskEnvelope` (`handlers_task.go:177-259`) writes the on-disk envelope and inbox file.

There is **no precondition check for capability/skill availability** at queue time. The MCP layer treats `body` as opaque `json.RawMessage` (no schema). All capability mismatch surfaces only later, when the worker session is spawned and actually fails.

### 3. Envelope on-disk schema

Definitive types at `internal/mcp/types.go:206-224`:

```go
type TaskEnvelope struct {
    V            int             // always 1
    ID           string          // UUIDv4
    From         TaskParty       // {role, pid}
    To           TaskParty       // {role}
    Body         json.RawMessage // opaque
    SentAt       string          // RFC3339
    ParentTaskID string          // omitempty
    DeadlineAt   string          // omitempty (declared but not set today)
    ExpiresAt   string           // omitempty
}
```

`createTaskEnvelope` (handlers_task.go:196-211) populates exactly these fields; `deadline_at` is declared but never written by the current code path. The envelope is written under `<taskStoreRoot>/.niwa/tasks/<task-id>/envelope.json`.

Alongside the envelope, `state.json` is seeded with `TaskState` (types.go:265-282) carrying `delegator_role`, `target_role`, `session_id`, `max_restarts=3`, `max_resumes=2`, and the initial `state_transitions: [{from:"", to:"queued", at:…}]` entry. The two files split mutable scope: `state.json` is the live ledger; `envelope.{from,to,sent_at,parent_task_id,expires_at}` are immutable, and only `envelope.body` is rewritable via `niwa_update_task`.

The envelope is then wrapped in a `Message{type:"task.delegate", body:envelopeBody}` (handlers_task.go:245-254) and written into the recipient inbox via atomic rename.

### 4. Inbox subdirectory layout — relevant for `niwa_redelegate` reads

Canonical inbox subdirs are defined at `internal/workspace/channels.go:186`:

```go
var inboxSubdirs = []string{"in-progress", "cancelled", "expired", "read"}
```

Provisioned at workspace creation (channels.go:295). Beyond those four, the runtime adds:

- `inbox/<task-id>.json` — queued (top-level inbox file).
- `inbox/in-progress/<task-id>.json` — daemon claimed, worker running (mesh_watch.go:852-857).
- `inbox/cancelled/<task-id>.json` — moved by `handleCancelTask` (handlers_task.go:894-898).
- `inbox/expired/<message-id>.json` — peer messages whose `expires_at` lapsed (server.go:613-643, `sweepExpired`).
- `inbox/read/` — provisioned but currently no code path moves files into it (channels.go:780 hints at message-read sweep; not wired today).
- `inbox/dangling/<task-id>.json` — created by `handleInboxEvent` in mesh_watch.go:792-801 when a queued envelope's task dir has no `state.json`. **Daemon-driven only**; not pre-provisioned.

There is no `completed/` or `abandoned/` inbox subdirectory. After a task finishes, the in-progress envelope is left in place (`internal/cli/mesh_watch.go:1708`: "The envelope file remains at .niwa/roles/<role>/inbox/in-progress/<id>.json across retries — the claim is one-shot"). A `task.completed` or `task.abandoned` peer message is delivered separately to the delegator's inbox via `sendTaskMessage` (handlers_task.go:1044-1065).

**Implication for redelegate cross-state reads:** the canonical place to look up *any* prior task by ID is `<taskStoreRoot>/.niwa/tasks/<task-id>/{envelope.json,state.json}` — the task store is **flat and keyed by task_id, not partitioned by state**. `taskDirPath` (auth.go:226-228) yields the same path regardless of terminal state. `ReadState` (taskstore.go:164-185) reads both files under a shared flock and returns them. Existing precedents already do this:

- `handleQueryTask` (handlers_task.go:387-393)
- `handleListOutboundTasks` (handlers_task.go:710-771) — iterates `<taskStoreRoot>/.niwa/tasks/` and filters by `env.From.Role == s.role`.
- `handleAwaitTask`'s race guard (handlers_task.go:427-430) re-reads `state.json` to detect terminal tasks.

A `niwa_redelegate` handler can therefore read the source envelope directly from `<taskStoreRoot>/.niwa/tasks/<source_task_id>/envelope.json` without scanning inbox subdirs at all. Inbox subdirs only matter if redelegate also wants to clean up the source's inbox file (e.g., move the dangling/cancelled file into a `redelegated/` cluster) — that requires `resolveInboxDir(sessionID, role)` (handlers_task.go:301-311), which already supports cross-session reads.

### 5. Authorization surface

`authorizeTaskCall` (auth.go) validates four kinds: `kindDelegator` (only the role on `from`), `kindExecutor` (the spawned worker), `kindParty` (either side), `kindTarget` (only the target role). For `niwa_redelegate` the natural choice is `kindDelegator` on the *source* task — only the original delegator can re-issue. `niwa_update_task` and `niwa_cancel_task` already use this pattern (handlers_task.go:797, 879).

### 6. What `niwa_query_task` and `niwa_list_outbound_tasks` surface today

`formatQueryResult` (handlers_task.go:937-960) returns: `task_id`, `state`, `state_transitions`, `restart_count`, optional `last_progress`, plus `result`/`reason`/`cancellation_reason` on terminal. **Envelope fields (body, from, to, parent_task_id, expires_at) are NOT surfaced.** The body is consultable only by re-reading `envelope.json` from disk.

`handleListOutboundTasks` (handlers_task.go:710-771) returns rows of `{task_id, to_role, state, age_seconds, body_summary}` — `body_summary` is a single-line truncation of the envelope body (200 char cap, handlers_task.go:775-789). No `parent_task_id`, no full body.

A redelegate caller working only from query/list output cannot recover the full body — they have to either remember it themselves or rely on the new tool reading envelope.json server-side.

### 7. Daemon ownership of `task.delegate`

`daemonOwnsInboxFile` in mesh_watch.go:746-758: the daemon claims **only** files whose body parses to `type=="task.delegate"`. So a `niwa_redelegate` that writes a fresh `task.delegate` message into the target inbox plugs into the existing claim/spawn loop with no additional daemon plumbing.

## Implications

### Where `required_skills` should gate (depends on lead 2 for the manifest)

The cleanest insertion point is **between the `UNKNOWN_ROLE` check and `createTaskEnvelope`** in `handleDelegate` (handlers_task.go:130-141). At that point the target role is known to exist; we just need to read the target session's plugin/skill manifest and intersect with `body.required_skills`. The check should:

- Return a new `BAD_PAYLOAD`-style code, e.g. `MISSING_SKILLS`, with the missing-skill list in the error body. Add it to the documented R50 error vocabulary in `handlers_task.go:13-15` and `handlers_session.go`.
- Read the target session's manifest from `<worktreePath>/.claude/...` (or wherever lead 2 lands the manifest source-of-truth). For non-session (`read_only:true`) routing, the manifest is the main instance's `.claude/` tree under `instanceRoot`.
- Be a precondition: it should not allocate a task ID or write any envelope.

`required_skills` should **not** be added to the top-level `delegateArgs` schema; it lives inside `body` because it is part of the workspace's body convention (already established by the `niwa-mesh` skill docs). The handler peeks at `body.required_skills` via a small `json.RawMessage` decode. Surface in audit log via `arg_keys` is unchanged because audit only logs top-level keys (`audit.go:28-37`).

### `niwa_redelegate` shape

Strawman wire schema (using terms from the prompt):

```
niwa_redelegate(
  source_task_id: string [required],
  to:            string,                       // optional override of source.to.role
  session_id:    string,                       // optional override of source.session_id
  read_only:     boolean,                      // optional override
  body_overrides: object,                      // shallow JSON-merge into source.body
  mode:          "async" | "sync",
  expires_at:    string,
)
```

Server-side flow (mirroring `handleDelegate` plus a source read):

1. Authorize the caller as `kindDelegator` on `source_task_id` (auth.go pattern, handlers_task.go:797 precedent). Returns `NOT_TASK_OWNER` / `NOT_TASK_PARTY` cleanly.
2. `ReadState(taskDirPath(s.taskStoreRoot(), source_task_id))` to fetch `srcEnv` and `srcState` (taskstore.go:164). Works regardless of source's terminal state — task dir is flat and persists.
3. Decide which fields propagate vs regenerate:
   - **Regenerate:** `id` (NewTaskID), `sent_at` (now), `parent_task_id` (= s.taskID, same auto-fill rule), state.json (`state_transitions`, `restart_count=0`, `worker={}`).
   - **Propagate from source (default):** `body` (with `body_overrides` merged), `to.role` (override-able via `to` arg), `session_id` (override-able), `expires_at` (only if the caller doesn't pass a fresh one).
   - **Refuse to propagate:** `from` (always set to caller's role/pid — the redelegator owns the new task; this preserves audit attribution), `result`, `reason`, `cancellation_reason`, `restart_count`, `last_progress` from source state.
4. Add a back-pointer field to the new task: either an envelope-level `redelegated_from: <source_task_id>` (additive on `TaskEnvelope` v=1, `omitempty`-safe) or a state.json field. Envelope is the more natural place because it is the immutable record.
5. Run the same `required_skills` gate as `handleDelegate` (so a redelegate cannot bypass capability checks).
6. Run `createTaskEnvelope` with the merged body — get the new task ID and inbox-write — and return it in the same shape as `niwa_delegate`. Sync mode reuses `registerAwaitWaiter`.

**Validation:** reject if source task has `state == queued` or `state == running` (no concurrent dual-fanout) — the user should `niwa_cancel_task` first. Allow redelegation from `abandoned`, `cancelled`, `completed`, and from `dangling` (which manifests as `queued` in state.json with the file in `inbox/dangling/`). The handler can detect dangling by `state==queued` plus the inbox file's absence at `inbox/<id>.json` (the existing `niwa_update_task` consumed-detection pattern at handlers_task.go:854-859 is a precedent).

**Attribution semantics:** the new envelope's `from.role` is the redelegator's role at the time of the call. If the redelegator is a different session/role from the original delegator (e.g. coordinator A redelegates a task originally queued by coordinator B), the task store reflects A as owner — required for `kindDelegator` auth on subsequent `cancel`/`update`/`redelegate` calls. The `redelegated_from` envelope field preserves the audit chain.

### Cross-inbox-subdirectory reads

A redelegate handler does not need to scan inbox subdirs to find the source — `taskDirPath` resolves to a flat directory keyed by task_id. It only needs `resolveInboxDir` (handlers_task.go:301-311) if it wants to stamp the source's inbox file into a new `redelegated/` cluster (mirroring `cancelled/`). Existing precedent for moving files between inbox subdirs is `handleCancelTask`'s `os.Rename(src, dst)` from `inbox/<id>.json` → `inbox/cancelled/<id>.json`.

## Surprises

- **Session-required is the only structured precondition today.** Even `UNKNOWN_ROLE` is only a directory existence check (`os.Stat`); a role with zero installed skills is "known" and `niwa_delegate` accepts everything. There is no skill manifest at all in `internal/mcp/`.
- **The task store is flat by task_id, not by state.** I expected separate dirs for terminal tasks. In fact, every task lives at `<taskStoreRoot>/.niwa/tasks/<id>/` for its entire lifetime — only the inbox **message** moves between subdirs. This makes redelegation lookups trivial.
- **`deadline_at` is in `TaskEnvelope` (types.go:222) but no code path writes it.** Only `expires_at` is set.
- **The `read/` inbox subdir is provisioned but unused.** `inboxSubdirs` includes it (channels.go:186) and the help text mentions it (channels.go:780-781), but there is no `os.Rename` into `inbox/read/` anywhere in the codebase. Potential cleanup target but not blocking.
- **Daemon ownership predicate is body-content based, not filename based.** `daemonOwnsInboxFile` (mesh_watch.go:746-758) parses `type` from the file body. So `niwa_redelegate` writing a `task.delegate` message gets claimed automatically — no daemon code change needed.
- **`niwa_query_task` does not surface envelope body or `parent_task_id`.** A coordinator redelegating from external state would have to re-state the body itself. The redelegate primitive's main value is server-side body re-read; without it, callers cannot recover the body via the public API.

## Open Questions

1. **Where does the skill/plugin manifest live?** Lead 2 will answer; the gate point in `handleDelegate` is identified, but the manifest source-of-truth and discovery rules are not yet known.
2. **Should `niwa_redelegate` allow body-override patches, or only full body replacement?** A shallow JSON-merge feels right (callers tweak instructions while keeping `required_skills` etc.), but recursive merge has well-known foot-guns. A simple "if `body_overrides` present, use it as the new body wholesale; otherwise keep source body" might be cleaner.
3. **Should the new envelope's `parent_task_id` point to the *source task* (chain) or to the calling worker's task (current handler default)?** Current code defaults `parent` to `s.taskID` (handlers_task.go:192-195). For redelegate, the source task is not the parent in the spawn-tree sense — it's the *predecessor*. A separate `redelegated_from` field plus the existing `parent_task_id` semantics seems cleanest; `parent_task_id` continues to mean "spawned by which running task."
4. **Do redelegated dangling tasks need the source inbox file cleaned up?** A dangling file in `inbox/dangling/` is harmless once redelegated, but operators may want it moved into a `redelegated/` subdir for clarity. Optional and easily added later.
5. **Should `required_skills` be a top-level argument or a body convention?** Existing convention is "body is opaque to niwa." Putting `required_skills` as a top-level delegateArgs field would add structured semantics and audit log fidelity (it would appear in `arg_keys`). But it splits the body convention. Keeping it inside `body.required_skills` matches existing skill docs at the cost of a body peek.
6. **Audit log impact.** A new tool needs an entry in `allowed_tools.go:18` (alongside `niwa_delegate`) and entries in audit tests (audit_test.go:96, 264). Trivial but easy to forget.

## Summary

`niwa_delegate` (registered at `server.go:264-279`, handled at `handlers_task.go:111-165`) accepts an opaque `body` plus `to`, `mode`, `expires_at`, `session_id`, `read_only` and writes a flat-keyed `<taskStoreRoot>/.niwa/tasks/<id>/{envelope.json,state.json}` plus a `task.delegate` inbox message; the only structured precondition today is `SESSION_REQUIRED`, with `UNKNOWN_ROLE` reduced to an `os.Stat` and no skill/manifest awareness anywhere in the MCP layer. A `required_skills` gate slots cleanly between the existing `UNKNOWN_ROLE` and `createTaskEnvelope` calls (handlers_task.go:130-141), peeking into `body.required_skills` and consulting whatever manifest lead 2 surfaces, returning a new `MISSING_SKILLS` error code without allocating a task ID. `niwa_redelegate` is straightforward because the task store is task_id-keyed and not partitioned by state — `ReadState(taskDirPath(...))` recovers any source envelope regardless of whether it sits in `inbox/<id>.json`, `inbox/in-progress/`, `inbox/cancelled/`, or `inbox/dangling/` — so the new handler reads the source body, merges any overrides, runs the same `required_skills` gate, and re-enters `createTaskEnvelope` with `from` reset to the caller and a new `redelegated_from` back-pointer on the envelope.
