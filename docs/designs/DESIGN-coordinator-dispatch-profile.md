---
schema: design/v1
status: Planned
problem: |
  A coordinator that niwa dispatches starts with every tool, skill listing,
  account connector and MCP server the host offers, and its turn-0 context is
  about 59K tokens. Across 17 measured coordinator tenures most of that surface
  was never called. niwa has no way to start one dispatched session with less
  while leaving other dispatches, and the sessions the owner starts by hand,
  exactly as they are.
decision: |
  A workspace declares named dispatch profiles under
  `[session.dispatch_profiles.<name>]`, and `niwa dispatch --profile <name>`
  selects one for that launch only. A Claude profile rides the launch
  `--settings` document niwa already sends, as a new contributor that adds a
  `permissions.deny` list of whole-tool names, `disableBundledSkills`, and
  `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the document's env block. The
  coordinator profile drops Artifact, Workflow, SendFeedback, ReportFindings,
  EnterWorktree, AskUserQuestion and Skill, the bundled skills, and the account
  connectors. It keeps Agent, ToolSearch, SendMessage, the Cron tools,
  TaskStop and the workspace's own MCP servers. The mapping records the profile
  name, and `niwa resume` accepts the profile's keys when they match what the
  recorded profile resolves to now.
rationale: |
  The launch settings document is the one per-launch channel niwa already owns,
  so nothing reaches a session that wasn't dispatched with the profile.
  Workspace or instance settings would also reach the owner's interactive
  sessions. A denylist of measured-unused items is backed by evidence for every
  entry. An allowlist would also drop tools nobody measured, and a coordinator
  can't recover a missing tool mid-tenure. Two trimmed `--bg` launches measured
  turn-0 context at 51.5K without the profile and 21.2K with it. Under the
  profile, SendMessage, the Cron tools and TaskStop loaded through ToolSearch
  and ran. The profile keeps every self-wake tool, so the keep-alive works
  whichever one a session picks. A profiled launch marks its environment, and
  a launch without the profile strips what a marked environment carries, so
  a worker a profiled coordinator dispatches starts as today.
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: Coordinator dispatch profile

## Status

Planned

## Context and Problem Statement

`niwa dispatch` launches a Claude worker as `claude --bg` with an argv that
`runDispatch` builds in `internal/cli/dispatch.go`. The argv carries
`--model`, `--permission-mode`, `--agent` and `--name`. It also carries one
`--settings` document, built from contributors (remote control, accepting
session messages, the launch environment, the MCP approval list) and rendered
once by `renderLaunchSettings` in `internal/cli/dispatch_settings.go`. Nothing
on that path removes anything from the session. The worker gets whatever the
host's settings, plugins and account give an interactive session.

For a coordinator that's a lot. A coordinator drives other sessions and
implements nothing itself, and its turn-0 context measures 59.2K tokens.
Across 17 coordinator tenures these items were never called:

- the Artifact, Workflow, SendFeedback, ReportFindings and EnterWorktree tools;
- the claude.ai account connectors;
- the built-in and account skills;
- the advisor.

Only the merge seat used the Agent tool. AskUserQuestion and the Skill tool
were needed only in a session the owner started by hand. Every token of that
surface is paid again on every turn of a tenure that can run for days.

The trim has to be per dispatch. A workspace-wide setting would also reach
the owner's interactive sessions, which must keep AskUserQuestion, the Skill
tool and every connector they have today. And a session dispatched without the
profile must start exactly as it does now.

## Decision Drivers

- **Per dispatch, never ambient.** Only a launch that asks for the profile
  gets it. Interactive sessions and other dispatches can't change at all.
- **Evidence per dropped item.** Each item a profile drops must have
  measurement saying the role doesn't use it. A wrong drop breaks a tenure,
  because the session has no way to get a tool back.
- **The coordinator's working tools survive.** SendMessage, the Cron tools and
  TaskStop are deferred tools that load through ToolSearch. The keep-alive niwa
  arms at dispatch has to keep working.
- **The profile doesn't travel down.** A coordinator's job is running
  `niwa dispatch`. A worker it dispatches without the profile must start as
  today, so nothing the profile puts in the coordinator's environment may
  reach that worker.
- **The profile survives resume and retask.** A coordinator brought back by
  `niwa resume` or handed its next task by `niwa retask` must come back under
  the same profile.
- **Agent-neutral dispatch path.** Per-agent facts stay in
  `internal/agentplan`, and the dispatch path consults a capability row, as
  remote control and inbound acceptance already do.

## Considered Options

### Decision 1: Which carrier the profile rides

Resolved inline. The profile has to reach Claude Code at launch through something niwa passes
to that one process.

**Chosen: a new contributor to the launch `--settings` document.** Claude
Code removes a tool from the model's tool list when a `permissions.deny` rule
names the whole tool. The `disableBundledSkills` settings key removes the
bundled skills. `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the env block stops the
account connectors from loading. The trimmed launches measured all three
arriving through `--settings`. The document is niwa's existing single
settings slot. It rides only the launched worker's argv, it already merges
several contributors into one value, and `niwa resume` already validates it.
Deny rules win over allow rules from any other settings source, so nothing in
the host's or the instance's settings can widen the trim back.

