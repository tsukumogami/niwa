# Pragmatic Review — PLAN Issue 5 (Infisical backend)

Target commit: `dbc217e240bd0884328a595c97c1ff5dca6595ee` on `docs/vault-integration`.

Files reviewed:
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/infisical.go`
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/subprocess.go`
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/infisical_test.go`

Focus: simplicity, YAGNI, KISS. Deferred v1.1 work (per-key version IDs) excluded from review.

## Summary

The PR is tight. The `commander` abstraction is warranted (idiomatic way to test `os/exec`), the single R22 regression test is mandatory, the init-panic matches the existing `Registry.Register` contract, and the synthesised SHA-256 VersionToken is acceptable with the existing TODO pointing at v1.1. No blocking findings. A couple of small advisory items.

## Findings

### 1. `commander` interface + `_commander` hook — justified

`internal/vault/infisical/subprocess.go:30-32` and `infisical.go:138-144`.

`os/exec.Cmd` is notoriously resistant to mocking without either (a) the well-known "run the test binary as a subprocess" trick or (b) an interface at the boundary. The interface has one production implementer and one test implementer — which is the normal shape for a subprocess seam. The `_commander` key is namespaced with a leading underscore and documented as test-only. This is the simplest correct approach; inlining `exec.Command` would force all 21 tests to shell out to a real or faked `infisical` binary. Not over-engineered.

### 2. Test redundancy — acceptable

21 tests. I looked for redundancy against acceptance criteria. Each test has a distinct purpose:

- Factory/registration: `TestFactoryKind`, `TestInfisicalFactoryRegisteredInDefaultRegistry`, `TestRegistryBuildsInfisicalProvider` — kind string, init-registration, end-to-end Registry path. Three related but non-overlapping assertions.
- Config parsing: `TestFactoryOpenRejectsMissingProject`, `TestFactoryOpenRejectsMalformedConfig` — happy path absent + type errors. Table-driven for the latter, appropriately compact.
- Laziness: `TestOpenIsLazy` — AC-backed.
- Resolve semantics: `TestResolveFetchesAndCaches`, `TestResolveReturnsKeyNotFound`, `TestResolveBatch`, `TestCloseClearsCache`, `TestEnvAndPathDefaults` — five distinct behaviors.
- Argv hygiene (R21): `TestArgvHygiene` — mandatory.
- Error mapping: `TestAuthFailureMapsToUnreachable`, `TestGenericFailureDoesNotMapToUnreachable`, `TestStartFailureMapsToUnreachable`, `TestMalformedJSONIsGenericError` — four distinct error-origin → sentinel mappings, no redundancy.
- R22 (mandatory): `TestR22StderrScrubPreventsLeak`, `TestR22ScrubStderrWithKnownValues` — the second covers the caller-supplied-values path independently.
- JSON shape handling: `TestArrayShapeParses`, `TestEmptyExportParses` — two distinct shapes the CLI has emitted.
- Misc: `TestResolveReturnsSecretValue` (fmt leak guard), `TestTokenChangesOnRotation` (rotation story for Issue 7), `TestLooksLikeAuthFailure` (marker set lock).

None of these are obviously redundant. `TestLooksLikeAuthFailure` is a whitebox test of an unexported helper — that's a style call but the helper is the contract between exit-code and sentinel mapping, so locking its table is defensible.

Advisory: `TestResolveReturnsSecretValue` overlaps slightly with `secret`-package formatter tests, but it guards the integration point (that Resolve returns a properly-constructed `secret.Value`). Keep.

### 3. `init()` panic on duplicate registration — matches contract

`infisical.go:333-337`. `Registry.Register` itself returns an error on duplicate (`registry.go:53-55`) and the Register doc (`registry.go:38-42`) explicitly says: *"callers that init()-register via a package-level side effect should treat that error as a programming error (panic if desired)."* The init is doing exactly what the registry contract invites. Not too aggressive — a silent failure here would be a ghost bug (package imported twice, or test-ordering race). Panic at startup is the right failure mode.

### 4. Synthesised SHA-256 VersionToken — acceptable v1

`subprocess.go:258-292`. The derivation is stable, rotation-sensitive, null-byte-separated (no `("a","bc")` vs `("ab","c")` ambiguity), and the TODO at `subprocess.go:107-112` documents the v1.1 replacement plan. The empty-map case returns an empty token but a still-useful Provenance URL — a considered choice, not an oversight. `TestTokenChangesOnRotation` covers the critical property. Ship it.

### 5. Minor — Advisory

**`NewFactory()` may be unused externally.** `infisical.go:64-66` exports `NewFactory()` for callers who want an un-registered factory. The only caller I see is `TestFactoryKind`, which could equally use `Factory{}` (as every other test does). If no production caller ever needs a fresh factory separate from the `init()`-registered one, `NewFactory` is a single-caller helper. Given this is a new public package and test-only use may evolve, leaving it is fine. **Advisory.**

**`defaultCommander` nil-guard in `runInfisicalExport`.** `subprocess.go:114-116` — `if c == nil { c = defaultCommander{} }`. Every call site goes through `Provider.commander`, which is set by `Open` to `defaultCommander{}` by default (`infisical.go:93`) and only replaced (not nilled) by the `_commander` hook. The nil path is unreachable given the struct's invariants. **Advisory** — harmless, small, and offers defense-in-depth against a future caller that constructs a `Provider` by hand. Not worth removing.

## Verdict

blocking_count: 0
non_blocking_count: 2 (both advisory, both leave-as-is is defensible)

Approve.
