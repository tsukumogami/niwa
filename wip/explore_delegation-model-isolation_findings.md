# Exploration Findings: delegation-model-isolation

## Core Question

Should all `niwa_delegate` calls be session-bound (worktree-isolated) by default, rather than opt-in via explicit `session_id`? And what is the right UX for humans working directly in a repo's main clone?

## Round 1

### Key Insights

- **The PRD contains an explicit logical contradiction** (prior art lead): Goal 3 states "the main clone always stays on main" while R13 states untagged delegates run in the main clone. These cannot coexist. The PRD resolved the tension via backward compatibility — a rationale that doesn't hold because niwa has no users.

- **Implicit session creation per task is architecturally mismatched with the session model** (implicit delegation consequences + implicit lifecycle leads): Sessions exist for multi-task conversation continuity via `--resume`. An auto-created session that destroys after one task cannot provide continuity. Persistent implicit sessions accumulate without meaningful purpose strings or clear ownership. Neither variant works within the existing design.

- **"Ephemeral task isolation" and "persistent session continuity" are distinct concepts needing separate designs** (implicit lifecycle lead): The current schema has no `auto_destroy` flag, no daemon callback on task terminal state, and `handleDestroySession` is an MCP tool not an internal callable. Ephemeral per-task worktrees need different schema, lifecycle, and ownership from persistent sessions.

- **Main clone read-only invariant is convention-only with no enforcement — and there is a separate apply bug** (main clone readonly lead): `niwa apply` installs config files unconditionally regardless of branch state. This is a quiet correctness problem independent of the delegation isolation question.

- **CLI-created and coordinator-created sessions are structurally identical and fully interoperable** (session conflict lead): Same `handleCreateSession` code path; only `parent_session_id` differs. A coordinator can delegate into a human-created session with no restriction. No conflict, but no ownership link either.

- **Prior art is one-directional: systems move from opt-in to mandatory isolation, never the reverse** (prior art lead): Every CI/CD system that started with shared workspaces eventually mandated isolation. The niwa design documents name "main clone branch contamination" as an observed failure mode, not a hypothetical.

### Tensions

- **Isolation goal vs. backward compat decision**: R13 and Goal 3 directly contradict. Accepted on weak grounds; revisable.
- **Full session overhead vs. read-only tasks**: A worktree per task (4-10 seconds, full daemon) is wasted cost for delegations that make no git changes.
- **Auto-destroy safety vs. data safety**: `git branch -d` guard consistently blocks automatic cleanup for in-progress work. `force=true` risks silent data loss. No middle ground without human input.
- **Ephemeral isolation vs. persistent session continuity**: Bundling these in one concept makes both worse.

### Gaps

- Apply config installation into non-default-branch clones: a correctness bug uncovered but out of scope here.
- `--parent` flag for `niwa session create` is in R16 but not implemented.
- R19 session-tree routing enforcement is in the PRD but not implemented in `handleAsk`.

### Decisions

- Opt-in model is revisable without migration cost.
- Implicit per-task sessions are the wrong fix.
- Direction chosen: **mandatory sessions** — `niwa_delegate` without `session_id` should be rejected.
- Human-in-main-clone: one-time notice at `niwa go <repo>`, not blocking.

### User Focus

User directed: pursue mandatory sessions.

## Decision: Crystallize

## Accumulated Understanding

The PRD's opt-in session model (R13) contradicts the stated goal (Goal 3) that the main clone always stays on main. The backward-compatibility rationale is weak given niwa has no users. The right fix is not implicit session creation (which defeats the session model's continuity value and has no clean lifecycle answer) but mandatory explicit session provisioning: `niwa_delegate` without `session_id` should be rejected. The design — what the rejection looks like, how read-only tasks opt out, what happens to coordinator prompts — is completely open and needs a design doc. Separately, the human-in-main-clone case is best addressed by a one-time notice at `niwa go <repo>`, not enforcement.
