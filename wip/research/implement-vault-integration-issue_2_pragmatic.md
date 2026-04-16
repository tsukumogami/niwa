# Pragmatic Review — Issue #2 (vault provider interface + fake backend)

**Target:** `553d92c86ce6b70f8ae8b7be80ea1c70d2de68d5` on `docs/vault-integration`
**Scope:** `internal/vault/{provider,registry,ref,errors,scrub}.go` (+ tests), `internal/vault/fake/fake.go` (+ test)

## Verdict

Approve. The PR stays within Issue 2's ACs and the surface area matches Decision 3 of the design almost verbatim. A few mild advisory notes follow; none are blocking.

## Blocking findings

None.

## Advisory findings

**1. `fake.NewFactory` is a trivial wrapper**
`internal/vault/fake/fake.go:43` — `NewFactory()` returns `&Factory{}` where `Factory` is an empty struct. Every call site could write `&fake.Factory{}` directly. Small naming-affordance win, so not worth fighting over — flagging only because the file has no state to initialize.

**2. Redundant fragment check in `ParseRef`**
`internal/vault/ref.go:61` — `if parsed.Fragment != "" || strings.Contains(uri, "#")`. The `Contains` branch is unreachable: `url.Parse` populates `Fragment` whenever `#` appears. One of the two checks can go. Trivial.

**3. `Bundle.CloseAll` sorts by name before closing**
`internal/vault/registry.go:142` — close order has no behavioral effect; sorting exists only so aggregated-error ordering is deterministic. Defensible for test stability, but the comment "so we can release them deterministically even if Close panics on one backend" overstates it — sort order doesn't affect panic recovery. Consider trimming the comment or dropping the sort. Inert either way.

**4. `knownProviderNames` is a single-caller helper**
`internal/vault/registry.go:162` — called only from `Bundle.Get` error construction. The "(anonymous)" substitution is the one piece of real logic; extraction is a judgment call. Keeping it is fine; mentioning for completeness.

## Notes on things that look suspicious but aren't

- **`fail_open` option on the fake backend** — exercised by `TestFailOpenReturnsUnreachable` and exists to let Issue 4's resolver tests simulate transient unreachability. Not dead config.

- **`ProviderSpec.Source`** — used only in the duplicate-name error message. Spec-mandated by the design and serves user orientation. Not gold-plating.

- **`DefaultRegistry` populated by backend init()** AC — Issue 2 doesn't register any backend (Infisical is Issue 5; fake deliberately does not register). The test asserts the registry exists and accepts registrations, which is the correct slice of that AC for this issue.

- **`Close` on fake provider treats post-close `Resolve` as `ErrProviderUnreachable`** — sensible reuse of an existing sentinel rather than inventing a new one.

- **`Build` pre-flight duplicate-name check before opening any subprocess** — legitimate: avoids leaking half-opened subprocess sessions on pure config errors.

- **`Registry.Build` takes `ctx` but only hands it to `Factory.Open`** — documented and correct; Build itself does no I/O.

## Design-surface observations (defer to architect-reviewer)

- `Registry.Build` does not pass `spec.Name` into `Factory.Open` — the fake duplicates the name via `config["name"]`. `Provider.Name()` can diverge from the bundle key. Whether Factory.Open should receive the name as a typed parameter is an interface-shape question, not a simplicity question.
