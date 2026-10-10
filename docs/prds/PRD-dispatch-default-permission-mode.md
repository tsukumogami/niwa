---
schema: prd/v1
status: In Progress
problem: |
  `niwa dispatch` takes a worker's permission mode from the workspace
  posture, which every machine running that workspace shares. A developer
  who wants bypass on some machines and not on others can only pass
  `--permission-mode` on every dispatch from the restricted one, and the
  workspace posture can't express Claude Code's auto mode at all.
goals: |
  A developer records one permission mode per machine, and every dispatch on
  that machine launches its workers in that mode without a flag, from any
  workspace. The flag still overrides it for one dispatch, a Codex dispatch
  never breaks because of it, and machines with nothing recorded behave
  exactly as they do today.
absorbed:
  - docs/briefs/BRIEF-dispatch-default-permission-mode.md
---

# PRD: dispatch-default-permission-mode

## Status

In Progress

Absorbed [BRIEF-dispatch-default-permission-mode](docs/briefs/BRIEF-dispatch-default-permission-mode.md); carried in Absorbed Brief.

## Absorbed Brief

How much a dispatched worker may do without asking is a decision about the
machine it runs on and the person who owns that machine, not about the
workspace. The brief framed it that way after a developer who runs the same
workspaces on a corporate laptop and on personal machines found no way to
keep bypass off the laptop and on everywhere else, short of typing a flag on
every dispatch.

The outcome it set: the developer states once, per machine, how trusted that
machine's dispatched workers are (auto on the laptop, bypass at home), and
every dispatch there follows it from any workspace with no change to the
shared workspace config. A flag still overrides it for one dispatch, a
machine with nothing recorded behaves as before, a Codex dispatch never fails
because of a Claude-only preference, and stderr says where the chosen mode
came from. Workspace maintainers are told the workspace-level posture is on
its way out, and while it exists the machine preference outranks it.

The brief's boundary is carried by this PRD's requirements and Out of Scope:
dispatch only (not `niwa watch` or hook-provisioned sessions), one answer per
machine with no per-workspace layer inside the machine config, no detection
of what a machine allows, and deprecation rather than removal of the
workspace posture.

## Problem Statement

Two terms are used throughout. The **workspace posture** is the
`[claude.settings] permissions` value a workspace config declares, `bypass`
or `ask`, which niwa copies into each instance's state when it creates or
applies the instance. A **permission mode** is a value of Claude Code's
`--permission-mode` flag, such as `auto` or `bypassPermissions`.

`niwa dispatch` picks a worker's permission mode in two steps today. An
explicit `--permission-mode` wins. Otherwise, when the launched agent is
Claude and the instance's workspace posture is `bypass`, the worker gets
`--permission-mode bypassPermissions`. In every other case no mode is
forwarded and the worker starts in Claude Code's default mode, which asks
before acting.

The workspace posture lives in the workspace config, and every clone of the
workspace shares it. A developer who runs the same workspaces on a corporate
laptop and on personal machines gets the same mode on all of them. To keep
bypass off the laptop they have to pass `--permission-mode` on every dispatch
from it; one forgotten flag and the worker runs in bypass anyway. Changing
the workspace instead takes bypass away from every other machine.

The workspace posture also has only two values. Neither is Claude Code's auto
mode, which is what a developer wants on a machine where bypass is off the
table but a worker that stops on every tool call is no use.

The developer is the only one who knows which mode a machine should get, and
niwa has nowhere to record that answer per machine.

## Goals

- A developer sets the dispatch permission mode once per machine, and it
  applies to every dispatch on that machine without a flag.
- The same workspace config produces the right mode on every machine.
- The explicit flag keeps working as a per-dispatch override.
- A machine setting that only makes sense for Claude never breaks a Codex
  dispatch.
- A developer can always tell which mode a worker got and why.
- Machines with no setting behave exactly as they do today.
- The workspace posture starts its deprecation, so the decision moves to the
  machine that owns it.

## User Stories

- As a developer on a corporate laptop where I don't want bypass, I want every
  dispatched worker on this machine to run in auto mode, so that I don't have
  to remember a flag and can't slip into bypass by forgetting one.
