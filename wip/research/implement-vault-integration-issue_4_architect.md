# Issue 4 (vault-integration) — Architect Review

Commit: `1ad548b22a87b8b81f2ee2589db44ba31618bb88` on `docs/vault-integration`.

## Verdict

Approved. No blocking architectural issues. Two advisory notes worth
tracking for follow-up, neither structural enough to hold the PR.

## Checks against the review-focus list

### 1. Import-cycle resolution — placement of the resolver

Verified: `internal/config/maybesecret.go:5` imports
`internal/vault` for `vault.VersionToken`. The resolver must import
`internal/config` to walk `*config.WorkspaceConfig`. Placing the
resolver in `internal/vault` would create `vault → config → vault`.
Sub-package `internal/vault/resolve` breaks the cycle cleanly:
`internal/vault/resolve` imports both `config` and `vault`, while
`internal/vault` stays free of any `config` dependency. Confirmed via
grep: only `internal/vault/resolve/*` references `internal/config`
from under the vault tree.

Alternatives considered and rejected:

- **Resolver in `internal/workspace`.** Would entangle the vault
  resolver with classify/clone/materialize orchestration. The resolver
  is a compositional stage consumed by `workspace` and (future) other
  callers, not a workspace concern. Also asymmetric: `BuildBundle` and
  `CheckProviderNameCollision` bridge config↔vault and don't belong in
  a workspace package either.

- **Move `VersionToken` out of `MaybeSecret`.** Would weaken the data
  model: every MaybeSecret carrying its own provenance is the whole
  point of that struct. Eliminating the cycle this way sacrifices a
  load-bearing invariant to save a file.

The sub-package is the right answer. The package doc (resolve.go:7–18)
states the reasoning explicitly, which will prevent a future
contributor from flattening it back into `internal/vault`.

### 2. Pipeline ordering — parse → resolve → merge → materialize

Verified in `internal/workspace/apply.go:runPipeline`:

- Lines 239–252: global override is **parsed** from disk.
- Lines 257–274: per-layer bundles built (team and personal), each from
  its own `VaultRegistry`. File-local scoping is preserved: the
  team bundle is built only from `cfg.Vault`; the personal bundle only
  from `globalOverride.Global.Vault`. The merge step never sees a
  fused `VaultRegistry`.
- Line 280: R12 collision check runs before any resolve call.
- Lines 285–291: team cfg **resolved**.
- Lines 298–305: personal overlay **resolved** against its own bundle.
- Line 306: `workspace.ResolveGlobalOverride` flattens per-workspace
  personal override (a different function, see advisory 1 below).
- Line 307: **merge** via `MergeGlobalOverride`.
- Lines 314+: classify/clone/materialize stages.

Ordering matches Design Decision 1 exactly.

### 3. R12 enforcement placement

`resolve.CheckProviderNameCollision` lives in the resolver package
and is called from `apply.go:280`. The docstring at resolve.go:146 and
the callsite comment at apply.go:276–279 both name the reason:
only `runPipeline` has both bundles in scope. R12 is a cross-layer
invariant that cannot be enforced inside `ResolveWorkspace` (which
sees only one layer) or inside `MergeGlobalOverride` (which runs
after resolve and would see already-resolved values). The chosen
placement is the right layer.

One minor observation: `CheckProviderNameCollision` operates solely on
`*vault.Bundle` — no `config` types — so it could also live in
`internal/vault` proper. Keeping it in `resolve` co-locates all R12
logic with the resolver call sites. Either placement is defensible.
Not flagged.

### 4. R8 `team_only` in merge — signature propagation

`MergeGlobalOverride` signature changed from
`(ws, g, dir) *WorkspaceConfig` to `(ws, g, dir) (*WorkspaceConfig, error)`.

Callers audited via grep:

- `internal/workspace/apply.go:307` — updated, handles error.
- `internal/workspace/override_test.go:589, 841, 873, 897` — updated,
  including a `mustMerge` helper (line 587) that wraps success-path
  callers.

No stale callers remain. R8 enforcement reads `teamOnly` from
`ws.Vault.TeamOnly` (override.go:397) and rejects overlay writes at
three leaves: `claude.settings`, `claude.env.vars`, `claude.env.secrets`,
plus `env.vars`, `env.secrets`, `files`. Errors wrap
`vault.ErrTeamOnlyLocked` with actionable remediation. Correct.

### 5. Bundle lifecycle

Sequential opens with paired defers:

```
teamBundle, err := resolve.BuildBundle(...)    // apply.go:257
if err != nil { return }
defer teamBundle.CloseAll()                    // apply.go:261

personalBundle, err := resolve.BuildBundle(...) // apply.go:270
if err != nil { return }
defer personalBundle.CloseAll()                // apply.go:274
```

Error paths analyzed:

- `BuildBundle` for team fails → returns before defer registers;
  nothing to close (Registry.Build closes partials internally and
  returns `nil`).
- `BuildBundle` for personal fails after team succeeds → team's defer
  still runs on function exit.
- Any later step (R12 check, ResolveWorkspace, ResolveGlobalOverride,
  MergeGlobalOverride, classify, clone, materialize) returning an
  error → both defers run via normal unwind.
- Success path → both defers run after the function returns
  `pipelineResult`.

