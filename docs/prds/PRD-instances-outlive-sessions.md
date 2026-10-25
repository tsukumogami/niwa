---
schema: prd/v1
status: Accepted
problem: |
  Coordinators and developers who hold a dispatched worker at a pause, or
  leave a workflow run for a successor, lose the niwa instance and hook-made
  worktree that work still needs. niwa decides from the Claude Code job
  entry, a day of transcript idleness, or a plain destroy, and reaps run at
  the top of every dispatch, create and watch. Coming back means a hand-made
  directory and a resume that drops niwa's flags and trust.
goals: |
  An instance or worktree stays on disk exactly as long as some session
  still needs it and goes on the next reap once nothing does. niwa learns
  "still needed" from a command the workspace declares, never from an
  engine's internals; koto answers that command for this organization's
  workspace; a worker whose directory went anyway comes back through niwa,
  trusted and in its dispatched posture; and the workspace configuration
  declares the two koto seams niwa already ships.
upstream: docs/briefs/BRIEF-instances-outlive-sessions.md
source_issue: 369
---

# PRD: Instances and worktrees outlive the sessions that need them

## Status

Accepted

## Problem Statement

People who run agent work through niwa hold sessions open on purpose. A
coordinator dispatches a worker, the worker reaches a checkpoint, and the
coordinator holds it there for hours or days while something upstream lands.
A workflow run that koto drives can outlive the Claude Code session that
started it: that session ends, and a successor picks the run up later,
sometimes from another instance. In both cases the directory the work lives
in has to stay put until the work is done or has moved somewhere else.

niwa can't see either case. Its reap decides whether an ephemeral instance
lives from the Claude Code job entry alone, and a reap runs at the top of
every `niwa dispatch`, `niwa create` and `niwa watch`, not only when someone
asks. `niwa destroy` removes the directory of a session that's still running
and only keeps the session's row. The sweep of worktrees niwa's
WorktreeCreate hook made removes a worktree once the requesting session's
transcript has gone a day without a write, which is exactly what a paused
worker looks like. Nothing in those rules can say "a session still needs this
path", and niwa has no engine knowledge to ask with: the engine that knows
which paths a run is bound to is koto, and niwa is deliberately free of it.

