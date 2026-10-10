# Lead

How should a host-level default dispatch permission mode plug into `niwa dispatch`,
following the existing `[global]` dispatch-default precedents in
`~/.config/niwa/config.toml`? Where does it slot into `derivePermissionMode`, what
key name fits the conventions, how are unreadable config, audit lines, setters and
validation handled by the precedents, and is there already a host-level way to
override the workspace permissions posture?

## Findings

### The five [global] dispatch precedents (internal/config/registry.go)

`GlobalSettings` (registry.go:28-124) carries five dispatch-scoped host defaults.
Each one resolves differently against its flag and against downstream config:

| Key | Type | Flag | Downstream layer | Precedence | Resolver |
|-----|------|------|------------------|-----------|----------|
| `remote_control_on_dispatch` (registry.go:37-43) | `*bool` | none | instance `[claude.settings] remoteControlAtStartup` | downstream > host | `resolveDispatchRemoteControl` (dispatch_remotecontrol.go:40) |
| `keep_alive_on_dispatch` (registry.go:44-51) | `*bool` | `--keep-alive` (tri-state) | instance `keepAliveOnDispatch` | flag > downstream > host > off | `resolveDispatchKeepAlive` (dispatch_keepalive.go:94-105) |
| `accept_session_messages_on_dispatch` (registry.go:52-68) | `*bool` | `--accept-session-messages` (tri-state) | none, deliberately | flag > host > off | `resolveDispatchInboundAcceptance` (dispatch_inbound.go:96-109) |
| `dispatch_model` (registry.go:69-75) | `string` | `--model` | none | flag > host > nothing | inline at dispatch.go:576-583, then `resolveDispatchModel` (dispatch_model.go:28-48) |
| `default_dispatch_harness` (registry.go:92-123) | `string` | `--harness`, `NIWA_DISPATCH_HARNESS` | `[workspace].default_agent` | flag > env > workspace > host > claude | `resolveSessionAgent` (dispatch.go:351) |

`dispatch_model` is the closest structural analogue to a permission-mode default:
a string, same vocabulary as its flag, no downstream layer, flag-wins. Its
resolution is two lines: `effectiveModel := dispatchModel; if effectiveModel == ""
&& gcErr == nil && gc != nil { effectiveModel = strings.TrimSpace(gc.Global.DispatchModel) }`
(dispatch.go:576-579). Note that `dispatch_model` translates a niwa-owned
vocabulary (categories `fast/balanced/powerful`) into each agent's concrete names
via `spec.ModelCategories` (dispatch_model.go:34-36; agentplan/dispatch.go Claude
and Codex `ModelCategories`), and forwards unknown values with a warning rather
than rejecting (dispatch_model.go:40-47). That is a precedent for cross-agent
vocabulary mapping.

`accept_session_messages_on_dispatch` is the precedent for "the host decides and
no cloned repo can": its doc comment (registry.go:60-64) and the guide
(docs/guides/session-message-acceptance.md:45-48) state no workspace/instance/repo
source is read because it is "the developer's call, not a cloned repo's". A
permission mode is the same class of trust decision.

### Unreadable config.toml (step 2a)

`gc, gcErr := config.LoadGlobalConfig()` runs once at dispatch.go:322. A missing
file returns an empty config (registry.go:283-288); an unreadable or unparseable
file returns an error. Every dispatch default treats `gcErr != nil` as "unset",
silently: the comment at dispatch.go:315-321 says "they are all opt-in". The
harness rung is guarded at dispatch.go:346-349 (`hostCfg = nil`), the model at
dispatch.go:577, keep-alive/inbound through a zero `hostGlobal` built at
dispatch.go:640-647, and RC at dispatch.go:687. No stderr notice is emitted for an
unreadable config.toml (only the workspace config gets `unreadableAgentRungNotice`,
dispatch.go:338-340). The guide admits this: a non-boolean value "makes the whole
`[global]` table unreadable ... Nothing warns you"
(docs/guides/session-message-acceptance.md:40-43). Because TOML decode is
all-or-nothing, a bad `dispatch_permission_mode` type would also silently disable
every other `[global]` key.

### Audit lines on stderr

