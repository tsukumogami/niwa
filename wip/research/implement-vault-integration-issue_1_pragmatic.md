# Pragmatic Review — Issue 1 (secret runtime)

**Target:** commit `b4cdbeaa8632a799bab16231bb1fe11fa20e1c1c` on `docs/vault-integration`
**Scope:** `internal/secret/{value,error,redactor}.go` + `internal/secret/reveal/reveal.go` and tests.
**PLAN ACs:** formatters emit `***`; `MarshalJSON`/`MarshalText` emit `***`; `GobEncode` refuses; `Wrap` registers fragments on a per-context Redactor; `Unwrap` preserves chain; `Scrub` skips `< 6` bytes, longest-first; 5-layer `%w` chain test.

## Overall

Implementation is tight and faithfully maps to Decision 2 of the design doc and the Issue 1 ACs. No scope creep into Issue 2+ territory (no provider types, no `MaybeSecret`, no config). Test coverage matches AC bullets. The single-responsibility split across three files is justified by the design's own sub-section split.

## Findings

### F1 (Blocking) — Hand-rolled `replaceAll` / `indexOf` duplicate stdlib

`internal/secret/redactor.go:116-151` implements `replaceAll` and `indexOf` because "We avoid strings.Index to keep imports tight" / "keeps the dependency footprint minimal." Grep confirms `strings` is used freely in `internal/workspace/*.go` and `internal/cli/*.go`; there is no codebase convention to avoid it. This is 35 lines of hand-rolled code duplicating `strings.ReplaceAll`, plus a rationale that does not match project practice.

**Fix:** replace the `for _, frag := range frags { out = replaceAll(out, string(frag), redactedPlaceholder) }` loop body with `out = strings.ReplaceAll(out, string(frag), redactedPlaceholder)` and delete `replaceAll` + `indexOf`.

Severity: blocking. The hand-rolled path has its own edge cases (e.g., `if old == new` early-return is a footgun if `redactedPlaceholder` ever changes), and the NIH footprint will compound as future issues add scrubbing helpers.

### F2 (Advisory) — `Error.Redactor()` accessor has no production caller

`internal/secret/error.go:50-55`. The method exists, documented as "exposed so callers who want to register additional fragments on the chain's Redactor can do so." Only `TestErrorRedactorAccessor` exercises it. The PLAN AC list does not require post-hoc fragment registration; the design doc's `type Error struct` sketch (lines 310-315) shows only `Error()` and `Unwrap()`. Speculative.

**Fix:** delete `Error.Redactor()` and the test that exercises it. If a future issue needs late registration, add it then. Keeping it small costs nothing today but creates an implicit contract (anyone can grab the Redactor, mutate shared state across goroutines you didn't expect).

Advisory rather than blocking because the method is three lines, package-local, and the lock on the Redactor makes the concurrency risk bounded.

### F3 (Advisory) — `Value.bytes()` duplicates package-level `Bytes(v)`

`internal/secret/value.go:157-171`. Both functions return `v.b`. `bytes()` is called exactly once, from `Redactor.RegisterValue` (`redactor.go:67`). The exported `Bytes(v)` exists for the `reveal` sub-package.

**Fix:** delete `bytes()`; call `Bytes(v)` from `RegisterValue` directly. Single-caller helper with no naming benefit over the already-exported plumbing function.

Advisory. Removing it reduces one layer of indirection and the comment explaining why both exist.

### F4 (Advisory) — Nil-receiver guards on `*Error`

`internal/secret/error.go:27-55`. `Error()`, `Unwrap()`, `Redactor()` all check `if e == nil`. The only path that produces a `*secret.Error` is `Wrap` / `Errorf`, neither of which returns a typed-nil pointer. Test `TestErrorNilReceiver` exists to cover this, but the branch is defensive against callers who manually declare `var e *secret.Error`, which is not a real code path.

**Fix:** drop the nil guards; let a bare `*secret.Error{}` panic if someone constructs a misconfigured one. Inline, the methods become two lines each.

Advisory. Three small branches; the tests that pin them are the main cost.

## Non-findings (deliberate choices worth noting)

- `secret.Bytes(v)` is package-level exported and technically callable by any `internal/secret` importer. The doc comment calls this out. Go has no better visibility control for cross-sub-package plumbing; the grep-ability argument (name `UnsafeReveal` in a named sub-package) is the actual review surface. Not an issue.
- `Format` implemented alongside `String`/`GoString` — intentional per design doc, addresses AC that all verbs redact regardless of which hook Go picks for reflection-based printing. Correct, not over-engineered.
- Two-pass arg scan in `pickRedactor` is a deliberate priority order (existing `*Error` wins over ctx-carried Redactor). Not a simplification opportunity.
- `context.WithRedactor` / `RedactorFrom` — design doc explicitly calls these out (mild anti-pattern accepted); the package doc even quotes the rationale. In-scope.
- Defensive copy in `secret.New` and `Redactor.Register` — required by the design's security considerations; `TestValueNewCopiesInput` pins it.

## Scope check

No files outside `internal/secret/` changed. No config, provider, resolver, or materializer code in this commit. Tests live alongside the code they exercise. `wip/` not touched by this commit (artifacts from parent workflow).

## Summary

- Blocking: 1 (F1 — hand-rolled stdlib replacement)
- Advisory: 3 (F2, F3, F4)

Recommend fixing F1 before merge. F2/F3/F4 are cheap-to-take cleanups but nothing compounds if kept.
