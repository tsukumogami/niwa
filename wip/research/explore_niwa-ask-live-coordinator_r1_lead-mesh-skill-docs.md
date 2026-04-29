# Lead: niwa-mesh skill documentation

## Findings

### 1. Where niwa-mesh skill documentation lives

**File locations:**
- Auto-generated `niwa-mesh` skill markdown: `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-6/public/niwa/internal/workspace/channels.go` (lines 638-748, function `buildSkillContent()`)
- Skill installations created in: `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` and `<repoDir>/.claude/skills/niwa-mesh/SKILL.md`
- User guides: `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-6/public/niwa/docs/guides/cross-session-communication.md`
- Design & PRD: `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-6/public/niwa/docs/designs/current/DESIGN-cross-session-communication.md` and `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-6/public/niwa/docs/prds/PRD-cross-session-communication.md`

The canonical source is **embedded in the Go binary** via `buildSkillContent()`. The skill is generated at apply time and installed to every coordinator and worker session.

### 2. Current documentation of `niwa_check_messages`, `niwa_wait` (awaiting), and `niwa_ask`

**What's currently documented:**

- **`niwa_check_messages`** (cross-session-communication.md, lines 206-216):
  - Returns unread messages from the caller's role inbox
  - Moves returned files to `inbox/read/` via atomic rename
  - Sweeps expired messages to `inbox/expired/` before returning
  - Recommended to call at idle points and every ~10 tool calls while working
  - In skill content (channels.go line 723-725): mentions it retrieves unread messages and moves them atomically

- **`niwa_await_task`** (PRD R21, line 349):
  - Blocks until a task reaches terminal state
  - Has optional `timeout_seconds` (default 600, i.e., 10 minutes)
  - Returns same payload as `niwa_query_task` or `{status: "timeout"}` with current state
  - In skill (channels.go lines 744-746): mentions re-await pattern for timeout scenarios
  - Long-running task guidance (cross-session-communication.md lines 462-482): explicit timeout up front or re-await loop

- **`niwa_ask`** (cross-session-communication.md, lines 171-186):
  - Creates first-class task with `body.kind = "ask"` (PRD R29)
  - Blocks on worker's reply via matching `niwa_send_message` with `reply_to`
  - Default timeout 600 seconds; returns `{status: "timeout"}` without cancelling
  - In skill (channels.go lines 717-720): "blocks until the peer responds or timeout elapses"; notes that if target has no live session, daemon spawns a worker
  - Worker asks coordinator pattern documented in skill (lines 732-735): use tight 60-120s timeout, fall back to abandon on timeout

### 3. Current documentation on coordinator polling loop

**What exists:**
- Supervisor/operational guidance (cross-session-communication.md lines 409-434):
  - After coordinator crash, reopen and check `niwa_check_messages` for accumulated `task.progress`, `task.completed`, `task.abandoned`
  - Recovery path is to query `niwa_query_task` or run `niwa task list` from CLI
  - `niwa_await_task` loses in-memory wake-up channel on crash (not persisted)
  - Note: awaitWaiters map is in-memory only; state.json is authoritative on disk

- Polling patterns (skill content, channels.go lines 728-746, "Common Patterns"):
  - Coordinator fan-out: call `niwa_delegate(mode="async")` for each role, collect task IDs, loop with `niwa_await_task` to gather results
  - Re-await loop: on `status:"timeout"`, re-call `niwa_await_task`
  - Progress polling: delegators observe progress through `niwa_check_messages`

**What's NOT documented:**
- Explicit guidance on when a coordinator should call `niwa_check_messages` while `niwa_await_task` is blocking
- The interaction pattern when a question arrives during `niwa_await_task`
- What the coordinator does when it receives both progress events AND a pending question in the same poll
- How `niwa_check_messages` and `niwa_await_task` coordinate at the daemon level

### 4. Documentation gap: how workers ask coordinators

**Current state:**
- Skill (channels.go lines 732-735): "Worker asks coordinator: inside a running task, call `niwa_ask(to="coordinator", body=...)` with a tight timeout (60-120s) when you need clarification; fall back to `niwa_finish_task(outcome="abandoned", reason="blocked: <detail>")` if the ask times out."
- PRD R29 (line 440-449): `niwa_ask` to idle roles spawns ephemeral workers; does NOT specify routing to a live coordinator
- Cross-session guide does NOT document what happens when a worker calls `niwa_ask(to="coordinator")`

