---
schema: brief/v1
status: Accepted
problem: |
  niwa registers Claude plugins for every repo of every workspace
  instance but never cleans up the install records it causes Claude
  Code to write, and it force-enables marketplace auto-update. Over
  time the user's plugin registry fills with stale records pointing at
  deleted paths, which intermittently breaks skill registration.
outcome: |
  niwa leaves the Claude plugin registry in a state that keeps skills
  registering reliably: the records it causes are cleaned up over their
  lifecycle, and auto-update is no longer forced in a way that hastens
  the breakage. The user stops seeing shirabe skills vanish mid-session.
motivating_context: |
  A multi-day investigation traced "shirabe skills randomly fail to
  register / de-register, fixed by /reload-plugins" to niwa's plugin
  handling: 111 install records for one plugin, 109 dangling. The
  symptom worsened as the user's workspace-instance count grew.
---

# BRIEF: niwa plugin record lifecycle

## Status

Accepted

The framing here is the problem, the user-visible outcome, and the
boundary between what niwa owns and what belongs to Claude Code. The
approach choices (where pruning hooks in, the concurrency-safety
contract, the auto-update default, the name-keying scope, and whether
per-repo enablement can be reduced) are carried forward to the
downstream PRD's Decisions and Trade-offs section.

## Problem Statement

Claude Code keeps a global plugin registry at
`~/.claude/plugins/installed_plugins.json` and writes one record per
`(plugin, project path, scope)` the first time a session in that path
enables the plugin. Claude Code owns that file and never removes a
record when the project path or the cached plugin version it points at
disappears. That weakness is dormant until something produces many
short-lived project paths and churns cached versions.

niwa is exactly that something. It manages many workspace instances
over time, and for each instance it writes plugin enablement into every
repo subdirectory — so each repo of each instance becomes a distinct
project path, and each becomes a distinct registry record. niwa then
tears those instances down without removing the records, and it
force-enables marketplace auto-update, which keeps cached plugin
versions turning over so Claude Code's own cache sweep deletes the old
ones. The records that pointed at those paths and versions are left
behind as dangling pointers.

The accumulated result is a registry where the overwhelming majority of
a plugin's records are dead. When a new session resolves that plugin at
startup, resolution runs against a pile of mostly-broken records and
intermittently lands on a dead one, so the plugin's skills fail to
register. The user sees workflow skills disappear, `/plugins` report an
error, and a manual `/reload-plugins` temporarily paper over it. The
failure is non-deterministic and grows worse the longer the user works
across workspace instances — which is the normal way niwa is used.

niwa does not own the registry file, and the deepest cause is Claude
Code's missing garbage collection. But niwa is the actor whose behavior
turns a dormant weakness into a daily, worsening failure, and it is the
actor that can stop doing so.

## User Outcome

A developer who runs many niwa-managed workspaces over days and weeks
keeps their workflow skills registering reliably. Skills do not vanish
mid-session, `/plugins` does not surface registry errors, and the user
no longer reaches for `/reload-plugins` as a routine workaround. The
plugin registry stays proportional to the workspaces that actually
exist, rather than growing without bound as instances come and go.

The change is invisible when it works: the user simply stops
experiencing the flapping they had learned to tolerate. niwa cleans up
the plugin state it is responsible for, the same way it is expected to
clean up the workspace files it creates.

## User Journeys

### An operator tears down a finished workspace instance

An operator finishes a feature across a niwa workspace instance and
destroys it. Today the instance's directories are removed but its plugin
records linger in the global registry, becoming dangling the moment
their cached versions are swept. In the intended outcome, destroying the
instance also retires the plugin records that instance caused, so no
dead pointers are left behind for a future session to trip over.

### A developer opens a fresh session and finds skills registered

A developer starts a brand-new session in a workspace and expects the
workflow skills to be available. Today resolution can land on one of
many dead records and the skills fail to register until a reload. In the
intended outcome, the registry no longer carries an accumulated backlog
of dead records, so first-session resolution finds valid state and the
skills register on the first try.

### An operator recovers a registry that has already accumulated damage

An operator who has worked across many instances for weeks finds the
registry already full of stale records from instances long gone. Rather
than hand-editing a Claude-owned file, the operator runs a niwa
operation that detects and retires the dead records, restoring reliable
registration without manual surgery.

### A marketplace author avoids forced auto-update churn

A developer who maintains a marketplace they also consume is hurt by
niwa force-enabling auto-update on it: every change they push churns
cached versions and accelerates the dangling-record problem in their
other live sessions. In the intended outcome, auto-update is a choice
rather than an imposition, so a locally-developed marketplace stays
stable for daily use.

## Scope Boundary

**In:**

- niwa retiring the plugin install records it is responsible for, over
  the workspace-instance lifecycle (creation through teardown).
- A recovery path for registries that have already accumulated dead
  records, so existing damage is repairable without manual file edits.
- Making marketplace auto-update a configurable choice rather than a
  forced default, so niwa stops accelerating the churn.
- Keeping the registry proportional to the workspace instances that
  currently exist.

**Out:**

- Fixing Claude Code's own missing garbage collection. The registry
  file is Claude Code's; the durable cross-vendor fix is an upstream
  report, tracked separately. This work is niwa mitigating its own
  amplification, not repairing Claude Code.
- Redesigning how niwa enables plugins per repo versus per instance.
  Reducing per-repo record proliferation is an attractive lever but
  depends on Claude Code's plugin-scoping semantics; whether to pursue
  it is a downstream investigation, not a commitment this brief makes.
- Changing which plugins or marketplaces niwa installs, or the content
  of any plugin. The feature concerns record lifecycle and update
  policy, not the plugin set.
- The one-time manual registry cleanup already performed during the
  investigation. That unblocked the user; this feature makes the fix
  systematic.

## References

- `internal/workspace/materialize.go` — writes plugin enablement into
  each repo's settings (the proliferation source).
- `internal/workspace/destroy.go`, `internal/workspace/destroy_workspace.go`
  — teardown paths that currently leave registry records behind.
- `internal/workspace/workspace_context.go` — marketplace registration
  that force-enables auto-update.
