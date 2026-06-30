# Round 2 product/spec review — remote-control by default on dispatched workers

Scope: requirement/spec correctness AND doc/spec internal consistency after the
Round-1 doc edits (override scope corrected to "workspace or instance"). Files
checked: BRIEF, PRD, DESIGN, guide (`docs/guides/remote-control-on-dispatch.md`),
and the implementation under `internal/`.

## Verdict

No blocking findings. The Round-1 scope correction is fully applied, the
implementation satisfies every PRD requirement (R1–R7) and acceptance criterion
(AC1–AC6), the instance settings.json a dispatched worker reads does reflect the
workspace+instance `[claude.settings]` merge via `MergeInstanceOverrides`, and the
guide's TOML example is correct and load-bearing-accurate.

## (1) Doc internal consistency after the R1 edits — PASS

- No lingering "repo"-scope override claims. Grep of all remote-control docs for
  `repo`/`per-repo`/`repo-scope`/`workspace/instance/repo` returns only the word
  "report(s)" (PRD:152, DESIGN:102/233, SPIKE:77/126) — no override-scope text.
- All four docs now say the override is **workspace or instance**:
  BRIEF:16,70,106-107; PRD goals:12 + R4:98-102; DESIGN:45-46 + Decision-3a:168;
  guide:27,29. Consistent with the code: `MergeInstanceOverrides`
  (`internal/workspace/override.go:166-203`) merges only `ws.Claude.Settings` and
  `ws.Instance.Claude.Settings` — a repo-level `[repos.X.claude.settings]` is NOT
  in that merge, so a repo-scope override genuinely cannot reach the instance-root
  worker. The R1 correction is factually right.
- DESIGN data-flow (lines 237-241) matches the implementation: `runDispatch` reads
  `[global].remote_control_on_dispatch`, cross-checks the provisioned instance's
  effective `remoteControlAtStartup` (read from the materialized settings.json,
  which reflects the workspace/instance + overlay merge), then conditionally
  appends one dispatch-only `--settings` flag. Verified against
  `internal/cli/dispatch.go:213-237`.
- DESIGN Decision-3a (lines 140-147) and the Decision-Outcome resolver pseudocode
  (lines 178-202) match `resolveDispatchRemoteControl`
  (`internal/cli/dispatch_remotecontrol.go:37-50`): default-fill, inject only when
  host-on AND downstream-unset AND not API-key-forced.

## (2) Requirement re-trace R1–R7 / AC1–AC6 — ALL MET

- **R1** host boolean with explicit unset — `GlobalSettings.RemoteControlOnDispatch
  *bool`, `toml:"remote_control_on_dispatch,omitempty"`
  (`internal/config/registry.go:36`); `nil` = unset. MET.
- **R2** dispatch-only — injection happens solely at the dispatch passthrough seam
  in `runDispatch` (`dispatch.go:224-236`); the materializer only emits
  `remoteControlAtStartup` when a user *explicitly* sets it in `[claude.settings]`
  (`materialize.go:425-431`), never from the host default. MET (AC2 holds by
  construction).
- **R3** host-on + no downstream → worker gets `remoteControlAtStartup:true` —
  resolver returns `inject=true`, appends `--settings {"remoteControlAtStartup":true}`.
  MET (test `TestDispatch_RemoteControl_HostOn_Injects`).
- **R4** downstream (workspace or instance) wins — resolver returns no-inject when
  `inst.RemoteControlAtStartup != nil`; the worker honors its own settings.json.
  Confirmed end-to-end: `InstallWorkspaceRootSettings`
  (`internal/workspace/workspace_context.go:243,334-335`) feeds
  `MergeInstanceOverrides(cfg).Claude.Settings` to `buildSettingsDoc`, so a
  downstream `false` materializes into the instance settings.json that the worker
  reads and that `readInstanceSettings` reads back. **This is the specific claim
  the review asked to verify, and it holds.** MET (test
  `TestDispatch_RemoteControl_DownstreamOff_Wins`).
