# Architecture Review: DESIGN-niwa-ask-live-coordinator.md

Reviewed against ground truth: `internal/mcp/server.go`, `watcher.go`, `handlers_task.go`, `types.go`, `liveness.go`, `internal/cli/session_register.go`, `internal/workspace/channels.go`.

---

## 1. Implementation clarity — missing interfaces

**Finding: `maybeRegisterCoordinator` has no specified write path for `sessions.json`.**

The design says this helper "writes a `SessionEntry` to `sessions.json`", but the existing write path (`writeSessionEntry` in `internal/cli/session_register.go`) lives in the CLI package — not in `internal/mcp`. The design doesn't specify whether the implementer should:

- Call `writeSessionEntry` from `internal/mcp` (creates a cross-package dependency from a lower package to a higher one — dependency inversion violation),
- Duplicate the write logic inside `internal/mcp/server.go` (parallel implementation of the same concern), or
- Extract `writeSessionEntry` into a shared sub-package.

The "atomic read-modify-write" described in Security Considerations mirrors `writeSessionEntry` exactly, but there is no interface definition for where this shared code lives. An implementer will independently invent one of the above approaches, and the wrong choice (option 1 or 2) creates a structural problem. **The design should specify that session write logic moves to `internal/mcp` or a new `internal/sessions` sub-package and becomes the single implementation used by both `maybeRegisterCoordinator` and `runSessionRegister`.** This is the one genuinely missing component.

**Finding: Catch-up scan mechanics are underspecified.**

The design states `handleAwaitTask` runs "a catch-up scan over `inbox/task.ask` messages" before blocking. But `notifyNewFile` moves `task.ask` files to `inbox/read/` only after a successful channel send. So the catch-up scan must read from `inbox/` (files that arrived when no channel was registered) — not from `inbox/read/`. This is inferrable but the scan's directory target, deduplication contract with `seenFiles`, and the channel-full case (should it dispatch immediately or queue for re-send?) are all left to the implementer to work out. A one-paragraph description of the scan algorithm would close this gap.

---

## 2. Missing components and interfaces

**`questionEvent` type is defined inconsistently.**

`types.go` defines `TaskEventKind` with six existing constants. The design says `types.go` "adds `EvtQuestion` to `taskEventKind` (or a new `questionEvent` struct if kept fully separate)" — leaving both options open. This ambiguity matters architecturally: if `EvtQuestion` is added to `TaskEventKind`, the `String()` method and the transitions.log format must be updated; if a separate `questionEvent` struct is used, `questionWaiters` has type `map[string]chan questionEvent` rather than `map[string]chan taskEvent`, and the watcher's dispatch path doesn't need to touch `taskEvent` at all. The design should commit to one shape. Based on the stated rationale (hard separation between terminal and question channels), a separate `questionEvent` struct is the consistent choice, but this is not stated as the decision.

**`handleCheckMessages` question handling has no code changes specified but requires behavioral awareness.**

The design says "no code changes needed" for the `niwa_check_messages` path because `task.ask` is just another inbox file. However, `handleCheckMessages` currently wraps `task.delegate` bodies with `wrapDelegateBody` (server.go:474). The design specifies a `_niwa_note` wrapper for `task.ask` bodies written by `handleAsk`, not for how they are rendered when read back via `niwa_check_messages`. A coordinator reading a `task.ask` via `handleCheckMessages` will receive the raw body as-is — the `_niwa_note` wrapper is inside the body, not applied by `handleCheckMessages`. This is fine, but the design doesn't confirm that "no code changes needed" accounts for this; an implementer might wonder whether `handleCheckMessages` should also special-case `task.ask` the way it special-cases `task.delegate`.

---

## 3. Phase sequencing

**Phase 1 and Phase 2 cannot be done out of order, but their dependency is not stated explicitly.**

Phase 1 routes questions to the coordinator's inbox via `handleAsk` writing `task.ask` notifications. Phase 2 adds `questionWaiters` dispatch and the catch-up scan to `handleAwaitTask`. These are correctly ordered: Phase 1 is the prerequisite for Phase 2 because:

- Phase 2's `notifyNewFile` dispatch branch fires on `task.ask` files that Phase 1 produces.
- Phase 2's catch-up scan reads files Phase 1 wrote.
- Implementing Phase 2 alone (without Phase 1's routing) means the watcher branch would never fire and the catch-up scan would find nothing.

The design doesn't make this dependency explicit. If a contributor starts Phase 2 first (e.g., adding the channel infrastructure), they'd have a dead code path with no test coverage until Phase 1 lands. Worth one sentence in the Implementation Approach section.

**Phase 3 (skill content) is independent and safe to do in any order relative to Phases 1 and 2.**

This is correctly implied by the design but not stated. An implementer doing Phase 3 first (updating `buildSkillContent()` to document `question_pending` before the feature exists) is harmless since the skill content is installed at apply time, not runtime. Confirming this would help a contributor who wants to parallelize.

---

## 4. Simpler alternatives — could `questionWaiters` be avoided?

**Short answer: no — but the design undersells why.**

The design correctly rejects Option A (extend `awaitWaiters` with `EvtQuestion`) on the grounds of shared failure domain. There is, however, a simpler path worth examining: since `notifyNewFile` already sends channel notifications (`notifications/claude/channel`) for non-terminal, non-reply messages, a question that arrives while the coordinator is blocking on `niwa_await_task` would *already* trigger a `notifications/claude/channel` push to Claude Code — which Claude Code could surface as an interrupt. This path exists today for all non-terminal inbox messages.

The reason this doesn't solve the deadlock is that `niwa_await_task` is synchronous from the MCP protocol's perspective: Claude Code is blocked waiting for the tool response and cannot act on a channel notification until the tool call returns. Channel notifications during a blocking tool call are queued and delivered after. So the interrupt mechanism genuinely cannot reuse the existing channel notification path for the deadlock case. This is worth one sentence in the design's rationale for completeness, because readers familiar with the channel notification infrastructure will ask "why not just use that?" and the answer requires understanding MCP's synchronous tool execution model.

**The `questionWaiters` addition is approximately 130-150 lines.** The alternative of a hybrid poll (Option C, rejected) would be ~40 lines simpler but incurs 500ms latency. Given the explicitly documented zero-latency goal for AI Q&A workflows, `questionWaiters` is the right call. The design's justification is correct and the code cost is proportionate.

---

## 5. Structural fit summary

The design fits the existing architecture: it adds to `notifyNewFile` (one new dispatch branch, same non-blocking send pattern), extends `Server` with a new field under the existing `waitersMu` mutex, and adds a helper to two existing handlers. No new MCP tools. No changes to the daemon event loop. The action dispatch, provider, state contract, and CLI surfaces are unaffected.

The one structural gap that would cause implementer divergence is the session write path ownership (Finding 1 above). Without a resolution, two independent implementers will produce different package dependency graphs. Everything else is implementable from the design as written, with the catch-up scan mechanics being the secondary clarification worth adding.

---

## Verdict on blocking issues

| Finding | Severity |
|---------|----------|
| Session write path unspecified (CLI package vs mcp package vs extracted) — will produce divergent package structures | **Blocking** |
| `questionEvent` type left as either/or — implementer cannot know which shape to use | **Blocking** |
| Catch-up scan mechanics (target directory, deduplication, channel-full behavior) underspecified | **Advisory** |
| Phase 1/2 dependency not stated — dead-code risk if done out of order | **Advisory** |
| Channel notification alternative not addressed — readers familiar with the infrastructure will ask | **Advisory** |
