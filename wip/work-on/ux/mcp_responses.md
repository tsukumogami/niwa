# niwa MCP tool surface — response-shape audit

Audit of the 14 registered niwa MCP tools, their response shapes, and how the
mesh-reliability design's new fields/codes line up with existing conventions.
All citations are file:line into
`/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/`.

## 1. Tool-by-tool inventory

The wire envelope for every tool is the MCP `toolResult`
(`internal/mcp/types.go:79-87`): a `content[]` of text blocks plus an
`isError` bool. There is no first-class structured channel — every payload
travels as a single `text` content block, and "structured" payloads are JSON
strings that callers re-parse. Two error-emission helpers exist: `errResult`
(plain text, `internal/mcp/server.go:988-990`) and `errResultCode`
(`error_code: <CODE>\ndetail: <text>`, `internal/mcp/server.go:992-997`).

### 1.1 niwa_check_messages — `internal/mcp/server.go:231-234`

- **Wire schema:** no parameters.
- **Success:** Markdown string, not JSON. `## N new message(s)` plus per-message
  block with `**ID**`, `**Sent**`, optional `**Reply to**`, `**Task ID**`,
  `**Expires**`, then a fenced `json` block containing the body
  (`internal/mcp/server.go:572-592`). Empty inbox → `"No new messages."`
  text (`server.go:563`).
- **Error:** `errResult` (no code) for the two failure modes:
  `"no inbox dir configured; is NIWA_SESSION_ROLE set?"` (`server.go:503`),
  `"cannot read inbox: ..."` (`server.go:514`).
- **Hint quality:** the missing-env-var error names the env var the caller
  must set — that is good. Other errors are unstructured.

### 1.2 niwa_send_message — `server.go:235-250`

- **Required:** `to`, `type`, `body`. Optional: `reply_to`, `task_id`,
  `expires_at`.
- **Success:** plain markdown string `"Message sent.\n- **ID**: <id>\n- **To**: <role>"`
  (`server.go:682`). Not JSON.
- **Error codes emitted:** `UNKNOWN_ROLE` (`server.go:700, 715`),
  `BAD_TYPE` (`server.go:703`), `INBOX_UNWRITABLE` (`server.go:721, 755, 760`).
  Plus three plain `errResult` calls — `"to and type are required"`
  (`server.go:697`), `"body is required"` (`server.go:708`), `"body exceeds 64
  KB limit"` (`server.go:711`).
- **Hint quality:** mixed. The `BAD_TYPE` detail surfaces the regex the value
  must match. `UNKNOWN_ROLE` says "not registered under .niwa/roles/". But the
  three plain-text errors carry no code, so callers cannot programmatically
  branch on "missing required field" vs other failures.

### 1.3 niwa_ask — `server.go:252-263`

- **Required:** `to`, `body`. Optional: `timeout_seconds` (default 600,
  `server.go:794`).
- **Success:** **two distinct JSON shapes plus a fall-through to a third.**
  When a live coordinator is registered: blocks until terminal, then returns
  `formatTerminalResult` shape (see niwa_query_task below). When no live
  coordinator: returns
  `{"status":"no_live_session","role":"<role>","message":"..."}`
  (`server.go:837-842`). On timeout: returns
  `{"status":"timeout","task_id":"<id>","timeout_seconds":N}`
  (`server.go:881-886`).
- **Error codes:** `UNKNOWN_ROLE` (`server.go:803`). Plus plain `errResult`
  for missing `to` (`server.go:797`), missing `body` (`server.go:800`), and
  marshal/IO failures (`server.go:813, 832`).