**Rejected: forwarding `--disallowedTools` or `--tools` on the argv.** Both
work. A trimmed launch with `--tools` loaded SendMessage, the Cron tools and
TaskStop through ToolSearch too. But each is a new forwarded flag that every
agent's `LaunchFlags`, resume's ordered options and retask's checked options
would have to learn, for no reach the settings slot lacks. `--tools` is an
allowlist, the shape Decision 2 rejects.

**Rejected: writing the profile into the workspace or instance settings
files.** Those files configure every session started in the instance,
including the owner's own. That breaks the third Evidence check by
construction.

**Rejected: running the coordinator as a custom `--agent` whose definition
lists its tools.** `--agent` is already the operator's passthrough for a
subagent type. An agent definition also replaces the session's system prompt,
which changes far more than the surface this design trims.

**Rejected: `--strict-mcp-config` for the connectors.** It drops every MCP
server not named in `--mcp-config`, including the workspace's own servers and
plugin servers that the profile has no evidence to drop.
`ENABLE_CLAUDEAI_MCP_SERVERS=false` drops only the account connectors. In the
trimmed launches the plugin's server and the workspace's server stayed.

### Decision 2: Which items are safe to drop

Resolved inline.

**Chosen: a denylist of the items measured as unused, plus two the owner's
role split settles.** The coordinator profile drops:

| Item | How it's dropped | Evidence |
|------|------------------|----------|
| Artifact, Workflow, SendFeedback, ReportFindings, EnterWorktree | `permissions.deny` | never called in 17 tenures |
| AskUserQuestion | `permissions.deny` | needed only in a session the owner starts; a dispatched coordinator reports by message |
| Skill (and with it the whole skill listing) | `permissions.deny` | needed only in a session the owner starts; a leading slash command in the prompt still expands with the tool denied (measured in print mode only; open for `--bg`, see Open Questions) |
| bundled skills | `disableBundledSkills: true` | never called in 17 tenures |
| account connectors | `ENABLE_CLAUDEAI_MCP_SERVERS=false` | never called in 17 tenures |

It keeps:

- Agent, which the merge seat uses;
- ToolSearch, which is how every deferred tool loads;
- SendMessage, ListAgents, CronCreate, CronList, CronDelete, TaskStop and
  Monitor, along with the rest of the core tools;
- ScheduleWakeup, which a profile may drop but has no reason to;
- the workspace's and plugins' MCP servers.

The advisor has no entry. Neither trimmed launch on the measuring host offered
an advisor tool, so there's nothing to deny and no name to measure a deny
against. Claude Code also flags a lowercase name in a deny list as an
unusable entry.

**Rejected: an allowlist of the tools a coordinator was seen calling.** It
fits the outcome's wording ("none of the rest") more literally, but it would
also drop every tool the 17 tenures didn't happen to need: PushNotification,
the Task tools, any tool a later Claude Code release adds. A coordinator that
needs one of those mid-tenure has no way to get it back. The denylist already
meets the turn-0 target with room to spare, and new entries can join it as
tenures show more unused items.

**Rejected: `skillOverrides` entries switching skills off one by one.** That
means about a hundred names that change whenever a plugin updates, while
denying the Skill tool drops the listing whole. The entry skill still reaches
the coordinator, because a prompt that begins with `/<plugin>:<skill>` is
expanded by the CLI rather than by the Skill tool.

### Decision 3: Where the profile is declared and how a dispatch selects it

Resolved inline.