When a directory goes early, the person pays. The session can't be resumed in
place, so people make a placeholder directory by hand and run `claude
--resume` from it. That brings the worker back without the permission mode,
environment and session name `niwa dispatch` launched it with, and without
the trust grant niwa withdrew when it removed the instance
(tsukumogami/niwa#369). Coordinator messages then wait for approval and the
worker stops on tool calls, with nothing saying why.

There's a quieter half. In this organization's workspace, koto can't tell
when niwa has removed a session, so koto's record of that agent session stays
open and keeps holding runs nobody will return to. And a worker a
coordinator dispatches starts with no link to the coordinator, so koto can't
say which runs a coordinator's workers hold.

Why now: koto has started recording that one session continues another
(koto#481 landed the relation on one host), which is what makes "this run has
moved elsewhere" answerable, and the held-worker pattern is now routine for
the coordinators that drive this organization's work.

## Goals

- A coordinator holding a dispatched worker, or a developer who left a
  workflow run for a successor, finds the instance and any hook-made worktree
  as they left them, however many reaps ran and however long the transcript
  sat idle.
- The directory goes on the next reap once nothing needs it: the job was
  removed, the run finished, or a successor took the run over from another
  instance. Nobody has to remember which instances to protect.
- An operator who tries to destroy an instance a session still needs is
  stopped and told why, and can still go ahead deliberately.
- A worker whose directory went anyway comes back through niwa in a restored,
  trusted directory and its dispatched posture, with no directory made by
  hand.
- niwa stays free of any workflow engine: it reports the reason an outside
  command gave, and a workspace that declares nothing sees today's behaviour.
- In this organization's workspace, koto's agent-session records close when
  niwa removes a session, and a dispatched worker is linked to its
  coordinator.

## User Stories

1. As a coordinator holding a dispatched worker at a checkpoint for two days
   while dispatching others into the same workspace, I want the held worker's
   instance and hook-made worktree to survive every reap those dispatches
   start, so that the worker resumes where it stopped when I send the go, and
   I want both gone on the next reap once I remove its job.
2. As a developer whose workflow run is paused after its Claude Code session
   was deleted, with no successor yet, I want reaps to leave the instance
   alone and say that koto reported a live run there, so that the successor
   can still pick it up; once a successor continues the run from another
   instance, or the run finishes, I want the old instance taken.
3. As an operator cleaning up, I want `niwa destroy` on an instance whose
   session is running, or that the workspace's command says is still needed,
   to refuse and tell me why, so that I don't delete work by accident, and
   `--force` to go ahead when I mean it.
4. As a coordinator whose held worker's instance was destroyed before I sent
   the go, I want to resume the worker through niwa so that it comes back in
   a restored, trusted directory with the permission mode, environment and
   name it was dispatched with, and my messages reach it with no approval
   hold.
5. As a coordinator after a day of dispatching, I want to ask koto which
   workflow runs my workers hold and see each worker under me, with a worker
   niwa reaped shown closed, so that I know what I'm still waiting on.

## Requirements

### The pre-removal command (niwa)

- **R1.** A workspace may declare, in its workspace configuration, a
  pre-removal command: a shell command niwa runs before it removes an
  instance or a worktree, with a per-run timeout and a list of environment
  variable names to pass. The load applies the same refusals
  `[session.on_reap]` applies to its own command, timeout and names.
- **R2.** niwa runs the command once for each instance it is about to remove
  in these steps, each with a fixed step name: the reap of mapped ephemeral
  instances (`reap`), the reap's backstop for unmapped dispatch instances
  (`reap-backstop`), `niwa destroy` of one instance (`destroy`), each instance
  of `niwa destroy --workspace` (`destroy-workspace`), `niwa watch`'s discard
  of a staged review instance (`watch-discard`), and the hook-made worktree
  sweep, once per worktree (`worktree-sweep`). It doesn't run for the
  rollback of an instance niwa created in the same command, or for `niwa
  reset`.
- **R3.** The command runs under `/bin/sh -c` from the workspace root, with
  stdin empty and exactly this environment: `PATH`, `HOME`,
  `NIWA_REMOVE_PATH` (the absolute path about to be removed), `NIWA_REMOVE_STEP`
  (the step name from R2), `NIWA_SESSION_IDS` (the session ids niwa maps to
  that instance, space-separated, empty when there are none), and each name
  the declaration lists that is set in niwa's environment.
- **R4.** Exit 0 lets the removal proceed. Any other exit, a run past its
  timeout, or a failure to start spares the path for this removal. niwa then
  prints one line to stderr naming the instance (by the name `niwa destroy`
  takes) and the reason: for a non-zero exit, the last non-empty line of the
  command's output; for a timeout or a start failure, which of those
  happened. The reason is at most 200 bytes, with control characters
  replaced, as the on-reap warning already does.
- **R5.** A spared path is left exactly as it was, with every session mapping
  kept, so a later reap asks again. Sparing never increments the on-reap
  failure count and never drops a mapping.
- **R6.** With no pre-removal command declared, every removal path behaves
  exactly as it does today and niwa starts no process for it.
- **R7.** niwa contains no code that reads koto's store, links, anchors or
  configuration, and its test suite passes on a host with no koto binary on
  `PATH`.
- **R8.** A run's timeout defaults to 10 seconds. One step's runs are
  sequential and together take at most 30 seconds from the first run's
  start: each run's timeout is cut to whatever remains of the 30 seconds,
  and a run that would start after they're spent doesn't start, spares its
  path, and its line says the step's time ran out.

### The worktree sweep (niwa)

- **R9.** When niwa's WorktreeCreate hook makes a worktree, it records the
  requesting Claude Code session's id and whether that session had a job
  entry at that moment.
- **R10.** For a hook-made worktree recorded as made by a session with a job
  entry, the sweep treats the session as ended only once its job entry is
  gone, however long its transcript has been idle. A record younger than the
  reap idle grace period (`NIWA_REAP_IDLE_GRACE`, 24 hours by default) is
  still never swept, and a worktree today's unchanged check calls changed
  (its HEAD or branch moved off the base commit, or a tracked or recorded
  ignored file differs) is still never removed.
- **R11.** For a hook-made worktree recorded as made by a session with no job
  entry, and for a record written before R9 (no recorded session id), the
  sweep keeps today's rule: the record is at least the grace period old and
  the transcript is gone or idle for the grace period.
- **R12.** When the jobs directory is missing or can't be read (including
  when the home directory can't be resolved), the sweep removes no worktree
  that R10 governs.
- **R13.** Before removing a worktree, the sweep runs the pre-removal command
  for the worktree path (step `worktree-sweep`), and a non-zero answer spares
  it as R4 and R5 describe.

### Destroying an instance a session still needs (niwa)

- **R14.** Before any prompt and before removing anything, `niwa destroy`
  checks each instance it would remove: whether a session niwa mapped to it
  is running, as `claude agents --json --all` reports it (a live process),
  and, when one is declared, the pre-removal command's answer (one run per
  instance; this is the R2 run for that instance, not a second one).
- **R15.** Without `--force`, when any check keeps an instance, destroy exits
  non-zero, removes nothing, and lists every reason for every kept instance:
  each running session by its short id (the `id` field `claude agents`
  reports, which `claude rm` takes) and, when recorded, its session name, and
  the command's reason line.
- **R16.** `niwa destroy --force <name>` runs the same checks, prints each
  reason it is overriding, and then removes the instance, as well as skipping
  the unpushed-work check it skips today.
- **R17.** `niwa destroy --workspace` removes nothing while any instance is
  kept by R14, and names each such instance with its reasons. It doesn't
  accept `--force` (as today); the operator destroys those instances by name
  first.
- **R18.** When an instance has a session niwa mapped to it and the session
  listing fails, destroy says so and refuses unless `--force` is given. An
  instance with no mapped session needs no listing.

### Resuming a dispatched worker (niwa)

- **R19.** `niwa dispatch` records, for each worker it launches, a launch
  record: the resolved permission mode, the session name, the model, the
  settings document it passed, and the names and values of the environment
  variables it set for the worker. Values of names `[session.dispatch_env]`
  would refuse as credentials are never recorded.
- **R20.** A launch record survives `niwa destroy` of the instance and the
  on-reap processing of the session. niwa removes it only once Claude Code no
  longer holds the session's transcript.
- **R21.** `niwa resume <name> [prompt]` finds the worker by instance name,
  session id or session name. An argument matching no launch record, or more
  than one, is refused and every match is listed.
- **R22.** `niwa resume` resumes the session with the recorded launch, never
  with values re-derived from current configuration. When the current
  configuration would give a different permission mode, it says so on stderr
  and uses the recorded one.
- **R23.** When the instance directory is gone, `niwa resume` first restores
  the instance under its original name and at its original path, from the
  workspace configuration and the repositories' pushed state, with the Claude
  trust grant niwa withdraws on destroy, then resumes the session there. It
  prints that unpushed work in the old directory isn't restored. When
  anything (a file, a directory or a symlink) exists at the original path but
  it isn't the instance, it refuses and names the path.
- **R24.** `niwa resume` refuses, naming the argument, when the session's
  transcript is gone or the session is already running, and starts nothing.
  On success it prints the resumed session's id and permission mode.
- **R25.** A resumed worker's instance is mapped and reaped like any
  dispatched instance, and its on-reap command receives the original
  dispatch id as `NIWA_REAP_KEY`.

### koto's answer (koto)

- **R26.** koto provides a read-only verb that, given a path, says whether a
  workflow session still needs it. A session is under the path when its
  execution anchor is the path or lies beneath it, compared by path component
  after resolving symlinks, so `/a/repo-2` is not under `/a/repo`.
- **R27.** The path is needed while at least one session under it is running
  (not terminal and not cancelled; koto has no paused state, so a held or
  paused run is running) and is not continued elsewhere. A session is
  continued elsewhere when some later member of its lineage (a session that
  continues it, or one that continues that one, and so on) has its execution
  anchor outside the path, whatever that member's own state. A later member
  with no recorded anchor doesn't count.
- **R28.** A session under the path that koto can't read, or whose relation
  koto can't judge, makes the path needed (exit 1, naming it as unreadable).
  A store koto can't list means it couldn't decide (exit 2). Neither ever
  exits 0.
- **R29.** The verb exits 0 when the path isn't needed, 1 when it is, and 2
  when it couldn't decide. On exit 1 it prints one line, at most 200 bytes,
  naming the session keeping the path whose name sorts first and why, and the
  count of others;
  on exit 2 it prints one line saying what it couldn't read.
- **R30.** The verb writes nothing: every session's log, keys and bucket
  copy are byte-identical before and after it runs, on the local and the
  cloud backend.
- **R31.** Under the cloud backend, the verb judges the sessions the host can
  read and treats a session whose relation it can't read as needing the path.

### The workspace configuration (dot-niwa)

- **R32.** dot-niwa's workspace configuration declares `[session.on_reap]`
  with koto's documented guarded anchor close (it exits 0 when
  `NIWA_REAP_KEY` is empty, when `PATH` has no koto, or when koto has no
  `session anchor close`, and otherwise closes the anchors observed with
  that key), and `[session.dispatch_env]`
  passing `KOTO_DISPATCHER_ANCHOR` and unsetting `KOTO_ANCHOR`.
