---
status: Accepted
problem: |
  niwa enumerates a workspace's role set at apply time to build the mesh
  inboxes but never persists it. Tools that address a role by name from
  outside niwa (a coordinator-bridge skill, a peer-messaging layer, an
  operator) have no workspace-aware source of truth and must hard-code
  identifiers or keep drifting side tables that diverge from the topology
  niwa already computed.
goals: |
  Make niwa apply emit a durable, readable role table that publishes the
  role set niwa already derives, so any session or sibling tool can
  resolve a role name to its delivery destination from one authoritative
  file that niwa keeps current on every apply.
upstream: docs/briefs/BRIEF-niwa-role-table.md
---

# PRD: niwa apply emits a workspace-aware role table

## Status

Accepted

Requirements locked via autonomous `/scope` run (recommended-default
judgment calls; no Open Questions remain). The feature carries
architectural choices left open for the design — the concrete file
format, the exact location and filename under `.niwa/`, the schema field
layout, and what a "delivery destination" resolves to — so a design doc
is warranted before implementation. Downstream design:
`docs/designs/DESIGN-niwa-role-table.md`.

## Problem Statement

A niwa workspace hosts multiple repos and a coordinator-plus-delegates
mesh. Messages and delegated work are addressed to *roles*: a coordinator
plus one role per cloned repo, the exact set niwa already enumerates at
`niwa apply` time (see `enumerateRoles` in
`internal/workspace/channels.go`) to lay down each role's message inbox
under `.niwa/roles/<role>/inbox/`.

That role set lives only in niwa's process memory. niwa derives it, uses
it to create the inbox directories, and discards it. Nothing on disk
tells an outside reader which roles a workspace has or where a message
addressed to a given role should be delivered.

The people and tools affected are those that address a role by name from
outside niwa:

- A coordinator-bridge skill (hosted in shirabe) that turns an `@role`
  mention into a dispatch has no list to resolve the name against. It
  falls back to explicit operator-supplied identifiers or its own side
  table of roles.
- A peer-messaging and mention-routing layer (koto) at the multi-repo
  substrate has the same problem one layer down.
- An operator who wants to see which roles a workspace has must read the
  config and re-derive the topology by hand.

Any side table a consumer keeps drifts the moment a repo is added or
removed, because the consumer is re-deriving a fact niwa already owns
authoritatively. This matters now because the workspace mesh — the
coordinator-and-delegates pattern these consumers are being built around
— depends on role addressing being ergonomic and consistent across
tools, and today every tool re-invents the same answer.

## Goals

- niwa publishes the role set it already computes, so it stops being a
  fact that lives only in niwa's head.
- A reader — a bridge skill, a peer-messaging layer, an operator, or any
  Claude Code session — can resolve a role name to its delivery
  destination from a single authoritative file without re-deriving the
  workspace topology.
- The published table tracks the workspace automatically: every apply
  regenerates it, and an apply that changes nothing changes nothing.
- The file is a stable contract sibling tools can build against, not an
  internal scratch artifact whose shape changes without notice.

## User Stories

- As a coordinator-bridge skill resolving an `@role` mention, I want to
  look up the role name in a workspace-provided table so that I can
  dispatch to the right destination without asking the operator to spell
  out an explicit identifier.
- As a peer-messaging layer routing to a role at the multi-repo
  substrate, I want one authoritative role-to-destination mapping so that
  my routing agrees with what every other tool resolves the same name to.
- As an operator inspecting a workspace, I want to read the role set niwa
  actually built so that I can confirm a newly added repo became an
  addressable role without re-deriving the topology myself.
- As an operator who re-applies after changing the repo set, I want the
  table regenerated automatically so that it never drifts from the
  inboxes niwa created in the same apply.
- As the maintainer of a tool that reads the table, I want its location
  and shape to be a versioned, stable interface so that a niwa upgrade
  does not silently break my consumer.

## Requirements

### Functional

- **R1.** `niwa apply` SHALL emit a role-table data file as part of every
  apply run that installs the mesh channel infrastructure.
- **R2.** The table SHALL be written to a known, stable location under
  the workspace instance's `.niwa/` directory, fixed across applies so
  consumers can locate it without discovery heuristics.
- **R3.** The table's source of truth SHALL be niwa's existing role
  enumeration — coordinator, topology-derived roles (one per cloned
  repo), and explicitly-configured `[channels.mesh.roles]` overrides. The
  table SHALL contain exactly that role set: every enumerated role and no
  others.