- Inbound: `inboundAuditFormat` names the source, either `--accept-session-messages`
  or `machine setting accept_session_messages_on_dispatch`
  (dispatch_inbound.go:52-70, `inboundAuditLine` at :118-124), printed at
  dispatch.go:986-987 only when the key actually went in; plus an override line
  when the flag turned off a host "on" (dispatch_inbound.go:63, dispatch.go:991).
  Exact text is pinned by `TestInboundLinesExactText`.
- Model: only a warning for an unrecognized value (dispatch.go:581-583); no audit
  line saying the host default was used.
- Permission mode today: `niwa dispatch: derived --permission-mode %s from the
  workspace's declared permissions posture` (dispatch.go:622-624), printed when
  `derived` is true. Plus a warning when the instance state can't be read and no
  mode was chosen (dispatch.go:618-621).

### Setters: `niwa config set/unset`

Only `default_dispatch_harness` has a setter: `niwa config set
default-dispatch-harness <agent>` / `niwa config unset default-dispatch-harness`
(internal/cli/config_default_harness.go:13-128), registered as subcommands of
`configSetCmd`/`configUnsetCmd` (config_set.go:13-16, config_unset.go:11-14,
config_default_harness.go:12-15). Pattern: kebab-case subcommand with the TOML
snake_case spelling as a cobra alias (:20); reject empty arg (:60-63); validate
through the single boundary (`agent.ParseAgent`) before touching the file
(:68-71); load, mutate, `SaveGlobalConfigTo` (:73-85); print what was set and the
precedence reminder (:87-88). Unset reports "No ... set." when already empty
(:111-114).

The other four keys deliberately have no setter. `SaveGlobalConfigTo` re-encodes
the struct, dropping comments and unknown keys (registry.go:312-333, the "KNOWN
LOSS" note naming dispatch_model, remote_control_on_dispatch,
keep_alive_on_dispatch, accept_session_messages_on_dispatch). The inbound guide
gives this as the reason for no setter on a "security-relevant key"
(session-message-acceptance.md:34-38). That argument applies directly to a
permission-mode default.

### Validation boundaries

- `default_dispatch_harness`: stored raw (registry.go:118-123), validated only by
  `agent.ParseAgent`, at both resolve time and set time.
- `dispatch_model`: never rejected; unknown forwarded with warning.
- `--permission-mode` today: no validation at all. The value is forwarded verbatim
  as the agent's own flag (dispatch.go:31 help: "dropped for an agent that has no
  such flag"; `buildDispatchPassthrough` dispatch.go:1270-1286).
- Bool keys: TOML type is the only validation; a wrong type fails the whole file.

### Where the permission flag goes per agent

`LaunchFlags.PermissionMode` is `--permission-mode` for Claude
(agentplan/dispatch.go:387) and `--sandbox` for Codex (agentplan/dispatch.go:459).
Codex `--sandbox` takes `read-only`/`workspace-write`/`danger-full-access`, an
unrelated vocabulary; niwa normally passes nothing there because
`WorkdirGrantArgs` (agentplan/dispatch.go:452) already grants trust, and the flag
"exists for a developer who asks for a posture deliberately"
(agentplan/dispatch.go:455-458). So a single Claude-vocabulary host value like
`auto` or `bypassPermissions` forwarded to Codex as `--sandbox auto` would fail
the launch.

### derivePermissionMode and the slot for a host value

`derivePermissionMode(explicit, recorded, flags)` (dispatch.go:1226-1245): explicit
wins with `derived=false`; else `bypassPermissions` with `derived=true` when
`flags.PermissionMode == "--permission-mode"` and `recorded == "bypass"`; else "".
Called at dispatch.go:612 after reading `state.ClaudePermissions` from the
just-provisioned instance (dispatch.go:607-611). Result goes into
`buildDispatchPassthrough(..., permissionMode)` at dispatch.go:635. `niwa watch`
passes "" at both its launch sites (dispatch.go:1261-1266 comment), so anything
resolved inside `runDispatch` cannot leak into watch; ephemeral sessions do not go
through this path either.