No path exits with an un-closed bundle. `CloseAll` is documented as
idempotent (registry.go:166–170), so the belt-and-suspenders `defer`
pattern is safe.

### 6. `MaybeSecret` invariant (Plain XOR Secret)

Every branch of `resolveOne` (resolve.go:462–550) returns a
freshly-constructed `MaybeSecret` with the appropriate field set:

- `IsSecret()` passthrough: unchanged (invariant preserved upstream).
- Empty Plain: returned as-is (zero value, invariant trivially holds).
- Plaintext in non-secrets table: returned as-is.
- Plaintext in secrets table: `MaybeSecret{Secret: val}` — Plain
  implicitly zero.
- Optional or AllowMissing miss: `MaybeSecret{}`.
- Successful resolve: `MaybeSecret{Secret: val, Token: token}` — Plain
  implicitly zero.

No branch co-populates Plain and Secret.
`TestResolveWorkspaceDoesNotMutateInput` (resolve_test.go:108) and
`TestResolveWorkspaceResolvesVaultURI` (resolve_test.go:62) both
assert the cleared-Plain invariant.

### 7. Fake backend `map[string]any` accommodation

The `fake.Open` now accepts both `map[string]string` and
`map[string]any` for the `values` config key (fake.go:71–93). This
accommodation lives entirely in the fake backend. Real backends
(future, Issue 5+) accept `vault.ProviderConfig = map[string]any` by
contract (provider.go:143); TOML decoding always produces that shape.
No new path feeds decoded TOML fragments into real backends that
didn't already receive them. The change is a test-ergonomics
improvement, not an architectural leak.

### 8. Deep-copy necessity

The resolver contract is "returns a NEW `*WorkspaceConfig`, never
mutates the input" (resolve.go:177–181, locked in by
`TestResolveWorkspaceDoesNotMutateInput`). The walker mutates map
values in place via `values[key] = resolved`, so the input's maps
must be cloned before the walk. The alternative ("construct a new
cfg field-by-field") would also require recreating every slice and
struct — the deep-copy approach just copies once and mutates the
copy. Simpler, not more complex.

`deepcopy.go` only clones fields that hold MaybeSecret transitively
(Claude, Env, Files, Repos, Instance); immutable fields (Workspace,
Sources, Groups) are shared by value. That's the minimal correct
copy.

## Advisory notes (non-blocking)

### A1. Function-name collision: `ResolveGlobalOverride`

Two functions share the name across packages:

- `workspace.ResolveGlobalOverride(g, workspaceName) GlobalOverride`
  — flattens per-workspace overlay (pre-existing).
- `resolve.ResolveGlobalOverride(ctx, gco, opts) (*GlobalConfigOverride, error)`
  — vault resolution pass (new in Issue 4).

`apply.go` calls both back-to-back (lines 299, 306). The return types
and parameter lists differ enough that the compiler will catch
swaps, but the name collision creates reader friction. A name like
`resolve.Overlay`, `resolve.Personal`, or `resolve.PersonalOverlay`
would disambiguate at the call site without changing behavior.
Rename is local; not worth holding the PR.

### A2. Parallel copy-helpers in `resolve` and `workspace`

`resolve/deepcopy.go` defines `cloneHooks`, `cloneSettings`,
`cloneStringMap`, `cloneEnvVarsTable`, `deepCopyEnv`,
`deepCopyClaudeEnv`. `workspace/override.go` defines near-identical
`copyHooks`, `copySettings`, `copyStringMap`, `copyEnvVarsTable`,
`copyEnv`, `copyClaudeEnv`. The deepcopy doc-comment
(deepcopy.go:19–21) explicitly acknowledges the duplication:

> Keep these helpers in sync with internal/workspace/override.go's
> copy* functions: they are separate so resolve doesn't import
> workspace, but the merge semantics share the same copy strategy.

The layering forces this: `workspace` imports `vault/resolve`, so
`resolve` cannot import `workspace`. A clean long-term fix is to
hoist these helpers into `internal/config` (they operate purely on
config structs), letting both callers import from there. That's a
separate refactor, outside Issue 4's scope. Drift risk is low
because the bodies are trivial map/slice clones.

### A3. Duplicated `vault://` prefix check

`resolve.isVaultURI` (resolve.go:579) mirrors `config.hasVaultPrefix`
(validate_vault_refs.go:13–15). Both are three lines. The resolver's
comment justifies the duplication:

> The prefix check is duplicated here and in internal/config to keep
> the resolver in lock-step with the config-layer validator.

Exporting `config.hasVaultPrefix` just to share a three-line function
would be over-engineered. Not flagged.

## Summary

The architectural choices in Issue 4 are sound:

- Sub-package placement correctly resolves the import cycle without
  compromising the `MaybeSecret` data model.
- Pipeline ordering (parse → resolve per-file → merge → materialize)
  preserves file-local scoping for R12 collision detection.
- R12 lives at the one call site that has both bundles in scope.
- R8 `team_only` enforcement lands in the merge layer with a
  properly-propagated signature change.
- Bundle lifecycle is watertight via paired defers.
- Plain XOR Secret invariant is preserved on every return path.
- Deep-copy strategy matches the "never mutate input" contract
  minimally.

Advisory items (name collision, copy-helper duplication, prefix
duplication) are contained and documented; none will compound if left
for follow-up work.

Counts:

- blocking_count: 0
- non_blocking_count: 3
