---
status: Accepted
problem: |
  Developers fanning out Claude Code background agents from a niwa workspace have no
  reliable one-step way to put each worker in its own fully-configured ephemeral
  instance. The automatic hook path cannot deliver instance configuration and
  mis-targets workers; the manual pre-create-then-launch path works but is several
  error-prone steps and records nothing for cleanup.
goals: |
  Ship a net-new, additive niwa command that, in one invocation, creates an ephemeral
  instance, launches a Claude Code background worker rooted inside it (so the worker
  loads that instance's full configuration and appears in Agent View), records the
  session-to-instance relationship, and leaves no unreclaimable instance behind under
  any failure or teardown path. The existing hook auto-provisioning is left untouched.
upstream: docs/briefs/BRIEF-instance-dispatch.md
motivating_context: |
  A prior exploration proved a Claude Code SessionStart hook can never re-root a
  session's settings resolution, so the existing hook-based isolation is capped at
  file-tree separation. Launching `claude --bg` from inside an instance directory was
  verified to deliver full per-instance configuration and still register the session in
  Agent View -- the mechanism this PRD specifies a command around.
---

# PRD: niwa instance-dispatch command

## Status

Accepted

This PRD is marked Complex: it warrants a downstream DESIGN before implementation
(concurrency on instance naming, session-identity capture under a race, and
partial-failure atomicity are real architectural questions). The exact command verb,
flag spellings, and mechanism choices are deferred to that DESIGN; this PRD fixes the
WHAT and WHY, including the full corner-case requirement surface.

## Problem Statement

niwa creates ephemeral workspace instances, each a full clone carrying its own
materialized Claude Code configuration (settings, plugins, skills, hooks, environment).
A developer who fans out Claude Code background agents wants each worker to run in its
own instance so workers do not collide on branches and files and each worker operates
with the configuration its instance carries.

Two paths exist today and both fall short. The automatic SessionStart-hook path
provisions an instance and tells the agent to `cd` into it, but Claude Code resolves a
session's configuration at launch from the launch directory and a mid-session `cd` does
not re-root that resolution, so the worker never loads its instance's plugins, hooks, or
environment; the same hook also keys background-worker detection on an unreliable signal
and silently skips the common case. The manual path -- pre-create an instance, then
launch a background worker from inside it -- does deliver full configuration and Agent
View registration, but it is several manual steps every time, easy to get wrong, and
records nothing that lets niwa reclaim the instance afterward.

The affected user is a developer running parallel Claude Code agents from a niwa
workspace. What is missing is a single, reliable command that performs the manual
path's correct sequence and records what reclamation needs, so the developer neither
assembles it by hand nor falls back to the hook path that cannot deliver configuration.

## Goals

- A developer dispatches an isolated, fully-configured background worker with one
  command and one task prompt, and can manage the resulting session in Agent View.
- Every dispatched instance is reclaimed automatically when its session ends, by any
  means, with no manual cleanup and no growing pile of abandoned instances.
- No failure path -- partial or concurrent -- leaves an instance that cannot be
  reclaimed.
- The feature is purely additive: the existing hook auto-provisioning behaves exactly
  as it does today.

## User Stories

- As a developer at a workspace root, I want to hand a task to a background agent with
  one command, so that it works in isolation while I keep using my own tree.
- As a developer running several agents at once, I want each dispatch to get its own
  instance without my coordinating names or directories, so that parallel workers never
  collide.
- As a developer, I want a dispatched worker to load the same skills, plugins, and
  environment my instance configuration declares, so that the agent is as capable as a
  session I would start by hand.
- As a developer, I want instances cleaned up whether a session finishes, is stopped, is
  deleted in Agent View, crashes, or is lost to a reboot, so that I never hunt for
  abandoned clones.
- As a developer who dispatches from inside an existing instance or from an unrelated
  directory, I want predictable behavior -- a correct workspace resolution or a clear
  error -- so that I am never surprised by where an instance landed.

## Requirements

Requirements use SHALL for mandatory behavior. "The command" is the net-new dispatch
command (working name `niwa dispatch`; final verb owned by the DESIGN).

### Command surface

- **R1.** The command SHALL be a net-new, additive niwa subcommand. It SHALL NOT modify
  the behavior of the existing SessionStart/SessionEnd hook auto-provisioning, `niwa
  create`, `niwa reap`, `niwa apply`, or the worktree commands.
- **R2.** The command SHALL accept the worker's task prompt as input and SHALL accept an
  optional human-friendly label for the instance/session.
- **R3.** On success the command SHALL report, to stdout, the launched session's
  identifier and how to manage it (attach/inspect/stop), so the developer can reach the
  session in Agent View or from the terminal.
- **R4.** The command SHALL function independently of the ephemeral-session-mode master
  switch that gates the hook path. Dispatch is an explicit user action and SHALL NOT
  require ephemeral mode to be enabled, nor SHALL it consult or toggle that switch.

### Launch-location resolution

- **R5.** Invoked at a workspace root, the command SHALL create the new instance under
  that workspace root.
- **R6.** Invoked from inside an existing instance, the command SHALL resolve the
  enclosing workspace root and create the new instance as a sibling under that root. It
  SHALL NOT create an instance nested inside another instance.
- **R7.** Invoked from inside a worktree or a repo checkout within a workspace, the
  command SHALL resolve the enclosing workspace root and create the new instance under
  that root.
- **R8.** Invoked from a directory that does not resolve to any niwa workspace, the
  command SHALL fail with a clear, actionable error and SHALL NOT create any instance,
  launch any session, or write any mapping.
- **R9.** Workspace resolution SHALL use niwa's existing cwd classification; the command
  SHALL NOT introduce a second, divergent notion of "which workspace am I in."

### Instance creation

- **R10.** The command SHALL create the instance through niwa's existing instance-create
  path, so the instance is a full, normally-materialized instance (including its
  `.niwa/instance.json`, its materialized Claude configuration, and its declared
  environment such as `claude.env`/`GH_TOKEN`).
- **R11.** Because there is no concurrency-safe reservation in the numbered-instance
  naming scan, the command SHALL name each instance from a freshly-generated unique
  token determined before creation, so that two simultaneous dispatches cannot resolve
  to the same instance name or directory.
- **R12.** The command SHALL trigger the same opportunistic reclamation sweep that
  `niwa create` performs, so repeated dispatch self-bounds the number of orphaned
  instances.
- **R13.** If instance creation fails, the command SHALL surface the error and SHALL
  leave no partially-materialized instance directory behind that lacks a reclamation
  path (see R32-R35).

### Session launch

- **R14.** The command SHALL launch a Claude Code background session (`claude --bg`)
  with its working directory set to the newly-created instance directory, so the session
  resolves that instance's configuration at launch.
- **R15.** The command SHALL pass the task prompt to the background session as the
  session's initial prompt.
- **R16.** The command SHALL verify the `claude` executable is available before creating
  the instance, so an unavailable `claude` fails fast and never produces an
  instance-without-a-session.
- **R17.** The launched session SHALL be listable and attachable via Agent View
  (`claude agents` / `claude attach`) after launch, and SHALL remain so; the command
  SHALL NOT take any step that de-registers it.

### Session-identity capture

- **R18.** The command SHALL capture the launched session's short identifier from the
  background-launch output (which today is human-formatted, e.g. `backgrounded ·
  <short-id>`, with no machine-readable mode).
- **R19.** The command SHALL resolve the launched session's full canonical UUID (the key
  the durable mapping requires) from the session's Claude Code job state, since the
  durable mapping store rejects any session id that is not a canonical UUID.
