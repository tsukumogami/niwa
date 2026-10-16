---
schema: brief/v1
status: Accepted
problem: |
  niwa pre-approves the MCP servers a workspace declares, but the servers only
  reach some of the ways a Claude Code session can start. A `niwa dispatch`
  worker stops on a per-server approval dialog nobody is there to answer, and
  an ephemeral session never loads the servers at all.
outcome: |
  A developer who declares MCP servers in their workspace gets them running in
  the sessions niwa sets up, however they were started and whatever the host's
  permission mode is, with no approval dialog and no stalled worker. Any path
  niwa can't cover is named in its documentation, with the reason.
motivating_context: |
  On a host whose dispatch permission mode was bypass, the dialog never
  appeared, so the gap went unnoticed. The first dispatch on a host set to
  auto stalled at startup on the approval dialog, even though niwa had trusted
  the instance and written the approval list.
---

# BRIEF: MCP server approval on every session launch path

## Status

Accepted

Two framing questions are left to the downstream PRD. First, an ephemeral
session's project is the workspace root, so its MCP configuration is the
root's: should niwa make the declared servers available at the workspace root
for those sessions, given that every other session started there would get
them too, or record the path as a known gap? Second, does every permission
mode behave the same once the approval arrives through a dispatch's launch
settings, or does any mode add a prompt of its own?

## Problem Statement

A workspace can declare MCP servers in `[mcp.servers]`. niwa generates them
into the instance root's `.mcp.json`, writes their names into Claude Code's
`enabledMcpjsonServers` list in the root's `.claude/settings.json`, and, when
the workspace opts in with `[claude] trust_instances`, marks the root trusted
in Claude Code's user config. The point of all three is that a session starts
with those servers running and nobody has to click through an approval dialog.

That promise holds for some launch paths and not others. Checked by hand on
Claude Code 2.1.301 and 2.1.302, against freshly created, trusted instances:

1. `claude` started interactively at an instance root starts the servers
   with no dialog, and so does `claude -p`.
2. `claude --bg` started by hand at an instance root does the same, in the
   default and `auto` permission modes, including immediately after the
   instance was created.
3. A worker launched by `niwa dispatch` stops at startup on "New MCP server
   found in this project" and waits; the background daemon reports it as
   stuck on a startup dialog. It happens on every dispatch tried, on both
   versions. Replaying the worker's exact command line, environment and
   prompt by hand, in the same instance, does not reproduce it, so the cause
   is not yet known. Writing the approval list into the instance's
   `.claude/settings.local.json` as well doesn't help. Sending the same list
   in the launch's own `--settings` document does: the worker starts with
   the servers connected.
4. A background session started at the workspace root, which niwa's
   SessionStart hook gives its own ephemeral instance, never loads the
   declared servers. Claude Code reads MCP configuration once, at startup,
   from the session's own project, and that project is the workspace root,
   which has no `.mcp.json`. The session moves into its instance only
   afterwards, by changing directory.

So the two launch paths niwa uses for unattended work are the two where the
servers don't arrive. A stalled worker is worse than a missing server: the
dispatch looks successful, the session sits there indefinitely, and nothing
reaches the developer until they go and look. A missing server is quieter
but no better for a task that needs it.

The host's default permission mode hid this. A host whose dispatches run in
bypass mode never shows the dialog, so the gap only appears once a host uses
auto, default, or another mode that still asks. A feature that only works
under bypass is one most hosts can't rely on.

## User Outcome

A developer who declares MCP servers in their workspace config, or its
overlay, gets those servers running in the Claude Code sessions niwa sets up
for them, without an approval dialog. That holds whether they start the
session themselves at an instance root, dispatch a worker with `niwa
dispatch`, or start a background session at the workspace root that niwa
turns into an ephemeral instance. It holds whatever the host's dispatch
permission mode is. Where a launch path is outside niwa's reach, the developer
can find out which one and why from niwa's own documentation, before a worker
stalls on it.

## User Journeys

### A developer dispatches a worker on an auto-mode host

A developer whose `[global] dispatch_permission_mode` is `auto` runs `niwa
dispatch "<task>" --detach` in a workspace that declares an MCP server. The
worker starts, the server is connected, and the task runs to its end. The
developer never attaches to the session to approve anything, and `niwa list`
never shows the worker sitting at a startup dialog.

### A developer starts a background session at the workspace root

A developer with ephemeral sessions enabled starts a background Claude Code
session at the workspace root. niwa's SessionStart hook provisions a fresh
instance and the session moves into it. The workspace's declared servers are
available in that session, with no approval dialog waiting in the background
session list; or, if niwa can't make them available on this path, the
developer learns that from niwa's documentation, not by finding a tool missing
halfway through a task.

### A developer starts a background session by hand in an instance

A developer in an existing instance root runs `claude --bg "<task>"` without
going through niwa. The session connects the declared servers and works on
the task, as it does today. Nothing niwa changes for the dispatch path makes
this one regress.

### A developer starts a foreground session by hand in an instance

A developer opens a terminal in an existing instance root, or in a repository
niwa cloned into it, and runs `claude` or `claude -p`. The servers start with
no dialog, as they do today. Nothing about the fix makes this path worse or
adds a second approval for the same server.

### A maintainer checks which launch paths are covered

A niwa maintainer upgrading Claude Code, or a developer whose session still
asked for approval, opens niwa's instance-trust guide. It lists each launch
path, says whether niwa pre-approves the declared servers on it, and for any
path niwa can't cover, says why and what the developer can do instead.

## Scope Boundary

**IN:**

- Every way a Claude Code session starts in an instance niwa set up:
  `niwa dispatch` workers (attached and detached), ephemeral background
  sessions provisioned from the workspace root by the SessionStart hook,
  background sessions a developer starts with `claude --bg` at an instance
  root or inside a cloned repository, and foreground sessions started there,
  both interactive and `-p`.
- Behavior that is the same under every permission mode a host can set for
  dispatch (`default`, `acceptEdits`, `plan`, `auto`, `bypassPermissions`),
  so a host in bypass mode is no longer the only one where it works.
- Only the servers niwa itself generated from `[mcp.servers]` in the base
  config or the overlay. The approval list stays bounded to those names.
- A way to confirm, launch path by launch path, that the declared servers
  arrive without a dialog, which a maintainer can rerun after a Claude Code
  upgrade.
- Updating the instance-trust guide's coverage section to match.

**OUT:**

- Servers from a `.mcp.json` distributed verbatim through the file tables.
  niwa doesn't read files it copies and won't approve their servers; that
  existing rule stays.
- Approving every project server on the machine (`enableAllProjectMcpServers`
  or any user-level setting niwa would write on the developer's behalf).
  niwa doesn't edit the developer's user settings for this.
- Finding and fixing the underlying Claude Code behavior that makes a
  dispatched worker ignore the instance's approval list. That is Claude
  Code's to fix; this work routes around it, and must keep working once it is
  fixed.
- Worktrees, which niwa doesn't trust today. A session started directly in
  one still asks, and that is unchanged by this work.
- Codex and any other agent harness. Their MCP configuration is separate.

## References

- `docs/designs/current/DESIGN-trust-instances.md` -- the design that added
  instance trust and declared-server approval.
- `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` -- how
  declared servers reach the instance root's `.mcp.json`.
- `docs/designs/current/DESIGN-ephemeral-session-instances.md` -- the
  SessionStart hook that moves a root-launched session into a new instance.
- `docs/designs/current/DESIGN-dispatch-default-permission-mode.md` -- the
  per-machine dispatch permission mode.
- `docs/guides/instance-trust.md` -- the guide whose coverage section this
  work updates.
