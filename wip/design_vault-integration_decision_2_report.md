# Decision 2: secret.Value Type Shape and Error-Wrap Strategy

**Decision question.** What is the shape of the `secret.Value` type,
and how does redaction survive Go formatters, JSON/gob encoding,
error-wrap chains (`fmt.Errorf("...: %w", err)`), and captured
provider-CLI stderr?

**Mode.** --auto. Research-first, commit to a recommendation, no user
gating.

**Inputs surveyed.**
- PRD R20–R31, especially R22 and the error-wrapping sub-requirements
  (`docs/prds/PRD-vault-integration.md`:713-739, 996-1001).
- Architect review S-2 and M-1 (`wip/research/review_prd-vault_architect.md`).
- Security review M1 and S3 (`wip/research/review_prd-vault_security.md`).
- Current materialization flow (`internal/workspace/materialize.go`:
  `ResolveEnvVars` returns `map[string]string` today; `buildSettingsDoc`
  consumes the same type; `EnvMaterializer.Materialize` writes
  `.local.env` from `map[string]string`).
- Current merge (`internal/workspace/override.go`: merge operates on
  `map[string]string` values end-to-end).
- 186 `fmt.Errorf(...: %w, err)` sites across 30 files — the error-wrap
  surface is large and pre-existing.

---

## Options Evaluated

### Option 1. Struct wrapper with redacted formatters + `secret.Error`
                  wrapper type

**Shape.**

```go
type Value struct {
    b      []byte    // the plaintext bytes (nil when empty)
    origin originTag // immutable metadata: provider name, key, versionToken
}
```

- Value semantics, not pointer: passing a `Value` cannot silently alias.
- `b` is private; the only legal reader is `UnsafeReveal(v Value)` living
  in an allow-listed internal package (`internal/secret/reveal`).
- Zero value is a legal "empty" secret (`IsEmpty() bool`).

**Formatters — all emit `***`.**

- `String() string` → `"***"` (satisfies `fmt.Stringer`, covers `%s`, `%v`).
- `GoString() string` → `"secret.Value(***)"` (covers `%#v` and `%+v`
  fallback when the formatter reflects into the struct).
- `Format(s fmt.State, verb rune)` — explicit; handles `%q`, `%+v`,
  `%-5s`, width/precision. Without `Format`, Go's default verb handling
  can bypass `Stringer` for some verbs and reflect into private fields
  under `%#v`. We implement `Format` so **every** verb routes through
  `"***"` (or `"\"***\""` for `%q`, `"[REDACTED]"` for `%+v`).
- `MarshalJSON() ([]byte, error)` → `[]byte("\"***\"")` (guards
  `encoding/json` which does NOT honor `fmt.Stringer`).
- `MarshalText() ([]byte, error)` → `[]byte("***")` (guards any
  library that honors `encoding.TextMarshaler`: YAML, TOML, and
  stdlib consumers).
- `GobEncode() ([]byte, error)` → returns
  `(nil, errors.New("secret.Value: refusing gob encoding"))`.
  Refusing outright is stricter than emitting `***`; we do not want
  a silently-lossy round-trip through gob to seed some future code
  path with `***` as if it were the real value.
- `MarshalBinary` / `AppendEncoder` — refuse with same error.

**Error-wrap strategy.**

```go
// Error is an error type that scrubs secret-derived substrings from
// its Error() method output, and propagates through errors.Is/As.
type Error struct {
    inner     error
    redactor  *Redactor // shared with the package-level scrubber
}

func (e *Error) Error() string { return e.redactor.Scrub(e.inner.Error()) }
func (e *Error) Unwrap() error { return e.inner }
```

- `secret.Wrap(err, values ...Value) error`. Registers the plaintext
  bytes of each `Value` as a scrub fragment on a per-context
  `Redactor`, then returns a `secret.Error` wrapping `err`. Multiple
  fragments accumulate.
- `secret.Errorf(format string, args ...any) error`. Drop-in for
  `fmt.Errorf`. Scans `args` for `Value` or `*Value`, auto-registers
  their bytes as fragments, and returns a `secret.Error`. Preserves
  `%w`.
