# Dispatch permission mode

How much a worker launched by `niwa dispatch` may do without asking is a
decision about the machine it runs on, not about the workspace. A developer who
runs the same workspaces on a corporate laptop and on a personal machine may
want auto mode on the first and bypass on the second. The machine setting
described here records that answer once per machine, and every dispatch there
follows it from any workspace, with no flag and no change to the shared
workspace config.

## The key and where it lives

The setting is `dispatch_permission_mode` under `[global]` in your personal
niwa config, `~/.config/niwa/config.toml` (or
`$XDG_CONFIG_HOME/niwa/config.toml` when `XDG_CONFIG_HOME` is set):

```toml
[global]
dispatch_permission_mode = "auto"
```

That file is yours and local. A workspace's `.niwa/` directory is often a
snapshot replaced on the next refresh, and it's shared with everyone who clones
the workspace, so it's the wrong place for a per-machine answer.

A missing key, an empty string, and a string of only whitespace all mean no
machine setting. With none, dispatch behaves exactly as it did before the
setting existed.

## Accepted values

The value is one of Claude Code's own permission modes, spelled the way Claude
Code's `--permission-mode` flag spells them:

| Value | What the worker does |
|-------|----------------------|
| `default` | Asks before acting, as an interactive session does |
| `acceptEdits` | Accepts file edits without asking; asks for the rest |
| `plan` | Plans without making changes |
| `auto` | Decides for itself which actions need a prompt |
| `dontAsk` | Doesn't ask; denies what isn't already allowed |
| `bypassPermissions` | Skips permission checks entirely |

See Claude Code's own documentation for the exact behavior of each mode; niwa
forwards the value and doesn't interpret it.

Surrounding whitespace is trimmed, then the value has to match one of the six
exactly, case included. `Auto` and `bypass` are both rejected.

## Precedence

For a Claude dispatch, the forwarded `--permission-mode` is the first of:

1. `niwa dispatch --permission-mode <mode>`, for one command.
2. `[global] dispatch_permission_mode`, for this machine.
3. `bypassPermissions`, when the instance's workspace posture
   (`[claude.settings] permissions`) is `bypass`.
4. Nothing. The worker starts in Claude Code's default mode.

The machine setting outranks the workspace posture in both directions. A
stricter value (`auto`, `default`) replaces a `bypass` posture, and a looser one
(`bypassPermissions`) applies where the workspace declares `ask` or nothing.

That order is deliberately the reverse of `default_dispatch_harness`, where a
workspace's `[workspace].default_agent` wins over the machine-wide default (see
`docs/guides/codex-agent.md`). Which agent a workspace runs is the workspace's
business. How much that agent may do unattended on your machine is yours, and a
cloned workspace config shouldn't be able to loosen it or keep it loose.

Dispatch says on stderr where the mode came from. A mode taken from the
machine setting prints one line naming the file:

```
niwa dispatch: using --permission-mode auto from [global] dispatch_permission_mode in /home/you/.config/niwa/config.toml
```

A mode derived from a `bypass` posture prints the line it always has:

```
niwa dispatch: derived --permission-mode bypassPermissions from the workspace's declared permissions posture
```

A mode from the flag prints nothing, since you typed it, and so does a dispatch
that forwards no mode.

## Codex

The machine setting holds Claude Code modes, and only reaches an agent whose
permission flag is Claude's `--permission-mode`. A Codex dispatch forwards
nothing from it: no `--sandbox`, no `--permission-mode`, and the same argv it
would have with the setting removed. Codex workers already get their trust
through the workdir grant, and Codex's `--sandbox` takes an unrelated set of
values, so routing a Claude mode there would be wrong.

The explicit flag is unchanged for every agent. `niwa dispatch --harness codex
--permission-mode workspace-write` still reaches Codex as
`--sandbox workspace-write`, unvalidated, whatever the machine setting says.

