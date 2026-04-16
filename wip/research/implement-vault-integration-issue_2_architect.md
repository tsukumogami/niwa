# Architect Review — Issue #2 (vault provider interface + fake backend)

**Target commit:** `553d92c86ce6b70f8ae8b7be80ea1c70d2de68d5` on `docs/vault-integration`
**Scope:** `internal/vault/{provider,registry,ref,errors,scrub}.go` + tests, `internal/vault/fake/fake.go` + test
**Verdict:** Approve. No blocking findings. Two non-blocking notes.

---

## Structural fit

The package matches Decision 3 of the design in shape and vocabulary:

- `Provider` has exactly the four mandated methods with the design-specified signatures (`Name() string`, `Kind() string`, `Resolve(ctx, Ref) (secret.Value, VersionToken, error)`, `Close() error`). The optional `BatchResolver` is a separate interface detected by runtime type assertion, matching design lines 371–382 and 811–851.
- `Factory` exposes `Kind()` + `Open(ctx, ProviderConfig) (Provider, error)`. There is no constructor alternative: every test and the fake backend goes through `Factory.Open`. No backend-bypass construction path exists.
- `Registry` / `Bundle` separation is clean: `Registry` is the static, concurrent-safe factory index (`sync.RWMutex` on `factories`, `NewRegistry()` constructs empty, `DefaultRegistry = NewRegistry()`); `Bundle` is the per-apply view returned by `Build`, one-shot and lifecycle-scoped. `Build` does not mutate the registry.
- `ParseRef` is colocated in the vault package (not `internal/config`), consistent with design line 427–430. The parser handles the URI grammar; the config layer will only call into it.
- `VersionToken{Token, Provenance}` is a two-string struct with doc comments reinforcing that both fields are non-secret (line 107–122 of `provider.go`). Matches design lines 393–396.
- Package imports flow downward only: `vault` imports `internal/secret` (lower-level primitive); nothing imports `internal/config`, `internal/workspace`, or `cmd/`. `vault/fake` imports `vault` + `secret` only. No cycles, no inversions.

## Check-by-check

1. **Provider signature match.** PASS. `provider.go:36-60` reproduces the design signature exactly. `BatchResolver` at `provider.go:70-72` is a separate interface detected via `p.(vault.BatchResolver)` — `provider_test.go:77-84` locks in the runtime type-assertion contract.

2. **Factory.Open is the only construction path.** PASS. No exported `NewProvider` or equivalent on the vault package. The fake backend's `NewFactory()` returns a `*Factory` that must then be passed through `Factory.Open` to yield a `vault.Provider`. `Provider` in the fake package is exported as a type but its zero value isn't usable (needs `Open`'s config parsing + mutex init); more importantly, the interface type users hold is `vault.Provider`, which is only producible via `Factory.Open`. Good.

3. **Registry/Bundle separation.** PASS. `Registry` is concurrent-safe (RWMutex at `registry.go:20,51,84`) and contains only the factory index. `Bundle` is a separate type with its own `sync.Mutex`, scoped to a single `Build` call. `Build` itself holds the RWMutex only during factory lookup (`registry.go:84-86`) so long-running `factory.Open` calls don't block concurrent `Register`s.

4. **ParseRef colocation.** PASS. `internal/vault/ref.go`. The parser rejects `vault://` URIs with userinfo, fragments, unknown query parameters, nested slashes, empty keys, wrong schemes. The strict parser matches design intent: the config layer in Issue 3 will delegate here rather than re-implementing.

5. **VersionToken shape.** PASS. `provider.go:109-122`. Both fields are `string`. Doc comments explicitly call out the non-secret contract ("Callers MUST treat Token as opaque"; "Provenance is a user-facing pointer"). `TestVersionTokenZeroValue` (`provider_test.go:113-118`) locks the zero value so future additions can't poison state.json diffing.

6. **ScrubStderr layering.** PASS, and this is the cleanest part of the review. `scrub.go:50-56` explicitly uses a *fresh* `secret.NewRedactor()` for the `known` fragments rather than calling `RegisterValue` on the context redactor. The doc comment (`scrub.go:47-49`) calls this out. `TestScrubStderrDoesNotPolluteContextRedactor` (`scrub_test.go:79-97`) is a named architectural test for exactly the concern in the review brief: single-call fragments must not widen the apply-scoped redactor's deny-list. This matches the design's intent and should be preserved as a regression test.

7. **DefaultRegistry for production.** PASS. `registry.go:36` declares `var DefaultRegistry = NewRegistry()`. Doc and package comments both make the contract explicit: real backends register via `init()` on import; tests use `NewRegistry()`. No production code yet registers (Infisical is Issue 5), which is correct for this slice.

8. **Bundle.CloseAll error handling.** PASS. `registry.go:130-158` continues closing every provider even on error (no `break` after the first failing close), aggregates errors into a single error value, special-cases single-error to avoid wrapping clutter, and clears the map so repeat calls are no-ops. `TestBundleCloseAllAggregatesErrors` (`registry_test.go:256-277`) verifies the continue-on-error behavior. Note: names are iterated in sorted order (`sort.Strings(names)` at line 142), which gives deterministic close order — nice for debugging even though the design doesn't require it.

9. **Fake backend isolation from production.** PASS. The fake package has no `init()` function (confirmed via grep), so importing `internal/vault/fake` does not mutate `DefaultRegistry`. `TestNotRegisteredInDefaultRegistry` (`fake_test.go:31-36`) locks this in. The only way to use the fake is to construct a fresh `vault.NewRegistry()` and explicitly `Register(fake.NewFactory())`. The `Kind` constant `"fake"` is deliberate and the doc comment (`fake.go:1-20`) is unusually careful about explaining why.

## Non-blocking notes

**N-1 — Registry duplicate-provider-name check uses an incomplete source attribution message.** `registry.go:77-78` formats the error as `"duplicate provider name %q (already declared in %s)"`, where `%s` is the prior spec's `Source`. If both specs have empty `Source` (programmatic construction in tests), the message becomes `already declared in ` with trailing whitespace. Purely cosmetic for test ergonomics; production callers from Issue 3 will always populate `Source`. Not worth fixing now.

**N-2 — `Factory` doc comment could explicitly state that `Open` is called once per `ProviderSpec`.** `provider.go:82-85` says the caller is responsible for `Close`, but doesn't say that `Build` calls `Open` exactly once per spec with no retries. This matters for backends that do expensive auth in `Open` (e.g., Infisical in Issue 5). Easy to clarify when Issue 5 lands; not a structural issue here.

## Items deliberately NOT flagged

- The fake's SHA-256 VersionToken derivation from post-decrypt plaintext (`fake.go:175-178`). The package comment already marks this as an acceptable-only-for-fake deviation from the design's rule that real backends MUST NOT derive tokens from plaintext. Issue 5's Infisical backend review is where that rule gets enforced.
- The `map[string]any` shape of `ProviderConfig`. The design picks this explicitly (design line 400–403) to keep the config layer decoupled from the set of compiled backends. Any typed-per-backend alternative would pull `internal/config` back into the dependency graph.
- Lack of a resolver, Infisical backend, or config-schema wiring — all deferred to Issues 4 and 5 per the PLAN.

## Summary

The Issue 2 slice is a cleanly-layered foundation that faithfully implements Design Decision 3. The interfaces, registry, bundle lifecycle, URI grammar, and redaction seam are all in the right packages with the right visibility. The fake backend is correctly quarantined from production via absence of `init()`. Nothing in this commit would cause a downstream issue (3, 4, 5) to have to re-shape the vault package.

Approve for merge.