- **Hint quality:** the no_live_session "message" string explains the
  condition in prose ("The role may have completed its task or not yet
  started."). That is genuine recovery context — but it lives in a free-form
  `message` field, not a hint field. No other tool uses a `message` field.

### 1.4 niwa_delegate — `server.go:265-279`

- **Required:** `to`, `body`. Optional: `mode`, `expires_at`, `session_id`,
  `read_only`.
- **Success (async, default):** `{"task_id":"<uuid>"}` (`handlers_task.go:147`).
- **Success (sync):** `formatTerminalResult` shape (`handlers_task.go:164`).
- **Error codes:** `BAD_PAYLOAD` (`handlers_task.go:122`), `SESSION_REQUIRED`
  (`handlers_task.go:126`), `UNKNOWN_ROLE` (`handlers_task.go:131`),
  `SESSION_NOT_FOUND` (`handlers_task.go:272`), `SESSION_INACTIVE`
  (`handlers_task.go:275`), `INVALID_WORKTREE_PATH` (`handlers_task.go:286`),
  `INBOX_UNWRITABLE` (`handlers_task.go:243`). Plain `errResult` for missing
  `to`/`body` (`handlers_task.go:113-117`) and several internal-state checks
  (`handlers_task.go:135, 138, 188, 208, 211, 233, 236`).
- **Hint quality:** `SESSION_REQUIRED` carries an explicit recovery sentence:
  "provision one with niwa_create_session, or set read_only:true for tasks
  that make no git changes" (`handlers_task.go:127-128`). This is the
  best-in-class hint pattern in the surface today. `UNKNOWN_ROLE` says
  what's wrong but not how to recover. `SESSION_INACTIVE` lists the actual
  status vs expected. The internal-state errResults ("NIWA_INSTANCE_ROOT not
  set", `handlers_task.go:135`) are actionable but unstructured.

### 1.5 niwa_query_task — `server.go:281-290`

- **Required:** `task_id`.
- **Success:** JSON string with `task_id`, `state`, `state_transitions`,
  `restart_count`, optionally `last_progress`, and on terminal states
  `result` / `reason` / `cancellation_reason` (`handlers_task.go:937-959`).
- **Error codes:** `NOT_TASK_PARTY` (auth.go:98, 130 etc.), `NOT_TASK_OWNER`
  (`auth.go:116`), `TASK_ALREADY_TERMINAL` is **not** emitted by this tool
  (kindParty intentionally accepts terminal — `auth.go:133-134`).
- **Hint quality:** all auth errors collapse to a single non-specific
  "not authorized for this task" detail (`auth.go:98, 109, 130, 142, 174`,
  etc.). This is intentional defense-in-depth (no enumeration leak,
  documented `auth.go:106-108`), but it means the caller has zero recovery
  signal: identical text for missing task, malformed ID, corrupted state,
  and lock timeout.

### 1.6 niwa_await_task — `server.go:292-302`

- **Required:** `task_id`. Optional: `timeout_seconds` (default 600).
- **Success (terminal):** `formatTerminalResult` shape: `status`, `task_id`,
  `restart_count`, optionally `max_restarts`, `last_progress`, plus
  `result`/`reason`/`cancellation_reason` (`handlers_task.go:967-989`).
  `status` here is the task **state** string (`completed`/`abandoned`/
  `cancelled`).
- **Success (timeout):** `{"status":"timeout","task_id":"<id>",
  "current_state":"<state>","timeout_seconds":N,"last_progress":...}`
  (`handlers_task.go:457-466`).
- **Success (question_pending):** `{"status":"question_pending",
  "ask_task_id":"<id>","from_role":"<role>","body":<wrapped body>}`
  (`handlers_task.go:527-534`). This is a multiplex on top of await,
  surfacing inbound questions as an alternative terminal-like outcome.
- **Error codes:** auth errors via `authorizeTaskCall(kindDelegator)`
  (`handlers_task.go:404`), special-cased so `TASK_ALREADY_TERMINAL` falls
  through to the success terminal shape (`handlers_task.go:407-415`).
- **Hint quality:** the timeout shape is genuinely useful — it preserves
  `current_state` and `last_progress` so the caller can decide whether to
  re-await. The question_pending multiplex is documented in shapes alone;
  callers must know to switch on `status`.

### 1.7 niwa_report_progress — `server.go:304-315`

- **Required:** `task_id`, `summary`. Optional: `body`.
- **Success:** `{"status":"recorded","task_id":"<id>","summary":"<truncated>"}`
  (`handlers_task.go:580-581`). Note `status` is `"recorded"` here — a
  literal not used elsewhere.
- **Error codes:** `BAD_PAYLOAD` (`handlers_task.go:541`), executor-auth
  failures (`auth.go:124-127`), plus taskstore mapping
  (`mapStoreError` → `TASK_ALREADY_TERMINAL` or unstructured
  `errResult`, `handlers_task.go:1020-1029`).
- **Hint quality:** `BAD_PAYLOAD` says "summary is required" — fine.
  Executor-auth errors carry the same generic "not authorized" string.

### 1.8 niwa_finish_task — `server.go:317-328`

- **Required:** `task_id`, `outcome`. Optional: `result`, `reason`.
- **Success (normal):** `{"status":"<outcome>","task_id":"<id>"}`
  (`handlers_task.go:695`). Here `status` is the task **state** string.
- **Success (already-terminal):** the special shape
  `{"status":"already_terminal","error_code":"TASK_ALREADY_TERMINAL",
  "current_state":"<state>"}` (`handlers_task.go:607-609`). **This is the
  only place in the surface where `error_code` appears as a JSON key inside
  a success-shape payload** — it's a non-error toolResult that nevertheless
  carries an embedded error code string.
- **Error codes:** `BAD_PAYLOAD` (`handlers_task.go:629, 637, 640, 643, 647`),
  executor/target auth (`handlers_task.go:597-622`), taskstore mapping.
- **Hint quality:** `BAD_PAYLOAD` strings are explicit and actionable
  ("outcome=completed requires result", "outcome=abandoned must not include
  result"). Strong.

### 1.9 niwa_list_outbound_tasks — `server.go:331-339`

- **Required:** none. Optional: `to`, `status`.
- **Success:** `{"tasks":[{"task_id":"<id>","to_role":"<role>","state":"<state>",
  "age_seconds":N,"body_summary":"<truncated>"}, ...]}`
  (`handlers_task.go:723-770`).
- **Error codes:** none (only plain `errResult` for missing instance root /
  unreadable tasks dir, `handlers_task.go:711-720`).
- **Hint quality:** N/A — read-only.

### 1.10 niwa_update_task — `server.go:342-352`

- **Required:** `task_id`, `body`.
- **Success:** `{"status":"updated"}` (`handlers_task.go:873`) or, if the
  task moved out of `queued` between auth and write,
  `{"status":"too_late","current_state":"<state>"}` (`handlers_task.go:824`)
  or `{"status":"too_late","current_state":"consumed"}`
  (`handlers_task.go:857`) or `{"status":"too_late","current_state":"terminal"}`
  (`handlers_task.go:819`).
- **Error codes:** `BAD_PAYLOAD` (`handlers_task.go:795`),
  `TASK_ALREADY_TERMINAL` via mapStoreError, delegator-auth errors,
  `INBOX_UNWRITABLE` via writeMessageAtomic.
- **Hint quality:** the `too_late` triple-shape is informative but
  tells the caller nothing about how to recover (the niwa-mesh skill
  describes redelegation as the recovery, but the response doesn't say so).

### 1.11 niwa_cancel_task — `server.go:354-362`

- **Required:** `task_id`.
- **Success:** `{"status":"cancelled"}` (`handlers_task.go:930`) or
  `{"status":"too_late","current_state":"<state>"}`
  (`handlers_task.go:902-906`).
- **Error codes:** delegator-auth.
- **Hint quality:** same as update — `too_late` says what but not how
  to recover.

### 1.12 niwa_create_session — `server.go:365-376`

- **Required:** `repo`, `purpose`. Optional: `parent_session_id`.
- **Success:** `{"session_id":"<8-hex>","worktree_path":"<abs>"}`
  (`handlers_session.go:216-227`). On daemon spawn failure (non-fatal),
  also includes `daemon_warning` field with the error string
  (`handlers_session.go:223-224`).
- **Error codes:** `BAD_PAYLOAD` (`handlers_session.go:148, 151`),
  `UNKNOWN_ROLE` (`handlers_session.go:160, 176`). Plain `errResult` for
  internal-state failures (`handlers_session.go:154, 166, 170, 182, 190,
  201, 208`).
- **Hint quality:** `UNKNOWN_ROLE` includes the path it looked at
  (`fmt.Sprintf("role %q not found at %s", args.Repo, roleDir)`,
  `handlers_session.go:160`) — that is genuinely useful. The git-worktree-add
  failure surfaces git's stderr verbatim (`handlers_session.go:190`).

### 1.13 niwa_destroy_session — `server.go:378-388`

- **Required:** `session_id`. Optional: `force` (default false).
- **Success:** `{"session_id":"<id>","status":"<status>"}` plus optional
  `branch_warning` (`handlers_session.go:298-308`). Idempotent: re-running
  on an already-ended session returns the same shape (`handlers_session.go:
  250-256`).
- **Error codes:** `SESSION_NOT_FOUND` (`handlers_session.go:245`).
- **Hint quality:** `branch_warning`, when present, contains the literal
  shell command to recover (`handlers_session.go:288-291`). Best-in-class
  recovery hint format anywhere in the surface.

### 1.14 niwa_list_sessions — `server.go:390-399`

- **Required:** none. Optional: `repo`, `status`.
- **Success:** JSON-encoded array of `SessionLifecycleState`
  (`handlers_session.go:42-49`). Empty array, not null. The on-disk schema
  is the wire schema (`session_lifecycle.go:30-47`): `v`, `session_id`,
  `parent_session_id`, `repo`, `purpose`, `status`, `creation_time`,
  `worktree_path`, `claude_conversation_id`, `creator_pid`,
  `creator_start_time`. Note: the response is **a bare JSON array**, not
  `{"sessions":[...]}` — inconsistent with niwa_list_outbound_tasks which
  wraps in `{"tasks":[...]}`.
- **Error codes:** none — only plain `errResult` for IO/marshal
  failures (`handlers_session.go:30, 47`).
- **Hint quality:** N/A.

## 2. Convention summary

### What is consistent

- **Field naming:** every JSON key on every wire payload is snake_case.
  No camelCase, no PascalCase. (`task_id`, `session_id`, `worktree_path`,
  `state_transitions`, etc., across `types.go:30-378`,
  `session_lifecycle.go:30-47`, `handlers_task.go:723-770, 938-989`.)
- **Error code casing:** `UPPER_SNAKE_CASE`, always
  (`handlers_task.go:14-15` enumerates the canonical eight; `BAD_TYPE`,
  `BAD_PAYLOAD`, `INBOX_UNWRITABLE`, `INVALID_WORKTREE_PATH`,
  `SESSION_REQUIRED`, `SESSION_NOT_FOUND`, `SESSION_INACTIVE`,
  `UNKNOWN_ROLE`, `NOT_TASK_OWNER`, `NOT_TASK_PARTY`,
  `TASK_ALREADY_TERMINAL` cover the rest).
- **Error code wire format:** every code goes through `errResultCode`
  which produces the literal two-line text
  `"error_code: <CODE>\ndetail: <text>"` inside a single text content block,
  with `isError=true` (`server.go:992-997`). Callers parse via the shared
  `errorCodeFromText` helper (`server.go:1012-1023`).
- **Task IDs are UUIDv4** (validated `auth.go:97`); session IDs are
  8 lowercase hex chars (`session_lifecycle.go:22`,
  `handlers_session.go:383`).
- **Timestamps are RFC3339** in every domain type
  (`types.go:233, 260, 280, 359`).
- **`status` field as the success-shape discriminant** is broadly used
  (`no_live_session`, `timeout`, `question_pending`, `recorded`, `updated`,
  `cancelled`, `too_late`, `already_terminal`, plus the task-state values
  `completed`/`abandoned`/`cancelled` from finish/await).

### What is inconsistent

1. **Markdown vs JSON for success.** `niwa_check_messages`
   (`server.go:572-592`) and `niwa_send_message` (`server.go:682`) return
   markdown text strings. Every other tool returns JSON. This forces
   every caller of those two tools to scrape prose for the message ID
   instead of decoding a field.
2. **`{"sessions":[...]}` vs bare array.** `niwa_list_outbound_tasks`
   wraps in `{"tasks":[...]}` (`handlers_task.go:769`); `niwa_list_sessions`
   returns a bare JSON array (`handlers_session.go:42-49`). Either is
   defensible alone; the mismatch isn't.
3. **`status` is overloaded.** Sometimes it carries a task **state**
   (`completed`/`abandoned`/`cancelled`, from `formatTerminalResult`,
   `handlers_task.go:967`). Sometimes a **transient outcome**
   (`timeout`, `no_live_session`, `question_pending`, `too_late`,
   `already_terminal`). Sometimes an **action verb**
   (`recorded`, `updated`, `cancelled`). Callers must memorize the
   per-tool vocabulary.
4. **Errors split between coded and uncoded.** Roughly half of error
   sites use `errResult` (no code) and half `errResultCode`. The split
   is not principled: `niwa_send_message` uses `errResult` for
   "to and type are required" and "body is required" (`server.go:697-708`),
   but `errResultCode("BAD_PAYLOAD", ...)` lives elsewhere
   (`handlers_task.go:122, 629, 637, 795`) for materially identical
   "you forgot a required field" failures.
5. **`already_terminal` carries `error_code` inside a success payload.**
   `handlers_task.go:607-609` is the only place a string-shaped
   `error_code` appears as a JSON key, and it's intentionally **not**
   `isError=true`. This is a deliberate design choice (the task is in a
   valid terminal state — the call shouldn't fail), but it inverts the
   wire convention.
6. **Recovery hints are ad-hoc.** Excellent in some places
   (`SESSION_REQUIRED` says how to recover, `handlers_task.go:127-128`;
   `branch_warning` includes the recovery shell command,
   `handlers_session.go:288-291`); generic in others (`NOT_TASK_PARTY`
   always returns the same six-word string, `auth.go:98, 109, 130, 142,
   174, 187, 202, 207, 216`); absent in many (`too_late` shapes don't
   suggest redelegation, `handlers_task.go:819-857, 902-906`).
7. **Auth errors collapse four distinct conditions into one code.**
   `NOT_TASK_PARTY` covers: malformed ID (`auth.go:97-100`), ENOENT
   (`auth.go:103-111`), corrupted state.json (same site), lock timeout
   (same site), and actual non-party. Document says this is fail-closed
   anti-enumeration (`auth.go:106-108`), and that's correct for the
   ENOENT/exists distinction — but bundling lock-timeout (a transient,
   retry-this condition) under the same code costs callers a useful
   recovery hint.

## 3. Mesh redesign alignment

| New field/code | Source (design) | Convention check | Recommendation |
|---|---|---|---|
| `DAEMON_SPAWN_TIMEOUT` | `DESIGN-niwa-mesh-reliability.md:1045-1051` | UPPER_SNAKE_CASE matches; emitted via `errResultCode` will fit the existing wire format. | OK as-is. Add a recovery hint in `detail`: e.g., "daemon did not write daemon.pid within 500 ms; check daemon.log under the worktree's .niwa/". The session is rolled back, so the hint is purely diagnostic — that is fine. |
| `MISSING_SKILLS` with `{missing, available}` | `DESIGN:1031-1043` | Code naming matches. Field names `missing` and `available` are snake_case-clean (single words). **But:** the design proposes returning these as **structured fields alongside `error_code`**, which the current `errResultCode` wire format cannot express — it's two prose lines, not a JSON object. | This is the largest divergence in the redesign. Either (a) extend `errResultCode` to accept a JSON detail (e.g., emit `error_code: MISSING_SKILLS\ndetail: ...\nstructured: {"missing":[...],"available":[...]}`), or (b) introduce a parallel `errResultStructured` helper that emits a JSON content block with `isError=true` and a top-level `error_code` field. Option (b) is cleaner; option (a) preserves the existing parser. Pick one and apply it as the convention forward. |
| `SOURCE_BODY_LOST` | `DESIGN:1023-1026` | UPPER_SNAKE_CASE matches. Returned synchronously, no structured fields needed. | OK as-is. The detail should name the source task ID and suggest passing `body_overrides` to `niwa_redelegate` — that's the documented recovery (DESIGN:1024-1026). |
| `daemon: {alive, pid, started_at}` on list_sessions | `DESIGN:1053-1068, 945-947` | Field names snake_case-clean. Adding a sub-object to an existing wire shape is exactly the kind of additive change the surface tolerates today (similar to `branch_warning` on destroy). | OK. **Note** the existing `niwa_list_sessions` returns a bare array — if this design adds `daemon` to each entry, that's fine, but the existing-array-vs-`{"tasks":[...]}` inconsistency stays unsolved. Consider piggy-backing a `{"sessions":[...]}` envelope on the same change if backward compat allows. |
| `source_state_at_fork` on redelegate | `DESIGN:1001-1011` | snake_case clean; value space is the existing task-state vocabulary. | OK as-is. |
| `redelegated_from` envelope field | `DESIGN:950, 1008` | snake_case clean; appears in TaskEnvelope (on-disk) and in the niwa_redelegate response. | OK as-is. Make sure it also appears in `niwa_query_task`'s shape so callers can audit redelegation chains without reading envelope.json directly — the design covers this implicitly via envelope persistence but the query handler at `handlers_task.go:937-959` does not currently project envelope fields. |
| `state="abandoned", reason="taskstore_lost"` | `DESIGN:484-553` | `taskstore_lost` is lower_snake_case. **But:** `reason` is currently `json.RawMessage` (`types.go:278`), not a string, and existing reason payloads are objects like `{"error":"session_destroyed",...}` (`handlers_session.go:345`). | Two recommendations: (a) make the new abandoned-reason shape an object: `{"error":"taskstore_lost", ...}` for parity with `session_destroyed`. (b) Document the canonical `error` field-name in the reason object — today `handlers_session.go:345` and `server.go:869` both use `"error":"<reason>"`, but this is convention-by-accident, not contract. Adopting it explicitly avoids a `reason` schema split. |

**Net verdict:** the redesign respects naming conventions (snake_case
fields, UPPER_SNAKE_CASE codes) without exception. The one structural
problem is **`MISSING_SKILLS` wants structured error fields, and the
existing error wire format can't carry them**. Solving that is a
prerequisite — and once solved, several existing errors (`SESSION_INACTIVE`
could carry `current_status`/`required_status`; `INVALID_WORKTREE_PATH`
could carry the offending path) would benefit from the same upgrade.

## 4. Existing UX problems

P1 — observed concrete defects, citations attached.

1. **Unstructured "missing required field" errors in messaging tools.**
   `server.go:697, 708, 711, 797, 800` return plain text without
   `error_code`. Tools that call into the same code path emit
   `BAD_PAYLOAD` (`handlers_task.go:541, 629, 637, 640, 643, 647, 795`).
   Same failure shape, two error formats.
2. **`niwa_check_messages` returns markdown.** `server.go:572-610` builds
   a prose document. Programmatic callers (CLI, tests, the niwa-mesh skill
   itself) cannot decode message IDs without scraping. The response is also
   the only place the `_niwa_task_body` envelope marker appears
   (`server.go:651-665`), buried inside a fenced code block — workers
   parse it indirectly through the LLM.
3. **`niwa_send_message` returns markdown for the success path.**
   `server.go:682`. The message ID lives in a markdown bullet line. Any
   caller that wants to subsequently send a `reply_to` must regex out the
   ID.
4. **`niwa_list_sessions` envelope shape inconsistent with
   `niwa_list_outbound_tasks`.** `handlers_session.go:42-49` returns
   `[...]`, `handlers_task.go:769` returns `{"tasks":[...]}`. Adding the
   new `daemon` sub-object (Issue 3 of the design) is a natural moment to
   reconcile.
5. **`NOT_TASK_PARTY` hides four distinct failure modes.** `auth.go:97-111`
   emits the same code+text for malformed ID, ENOENT, corruption, and
   lock timeout. The fail-closed anti-enumeration intent only justifies
   collapsing existence-revealing cases (malformed/ENOENT). Lock timeout
   is transient and retry-actionable; it should be a distinct code so
   callers can retry without losing the anti-enumeration property.
6. **`too_late` shapes don't tell the caller how to recover.** Three sites
   emit `{"status":"too_late","current_state":"<state>"}` —
   `handlers_task.go:819, 824, 857, 902-906`. The documented recovery
   (per the niwa-mesh skill) is `niwa_redelegate`, but the response says
   nothing.
7. **`status` is overloaded across 9+ literal values with no documented
   vocabulary.** Tool descriptions (`server.go:228-400`) describe
   parameters but never the success-shape `status` value space. Callers
   must read each handler.
8. **`already_terminal` success shape carries `error_code`.**
   `handlers_task.go:607-609`. This breaks the rule "`error_code` only
   appears when `isError=true`" — a callable convention that would
   otherwise let parsers route on `isError` alone.
9. **`niwa_create_session` warning lives in the same payload as success.**
   `daemon_warning` (`handlers_session.go:223-224`) is a magic key on the
   success object. There is no general "warnings" envelope — this is the
   only tool that emits one. `niwa_destroy_session`'s `branch_warning`
   (`handlers_session.go:301`) is the second instance. Two tools, two
   different field names, no shared shape.
10. **No tool description in `tools/list` documents its response schema.**
    `server.go:228-400` describes parameters only. The response shape
    is implicit, so an LLM caller that does not have prior context cannot
    know what to expect — especially for the multiplex tools
    (`niwa_await_task` returning `question_pending`, `niwa_ask` returning
    `no_live_session`).

## 5. Proposed UX issues

Numbered. Each is independent enough to ship on its own; a couple share
implementation surface (E1 + E2).

### E1 — Define a structured-error wire format

- **Goal.** Let error responses carry typed fields beyond `error_code` and a
  prose detail, unblocking `MISSING_SKILLS` and several existing codes that
  would benefit from structured detail.
- **AC1.** Add an `errResultStructured(code string, fields map[string]any)`
  helper that emits a JSON-encoded text content block with top-level
  `error_code` and the supplied fields, `isError=true`.
- **AC2.** Update `errorCodeFromText` (`server.go:1012-1023`) to also accept
  the JSON form (sniff the first non-whitespace byte for `{`).
- **AC3.** Document the format in `docs/guides/` alongside the existing
  R50 error-code list, and call it out in tool-description prose for any
  tool whose errors use the structured form.
- **AC4.** Migrate `MISSING_SKILLS`, `SESSION_INACTIVE`, and
  `INVALID_WORKTREE_PATH` to the structured form (each gains a fields
  payload that surfaces what the caller needs to recover).
- **AC5.** Existing `errResultCode` calls keep working unchanged — the
  prose form remains valid.

### E2 — Replace markdown returns with JSON for niwa_check_messages and niwa_send_message

- **Goal.** Stop forcing programmatic callers to scrape prose.
- **AC1.** `niwa_send_message` returns `{"message_id":"<uuid>","to":"<role>"}`.
- **AC2.** `niwa_check_messages` returns
  `{"messages":[{"id":...,"type":...,"from":...,"sent_at":...,"reply_to":...,
  "task_id":...,"expires_at":...,"body":<json>},...]}`. The
  `_niwa_task_body` wrapping for `task.delegate` bodies stays — it's a
  prompt-injection defense, not a presentation choice.
- **AC3.** Empty inbox returns `{"messages":[]}` (matches list_outbound_tasks
  shape).
- **AC4.** Tool-description prose updated; CLI side that today consumes
  these tools (mesh-watch, niwa session register prompt) updated in the
  same change.

### E3 — Reconcile list-shape envelope across niwa_list_sessions and niwa_list_outbound_tasks

- **Goal.** One envelope shape for list tools.
- **AC1.** `niwa_list_sessions` returns `{"sessions":[...]}` instead of a
  bare array (`handlers_session.go:42-49`).
- **AC2.** Each entry gains the design's `daemon: {alive, pid, started_at}`
  sub-object (Issue 3 of the mesh design); `daemon.alive` is a real probe.
- **AC3.** Tool description in `server.go:390-399` documents the shape.
- **AC4.** CLI consumers updated in the same change; functional tests
  under `test/functional/features/` cover both the bare-array regression
  fix and the new daemon sub-object.

### E4 — Promote auth lock-timeout from NOT_TASK_PARTY to a distinct retryable code

- **Goal.** Let callers retry transient lock contention without losing
  anti-enumeration on existence/identity errors.
- **AC1.** `ReadState`'s `ErrLockTimeout` path in `auth.go:103-111` returns
  a new `TASKSTORE_BUSY` (or similar) code instead of `NOT_TASK_PARTY`.
- **AC2.** `mapStoreError` (`handlers_task.go:1020-1029`) emits the same
  new code for ErrLockTimeout instead of plain `errResult`.
- **AC3.** ENOENT, corrupted state.json, and malformed ID continue to
  emit `NOT_TASK_PARTY` — the anti-enumeration rationale at
  `auth.go:106-108` still holds for those.
- **AC4.** Tests in `auth_test.go` cover the new code.
- **AC5.** niwa-mesh skill documents that callers seeing `TASKSTORE_BUSY`
  should retry with backoff.

### E5 — Add recovery hints to too_late responses

- **Goal.** Tell callers how to recover from update/cancel races.
- **AC1.** Every `too_late` shape in `handlers_task.go:819, 824, 857, 902-906`
  gains a `recovery_hint` string field naming `niwa_redelegate` and
  pointing to the new task's source.
- **AC2.** When `redelegate` is unavailable (e.g., terminal state), the
  hint says so and points to `niwa_query_task` instead.
- **AC3.** A `current_state` field is always present (already true today —
  this AC just says don't drop it).

### E6 — Document response shapes in tools/list descriptions

- **Goal.** Let LLM callers know what to expect without reading source.
- **AC1.** Each `toolDef.Description` in `server.go:228-400` gains a
  one-line shape sketch (e.g., `"... returns
  {task_id, status, ...}"`).
- **AC2.** Multiplex tools (`niwa_await_task`, `niwa_ask`) call out the
  alternative success shapes (`question_pending`, `timeout`,
  `no_live_session`).
- **AC3.** A new `docs/guides/mcp-response-shapes.md` enumerates every
  tool's success and error shape with a JSON example for each.

### E7 — Standardize warning fields on success payloads

- **Goal.** One pattern for success-with-caveat. Today `daemon_warning`
  (`handlers_session.go:223-224`) and `branch_warning`
  (`handlers_session.go:301`) are both ad-hoc.
- **AC1.** Replace both with a uniform optional `warnings` array, each
  entry shaped `{"code":"DAEMON_SPAWN_FAILED" | "BRANCH_UNMERGED",
  "message":"...","recovery_hint":"..."}`.
- **AC2.** Wire-format change documented; old field names removed in the
  same release (no caller currently parses them in the public surface
  beyond the niwa CLI itself).
- **AC3.** Future tools that need to surface non-fatal caveats can
  append to `warnings` without inventing new field names.

### E8 — Standardize the `reason` payload object shape

- **Goal.** Make `reason` (and `cancellation_reason`) a stable JSON
  object schema rather than a free-form `json.RawMessage`.
- **AC1.** Define the canonical shape: `{"error":"<lower_snake_code>",
  "details":<object>}` with `error` matching the existing
  `taskstore_lost`, `session_destroyed`, `ask_timeout` literals
  (`handlers_session.go:345`, `server.go:869`,
  `DESIGN-niwa-mesh-reliability.md:497`).
- **AC2.** Document the canonical reason-code vocabulary alongside the
  R50 error-code list.
- **AC3.** Existing reason emitters (`server.go:869`,
  `handlers_session.go:345`, plus daemon-side abandon paths) audited
  for conformance; any deviation fixed in the same change.
- **AC4.** No wire-format break — the existing reason payloads are
  already shaped this way; this issue codifies the contract.
