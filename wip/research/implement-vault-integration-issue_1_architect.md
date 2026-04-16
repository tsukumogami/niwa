# Architect Review — Issue #1 (vault-integration secret runtime)

Target: `b4cdbeaa8632a799bab16231bb1fe11fa20e1c1c` on `docs/vault-integration`.

## Scope

Structural fit for the secret runtime package (`internal/secret/`,
`internal/secret/reveal/`). Reviewed against PRD R22, DESIGN Decision 2,
and the "Redactor Implementation Notes" and "Forward-Looking: Explicit
Subprocess Env" sections of the design.

## Summary

The package layout matches the design: `Value` is a struct with private
bytes, `reveal.UnsafeReveal` is the only grep-able plaintext accessor,
`Error` + `Redactor` + `Wrap` / `Errorf` implement the error-wrap
scrubbing chain. Dependency direction flows correctly downward
(`reveal` imports `secret`, nothing imports upward). Formatter coverage
is complete (all six PRD-required paths). The 5-layer error-wrap
acceptance test passes structurally.

One structural concern worth flagging and one worth noting. The rest of
the package fits the architecture.

---

## Findings

### F1 (Advisory) — `secret.Bytes` is exported, widening the grep-surface beyond `reveal`

`internal/secret/value.go:169`

```go
func Bytes(v Value) []byte {
    return v.b
}
```

The function is exported (capital B) so that `internal/secret/reveal`
can import it — the doc comment even says "This function is NOT part
of the public API contract of this package and should not be called
outside internal/secret/reveal." The problem: Go's visibility model
does not enforce that restriction. Any package inside this module
(`internal/...` because it's in-module; the `internal` directory
prefix only blocks cross-module imports) can write
`secret.Bytes(v)` and get plaintext bytes without going anywhere near
`reveal.UnsafeReveal`.

Structural impact:

- **Defeats the design's central review heuristic.** Decision 2 and
  the "Mitigations" section both state the point of the sub-package
  split is that `UnsafeReveal` is the single grep-able read site. With
  `secret.Bytes` present, a reviewer grepping for `UnsafeReveal`
  misses any caller of `secret.Bytes`. The allow-list linter (deferred
  Option 4) now needs to watch two symbols instead of one, and every
  future reader has to know about both.
- **Pattern will be copied.** If Issue 5 (Infisical backend) or Issue
  6 (materializer) authors see `secret.Bytes` exists and spares them
  an import of `reveal`, they will call it. One short function away
  from "UnsafeReveal is the only plaintext accessor" becoming false.
- **State-file comment already internalizes the break.** The
  implementer's own notes in `wip/implement-vault-integration-state.json`
  describe this as "package-private" — the intent is clear, but the
  implementation doesn't deliver it.

Go idioms for this scenario:

1. **go:linkname trick** — keep `bytes()` unexported; expose it to
   `reveal` via `//go:linkname` in both packages. Ugly but widely used
   (stdlib `runtime` uses it).
2. **Internal sub-package for the shared mechanism** — move the real
   `Value` type into `internal/secret/internal/secretcore` (a
   Go-enforced `internal/` sub-tree), re-export it from
   `internal/secret` as an alias, and give `reveal` an import path
   that goes through `secretcore` directly. Heavier refactor.
3. **Accept the export + lean hard on the linter.** Document the
   two-symbol allow-list (`secret.Bytes` and `reveal.UnsafeReveal`)
   and keep the `DO NOT CALL` comment. This is what the code does
   today.

I'm rating this **advisory**, not blocking, for three reasons:

- The module is a single private Go module (`github.com/tsukumogami/niwa`),
  so no external code can ever call `secret.Bytes`.
- The doc comment is explicit and loud.
- Decision 2 already commits to a go/analysis linter in v1.1; that
  linter can allow-list exactly two symbols as easily as one.

But it is worth surfacing so the author makes an informed call. If the
deferred linter slips past v1.1, this shortcut quietly widens the
attack surface over time as the codebase grows.

