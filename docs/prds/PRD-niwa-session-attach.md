---
status: Done
source_issue: 117
problem: |
  When a worker agent in a niwa mesh session hits an interesting edge case —
  abandons mid-task, makes a questionable decision, hits a constraint, or
  stalls — the workspace coordinator's only recovery options today are to let
  the worker complete on its own, send a one-way `niwa_send_message`, or
  destroy the session and restart from scratch. There is no human-in-the-loop
  primitive that lets an operator step into a session, see what the agent has
  done, prompt it interactively, fix things manually, and hand it back
  without losing context or fighting the mesh for control.
goals: |
  Operators can attach to any active session in their workspace instance,
  resume Claude Code with the worker's full transcript history, work
  interactively, and detach cleanly with the session returning to normal mesh
  operation. Discovery, lock acquisition, daemon coordination, and transcript
  loading happen end-to-end through a single `niwa session attach <id>`
  command. Operators can break stale locks via `niwa session detach <id>
  --force`. The lock state is visible to coordinators (via filesystem-readable
  state) so the mesh naturally backs off without push notifications or new
  MCP tools.
---

# PRD: niwa session attach

## Status

Done

## Glossary

Terms used throughout this PRD with their precise meaning in the niwa context.

| Term | Definition |
|------|-----------|
| **active session** | `SessionLifecycleState` whose `status` field equals `active` (worktree exists, not destroyed) |
| **attach lock** | Exclusive `flock(2)` on `<worktree>/.niwa/attach.lock` held by the foreground `niwa session attach` process |
| **attach sentinel** | JSON file at `<worktree>/.niwa/attach.state` recording lock-holder metadata (visibility surface for `session list`) |
| **catch-up replay** | The per-worktree daemon's `scanExistingInboxes` pass on startup that drains any envelopes that arrived while the daemon was not running |
| **conv_id** | Lowercase UUID v4 captured by niwa as `claude_conversation_id` after the worker's first task exits; passed to `claude --resume` |
| **dead PID** | A `(pid, start_time)` tuple where `IsPIDAlive` returns false (process gone, or PID recycled by a different process) |
| **envelope** | A queued task message in the per-worktree daemon's inbox (`<worktree>/.niwa/roles/<repo>/inbox/`) |
| **fresh start** | The state where Claude Code launches with no transcript history (no `--resume`); attach must NEVER produce this silently |
| **operator** | The human running `niwa` locally; same as "workspace coordinator" in user stories |
| **stale lock** | An attach sentinel whose recorded `owner_pid` is dead per `IsPIDAlive`; the kernel has already released the flock |
| **terminal task state** | A `state.json.State` value of `complete`, `failed`, `cancelled`, or `abandoned` (the existing taskstore terminal vocabulary) |
| **transcript file** | The worker's Claude Code conversation history at `~/.claude/projects/<encoded_cwd>/<conv_id>.jsonl` |
| **encoded_cwd** | The worker's CWD with every non-`[A-Za-z0-9]` character replaced by `-`, leading `/` becoming a leading `-`. Empirically verified against claude v2.1.138. |

## Problem Statement

A workspace coordinator runs multi-step work through the niwa mesh. Today,
when a worker hits an interesting edge case — drifts off-task, makes a
questionable design call, stalls waiting for input it cannot get, or runs
into a constraint that requires human judgement — the coordinator has three
options:

1. Let the worker complete on its own. Wastes time when the trajectory is
   already wrong.
2. Send `niwa_send_message`. One-way and unacknowledged; the worker may not
   read it, may misinterpret it, and there is no way for the coordinator to
   confirm the prompt landed.
3. Destroy the session and restart. Discards everything the worker already
   produced — including unpushed commits, in-flight design decisions, and
   the conversation context that may represent significant work.

There is no fourth option that lets a human take the conversation over,
inspect what the agent has done, drive it interactively for a few turns,
and hand it back. This forces a binary choice between trusting an agent
that's already off-track and discarding accumulated context.

The underlying mechanism for the fourth option already exists. niwa
captures `claude_conversation_id` after the worker's first task exits and
re-uses it via `claude --resume` for every subsequent worker spawn in the
session. The same `--resume` mechanic, invoked by a human in the worktree,
would land them in Claude Code with the worker's full transcript loaded.
What's missing is the orchestration: lock acquisition so the daemon stops
spawning workers while the human is in the session, validation that the
transcript is loadable, the discovery surface to find the right session,
and the operator-friendly UX around it all.

## Goals

1. **Human-in-the-loop primitive**. An operator can step into any active
   session, see the worker's full conversation history, prompt the agent
   interactively or fix things manually, and exit cleanly — without
   destroying state.

2. **Mesh-safe by construction**. While an operator is attached, the mesh
   does not spawn workers in that session. When the operator detaches, the
   mesh resumes naturally and processes any envelopes that queued during
   attach.

3. **Recoverable from failures**. Stale locks (SSH disconnect, terminal
   crash, host reboot) are detectable from the next `session list` poll
   and breakable via an explicit `detach --force` command. No support
   intervention or filesystem surgery required.

4. **Visible without polling fatigue**. The attach state appears as a
   first-class column on `niwa session list` and a first-class field on
   `niwa_list_sessions` — coordinators see "this session is held by a
   human" without inventing new RPC surfaces or push channels.

5. **Independent shipping**. The feature does not depend on fixes to
   issues #108, #109, #111, or #112. It interoperates with PR #115's
   mesh-reliability work via additive schema fields rather than colliding
   with it.

## User Stories

