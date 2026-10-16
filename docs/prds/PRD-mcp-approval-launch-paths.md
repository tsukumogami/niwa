---
schema: prd/v1
status: In Progress
problem: |
  A developer who declares MCP servers in `[mcp.servers]` expects them running
  in every Claude Code session niwa sets up. Foreground sessions and hand-started
  background sessions in a trusted instance get them, but a `niwa dispatch`
  worker stops at startup on Claude Code's per-server approval dialog and waits
  for someone to attach, and an ephemeral session started at the workspace root
  never loads the servers at all, without saying so.
goals: |
  Dispatched workers start with the declared servers connected and no dialog,
  under every permission mode a host can set. Ephemeral sessions tell the agent
  up front which declared servers they lack and where to get them. The paths
  that already work keep working, and niwa's guide says which launch path gets
  the servers and which doesn't, verified against a named Claude Code version.
absorbed:
  - docs/briefs/BRIEF-mcp-approval-launch-paths.md
motivating_context: |
  The gap went unnoticed on a host whose dispatches ran in bypass mode. The
  first dispatch on a host set to `auto` stalled on the approval dialog even
  though niwa had trusted the instance and written the approval list.
---

# PRD: MCP server approval on every launch path

## Status

In Progress

Absorbed [BRIEF-mcp-approval-launch-paths](docs/briefs/BRIEF-mcp-approval-launch-paths.md); carried in Absorbed Brief.

## Absorbed Brief

The framing this PRD was written from, carried forward because the brief that
held it was folded in here.

The feature exists because niwa's promise about declared MCP servers is a
promise about sessions, not about files. niwa can write the `.mcp.json`, the
approval list and the trust entry correctly and still leave a developer with a
worker stalled on a dialog or a session missing its tools, because what
counts is what each launch path actually loads. So the framing measures
success per launch path: dispatch workers, ephemeral sessions, foreground
sessions and hand-started background sessions, each judged on whether the
servers arrive without a dialog.

The outcome has three parts. Unattended work, the reason dispatch exists,
must not wait on a person, under any permission mode a host can choose,
because a fix that only holds in bypass mode is one most hosts can't rely on.
Paths that already work must not regress. And where niwa can't deliver the
servers, the gap must be visible before a task depends on them, in the session
and in the guide, rather than discovered halfway through.

The boundary was drawn at framing time: niwa approves only servers it
generated itself, never writes the developer's user settings, and doesn't try
to fix Claude Code's own behavior, only to route around it in a way that keeps
working once that behavior is fixed.

## Problem Statement

A workspace can declare MCP servers in `[mcp.servers]`, in the base config or
the overlay. niwa generates them into the instance root's `.mcp.json`, lists
their names under Claude Code's `enabledMcpjsonServers` in the instance root's
`.claude/settings.json`, and, with `[claude] trust_instances`, marks the root
trusted in Claude Code's user config. Together these are meant to give every
session in the instance the servers without an approval dialog.

Checked by hand on Claude Code 2.1.301 and 2.1.302:

- `claude`, `claude -p` and a hand-started `claude --bg` at a trusted instance
  root all start the servers with no dialog, in the default and `auto`
  permission modes, including right after the instance is created.
- Every `niwa dispatch` worker tried stops at startup on "New MCP server found
  in this project", and the background daemon reports it as stuck on a startup
  dialog. Replaying the worker's command line, environment and prompt by hand in
  the same instance does not reproduce it, so the cause inside Claude Code is
  unknown. Adding the approval list to the instance's
  `.claude/settings.local.json` does not help. Sending the list in the launch's
  own `--settings` document does.
- An ephemeral session, a background session started at the workspace root
  that niwa's SessionStart hook gives its own instance, never loads the
  servers. Claude Code reads MCP configuration once at startup from the
  session's project, which stays the workspace root; the session only changes
  directory into its instance afterwards.

The two failing paths are the ones niwa uses for unattended work. A stalled
worker looks like a successful dispatch and sits indefinitely; a session
missing its servers finds out halfway through a task. Hosts in bypass mode hid
the first one, so most hosts would hit it.

## Goals

A developer who declares MCP servers gets them in the sessions niwa sets up,
with no approval dialog: dispatched workers under any permission mode,
foreground sessions, and hand-started background sessions in an instance.
Where niwa can't deliver them, the ephemeral session, the session itself says
so at start and names the alternatives, and the guide records the gap with its
reason. Nothing about the fix widens which servers are approved.

## User Stories

- As a developer whose `[global] dispatch_permission_mode` is `auto` (or
  `default`, `acceptEdits`, `plan`, `dontAsk`, `bypassPermissions`), I want
  `niwa dispatch --detach` to start a worker with the declared servers
  connected, so that the task runs without me attaching to approve anything.
- As a developer with ephemeral sessions enabled, I want a background session
  started at the workspace root to tell me, at start, which declared servers it
  won't have and how to get them, so that I don't discover a missing tool
  partway through a task.
