# Decision 1: Pipeline Ordering

Where `vault://` URI resolution sits relative to parse and merge, and
the boundaries between config-loading, resolution, and merge stages.

The PRD amendment (D-6 "Resolution order") already commits the project
to `parse -> per-file-resolve -> merge -> materialize`. This report
evaluates how to realize that ordering in Go code — specifically where
the resolver lives, what type-shape it produces, and which of today's
functions it couples to. The options differ most on the call-graph and
test-surface consequences, not on the ordering itself.

## Options Evaluated

### Option 1: Resolve-inside-load

LoadWorkspace and ParseGlobalConfigOverride return already-resolved
`*WorkspaceConfig` and `*GlobalConfigOverride` in which every string
slot that was a `vault://` URI has been replaced with a `secret.Value`.
Resolution is invoked from inside the loader after TOML decode.

Shape:

- `config.Load(path, registry)` replaces `config.Load(path)`.
- `config.ParseGlobalConfigOverride(data, registry)` similarly.
- Every TOML `string` field that may hold a secret gets a sum type
  (`config.MaybeSecret` = `{Plain: string} | {Secret: secret.Value}`).

Trade-offs:

- Smallest diff at the merge layer — `MergeOverrides` and
  `MergeGlobalOverride` keep their current last-writer-wins structure
  and only have to handle a new field type.
- Loader acquires a subprocess dependency. Every call site that parses
  a `WorkspaceConfig` must thread a `ProviderRegistry`. Parse-only
  tests (today pure, fast, no external processes) must now either mock
  the registry or pass a `NopRegistry`. Every test in `internal/config`
  gains a test seam.
- The loader already builds the provider table from the TOML (it is in
  the same file), so "file-local provider context" is captured
  structurally: the registry built from `cfg.Vault.Providers` is the
  only thing the loader resolves against. That is a natural place to
  enforce D-9.
- Breaks `Parse([]byte) -> *ParseResult`, today a pure function used
  by unit tests. A parse step that can spawn an `infisical` subprocess
  is a large semantic change to the lowest layer.
