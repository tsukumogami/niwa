# Lead

Where does the workspace `[claude.settings] permissions` posture ("bypass"/"ask") flow today, and what would deprecating it touch? Enumerate every consumer, find who declares it, check for an existing config-key deprecation mechanism, and note whether the posture does anything besides drive dispatch.

## Findings

### 1. Parsing and validation

- The key is not a typed field. `SettingsConfig` is `map[string]MaybeSecret` (internal/config/config.go:436-441); its comment names "permissions" as "the primary key today" with values "bypass"/"ask". So the parser itself never validates the value, and an unknown key under `[claude.settings]` would not trip the `md.Undecoded()` "unknown config field" warning (config.go:651-653) because the map absorbs any key.
- Validation actually happens at materialize time: `claudeDefaultMode` (internal/workspace/materialize.go:342-351) switches on `postureBypass`/`postureAsk` and returns `errUnknownPosture` (materialize.go:319) for anything else; `buildSettingsDoc` turns that into `settingValueError("unknown permissions value", ...)` (materialize.go:736-744). Values can be vault-backed; error text is careful not to leak the plaintext.
- internal/config/permissions_parse_test.go pins that both values parse at five levels: `[claude.settings]`, `[instance.claude.settings]`, `[repos.<r>.claude.settings]`, and in the personal overlay `[global.claude.settings]` and `[workspaces.<ws>.claude.settings]` (lines 8-60).

### 2. Where it is declared

- dot-niwa: public/dot-niwa/.niwa/workspace.toml:24-25 declares `[claude.settings] permissions = "bypass"`. That is the only declaration in dot-niwa (no .md mentions).
- Scaffold: internal/workspace/scaffold.go:80-81 has a commented `# [instance.claude.settings] / # permissions = "ask"` example. No bypass example in the scaffold.
- Personal overlay (the global config *repo*, `GlobalConfigOverride`, internal/config/config.go:783-790) can declare it under `[global.claude.settings]` / `[workspaces.<ws>.claude.settings]`. Note this is a git-synced per-user layer, not a per-host file, so it cannot give "auto on machine A, bypass on machine B".
- Tests declaring it: internal/config/{permissions_parse,vault,config}_test.go; internal/workspace/{apply,characterization,instance_posture,materialize_permissions,root_materializer,posture_documents,override,state,...}_test.go; internal/cli/{permissions_posture,dispatch_permissionmode,dispatch}_test.go; test/functional/features/permission-posture-documents.feature (Scenario Outline at :27, dispatch derivation scenarios at :55 and :74), dispatch.feature, worktree.feature, workspace-config-sources.feature, plus test/functional/posture_steps_test.go.

### 3. Instance state recording

- `instancePermissionsPosture(cfg)` (internal/workspace/instance_posture.go:31-44) reads `MergeInstanceOverrides(cfg).Claude.Settings["permissions"]` and canonicalizes to "bypass"/"ask"/"". Precedence (low to high): workspace overlay, workspace, personal overlay, `[instance.claude.settings]`; per-repo overrides never reach it (comment :17-20). It deliberately does not validate and relies on the materializer having already rejected bad values so "" means undeclared.
- Called once in the pipeline: internal/workspace/apply.go:2193 (`claudePermissions: instancePermissionsPosture(effectiveCfg)`), field declared at apply.go:399-402, persisted into `InstanceState.ClaudePermissions` at apply.go:611 (Create) and :811 (Apply).
- `InstanceState.ClaudePermissions` (`json:"claude_permissions,omitempty"`, internal/workspace/state.go:147-162). Comment: recomputed every Create/Apply, never carried over, and "Only `niwa dispatch` reads it".

### 4. Settings materialization (the non-dispatch effect)

- `buildSettingsDoc` writes `permissions.defaultMode = "default"` only for "ask"; "bypass" writes nothing (materialize.go:321-351, 735-760). Rationale in the comment: Claude Code 2.1.257+ ignores `bypassPermissions` from project scope and falls back to "default", so writing it would override the developer's own mode.
- This document is produced at three sites, all via `buildSettingsDoc`:
  - Instance root `.claude/settings.json` (RootSettingsMaterializer).
  - Per-repo `.claude/settings.local.json` via SettingsMaterializer using `ctx.Effective.Claude.Settings` (materialize.go:1283, 1318) -- here per-repo `[repos.X.claude.settings] permissions` does apply.
  - Workspace root `.claude/settings.json` via `writeRootSettings` (internal/workspace/root_materializer.go:225-262), from workspace + `[instance.claude.settings]` only, no overlays (comment :228-235).