- As a developer who starts `claude`, `claude -p` or `claude --bg` by hand in an
  instance, I want the declared servers to keep starting with no dialog, so
  that the dispatch fix doesn't cost me anything.
- As a niwa maintainer upgrading Claude Code, I want the guide to list each
  launch path with whether it gets the servers and the version that was last
  checked, so that I can rerun the check and see a regression.

## Requirements

### Functional

- **R1. Dispatch carries the approval.** Every Claude Code worker `niwa
  dispatch` launches, attached or detached, carries `enabledMcpjsonServers` in
  its launch `--settings` document. The value is the list niwa approved for
  that instance's root (the list it writes into the instance root's
  `.claude/settings.json`), minus any name R3 leaves out, sorted in ascending
  byte order. When that leaves no names, the document carries no such key, and
  a launch with no other launch setting has no `--settings` document at all.
  This holds whether or not `[claude] trust_instances` is on.
- **R2. The list comes from niwa's own approval decision.** The names are the
  ones niwa itself decides to approve from `[mcp.servers]` (base and overlay),
  computed from the workspace configuration at dispatch time. They are never
  read from a `.mcp.json`, from a file distributed verbatim, or from the
  instance's settings files, so editing those files after niwa wrote them
  can't add a name to the launch. niwa never sends
  `enableAllProjectMcpServers`.
- **R3. Only plain names are sent.** A name is sent only when it matches
  `^[A-Za-z0-9_-]{1,64}$`. Any other declared server is left out of the
  launch list, the dispatch continues, and niwa prints one line to stderr per
  such server, naming it (quoted) and the pattern. The value is always a JSON
  array of strings under the one fixed key.
- **R4. Independent of permission mode.** R1 produces the same list for every
  permission mode a dispatch can run under: each value of `[global]
  dispatch_permission_mode` (`default`, `acceptEdits`, `plan`, `auto`,
  `dontAsk`, `bypassPermissions`), an explicit `--permission-mode`, the
  workspace's recorded bypass posture, and no mode at all.
- **R5. Existing approvals stay.** For the same configuration, the instance
  root's `.mcp.json` and `.claude/settings.json`, each repository's
  `.claude/settings.local.json`, and the trust entries niwa writes to Claude
  Code's user config have the same contents as before this change, so
  foreground sessions and hand-started background sessions keep getting the
  servers.
- **R6. Ephemeral sessions say what they lack.** When the SessionStart hook
  provisions an instance for an ephemeral session and the workspace declares
  at least one MCP server, the context it injects carries one added paragraph
  that (a) lists the declared servers whose names match R3's pattern, plus a
  count of any others, (b) states that the session started at the workspace
  root, so it did not load the servers declared for the instance, and (c)
  names `niwa dispatch` and a session started inside the instance as the two
  ways to get them. When the workspace declares no servers, the injected
  context is unchanged.
- **R7. The guide states coverage per launch path.** The instance-trust guide
  has a coverage section listing four launch paths: `niwa dispatch` workers;
  ephemeral sessions; foreground `claude` and `claude -p` at an instance root
  or in a cloned repository; and `claude --bg` started by hand there. For each
  it says whether the declared servers load without a dialog; for ephemeral
  sessions it gives the reason and the two workarounds. It names the Claude
  Code version the R10 check last passed on.
- **R8. Other agents and launches are untouched.** A dispatch that launches
  Codex, or any agent without a settings flag, carries no approval and its
  command line is unchanged. `niwa watch` review launches are unchanged.

### Non-functional

- **R9. Automated tests.** Unit tests cover R1, R2, R3, R4, R6 and R8; a
  functional scenario using the existing fake `claude` that records the launch
  command line covers R1 and R4. All run in CI.
- **R10. Checked against a real Claude Code.** A manual check on a named Claude
  Code version, recorded in the instance-trust guide, confirms that a
  dispatched worker in a workspace declaring a server reaches its first turn
  with no startup dialog, under `auto` and under `default`.

## Acceptance Criteria

- [ ] A dispatch in a workspace declaring servers `zeta` and `example` launches
  with exactly one `--settings` document whose `enabledMcpjsonServers` is
  `["example","zeta"]`, and the instance root `settings.json` carries the same
  list.
- [ ] The same holds with `[claude] trust_instances` off.
- [ ] Attached (`niwa dispatch` without `--detach`) and detached dispatches
  carry the same list.
- [ ] A dispatch in a workspace declaring no servers launches with no
  `enabledMcpjsonServers` key, and with no `--settings` document when no other
  launch setting applies.
- [ ] A dispatch whose workspace distributes a `.mcp.json` only through a file
  table, with no `[mcp.servers]`, launches with no `enabledMcpjsonServers` key.
- [ ] A server declared only in the overlay appears in the launch list.
- [ ] Adding a name to the instance root's `settings.json` list or `.mcp.json`
  by hand after create does not add it to the launch list.
