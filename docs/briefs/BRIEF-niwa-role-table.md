---
schema: brief/v1
status: Accepted
problem: |
  niwa computes a workspace's role set at apply time to build the mesh
  inboxes, but never writes that set down. Tools that address a role
  from outside niwa (a bridge skill routing an @role mention, koto
  resolving a peer-message destination) have no workspace-aware source
  of truth and must hard-code identifiers or keep drifting bookkeeping.
outcome: |
  After niwa apply, a workspace carries a readable role table any Claude
  Code session or sibling tool can consult to answer "what roles exist?"
  and "where does a message to role X go?" without re-deriving topology
  or maintaining a parallel list.
---

# BRIEF: niwa apply emits a workspace-aware role table

## Status

Accepted

Framing locked via autonomous `/scope` run (recommended-default judgment
calls; no Open Questions remain at the brief altitude). The deferred
framing details — exact file shape, schema fields, resolution semantics,
refresh contract — are requirements the downstream PRD owns, not brief-
level gaps. Downstream PRD: `docs/prds/PRD-niwa-role-table.md`.

## Problem Statement

A niwa workspace hosts multiple repos and a coordinator-plus-delegates
mesh. Work and messages are addressed to *roles*: a coordinator plus one
role per cloned repo, the exact set niwa already enumerates at `niwa
apply` time to build each role's message inbox.

The gap is that this role set lives only in niwa's head. niwa derives it,
uses it to lay down inbox directories, and then discards it. Nothing on
disk tells an outside reader which roles exist or where a message
addressed to a given role should land.

That forces every tool that wants to address a role by name to solve the
problem on its own terms:

- A coordinator-bridge skill that turns an `@role` mention into a
  dispatch has no list to resolve the name against, so it either asks
  the operator to spell out explicit delivery identifiers every time or
  keeps its own side table of roles.
- A peer-messaging layer that routes a message to a role at the
  multi-repo substrate has the same problem one layer down.

Any side table a consumer keeps drifts the moment a repo is added or
removed, because the consumer is re-deriving a fact niwa already computed
authoritatively. The workspace topology has one owner — niwa — but the
answer it produces is not published, so consumers re-invent it and the
copies diverge.

## User Outcome

After `niwa apply`, the workspace carries a role table a reader can open
to learn, without re-deriving anything, what roles the workspace has and
where a message to each one is delivered.

For the author working through a bridge skill, mentioning `@reviewer`
resolves against that table, so addressing a teammate by role name is as
ergonomic as the workspace topology allows — no hand-entered delivery
identifiers, no stale side list. For a peer-messaging layer, the same
file answers the same question at the substrate level. For an operator,
the table is a legible picture of the workspace's roles that niwa keeps
current: re-apply after adding a repo and the new role is simply there.
A workspace with no bridge skill or peer-messaging layer present still
gets the table; it is a plain readable file, useful to any session that
opens it.

## User Journeys

### Bridge skill resolves an @role mention

A coordinator-bridge skill (hosted in shirabe) is dispatching peer
work. The trigger is an author mentioning `@worker` in a message the
bridge processes. The bridge reads the workspace role table, resolves
`worker` to its delivery destination, and dispatches without asking the
author to supply an explicit identifier. The outcome shape: an `@role`
mention becomes an addressed dispatch using niwa's authoritative role
set rather than coordinator-side bookkeeping.

### Peer-messaging layer routes to a role

koto's peer-messaging and mention-routing layer needs to turn a role
name into a destination at the multi-repo substrate. The trigger is a
message addressed to a role name. koto resolves the name against the
table niwa emitted (in koto's case, through the niwa-provided resolution
path rather than reading the file directly). The outcome shape: the
multi-repo substrate resolves role names from one shared source of truth,
so koto and the bridge skill agree on what a role name means.

### Operator inspects the workspace's roles

An operator wants to see which roles a workspace has and where each one
resolves — for example, to confirm a freshly added repo became an
addressable role. The trigger is the operator inspecting the workspace
through the role-aware niwa surface that reads the table. The outcome
shape: the operator sees the same role set niwa used to build the mesh,
read back from the emitted table rather than re-derived by hand.

### Re-apply keeps the table current

An operator adds a repo to the workspace and runs `niwa apply` again.
The trigger is the re-apply. niwa regenerates the role table to include
the new repo's role, idempotently — an apply that changes no roles
rewrites no bytes. The outcome shape: the table tracks the workspace
topology automatically, with no manual edit and no drift between the
table and the inboxes niwa laid down in the same apply.

### Standalone workspace with no consumers present

A workspace is applied with neither a bridge skill nor a peer-messaging
layer installed. The trigger is a plain `niwa apply`. The table is
emitted anyway, as an ordinary readable file at a known location. The
outcome shape: the emission is unconditional — its value to consumers
depends on those consumers existing, but the file itself is always
present and any Claude Code session can read it.

## Scope Boundary

### IN

- Emission of a workspace-aware role-table data file during `niwa
  apply`, at a known location, as a durable readable artifact.
- Use of niwa's existing role enumeration as the table's source of
  truth — the same role set niwa already computes to build the mesh
  inboxes (coordinator plus topology-derived and explicitly-configured
  roles).
- Regeneration on every apply, idempotently, so the table tracks the
  workspace topology without manual edits.
- Treating the emitted file as a stability surface other tools read
  against — the table is a contract, not an internal scratch file.

### OUT

- The bridge skill's content and mention-routing logic. The skill lives
  in shirabe and *consumes* the table; this feature emits the data, not
  the skill behavior.
- The `niwa role` CLI commands (such as listing roles or resolving a
  name to a destination) that read the table. Those are a separate niwa
  feature; this feature emits the file they will read.
- The peer-messaging layer's routing logic in koto. koto consumes the
  resolution this feature makes possible; its routing code is out of
  scope here.
- The `.claude/agents/<role>.md` subagent-definition files generated at
  apply time. That is a sibling feature (the agent-definition companion);
  it produces agent *definitions*, while this feature produces the role
  *table* (data). The two share the apply codepath and the same role set
  but are scoped separately.
- Changes to how roles are enumerated, named, or validated. This feature
  reuses niwa's existing enumeration unchanged; it serializes the result
  rather than redefining it.
- Message delivery and transport mechanics. The per-role inboxes already
  exist; this feature publishes where they are, it does not move messages.

## Downstream Artifacts

- `docs/prds/PRD-niwa-role-table.md` — requirements for the role-table
  emission (file shape, schema, resolution semantics, refresh contract).
- `docs/designs/DESIGN-niwa-role-table.md` — technical design of the
  emitted table and the apply-codepath integration.
- `docs/plans/PLAN-niwa-role-table.md` — implementation plan.

## References

- `internal/workspace/channels.go` — `enumerateRoles` and the existing
  per-role inbox installation this feature's table mirrors.
- `docs/designs/current/DESIGN-cross-session-communication.md` — the
  peer-messaging model that motivates role-name resolution.
- `docs/designs/current/DESIGN-mesh-session-lifecycle.md` — role-based
  routing for delegation and peer messaging.
