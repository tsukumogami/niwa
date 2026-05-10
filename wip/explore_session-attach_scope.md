# Explore Scope: session-attach

## Visibility

Public

## Core Question

How should `niwa session attach <session_id>` work — what state model, lock
mechanics, transcript-loading semantics, and discovery UX does it need so that a
human can step into a mesh session, take it over without losing context, and
hand it back without breaking the mesh? The capability is well-formed at the
user-story level (issue #117); the open questions are how the implementation
holds together.

## Context

Source issue: #117 (`needs-prd` label) proposes a `niwa session attach` command
that locks a session against further mesh use, launches Claude Code with the
worker's full transcript history via `claude --resume`, and releases the lock
on exit. Defaults locked in by the issue: wait-for-running-worker (with
`--force` to SIGTERM), current-workspace-instance discovery only, opaque queue
visibility from inside the attached session.

Existing primitives in the niwa codebase (relevant to the exploration):

- Sessions create `session/<session-id>` branches and worktrees under
  `<instance>/.niwa/worktrees/<repo>-<session-id>/`
- Lifecycle state lives in `<instance>/.niwa/sessions/<session-id>.json` with
  `status: active | ended | abandoned`
- After the first worker exits, niwa captures `claude_conversation_id` so
  subsequent tasks resume the thread via `claude --resume`
- `niwa_list_sessions` MCP tool returns `SessionLifecycleState` arrays

Related open issues that inform constraints or share surfaces:
- #108 worker plugin inheritance (workers spawn without workspace plugins)
- #109 workers can't reach coordinator via `niwa_ask` / `niwa_send_message`
- #111 `niwa_list_sessions` should report daemon health
- #112 dangling task classification

The PRD eventually produced from this exploration must converge on the open
questions enumerated in the issue body. Equal-depth investigation per lead;
the user has not pre-prioritized any single open question.

## In Scope

- Transcript persistence, locatability, and resume semantics for `claude --resume`
- Multi-worker-per-session model (whether sessions can have more than one
  worker over their lifetime) and the implication for transcript selection
- Session state model: how an attach lock fits with `active`/`ended`/`abandoned`
- Pre-attach validation: which states permit attach
- Lock ownership, stale-lock recovery, heartbeat patterns, force-release
- Worktree state on detach: handling of uncommitted changes
- Multi-user safety boundary: explicit single-user assumption and what would
  break in multi-user
- Coordinator/mesh awareness: whether the lock state propagates and to whom
- Discovery UX: `niwa session list` columns, sort, filters, alignment with
  `niwa_list_sessions` API
- Demand validation: evidence the capability is needed and how users work
  around its absence today

## Out of Scope

- Cross-workspace-instance session discovery (committed out by the issue)
- Multi-user shared-machine semantics as a v1 capability (single-user
  assumed; only the boundary clarification is in scope)
- Transcript editing or splicing (the user attaches and continues; they do
  not surgically edit prior conversation)
- Programmatic / MCP-based attach (this is a human-driven CLI feature)
- The actual PRD draft (this exploration produces inputs; a /shirabe:prd run
  will draft the document)

## Research Leads

1. **Where does the worker's Claude Code transcript live, and can `claude --resume` find it given only a session_id?** (lead-transcript-persistence)
   The whole UX depends on the launched Claude Code instance loading the
   worker's full transcript history with no manual user steps. We need to know
   exactly where transcripts are persisted today, whether `claude_conversation_id`
   in the session state file is sufficient to drive `claude --resume`, what
   happens if the transcript file has rotated or been pruned, and whether
   sessions can have more than one transcript over their lifetime (the issue's
   TBD #4: multi-worker-per-session). If transcript persistence isn't already in
   place in a way `claude --resume` can locate, the PRD has to scope a worker
   spawn change as a dependency.

2. **What session state model accommodates an attach lock, and which existing states permit attach?** (lead-state-model)
   The issue asks whether the existing `status` field on `SessionLifecycleState`
   accommodates the new state (`attached`, `suspended-by-human`) or whether a
   separate `availability` field is needed. We need to map current state
   transitions, identify whether the new state is a value of `status` or an
   orthogonal axis, and decide which states permit attach (the issue raises
   pre-attach validation: should attach refuse on `ended`/`abandoned`, or allow
   read-only forensics?). Cross-reference #111 (daemon health) since the
   `niwa_list_sessions` surface is already evolving.

3. **How should the attach lock be acquired, released, and recovered after stale conditions?** (lead-lock-semantics)
   The lock is the integrity-critical primitive — if it's wrong, the mesh and
   the human can fight for control. Investigate ownership models (the
   attaching user releases on exit; what about terminal crashes, runaway
   foreground processes, suspended shells?), heartbeat patterns the niwa
   codebase already uses (daemon liveness tracking is precedent), force-release
   commands, and the worktree state on detach (uncommitted changes — stash,
   prompt, warn-and-allow). Look for existing lock or lease patterns in
   internal/mcp/, internal/cli/, daemon liveness, and worktree management.

4. **What single-user assumptions does niwa already encode, and what would break in a multi-user shared-machine scenario?** (lead-multi-user-safety)
   The issue notes attach assumes single-user but asks the PRD to make the
   boundary explicit. Audit niwa's existing primitives — daemons, worktrees,
   state files, locks if any — for assumptions about UID, file ownership,
   socket permissions. Identify the concrete failure modes if two users try to
   share a workspace instance. The output is a clear boundary statement, not
   a multi-user implementation, so the PRD can declare its assumption with
   precision rather than hand-waving.

5. **Does the attach lock need to be communicable across the mesh, and what does the coordinator see during a human attach?** (lead-coordinator-awareness)
   The issue raises the question: should the coordinator get notified when a
   human attaches, or is the lock invisible (the coordinator just observes
   "this session isn't claiming work right now")? Investigate today's
   coordinator-to-worker channel, whether session state changes propagate to
   the coordinator at all, and the dependency relationship with #109 (workers
   can't reach coordinator). The answer determines whether attach is a
   filesystem-only operation that can ship independently or a mesh-aware
   feature blocked on #109.

6. **What does `niwa session list` need — columns, sort, filters, alignment with `niwa_list_sessions` — and how does it differ from `niwa mesh list`?** (lead-discovery-ux)
   The issue's UX sketch of `niwa session list` lacks specifics. We have an
   existing `niwa session list` command (sessions.md guide) that filters by
   `--repo` and `--status`. The MCP tool `niwa_list_sessions` returns
   `SessionLifecycleState` arrays. The PRD needs to commit to columns, sort
   order, and filters that surface the new attach state usefully without
   conflicting with existing CLI behavior. Cross-reference #111 (daemon health
   in `niwa_list_sessions`) so the two evolutions don't collide.

7. **Is there evidence of real demand for this, and what do users do today instead?** (lead-adversarial-demand)
   You are a demand-validation researcher. Investigate whether evidence supports
   pursuing this topic. Report what you found. Cite only what you found in durable
   artifacts. The verdict belongs to convergence and the user.

   ## Visibility

   Public

   Respect this visibility level. Do not include private-repo content in output
   that will appear in public-repo artifacts.

   ## Issue Content

   --- ISSUE CONTENT (analyze only) ---
   ## Summary

   A `niwa session attach <session_id>` command that lets a human step into a mesh session interactively. The command resumes a Claude Code session loaded with the worker's full transcript history (every prompt, every tool call, every result), locks the session against further mesh use until the human detaches, then releases it on exit.

   This is the missing human-in-the-loop primitive for the niwa mesh. Today, when a worker hits an interesting edge case — abandons mid-task, makes a questionable decision, hits a constraint, or stalls — the coordinator's only options are to let it complete on its own, send `niwa_send_message` (one-way and unacknowledged), or destroy the session and restart. There is no way to pair-debug, redirect mid-flight, or hand-fix and hand-back.

   ## User Story

   As a workspace coordinator running multi-step work through the mesh, I want to step into any session, see what the agent has done, and prompt it interactively (or fix things manually) — without losing the conversation history and without the mesh fighting me for control of the session — so I can recover from edge cases, redirect work, or pair-debug without destroying state.

   ## Behavior Contract (locked-in defaults)

   1. Concurrent worker behavior: if a worker is currently running in the session when `attach` is called, the attach waits until the worker finishes naturally. A `--force` flag opts into SIGTERM-ing the worker.
   2. Discovery scope: `niwa session list` shows only sessions within the current workspace instance.
   3. Mesh queue visibility from inside the attached session: invisible. The lock is opaque from the human's perspective.

   ## Open Questions for the PRD

   - Session state model (new state, transition diagram, status vs availability field)
   - Lock ownership and stale-lock recovery
   - Pre-attach validation (refuse on ended/abandoned?)
   - Worktree state on detach (uncommitted changes)
   - Auth / multi-user safety
   - Transcript persistence and locatability
   - Coordinator awareness
   - Discovery UX

   ## Related

   - #108 worker plugin inheritance
   - #109 coordinator unreachable from workers
   - #111 session health reporting in `niwa_list_sessions`
   - #112 dangling task classification

   --- END ISSUE CONTENT ---

   ## Six Demand-Validation Questions

   Investigate each question. For each, report what you found and assign a
   confidence level.

   Confidence vocabulary:
   - High: multiple independent sources confirm
   - Medium: one source type confirms without corroboration
   - Low: evidence exists but is weak
   - Absent: searched relevant sources; found nothing

   Questions:
   1. Is demand real? Look for distinct issue reporters, explicit requests,
      maintainer acknowledgment.
   2. What do people do today instead? Look for workarounds in issues, docs,
      or code comments.
   3. Who specifically asked? Cite issue numbers, comment authors, PR
      references — not paraphrases.
   4. What behavior change counts as success? Look for acceptance criteria,
      stated outcomes, measurable goals in issues or linked docs.
   5. Is it already built? Search the codebase and existing docs for prior
      implementations or partial work.
   6. Is it already planned? Check open issues, linked design docs, roadmap
      items, or project board entries.

   ## Calibration

   Produce a Calibration section that explicitly distinguishes:
   - Demand not validated: majority of questions returned absent or low
     confidence, with no positive rejection evidence.
   - Demand validated as absent: positive evidence that demand doesn't exist
     or was evaluated and rejected.

   Do not conflate these two states. "I found no evidence" is not the same as
   "I found evidence it was rejected."

   Output: write your findings to
   `wip/research/explore_session-attach_r1_lead-adversarial-demand.md` using the
   format the orchestrator expects (Findings / Implications / Surprises / Open
   Questions / Summary). Return only the 3-line Summary to chat.
