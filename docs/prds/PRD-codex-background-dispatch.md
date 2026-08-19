---
schema: prd/v1
status: Accepted
problem: |
  niwa dispatch refuses any workspace whose resolved agent is not Claude,
  and row 22 of the capability table declares that refusal as niwa's own
  debt. Lifting the refusal naively is not enough: the reclamation sweep
  infers session death from Claude's jobs directory, so a Codex worker's
  instance would be destroyed by the next dispatch while it was still
  working in it.
goals: |
  A developer in a codex-default workspace dispatches a background Codex
  worker, and it launches, is captured, can be resumed, and is never reaped
  while alive -- through one agent seam, not a parallel pass. Row 22 flips
  to implemented in the change that delivers it, the gate becomes a
  declaration lookup, and every remaining gap is declared where the
  generated gap list already lives. The seam lands first against Claude
  only, its four user-visible changes named rather than claimed away;
  Codex arrives as its second implementation.
upstream: docs/briefs/BRIEF-codex-background-dispatch.md
motivating_context: |
  The capability contract made workspace preparation serve both agents and
  put the dispatch refusal on the generated gap list as not-built, out of
  its own scope. This feature closes that row. The prior dual-agent
  attempt failed by shipping agent-specific behavior as a parallel
  hardcoded pass; dispatch is where that failure mode is most likely to
  recur, because Claude's session bookkeeping sits beside every seam.
---

# PRD: codex background dispatch

## Status

Accepted

This PRD owns the requirements for delivering `niwa dispatch` to Codex
across four surfaces -- launch, capture, resume, liveness -- as two
sequenced pull requests. The downstream design owns the shape of the
per-agent launch description, the mechanics of the launch-route binding,
the structural scan's construction, and the resume handle's type.

## Problem Statement

`internal/cli/dispatch.go` refuses, at its step (2b), any workspace whose
resolved agent is not Claude. Row 22 of the capability table in
`internal/agentplan/declaration.go` declares that refusal as niwa's own
debt: `DispatchLaunch` for Codex is unavailable, not-built, "Nothing in
niwa knows how to start a Codex worker, recover which session it became,
or step back into one." An earlier form of the reason cited the
per-agent model table's Codex entries as half-built groundwork; the
launch route's binding check identified those entries as a delivery no
declaration stood behind -- nothing read them, because the refusal fired
first -- and they are gone. The generated gap list in
`docs/guides/codex-agent.md` publishes the same sentence, and the
functional scenario at `test/functional/features/codex-agent.feature:83`
pins refusal, declaration, and gap list together as today's correct
behavior.

Be clear-eyed about the scaffolding underneath: `internal/cli` does not
import `internal/agentplan` at all today. Row 22 has an opinion and no
code reads it. `RouteLaunch` -- the route whose declaration is supposed to
*be* the gate -- has no live enforcing test the way the plan and procedure
routes do. Whatever ties the gate to the declaration is new, and it is the
thing that stops the guide and the refusal from drifting apart later.

The rest of the dispatch flow is Claude-shaped by name rather than by
structure. Capture polls Claude's jobs directory for a state file whose
recorded cwd matches the instance; resume and the printed management hints
key on Claude's short job id; the reclamation sweep decides an ephemeral
instance's fate by checking that same jobs directory. None of these
mechanisms is wrong for Codex -- measurement showed the same
correlate-by-cwd, poll-until-unambiguous shape works against Codex's
on-disk session records -- but every one of them reaches Claude by naming
it, and a Codex delivery bolted on beside them would be the parallel
hardcoded pass the capability contract exists to prevent.

## Goals

- A developer in a codex-default workspace runs `niwa dispatch "<task>"`
  with no new flag and gets what a Claude-default workspace always got: a
  fresh instance, a running worker, a captured session id, a durable
  mapping, and a way back in.
- Agent-specific behavior on the dispatch path is reached through the
  capability contract, never by naming an agent at a call site, and a test
  that has been seen failing holds the property.
