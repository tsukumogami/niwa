# Maintainer Review — Issue #2 (vault provider interface + fake backend)

Target commit: `553d92c86ce6b70f8ae8b7be80ea1c70d2de68d5` on `docs/vault-integration`.

Scope: `internal/vault/{provider,registry,ref,errors,scrub}.go` + tests and `internal/vault/fake/fake.go` + test.

Overall the package is unusually well-documented for a skeleton: every exported symbol has a doc comment, interface contracts spell out thread-safety and idempotency, sentinel errors have "when to expect this" comments, the `ParseRef` doc enumerates accepted shapes, and test names describe the scenario rather than the implementation. The review is close to a clean approval; only one blocking finding and a handful of advisory notes.

---

## Blocking

### B1. `TestNotRegisteredInDefaultRegistry` does not test what its name claims

`internal/vault/fake/fake_test.go:27-36`

```go
// TestNotRegisteredInDefaultRegistry asserts the deliberate design
// choice: the fake backend must not auto-register with
// vault.DefaultRegistry. Tests use a fresh Registry and Register
// the fake explicitly.
func TestNotRegisteredInDefaultRegistry(t *testing.T) {
    r := vault.NewRegistry()
    if err := r.Register(fake.NewFactory()); err != nil {
        t.Fatalf("fresh registry could not register fake: %v", err)
    }
}
```

The test constructs a *fresh* `vault.NewRegistry()` and registers the fake into it. That tells us nothing about `vault.DefaultRegistry`. If a future contributor adds an `init()` to `fake.go` that does `vault.DefaultRegistry.Register(NewFactory())`, this test will still pass — the anti-registration invariant is not actually enforced.

This is exactly the "test name lies" heuristic (Heuristic 6). The invariant is load-bearing:

- `internal/vault/registry.go:31-35` doc says "the fake backend intentionally does NOT register with DefaultRegistry so that production code paths never see it."
- `internal/vault/fake/fake.go:2-6` doc calls it "intentionally NOT registered".

The next developer looking for this guarantee will trust the test, but the test doesn't guarantee anything.

**Fix.** Assert the negative directly. For example, attempt to register the fake into `DefaultRegistry` within the test — success means it was not previously registered; failure (duplicate-kind error) means something auto-registered it. Use a deferred cleanup to avoid polluting other tests, or use a probe-kind pattern. A sketch:

```go
func TestNotRegisteredInDefaultRegistry(t *testing.T) {
    // If fake auto-registered in init(), this second registration
    // would return a "already registered" error.
    err := vault.DefaultRegistry.Register(fake.NewFactory())
    if err != nil {
        t.Fatalf("fake appears to have auto-registered with DefaultRegistry: %v", err)
    }
}
```

That directly proves the invariant. (Note: `TestDefaultRegistryInitialized` in `registry_test.go:282-292` already uses a similar side-effect-in-test pattern with a `registry-test-probe` kind.)

---

## Advisory

### A1. `Bundle.CloseAll` aggregation hides the underlying errors from `errors.Is`

`internal/vault/registry.go:143-158`

```go
var errs []error
for _, name := range names {
    if err := b.providers[name].Close(); err != nil {
        errs = append(errs, fmt.Errorf("vault: closing provider %q: %w", name, err))
    }
}
// ...
if len(errs) == 1 {
    return errs[0]
}
return fmt.Errorf("vault: %d errors closing providers: %v", len(errs), errs)
```

The multi-error branch builds a string via `%v` over the slice, so `errors.Is(bundle.CloseAll(), vault.ErrProviderUnreachable)` returns `false` even when one of the wrapped errors chains to that sentinel. The single-error branch wraps cleanly; the aggregate branch does not.

The doc comment says "aggregates any errors into a single error value" without promising unwrap semantics, so this is not a bug. But the next developer will reasonably expect `errors.Is` to work on whatever CloseAll returns (the single-error branch supports it), and will be surprised when it silently stops working once a second provider also fails. `errors.Join` (Go 1.20+) solves this cleanly.

### A2. Closed-provider error in fake is indistinguishable from network failure

`internal/vault/fake/fake.go:122-124`

```go
if p.closed {
    return secret.Value{}, vault.VersionToken{}, fmt.Errorf("fake: provider %q: %w", p.name, vault.ErrProviderUnreachable)
}
```