- As the same developer on a personal machine, I want dispatched workers to run
  in bypass even from workspaces that declare no posture, so that background
  work never stops to ask.
- As a developer with a machine setting, I want `--permission-mode` on one
  dispatch to win over it, so that a sensitive task can run under a stricter
  mode without changing my setup.
- As a developer who dispatches to both Claude and Codex, I want one machine
  setting to be safe for both, so that a Codex dispatch launches the way it
  does today.
- As a developer wondering why a worker did or didn't stop to ask, I want
  dispatch to tell me the mode it chose and where it came from, so that I
  don't have to reconstruct the precedence by hand.
- As a maintainer of a shared workspace config, I want to be told the
  workspace posture is deprecated and where its replacement lives, so that I
  can move off it before it is removed.

## Requirements

### Functional

- **R1. Machine setting.** niwa's machine config
  (`~/.config/niwa/config.toml`, or the path `GlobalConfigPath` resolves when
  `XDG_CONFIG_HOME` is set) accepts a `[global]` key named
  `dispatch_permission_mode` holding a string. A missing key, an empty
  string, and a string of only whitespace all mean "no machine setting".
- **R2. Value set.** The accepted values are exactly Claude Code's permission
  modes: `default`, `acceptEdits`, `plan`, `auto`, `dontAsk`,
  `bypassPermissions`. Surrounding whitespace is trimmed, then the value must
  match one of the six exactly, case included.
- **R3. Precedence for Claude.** For a dispatch whose agent's permission flag
  is Claude's `--permission-mode`, the forwarded mode is the first of: the
  explicit `--permission-mode` value; the machine setting; `bypassPermissions`
  when the instance's workspace posture is `bypass`; nothing.
- **R4. Machine outranks workspace.** R3 holds in both directions: a machine
  setting replaces the workspace-derived mode whether it is stricter (`auto`
  or `default` over a `bypass` posture) or looser (`bypassPermissions` over an
  `ask` posture or none).
- **R5. Other agents.** For an agent whose permission flag is not Claude's
  `--permission-mode` (Codex today), a valid machine setting forwards nothing
  and changes nothing about the launch.
- **R6. Flag unchanged.** The explicit `--permission-mode` flag keeps its
  current behavior for every agent: its value is forwarded unvalidated and
  untranslated as the agent's permission flag (Claude's `--permission-mode`,
  Codex's `--sandbox`).
- **R7. Invalid value fails closed.** A machine setting outside R2's set
  stops every dispatch, whatever the agent and whether or not
  `--permission-mode` is given. The check runs right after the machine config
  is loaded, before the workspace config is read and before any instance
  directory is created. The command exits non-zero, launches nothing, and its
  error names `dispatch_permission_mode`, the offending value, and the six
  accepted values.
- **R8. Unreadable machine config fails closed.** When the machine config
  file exists but can't be read or can't be parsed as TOML (a non-string
  `dispatch_permission_mode` included), dispatch stops the same way R7 does,
  with an error naming the file and the read or parse error. A missing file
  is not an error and means no machine setting.
- **R9. Source on stderr.** When the forwarded mode comes from the machine
  setting, dispatch prints exactly one stderr line:
  `niwa dispatch: using --permission-mode <mode> from [global] dispatch_permission_mode in <config path>`.
  When it comes from the workspace posture, the existing line is printed
  unchanged:
  `niwa dispatch: derived --permission-mode bypassPermissions from the workspace's declared permissions posture`.
  When it comes from the flag, or nothing is forwarded, no permission-mode
  line is printed. Dispatch's existing warning about an unreadable instance
  state file is unaffected.
- **R10. Setter.** `niwa config set dispatch-permission-mode <value>` trims
  the value, checks it against R2's set, and writes it as
  `dispatch_permission_mode` under `[global]`, replacing any earlier value and
  leaving other keys in the file in place, and creating the file when it
  doesn't exist. A value outside the set exits
  non-zero with R7's error and leaves the file untouched.
  `niwa config unset dispatch-permission-mode` removes the key and succeeds
  when the key is already absent. Following the `default-dispatch-harness`
  setter's wording, they print to stdout
  `Dispatch permission mode set to <value> in <config path>`,
  `Dispatch permission mode removed.`, or, when unset finds nothing,
  `No dispatch permission mode set.`
