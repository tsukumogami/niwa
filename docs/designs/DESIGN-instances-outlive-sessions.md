---
schema: design/v1
status: Accepted
problem: |
  niwa removes ephemeral instances and hook-made worktrees on signals that
  can't see a paused worker or a workflow run waiting for its successor: the
  Claude Code job entry, transcript idleness, and a destroy that ignores a
  running session. It has no way to ask whether something outside it still
  needs a path, it keeps nothing it would need to resume a worker whose
  directory went, and this organization's workspace doesn't declare the two
  koto seams niwa already ships.
decision: |
  niwa gains one engine-neutral gate, a workspace-declared
  [session.before_remove] command it runs immediately before every removal
  (reap, backstop, watch discard, the worktree sweep and destroy), where exit
  0 removes and anything else spares and is reported. koto answers it with a
  new read-only verb, `koto session needed <PATH>`, over execution anchors and
  the continues relation. The worktree sweep records the requesting session
  and keys on its job entry. destroy runs a preflight that refuses a running
  or vetoed instance unless forced. dispatch writes a launch record that
  `niwa resume` replays, restoring a destroyed instance by name first.
  dot-niwa declares on_reap, dispatch_env and, once released, before_remove.
rationale: |
  A command keeps niwa free of koto while letting any engine answer, and
  making every failure spare matches the reaper's rule that an instance is
  never reclaimed on no evidence. koto already has a read-only, component-wise
  "sessions under this path" read and, since koto#481, a successor walk, so the
  verb composes existing reads instead of inventing a store reader. A separate
  launch record is the only place the dispatch flags can survive destroy
  without changing what the reaper's mapping means. The work splits so that
  only koto's judgement of successors across hosts and imports waits for the
  continues relation to finish landing; the rest builds on what has landed.
upstream: docs/prds/PRD-instances-outlive-sessions.md
decision_provenance: inline-resolved
---

# DESIGN: Instances and worktrees outlive the sessions that need them

## Status

Accepted

## Context and Problem Statement

niwa removes directories in six places. The reap of mapped ephemeral
instances (`selectReapTargets` and `reapWorkspace` in `internal/cli/reap.go`)
reclaims an instance once its Claude Code job entry is gone. The backstop
(`reapBackstop`) reclaims an unmapped, dispatch-named instance past a 30-minute
TTL unless a live job or recorded session is rooted in it. `niwa watch`'s
`discardStagedRecord` removes a staged review instance whose job isn't live.
The hook-made worktree sweep (`sweepHookWorktreesIn` in
`internal/cli/reap_worktrees.go`) removes an unchanged worktree once its record
is a day old and the requesting session's transcript is gone or a day idle.
`niwa destroy` removes one instance or, with `--workspace`, all of them; it
lists Claude Code's sessions (`claude agents --json --all`) only to decide
which to `claude rm`, keeps a running one's row, and removes the directory
regardless. The first three, plus the sweep, run implicitly at the top of
every `niwa dispatch`, `niwa create` and `niwa watch` (`reapOpportunistically`).