One thing does reach a Codex dispatch: a value outside the accepted set stops
it, the same as a Claude one. See below.

## Setting it from the command line

```
niwa config set dispatch-permission-mode auto
```

prints

```
Dispatch permission mode set to auto in /home/you/.config/niwa/config.toml
```

The command trims the value, checks it against the accepted set before touching
the file, and writes it under `[global]`. It replaces an earlier value, keeps
the other keys in the file, and creates the file when it doesn't exist. A value
outside the set exits non-zero with the error dispatch would give, naming the
key, the value, and the six accepted values, and leaves the file as it was. An empty argument is rejected too, so a setup script
that passes an unset variable fails loudly instead of reporting success.

To remove the setting:

```
niwa config unset dispatch-permission-mode
```

prints `Dispatch permission mode removed.`, or `No dispatch permission mode
set.` when there was nothing to remove. Either way it exits zero.

The TOML spelling works as the subcommand name too:
`niwa config set dispatch_permission_mode auto`. Both commands rewrite the
config file through its parsed form, so comments in it aren't kept, as with
`niwa config set default-dispatch-harness`.

## A bad machine config stops every dispatch

A setting meant to restrict a machine must not quietly stop restricting it. So
when the machine config can't be trusted, dispatch refuses to run instead of
falling through to a workspace `bypass` posture.

A value outside the accepted set stops every dispatch, for every agent, whether
or not `--permission-mode` was given:

```
niwa: error: invalid [global] dispatch_permission_mode "atuo"; accepted values are default, acceptEdits, plan, auto, dontAsk, bypassPermissions (in /home/you/.config/niwa/config.toml)
```

A config file that exists but can't be read, or isn't valid TOML (a
`dispatch_permission_mode` that isn't a string included), stops dispatch with
an error naming the file and the read or parse error:

```
niwa: error: loading /home/you/.config/niwa/config.toml: <read or parse error>
```

Both checks run right after the machine config is loaded, before the workspace
config is read and before any instance is created, so a refused dispatch leaves
nothing behind. A missing config file isn't an error; it means no machine
setting.

Fix the value, or run `niwa config unset dispatch-permission-mode`, which works
even while the stored value is invalid.

## The workspace posture is deprecated

The workspace posture, `permissions = "bypass"` or `"ask"` in
`[claude.settings]` or `[instance.claude.settings]`, is on its way out as the
source of a dispatched worker's mode. A workspace config that declares it now
gets a warning, printed once per run of `niwa apply`, `niwa create` and
`niwa reset` along with their other config warnings:

```
warning: [claude.settings] permissions is deprecated for dispatch; set [global] dispatch_permission_mode in ~/.config/niwa/config.toml instead (niwa config set dispatch-permission-mode <mode>). The workspace value still applies until it is removed
```

Nothing else changes yet. A `bypass` posture still derives
`bypassPermissions` for dispatch when nothing outranks it, and an `ask` posture
still writes `permissions.defaultMode: "default"` into the generated settings
files of the instance root, each repo, and the workspace root. Moving a shared
workspace off the key is its maintainers' call; each developer who wants bypass
on their own machine sets `dispatch_permission_mode` there.

## Claude Code may start in its default mode anyway

Claude Code doesn't fail when told to start in a mode it won't use. If the
requested mode is disallowed on the machine (by managed policy, for example) or
isn't supported by the model the worker runs, Claude Code starts the worker in
its default mode without saying so to niwa. niwa forwards the mode you asked
for, prints where it came from, and can't tell that the worker didn't get it.

That fallback errs toward more prompting, not less. If a worker keeps stopping
to ask when you expected it not to, check what your machine's Claude Code
policy allows and whether the dispatched model supports the mode.

## What it doesn't cover

The setting is read by `niwa dispatch` only. `niwa watch` review sessions run
under their own containment setting and forward no permission mode, and
ephemeral sessions provisioned by the SessionStart hook aren't launched by
dispatch, so neither is affected.
