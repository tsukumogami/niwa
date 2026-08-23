---
schema: brief/v1
status: Accepted
problem: |
  Every background worker niwa dispatches stands at a workspace instance
  root, and a Codex worker standing there receives none of the
  workspace's skills -- while the same worker one directory down inside
  a cloned repository receives them all. The root's own orientation
  document tells the worker to invoke a skill it doesn't have.
outcome: |
  A dispatched Codex worker invokes workspace skills at the instance
  root exactly as it would inside a repository. The guide's generated
  gap list drops both rows, the dispatch warning stays truthful about
  what's still missing, and both capabilities end bound to deliveries
  that tests hold in place.
motivating_context: |
  Rows 18 (RootProjectSkills) and 19 (NiwaPlugin) of the capability
  contract are declared unavailable for Codex with reason kind
  ReasonNotBuilt -- recorded as niwa's own unbuilt work, not a Codex
  limitation. Measurement against the real Codex binary confirmed that
  skills load from an untrusted project layer, so the debt is payable
  without widening what niwa writes into the developer's own Codex
  configuration.
---

# BRIEF: codex instance root skills

## Status

Accepted

## Problem Statement

`niwa dispatch` launches every background worker at the root of a
workspace instance -- the directory that holds the cloned repositories,
not any one of them. A Claude Code session started there finds the
workspace's plugin skills in place. A Codex session does not: it
resolves zero workspace skills at the root, while the identical session
one directory down, inside any cloned repository, resolves all of them
with correct namespaces. The root even carries an orientation document
whose prose tells the worker to invoke a workspace skill -- an
instruction a dispatched Codex worker cannot follow.

The capability contract records this honestly as niwa's own debt. Row
18, `RootProjectSkills`, is declared unavailable for Codex with reason
kind `ReasonNotBuilt`: "niwa writes no project layer at an instance
root." Row 19, `NiwaPlugin` -- niwa's own plugin, which carries the
config-migration skill -- is declared unavailable the same way: the
wiring is unbuilt. Neither reason says Codex can't receive the
capability, because it can.

That deliverability is measured, not assumed. Skills are the one
project-layer capability shown to load from an untrusted layer: a real
plugin tree symlinked at a session's own working directory yields every
skill, correctly namespaced, with no trust entry and no project-root
marker anywhere in the ancestry -- and the identical tree one directory
above the session yields nothing. Everything else a root session lacks
lives in Codex's config document, where every key is inert without a
trust entry in the developer's own Codex configuration. Whether niwa
writes such an entry is a reserved decision this work must not touch.
Skills sit on the near side of that line.

Left unbuilt, the gap does more than inconvenience a worker. The
dispatch-time warning, the guide's instance-root section, and the
acceptance feature file all describe the missing delivery in authored
prose that drifts as the facts change. And both rows sit outside the
bound-capability set, so nothing mechanical ties what the contract
declares to what the code actually delivers.

## User Outcome

A developer dispatches a background Codex worker and the worker can do
what the orientation document tells it to do. The workspace's skills
resolve at the instance root with the same namespaced names a
repository-started session sees, delivered as the same symlinked plugin
tree niwa already builds per repository, one directory higher. Nothing
changes for the teammate who opens the same instance with Claude Code,
and nothing changes inside the repositories.

niwa's own plugin reaches the root session too. A Codex worker can
resolve the config-migration skill instead of finding niwa's plugin
absent from the one place every dispatched worker starts.

The honest accounting survives the fix. The guide's gap list --
generated from the declarations -- drops both bullets on regeneration
rather than by hand-editing. The dispatch-time warning doesn't go
silent when the rows flip; it names what a root-started Codex session
genuinely still lacks -- MCP servers, the session environment, the
approval and sandbox posture -- and nothing more. And a maintainer can
trace both rows to named deliveries: each capability joins the bound
set, so a declaration that flips without its delivery, or a delivery
that vanishes without its declaration, fails a test. The behavior of
root symlink targets when marketplace content is replaced is written
down as a reasoned position rather than left as an assumption.

## User Journeys

### A dispatched Codex worker uses the workspace's skills

A developer hands work to a background Codex worker with
`niwa dispatch`. The worker boots at the instance root, reads the
orientation document, and invokes the workspace skill the document
names -- the skills resolve with the same namespaces a
repository-started session gets. Before this feature, the same worker
resolved zero workspace skills and the instruction was a dead end.

### A developer hits the remaining root gap and gets a straight answer