**Chosen: `[session.dispatch_profiles.<name>]` in the workspace config,
selected by `niwa dispatch --profile <name>`.** Like `[session.dispatch_env]`,
it's read only at dispatch and never at apply. The table holds three keys:

```toml
[session.dispatch_profiles.coordinator]
drop_tools = ["Artifact", "Workflow", "SendFeedback", "ReportFindings",
              "EnterWorktree", "AskUserQuestion", "Skill"]
drop_bundled_skills = true
drop_account_connectors = true
```

The keys name what's removed, not how Claude Code spells it. `internal/agentplan`
maps them to the settings keys, so the workspace file states intent and the
spelling lives with the other per-agent facts. A tool name must match
`^[A-Z][A-Za-z0-9]*$` or `^mcp__[A-Za-z0-9_-]+$`. That refuses lowercase names
and scoped rules such as `Bash(git *)`: a scoped rule is a permission rule,
not a removal, and doesn't belong in a profile.

Several things refuse before anything is provisioned:

- an unknown profile name;
- a profile asked for on an agent whose `DispatchProfile` capability row isn't
  implemented (Codex today, with the row's reason). A profile asked for and
  silently not applied would leave a coordinator at full context with nobody
  told;
- a profile that drops ToolSearch, since no deferred tool could load;
- a profile that drops every tool able to schedule a self-wake (CronCreate and
  ScheduleWakeup for Claude) on a launch whose keep-alive resolved on, since
  the arming would have nothing to arm with.

The last rule doesn't depend on which tool a session picks. niwa's arming
text asks for "one recurring, session-scoped self-wake" and names no tool, so
the refusal names the whole set, and `internal/agentplan` holds that set as a
per-agent fact. The coordinator profile drops neither tool.

**Rejected: a `--profile` selected from a machine `[global]` default.** A
machine default would apply to every dispatch from that host, which breaks the
second Evidence check for every launch that didn't ask.

## Decision Outcome

A coordinator dispatched with `--profile coordinator` gets the denylist,
`disableBundledSkills`, and the connectors switch in its one launch settings
document. Other launches get today's document, byte for byte. Interactive
sessions read no file niwa changed.

## Solution Architecture

**Config (`internal/config`).** `SessionConfig` gains
`DispatchProfiles map[string]SessionDispatchProfile`. Load checks the profile
name (`^[a-z0-9][a-z0-9-]*$`), each tool name's grammar, and that a profile
declares at least one drop. Like the other `[session]` tables it lives in the
workspace config, and the vault resolver's deep copy clones it.

**Capability row (`internal/agentplan`).** A new `DispatchProfile` capability,
routed as a launch capability, is implemented for Claude and unsupported for
Codex with a reason. A function
`ProfileSettings(profile) (keys map[string]any, env map[string]string)`
returns Claude's spelling: `permissions: {deny: [...]}`,
`disableBundledSkills: true`, and `ENABLE_CLAUDEAI_MCP_SERVERS: "false"` for
the env block. Spelling it here keeps every agent name off the dispatch path,
which the existing dispatch-path scan enforces.

**Dispatch (`internal/cli/dispatch.go`).** `--profile <name>` is resolved
after the agent and before provisioning, so a refusal leaves nothing behind.
A fifth contributor adds the profile's keys to `launchSettings` after the MCP
approval contributor, and its env value joins the launch environment through
`launchEnvShape`, so `applyLaunchEnv` puts it in both the env block and the
process environment. With no `--profile`, the contributor adds nothing and the
rendered document is unchanged. A unit test pins that. Three more pieces:

- the session mapping gains `dispatch_profile`, and `niwa list` shows it;
- the audit line printed after the launch names the profile;
- the profile stops at the session it was asked for (below).

**Keeping the profile from travelling down.** A worker's environment starts
from the dispatcher's own (`os.Environ()` in `realDispatchLaunch`), and
`shapeLaunchEnv` removes only the reap key, the dispatch identity and the
`[session.dispatch_env]` unset names. A profiled coordinator carries
`ENABLE_CLAUDEAI_MCP_SERVERS=false` in its process environment and in its
settings `env` block, and Claude Code hands that environment to the commands
its tools run. Left alone, every worker the coordinator dispatched without
the profile would inherit the switch and start without its connectors, which
breaks the second Evidence check for exactly the dispatches a coordinator
makes.

So a profiled launch also sets a marker, `NIWA_DISPATCH_PROFILE=<name>`,
alongside the profile's env values, through the same `launchEnvShape`. On
every launch, `shapeLaunchEnv` checks the inherited environment for that
marker. When it's there, the launch removes the marker and every env name a
profile can set for that agent, which `internal/agentplan` lists as
`ProfileEnvNames()`, before it adds its own values. A profiled launch then
sets them again from its own profile. The result:

- a worker dispatched without `--profile` from inside a profiled coordinator
  gets neither the switch nor the marker, so it starts as a dispatch from a
  plain shell does;
- a worker dispatched from a shell without the marker inherits exactly what
  it inherits today. An owner who exported `ENABLE_CLAUDEAI_MCP_SERVERS`
  themselves keeps that choice, because nothing is removed without the
  marker;
- the argv and the `--settings` document of an unprofiled launch don't
  change at all. The removal touches only the process environment, and only
  when the marker is present.

The deny list and `disableBundledSkills` need no such step. They ride only
the profiled worker's argv, which no child process inherits.

**Resume (`internal/cli/resume.go`).** `checkResumeSettings` refuses every
key dispatch doesn't emit, so a profiled session couldn't be resumed today.
It learns three keys:

- `permissions`, accepted when it's exactly `{deny: [...]}`;
- `disableBundledSkills`, accepted when it's `true`;
- the connectors name in `env`.

Each is accepted only when it equals what the recorded `dispatch_profile`
resolves to in the workspace's current config, the same rule the env check
already applies to `[session.dispatch_env]`. A profile that has since been
removed or changed refuses the resume and names the profile. `niwa retask`
needs no change. Its flagless wake re-applies the options the job saved,
`--settings` included, and its option check already compares the saved
document with the record.

**The coordinator's dispatcher.** A coordinator under the profile can't
call the Skill tool. Its entry skill has to reach it another way, so whatever
dispatches a coordinator has to do two things:

- pass `--profile coordinator`;
- start the prompt with the skill's slash invocation,
  `/<plugin>:coordinate <args>`, rather than a sentence asking the session to
  run it.

Print-mode launches measured that a leading slash command is expanded by the
CLI with the Skill tool denied. Whether a `claude --bg` launch, which is how
coordinators run, expands it the same way hasn't been measured yet; see Open
Questions. A prompt that only asks the session to run the skill would leave a
profiled coordinator unable to load it in either case. The dispatcher is
the coordination skill's own dispatch step, which lives outside this
repository. It can't pass `--profile` before a niwa release carries the flag,
so the plan puts that change after the release.

**Docs.** A guide, `docs/guides/dispatch-profiles.md`, covers the table, the
flag, the refusals, what a profile can't drop, and the three checks below with
the Claude Code version they last passed on.

## Implementation Approach

The work goes in dependency order:

1. The config table and its load checks.
2. The capability row and Claude's spelling.
3. The dispatch flag and contributor, with the byte-identical no-profile test
   and the record field.
4. The resume acceptance.
5. The guide and the live checks, ending in a niwa release.
6. The coordinator's dispatcher passes the profile and leads its prompt with
   the slash invocation.

`docs/plans/PLAN-coordinator-dispatch-profile.md` has the issues.

## Evidence

These are the acceptance checks for the implementation. A reviewer runs them
against a workspace that declares the coordinator profile above.

**1. A coordinator under the profile starts below 35K and keeps its tools.**
Dispatch a coordinator with
`niwa dispatch --profile coordinator --name cdp-check --detach "<coordinator prompt>"`.
Open the worker's transcript under the Claude Code projects directory for the
instance path, and add `input_tokens`, `cache_creation_input_tokens` and
`cache_read_input_tokens` from the first assistant message's `usage`. The sum
must be below 35,000. Today's figure is 59,200. Then have the session load
SendMessage, CronCreate, CronList, CronDelete and TaskStop through ToolSearch
and use each one:

- send a message to a named session;
- create, list and delete a cron;
- start a background command and stop it.

The transcript shows each call returning without an error.

**2. A dispatch without the profile is unchanged.** Dispatch the same prompt
without `--profile`. The launch argv and `--settings` document match what
today's niwa produces for that workspace; the unit test in the plan pins
this. The worker's turn-0 sum and its reported tool list match a dispatch made
with today's niwa, within the variation that MCP servers still connecting at
turn 0 produce.

The check is run twice: once from a plain shell, and once from inside the
profiled coordinator of the first check, which runs the same unprofiled
`niwa dispatch` from its own Bash tool. Both workers must start exactly as
today. For the second one, the worker's environment holds neither
`ENABLE_CLAUDEAI_MCP_SERVERS` nor `NIWA_DISPATCH_PROFILE`, `/mcp` lists the
account connectors, and its tool list includes AskUserQuestion and Skill. A
unit test pins the environment half: a launch without `--profile`, from a
base environment carrying the marker and the switch, produces a worker
environment with neither.

**3. An interactive session is unchanged.** Start `claude` by hand in the same
instance. AskUserQuestion and the Skill tool are in its tool list, `/mcp`
lists the account connectors, and the skill listing includes the bundled and
account skills. niwa writes the profile into no settings file, so this holds
by construction. The check guards against a regression that would put it
there.

**What the trimmed launches measured.** These ran on Claude Code 2.1.319 from
an instance root, with the coordinator profile as a launch settings document.

- **Context.** A backgrounded session started at 51.5K tokens of turn-0
  context without the profile and 21.2K with it, a cut of about 30K. A
  coordinator measured at 59.2K today would land near 29K, which is a
  projection the first check confirms.
- **Deferred tools.** Under the profile, ToolSearch returned SendMessage,
  CronCreate, CronList and TaskStop. SendMessage delivered to another session,
  TaskStop stopped a background command, and CronList listed the job
  CronCreate made. A second trimmed launch is repeating this check; until it
  reports, this is one measurement (see Open Questions).
- **Keep-alive.** Both sessions, given niwa's keep-alive arming text, armed it
  with CronCreate, the profiled one with ScheduleWakeup denied. The design
  doesn't rest on this: the profile keeps both self-wake tools, and dispatch
  refuses only a profile that drops all of them.
- **The Skill tool.** In print-mode launches, denying it cut about 10K tokens
  on its own. A prompt beginning with a plugin skill's slash command still
  expanded that skill with the tool denied. This wasn't measured under
  `--bg`.
- **Connectors.** `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the settings env
  removed the account connector and left the plugin's and the workspace's MCP
  servers in place.

## Open Questions

These are measurements still to come back. Each names what changes if it
comes back the other way.

1. **Does a leading slash command expand under `claude --bg` with the Skill
   tool denied?** Measured only in print mode so far. If it doesn't, the
   coordinator profile can't deny Skill, the row comes out of Decision 2's
   table, and the turn-0 figure loses about 10K of its cut. The design's
   other choices don't move. The plan's step 5 live run answers it at the
   latest.
2. **Do deferred tools still load through ToolSearch under the deny list, on
   a second launch?** One trimmed launch said yes. A deny rule names only the
   tools it removes, so nothing in the design expects otherwise, but the
   carrier choice in Decision 1 is restated from the second launch's result
   before this design is accepted.

## Security Considerations

A profile only removes. A deny list can't grant a tool, so a planted or stale
profile can at worst leave a session with less than it needs, and the resume
check refuses a recorded profile that no longer matches the config. The
document still rides the worker's argv, where other users of the host can read
it in a process listing. The profile's values are tool names and a fixed
boolean switch, never credentials. The tool-name grammar keeps a profile from
smuggling a scoped permission rule into the document.

## Consequences

**Positive.** A coordinator tenure pays about 30K fewer tokens on every turn.
The selection is explicit on the dispatch line and recorded on the mapping.
Every other launch path is unchanged.

**Negative.**

- A profile is Claude Code's vocabulary: tool names and the connectors switch.
  A Codex profile needs its own spelling and its own measurements, and until
  then `--profile` refuses for Codex.
- A Claude Code release that renames a dropped tool silently stops dropping
  it. The guide's live check, rerun on upgrades, is what catches that.
- A dispatched coordinator under the profile sees a SessionStart hint about
  EnterWorktree that it can't act on. Coordinators implement nothing, so the
  hint costs nothing but the line.
- A coordinator's entry skill has to arrive as a slash command at the start of
  its prompt, since it can't call the Skill tool.
- Anything else started from a profiled coordinator's shell, other than a
  `niwa` launch, still inherits the connectors switch. An owner who opens
  `claude` by hand from inside that shell gets a session without account
  connectors. Owners start their sessions from their own terminals, so this
  is a note rather than a case the design handles.
