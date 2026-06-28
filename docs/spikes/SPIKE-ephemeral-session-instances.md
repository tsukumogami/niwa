---
status: Complete
question: |
  Can a Claude Code session launched from a niwa workspace root provision its own
  ephemeral niwa instance on start (via a SessionStart hook running `niwa create`),
  do its work inside that instance, and have the instance garbage-collected on
  session end — using only Claude Code's stock `claude agents` background-session
  feature plus niwa-side hooks, with no Agent SDK orchestrator?
timebox: "1 session (exploration + live dogfood)"
---

# SPIKE: ephemeral per-session niwa instances (Claude Code session-hook delegation)

## Status

Complete

The feasibility question is settled: the mechanism works end-to-end with no niwa
code changes and no Agent SDK. Two design problems remain — both demonstrated
live, both downstream design work, neither a feasibility blocker.

A later follow-up probe (2026-06-27) added one more empirical finding that
reshaped the garbage-collection contract: the Claude Code job entry lifecycle
across the done/idle/delete states. It is recorded below under "Job-entry
lifecycle across done/idle/delete" and grounds the DESIGN's delete-only,
entry-present teardown rule (Decision 6 revision).

## Question

niwa's model distinguishes a *workspace* (the root from `niwa init`) from a
*workspace instance* (`niwa create`'d from inside the workspace, designed to be
ephemeral despite an expensive build-from-remote). The desired model is **1 Claude
Code session == 1 ephemeral instance**: launch `claude agents` from the workspace
root, and each dispatched background session gets its own instance, created on
start and torn down on end.

The blocking questions before committing to a design:

1. Can a dispatched background session's working directory be pointed at a
   freshly-created instance — given Claude Code cannot pin a per-session cwd at
   dispatch time?
2. Does a `SessionStart` hook fire for an Agent-View-dispatched background session,
   and can it inject the instance's context?
3. Is `SessionEnd` a reliable enough garbage-collection trigger, and what does it
   carry?

## Context

`claude agents` opens "Agent View", a list of background sessions; each is a full,
independent Claude Code conversation. niwa already integrates with Claude Code at
the *worktree* level (`WorktreeCreate`/`WorktreeRemove` hooks → `niwa worktree
from-hook`), already supports multiple coexisting instances (`niwa create` →
`tsuku`, `tsuku-2`, …), and already ships `niwa destroy` for non-interactive
teardown. The session under which this spike ran was itself a `niwa create`'d
instance, so the dogfood path was real.

An early framing assumed the hook would create the instance and then relocate the
session into it. That is blocked: Claude Code cannot set a session's cwd at
dispatch (open upstream feature requests), and a hook cannot relocate the parent
session. The reframe that unblocked it: dispatch into the workspace **root** (no
cwd support needed), create the instance during `SessionStart`, and have the agent
`cd` into it at runtime — with context delivered by hook injection rather than by
relocation.

## Approach

A throwaway harness (no niwa involved) registered a `SessionStart`/`SessionEnd`
hook in a directory's `.claude/settings.json`. The hook stood in for `niwa create`
by making a folder containing a `CLAUDE.md`, logged its raw stdin, and injected
`additionalContext` carrying a unique passphrase plus a "cd into the instance"
instruction. A second passphrase lived only inside the generated `CLAUDE.md`. A
background session was dispatched via `claude agents` from that directory and asked
a leak-free question; the two passphrases separated "injection reached the agent"
from "agent entered the instance and read its file". Hook stdin and event ordering
were read from the log.

## Findings

- **`SessionStart` fires for an Agent-View background session.** Empirical stdin
  schema (richer than docs): `session_id`, `transcript_path`, `cwd`, `agent_type`,
  `hook_event_name`, `source` (`"startup"`), `model`.
- **Dispatched sessions inherit the launch cwd.** Every fire was rooted at the
  directory `claude agents` was launched from — so dispatching from the workspace
  root is sufficient; no cwd-at-dispatch capability is needed.
- **`additionalContext` injection reaches the agent.** It recited the
  injection-only passphrase verbatim.
- **Lightweight relocation works end-to-end.** It recited the file-only passphrase,
  i.e. it followed the injected instruction, `cd`'d into the instance, and read its
  `CLAUDE.md`. Pure hook-injection drove it; no session re-root was needed.
- **`cd` moves only the Bash tool's working directory, not the session's project
  root.** Mid-session `cd` does not reload `CLAUDE.md`/context — context discovery
  is fixed at launch. So the instance's context must be delivered by injection, not
  by relocation.
- **`SessionEnd` fires and carries `session_id` + `cwd`**, but `reason` is coarse
  (`"other"`), and crucially **its `cwd` is the launch dir, not the instance** (the
  Bash `cd` never moved the session cwd). Teardown therefore cannot key on `cwd`; it
  must key on a `session_id → instance` mapping written at `SessionStart`.
