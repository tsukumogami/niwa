# Lead: What session state model accommodates an attach lock, and which existing states permit attach?

## Findings

### Current state machine (as implemented today)

Defined in `/home/dgazineu/dev/niwaw/tsuku/tsuku-4/public/niwa/internal/mcp/session_lifecycle.go:153-158`:

```go
const (
    SessionStatusActive    = "active"
    SessionStatusEnded     = "ended"
    SessionStatusAbandoned = "abandoned"
)
```

The `SessionLifecycleState` struct (lines 30-47) carries a single string `Status` field — there is no orthogonal axis. Schema version is `V: 1` (line 31). The contributor note in `docs/guides/sessions.md:303-308` says additions require bumping `V` and handling zero-values when reading existing files.

**Where `Status` is set:**
- `internal/mcp/session_lifecycle.go:171` — `NewSessionLifecycleState` initializes to `SessionStatusActive`.
- `internal/mcp/handlers_session.go:264` — `handleDestroySession` sets `SessionStatusEnded`.

**That is the entire set of writers.** No code path writes `SessionStatusAbandoned`. The constant exists, the destroy idempotency check at `handlers_session.go:249` treats it as terminal, and `niwa_list_sessions` filters accept it (`server.go:391`, `:396`), but no writer produces it. It is a reserved-but-unused enum value.

**Where `Status` is read for gating:**
- `internal/mcp/handlers_task.go:274-275` — `resolveCreationInboxDir` returns `SESSION_INACTIVE` when `session.Status != SessionStatusActive`. **Anything that is not exactly `active` blocks new task delegation.** This is the only pre-action gate today.
- `internal/mcp/handlers_session.go:249` — destroy is idempotent on `ended` or `abandoned`.
- `internal/mcp/handlers_session.go:37`, `internal/cli/session_lifecycle_cmd.go:136` — read-only filter for listing.

**Effective transition graph today:**

```
                  niwa_create_session
                          │
                          ▼
                      [active]
                          │
                  niwa_destroy_session
                          │
                          ▼
                       [ended]   (terminal; file retained, worktree removed)

[abandoned]   reserved constant, no writer, no transition
```

The `DESIGN-mesh-session-lifecycle.md:205` line "Terminal states (`ended`, `abandoned`) are written once and never updated" is aspirational — the design doc reserved `abandoned` but never specified a writer in V1. `DESIGN-niwa-destroy.md` (the most recent destroy rework) does not introduce an `abandoned` writer either; a grep across that file finds zero hits for "abandoned". Issue #117 itself in its open questions section asks "should attach refuse if the session is `ended` or `abandoned`?" implying the author also believed `abandoned` was reachable — it is not, today.

### Two natural shape options for the attach lock

**Option A — New `Status` value (`attached` and/or `suspended-by-human`).**

Pros: matches the "single Status enum" precedent of every session-related code path. The `SESSION_INACTIVE` gate at `handlers_task.go:274` already does the right thing — any non-`active` status blocks new delegation, so `Status == "attached"` would automatically queue/reject coordinator delegations to the locked session without changing the gate. Filter by `--status attached` works for free.

Cons: conflates two semantics that the issue's reporter explicitly distinguished — "lifecycle" (created → live → terminal) vs. "availability for mesh use." A session that is `attached` is still alive, still has a daemon, still owns a worktree, still has a Claude conversation, and is expected to return to `active`. Lumping it into the same enum as `ended` (terminal, worktree deleted) means every reader of `Status` has to learn that some non-`active` values are terminal and others are recoverable. Issue #111 wants `daemon.alive` as a separate field precisely because session lifecycle and runtime availability are different axes.

Also: with one Status field, expressing "attached AND daemon-dead" or "active but coordinator should not delegate right now" is impossible without further tuple-encoding inside the string.

**Option B — Orthogonal `availability` (or similar) field, leave `Status` as the lifecycle marker.**

Adds one field — e.g.

```go
Availability string `json:"availability,omitempty"` // "" or "free" or "attached"
AttachOwner  string `json:"attach_owner,omitempty"` // PID@host or token
AttachStartedAt string `json:"attach_started_at,omitempty"`
```