- [ ] With servers `ok` and `bad name` declared, the launch list is `["ok"]`,
  the dispatch exits 0, and stderr has exactly one line naming `"bad name"`.
  With only `bad name` declared, the launch carries no
  `enabledMcpjsonServers` key.
- [ ] The launch list is identical for dispatches under each of `default`,
  `acceptEdits`, `plan`, `auto`, `dontAsk`, `bypassPermissions`, an explicit
  `--permission-mode`, the recorded bypass posture, and no mode.
- [ ] The launch document never contains `enableAllProjectMcpServers`.
- [ ] When remote control, session-message acceptance and lineage apply, their
  keys and `enabledMcpjsonServers` are all in the one `--settings` document.
- [ ] A Codex dispatch's command line is the same as before this change.
- [ ] A `niwa watch` review launch's command line is the same as before this
  change.
- [ ] For a fixed configuration, the instance root's `.mcp.json` and
  `settings.json`, the repositories' `settings.local.json`, and the trust
  entries written are identical to those produced by the previous release.
- [ ] For a workspace declaring `alpha` and `beta`, the hook's injected context
  contains both names, the word `niwa dispatch`, and the instruction to start
  a session inside the instance; for a workspace declaring none, it is
  identical to the previous release's.
- [ ] The instance-trust guide's coverage section lists the four launch paths
  of R7 with their status and names the Claude Code version checked.
- [ ] The guide records the R10 check with the Claude Code version, and in it
  dispatched workers under `auto` and under `default` each reached their first
  turn with no startup dialog.
- [ ] The tests named in R9 exist and pass in CI.

## Out of Scope

- **Loading declared servers into ephemeral sessions.** It would need the
  servers, with their resolved secrets, materialized at the workspace root,
  which every session started there would then load, on root trust niwa
  refuses to grant itself. See Decisions and Trade-offs.
- **Servers from a verbatim `.mcp.json`.** niwa doesn't read files it copies
  and won't approve their servers.
- **User-level settings.** niwa doesn't write `enableAllProjectMcpServers` or
  any other user setting on the developer's behalf.
- **The cause inside Claude Code.** Why a dispatched worker ignores the
  instance's approval list is Claude Code's to find and fix. This work routes
  around it and must keep working once it is fixed.
- **`niwa watch` review sessions.** They run in deliberately untrusted
  instances and, under the sandbox, ignore the project's MCP configuration;
  approving servers there would weaken that containment.
- **Worktrees**, which niwa doesn't trust; unchanged.
- **Codex and other agent harnesses**, which have no settings flag and no
  per-server approval.

## Known Limitations

- The fix depends on Claude Code honoring `enabledMcpjsonServers` from a
  launch's `--settings` document. That was observed on 2.1.301, not documented
  by Claude Code, so a future version could change it; the manual check in R10
  is how a regression would be found.
- Ephemeral sessions still run without the declared servers; R6 makes the gap
  visible rather than closing it.
- A server whose name falls outside the documented character set loses its
  dispatch approval and will show the dialog in a dispatched worker. The
  warning is the only signal.

## Decisions and Trade-offs

- **Ephemeral sessions: a surfaced gap, not root materialization** (closes the
  framing's first deferred question). Making the servers load means generating
  them at the workspace root. Today root materialization gets the raw config,
  with no overlay merge, no vault resolution and no file tracking, so the work
  would move overlay and vault resolution out of the per-instance pipeline and
  leave one long-lived credential file at the root, loaded by every root
  session, coordinators included, on trust the developer granted by hand.
  Generating them without secrets would load the servers that need credentials
  broken. Rooting the session in its instance isn't possible from a hook. Telling the session what it lacks costs one
  addition to text the hook already emits and replaces a silent failure with a
  visible one. Revisit if Claude Code lets a hook relocate a session or add MCP
  servers, or root-scope apply gains vault resolution.
- **Permission mode doesn't change the approval** (closes the framing's second
  deferred question). The mode is passed as its own flag and never enters the
  settings document, so the approval is sent the same way under every mode.
  Whether Claude Code would have shown the dialog under a given mode doesn't
  matter: sending the list unconditionally makes the outcome the same on every
  host, which is the point of the work.
- **The launch document over the instance's settings files.** The instance's
  `settings.json` and `settings.local.json` both carry the list and a
  dispatched worker ignores both; the launch document is the one source
  observed to work. It also outranks the instance's files, which is why R2 and
  R3 bound what may enter it.
- **Source the names from niwa's decision, not from the settings file.**
  niwa already reads the dispatch's permission posture from its own saved
  state rather than from a generated settings file, so that a hand-edited file
  can't change what the launch carries. The approval list follows the same
  rule.
- **Drop and warn on an out-of-set name, not fail.** Failing the dispatch
  would break workspaces whose names worked before; dropping keeps the rest of
  the approval and the warning names what to rename.

## Downstream Artifacts

- `docs/designs/DESIGN-mcp-approval-launch-paths.md` (planned)