- The `Redactor` is per-resolver-call (attached to a `context.Context`
  via typed key, not goroutine-local). It holds a `[][]byte` of
  fragments and a `Scrub(s string) string` method that does a
  single-pass replacement of every non-empty fragment with `***`.
  Also scrubs any trailing substring of length ≥8 that appears
  verbatim in the fragment set (for partial leaks from line-wrapping
  or JSON fragments; bounded by fragment count × input length, still
  O(n) per scrub because the fragment set is O(1)-sized per resolve).
- Scrubbing is applied **before** interpolation into new errors, not
  after. `secret.Wrap` scrubs once at wrap time and caches the scrubbed
  string; wrapping that same error a second time is idempotent.
- `errors.Is` / `errors.As` work normally: `Unwrap()` returns the
  inner chain.

**Provider-CLI stderr path.** The provider package's subprocess runner
uses a `scrubbingReader` that wraps `cmd.Stderr`. On auth failure, the
captured stderr is **never** interpolated raw into a returned error;
the runner calls:

```go
stderrBytes := runner.StderrBuffer()          // raw capture
scrubbed    := redactor.Scrub(string(stderrBytes))
return secret.Wrap(
    fmt.Errorf("infisical: exit %d: %s", code, scrubbed),
    knownFragments...)
```

Even if `scrubbed` still contains a fragment the redactor didn't know
about (e.g., a token the provider logged but niwa never saw as a
`Value`), the outer `secret.Wrap` call will scrub again using any
fragments registered on `context`. Defense in depth.

**Type API: materialization affordance.** Materializers need bytes.
They call `secret.UnsafeReveal(v) []byte` from
`internal/secret/reveal`. The function name is `UnsafeReveal`
deliberately (grep-able in code review). It lives in a separate
sub-package so a `go/analysis` linter can flag every call-site outside
`internal/workspace/materialize.go` and
`internal/vault/provider/*` (the only legal callers).