A developer expects their MCP servers or sandbox posture to reach a
root-started Codex session, and they don't. The dispatch-time warning
and the guide's instance-root section tell them exactly what's missing
and why: those capabilities live in Codex's config document, which niwa
delivers per repository, not at the root. The warning is re-gated on
that fact rather than on row 18, so delivering skills doesn't silence
it while the gap it describes is still real.

### A maintainer traces a capability row to its delivery

A maintainer reviews the change that flips both rows, or audits the
contract months later. Both rows read implemented for Codex, both
capabilities sit in the bound set, and Claude's existing deliveries are
named and registered beside the new Codex ones. Removing a delivery or
flipping a declaration without its counterpart fails the binding test,
and the guide's gap list regenerates from the declarations instead of
trusting anyone to remember it.

### A reviewer verifies the delivery at zero model cost

A reviewer of the implementing pull request wants evidence the
delivered tree actually loads, not a claim. The acceptance scenario
renders a session's resolved skills with `codex debug prompt-input`
under an isolated `CODEX_HOME`: the root tree's skills appear
namespaced in the output, and the negative control -- an identical
tree one directory above the session -- yields nothing. The whole
check runs without a single model turn.

## Scope Boundary

**In:**

- Delivering the workspace's plugin skills to the instance root for
  Codex sessions: the same symlinked tree niwa builds per repository,
  one directory higher (row 18).
- Delivering niwa's own plugin so a Codex session at the root can
  resolve its config-migration skill (row 19).
- Binding both rows. Both capabilities join the bound set, which
  requires a named, registered delivery for every implemented
  (capability, agent) pair -- Claude's existing deliveries included.
- Regenerating the guide's gap list so both bullets disappear.
- Correcting the authored prose the change makes stale: the
  dispatch-time warning (re-gated so it stays accurate instead of
  going silent), the guide's instance-root section, and the acceptance
  feature file's preamble.
- A functional acceptance scenario with a negative control, runnable
  at zero model cost.
- An explicit, written answer on how root symlink targets behave when
  marketplace content is replaced.

**Out:**

- Any trust entry for the instance root in the developer's own Codex
  configuration. Skills load untrusted; trust is what gates the
  config-document capabilities, and widening what niwa writes outside
  an instance is reserved to the author, not this chain.
- Widening the Codex payload layout to instance-root scope. MCP
  servers, the session environment, the approval and sandbox posture,
  and the doc budget stay repository-scoped; the layout table's own
  comment marks the widening as the same reserved decision.
- A where-from axis on the capability declaration schema. Rows are
  scoped by who receives a capability, never by where from.
- Shipping or relying on `codex exec --dangerously-bypass-hook-trust`
  or any other trust-bypass flag.
- Changing the per-repository or worktree skills delivery, which works
  today and must keep working unchanged.

## Open Questions

- **Row 18's binding shape.** Its route requires a named delivery to
  register a materializer, while every other plan-routed capability in
  the tree today is handled by tagging plan entries and testing the
  tag. The bound-set mandate points one way, the codebase's precedent
  the other; the design owns the choice.
- **Whether niwa's embedded plugin tree gains a Claude plugin
  manifest.** Measured: without one Codex resolves the skill
  unnamespaced, with one it namespaces. But the same tree is installed
  into the developer's global Claude plugin directory, and how Claude
  Code resolves the current shape is unmeasured -- adding the manifest
  could silently rename an existing command. The design must either
  measure that or scope the change so the Claude path can't move.
- **How the dispatch warning is re-gated.** An exported predicate over
  the payload layout's scope support keeps the warning mechanically
  tied to the table it describes; reading the agent name directly is
  simpler but reintroduces a name-based check the existing test design
  deliberately avoided.
- **Where the embedded tree is materialized inside the instance**, and
  whether that location's name can collide with a configured
  marketplace called `niwa`.

None of these block the brief; each defers a requirements- or
design-level determination to the downstream PRD and design.

## References

- docs/designs/current/DESIGN-agent-capability-contract.md -- the
  capability contract: its closed capability set, enforcement-test
  families, and the binding rule in both drift directions.
- docs/spikes/SPIKE-codex-discovery-mechanics.md -- the measured
  discovery mechanics; its finding that skills load from an untrusted
  layer is what makes root delivery possible without touching trust.
- docs/prds/PRD-agent-capability-contract.md -- the capability matrix,
  maintained as a point-in-time record with appended amendments; rows
  18 and 19 take another amendment in that style.
- docs/guides/codex-agent.md -- the user guide whose generated gap
  list and authored instance-root section this work updates.
- docs/briefs/BRIEF-agent-capability-contract.md -- the framing that
  established the contract these two rows live in.
