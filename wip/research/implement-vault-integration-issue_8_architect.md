# Architect Review — Issue 8 (shadow detection + diagnostics)

Commit: `5ffd02adbb00b899a32c7e4e4e90e7d182fefa62`
Branch: `docs/vault-integration`
Scope: architecture + R22 compliance

## Verdict

**Non-blocking.** The change respects existing structural contracts: the shadow detection functions are pure and live at the correct package level; persistence threads through the established `pipelineResult` → `InstanceState` flow; scope matching is aligned with the merge-side resolver; R22 is upheld by both struct design and a compile-time reflection test. One minor advisory below — no blockers.

## Checks

### 1. R22: Shadow struct has no `secret.Value` field

**PASS.** Verified two ways:

- **By inspection** (`internal/workspace/shadows.go:20-45`): `Shadow` carries only `Kind`, `Name`, `TeamSource`, `PersonalSource`, `Layer` — all strings. `internal/vault/shadows.go:17-33` `ProviderShadow` is the same shape (strings only). Neither struct imports `internal/secret`.
- **By compile-time-style test** (`internal/workspace/shadows_test.go:16-26`, `TestShadowHasNoSecretValueField`): reflects over every field on `Shadow` and fails if any field has type `secret.Value{}`. Good invariant enforcement — the test names "one commit away from leaking" as its failure-mode framing, which matches the structural risk.
- **Defensive assertion** (`shadows_test.go:245-291`, `TestDetectShadowsNeverEmitsSecretValues`) additionally constructs a resolved-secret MaybeSecret and verifies the rendered Shadow contains no secret bytes. The apply-level stderr capture in `apply_vault_test.go:332-339` re-verifies this end-to-end.

R22 is satisfied at the struct, detection, and integration layers.

### 2. Pipeline ordering (R12 gating)

**PASS.** `internal/workspace/apply.go`:

| Line | Step |
|------|------|
| 292–295 | `resolve.BuildBundle` — team bundle |
| 305–308 | `resolve.BuildBundle` — personal bundle |
| 320 | `vault.DetectProviderShadows(teamBundle, personalBundle)` — stderr only |
| 321–329 | Emit shadow diagnostic lines |
| 335 | `resolve.CheckProviderNameCollision` — R12 hard error |
| 346 | `DetectShadows(cfg, globalOverride)` — env/files/settings |
| 347–352 | Emit shadow diagnostic lines |
| 355–362 | `resolve.ResolveWorkspace` (team resolution) |
| 369–382 | Overlay resolve + merge |

`DetectProviderShadows` fires BEFORE `CheckProviderNameCollision`, so the user sees the diagnostic line even though R12 then aborts apply. The ordering comment at `apply.go:311-319` explicitly documents this intent.

**Note on prompt phrasing:** the prompt says "DetectShadows fires between resolve and merge." The implementation fires it BEFORE resolve (line 346, before ResolveWorkspace at 355). This is a deliberate and correct structural choice: DetectShadows is pure and operates on parsed configs only (per the doc comment at `apply.go:339-343`: "Pure function over the parsed team config and the parsed (pre-resolve) overlay so no vault calls are needed"). Running pre-resolve is stronger for R22 compliance (no resolved values are even in scope when detection runs). What architecturally matters — firing before merge so detection sees the two layers independently — holds.

### 3. Scope matching (`team.Workspace.Name` vs `Sources[0].Org`)

**PASS.** The coder's choice aligns with the existing resolver key:

- `internal/workspace/override.go:257` — `ResolveGlobalOverride(g *config.GlobalConfigOverride, workspaceName string)` keys `g.Workspaces[workspaceName]`.
- Invoked from `apply.go:376` as `ResolveGlobalOverride(resolvedOverride, cfg.Workspace.Name)`.
- `internal/workspace/shadows.go:156` — `DetectShadows` does `overlay.Workspaces[team.Workspace.Name]`.

Same key, same semantics. `Sources[0].Org` would have been incorrect — the `[workspaces.<scope>]` sub-block is keyed by workspace name, not by source org, and a workspace can have multiple sources. The test `TestDetectShadowsPerWorkspaceScope` (`shadows_test.go:198-243`) locks this in by verifying that a `workspaces.other-ws` entry does NOT shadow a workspace named `my-ws`.

### 4. State schema

**PASS.** `internal/workspace/state.go:64`:

```go
Shadows []Shadow `json:"shadows,omitempty"`
```