- **R11. Deprecation notice.** When a workspace config declares
  `[claude.settings] permissions`, parsing it yields a warning that the
  workspace posture is deprecated for dispatch and names
  `[global] dispatch_permission_mode` in `~/.config/niwa/config.toml` as the
  replacement. `niwa apply`, `niwa create` and `niwa reset` print it with
  their other config warnings, once per run.
- **R12. Workspace posture keeps working.** During deprecation, a declared
  workspace posture keeps every effect it has today: `bypass` still derives
  the dispatch mode under R3 when nothing outranks it, and `ask` still writes
  `permissions.defaultMode: "default"` into the generated settings files of
  the instance root, each repo, and the workspace root.

### Non-functional

- **R13. No change without the setting.** With no machine setting and a
  readable (or missing) machine config, every dispatch forwards the same argv
  and prints the same stderr it does today, for every agent and posture.
  The only new output anywhere is R11's warning from apply, create and reset.
- **R14. Documented.** A new guide, `docs/guides/dispatch-permission-mode.md`,
  describes the key, its values, R3's precedence, what it means for Codex,
  the setter, and that Claude Code silently starts in its default mode when a
  requested mode is disallowed on the machine or unsupported by the model.
  The README's list of `[global]` settings links to it.

## Acceptance Criteria

Each criterion is checked by a test against the dispatch argv and stderr, or
against the files named.

- [ ] Machine setting `auto`, workspace posture `bypass`, Claude: argv carries
      `--permission-mode auto` and stderr carries R9's machine-setting line.
- [ ] Machine setting `default`, workspace posture `bypass`, Claude: argv
      carries `--permission-mode default`.
- [ ] Machine setting `bypassPermissions`, no workspace posture, Claude: argv
      carries `--permission-mode bypassPermissions` and stderr carries R9's
      machine-setting line.
- [ ] Machine setting `bypassPermissions`, workspace posture `ask`, Claude:
      argv carries `--permission-mode bypassPermissions`.
- [ ] Each of `acceptEdits`, `plan`, `dontAsk` as the machine setting, Claude:
      argv carries `--permission-mode <that value>`.
- [ ] Machine setting `auto` plus `--permission-mode default`, Claude: argv
      carries `--permission-mode default` once, and stderr has no
      permission-mode line.
- [ ] No machine setting, workspace posture `bypass`, plus
      `--permission-mode plan`, Claude: argv carries `--permission-mode plan`.
- [ ] No machine setting, workspace posture `bypass`, Claude: argv carries
      `--permission-mode bypassPermissions` and stderr carries R9's existing
      workspace line, byte for byte.
- [ ] No machine setting, posture `ask` or none, Claude: argv has no
      `--permission-mode`, and stderr has no permission-mode line.
- [ ] `dispatch_permission_mode = ""` and `dispatch_permission_mode = "  "`
      each behave as no machine setting.
- [ ] Machine setting `  auto  ` forwards `--permission-mode auto`; `Auto` is
      rejected under R7.
- [ ] Machine setting `bypassPermissions`, Codex: argv has no `--sandbox` and
      no `--permission-mode`, and equals the argv of the same dispatch with
      the setting removed.
- [ ] Machine setting `auto` plus `--permission-mode workspace-write`, Codex:
      argv carries `--sandbox workspace-write`, as it does today. Neither
      Codex criterion prints a permission-mode line.
- [ ] Machine setting `atuo`: dispatch exits non-zero, the error names
      `dispatch_permission_mode`, `atuo`, and all six values, and no instance
      directory exists afterward. The same holds with `--permission-mode
      default` passed, and for a Codex dispatch.
- [ ] A machine config that isn't valid TOML, and one with
      `dispatch_permission_mode = 3`, each make dispatch exit non-zero with an
      error naming the config file, and no instance directory exists
      afterward. The same holds for a machine config the process can't read
      (mode 000). With no machine config file at all, dispatch proceeds.