- **R33.** Once a niwa release carries R1 and a koto release carries R26,
  dot-niwa declares the pre-removal command as koto's verb, guarded so that a
  host without koto, or with a koto lacking the verb, exits 0 and the removal
  proceeds as today.

## Acceptance Criteria

### The pre-removal command

- [ ] With no pre-removal command declared and a stub `sh` that records each
  invocation first on `PATH`, a reap, a destroy and a watch discard start no
  process for it, and niwa's existing reap, destroy and watch tests pass
  unchanged.
- [ ] The full niwa test suite passes with no koto binary on `PATH`, and no
  non-test Go file in niwa contains the strings `koto/links`, `.koto/`,
  `koto session` or `KOTO_`.
- [ ] A pre-removal declaration listing a credential-looking name (one
  ending `_TOKEN`), a name starting `NIWA_`, a timeout that doesn't parse or
  a timeout of `0s` is refused when the configuration loads, naming the
  field; a valid declaration loads.
- [ ] With a command that prints `kept by test` and exits 1, a reap started
  implicitly by `niwa dispatch` leaves a reapable ephemeral instance on disk
  with its mapping and its on-reap failure count unchanged, and stderr has a
  line naming the instance and `kept by test`; three more reaps leave the
  same state; a reap with a command that exits 0 then removes it.
