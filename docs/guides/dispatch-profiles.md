# Dispatch profiles

A dispatch profile trims one dispatched worker's context. It names tools to
deny, and can switch off Claude Code's bundled skills and the account's
connectors. `niwa dispatch --profile <name>` applies it to that one launch and
nothing else: every dispatch without the flag, every interactive session and
every other launch path starts exactly as it did before profiles existed.

The case it was built for is a coordinator, a long-running session that hands
work to other sessions and implements nothing itself. A coordinator pays for
its turn-0 context on every turn of a tenure that can run for days, and most
of that context is tools and skills it never calls. Under the coordinator
profile below, a backgrounded session's turn-0 context dropped from about 51K
tokens to about 21K.

## Declaring a profile

Profiles live in the workspace config, one table per profile under
`[session.dispatch_profiles]`. Like `[session.dispatch_env]`, the table is
read only at dispatch, never at apply, so adding or changing a profile needs
no `niwa apply`.

```toml
[session.dispatch_profiles.coordinator]
drop_tools = ["Artifact", "Workflow", "SendFeedback", "ReportFindings",
              "EnterWorktree", "AskUserQuestion", "Skill"]
drop_bundled_skills = true
drop_account_connectors = true
```

The three keys say what's removed, not how the agent spells it:

- `drop_tools` lists tool names to deny. A name is either a built-in tool,
  starting with an upper-case letter and holding only letters and digits
  (`Artifact`), or an MCP tool (`mcp__server_tool`). A scoped rule such as
  `Bash(git *)` is a permission rule rather than a removal, so it's refused.
- `drop_bundled_skills = true` leaves out the skills that ship with the agent.
- `drop_account_connectors = true` leaves out the connectors attached to the
  account, while the workspace's and plugins' MCP servers stay.

A profile name uses lowercase letters, digits and `-`, starting with a letter
or digit.