The natural slot: between the explicit check and the posture check, i.e. a new
`host` parameter: `derivePermissionMode(explicit, host, recorded, flags)` returning
a source (flag / host / posture / none) instead of the bool, so the audit line at
dispatch.go:622-624 can name `machine setting dispatch_permission_mode` the way
`inboundAuditLine` does. The host value is read at the call site like
`dispatch_model` (gate on `gcErr == nil && gc != nil`); `hostGlobal` is built
later (dispatch.go:645) so either move that block above line 612 or read `gc`
directly as step 9a does. The stateErr warning at dispatch.go:618 is already
conditioned on `permissionMode == ""`, so it naturally goes quiet when the host
default supplies a mode.

### Is there already a host-level override of the workspace posture?

Partly, but not in config.toml and not per machine. The personal global config
repo (`niwa config set global <repo>`, cloned to `$XDG_CONFIG_HOME/niwa/global`,
read as `niwa.toml`, config_set.go:23-37) parses into `GlobalConfigOverride`
with `[global]` and `[workspaces.<name>]` `GlobalOverride` blocks
(config/config.go:769-790). `GlobalOverride.Claude` is a `ClaudeOverride`
carrying `Settings SettingsConfig` (config.go:98-104). `MergeGlobalOverride`
applies it with "global value wins per key" (workspace/override.go:479,
533-546), subject to `[vault].team_only` locks (override.go:535-541). The merged
`effectiveCfg` (apply.go:1502) is what `instancePermissionsPosture` reads
(apply.go:2193, workspace/instance_posture.go:31-44). So
`[global.claude.settings] permissions = "bypass"` in a personal overlay does
flow into `claude_permissions` and on to `--permission-mode bypassPermissions`.

Limits: the vocabulary is only `bypass`/`ask` (instance_posture.go:36-43;
anything else errors in `claudeDefaultMode`, materialize.go:325-345), so `auto`
can't be expressed; `ask` maps to a settings `defaultMode: default`, not a flag;
it's an apply-time setting that also writes the instance's settings.json (affects
interactive sessions, not just dispatch); a workspace can lock it via
`team_only`; an instance created with `--skip-global` ignores it; and the overlay
is a GitHub repo meant to be shared across a developer's machines
(PRD-global-config.md:29, 66), so it does not give "auto on one machine, bypass on
another".

### How host settings are documented

- README.md:155 mentions `dispatch_model` and `remote_control_on_dispatch` inline
  in the `niwa dispatch` row of the command table.
- docs/guides/codex-agent.md:102-113 calls these "the other host-level dispatch
  defaults (`dispatch_model`, `keep_alive_on_dispatch`)" and explains the harness
  key's weakest-rung placement.
- docs/guides/session-message-acceptance.md:20-43 is the most complete template:
  numbered "flag, then machine setting, then off" list, exact flag help text,
  "There is no `niwa config set` for the key" with the reason, the unreadable-file
  warning, and a "What can't turn it on" section (:45-60) including the
  relocated-`XDG_CONFIG_HOME`/`HOME` caveat.
- docs/guides/session-keep-alive.md:26-27 and remote-control-on-dispatch.md:11-15
  show a `[global]` TOML snippet.
- Flag help mirrors the key: `--model` help ends "overrides the [global]
  dispatch_model default" (dispatch_model.go:56-60); `--accept-session-messages`
  help ends "overrides the [global] accept_session_messages_on_dispatch machine
  setting in either direction" (dispatch_inbound.go:24-25). The `--permission-mode`
  help (dispatch.go:31) would need the same suffix.

### Proposed key name (evidence, not decision)

`dispatch_permission_mode`, string. Reasons: it is string-valued with the same
vocabulary as a flag, exactly like `dispatch_model` (which is named
`dispatch_<flag>`); the `_on_dispatch` suffix is used only for the `*bool` keys;
the `default_` prefix appears only on the one key that has a setter and a
workspace rung above it. If a setter is added later,
`niwa config set dispatch-permission-mode` with the snake_case alias follows
config_default_harness.go:20. If Codex needs its own value, the per-agent split
could be `dispatch_permission_mode` plus a niwa-owned vocabulary mapped through
`LaunchSpec` (the `ModelCategories` precedent), or a value applied only when the
agent's flag is `--permission-mode` (the current derivation's scoping).

