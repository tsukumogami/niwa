# Phase 6 Architecture Review: vault-integration

## Summary

The architecture is implementation-ready: the pipeline ordering, package
boundaries, and interface signatures are concrete enough for a Go engineer
familiar with niwa to start building. A handful of wiring gaps (error-type
catalog, CLI flag plumbing in the materializer path, `state.json` v1-to-v2
migration semantics, and the `ProviderConfig` schema) should be filled in
before plan generation, but none are structural — they are prose-level
clarifications, not decisions to remake. Verdict: APPROVE_WITH_CHANGES.

## Answers to Review Questions

### 1. Clarity for implementation

Mostly yes. The design commits to concrete package paths (`internal/secret`,
`internal/secret/reveal`, `internal/vault`, `internal/vault/infisical`,
`internal/vault/sops`, `internal/guardrail`), a 14-step numbered pipeline in
`apply.go:runPipeline` that a maintainer can graft onto the existing
`runPipeline` (verified present at line 172 of
`internal/workspace/apply.go`), and full Go signatures for `secret.Value`,
`secret.Error`, `vault.Provider`, `vault.Factory`, `vault.Registry`,
`vault.Bundle`, `vault.Resolver`, `config.MaybeSecret`, `workspace.Shadow`,
`vault.ProviderShadow`, `workspace.SourceEntry`, and the guardrail entry
point. The schema-version bump from 1 to 2 matches the actual
`InstanceState.SchemaVersion` field in `state.go`.

Gaps that a first-time implementer will hit:

- `vault.ProviderConfig` is referenced by `Factory.Open` but never defined.
  The TOML shape (`[vault.provider]` vs `[vault.providers.<name>]`) is clear
  from the PRD, but the Go struct that the resolver hands to a factory is
  not spelled out. Implementers of Phase 2 need to know whether it's
  `map[string]any`, a typed struct with `Kind` + `Extra`, or something else.
- `vault.BatchResult` is referenced inside the `BatchResolver` signature but
  never defined. A one-line struct def (`{Ref Ref; Value secret.Value;
  Token VersionToken; Err error}`) would close that loop.
- `secret.Origin` is returned by `Value.Origin()` but the struct has no
  definition. Given R22 forbids origin from leaking plaintext, implementers
  need to know which fields are allowed (`ProviderName`, `Key`,
  `VersionToken`? Yes; `Kind`? Probably).
- The `ProviderSpec` type used by `Registry.Build(ctx, specs
  []ProviderSpec)` is mentioned once without a definition.

These are small and easily fixed in the Key Interfaces section.

### 2. Missing components / interfaces

- **Error type catalog.** `vault.ErrKeyNotFound` is named. The design should
  also name the errors for: unresolvable provider name (R9 remediation
  pointer), R12 provider-name collision (needs a sentinel or typed
  error for the apply CLI to format remediation), `--allow-missing-secrets`
  soft-miss, and mixed-anon-named parser rejection. Without a catalog,
  Phase 4 and Phase 5 will invent one ad hoc.
- **CLI flag wiring is one hop shallower than implementers need.** The
  design names `--allow-missing-secrets` and `--allow-plaintext-secrets`
  but only the former is threaded into a typed options struct
  (`vault.ResolveOptions`). The latter is a bare bool on the guardrail
  entry point — fine, but `apply.go:runPipeline` and its `pipelineOpts`
  struct need to be updated too, and the design doesn't mention that
  specific edit. Worth a sentence in Phase 9 deliverables.
- **State migration semantics.** The design says "old states load fine"
  and old schema-1 states parse with `Sources == nil` / `Shadows == nil`.
  Fine, but what about WRITE? After a successful v2 apply on a workspace
  that previously had a v1 state, does the state get rewritten as v2
  unconditionally? The answer is almost certainly yes (because apply
  writes the whole state), but Phase 7 should name it so implementers
  don't accidentally add a gate.