None of those can tell that a session still needs a path. A held worker's job
entry stays listed, so the instance reap spares it, but its worktree is swept
after a day of transcript silence. A workflow run whose Claude Code session was
deleted while the run waits for a successor has no job entry at all, so its
instance is reaped. Destroy takes a running session's directory from under it.
The information that would answer "is this path still needed" lives in koto:
each workflow session records an execution anchor (`execution_dir` in its
header), and since koto#481 a successor records the session it continues. niwa
must not read that store (the PRD's R7): the answer has to come through a seam
any engine can implement.

When a directory goes anyway, nothing is left to come back with. Dispatch
resolves the permission mode (flag, then the host's
`dispatch_permission_mode`, then the instance's posture), the settings
document, the model, the session name and the launch environment, and records
only the session name and dispatch id on the session mapping
(`internal/workspace/session_map.go`). Destroy withdraws the instance's Claude
trust grant, runs `claude rm` (so `claude attach`, the only re-entry niwa
builds, has nothing to attach to), and deletes the mapping once the on-reap
command succeeds. The provisioner always computes a fresh instance name, and
Claude Code keys a transcript by its working directory, so a resume has to
happen at the original path.

Finally, niwa already ships `[session.on_reap]` and `[session.dispatch_env]`
(`internal/config/session.go`, `internal/cli/on_reap.go`) for koto, but
dot-niwa's `.niwa/workspace.toml` declares neither, so koto's agent-session
anchors are never closed when niwa removes a session and a dispatched worker's
anchor carries no link to its coordinator's.

## Decision Drivers

- **niwa stays engine-free.** No niwa code reads koto's store, links, anchors
  or configuration, and niwa's tests pass with no koto on `PATH` (PRD R7).
- **No reclamation on no evidence.** The reaper's standing rule: a directory
  whose need can't be established is kept and reported, never removed. A
  check that fails, times out or can't start spares (R4, R5).
- **Nothing declared means nothing changes.** Without the new declaration,
  every removal path behaves as today and niwa starts no extra process (R6).
- **The implicit reap stays affordable.** It runs under commands people ran
  for another reason, so the gate is bounded: 10 seconds per run by default,
  30 seconds for one step's runs together, sequential (R8).
- **One override.** `--force` on destroy already means "remove it"; it
  overrides the running-session refusal and the gate too, after saying what
  it overrides (R14 to R17).
- **Resume replays, it doesn't re-derive.** A resumed worker gets the flags
  dispatch used, read from a record, even when configuration changed since
  (R19 to R22, niwa#369).
- **koto reads stay read-only.** The verb must leave every session's log,
  keys and bucket copy byte-identical (R30), which rules out `koto context
  get`, which can write (koto#436).
- **Build order follows the continues relation.** koto#481 landed the
  relation and its successor walk on one host; its bucket back-reference and
  import contract are still to come. Only the part of koto's verb that
  judges cross-host and imported successors may wait for those; everything
  else should be buildable now.

## Considered Options

### Decision 1: The pre-removal seam's contract and where niwa calls it

Resolved inline.

The PRD fixes the exit contract and the environment: exit 0 removes, any
non-zero exit, a timeout or a failure to start spares this removal and prints
one line naming the instance (by its directory name, the name `niwa destroy`
takes) and the reason (the command's last non-empty output line, at most 200
bytes, control characters replaced). The command sees exactly `PATH`, `HOME`,
`NIWA_REMOVE_PATH`, `NIWA_REMOVE_STEP` (one of `reap`, `reap-backstop`,
`destroy`, `destroy-workspace`, `watch-discard`, `worktree-sweep`),
`NIWA_SESSION_IDS` (space-separated, empty when unmapped) and its declared
names. What's left is how it's declared and where niwa calls it.

Key assumptions:
- Rollback destroys of an instance niwa created in the same command, and
  `niwa reset`, keep calling the destroy function directly and never the gate.
- The gate loads its declaration once per niwa command, as the on-reap step
  does.

#### Chosen: a `[session.before_remove]` table run by one gate at every removal site

A new table beside `[session.on_reap]`, with `command`, `timeout` and `env`,
validated at load by the same name and timeout refusals
(`ValidateSessionEnvName`, positive durations). One helper,
`removalGate.ask(path, step, sessionIDs)`, runs it with the on-reap runner's
exec machinery (absolute `/bin/sh`, its own process group killed on timeout,
bounded stderr tail, the same sanitizer) and holds the step's 30-second
budget. It is called immediately before `destroyInstanceFunc` in the reap and
backstop loops and in `discardStagedRecord`, per worktree in
`sweepHookWorktreesIn`, and once per instance in destroy's new preflight. A
refusal makes the caller skip that path exactly as a failed destroy already
does: the mapping stays and the next reap asks again. A separate table keeps
the two seams' opposite failure meanings apart: on-reap runs after a removal,
is retried and is eventually dropped, while this one runs before and never
drops anything.

#### Alternatives Considered

**A `veto = true` field on `[session.on_reap]`.** One table with two opposite
failure meanings is easy to misdeclare, and on-reap's retry and drop
bookkeeping would leak into a check that must never drop a mapping.

**A structured protocol (JSON request on stdin, JSON verdict on stdout).**
More surface for a yes-or-no question. niwa only reports one reason line, and
an exit code keeps a declaration's guard a one-line shell condition.

**Wrapping `workspace.DestroyInstance`.** It has no session knowledge, tests
replace `destroyInstanceFunc`, and rollbacks must bypass the check. The gate
belongs at the call sites that know the step.

### Decision 2: How koto answers without niwa reading koto

Resolved inline.

PRD R26 to R31 state the rule: a path is needed while a workflow session
anchored at or under it (by path component, after resolving symlinks) is
running and isn't continued elsewhere, meaning no later member of its lineage
is anchored outside the path; terminal and cancelled sessions don't need it;
anything unreadable keeps it; the verb writes nothing. koto has no paused
state, so a held worker's run and an un-taken-over predecessor are both
running.

Key assumptions:
- The hierarchy Graph's successor walk can be built read-only for the
  sessions under one path without loading retention judgement.
- One inventory scan plus one Graph build per call is acceptable at reap
  frequency, since the gate calls it once per removal candidate.

#### Chosen: a new verb, `koto session needed <PATH>`

It reuses the inventory read behind `koto session list --execution-dir`
(read-only by contract, component-wise containment, symlinks resolved, status
running, terminal, cancelled or unreadable), then for each running session
walks its successors through the hierarchy Graph koto#481 added, never through
`koto context get`. It exits 0 when the path isn't needed, 1 when it is
(printing one line of at most 200 bytes naming the session whose name sorts
first, why, and how many others), and 2 when it couldn't decide (a store it
can't list). An unreadable session or relation under the path is exit 1,
named as unreadable. Under the cloud backend it judges the sessions the host
can read; until the relation's bucket back-reference ships, no cloud session
holds a relation, so a running cloud session keeps its path.

#### Alternatives Considered

**A `--needed` flag on `koto session list`.** A listing command with JSON
output would also have to become a predicate with exit codes, two contracts
on one command.

**A dot-niwa script composing `koto session list` and `koto session
hierarchy` with `jq`.** It needs no koto change, but it puts koto's lineage
rule in workspace configuration, needs one hierarchy call per session, and
would drift from koto's own semantics.

**niwa reading koto's store.** Breaks R7 and the reason the seam exists.

**Reusing retention's relation judgement.** It answers "may this session be
deleted yet" with a keep window, not "is this path's work still here".

### Decision 3: The resume path's shape

Resolved inline.

PRD R19 to R25: dispatch records the launch, the record survives destroy and
on-reap and lives as long as the transcript; `niwa resume <name> [prompt]`
finds the worker by instance name, session id or session name, refuses
ambiguity, replays the recorded launch, restores a missing instance under its
original name and path with its trust grant, refuses an occupied path, a gone
transcript or a running session, and maps the resumed worker under its
original dispatch id.

Key assumptions:
- `claude --bg --resume <session-id>` from the same directory continues the
  same session; if Claude Code forks a new id, the capture step records the
  new id and the launch record follows it.
- The applier's create path, given an explicit instance name, reproduces the
  instance's trust grant and settings the way provisioning does.

#### Chosen: a launch record per dispatch, replayed by `niwa resume`

Dispatch writes `<workspace root>/.niwa-launch/<dispatch-id>.json` (mode
0600, directory 0700) once it has captured the session: dispatch id, session
id, agent, instance name and path, transcript path, resolved permission mode,
session name, model, the settings document it passed, and the environment
names and values it set, leaving out any value whose name
`ValidateSessionEnvName` refuses as a credential. The store sits outside the
configuration directory, like `.niwa-lineage/`, so neither a reap nor a
refresh touches it. `niwa resume` resolves its argument against the records,
restores a missing instance through `Applier.Create` with the recorded name
(the same call provisioning makes, minus the fresh-name step), then builds the
agent's resume argv in `dispatch_reentry.go` (the one file allowed to read
`ResumeArgs`): `claude --bg --resume <session-id>` plus `--permission-mode`,
`--name`, `--settings` and `--model` from the record, run from the instance
directory with the recorded environment. It captures the job the way dispatch
does and writes a fresh session mapping with the original dispatch id. The
reap drops a record whose transcript is gone.

#### Alternatives Considered

**Extend the session mapping and keep it after destroy.** The mapping is the
reaper's source of truth; a mapping for a removed instance changes what the
stored-mapping walk and the on-reap command do with it.

**Extend the lineage entry.** Lineage is a provenance and authority record
with a closed member set and an 8 KiB cap. Launch posture is a different
concern with its own retention.

**Re-derive the flags at resume time.** niwa#369 asks for the flags dispatch
used; re-deriving silently changes a worker's posture when configuration
changed in between.

**Resume through `claude attach`.** It works only while the job entry exists,
which is exactly what's gone after `claude rm` or a deleted session.

### Decision 4: Keying the worktree sweep on job presence

Resolved inline.

PRD R9 to R13. The WorktreeCreate hook's payload carries Claude Code's session
id, but `runFromHookCreate` never stores it (`ClaudeConversationID` exists on
the record and is never written).

Key assumptions:
- A subagent's WorktreeCreate payload carries its parent session's id, and
  keying on the parent's job is what's wanted.

#### Chosen: record the requesting session and whether it had a job, then key on the job

At create, the hook writes the payload's session id into
`ClaudeConversationID` and a new `ClaudeJobAtCreate` flag, true when
`readJobState` found a job entry for that id. The sweep's `claudeSessionEnded`
becomes: for a record with the flag, the session has ended once
`sessionLive(jobsDir, id)` is false and the jobs directory was readable; for a
record without it (an interactive session, or a record from before the
change), today's transcript rule. The age floor (`NIWA_REAP_IDLE_GRACE`, 24
hours by default) and the unchanged-worktree check stay in front of both, and
the gate (Decision 1) runs for the worktree path last.

#### Alternatives Considered

**Parse the session id from the transcript file name.** It works today but
leans on Claude Code's file naming; it stays only as a fallback for records
written before the change.

**Job presence for every hook-made worktree.** Sweeps an interactive
session's worktree a day after creation while it's still in use.

**Keep the transcript rule with a longer grace period.** A held worker can be
held for any length of time; any fixed idleness bound eventually removes it.

**Check for a job only at sweep time, recording nothing.** At sweep time an
absent job entry reads the same whether the session never had one (an
interactive session, which must keep today's rule) or had one that was
removed (a dispatched worker that is done). Only a fact captured at create can
tell the two apart.

## Decision Outcome

The four choices meet at one rule: a directory goes only when everything that
can speak for it says it may. niwa's own evidence comes first and is
unchanged in kind: the ephemeral marker, the job entry (now also for
hook-made worktrees whose requesting session had one, so a held worker's
worktree lives as long as its job is listed), and, in destroy's new
preflight, whether a session is running. The workspace's
evidence comes second, through `[session.before_remove]`, and for this
organization's workspace that is koto saying whether a run is still anchored
under the path and hasn't moved on. Any failure to get an answer keeps the
directory. `--force` on a single destroy is the one way past all of it, and it
says what it is overriding.

If a directory goes anyway, the launch record and `niwa resume` close the
loop: the worker comes back at the same path, trusted and in the posture it
was dispatched with, so the conversation resumes where it stopped. Everything
a session still needs from its directory is either kept or restored, and
neither path makes niwa read koto.

## Solution Architecture

### Overview

```
removal step (reap | reap-backstop | watch-discard | worktree-sweep | destroy)
   │ niwa's own guards first (ephemeral, job entry, running session, unchanged)
   ▼
removalGate.ask(path, step, sessionIDs)  ── [session.before_remove] declared? ──no──► remove
   │ yes: /bin/sh -c <command>, fixed env, bounded
   ▼
exit 0 ──► remove        anything else ──► spare, keep mapping, print reason
                                             (destroy without --force: refuse)

dot-niwa before_remove ──► guard (no koto / no verb ⇒ exit 0) ──► koto session needed "$NIWA_REMOVE_PATH"
                                                                   inventory (execution_dir under path)
                                                                   + successor walk (continues relation)

niwa dispatch ──► .niwa-launch/<dispatch-id>.json ──► niwa resume ──► restore instance by name (if gone)
                                                                    ──► claude --bg --resume <id> + recorded flags
```

### Components

**niwa: `internal/config/session.go`.** `SessionConfig` gains `BeforeRemove
SessionBeforeRemoveConfig` (`Command`, `Timeout`, `Env`), validated in
`validateSessionDeclaration` like `OnReap`, with `DefaultBeforeRemoveTimeout =
10s`.

**niwa: `internal/cli/removal_gate.go` (new).** `removalGate` loads the
declaration once per command and exposes `ask(path, step string, sessionIDs
[]string) gateVerdict` (`Allow` or `Spare` with a reason). It shares the exec,
environment, tail-buffer and sanitizer helpers with `on_reap.go` (moved into a
small shared file), holds the step's 30-second budget (each run's timeout is
cut to what's left; a run that can't start spares with "time ran out"), and
writes the spare line to the command's stderr writer. Unlike the on-reap
runner, which passes the command's stdout straight through, the gate captures
both streams into bounded buffers and prints only the sanitized reason line,
so nothing the command writes reaches the terminal raw. The sanitizer
replaces C0 and C1 control characters and Unicode bidirectional overrides,
and the line quotes the reason after a fixed prefix (`niwa: kept <instance>:
reason from before_remove: <reason>`, with the reason Go-quoted so embedded
quotes are escaped) so a reader, person or agent, sees it as the command's
data rather than niwa's own words. The run keeps the on-reap
runner's 500 ms wait delay, so a grandchild that holds a pipe open can't
stretch a run past its timeout. `NIWA_SESSION_IDS` carries only ids that pass
`workspace.ValidSessionID`. A test in the package lists the callers of
`destroyInstanceFunc` and `worktree.DestroySession` and fails on a new one,
so a removal path added later can't skip the gate silently. With no command
declared, `ask` returns `Allow` without starting anything.
`validateSessionDeclaration` gains the same checks for `BeforeRemove.Env` and
`BeforeRemove.Timeout` it applies to `OnReap`.

**niwa: call sites.** Each removal pass constructs its own gate from the
workspace root and so owns its own 30-second budget: the mapped reap, the
backstop, the worktree sweep, the watch discard, and the destroy preflight.
`reapWorkspace` and `reapBackstop` call the gate right before
`destroyInstanceFunc` (after the existing re-asks) and `continue` on `Spare`.
`discardStagedRecord` in `watch.go` does the same. `sweepHookWorktrees`, which
has the workspace root, builds the gate and passes it to
`sweepHookWorktreesIn`, which calls it after `WorktreeUnchanged` and before
`worktree.DestroySession`, with `NIWA_SESSION_IDS` set to the record's Claude
session id when one is recorded. The gate always runs from the workspace
root, and its reason line goes to the pass's stderr writer. `dispatch.go`'s
and `watch.go`'s rollbacks and `reset.go` don't call it. The gate keeps no
state between runs, so two reaps running at once each ask on their own, as
the reaper's existing re-asks already do.

**niwa: destroy preflight (`internal/cli/destroy_preflight.go`, new).** For
each instance a destroy would remove, it lists Claude Code's sessions once
(`listClaudeSessionsFunc`), finds owning mappings that are running
(`claudeSessionRow.running`), and asks the gate with step `destroy` or
`destroy-workspace`. It returns per-instance reasons. `runDestroyInstance`
calls it before the unpushed-work prompt; without `--force` any reason means
exit non-zero, nothing removed, every reason listed (short id, session name
when recorded, gate reason). With `--force` it prints each reason as
overridden and continues to today's path. `runDestroyWorkspace` calls it for
every instance before the confirmation prompt and refuses as a whole. A
listing failure is a reason when the instance has a mapped session.

**niwa: worktree records.** `runFromHookCreate` checks the payload's session
id with `workspace.ValidSessionID` (a lowercase UUID; anything else is not
recorded, and the record then falls under today's rule), writes it to
`SessionLifecycleState.ClaudeConversationID` (the sweep checks it again with
`ValidSessionID` when it reads the record) and sets the new
`ClaudeJobAtCreate` from `readJobState(defaultJobsDir(), id)`.
`claudeSessionEnded` gains the job branch described in Decision 4, placed
before today's empty-transcript guard (which returns "not ended" first), with
a `jobsDirReadable` check that treats a missing or unreadable directory as
"can't tell".

**niwa: launch records (`internal/workspace/launch_store.go`, new).**
`WriteLaunchRecord`, `ReadLaunchRecord`, `ListLaunchRecords`,
`RemoveLaunchRecord` over `<root>/.niwa-launch/`, keyed by a validated
dispatch id, atomic write-then-rename, 0600. `dispatch.go` writes the record
after the session mapping, from the values it already resolved
(`derivePermissionMode`, `renderLaunchSettings`, `applyLaunchEnv`'s result,
the forwarded name and model). A write failure warns and the dispatch goes on.
The reap's stored-mapping pass also drops records whose transcript is gone.

**niwa: `niwa resume` (`internal/cli/resume.go`, new).** Resolves the
argument against launch records (instance name, then session id, then session
name; no match refuses naming the argument, more than one refuses and lists
them). Refuses when the
transcript is gone or the session is running (same listing as the destroy
preflight). When the instance directory is missing, refuses if anything
occupies the path, otherwise restores it through a new
`provisionInstanceNamed(workspaceRoot, name)` in `instance_from_hook.go`,
beside `realProvisionInstance`, that calls `Applier.Create`
with the recorded name and prints that unpushed work isn't restored. Then it
builds the argv with a new `reentryResumeArgs(spec, record)` in
`dispatch_reentry.go`, launches it from the instance directory with the
recorded environment, captures the job through dispatch's capture path, and
writes a session mapping carrying the original dispatch id. When the current
configuration's permission mode differs from the record's, it prints so and
uses the record's. Before launching it runs the same machine authority
checks dispatch runs (`checkMachineAuthority`) and refuses when they fail, and
it prints the permission mode it is about to launch with.

Every record is validated when read, not trusted because niwa wrote it:

- the schema, the dispatch and session ids, and the agent;
- the permission mode against the agent's declared set;
- the model and session name against a plain-token shape (starting with a
  letter or digit, then letters, digits, `.`, `_`, `-`, bounded length), and
  both are passed as single `--name=<v>` and `--model=<v>` arguments so no
  value can be read as a flag;
- the settings document against a closed key set: only the keys
  `renderLaunchSettings` emits (the MCP approval list, each name matching
  `mcpServerNamePattern`, and the `env` object). Any other key, such as
  `hooks`, `apiKeyHelper`, `statusLine` or `permissions`, refuses the record;
- the environment, in the record and in the settings `env`, by an allowlist
  rather than a denylist: a name is replayed only when the workspace's
  current `[session.dispatch_env]` passthrough lists it or it is one of the
  names niwa itself sets (`config.LaunchSettingsEnvNames`). Any other name
  refuses the record, so a planted `CLAUDE_CONFIG_DIR`, `HOME`, proxy, CA or
  git variable can't reach the worker. Values are bounded in length and may
  hold no control characters.

The instance path and the transcript path are never read from the record:
the instance path is the workspace root joined with the recorded instance
name, which must pass the instance-name check, and the transcript path is
derived from it and the session id the way `sessionattach` does. A record
that fails any check is refused, named, and left for the operator. Record
files are opened without following symlinks, and the owner and mode are
checked on the opened descriptor (current user, 0600); the store directory
must be a real directory owned by the current user with mode 0700. The resume
argv puts `--` before the optional prompt. The occupied-path check uses
`Lstat`, so a symlink at the original path counts as occupied. Resume writes
the session mapping (with the recorded session id and original dispatch id)
before it launches, so the backstop never sees the restored instance as an
unmapped orphan, and corrects the id after capture if Claude Code reports a
new one. The reap drops a launch record only when the transcript is
positively gone; an unreadable transcript directory keeps it.

**koto: `koto session needed <PATH>` (`src/cli/session.rs`,
`src/session/needed.rs`, new).** Canonicalizes PATH, runs the inventory
inspection behind `list --execution-dir`, and for each running session builds
the local hierarchy Graph's successor chain and checks each later member's
`execution_dir` against PATH. Prints one line and exits 0, 1 or 2. A PATH
that can't be canonicalized is exit 2. Only sessions anchored at PATH or
below it count; one anchored above PATH (at the workspace root, say) doesn't
make PATH needed. It calls no command that pulls, spools or restores, so
nothing is written on either backend.

**dot-niwa: `.niwa/workspace.toml`.** Three tables:

```toml
[session.on_reap]
command = '''[ -n "$NIWA_REAP_KEY" ] || exit 0; command -v koto >/dev/null 2>&1 || exit 0
koto session anchor close --help >/dev/null 2>&1 || exit 0
exec koto session anchor close --key="$NIWA_REAP_KEY" --harness-session="$NIWA_SESSION_ID"'''
env = ["KOTO_HOME"]   # plus any name this workspace's koto needs to find its store

[session.dispatch_env]
passthrough = ["KOTO_DISPATCHER_ANCHOR"]
unset = ["KOTO_ANCHOR"]

# once niwa and koto releases carry the gate and the verb:
[session.before_remove]
command = '''command -v koto >/dev/null 2>&1 || exit 0
koto session needed --help >/dev/null 2>&1 || exit 0
exec koto session needed "$NIWA_REMOVE_PATH"'''
env = ["KOTO_HOME"]
```

Both commands also list, in their `env`, whatever names this workspace's koto
needs to find the same store its sessions use (for example `KOTO_HOME`), since
the commands get a fixed environment.

### Key Interfaces

- `[session.before_remove]`: `command` (string), `timeout` (duration, default
  `10s`), `env` (names). Environment: `PATH`, `HOME`, `NIWA_REMOVE_PATH`,
  `NIWA_REMOVE_STEP`, `NIWA_SESSION_IDS`, declared names. Exit 0 allows; else
  spares.
- `koto session needed <PATH>`: exit 0 not needed, 1 needed (one line, at
  most 200 bytes), 2 couldn't decide (one line).
- Launch record (`niwa.launch/v1`): `dispatch_id`, `session_id`, `agent`,
  `instance_name`, `instance_path`, `transcript_path`, `permission_mode`,
  `session_name`, `model`, `settings` (the JSON document passed), `env`
  (name to value), `written_at`.
- `SessionLifecycleState`: `claude_conversation_id` (now written),
  `claude_job_at_create` (new, omitempty).
- `niwa resume <instance-name | session-id | session-name> [prompt]`: prints
  the resumed session id and permission mode; exits non-zero naming the
  argument on every refusal.

### Data Flow

A reap at the top of `niwa dispatch` selects targets as today. For each, after
niwa's own re-asks, the gate runs the workspace's command with the path. In
this organization's workspace that is the guarded `koto session needed`,
which scans the sessions anchored under the path, follows each running one's
successors, and answers. A spare leaves the instance and its mapping; the line
goes to stderr. A later reap asks again, so the instance goes on the first
reap after its run finishes or moves.

At dispatch, after the mapping write, niwa writes the launch record. When the
instance is later destroyed, the record stays. `niwa resume` reads it,
restores the instance if needed, resumes the session with the recorded flags,
and writes a new mapping with the same dispatch id, so the next reap and
on-reap treat the worker as the same dispatch.

## Implementation Approach

### Phase 1: dot-niwa declares the shipped seams

Declare `[session.on_reap]` with koto's guarded anchor close and
`[session.dispatch_env]` passing `KOTO_DISPATCHER_ANCHOR` and unsetting
`KOTO_ANCHOR`. No niwa or koto change; niwa v0.36.0 and later read both.

Deliverables:
- `.niwa/workspace.toml` in dot-niwa, and a load check in its test workflow.

### Phase 2: the gate and its removal sites

The `[session.before_remove]` table, `removalGate`, the shared exec helpers,
and the calls in reap, backstop and watch discard.

Deliverables:
- `internal/config/session.go`, `internal/cli/removal_gate.go`, edits to
  `reap.go`, `watch.go`, `on_reap.go`; tests with a stub command.

### Phase 3: the worktree sweep

Record the session id and job flag at hook time; key the sweep on the job;
call the gate per worktree. Depends on Phase 2 for the gate call.

Deliverables:
- `session_from_hook_cmd.go`, `internal/worktree/session_lifecycle.go`,
  `reap_worktrees.go`; tests.

### Phase 4: destroy's preflight

The preflight, refusal and `--force` override for single and workspace
destroy. Depends on Phase 2.

Deliverables:
- `internal/cli/destroy_preflight.go`, edits to `destroy.go`; tests with the
  session-listing seam.

### Phase 5: launch records

Write a launch record at dispatch and drop it in the reap once the transcript
is gone. Independent of Phases 2 to 4.

Deliverables:
- `internal/workspace/launch_store.go`, edits to `dispatch.go` and `reap.go`.

### Phase 6: `niwa resume`

The verb, restore by name, the resume argv in `dispatch_reentry.go`, capture
and remapping. Depends on Phase 5. It starts by confirming the two
assumptions Decision 3 rests on, with a stub-free check against a real
`claude`: that `claude --bg --resume <id>` from the same directory keeps the
session id (or how it reports a new one), and that `Applier.Create` with an
explicit name restores the instance's trust grant.

Deliverables:
- `internal/cli/resume.go`, edits to `dispatch_reentry.go`,
  `instance_from_hook.go` (provision by name); tests with a stub `claude`.

### Phase 7: koto's verb over the local relation

`koto session needed` over the inventory read (running, terminal, cancelled,
unreadable, component-wise containment, exit codes, no writes) and the
successor walk through the continues relation on the local backend, which
koto#481 already ships. It starts with a short spike confirming the
hierarchy Graph can be built read-only for the sessions under one path
without loading retention judgement, and measuring one call's cost against
the gate's 10-second run budget.

Deliverables:
- `src/session/needed.rs`, the CLI entry, guide text, tests for chains,
  split successors, unanchored and unreadable successors, and the store-hash
  check on the local backend.

### Phase 8: koto's verb across hosts and imports

Judge successors that only the bucket knows about, and an imported copy
naming its source, once the continues relation's bucket back-reference and
import contract land. Until then a cloud session holds no relation, so the
verb keeps its path, which is the safe direction.

Deliverables:
- the cloud branch of the successor walk; tests against koto's test bucket,
  including the store-hash check.

### Phase 9: dot-niwa declares the gate, and the end-to-end check

Once niwa and koto releases carry Phases 2 and 7, declare
`[session.before_remove]` with the guarded verb, and verify the end-to-end
clauses in a scratch workspace on one host. It must not be declared on
Phase 2 alone or before Phase 7's successor walk: without the walk a
taken-over predecessor reads as running forever and its instance is never
reclaimed.

Deliverables:
- `.niwa/workspace.toml` in dot-niwa.

## Security Considerations

**A command that runs on every reap.** `[session.before_remove]` executes a
workspace-declared shell command, implicitly, at the top of `niwa dispatch`,
`niwa create` and `niwa watch`. It has the same trust level as the on-reap
command and the hooks a workspace configuration installs: whoever controls
the workspace configuration already controls commands niwa runs. The gate
adds no new principal. It runs the shell by absolute path, with stdin empty,
a fixed environment (no inherited variables beyond `PATH`, `HOME` and names
the declaration lists, which the load filters through the same credential
and reserved-prefix refusals as `[session.dispatch_env]`), its own process
group killed on timeout, and a 30-second ceiling per step. Its output is
truncated and control characters are replaced before niwa prints it, so a
command can't forge terminal escapes in niwa's output.

**Fail-safe direction.** Every failure spares. A hostile or broken command can
keep directories on disk, which costs space and is reported on every reap; it
can't make niwa delete anything niwa wouldn't have deleted without it.

**The path is data.** `NIWA_REMOVE_PATH` reaches the command as an
environment variable, never interpolated into the command string, and the
declared koto command quotes it. koto canonicalizes it and only reads.

**Launch records hold launch settings.** A record holds the settings document
and environment values dispatch passed to a worker. That document is already
built to ride the worker's command line, where other users of the host can
read it: it carries MCP approval names and an environment object whose names
`[session.dispatch_env]` filters for credentials at load. The difference is
lifetime: a process listing shows those values while the worker runs, and a
record keeps them on disk until the transcript goes. That is accepted because
the values are the ones a workspace chose to pass by name with credential
names refused, and the record is 0600 in a 0700 directory under the
workspace root, beside the session mappings. Recording names alone was
rejected: the values (a dispatcher's koto anchor, for one) are what put the
worker back in the posture it had, and the anchor a resumed worker gets is
the one it was dispatched under, which is the link it should keep. A record
grants nothing by itself: resuming still needs the transcript and the local
`claude` binary. `niwa destroy --workspace` removes the workspace root, and
the store with it.

**A record is input, not authority.** Anything that can write under the
workspace root could plant or edit a record, so `niwa resume` validates every
field on read (schema, ids, the permission mode against the agent's declared
set, the model and name against a plain-token shape, the settings document
against the closed key set dispatch emits so it can't smuggle hooks, a key
helper or permission rules, and environment names against the credential and
loader refusals), derives the instance and transcript paths instead of
reading them, opens record files without following symlinks and checks
their owner and mode, refuses a symlinked store, and runs the same machine
authority checks dispatch runs before it launches.

**Replaying a permission mode the current policy would no longer give.**
Resume uses the recorded mode even when the workspace's configuration has
since become stricter, and when the instance had to be restored that comes
with a fresh clone and a trust grant. Using the stricter of the two modes, or
requiring an explicit flag, was considered and set aside: niwa#369 asks
precisely that a resumed worker keep the posture it was dispatched with,
because the failure it reports is a worker that silently came back prompting
and stalled its coordinator. The mode was granted by whoever dispatched the
worker, the machine authority checks still bound it, and resume prints the
mode and, when it differs from what configuration gives now, says so before
launching. This is an accepted risk, not a mitigated one.

**Restoring trust.** `niwa resume` re-grants the Claude trust niwa withdrew,
only for the path derived above and only through the applier's own trust
step, so a record can't direct a trust grant at an arbitrary directory, and an
occupied path is refused rather than overwritten. The restored repositories
are cloned at their current remote state, so the trust extends to whatever
`.claude` settings those repositories now commit. That is what `niwa
dispatch` itself does (a fresh clone, a trust grant and the requested
permission mode, possibly from an agent), so resume adds no capability
dispatch doesn't have; it prints the commit each repository was restored at
so the operator can see what came back.

**A second implicit command.** `[session.before_remove]`, like
`[session.on_reap]`, is a workspace-level table (no per-repo override, merged
from an overlay the same way), so it can arrive through a workspace
configuration refresh and then run, unsandboxed and with the operator's
authority, at the top of every dispatch, create and watch. That is accepted:
it is the same trust boundary as every hook the configuration installs, and
whoever controls that configuration already controls commands niwa runs. The
gate captures the command's output instead of passing it through raw.

**The koto guards fail open on purpose.** dot-niwa's declaration exits 0 when
niwa's `PATH` has no koto or a koto without the verb, which is the PRD's R33:
a host without koto keeps today's behaviour. The cost is that a host where
koto runs for sessions but isn't on niwa's `PATH` gets no protection from the
gate. A second quiet failure has the same shape: if the declaration doesn't
pass the names koto needs to find this workspace's store, koto opens an empty
default store and answers 0. The end-to-end check runs with koto on niwa's
`PATH` and the store names passed, and it asserts the gate spares a known
held instance, so a misconfigured declaration fails that check rather than
passing silently. The verb never answers 0 for a store it can't list (exit
2). koto's cloud configuration belongs in its config file, as koto's guide
recommends, rather than in variables the declaration would have to pass
(credential-shaped names are refused there).

**When the gate itself is broken.** A gate that spares everything blocks
reaps and plain destroys. The way out is per instance: `niwa destroy --force
<name>`, which says what it overrides. `--workspace` keeps refusing until the
gate is fixed or those instances are gone. That is deliberate friction, not a
control: a loop of forced destroys gets the same result, but it names every
instance it overrides.

**Paths that skip the gate.** `niwa reset` and the rollback of an instance a
failed dispatch or watch just created don't ask the gate: reset recreates the
instance it removes, and a rollback removes a directory no session has used.
Both are accepted, and the caller-list test keeps any further caller from
skipping it unnoticed.

**A koto that is installed but broken.** The guard falls through when `koto
session needed --help` fails, which covers a koto too old to have the verb
and also a koto that is broken outright. Both read as "no koto" and keep
today's behaviour. Telling them apart in a shell guard isn't reliable across
koto versions, so this is accepted alongside the absent-koto case.

**Time of check.** There is a window between the gate's answer and the
removal, as there is today between the reaper's re-ask and the destroy. The
gate runs last, immediately before the removal, to keep it short.

**koto's verb is read-only.** It reads headers and links and writes nothing,
on either backend, which the test suite checks by hashing the store. It never
calls the commands that pull or spool (koto#436, koto#477).

## Consequences

### Positive

- A held worker keeps its instance and worktree for as long as its job is
  listed, and a waiting workflow run keeps its instance until it finishes or
  moves, with no person tracking which instances to protect.
- niwa's removals stay engine-free; any workflow engine can answer the gate.
- Destroy no longer pulls a directory from under a running session by
  default, and the refusal says why.
- A worker whose directory went comes back at the same path in its
  dispatched posture, without hand-made directories.
- dot-niwa's declarations close koto's agent-session anchors on reap and link
  dispatched workers to their coordinators.

### Negative

- Each removal candidate now costs a process start when the gate is declared,
  and a slow command can add up to 30 seconds per step to dispatch, create
  and watch.
- A broken gate keeps instances until someone fixes it or forces destroys.
- Launch records are a new store with its own retention to get right.
- Under the cloud backend, until the relation's bucket back-reference ships,
  a predecessor's instance is kept until the predecessor finishes, even when a
  successor has taken over.

### Mitigations

- The gate's budget is bounded and sequential, and with nothing declared it
  costs nothing.
- Every spare is reported on every reap with the command's reason, so a broken
  gate is visible the first time it matters.
- The launch store mirrors the lineage store's validated-id, atomic-write
  pattern, and the reap removes a record once its transcript is gone.
- The cloud limitation errs toward keeping, which is the safe direction, and
  closes when the relation's bucket half lands.
