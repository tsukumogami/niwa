# Decision 2: Dangling task lifecycle shape

## Question

How should the dangling task classification be modeled so the API surface tells the truth and operators have a recovery path? Today `dangling` is a daemon-only filesystem quarantine (`inbox/<role>/dangling/<task-id>.json`) that is invisible to `state.json`; `niwa_query_task` and `niwa_list_outbound_tasks` keep reporting `state="queued"`, `niwa_cancel_task` returns the contradictory `{status:"too_late",current_state:"queued"}`, `niwa_await_task` blocks until timeout, and `niwa_update_task` half-succeeds (mutates state.json, then trips on the inbox stat). The fix must compose with `niwa_redelegate` (#114), which the lead-4 research already names as the documented recovery primitive.

## Options

### A. Make `dangling` a real state.json state

**Mechanics.** When `handleInboxEvent` (`internal/cli/mesh_watch.go:776-803`) detects `state.json` missing for a `task.delegate` envelope, the daemon transitions state.json to a new terminal state — most honestly `abandoned` with `reason="taskstore_lost"` (reusing the existing `TaskStateAbandoned` constant in `internal/mcp/types.go:173-200`) rather than introducing a sixth state. The envelope still moves to `inbox/dangling/` for forensic preservation, but the rename is now bookkeeping, not the primary signal. If state.json is *missing* (the genuine `taskstore_lost` case), the daemon recreates a minimal stub (`taskDir + state.json` with `state=abandoned`, `reason=taskstore_lost`, transition log seeded `unknown -> abandoned`). If state.json is *present* but the task dir was found in some inconsistent state (the field-repro flagged in §4 of lead 4: state.json survived, envelope was hand-seeded), the daemon transitions `queued -> abandoned`. `niwa_query_task`, `niwa_list_outbound_tasks`, `niwa_await_task` then naturally surface `state="abandoned"` with the diagnostic reason. `niwa_cancel_task` and `niwa_update_task` already treat terminal states correctly (they read state.json first; the inbox-rename ENOENT path is no longer reached because the early state guard short-circuits).

**Pros.**
- API truthfulness: every read tool returns the same word for dangling envelopes — `abandoned` — and `reason="taskstore_lost"` distinguishes it from worker-driven abandonment.
- No new on-disk schema. Reuses the existing flat task store at `<taskStoreRoot>/.niwa/tasks/<id>/{envelope.json,state.json}` and the existing `TaskStateAbandoned` constant.
- Unblocks await: a delegator calling `niwa_await_task` on a dangling task returns immediately with `{status:"abandoned",reason:"taskstore_lost"}` instead of blocking 10 minutes.
- Resolves the `{too_late, queued}` contradiction without changing cancel semantics: cancel sees `state=abandoned`, refuses cleanly with the existing `isTaskStateTerminal` guard pattern.
- Composes naturally with `niwa_redelegate` — redelegate already plans to read the source envelope from `<taskStoreRoot>/.niwa/tasks/<source>/envelope.json` regardless of state (lead-4 §4: "task store is flat by task_id, not partitioned by state"), and its validation lists `abandoned`, `cancelled`, `completed` as legal source states.

**Cons.**
- Mutates state.json from the daemon, which today only the MCP server transitions. New write-path needs flock discipline (the existing `taskstore.go` write helpers already provide it; the daemon would call into the MCP package or a shared `mesh/taskstore` helper).
- The `taskstore_lost` recreate-stub case does fabricate `envelope.json`-adjacent data the daemon never wrote. Mitigated by writing only `state.json` (the envelope is not needed for terminal-state queries to succeed; `formatQueryResult` reads only `state.json` fields).
- Permanently classifies the task `abandoned` — irreversible from state.json's perspective. Operators recover only via redelegate (#114), which produces a *new* task with `redelegated_from`. This is by design but worth naming.
- Introduces a daemon-internal MCP-server call (or shared package) that the watch loop didn't previously have. Acceptable; the daemon already shares types and authorization helpers with the MCP server via `internal/mcp`.

**Risk.** Low-medium. The mechanical change is small, the state machine stays at five legal states, and the daemon-write path can be unit-tested through the existing `mesh_watch_test.go` fixtures (`TestHandleInboxEvent_DanglingEnvelope` at line 743). Risk concentrates in the `taskstore_lost` recreate-stub edge case where state.json must be authored by the daemon — prior art in `createTaskEnvelope` (`handlers_task.go:177-258`) is the obvious template.

### B. Resurrect primitive only

**Mechanics.** Keep `dangling` as today — filesystem quarantine, state.json untouched. Add `niwa_resurrect_task(task_id)` (or CLI-only `niwa task resurrect`) that does the inverse rename (`inbox/dangling/<id>.json` -> `inbox/<id>.json`) under flock, *only* if state.json shows `state=queued` and exists. Operators run this after manually restoring state.json or after the underlying cause clears.

**Pros.**
- Preserves quarantine semantics that prevent inotify thrash (the original motivation cited at `mesh_watch.go:789-791`).
- Smallest surface change: one new MCP tool (or CLI subcommand), no daemon write paths into state.json.
- Operator-driven recovery is auditable — a `TransitionLogEntry` with `kind:"resurrected"` records the operator's intervention.

**Cons.**
- **Does not fix the API-truthfulness goal stated in the prompt.** Until resurrect is invoked, `niwa_query_task`/`niwa_list_outbound_tasks`/`niwa_await_task`/`niwa_cancel_task`/`niwa_update_task` keep lying. The `{too_late, queued}` contradiction persists.
- Resurrect requires the operator to *first* restore state.json by hand for the `taskstore_lost` case (the dominant field repro per lead 4 §4, where `.niwa/tasks/` was wiped). The tool is a no-op without manual filesystem surgery first.
- Double-classification loop: even after a successful resurrect, if state.json is still missing the daemon will deterministically re-quarantine within milliseconds. Resurrect must front-load the state.json check, but an operator who *did* restore state.json could just as easily have restored the inbox file — the tool's marginal value over `mv` is small.
- Skill text and `docs/guides/sessions.md` still need to document `dangling` as a real-but-undocumented limbo, contradicting the prompt's "honest about the lifecycle" goal.
- Composes awkwardly with `niwa_redelegate`: redelegate accepts dangling-source tasks (lead 4 §4, fifth bullet), but resurrect competes for the same recovery role. Two recovery primitives split documentation and operator habit.

**Risk.** Low mechanically, high on the goal. The primitive is easy to implement; it just doesn't satisfy the constraint that "API surface tells the truth."

### C. Both

**Mechanics.** A's daemon-driven `state=abandoned` transition *plus* B's resurrect primitive layered on top, where resurrect is gated on `state=queued` (i.e., the unusual hand-seeded-envelope case from lead 4 §4 where state.json survived intact and the only fix is moving the file back).

**Pros.**
- Covers both the API-truthfulness goal (A) and the rare operator-recovery case where the field repro really is "envelope was misclassified, state.json was always fine."

**Cons.**
- Two competing recovery paths: resurrect (rename file) vs redelegate (new task with `redelegated_from`). The prompt explicitly names redelegate as "the documented recovery path." Adding a second one fragments documentation.
- Resurrect's narrow surface area shrinks further once A lands: A transitions state.json to `abandoned` for the missing-state.json case, leaving resurrect applicable only to the hand-seeded-envelope edge that operators almost never hit in production. A tool that exists for a corner case attracts misuse.
- Skill text now has to describe both: "if you see X, redelegate; if you see Y, resurrect." Increases cognitive load for callers who already have to reason about queued/running/terminal.

**Risk.** Higher than A alone, primarily through documentation and operator-confusion cost. Mechanically the same risks as A plus a small additional surface for the resurrect tool.

### D. Make dangling structurally impossible

**Mechanics.** Eliminate the trigger by changing the daemon so it cannot encounter the missing-state.json case. Two sub-shapes:

- **D1 (fatal-error variant):** the watch loop treats `state.json` missing for an owned `task.delegate` envelope as a workspace-fatal error — log, alert, refuse to claim, do not quarantine. The envelope sits in `inbox/<id>.json` until an operator either restores state.json (envelope gets claimed normally) or deletes the envelope.
- **D2 (refuse-write variant):** the daemon refuses to write any state.json transitions if the corresponding task dir disappears mid-flight. Doesn't address the entry-time case (envelope already in inbox before daemon starts).

**Pros.**
- Strongest invariant: dangling literally cannot exist as a category. No new state, no new tool.
- Removes the silent quarantine that consumes disk forever (lead 4 §13: "Dangling tasks consume taskstore disk forever").

**Cons.**
- **The trigger is not fully under the daemon's control.** Lead 4 §4 enumerates the failure modes: manual `rm -rf .niwa/tasks/`, partial workspace destroy, hand-seeded test fixtures, fresh checkout that wipes the task store while preserving inbox files. No daemon code can prevent these — they happen *outside* the daemon's process. So D1 still has to do *something* when the daemon observes the inconsistency at startup or at fsnotify time. Refusing to claim leaves the file in `inbox/`, and `scanExistingInboxes` will retry it on every daemon restart, producing log spam without resolution.
- D2 doesn't address the dominant field repro at all — by the time the daemon is observing the inbox, the task dir loss has already happened.
- Any "fatal error" framing punishes the rest of the workspace for one bad envelope: a single hand-seeded test fixture would prevent the daemon from processing legitimate sibling envelopes in the same inbox.
- Does not provide a path forward for operators in the `taskstore_lost` case. They still need to either delete the orphan envelope or restore state.json by hand. No principled API affordance for either.
- `niwa_redelegate` (#114) loses its ability to accept dangling source tasks because there are no dangling tasks — but also loses the ability to handle `taskstore_lost` recovery, because state.json is missing and `ReadState` fails. The redelegate composition story regresses.

**Risk.** High. Removes the symptom by removing the category, but the category exists because the underlying inconsistency exists, and the inconsistency is caused by external operations the daemon can't prevent.

## Chosen

**Option A** — make `dangling` resolve into a real `state.json` state, specifically `abandoned` with `reason="taskstore_lost"` (or `reason="orphan_envelope"` for the rare hand-seeded case where state.json was `queued` but the envelope was misclassified). The filesystem quarantine (`inbox/dangling/`) is preserved as a forensic side-effect but no longer carries semantic weight.

## Rationale

The prompt names two satisfaction criteria: API truthfulness and composition with `niwa_redelegate`. Option A is the only option that satisfies both directly.

**On API truthfulness.** The five tools enumerated in the prompt all already read state.json correctly for terminal states (the `isTaskStateTerminal` guards in `handleCancelTask`/`handleUpdateTask` short-circuit before the inbox-rename code that produces today's contradiction). Once state.json says `abandoned`, the tools speak the same language without further changes:

- `niwa_query_task` returns `{state:"abandoned", reason:"taskstore_lost"}` via `formatQueryResult` (`handlers_task.go:937-960`).
- `niwa_list_outbound_tasks` returns rows with `state="abandoned"` because it reads state.json directly (`handlers_task.go:710-771`).
- `niwa_await_task` exits immediately because its terminal-state guard at `handlers_task.go:427-430` already handles abandoned.
- `niwa_cancel_task` refuses with a clean `state=abandoned` answer instead of the contradictory `{too_late, queued}`. The fix is an early state guard (mirroring the existing terminal check pattern at `handlers_task.go:840-870` for update).
- `niwa_update_task` refuses cleanly for the same reason — no half-mutation.

**On composition with `niwa_redelegate`.** Lead 4 §4 has already specified that redelegate reads `<taskStoreRoot>/.niwa/tasks/<source>/envelope.json` regardless of state, validates that the source state is non-active (`abandoned`, `cancelled`, `completed`, plus the dangling-as-`queued` carve-out), and creates a new task with `redelegated_from`. Option A *simplifies* that validation: dangling is now `abandoned`, which is already on redelegate's allow-list. The carve-out for `state=queued && file in inbox/dangling/` (lead 4 §4, fifth bullet) goes away. Redelegate's call site no longer has to inspect inbox subdirectories to detect dangling — it just reads `state.json` and proceeds.

**On the recovery story for skill docs and `docs/guides/sessions.md`.** The honest sentence becomes one line: "If a task's task store is lost (e.g., manual cleanup, partial workspace destroy), the daemon transitions the task to `abandoned` with `reason=taskstore_lost`. Use `niwa_redelegate` to re-issue with the original body." No mention of `dangling` or quarantine is required in user-facing text; the inbox subdir is an implementation detail for forensics, not a lifecycle stop.

**Why not B alone?** Resurrect doesn't satisfy the API-truthfulness goal. Until the operator runs it, every read tool keeps lying. The prompt explicitly calls out that the surfaces must be consistent, not that there must be a way to make them eventually consistent. Resurrect also competes with redelegate for the recovery role, and the prompt names redelegate as the documented path.

**Why not C?** The marginal surface area of resurrect after A lands is the rare hand-seeded-envelope case where state.json was perfectly fine and only the inbox classifier was wrong. Operators hitting that case can `mv inbox/dangling/<id>.json inbox/<id>.json` themselves (the daemon would briefly re-quarantine, but if state.json really is intact, A's logic would transition to `abandoned` — which is exactly the answer the operator wanted, just via a different path). The cost-benefit doesn't justify a second recovery primitive.

**Why not D?** The trigger is exogenous to the daemon (manual deletion, fresh checkout, partial destroy, test fixtures). D can suppress the *symptom* but not the *underlying inconsistency*; refusing to classify just leaves the orphan envelope sitting in `inbox/` to be rediscovered every daemon restart. D also breaks the redelegate composition story by leaving state.json missing, making `ReadState` fail and `niwa_redelegate` unable to recover the body. A is strictly more powerful than D for the same risk surface.

## Composition with niwa_redelegate (#114)

Lead 4 §4 gives the redelegate happy path: `ReadState(taskDirPath(s.taskStoreRoot(), source_task_id))` reads the source envelope and state regardless of state, the handler merges body overrides, runs the `required_skills` gate, and calls `createTaskEnvelope` with `from` set to the caller and `redelegated_from` on the new envelope. Option A interacts cleanly:

- **Validation list extends naturally.** Redelegate's source-state allow-list already includes `abandoned`. Dangling tasks now appear in that bucket without a special case. The lead-4 carve-out for `state=queued && inbox/dangling/<id>.json present` is no longer needed — that inconsistent state cannot exist after A lands.
- **Body recovery works.** `taskstore_lost` is the worst case, where the daemon recreates only state.json (not envelope.json). Redelegate must handle this gracefully: if `envelope.json` is missing, redelegate fails with a structured error like `SOURCE_BODY_LOST` and asks the caller to provide the body explicitly via `body_overrides`. This is rare and tractable; in the common dangling cases (orphan envelope where the task store survived), envelope.json is intact and body recovery works as designed.
- **Audit chain is preserved.** The new task's envelope carries `redelegated_from: <original-task-id>`. The original task's state.json carries the `reason="taskstore_lost"` annotation. An operator (or future tooling) can walk the chain.
- **No competing recovery path.** Redelegate is the only documented operator-facing recovery primitive. `dangling` becomes a daemon-internal classification that resolves through state.json without operator action. Skill docs and `docs/guides/sessions.md` describe one path for taskstore loss: redelegate.

## Confidence

**High.** Option A reuses existing primitives (`TaskStateAbandoned`, `state_transitions`, the flock'd state.json writer in `taskstore.go`), produces structurally consistent answers across all five tools without further per-tool changes, and composes with `niwa_redelegate` (#114) without special-casing. The risk concentrates in the daemon-write path and the `taskstore_lost` recreate-stub case, both of which have direct prior art in `createTaskEnvelope`.

## Assumptions

1. The daemon may legitimately write `state.json` transitions. The daemon already shares the `internal/mcp` package surface (types, authorization helpers); a shared `taskstore` helper that flocks and appends a `TransitionLogEntry` is acceptable. If the project policy is "only the MCP server may write state.json," Option A would need a small refactor to route the daemon's transition through an in-process MCP call — adds plumbing but does not change the answer.
2. `TaskStateAbandoned` with `reason="taskstore_lost"` is more honest than a sixth `dangling` state. The five-state alphabet is the right granularity; the distinguishing detail belongs in `reason`. If the project insists on a sixth state for symmetric naming with the inbox subdir, the structural argument is the same — replace `abandoned + reason` with a new `dangling` constant in `validTaskStates` and update `isTaskStateTerminal`.
3. Redelegate (#114) handles the `envelope.json` missing case (the rare `taskstore_lost` recreate-stub) by returning a structured error rather than crashing. Lead 4 §4 implicitly assumes envelope.json is readable; this report flags the edge case explicitly.
4. The `inbox/dangling/` filesystem layout can be retained as forensic-only without operator confusion. Skill docs would not mention it; the audit fields (`reason="taskstore_lost"`) carry the user-facing signal.
5. The `taskstore_lost` recreate-stub is acceptable on the daemon write path — the daemon authors only `state.json` (not `envelope.json`) when the task dir was wiped, and `formatQueryResult` reads only state.json fields, so query/list/await/cancel/update all work without a fabricated envelope.