- **Test structure.** The design mentions unit tests, integration tests,
  and "R22-compliance acceptance test." Not mentioned:
  - `fake` backend test support for Phase 4+ functional tests — the
    design introduces it in Phase 2 (good) but doesn't specify whether
    it lives under `internal/vault/fake` for production code or under
    a `testdata` / `internal/vault/vaulttest` location. Conventional
    Go idiom is an exported test-support subpackage; worth naming.
  - Which tests cross the `TestMain` / env-var boundary to exercise the
    real `infisical` CLI versus mock-only? Phase 5 says "Depends on
    users having `infisical` CLI installed and authed" — CI implications
    not stated.
- **`MaybeSecret` zero-value and omitempty semantics.** The struct has
  three fields and says "exactly one of Plain or Secret is set," but
  the IsSecret / IsEmpty contract for a zero-value MaybeSecret is
  not nailed down. The TOML decoder will produce `{Plain: "", Secret:
  zero, Token: zero}` for absent keys; whether that round-trips
  through merge as "empty" or "plain-empty-string" affects R12 and R8
  code paths.
- **`vault://` URI grammar.** Decision 1 and the prose say URIs look
  like `vault://[name]/key` and accept a `?required=false` query flag.
  No formal grammar; no mention of whether the parser lives in
  `internal/vault` or `internal/config`. `Ref` is described as the
  parsed form, so by implication the parser is in vault, but a reader
  has to infer this.
- **`ScrubStderr` signature.** Called out in prose (`vault.ScrubStderr(raw,
  known...)`) but not in the Key Interfaces table. Since every backend
  must use it, a one-liner signature belongs there.

None of these are architecture-invalidating — they are short prose fills.

### 3. Phase sequencing

Sequencing is sound. The skeleton-first ordering (secret runtime → provider
interface → schema → resolver → real backend → materializer → state →
shadows → guardrail → CLI → docs) tracks the dependency DAG:

- Phase 1 has no predecessors.
- Phase 2 depends only on Phase 1 (`secret.Value` is in the Provider
  signature).
- Phase 3 depends on Phase 1 (MaybeSecret imports `secret.Value`) and Phase
  2 (MaybeSecret.Token uses `vault.VersionToken`). Order is correct.
- Phase 4 (Resolver) needs 1–3. Correct.
- Phase 5 (Infisical) needs 1–4 plus the fake-backend test harness from 2.
  Correct.
- Phase 6 (materializer + 0o600 + `.local` + `.gitignore`) needs MaybeSecret
  from Phase 3 and the working resolver from Phase 4. Correct.
- Phase 7 (fingerprint + state schema) needs Phase 6's materializer emission
  points. Correct.
- Phase 8 (shadows) is independent of 5/6/7 from a type-dependency view
  (depends on Phase 3 + Phase 4 only) but is correctly placed late
  because it persists to state, which means state schema must already
  be v2 (Phase 7). Correct.
- Phase 9 (guardrail) is independent of 5–8 and could move earlier; parking
  it at step 9 is fine and matches the "hardening after the happy path
  works" ordering.
- Phase 10 (CLI surface) depends on everything. Correct.
- Phase 11 (docs) correctly last.

One real concern: **Phase 6 changes file permissions to `0o600`
unconditionally.** That's orthogonal to vault (and flagged as a pre-existing
bug fix). If it lands as a separate PR before the vault series, existing
users who run any niwa version between the `0o600` fix and the full vault
release will see a file-mode change without a vault explanation. This is a
release-sequencing question, not a phase-ordering bug. Suggest the design
note whether Phase 6 ships as part of the vault PR chain or as an
independent pre-cursor.

One minor invalidation risk: **Phase 4 edits `override_test.go` fixtures
to wrap plain strings in `MaybeSecret{Plain: ...}`.** Phase 6's
materializer edits are type-compatible but untested against those new
fixtures until Phase 7's functional tests arrive. If the Phase 4 PR lands
and the materializer isn't yet updated, a merge on `main` is broken.
Realistically this is handled by Phase 4 and Phase 6 being in the same
chain, but the design doesn't call out "Phase 6 MUST land before next
release after Phase 4." Worth a sentence.

### 4. Simpler alternatives overlooked