The primary persona is the **workspace coordinator** (a human running niwa
locally, driving multi-step delegation through the mesh). The secondary
persona is the **on-call engineer who inherits a workspace from a
colleague** — semantically conflated with the coordinator in this PRD,
but called out so future scenarios (attaching to a session you didn't
create; reading the worker's transcript before deciding to take over)
remain in scope for the design.

1. **As a workspace coordinator running a long-form mesh task**, I want
   to step into the session, see what the agent has done so far, and
   prompt it interactively for a course correction, so that I can recover
   from edge cases without destroying state.

2. **As a workspace coordinator pair-debugging a stuck worker**, I want to
   discover the session by ID and attach to it, so that I can run a few
   exploratory `git status` and tool calls in the same conversation
   thread the agent has been in.

3. **As a workspace coordinator with an SSH session that disconnected
   mid-attach**, I want a single command that breaks the stale lock so
   the mesh can resume, so that I don't have to manually delete lock
   files or restart the daemon.

4. **As a workspace coordinator who attempts to attach to a session that
   was destroyed**, I want a clear refusal with an explanation, so that
   I understand the worktree is gone and can take a different action
   instead of guessing why the command failed.

5. **As a workspace coordinator who runs `niwa session list` while a
   colleague is attached to one of the sessions**, I want to see the
   attach state in the listing, so that I can avoid running mesh
   delegations that would queue indefinitely.

## Critical User Journeys

The user stories say what the operator wants. These CUJs show *what
they actually do* — including the discovery moves, the in-the-moment
thinking, and the failure paths. A common thread runs through every
journey: the coordinator is a router, and attach is the human's tool
for bypassing the router when they need a direct conversation with a
specific delegated agent. Without attach, every operator-to-agent
exchange goes through the coordinator and loses fidelity. With
attach, the human can step into the actual conversation thread.

### CUJ-1: Pre-merge review across delegated PRs in multiple repos

**Context.** The operator asked the coordinator to implement a
feature from a roadmap. The coordinator delegated to three sessions
across three repos and reports back: "Done — PRs are #422 (payments),
#118 (sdk), and #91 (docs). Each was implemented by its own
delegated agent in the corresponding session."

**Goal.** "Before I approve any of these, I want to ask each of the
delegated agents real review questions about THEIR PR — not have
those questions paraphrased by the coordinator and re-prompted to
the agent. I want the equivalent of a code review with the actual
author."

**Walkthrough.**
1. Operator runs `niwa session list --status active` and spots the
   three session IDs the coordinator named, with `AVAILABILITY=available`
   (the agents are between tasks).
2. Opens a new terminal pane, runs `niwa session attach payments-feat-m4k1`.
   The attach lands at the end of the last conversation turn between
   the coordinator and the agent — the agent's last message is its
   "PR ready for review" announcement.
3. Pastes review notes inline: "Why `customerCharge` not
   `chargeRequest` like our `RefundRequest`/`VoidRequest` pattern?"
   The agent explains it picked `Charge` to match the Stripe adapter
   naming. The operator accepts the rationale and moves on.
4. Asks the next question: "What happens if `amount` is zero?" The
   agent says Stripe rejects it. Operator pushes back: "Reject
   earlier with `ErrInvalidAmount` from `payments/errors.go`." Agent
   commits the fix, pushes the fixup, reports the SHA inline.
5. Types `/exit`. Repeats for the SDK and docs sessions in turn,
   each from a separate terminal pane.
6. Returns to the coordinator pane: "Reviewed all three. payments
   pushed two fixups, sdk is clean, docs needs a header rename
   tracked as a follow-up. Approve and merge."

**Success.** Review converges in three short attach sessions instead
of three multi-round coordinator-mediated exchanges per PR. The
operator interacted with the agent that wrote each PR; nuance and
intent were preserved.

**What goes wrong.** One of the sessions has been compacted since
the PR was published. The operator notices the agent's recall of
its choices feels reconstructed, not lived. They read the actual
commits via `git log -p` to verify the reasoning, using the agent's
narrative as a map.

### CUJ-2: Unblock a stuck delegated agent without telephone-game

**Context.** The operator is mid-conversation with the coordinator
on a refactor. The coordinator reports: "Session `auth-rewrite-9q2x`
has been spinning on the import path for `pkg/auth/jwt` — the agent
isn't finding the existing helpers in `internal/authz`. It's about
to start writing them from scratch."

**Goal.** "Get the missing context to that delegated agent now,
without playing telephone through the coordinator. Coordinator will
paraphrase, the agent will guess at intent, and we'll burn another
round trip while the wrong solution gets written."

**Walkthrough.**
1. Operator opens a new terminal pane. Runs `niwa session attach
   auth-rewrite-9q2x`. Lands at the end of the last conversation
   turn — sees the agent listing what it can't find.
2. Types directly: "Look in `internal/authz/policy.go`. The helpers
   you need are `IssueJWT` and `VerifyJWT`. Don't reimplement them
   — import `internal/authz` and call those. While you're at it,
   `internal/authz/keys.go` has the rotation helpers if you need
   them for refresh tokens."
3. Watches the agent acknowledge, run `grep` to confirm, then
   continue with the right import. Types `/exit`.
4. Back in the coordinator pane: "I gave `auth-rewrite-9q2x` the
   missing import context directly. It's unblocked. Continue."

**Success.** The delegated agent gets unstuck in 90 seconds instead
of three coordinator round trips. Intent is preserved because the
operator typed the context themselves. The session's transcript
records exactly what the operator said.

**What goes wrong.** The operator forgets to tell the coordinator
they intervened. Next time the coordinator delegates a similar task
to the same session, it doesn't know the agent now has the
`internal/authz` context primed. Mitigation: develop the habit of
narrating attach interventions back to the coordinator on detach.

### CUJ-3: Override a coordinator's in-flight decision

**Context.** The coordinator just announced: "I've delegated the
package layout decision to `auth-refactor-7k2m`. The agent is
deciding between a nested `pkg/auth/{jwt,session,oauth}` layout and
a flat `pkg/auth_jwt`, `pkg/auth_session`. I'll report back in a
few minutes." Two seconds later, the operator realizes the team
standardized on the flat layout in the billing repo last quarter
— the agent doesn't have that context.

**Goal.** "Get the directive to the delegated agent now, before it
spends 4 minutes weighing options and picks the wrong one. I don't
want to wait for the coordinator round trip — that wastes the
agent's tool calls and adds latency."

**Walkthrough.**
1. Operator opens a new terminal. Pulls the session ID from the
   coordinator's last message. Runs `niwa session attach
   auth-refactor-7k2m`. Transcript scrolls in — agent is mid-
   evaluation, listing pros and cons.
2. Types: "Stop the comparison. Use the flat layout —
   `pkg/auth_jwt`, `pkg/auth_session`, `pkg/auth_oauth`. We
   standardized on that in the billing repo last October. Proceed."
3. Watches the agent acknowledge and switch direction. Types
   `/exit`.
4. Returns to the coordinator: "I redirected `auth-refactor-7k2m`
   to use the flat layout. Adjust the other two sessions' plans
   accordingly."

**Success.** The wrong layout never gets generated. The operator
saved the round-trip latency and the coordinator's plan stays
coherent.

**What goes wrong.** The agent had already started writing files
for the nested layout before the operator attached. The operator
re-attaches after detach to ask "show me `git status` — did you
remove the nested directories?" — and either confirms the rollback
or directs it.

### CUJ-4: Inject tribal knowledge the coordinator never had

**Context.** Coordinator delegated a database migration task to
session `db-migrate-x9p3`. The agent is generating a migration that
adds an index on `users.email`. The operator knows — from a
postmortem two years ago that nobody documented — that this exact
index caused a 40-minute lock on production because the table is
80M rows. The coordinator doesn't know this. The operator's
CLAUDE.md doesn't mention it. The agent is about to ship a
migration that will page someone at 3am.

**Goal.** "Hand the delegated agent the missing context, have it
persist for the rest of the session, without writing a doc, updating
CLAUDE.md, or routing the warning through the coordinator who would
paraphrase it."

**Walkthrough.**
1. Operator runs `niwa session attach db-migrate-x9p3`. Lands at
   the end of the last turn — the agent just proposed
   `CREATE INDEX idx_users_email ON users(email)`.
2. Types: "Stop. Before you ship any index migration on `users`:
   this table is 80M rows on prod, MySQL 5.7, and standard
   `CREATE INDEX` takes a write lock for ~40 minutes. We learned
   this the hard way in 2024. Use `pt-online-schema-change` or
   split it into a shadow column + backfill + swap. Apply this
   rule to any other large-table migration in this session, not
   just this one."
3. Agent acknowledges and regenerates with `pt-online-schema-change`.
   Operator skims the diff inline, confirms it's correct, types
   `/exit`.
4. Back in the coordinator: "I gave `db-migrate-x9p3` the prod-
   locking context for large-table indexes. Relay the same context
   to the other migration sessions."

**Success.** The migration is safe before it ever surfaces in a PR.
The agent retains the knowledge for the rest of the session, so
subsequent delegated tasks ("add an index on `users.created_at`")
inherit the same caution.

**What goes wrong.** The operator forgets to relay the context to
the coordinator. The next session repeats the mistake. Mitigation:
the worktree-state warning loop on detach is a good prompt to
narrate back; the doc guide includes "always summarize back to the
coordinator" as recommended hygiene.

### CUJ-5: Morning triage of an overnight delegation fan-out

**Context.** Yesterday at 6pm the operator asked the coordinator to
"spike three migration strategies in parallel — one per branch —
and report back." Coordinator spawned `spike-pgvector`,
`spike-typesense`, and `spike-meilisearch`. The operator closed the
laptop. It's now 8am the next day. `niwa session list --status
active` shows all three with `AVAILABILITY=available`, each with
hours of transcript.

**Goal.** "I have a 9am meeting where I need to pick one. I don't
want to read three multi-hour transcripts. I want to attach to
each, ask 'one paragraph: what was the bottom line?', and rank
them."

**Walkthrough.**
1. `niwa session list --status active` — confirms all three are
   `available` (no active workers), sorted by attach state then
   creation time. Notes the AVAILABILITY column shows all three
   as free; no stale sentinels to worry about.
2. `niwa session attach spike-pgvector`. Asks: "One paragraph: did
   it work, what was the bottleneck, what would the production
   migration look like?" Agent answers from its own context — p99
   recall latency was the killer. Detach.
3. `niwa session attach spike-typesense`. Same prompt verbatim.
   Agent reports cleaner integration but licensing friction.
   Detach.
4. `niwa session attach spike-meilisearch`. Same prompt. Agent
   flags a corrupted index it never recovered from — the spike is
   half-done. Operator asks a follow-up: "If I gave you another
   hour, could you finish?" Agent says yes, lists what it needs.
   Detach.
5. Returns to the coordinator: "Kill the meilisearch spike, archive
   typesense, keep pgvector and have it draft the migration plan."

**Success.** In ~15 minutes the operator has a decision-ready
summary from three sessions, written by the agents that lived
through the work, not reconstructed from logs.

**What goes wrong.** One of the sessions has compacted overnight.
The agent's "bottom line" summary feels paraphrased, not lived.
The AVAILABILITY column shows nothing about compaction state — the
operator must scroll the transcript to verify they're getting the
agent's original reasoning, not a summary of a summary.

### CUJ-6: Forensic peek at an abandoned session

**Context.** Coordinator reports overnight results: "Two sessions
shipped PRs successfully. The third, `payments-refactor-3p4q`,
abandoned its task — it logged that it hit a contradiction in the
requirements and stopped. I haven't destroyed it yet."

**Goal.** "Before I either restart the work or accept the
abandonment, I want to read what the agent saw. The coordinator's
summary won't tell me which specific requirement contradicted which.
I need to see the agent's reasoning at the moment it stopped."

**Walkthrough.**
1. `niwa session list --status active` — confirms
   `payments-refactor-3p4q` is still `AVAILABILITY=available` and
   the worktree exists. Good; nothing's been destroyed yet.
2. `niwa session attach payments-refactor-3p4q`. Scrolls the
   transcript to the last assistant turn. The agent had been asked
   to implement a 3-tier discount calculation; mid-task it found
   that the requirements doc specified both "discounts compound
   multiplicatively" and "the maximum total discount is 30%". The
   agent computed an example showing the two rules disagree at
   ~30% and chose to abandon rather than guess.
3. Operator agrees this was the right call. Asks: "Which sample
   inputs did you run through both rules to find the contradiction?"
   Agent walks through. Operator captures the test cases into
   `docs/decisions/discount-contradiction.md` for the next
   delegation.
4. Detach. Tells the coordinator: "The abandon was correct. I've
   captured the contradiction in a decision doc. Destroy
   `payments-refactor-3p4q` and re-delegate once the requirements
   are reconciled."

**Success.** The operator preserved the agent's reasoning before
the session was destroyed. The decision doc means the next
delegation won't repeat the discovery.

**What goes wrong.** The session's transcript has been compacted
since the abandon, and the agent's reasoning at the moment of
abandonment is now a summary, not the original chain. The operator
must reconstruct from commit messages or the daemon log. This
informs a v2 nudge: a "transcript snapshot at terminal-state"
feature would prevent the loss.

## Timing and Limits

A single source of truth for cadence and timing constants used throughout
this PRD. Implementers must use these values; reviewers can verify them
in one place.

| Constant | Value | Used in | Notes |
|----------|-------|---------|-------|
| Acquire-to-launch latency budget | ≤ 5s on `linux/amd64` with NVMe-class storage and 4+ cores, idle worktree | R21 | Measured with `NIWA_DESTROY_GRACE_SECONDS=0` and no live worker. With a worker present and default grace, budget is `5s + grace`. |
| Daemon-terminate grace period | 5s default; configurable via `NIWA_DESTROY_GRACE_SECONDS` env var | R6 | Existing constant; reused. |
| Worker-wait poll interval (no `--force`) | 1s | R6 | Reads task `state.json` files; bounded by Ctrl-C from the operator. |
| Wait-message print cadence | Every 5s while the worker is alive | R6 / AC14 | Prints `niwa: waiting for worker on task <task_id>...` to stderr. |
| Daemon respawn deadline on detach | ≤ 5s after the lock is released | R5 | Verifiable via `daemon.pid` file existence and `IsPIDAlive`. |
| Stale-lock reap interval | On every read (lazy; no background process) | R19 | Performed opportunistically by `niwa session list`, `niwa session attach`, `niwa session detach`. |

## Exit Code Mapping

Single source of truth for `niwa session attach` and `niwa session detach`
exit codes. Each named error condition maps to exactly one code.

| Code | Meaning | Triggered by |
|------|---------|--------------|
| 0 | Success | Clean exit (Claude Code exited 0; no warnings) |
| 1 | Pre-flight validation failure | R2 (status != active), R4 case A/B/C (transcript validation), AC5 (session not found), AC28 (UID mismatch) |
| 2 | Usage error | R10 (`niwa session detach` without session_id), unknown flags, malformed session_id |
| 3 | Lock contention | R3 (attach lock held by live process) |
| 4 | Force operation killed live holder | R9 force-detach when holder is alive (operator may want to know they SIGTERM'd somebody) |
| 1-125 | Propagated from Claude Code | R7: Claude exit codes 1-125 pass through; codes ≥ 126 are clamped to 125 to avoid shell-reserved codes |

## Requirements

### Functional

**R1.** `niwa session attach <session_id>` shall acquire an exclusive
`flock(2)` on `<worktree>/.niwa/attach.lock`, terminate the session's
per-worktree daemon, validate the transcript per R4, change directory
to the worker's CWD (`<worktree>/<repo_name>` per `resolveRoleCWD`), and
exec `claude --resume <claude_conversation_id>` as a child process. The
foreground `niwa session attach` process holds the flock for the
lifetime of the Claude child.

**R2.** `niwa session attach` shall reject the operation if the
session's `status` is anything other than `active`. The error shall
name the current status, name a recovery action, and exit with code 1.
Specifically: `niwa: error: session <id> has status <status>; attach
requires status active. (For ended sessions, the worktree was removed
on destroy; create a new session instead.)`

**R3.** `niwa session attach` shall reject the operation if a non-stale
attach lock is already held by another process. The error shall name
the holder's PID, the start timestamp in RFC3339, and the recovery
command, and shall exit with code 3. Specifically: `niwa: error:
session <id> is already attached (pid=<int>, started=<RFC3339>). Run
\`niwa session detach <id> --force\` to break the lock if the holder
is gone.`

**R4.** `niwa session attach` shall pre-flight validate the transcript
file before launching Claude Code, with three distinct error strings,
each exiting code 1:

- **Case A (no captured conversation id):** `claude_conversation_id`
  in the lifecycle state file is empty.
  Error: `niwa: error: session <id> has no captured claude
  conversation id (the worker may have crashed before MCP server
  startup; inspect with \`niwa session list --status active\` or remove with
  \`niwa session destroy <id>\`).`

- **Case B (transcript file missing):** the deterministic transcript
  path does not exist.
  Error: `niwa: error: claude transcript missing for session <id>
  (expected: ~/.claude/projects/<encoded>/<conv_id>.jsonl). Claude
  may have purged the transcript or the worktree was moved. Start a
  fresh session with \`niwa session create\` or remove with \`niwa
  session destroy <id>\`.`

- **Case C (transcript file empty):** the transcript file exists but
  has zero bytes.
  Error: `niwa: error: claude transcript is empty for session <id>
  (path: ~/.claude/projects/<encoded>/<conv_id>.jsonl). The transcript
  was started but no records were written. Start a fresh session with
  \`niwa session create\`.`

The deterministic path is computed as:
`~/.claude/projects/<encoded_cwd>/<conv_id>.jsonl` where `encoded_cwd`
is defined in the Glossary.

**R5.** `niwa session attach` shall hold the lock for the lifetime of
the launched Claude Code process. When Claude Code exits, the lock
shall be released, the per-worktree daemon shall be respawned via
`EnsureDaemonRunning` within 5 seconds (per Timing and Limits), and
any envelopes that accumulated in the inbox during the attach shall
be processed by the respawned daemon's catch-up replay
(`scanExistingInboxes`).

**R6.** `niwa session attach --force` shall, if a worker is currently
running in the session, send SIGTERM to the worker's process group,
wait the daemon-terminate grace period from Timing and Limits (5s
default; `NIWA_DESTROY_GRACE_SECONDS` override), and SIGKILL if still
alive. Without `--force`, attach shall poll every 1s (per Timing and
Limits) until any running worker reaches a terminal task state (per
Glossary), printing `niwa: waiting for worker on task <task_id>...`
to stderr every 5s. A SIGINT (Ctrl-C) during the wait shall abort
the attach attempt cleanly without acquiring the lock and without
disturbing the worker.

**R7.** `niwa session attach` and `niwa session detach` shall use exit
codes per the Exit Code Mapping table.

**R8.** `niwa session detach <session_id>` shall release the attach
lock if it is held by a dead process (auto-recovery; no `--force`
required). It shall exit 0 silently if no lock is held.

**R9.** `niwa session detach <session_id> --force` shall release the
attach lock unconditionally, including by sending SIGTERM to a live
holder process (waiting the grace period, then SIGKILL if necessary).
When the holder was alive at the time of force, the command shall
exit code 4 (per Exit Code Mapping) so scripts can distinguish
"reaped a stale lock" from "killed an active operator's session".

**R10.** `niwa session detach` (without a session_id) shall print a
usage error explaining that detach is an explicit operator command
and that normal release happens automatically on Claude Code exit.
Exits code 2. Specifically: `niwa: usage: niwa session detach
<session_id> [--force]. Normal attach release happens automatically
when claude code exits; this command exists to break stale locks.`

**R11.** `SessionLifecycleState` shall gain a nested `attach`
sub-object with the on-disk shape:

```json
"attach": {
  "owner_pid": 12345,
  "owner_start_time": 9876543210,
  "started_at": "2026-05-10T14:32:11Z",
  "lock_path": ".niwa/attach.lock"
}
```

The sub-object is computed at read time from
`<worktree>/.niwa/attach.state` (the sentinel) and projected into the
lifecycle response — it is NOT persisted into
`<instance>/.niwa/sessions/<sid>.json`. This mirrors PR #115's
`daemon` sub-object pattern (also computed, not persisted, on the
lifecycle file). No `SessionLifecycleState.V` bump (additive
projection field under existing V:1 readers).

**R12.** `niwa_list_sessions` MCP tool shall include the `attach`
sub-object in its return shape, computed at query time from the
on-disk sentinel file. The sub-object key shall be **omitted** from
the JSON response (not present, not `null`) when no lock is held.
The MCP tool shall also accept the new optional input parameters
`attached: bool` and `available: bool` matching the CLI flag
behaviour from R16.

**R13.** `niwa_destroy_session` MCP tool shall return a structured
error with `error_code: "SESSION_ATTACHED"` when destroy is attempted
on an attached session and `force: false`. The error message shall
contain the literal substring `niwa session detach --force` and the
literal `pid=<int>` of the holder. The error response is a
machine-readable refusal, not a session destruction.

**R14.** `niwa session list` (no flags) shall default to the lifecycle
view. The deprecated `mesh list` alias path on `niwa session list`
shall be removed entirely (not just demoted with a warning). Users
wanting the coordinator process registry shall call `niwa mesh list`
directly. `niwa mesh list` itself shall continue to work unchanged.

**R15.** `niwa session list` shall add an `AVAILABILITY` column with
values `available`, `attached`, `stale`. The column appears between
`STATUS` and `CREATED`. The `stale` value indicates the sentinel
file exists but the recorded `owner_pid` is dead per `IsPIDAlive`
(the kernel has already released the flock).

**R16.** `niwa session list` shall support `--attached` and
`--available` filter flags, AND-combined with the existing `--repo`
and `--status` filters. `--attached` includes only sessions whose
`AVAILABILITY` is `attached`; `--available` includes only sessions
whose `AVAILABILITY` is `available`. Sessions in the `stale` state
appear under neither filter (operators must run `niwa session list`
without filters to see them).

**R17.** `niwa session list` default sort order shall be: attached
sessions first (descending by `started_at` from the sentinel), then
by `status` (active before terminal), then by `creation_time`
descending.

**R18.** Lock acquisition shall use a non-blocking exclusive `flock(2)`
on `<worktree>/.niwa/attach.lock` with implicit release via
file-descriptor lifetime. The lock file shall be created with mode
`0600`. A sibling sentinel JSON file at `<worktree>/.niwa/attach.state`
shall record `{ owner_pid, owner_start_time, started_at, lock_path }`
with mode `0600`, written via the existing atomic tmp+rename pattern.
On clean exit (Claude Code returned), the niwa-attach process shall
remove the sentinel file before exiting (so the AVAILABILITY column
shows `available` immediately). If the niwa-attach process is killed
without running its exit handler, the sentinel file persists and is
detected as `stale` by readers per R19.

**R19.** Stale-lock detection shall use the existing `IsPIDAlive`
helper (PID + start_time defends against PID recycling). A sentinel
file whose `owner_pid` is dead shall be treated as released by all
three readers: `niwa session list` renders it as `AVAILABILITY=stale`
and may opportunistically delete the sentinel; `niwa session attach`
treats the slot as free and acquires; `niwa session detach` deletes
the sentinel and exits 0. Any of the three opportunistic reaps is a
best-effort delete; failure does not propagate.

**R20.** Worktree state on detach: if the worktree has uncommitted
changes, untracked files, or unpushed commits on the session branch
on the natural-detach release path (Claude Code exited), niwa shall
print a warning to stderr labelled `warning: worktree has <kind>` for
each kind found, where `<kind>` is one of `uncommitted changes`,
`untracked files`, `unpushed commits on session/<id>`. The warning
includes the corresponding `git status --porcelain` or
`git for-each-ref` line(s). The warnings are printed but do not
abort, prompt, or auto-clean the worktree. The explicit
`niwa session detach <id>` command (operator escape hatch) does NOT
run this check — it owns no claude exit semantics.

### Non-Functional

**R21.** The acquire-to-launch latency budget is ≤ 5 seconds, measured
on a reference environment of `linux/amd64` with NVMe-class storage,
4+ CPU cores, and `NIWA_DESTROY_GRACE_SECONDS=0` (so the grace period
does not bind the budget). With a live worker and default grace
period, the effective budget is `5s + NIWA_DESTROY_GRACE_SECONDS`.

**R22.** No new external dependencies. Lock primitive uses
`syscall.Flock` (already in the codebase). Liveness uses `IsPIDAlive`
(already in the codebase). Daemon orchestration uses
`TerminateDaemon` and `EnsureDaemonRunning` (already in the
codebase).

**R23.** No schema-version bump on `SessionLifecycleState`. Additive
projection field only (the `attach` sub-object is computed at read
time, not persisted), with zero-value handling matching the
contributor note in `docs/guides/sessions.md`. Coordinated with
PR #115 to land both features under V:1 cleanly.

**R24.** Single-user-per-instance assumption is declared by reference
to `DESIGN-cross-session-communication.md`'s "same-UID cooperative
trust" boundary. No cross-UID safeguards beyond the existing 0600
file permissions are added in this PRD.

**R25.** Linux is the supported platform for the precise stale-lock
detection (PID start-time comparison reads `/proc/<pid>/stat`, which
is Linux-only). On non-Linux platforms (macOS, BSD), niwa shall fall
back to the conservative signal-0 liveness check (existing behaviour
in `internal/mcp/liveness.go:31-33`). Attach functionality shall
remain operational on non-Linux but with a documented limitation:
PID-recycling false-positives are possible in long-lived workspaces.

**R26.** Cross-UID error handling: `niwa session attach <id>` invoked
by a different UID than the workspace owner shall surface a clear
error to the operator. The implementation MAY perform an explicit
`os.Geteuid()` check before any state read, OR rely on the EACCES
that the existing 0600 permissions produce on first state read and
re-frame it. Either implementation is acceptable provided the
user-visible error is `niwa: error: cannot attach to session owned by
another user (file owner uid=<int>, your uid=<int>)`. This is an
error-message quality requirement only, NOT a multi-user safeguard
(which R24 declares out of scope).

## Acceptance Criteria

### Happy Path

- [ ] AC1. `niwa session attach <id>` on a healthy active session: the
  launched child process's `argv` equals
  `["claude", "--resume", "<conv_id>"]` and its working directory
  equals the worktree path resolved by `resolveRoleCWD`. `pgrep` of
  the per-worktree daemon's PID returns no result during attach.
- [ ] AC2. The launched Claude Code's transcript-loading is verified
  by inspecting that the transcript file
  `~/.claude/projects/<encoded_cwd>/<conv_id>.jsonl` is open by the
  Claude child PID (e.g., via `lsof -p <pid> | grep .jsonl`).
- [ ] AC3. After Claude Code exits cleanly (exit 0), the lock file
  is released within 1s, the sentinel file is removed, the
  per-worktree daemon is respawned within 5s (deadline per Timing
  and Limits), and `niwa session list` shows `AVAILABILITY=available`
  for the session.
- [ ] AC4a. After detach, `niwa_delegate(session_id=<id>, ...)` for
  a NEW envelope succeeds and the daemon picks up the new envelope
  within 5s.
- [ ] AC4b. Catch-up replay: an envelope written directly to the
  inbox while the lock was held (simulating a delegate during
  attach) is processed by the respawned daemon within 5s of detach.

### Pre-Attach Validation

- [ ] AC5. `niwa session attach <id>` against a non-existent session
  ID exits 1 with stderr exactly `niwa: error: session <id> not
  found`.
- [ ] AC6. `niwa session attach <id>` against an `ended` session
  exits 1 with stderr matching the R2 error format and naming
  status `ended`.
- [ ] AC6b. `niwa session attach <id>` against an `abandoned` session
  exits 1 with stderr matching the R2 error format and naming
  status `abandoned` (note: this state has no writer today; the AC
  guards against future regressions).
- [ ] AC7. `niwa session attach <id>` where
  `claude_conversation_id` is empty exits 1 with stderr matching the
  R4 case-A error string verbatim.
- [ ] AC8. `niwa session attach <id>` where the transcript file is
  missing exits 1 with stderr matching the R4 case-B error string
  verbatim and including the expected path.
- [ ] AC9. `niwa session attach <id>` where the transcript file is
  zero bytes exits 1 with stderr matching the R4 case-C error string
  verbatim and including the path.

### Lock Contention

- [ ] AC10. A second `niwa session attach <id>` while the first is
  still attached exits 3 with stderr matching the R3 error format
  exactly: contains the literal `pid=<int>`, the literal
  `started=<RFC3339 timestamp>`, and the literal substring
  `niwa session detach <id> --force`.
- [ ] AC11. `niwa session attach <id>` after the previous attach
  died cleanly (process gone, the niwa-attach exit handler removed
  the sentinel) succeeds without manual intervention. The lock and
  sentinel are absent from disk.
- [ ] AC12. `niwa session attach <id>` when the previous holder
  process was killed without running its exit handler (sentinel
  remains, owner PID is dead) succeeds, treating the stale sentinel
  as released. The new attach overwrites the sentinel with its own
  metadata.
- [ ] AC12b. Three concurrent attach attempts: the first wins, the
  second and third both exit 3. No FIFO queue (per Known
  Limitation 4).
- [ ] AC12c. `niwa session list` rendering a stale lock shows
  `AVAILABILITY=stale` and (best-effort) deletes the stale sentinel
  on the same call.

### Force Behaviour

- [ ] AC13. `niwa session attach <id> --force` against a session with
  a running worker SIGTERMs the worker's process group, waits the
  default grace period (5s), and proceeds to acquire the lock. After
  the grace, if the worker is still alive, SIGKILL is sent and the
  acquire proceeds.
- [ ] AC13b. With `NIWA_DESTROY_GRACE_SECONDS=1`,
  `niwa session attach --force` against a worker that ignores
  SIGTERM escalates to SIGKILL within ~1s (within ±0.5s tolerance
  for test stability).
- [ ] AC14. `niwa session attach <id>` (without --force) against a
  session with a running worker waits for the worker to complete
  naturally before acquiring the lock. Stderr emits the literal
  `niwa: waiting for worker on task <task_id>...` (with the actual
  task_id substituted) at least once per 5 seconds while the worker
  is alive.
- [ ] AC14b. SIGINT (Ctrl-C) during the wait aborts the attach
  attempt: niwa exits non-zero (code 130 by Go convention, OR a
  niwa-defined "aborted" code if defined), the lock is not held,
  and the worker is not signalled.
- [ ] AC15. `niwa session detach <id>` against a session with a dead
  attach holder removes the stale sentinel and exits 0 silently. No
  `--force` required.
- [ ] AC16. `niwa session detach <id> --force` against a session
  with a live attach holder sends SIGTERM to the holder, waits the
  grace period (SIGKILL if needed), removes the sentinel, and exits
  with code 4 (per Exit Code Mapping; signals "killed live
  holder").

### Discovery Surface

- [ ] AC17. `niwa session list` (no flags) shows the lifecycle view.
  No deprecation-warning text appears on stderr (R14: alias
  removed, not demoted).
- [ ] AC17b. `niwa mesh list` (direct invocation) still returns the
  coordinator process registry view with columns
  `ROLE | PID | STATUS | LAST-SEEN | PENDING`.
- [ ] AC18. `niwa session list` includes the `AVAILABILITY` column
  between `STATUS` and `CREATED`. Header is the literal `AVAILABILITY`
  in uppercase. Values are exactly `available`, `attached`, or
  `stale` in lowercase.
- [ ] AC19. `niwa session list --attached` filters to only sessions
  currently held by an attach lock with a live owner.
- [ ] AC20. `niwa session list --available` filters to only sessions
  whose AVAILABILITY is `available`. Sessions with `stale` value
  appear under neither `--attached` nor `--available` (per R16).
- [ ] AC21. With three sessions created in known order (A oldest, B
  middle, C newest) and B subsequently attached, `niwa session list`
  returns rows in the order: B (attached, by `started_at`), C, A
  (by creation time descending). Tested with fixture-driven
  timestamps.
- [ ] AC22. `niwa_list_sessions` MCP tool returns the `attach`
  sub-object with the full R11 shape when a lock is held; the
  `attach` key is **absent** from the JSON when no lock is held
  (not `null`). Verified by JSON unmarshal into a struct that fails
  on an explicit null.
- [ ] AC22b. `niwa_list_sessions` accepts the new `attached: true`
  and `available: true` input parameters, AND-combined with `repo`
  and `status` filters identically to the CLI flags (R12, R16).
- [ ] AC22c. A session with both the PR #115 `daemon` sub-object
  (when that PR lands) and the `attach` sub-object populated
  round-trips through the `SessionLifecycleState` reader/writer
  without a V bump or migration error. Tested with a fixture
  state.json that includes both sub-objects.

### Destroy Interaction

- [ ] AC23. `niwa_destroy_session` (or `niwa session destroy <id>`)
  against an attached session without `force: true` returns a
  structured error response with `error_code` field equal to the
  literal string `SESSION_ATTACHED` and a `message` field
  containing the literal substring `niwa session detach --force`
  and the literal `pid=<int>` of the holder.
- [ ] AC24. `niwa session destroy <id> --force` against an attached
  session sends SIGTERM to the attach holder, waits the grace
  period, and proceeds with destruction (kills daemon, removes
  worktree, marks ended). Same behaviour for the MCP tool with
  `force: true`.

### Worktree State on Detach

- [ ] AC25. Detach with a clean worktree exits with the propagated
  Claude exit code and emits no warnings to stderr.
- [ ] AC26. Detach with uncommitted edits (modified or staged
  files; `git status --porcelain` non-empty for changes) prints
  stderr lines matching `warning: worktree has uncommitted changes`
  followed by the porcelain output, then exits with the propagated
  Claude exit code. Does not abort.
- [ ] AC26b. Detach with untracked-only files (no committed changes,
  but `git status --porcelain` shows `??` entries) prints stderr
  lines matching `warning: worktree has untracked files` followed
  by the porcelain output, then exits with the propagated Claude
  exit code.
- [ ] AC27. Detach with unpushed commits on the session branch
  (`git for-each-ref` shows ahead-count > 0) prints stderr lines
  matching `warning: worktree has unpushed commits on
  session/<id>` followed by the ahead-count, then exits with the
  propagated Claude exit code.
- [ ] AC27b. The explicit `niwa session detach <id>` command (no
  Claude in the picture) does NOT run the worktree-state check;
  exits 0 silently when releasing a stale lock.

### Multi-User Boundary

- [ ] AC28. `niwa session attach <id>` invoked by a different UID
  than the workspace owner exits 1 with stderr matching `niwa:
  error: cannot attach to session owned by another user (file
  owner uid=<int>, your uid=<int>)` (per R26). Implementation may
  use `os.Geteuid()` or wrap an EACCES from the first state read;
  the user-visible error is what the AC tests.

### Documentation

- [ ] AC29. `docs/guides/sessions.md` contains a top-level section
  with the literal heading `## Human-in-the-Loop: Attaching to a
  Session`. The section contains all of the following subsections
  (verifiable via grep on `### `): `Attach`, `Detach`,
  `Discovering an Attached Session`, `Force Detach`,
  `Failure Modes`, `Scenario Walkthroughs`. The Failure Modes
  subsection contains the three R4 error strings (cases A, B, C)
  verbatim.

### Test Coverage

- [ ] AC30. A `@critical` Gherkin scenario in
  `test/functional/features/` exercises the attach → detach → mesh
  resume golden path end-to-end against the real `niwa` binary.
  The scenario file's `Scenario:` line contains the literal
  `attach`.
- [ ] AC31. Unit tests exist (verifiable via `go test -v -run
  <name>` returning non-zero output) for: `TestAttachLockAcquire`,
  `TestAttachLockStaleRecovery`, `TestAttachPreflightCaseA`,
  `TestAttachPreflightCaseB`, `TestAttachPreflightCaseC`,
  `TestSessionListAvailabilityColumn`,
  `TestSessionListSortAttachedFirst`,
  `TestDestroySessionAttachedError`,
  `TestSentinelFileShape`,
  `TestExitCodeMapping`,
  `TestForceDetachLiveHolder`.

### Non-Functional Verification

- [ ] AC32. R21 latency: on the reference environment, end-to-end
  `niwa session attach <id>` to Claude Code's first prompt
  completes within 5 seconds with `NIWA_DESTROY_GRACE_SECONDS=0`
  and no live worker. Measured by wrapping the command in `time`
  and parsing the wall-clock output.
- [ ] AC33. R25 non-Linux fallback: on macOS or BSD (where
  `/proc/<pid>/stat` is unavailable), `IsPIDAlive` falls back to
  signal-0 only. This AC is documentation/manual-only since CI is
  Linux; flagged as `@manual` in functional tests.

## Out of Scope

The following are deliberately excluded from this PRD. Each has either
a deferred plan, a separate issue, or a structural reason it doesn't
fit.

- **Cross-workspace-instance session discovery.** `niwa session list`
  stays scoped to the current instance per issue #117 lock-in. A
  follow-up may add `--instance <name>` if a user need emerges.
- **Multi-user shared-machine semantics.** Single-UID is the declared
  trust model. Multi-user support would require permission redesign,
  socket UID checks, and ownership coordination — out of scope for v1.
- **Transcript editing or splicing.** The operator attaches and
  continues the conversation. Surgically editing prior turns is not
  supported.
- **Programmatic / MCP-based attach.** No `niwa_attach_session` MCP
  tool. Attach is a human-driven CLI feature; programmatic attach
  would require a different concurrency model and is not justified by
  any current request.
- **Heartbeat protocol for SSH-disconnect-with-survivor detection.**
  v1 ships with SIGHUP-handler-only auto-release plus
  `niwa session detach --force` as the operator escape hatch. A
  heartbeat would be the only new pattern in the codebase; not
  justified for the rare nohup-style hostile-detach case.
- **Forensic attach to ended sessions.** `niwa_destroy_session` runs
  `git worktree remove --force` and deletes the session branch — both
  the working directory and the ref `claude --resume` would need are
  destroyed. Forensic inspection of an ended session means reading
  its `<sid>.json` state file, not attaching.
- **Multi-worker-per-session transcript selection.** Today's mesh
  model is single-worker-per-session: one captured
  `claude_conversation_id` is reused across worker re-spawns within
  the session. If the model ever permits multiple workers per session
  with distinct transcripts (sequential or concurrent), the PRD that
  introduces that capability owns the resume-disambiguation
  semantics. This PRD assumes the existing 1:N-sequential model.
  See D19 for the explicit decision.
- **Coordinator push notification on attach state change.** The
  filesystem-visible state file is sufficient via polling. No new
  notification channel is added.
- **Visibility of mesh queue from inside the attached session.** Per
  the issue's locked-in default, queued envelopes are invisible to
  the operator inside the attached Claude Code session. They become
  visible (and processable) via daemon catch-up replay after detach.
- **Fixing related issues #108, #109, #111, or #112.** These are
  addressed in PR #115 (`docs/niwa-mesh-reliability`). This PRD's
  schema additions coordinate with PR #115's `daemon` sub-object
  pattern but do not depend on it landing first.
- **Plugin or Claude-Code-config inheritance for the attached
  session.** Attach exec's the user's plain `claude` binary; the
  attached session inherits the user's normal Claude Code
  configuration (full plugins, full tool palette). Workers' stripped-
  down config is not propagated. This is the right default for
  pair-debug and recovery use cases.

## Compatibility and Migration

R14 removes the deprecated `niwa session list` (no flags) alias to
`niwa mesh list`. Operators or scripts relying on the legacy behaviour
must migrate to `niwa mesh list` directly. The deprecation warning
has been live since DESIGN-mesh-session-lifecycle landed; attach is
the trigger for completing the rename. Release notes shall include
this migration step explicitly.

## Open Questions

None remain that block PRD acceptance. All 7 open questions from the
source issue (#117) plus the multi-worker-per-session question and
the dangling-envelope-visibility cross-ref to #112 are resolved:

- State-model field shape: nested `attach` sub-object (R11).
- Schema versioning: no V bump (R23, per PR #115 precedent).
- Lock primitive: `flock` + sentinel JSON (R18).
- Stale-lock recovery: `IsPIDAlive` + `--force` escape (R19, R8, R9).
- Pre-attach validation states: `active` only (R2, AC6, AC6b).
- Worktree state on detach: warn loudly, don't auto-clean (R20, AC25-AC27b).
- Multi-user boundary: declared by reference + R26 error wrapper (R24, R26, AC28).
- Transcript persistence: pre-flight stat for UX, never `--continue`
  (R4, D9, D10).
- Coordinator awareness: polling, no push channel (R12).
- Discovery UX: AVAILABILITY column orthogonal to STATUS, attached-first
  sort (R15, R17).
- MCP surface: additive `attach` sub-object + `SESSION_ATTACHED` error
  (R12, R13).
- Force semantics: explicitly different on attach vs detach
  (Exit Code Mapping table; D3).
- Multi-worker-per-session: single-worker model assumed; out of scope
  (D19; Out of Scope item).
- Dangling-envelope visibility from inside attach: invisible
  (Out of Scope item; R5 catch-up handles post-detach).

The remaining undocumented question (claude-version-drift sensitivity
to the path encoding) is a known fragility, not a blocker: R4 already
requires niwa to tolerate `claude --resume` exit 1 even when the
pre-flight check passes. If a future Claude Code release changes the
path scheme, attach degrades to "always defer to claude's own error"
rather than breaking outright.

## Known Limitations

1. **Demand validation is incomplete.** The exploration's
   adversarial-demand lead concluded "demand not validated" — issue
   #117 is a single-author proposal with no corroborating asks in the
   niwa repo. The maintainer's direction is to proceed; this PRD
   documents the assumption that the maintainer values this enough to
   maintain the new surface. Telemetry trigger for revisiting:
   if observed `niwa session attach` invocations are < 1 per
   workspace-instance per month after 8 weeks of release availability,
   the feature is a candidate for removal or simplification.

2. **Linux-only PID-liveness verification.** R25 documents the
   non-Linux fallback. Long-lived workspaces on macOS may experience
   "lock held by alive process" false positives after PID recycling.
   Acceptable for v1 because niwa is Linux-first today.

3. **SSH-disconnect-with-survivor relies on operator action.** If a
   user's SSH session disconnects but the niwa-attach process inherits
   no controlling terminal, the lock stays held until the operator
   runs `niwa session detach --force`. No automatic detection.
   Heartbeat was considered and rejected as over-engineering for v1.

4. **Concurrent attach attempts reject rather than queue.** AC10,
   AC12b. Two-user "tmux-style shared session" attach is explicitly
   out of scope.

5. **Exit-code asymmetry between attach contexts.** Codes 0, 1, 2, 3,
   4 originate from niwa; codes 1-125 may also propagate from Claude
   Code. The Exit Code Mapping table is the single source of truth.
   Operators reading exit codes need to consult the table to know
   which side produced the failure.

## Decisions and Trade-offs

The following decisions were made during exploration and locked in by
this PRD. Each entry: what was decided, what alternatives were
considered, and why the chosen option won.

### D1. Verb pair: `attach` + `detach` (not `enter`/`exit`, not `connect`/`disconnect`)

- **Decided:** `niwa session attach <id>` for normal flow,
  `niwa session detach <id> [--force]` as operator escape hatch.
- **Alternatives:** `enter`/`exit`, `connect`/`disconnect`, `take`/`release`.
- **Why:** tmux + Docker establish `attach`/`detach` as deeply
  ingrained muscle memory for the human-takeover pattern. Other verbs
  add cognitive load without payoff.

### D2. Detach is operator-only, not the normal release path

- **Decided:** Normal attach release happens automatically when Claude
  Code exits. The explicit `niwa session detach <id>` command exists
  only for stale-lock recovery.
- **Alternatives:** require explicit `niwa session detach` after every
  attach; symmetric attach/detach commands.
- **Why:** Asymmetric pairing matches user expectation (you exit
  Claude Code, you don't run a release command). Explicit detach
  exists for the case where the lock is stuck.

### D3. `--force` semantics differ between `attach` and `detach`

- **Decided:** On `attach`, `--force` SIGTERMs a running worker. On
  `detach`, `--force` steals the lock from another holder. PRD calls
  this out explicitly because the symmetry instinct misleads
  operators.
- **Alternatives:** make `--force` mean the same thing on both;
  rename one of the two flags (e.g., `--steal` on detach).
- **Why:** Each command has exactly one destructive thing it can do,
  and `--force` naming is the established niwa convention. Renaming
  would be inconsistent with `niwa session destroy --force`. The
  doc-time clarification is cheap.

### D4. State-model field shape: nested `attach` sub-object (computed, not persisted)

- **Decided:** New nested `attach` sub-object on
  `SessionLifecycleState` with `{ owner_pid, owner_start_time,
  started_at, lock_path }`, computed at read time from the on-disk
  sentinel — not persisted into `<sid>.json`.
- **Alternatives:** new `Status` value (`attached`); flat
  `availability` string field; persist the sub-object directly into
  `<sid>.json`.
- **Why:** Mirrors PR #115's `daemon` sub-object precedent (also
  computed, not persisted). Overloading `Status` would conflate two
  semantics; persisting attach state into `<sid>.json` would create a
  staleness window between the sentinel and the lifecycle file.

### D5. No `SessionLifecycleState.V` bump

- **Decided:** Additive computed field under existing V:1.
- **Alternatives:** bump to V:2 per the contributor note in
  `docs/guides/sessions.md`.
- **Why:** PR #115 sets the no-bump precedent for additive computed
  sub-objects. Following its lead avoids contradicting an in-flight
  design choice. Existing V:1 readers ignore unknown projection
  fields.

### D6. Lock primitive: `flock` + sentinel JSON

- **Decided:** Non-blocking exclusive `flock(<worktree>/.niwa/attach.lock)`
  for mutual exclusion + sibling `<worktree>/.niwa/attach.state` JSON
  sentinel (atomic tmp+rename) for visibility.
- **Alternatives:** flock-only (no sentinel; visibility via flock
  probing); state-file-only (no flock; rely on PID liveness alone);
  `lock_holder` field directly on `SessionLifecycleState` JSON.
- **Why:** Direct precedent in `acquireDaemonPIDLock`. Implicit
  release via fd-lifetime is the simplest stale-recovery story. The
  sentinel exists so `niwa session list` can read holder metadata
  without flock-probing the file.

### D7. `niwa session attach` is a long-running parent process, NOT exec-replacement

- **Decided:** niwa stays running, runs Claude Code as a child via
  `exec.Cmd`, waits, releases lock on Claude exit.
- **Alternatives:** `syscall.Exec` to replace niwa with Claude Code;
  wrapper-driven acquire/release/cd via shell helper.
- **Why:** Exec-replacement drops the flock immediately, making the
  lock useless. Wrapper-driven adds three calls and is fragile across
  shell exits. Niwa-supervised matches the daemon supervision pattern
  the codebase already uses.

### D8. Daemon coordination: terminate-and-respawn (not lock-aware-skip)

- **Decided:** `TerminateDaemon` on attach acquire,
  `EnsureDaemonRunning` on detach release. Catch-up inbox replay
  handles in-flight envelopes naturally.
- **Alternatives:** add a sentinel-file-skip in `handleInboxEvent` so
  the daemon stays alive but doesn't claim envelopes during attach;
  pause via a signal to the daemon.
- **Why:** Terminate-and-respawn requires zero changes to the
  daemon's hot path. The catch-up replay path is already exercised by
  every daemon restart.

### D9. Pre-flight transcript validation is for UX, not safety

- **Decided:** Validate before exec, with three distinct error
  messages.
- **Alternatives:** trust claude (it already exits 1 on every failure
  mode); validate but fall back to fresh session on failure.
- **Why:** Empirically `claude --resume` fails loudly so safety is not
  the issue. Pre-flight exists so niwa can emit niwa-shaped error
  messages with three actionable cases instead of `claude`'s opaque
  "No conversation found with session ID: <uuid>".

### D10. Never use `claude --continue`

- **Decided:** Always use `claude --resume <uuid>`, never `--continue`.
- **Alternatives:** `--continue` (resumes most-recent session in CWD).
- **Why:** Empirically, `--continue` silently degrades to a fresh
  session (exit 0) when invoked from a CWD with no history.
  `--resume <uuid>` always fails loudly when the transcript is
  missing.

### D11. Path encoding: `s/[^A-Za-z0-9]/-/g`

- **Decided:** Match Claude Code's actual encoding rule (verified
  empirically on claude v2.1.138).
- **Alternatives:** `base64url(cwd)` (round-1 hypothesis; empirically
  wrong).
- **Why:** Niwa must look in the directory Claude Code actually
  writes to.

### D12. AVAILABILITY column values: `available` / `attached` / `stale`

- **Decided:** Lowercase kebab-case state vocabulary matching niwa's
  existing voice.
- **Alternatives:** `free`/`attached`/`stale-lock`,
  `idle`/`attached`/`expired`.
- **Why:** Matches `active`/`ended`/`abandoned` style. `available` is
  unambiguous in column-header context; `stale` is shorter than
  `stale-lock` for the table.

### D13. Default sort: attached first, then status, then creation time descending

- **Decided:** Three-key composite sort.
- **Alternatives:** sort by ID (current behaviour, effectively
  random); creation-time-only.
- **Why:** Attach surfaces the operator's hot question ("is anyone in
  there right now?") to the top of the table.

### D14. `niwa session list` flagless default flips to lifecycle view

- **Decided:** Remove the deprecated `mesh list` alias path entirely.
- **Alternatives:** keep the deprecation warning for another release.
- **Why:** Attach is the right reason to flip the default; the
  deprecation has been live since DESIGN-mesh-session-lifecycle
  landed.

### D15. SSH-disconnect: SIGHUP-handler + `--force`, no heartbeat

- **Decided:** v1 relies on SIGHUP cascading to kill niwa-attach plus
  operator-driven `niwa session detach --force` for the rare
  survivor case.
- **Alternatives:** heartbeat from niwa-attach; TTY-poll.
- **Why:** Heartbeat would be the only new pattern in the codebase.
  Operator recovery is one command.

### D16. Exit codes: propagate Claude exit capped at 125

- **Decided:** Forward Claude Code's exit code through niwa, capped
  at 125. niwa-side errors use codes 1, 2, 3, 4 per the Exit Code
  Mapping table.
- **Alternatives:** always exit 0 on successful detach; use a
  niwa-specific code for "Claude exited non-zero".
- **Why:** Operators write scripts around niwa commands. Hiding
  Claude's exit code breaks that contract.

### D17. MCP surface change is minimal and additive

- **Decided:** New `attach` sub-object on `niwa_list_sessions`
  output. New `attached`/`available` input parameters on
  `niwa_list_sessions`. New `SESSION_ATTACHED` error code from
  `niwa_destroy_session`. No new MCP tools.
- **Alternatives:** add `niwa_attach_session` programmatic tool; add
  push-notification channel.
- **Why:** Filesystem-visible state suffices for coordinator polling.
  No external request emerged for programmatic attach.

### D18. Demand-validation caveat surfaced as a Known Limitation

- **Decided:** Document the demand-validation finding as a Known
  Limitation rather than blocking on stop-gate evidence.
- **Alternatives:** route to a Rejection Record artifact; require
  more demand evidence before drafting the PRD.
- **Why:** The maintainer has set direction to proceed. The risk is
  documented (with a numeric removal trigger in Known Limitations
  #1) so future telemetry can revisit if the feature is rarely used.

### D19. Multi-worker-per-session: single-worker model assumed

- **Decided:** This PRD assumes today's mesh model: one captured
  `claude_conversation_id` per session, reused across worker
  re-spawns within the session (1:N sequential). Multi-worker-per-
  session resume disambiguation is explicitly out of scope.
- **Alternatives:** define resume semantics for multiple transcripts
  per session now; defer the entire attach feature until
  multi-worker is settled.
- **Why:** Today's daemon path captures the conversation ID once and
  re-uses it; there is no producer of additional transcript IDs.
  Locking attach to today's model means attach can ship now without
  blocking on a hypothetical capability. If multi-worker is added
  later, the PRD that introduces that capability owns the
  resume-disambiguation question.
