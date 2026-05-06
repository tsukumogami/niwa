# Exploration Decisions: delegation-model-isolation

## Round 1

- **Opt-in session model is an oversight, not a deliberate design choice**: The PRD's backward-compatibility rationale for opt-in sessions (R13) is weak because niwa has no users. The contradiction between Goal 3 ("main clone always stays on main") and R13 (untagged delegates run in the main clone) was accepted knowingly but on a rationale that doesn't hold. Revisable without migration debt.

- **Implicit per-task session creation is the wrong fix**: Implicit sessions that auto-destroy after one task defeat the session model's primary value (multi-task conversation continuity via --resume). Implicit sessions that persist accumulate without meaningful purpose strings or clear ownership. Neither option works within the existing session model.

- **Ephemeral task isolation and persistent session continuity are distinct concepts**: These should not be bundled into the same abstraction. The current session model is designed for multi-task continuity. A per-task ephemeral worktree would need a different schema, different lifecycle, and different ownership model.

- **Pursuing mandatory sessions**: `niwa_delegate` without `session_id` should be rejected. The details — how to reject, what read-only tasks do, what the coordinator UX is — are open and need a design doc.

- **Human-in-main-clone case is separate from delegation isolation**: A one-time notice at `niwa go <repo>` pointing toward `niwa session create` is the appropriate intervention. Should not block, as read-only exploration is legitimate.

- **Apply config installation into non-default-branch clones is a separate correctness bug**: `niwa apply` unconditionally writes hooks, settings, and CLAUDE.md to a repo directory regardless of branch state. This is a quiet correctness problem that should be tracked separately.
