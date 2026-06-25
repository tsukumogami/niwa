---
status: Current
upstream: docs/prds/PRD-instance-dispatch.md
problem: |
  niwa has no command that launches a Claude Code background worker rooted inside a
  fresh ephemeral instance. The hook path cannot deliver instance configuration; the
  manual path is error-prone and records nothing for reclamation. A command must create
  an instance, launch a worker in it, capture the worker's session UUID, record a
  mapping, and leave no unreclaimable instance under any failure or teardown path.
decision: |
  Add a top-level `niwa dispatch` command that creates an instance with a unique random
  name, launches `claude --bg` rooted in it, recovers the session UUID by correlating
  the jobs-dir state.json whose cwd equals the unique instance directory, and writes an
  ephemeral mapping marked with a dispatch origin. Atomicity is a command self-rollback
  (destroy the instance on any pre-mapping failure) plus a name-and-TTL reaper backstop
  that closes the SIGKILL gap a process-local rollback cannot. The command reuses the
  existing create, destroy, mapping, and reclamation machinery; it does not touch the
  hook path.
rationale: |
  Correlating on the unique instance directory avoids depending on the undocumented
  background-launch stdout format and gives an exact disambiguation key. Self-rollback
  handles every failure the command returns from; the name+TTL reaper branch is the
  only way to reclaim an instance orphaned by a SIGKILL, and gating it on a TTL longer
  than a dispatch keeps it from reaping a healthy in-flight instance. Reusing the
  existing primitives keeps the command additive and small.
---

# DESIGN: niwa instance-dispatch command

## Status

Current

