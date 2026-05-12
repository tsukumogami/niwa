# F5 Exploration Synthesis — Coordinator-Adjudicated Handoff to PRD

**Issue:** [tsukumogami/vision#381](https://github.com/tsukumogami/vision/issues/381) — F5: Change-as-reviewable primitive and basic web render
**Roadmap:** [ROADMAP-niwa-collab-surface](https://github.com/tsukumogami/vision/blob/main/docs/roadmaps/ROADMAP-niwa-collab-surface.md)
**Upstream VISION:** [VISION-niwa-collab-surface](https://github.com/tsukumogami/vision/blob/main/docs/visions/VISION-niwa-collab-surface.md)
**Date:** 2026-05-12
**Coordinator:** Claude (Opus 4.7), routing decisions per user-confirmed contract: agents recommend, I adjudicate.

---

## Why this doc exists

F5's acceptance criteria pin down a lot but require schema/contract commitments that downstream features (F6–F15) encode against. Getting the data model or URL contract wrong forces migration of stored review data. This synthesis collects:

1. The substrate facts the niwa agent surfaced (the world as it actually is in niwa v0.10.x).
2. The reality check the coding-tools agent surfaced (the world as the roadmap *describes* vs. the world as it *is*).
3. Coordinator-adjudicated decisions on every schema/contract question the PRD must commit on.
4. The boundary lines: what F5 owns vs. what is out-of-scope deferral.

The vision agent uses this as the input package for `/shirabe:prd`.

---

## Substrate snapshot (from niwa research)

Full research: `/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/wip/research/explore_f5_niwa-substrate.md` (will be GC'd; key facts captured here).

- **`internal/` packages:** 12 today. No naming collisions for `web`, `server`, `http`, `review`, `surface`. New `internal/web/` slots cleanly.
- **`.niwa/tasks/<task-id>/`** holds `.lock`, `envelope.json`, `state.json`, `transitions.log`, `worker.mcp.json`. Task IDs are UUIDv4.
- **`.niwa/sessions/<sid>.json`** holds session lifecycle state. Session IDs are 8-char hex (`^[0-9a-f]{8}$`). Branch is deterministic: `session/<sid>`.
- **MCP server:** native protocol over stdio, no SDK. 15 tools registered as `toolDef` entries in `server.go:280-475` and dispatched via switch in `server.go:477-568`. Domain-grouped handlers: `handlers_task.go`, `handlers_session.go`. Audit emission is centralized in `dispatch()` — **new F5 MCP tools get audit logging free**.
- **`mcp-audit.log`:** NDJSON, instance-root, v=1 schema = `{v, at, role, task_id, tool, arg_keys, ok, error_code}`. RFC3339Nano UTC. Mutex + `O_APPEND ≤ PIPE_BUF` for atomicity. No flock, no fsync.
- **`transitions.log`** (per-task): NDJSON, kind+from+to+at, **kind is a free string with no registry** — `change_ready` etc. are purely additive. Per-task flock + fsync.
- **No HTTP listener exists today.** Niwa is greenfield for inbound HTTP.
- **No bearer-token auth exists.** Existing auth is process-identity (PID + env match). `surface.token` is greenfield.
- **No CORS handling anywhere.** Greenfield.
- **Session→branch is deterministic, but base-ref discovery is missing.** First feature to need `git merge-base` discovery.
- **Stop-hook does NOT talk to Telegram in niwa.** It writes `last_progress.at` via `niwa mesh report-progress`. The Telegram bridge is owned by an external process.
- **Concurrent-creation precedent:** `O_CREATE|O_EXCL` placeholder + 5-retry birthday loop (`session_lifecycle.go:140-159`). F5 must mirror this for `.niwa/changes/<id>/`.
- **Tests:** `_test.go` adjacency, table-driven, no golden files. End-to-end via godog Gherkin in `test/functional/features/`. `@critical` tag required for user-facing CLI changes.
- **Foot-guns:** session_id format ≠ task_id format (two ID conventions live side by side); no central path resolver (355+ literal `".niwa"` strings); audit logs have **different** atomicity models; redaction discipline is a security boundary (arg keys only, never values).

---

## The cross-cutting reality check (from coding-tools research)

Full research: `/home/dgazineu/dev/niwaw/tsuku/tsuku-4/private/coding-tools/wip/research/explore_f5_coding-tools-telegram.md`.

**The roadmap's stated transport mechanism is wrong about how the Telegram plugin actually works.** The roadmap says:

> `change_ready` event: fired into `mcp-audit.log` when a change becomes reviewable; routed through niwa's stop-hook + `coding-tools` Telegram path

Reality check from code: the Telegram plugin does NOT tail `mcp-audit.log`. It's a standalone MCP server that long-polls Telegram's `getUpdates` API and emits `notifications/claude/channel` events upward to a connected Claude Code session. There is no "your plugin picks it up" path today. The audit log is observability substrate, not notification transport.

**The plugin already supports a proven pattern** for the event-to-Telegram-to-callback shape F5 (one-way) and F10 (round-trip with cast buttons) need: `notifications/claude/channel/permission_request` with inline-keyboard URL/callback buttons.

**Audit log schema doesn't fit `change_ready` natively.** v=1 has no `event` discriminator and no payload slot. Either bump to v=2 or add a sibling stream. Field convention is snake_case; timestamp field is `at` (not `ts`) in RFC3339Nano UTC. Payload budget: ≤2KB for PIPE_BUF safety. No throttling/dedup anywhere — the emitter must be idempotent keyed by `change_id`.

**Coordinator's decision on the discrepancy:** the F5 PRD will scope the **emit** side (audit + per-change log + URL contract) and **defer the bridge wiring** to a follow-up spec ("niwa↔coding-tools notification bridge"). The PRD will *not* claim mcp-audit.log is the transport to Telegram — that is wrong and would harden a fiction into a contract. The PRD documents the boundary and names the follow-up.

---

## Coordinator-adjudicated decisions

These are the schema/contract commitments the PRD MUST encode. Each is the recommendation from substrate research, validated against the cross-cut findings.

### Data plane

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 1 | `change_id` format | **UUIDv4** | Matches task IDs; established `crypto/rand` precedent; entropy matters more than display length for durable artifacts. Reject 8-char hex (sessions) — sessions are short-lived addressable entities, changes are durable. |
| 2 | `.niwa/changes/<id>/` layout | `.lock`, `state.json`, `transitions.log`, `diff.patch` | Mirrors `.niwa/tasks/<id>/`. F6 adds `comments.ndjson` to the same dir later. All 0o600. |
| 3 | `ChangeState` v=1 fields | See schema below | Minimal viable; verdict slot reserved (populated by F10); plural `originating_sessions` per VISION's mesh-co-authorship language. |
| 4 | Atomic creation | `O_CREATE\|O_EXCL` placeholder + 5-retry birthday loop | Mirrors `newSessionLifecycleID` (session_lifecycle.go:140-159). Reject TOCTOU `os.Stat`+`MkdirAll`. |
| 5 | Event emission location | **Both**: `.niwa/changes/<id>/transitions.log` AND `.niwa/mcp-audit.log` | Per-change for change-scoped reads; instance-wide audit for observability + the eventual bridge consumer surface. One extra append per event. |
| 6 | Audit log v=2 schema bump | Yes; backward-compatible reader | v=2 adds `kind` discriminator + `payload` slot. v=1 readers continue to work (treat absence of `kind` as `kind="tool_call"`). |
| 7 | Field naming + timestamps | snake_case; `at` field; RFC3339Nano UTC | Matches `audit.go` json tags + plugin meta keys. Reject `ts`, `taskId`, etc. |
| 8 | Event payload size budget | ≤2KB | PIPE_BUF (~4096 bytes) atomic-append safety on Linux O_APPEND. Reject above-budget payloads at the emitter. |
| 9 | Emitter idempotency | F5 emitter idempotent keyed by `change_id` | No throttling/dedup downstream; bouncy emitter would spam any bridge that arrives. |

### `ChangeState` v=1 schema (commit verbatim)

```json
{
  "v": 1,
  "id": "<uuidv4>",
  "state": "pending" | "in-review" | "verdict-cast" | "cleaned",
  "originating_sessions": ["<sid>"],
  "originating_tasks": ["<task-uuid>"],
  "created_at": "<rfc3339nano>",
  "updated_at": "<rfc3339nano>",
  "base_ref": "<commit-sha>",
  "head_ref": "<commit-sha>",
  "branch": "session/<sid>",
  "worktree_path": "<abs path>",
  "diff_path": "diff.patch",
  "verdict": null,
  "metadata": {}
}
```

`verdict` stays `null` for F5 (F10 populates it). `metadata` is the role-keyed extension slot for F6+ (per roadmap "role-keyed metadata"). State machine validation in code:
- `pending` → `in-review` (first surface open or first comment in F6 era)
- `in-review` → `verdict-cast` (F10)
- any → `cleaned` (GC after N days, configurable)

### Event taxonomy (F5 emits four)

| Event | When | Payload required |
|-------|------|------------------|
| `change_ready` | `niwa_create_change` completes | `change_id`, `url`, `originating_sessions`, `base_ref`, `head_ref` |
| `review_surface_opened` | HTTP GET `/` or `/changes/` (index) hit | `surface_url`, no per-change id |
| `change_engaged` | HTTP GET `/changes/<id>` hit | `change_id`, `surface_url` |
| `change_cleaned` | GC moves change to `cleaned` state | `change_id`, `reason` (e.g., `"abandoned_after_n_days"`), `n_days` |

All emitted via the same `appendEventLog` helper to both per-change `transitions.log` and `mcp-audit.log` v=2.

### MCP tools (commit verbatim names + shapes)

#### `niwa_create_change`
```json
// Input
{
  "session_id": "<8-hex>",
  "base_ref_hint": "<optional ref>",   // for explicit override of base-branch discovery
  "metadata": {}                       // optional role-keyed extension
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

#### `niwa_list_changes`
```json
// Input
{
  "state": "pending|in-review|verdict-cast|cleaned",    // optional filter
  "session_id": "<8-hex>"                                // optional filter
}
// Output
{
  "changes": [
    { "id": "<uuidv4>", "state": "<state>", "created_at": "<at>", "url": "<url>", "head_ref": "<sha>", "branch": "<branch>" },
    ...
  ]
}
```

#### `niwa_query_change`
```json
// Input
{ "change_id": "<uuidv4>" }
// Output: the full ChangeState (above) + recent transitions tail (last 20)
```

### HTTP surface

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 10 | Routing library | **stdlib `net/http`** | Go 1.22+ method+path patterns sufficient; matches niwa's thin-dep philosophy. Reject chi/gorilla/echo. |
| 11 | Listener binding | **127.0.0.1 only** | Same-machine threat model. No 0.0.0.0. |
| 12 | Port allocation | **Ephemeral via `net.Listen("tcp", "127.0.0.1:0")`** | Kernel-assigned. Write actual port to `.niwa/surface.port` atomically (tmp+rename, 0o600). CLI override: `niwa surface serve --port N`. |
| 13 | Auth | **Per-instance UUIDv4 token in `.niwa/surface.token`** | crypto/rand precedent, 122 bits entropy, validates with existing UUID regex idiom. 0o600. Required for ANY verdict-affecting endpoint (none in F5 yet, but lock the contract). Rotation via `niwa surface serve --rotate-token`. |
| 14 | Auth header | `Authorization: Bearer <token>` | Matches outbound convention in `internal/github/client.go`. Reject all other forms. |
| 15 | CORS | **Reject cross-origin entirely** | No `Access-Control-Allow-*` headers. Same-origin enforced. F16 (hosted) can extend. |
| 16 | Shutdown | **Context-cancel from MCP dispatch → `http.Server.Shutdown(ctx)`** | Run HTTP listener in goroutine spawned from MCP startup; cancel context on MCP exit. **No signal handlers in `internal/web/`**. |
| 17 | URL contract | `http://127.0.0.1:<port>/changes/<change_id>` with fragment `#comment-<comment-id>` | Locked at F5 per roadmap. F13's polished scroll-to-comment depends on this not breaking. |
| 18 | Index endpoint | `GET /` redirects to `/changes/`; `GET /changes/` lists changes (HTML) | Minimal but real. Auth not required for read-only views in F5 (no verdict mutation exists yet); future endpoints that mutate require token. |
| 19 | Per-change endpoint | `GET /changes/<id>` renders diff + metadata (HTML) | Read-only. Server reads `diff.patch` from disk. |
| 20 | Static assets | Inline CSS in the HTML page; no asset endpoint | Keep F5 footprint minimal; F11/F12/F13 can layer on. |

### Diff capture

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 21 | Capture strategy | **Snapshot to `diff.patch` at `niwa_create_change` time** | Durable across force-push, base-ref drift, post-session-destroy. Cost: one disk write at create time. |
| 22 | Base-ref discovery | `git symbolic-ref refs/remotes/origin/HEAD` → `origin/main` → `origin/master` → `main` → `master` | Document explicitly; warn if falls past `origin/HEAD`. F5 is the first feature that needs this — establishes the convention. |
| 23 | Diff format | Unified diff via `git -C <worktree> diff <base>..<head>` | Standard format; renderable by both web and future CLI. |

### GC / cleanup

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 24 | GC trigger | On `niwa surface serve` boot AND on a configurable interval thereafter (default 6h) | No new daemon needed. |
| 25 | Abandonment threshold | Default 14 days configurable per workspace via `workspace.toml` `[changes] gc_abandon_days = 14` | "Abandoned" = state stays `pending` for N days without state advance. |
| 26 | Cleanup action | Move to `cleaned`; delete `diff.patch`; emit `change_cleaned` event; keep `state.json` (for forensics) | Observable, not silent. |

### Testing

| # | Decision | Choice | Why |
|---|----------|--------|-----|
| 27 | Unit tests | `_test.go` adjacent; `httptest.Server` for handlers | Established convention. |
| 28 | E2E tests | New `test/functional/features/review-surface.feature` with `@critical` tag | User-facing CLI change; CLAUDE.md mandates `@critical`. |
| 29 | Coverage scope | MCP tools, HTTP handlers, GC behavior, atomic creation race, base-ref fallback chain | The non-obvious ones. |

### Package layout

| # | Decision | Choice |
|---|----------|--------|
| 30 | Package name | `internal/web/` (domain-neutral) |
| 31 | Subpackages | `internal/web/server`, `internal/web/handlers`, `internal/web/render` as needed (not pre-committed; let implementation drive) |
| 32 | MCP-tool handlers | New `internal/mcp/handlers_change.go` (matches `handlers_task.go`/`handlers_session.go` convention) |

### Out-of-scope deferral (PRD documents explicitly)

| # | Item | Why deferred | Where it goes |
|---|------|--------------|---------------|
| D1 | Telegram bridge wiring | The plugin doesn't tail audit logs; the roadmap's "fired into mcp-audit.log → coding-tools picks it up" is mechanistically wrong. F5 emits the event correctly; the bridge needs its own spec. | Follow-up: "niwa↔coding-tools notification bridge" exploration. |
| D2 | Comments rendering | F6 owns the comment primitive | F6 PRD (#382) |
| D3 | Verdict slot UI | F10 owns the verdict gate | F10 PRD (#386) |
| D4 | Line-anchoring | F7 | F7 design doc (#383) |
| D5 | Threading | F8 | F8 PRD (#384) |
| D6 | @Mentions | F9 | F9 PRD (#385) |
| D7 | CLI surface | F11 | F11 PRD (#387) |
| D8 | TUI | F12 | F12 PRD (#388) |
| D9 | Polished deep-link UX | F13 | F13 PRD (#389) |
| D10 | koto session linkage | F14 | F14 PRD (#390) |
| D11 | Hosted tier | F16 (gated on launch-post external interest) | F16 spike (#392) |

---

## Open items the PRD MUST address explicitly

These are not deferred — they are decisions F5 must make but where reasonable people could land differently. Each is flagged for explicit PRD prose.

1. **State machine entry to `in-review`.** Is it the first surface open? First comment post (which is F6)? The PRD must commit. **Recommendation:** first surface open in F5 era (no comments yet); F6 PRD revisits.
2. **Multi-instance Telegram broadcast.** The plugin is per-user; mcp-audit.log is per-instance. Does each instance's `change_ready` reach the chat, or just one? **Recommendation:** broadcast to all `allowFrom`. Single-user model holds at F5.
3. **Force-push semantics.** If the session branch is force-pushed AFTER a change is created, what happens to the snapshotted diff? **Recommendation:** snapshot is immutable; emit `change_diff_drift` event for observability; F7 handles port-forward on amendment (downstream).
4. **Verdict-affecting endpoint at F5.** F5 itself ships no verdict endpoint, but the token contract must be defined so F10 can compose. **Recommendation:** lock the token-required-for-mutations contract in F5; F10 layers the actual endpoints on it.
5. **First-time UX for surface.token.** Token is created on first `niwa surface serve` boot, written to `.niwa/surface.token` at 0o600. CLI tools (and future bridges) read it. **Recommendation:** generate on first boot if absent; print path + first 8 chars to stderr; `--rotate-token` regenerates.

---

## Source materials

The vision agent should consult these directly:

- Issue body: https://github.com/tsukumogami/vision/issues/381 (acceptance criteria are the contract the PRD must satisfy)
- Roadmap: https://github.com/tsukumogami/vision/blob/main/docs/roadmaps/ROADMAP-niwa-collab-surface.md (especially F5 section + Sequencing Rationale)
- VISION: https://github.com/tsukumogami/vision/blob/main/docs/visions/VISION-niwa-collab-surface.md (especially "Resource Implications → Constraints for downstream design" and "Success Criteria")
- Niwa substrate research (full): `public/niwa/wip/research/explore_f5_niwa-substrate.md` (read-only, will be GC'd; key facts captured above)
- Coding-tools Telegram research (full): `private/coding-tools/wip/research/explore_f5_coding-tools-telegram.md` (read-only, will be GC'd; key facts captured above)
- Adjacent codebase context: `public/niwa/internal/mcp/audit.go`, `public/niwa/internal/mcp/server.go`, `public/niwa/internal/mcp/session_lifecycle.go`, `public/niwa/internal/mcp/taskstore.go`

---

## PRD writing brief (for the vision agent)

The PRD must:

1. State the problem F5 solves (the missing change primitive + the missing web surface that downstream features compose on).
2. List user-visible behaviors observable end-to-end after F5 ships (the walking-skeleton criterion: "an agent creates a change, Dan opens the browser, sees the diff").
3. Commit verbatim to: `ChangeState` v=1 schema, audit log v=2 schema with `kind` discriminator, event taxonomy (4 events with payload schemas), MCP tool I/O shapes, URL contract, auth contract, GC behavior.
4. Reconcile the roadmap's stated Telegram mechanism with reality (D1 deferral, explicit boundary statement).
5. Spell out the 5 open items above with explicit prose answers.
6. Cite the niwa substrate research for "why this shape, not that one" on each schema choice.
7. Honor the constraints in the VISION's "Constraints for downstream design": sub-second MCP latency target (p95 < 1s); verdict gate sits between turns, not within them; agent-side verdict consumption via inbox (deferred to F10).
8. Land the acceptance criteria from issue #381 cleanly — each `- [ ]` item in the issue must map to a PRD section or be marked as deferred (with rationale).

The PRD lives at `docs/prds/PRD-niwa-change-primitive.md` (or whatever the shirabe PRD skill's naming convention dictates). The vision repo is **private**, so internal rationale and competitor references are allowed.
