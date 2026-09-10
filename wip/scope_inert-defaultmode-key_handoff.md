# /scope Handoff: inert-defaultmode-key

## Provenance

Written by `/explore` on 2026-09-09 from
`wip/explore_inert-defaultmode-key_crystallize.md`.

Research files: `wip/explore_inert-defaultmode-key_findings.md`,
`wip/explore_inert-defaultmode-key_decisions.md`, and
`wip/research/explore_inert-defaultmode-key_r*_lead-*.md`.

Two discover-converge rounds, nine leads. Round 1 traced the code graph, the
test surface, the argv channel, and external prior art, and narrowed four
candidate shapes to two. Round 2 was scoped to measurement only -- three
questions round 1 had inferred rather than tested -- and one of those
measurements inverted the problem statement. The exploration ran in auto mode
with no interactive author; the dispatch brief's stated priorities stood in for
the author's narrowing, and every decision point is recorded as a decision
block rather than as a confirmed choice.

## Problem Statement

niwa writes `permissions.defaultMode: "bypassPermissions"` into three
materialized documents per instance, and Claude Code has not honored that value
from project or local scope since 2.1.257. The value is not merely ignored: it
wins the settings scope merge and is then downgraded to `default`, so it
destroys whatever posture the developer's own user settings would have
contributed. A dispatched worker in a bypass-declaring workspace therefore ends
up *more* restrictive than if niwa had written nothing at all, while the
document it reads from displays an authoritative-looking permissions block that
governs nothing.

Meanwhile niwa's own dispatch flow reads that same materialized key back to
decide whether to forward `--permission-mode bypassPermissions` -- the channel
that does work -- which means the dead key is load-bearing as an internal
signal. The round trip is unnecessary on its own terms: the effective,
vault-resolved config the materializer turned into that key is computed inside
the same function a few hundred lines earlier and discarded.

## Scope Boundary

### In scope

- Stopping the materializer from emitting a `permissions.defaultMode` value
  that Claude Code cannot honor, across all three documents it writes to.
- Replacing the dispatch derivation's input so it reads niwa's own resolved
  config rather than round-tripping through the materialized output.
- The `"ask"` -> `askPermissions` mapping, which has never been a valid Claude
  Code permission mode and is pinned by a unit test and a PRD.
- Deleting `internal/workspace/permissions.go`'s `WorkerPermissionMode` and its
  test, which a shipped design doc already declared dead and then left in place.
- Correcting the committed documents that describe the materialized key as the
  mechanism governing a session's posture -- four inside niwa, plus a
  cross-reference note for three outside it.
- Correcting the version cited in
  `docs/designs/current/DESIGN-dispatch-permission-mode.md`, which says 2.1.258
  in four places.

### Out of scope

- **The `[claude.settings] permissions` TOML surface itself.** It is a
  user-facing config key with a live declaration in a real workspace; changing
  it is a compatibility break and a separate decision. The exploration's brief
  excluded changing what a workspace may declare, and the external-reader sweep
  confirmed the input surface is wider than the output surface.
- **`niwa watch`'s operator-approval posture.** It writes
  `defaultMode: "default"`, a restrictive value, which was measured to be
  honored from project scope. It is sound and must not be broken. Its
  hard-deny posture carries a *comment* claiming to inherit a bypass that never
  arrives, which is a documentation defect worth recording but produces a more
  restrictive session rather than a less restrictive one.
- **Switching remote control from `--settings` to the `--remote-control`
  flag.** The exploration measured that this works and that it would vacate the
  single `--settings` slot, but it is separate work with its own risk surface.
- **Building a merged settings-document builder.** See Coverage Notes; the
  exploration's conclusion is that this problem does not require one.
- **The peer-message pre-approval work**, owned by a sibling session.
- **Adding containment for a bypass worker.** The governing design doc already
  flags this as a known gap and a follow-up; nothing here changes it.

## Decisions Already Settled

From `wip/explore_inert-defaultmode-key_decisions.md`, twelve blocks. The ones
that bear on what gets built:

- **Annotating the key in place is eliminated.** The published Claude Code
  settings schema makes the `permissions` object `additionalProperties: false`,
  so a sibling explanatory key is schema-illegal in the one spot it would go.
  No project was found that annotated a dead key it kept writing. And it does
  not address the complaint: a reader still sees an authoritative permissions
  block that governs nothing.
- **Moving the signal into the `--settings` payload is eliminated as the fix.**
  It was measured to work -- `--settings` is the one document channel that
  still carries a permissive `defaultMode` -- but `--permission-mode` already
  delivers the same signal for free, so it would spend a scarce slot for
  nothing. The measurement is recorded for whoever later needs a
  permission-shaped signal the flag's closed enum cannot express.
- **The framing is "stop writing the values that cannot work", not "stop
  writing the key".** Only `bypassPermissions` and `auto` are dead from project
  scope; `default`, `acceptEdits`, `plan`, and `dontAsk` were all measured to
  be honored. The key has a legitimate resident in `niwa watch`.
- **This is a regression, not a documentation defect.** The clobbering
  measurement is what changed the problem statement between rounds.
- **A merged settings-document builder is not needed for this problem**, and
  the single-slot constraint has a phantom occupant.
- **The recommendation is to stop the round trip rather than rename the key.**
  Renaming into niwa's existing `keepAliveOnDispatch` family is one line and
  has in-repo precedent twice over, but it preserves the round trip and answers
  only the honesty half. The two are not exclusive -- the write-side fix is
  needed either way -- so the write-side and read-side halves can be sequenced.

## Coverage Notes

- **What the materializer should emit instead is unsettled.** Three candidates
  went unresolved: emit nothing for a `bypass` declaration; emit a niwa-owned
  key alongside the existing `keepAliveOnDispatch` family; or emit nothing in
  the two documents with no reader and a niwa-owned key only in the
  instance-root document that has one. The exploration leans toward the last,
  but did not settle it.
- **How the effective config reaches the derivation is unsettled.** The
  exploration established that `provisionInstanceFunc` already computes it and
  that `pipelineResult` already carries five other pipeline outputs the same
  way. It did not decide what the field looks like, whether the derivation
  should move earlier so the SessionStart hook and `niwa watch` can share it,
  or whether a persisted field in `.niwa/instance.json` is wanted as well.
- **Sequencing is constrained and needs stating.** The two `@critical`
  scenarios in `test/functional/features/dispatch.feature` assert only the
  derived argv, never the file, which makes them the right regression net --
  but only if the producer and the reader move in one commit. The seven unit
  tests in `internal/cli/dispatch_permissionmode_test.go` fabricate their own
  fixture bytes and will go green while testing nothing under any rename or
  relocation, so they need deliberate updating; CI will not catch it.
- **Two golden characterization manifests pin the settings documents
  byte-exactly** and will need regenerating under any option, including one
  that only removes a key.
- **A guard for the `--settings` slot is recommended but unowned.** A test
  asserting the flag appears at most once in the assembled argv costs almost
  nothing and converts tribal knowledge into a failing build. It may belong
  here or in a separate issue.
- **The version boundary was not bisected.** 2.1.257 for `bypassPermissions`
  and 2.1.142 for `auto` come from release notes; only 2.1.267 was measured
  directly. Whatever lands should cite the release notes rather than restating
  the repo's current 2.1.258.
- **A second, independent producer of the same dead key exists outside niwa**
  in a legacy installer that predates the materializer. It is a writer, not a
  reader, so it does not constrain this work, but it will keep producing the
  same defect until it is retired.

## Upstream Observations

`docs/designs/current/DESIGN-dispatch-permission-mode.md` is the governing
design for the derivation this work changes. It is accurate about the mechanism
and the trust reasoning, and it already contains the sentence that authorises
deleting `WorkerPermissionMode`. Three things in it are now wrong or
incomplete: it names 2.1.258 rather than 2.1.257; its options section weighs
only *where in the function* the derivation goes and never considers where the
input comes from, which is the question this work reopens; and its security
section argues from "the value is inert" rather than "the value actively
suppresses the operator's own posture", which is a stronger argument for the
same conclusion.

