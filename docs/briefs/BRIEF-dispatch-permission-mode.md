---
schema: brief/v1
status: Accepted
problem: |
  A workspace can declare `permissions = "bypass"` in `workspace.toml`, but
  Claude Code 2.1.258 stopped honoring `defaultMode: "bypassPermissions"` when
  it is only set in a project's materialized `.claude/settings.json`. `niwa
  dispatch` forwards `--permission-mode` to a launched worker only when an
  operator passes it explicitly; nothing derives it from the workspace's own
  declared posture, so a bypass-configured workspace's worker silently starts
  in whatever mode the operator's own global Claude Code settings default to.
outcome: |
  A worker dispatched into a `permissions = "bypass"` workspace runs
  unattended: its tool calls execute without a human in the loop, and a
  coordinator's messages to it are not swallowed behind an approval prompt.
  An explicit `--permission-mode` still wins, and an unconfigured or
  `"ask"` workspace sees no change.
---

## Status

Accepted

Codebase research during PRD drafting (PRD-dispatch-permission-mode) found
that niwa's only other dispatch-capable agent, Codex, already gets an
equivalent trust posture through a separate mechanism
(`WorkdirGrantArgs`), and that its permission-equivalent flag (`--sandbox`)
takes a value vocabulary this feature's derived value would not fit. The
Scope Boundary below is updated accordingly: this feature covers Claude
Code workers only, narrower than the "non-Claude agents" language it
originally carried.

## Problem Statement

An operator who declares `permissions = "bypass"` in a workspace's
`workspace.toml` is stating a trust decision for every worker that runs
inside it: this workspace's dispatched sessions should act without needing
a human to approve each tool call. Until Claude Code 2.1.258, writing that
decision into the instance's materialized `.claude/settings.json` as
`permissions.defaultMode: "bypassPermissions"` was enough to carry it
through to a `niwa dispatch`-launched worker.

That is no longer true. Claude Code 2.1.258 changed which configuration
channels honor `defaultMode: "bypassPermissions"`: a value set in a
project's `.claude/settings.json` or `.claude/settings.local.json` is now
ignored, treated the same as `"auto"`. Only two channels still work — the
user's own (non-project) settings, or the `--permission-mode` CLI flag
passed at launch. `niwa dispatch` already has a `--permission-mode` flag
and already forwards it to the launched worker, but only when an operator
types it explicitly on the `niwa dispatch` invocation itself. Nothing in
the dispatch path reads the workspace's own declared `permissions` posture
and carries it forward as that flag.

The practical effect: a workspace configured for `bypass` gets a worker
that silently starts in whatever permission mode the *invoking user's own
global* Claude Code settings happen to default to — `plan`, `default`, or
whatever else that user has set for themselves, which has nothing to do
with the trust decision the workspace declared. Because dispatched workers
run headless, with no person present to click through an approval dialog,
every tool call that mode would otherwise gate on approval simply stalls.
That includes not just file edits and shell commands the worker itself
wants to run, but messages a coordinator session sends the worker after
launch — those arrive as tool calls too, and stall the same way. An
operator watching a dispatched worker do nothing has no direct signal that
the cause is a permission-channel change in the underlying agent, not a
bug in their workspace configuration.

## User Outcome

An operator who has declared `permissions = "bypass"` for a workspace
dispatches a worker into it and the worker actually behaves like a bypass
worker: it reads, edits, and runs shell commands as its task requires,
without stalling on approval prompts nobody is present to answer. A
coordinator session that sends the worker a message gets that message
acted on, not silently queued behind an unanswerable prompt.

An operator who passes `--permission-mode` explicitly on the `niwa
dispatch` invocation continues to get exactly the mode they asked for —
the explicit flag is still the final word, regardless of what the
workspace declares. And an operator working in a workspace with no
declared permission posture, or one explicitly set to `"ask"`, sees no
behavioral change at all: dispatch forwards no `--permission-mode` flag
from this path, exactly as it does today.

## User Journeys

### An operator dispatches into a bypass-configured workspace with no explicit flag

An operator whose workspace declares `permissions = "bypass"` in
`workspace.toml` runs `niwa dispatch "<task>" --name <slug>` with no
`--permission-mode` flag. The launched worker's argv carries the
equivalent of `--permission-mode bypassPermissions` (for a non-Claude
agent, whatever `agentplan.LaunchFlags` names as the equivalent mode), so
the worker starts able to act on its task without stalling on approval.

### An operator overrides the workspace's declared posture

An operator working in a `bypass`-configured workspace has a reason to
launch one worker more cautiously and runs `niwa dispatch "<task>"
--permission-mode acceptEdits`. The explicit flag is what reaches the
worker; the workspace's own `bypass` declaration does not override it.

### An operator dispatches from a workspace with no declared posture

An operator whose `workspace.toml` sets no `permissions` key (or sets it
to `"ask"`) runs `niwa dispatch "<task>"` as before. No `--permission-mode`
flag is forwarded from this path — the worker starts in whatever mode its
own settings and the invoking user's defaults already produce, unchanged
from today's behavior.

## Scope Boundary

**In:**
- Deriving a `--permission-mode` value for a `niwa dispatch`-launched
  worker from the workspace's own declared `permissions` posture, when the
  operator did not pass the flag explicitly.
- Preserving the existing precedence: an operator-supplied
  `--permission-mode` on the `niwa dispatch` invocation always wins over
  any workspace-derived value.
- Leaving today's behavior unchanged for a workspace that declares no
  permission posture, or declares `"ask"`.
- Covering Claude Code workers — the agent whose dispatch permission flag
  (`--permission-mode`) this derivation targets.
- **A purely internal fix.** An operator's side of this is unchanged: the
  same `workspace.toml` `permissions = "bypass"` declaration and the same
  `niwa dispatch` invocation they already use today, with no new flag, no
  new `workspace.toml` key, and no new step to learn. Niwa absorbs the
  Claude Code 2.1.258 channel change internally; nothing about how an
  operator interacts with niwa changes because of it.

**Out:**
- Changing how a workspace *materializes* `permissions.defaultMode` into
  an instance's `.claude/settings.json` — that write path is unaffected;
  this feature adds a second, still-honored channel for the same
  declared trust decision, it does not replace the first.
- Fixing the unrelated `permissionsMapping["ask"] = "askPermissions"`
  value in `internal/workspace/materialize.go`, which is an invalid
  Claude Code value tracked separately (niwa#257) and predates the
  2.1.258 channel change.
- Any change to how a workspace's `permissions` key is declared or
  validated in `workspace.toml` itself — the posture already exists and
  is already read elsewhere; this feature is about one more place that
  reads it.
- Reviving or reusing `internal/workspace/permissions.go`'s
  `WorkerPermissionMode` — it is dead code left over from the removed
  mesh daemon, and its fallback semantics (mapping every non-bypass case
  to `acceptEdits`) don't match this feature's requirement to forward
  nothing when bypass isn't configured.
- Deriving or forwarding any value for Codex-launched workers. Codex's
  dispatch permission-equivalent flag (`--sandbox`) takes a value
  vocabulary unrelated to `bypassPermissions`, and Codex workers already
  receive an equivalent trust posture through a separate mechanism
  (`WorkdirGrantArgs`'s `trust_level="trusted"` grant). This narrows the
  scope from this brief's original framing — see the Status section.

## References

- niwa#276 — the GitHub issue this brief frames.
- niwa#257 — the neighboring, unrelated `permissionsMapping["ask"]` bug in
  the same materialization code, called out here only to keep it out of
  this feature's scope.