- [ ] `niwa config set dispatch-permission-mode auto` on a file holding other
      `[global]` keys writes `dispatch_permission_mode = "auto"`, keeps the
      other keys, and prints `Dispatch permission mode set to auto in
      <config path>`; running it
      again with `plan` replaces the value.
- [ ] `niwa config set dispatch-permission-mode bogus` exits non-zero and the
      file's bytes are unchanged.
- [ ] `niwa config unset dispatch-permission-mode` removes the key and keeps
      the others and prints `Dispatch permission mode removed.`; run again
      with the key absent, it exits zero and prints
      `No dispatch permission mode set.`
- [ ] Parsing a workspace config that declares `[claude.settings] permissions`
      returns a warning containing `dispatch_permission_mode` and
      `~/.config/niwa/config.toml`; one without the key returns no such
      warning. `niwa apply` on the first prints the warning once.
- [ ] An instance created from a workspace with posture `ask` still has
      `permissions.defaultMode: "default"` in its instance-root, repo, and
      workspace-root generated settings files.
- [ ] `niwa watch` launches its review sessions with the same argv whether or
      not a machine setting is present.
- [ ] `docs/guides/dispatch-permission-mode.md` exists and has a section for
      each item R14 lists, and the README links to it.

## Out of Scope

- **`niwa watch` review sessions.** They run under their own containment
  setting and forward no permission mode; the machine setting doesn't reach
  them.
- **Ephemeral sessions from the SessionStart hook.** They aren't launched by
  dispatch.
- **Per-workspace overrides in the machine config.** The point is one answer
  per machine.
- **Detecting what a machine allows.** niwa takes the developer's setting as
  given and doesn't read managed policy or probe Claude Code.
- **Changing the flag's Codex passthrough.** The explicit flag still reaches
  Codex as its sandbox flag (R6); fixing that is a separate behavior change
  for existing callers.
- **Removing the workspace posture, and replacing what `ask` writes into
  generated settings files.** Deprecation only; removal is later work.
- **Editing shared workspace configs that declare the posture.** Moving a
  given workspace off the key is its maintainers' call.

## Known Limitations

- Claude Code doesn't fail when told to start in a mode the machine disallows
  or the model doesn't support; it starts in its default mode instead. niwa
  forwards the requested mode and can't tell the worker didn't get it.
- `niwa config set` and `unset` rewrite the config file without its comments,
  as the existing `default-dispatch-harness` setter does.
- R8 makes a malformed machine config stop dispatch, where today it is
  skipped. A developer with a broken file finds out on their next dispatch.

## Decisions and Trade-offs

- **Value set uses Claude Code's spellings.** Alternative: a niwa-owned set
  such as ask/auto/bypass. The flag already takes Claude's spellings and
  developers reach for them by name; niwa still owns the closed set and what
  each value means per agent. This closes the BRIEF's question about one
  vocabulary: the machine setting accepts the values a developer already
  passes to the flag.
- **Codex gets nothing from the machine setting.** Alternatives: map
  `bypassPermissions` to Codex's full-access sandbox, or pass the value
  through. Full access turns Codex's sandbox off and records trust, which
  niwa's Codex posture rules out; passthrough breaks the launch. Codex
  workers already launch with the trust niwa grants them per invocation.
  This closes the BRIEF's Codex-mapping question.
- **Invalid values and unreadable configs fail closed.** Alternative: warn and
  treat as unset, as `dispatch_model` does. Treating a broken setting as unset
  can fall through to a workspace bypass on the very machine the developer
  meant to restrict.
- **Machine outranks workspace.** This reverses the order
  `default_dispatch_harness` uses, where the workspace wins. Permission
  posture belongs to the machine and its owner; which agent a workspace
  prefers belongs to the workspace.
- **A flag-sourced mode prints nothing.** The developer typed it, and today's
  output stays as it is (R13).
- **What `ask` writes stays put.** This closes the BRIEF's first question for
  now: during deprecation the effect is unchanged, and what replaces it is
  decided when the key is removed.
- **The flag is left as is.** Validating or translating it would change
  behavior for existing callers and isn't needed for the per-machine outcome.