`docs/prds/PRD-config-distribution.md` enshrines the `"ask"` ->
`askPermissions` mapping. `docs/guides/ephemeral-session-instances.md` promises
a root-level bypass posture applies to every root session, which is false.
`docs/designs/current/DESIGN-mcp-root-instance-distribution.md` rests a "no new
code needed" decision entirely on the expired behavior.
`docs/designs/current/DESIGN-agent-capability-contract.md` asserts the
permission posture has no vehicle other than the settings file, which was
already untrue when written.

No ROADMAP exists in this repo -- there is no `docs/roadmaps/` directory -- so
nothing travels on `--upstream`.

## Framing-Shift Answer

**Pre-supplied answer:** yes, the framing shifted.

**Evidence:** the problem statement changed between rounds, twice. Round 1
narrowed "stop writing `permissions.defaultMode`" to "stop writing the values
that cannot work", once the scope rule turned out to spare the restrictive
values and `niwa watch` turned out to depend on one of them. Round 2 then
changed the problem's *category*: the measured clobbering behavior -- a
project-scope `bypassPermissions` winning the merge and being downgraded to
`default`, rather than falling through to the user's own setting -- moved this
from a misleading-document complaint to a live behavior regression that leaves
dispatched sessions more restrictive than doing nothing. The success criterion
moved with it: "a reader is not misled" is no longer sufficient, because a fix
satisfying only that would leave the suppression in place.

## Shape Signals

### Architectural alternatives left open

- **Rename into the niwa-owned key family versus stop the round trip.**
  Renaming costs one materializer line plus a JSON tag, matches
  `keepAliveOnDispatch` and `ephemeralSessionMode` precedent, and leaves the
  read path byte-identical. Stopping the round trip means surfacing the
  effective config through `provisionResult`, which removes a file read and a
  whole class of drift between what was declared and what was recovered, but
  touches the provisioning result type and the step ordering in an already
  dense function. The exploration recommends the second and notes they compose.
- **What the two reader-less documents should carry.** The per-repo
  `settings.local.json` and the workspace-root `settings.json` have no niwa
  consumer at all. Emitting nothing there is the honest option; emitting a
  niwa-owned key everywhere is the uniform one. Note that a sibling public
  repo's acceptance criteria forbid a `permissions` key in the committed
  per-repo `settings.json`, so that document is fenced off regardless.
- **Whether the resolved posture should be persisted as well as passed.**
  `.niwa/instance.json` is a versioned, niwa-owned store with additive
  `omitempty` fields and no migration machinery, and it already holds several
  decide-once-read-later flags. A persisted field would let a resumed session
  or the SessionStart hook act on the same posture. The exploration did not
  establish whether anything needs that.
- **Whether the derivation moves earlier.** Today it sits at the dispatch
  launch seam. `niwa watch` never runs it, which is why its hard-deny posture
  forwards no flag despite a comment saying it inherits one. Moving the
  derivation into the provisioning path would give both callers the same
  resolved posture; keeping it at the launch seam is the narrower change.

### Complexity signals

- The work spans four subsystems -- the materializer, the dispatch input path,
  a dead-code deletion, and a documentation sweep -- with a hard ordering
  constraint between the first two, since the functional scenarios only protect
  the pair when they move together.
- The test surface is deceptive in a specific, nameable way: the coverage that
  looks incidental (two `@critical` scenarios asserting argv) is the real net,
  and the coverage that looks thorough (seven unit tests with hand-written
  fixtures) will pass through a broken change without complaint.
- One contested trade-off remains genuinely open -- the minimal write-side fix
  versus the structural read-side fix -- and it is the kind where reasonable
  people disagree on whether removing a file read justifies touching a
  provisioning result type.
- Nothing about the work is risky in the security sense. It restores an
  operator's declared posture through a channel that works and stops
  suppressing a posture they declared elsewhere; it grants nothing a workspace
  could not already grant itself.
- Blast radius is small and measured: nothing in apply, drift, or verification
  depends on the key, and nothing outside the niwa repo reads it.