`Status` keeps its current three values (with `abandoned` still reserved for future "creator died with worktree intact"). Pre-attach validation reads `Status == active && Availability != "attached"`. Coordinator's delegation gate adds one more line: `if session.Availability == "attached" → return SESSION_LOCKED`. Listing surfaces `availability` as a separate column or sub-object.

Pros: aligns with #111's direction (separate `daemon.{alive, last_claim_at, ...}` sub-object alongside `status`). Issue #111's proposed shape is explicitly:

```json
{ "status": "active", "daemon": { "alive": true, ... } }
```

with the comment "`status` remains the lifecycle marker (active/ended/abandoned). `daemon.alive` is the runtime health." That is the same axis-separation argument applied to a different concept; the attach lock is a third axis (human availability) that the same shape accommodates cleanly.

Cons: more fields, more zero-value handling on read, more places to keep in sync. The schema-version bump (`V: 1 → V: 2`) is non-trivial — `docs/guides/sessions.md:303-308` mandates it. Also: existing CLI/MCP filters (`--status active`) don't transparently exclude attached sessions from coordinator pickers; new logic is needed.

### Recommendation (with reasoning from precedent)

**Option B (orthogonal field) is consistent with how the codebase already plans to model runtime state.** The argument is not preference — it is direct precedent from issue #111, which is already on the roadmap and proposes a parallel `daemon` sub-object precisely because `status` is overloaded. The PRD for #117 should commit to a parallel field rather than overload `Status`, because:

1. Issue #111's `daemon` axis and #117's attach-lock axis are independent: a daemon can be alive while the session is attached (lock held by human), or dead while unattached (mesh unable to deliver). Both must be representable.
2. The destroy gate (`handlers_session.go:249`) and the delegation gate (`handlers_task.go:274`) treat any non-`active` value as terminal-or-inactive — collapsing "attached" into Status would require both gates to learn about attach-as-pause.
3. The schema version bump is required by either approach (new value or new field), so versioning cost is equal. Field addition is the additive change; new enum value is also additive but interacts with existing readers.

### Pre-attach validation — which states permit attach?

| State | Permit attach? | Reasoning |
|-------|---------------|-----------|
| `active` | Yes | Obvious case. Worktree exists, daemon is (probably) running, Claude conversation ID may be present after first task. |
| `ended` | No (in V1) | `handleDestroySession` at `handlers_session.go:276-277` runs `git worktree remove --force <worktreePath>` and `handlers_session.go:287` deletes the `session/<id>` branch. Forensic attach is **not feasible** because the working directory and branch are gone. Only the state JSON file remains. The state file has `claude_conversation_id`, but `claude --resume` needs a CWD to launch into; there is no CWD. The PRD should refuse attach on `ended` and explain why in the error message. |
| `abandoned` | N/A (no writer) | Today no path produces this state. If it is ever introduced (e.g., for "creator process died but worktree intact"), the attach decision depends on whether the worktree survives. Recommend: PRD defers `abandoned` attach semantics until a writer for `abandoned` is specified. |

The `ended` case in particular surprises issue #117's author, who asked about forensic attach in the open questions; the codebase makes it impossible because the worktree is force-removed on destroy.

### Cross-reference with #111

Issue #111 explicitly proposes:

```json
{
  "status": "active",
  "daemon": {
    "pid": 12345,
    "alive": true,
    "started_at": "...",
    "last_claim_at": "...",
    "last_progress_at": "...",
    "watcher_count": 1
  }
}
```

with the line "`status` remains the lifecycle marker (active/ended/abandoned). `daemon.alive` is the runtime health." That is the architectural precedent for keeping `Status` as the lifecycle axis and adding parallel runtime/availability sub-objects. The attach lock should follow the same shape — either as a sibling sub-object (`attach: { owner, started_at, ... }`) or a single `availability` string field if the PRD prefers a flat schema.

Issue #117's "Related" section already calls out the coordination: "Coordinated design with #111 makes this cheaper." The PRD should commit to the parallel-axis shape so #111 and #117 land schema changes that compose rather than collide. If #111 lands first with `daemon` as a sub-object, the attach feature should add `attach` (or fold into a `runtime` umbrella) the same way.

### State transition diagram including attach/detach

Assuming Option B (parallel axis):