This design implements the Accepted PRD docs/prds/PRD-instance-dispatch.md (R1-R46). It
is scoped for a single-PR plan. It is purely additive: the existing
SessionStart/SessionEnd hook auto-provisioning (tsukumogami/niwa#171, #172) is not
touched.

## Context and Problem Statement

A prior exploration proved a Claude Code SessionStart hook cannot re-root a session's
settings resolution, so the existing hook-based instance isolation is capped at
file-tree separation and cannot deliver an instance's plugins, hooks, or environment to
a dispatched worker. Launching `claude --bg` from inside an instance directory was
verified to deliver full per-instance configuration at launch and to register the
session in Agent View. The PRD specifies a command around that mechanism.

The implementation must resolve several concrete technical problems established by
grounding research against the current niwa code:

- **No session id exists before launch.** The durable mapping store
  (`internal/workspace/session_map.go`) keys on a canonical UUID and rejects anything
  else (`ValidSessionID`). The UUID only exists after the worker launches, so the
  mapping cannot be pre-written.
- **The instance-naming scan is racy.** `computeInstanceName`'s numbered fallback
  (`internal/cli/create.go`) is a TOCTOU `os.Stat` loop with no lock, and `os.MkdirAll`
  is non-exclusive, so two concurrent dispatches could collide. The hook avoids this by
  naming from the session-id prefix, which dispatch cannot do pre-launch.
- **The reaper only reclaims mapped instances.** `selectReapTargets`
  (`internal/cli/reap.go`) joins on-disk instance records against the mapping store; an
  instance with no mapping is skipped. So an instance created but not yet mapped is
  invisible to reclamation -- an unreclaimable orphan if the command dies in that window.
- **`claude --bg` has no machine-readable identity output.** It detaches, prints a
  human line `backgrounded · <short-id>`, and writes `~/.claude/jobs/<short-id>/state.json`
  carrying the full `sessionId`, `cwd`, `state`, and `updatedAt`.

## Decision Drivers

- **No unreclaimable orphan, ever (PRD R32).** The create-before-map window and the
  possibility of a SIGKILL make atomicity the hardest constraint.
- **Concurrency safety without a new lock.** N simultaneous dispatches must not collide
  on names, directories, or mappings (R36-R38).
- **Don't depend on undocumented output formats.** The `backgrounded · <id>` text is not
  a stable contract; the `state.json` file already is, since niwa's reaper reads it.
- **Additive and small.** Reuse `niwa create`, `niwa destroy`, the mapping store, and the
  reaper; do not modify the hook path (R1).
- **Offline-testable.** The PRD's acceptance criteria are largely tagged [offline]; the
  design must expose injection seams so tests need no live `claude` and no live daemon.

## Considered Options

### D1 -- Command surface

Options: a top-level `niwa dispatch <prompt>`; `niwa run`; `niwa agent`; a nested
`niwa instance dispatch`. **Chosen: `niwa dispatch <prompt>`**, a top-level verb beside
`niwa create`/`niwa reap`, with `--label` for the optional freeform mapping alias,
`--name`/`-n` for an optional session name (see D2 -- sanitized into a slug that names
both the instance and the Claude session), and pass-through
`--model`/`--permission-mode`/`--agent` forwarded to `claude --bg`.

**Attach by default; `--detach`/`-d` to skip (R47).** The common interactive case is
"give me a fresh isolated agent and let me work in it," so by default the command runs
`claude attach <session>` as its final step, landing the developer inside the new
session. `--detach`/`-d` skips the attach and returns after printing the
`claude attach`/`logs`/`stop` hints -- the mode for fan-out and scripting. The naming
follows the Docker convention (`docker run` attaches; `-d` detaches the terminal), which
is more precise here than `--headless` (which connotes "no UI"). This is a deliberate
inversion of an earlier "always return" stance: attaching is the better default for a
single dispatch, and `--detach` preserves the non-blocking behavior for the many-agent
case. Rejected names: `run`/`agent` are vaguer and collide with common mental models; a
nested subcommand buries a primary action; `--headless`/`--no-attach` are viable flag
spellings but `--detach`/`-d` is the most recognizable.

The attach is structurally safe because it is strictly the **last** step, after the
mapping is durable and the rollback window has closed: `claude --bg` is a daemon-backed
session that outlives the attach, so detaching or closing the terminal neither ends the
session nor reclaims the instance (reclamation stays governed by the session's own
lifecycle). An attach failure -- e.g. a fast session that already exited -- is therefore
not a dispatch failure: the command degrades to printing the hints and never rolls back.
The session id needed for attach is already in hand from capture (D3): the correlated
`state.json` gives the full `sessionId`, and its containing directory name gives the
short id, so either form `claude attach` accepts is available.

### D2 -- Concurrency-safe instance naming

Options: the existing numbered scan; a timestamp suffix; a random token. **Chosen: name
the instance `<config>-disp-<8 random hex>` and pass it through the existing
`--name`/`customName` create branch**, which bypasses the racy numbered scan entirely.
A random token is collision-safe under concurrency without any lock and reads clearly in
`niwa list` as a dispatch-created instance. Rejected: the numbered scan (TOCTOU, the
exact race R36/R37 forbid); a timestamp (collisions when two dispatches start in the
same instant).

**Optional `--name`/`-n` slug (additive, signature-preserving).** When the developer
passes `--name <raw>`, it is sanitized into a slug (lowercase, `[a-z0-9]` runs collapsed
to single hyphens, trimmed, length-capped; empty result falls back to no slug) and used
two ways: it is inserted into the instance name BEFORE the signature suffix --
`<config>-<slug>-disp-<8 random hex>` -- and it is forwarded to the session as
`claude --bg --name <slug>` so the Claude session carries a human display name in Agent
View. The random 8-hex is always kept, so the `-disp-<8hex>` end-anchored signature the
reaper backstop matches (`isDispatchInstanceName`, regex `-disp-[0-9a-f]{8}$`) is
preserved and concurrency stays collision-safe even when two dispatches share a `--name`.
The slug is additive: it never replaces the random token. With no `--name` (or an
empty-after-sanitize one), behavior is exactly the random-token default. `--name` (the
slug, which names the instance and the session) is distinct from `--label` (a freeform
alias recorded only on the mapping).

### D3 -- Session-identity capture

Options: (A) scrape `backgrounded · <short-id>` from stdout, then read
`jobs/<short-id>/state.json` for the UUID; (B) ignore stdout, poll the jobs directory
for the `state.json` whose `cwd` equals the instance directory the command launched in,
and read its `sessionId`; (C) a hybrid that scrapes but verifies by `cwd`. **Chosen:
Option B.** The instance directory is a freshly-created unique path, so `cwd == instanceDir`
is an exact correlation key -- stronger disambiguation (R21) than the probabilistic
8-char short id -- and it depends only on the `state.json` contract niwa already relies
on, not the undocumented human output. It also needs no stdout-capture mode in the
launcher. The poll is bounded with a timeout (R20); exhaustion is a capture failure
(R22) that triggers rollback. Path comparison normalizes both sides with
`filepath.EvalSymlinks` + `filepath.Clean` to avoid a symlink mismatch. This subsumes
the PRD's R18 (which named scraping) under the stronger R19/R21 mechanism. Rejected: A
and C add a dependency on the fragile output format and a second test seam for fake
stdout, buying nothing over the exact `cwd` match.

### D4 -- Partial-failure atomicity

Options: (A) command self-rollback only; (B) self-rollback plus a reaper backstop for
the crash case; (C) a provisional mapping finalized after capture. **Chosen: B.** The
command wraps the create-to-map window in a deferred cleanup guarded by a success flag:
on any failure between instance creation and a durable mapping write -- launch failure
(R33), capture failure/timeout (R34), or mapping-write failure (R35) -- it destroys the
just-created instance via the existing destroy path before returning an error. But a Go
`defer` does not run on `SIGKILL`, OOM, or power loss, so self-rollback alone leaves an
unmapped orphan in those cases and cannot strictly satisfy R32. The backstop closes that
gap: the reaper gains a **separate scan** (not a branch inside `selectReapTargets`):
because `EnumerateInstanceRecords` derives an instance's `Ephemeral` flag solely from the
mapping store, an unmapped orphan is already `Ephemeral:false` and is dropped before any
per-record branch runs, so the backstop cannot live inside that loop. The scan reclaims an
instance when, and only when: its mapping is absent, **its directory name carries the
dispatch `-disp-<hex>` signature**, and its age exceeds a TTL strictly longer than the
worst-case dispatch wall-clock. **The eligibility signal is the instance NAME, not a
marker file** -- the name is created atomically with the directory by the provision step,
so there is no window in which a committed-on-disk dispatch instance lacks its signal
(an earlier marker-file-only design left a create-then-write-marker gap in which a SIGKILL
produced an instance that was both unmapped and unmarked, hence invisible to both sweeps
-- an unreclaimable orphan; keying on the name closes it). For age, the command still
writes a small `.niwa/dispatch-pending` file embedding an RFC3339 creation timestamp as
the *precise* age source; when that file is absent or unparseable (the SIGKILL-before-marker
case) the scan falls back to the directory mtime. The no-mapping gate excludes every
successful dispatch (it is mapped); the name signature excludes developer instances
(`<config>`, `<config>-2`) and hook-created instances (`<config>-<sessionhex>`, no
`-disp-` segment); and the TTL gate preserves R38 -- a healthy in-flight instance is
younger than the TTL and is never reaped. Rejected: A leaves the SIGKILL
orphan; C is impossible because the store rejects a non-UUID provisional key
(`session_map.go`) and would require weakening that validation.

### D5 -- In-flight instance protection under concurrent reclamation

Options: a new lock or a reservation table; rely on the existing reaper's structure.
**Chosen: rely on the existing structure -- no new lock.** The reaper's `Ephemeral`
verdict derives entirely from the mapping store, and `selectReapTargets` skips any
instance without a mapping. An in-flight dispatch's instance has no mapping until the
command finishes, so a concurrent dispatch's opportunistic sweep cannot see it (R38).
The only path that could see an unmapped instance is the D4 backstop, and its TTL gate
(longer than a dispatch) excludes a young in-flight instance. Rejected: a lock adds a new
failure mode and contention for a window the data model already protects.

### D6 -- Mapping provenance marker

Options: a new `Origin` field; reuse the existing free-form `Label`; no marker.
**Chosen: add an additive `Origin` field** to `SessionMapping` (JSON-omitempty; an absent
value decodes to the zero string, so existing hook-written and developer-written mappings
remain valid). The dispatch command sets `origin: "dispatch"`. The field is informational
-- it surfaces provenance in tooling and keeps the two write paths legible -- and the
reaper ignores it, so reclamation eligibility is unchanged (R41). Rejected: overloading
`Label` (which is a user-facing friendly name) conflates two concerns; no marker loses
provenance the PRD asks for (R24).

### D7 -- Reuse and code structure

Options: a self-contained command; reuse the existing primitives. **Chosen: reuse.** A
new `internal/cli/dispatch.go` hosts the command. Instance creation calls the existing
`realProvisionInstance`/`applier.Create` path (which already materializes `claude.env`
into the instance tree, so the worker inherits the environment for free). Rollback and
teardown reuse `destroyInstanceFunc`/`workspace.DestroyInstance`. The mapping reuses
`WriteSessionMapping`. The background launch generalizes the exec pattern in
`internal/cli/sessionattach/supervise.go` (which already sets `cmd.Dir`) into a small
launcher abstraction that runs `claude --bg <prompt>` with `cmd.Dir = instanceDir`;
because capture is by jobs-dir correlation (D3), the launcher does not need to capture
stdout. Rejected: a self-contained command would duplicate create/destroy/mapping logic
and drift from the hook path's behavior.

### D8 -- Prompt handling

Options: shell-interpolate; pass as a single argument. **Chosen: pass the prompt as a
single `exec` argument vector element** (Go `exec` does not invoke a shell, so quotes,
newlines, and metacharacters are safe). An empty prompt is rejected with a clear error
before any instance is created; a prompt that would exceed the operating system's
argument-length limit fails with a clear error rather than being truncated (R43).
Rejected: any shell path would reintroduce injection and quoting hazards.

### D9 -- Test seams

Options: integration-only testing against a real `claude` and a live daemon; injectable
seams for offline tests. **Chosen: injectable seams.** The launcher is a package-level
function variable; the jobs-dir root and a clock are injected into capture and into the
reaper backstop; `destroyInstanceFunc` is already a package variable; and instance
creation runs against the existing offline `localGitServer` harness. Together these let
every PRD [offline] acceptance criterion run in CI with a stubbed launcher and fabricated
`state.json` files -- no live `claude`, no daemon. Rejected: integration-only testing is
slow, flaky, cannot run in CI, and would leave the failure and reclamation paths -- the
riskiest behavior -- effectively untested.

### Hook-path coexistence (R39, R40)

The command does not touch the existing SessionStart/SessionEnd hook code. The one
interaction is benign and is relied upon, not modified: a dispatched worker boots inside
a dispatch-created instance, which is an ordinary, valid niwa instance (it carries
`.niwa/instance.json`). So if that instance carries the workspace's SessionStart hook,
the hook's existing re-entrancy guard -- which no-ops when the launch cwd already
resolves inside a valid instance -- fires and the hook provisions nothing (R39). The
design's only obligation is that dispatch-created instances are indistinguishable to that
guard from any other instance (R40), which holds because they are created through the same
`realProvisionInstance` path. This interaction is covered by an acceptance criterion in
the plan; no guard code changes.

## Decision Outcome

`niwa dispatch <prompt>` resolves the enclosing workspace root from the current directory
using the existing `ClassifyCwd` (workspace root, inside-instance, inside-worktree all
resolve to their workspace root; an unresolved directory is a clean error), verifies
`claude` is on `PATH`, then: creates an instance named `<config>-disp-<random>` through
the existing create path, drops a pending-marker in it, launches `claude --bg <prompt>`
rooted in the instance, polls the jobs directory for the `state.json` whose `cwd` is the
instance directory to recover the full session UUID, writes an ephemeral mapping
(`origin: dispatch`) keyed on that UUID, removes the marker, prints the session id with
management hints, and -- unless `--detach` was given -- attaches the terminal to the new
session as the final step (D1). Any failure before the mapping is durable triggers a
self-rollback that destroys the instance; the attach step runs only after that window has
closed and its failure is non-fatal. A SIGKILL in that window leaves a marked, unmapped instance
that the reaper's new name+TTL branch reclaims later. Teardown of a normally-running
dispatch is the existing reaper keyed on the ephemeral mapping plus job-state liveness,
which already treats a terminal (`done`) or past-TTL session as dead.

## Solution Architecture

Components (new unless noted):

- **`niwa dispatch` command (`internal/cli/dispatch.go`).** Cobra command; positional
  prompt; `--label`, `--model`, `--permission-mode`, `--agent`, and `--detach`/`-d`
  flags. Orchestrates the sequence below, owns the rollback, and runs the final attach
  step unless `--detach` is set.
- **Attach step (injectable).** A package-level function variable (test seam, like the
  launcher) that runs `claude attach <session>`; production wires it to the real exec,
  tests substitute a fake to assert it is/ isn't called and that its failure is non-fatal.
- **Workspace resolution.** Reuses `workspace.ClassifyCwd`; maps each class to the
  enclosing `WorkspaceRoot`; `CwdOutside` -> clean error (R5-R9).
- **Instance creation.** Reuses `realProvisionInstance` with a generated
  `disp-<random>` name (D2). Returns the instance path. Triggers the existing
  opportunistic reap (R12) the way `runCreate` does.
- **Pending-marker (precise-age aid).** A small `.niwa/dispatch-pending` file written
  immediately after create, embedding an RFC3339 creation timestamp. It is NOT the
  backstop eligibility signal (the instance name is); it only supplies a precise age, with
  directory mtime as the fallback when it is absent (D4).
- **Background launcher (generalized from `sessionattach`).** A package-level function
  variable (test seam) that runs `claude --bg <prompt>` with `cmd.Dir = instanceDir`,
  forwarding the pass-through flags. Each of the prompt and the pass-through values is a
  discrete argv element -- never assembled by string concatenation -- so a crafted prompt
  or `--label` cannot inject an additional `claude` flag. Does not capture stdout.
- **Identity capture.** Polls an injectable jobs-dir root for a `state.json` whose
  normalized `cwd` equals the normalized instance path; reads `sessionId` and validates it
  with `ValidSessionID` before use. Bounded by an injectable clock + timeout. If more than
  one `state.json` claims the same instance `cwd`, capture is treated as a failure (which
  rolls back), never an arbitrary pick (R21/R22). Requires adding a `Cwd` field to the
  job-state struct in `internal/cli/job_state.go`.
- **Mapping write.** Reuses `WriteSessionMapping` with the new `Origin` field set to
  `dispatch` (D6).
- **Rollback.** A deferred cleanup guarded by a success flag; on the failure path calls
  `destroyInstanceFunc` (D4, D7).
- **Reaper backstop (additive separate scan in `internal/cli/reap.go`).** A scan distinct
  from `selectReapTargets` -- because that function's `Ephemeral` verdict comes from the
  mapping store and drops unmapped instances before any per-record check. The backstop
  enumerates on-disk instances and reclaims one only when its directory name carries the
  dispatch `-disp-<hex>` signature, it has no mapping, and its age (marker timestamp, else
  directory mtime) exceeds the backstop TTL. A shared name predicate is used by both the
  command's naming and this scan so they cannot drift. It never touches mapped instances
  (the existing sweep owns those), non-dispatch-named instances, or young in-flight ones
  (D4, D5).
- **`SessionMapping.Origin` (additive field, `internal/workspace/session_map.go`).**

Data flow (happy path):

1. `niwa dispatch "<task>"` -> resolve workspace root; preflight `claude` on PATH.
2. Create instance `<config>-disp-<rand>`; write pending-marker; arm deferred rollback.
3. Launch `claude --bg "<task>"` with cwd = instance dir.
4. Poll jobs dir until a `state.json` has `cwd == instanceDir`; read full `sessionId`
   (or time out -> rollback).
5. `WriteSessionMapping{session_id: UUID, instance_*: ..., ephemeral: true, origin:
   dispatch, label?}`.
6. Remove pending-marker; disarm rollback; print id + hints.
7. Unless `--detach`: `claude attach <session>` (final step; failure is non-fatal, no
   rollback).

Teardown: the existing reaper reclaims the instance when the session reaches a terminal
or past-TTL job state (R27-R31). The backstop branch reclaims a SIGKILL-orphaned
marked-and-unmapped instance after the backstop TTL.

## Implementation Approach

A single PR, built in this order so each step is independently testable:

1. Add the `Origin` field to `SessionMapping` (additive, omitempty) and the `Cwd` field
   to the job-state struct, with unit tests for backward-compatible decoding.
2. Add the background launcher abstraction (function variable) generalized from the
   `sessionattach` exec pattern; unit-test the constructed argv and `cmd.Dir`.
3. Add identity capture: jobs-dir cwd-correlation poll with injectable jobs-dir root and
   clock; table tests over fixture `state.json` trees (found, not-yet-written-then-found,
   timeout, ambiguous-but-cwd-disambiguated).
4. Add `niwa dispatch` (`internal/cli/dispatch.go`): workspace resolution, PATH
   preflight, create + marker, launch, capture, mapping, marker removal, and the
   deferred self-rollback. Unit-test the guard/rollback matrix with a fake launcher and
   fake capture.
5. Add the reaper name+TTL backstop as a separate scan (not a `selectReapTargets`
   branch, since an unmapped instance is `Ephemeral:false` and dropped there); gate on the
   `-disp-<hex>` name signature + no-mapping + age (marker timestamp, else dir mtime) via
   the injectable clock. Unit-test that it reclaims a disp-named-unmapped-old instance
   (with marker, and without marker via mtime — the SIGKILL-before-marker case), spares a
   disp-named-unmapped-young one (R38), spares a mapped instance, and never touches a
   non-dispatch-named (developer/hook) instance.
6. Add a `@critical` functional Gherkin scenario using the offline `localGitServer`
   harness and a stubbed launcher + fabricated jobs-dir: dispatch provisions and maps;
   an induced launch failure rolls back; a fabricated terminal/stale job state lets the
   reaper reclaim; a fabricated live job state is spared.
7. Add the live end-to-end test (`test/live/`, a `make test-live` target). It runs the
   real `claude` lifecycle: init a workspace, `niwa dispatch --detach`, assert the
   well-constructed dedicated instance (`.niwa/instance.json`, materialized config/env)
   and the registered session, then `claude stop` + `niwa reap`, and confirm the instance
   is destroyed and the mapping deleted; a still-live second session is spared. The test
   is gated on a live-availability check (a build tag plus a `claude`-usable probe) so it
   runs whenever a usable `claude` is present and is skipped only when none is -- it is not
   silently skipped on a developer machine. This is the offline scenario's live counterpart
   and the feature's definition-of-done gate (R48).

**Reclamation is reaper-primary, so `stop` does not destroy instantly.** Because no hook
fires when an instance-rooted session ends, `claude stop` only drives the session's job
state terminal; the instance is reclaimed by the next `niwa reap` (on demand, or
opportunistically at the next dispatch/create). The live test therefore stops, then runs
`reap`, then asserts destruction -- matching the real behavior rather than assuming an
instant teardown.

## Security Considerations

- **Prompt is never shell-interpreted.** The prompt and the pass-through flags are passed
  as `exec` argument vector elements, not through a shell, so prompt content cannot inject
  a command (D8). Each value MUST remain a single discrete argv element -- the launcher
  SHALL NOT assemble the command line by string concatenation -- so a crafted prompt or
  `--label` cannot smuggle in an extra `claude` flag (flag-injection). The empty-prompt
  and argument-length checks bound the input.
- **Untrusted job-state input.** The `state.json` files are read, not executed; the
  command trusts only the `cwd` correlation and the `sessionId` field, and validates the
  recovered id against `ValidSessionID` before it becomes a path component or mapping key
  -- the same validation the hook path already applies.
- **Destroy blast radius.** Rollback and the reaper backstop destroy only the
  command's own freshly-created instance (rollback) or a `-disp-<hex>`-named instance with
  no mapping past the TTL (backstop). A developer's normal instance is not dispatch-named
  and is never a target; a mapped instance is reclaimed only by the existing liveness rule,
  unchanged.
- **Name-signature forgery.** The dispatch name signature and the pending-marker both live
  inside the workspace; an attacker who can create a `-disp-<hex>`-named directory or write
  a marker there can already manipulate instances, so neither widens the trust boundary. The
  backstop's TTL and mapping-absent gates keep a stray dispatch-named directory from causing
  a live (mapped) instance to be reaped.
- **No new credentials or network surface.** The command adds no secret handling beyond
  what `niwa create` already does (it materializes the instance's declared env), and runs
  entirely on the local host.

## Consequences

Positive:

- A single command delivers a fully-configured, Agent-View-managed, isolated background
  worker, with reclamation guaranteed under every failure and teardown path the PRD names.
- Capture by `cwd` correlation removes any dependency on the undocumented background-launch
  output format.
- The feature is additive: the hook path, `niwa create`, and the worktree commands are
  untouched; the reaper and mapping changes are backward-compatible additions.

Negative / mitigations:

- The reaper gains a second reclamation scan (name+TTL), a small increase in its
  surface. Mitigated by gating it strictly (mapping-absent AND `-disp-<hex>`-named AND
  age>TTL) and unit-testing the spare/reap matrix, including the in-flight (young) case
  and the no-marker (mtime-fallback) case.
- Capture depends on Claude Code writing `state.json` with a `cwd` field; if that format
  changes, capture breaks. Mitigated because niwa's reaper already depends on the same
  file, so the dependency is shared, not new, and a capture failure rolls back cleanly
  rather than orphaning.
- A SIGKILL between create and mapping leaves an instance reclaimed only after the
  backstop TTL, not immediately. Accepted: the alternative (immediate) is impossible
  without a process-external agent, and the orphan is bounded and reclaimed.
- The backstop TTL must be chosen longer than the worst-case dispatch wall-clock; a
  misconfigured (too-short) TTL could reap a slow in-flight instance. Mitigated by a
  conservative default -- 30 minutes, far above a dispatch (a clone is seconds to low
  minutes), and aligned in magnitude with the existing `jobLivenessTTL` though
  conceptually separate -- making it the single tuning knob.

## References

- docs/prds/PRD-instance-dispatch.md -- the requirements this design implements.
- docs/briefs/BRIEF-instance-dispatch.md -- the framing.
- docs/designs/current/DESIGN-ephemeral-session-instances.md -- the hook-based feature
  this command coexists with and does not modify.
- docs/guides/ephemeral-session-instances.md -- the mapping store and `niwa reap`
  reclamation sweep reused here.