That example is the coordinator profile. Each item it drops was never called
across 17 coordinator tenures, except AskUserQuestion and Skill, which only a
session the owner starts needs: a dispatched coordinator reports by message,
and its entry skill reaches it another way (see
[A profiled coordinator](#a-profiled-coordinator)). It keeps Agent, ToolSearch,
SendMessage, ListAgents, the Cron tools, TaskStop, Monitor, ScheduleWakeup, the
rest of the core tools, and the workspace's and plugins' MCP servers.

### What the load refuses

A workspace config with any of these doesn't load, and every command that
reads it says why:

- a profile name outside the name grammar;
- a profile that drops nothing (no `drop_tools`, and neither switch set);
- a `drop_tools` name outside the tool-name grammar, or one listed twice;
- `ENABLE_CLAUDEAI_MCP_SERVERS` in `[session.dispatch_env]`, as a passthrough
  or an unset name, in any case. The profile owns that variable (see
  [Upgrading](#upgrading));
- `NIWA_DISPATCH_PROFILE` in `[session.dispatch_env]`. It's one of the names
  niwa sets itself.

## Dispatching with a profile

```bash
niwa dispatch --profile coordinator --name coord --detach "/<plugin>:coordinate <args>"
```

For a Claude worker, niwa adds the profile to the launch's one `--settings`
document, beside every other key the launch already carries:

- `permissions` holding only `deny`, the `drop_tools` names in declared order;
- `disableBundledSkills: true` when the profile drops bundled skills;
- `ENABLE_CLAUDEAI_MCP_SERVERS=false` in the document's `env` object and in
  the worker's process environment, when the profile drops account
  connectors.

The launch also sets `NIWA_DISPATCH_PROFILE` to the profile's name, in both
places. The post-launch audit lines include
`niwa dispatch: launched with dispatch profile "coordinator"`. The profile's
name goes on the launch record, which resume and retask read, and on the
session mapping, where `niwa list` shows a `dispatch profile:` line and
`niwa list --json` a `dispatch_profile` field.

niwa never writes a profile into a settings file. It rides the launch only.

### What the dispatch refuses

Each of these refuses before anything is provisioned, so a refused dispatch
leaves no instance behind:

- an unknown profile name. The message lists the declared names;
- a profile asked for when the workspace config didn't load;
- a profile asked for a Codex worker. Codex has no profile spelling yet, and
  `--profile` says so rather than launching a worker at full context with
  nobody told;
- a profile that drops a tool the agent's dispatch path needs.

### What a profile can't drop

For Claude, a profile may not drop:

- **ToolSearch.** It's how every deferred tool loads. Without it, SendMessage,
  the Cron tools, TaskStop and the rest could never be called.
- **CronCreate.** niwa's keep-alive is a recurring session cron, and
  `niwa list` and `niwa watch` recognize an armed keep-alive only by that
  cron. ScheduleWakeup can't stand in for it.

The rule holds whether or not keep-alive is on for the dispatch. Keep-alive
is decided only after the instance is provisioned, and a rule that waited for
it couldn't refuse before provisioning.

### A profile stops at its session

A profile applies to the session that asked for it, never to that session's
children. A dispatch that inherits `NIWA_DISPATCH_PROFILE` from its
environment, because the dispatcher is itself a profiled worker, removes the
marker and every variable a profile can set before shaping the worker's
environment. So an unprofiled dispatch from inside a profiled coordinator
starts its worker like one dispatched from a plain shell, with its account
connectors. Without the marker nothing is removed, so a developer who
exported `ENABLE_CLAUDEAI_MCP_SERVERS` themselves keeps it.

Only niwa launches do this. Anything else started from a profiled worker's
shell inherits the connectors switch: a `claude` opened by hand from inside
that shell starts without account connectors.

## A profiled coordinator

Under the coordinator profile, a coordinator can't call the Skill tool, so
its entry skill has to reach it another way. Whatever dispatches a
coordinator does two things:

- passes `--profile coordinator`;
- starts the prompt with the skill's slash invocation,
  `/<plugin>:coordinate <args>`, rather than a sentence asking the session to
  run the skill.

Claude Code expands a leading slash command itself, with the Skill tool
denied, in print mode and under `--bg`. A prompt that only asks the session
to run the skill would leave a profiled coordinator with no way to load it.

A dispatched coordinator under the profile is still told to use
EnterWorktree, by the workspace's session-start context and by the background
session's own system prompt, which asks for isolation before the first file
edit. With the tool denied it can't comply. The system prompt's fallback is
to continue in place when EnterWorktree fails, and coordinators don't edit
files, so the cost is those lines of context. A coordinator that does edit a
file works in its instance's checkout. The first live check reads the
transcript for a coordinator that stalls on the instruction instead.

## Resume and retask

`niwa resume` and `niwa retask` bring back a profiled worker under the same
profile. Both read the profile's name from the launch record and resolve it
against the workspace's current config, then check the recorded settings
document and environment against what it resolves to now:

- `NIWA_DISPATCH_PROFILE` must equal the record's profile, and
  `ENABLE_CLAUDEAI_MCP_SERVERS` must equal the value the profile sets. Both
  must be present on a profiled record, in the recorded environment and in
  the document's `env` object.
- `permissions` must hold only a deny list, every name in the tool-name
  grammar, equal to the profile's `drop_tools` in order. The shape is checked
  first, whatever the profile says, so an `allow` list or any other rule
  refuses.
- `disableBundledSkills` must equal what the profile sets, and every key the
  profile sets must be in the document.
- A record that names no profile may carry none of these.

So a resume refuses, naming the profile, when the recorded profile was removed
from the config or its drops changed. That's deliberate: the recorded settings
were made for a profile that no longer exists, and a resume never launches a
worker under settings the current config doesn't describe. Restore the
profile, or dispatch a fresh worker.

A resume relaunches with the recorded `--settings` document and environment
as they were. When it rewrites a session mapping, for instance after a reap
deleted it, the mapping carries the profile again.

`niwa retask` adds no code of its own here. It runs the same launch-record
check, so a changed or removed profile refuses a retask the same way. Its
wake adds no flag: Claude Code re-applies the options the job saved,
`--settings` included, and retask's saved-options check already compares the
saved document with the record.

Records made without a profile resume and retask exactly as before.

Name the worker by the session name niwa recorded, not the `--name` you
dispatched it with. niwa turns `-` into `_` and adds a suffix, so
`--name cdp-check` is recorded as something like `cdp_check-3e7ef6ea`, and
that's the name a retask takes. The dispatch prints it as the session name,
and `niwa list` shows it.

## Upgrading

A workspace that lists `ENABLE_CLAUDEAI_MCP_SERVERS` in
`[session.dispatch_env]`, as a passthrough or an unset name, no longer loads
after this upgrade. Remove the entry. To launch workers without account
connectors, declare `drop_account_connectors = true` in a profile and
dispatch with `--profile`. A passthrough would have fought the profile for
the variable and let a resume accept the switch on a record that names no
profile.

## Validation

### The three live checks

The unit tests pin what niwa puts in a launch. They can't show what Claude
Code does with it, so three checks run against a real build, on every Claude
Code upgrade too: a release that renames a dropped tool silently stops
dropping it. Run them from a workspace that declares the coordinator profile
above.

**Reading turn-0 context.** A worker's transcript is under
`~/.claude/projects/`, in the directory named for the instance path with
every character other than a letter or digit replaced by `-`, as
`<session id>.jsonl`. `niwa list --json` gives the session id. Turn-0 context
is the sum of `input_tokens`, `cache_creation_input_tokens` and
`cache_read_input_tokens` in the first assistant message's `usage`:

```bash
jq -s '[.[] | select(.type == "assistant" and .message.usage)][0].message.usage
       | .input_tokens + .cache_creation_input_tokens + .cache_read_input_tokens' \
   <transcript>.jsonl
```

MCP servers still connecting at turn 0 move the sum by a few thousand tokens.
Compare runs where the same servers had connected, or take the lower of two.
The model moves it too, so read it on the model coordinators run on.

**1. A profiled coordinator starts below 35K and keeps its tools.** Dispatch
a coordinator:

```bash
niwa dispatch --profile coordinator --model opus --name cdp-check --detach "/<plugin>:coordinate <args>"
```

Every dispatch in these checks names its model with `--model`, the one
coordinators run on, so the turn-0 figures compare and none of them depends
on a default. Its turn-0 sum must be below 35,000. Then retask the session,
which also exercises retask on a profiled worker, to load SendMessage,
CronCreate, CronList, CronDelete and TaskStop through ToolSearch and use each:
send a message to a named session; create, list and delete a cron; start a
background command and stop it. The transcript shows each call returning
without an error. Read the same transcript for two more things: the leading
slash invocation expanded into the skill's instructions, with Skill denied;
and the coordinator went on with its work rather than stalling on the
EnterWorktree instruction.

**2. A dispatch without the profile is unchanged.** Dispatch the same prompt
without `--profile`, with the same `--model`, twice: once from a plain shell,
and once from inside the profiled coordinator of the first check, through its
own Bash tool. Each
worker's turn-0 sum and tool list match a dispatch made with the previous
niwa, within the connecting-server variation above. For the worker dispatched
from inside the coordinator, its environment holds neither
`ENABLE_CLAUDEAI_MCP_SERVERS` nor `NIWA_DISPATCH_PROFILE`, its tool list
includes AskUserQuestion and Skill, and `/mcp` lists the account connectors.
The launch argv and `--settings` document are pinned by unit-test fixtures
recorded from the code before profiles existed.

**3. An interactive session is unchanged.** Start `claude` by hand in the
same instance, from your own terminal. AskUserQuestion and Skill are in its
tool list, `/mcp` lists the account connectors, and the skill listing holds
the bundled and account skills. niwa writes the profile into no settings
file, so this holds by construction; the check guards against a regression
that would put it there. A person has to look at this one: it's the
interactive session's own view.

### Where the checks stand

The design's trimmed launches ran on Claude Code 2.1.319, with the
coordinator profile passed as a launch settings document rather than through
`--profile`. A backgrounded session read 51.5K tokens at turn 0 without the
profile and 21.2K with it. Under the profile, ToolSearch loaded SendMessage,
CronCreate, CronList and TaskStop, and each worked. A leading slash command
expanded with Skill denied, under `--bg` too. `ENABLE_CLAUDEAI_MCP_SERVERS=false`
removed the account connector and left the plugin's and the workspace's MCP
servers.

The first two checks passed through `--profile` on Claude Code 2.1.325, with
every dispatch on `--model opus` from the same workspace and the same MCP
servers connected:

- The profiled coordinator read 27,300 tokens at turn 0. ToolSearch loaded
  SendMessage, CronCreate, CronList, CronDelete and TaskStop, and each call
  returned without an error. The leading slash command expanded with Skill
  denied, no account connector tool was listed, and the session went on
  without calling EnterWorktree. Its environment held
  `NIWA_DISPATCH_PROFILE=coordinator` and `ENABLE_CLAUDEAI_MCP_SERVERS=false`,
  and a retask of it came back under the same profile.
- Without the profile, a dispatch from the previous niwa read 56,888 tokens,
  one from this build in a plain shell read 56,918, and one from inside the
  profiled coordinator read 56,889. All three listed the same tools,
  AskUserQuestion, Skill and the account connectors' tools among them. None
  held either variable, and the plain dispatch's settings document carried no
  `permissions` or `disableBundledSkills` key.

The third check is still open. It's run by hand by the workspace owner and
tracked separately from the first two. It still has to show that a session
started by hand keeps its full tools: AskUserQuestion and Skill, the account
connectors in `/mcp`, and the bundled and account skills.

## See also

- [session-env-passthrough.md](session-env-passthrough.md) for
  `[session.dispatch_env]`, whose load refuses the names a profile owns.
- [ephemeral-session-instances.md](ephemeral-session-instances.md) for the
  launch store, `niwa resume` and `niwa retask`.
- [session-keep-alive.md](session-keep-alive.md) for the keep-alive cron a
  profile can't drop.