- Worktree content path relies on the "bypass"/"ask" constraint to argue no unresolved `vault://` reaches disk (internal/workspace/worktree_content.go:1016-1023).
- So **"ask" has a real interactive effect**: it pins `defaultMode: "default"` in every generated settings file, which constrains interactive sessions (instance root, repo, workspace root) as well as dispatched workers. "bypass" has no settings-file effect at all today; its only effect is the dispatch flag.

### 5. Dispatch derivation

- internal/cli/dispatch.go:585-624: after Create, `workspace.LoadState(instancePath)` reads `ClaudePermissions`, calls `derivePermissionMode(dispatchPermissionMode, recordedPermissions, spec.Flags)`. On state-read error with no mode it warns (:618-621); on derivation it prints "derived --permission-mode %s from the workspace's declared permissions posture" (:622-624).
- `derivePermissionMode` (dispatch.go:1226-1245): explicit flag wins unchanged; else `bypassPermissions` iff `flags.PermissionMode == "--permission-mode"` and recorded == "bypass"; else "". "ask" yields nothing (worker inherits defaultMode "default" from the settings file).
- The result goes through `buildDispatchPassthrough(flags, displayName, model, permissionMode)` (dispatch.go:1258-1285), a pure argv builder.
- Precedent for a host default: `gc.Global.DispatchModel` is consulted right above (dispatch.go:576-579) as "flag, else host [global] dispatch_model" -- the exact shape the new key would follow. `GlobalSettings` (internal/config/registry.go:28-80) already holds `dispatch_model`, `remote_control_on_dispatch`, `keep_alive_on_dispatch`, `accept_session_messages_on_dispatch`. `accept_session_messages_on_dispatch` is the closest analogue philosophically: its comment says no workspace/instance/repo source is read at all because it is "meant to be the developer's call, not a cloned repo's".

### 6. niwa watch

- `watchReviewLaunch`/`watchResumeLaunch` (internal/cli/watch.go:874-900) pass "" as permission mode; comment explicitly says review sessions must not get the dispatch-derived mode. The watch "askPosture" (`resolveAskPosture`, watch.go:948-971; `watch.ApplyReviewSettings`, internal/watch/containment.go:314-322, 391-442) is a separate sandbox/trust-driven concept that writes `defaultMode: "default"` itself; it does not read the workspace posture. A deprecation does not touch watch beyond comments that reference "the instance's recorded permissions posture" (watch.go:877-880). A new host default must also be kept out of watch launches (they already hard-code "").

### 7. Codex