### F2 (Advisory, minor) — `pickRedactor` does not walk `%w`-wrapped errors for `context.Context`

`internal/secret/error.go:149` — `pickRedactor` scans `args` for
direct `error` and `context.Context` types. It does not recurse into a
wrapped-in-`%w` error's `Unwrap()` chain to find a `context.Context`
that may have been referenced earlier in the chain. This is not
actually a bug — contexts are not typically carried inside errors —
but the asymmetry with the `error` branch (which does walk via
`inheritRedactor` → `errors.As`) is worth a comment. No structural
fix needed; flag for future readers.

---

## Non-findings (addressed by the review prompt)

**Q1. Plaintext isolation outside `reveal.UnsafeReveal`.**
`Value.b` is private and never returned by any method except
`bytes()` (private) and `Bytes()` (exported, called only from
`reveal.UnsafeReveal`). `New` defensively copies input bytes so caller
mutation doesn't affect the Value. `TestValueNewCopiesInput`
exercises this. No accidental exposure path exists beyond F1 above.

**Q3. 6-byte MUST threshold.**
`redactor.go:16` const `minFragmentLen = 6` and `Register` silently
drops anything shorter. `TestRedactorSkipsShortFragment` covers the
boundary (5 refused, 6 accepted). Matches the DESIGN "Redactor
Implementation Notes" MUST exactly. The design also specifies that
short secrets are rejected at resolution time with a hard error;
Register's silent refusal here is correct — the hard-error policy
lives at the resolver (Issue 4), not in this package.

**Q4. `fmt.Errorf("%w")` chain scrubbing.**
`TestWrapFiveLayerErrorfChain` demonstrates the design-claimed
invariant: a `secret.Error` at the base of the chain scrubs on every
call to `Error()`, and Go's standard `fmt.Errorf` `%w` wrapper calls
the inner `Error()` recursively, so the scrub runs before any outer
interpolation sees the plaintext. The approach matches Decision 2's
rationale ("scrubs before interpolation (idempotent on re-wrap)").

**Q5. Formatter completeness.**
All six PRD-required emission paths covered:

- `%s` / `%v` / `%+v` → `***` (via `Format`)
- `%q` → `"***"` (explicit `"q"` branch)
- `%#v` → `secret.Value(***)` (explicit `'#'` flag branch)
- `MarshalJSON` → `"***"`
- `MarshalText` → `***`
- `GobEncode` → refuses with `errGobRefused`

`TestValueFormatWidthPrecision` additionally covers width/precision
modifier combinations so verb-flag exotica don't bypass the switch.
`String` and `GoString` are kept as safety nets even though `Format`
handles every verb directly.

**Q6. Context-scoped Redactor boundary.**
`WithRedactor` / `RedactorFrom` use an unexported `redactorKey struct{}`
type for the context-value key — no collision risk, no global state.
The Redactor itself is allocated per-call (`NewRedactor()`); there are
no package-level `var` slots that could retain fragments across apply
invocations. `TestRedactorConcurrent` exercises the mutex under
concurrent Register/Scrub. No goroutine-local state (no
`runtime.LockOSThread` shenanigans), no package globals.

**Dependency direction.**
`reveal` imports `secret`. `secret` imports only stdlib. No upward
or lateral imports. Clean.

**Package API surface vs DESIGN §"Key Interfaces".**
The design specifies `Value`, `Error`, `Redactor`, `Wrap`, `Errorf`,
`WithRedactor`, `RedactorFrom`, `IsEmpty`, `Origin` — all present and
typed as specified. The only addition beyond the specified surface is
`Bytes` (F1 above).

---

## Recommendation

**Approve with advisory.** The structural shape matches Decision 2.
The secret runtime is ready to be built on by Issue #2 (vault provider
interface). The `secret.Bytes` export is worth a follow-up — either a
comment-level lean on the deferred linter, or a small refactor to
`go:linkname` or a `secretcore` internal sub-package. Neither blocks
Issue #1 from landing; both are cheap to do later.

blocking_count: 0
non_blocking_count: 2