**Trade-offs.**
- **+** Handles the hardest case (error-wrap interpolation of
  provider-CLI stderr carrying fragments niwa didn't explicitly tag)
  via the redactor.
- **+** `UnsafeReveal` is a single grep-able entry point.
- **+** Zero new Go deps (R20): everything uses stdlib interfaces.
- **+** Works for every invariant R22 enumerates.
- **−** `secret.Wrap` / `secret.Errorf` is a new idiom the codebase
  must learn. 186 existing `fmt.Errorf(...: %w)` sites don't need to
  change — only the ~10 sites in `internal/vault/` and the materializer
  that carry a `Value`. The linter flags new violations.
- **−** `Redactor` in `context.Context` is a mild anti-pattern (Go
  stdlib discourages context values for non-request-scoped data), but
  a per-resolver-call redactor IS request-scoped in the HTTP-server
  sense, and the alternative (thread a `*Redactor` through every
  function) bloats signatures.

### Option 2. String alias with marshal traps only

**Shape.** `type Value string`. Methods: `String()`, `MarshalJSON`,
`MarshalText`. No struct, no private field.

**Error-wrap.** Falls back to `fmt.Errorf`'s default behavior. Cannot
intercept `fmt.Errorf("failed: %w", err).Error()` when `err`'s chain
contains a wrapped error whose message was built from raw provider
stderr bytes. The alias's formatter only fires when a `Value` is
**directly** interpolated — once the value is inside an error message
as a substring, no method on `Value` runs.

**Trade-offs.**
- **+** Smaller surface; zero new error type.
- **−** Fatally breaks R22's error-wrap sub-requirement. An auth-failure
  stderr interpolated into `fmt.Errorf("infisical: %s", stderrStr)`
  and then `%w`-wrapped once is unreachable.
- **−** `type Value string` also inherits `+` operator and implicit
  conversions that ruin grep-ability of plaintext access.
- **Rejected as insufficient.** Fails the acceptance test by construction.

### Option 3. Interface with per-backend implementation

**Shape.**

```go
type Value interface {
    Bytes() []byte      // plaintext accessor
    Redacted() string   // redacted form for logs
    fmt.Stringer
}
```

Each backend returns its own concrete type implementing `Value`.

**Error-wrap.** Same weakness as Option 2 — interfaces don't intercept
error-chain interpolation of substrings. Would require a separate
`secret.Error` wrapper anyway, making the interface layer gratuitous.

**Trade-offs.**
- **+** Per-backend implementation can carry backend-specific metadata.
- **−** `Bytes()` on the interface is a trivially-callable plaintext
  accessor — loses the grep-ability advantage of `UnsafeReveal`.
- **−** An interface value is nil-comparable in a way `Value{}` (zero
  struct) isn't; opens a footgun where `var v secret.Value; v == nil`
  compiles and is true, making empty-vs-unresolved distinctions
  confusing.
- **−** Each backend could implement `Redacted()` differently
  (inconsistency risk). Centralizing formatters on one type prevents
  this.
- **Rejected.** The metadata-per-backend argument is real but belongs
  on `VersionToken` (Decision D4), not on `Value`.

### Option 4. Struct wrapper + taint-propagation + go/analysis linter

**Shape.** Same struct as Option 1.

**Addition.** A custom `go/analysis` linter:
- Flags every `fmt.Errorf(..., %w, err)` where `err`'s type flows from
  a function returning `(secret.Value, _, error)` or a function in
  `internal/vault/provider/` unless the call is `secret.Errorf` or
  `secret.Wrap`.
- Flags every conversion `string(v)` or `[]byte(v)` where `v` is a
  `secret.Value`, unless the call is in an allow-listed package.
- Flags `fmt.Fprintf(os.Stderr, ..., v)` if `v` is `secret.Value`
  without `Value.String()` being the effective formatter (covers
  `%v` mistakes).

**Trade-offs vs Option 1.**
- **+** Catches human mistakes at CI time instead of relying on runtime
  scrubbing.
- **+** Narrows the blast radius of a missed `secret.Wrap` call:
  runtime scrubbing still fires (Option 1's redactor), but the linter
  shouts loudly in PR review.
- **−** Custom `go/analysis` linter is ~300 LoC of new code in a
  zero-dep repo. It adds a CI step and a maintenance surface.
- **−** A linter that only niwa uses can diverge from upstream Go
  tooling (shared with `go vet`).

---

## Chosen

**Option 1, with the Option 4 linter deferred to a follow-up issue
(not blocking v1).**

The type is a struct wrapper `secret.Value` with all six formatters
(`String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`,
`GobEncode`) redacted or refusing. The error-wrap strategy is a
`secret.Error` type plus a `Redactor` that scrubs substrings, exposed
via `secret.Wrap(err, ...Value)` and `secret.Errorf(format, ...any)`
helpers. Materialization calls `secret.UnsafeReveal(v) []byte` from a
grep-able allow-listed sub-package. The linter from Option 4 is a
Phase 6 (post-v1) hardening — it's a quality-of-life CI check, not a
correctness gate, because the runtime scrubber in Option 1 already
handles leaks the linter would otherwise catch.

## Rationale

R22's "survive `fmt.Errorf("...: %w", err)`" requirement is the sharp
edge that rules out Options 2 and 3 — once a secret-derived substring
is baked into an error string somewhere in the chain, no method on
the value-type ever runs again, so formatter traps alone are
insufficient. A substring-scrubbing `secret.Error` wrapper (or
equivalent) is mandatory; the PRD already names it. Option 1 gives us
exactly that, plus every formatter the PRD enumerates, with zero
external dependencies. The `Redactor`-in-context design keeps the
plaintext bytes out of every function signature while still scoping
the scrub to a single resolver call (no global state). A custom linter
(Option 4) is appealing but adds maintenance cost for a
correctness-at-runtime property Option 1 already provides; ship it as
hardening once the shape has proved out.

## Rejected

- **Option 2 (string alias):** cannot intercept substrings already
  baked into error-chain strings, fails R22's error-wrap acceptance
  test.
- **Option 3 (interface):** exposes `Bytes()` as a trivially-callable
  plaintext accessor, loses the grep-able audit hook that makes
  `UnsafeReveal` a single inspection point.
- **Option 4 (linter addition in v1):** valuable but not
  correctness-critical given Option 1's runtime scrubber; the linter
  ships as a follow-up to avoid adding a new CI-build toolchain piece
  on the v1 critical path.

## Type API Sketch

```go
// Package secret provides an opaque secret.Value type, error-wrapper,
// and a Redactor that scrubs known secret fragments from strings
// (including error-chain strings). Invariants R22, R27, R28 are
// enforced here.
package secret

import (
    "context"
    "encoding/gob"
    "errors"
    "fmt"
)

// Value is the opaque runtime representation of a resolved secret.
// The zero Value is the legal "empty secret" (IsEmpty()==true).
// Value is a value type; pass by copy. All formatters redact. The
// plaintext bytes are reachable ONLY via
// internal/secret/reveal.UnsafeReveal, which is an allow-listed
// call-site audit point.
type Value struct {
    b      []byte    // plaintext; nil for empty
    origin originTag // immutable provider/key/version; for diagnostics
}

type originTag struct {
    providerName string
    key          string
    versionToken string
}

// New constructs a Value from plaintext bytes. b is copied defensively.
// Callers (providers, typically) SHOULD wipe their source buffer after.
func New(b []byte, providerName, key, versionToken string) Value {
    cp := make([]byte, len(b))
    copy(cp, b)
    return Value{b: cp, origin: originTag{providerName, key, versionToken}}
}

func (v Value) IsEmpty() bool { return len(v.b) == 0 }

// Origin returns non-secret provenance metadata. Safe to log.
func (v Value) Origin() (provider, key, version string) {
    return v.origin.providerName, v.origin.key, v.origin.versionToken
}

// String implements fmt.Stringer. Always returns "***".
func (v Value) String() string { return "***" }

// GoString implements fmt.GoStringer. Always returns "secret.Value(***)".
func (v Value) GoString() string { return "secret.Value(***)" }

// Format implements fmt.Formatter. Routes EVERY verb to a redacted
// form so width/precision/quoting never reach v.b.
func (v Value) Format(s fmt.State, verb rune) {
    switch verb {
    case 'q':
        fmt.Fprint(s, `"***"`)
    case 'v':
        if s.Flag('+') || s.Flag('#') {
            fmt.Fprint(s, "secret.Value(***)")
            return
        }
        fmt.Fprint(s, "***")
    default:
        fmt.Fprint(s, "***")
    }
}

// MarshalJSON guards encoding/json which does NOT honor fmt.Stringer.
func (v Value) MarshalJSON() ([]byte, error) {
    return []byte(`"***"`), nil
}

// MarshalText guards encoding.TextMarshaler consumers (YAML, TOML,
// stdlib-driven encoders).
func (v Value) MarshalText() ([]byte, error) {
    return []byte("***"), nil
}

// GobEncode refuses gob encoding outright. A silent redaction here
// would let a caller gob-round-trip "***" into a Value field and
// corrupt state.
func (v Value) GobEncode() ([]byte, error) {
    return nil, errors.New("secret.Value: refusing gob encoding (R22)")
}

// Assertion: Value implements every required interface.
var (
    _ fmt.Stringer         = Value{}
    _ fmt.GoStringer       = Value{}
    _ fmt.Formatter        = Value{}
    _ gob.GobEncoder       = Value{}
)

// ---- Error-wrap layer ----

// Error wraps an error and scrubs its Error() string through a
// Redactor. Unwrap returns the inner error unchanged so errors.Is
// and errors.As propagate.
type Error struct {
    inner error
    r     *Redactor
}

func (e *Error) Error() string {
    if e.inner == nil {
        return ""
    }
    return e.r.Scrub(e.inner.Error())
}
func (e *Error) Unwrap() error { return e.inner }

// Wrap returns an *Error that scrubs any occurrence of values'
// plaintext bytes from the eventual Error() output. Extra Value
// arguments accumulate fragments into the call-scoped Redactor.
// If err is already an *Error, its Redactor is extended rather
// than re-wrapped, so double-wrapping is idempotent.
func Wrap(ctx context.Context, err error, values ...Value) error {
    if err == nil {
        return nil
    }
    r := redactorFrom(ctx)
    for _, v := range values {
        r.Add(v.b)
    }
    if existing, ok := err.(*Error); ok && existing.r == r {
        return existing
    }
    return &Error{inner: err, r: r}
}

// Errorf is a drop-in for fmt.Errorf that auto-scans args for Value
// arguments, registers their bytes on the ctx-scoped Redactor, then
// wraps the formatted error.
func Errorf(ctx context.Context, format string, args ...any) error {
    r := redactorFrom(ctx)
    for _, a := range args {
        switch v := a.(type) {
        case Value:
            r.Add(v.b)
        case *Value:
            if v != nil {
                r.Add(v.b)
            }
        }
    }
    return &Error{inner: fmt.Errorf(format, args...), r: r}
}

// ---- Redactor ----

type Redactor struct {
    fragments [][]byte // all known plaintext fragments to scrub
}

// Add registers a fragment. Empty and very short fragments (<6 bytes)
// are ignored to avoid gratuitous substring matches on common English.
func (r *Redactor) Add(b []byte) {
    if len(b) < 6 {
        return
    }
    cp := make([]byte, len(b))
    copy(cp, b)
    r.fragments = append(r.fragments, cp)
}

// Scrub returns s with every registered fragment replaced by "***".
// Replacement is byte-literal (no regex). Multiple overlapping
// fragments are replaced longest-first to avoid partial-match gaps.
func (r *Redactor) Scrub(s string) string { /* ... */ }

type redactorKey struct{}

// WithRedactor attaches a fresh Redactor to ctx. Each resolver call
// SHOULD create one at entry and attach it for the lifetime of the
// call.
func WithRedactor(ctx context.Context) context.Context {
    return context.WithValue(ctx, redactorKey{}, &Redactor{})
}

func redactorFrom(ctx context.Context) *Redactor {
    if r, ok := ctx.Value(redactorKey{}).(*Redactor); ok && r != nil {
        return r
    }
    return &Redactor{} // callable but non-propagating; fail-safe
}
```

**Materialization affordance** — in a separate package that is the
**only** legal plaintext-read site:

```go
// Package reveal exposes plaintext access to secret.Value. The
// single entry point UnsafeReveal is grep-able and its call-sites
// must live in the allow-list enforced by CI (materialize.go,
// provider runners).
package reveal // import "github.com/tsukumogami/niwa/internal/secret/reveal"

import "github.com/tsukumogami/niwa/internal/secret"

// UnsafeReveal returns the plaintext bytes backing v. Callers MUST
// ensure the returned slice never reaches a formatter, a log, or a
// subprocess argv. Defensive copy: the returned slice is safe to
// mutate.
func UnsafeReveal(v secret.Value) []byte { /* ... */ }
```

**Materializer call-site shape:**

```go
// EnvMaterializer.Materialize — revised skeleton
for _, k := range keys {
    v := resolved[k] // secret.Value
    buf.WriteString(k)
    buf.WriteByte('=')
    buf.Write(reveal.UnsafeReveal(v))
    buf.WriteByte('\n')
}
os.WriteFile(target, buf.Bytes(), 0o600) // R24
```

**Provider runner call-site shape:**

```go
// internal/vault/provider/infisical/runner.go — sketch
cmd := exec.CommandContext(ctx, "infisical", args...)
var stderr bytes.Buffer
cmd.Stderr = &stderr
out, err := cmd.Output()
if err != nil {
    scrubbed := secret.RedactorFromContext(ctx).Scrub(stderr.String())
    return nil, secret.Errorf(ctx,
        "infisical %s: exit error: %s: %w",
        args[0], scrubbed, err)
}
```

## Enforcement Strategy

| Leak class | Enforcement mechanism | Confidence |
|-----------|----------------------|-----------|
| `fmt.Printf("%s", v)` / `%v` / `%+v` / `%q` of a `Value` | `Value.Format` intercepts every verb | Compile-time (type) + runtime (always "***") — high |
| `json.Marshal(v)` of a struct embedding `Value` | `Value.MarshalJSON` | Runtime — high |
| `yaml.Marshal` / `toml.Marshal` | `Value.MarshalText` | Runtime — high (both libraries honor TextMarshaler) |
| `gob.NewEncoder(w).Encode(v)` | `GobEncode` returns error | Runtime — refuses outright |
| `fmt.Errorf("...: %s", v)` direct interpolation | `Value.String()`/`Format` returns "***" | Runtime — high |
| `fmt.Errorf("...: %w", providerErr)` where `providerErr` carries stderr | `secret.Errorf` / `secret.Wrap` scrubs via ctx-scoped `Redactor` | Runtime — requires discipline at provider runner boundary; acceptance test validates |
| Provider CLI stderr interpolated raw into an error | Runner scrubs stderr BEFORE interpolation + outer `Wrap` re-scrubs | Runtime — double layer |
| `os.Setenv("KEY", string(revealed))` (R28) | No API exists that implicitly converts `Value` → string; `UnsafeReveal` is grep-able | Code review (grep) — medium; see follow-up linter |
| Accidental plaintext round-trip through gob | `GobEncode` refuses; caller sees error | Runtime — high |
| Disk cache (R29) | No `MarshalBinary` / `MarshalDisk` exists on `Value`; persistence hooks absent by design | Compile-time (type) — high |
| Future accidental `fmt.Errorf("%w", err)` in a new vault-touching code path that forgets `secret.Wrap` | Runtime scrubber still fires **if** ctx-scoped Redactor has the fragment; otherwise a leak | Medium — followed up by Option 4 linter in a post-v1 issue |

**Test coverage (from PRD acceptance criteria, §Security):**

- Unit: `Value` formatters emit `***` under `%s`, `%v`, `%+v`, `%q`,
  `%#v`, and `json.Marshal` (PRD line 996-997).
- Unit: `secret.Errorf` wrapping an error whose `Error()` contains
  registered fragments returns a string that does NOT contain any
  fragment.
- Unit: gob round-trip of a struct with a `Value` field returns
  `GobEncode` error.
- Integration / acceptance: provider runner for `infisical` with a
  mocked auth-failure stderr containing a known-fragment token — the
  returned error's `Error()` does NOT contain the fragment (PRD
  line 736-739).
- Grep-based CI check (cheap): assert no file outside an allow-list
  calls `reveal.UnsafeReveal`.
- Integration: `niwa apply` test that intercepts `os.Setenv` (monkey-
  patched via test harness) confirms it is never called with a
  resolved secret (R28, PRD line 1001).

## Open Items for Phase 3 Cross-Validation

1. **D1 (pipeline ordering) coupling.** D1 decides WHERE resolution
   happens relative to the merge. This decision assumes `secret.Value`
   is the type that flows **out** of the per-file resolution stage
   into the merge pipeline. D1 must confirm: (a) the merge stage
   operates on `secret.Value` (not strings) for secret-bearing fields;
   (b) a `context.Context` with an attached `Redactor` is threaded
   through the resolution stage so `secret.Errorf` can find it;
   (c) the merge's existing "last-writer-wins" semantics (see
   `internal/workspace/override.go:28-123`) work on `secret.Value`
   unchanged — `Value` is comparable by reference copy, not by
   content, so this should hold.