- Codex posture is a separate, agent-neutral table `[session.posture]` (internal/config/session.go:25-60; internal/agentplan/posture.go) with approvals/sandbox vocabularies mapped to `approval_policy`/`sandbox_mode`. session.go comment: "Claude Code's approval posture keeps coming from [claude.settings] permissions ... Pointing that agent at the neutral declaration too is a separate change." derivePermissionMode excludes Codex by flag spelling (Codex's `--sandbox`), and Codex workers get trust via WorkdirGrantArgs (dispatch.go:589-595). DESIGN-codex-dispatch-posture-persistence.md does not discuss the Claude permissions key (only an incidental "for Claude" re: reentry argv, :623). Deprecation does not touch Codex code.

### 8. Docs

- docs/guides/file-distribution.md:60-80 tells users to declare `permissions = "bypass"` to skip MCP trust prompts for dispatched workers.
- docs/guides/ephemeral-session-instances.md:317-336 explains the root settings carry no bypass and dispatch derives the flag from instance state.
- Designs/PRDs referencing it: DESIGN-dispatch-permission-mode.md (with a "Later change" note pointing to PRD-inert-defaultmode-key R3), DESIGN-inert-defaultmode-key.md, PRD-inert-defaultmode-key.md, DESIGN-workspace-config.md, DESIGN-workspace-root-claude.md, DESIGN-ephemeral-session-instances.md, DESIGN-config-distribution.md, DESIGN-mcp-root-instance-distribution.md, DESIGN-session-store-teardown.md, PRD-config-distribution.md, PRD-dispatch-sendmessage-approval.md, archive/DESIGN-worker-permissions.md.

### 9. Existing deprecation mechanisms

- Workspace config: `Parse` returns `ParseResult{Config, Warnings}`; the `[claude.content]` -> `[content]` move (config.go:610-634) is the template: accept the old spelling with a warning naming the removal version ("removed at v1.0"), error if both old and new are set, migrate the value into the canonical field. Unknown fields become "unknown config field" warnings (config.go:651-653).
- Source layout: rank-2 layout deprecation uses `DeprecationNotice` from `RankDecider` (internal/config/discover.go:113-236) and a one-time disclosure notice with an ID (internal/workspace/disclosure.go:10-14, 83-100; emitted at apply.go:561, 728, 1157), tracked so it is shown once.
- `ParseGlobalConfigOverride` (personal overlay) and `ParseGlobalConfig` (~/.config/niwa/config.toml, registry.go:293-299) do plain `toml.Unmarshal` with no warnings channel; a warning for a personal-overlay declaration would need a new path.

## Implications

- The posture has two distinct effects that a deprecation must separate: (a) "bypass" -> dispatch `--permission-mode bypassPermissions` (dispatch-only, which the host default replaces cleanly), and (b) "ask" -> `permissions.defaultMode: "default"` written into instance-root, per-repo, and workspace-root settings files, affecting interactive sessions too. Deprecating "bypass" is straightforward; deprecating "ask" removes a real interactive guardrail unless something else (Claude Code's own user settings, or a kept/renamed key) replaces it. A plausible path is to deprecate only the dispatch-driving meaning (warn on "bypass"), or to deprecate the whole key but leave "ask" materialization working through the window.
- The deprecation warning fits naturally in `config.Parse` next to the `[claude.content]` precedent, since `[claude.settings]` is a free map: check `cfg.Claude.Settings["permissions"]`, `cfg.Instance.Claude.Settings`, and each repo's settings, append a warning. Overlay/personal declarations need separate handling (personal overlay has no warnings channel). A one-time disclosure notice (disclosure.go pattern) is the alternative if a per-apply warning is too noisy for dot-niwa users.
- The host default slots in at dispatch.go:612 as a new middle rung: explicit flag > host `[global]` default > recorded workspace posture. `derivePermissionMode` gains a parameter; the Claude-only guard (`flags.PermissionMode == "--permission-mode"`) should apply to the host value too, since the vocabulary is Claude's. Watch must keep passing "".
- `ClaudePermissions` state and `instancePermissionsPosture` can be removed only at the end of the window; until then they stay as the lowest rung. state.go's "Only niwa dispatch reads it" means removal touches only dispatch plus tests.
- dot-niwa's `permissions = "bypass"` would start warning once deprecated; dot-niwa should drop it (its users move to the host key) in lockstep, and file-distribution.md's advice needs rewriting to point at the host key.

## Surprises

- The personal overlay already accepts `[global.claude.settings] permissions`, which looks like a "user default" but is git-synced across machines, so it cannot solve the per-host problem; it is also a declaration site a deprecation has to cover.
- "ask" is not dispatch-related at all in practice: dispatch derives nothing from it; its whole effect is the `defaultMode: "default"` written into settings files for every session.
- The workspace-root settings file sources the posture without overlays, so root and instance can disagree (root_materializer.go:228-235).
- `accept_session_messages_on_dispatch` already establishes the principle "host-only, no workspace source is read", which is the end state the user wants for permission mode.
- `[claude.settings]` being a free-form map means a misspelled replacement key would produce no "unknown field" warning; a new host key in `[global]` of config.toml also gets no unknown-key warning because `ParseGlobalConfig` ignores undecoded keys.

## Open Questions

- Should the deprecation cover "ask" (interactive guardrail) or only "bypass" (dispatch)? If "ask" goes, what replaces the `defaultMode: "default"` pin in per-repo and root settings?
- Should the host default's vocabulary be Claude's raw mode strings (`auto`, `bypassPermissions`, `default`, ...) or niwa-neutral, given `[session.posture]` already defines a neutral approvals vocabulary for Codex?
- Should a host default of `bypassPermissions` be applied when the workspace declares "ask"? Today "ask" plus host bypass would mean the flag overrides the settings-file `defaultMode: "default"`; is that acceptable when the user asked "host wins over workspace"?
- Warning per parse vs. one-time disclosure for dot-niwa users, and which release removes it.
- Does the personal-overlay declaration also get deprecated, and through what warnings channel?

## Summary

The workspace `[claude.settings] permissions` value is parsed as a free-form map entry, validated only at materialize time (claudeDefaultMode), recorded per instance as `claude_permissions` (instancePermissionsPosture, apply.go:2193), and read solely by `niwa dispatch`'s derivePermissionMode to add `--permission-mode bypassPermissions` for Claude when it's "bypass"; watch and Codex ignore it, and dot-niwa declares `permissions = "bypass"`. Besides driving dispatch, "ask" writes `permissions.defaultMode: "default"` into the instance-root, per-repo, and workspace-root settings files, which also constrains interactive sessions, so deprecating the key has to keep or replace that effect, while "bypass" has no effect outside dispatch. niwa already has a deprecation pattern to copy (the `[claude.content]` warning in config.Parse, plus one-time disclosure notices), and `[global] dispatch_model` / `accept_session_messages_on_dispatch` in ~/.config/niwa/config.toml show where the host default would go: as a middle rung at dispatch.go:612, below the flag and above the recorded posture.
