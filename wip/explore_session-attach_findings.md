# Exploration Findings: session-attach

## Core Question

How should `niwa session attach <session_id>` work — what state model, lock
mechanics, transcript-loading semantics, and discovery UX does it need so that
a human can step into a mesh session, take it over without losing context, and
hand it back without breaking the mesh? The capability is well-formed at the
user-story level (issue #117); the open questions are how the implementation
holds together.

## Round 1

### Key Insights

- **Transcript path encoding is deterministic** (lead-transcript-persistence). Worker transcripts live at `~/.claude/projects/<encoded_cwd>/<conv_id>.jsonl`. Round 1 reported `base64url(cwd)`; round 2 corrected this to `s/[^A-Za-z0-9]/-/g`. The `claude_conversation_id` captured by niwa is sufficient for `claude --resume` ONLY when invoked from the worker's original CWD (`<worktree>/<repo>`). niwa's existing `mesh_watch.go:1010-1012` already handles this for daemon-spawned `--resume`; attach must replicate the `cd` step.
- **Existing pre-flight integrity helper exists** (lead-transcript-persistence). `checkSessionFileIntegrity` at `mesh_watch.go:1832-1868` already performs the deterministic-path stat. Attach can reuse this primitive instead of re-implementing.
- **Session state model: orthogonal field, not new Status value** (lead-state-model). PR #115's design for #111 establishes the precedent of a parallel `daemon` sub-object on `SessionLifecycleState`. Attach should mirror this with an `attach` sub-object (or `availability` field) so lifecycle/availability/health stay independent axes.
- **`abandoned` is a reserved-but-unwritten status** (lead-state-model). No code path produces it. PRD can ignore.
- **Pre-attach validation: only `Status == active` permits attach** (lead-state-model). Forensic attach on `ended` is physically infeasible because `niwa_destroy_session` runs `git worktree remove --force` and `git branch -d/-D` — both the CWD and the ref `claude --resume` would need are gone.
- **Lock = filesystem flock at `<worktree>/.niwa/attach.lock`** (lead-lock-semantics). Direct precedent: `acquireDaemonPIDLock` at `mesh_watch.go:2363`. Non-blocking exclusive flock; release implicit via fd-lifetime. Foreground `niwa session attach` process holds the flock and forks `claude --resume` as a child (NOT exec-replace, otherwise the lock dies immediately).
- **Sentinel JSON `<worktree>/.niwa/attach.state` for visibility** (lead-lock-semantics). Holds `{owner_pid, owner_start_time, started_at}`; `IsPIDAlive` (`internal/mcp/liveness.go:14`) provides automatic stale detection. Atomic tmp+rename, no lock needed.
- **Daemon coordination: terminate per-worktree daemon during attach, respawn on detach** (lead-lock-semantics + lead-coordinator-awareness). `TerminateDaemon` + `EnsureDaemonRunning` already exist. Catch-up replay handles in-flight queue. This subsumes the issue's "wait for running worker" default — there's no running worker once the daemon is paused.
- **Multi-user: niwa already enforces single-UID via 0600 perms** (lead-multi-user-safety). Cited in `DESIGN-cross-session-communication.md` as "same-UID cooperative trust." No new safeguard needed; PRD declares boundary by reference.
- **Coordinator awareness: filesystem-visible state means attach is independent of #109/#111** (lead-coordinator-awareness). Coordinator sees `availability=attached` on next `niwa_list_sessions` poll. One subtlety: daemon must skip lock-deferred envelopes BEFORE its dangling-classifier sees them — the daemon-pause-during-attach approach handles this naturally.
- **Discovery UX: today's `niwa session list` is a confused two-mode gateway** (lead-discovery-ux). Flagless = deprecated alias for `niwa mesh list` (a different concept entirely). With `--repo` or `--status` it shows lifecycle sessions sorted by random hex ID. PRD must (a) flip default to lifecycle view, (b) add `AVAILABILITY` column orthogonal to `STATUS`, (c) sort attached-first then status then created-desc.
- **Demand: "not validated" rather than "validated as absent"** (lead-adversarial-demand). Issue #117 is single-author with no corroborating asks. The underlying problem is real and visible in code (today's recovery options are limited). User direction: proceed; PRD surfaces this as a risk/assumption, not a stop-gate.

### Tensions

- **State-model field shape unresolved**: nested `attach` sub-object (mirrors PR #115) vs. flat `availability` string. Round 1 (lead-state-model) deferred to coordination with #111. Round 2 (lead-ux-mcp-surface) recommends following PR #115's no-version-bump precedent and the nested-sub-object shape since #115 already establishes it. **Resolution: nest under `attach` sub-object, no V bump.**
- **Schema version bump**: `docs/guides/sessions.md:303-308` mandates V bump for additive fields. PR #115 doesn't bump V despite the contributor note. The exploration follows PR #115's precedent (no V bump for additive fields under existing V:1 readers), since contradicting that decision now would also mean reverting #111's choice. **Resolution: no V bump.**
- **SSH-disconnect-with-survivor case**: heartbeat (complex; new pattern) vs. SIGHUP-handler-only + `--force` escape hatch (simpler; matches existing patterns). All round-1 leads converged on the second option for v1; heartbeat is over-engineering.
- **`--force` semantic asymmetry**: on `attach`, `--force` SIGTERMs the running worker (per issue's locked-in default). On `detach`, `--force` steals the lock from another holder. Round 2 scenarios surfaced this as a UX risk that the PRD must call out explicitly because the symmetry instinct will mislead operators.

### Gaps

- None remain that block PRD work. All 7 issue-body open questions have answers grounded in code precedent.

### Decisions

- See `wip/explore_session-attach_decisions.md`.

## Round 2

### Key Insights

- **`claude --resume` fails LOUDLY** (lead-transcript-failure-modes, empirical). `claude --resume <uuid>` returns exit 1 with stderr `No conversation found with session ID: <uuid>` for every failure mode tested (missing JSONL, wrong CWD, corrupted JSONL, zero-byte JSONL, never-seen project dir). No silent-fresh-degradation risk — provided niwa uses `--resume <uuid>` exclusively. **`--continue` IS unsafe** (silent fresh session, exit 0, when CWD has no history) — niwa MUST never call it.
- **Path encoding is `s/[^A-Za-z0-9]/-/g`, NOT base64url** (lead-transcript-failure-modes, empirical correction to round 1). Realistic worktree paths under `<workspace>/<instance>/<repo>` won't collide.
- **Pre-flight validation is for UX, not safety** (lead-transcript-failure-modes). claude already fails loudly. Pre-flight just lets niwa emit niwa-shaped errors with three distinct messages: case A (no conv_id captured), case B (transcript file missing), case C (zero-byte transcript).
- **CLI tone is unornamented and consistent** (lead-ux-cli-tone). ALL-CAPS table headers (`SESSION_ID`, `REPO`, `STATUS`, `AVAILABILITY`, `CREATED`, `PURPOSE`); lowercase kebab-case state values (`active`, `attached`, `available`); two-space leading indent on tables; success messages use the form `<noun>: <verb> <id>` on stderr; long-form flags by default; lowercase `warning:`/`note:`/`hint:` prefixes; no confirmation prompts (warn loudly, never block).
- **Peer precedent: tmux + Docker** (lead-ux-peer-patterns). Verb `attach`, noun `session`, "(attached)" annotation are deeply established muscle memory. `niwa session attach <id>` and `niwa session detach <id> --force` are the right names. Tmux's `attach -d` (one-step takeover) is an alternative to two-step `detach --force; attach` — round-2 flagged this as the open ergonomics question; the PRD picks two-step for clarity (force-on-attach already steals from the running mesh worker; force-on-detach steals from a human; conflating them in one flag obscures intent).
- **MCP surface change is minimal and additive** (lead-ux-mcp-surface). Computed `attach` sub-object on `niwa_list_sessions` output (read at query time from `<worktree>/.niwa/attach.state`, mirroring how PR #115 projects its `daemon` sub-object). New `SESSION_ATTACHED` error code from `niwa_destroy_session` when force is not set. NO change to `niwa_delegate`, `niwa_ask`, `niwa_send_message`, `niwa_create_session`, `niwa_query_task`, `niwa_await_task`. The user's "no new MCP tools, CLI-first" guidance holds.
- **7 scenarios with exact terminal output mocked** (lead-ux-scenarios). Pattern: 6 stderr lines pre-launch (acquired-lock, paused-daemon, validated-transcript, etc.), claude in terminal, 3 stderr lines post-detach (released-lock, restarted-daemon, surfaced-warnings). Each line names exactly one on-disk effect, so partial failures are diagnosable from the last printed line.
- **Exit-code policy**: scenarios picked "propagate claude's exit, capped at 125" (reserve 126/127/128+ for shell semantics). PRD adopts this.

### Tensions

- **Naming: `available` vs `free` vs `idle`**: peer-patterns suggested `free`/`attached`/`stale-lock`; CLI-tone-audit suggested `available`/`attached`/`stale`; scenarios used `available`/`attached`/`stale`. **Resolution: `available`/`attached`/`stale`** because (a) the CLI-tone agent grounded its prescription in niwa's actual existing vocabulary, (b) `available` is unambiguous in the AVAILABILITY column header context, and (c) `stale` is shorter than `stale-lock` for the table.
- **Attach launch model: `cmd.Run()` vs `syscall.Exec`**: discovery-UX agent suggested `syscall.Exec` for the "hand-off-and-disappear" feel of `niwa go`. Lock-semantics agent argued for `cmd.Run()` because exec-replacement drops the flock immediately and the lock would be useless. **Resolution: `cmd.Run()` (parent-niwa + child-claude).** The lock requires niwa to outlive claude; tmux's pattern is identical (tmux client process holds the multiplexer connection while the user is in the session).
- **Schema V bump**: contributor doc says yes; PR #115 says no. Resolved above (no bump, follow PR #115 precedent).
- **Wrapper-driven vs niwa-supervised attach**: wrapper-driven adds three calls (`acquire`, `cd && claude`, `release`) and is fragile across shell exits; niwa-supervised is one process with implicit cleanup via flock-fd-lifetime. **Resolution: niwa-supervised** — matches the daemon supervision pattern niwa already uses.

### Gaps

- **Concurrent worker write + attach read race**: lead-transcript-failure-modes flagged that it couldn't safely test what claude does when a worker is mid-write and attach reads. The daemon-pause-during-attach approach SUBSUMES this gap — by the time attach reaches the `claude --resume` step, the worker is gone and the JSONL is no longer being written. The PRD locks daemon-pause-first as a precondition.

### Decisions

- See `wip/explore_session-attach_decisions.md`.

## Accumulated Understanding

**The capability shape is fully resolved.** All 7 issue-body open questions have answers grounded in code precedent, plus an empirical drill-down on the highest-stakes claude-resume mechanic. The PRD has enough material to lock in:

1. **Verb pair**: `niwa session attach <id>` (acquire lock, pause daemon, exec claude --resume); `niwa session detach <id> [--force]` (operator escape hatch for stale locks). Auto-detach on claude exit is the normal release path; the explicit `detach` command exists only for stale-lock recovery.

2. **Lock primitive**: filesystem flock at `<worktree>/.niwa/attach.lock`, exclusive non-blocking. Sibling `<worktree>/.niwa/attach.state` JSON sentinel for visibility, atomic tmp+rename, `IsPIDAlive`-checked.

3. **State model**: new `attach` sub-object on `SessionLifecycleState` (mirrors PR #115's `daemon`). No V bump. Attach permitted only when `Status == active`. `AVAILABILITY` is a new column on `niwa session list` with values `available` / `attached` / `stale`.

4. **Daemon coordination**: `TerminateDaemon` on attach acquire; `EnsureDaemonRunning` on detach release. The catch-up-replay path drains the inbox naturally on respawn. Coordinator's `niwa_delegate` envelopes queue silently in the inbox during attach.

5. **Worktree state on detach**: warn loudly via `git status`, never auto-clean. Mirrors `branch_warning` precedent on `niwa_destroy_session`.

6. **Multi-user**: declare single-UID by reference to `DESIGN-cross-session-communication.md`. No new safeguards.

7. **Coordinator awareness**: filesystem-visible state file means polling-only is sufficient. New `SESSION_ATTACHED` MCP error code from `niwa_destroy_session` when force isn't set. No changes to other MCP tools.

8. **Transcript validation**: deterministic file-stat at `~/.claude/projects/<s/[^A-Za-z0-9]/-/g(worktree)>/<convid>.jsonl` before `cmd.Run()` of `claude --resume <uuid>`. Three distinct error messages (no-conv-id, transcript-missing, transcript-empty). NEVER use `--continue`.

9. **Discovery UX**: flip `niwa session list` flagless default to lifecycle view (deprecation already planned). New columns `SESSION_ID | REPO | STATUS | AVAILABILITY | CREATED | PURPOSE`. Sort: attached first, then status, then creation-time descending. Filters: `--repo`, `--status`, `--attached`/`--available`. Coordinated with PR #115's `--daemon-alive`.

10. **CLI tone**: matches existing niwa voice — ALL-CAPS headers, lowercase kebab-case state values, lowercase `warning:`/`note:`/`hint:` prefixes, long-form flags. Success messages: `session: attached <id> at <path>` (stderr). Exit codes propagate from claude, capped at 125.

11. **Demand caveat**: the underlying problem is real but only one user has formally asked for this exact solution. PRD documents this as an assumption ("the user values this enough to maintain the new surface"); not a blocker.

## Decision: Crystallize

In auto mode, the orchestrator-level decision is to proceed to Phase 4. Per the
heuristic in SKILL.md ("If findings are sufficient and no major gaps remain,
recommend Ready to decide"), all 7 open questions from the issue body have
grounded answers, the highest-stakes risk (claude --resume mechanics) was
resolved empirically, and the gaps surfaced in round 1 (encoding wrong, MCP
surface, schema versioning, force semantics) were either resolved by round 2
or have explicit decisions in `wip/explore_session-attach_decisions.md`.

**Recommended artifact: PRD.** Issue #117 carries the `needs-prd` label. The
exploration confirmed PRD-shape work is the right next step: the requirements
are the artifact (what behavior, what error messages, what columns, what flag
semantics, what state-model field shape). The technical approach is settled
enough that a design doc would mostly restate code-grounded prescriptions
already captured here; the remaining design work is implementation patterns
that fit naturally inside the design doc that follows the PRD.