```
Lifecycle axis (Status):

   create       destroy
─▶ active ────▶ ended (terminal)
   │
   └─▶ (abandoned reserved; no writer in V1)


Availability axis (orthogonal; only meaningful while Status==active):

   ┌──────────────────────────────────┐
   │                                  │
   ▼   attach                         │
  free ──────────▶ attached           │
   ▲                  │               │
   │   detach         │               │
   └──────────────────┘               │
                                      │
   stale-lock recovery:               │
   attached + owner-pid-dead ─────────┘
   (transition: attached → free)
```

**Recovery from human terminal crash mid-attach (boundary with the lock-semantics lead):**

The attach record stores the owner identity (e.g., owner PID + start time, mirroring the existing `CreatorPID` + `CreatorStartTime` pattern at `session_lifecycle.go:163-165` already used with the existing `IsPIDAlive` helper for detection of dead session creators). Recovery is a **passive liveness check**, not an active heartbeat:

- On the next coordinator call that reads the session (e.g., `niwa_list_sessions`, `niwa_delegate(session_id=X)`, or another `niwa session attach`), the reader calls `IsPIDAlive(AttachOwnerPID, AttachOwnerStartTime)`.
- If the owner is dead, the reader treats `Availability == attached` as `free` for gating purposes, and may opportunistically clear the field via the same atomic temp+rename pattern used elsewhere.
- An explicit `niwa session attach --force` (or `niwa session detach --force`) provides a human-driven release.