- Session identity has one representation and one store for both agents,
  and the mapping records its agent.
- A live Codex worker's instance is never reclaimed. The gap that remains
  -- no automatic reclamation for Codex-dispatched instances -- is
  declared in the table and published in the guide, with its closure named
  as future work.
- The seam lands first against Claude only. Its behavior delta is stated
  exactly -- four named user-visible changes, everything else proven
  unchanged -- concretely enough that a reviewer can check the claim
  mechanically.

## User Stories

- As a developer on a codex-default workspace, I want `niwa dispatch` to
  launch a Codex worker in a fresh instance without my naming an agent on
  the command line, so that dispatching work is the same verb regardless
  of which agent my workspace runs.
- As a developer with a Codex worker mid-task, I want the next dispatch's
  reclamation sweep to leave my worker's instance alone, so that
  parallel dispatches don't destroy each other's work.
- As a developer returning to a dispatched session, I want one resume verb
  that works for whichever agent the session ran, so that I don't need to
  remember which agent a session was before I can step back into it.
- As a maintainer reviewing the first pull request, I want its behavior
  claim to name its user-visible changes outright and rest on named seam
  signatures and untouched behavior assertions rather than on "the suite
  passes," so that a signature change and its updated fakes can't smuggle
  an unnamed behavior change past me.
- As a developer reading the guide, I want the gap list to say exactly
  what a Codex dispatch doesn't do -- no keep-alive, no automatic
  reclamation -- so that I learn the boundary by reading rather than by
  losing an instance or waiting for a cleanup that never comes.

## Requirements

### Selection and the gate

- **R1. No new selection flag.** The agent a dispatch launches is resolved
  exactly as every other niwa surface resolves it: `NIWA_AGENT`, then the
  workspace `default_agent`, through the existing resolution call with an
  empty flag argument. `niwa dispatch --agent` keeps its current meaning
  -- Claude's subagent-type passthrough, forwarded to the worker -- and
  never participates in niwa-agent resolution. No `--agent-type` or any
  other selector is added; a planning document elsewhere used that
  spelling, and it corresponds to nothing in the code. Acceptance: a test
  fails if dispatch's flag set gains an agent selector, or if a dispatch
  in a codex-default workspace with `--agent <subagent>` set resolves to
  anything but Codex.
- **R2. The gate is a declaration lookup.** Dispatch's agent gate consults
  the `DispatchLaunch` declaration for the resolved agent instead of
  comparing against an agent constant. A declared-implemented agent
  proceeds; a declared-unavailable agent is refused with a message that
  renders the declared reason and names the agents the table declares
  launchable, enumerated from the declarations so the hint cannot go
  stale. Acceptance: the failure this catches is
  drift -- a test fails when the gate refuses an agent whose row is
  implemented, or admits one whose row is unavailable; hardcoding the
  refusal so it agrees with the table today would leave that test unable
  to fail when the table changes, which R4's binding also rejects.
- **R3. Preflight before provisioning.** The launched agent's binary is
  checked on PATH after the gate and before any instance is created, so an
  absent binary fails with no instance directory and no mapping on disk.
  This ordering is currently pinned by exactly one test, which asserts
  the provisioner was never called when `claude` is absent. Acceptance:
  the same property holds per agent -- a dispatch in a codex-default
  workspace with no `codex` on PATH fails with `provisionCalled == 0`,
  and reordering the preflight after provisioning fails both agents'
  tests.

### The contract and its binding

- **R4. Row 22 flips with its delivery, and the binding fails in both
  drift directions.** The `DispatchLaunch` declaration for Codex becomes
  implemented in the change that delivers the launch, never before. The
  binding must make the declaration and the delivery inseparable: deleting
  the per-agent launch delivery fails the declaration check, and adding a
  launch delivery for an agent whose row is unavailable fails it too. The
  bar is concrete: if the delivery can be deleted and the declaration
  still passes, the binding is not doing its job for this row. Acceptance:
  both directions are demonstrated as failing tests during development
  and recorded in the PR description.
