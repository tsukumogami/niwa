---
status: Draft
upstream: docs/roadmaps/ROADMAP-niwa-collab-surface.md
problem: |
  niwa's collab surface (F5–F15 in ROADMAP-niwa-collab-surface) has no
  primitive for the unit it exists to review. Every downstream feature
  (F6 comments, F7 anchoring, F8 threading, F9 mentions, F10 verdict,
  F11/F12/F13 surfaces, F14 koto linkage) encodes against a "change"
  object that doesn't exist yet. There is also no web surface — the
  category niwa needs to establish before retrofitting it under F13.
  Getting the data model, URL contract, or auth contract wrong forces
  migration of stored review data and breaks F5–F12-era Telegram links
  when F13's polished scroll-to-comment ships.
goals: |
  Ship the foundational change primitive (data model, lifecycle,
  on-disk format), the basic web render at `niwa surface serve`, and
  the URL + auth contracts that downstream features compose on. Land
  the walking skeleton: an agent creates a change, Dan opens the
  browser, sees the diff — end-to-end observable before any comment
  or verdict primitive exists. Commit verbatim to schemas and contracts
  so F6–F14 can build against stable shapes.
---

# PRD: niwa Change-as-Reviewable Primitive and Basic Web Render

## Status

Draft

## Problem statement

niwa is the local-first medium for human-agent and agent-agent review
(see [VISION-niwa-collab-surface](../visions/VISION-niwa-collab-surface.md)).
The substrate that the collab surface composes on — mesh, MCP
coordination, session worktrees, role-stable identity, audit logs —
runs daily across the reference fleet. What is missing is the unit of
review itself: a `change` object that ties a worktree branch + commit
range + originating sessions + lifecycle state + (eventually) comments
and a verdict into one addressable, reviewable thing.

Three concrete gaps:

**No data primitive.** Tasks (`.niwa/tasks/<task-id>/`) and sessions
(`.niwa/sessions/<sid>.json`) exist; neither models "a reviewable code
unit." A task is an executable work unit (lifecycle = queued → claimed →
done/abandoned); a change is a reviewable code unit (lifecycle =
pending → in-review → verdict-cast → cleaned). Different lifetimes,
different state machines. Forcing a change into the task envelope
collapses two state machines into one and forecloses mesh
co-authorship (a worktree branch where multiple agents collaborated on
one change).

**No web surface category.** niwa is CLI-first today. The CLI is the
obvious surface, but the web is the *new* surface category — the one
Dan opens from a Telegram drill-in on his phone, the one F13 polishes
post-launch. Adding the web surface at F5 forces it to be a first-
class design concern from the foundation, rather than retrofitting it
under F13 against schemas that didn't anticipate it.

**No URL contract.** F13 ships polished scroll-to-comment over a
fragment scheme like `#comment-<id>`. If F5 ships the web surface but
doesn't lock the fragment scheme, every F5–F12-era Telegram drill-in
URL breaks when F13 lands. Locking the contract once, at F5, prevents
that churn.

The walking-skeleton commitment from the roadmap is: after F5 ships,
an agent creates a change, Dan opens `http://127.0.0.1:<port>/changes/<id>`
in his browser, sees the diff — end-to-end observable before any
comment or verdict primitive exists. F5 establishes the change
primitive *and* the web surface category in one ship.

## Goals

1. **Change primitive committed.** UUIDv4-keyed change object stored
   at `.niwa/changes/<change-id>/state.json`, with a four-state
   lifecycle (`pending` → `in-review` → `verdict-cast` → `cleaned`),
   `originating_sessions: [<sid>]` plural for mesh co-authorship, and
   a reserved verdict slot (populated by F10).

2. **MCP tools shipped.** `niwa_create_change`, `niwa_list_changes`,
   `niwa_query_change` registered in the existing MCP server with
   verbatim I/O shapes downstream features can build against.

3. **Web surface category established.** `niwa surface serve` boots an
   HTTP listener on 127.0.0.1 (ephemeral port) in a new
   `internal/web/` package. Read-only render of diff + change metadata
   at `/changes/<id>`. F6+ layers comments, F10 layers verdict, F13
   polishes ergonomics — all on the contracts locked here.

4. **Per-instance auth contract locked.** UUIDv4 token at
   `.niwa/surface.token` (0o600), required for any mutation endpoint.
   F5 ships no mutation endpoints itself; the contract is locked so
   F10's verdict-cast endpoint composes without retrofit. Strict CORS
   rejection.

5. **URL fragment scheme locked.**
   `http://127.0.0.1:<port>/changes/<change-id>#comment-<comment-id>`.
   F5 has no comments (F6 owns the primitive), but the fragment shape
   ships so F5–F12-era Telegram links remain stable when F13's
   scroll-to-comment lands.

6. **Audit events emitted into the substrate.** `change_ready`,
   `review_surface_opened`, `change_engaged`, `change_cleaned`. Per-
   change `transitions.log` + instance-wide `mcp-audit.log` (v=2
   schema bump). Observable, not silent.

7. **Lifecycle GC committed.** Abandoned changes (≥14 days at
   `pending`, configurable per workspace) move to `cleaned` on a
   recurring sweep. Cleanup is observable via `change_cleaned`.

## Non-goals

- **Comments.** F6 owns the comment primitive, the storage format
  (`comments.ndjson`), the `niwa_post_review` batch tool, and the
  comment rendering. F5 is read-only diff + metadata.
- **Verdict cast.** F10 owns the verdict state machine transitions,
  the cast UI, the agent-read pathway, and the auto-stale dampening.
  F5 reserves the verdict slot in `state.json` (always `null` until
  F10).
- **Line anchoring.** F7 owns anchor tuples and port-forward.
- **Threading, mentions, CLI/TUI surfaces, koto linkage, polished
  deep-link.** F8/F9/F11/F12/F13/F14/F15 own their respective scopes.
