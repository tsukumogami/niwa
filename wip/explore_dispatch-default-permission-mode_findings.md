# Exploration Findings: dispatch-default-permission-mode

## Core Question

How should a developer set the permission mode `niwa dispatch` workers launch
with as a host-wide default in `~/.config/niwa/config.toml`, so the same
workspace config yields `auto` on one machine and `bypassPermissions` on
another, with `--permission-mode` still overriding it? What does it take to
deprecate the workspace-level `[claude.settings] permissions` posture?

## Round 1

### Key Insights

- `dispatch_model` is the template: a `[global]` string, flag > host > nothing,
  read from the config loaded at dispatch step 2a, unreadable config treated as
  unset. The new key slots into `derivePermissionMode` (internal/cli/dispatch.go:1237)
  between the explicit flag and the recorded posture; the audit line at
  dispatch.go:622 should name its source (flag, host, workspace) the way
  `inboundAuditLine` does. (host-default-precedent)
- Only `default_dispatch_harness` has a `niwa config set/unset` setter; other
  `[global]` keys are hand-edited because `SaveGlobalConfigTo` drops comments
  and unknown keys. (host-default-precedent)
- The workspace posture has two jobs. `bypass` only drives the dispatch flag;
  `ask` also writes `permissions.defaultMode: "default"` into instance-root,
  per-repo, and workspace-root settings, which constrains interactive sessions.
  Deprecation must replace or deliberately drop the `ask` behavior.
  (posture-flow)
- dot-niwa declares `permissions = "bypass"`; niwa has a deprecation pattern to
  copy (the `[claude.content]` warning in config.Parse plus one-time notices).
  (posture-flow)
- An explicit `--permission-mode` is forwarded raw to Codex as
  `--sandbox <value>` today (dispatch.go:1237-1283): `bypassPermissions` breaks
  a Codex launch, and an elevated Codex value writes a trust stanza into
  `~/.codex/config.toml`. This is an existing bug. (codex-vocabulary)
- Codex workers already get workspace-write through the per-invocation trust
  grant; Codex has no headless analog of `auto`. The best fit is one host key
  with a niwa-owned vocabulary that each agent's LaunchSpec maps, with Codex
  mapping to "forward nothing" and unknown values refused. (codex-vocabulary)
- Claude Code silently falls back to Manual (`default`) when a launched mode is
  locked by policy or unsupported (auto on an unsupported model), with exit 0.
  The CLI flag beats every `defaultMode` but not policy locks. `--bg` bypass
  launches also need `skipDangerousModePermissionPrompt` accepted. Headless
  `-p` sessions default to Manual, not auto, so "no flag" never yields auto.
  (claude-mode-behavior)

### Tensions

- Host-wins precedence reverses the polarity `default_dispatch_harness`
  documents (workspace outranks host). Justified because permission posture is
  host policy, not workspace policy; the reversal needs to be stated in the
  key's comment and docs.
- A personal global-config overlay can already set
  `[global.claude.settings] permissions`, but it is shared across machines,
  only knows bypass/ask, and a workspace can lock it with `team_only`, so it
  does not solve per-host configuration.
- niwa-owned vocabulary vs. the existing explicit flag, which takes Claude's
  raw values. The flag's vocabulary and the host key's should probably match.

### Gaps

- `--bg` behavior under a policy lock was not tested directly (only `-p`).
- Whether niwa can cheaply read a worker's effective mode after a `--bg` launch.

### Decisions

See wip/explore_dispatch-default-permission-mode_decisions.md, Round 1.

### User Focus

The host restriction is the user's own policy call (corporate laptop), not
something niwa should validate: "I do not want to use bypass on this host, but
I want to use bypass on other hosts." Ready to decide.

## Accumulated Understanding

The feature is a new `[global]` key in `~/.config/niwa/config.toml` (likely
`dispatch_permission_mode`) that sets the permission mode for dispatched
workers on that host. Resolution is flag > host key > workspace posture
(deprecated) > nothing, with stderr naming the source. The value set is
niwa-owned (at least the Claude modes the user needs: auto, bypassPermissions,
plus the others Claude accepts) and mapped per agent through LaunchSpec; for
Codex the mapping forwards nothing, which also fixes the existing bug where an
explicit Claude value reaches Codex as `--sandbox`. Exposure through
`niwa config set/unset` follows the `default-dispatch-harness` setter.

Deprecation of the workspace `permissions` key is a separate, staged concern:
warn on parse, keep honoring it below the host key, and decide what replaces
the `ask` posture's `defaultMode` write for interactive sessions before the
key is removed. Open design choices remain (vocabulary naming, whether the
flag adopts the niwa vocabulary, what replaces `ask`, whether to add a
post-launch mismatch warning), but no unknowns block specifying it.

## Decision: Crystallize
