# Maintainer Review — Issue #1 (secret runtime)

**Commit:** `b4cdbeaa8632a799bab16231bb1fe11fa20e1c1c` on `docs/vault-integration`
**Review lens:** Can the next developer understand and change this package with confidence?

**Overall:** The package is unusually well-documented for Go. Package-level comments
justify every non-obvious decision (gob refusal, Bytes vs bytes, Redactor on context,
grep-able UnsafeReveal). Tests are descriptively named and tied to ACs via comments.
The `UnsafeReveal` naming and sub-package split meet the goal of making intentional
plaintext access grep-able (verified: `rg UnsafeReveal` returns the handful of
expected files).

Approving with one blocking finding and a handful of advisories.

---

## Blocking

### B1. `Wrap` can silently drop the outer error message when the chain already contains a `*Error`

**Location:** `internal/secret/error.go:88-91`

```go
var existing *Error
if errors.As(err, &existing) && existing.redactor == r {
    return existing
}
```

`errors.As` walks the chain and binds `existing` to the *first* `*Error` it
finds — which may be arbitrarily deep inside `err`. When the redactor
pointers match, `Wrap` returns that inner `*Error`, discarding every layer
of `err` above it.

**The next-developer misread:** The comment says "If err is already a *Error
with the same Redactor, we don't need a new wrapper layer." A developer reads
that and assumes it's a direct-type check. The code actually does an
As-walk.

**The concrete footgun:**

```go
// Somewhere deep: returns a *secret.Error with redactor R
innerErr := someVaultCall()

// Caller wraps with plain fmt.Errorf, then hands off to secret.Wrap
// to add scrubbing for a new value:
return secret.Wrap(fmt.Errorf("reading %s: %w", path, innerErr))
```

Trace:
1. `inheritRedactor(err)` → walks to `innerErr`, returns `R`.
2. No values registered (none passed).
3. `errors.As(err, &existing)` → binds `existing = innerErr` (inner `*Error`,
   not `err` itself).
4. `existing.redactor == R == r` → returns `existing`.
5. The `"reading %s: "` prefix is silently dropped.

There's no test that covers this: `TestWrapPreservesRedactorAcrossReWrap`
passes Value-args to both Wrap calls, so a fresh `&Error{}` is always
returned on the way out; the shallow-return branch is never exercised with
an err that has non-secret.Error layers above a `*Error`.

**Suggested fix:** Either tighten the condition to a direct type check
(`if se, ok := err.(*Error); ok && se.redactor == r { return se }`) or
always wrap when `err != existing`. Update the comment to match.

**Why blocking:** The function is called `Wrap`. A caller expects wrapping
semantics. Silently dropping an error-message layer is the kind of bug that
shows up as "the error message changed, where did the context go?" during an
incident — exactly the wrong moment. Failure mode is data loss (context
loss) in error paths, which are already hard to debug.

---

## Advisory

### A1. Doc claim that `Format` honors width/precision doesn't match the implementation

**Location:** `internal/secret/value.go:113-114`

> *"Width and precision flags are honored against the placeholder, not against
> the plaintext."*

The implementation just calls `fmt.Fprint(s, placeholder)`. `fmt.Fprint`
writes raw bytes to the State's `Write` method; it does NOT consult the
State's width/precision flags. So `fmt.Sprintf("%10s", v)` returns `"***"`,
not `"       ***"`.

The test `TestValueFormatWidthPrecision` only checks that plaintext is absent
and `"***"` is present — it does not assert the width is applied. So the
tests pass, but the next developer who reads the doc and tries
`%10s`-for-alignment will get a silently wrong column.

**Fix options:** Either drop the claim from the docstring (width/precision
are ignored, which is the simplest correct behavior), or actually emit
padding using `fmt.Fprintf(s, "%*s", width, placeholder)` when `s.Width()` is
set. The first is fine.

---

### A2. `redactedPlaceholder` constant exists but isn't used where the literal appears

**Location:** `internal/secret/redactor.go:20` vs `internal/secret/value.go:103,108,128,130,138,144`, `error.go` has no constant

