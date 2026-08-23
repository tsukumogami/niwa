# /scope Handoff: codex-instance-root-skills

## Provenance

Written by `/explore` on 2026-08-23 from
`wip/explore_codex-instance-root-skills_crystallize.md`.
Research files: `wip/explore_codex-instance-root-skills_findings.md`,
`wip/explore_codex-instance-root-skills_decisions.md`, and
`wip/research/explore_codex-instance-root-skills_r*_lead-*.md`.

Two discover-converge rounds. Round 1 ran seven leads across the delivery path,
the layout tables, niwa's own plugin, the structural scans, symlink-target
rotation, the acceptance-test surface, and root hygiene. Convergence narrowed to
two gaps and round 2 ran two more leads against them. Three round-1 agent claims
were checked against the code and found wrong; the corrections are recorded in
the findings rather than left to propagate. The exploration also took its own
measurements against the real `codex-cli 0.147.0` binary at zero model cost,
which settled two questions no amount of code reading would have.

## Problem Statement

Every background worker `niwa dispatch` launches stands at a workspace instance
root, and a Codex worker standing there receives none of the workspace's skills
— while the same worker one directory down inside a cloned repository receives
them all. The root now carries an orientation document whose prose tells the
worker to invoke a skill it does not have. Two capability rows record this as
niwa's own debt: row 18 `RootProjectSkills` and row 19 `NiwaPlugin`, both
declared unavailable for Codex with reason kind `ReasonNotBuilt`. Closing them
means delivering the same symlinked plugin tree niwa already builds per
repository, one directory higher, and binding both rows to real deliveries.

## Scope Boundary

### In scope

- Delivering workspace plugin skills to the instance root for Codex.
- Row 19: niwa's own plugin, which carries the migrate-config skill.
- Making both rows end implemented with a delivery bound to each declaration.
- Regenerating the guide's gap list so both bullets disappear.
- Correcting the authored prose that goes stale: the dispatch-time warning, the
  guide's instance-root section, and the feature file's own preamble.
- A functional acceptance scenario with a negative control.
- An explicit, written answer on what happens to root symlink targets under
  rotation.

### Out of scope

- Any `[projects."<instance root>"]` trust entry. Skills load untrusted;
  reaching for trust crosses into rows 5, 8, 9 and 12, and widening what niwa
  writes into the developer's own Codex config is the author's decision, not
  this chain's.
- Widening `payloadLayouts[agent.AgentCodex]` to `PayloadAtInstanceRoot`. That
  table's own comment says the change is a decision about what niwa writes
  outside an instance, which is the same reserved decision.
- Adding a where-from axis to the declaration table. The schema is scoped by who
  receives a capability, never by where from.
- Shipping `codex exec --dangerously-bypass-hook-trust`.
- Changing the per-repository or worktree delivery, which works and must keep
  working unchanged.

## Decisions Already Settled

From `wip/explore_codex-instance-root-skills_decisions.md`, six decisions the
chain treats as settled inputs:

- **The delivery is a sibling producer method, not a payload-layout scope.**
  Row 18's declaration comment points at the payload table, which governs the
  trust-gated config document and is the reserved decision. Skills reach a
  session through a separate table with no scope concept. The chain follows the
  `RootContextPlan` / `RepoContextPlan` precedent instead.
- **No scope field is added to the skills input.** The codebase carries both
  patterns; the single-capability-with-an-internal-scope-gate one has already
  produced three declarations that overclaim.
- **Root links point at the same targets the repository links already point at**,
  as a reasoned position rather than an inheritance. The targets are owned at
  the same scope as the link and replaced path-stably, never moved.
- **The root delivery does not reach for the repository exclude path**, because
  that helper searches upward for an enclosing repository and could write into
  a repository niwa was not asked to touch.
- **Both rows join the bound-capability set**, which pulls their Claude sides in
  with them and makes naming Claude's existing deliveries part of this work.
- **The plugin-manifest question is escalated to the design hop** with its
  evidence gathered rather than decided during exploration.

## Coverage Notes

Four things the exploration deliberately did not settle, each needing the design
hop rather than more research:

- **Row 18's binding shape.** Its route is `RoutePlan`, so a named binding must
  register a `Materializer`. The alternative is to bind it the way every other
  plan-routed capability is bound today — by tagging plan entries and testing
  the tag — and leave it out of the bound set. The brief's mandate names the
  binding rule, which is the bound-set one; the codebase's own precedent for
  plan-routed capabilities is the other. Neither is obviously right.
- **Whether niwa's embedded plugin tree gains a Claude plugin manifest.**
  Measured: without one Codex resolves the skill unnamespaced, with one it
  namespaces. But the documented command name comes from a different manifest
  field, and how Claude Code resolves this exact tree shape today is unmeasured.
  Changing the tree could silently rename an existing command.
