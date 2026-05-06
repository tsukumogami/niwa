# Lead: Prior art on worktree isolation models

## Findings

### CI/CD Systems: Isolation Is Mandatory, Not Opt-In

GitHub Actions, Jenkins (with ephemeral agents), and most modern CI systems treat
job isolation as mandatory infrastructure — not a per-job option. Each job gets a
fresh runner (either a new VM, a container, or a clean ephemeral workspace). The
rationale is consistent across all systems:

- **State pollution.** Without isolation, artifacts, env vars, checked-out branches,
  and partially-applied configs from job A bleed into job B. The failure mode is
  subtle: job B works locally (clean environment) but fails in CI (contaminated
  environment), or worse, passes with incorrect behavior because it inherited a
  precondition from a prior job.
- **Parallel safety.** Two jobs running concurrently on a shared working tree produce
  undefined behavior: conflicting branch checkouts, interleaved file writes, lock
  contention on `.git/index`. Git itself does not protect against this — multiple
  processes in the same working tree is not a supported mode of operation.
- **Auditability.** Ephemeral environments make failure root-cause analysis easier:
  a job's environment is completely defined by its inputs, not by accumulated prior
  state.

GitHub Actions explicitly documents that jobs in the same workflow do NOT share
a workspace by default. The `actions/checkout` action creates a fresh checkout on
every job invocation. Sharing is opt-in via artifacts or cache actions — not the
default.

**The analogy to niwa**: A `niwa_delegate` call without `session_id` runs the worker
in the main clone. Multiple untagged delegations targeting the same repo run in the
same working tree, with no isolation between them. This is the exact pattern CI
systems recognized as problematic and moved away from — not as a minor improvement,
but as the foundational requirement that made multi-job pipelines reliable.

---

### Git Worktree Conventions: Designed for Concurrent Isolation, Not Shared Use

`git worktree` was introduced precisely to support concurrent work on multiple
branches without branch switching. The git documentation frames worktrees as
parallel working trees, each on their own branch — not as a shared space for
concurrent processes.

The key constraint git documents: **you cannot check out the same branch in two
worktrees simultaneously.** This is an enforcement mechanism, not a recommendation.
Git refuses the operation because the outcome is undefined.

This has a direct consequence for the niwa main clone: if any delegation (or human
work) checks out a feature branch in the main clone, and a subsequent actor checks
out the same branch anywhere (main clone or worktree), git will refuse or produce
an error. The main clone is effectively a coordination hazard once it diverges from
the default branch.

**Ephemeral vs. persistent worktrees**: git documentation and tooling treat worktrees
as durable across their active work period but deliberately destroyed when the work
is done (typically after merge). The `git worktree prune` command handles stale
entries. There is no convention in the git ecosystem for "shared, persistent working
tree used by multiple processes." That pattern is associated with normal clones, and
the understood model is one-process-at-a-time per clone.

---

### The niwa Codebase: Main Clone Contamination as a Named Problem

The niwa codebase itself names "Main clone branch contamination" as a first-class
failure mode. The problem statement in `DESIGN-mesh-session-lifecycle.md` states
(lines 50-54):

> **Main clone branch contamination.** All work happens on the main clone's checked-
> out branch. After a coordinator finishes a feature branch and moves on, the repo
> stays on that branch. `niwa apply` skips non-default-branch repos, so workspaces
> accumulate stale checkouts with no automated recovery path.

This is an observed failure mode from the existing opt-in model, not a hypothetical.
The PRD (`PRD-mesh-session-lifecycle.md`) lists as a goal: "The main clone of every
repo in a workspace always stays on `main`." Yet PRD R13 simultaneously preserves
the untagged delegation path: "`niwa_delegate` called without a `session_id` behaves
exactly as today: each task gets a fresh Claude session."

This is a logical contradiction. Goal 3 (main clone stays on main) and R13 (untagged
delegates run in the main clone) cannot both be satisfied simultaneously. A single
untagged delegation that checks out a feature branch violates Goal 3.

The PRD's own Decisions section acknowledges this explicitly (line 682-684):

> **No implicit sessions on untagged niwa_delegate.** A delegation without a
> `session_id` continues to behave exactly as today. This preserves backward
> compatibility and makes the session model strictly opt-in. Implicit sessions were
> rejected because they would silently change the behavior of all existing
> coordinator prompts.

The rationale given was backward compatibility with "existing coordinator prompts" —
but niwa has no users, and the codebase was pre-1.0 at the time of this decision.
The scope file `explore_delegation-model-isolation_scope.md` specifically calls this
out as a potentially weak rationale.

---

### Analogous Systems: Devin, Cursor Background Agents, and Operator Frameworks

None of these tools are well-documented enough in public sources to cite precise
behavioral specifications, so I will not fabricate specifics. However, the general
pattern that can be asserted from public knowledge:

- AI coding agent systems that support parallel task execution universally use
  isolation as the foundation — whether via containers, sandboxes, or git worktrees.
  No publicly documented multi-agent system designed after 2022 uses a shared working
  tree as the default execution environment for parallel agents.

- The Claude agent SDK (`claude-code-sdk` and the subagent model) documents that
  each `claude -p` invocation starts with its own working directory context. The
  convention in niwa's own codebase (the bootstrap prompt, the MCP handler comments)
  treats workers as isolated actors — they do not know about each other's existence,
  and the system design assumes they cannot interfere with each other's file writes.
  That assumption is false when multiple workers share a working tree.

---

### Shared Working Tree Failure Modes: What Actually Goes Wrong

The failure modes when multiple concurrent processes share a git working tree are
well-understood in practice:

1. **Branch checkout conflicts**: two workers try to check out different branches.
   One wins; the other gets an unexpected working tree state or an error.

2. **`.git/index` lock contention**: git operations acquire `index.lock`. Concurrent
   `git add`, `git checkout`, `git status`, or `git commit` operations fail with
   "Unable to create index.lock: File exists." Workers that do not handle this error
   (and most `claude -p` workers will not) leave the working tree in an unknown state.

3. **Interleaved file writes**: worker A creates file `foo.go`, worker B overwrites
   it. Neither worker detects the interference. The last writer wins, but neither
   the coordinator nor either worker is aware this happened.

4. **`git status` misread**: a worker calls `git status` to determine what needs
   committing. If another worker's uncommitted changes are present in the same tree,
   the first worker sees changes it did not make and either tries to commit them,
   reverts them, or becomes confused about its own state.

5. **Stall watchdog and restart races**: niwa's stall watchdog can kill and respawn
   a worker. If the killed worker was mid-operation in the main clone, the newly
   spawned worker starts with partial state it did not create. This is a specific
   failure mode documented in `DESIGN-coordinator-loop.md` — the resume path was
   designed to address it for session workers, but untagged delegates in the main
   clone have no equivalent recovery path.

---

### Ephemeral Task Isolation vs. Persistent Session Isolation

The distinction between ephemeral task isolation (destroyed after task) and
persistent session isolation (kept for continuity) maps directly onto niwa's own
design:

- **Ephemeral task isolation** (CI analogy): each `niwa_delegate` call gets an
  isolated environment for the duration of the task, then that environment is
  discarded. No context carries forward. This prevents contamination but gives up
  continuity.

- **Persistent session isolation** (niwa session model): a session worktree persists
  across multiple tasks, enabling `--resume` continuity. The session is created
  explicitly, exists for a defined purpose, and is destroyed explicitly. This gives
  both isolation and continuity.

The current opt-in model has neither for untagged delegations: no isolation (shared
main clone), no continuity (fresh Claude process per task). Untagged delegations
get the worst properties of both models.

CI systems moved from shared-workspace to ephemeral-per-job isolation early in their
evolution. Jenkins originally used persistent workspace directories per job — a model
that caused exactly the contamination problems documented. The shift to ephemeral
agents (containerized runners, cloud VMs) was driven by real-world failures, not
theoretical concerns. GitHub Actions was designed from the start with ephemeral
isolation as the default; the lesson from Jenkins was already encoded into its design.

---

### Human Work in the Main Clone: A Distinct and Unaddressed Case

The scope document identifies this as a separate concern: humans who launch Claude
directly in a repo's main clone produce the same dirty-main problem as untagged
delegations.

There is no prior art in the CI/CD space for this case because CI systems don't have
a "human operator running directly in the build environment" use pattern. The closest
analog is developers who `ssh` into a CI server and run commands manually — a
universally discouraged practice precisely because it contaminates shared state.

The workspace instance root (where the coordinator runs) has no session equivalent.
The DESIGN-mesh-session-lifecycle.md explicitly treats this as accepted: the coordinator
runs at the workspace root, not in a session worktree. This is a design asymmetry:
session workers are isolated by design; the coordinator is not.

The practical consequence: if a human operator runs `claude` inside a repo's main
clone (for interactive exploration or quick fixes), they share state with any running
workers and any subsequent delegations. The niwa design has no mechanism to warn,
redirect, or block this.

---

## Implications

### On Always-Isolated Delegation

The prior art strongly supports making all delegation session-bound by default, not
opt-in. The failure modes of shared working trees are not edge cases — they are the
predictable and documented outcome of concurrent processes in shared git state. Every
CI system that started with shared workspaces eventually moved to isolation.

If niwa's goal is for the main clone to always stay on `main`, then untagged
delegations directly undermine that goal. The opt-in design effectively means "the
main clone stays on main unless a coordinator forgets to pass `session_id`" — which
is not a useful guarantee.

### On Ephemeral vs. Persistent Auto-Sessions

If isolation is made the default for untagged delegations, the question is whether
that isolation is ephemeral (destroyed after the task) or persistent (session
semantics). The explicit session model provides persistence and continuity. An
ephemeral auto-session would give isolation without the context continuity benefit
— but it would prevent contamination, which is the more critical property.