- **R5. The closed capability set stays closed.** No new capability row is
  added. `DispatchKeepAlive` (row 21) stays declared no-such-concept for
  Codex with no partial bridge toward it, and every capability this work
  touches ends implemented or declared unavailable with a reason --
  nothing in neither state. If the work appears to need a 25th row, that
  is a product decision to escalate, not an implementation detail.
  Acceptance: the existing exhaustiveness suite over the 24-row set passes
  unchanged in both PRs; a diff adding a `Capability` constant fails
  review by rule.
- **R6. No agent constants at call sites in the dispatch path.** The
  dispatch-path files -- dispatch, launcher, capture, model resolution,
  and the resume file -- name neither agent constants nor agent binary
  names as literals; agent-specific behavior is reached through the
  contract. This is held by a new structural test over the syntax tree
  (so comments don't trip it and formatting can't hide from it), scoped by
  a denylist of dispatch-path files. The test must be shown able to fail:
  it is red on today's tree at four known sites -- the step (2b) refusal,
  the model resolver's hardcoded agent, the per-agent model tables, and
  the launcher's binary lookup -- and turning it green is PR 1's own work.
  The existing scan in `internal/agentplan` and its pending-conversion
  file list are not touched; they belong to a different feature.
  Acceptance: the scan fails when any denylisted file regains an agent
  constant or binary-name literal, and the PR that lands it says how it
  was seen red.

### Launch

- **R7. The worker launches detached, per the agent's launch mode.** How
  a worker is started is per-agent data, not launcher logic: Claude's
  background launch returns promptly, so its launcher runs the command to
  completion; Codex's headless execution runs the whole turn in the
  foreground, so its launch is start-and-release. Three properties are
  hard requirements grounded in measurement: the worker's stdin is
  `/dev/null`, never inherited or left an open pipe (a Codex worker with
  open stdin blocks indefinitely with nothing on disk to diagnose it);
  stdout and stderr are captured to separate destinations and never
  merged (Codex writes diagnostics to stderr on healthy runs); and
  non-empty stderr is not treated as failure -- exit codes are. The
  prompt remains a single argv element, never shell-interpolated.
  Acceptance: a launcher that leaves stdin open fails a test rather than
  hanging one; merged streams fail; a healthy run with non-empty stderr
  is not reported as an error.
- **R8. The launch works from an instance root: `--skip-git-repo-check`,
  and nothing written there.** An instance root is not a git repository,
  and the Codex launch will not start in one without
  `--skip-git-repo-check` -- measured, and marking the directory trusted
  does not satisfy that check: the identical startup failure reproduces
  with the path trusted, so the flag is mandatory rather than a trust
  workaround. And this feature writes nothing Codex-shaped at the
  instance root. Both properties hold whatever the disposition of the
  instance-root delivery gap turns out to be (see the open questions).
  Acceptance: a launch argv missing `--skip-git-repo-check` fails the
  launch test, including with the instance root trusted; the diff adds
  no Codex-read content or configuration at the instance root.
- **R9. No dispatch causes a write to the developer's Codex
  configuration.** Codex appends a trust stanza for the working
  directory to the developer's own configuration file whenever an
  effective elevated posture meets an untrusted directory -- from the
  sandbox flag and from a config-shaped sandbox override alike: one
  stanza per dispatch, into a shared file that is never pruned, racing
  concurrent dispatches on a write niwa holds no lock for, each stanza
  outliving the instance it names once the instance is reaped. niwa
  must not cause that, and it doesn't have to: a per-invocation trust
  override is measured to set the elevated posture with zero footprint
  on the developer's configuration -- nothing written, nothing to
  retract, the grant scoped to one process. Row 22 therefore carries no
  directory-trust requirement edge: this launch needs nothing from
  directory trust, and an edge that isn't load-bearing is a false
  dependency. The mechanism is the design's, under two measured traps:
  the dotted-path spelling of the same override parses without error
  and silently does nothing, posture left read-only -- so a clean exit
  from an override is not proof it took effect -- and overriding the
  sandbox mode through configuration instead of the flag still writes
  the stanza, because the write-back keys on the effective posture, not
  its source. Acceptance: the developer's Codex configuration is
  byte-identical before and after a dispatch; the named failure a test
  catches is a launch argv that would make Codex write a trust stanza.