2. **D3 (provider interface) coupling.** D3 pins the `Resolve`
   signature. This decision assumes the signature is
   `Resolve(ctx context.Context, ref Ref) (secret.Value, VersionToken, error)`
   per the architect-review sketch (`wip/research/review_prd-vault_architect.md`:165-170)
   and that the `ctx` passed in already carries a `Redactor` (attached
   upstream in the resolver pipeline via `secret.WithRedactor(ctx)`).
   D3 must confirm: (a) providers MUST use `secret.Errorf` / `secret.Wrap`
   rather than `fmt.Errorf` for any error touching provider output;
   (b) providers MUST scrub captured subprocess stderr before
   interpolation; (c) `Close()` does not see `Value` (no redaction
   concern).

3. **D4 (SourceFingerprint) coupling.** `Value.origin.versionToken` is
   the hook D4 uses to build `SourceFingerprint`. This decision carves
   out the field but leaves the token shape to D4. Cross-check: D4
   must confirm that `versionToken` is **non-secret** (e.g., an
   Infisical version ID, a sops blob SHA-256, a git commit SHA) so
   that `Value.Origin()` can return it for diagnostic rendering
   without redaction.

4. **Linter follow-up (Option 4).** File a v1.1 issue titled
   "add go/analysis linter for secret.Value call-sites" that wires in
   the checks Option 4 sketched: forbidden `fmt.Errorf(%w, err)` on
   provider errors, forbidden `string(Value)` conversions, forbidden
   `reveal.UnsafeReveal` calls outside the allow-list.

5. **Memory hygiene deferred (security review S1).** Security-review
   S1 raised core-dump / swap leakage (`wip/research/review_prd-vault_security.md`:79-83).
   This decision does NOT address that — `Value.b` sits in Go's GC'd
   heap, not `mlock`ed memory. The PRD explicitly scopes this as
   out-of-scope defense-in-depth. Noted here so Phase 5 (Security
   Considerations) can document the gap.