- **R20.** Capture SHALL tolerate the job-state-not-yet-written race with a bounded
  wait/retry, and SHALL treat exhaustion of that bound as a capture failure (R22), not as
  a silent success.
- **R21.** When the short identifier is ambiguous (more than one candidate session
  matches), the command SHALL resolve the correct session deterministically (for example
  by correlating the instance directory the command launched in against the job state's
  recorded working directory) or SHALL treat it as a capture failure rather than guess.
- **R22.** If session-identity capture fails for any reason (output not parseable, job
  state never appears within the bound, unresolved ambiguity), the command SHALL treat
  the dispatch as failed and SHALL apply the partial-failure handling in R32-R35 so no
  unreclaimable instance remains.

### Session-to-instance mapping

- **R23.** On successful capture, the command SHALL write a durable session-to-instance
  mapping keyed on the full UUID, marked as ephemeral, recording at least the instance
  name, the instance path, and the session id, so the existing reclamation sweep can
  reclaim the instance.
- **R24.** The mapping SHALL carry a marker identifying the instance as dispatch-created,
  distinct from hook-created and developer-created instances, so operators and tooling can
  tell the provenance apart and so future changes to one path do not silently affect the
  other.
- **R25.** The command SHALL record the optional human-friendly label (R2) on the mapping
  when one is supplied.