- [ ] Each of the steps `reap`, `reap-backstop`, `destroy`,
  `destroy-workspace`, `watch-discard` and `worktree-sweep` runs the command
  with `NIWA_REMOVE_STEP` set to that name and spares its path on exit 1; a
  rollback after a failed dispatch and `niwa reset` run no command.
- [ ] The command's environment holds exactly `PATH`, `HOME`,
  `NIWA_REMOVE_PATH`, `NIWA_REMOVE_STEP`, `NIWA_SESSION_IDS` and the declared
  names that are set; a declared name that is set appears, one that is unset
  is absent, an undeclared variable is absent, its working directory is the
  workspace root, a read from stdin sees end of file at once, and
  `NIWA_SESSION_IDS` is empty for an unmapped backstop instance and lists
  both ids for an instance with two mappings.
- [ ] The stderr line for a spared instance names it by its directory's base
  name, the argument `niwa destroy` accepts.
- [ ] A command that sleeps past a 1-second timeout spares the instance and
  its line says it timed out; a command naming a missing executable spares it
  and its line says it failed to start; a command whose last line is 300
  bytes with an escape character produces a reason of at most 200 bytes with
  the escape replaced.
- [ ] With no timeout declared and four instances whose commands each sleep
  12 seconds, each of the first three runs is cut off at 10 seconds and
  spares its instance as timed out, and the fourth doesn't start and its line
  says the step's time ran out.

### The worktree sweep

- [ ] A hook run for a session with a job entry records that session's id
  and that it had a job entry; a hook run for a session with none records
  its id and that it had none.
- [ ] A held worker in a hook-made worktree, with its record and transcript
  both 48 hours old and its job entry still listed, keeps its worktree
  through every reap; once its job entry is removed, the next reap removes
  the worktree.
- [ ] A hook-made worktree recorded with no job entry, and one whose record
  has no session id, are removed once record and transcript are older than
  the grace period, and kept while the transcript is younger.
- [ ] A job-keyed worktree whose record is younger than the grace period, or
  that has a commit or file change since creation, is kept with its job
  entry gone.
- [ ] With the jobs directory unreadable, no job-keyed worktree is removed.
- [ ] A worktree that would otherwise be removed is kept when the
  pre-removal command exits 1 for its path, with a line naming it.

### Destroy

- [ ] `niwa destroy <name>` of an instance whose mapped session `claude
  agents --json --all` reports with a live process exits non-zero, names the
  session's short id and session name, shows no prompt and leaves the
  instance on disk.
- [ ] With the same instance also refused by the pre-removal command, the
  refusal lists both reasons, and the command ran once.
- [ ] `niwa destroy --force <name>` of that instance prints both reasons as
  overridden and removes the instance.
