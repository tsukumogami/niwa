## Visibility

Public

## Core Question

Should all `niwa_delegate` calls be session-bound (worktree-isolated) by default, rather than opt-in via explicit `session_id`? And given that the workspace instance root has no equivalent isolation, what is the right UX for humans working directly in a repo's main clone — should that be a first-class path, or should it be discouraged in favor of explicit sessions?

## Context

The PRD's decision to make sessions opt-in cited backward compatibility, but niwa has no users yet, so that rationale is weak. The primary motivation for sessions is keeping main clones clean and pinned to the default branch. But that goal is equally undermined by untagged delegations that run in the main clone. The user suspects the opt-in design was an oversight by reviewers who took the backward-compat rationale at face value.

A second concern: humans can currently launch Claude directly in a repo's main clone. This produces the same dirty-main problem as untagged delegations. The existing design has no answer for this case.

## In Scope

- Whether `niwa_delegate` without `session_id` should implicitly create a session
- Lifecycle implications of implicit session creation (who names, who destroys)
- Whether the main clone should be treated as read-only by convention or enforcement
- The UX for non-coordinator (human) work at the repo level: main clone vs. manual session
- Conflict potential between human-created sessions (CLI) and coordinator-created sessions

## Out of Scope

- The session tree model and parent-child routing (settled)
- The internal mechanism of worktree provisioning (settled)
- Session-to-session communication (separate design)
- Workspace instance root UX (no worktree equivalent exists there; accepted constraint)

## Research Leads

1. **What are the practical consequences of making all delegation session-bound?**
   Covers: worktree lifecycle management (who creates/destroys auto-sessions), overhead of a worktree per task, what happens to short-lived or read-only delegations that produce no git changes.

2. **Should the main clone be a fully read-only artifact?**
   If all work — delegated and human — happens in session worktrees, the main clone could be locked to the default branch. What enforcement exists or would need to exist? What breaks or simplifies?

3. **What is the right interaction model when a human launches Claude directly in a repo's main clone?**
   Should niwa detect and warn? Redirect to a session? Block? Or is direct main-clone work acceptable and the docs just need to name it clearly?

4. **Do manually created sessions (niwa session create) and coordinator-created sessions conflict?**
   Same worktrees directory, same daemon per worktree model. Are there ownership ambiguities, shared-state races, or routing problems when a human-created session and a coordinator-created session coexist for the same repo?

5. **What does implicit session creation per delegation look like operationally?**
   Who sets the purpose string? Does the session auto-destroy when the task completes, or does it persist like explicit sessions? Who is responsible for the resulting branch? Does it block `niwa apply`?

6. **What have other multi-agent or worktree-based tools done about workspace isolation?**
   Any prior art on always-isolated vs opt-in delegation that informs what failure modes appear in practice?