- **R26.** If the mapping cannot be written after the session has launched, the command
  SHALL apply partial-failure handling (R32-R35); it SHALL NOT leave a launched session
  whose instance has no reclamation record.

### Teardown and reclamation

- **R27.** Instance teardown SHALL be performed by the existing reclamation sweep (`niwa
  reap` and its opportunistic invocation), keyed on the ephemeral mapping plus the
  session's job-state liveness. The command SHALL NOT depend on the workspace-root
  SessionEnd hook firing, because a session rooted in the instance resolves hooks from
  the instance, not the workspace root, and the root SessionEnd hook does not fire for it.
- **R28.** A dispatched instance SHALL be reclaimed after its session ends normally
  (terminal job state such as `done`).
- **R29.** A dispatched instance SHALL be reclaimed after its session is stopped from the
  terminal (`claude stop <id>`) or deleted from the Agent View UI, both of which drive the
  session's job state to a non-live condition.
- **R30.** A dispatched instance SHALL be reclaimed after its session is lost without a
  clean end -- process crash or machine reboot -- via the reclamation sweep's liveness
  backstop (a terminal or sufficiently-stale job state), with no manual step required.
  The mapping and job state both persist on disk across a reboot, so a later sweep
  reconciles them.
- **R31.** Reclamation SHALL NOT destroy an instance whose session is still live. The
  command SHALL NOT weaken the existing liveness rule that protects a running session's
  instance.

### Partial-failure atomicity

- **R32.** The dispatch sequence (resolve workspace -> create instance -> launch session
  -> capture id -> write mapping) SHALL be atomic in effect: every terminal state SHALL
  be either (a) fully successful with a durable mapping, or (b) fully cleaned up with no
  instance left behind, or (c) an instance that is guaranteed reclaimable by the existing
  sweep.
- **R33.** If the session fails to launch after the instance was created, the command
  SHALL reclaim (destroy) the just-created instance before returning an error, because an
  instance with no session and no mapping is not reclaimable by the sweep.
- **R34.** If the session launches but identity capture fails (R22), the command SHALL
  either stop the just-launched session and reclaim its instance, or otherwise guarantee
  the instance becomes reclaimable; it SHALL NOT return leaving a running session whose
  instance has no mapping.
- **R35.** If the session launches and is captured but the mapping write fails (R26), the
  command SHALL retry or roll back so the end state is either a durable mapping or a
  reclaimed instance; it SHALL surface the outcome to the developer.

### Concurrency

- **R36.** N simultaneous dispatches SHALL each produce a distinct instance and a distinct
  mapping, with no two dispatches sharing an instance directory, instance name, or mapping
  key (guaranteed by R11's unique-token naming and the UUID mapping key).
- **R37.** Concurrent dispatches SHALL NOT corrupt the instance state file or the mapping
  store: after N concurrent dispatches complete, the workspace state file SHALL parse
  cleanly and the mapping store SHALL contain exactly N distinct ephemeral mappings, one
  per dispatch. The command SHALL NOT introduce a new lost-update window on shared state.
- **R38.** The opportunistic reclamation sweep triggered by one dispatch SHALL NOT reclaim
  an instance another in-flight dispatch has created but not yet mapped; the design SHALL
  ensure an in-flight dispatch's instance is protected until its mapping is durable or it
  is rolled back.

### Coexistence with the hook path

- **R39.** The dispatched worker's own SessionStart hook (resolved from the instance, if
  the instance carries one) SHALL NOT double-provision: because the worker launches inside
  a valid instance, the existing hook re-entrancy guard (instance discovery succeeds) SHALL
  cause it to no-op, and this PRD SHALL NOT change that guard.