- **`SessionEnd` is best-effort.** Across three observed sessions, one fired no
  `SessionEnd` at all. Garbage collection cannot rely on it alone.
- **No native field distinguishes the coordinator from a worker.** The `claude
  agents` launch and the dispatched workers all presented `source:startup`,
  `agent_type:claude`; the coordinator launch spuriously created an instance.

## Job-entry lifecycle across done/idle/delete (follow-up probe, 2026-06-27)

The original spike settled feasibility but left the garbage-collection
discriminator coarse: it knew `SessionEnd` was best-effort and keyed teardown on a
mapping, but it did not pin down what observable signal distinguishes "the session
ended / is gone" from "the session finished a task but is still resumable in the
Agent View." A follow-up probe on a live machine (against real
`~/.claude/jobs/*/state.json` plus a capture probe) settled it:

- **A finished background session stays resumable for a long time, with its job
  entry intact.** Three `template: "bg"` sessions in `state: "done"` had
  `firstTerminalAt` stamped 37 min – 2 h 13 m *before* their last heartbeat — they
  finished a task, recorded `done` + `firstTerminalAt`, and kept living (resumable)
  for hours, job entry present the whole time.
- **Explicit deletion is what removes the job entry.** A real dispatched
  `template: "bg"` worker (ephemeral mode on) went `done` + `firstTerminalAt`
  stamped, and its `~/.claude/jobs/<id>/` entry stayed present for 4+ minutes
  ("completed but resumable" in the Agent View). Pressing **Ctrl+X twice (delete)**
  removed the entry — the last recorded state was still `done` /
  `firstTerminalAt`-set, so removal was driven by the delete, not by the terminal
  state. A non-terminal interactive session deleted the same way also lost its
  entry.

**Conclusion (verified end to end):** completion and idle KEEP the
`~/.claude/jobs/<id>/` entry; explicit delete REMOVES it. So **entry-present** is a
faithful proxy for "the session still exists in the Agent View" and **entry-gone**
is a faithful proxy for "the developer deleted it." This is the discriminator the
GC must key on — not terminal `state`, not `firstTerminalAt`, not an idle TTL, each
of which is true of a live-but-resumable session.

**Key Claude Code facts (documented).** `SessionEnd` `reason` values are `clear`,
`resume` ("suspended for later resumption"), `logout`, `prompt_input_exit`,
`bypass_permissions_disabled`, and `other`. None uniquely means "the user deleted
the session" — deletion is observable only as the job entry disappearing. The
job-state file is undocumented and internal, so every reader must fail safe and
absent fields decode to zero.

**Not probed:** the ~1-hour Agent-View supervisor process-stop on a finished bg
session. Whether the job entry survives that stop is unknown; the DESIGN's
Decision 6 records how the contract handles that residual (accept it, with an
optional long-TTL backstop as a follow-up).

## Recommendation

Proceed to design. The achievable architecture, proven here:

- **Provision on start:** a `SessionStart` hook at the workspace root runs `niwa
  create`, records a `session_id → instance-path` mapping, and injects the new
  instance's context plus a "cd into PATH" instruction via `additionalContext`.
- **Work inside the instance:** the agent `cd`s in via the Bash tool; the
  instance's context arrives by injection, not relocation.
- **Garbage-collect on end:** a `SessionEnd` hook looks the instance up by
  `session_id` (never `cwd`) and runs `niwa destroy --force`, backstopped by a
  reaper that sweeps instances whose `SessionEnd` never fired. *(Superseded by the
  2026-06-27 follow-up above: `SessionEnd` fires on idle-suspend, not uniquely on
  delete, so the reaper became the SINGLE teardown path keyed on entry-gone =
  deleted. See DESIGN Decision 6 revision.)*

Two problems are design work, not feasibility unknowns: (a) a **coordinator-vs-worker
guard**, since no native hook field distinguishes them; and (b) a **GC model** keyed
on the session→instance mapping plus a reaper, since `SessionEnd`'s `cwd` is wrong
and its firing is best-effort. The supporting niwa primitives a design will need:
machine-readable `niwa create` output, a way to enumerate instances for the reaper,
an instance liveness/TTL marker, the mapping store, and the already-available
non-interactive `niwa destroy --force`.

## References

- tsukumogami/niwa, `docs/guides/worktree.md` — the existing per-repo Claude Code
  hook integration (`WorktreeCreate`/`WorktreeRemove` → `niwa worktree from-hook`)
  that this feature mirrors at the instance level.
- tsukumogami/niwa, `internal/cli/create.go`, `internal/cli/destroy.go` — the
  `niwa create` / `niwa destroy` lifecycle this feature drives.