- **R4.** For each role, the table SHALL record the information a consumer
  needs to (a) resolve the role name to its delivery destination (the
  role's message inbox) and (b) identify the repo the role is bound to
  (or mark it as the coordinator, which is bound to the instance root).
- **R5.** The coordinator role SHALL always be present in the table.
- **R6.** The table SHALL carry a schema version so a consumer can detect
  an incompatible change rather than misread a newer table.
- **R7.** Role entries SHALL appear in a deterministic order that is
  stable across applies which do not change the role set.
- **R8.** Re-running `niwa apply` SHALL regenerate the table. An apply
  that does not change the enumerated role set SHALL produce byte-
  identical output; a change to the role set SHALL be reflected in the
  regenerated table.
- **R9.** The emitted table SHALL be registered as a niwa-managed file for
  the instance, so it participates in apply's managed-file tracking and
  drift detection like other apply-materialized files.
- **R10.** The table SHALL be emitted unconditionally whenever mesh
  channel infrastructure is installed — independent of whether any
  consumer (bridge skill, peer-messaging layer, role CLI) is present in
  the workspace.
- **R11.** Role names in the table SHALL conform to niwa's existing
  role-name rules. This feature introduces no new naming, enumeration, or
  validation behavior; it serializes the result of the existing
  enumeration unchanged.

### Non-functional

- **R12.** The table SHALL be a stability surface: its location and schema
  constitute a public interface sibling tools read against, and changes to
  that interface SHALL follow an additive, version-gated discipline rather
  than silent reshaping.
- **R13.** The table SHALL be plain-text, machine-readable structured data
  a human can also read directly.
- **R14.** Emission SHALL add negligible cost to apply, since it serializes
  data the apply run has already computed; it SHALL NOT introduce new
  network calls or repo scans beyond the existing enumeration.
- **R15.** The table SHALL contain no secrets, tokens, or credentials.
- **R16.** Destination references in the table SHALL be expressed relative
  to the workspace instance root, so the file is position-independent and
  does not leak absolute host paths.

## Acceptance Criteria

- [ ] After `niwa apply` on a workspace whose mesh infrastructure is
  installed, the role-table file exists at its fixed `.niwa/` location.
- [ ] The table lists exactly the roles niwa enumerated for the workspace
  (coordinator + one per cloned repo + explicit overrides) — no missing
  roles and no extra entries.
- [ ] Each role entry's recorded delivery destination matches the role's
  actual inbox directory created during the same apply.
- [ ] The coordinator role is present in the table.
- [ ] The table includes a schema-version field.
- [ ] Running `niwa apply` twice on an unchanged workspace yields a
  byte-identical table on the second run (no spurious rewrite).
- [ ] Adding one repo and re-applying produces exactly one additional
  role entry corresponding to that repo.
- [ ] Removing a repo and re-applying removes exactly that repo's role
  entry.
- [ ] The table is recorded among the instance's niwa-managed files
  (observable in the instance state niwa persists for the workspace).
- [ ] Emission occurs even when no bridge skill, peer-messaging layer, or
  role CLI is installed in the workspace.
- [ ] A reader can resolve a role name to its delivery destination using
  only the file's documented schema, without niwa-internal knowledge.
- [ ] The table contains no secrets, tokens, or credentials, and no
  absolute host paths.

## Decisions and Trade-offs

- **Source of truth is the existing enumeration, not a new role model.**
  The table serializes `enumerateRoles` output. The alternative — a
  dedicated role-definition config the table reads from — was rejected
  because it would duplicate the topology niwa already derives and create
  a second place for the role set to drift. Trade-off accepted: the table
  is exactly as expressive as the existing enumeration, no more.
- **Emission is unconditional whenever mesh infra installs.** Gating
  emission on a consumer being present was rejected: the role CLI and any
  standalone reader need the file regardless, and a conditional emission
  would make "does the table exist?" depend on unrelated workspace state.
- **Architectural choices are deferred to the design.** The concrete file
  format, the exact filename and location under `.niwa/`, the schema field
  layout, and the precise meaning of "delivery destination" (inbox path,
  repo path, or both) are genuine architectural alternatives with viable
  options. They are left open for `DESIGN-niwa-role-table.md` so the
  design can weigh them against the consumer contract; this PRD fixes the
  behavior and the contract obligations, not the wire shape.

## Known Limitations

- The table's value to addressing ergonomics depends on consumers (bridge
  skill, peer-messaging layer, role CLI) existing. Without them the file
  is still emitted and readable, but the end-to-end `@role` resolution
  experience is only realized once a consumer reads it. This PRD
  deliberately scopes only the emission side.
- The table reflects the role set as of the last apply. Between applies,
  manual filesystem changes to the repo set are not reflected until the
  next `niwa apply`; the table is an apply-time snapshot, consistent with
  how niwa materializes every other managed file.

## Out of Scope

- The coordinator-bridge skill's content and mention-routing logic. It
  lives in shirabe and consumes the table; this PRD covers emitting the
  data, not the skill.
- The `niwa role` CLI commands (e.g. listing roles, resolving a name to a
  destination) that read the table. Separate niwa feature; this PRD emits
  the file those commands will read.
- koto's peer-messaging and mention-routing logic that consumes the
  resolution this feature enables.
- The `.claude/agents/<role>.md` subagent-definition files generated at
  apply time. That is the sibling agent-definition feature; it produces
  agent *definitions*, while this PRD covers the role *table* (data). Both
  share the apply codepath and the same role set but are scoped separately.
- Any change to how roles are enumerated, named, or validated. The feature
  reuses the existing enumeration unchanged.
- Message delivery and transport. The per-role inboxes already exist; this
  feature publishes where they are, it does not move messages.