- **R40.** Dispatch-created instances SHALL be valid niwa instances indistinguishable to
  the hook's re-entrancy guard from any other instance, so the no-op in R39 holds.
- **R41.** The reclamation sweep SHALL reclaim dispatch-created and hook-created ephemeral
  instances by the same liveness rule; the provenance marker (R24) SHALL be informational
  and SHALL NOT change which instances are eligible for reclamation.
- **R46.** When the command is run by a dispatched worker (a session already inside an
  instance) -- i.e. a worker self-dispatching a sub-worker -- it SHALL behave exactly as
  R6: resolve the enclosing workspace root and create the new instance as a sibling under
  that root, never nested inside the calling worker's instance. Self-dispatch therefore
  produces a flat set of sibling instances under one workspace root and SHALL NOT deepen
  with each level; each sub-worker is itself an independently-mapped, independently-
  reclaimable dispatch.

### Non-functional

- **R42.** Every failure mode named in this PRD SHALL produce a clear, actionable error
  message naming what failed and the state left behind (instance reclaimed, mapping
  written, etc.).
- **R43.** The command SHALL handle the prompt being argv-only: it SHALL accept
  multi-line and special-character prompts safely (passed as a single argument vector
  element, not through a shell), SHALL reject an empty prompt with a clear error, and SHALL
  fail clearly when a prompt exceeds the operating system's argument-length limit rather
  than truncating it silently.
- **R44.** The command SHALL add no new system dependency beyond what `niwa create` and a
  local `claude` already require; it SHALL run on the same host as the workspace.
- **R45.** The command SHALL NOT add an unbounded wait: the identity-capture wait (R20)
  SHALL return or fail within a fixed, configurable bound, so a session whose job state
  never appears surfaces a capture failure within that bound rather than hanging. (That
  dispatch's wall-clock is otherwise dominated by instance-create cost is a property, not
  a tested threshold; see Known Limitations.)

## Acceptance Criteria

Each criterion is tagged **[offline]** (achievable in CI against niwa's `localGitServer`
harness, fabricating `~/.claude/jobs/<id>/state.json` files and stubbing the `claude`
launch) or **[live]** (requires a real `claude --bg` session). The DESIGN owns the test
seams that make the [offline] tags achievable (e.g. an injectable launcher and jobs-dir
root).

Happy path and configuration:

- [ ] **[live]** Running the command at a workspace root creates a new instance, launches
  a `claude --bg` worker rooted in it, and the worker is listed by `claude agents`. **(R5,
  R10, R14)**
- [ ] **[live]** The launched worker loads the instance's configuration: a skill or plugin
  declared only in the instance's materialized settings is invocable by the worker (or the
  worker's resolved settings path points into the instance directory). **(R10, R14)**
- [ ] **[live]** The launched session remains listable and attachable via `claude agents`
  / `claude attach` after launch completes. **(R17)**
- [ ] **[offline]** After a successful dispatch, a durable ephemeral mapping keyed on the
  full session UUID exists, recording instance name, instance path, the session id, and the
  dispatch-created provenance marker; an optional supplied label is recorded on it. **(R23,
  R24, R25)**

Launch-location resolution:

- [ ] **[offline]** Run from inside an existing instance, the command creates a sibling
  instance under the shared workspace root, not a nested instance. **(R6)**
- [ ] **[offline]** Run from inside a worktree and from inside a repo checkout, the command
  resolves the same enclosing workspace root and creates the instance there. **(R7, R9)**
- [ ] **[offline]** Run from a directory that resolves to no niwa workspace, the command
  exits non-zero with a clear error, and afterward no new instance directory, no session,
  and no mapping exist on disk. **(R8)**
- [ ] **[offline]** A dispatched worker that itself runs the command produces another
  sibling instance under the same workspace root (flat, not nested). **(R46)**

Session launch and identity capture:

- [ ] **[offline]** With `claude` not on PATH, the command fails before creating any
  instance: no new instance directory and no mapping exist on disk afterward. **(R16, R13)**
- [ ] **[offline]** An empty prompt is rejected with a clear error and no instance is
  created. **[live]** A multi-line prompt containing quotes and shell metacharacters is
  delivered to the worker intact (not shell-interpreted). **(R43)**
- [ ] **[offline]** When job state never appears for a launched short id, identity capture
  fails within the fixed configured bound rather than hanging. **(R20, R45)**
- [ ] **[offline]** When two job entries could match a scraped short id, the command
  resolves the one whose recorded working directory is the instance it launched in, or
  treats it as a capture failure -- it never writes a mapping for the wrong session. **(R21)**

Partial-failure atomicity (each asserts which R32 terminal bucket was reached):

- [ ] **[offline]** Inducing a launch failure after instance creation: the command rolls
  the instance back (no instance directory remains), writes no mapping, and returns a clear
  error -- R32 bucket (b). **(R33, R32, R42)**
- [ ] **[offline]** Inducing an identity-capture failure after a successful launch: the
  command leaves no running session whose instance lacks a mapping, and no unreclaimable
  instance -- R32 bucket (b) or (c). **(R34, R32)**
- [ ] **[offline]** Inducing a mapping-write failure after successful capture (e.g. the
  sessions store is unwritable): the end state is either a durable mapping or a reclaimed
  instance, and the outcome is reported to the developer. **(R35, R42)**

Concurrency:

- [ ] **[offline]** Two dispatches started simultaneously produce two distinct instances
  and two distinct mappings; neither clobbers the other's directory, name, or mapping key.
  **(R11, R36)**
- [ ] **[offline]** After N concurrent dispatches, the workspace state file parses cleanly
  and the mapping store contains exactly N distinct ephemeral mappings. **(R37)**
- [ ] **[offline]** With a freshly-created, not-yet-mapped in-flight instance present, a
  concurrent dispatch's opportunistic reclamation sweep does not reclaim that in-flight
  instance. **(R38, R12)**

Teardown and reclamation (each fabricates the relevant job-state condition):

- [ ] **[offline]** With a mapping marked ephemeral and a job state at terminal `done`, a
  reclamation sweep destroys the instance and deletes the mapping. **(R27, R28)**
- [ ] **[offline]** With a job state driven non-live by a stop/delete (terminal/removed), a
  reclamation sweep reclaims the instance. **(R29)**
- [ ] **[offline]** With a job state whose `updatedAt` is past the liveness TTL and a
  non-terminal `state` (crash/reboot simulation), a reclamation sweep reclaims the instance
  with no manual step. **(R30)**
- [ ] **[offline]** With a job state having a fresh `updatedAt` and a non-terminal `state`
  (a live session), a reclamation sweep does NOT destroy the instance. **(R31)**
- [ ] **[offline]** A dispatch-created and a hook-created ephemeral instance, both under the
  same terminal/stale job-state condition, are both reclaimed by the same sweep -- the
  provenance marker does not change eligibility. **(R41)**

Coexistence with the hook path:

- [ ] **[offline]** The existing hook auto-provisioning unit and functional tests still
  pass unchanged. **(R1, R39)**
- [ ] **[offline]** A dispatched worker's SessionStart hook, evaluated against a
  dispatch-created instance, hits the existing re-entrancy guard and no-ops (provisions no
  second instance). **(R39, R40)**

### Requirement-to-criterion traceability

Every requirement maps to at least one acceptance criterion above. Requirements verified
indirectly: R2/R3 (surface) via the happy-path and mapping/label criteria; R4
(ephemeral-mode independence) is exercised by every [offline] criterion running with the
master switch off; R15 (prompt passed as initial prompt) via the multi-line-prompt and
config criteria; R18/R19 (short-id scrape + UUID resolve) via the mapping and
ambiguity/capture-bound criteria; R26 via the mapping-write-failure criterion; R44
(no new system dependency) by the suite running in the existing CI environment. No
requirement is left without a path to a pass/fail check.