**Critical gap identified:** The documentation assumes `niwa_ask(to="coordinator")` spawns an ephemeral `claude -p`, but this breaks approval gates. The design intent (issue #92) is to route to the live coordinator session. Currently no documentation explains the coordinator receiving the question.

### 5. Design changes needed in documentation: polling loop returning questions

**New semantics to document:**

1. **`niwa_check_messages` will return questions** (currently documents only task updates):
   - Pending questions from workers will arrive as messages in the coordinator's inbox
   - These are distinct from `task.progress`, `task.completed`, `task.abandoned` messages
   - Call signature unchanged; return format expanded to include question messages

2. **`niwa_await_task` will return early for questions** (currently documents only task terminal state):
   - Early return: `{status: "question", from_role: "...", body: {...}, ask_task_id: "..."}`
   - Coordinator must respond to the question before re-awaiting
   - This breaks the "wait until terminal" contract — need clear re-await guidance

3. **New response mechanism** (not yet documented):
   - Either a new `niwa_respond(ask_task_id, reply_body)` tool OR
   - Existing `niwa_send_message(reply_to=<question_id>, ...)`
   - Needs explicit documentation of call shape and return behavior

4. **Polling loop pattern changes**:
   - Coordinator must handle: "check for messages, handle questions if any, optionally await tasks"
   - Question handling does NOT interrupt async delegation — other tasks continue running
   - Re-await pattern after answering a question needs clear guidance

### 6. Currently documented vs. missing patterns

**Currently documented patterns in skill:**
- Coordinator fan-out/collect (async delegation)
- Long-running task re-await on timeout
- Worker asks coordinator (WITH TIMEOUT FALLBACK — assumes spawn)
- Progress polling during `niwa_await_task` — mentions observing via `niwa_check_messages` but doesn't explain polling cadence or interaction

**Missing patterns that new design requires:**
- **Question handling loop**: "While awaiting a task, questions may arrive. If `niwa_await_task` returns early with a question, respond via [tool], then re-await the same task."
- **Multi-question scenarios**: What happens if multiple workers ask the coordinator simultaneously? (Likely: each is handled in turn via separate `niwa_await_task` early returns)
- **Coordinator offline handling**: If coordinator is offline and worker asks, what happens? (Likely: question queues; no spawn fallback)
- **Distinguishing progress from questions**: What does the inbox message format look like for each? How does coordinator recognize a question vs progress?
- **Question timeouts**: Worker has 60-120s to wait for answer. What happens coordinator-side if question sits unanswered beyond that?

## Implications

1. **Skill content (buildSkillContent in channels.go) MUST be updated:**
   - "Peer Interaction" section needs explicit mention that questions arrive through `niwa_check_messages` and `niwa_await_task` early returns
   - "Common Patterns" section must document the "question in await loop" scenario
   - Response mechanism shape (tool name, parameters) must be specified in the skill

2. **Cross-session-communication.md guide should add:**
   - Section on "Coordinator Polling Loop Patterns" with examples of handling questions during await
   - Explanation of early return semantics from `niwa_await_task` when questions arrive
   - Worked example: coordinator awaits two tasks, receives a question from one worker, answers it, then continues awaiting

3. **Acceptance criteria gap:**
   - PRD does not include acceptance criteria for coordinator receiving questions during await
   - AC-M1/M2 cover ask/reply but assume ephemeral spawn, not live coordinator
   - New AC needed: "When a running worker calls `niwa_ask(to="coordinator")`and coordinator is alive and registered, the question queues in coordinator's inbox; next `niwa_check_messages` or `niwa_await_task` early return surfaces it"

4. **Implementation must track:**
   - How daemon distinguishes "live coordinator registered" from "no coordinator session"
   - Session liveness heartbeat mechanism (already exists per design, needs verification)
   - Message format for questions vs task updates (probably type field: `question.ask` vs `task.progress`)

## Surprises

1. **No existing `niwa_wait` tool:** The scope document mentions "`niwa_wait`" but the actual tool is `niwa_await_task`. This may be a terminology mismatch in the design docs vs implementation.

2. **Skill content is embedded Go code, not markdown files:** The canonical niwa-mesh skill lives in `channels.go` as a builder function, not as a tracked `.md` file. Changes to skill content require editing the Go binary builder, not filesystem files. This has implications for version control and rollback.

3. **Question handling is NOT mentioned anywhere in current PRD or skill:** Despite issue #92 being about routing `niwa_ask(to="coordinator")`, there is zero mention in the PRD (status: Delivered) of how the coordinator receives questions. This is a design gap introduced by the new work.

4. **`niwa_check_messages` and `niwa_await_task` are orthogonal in the current design:** No explicit coordination between them. The new design likely needs to unify their inbox-watching behavior so questions can surface from either call.

## Open Questions

1. **What's the message format for questions?** Are they `type: "question.ask"` in the inbox, or a new message type like `type: "worker.question"`? How does the coordinator parse the body to extract the worker's question text?

2. **Does the coordinator need a new response tool?** Or does `niwa_send_message(reply_to=<question_id>, ...)` suffice? What's the API surface?

3. **How is "live coordinator registered" tracked at daemon level?** Does `niwa session register` populate a heartbeat field? Is liveness checked on-demand or periodically?

4. **What happens if multiple workers ask the coordinator simultaneously?** Do questions queue in the inbox and get returned one-by-one by `niwa_check_messages`? Or does `niwa_await_task` return early only for the first question?

5. **Does `niwa_await_task` return early ONLY for questions, or for any message type?** (e.g., should `task.progress` also trigger early return?) Current PRD says it blocks until task terminal, so progress is not early-return material.

6. **What's the re-await signature after answering a question?** Does the coordinator call `niwa_await_task` again with the same `task_id`, and the daemon remembers it was interrupted?

7. **How does the skill content generation know the response tool name?** The Go builder function needs to reference the tool in the "Peer Interaction" section. Does this require a build-time constant?

## Summary

The niwa-mesh skill documentation lives in `internal/workspace/channels.go` (function `buildSkillContent()`) and currently describes delegation, progress reporting, completion, message vocabulary, and peer interaction. The **critical gap:** current docs assume `niwa_ask(to="coordinator")` spawns ephemeral workers (breaking approval gates), but the new design routes to live coordinator sessions by having questions surface in `niwa_check_messages` and `niwa_await_task` early returns. **Three areas need updating:** (1) skill "Peer Interaction" section to explain questions arrive via polling, (2) new documentation in "Common Patterns" for the coordinator question-handling loop, and (3) cross-session-communication.md guide with worked examples. The implementation must track live coordinator liveness and define the response tool/API shape; the skill must reference it.