This avoids inventing a heartbeat protocol — niwa already uses creator-PID liveness as its standard staleness check (`session_lifecycle.go:165`, `IsPIDAlive` is referenced throughout the lifecycle module's docstrings). The boundary with the lock-semantics lead: that lead owns the *exact* lock representation (file-on-disk vs. status-field vs. flock-on-worktree) and the staleness-recovery protocol details; this lead simply notes that staleness *must* be representable, and the precedent says it should be PID-based.

## Implications

**For the PRD's "session-state-model" open question:**

- The PRD should commit to an **orthogonal field for the attach lock**, not a new `Status` value, on the precedent of #111's `daemon` sub-object. State the architectural reason explicitly: lifecycle (created/live/terminal) and availability-for-mesh-use (free/attached) are independent axes; `status` already disambiguates lifecycle and overloading it conflates the two. This will frame the PR review correctly.
- The PRD must commit to bumping `SessionLifecycleState.V` from 1 to 2 and document zero-value handling for the new field(s) per `docs/guides/sessions.md:303-308`. Empty `Availability` reads as `free`. This is non-negotiable per the contributor notes.
- The PRD should defer the exact shape of the new field (single `availability` string vs. `attach` sub-object with owner/started_at/etc.) to coordinated design with #111. If #111 ships first with a `daemon` sub-object, mirroring that shape (an `attach` sub-object) is cheapest. If #111 hasn't shipped, the PRD can scope a minimal flat field and let #111 reshape later.

**For the PRD's "pre-attach-validation" open question:**

- The PRD must commit: **attach permitted only when `Status == active`**. Refuse on `ended` (and any future `abandoned`) with an explicit error message.
- The PRD should explain *why* forensic attach on `ended` is infeasible: `niwa_destroy_session` runs `git worktree remove --force` and `git branch -d/-D session/<id>`. Both the working directory and the branch are gone; `claude --resume` has no CWD to launch into. Forensic inspection of an ended session is reading the JSON state file, not attaching.
- The PRD can defer `abandoned` semantics — there is no writer for it today, so the PRD does not need to define attach behavior for a state that does not exist. A line of "if `abandoned` is introduced in the future, attach behavior will be defined alongside that introduction" is sufficient.

**What the PRD must commit to (not defer):**
1. The orthogonal-axis decision (Status vs. availability separation).
2. `Status == active` is the only state that permits attach in V1.
3. Stale-attach recovery uses PID-based liveness (`IsPIDAlive` precedent) rather than a heartbeat protocol.
4. Schema version bump V: 1 → 2.

**What the PRD can defer (with a stated rationale):**
1. Exact field shape (flat `availability` string vs. nested `attach` sub-object) — defer to coordinated review with #111.
2. `abandoned` attach semantics — defer until `abandoned` has a writer.
3. Whether the lock owner identity is PID+start-time, a token, or both — boundary with the lock-semantics lead.

## Surprises

1. **`abandoned` is a reserved constant with no writer.** The `DESIGN-mesh-session-lifecycle.md` design doc reserves it ("Terminal states (`ended`, `abandoned`) are written once and never updated"), `niwa_list_sessions` accepts it as a filter (`server.go:391`, `:396`), and the destroy idempotency gate accepts it (`handlers_session.go:249`), but a `grep -rn "Status = SessionStatusAbandoned"` returns zero matches in non-test code. Issue #117's open question "should attach refuse if `ended` or `abandoned`?" assumes `abandoned` is reachable, which it is not. The PRD can punt on `abandoned` semantics entirely without losing correctness.

2. **The forensic-attach scenario in #117 is unambiguously infeasible.** `handleDestroySession` at `handlers_session.go:276-277` runs `git worktree remove --force` and `:287` deletes the session branch — both the CWD and the ref `claude --resume` would need are destroyed. The state JSON file persists with `claude_conversation_id`, but conversation resume requires a working directory. The PRD should not even open the door on this; it would require redesigning destroy to retain the worktree, which contradicts the explicit goal at `DESIGN-mesh-session-lifecycle.md:489-509` of cleaning up worktrees on destroy.

3. **`Status != active` is a single delegation gate.** `handlers_task.go:274-275` returns `SESSION_INACTIVE` for any non-`active` status. A new `attached` *Status value* (Option A) would unintentionally satisfy this gate (delegation already blocked) — which is what we want — but would also be misclassified by the destroy idempotency gate (`handlers_session.go:249`) as terminal. Option B (orthogonal field) requires explicit gate addition but is unambiguous about which axis carries the meaning.

4. **The codebase's staleness primitive is already PID-based** (`IsPIDAlive` referenced from session lifecycle, used to validate creator liveness via `CreatorPID` + `CreatorStartTime`). Stale-attach recovery does not need to invent anything new — reuse the same PID-comparison pattern with an `AttachOwnerPID` + `AttachOwnerStartTime` pair.

## Open Questions

1. **Coordinator-side lock visibility.** From inside the attached session, the issue says mesh queue is invisible (item 3 in the locked-in defaults). But what does the *coordinator* see? `niwa_list_sessions` lists the session — does it surface `availability=attached` to the coordinator, and if so, does the coordinator's mesh skill instruct it to back off, or does the coordinator silently fail with `SESSION_LOCKED` on each delegate? This is a UX/skill-update question; the PRD should resolve it but the answer doesn't change the state model.

2. **Should `attach` block destroy?** If a human is attached and the coordinator (or another human) calls `niwa_destroy_session`, does the destroy succeed (kicking the human out and removing the worktree under their feet) or does it fail with `SESSION_ATTACHED`? The lock-semantics lead probably owns this, but the state model determines whether destroy *can* express "session is locked, refuse." Recommend: yes, destroy should respect the lock unless `--force`.

3. **What happens on detach if the daemon died while the human was attached?** When attach releases, `Availability` returns to `free`, but `Status` is still `active` and the daemon may be dead. This is the #111 territory; the PRD should not solve it, but should note that a clean-detach path that leaves a dead-daemon session in the registry is the existing gap #111 identifies.

4. **Multi-attach prevention semantics** — strictly a state-model concern: should `Availability == attached` with a stale owner be auto-cleared on the next attach attempt, or require an explicit `--force`? Probably auto-clear (matches niwa's existing "PID-based staleness, fall through to retry" patterns), but worth confirming with the lock-semantics lead.

## Summary

Today's `Status` enum has three values (`active`, `ended`, `abandoned`) but only `active` and `ended` have writers — `abandoned` is a reserved constant with no producer, so the PRD can ignore it. The attach lock should land as an **orthogonal field** (precedent: issue #111's `daemon` sub-object, which separates lifecycle from runtime health on the same architectural argument); attach must be permitted only when `Status == active` because `niwa_destroy_session` removes the worktree and branch on `ended`, making forensic attach physically impossible without redesigning destroy. The biggest open question is the exact field shape — flat string vs. nested sub-object — which should be coordinated with #111 to avoid colliding schema-version bumps.