- `omitempty` is present — v1 state files that lack the key unmarshal cleanly, and v2 states with zero shadows serialize without the field.
- The doc comment on `InstanceState` (`state.go:45-51`) notes Shadows is persisted so offline `niwa status` can render summary without re-running the resolver — a consumer exists (`internal/cli/status.go:216-224`), so this is not orphan schema.
- The field is added alongside Issue 7's `ManagedFile.SourceFingerprint` / `Sources` under the same `SchemaVersion = 2` cut; no version bump was needed for Issue 8 on top of Issue 7.

No schema drift.

### 5. Pipeline result threading

**PASS.** Structural path is clean:

1. `DetectShadows` called in `runPipeline` (`apply.go:346`).
2. Result captured in `pipelineShadows` and assigned to `pipelineResult.shadows` on return (`apply.go:624`).
3. Both `Create` (`apply.go:122`) and `Apply` (`apply.go:193`) copy `result.shadows` into `InstanceState.Shadows` before `SaveState`.
4. `pipelineResult.shadows` is a new field on an existing struct — no parallel pattern introduced.

The `TestApplyPersistsShadowsInState` test (`apply_vault_test.go:342-427`) exercises this end-to-end.

### 6. DetectShadows purity

**PASS.** `shadows.go:93-170`:

- No I/O (no `os`, no `io`, no `http`, no filesystem calls).
- No mutation of inputs (only reads from `team.*` and `overlay.*` maps; never writes back).
- No vault provider consultation (compare to `internal/vault/shadows.go:51-70`, which inspects `Bundle.Names()` but does not call `Resolve`).
- Returns a sorted slice (`sort.Slice` at `shadows.go:163-168`, sort key `(Kind, Name)`).
- Nil-input guards return `nil` (`shadows.go:94-96`).
- Same for `vault.DetectProviderShadows` (`vault/shadows.go:51-70`).

Both functions are dependency-free enough to live in their respective packages without broader imports. `internal/workspace/shadows.go` imports only `sort` + `internal/config`; `internal/vault/shadows.go` imports only `sort`. Dependency direction is correct (no upward imports).

## Architectural observations

### Advisory (non-blocking)

1. **Attribution defaults live in two places.** `workspace.Shadow` populates `TeamSource`/`PersonalSource` using the `teamSourceDefault` / `personalSourceDefault` constants (`shadows.go:72-75`). `vault.ProviderShadow` leaves those fields blank intentionally and the CLI diagnostic site at `apply.go:327-328` hard-codes `"workspace.toml"` / `"niwa.toml"`. The doc comment on `vault.DetectProviderShadows` (`vault/shadows.go:43-46`) explicitly calls this out as deliberate — keeping the pure function free of "string constants that belong to the apply pipeline's attribution policy." This is defensible but worth revisiting when per-provider attribution lands (the `TODO`-shaped comment on `ProviderShadow.TeamSource` at `vault/shadows.go:27-29` acknowledges the forward-compat gap). No action required today.

2. **`renderShadowedColumn` concatenates `sh.Layer` verbatim** (`cli/status.go:245`) rather than switching on `ShadowLayerPersonalOverlay`. Today `Layer` is always the constant's value, so this is harmless; if a second layer (e.g., machine-local) lands, the helper will emit whatever string the new layer uses, which may or may not be the desired CLI presentation. Not a current violation.

3. **Provider-level shadows are not persisted.** Comment at `apply.go:314-319` explains the rationale: CheckProviderNameCollision rejects the apply before SaveState, so there's no state to persist into. This is a consistent one-pattern-per-concern decision — provider shadows are stderr-only, key shadows are stderr + state — and the reasoning is load-bearing (if R12 ever becomes a warning instead of an error, provider shadows would need the same persistence path key shadows already have).

### Structural fit

- `DetectShadows` lives in `internal/workspace` (alongside the merge it informs); `DetectProviderShadows` lives in `internal/vault` (alongside `Bundle`). Both sit at their natural package level — no dependency inversions, no cross-package pattern duplication.
- The CLI layer (`status.go`) consumes the persisted `state.Shadows` without reaching back into `workspace.DetectShadows` — the state contract is honored.
- The `pipelineResult` struct absorbed the new `shadows` field cleanly; no parallel result-threading mechanism was introduced.

## Summary

Issue 8's shadow detection and diagnostics fit the existing architecture. R22 is upheld at every layer that could leak (struct definition, reflection test, detection code paths, integration-level stderr capture). Pipeline ordering correctly places informational detection before hard-error enforcement. Scope matching on `team.Workspace.Name` is exactly the right key — it is the same key the merge-side `ResolveGlobalOverride` uses, so detection stays in lock-step with which overlay values actually land. State schema extends `InstanceState` with an `omitempty` slice whose consumer already exists in the CLI status view.

Approved.