## Decisions and Trade-offs

This section also closes the upstream BRIEF's Open Questions.

- **Instance named from a fresh unique token, not the numbered scan (closes part of the
  BRIEF's command-surface question).** The numbered-instance naming path is not
  concurrency-safe (no lock; TOCTOU scan). The hook avoids this by naming from the session
  id, but dispatch has no session id before launch. Decision: generate a unique token at
  dispatch time and name the instance from it. Alternative (add locking to the numbered
  scan) is broader surgery on shared create code and is out of scope for an additive
  command.
- **Mapping written after launch, keyed on the real UUID; rollback covers the gap.** The
  mapping store requires a canonical UUID, which only exists after launch, so the mapping
  cannot be pre-written. That creates a window where an instance exists with no mapping,
  and the reclamation sweep cannot reclaim a mapping-less instance. Decision: make the
  command responsible for rolling back (reclaiming) its own instance on any pre-mapping
  failure (R32-R35). Alternative (teach the sweep to reclaim mapping-less ephemeral
  instances by directory marker) is a larger change to reclamation semantics and is left
  to the DESIGN to consider but not required.
- **Reaper-primary teardown (closes the BRIEF's reclamation question).** The root
  SessionEnd hook does not fire for an instance-rooted session, so teardown relies on the
  existing sweep keyed on ephemeral mapping + job-state liveness, which already treats a
  terminal (`done`) or sufficiently-stale session as dead. No new teardown mechanism is
  built.
- **Prompt delivery is argv-only (closes the BRIEF's long-prompt question).** Because
  `claude --bg` accepts the prompt only as an argument, the command passes it as a single
  argv element (safe for multi-line/special characters via the process argument vector,
  not a shell) and fails clearly at the OS argument-length limit. A file/stdin prompt
  channel is not available from the background-launch surface and is out of scope.
- **Attach-after-launch is left to the DESIGN.** Whether the command returns immediately
  with management hints (R3) or attaches the developer to the session is a UX decision the
  DESIGN settles; the requirements only fix that the session is reachable.

## Known Limitations

- **Inherited reclamation-liveness window.** The command reuses the existing liveness
  rule, which can reclaim an ephemeral instance whose session has gone stale beyond the
  liveness TTL even if the process is technically alive but idle. This is pre-existing
  behavior shared with the hook path; the command does not worsen it, but a very long
  idle dispatched session is subject to the same TTL backstop. Tuning the TTL is out of
  scope.
- **Short-id scrape fragility.** Capturing the short identifier depends on the
  background-launch output format, which is human-oriented and undocumented as a stable
  contract. The full-UUID resolution via job state (R19) reduces but does not eliminate
  this dependency. If Claude Code adds a machine-readable launch output, the command
  should prefer it.
- **No Agent-View-side dispatch.** This command is a terminal-side launcher. Dispatching
  into a chosen instance from within the Agent View UI depends on an unshipped Claude Code
  per-worker working-directory capability and is out of scope.

## Out of Scope

- Modifying or retiring the existing SessionStart/SessionEnd hook auto-provisioning
  (tracked as tsukumogami/niwa#171 and tsukumogami/niwa#172). Both paths coexist; the
  team will revisit retirement after observing Agent View's evolution.
- Per-worker working-directory dispatch from inside the Agent View UI (depends on
  unshipped Claude Code capability; tracked upstream as anthropics/claude-code#60975 and
  #31940).
- Changing the instance model -- how instances clone, materialize configuration, or are
  numbered -- beyond passing a unique name at create time.
- Building a new reclamation mechanism. Teardown reuses the existing sweep.
- Cross-machine or remote dispatch.
- Adding concurrency locking to the shared numbered-instance naming scan (the command
  sidesteps it with unique-token naming rather than fixing the underlying scan).

## References

- docs/briefs/BRIEF-instance-dispatch.md -- the upstream framing.
- docs/designs/current/DESIGN-ephemeral-session-instances.md -- the existing hook-based
  feature this command coexists with and does not modify.
- docs/guides/ephemeral-session-instances.md -- the mapping store, `niwa reap`, and the
  reclamation sweep this command reuses.
