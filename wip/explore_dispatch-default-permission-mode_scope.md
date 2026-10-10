# Explore Scope: dispatch-default-permission-mode

## Visibility

Public

## Core Question

How should a developer set the permission mode `niwa dispatch` workers launch
with as a host-wide default in `~/.config/niwa/config.toml`, so the same
workspace config can yield `auto` on one machine and `bypassPermissions` on
another, with the existing `--permission-mode` flag still overriding it per
dispatch? And what does it take to deprecate the workspace-level
`[claude.settings] permissions` posture that currently drives the derivation?

## Context

Today `derivePermissionMode` (internal/cli/dispatch.go) returns the explicit
`--permission-mode` if given; otherwise `bypassPermissions` when the agent's
flag is Claude's `--permission-mode` and the instance's recorded posture
(`claude_permissions` in `.niwa/instance.json`) is `bypass`; otherwise nothing.

The motivating host does not support `bypassPermissions` (Claude Code refuses
it there), yet every workspace declaring `permissions = "bypass"` still derives
that flag. The user wants `auto` on that host and `bypassPermissions` on their
other hosts, with identical workspace roots and config. They see no sense in
configuring this per workspace and want the workspace key deprecated; while both
are supported, the host default wins over the workspace posture. The flag stays
above both.

Precedent: `[global]` already has host-wide dispatch defaults --
`dispatch_model` (flag overrides), `accept_session_messages_on_dispatch`
(host plus flag only, no workspace source read), `keep_alive_on_dispatch`,
`default_dispatch_harness` (exposed via `niwa config set/unset`).

## In Scope

- A host-level `[global]` key for the default dispatch permission mode
- Precedence: flag > host default > workspace posture (until deprecated) > none
- Claude dispatch and Codex dispatch (Codex uses `--sandbox` with a different
  vocabulary and currently gets trust via WorkdirGrantArgs)
- Deprecation path for the workspace `permissions` posture
- `niwa config set/unset` exposure and validation

## Out of Scope

- `niwa watch` review sessions
- Ephemeral sessions provisioned by the SessionStart hook
- Per-workspace overrides inside the host config

## Research Leads

1. **Where does the workspace `permissions` posture flow today, and what would deprecating it touch?** (lead-posture-flow)
   It feeds the dispatch derivation, instance state, and the instance-root and
   workspace-root `permissions.defaultMode` written for `ask`. We need the full
   list of consumers, and who declares it (dot-niwa, scaffold, docs).

2. **How should the host default plug into dispatch, following the existing `[global]` dispatch-default precedents?** (lead-host-default-precedent)
   Key naming, value validation, `niwa config set/unset` wiring, the audit
   line on stderr, and error handling when config.toml is unreadable.

3. **Can one host key serve both Claude and Codex, given their different permission vocabularies?** (lead-codex-vocabulary)
   Claude takes `--permission-mode <mode>`; Codex takes `--sandbox` (and
   possibly approval policy) and gets full trust today through
   WorkdirGrantArgs. Per-harness keys, a niwa-owned vocabulary mapped per
   agent, or raw passthrough?

4. **What does Claude Code do when launched with a mode the host forbids, and what does `auto` need to work?** (lead-claude-mode-behavior)
   Managed settings can disable bypass mode; auto mode may depend on plan,
   model, or settings. Does niwa need to validate or detect anything, and how
   does the CLI flag interact with a `permissions.defaultMode` in the
   instance's settings.json?
