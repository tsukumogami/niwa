---
schema: brief/v1
status: Accepted
problem: |
  The permission mode a dispatched worker launches with comes from the
  workspace config, so every machine running that workspace gets the same
  mode. A developer who wants bypass on one machine and not on another can
  only retype a flag on every dispatch.
outcome: |
  A developer states once, per machine, how trusted that machine's dispatched
  workers are, and every dispatch there follows it without a flag. The same
  workspace config yields the right mode on each machine, and a flag still
  overrides it for a single dispatch.
motivating_context: |
  A developer runs the same workspaces on a corporate laptop, where they don't
  want workers in bypass mode, and on personal machines, where they do. The
  workspace declares bypass, so the laptop's workers get it too unless the
  developer remembers a flag every time.
---

# BRIEF: dispatch-default-permission-mode

## Status

Accepted

## Problem Statement

How much a dispatched worker may do without asking is a decision about the
machine it runs on and the person who owns that machine. niwa makes it a
decision about the workspace. `niwa dispatch` derives the worker's permission
mode from the workspace's declared permissions posture, and that posture lives
in the workspace config that every clone of the workspace shares. A developer
who runs the same workspaces on several machines gets the same mode on all of
them.

That is the wrong answer as soon as the machines differ. A developer may be
fine with fully unattended workers on a personal dev box and not want them on
a company laptop, where policy or plain caution says otherwise. Today the only
way to get a different mode on one machine is to pass `--permission-mode` on
every dispatch from it. Forget once and the worker runs with the workspace's
mode. Change the workspace instead and every other machine loses the mode its
owner wanted.

The workspace setting also can't express what the developer actually wants on
the restricted machine. It knows two values, bypass and ask, and neither is
Claude Code's auto mode, which is the middle ground a developer reaches for
when bypass is off the table but stopping on every tool call is too much.

Dispatch to Codex has a related gap. The flag that does exist is forwarded to
a Codex worker as-is, under a flag that takes an unrelated set of values, so a
mode that is right for Claude breaks a Codex launch. A developer who uses both
agents can't set one answer for the machine and trust it to mean something
sensible for each.

## User Outcome

A developer tells niwa once, on each machine, how much trust that machine's
dispatched workers get. On the corporate laptop that's auto mode; on the
personal machines it's bypass. From then on every `niwa dispatch` on that
machine launches workers in the stated mode without a flag, whichever
workspace it dispatches from, and nothing about the shared workspace config
has to change or diverge between machines.

When one dispatch needs something different, the developer passes
`--permission-mode` and that dispatch gets it, exactly as the flag works
today. When a machine has no preference recorded, dispatch behaves the way it
does now, so nobody's existing setup shifts under them.

Every dispatch says on stderr which mode it chose and where that came from:
the flag, the machine's preference, or the workspace. A developer who wonders
why a worker is or isn't asking can check without guessing.

A developer who uses both Claude and Codex sets one answer per machine, and a
Codex dispatch never fails because of a preference that only makes sense for
Claude.

Workspace maintainers stop carrying a setting that was never theirs to make.
The workspace-level posture is on its way out, and while it still exists a
machine preference outranks it.

## User Journeys

### Journey 1: Developer setting up a restricted machine

A developer gets a new corporate laptop and clones the same workspaces they
use at home. Before their first dispatch they record, in niwa's per-machine
config, that this machine's dispatched workers run in auto mode. Their next
dispatch from a workspace that declares bypass launches the worker in auto,
and stderr says the mode came from the machine's setting. They never touch
the workspace config.

### Journey 2: Developer on an unrestricted machine

The same developer sets up a personal machine and dispatches from a
workspace that declares nothing about permissions. Today that worker would
launch with no mode at all and stop to ask. They record bypass as this
machine's preference, and from then on its workers run unattended from every
workspace, because the machine's preference fills in where the workspace is
silent.

### Journey 3: One-off override

On the laptop, the developer wants one worker to stop and ask before every
action because the task touches something sensitive. They pass
`--permission-mode default` on that one dispatch. That worker launches in the
mode the flag names, stderr says the flag decided, and the next dispatch
without a flag is back in auto.

### Journey 4: Developer dispatching to Codex

A developer whose machine preference is bypass dispatches a task to Codex.
The worker launches the way Codex workers launch today, with the sandbox and
trust arrangement niwa already gives them, instead of a Claude mode name
pushed through Codex's sandbox flag. The Claude-only preference doesn't cost
them a failed launch.

### Journey 5: Workspace maintainer reading a deprecation notice

A maintainer of a shared workspace config that declares a permissions posture
runs `niwa apply` and sees a notice that the workspace-level posture is
deprecated in favor of the per-machine setting, naming where that setting
lives. Their workspace keeps working in the meantime; they can drop the key
when they're ready.

## Scope Boundary

### In

- A per-machine default for the permission mode of `niwa dispatch` workers,
  recorded in niwa's per-machine config rather than in any workspace
- Precedence: the existing `--permission-mode` flag, then the machine
  default, then the workspace posture while it still exists, then nothing
- What the machine default means for both Claude and Codex workers, so one
  answer per machine is safe for either agent
- A stderr line on every dispatch naming the chosen mode and its source
- Deprecating the workspace-level permissions posture: a notice and
  continued support below the machine default

### Out

- `niwa watch` review sessions: they run under their own containment setting
  and forward no permission mode today; this feature doesn't change that
- Ephemeral sessions started by the SessionStart hook: they aren't launched
  by dispatch
- Per-workspace overrides inside the machine config: the point is one answer
  per machine, not a second per-workspace layer
- Detecting whether a machine allows a given mode: the developer knows their
  machine's policy, and niwa takes the stated preference as given
- Removing the workspace posture outright: this feature deprecates it; the
  removal is later work once its interactive-session effect has a home