### Session identity and capture

- **R10. One identity representation, and the mapping records its agent.**
  Whatever niwa stores to answer "which session is this, and how do I get
  back to it" round-trips both agents' identifiers through the same type
  and the same store, with no agent conditional at the read or write
  site. Codex session ids are UUIDs and pass the existing session-id
  validation unchanged, so the store needs no second format. The mapping
  records which agent it belongs to, and the agent is never inferred from
  the shape of an id -- the reaper cannot pick a liveness rule without
  it. The representation carries a resume handle whose relationship to
  the session id is the agent's business, not the caller's: Claude's
  management commands take a short id distinct from the UUID, Codex's
  take the UUID itself. Acceptance: a test round-trips a Codex-agent
  mapping through write, list, and read; a mapping without an agent
  fails validation; and a reader that switches on id shape has no test
  to hide behind because the shapes are identical.
- **R11. One capture, pluggable candidate source.** There is one capture
  mechanism with a per-agent candidate source, not two captures that
  resemble each other. The mechanism's rules are fixed and shared:
  candidates are correlated to the dispatch by recorded working directory
  equal to the instance directory (after symlink resolution and path
  cleaning on both sides); zero matches at timeout is an error; more than
  one candidate claiming the same cwd is an ambiguity error, never a
  guess; and a candidate that exists but is not yet readable keeps being
  polled rather than failing the capture. Clock, poll interval, and
  candidate directories stay injected so the whole path is
  offline-testable. Acceptance: the proving test runs the same
  correlation, ambiguity, timeout, and not-yet-ready assertions against
  both agents' candidate sources; a second capture implementation, or a
  source that bypasses the shared rules, fails it.
- **R12. Codex capture reads the on-disk session record.** The Codex
  candidate source is the session record Codex itself persists on disk
  (the rollout file), not the launch-time event stream. This is a
  requirement on what capture must be: one on-disk store per agent,
  serving both capture and liveness, exactly as Claude's jobs directory
  already serves both. The record appears early enough to poll for at
  launch, a resume appends to it rather than creating a new file, and
  Codex mints the session id itself with no way to pre-assign one -- so
  capture-once-at-launch is sound and any design premised on niwa
  choosing the id is dead. The event stream is the recorded, considered
  alternative: it would fuse launch and capture for one agent while
  leaving them separate for the other, and liveness would still need the
  on-disk store. How the record is read -- paths, parsing, the local-time
  date tree, the oversized first line -- is the design's. Acceptance: a
  capture that consumes the worker's stdout stream for the id, or a
  liveness check that reads a different store than capture, fails
  review against this requirement; the capture behavior itself is held
  by R11's shared assertions.

### Resume

- **R13. Resume is one verb.** Stepping back into a dispatched session is
  a single code path for both agents: the mapping lookup, the handle
  validation, and the exit-code propagation are shared and agent-neutral,
  and only the launched command varies, reached through the contract. Two
  resume implementations selected by a conditional at the call site is
  the named failure mode, however tidy each half looks. Acceptance: a
  test substitutes the launched command and shows the shared half --
  lookup, validation, propagation -- running unchanged for both agents;
  a second resume entry point fails the structural scan (R6) or this
  test.

### Liveness and reclamation

- **R14. A live worker's instance is never reaped, whatever its agent.**
  The reclamation sweep answers "does this session still exist" with the
  liveness rule for the mapping's recorded agent, through the same
  per-agent description the launch and capture come from. The Codex rule
  is the same entry-present shape as Claude's -- the session record is
  gone -- with the same spare-while-resumable semantics. Acceptance: the
  named failing scenario is the one the code has today -- a second
  dispatch reclaiming a live Codex worker's instance because the sweep
  consulted Claude's jobs directory. A test dispatches (or fakes) a
  Codex-agent mapping with a present session record and asserts the
  sweep spares it; removing the record makes it eligible only under the
  rules R15 declares.
