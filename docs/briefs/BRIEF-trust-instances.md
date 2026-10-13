---
schema: brief/v1
status: Accepted
problem: |
  A non-bypass Claude Code session in a fresh niwa instance stops on a
  folder-trust dialog, then on an approval prompt per declared MCP server.
  Claude Code ignores project settings in an untrusted directory, so niwa's
  approval list does nothing, and every dispatch makes a new instance.
outcome: |
  A workspace author who opts in gets instances that come up already trusted:
  a session started in one, in any permission mode, asks nothing, and the
  declared servers and project settings apply as written. niwa takes back the
  trust it gave when the instance goes away, and touches nothing it didn't add.
motivating_context: |
  The workspace owner hit both prompts on a dispatched session that wasn't in
  bypass mode and asked for them to go away. niwa already writes the approval
  list for the servers it declares into the instance's project settings, and
  a check on an isolated HOME showed Claude Code reads that list only after
  the instance directory itself is trusted. A trusted parent doesn't count.
---

# BRIEF: Trust the instances niwa creates

## Status

Accepted

Framing for an opt-in that lets niwa mark its own instance roots trusted in
Claude Code, so the declared-server approval list it already writes takes
effect without a prompt.

## Problem Statement

niwa creates a new instance for every dispatch and for every `niwa create`.
Claude Code treats each of those directories as a stranger. When a developer
or a dispatched worker starts a session there in any mode other than bypass,
Claude Code first asks whether to trust the folder, and then asks whether to
allow each MCP server the instance's `.mcp.json` declares. Nobody chose to be
asked: the workspace author already put those servers in workspace config, and
niwa already generated the file from it.

niwa does try to answer the second question ahead of time. When it generates
an instance-root `.mcp.json` from `[mcp.servers.<name>]`, it also writes those
server names into the instance's `.claude/settings.json` as the approved list.
That list has no effect today. Claude Code honors project settings only in a
directory whose own trust entry is set in the user's Claude config, and a
trusted parent directory doesn't count. On a host with dozens of niwa
instances, every instance root shows up there untrusted with an empty approved
list.

So the cost lands three ways. A person at the keyboard answers the same two
prompts in every new instance. A session started without a person, in default
or accept-edits mode, stalls on them. And the one place a workspace author
could say "these servers belong here" is silently ignored. The only ways around
it today are bypass mode, which drops every permission check rather than just
these, or editing user-level Claude settings by hand on each host, which niwa
deliberately never touches.

## User Outcome

A workspace author turns the behavior on once, in workspace config, and from
then on the instances niwa creates or applies come up trusted. A developer who
opens a session in one, in default permission mode, goes straight to work: no
trust dialog, no server approval prompt, and the servers the workspace declared
are connected. A dispatched worker that isn't in bypass mode gets the same.

The author knows what they're agreeing to. Trusting an instance means Claude
Code applies that instance's project permissions, hooks and MCP servers without
asking first, and the docs say so where the option is described.

When niwa destroys or reaps an instance, it removes the trust it added, and an
entry a person created by hand is left as they left it. A workspace that never
turns the option on sees no change at all.

## User Journeys

### Workspace author opts in

A workspace author who maintains a team's workspace config adds the trust
option once, in the base config or in an overlay, because the team's dispatched
sessions keep stopping on prompts. The next `niwa create` trusts the new
instance root, and the next `niwa apply` in an existing instance trusts that one
too, so instances made before the change catch up without being recreated.

### Developer starts a default-mode session in a fresh instance

A developer runs `niwa create`, changes into the new instance, and starts
`claude` without any permission flag. The session opens with no trust dialog
and no MCP approval prompt, and the tools from the workspace's declared servers
are available on the first turn. The same holds for a worker niwa dispatches
into a fresh instance in a mode other than bypass.

### Instance reclaimed

A developer destroys an instance, or `niwa reap` reclaims one whose session is
gone. niwa removes the trust entry it added for that path from the user's
Claude config and leaves every other entry alone, including one the developer
had created by hand for the same path before niwa got there.

### Host with a Claude config niwa can't read

On a host where the user's Claude config exists but isn't valid JSON, `niwa
create` or `niwa apply` with the option on prints a warning naming the file,
changes nothing in it, and finishes the rest of the work. The developer gets
the old prompts on that host until the file is fixed, but never a damaged
config.

## Scope Boundary

### IN

- An opt-in workspace config key, off by default, that a base config or an
  overlay can set.
- Marking the instance root trusted in the user's Claude config on `niwa
  create` and on `niwa apply`, for every instance the option applies to.
- Removing, on destroy and on reap, only a trust entry niwa itself added.
- Leaving the user's Claude config untouched, with a warning, when it can't be
  parsed.
- Docs that state plainly what trusting an instance lets Claude Code do without
  asking.

### OUT

- Approving MCP servers. The declared-server approval list is written by
  separate, earlier work; this feature only makes Claude Code read it.
- Approving servers from a `.mcp.json` distributed verbatim, or writing an
  "approve every project server" setting. Neither is added here.
- Trusting the workspace root, repo directories inside an instance, or any
  path niwa didn't create as an instance root.
- Editing user-level Claude settings (`~/.claude/settings.json`) in any way.
- Trusting instances for agents other than Claude Code.
- Moving any particular workspace's servers into `[mcp.servers]`; that's the
  workspace author's change, made in their own config.
