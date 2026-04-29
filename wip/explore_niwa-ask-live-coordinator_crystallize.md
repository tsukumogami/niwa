# Crystallize Decision: niwa-ask-live-coordinator

## Chosen Type

Design Doc

## Rationale

Requirements are fully specified in issue #92's acceptance criteria — no PRD needed.
The exploration's findings show clear what-to-build but multiple open how-to-build
decisions: how coordinator sessions register (auto vs explicit), what `niwa_await_task`
returns when a question arrives, where the "queue vs spawn" routing logic lives in
`handleAsk`, and how skill content in `channels.go` should document the new loop
pattern. One architectural decision was already made during exploration (response
mechanism is `niwa_finish_task`, not `niwa_send_message`) and needs a permanent record.
The implementation touches the daemon watcher, ask handler, task model, and generated
skill content — enough architectural scope to warrant a design doc before implementation.

## Signal Evidence

### Signals Present

- **What to build is clear, how to build it is not**: Issue #92 has 4 acceptance criteria
  describing exact behavior; the implementation approach (session liveness mechanism,
  early-return event type, routing logic location) is unresolved.
- **Technical decisions between approaches**: Session registration can be auto-on-first-MCP-call
  or explicit CLI step; `niwa_await_task` early-return payload shape has multiple candidates;
  routing check can live in `handleAsk` or in a new pre-handler layer.
- **Architecture and system design questions remain**: Daemon watcher (`notifyNewFile`)
  needs to dispatch a new event kind; the `awaitWaiters` mechanism needs to handle
  question events alongside terminal task events.
- **Multiple viable implementation paths**: Auto-registration vs enforced explicit
  registration; `question_pending` response shape options documented in lead-niwa-wait-semantics.
- **Architectural decisions made that should be on record**: `niwa_finish_task` (not
  `niwa_send_message`) is the correct coordinator response path — this decision was
  made during exploration and will confuse future contributors if only in wip/ files.
- **Core question is "how should we build this?"**: Requirements are settled; the design
  question is how to wire session liveness, early-return delivery, and response handling.

### Anti-Signals Checked

- **What to build is still unclear**: Not present — issue #92 AC is explicit and user-confirmed.
- **No meaningful technical risk or trade-offs**: Not present — session liveness has
  registration race risk; `niwa_await_task` semantic change breaks existing coordinator code.
- **Problem is operational, not architectural**: Not present — changes span daemon watcher,
  ask handler, generated skill content.

## Alternatives Considered

- **Plan**: Demoted. No upstream design doc exists yet; technical approach is not settled.
  Building issues before the design would require rework.
- **Decision Record**: Demoted. Multiple interrelated decisions (session liveness +
  await early-return + routing logic + skill update) exceed a single-decision record scope.
- **PRD**: Demoted. Requirements were provided as input (issue AC), not identified during
  exploration.
- **No Artifact**: Demoted. Architectural decisions were made during exploration that future
  contributors need; direct implementation without documentation risks re-litigating them.