- **R15. The reclamation gap is declared, not merely honest in prose.**
  Codex session records are never aged out by Codex, so a
  Codex-dispatched instance is not reclaimed by the mapping path until
  the record is deleted or the instance destroyed. That cost is declared
  where the gap list already lives -- visible in the declaration table's
  row commentary and rendered in the published guide -- and its closure
  (a name-plus-TTL-plus-mtime liveness backstop for Codex) is named as
  the next feature's work, so the next author inherits the boundary
  rather than rediscovering it. Building that backstop here is out of
  scope; this feature's job is the narrower one of not introducing data
  loss. Acceptance: the guide's regenerated section and surrounding prose
  state the gap and its owner; the gap-list drift test fails if the
  declared reason and the committed guide diverge; and no TTL or mtime
  rule for mapped Codex instances appears in the diff.

### Documentation and errors

- **R16. The gap list regenerates, and the prose around it moves in the
  same change.** The generated section of `docs/guides/codex-agent.md` is
  regenerated from the declarations via the existing update flag, never
  hand-edited; the same test without the flag passes. The hand-written
  prose around the markers -- which today says dispatch is one of the
  routes niwa hasn't wired up -- contradicts the regenerated block unless
  it is edited in the same change. Both halves are required. Acceptance:
  the drift test fails on a hand-edited generated section or an
  unregenerated one; review rejects a diff that regenerates the block
  while leaving the surrounding prose describing the refusal.
- **R17. Exit status is reported for what it is.** A Codex worker's exit
  code is not a task-success signal: under the default read-only sandbox
  a worker fails every write and still exits 0, with the failure visible
  only in the model's own final message -- measured. And exit 1 covers
  every API error alike (R18). Dispatch and resume therefore report only
  what the exit status can support -- the session ran and ended, with
  which code -- and never render exit 0 as "the task succeeded."
  Acceptance: the silent-failure mode a test catches is a worker that
  exits 0 having failed its writes being reported as task success; the
  reporting for that run claims completion of the session, never of the
  task.
- **R18. Quota exhaustion is classified, never acted on.** A Codex API
  error of any kind exits 1, so quota exhaustion is not distinguishable
  by exit code; classification means recognizing the error payload's
  markers (`usage_limit_reached`, `UsageLimitReached`,
  `CreditsDepleted`). Dispatch and resume surface the condition as what
  it is rather than a generic launch failure. No automatic agent
  switching, retry, or fallback is built on it; acting on the condition
  is a policy decision nobody has made. Acceptance: a test feeds a
  quota-shaped error payload and asserts the classified message; a
  generic failure stays generic; and no code path selects a different
  agent in response.

### Sequencing and proof

- **R19. Two pull requests, in order.** PR 1 lands the dispatch path's
  agent seam against Claude only, with no behavior change beyond the
  four user-visible changes R20 names: the gate as a declaration lookup,
  the identity representation, the generalized capture, and the
  structural scan going red to green. PR 2 delivers
  Codex as the second implementation: launch, capture, resume, liveness,
  row 22 flipped, guide regenerated. If PR 2 grows beyond one reviewable
  change it splits by surface -- launch, then capture and liveness, then
  resume -- never into "the plumbing" and "the behavior"; that second
  split is what made a prior attempt unreviewable.