- **How the dispatch-time warning is re-gated.** It is gated on row 18 today and
  will go silent when row 18 flips, while the gap it warns about is still real.
  No declaration row means "delivered in a repository, not at the root", and
  inventing one is forbidden, so the gate must read the payload layout — which
  needs a new exported predicate, since nothing exposes scope support today.
- **Where the embedded tree is materialized inside the instance**, and whether
  that name can collide with a configured marketplace called `niwa`.

Two things the exploration measured that the chain should not re-derive: a real
plugin tree symlinked at a session's own working directory yields 20 correctly
namespaced skills untrusted with no project-root marker in the ancestry, and the
identical shape one directory up yields nothing. Both were taken with
`codex debug prompt-input` under an isolated `CODEX_HOME`, at zero model cost,
which is also the shape the acceptance scenario should take.

## Upstream Observations

The exploration read three upstream documents and observed the following.

`docs/designs/current/DESIGN-agent-capability-contract.md` carries the contract,
its closed capability set, the three enforcement-test families, and the binding
rule in both drift directions. Its Decision 3 describes plan-route binding as a
canonical-fixture check that no test in the tree actually implements; per-producer
tests assert the capability tag instead. That gap is what makes row 18's binding
shape a real choice rather than a lookup.

`docs/spikes/SPIKE-codex-discovery-mechanics.md` records the measured discovery
mechanics. Its finding 5 is the load-bearing one — skills load from an untrusted
layer while every configuration key beside them requires trust. The exploration's
own measurements extend findings 1 and 5 with the plugin-manifest namespacing
rule and a root-layer positive/negative control, and belong as amendments to
that document rather than in a competing one.

`docs/prds/PRD-agent-capability-contract.md` carries the capability matrix and is
already a point-in-time record with appended amendments — its row 22 and its
stated column totals are both superseded by amendments below the table, which
name the test as the authority the totals are not. Rows 18 and 19 want another
amendment in that same style, not a matrix rewrite. Nothing mechanically checks
this document against the declaration table.

No ROADMAP governs this work, so no `--upstream` flag is passed.

## Framing-Shift Answer

**Pre-supplied answer:** no signal surfaced.

**Evidence:** the problem shape, the audience, the scope boundary and the
success criterion all held across both rounds. The work arrived framed as "close
two capability rows for Codex at the instance root" and ended framed the same
way. What moved was the *route*, not the framing: round 1 established that the
row's own declaration comment names the wrong table and that following it would
cross the reserved trust decision, so the delivery mechanism changed while the
problem and its boundary did not. The out-of-scope list grew one entry (the
payload-layout widening) for that reason, which narrows the implementation
rather than reframing the feature.

## Shape Signals

### Architectural alternatives left open

- **Row 18's binding shape: named delivery versus plan-entry tag.** A named
  delivery costs a `Materializer` type whose `Name()` matches a new delivery
  constant, and makes the row answerable to the bound-set drift test in both
  directions. A plan-entry tag costs a capability field on the produced entries
  plus a test asserting it, matches how every other plan-routed capability in
  the tree is handled today including row 5, and leaves the row outside the
  bound set — which the codebase currently treats as an honest record of "not
  bound yet" rather than a defect.
- **Row 19's Codex materialization site.** Extracting the embedded tree to a
  niwa-owned path under the instance's own `.niwa/` and symlinking it costs one
  new extraction step and reuses the reconcile mechanism unchanged. Copying the
  embedded tree directly into the delivered location costs no extraction but
  makes the delivery a copy rather than a symlink, which the reconcile
  mechanism recognizes only through its sentinel file.
- **The dispatch warning's new gate.** A new exported predicate on the producer
  keeps the warning mechanically tied to the layout table it describes. Reading
  the agent name directly in the dispatch path is simpler and reintroduces a
  name-based check the existing test design deliberately avoided.
- **Whether the embedded tree gains a Claude plugin manifest**, trading a
  correct Codex namespace against an unmeasured risk to an existing Claude
  command name.

### Complexity signals

- The change spans four packages and touches at least eight surfaces that react
  mechanically: three tests pinning the capability column's contents and counts,
  the two binding tests, the generated gap list's drift test, the dispatch
  warning and its two pinning tests, and three separate pieces of authored prose
  in two documents plus a feature file preamble.
- Two structural scans constrain the implementation rather than merely reacting
  to it: no agent constant may appear at the materializer call site, and the
  workspace package may name no agent and no agent context filename. The root
  delivery must therefore reach its own directory name through the producer.
- Binding routes are fixed per capability in a catalog, so the two rows bind
  through two different registries — one materializer, one procedure — even
  though their deliveries are mechanically similar.
- The two agents' row-19 deliveries have genuinely different lifecycles: one is
  global, once-per-developer-machine, fired only inside a config-migration
  branch, and untouched by instance teardown; the other would be per-instance
  and reaped with the instance.
- Contested trade-offs that need settling rather than discovering: all four
  alternatives above, each with a real cost on both sides.
