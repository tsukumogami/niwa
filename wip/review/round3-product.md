# Round 3 (final) product/spec review — remote-control by default on dispatched workers

HEAD e76800f. Fresh skeptical pass over BRIEF / PRD / DESIGN / PLAN / guide vs. the
implementation in `internal/`. Focus: is every requirement still met, are the docs
mutually consistent and consistent with the code, and (the round-3 question) is
`[instance.claude.settings].remoteControlAtStartup` a real, effective config path that
`MergeInstanceOverrides` honors?

## Verdict

No blocking issues. The feature is implemented as specified; the docs are consistent
with the code, including the latest guide edit distinguishing `[claude.settings]` from
`[instance.claude.settings]`.

## The round-3 load-bearing question: is `[instance.claude.settings]` effective?

Traced end-to-end; the guide's claim is TRUE and the path is effective:

1. `[instance]` parses into `WorkspaceConfig.Instance` (`InstanceConfig`, config.go:231,
   242-246) whose `Claude *ClaudeOverride` carries `Settings SettingsConfig`.
2. `MergeInstanceOverrides` (override.go:166-274) seeds `result.Claude.Settings` from the
   workspace `[claude.settings]` (`copySettings(ws.Claude.Settings)`, line 176) and then,
   when `override.Claude != nil`, overlays `[instance.claude.settings]` per key
   (override.go:197-203). The early-return guard (line 193) does not fire when only
   `[instance.claude.settings]` is set, because `override.Claude != nil`.
3. The instance-root materializer `InstallWorkspaceRootSettings`
   (workspace_context.go:242-345) calls `MergeInstanceOverrides(cfg)` and feeds
   `effective.Claude.Settings` to `buildSettingsDoc`.
4. `buildSettingsDoc` (materialize.go:425-432) emits `doc["remoteControlAtStartup"]` as a
   JSON bool, parsed from the quoted string via `strconv.ParseBool`; an unparseable value
   is a config error. The key is emitted ONLY when explicitly present — nothing turns it on
   by default here.
5. The doc is written to `<instanceRoot>/.claude/settings.json`, which is exactly what
   `readInstanceSettings` (dispatch_plugins.go:161-177) reads back into
   `instanceSettings.RemoteControlAtStartup *bool`.
6. `resolveDispatchRemoteControl` (dispatch_remotecontrol.go:37-50): when
   `inst.RemoteControlAtStartup != nil` it returns `(false, "")` — niwa injects nothing and
   the worker honors its own settings.json. A downstream `false` thus wins over the host
   default even against `--settings` precedence (spike Variant C), and a downstream `true`
   is likewise honored.

Both opt-out locations the guide lists are real: workspace `[claude.settings]` (the merge
base) and `[instance.claude.settings]` (the per-instance override) both land in the
dispatched instance's root `settings.json` and are both read back by the resolver. This is
the same vocabulary already exercised by plugin/marketplace pre-warming, so it is a
contained addition to a working path, not a new mechanism.

## Requirement coverage (re-spot-checked)

- R1/AC: `GlobalSettings.RemoteControlOnDispatch *bool`, tag `remote_control_on_dispatch`
  (registry.go:36); nil/true/false + round-trip covered by registry_remotecontrol_test.go.
- R2/AC2: injection lives only in `runDispatch` step (9a) (dispatch.go:213-237); the host
  default never materializes (buildSettingsDoc emits the key only on explicit user config),
  so interactive / ephemeral / `niwa apply` sessions are untouched.
- R3/AC1: host on + downstream unset + no API key => `inject=true`, appends
  `--settings {"remoteControlAtStartup":true}` as two discrete argv elements
  (dispatch.go:234-236; const built from `config.RemoteControlAtStartupKey`,
  dispatch_remotecontrol.go:16).
- R4/AC3: downstream non-nil => no inject (resolver); worker honors its own settings.json
  (double-anchored).
- R5/AC4: host nil/false => resolver returns `(false, "")`; passthrough unchanged and
  `cmd.Env = os.Environ()` unchanged. The extra `LoadGlobalConfig` + `readInstanceSettings`
  reads do not alter argv/env bytes.
- R6/AC5: `ANTHROPIC_API_KEY` non-empty => `(false, warning)`; warning printed to stderr as
  `niwa dispatch: <warning>` and the worker still launches (dispatch.go:231-239).
- R7: documented in docs/guides/remote-control-on-dispatch.md (host preference + both
  downstream override locations + eligibility caveat).
- AC6: pure resolver truth-tested (dispatch_remotecontrol_test.go), materialization tested
  (materialize_remotecontrol_test.go), tag-vs-const pinned (dispatch_remotecontrol_roundtrip_test.go).

The eligibility printed line in the guide (guide line 63) matches the `apiKeyForcedWarning`
constant (dispatch_remotecontrol.go:21) byte-for-byte once prefixed with `niwa dispatch: `.

## Non-blocking observations

1. **NON-BLOCKING** docs/plans/PLAN-remote-control-by-default.md:2 — PLAN status is `Draft`
   while BRIEF/PRD are `Accepted` and DESIGN is `Current`, and it ships in this PR. Cosmetic;
   plans commonly remain Draft through single-PR execution. No requirement impact.

2. **NON-BLOCKING** docs/plans/PLAN-remote-control-by-default.md:151-160 — Issue 5 names a
   "global-config reference" as a doc target. No standalone guide enumerates the `[global]`
   keys (clone_protocol / auto_install_plugins are not catalogued in any guide either), so
   nothing is conspicuously missing the new key. PRD R7 is satisfied by the dedicated
   dispatch guide. If a `[global]` reference is ever created, add `remote_control_on_dispatch`
   then.

3. **NON-BLOCKING** docs/designs/current/DESIGN-remote-control-by-default.md:184-186 — the
   resolver pseudocode's warning wording ("ANTHROPIC_API_KEY forces API-key auth; ... needs a
   claude.ai login") differs slightly from the shipped constant ("ANTHROPIC_API_KEY is set,
   which forces API-key auth; ... requires a claude.ai login"). The DESIGN labels this block
   illustrative ("the heart of the design"), and the guide's literal example matches the code
   exactly, so this is not a spec contradiction.

4. **NON-BLOCKING** docs/guides/remote-control-on-dispatch.md:36-45 — the workspace-scope
   example carries the inline note "values in [claude.settings] are written as quoted strings"
   while the instance-scope example omits it; both examples nonetheless correctly quote
   `"false"`. Optional symmetry tweak, no correctness impact.

## Summary

The `[claude.settings]` vs `[instance.claude.settings]` distinction in the latest guide edit
is accurate: both are real config paths honored by `MergeInstanceOverrides`, both materialize
into the dispatched instance's root settings.json, and both are read back by the dispatch
resolver to override the host default. Every PRD requirement (R1-R7) and acceptance criterion
(AC1-AC6) is met by the code as it now stands. No contradictions, false claims, or unmet
requirements remain. Ready to mark ready.

BLOCKING COUNT: 0