- **R20. PR 1's behavior claim is exact: four user-visible changes,
  named, and nothing else.** A flat "no behavior change" would be false,
  and an overstated claim in the documents a bisecting reader trusts --
  a squash-merged PR body becomes the permanent commit message -- is the
  prior attempt's failure in a smaller frame. PR 1 carries exactly four
  user-visible changes, three incidental to routing the gate through the
  declaration and one a fix:
  - The refusal's wording. It renders the declaration's own reason and
    names the agents the table declares launchable, enumerated from the
    declarations rather than written into the string. The old message's
    hardcoded escape-hatch hint would go stale silently the day a row
    flips, and dropping it outright would leave the reader without the
    one fact they are missing.
  - The gate runs even when the workspace config cannot be loaded.
    Previously the whole gate sat inside a successful-config-load
    branch, so `NIWA_AGENT=codex` with an unreadable config skipped the
    check and launched Claude anyway; the agent now resolves from the
    environment alone and the refusal fires. A genuine fix riding along,
    named as one rather than found.
  - The `--model` help lists the portable categories, not one agent's
    concrete model names -- text that would start lying the day a second
    agent shipped. The names still appear in the unrecognized-value
    warning.
  - The preflight error names the missing binary rather than a product.
  Everything else is proven unchanged, and the naive proof -- "the suite
  passes" -- is defeated by changing a seam's signature and its fakes in
  the same commit. The dispatch path's substitutable seams are
  package-level function variables, and every substituting test breaks
  when a signature changes, so the proof is: the seams whose signatures
  change are named in the PR description with the reason (capture and
  launch need the agent; others only if argued), every other seam
  declaration is byte-identical, and the behavior assertions running on
  top of the substituted seams are not modified or deleted -- new
  assertions may be added. Acceptance: the PR description names the four
  user-visible changes and every changed seam signature; a reviewer can
  diff the seam declarations against main and check both lists are
  exhaustive; an unnamed user-visible change, an unnamed signature
  change, or a weakened behavior assertion fails the review contract
  this requirement defines.
- **R21. The functional scenario is extended, not replaced.** The
  scenario at `test/functional/features/codex-agent.feature:83` --
  "dispatch's refusal in a codex-default workspace is the declared gap"
  -- currently pins the refusal, the declaration, and the committed gap
  list together. PR 2 extends that same scenario to assert the declared
  delivery: a dispatch in a codex-default workspace proceeds past the
  gate, `dispatch-launch` is declared implemented for Codex, and the gap
  list no longer carries it. A parallel scenario left beside the old one
  would let the refusal assertions survive the delivery and fail the
  suite -- or worse, be deleted quietly. Acceptance: the feature file
  diff shows one scenario transformed; no scenario asserting the refusal
  remains once row 22 is implemented.

### Non-functional

- **N1. Standard toolchain only.** The structural scan and binding tests
  use the Go standard library; no new module dependencies. CI remains
  `gofmt -l .`, `go vet ./...`, and `go test -race ./...`.
- **N2. The critical path tests offline.** Every acceptance property above
  is testable without a `codex` binary, a network, or a live model, via
  the injected seams and fake candidate sources. Anything that genuinely
  needs a live Codex session follows the feature file's existing
  convention: tagged, gated on the binary's presence, and never the only
  coverage for a mechanism.

## Acceptance Criteria

Gate and contract:

- [ ] A dispatch in a codex-default workspace with `codex` on PATH
  proceeds past the gate; the same dispatch fails today. The gate reads
  the declaration: flipping row 22 back to unavailable restores the
  refusal, with the declared reason in the message, and no code change.
- [ ] Deleting the Codex launch delivery fails the declaration suite;
  adding a launch delivery for an agent declared unavailable fails it in
  the other direction. Both failures were observed and are recorded in
  the PR description.
- [ ] The structural scan over the dispatch-path files fails on today's
  tree at the four known sites and passes at PR 1's head; reintroducing
  an agent constant or binary-name literal into a denylisted file makes
  it fail again.
- [ ] A dispatch with the launched agent's binary absent from PATH fails
  before any instance exists: `provisionCalled == 0`, no instance
  directory, no mapping -- asserted per agent.
- [ ] The 24-row capability set is unchanged; `DispatchKeepAlive` for
  Codex still declares no-such-concept; no capability this work touches
  is left in neither state.

Identity, capture, resume:

- [ ] A Codex-agent session mapping round-trips write, list, and read
  through the same store and type as a Claude one; a mapping missing its
  agent fails validation; the reaper reads the recorded agent, never the
  id's shape.