A test that accidentally resolves against a closed fake gets `fake: provider "team": vault: provider unreachable`, which reads like a simulated outage. The next developer debugging a test failure will look at `fail_open` or transient state before realizing the provider was closed. Consider `fmt.Errorf("fake: provider %q is closed: %w", p.name, vault.ErrProviderUnreachable)` so the message points at the real cause. (Heuristic 5.)

### A3. `Provider.Resolve` doc references `secret.Errorf` without a pointer

`internal/vault/provider.go:48-53`

```go
// Resolve must return ErrKeyNotFound when the key does not exist
// and ErrProviderUnreachable when the backend cannot be contacted
// (auth failure, network error, CLI not installed). Other errors
// may be wrapped with secret.Errorf.
```

`secret.Errorf` lives in `internal/secret`. The next developer reading this interface to implement a new backend will grep fruitlessly for `vault.Errorf`. A one-word change — "wrapped with `secret.Errorf` (see `internal/secret`)" — prevents the detour.

### A4. Typo in `Registry.Build` doc comment

`internal/vault/registry.go:66`

```go
// Build takes ctx as a hand to both the Factory and so individual
// providers can register their cancellation signal. ctx is not
// stored in the Bundle.
```

"as a hand to" should be "as a handle to" (or similar). The sentence is also slightly garbled ("to both the Factory and so individual providers"). Minor; only flagged because the surrounding docs are otherwise pristine and this stands out.

### A5. `ProviderSpec.Source` equality caveat lives in package doc, not field doc

`internal/vault/provider.go:147-149` (struct doc) and `165` (Source field).

The struct-level comment says "Source is kept for error-message attribution only; it is never compared for equality." The field-level comment repeats the error-message purpose but drops the equality caveat. If a future developer uses `cmp.Diff` or reflect-based comparison on `ProviderSpec`, they may not read both comments. Moving the equality note to the field doc (or repeating it) is a cheap win.

---

## Items checked and confirmed clear

- **All exported types/functions have GoDoc comments.** Spot-checked `Provider`, `BatchResolver`, `Factory`, `Registry`, `Bundle`, `Ref`, `VersionToken`, `BatchResult`, `ProviderConfig`, `ProviderSpec`, `ParseRef`, `ScrubStderr`, all sentinels, `fake.Kind`, `fake.NewFactory`, `fake.Factory`, `fake.Provider`, and all methods on each. Every exported symbol has a comment, most are multi-sentence with a clear contract.
- **Interface contracts are stated.** `Provider.Close` is documented idempotent; `Registry` thread-safety is stated; `BatchResolver` ordering-of-results contract ("same order; missing keys are signaled by ... Err") is explicit; `Bundle` "safe for concurrent Get" / "CloseAll is one-shot" is explicit.
- **`ParseRef` error messages are useful.** Every error path names the full URI and the specific rejection reason ("has nested slashes in key segment", "has unknown query parameter %q", "must not contain userinfo", etc.). All prefixed with `vault:` so they survive wrapping into higher layers.
- **Test names describe scenarios, not implementations.** `TestParseRefRejectsMalformed`, `TestBuildClosesOpenedOnFailure`, `TestScrubStderrDoesNotPolluteContextRedactor`, `TestCloseClearsValues`, etc., all read as behavioral contracts. The one exception is B1 above.
- **Sentinel errors have "when to expect this" comments.** `errors.go` is textbook: each sentinel explains the trigger condition and references the design-doc requirement (R8, R12) where relevant.
- **Fake backend's purpose and non-registration rule are documented.** `fake.go` package doc explains the deterministic-token derivation trade-off and cites the design doc for why it's acceptable only for fixtures. The non-registration rule is stated in both `registry.go` (for the reader approaching from the registry) and `fake.go` (for the reader approaching from the backend) — good redundancy.
- **Stale / out-of-sync comments.** None found. The TODOs, doc references, and sentinel "why" comments all match the current code.
- **Divergent twins.** None found. `ScrubStderr`'s two-layer design is an example of deliberate duplication with an explanation comment, which is the right way to handle it.

---

## Verdict

One blocking finding (the misnamed fake-registration test — easy fix). Five advisory items, none of which block merge. With B1 addressed, the package is maintainable and ready for Issue 3 to depend on.
