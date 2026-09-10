---
schema: design/v1
status: Proposed
upstream: docs/prds/PRD-inert-defaultmode-key.md
---

# DESIGN: inert-defaultmode-key

## Status

Proposed

## Context and Problem Statement

A workspace declares a permission posture through `permissions` under
`[claude.settings]`, with `"bypass"` and `"ask"` as the accepted values. niwa
resolves that declaration per generated document and writes the result into
`permissions.defaultMode`. `buildSettingsDoc` in
`internal/workspace/materialize.go` is the one builder for all four
documents: the instance-root `.claude/settings.json`, each repo's
`.claude/settings.local.json`, each worktree's `.claude/settings.local.json`,
and the workspace-root `.claude/settings.json`. It maps the declared value
through `permissionsMapping`, which today is `bypass -> bypassPermissions`
and `ask -> askPermissions`.

There are three coupled technical defects.

**The mapping writes values Claude Code ignores or rejects.** From Claude
Code 2.1.257, `bypassPermissions` no longer takes effect from a project or
local settings file, and `auto` stopped at 2.1.142. Measured on 2.1.267, the
ignored value wins the settings merge and is downgraded to `default`, which
overrides a developer's own `acceptEdits` or `plan`. `askPermissions` was
never a valid mode, and a file carrying it is discarded whole, so every other
key niwa writes into an `ask` scope's document (hooks, worktree-delegation
deny rules, plugins) silently stops taking effect. The PRD requires that no
generated document ever carries `bypassPermissions`, `auto`, or
`askPermissions`, and that every value written is one Claude Code honors from
project scope (`default`, `acceptEdits`, `plan`, `dontAsk`). A `bypass`
document must carry no `permissions.defaultMode`. An `ask` document must
carry `default`, with every other key identical to the undeclared case.
Undeclared carries nothing.

**The only working route reads its input from the dead value.** `niwa
dispatch` forwards `--permission-mode bypassPermissions` on the worker's
launch command, and that flag does take effect. The derivation in
`runDispatch` (`internal/cli/dispatch.go`) decides whether to forward it by
reading the instance-root `settings.json` back through `readInstanceSettings`
and the `Permissions` field of the `instanceSettings` projection
(`internal/cli/dispatch_plugins.go`), then comparing it to
`"bypassPermissions"`. So the value this change must stop writing is the
derivation's only input. The PRD requires the decision to come from the
instance's effective declared posture: the workspace overlay, the workspace,
the personal overlay, then `[instance.claude.settings]`, highest winning.
Per-repo overrides don't affect it. A stale or hand-edited instance-root
document must not grant bypass, and a deleted one must not withhold it. The
flag must reach every form of dispatch (detached, attached, with remote
control). An operator's explicit `--permission-mode` must win and be the
only one on the argv. The effective config that decision needs is already
computed during provisioning: `provisionInstanceFunc` resolves it and
discards it before the derivation runs.

**The tests pin the wrong bytes and fixture the reader.** Six materializer
tests assert the values that must change, and the `ask` test has asserted
`askPermissions` since the mapping was written. The seven dispatch
permission-mode unit tests write their own settings file instead of running
the materializer, so they would stay green if the producer changed and the
reader didn't. Per-location equality checks can't show that a value is
absent everywhere, and today's golden manifests don't cover the workspace
root.

The system boundaries are `internal/workspace`, which holds the materializer,
the root materializer, the dead `WorkerPermissionMode` reader in
`permissions.go`, and the override resolution. They also include
`internal/cli`: the dispatch derivation, the shared argv builder
`buildDispatchPassthrough`, the provisioning result, and the re-entry
surfaces. `internal/watch` is a boundary that must not move. Its
operator-approval posture writes `default` into the same instance-root file,
and its review launches share the argv builder with an always-empty
permission value. The workspace docs are the last boundary: seven committed
documents still describe the retired mechanism.

## Decision Drivers

- **The declared posture decides, and no file can.** The derivation's input
  is the instance's effective posture as resolved from the declaration. A
  generated settings document can neither grant bypass (tampered to say
  `bypassPermissions`) nor withhold it (deleted). (PRD R3)
- **The flag stays on the launch argv.** Every dispatch form carries the
  derived flag. An explicit operator flag wins and is the only
  `--permission-mode` on the argv. Re-entry is `claude attach <handle>`, which
  takes no options, so resume depends on Claude Code preserving the launch
  flags. (R1, R2, R11)
- **Watch never gets a derived flag.** `niwa watch` builds its review argv with
  the same `buildDispatchPassthrough`, and a command-line flag outranks the
  `default` the review writes. The derivation must not live inside that
  builder or in state the builder reads implicitly. (R12, R13)
- **The materializer output follows the expected-value matrix.** Bypass writes
  nothing, ask writes `default`, and undeclared writes nothing, at all four
  locations and under every override combination. Since apply already
  overwrites each document whole, re-apply repairs existing instances without
  a migration. (R4-R9)
- **Invalid declarations are rejected at every level.** That includes the
  personal overlay, with an error naming `bypass` and `ask`, and no document
  written carrying the value. (R10)
- **No configuration change.** The declaration key, its values, and its
  override levels are unchanged. (R16)
- **The tests have to be able to fail.** Derivation tests take their input from
  a declaration, not a hand-written file. The document matrix is asserted with
  every location present. (R17, R18)
- **Codex is untouched.** Its dispatch forwards nothing on `--permission-mode`
  or `--sandbox`. (R12)
- **Smallest diff that fits the existing grain.** `runDispatch` is dense, and
  the codebase already threads pipeline outputs through `pipelineResult` and
  `provisionResult`. Prefer extending those over new persisted state or a
  second settings read, unless the extra buys a requirement.
- **No second `--settings` producer.** The single inline-settings slot is
  occupied by remote control, and repeated `--settings` is silent last-wins.
  Nothing in this design may add a document to that slot.