**(a) `MaybeSecret` vs a simpler pattern.** Could `MaybeSecret` just be
`secret.Value` with a `Plain` field? No — that would pollute every plain
string slot with secret semantics. Could it be `*secret.Value` (nil for
plain, non-nil for secret) alongside a parallel `string` field? That's
essentially the sum type the design already commits to, minus the named
type. The named sum is the right call — it makes merge-test fixtures
grep-able and gives the resolver a single allocation site. Alternative
considered and correctly rejected in Decision 1 Option 4 (lazy Value). No
simpler pattern is viable given R22's "no bare strings downstream" goal.

**(b) 3-package split (secret/vault/guardrail).** This split maps to three
distinct concerns that already cross different review surfaces:
`internal/secret` is the type-safety layer (reviewed for R22),
`internal/vault` is the provider integration (reviewed for R1/R12/R15),
`internal/guardrail` is the apply-time block (reviewed for R14/R30). A
single `internal/secrets` package holding all three would conflate them
and force the linter/allow-list for `UnsafeReveal` to span a wider
surface. The split is right-sized, not over-engineered. Consolidating
`internal/secret/reveal` into `internal/secret` itself would be a mild
simplification (remove one directory), but the sub-package is
specifically chosen to make the plaintext-read surface grep-able — that's
a deliberate security property, not ceremony.

**(c) Shadow detection integration as simplest path to R31.** The chosen
Option 2 (post-resolve pre-merge visitor, persist to state) is the
simplest of the four considered options that preserves the existing
`MergeGlobalOverride` signature. The one alternative the design did not
explicitly consider: computing shadows during `ResolveGlobalOverride`
(the existing flattener) by having it return `(GlobalOverride, []Shadow)`.
That would save one tree-walk. It was probably dismissed because
`ResolveGlobalOverride` is a pure selector and doesn't need the team
config, whereas shadow detection does. Current design is fine; no reviewer
blocker.

## Blocking Issues

None. The design is implementation-ready modulo the Non-Blocking
Suggestions below.

## Non-Blocking Suggestions

1. **Add missing type definitions to the Key Interfaces section:**
   `vault.ProviderConfig`, `vault.BatchResult`, `vault.ProviderSpec`,
   `secret.Origin`, and the `vault.ScrubStderr` signature. Each is a
   1–3 line addition.

2. **Name the error catalog.** List the sentinel/typed errors that
   `vault`, `config`, and `guardrail` export so `cli/apply.go` has a
   stable surface to format R9/R12/R14 remediation pointers. Suggested
   exports: `vault.ErrKeyNotFound`, `vault.ErrProviderNotFound`,
   `vault.ErrProviderCollision`, `config.ErrMixedAnonNamed`,
   `config.ErrVaultInForbiddenContext`, `guardrail.ErrPublicRepoSecrets`.

3. **Clarify state-file upgrade semantics.** One sentence in Phase 7:
   "After a successful v2 apply, `SaveState` writes
   `SchemaVersion: 2` unconditionally; there is no down-migration."

4. **Call out the Phase 4 / Phase 6 release coupling.** Either land Phases
   4 and 6 in the same release, or document that intermediate releases
   carry a type-wrapped `MaybeSecret` without materializer support.

5. **Position the `0o600` fix relative to the vault PR chain.** Either
   extract it into an independent pre-cursor PR with its own release
   note, or fold it into Phase 6 and explicitly call it a vault-release
   behavior change.

6. **Specify `MaybeSecret` zero-value semantics.** One line: "A zero-
   value `MaybeSecret{}` is treated as absent. `IsSecret` returns false;
   `IsEmpty` returns true when both `Plain == ""` and `Secret.IsEmpty()`
   and `Token.Token == ""`."

7. **Point at the URI parser's home.** Add one sentence stating
   `vault.ParseRef(s string) (Ref, error)` is the URI parser and lives
   in `internal/vault`; `internal/config` stores vault URIs as raw
   strings (via `MaybeSecret.Plain`) and defers parsing to the resolver.

8. **Test support package name.** Specify whether the fake backend
   lives in `internal/vault/fake` (importable by tests across packages)
   or `internal/vault/vaulttest` (Go idiomatic name for test-support
   subpackages).