## Implications

- The precedence the exploration wants (flag > host > workspace posture > none)
  fits cleanly as one more branch in `derivePermissionMode`, between explicit and
  recorded. It is a small diff and keeps `buildDispatchPassthrough` a pure argv
  builder.
- The Codex half is the real design question. Today's derivation is Claude-only
  by flag-spelling check; a host default that "applies to Codex too" needs either
  a niwa-owned vocabulary translated per agent, a per-agent key/table, or
  passing the raw value only to the agent whose vocabulary it matches. Forwarding
  Claude's `auto` to `codex --sandbox` breaks the launch.
- The no-setter, hand-edit-only convention plus the KNOWN LOSS comment argue
  against shipping a `niwa config set` for this key unless the round-trip loss is
  fixed first.
- An audit line naming the source (mirroring `inboundAuditLine`) is the
  established pattern; the existing "derived ... from the workspace's declared
  permissions posture" line generalizes to a source-naming line.
- Validation: following `dispatch_model`, unknown values would be forwarded with
  a warning; following `default_dispatch_harness`, they'd be rejected at one
  boundary. Neither precedent rejects at decode time.

## Surprises

- The proposed precedence inverts the polarity the harness key's doc comment
  defends (registry.go:104-107: a host key a downstream value outranks, so "the
  file's precedence" is learned once). RC, keep-alive and harness all let the
  workspace/instance win over the host; only `accept_session_messages_on_dispatch`
  ignores downstream. A host permission default that beats the workspace posture
  breaks that consistency, which the design should justify (the posture is being
  deprecated, and the inbound key is the precedent for host-only trust decisions).
- Unreadable config.toml degrades silently. For permissions this cuts both ways:
  with host `auto` and workspace `bypass`, a typo anywhere in `[global]` silently
  falls back to the more permissive `bypassPermissions`.
- There's already a semi-host-level route (personal overlay
  `[global.claude.settings] permissions = "bypass"`), but it's shared across
  machines, limited to bypass/ask, apply-time, and team_only-lockable.
- `--permission-mode` has no value validation at all today.
- Codex's `codex exec resume` has no sandbox flag (DESIGN-codex-dispatch-posture-
  persistence.md:484-485), so a host-chosen Codex sandbox might not survive
  re-entry the way the trust grant does.

## Open Questions

- Should one key serve both agents through a niwa-owned vocabulary (e.g. mapping
  `bypass`/`auto`/`ask` per agent), or should the host default be Claude-only for
  now with Codex handled separately?
- Should the host value outrank the workspace posture now, or only once the
  posture is deprecated? What does the deprecation warning say when both are set?
- Validate (reject) or forward-with-warning for unknown values?
- Should an unreadable config.toml print a notice when this key is in play, given
  the permissive-fallback risk?
- Does a host default need a setter, given the round-trip loss in
  `SaveGlobalConfigTo`?
- Should the explicit `--permission-mode` value and the host value be recorded in
  instance state so re-entry (Codex resume) can restore it?

## Summary

The closest precedent is `dispatch_model` (string, flag > host > nothing, read inline at dispatch.go:576-579 from the step-2a `gc` with unreadable config silently treated as unset), so a `dispatch_permission_mode` key slots in as a new `host` branch in `derivePermissionMode` between the explicit flag and the recorded `bypass` posture (dispatch.go:612, 1237-1245), with the audit line at dispatch.go:622-624 generalized to name the source like `inboundAuditLine`. Only `default_dispatch_harness` has a `niwa config set/unset` setter; the other keys are hand-edit-only because `SaveGlobalConfigTo` drops comments and unknown keys, and the main design issue is Codex, whose `--sandbox` vocabulary means a Claude value like `auto` can't be forwarded raw. A personal global-config overlay can already set `[global.claude.settings] permissions = "bypass"` and it reaches the recorded posture, but it is shared across machines, limited to bypass/ask, and team_only-lockable, so it doesn't meet the per-machine need, and host-over-workspace precedence inverts the polarity the harness key's comment argues for.