- Forces the loader to know about `context.Context` (needed for
  Resolve's cancellation) — `config.Load` currently has no ctx.

### Option 2: Separate Resolver stage between load and merge

The loader stays pure TOML. A new package (`internal/vault/resolver`
or similar) exposes a resolver that walks a parsed `*WorkspaceConfig`
and returns a new `*WorkspaceConfig` in which every vault URI has been
replaced by a `secret.Value`. The apply orchestrator composes:

```
ParseResult      -> parse (pure)
ResolvedConfig   -> resolver.Resolve(parseResult, registry, ctx)
MergedConfig     -> MergeGlobalOverride(resolvedTeam, resolvedGlobal, ...)
                    MergeOverrides(merged, repoName)
Written files    -> materializer.Materialize(effective, ctx)
```

Shape:

- `config.Parse` / `config.Load` stay unchanged.
- `config.ParseGlobalConfigOverride` stays unchanged.
- New `vault.Resolver.ResolveWorkspace(ctx, *WorkspaceConfig) ->
  (*WorkspaceConfig, error)` and
  `vault.Resolver.ResolveGlobalOverride(ctx, *GlobalConfigOverride)
  -> (*GlobalConfigOverride, error)`. Each builds its provider
  registry from the SAME struct it is resolving — D-9 enforced
  structurally.
- Merge functions keep operating on `*WorkspaceConfig` /
  `GlobalOverride`. Field types change (string -> `MaybeSecret`) but
  merge semantics do not.
- `apply.go` gains two resolve calls before the existing
  `MergeGlobalOverride` call.

Trade-offs:

- Preserves two pure, independently testable layers: parse (no IO,
  fast, deterministic) and merge (pure function over typed values,
  no subprocess dependency). Existing merge tests need a one-time
  fixup for the new field type, not a per-test provider mock.
- File-local provider scoping is a syntactic property of the resolver:
  it only reads `cfg.Vault` from the same `cfg` it is resolving. No
  cross-file state can leak in.
- Explicit stage boundary makes R16 ("re-resolve every apply") trivial
  — the apply orchestrator calls `Resolve` unconditionally on every
  invocation; no lifetime question to answer.
- Adds one call site coordination point in `apply.go`, which is
  acceptable — `apply.go` is already the orchestrator and already has
  the global-config fetch/parse/merge sequence (apply.go:211-227)
  right where resolution would slot in.
- Does NOT remove the need for a sum type in `config` (the post-resolve
  struct must be able to carry either a plain string or a
  `secret.Value`), but the sum type is local to a few specific fields,
  not every string in the config.

### Option 3: Resolve-at-materialize with provenance sidecar

Keep URIs as plain strings through merge. Attach an out-of-band
`map[fieldpath]providerTable` that the merge pipeline threads through
unchanged. Materializers consult the sidecar at write time and resolve
each URI against the right per-file provider table.

Shape:

- Merge functions grow a second output: `(merged, provenance, err)`.
- `EffectiveConfig` grows a `Provenance` field keyed by logical
  field-path (e.g., `env.vars.GITHUB_TOKEN` -> provider table from
  workspace.toml).
- Materializers gain a `Resolver` dependency they must invoke for each
  string slot before writing.

Trade-offs:

- Defers resolution to the last possible moment. But that is not a
  feature the PRD asks for — R16 only requires re-resolve per apply,
  not per file write.
- Merge pipeline is no longer a pure reduction over `*WorkspaceConfig`.
  Every merge step has to also merge provenance, and provenance
  itself is not obviously mergeable: what does "last-writer-wins" mean
  for `{field: providerTable}` when the team and personal layers have
  different tables for the same field? The answer (winner's
  provenance wins) is right but now has to be written in parallel for
  every merge branch, doubling the merge code.
- Materializers become security-critical — they must correctly route
  each URI to its origin provider table, and a bug here is a
  vault-crossing bug (team URI resolved against personal provider).
  This concentrates risk at the wrong layer: materializers today are
  stateless file writers.
- Violates R22 in spirit: the pipeline carries URIs, not
  `secret.Value`, almost all the way to disk. The "typed value"
  discipline is deferred to the last hop, which is when the redaction
  surface is widest (error wrapping, write errors, etc.).

### Option 4: Lazy `secret.Value` that defers resolution

`secret.Value` is a handle whose closure captures the provider table
from the file it was parsed from. The pipeline carries handles;
`.Get(ctx)` triggers resolution at materialize time. File-local
scoping is enforced because each handle closes over its file's
provider table.

Shape:

- `secret.Value` is now two things: a resolved-secret wrapper (per
  R22) AND a lazy resolver. Or two types: `secret.Ref` (lazy) and
  `secret.Value` (resolved).
- Loader produces `secret.Ref` instances. Merge carries them.
  Materializer calls `.Get(ctx)`.

Trade-offs:

- Elegant in theory. In practice:
  - `.Get(ctx)` has to pass through the context and error from
    deep inside the materializer. Every string-field handler grows a
    call site that may fail on subprocess error.
  - R16 ("re-resolve every apply") is not compromised, but also not
    obviously supported: the question "is each handle called at least
    once per apply?" becomes an invariant that the materializer has
    to satisfy. In Option 2 the resolver drives a complete walk
    unconditionally; here it depends on whether the materializer
    touches the field.
  - Redaction of errors (R22 error-wrapping coverage) gets harder
    because resolution errors now surface inside the materializer,
    far from where the URI text lives. The error message that says
    "failed to resolve `vault://team/pat`" has to be constructed by
    `.Get(ctx)` without revealing the resolved bytes of anything
    adjacent.
  - Versioning / fingerprinting (R15 `SourceFingerprint`) is
    complicated because the fingerprint needs the resolution
    version-token, which is only produced by `.Get(ctx)`. The code
    that computes `SourceFingerprint` would have to invoke `.Get`
    for every secret-bearing source, duplicating the resolver's
    walk.

## Chosen

**Option 2: Separate Resolver stage between load and merge.**

Pipeline:

```
config.Load(path)                         -> *ParseResult             (pure)
config.ParseGlobalConfigOverride(data)    -> *GlobalConfigOverride    (pure)
vault.ResolveWorkspace(ctx, cfg)          -> *WorkspaceConfig         (subprocess IO, re-run every apply)
vault.ResolveGlobalOverride(ctx, gco)     -> *GlobalConfigOverride    (ditto)
workspace.ResolveGlobalOverride(...)      -> GlobalOverride           (existing pure merge)
workspace.MergeGlobalOverride(...)        -> *WorkspaceConfig         (existing pure merge)
workspace.MergeOverrides(...)             -> EffectiveConfig          (existing pure merge)
materializers                             -> files                    (write)
```

Field-type change: every TOML `string` slot that accepts a vault URI
becomes a `config.MaybeSecret` carrying either `{Plain: string}` or
`{Secret: secret.Value}`. In the parsed struct it is always
`{Plain: string}` — the resolver is what replaces `vault://...` plains
with `{Secret: secret.Value}`. After resolve, merge operates on the
typed shape unchanged by vault semantics.

## Rationale

Option 2 wins because it keeps two valuable invariants intact — parse
stays pure (no subprocess in the config package's test harness) and
merge stays a pure reduction (existing merge tests keep working with
a one-time field-type migration, not a per-test provider mock). File-
local scoping is enforced structurally because the resolver reads the
provider table from the same file it is resolving. R22's typed-value
discipline starts at the earliest possible point (immediately after
parse), so the merge pipeline and every downstream layer never handle
vault URIs as bare strings. R16 is satisfied by construction:
`apply.go` calls `Resolve` unconditionally on every apply, with no
cache involved. Options 1 and 4 couple subprocess IO to layers that
should stay pure, and Option 3 spreads security-critical routing
across every materializer.

## Rejected

- **Option 1 (Resolve-inside-load)** — rejected: forcing subprocess
  IO into `config.Parse` / `config.Load` ruins the pure-parse test
  surface and forces every loader call site to thread a
  `ProviderRegistry`.
- **Option 3 (Resolve-at-materialize with provenance sidecar)** —
  rejected: doubles merge code (every merge branch also merges
  provenance) and puts security-critical vault routing inside
  stateless materializers, widening the attack surface for a property
  the PRD never asks for (deferred resolution).
- **Option 4 (Lazy `secret.Value`)** — rejected: `Get(ctx)` at
  materialize time spreads subprocess errors and R22 error-redaction
  concerns across every materializer, and complicates R15 fingerprint
  computation by forcing it to re-trigger resolution.

## Implementation Implications

Concrete consequences for the design doc's Solution Architecture:

1. **New package.** `internal/vault` holds the `Provider` interface
   (R1), the `ProviderRegistry` built from `cfg.Vault`, and the
   `Resolver` whose entry points are
   `ResolveWorkspace(ctx, *config.WorkspaceConfig) (*config.WorkspaceConfig, error)`
   and
   `ResolveGlobalOverride(ctx, *config.GlobalConfigOverride) (*config.GlobalConfigOverride, error)`.
2. **Config-layer schema.** Add a `config.MaybeSecret` type (or
   equivalent) used in every field that R3 lists as reference-accepting:
   `EnvConfig.Vars` values, `ClaudeEnvConfig.Vars` values,
   `SettingsConfig` values, `WorkspaceConfig.Files` keys and values,
   and any `EnvSecretsConfig` / `ClaudeEnvSecretsConfig` introduced
   by R33. `Parse` decodes these as plain strings; the resolver
   rewrites those that begin with `vault://` into `{Secret: ...}`.
3. **No ripple in `MergeOverrides` / `MergeGlobalOverride`.** The merge
   functions in `internal/workspace/override.go` keep their current
   signatures and semantics; they operate on a `*WorkspaceConfig`
   whose leaf types happen to be `MaybeSecret`. Existing merge tests
   need one fixture update (wrap the plain strings in
   `{Plain: ...}`), not per-test provider wiring.
4. **Apply orchestrator change (narrow).** `apply.go:runPipeline`
   gets two new calls immediately after the existing parse/fetch of
   global config (apply.go:211-227) and before `MergeGlobalOverride`:
   `vault.ResolveWorkspace` on `cfg`, then (if global is active)
   `vault.ResolveGlobalOverride` on the parsed global. This is a
   single insertion point; the rest of runPipeline is unchanged.
5. **Test layering.** Three strata stay independently testable:
   (a) `config` package — pure TOML parse, no vault; (b)
   `internal/vault` — resolver tests with a fake `Provider`
   implementation, no merge; (c) `internal/workspace` — merge and
   materializer tests with `MaybeSecret` fixtures that do not need a
   provider mock.
6. **Error / diagnostic surface.** The resolver is where R9's
   error-message requirements (name provider, name key, distinguish
   failure modes) are implemented. R31 (shadow diagnostics) runs
   immediately after merge, reading `MaybeSecret` typed fields that
   the resolver annotated — not a separate pass over URIs.
7. **Fingerprint production (R15).** The resolver returns both the
   `secret.Value` and the `version-token` per `Resolve` call. Those
   tuples feed the materializer's `SourceFingerprint` computation.
   Because resolution happens before merge, the version-token lives
   on the `MaybeSecret{Secret: ...}` field alongside the value, and
   the materializer that writes the file reads both.

## Open Items for Phase 3 Cross-Validation

Assumptions this decision bakes in, to verify against other decisions:

1. **R12 shape (Decision 5 / "GlobalOverride.Vault").** This decision
   assumes personal-overlay providers resolve within the personal
   overlay file and team providers resolve within the team file.
   The resolver handles team and personal-overlay as two independent
   walks that then merge. If R12 ends up allowing personal overlay
   to "extend" the team provider registry (M-2's option b), the
   resolver needs a combined registry for team resolution, which
   breaks file-local scoping. Verify D-9 / R12 reconciliation.
2. **R33 / D-10 (`env.vars` vs `env.secrets`).** This decision assumes
   both tables go through the same resolver, with the `vars/secrets`
   distinction consumed by the guardrail (R14) and redaction
   classifier, not by the resolver. Verify the split is purely about
   classification, not about which fields the resolver visits.
3. **R15 fingerprint reduction rule.** This decision assumes the
   version-token rides on `MaybeSecret{Secret: ...}` and the
   materializer reduces per-source tuples. If the fingerprint
   decision separates `SourceFingerprint` computation into a stage
   outside the materializer, the version-token's carrier type may
   need to live at a different layer.
4. **`Resolver.Close` lifecycle.** R1 requires `Close()` on providers
   (session cleanup). The orchestrator's decision about where to
   Close providers — after `Resolve` completes for each file, or
   after the whole apply — needs to be consistent with "re-run
   resolver on every apply" (R16). Cross-check with the
   subprocess-lifecycle decision.
5. **Context plumbing depth.** This decision introduces the first
   `context.Context` path that reaches into config-adjacent code.
   Cross-check with any decision on cancellation / timeouts — the
   resolver surface is the natural place to enforce a per-apply
   resolution deadline.
6. **Parse-time rejection of bare `vault://` URIs when no providers
   are declared (R3).** Whether that rejection fires in the parser,
   in a linter-style validator, or in the resolver — this decision
   puts it in the resolver (it is the first layer with access to
   both the URI text and the provider table). Verify this matches
   the config-validation decision.