- **Telegram bridge wiring.** F5 emits `change_ready` correctly into
  the audit substrate. The roadmap describes a "fired into
  `mcp-audit.log` → coding-tools picks it up" path that is
  mechanistically inaccurate (the Telegram plugin does not tail audit
  logs; it long-polls Telegram's `getUpdates` API). The bridge needs
  its own spec; F5 documents the boundary. See [Reconciliation:
  Telegram bridge boundary](#reconciliation-telegram-bridge-boundary).
- **Hosted niwa, team-scale.** F16/F17, gated on launch-post external
  interest signals per VISION D1.

## User stories

**US1: Agent creates a reviewable change.** As a worker agent that has
just produced a commit range on its session branch, I want to call
`niwa_create_change` so that the work becomes addressable as a change
object (with a `change_id`, a URL, and a captured diff snapshot) and an
observable `change_ready` event fires into the audit substrate.

**US2: Dan opens a change from his browser.** As Dan with a Telegram
ping pointing me at `http://127.0.0.1:<port>/changes/<id>`, I want to
open the URL and see the diff + change metadata so that I can decide
whether to engage further. The page renders without any handshake or
manual login.

**US3: Dan lists pending changes from the index.** As Dan visiting
`http://127.0.0.1:<port>/changes/`, I want to see all changes in
`pending` or `in-review` state with their IDs, head commits, and last-
updated times so that I can scan what's awaiting review without poll
the CLI.

**US4: Agent queries change state for downstream composition.** As an
F10-era verdict cast or an F14-era koto bridge that needs to read a
change's current state, I want to call `niwa_query_change(change_id)`
and receive the full `state.json` plus a tail of recent transitions so
that I can compose against stable shapes without parsing files
directly.

**US5: Mesh agents co-author one change.** As multiple worker agents
operating on the same session worktree, I want a single change to
record all of us in `originating_sessions: [<sid>...]` so that the
review surface knows the work has multiple authors and downstream
features (F6 author labels, F10 verdict notification fan-out) reach
all of us.

**US6: GC reclaims an abandoned change.** As Dan with stale `pending`
changes from sessions I never reviewed, I want niwa to mark changes
abandoned after 14 days and emit a `change_cleaned` event so that the
listing stays focused and the cleanup is auditable, not silent.

**US7: F10 composes verdict mutations on the locked auth contract.**
As the F10 PRD author, I want F5's `.niwa/surface.token` and Bearer-
auth contract to be in place so that F10 can ship `POST
/changes/<id>/verdict` without inventing the auth layer.

## Requirements

### Functional

#### R1: `change` object schema (v=1, commit verbatim)

`.niwa/changes/<change-id>/state.json` contains the following document.
All fields are required unless marked optional. Field naming is
`snake_case`. Timestamps are RFC3339Nano UTC.

```json
{
  "v": 1,
  "id": "<uuidv4>",
  "state": "pending" | "in-review" | "verdict-cast" | "cleaned",
  "originating_sessions": ["<session-id>", "..."],
  "originating_tasks": ["<task-uuid>", "..."],
  "created_at": "<rfc3339nano>",
  "updated_at": "<rfc3339nano>",
  "base_ref": "<commit-sha>",
  "head_ref": "<commit-sha>",
  "branch": "session/<session-id>",
  "worktree_path": "<abs-path>",
  "diff_path": "diff.patch",
  "verdict": null,
  "metadata": {}
}
```

- `id` is UUIDv4 (matches `task_id` convention; rejected: 8-hex session
  ID format, which is for short-lived addressable entities — changes
  are durable artifacts).
- `originating_sessions` is plural to support mesh co-authorship (a
  worktree branch where multiple agents collaborated on one change).
  F5 typically writes a single session ID; the array shape locks
  early to avoid F6/F10 schema migration.
- `originating_tasks` is plural for symmetry; a change can reference
  N task UUIDs that produced it.
- `verdict` stays `null` for F5. F10 populates it. The slot is
  reserved on the v=1 schema so F10 does not require a v bump.
- `metadata` is an opaque extension slot reserved for downstream
  features. F5 writes `{}` and reads it back unchanged; F5 makes no
  commitments about its internal shape. F6+ PRDs may add fields here
  or introduce new top-level fields on `state.json` as needed.

State machine (validated in code):

- `pending → in-review` on first review-surface engagement with the
  change. F5 era: the first `GET /changes/<id>` hit triggers the
  transition. F6 PRD may revisit (e.g., first comment posted is the
  trigger). See [Open item 1](#open-item-1-state-machine-entry-to-in-review).
- `in-review → verdict-cast` on F10's verdict cast (F5 reserves the
  transition; does not invoke it).
- `pending → cleaned` on GC sweep when the change is abandoned (R9).
  Changes in `in-review` or `verdict-cast` are never auto-cleaned.

Concurrent state transitions are serialized via the per-change
`.lock` file: any handler mutating `state.json` (including the
`pending → in-review` write triggered by `GET /changes/<id>`)
acquires the flock, re-reads `state.json`, transitions if the
read-state still permits it, writes atomically (tmp+rename, 0o600),
and releases the flock. A double-arrival race on `GET /changes/<id>`
therefore produces one `in-review` write and one no-op observation;
`change_engaged` events fire once per HTTP hit regardless (R5).

Atomic creation uses `O_CREATE|O_EXCL` placeholder + 5-retry birthday
loop, mirroring `newSessionLifecycleID` (`session_lifecycle.go:140-159`
in niwa). Rejected: TOCTOU `os.Stat` + `MkdirAll`.

File modes on the change directory and all files within: `0o600` for
files, `0o700` for the directory.

#### R2: On-disk layout

```
.niwa/changes/<change-id>/
├── .lock                # exclusive lock file (matches .niwa/tasks/ pattern)
├── state.json           # ChangeState v=1 (R1)
├── transitions.log      # per-change NDJSON, append-only under flock+fsync
└── diff.patch           # unified diff snapshot (R7)
```

F6 will add `comments.ndjson` to the same directory. The directory is
orthogonal to `.niwa/tasks/<task-id>/`: different lifetimes, different
state machines. The two compose by reference (a change records
`originating_tasks: [<task-uuid>...]`).

#### R3: MCP tools (commit verbatim)

The MCP server (`internal/mcp/server.go`) gains three tools registered
as `toolDef` entries, dispatched via the existing switch, with
audit-logging emitted for free by `dispatch()`. Handlers live in
`internal/mcp/handlers_change.go` (matches `handlers_task.go` /
`handlers_session.go` convention).

##### `niwa_create_change`

```json
// Input
{
  "session_id": "<8-hex>",                       // required
  "base_ref_hint": "<git ref>",                  // optional override
  "metadata": {}                                 // optional, role-keyed
}

// Output
{
  "change_id": "<uuidv4>",
  "state": "pending",
  "url": "http://127.0.0.1:<port>/changes/<change_id>",
  "base_ref": "<sha>",
  "head_ref": "<sha>"
}
```

Behavior:
- Validate `session_id` (must match `^[0-9a-f]{8}$`); on malformed
  input return error `invalid_session_id`.
- Load `.niwa/sessions/<session_id>.json`; on absent file return
  error `session_not_found`.
- Resolve the session's worktree path; on missing worktree (path
  absent or not a git repo) return error `worktree_missing`.
- Compute (`session_id`, `head_ref`) idempotency key. If a non-
  `cleaned` change for this key already exists, return its existing
  `change_id` with `state: "not_modified"` and skip event emission.
  The dedup check runs **before** UUIDv4 allocation so the birthday
  loop does not race against an already-emitted `change_ready`.
- Resolve `base_ref`: if `base_ref_hint` is provided, use it. On a
  malformed or unresolvable hint return error
  `base_ref_hint_unresolved` (no fallback to the discovery chain —
  explicit hints are authoritative). Otherwise resolve via the
  discovery precedence in [R8](#r8-base-ref-discovery); on full chain
  failure return error `base_ref_unresolved`.
- Capture the diff snapshot to `diff.patch` (R7). Empty diff is
  permitted (change still created; `change_ready` still fires).
- Write `state.json` atomically (R1).
- Emit `change_ready` event (R5).
- Return the URL composed from the live surface port read from
  `.niwa/surface.port` (R10). If `.niwa/surface.port` is absent
  (surface not running), return the URL with the placeholder
  `<port>` literally substituted (e.g.,
  `http://127.0.0.1:<port>/changes/<id>`). The caller is expected
  to refresh once the surface boots; the URL is durable across
  surface restarts via the change ID, not the port.

Error code summary (returned in MCP error envelope):

| Code | Trigger |
|------|---------|
| `invalid_session_id` | `session_id` does not match `^[0-9a-f]{8}$` |
| `session_not_found` | `.niwa/sessions/<session_id>.json` absent |
| `worktree_missing` | Session's worktree path absent or not a git repo |
| `base_ref_hint_unresolved` | Caller-provided `base_ref_hint` does not resolve via `git rev-parse` |
| `base_ref_unresolved` | Auto-discovery chain (R8) fully exhausted |
| `payload_too_large` | Audit-log entry exceeds 2KB; mutation succeeds, audit downgraded (R4) |

##### `niwa_list_changes`

```json
// Input
{
  "state": "pending|in-review|verdict-cast|cleaned",    // optional filter
  "session_id": "<8-hex>"                                // optional filter
}

// Output
{
  "changes": [
    {
      "id": "<uuidv4>",
      "state": "<state>",
      "created_at": "<rfc3339nano>",
      "url": "<url>",
      "head_ref": "<sha>",
      "branch": "<branch>"
    }
  ]
}
```

Both filters are AND-composed when both present. Order: most recently
`updated_at` first.

##### `niwa_query_change`

```json
// Input
{ "change_id": "<uuidv4>" }

// Output: full ChangeState (R1 shape) + recent transitions tail
{
  "state": { /* R1 shape */ },
  "transitions": [
    /* last 20 entries from transitions.log, oldest-first */
  ]
}
```

Returns `not_found` error if the change does not exist or is in
`cleaned` state (state.json is preserved for forensics, but
`niwa_query_change` treats cleaned as not visible to programmatic
consumers; web surface shows it with a "cleaned" badge — see R12).

#### R4: Audit log v=2 schema bump

`.niwa/mcp-audit.log` schema is incremented from v=1 to v=2 with a
backward-compatible reader. v=1 entries continue to round-trip
(absence of `kind` field is treated as `kind="tool_call"` by readers).

v=2 entry shape:

```json
{
  "v": 2,
  "at": "<rfc3339nano>",
  "kind": "tool_call" | "event",
  "role": "<role>",
  "task_id": "<task-uuid>",
  "tool": "<tool-name>",                  // present when kind=tool_call
  "arg_keys": ["<key>", "..."],           // present when kind=tool_call
  "event": "<event-name>",                // present when kind=event
  "payload": { /* event-specific */ },    // present when kind=event
  "ok": true|false,
  "error_code": "<code>"                  // optional
}
```

The atomicity model (mutex + `O_APPEND ≤ PIPE_BUF`) is unchanged.
Total entry size budget is ≤2KB (PIPE_BUF on Linux is ~4096 bytes;
2KB leaves headroom for envelope fields). The emitter handles over-
budget payloads as follows:

- The audit entry is downgraded: `kind="event"` is preserved, the
  original `payload` is replaced with `{}`, and `error_code` is set
  to `"payload_too_large"`.
- The originating mutation (e.g., `niwa_create_change`) still succeeds
  — audit-emit failure does not poison the MCP call. The MCP response
  includes the actual result; audit downgrade is observable only by
  audit-log readers.
- This keeps F5 emits robust against schema growth in downstream
  features that may stuff more fields into event payloads; the audit
  substrate stays append-only and bounded even when payload conventions
  expand.

#### R5: Event taxonomy (F5 emits four)

| Event | When | Required payload |
|-------|------|------------------|
| `change_ready` | `niwa_create_change` succeeds | `change_id`, `url`, `originating_sessions`, `base_ref`, `head_ref` |
| `review_surface_opened` | `GET /` or `GET /changes/` hit | `surface_url` |
| `change_engaged` | `GET /changes/<id>` hit | `change_id`, `surface_url` |
| `change_cleaned` | GC moves a change to `cleaned` | `change_id`, `reason` (e.g., `"abandoned_after_n_days"`), `n_days` |

All four are emitted via a single `appendEventLog` helper to **both**
the per-change `transitions.log` AND `.niwa/mcp-audit.log` v=2. One
extra append per event vs. single-target — accepted cost for
observability of change-scoped reads (per-change log) plus
instance-wide observability (audit log for future bridge consumers).

`transitions.log` entries follow the existing NDJSON shape with
`{kind, from, to, at, payload}`; `kind` is a free string per the
existing convention (no central registry — emitter writes, readers
discriminate).

Emitter is idempotent for `change_ready` keyed by the (`session_id`,
`head_ref`) tuple computed before `change_id` allocation. A re-issued
`niwa_create_change` for the same tuple is a `not_modified` no-op and
emits no event. The existing `change_id` is returned to the caller.
For `review_surface_opened` / `change_engaged`, the emitter does not
dedup — every HTTP hit produces an event. The VISION's success-
criteria "drill-in within 60 seconds" is computed at analysis time by
correlating `review_surface_opened` and `change_engaged` event
timestamps; F5 emits both events on every hit and leaves the
correlation window to the analysis layer (not enforced at emit
time).

#### R6: HTTP surface

A new package `internal/web/` ships the listener. The MCP server
spawns the listener in a goroutine at startup; cancellation flows from
the MCP server's root context through `http.Server.Shutdown(ctx)`.
**No signal handlers live in `internal/web/`** — lifecycle ownership
stays with the MCP server.

| Concern | Decision |
|---------|----------|
| Routing library | stdlib `net/http` (Go 1.22+ method+path patterns). Rejected: chi, gorilla, echo. |
| Bind address | `127.0.0.1` only. Rejected: `0.0.0.0`. |
| Port | Ephemeral via `net.Listen("tcp", "127.0.0.1:0")`. Kernel-assigned. CLI override: `niwa surface serve --port N`. |
| Port advertisement | Atomic write (tmp+rename) of the actual bound port to `.niwa/surface.port` (0o600). MCP tool URL composition reads this file. |
| Auth token | UUIDv4 stored at `.niwa/surface.token` (0o600). Generated on first `niwa surface serve` boot if absent. |
| Auth header | `Authorization: Bearer <token>`. Rejected: cookies, basic auth, query-param token. |
| Token rotation | `niwa surface serve --rotate-token` regenerates the file. |
| CORS | Strict-origin. No `Access-Control-Allow-*` headers. All cross-origin requests rejected. |

Endpoint catalog (F5 era):

| Route | Method | Auth | Behavior |
|-------|--------|------|----------|
| `/` | GET | none | 302 redirect to `/changes/` |
| `/changes/` | GET | none | HTML index: list of changes in `pending` and `in-review` state, sorted by `updated_at` desc |
| `/changes/<id>` | GET | none | HTML render of `state.json` metadata + unified diff from `diff.patch`. The URL fragment `#comment-<id>` is part of the URL contract (R11); F5 emits no DOM anchors because no comments exist, but the fragment never breaks the URL |

Auth is **not required** for the F5 read-only endpoints. The token
contract is locked for future mutation endpoints. F10's `POST
/changes/<id>/verdict` will require Bearer auth; F5 ships no mutation
endpoints, so requiring auth on reads adds friction without security
benefit. The security boundary at F5 era: any same-machine process
that can read `.niwa/surface.token` already has filesystem access to
`.niwa/changes/<id>/`. The token gates mutations, not reads.

CSS is inlined in the HTML response. No static-asset endpoint. F11/F12/
F13 may layer asset endpoints on later; F5 keeps the surface area
minimal.

Per-instance scope: one HTTP listener per niwa instance, serving all
sessions' changes for that instance. The roadmap names `niwa surface
serve` as instance-scoped, not session-scoped.

#### R7: Diff capture

At `niwa_create_change` time, snapshot the diff to
`.niwa/changes/<change-id>/diff.patch` via:

```
git -C <worktree-path> diff <base_ref>..<head_ref>
```

Unified diff format. The snapshot is **immutable** once written —
post-creation force-push, rebase, or session destruction do not
rewrite `diff.patch`. This is durable across session lifecycle events
that would otherwise lose review context. F7 (line-anchoring with
port-forward on amendment) handles diff drift detection and re-
anchoring; F5's snapshot is the substrate F7 ports forward from.

Diff size cap: if the captured diff exceeds 4 MiB (4,194,304 bytes),
truncate the file at the byte boundary and append a single-line
trailer `--- diff truncated at 4 MiB; full diff available via 'git -C
<worktree> diff <base>..<head>' ---`. The unified-diff format
tolerates trailing trailer lines outside any hunk; renderers treat
the trailer as plain text. The cap exists to bound disk usage on
auto-generated large changes; downstream features that need full diff
content invoke git directly. See [Open item 6](#open-item-6-diff-size-cap).

Empty-diff case: if `git diff <base>..<head>` produces no output
(base and head identical, or no changed lines), `diff.patch` is
written as an empty file. The change is still created and
`change_ready` fires. The web render shows "no changes" in the body.

#### R8: Base-ref discovery

When `niwa_create_change` is invoked without `base_ref_hint`, resolve
the base ref using the following precedence (first match wins):

1. `git -C <worktree> symbolic-ref refs/remotes/origin/HEAD` (resolves
   to e.g. `refs/remotes/origin/main`)
2. `origin/main`
3. `origin/master`
4. `main`
5. `master`

If all five fail, return error `base_ref_unresolved` from
`niwa_create_change`. Document the discovery chain in the error
message so the caller can pass `base_ref_hint` explicitly. F5 is the
first niwa feature to need `git merge-base`-style discovery;
establishing the precedence here is the convention for downstream
features.

When the resolved base falls past `origin/HEAD` (i.e., precedence ≥3
fires), emit a warning to stderr (`niwa surface serve` logs) noting
that no `origin/HEAD` was found and the resolved base may not match
the repo's default branch.

#### R9: GC / cleanup

Garbage collection sweep:

- **Trigger:** on `niwa surface serve` boot AND every
  `gc_interval_hours` thereafter. Default 6. Configurable via
  `[changes] gc_interval_hours = 6` in `workspace.toml`. Valid range:
  ≥1 and ≤168 (1 week). Values outside the range cause
  `niwa surface serve` to exit 1 at boot with
  `"invalid gc_interval_hours: must be between 1 and 168"`.
- **Abandonment threshold:** change in `pending` state for
  ≥`gc_abandon_days` days without an `updated_at` advance. Default
  14. Configurable via `[changes] gc_abandon_days = 14`. Valid
  range: ≥1 and ≤365. Out-of-range values exit 1 at boot.
- **Action:** transition the change to `cleaned`; delete `diff.patch`
  to reclaim disk; preserve `state.json` and `transitions.log` for
  forensics; emit `change_cleaned` event.

Only changes in `pending` are swept. Changes in `in-review` or
`verdict-cast` state are never auto-cleaned — the human or an agent
has engaged with the change, and silent loss of `diff.patch` would
break F7-era line anchors and F10-era verdict drift detection.

A daemon process is not required: the sweep runs in a goroutine spawned
by the HTTP server's lifecycle. If `niwa surface serve` is never
started, no GC runs and changes accumulate; this is the expected
behavior (no listener = no observability of changes anyway).

#### R10: Surface lifecycle

`niwa surface serve` is the CLI command that boots the HTTP listener.
Behavior on boot:

1. Acquire instance-wide lock via `.niwa/surface.lock`. The lock file
   contains the holder PID (decimal, single line). Acquisition
   protocol:
   - `O_CREATE|O_EXCL` create + write PID; on success, proceed.
   - On `EEXIST`: read the PID; check whether a process with that PID
     exists (`os.FindProcess` + `Signal(syscall.Signal(0))` on Unix).
     If the holder is dead, the lock is stale: remove the file and
     retry `O_CREATE|O_EXCL` once. If the holder is alive, exit 1
     with `"surface already running for this instance (pid <PID>)"`.
2. Generate `.niwa/surface.token` (UUIDv4, 0o600) if absent. If
   `--rotate-token` is passed, regenerate even if present.
3. Bind 127.0.0.1 ephemeral port (or `--port N` override). Write the
   actual port to `.niwa/surface.port` (tmp+rename, 0o600).
4. Print to stderr: the URL and the path to `.niwa/surface.token`.
   The token contents are never logged or printed in any form.
5. Run GC sweep once synchronously, then start the 6h ticker in a
   goroutine.
6. Serve HTTP until SIGTERM/SIGINT. On shutdown: cancel the GC
   ticker, `http.Server.Shutdown(ctx)`, release the lock (remove
   `.niwa/surface.lock`), remove `.niwa/surface.port` (token
   persists).

`niwa surface serve --rotate-token` regenerates the token and starts the
listener. Existing Bearer-auth clients (F10+ era) receive 401 on next
request and must refresh from `.niwa/surface.token`.

#### R11: URL contract (locked)

The URL shape **locked at F5 and stable through F13 and beyond**:

```
http://127.0.0.1:<port>/changes/<change-id>#comment-<comment-id>
```

- `<port>` is the kernel-assigned ephemeral port for the instance,
  readable from `.niwa/surface.port` by drill-in callers.
- `<change-id>` is the UUIDv4 from R1.
- `#comment-<comment-id>` is the fragment scheme. F5 has no comments;
  the fragment is reserved DOM addressing F6 will populate. F13's
  polished scroll-to-comment is the consumer.

Externally-visible commitments F5 makes:

- The path prefix `/changes/` is stable.
- The fragment scheme `#comment-<id>` is stable.
- Subdomain/host: always `127.0.0.1` (loopback). Hosted-tier URL
  shape is F16 territory and out of scope here.

#### R12: HTML rendering (read-only at F5)

`GET /changes/<id>` renders:

- **Header:** change ID, state badge, originating sessions, base→head
  ref summary, created/updated timestamps.
- **Body:** the unified diff from `diff.patch`, rendered as
  `<pre>`-wrapped text with file/hunk separators visually distinct.
  No syntax highlighting; no side-by-side view; no file tree
  navigation (all deferred to F13 per D9).
- **Footer:** no buttons. F10 lands the verdict cast button. F6 lands
  comments. F5 ships read-only.

`GET /changes/` renders a flat HTML list of changes with `id`, `state`,
`updated_at`, `head_ref`, and a link to `/changes/<id>`. Sorted
`updated_at` desc. Cleaned changes appear with a `[cleaned]` badge but
are de-emphasized (lower in the list); they retain `state.json` for
forensics but their diff is gone.

### Non-functional

#### NFR1: MCP latency

`niwa_create_change`, `niwa_list_changes`, `niwa_query_change` must hold
p95 < 1s on the reference fleet (per VISION's "Constraints for
downstream design" — sub-second MCP latency target for human-facing
paths). The dominant cost in `niwa_create_change` is `git diff`
invocation; the snapshot strategy (R7) keeps this cost bounded because
the diff is computed once at create time, not repeatedly on read.

#### NFR2: HTTP latency

`GET /changes/<id>` must render in <200ms on the reference fleet for
diffs up to 10k lines. The implementation reads `state.json` +
`diff.patch` from disk and concatenates into an HTML template — no
parsing of the diff is performed (the unified diff is wrapped in
`<pre>` as-is).

#### NFR3: Concurrency safety

Change creation races must be safe. Two concurrent
`niwa_create_change` calls for the same session must not produce two
overlapping `change_id`s. The atomic creation protocol from R1
(`O_CREATE|O_EXCL` placeholder + 5-retry birthday loop) mirrors the
session-lifecycle precedent and is the only acceptable approach.

`transitions.log` writes are protected by per-change `flock` + `fsync`,
matching `.niwa/tasks/<id>/transitions.log` conventions.

`mcp-audit.log` writes are protected by mutex + atomic `O_APPEND` ≤
PIPE_BUF (matches existing v=1 emitter; v=2 inherits the same model).

#### NFR4: Security boundary

- The HTTP listener binds 127.0.0.1 only. Cross-host access is
  structurally impossible.
- `surface.token` (R6) gates mutations only. F5 has no mutation
  endpoints; reads are unauthenticated because any process on the same
  host that can `curl 127.0.0.1` can also `cat .niwa/changes/<id>/`.
- CORS is strict-origin (no `Access-Control-Allow-*` headers). Cross-
  origin browser requests are rejected by the browser; the server
  emits no permissive headers.
- All `.niwa/changes/<id>/` files are `0o600`; the directory is
  `0o700`. Token, port file, lock file: also `0o600`.
- The token is printed to stderr as **first 8 chars only**
  (fingerprint). The full token never appears in logs or stderr.
- HTML responses do not echo user-controlled strings without escaping.
  Diff content is rendered via `html.EscapeString` before wrapping in
  `<pre>`. This is mechanical content (`git diff` output), not
  trusted, and may contain `<script>` tags from the change being
  reviewed.

#### NFR5: Test coverage

- Unit tests adjacent (`_test.go`) for `internal/mcp/handlers_change.go`
  and `internal/web/` (using `httptest.Server`). Coverage targets:
  - MCP tool I/O happy path
  - Atomic creation race (concurrent `niwa_create_change` for same
    session)
  - Base-ref discovery fallback chain (all five precedence levels)
  - Diff snapshot capture
  - GC sweep behavior (boundary at exactly `gc_abandon_days`)
  - Audit log v=1/v=2 backward-compat reader
- New `test/functional/features/review-surface.feature` E2E in Gherkin,
  tagged `@critical` per CLAUDE.md (`@critical` required for user-
  facing CLI changes; `niwa surface serve` is user-facing).
  Scenarios:
  - Agent creates a change; Dan opens browser; sees diff
  - Agent creates a change; HTTP listener not running; URL still
    composes from `surface.port` once started
  - GC moves abandoned change to `cleaned`; web index reflects new
    state

## Acceptance criteria

Each `- [ ]` from issue #381 maps to a PRD section.

| Issue criterion | PRD coverage |
|---|---|
| PRD published at `docs/prds/` in the vision repo | This document. |
| Data model: change object with `id` (UUIDv4), lifecycle state machine, `originating_sessions: [session_id]` plural, role-keyed metadata, verdict slot | [R1](#r1-change-object-schema-v1-commit-verbatim). |
| On-disk format: `.niwa/changes/<change-id>/state.json` orthogonal to `.niwa/tasks/` | [R2](#r2-on-disk-layout). |
| MCP tools: `niwa_create_change`, `niwa_list_changes`, `niwa_query_change` with schema | [R3](#r3-mcp-tools-commit-verbatim). |
| `change_ready` event fired into `mcp-audit.log` when a change becomes reviewable; the issue's "routed through stop-hook + `coding-tools` Telegram path" clause is reconciled with the actual plugin mechanics below | [R5](#r5-event-taxonomy-f5-emits-four) for the F5 emit. The routing clause is mechanistically inaccurate as written (the plugin does not tail audit logs); F5 emits the event correctly into the audit substrate so any future bridge can consume it. Bridge wiring deferred to a follow-up spec — see [Reconciliation: Telegram bridge boundary](#reconciliation-telegram-bridge-boundary) and [D1](#out-of-scope-deferrals). |
| Web server in a new `internal/web/` package; `niwa surface serve` boots it | [R6](#r6-http-surface), [R10](#r10-surface-lifecycle). |
| Per-instance authentication non-optional: 0600 token in `.niwa/surface.token` required for any verdict-affecting endpoint; strict CORS; token rotation on `--rotate-token` | [R6](#r6-http-surface), [R10](#r10-surface-lifecycle), [NFR4](#nfr4-security-boundary). |
| URL contract locked: `/changes/<id>#comment-<comment-id>` fragment scheme committed | [R11](#r11-url-contract-locked). |
| Read-only render at this stage: diff + change metadata; no comments, no verdict, no interaction | [R12](#r12-html-rendering-read-only-at-f5). |
| Events emitted: `review_surface_opened` and `change_engaged` | [R5](#r5-event-taxonomy-f5-emits-four). |
| Change cleanup/GC: abandoned changes after N days (configurable) move to `cleaned` state; `change_cleaned` audit event so cleanup is observable | [R9](#r9-gc--cleanup), [R5](#r5-event-taxonomy-f5-emits-four). |
| Reviewed and approved before F6 PRD work begins | Jury review in this PRD's Phase 4; merge of this PR is the gate on F6. |

## Open items (committed answers)

### Open item 1: state machine entry to `in-review`

**Question:** What event transitions a change from `pending` to
`in-review`?

**Committed answer:** In the F5 era (no comments yet), the **first
`GET /changes/<id>` hit** triggers `pending → in-review`. The F6 PRD
may revisit and let "first comment posted" be the trigger; whichever
fires first wins.

**Rationale:** F5 has no comments; the only observable engagement
signal at the F5 era is HTTP drill-in. Using surface open is consistent
with the `change_engaged` event semantics (R5) and produces a clear
state transition the success criteria can measure (voluntary opens
becoming in-review changes). F6 layering comments-as-trigger is
additive and does not migrate stored state.

**Alternative considered:** `in-review` triggered only on first
comment (deferred to F6). Rejected because it leaves changes in
`pending` indefinitely during the F5 era, masking the difference
between never-opened and actively-being-reviewed. Counts of `pending`
changes need to mean "nobody's looked at this yet."

### Open item 2: multi-instance broadcast semantics

**Question:** When multiple niwa instances on the same machine emit
`change_ready` events, what reaches the operator?

**Committed answer (F5 emit semantics):** Every instance emits
independently into its own `.niwa/mcp-audit.log`. The single-user
reference fleet runs 10 instances across two roots
(`~/dev/niwaw/tsuku/` and `~/dev/niwaw/cs/`); each instance's
`change_ready` is an independent event in its own substrate. F5 makes
no central registry, no cross-instance dedup, and no per-machine
aggregator. Per-instance scope is the F5 contract.

The eventual bridge spec (D1) consumes these per-instance event
streams. F5's emit semantics support the synthesis recommendation
"broadcast to all `allowFrom`" in the sense that all instances emit
events the bridge can consume; whether the bridge fans out to one chat
or many is the bridge spec's call, not F5's. The synthesis
recommendation is honored at the emit layer (every instance emits);
F5 does not constrain bridge routing because the bridge transport is
itself undecided (D1).

**Rationale:** Per-instance scope matches the rest of niwa's
substrate (`.niwa/mcp-audit.log`, `.niwa/tasks/`, `.niwa/sessions/`
are all per-instance). Aggregating across instances at the F5 layer
would require a cross-instance coordinator that does not exist;
deferring aggregation to the bridge spec keeps F5 inside its scope
while supporting the synthesis's intended outcome at the layer
designed to do it.

### Open item 3: force-push semantics

**Question:** If the session branch is force-pushed AFTER a change is
created, what happens to the snapshotted diff?

**Committed answer:** The snapshot is **immutable**. `diff.patch` is
not rewritten on force-push, rebase, amend, or session destruction.
The F5 surface continues to render the captured diff; the diff state
in the browser reflects the change at create time. F5 ships no drift
detection and no drift events — F7 (line-anchoring with port-forward
on amendment) owns drift detection, drift events, and re-anchoring
logic over the immutable snapshot F5 captures.

**Rationale:** Snapshot-at-create is the only durable strategy across
force-push, base-ref drift, and post-session-destroy. Re-computing
the diff on each render fails when the worktree no longer exists.
F7 ports forward from the snapshot, so the snapshot must be the
ground truth. Drift detection and the user-visible drift signal
belong with F7's anchoring work, not with F5's substrate ship — F5
introducing drift events would pre-commit semantics F7 owns.

**Alternative considered:** Re-snapshot on each render if the worktree
exists. Rejected because it makes diffs inconsistent across views
(the diff Dan sees in his browser changes if the agent amends mid-
view) and breaks line-anchored comments in F7 era.

### Open item 4: verdict-affecting endpoint at F5

**Question:** F5 itself ships no verdict endpoint; how does F10 compose
auth on the surface F5 sets up?

**Committed answer:** F5 **locks the token-required-for-mutations
contract** in R6 and R10. F5 ships no mutation endpoints, so no
endpoint requires auth in the F5 era. The token exists and is
enforced for any endpoint declared `requires_mutation`; F10 layers
`POST /changes/<id>/verdict` (and other mutation endpoints) on this
contract without inventing the auth layer.

**Rationale:** Locking the contract once at F5 keeps F10's surface area
minimal — F10 adds endpoint handlers, not security infrastructure.
The token, the Bearer-auth header, the 0o600 storage, and the rotation
flag are all in place before F10 begins.

### Open item 5: first-time UX for `surface.token`

**Question:** How does `surface.token` come into existence the first
time `niwa surface serve` is invoked?

**Committed answer:** On first boot, if `.niwa/surface.token` is
absent, generate a UUIDv4, write it atomically (tmp+rename, 0o600),
and print to stderr:

```
niwa surface: serving on http://127.0.0.1:<port>
niwa surface: token file: .niwa/surface.token (mode 0600)
```

The token contents are never printed or logged in any form.
`--rotate-token` regenerates the file. Existing Bearer clients (none
at F5; F10+ clients) receive 401 on next request and must refresh from
`.niwa/surface.token`.

**Rationale:** First-time UX is observable but minimal. F5 ships no
clients that need to confirm which token is active (F10+ era handles
operational ergonomics around token rotation as it lands the first
mutation endpoints). Keeping the print minimal avoids speculative UX
the F10 PRD would have to override.

### Open item 6: diff size cap

**Question:** What is the maximum captured diff size, and what
happens above it?

**Committed answer:** 4 MiB (4,194,304 bytes). Above the cap, the
diff is truncated at the byte boundary and a single trailer line is
appended (`--- diff truncated at 4 MiB; full diff available via 'git
-C <worktree> diff <base>..<head>' ---`). The cap exists to bound
per-change disk usage; downstream features (F11 CLI, F13 polished
view) that need the full diff invoke git directly against the
worktree. Cap is fixed (not configurable) at F5 — F11/F13 can revisit
if real changes show 4 MiB to be insufficient.

**Rationale:** 4 MiB covers ~80,000 lines of typical diff output;
larger captures usually indicate auto-generated content (lockfiles,
vendored dependencies) where line-by-line review is the wrong shape
anyway. Bounded disk usage matters for the `.niwa/changes/` directory
to remain manageable across hundreds of changes.

## Reconciliation: Telegram bridge boundary

The roadmap (F5 section, ROADMAP-niwa-collab-surface.md) describes the
`change_ready` flow as:

> `change_ready` event: fired into `mcp-audit.log` when a change
> becomes reviewable; routed through niwa's stop-hook + `coding-tools`
> Telegram path

The substrate research established that this description is
mechanistically inaccurate (private/coding-tools research, cited in
`wip/explore_f5_findings.md`):

- The `coding-tools` Telegram plugin does **not** tail `mcp-audit.log`.
- The plugin is a standalone MCP server that long-polls Telegram's
  `getUpdates` API and emits `notifications/claude/channel` events
  upward to a connected Claude Code session.
- Niwa's stop-hook does not talk to Telegram either; it writes
  `last_progress.at` via `niwa mesh report-progress`.

There is therefore no `mcp-audit.log` → `coding-tools` → Telegram
chain today. The audit log is observability substrate, not
notification transport.

**F5's commitment:** F5 emits `change_ready` correctly into the audit
substrate (R5) so an eventual bridge has a stable, well-shaped event
to consume. The plugin already supports a proven pattern for event-
to-Telegram-to-callback shape via
`notifications/claude/channel/permission_request` with inline-keyboard
URL/callback buttons, so the bridge has a clear consumer surface.

**F5's deferral:** the niwa↔coding-tools notification bridge spec is
**out of scope**. The bridge is named as a follow-up
(`docs/designs/DESIGN-niwa-coding-tools-bridge.md` or similar; not yet
filed) that decides:

- Transport mechanism (audit-log tail vs. event subscription vs. MCP
  push)
- Routing semantics (per-instance vs. per-user vs. per-session)
- Failure semantics (offline-tolerant queueing, dedup, retry policy)
- Auth model between niwa and the bridge

F5 documents the boundary explicitly — the alternative (writing the
PRD as if the audit-log path worked) would harden a fiction into a
contract and block the bridge spec from picking the right transport.

## Out of scope (deferrals)

| ID | Item | Where it lives | Rationale |
|----|------|----------------|-----------|
| D1 | Telegram bridge wiring | Follow-up spec: niwa↔coding-tools notification bridge | Plugin does not tail audit logs; transport is unresolved. F5 emits the event; the bridge spec wires the transport. |
| D2 | Comments primitive and rendering | F6 PRD ([#382](https://github.com/tsukumogami/vision/issues/382)) | F6 owns `comments.ndjson` storage + `niwa_post_review` batch tool + comment rendering. |
| D3 | Verdict cast UI and state transitions | F10 PRD ([#386](https://github.com/tsukumogami/vision/issues/386)) | F10 owns the verdict state machine, MCP tool, notification flow, dampening. F5 reserves the slot. |
| D4 | Line anchoring with port-forward | F7 design doc ([#383](https://github.com/tsukumogami/vision/issues/383)) | Anchor tuple format + four-rewrite-operation behavior is the hardest implementation question in the comment substrate. Spike-worthy. |
| D5 | Threading and per-thread resolution | F8 PRD ([#384](https://github.com/tsukumogami/vision/issues/384)) | `in_reply_to`, `resolved` state, `niwa_resolve_thread` MCP tool. |
| D6 | @-mentions and inbox routing | F9 PRD ([#385](https://github.com/tsukumogami/vision/issues/385)) | Regex parsing, SessionEntry resolution, fan-out via `niwa_send_message`. |
| D7 | CLI review surface | F11 PRD ([#387](https://github.com/tsukumogami/vision/issues/387)) | `niwa change view <id>`, `niwa change comment`, `niwa change verdict`. Launch-blocking. |
| D8 | TUI review surface | F12 PRD ([#388](https://github.com/tsukumogami/vision/issues/388)) | Split-pane terminal UI. Post-launch. |
| D9 | Polished web deep-link UX (scroll-to-comment, side-by-side diff, syntax highlighting, file tree navigation, mobile layout, "next unresolved" navigation) | F13 PRD ([#389](https://github.com/tsukumogami/vision/issues/389)) | F13 polishes ergonomics over the URL contract F5 locks. F5 ships only `<pre>`-wrapped unified diff for the walking-skeleton commitment. Post-launch. |
| D10 | niwa↔koto linkage fields | F14 PRD ([#390](https://github.com/tsukumogami/vision/issues/390)) | `koto_session_id` field + cross-repo coordination with koto F4. |
| D11 | Hosted niwa with session memory | F16 spike ([#392](https://github.com/tsukumogami/vision/issues/392)) | Gated on launch-post external interest per VISION D1. |

## Decisions (summary table)

Adjudicated by the F5 exploration synthesis
(`wip/explore_f5_findings.md`). Each decision is the substrate-research
recommendation validated against the cross-cut findings and applied to
the relevant PRD requirement.

| # | Decision | Choice | Requirement |
|---|----------|--------|-------------|
| 1 | `change_id` format | UUIDv4 | R1 |
| 2 | On-disk layout | `.lock`, `state.json`, `transitions.log`, `diff.patch` (mirrors `.niwa/tasks/`) | R2 |
| 3 | `ChangeState` v=1 fields | Verbatim schema in R1; verdict slot reserved for F10 | R1 |
| 4 | Atomic creation | `O_CREATE\|O_EXCL` placeholder + 5-retry birthday loop | R1 (NFR3) |
| 5 | Event emission targets | Both per-change `transitions.log` AND `mcp-audit.log` v=2 | R5 |
| 6 | Audit log v=2 schema | `kind` discriminator + `payload` slot; backward-compatible v=1 reader | R4 |
| 7 | Field naming | snake_case; `at` (RFC3339Nano UTC); no `ts`, no `taskId` | R1, R4 |
| 8 | Event payload size budget | ≤2KB; emitter downgrades over-budget entries | R4 |
| 9 | Emitter idempotency | `change_ready` idempotent keyed by `change_id`; surface-hit events not deduped | R5 |
| 10 | Routing library | stdlib `net/http` (Go 1.22+) | R6 |
| 11 | Bind address | 127.0.0.1 only | R6 |
| 12 | Port allocation | Ephemeral via `net.Listen("tcp", "127.0.0.1:0")`; CLI override `--port N`; advertise in `.niwa/surface.port` | R6, R10 |
| 13 | Auth token format | UUIDv4 at `.niwa/surface.token` (0o600); auto-generate on first boot | R6, R10 |
| 14 | Auth header | `Authorization: Bearer <token>` | R6 |
| 15 | CORS | Reject cross-origin entirely (no `Access-Control-Allow-*`) | R6, NFR4 |
| 16 | Shutdown | Context-cancel from MCP dispatch → `http.Server.Shutdown(ctx)`; no signal handlers in `internal/web/` | R6 |
| 17 | URL contract | `http://127.0.0.1:<port>/changes/<id>#comment-<id>`; fragment locked | R11 |
| 18 | Index endpoint | `GET /` → 302 `/changes/`; `GET /changes/` HTML index | R6, R12 |
| 19 | Per-change endpoint | `GET /changes/<id>` HTML diff + metadata (read-only) | R6, R12 |
| 20 | Static assets | Inlined CSS; no asset endpoint | R12 |
| 21 | Diff capture | Snapshot to `diff.patch` at create time; immutable | R7 |
| 22 | Base-ref discovery | `origin/HEAD` → `origin/main` → `origin/master` → `main` → `master` | R8 |
| 23 | Diff format | Unified diff via `git -C <worktree> diff <base>..<head>` | R7 |
| 24 | GC trigger | On boot + 6h interval (configurable) | R9 |
| 25 | Abandonment threshold | 14 days default, per-workspace via `workspace.toml` `[changes] gc_abandon_days` | R9 |
| 26 | Cleanup action | Move to `cleaned`; delete `diff.patch`; emit `change_cleaned`; keep `state.json` for forensics | R9 |
| 27 | Unit test scope | `_test.go` adjacency + `httptest.Server` | NFR5 |
| 28 | E2E test | New `@critical` Gherkin feature `review-surface.feature` | NFR5 |
| 29 | Coverage scope | MCP tools, HTTP handlers, GC, atomic creation race, base-ref fallback | NFR5 |
| 30 | Package layout | `internal/web/` (domain-neutral) | R6 |
| 31 | Subpackages | Implementation-driven; not pre-committed | R6 |
| 32 | MCP-tool handlers | `internal/mcp/handlers_change.go` (matches `handlers_task.go` / `handlers_session.go`) | R3 |

## Dependencies

- F1 (MCP coordination primitives) — Done. `internal/mcp/server.go`
  dispatcher, `toolDef` registry, audit-log emission, `_niwa_note` /
  `wrapDelegateBody` prompt-injection wrapping all exist.
- F2 (Session worktrees and `niwa session attach`) — Done. Worktree
  branches at `session/<sid>` are the input to change creation.

No vision-repo issue dependencies. This is the foundational PRD for
the collab surface.

## Downstream PRDs (consumers of this contract)

- F6 PRD (#382): comment primitive on `.niwa/changes/<id>/comments.ndjson`
- F7 design (#383): line-anchoring port-forward from `diff.patch` snapshots
- F8 PRD (#384): threading on F6's comment shape
- F9 PRD (#385): @-mention routing on F6's comment shape
- F10 PRD (#386): verdict cast on R6's auth contract + R1's verdict slot
- F11 PRD (#387): CLI surface composing on R1 + R3
- F12 PRD (#388): TUI surface composing on R1 + R3 + F11's CLI MCP shape
- F13 PRD (#389): polished deep-link UX on R11's URL contract
- F14 PRD (#390): koto linkage in R1's `metadata` slot or a new field

Getting R1 (schema), R3 (MCP I/O), R6 (auth), and R11 (URL) right at
F5 is the contract this entire downstream chain encodes against.