This is a real design choice with no obvious answer from prior art alone. CI systems
use ephemeral isolation precisely because they do NOT want state to persist between
jobs. Niwa's explicit session model was designed specifically because state SHOULD
persist between related tasks. The right answer depends on whether untagged delegations
are expected to be stateless (CI analogy) or stateful (part of a multi-step workflow
that forgot to use a session).

### On the Main Clone as Read-Only

The CI analogy suggests the main clone should be treated as read-only infrastructure —
a base from which worktrees are created, never modified directly by workers. This is
the `niwa apply` intent already: apply operates on the main clone; sessions operate
in worktrees. The missing piece is enforcement or convention for worker processes.

### On Human Work

There is no clean prior art for the human-direct-in-main-clone case. The most defensible
position is documentation and convention: clearly name the main clone as a read-only
artifact and direct humans to use `niwa session create` for any work that produces
changes. Enforcement (blocking `claude` in the main clone) is operationally feasible
but would require detection logic niwa doesn't have and may break non-code-writing
use patterns (e.g., reading files, running tests to understand behavior).

---

## Surprises

1. **The contradiction is explicit in the PRD.** Goal 3 ("main clone always stays on
   main") and R13 (untagged delegates run in main clone) directly conflict, and the
   PRD's own text resolves this via backward compatibility — a rationale the scope
   document identifies as weak. This is not a hidden tension; it was visible during
   design review and accepted as a known trade-off.

2. **The main-clone contamination problem was already observed.** The design documents
   describe "Main clone branch contamination" as a named, observed failure mode with
   concrete symptoms (workspaces accumulate stale checkouts, `niwa apply` skip logic
   triggered). The opt-in model does not prevent the exact failure it was designed to
   address — it only prevents it for coordinations that use sessions correctly.

3. **The current model has no recovery path for untagged delegate contamination.**
   Explicit sessions have documented recovery (destroy → clean worktree). The main
   clone after an untagged delegation that leaves a feature branch has no automated
   recovery. The workspace accumulates drift until a human manually resets it.

4. **The prior art on isolation is one-directional.** I found no examples of systems
   that started with mandatory isolation and then relaxed it to opt-in. The movement
   is always from opt-in/shared to mandatory isolation. This asymmetry is informative:
   the cost of forced isolation (overhead, UX friction) is consistently judged
   acceptable compared to the cost of shared state (unpredictable failures).

---

## Open Questions

1. **What is the overhead cost of a worktree per untagged delegation?** A worktree
   for a read-only task (exploration, analysis, no git changes) produces a branch
   and worktree directory that must be cleaned up. If the cleanup is automatic
   (ephemeral), what triggers it? If it's manual, does it create a new class of
   stale-worktree debt?

2. **Should implicit sessions be ephemeral or persistent?** If a delegation without
   `session_id` creates a session, should that session be destroyed automatically
   when the task completes (losing the branch but gaining clean state), or should it
   persist for coordinator inspection (same lifecycle as explicit sessions)?

3. **What is the interaction between auto-created sessions and the `niwa apply` skip
   logic?** The session model already handles this for explicit sessions (worktrees
   are inside `.niwa/worktrees/`, invisible to `niwa apply`). Auto-sessions would use
   the same layout, so this is likely a non-issue — but needs confirmation.

4. **Does making isolation default break any currently useful untagged delegation
   pattern?** The backward-compatibility rationale assumes existing coordinator
   prompts rely on untagged delegation in the main clone. With no users and no
   deployed prompts, the practical answer is no — but this needs explicit
   acknowledgment before the decision is made.

5. **Is there a case for read-only worktrees?** Some delegations genuinely produce
   no git changes (research, code review, analysis). A worktree for these tasks
   wastes disk space and adds cleanup overhead. Is there a lighter isolation option
   (no branch creation, no worktree, just process isolation) for read-only tasks?
   Or is the overhead of a worktree acceptable for all tasks?

6. **What does "human works directly in the repo" mean operationally?** Is this
   `cd repo && claude` for interactive exploration? Or is it more structured? The
   answer shapes whether niwa needs to address it via enforcement, documentation,
   or a dedicated non-session interactive mode.

---

## Summary

Prior art from CI/CD systems and git's own worktree model consistently treats
shared working trees as a liability — every system that started with opt-in isolation
eventually mandated it to prevent state contamination between concurrent processes.
The niwa codebase itself names "Main clone branch contamination" as an observed
failure mode and sets "main clone always stays on main" as a PRD goal, yet the
current design allows untagged delegations to run directly in the main clone,
directly undermining that goal. The biggest open question is whether implicit
isolation should be ephemeral (destroyed when the task ends, no continuity) or
persistent (session semantics, kept for inspection), since these two modes serve
different use cases and have different operational costs.