- [ ] One capture test suite runs the same four assertions -- cwd
  correlation, ambiguity error on two claimants, error at timeout on
  zero, keep-polling on a present-but-unreadable candidate -- against
  both agents' candidate sources, offline, with injected clock and
  directories.
- [ ] The Codex candidate source is the on-disk session record; capture
  and liveness read the same store; nothing parses the worker's stdout
  for a session id.
- [ ] The resume path's shared half -- mapping lookup, handle validation,
  exit-code propagation -- runs unchanged for both agents under a
  substituted launch command; there is no second resume entry point.
- [ ] A Codex worker launched by the test launcher has `/dev/null` stdin
  and separate stdout/stderr destinations; a healthy nonzero-stderr run
  is not an error; a quota-shaped payload classifies as quota
  exhaustion and triggers no agent switch.
- [ ] The Codex launch argv carries `--skip-git-repo-check`, and the
  launch test fails without it even with the instance root trusted;
  nothing Codex-shaped is written at the instance root in either PR.
- [ ] The developer's own Codex configuration is byte-identical before
  and after a dispatch; a launch argv that would make Codex write a
  trust stanza is the named failure the test catches.
- [ ] A worker that exits 0 having failed its writes is not reported as
  task success; the completion message claims the session ended, not
  that the task succeeded.

Liveness and reclamation:

- [ ] With a live Codex worker's session record present, a second
  dispatch's reclamation sweep spares its instance -- the scenario that
  destroys it on today's code is the named regression target.
- [ ] With the record gone, the instance is reclaimed only under the
  declared rules; no TTL or mtime rule for mapped Codex instances exists
  in the delivered code.

Documentation and process:

- [ ] The guide's generated section is regenerated (the drift test passes
  without the update flag), and the surrounding hand-written prose no
  longer describes dispatch as unbuilt; the reclamation gap and its
  named next-feature owner appear in the published guide.
- [ ] `codex-agent.feature`'s dispatch scenario asserts the declared
  delivery; no scenario pinning the refusal survives PR 2.
- [ ] PR 1's description names its four user-visible changes and every
  seam whose signature changed and why; every unnamed seam declaration
  is byte-identical to main; no user-visible change exists outside the
  named four; no existing behavior assertion on the dispatch path was
  modified or deleted.

## Out of Scope

- `niwa session attach` / `niwa worktree attach`,
  `internal/cli/sessionattach/`, and `internal/worktree/`. Dispatch
  resume lives on the dispatch path's own attach step; editing those
  surfaces is the signal a boundary has been crossed. Their inert state
  is a real defect owned by whoever owns that command.
- `internal/cli/watch.go`'s review-continuation path (`claude stop`,
  `--resume`). It belongs to watch.
- Ephemeral-session provisioning (row 17) and dispatch keep-alive
  (row 21). Both stay declared unavailable for Codex; no partial bridge.
- Automatic agent switching on quota exhaustion. Detection is in scope
  only for classification (R18); acting is a policy decision nobody has
  made.
- Any route installing a niwa-owned hook for Codex. The binary carries a
  hook-trust bypass flag whose behavior nobody has measured; shipping it
  is a policy call, not this feature's.
- Slash-command and extension-tree distribution for Codex. Whether Codex
  surfaces plugin command trees at all is unmeasured -- an open question
  upstream of this work, not a small thing to finish while in the area.
- The Codex liveness backstop (name-plus-TTL-plus-mtime aging of
  unreclaimed instances). This feature declares the gap (R15); building
  the backstop is the next feature, named as such.
- Converting the four files under the existing `internal/agentplan` scan's
  pending-conversion gate. They belong to a different feature (R6).

## What a Dispatched Codex Worker Does Not Receive