`redactor.go` defines `redactedPlaceholder = "***"` and uses it once. Every
other emission site in `value.go` uses the bare literal `"***"` (plus
`"secret.Value(***)"` twice and `"\"***\""` once). If someone ever updates
the placeholder convention, they have to touch six sites across two files;
missing one produces divergent emission between, say, `MarshalText` and
`Scrub`.

**Fix:** Export the constant (or move it to a small `placeholders.go`) and
use it everywhere. The `"secret.Value(...)"` wrapper can be a derived
constant.

---

### A3. Comment justifying the `replaceAll`/`indexOf` reinvention is weak

**Location:** `internal/secret/redactor.go:113-115,134-136`

> *"strings.ReplaceAll inlined to avoid importing the strings package
> alongside bytes — keeps the dependency footprint minimal and mirrors the
> byte-oriented design."*

`strings` is stdlib; there is no real footprint to mind. The next developer
will see 25 lines reinventing `strings.ReplaceAll` / `strings.Index` and
wonder whether there's a subtlety they're missing (allocation behavior? an
edge case?). There isn't.

Either use `strings.ReplaceAll` directly and delete the helpers, or update
the comment to explain the real reason if one exists (e.g., "needed for
future []byte-on-[]byte scrubbing without round-tripping through string" —
though the current code doesn't take that shape). As written, the comment
invites a refactor that then fails code review for "unnecessary churn."

---

### A4. `TestValueNewCopiesInput` doesn't actually verify what its name promises

**Location:** `internal/secret/value_test.go:207-228`

The test mutates the caller's buffer, then checks `MarshalJSON` still returns
`"\"***\""`. But `MarshalJSON` always returns the placeholder regardless of
whether the copy happened — so the test would pass even if `New` aliased
the buffer. The comment acknowledges "We can't directly read v's bytes
from this package" and settles for MarshalJSON as a "sanity check."

The real copy-verification test exists in
`internal/secret/reveal/reveal_test.go:TestUnsafeRevealAfterCopy`, which
does the right thing. The value_test version is misleadingly named — a
reader who sees it in a test report believes there's coverage where there
isn't.

**Fix options:** (a) Delete this test (reveal covers it). (b) Rename to
something like `TestValueNewPostMutationStillRedacts` to match what the body
actually checks. (c) Call `reveal.UnsafeReveal` from the test (it's an
internal package so the test can import it).

---

### A5. `Bytes` package-level function returns the live buffer without documenting the aliasing

**Location:** `internal/secret/value.go:169-171`

```go
func Bytes(v Value) []byte {
    return v.b
}
```

`reveal.UnsafeReveal` documents "callers MUST NOT retain or mutate" the
returned slice. But `secret.Bytes` itself — which is the actual plumbing —
only explains *why* it exists (to avoid a method on Value), not that it
returns the live internal buffer. A future contributor who decides to add
another caller inside `internal/secret/` (say, a helper that computes a
fingerprint) may not realize mutation propagates back into the Value.

**Fix:** One line: "The returned slice aliases the Value's internal buffer;
callers must not retain or mutate it." Bring the reveal docstring's warning
inline here too.

---

## Checklist against the review request

| # | Item | Result |
|---|------|--------|
| 1 | Exported functions documented with GoDoc | Yes — every exported symbol has a GoDoc comment, most with rationale |
| 2 | Signatures make data flow obvious | Mostly yes. `Wrap` hides the shallow-return surprise (see B1) |
| 3 | Test names descriptive | Yes, with the one caveat in A4 |
| 4 | `UnsafeReveal` grep-able | Yes. Name includes "Unsafe", lives in its own package, package docstring warns against new imports |
| 5 | `secret.Bytes` contract documented | Partly — purpose is documented, aliasing is not (A5) |
| 6 | Magic values named | `minFragmentLen = 6` and `redactedPlaceholder = "***"` are named in redactor.go. But the placeholder literal is duplicated across value.go (A2) |

## Verdict

Approve with B1 fixed. The advisories are real but don't block merge — A1
and A4 are misleading-doc/misleading-test issues that will bite on second
contact; A2/A3/A5 are readability nudges.