- **R5 / AC4** unset → byte-for-byte unchanged — resolver short-circuits to
  `(false,"")` when `RemoteControlOnDispatch == nil`; no append. MET, and asserted
  with `slices.Equal` against the baseline passthrough
  (`TestDispatch_RemoteControl_HostUnset_NoChange`).
- **R6 / AC5** ineligible host → clear one-line reason + still launch — resolver
  returns `(false, apiKeyForcedWarning)` when `ANTHROPIC_API_KEY` is set; warning
  printed to stderr, dispatch continues. The eligibility check inspects
  `os.Environ()` — the *same* env `realDispatchLaunch` passes via
  `cmd.Env = os.Environ()` (`dispatch_launcher.go:40`), so the warning describes
  the worker's real auth context. MET (`TestDispatch_RemoteControl_APIKey_WarnsAndSkips`).
- **R7** documented — `docs/guides/remote-control-on-dispatch.md` covers host
  enable, downstream override, and the eligibility caveat. MET.
- **AC1/AC3/AC4/AC5/AC6** — covered by the dispatch-layer tests in
  `dispatch_wiring_remotecontrol_test.go` plus the truth-table tests in
  `dispatch_remotecontrol_test.go`; runnable via `go test ./...`. MET.

## (3) Guide TOML example — CORRECT

```toml
[claude.settings]
remoteControlAtStartup = "false"
```
`SettingsConfig` is `map[string]MaybeSecret` (`internal/config/config.go:320`), so
values are TOML strings — the quoted `"false"` is required and correct (an unquoted
TOML boolean would not decode into `MaybeSecret`). The materializer parses it with
`strconv.ParseBool(strings.TrimSpace(raw))` and emits a JSON bool
(`materialize.go:425-431`); `readInstanceSettings` reads it back into
`RemoteControlAtStartup *bool` (`dispatch_plugins.go:143`), so the resolver sees
"downstream decided" and suppresses injection while the worker loads `false` from
its own settings.json. The example would take effect exactly as the guide claims.

## (4) Stale / contradictory — none blocking

NON-BLOCKING observations only:

- **NON-BLOCKING** `DESIGN-remote-control-by-default.md:184-186` — the resolver
  *pseudocode* warning string ("...ANTHROPIC_API_KEY forces API-key auth; ...needs
  a claude.ai login...") differs in wording from the actual runtime string in
  `dispatch_remotecontrol.go:21` and the guide (line 55): "...ANTHROPIC_API_KEY is
  set, which forces API-key auth; ...requires a claude.ai login...". The DESIGN
  block is explicitly an illustrative `warn(...)` sketch, not a verbatim contract,
  and the guide+code agree with each other, so this is not a contradiction — only
  a cosmetic drift worth a one-line tightening if touched.
- **NON-BLOCKING** `guide:27-37` — "Turn it off for a specific workspace **or
  instance**... set `remoteControlAtStartup` under that scope's `[claude.settings]`"
  shows only the bare `[claude.settings]` table (the workspace-level table). For an
  instance-scoped override the TOML table is `[instance.claude.settings]` (what
  `MergeInstanceOverrides` reads as `ws.Instance.Claude.Settings`). The phrase "that
  scope's `[claude.settings]`" implies the scope-appropriate table, but a reader
  doing instance-only scoping could mistakenly use the workspace table and set it
  workspace-wide. Worth one clarifying line; does not block.
- **NON-BLOCKING** AC2 (dispatch-only) is guaranteed structurally (the inject seam
  exists only in `runDispatch`) but has no explicit negative test asserting an
  interactive/`niwa apply`/ephemeral session never receives the flag. The guarantee
  is sound by construction; an explicit test would only document it.

## Summary

The Round-1 override-scope correction is consistently applied across BRIEF/PRD/
DESIGN/guide and matches what `MergeInstanceOverrides` actually merges. The
implementation meets R1–R7 and AC1–AC6, the downstream override is double-anchored
(resolver default-fill + the worker loading its own settings.json), and the guide's
quoted-TOML example is correct. Remaining items are cosmetic doc-tightening only.

BLOCKING COUNT: 0