A dispatched worker at the instance root receives none of what rows 5, 8,
9, and 12 declare implemented -- skills, MCP servers, environment, and
posture are all delivered inside repositories -- and no orientation
either. niwa never launched a Codex session at an instance root before,
so this feature introduces the gap rather than inheriting it, and it is
worse than "unoriented": discovery is fixed at session construction and
keyed to the launch directory, following neither the working directory
nor an instruction naming a repository, and readable-on-request files are
a categorically weaker delivery than context. The contract cannot
currently express this gap -- declarations are per capability and agent,
two states, scoped by who receives and never by where from -- and no new
capability row is invented for it (R5 stands). Whether closing it is this
feature's work or a follow-on is being decided above this feature, given
that closing means correcting row 2's stated reason (factually wrong: a
session's working directory always contributes its context file, measured
with and without a marker-bearing ancestor, so a context file at an
instance root would be read) and writing Codex-shaped content at the
instance root -- both touching already-shipped declarations.

## Open Questions the Design Owns

- **The shape of the per-agent launch description.** What it carries --
  command and argv construction, launch mode (run-to-completion versus
  start-and-release), the capture candidate source, the liveness probe,
  the resume command -- whether it is data or behavior, and where it
  lives relative to the declaration table it answers for.
- **How a launch-routed capability binds in both drift directions.** The
  existing binding mechanisms check registrations that a launch doesn't
  have: plan entries carry capability tags, procedures live in a
  registry, but nothing in the workspace layer exists for a launch to
  bind to, and inventing a fake registration would recreate the
  agrees-with-itself problem the binding table prevents. The design owns
  a mechanism that meets R4's bar without cross-package name matching.
- **Where the new structural scan lives and what it scans.** The denylist
  of dispatch-path files (including how the not-yet-written resume file
  gets onto it), the exact symbol set -- agent constants, binary-name
  literals, anything else -- and how red-then-green is demonstrated
  inside one PR. The repo has a file-scoped AST-test idiom to model on;
  the design picks the file and the walk.
- **The resume handle's type.** Claude's management commands take a short
  id that is not the session UUID; Codex has no second handle -- its
  sessions are named by the UUID. Whether the handle is a plain field
  that happens to equal the session id for Codex, or a typed value only
  the agent's description interprets, decides what validation the shared
  resume path (R13) can honestly perform.
- **Where the instance-root delivery gap lands.** The gap itself is
  stated under "What a dispatched Codex worker does not receive" above.
  Whether closing it is this feature's work or a follow-on is being
  decided above this feature; the design inherits whichever answer
  lands.
- **Whether a per-invocation project-root-marker override reaches an
  instance root.** A per-invocation configuration override of Codex's
  project-root markers is being measured right now and is not settled.
  Even if it works, it buys a reachable directory rather than a populated
  one -- niwa deliberately writes no Codex-shaped content at an instance
  root today -- so its disposition follows the instance-root landing
  question above.
- **How an implemented row carries a declared cost.** Row 22 flips to
  implemented, yet R15 requires the reclamation gap to be visible in the
  table and the guide. The declaration schema renders reasons only for
  unavailable rows, so the design owns where an implemented row's caveat
  lives -- row commentary feeding the generator, a guide-prose
  obligation, or something else -- without adding a third state.

## References

- docs/briefs/BRIEF-codex-background-dispatch.md -- the upstream framing
  this PRD's requirements are written from.
- docs/prds/PRD-agent-capability-contract.md -- the contract this feature
  delivers through; its row 22 and out-of-scope list name this work.
- docs/designs/current/DESIGN-agent-capability-contract.md -- Decision 3's
  `RouteLaunch` bullet states the gate-as-declaration-lookup shape, and
  its enforcement-test families are the vocabulary R4 and R6 extend.
- docs/guides/codex-agent.md -- the published guide whose generated gap
  list and surrounding prose change under R15 and R16.
- internal/agentplan/declaration.go and internal/agentplan/capability.go
  -- the declaration table and the closed set (R2, R4, R5).
- internal/cli/dispatch.go and internal/cli/dispatch_capture.go -- the
  refusal, the flow, and the capture mechanism R11 generalizes.
- test/functional/features/codex-agent.feature -- the scenario R21
  extends.