- [ ] `niwa destroy --workspace --confirm <name>` with one instance kept by a
  running session and another kept by the command removes nothing and names
  both instances with their reasons; `niwa destroy --workspace --force` is
  rejected as it is today.
- [ ] A running session with no recorded session name is named by its short
  id alone.
- [ ] With the session listing failing, destroy of an instance with a mapped
  session refuses, `--force` proceeds, and destroy of an instance with no
  mapped session proceeds without listing.

### Resume

- [ ] After a worker dispatched with `--permission-mode bypassPermissions`
  has its instance destroyed, its launch record still exists and holds the
  permission mode, session name, model and settings, the name and value of a
  non-secret variable `[session.dispatch_env]` passed, and no value of a name
  ending `_TOKEN`.
- [ ] After that worker's job is removed and a reap runs its on-reap
  command, the launch record is still there while the transcript exists.
- [ ] `niwa resume <instance name>` for that worker, with a stub `claude`
  recording its arguments, restores the directory at the original path,
  writes the Claude trust entry for it, runs `claude --resume <session id>`
  with `--permission-mode bypassPermissions`, the recorded `--name` and
  settings, prints the session id and permission mode, and prints that
  unpushed work isn't restored; nothing created the directory before niwa
  did.
- [ ] `niwa resume <session id>` and `niwa resume <session name>` resume the
  same worker as `niwa resume <instance name>`.
- [ ] `niwa resume` of a worker whose instance directory is still there
  restores nothing, writes no trust entry and resumes in place.
- [ ] With the workspace's default permission mode changed after dispatch,
  `niwa resume` passes the recorded mode and prints that it differs.
- [ ] The resumed instance has a session mapping whose dispatch id equals the
  original, and a later reap after its job is removed runs the on-reap
  command with that id as `NIWA_REAP_KEY`.
- [ ] `niwa resume` exits non-zero, naming the argument and starting nothing,
  for: a name with no launch record; a name matching two records (both
  listed); a session whose transcript is gone; a session already running;
  and an original path occupied by another directory.
- [ ] Once Claude Code no longer holds a session's transcript, the next reap
  removes its launch record.

### koto's verb

- [ ] Given an instance path holding the execution anchor of a running
  session that nothing continues, the verb exits 1 and names the session;
  after a session anchored in another directory continues it, the verb exits
  0; separately, after the session reaches a terminal state, or is
  cancelled, the verb exits 0. A verb that always exits 0, or always 1, fails
  this criterion.
- [ ] With a chain v1 under the path, v2 continuing v1 also under the path,
  and v3 continuing v2 anchored outside it, the verb exits 0; with v3 also
  under the path and running, it exits 1.
- [ ] A successor with no recorded anchor leaves the path needed.
- [ ] `/a/repo-2` holding a running session doesn't make `/a/repo` needed,
  and a symlinked spelling of a path gives the same answer as the path.
- [ ] A session file the verb can't parse, under the path, makes it exit 1
  naming that session as unreadable; a store it can't list makes it exit 2.
- [ ] With three running, un-continued sessions under the path, the verb's
  output on exit 1 is one line of at most 200 bytes naming the one whose
  name sorts first and saying 2 others keep the path.
- [ ] With v2 continuing v1, v2 anchored outside the path and already
  terminal, and v1 running under the path, the verb exits 0.
- [ ] Hashing the store before and after the verb shows no change, on the
  local backend and on the cloud backend against koto's own test bucket
  (never a real one); under the cloud backend a session whose relation can't
  be read keeps the path needed.

### End to end and the workspace configuration

- [ ] dot-niwa's workspace configuration loads under niwa with no warning,
  and declares the on-reap command with the three guards R32 names and the
  dispatch environment passing `KOTO_DISPATCHER_ANCHOR` and unsetting
  `KOTO_ANCHOR`.
- [ ] Once declared, dot-niwa's pre-removal command exits 0 on a host whose
  `PATH` has no koto, and on one whose koto lacks the verb.
- [ ] In a scratch workspace with koto's verb declared as the pre-removal
  command, an instance holding the anchor of a paused, un-continued run is
  spared by every reap and by `niwa destroy` without `--force`, with koto's
  reason line in niwa's stderr; once a successor continues that run from
  another instance, or it finishes, the next reap removes the instance.
- [ ] In a scratch workspace with dot-niwa's declarations, a coordinator
  session dispatches a worker through niwa; `koto session hierarchy` lists
  the worker's anchor with a dispatched-by link to the coordinator's anchor,
  and after niwa reaps the worker the worker's anchor has a close record.

## Out of Scope

- The continues relation itself, its retention behaviour, its bucket
  back-reference and import's new contract. koto is building them separately;
  this feature reads the relation and waits for it.
- Removal guards inside koto (a retention floor on prune, `KOTO_HOME`
  isolation, read fixes) and a coordinator successor's takeover of a run.
- Retiring today's workarounds (hand-made directories, the host-wide koto
  freeze); that follows once this and the neighbouring work land.
- Any koto-specific code path in niwa, or niwa reading koto's files.
- Changing which instances a reap may consider at all: the ephemeral marker,
  the backstop's name rule and TTL, and developer instances keep their rules.
- Changing when reaps run. The implicit reap at dispatch, create and watch
  stays; the pre-removal command is what makes it safe.
- Worktrees made with `niwa worktree create`, which reap never removes.
- Resuming agents other than Claude Code beyond today's re-entry hints, and
  restoring a worker's unpushed working tree.

## Decisions and Trade-offs

- **The command's exit decides, and every failure spares.** Exit 0 removes;
  any non-zero exit, a timeout or a failure to start spares and is reported.
  The alternative, removing when the command can't answer, would make a
  broken or slow command delete exactly the directories it exists to keep.
  The cost is that a broken command keeps instances until someone fixes it or
  uses `niwa destroy --force`; R4's report makes that visible on every reap.
- **A new declaration, not a field on `[session.on_reap]`.** The on-reap
  command runs after a removal, retries on failure and eventually drops the
  mapping; the pre-removal command runs before and must never drop anything.
  One table with two opposite failure meanings would be easy to misdeclare.
  The table's name and keys are the design's call.
- **`--force` is the one override.** It already skips the unpushed-work
  check, and the operator who forces a destroy means "remove it". A separate
  flag per guard was considered and rejected as three flags for one intent;
  the refusal lists every reason so nobody forces blind.
- **`--workspace` refuses as a whole.** Removing the free instances and
  leaving the rest would leave a half-destroyed workspace with no clear next
  step. The operator destroys the kept instances by name first.
- **The worktree sweep keys on the job only when there was one.** A session
  that never had a job entry (an interactive one) would read as ended at once
  under a job-only rule, which would sweep worktrees today's rule keeps. So
  the job rule applies where a job existed at creation, and today's rule
  stays for the rest.
- **The sweep asks the same command about the worktree.** A held worker's
  workflow run is often anchored inside its worktree, so the worktree path is
  as much a "still needed" question as the instance path.
- **Resume replays, it doesn't re-derive.** niwa#369 asks for the flags
  dispatch used; re-deriving from current configuration would silently change
  a worker's posture when the workspace changed in between.
- **The launch record lives as long as the transcript.** Without a transcript
  `claude --resume` has nothing to resume, so there's no point keeping the
  record longer, and keeping it shorter breaks user story 4.
- **A continuation anywhere down the lineage releases the path.** If a later
  member of a run's lineage is anchored outside the path, the work has moved,
  whether that member is still running or already finished. Requiring the
  direct successor alone would keep an instance whose run moved two hops
  away; requiring the outside member to be running would keep an instance
  whose run finished elsewhere.
- **One check per instance in destroy.** The running-session check and the
  pre-removal command run once, before any prompt, and `--force` runs them
  too so it can say what it overrides.
- **koto's "needed" is narrow on purpose.** Running and not continued
  elsewhere is the whole rule; koto has no notion of a paused run, and a held
  worker's session and an un-taken-over predecessor are both just running.
  Anything koto can't read counts as needed, for the same reason niwa spares
  on failure.
- **The implicit reaps stay.** Stopping the reap at the top of `niwa
  dispatch` was considered, but it's how a workspace stays bounded; making
  each removal ask first fixes the harm without giving that up.

## Known Limitations

- A pre-removal command that always fails keeps every instance it's asked
  about until it's fixed; reap reports each one, and `--force` still works.
- Because reaps run at the top of `niwa dispatch`, `niwa create` and `niwa
  watch`, a slow pre-removal command can add up to 30 seconds per removal
  step to those commands.
- A restored instance comes back from the workspace configuration and the
  pushed state of its repositories. Unpushed work in the destroyed directory
  is gone.
- Under the cloud backend, until koto's bucket back-reference ships, no cloud
  session can record that it continues another, so koto's verb keeps a
  predecessor's instance until that predecessor finishes.
