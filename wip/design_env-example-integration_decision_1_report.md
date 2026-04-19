<!-- decision:start id="env-example-parser-strategy" status="assumed" -->
### Decision: Parser implementation strategy for `.env.example` Node-style syntax

**Context**

`parseEnvFile` in `internal/workspace/materialize.go` (line 727) is a 15-line
function: read file, split on `\n`, trim whitespace, skip comments and blank
lines, cut on `=`, return `map[string]string` with an `error` return for
whole-file failures. Three call sites use it — two in `ResolveEnvVars`
(lines 563, 606) for workspace-level env files and per-repo discovered env
files, and one in `workspace_context.go` (line 311) for context env files.
All three call sites wrap the error and abort: they pass parse failures up the
stack as hard errors.

The new `.env.example` layer requires a parser that handles the full Node-style
syntax (single-quoted literals, double-quoted values with `\n`/`\t`/`\"`/`\\`
escape sequences, `export KEY=VALUE` prefix, CRLF normalization) and a
fundamentally different error model: per-line tolerance — bad line produces a
warning and parsing continues; the function never aborts for a malformed line.
This tolerance model is required only for `.env.example`; the existing callers
must not change behavior.

**Assumptions**

- The existing three call sites will not need Node-style syntax support in v1.
  If one of them does in the future, the decision can be revisited without
  rework — Option B's `parseDotEnvExample` can be called from those sites too.
- `parseEnvFile` has no direct unit tests today; it is exercised indirectly.
  This makes it lower risk to leave untouched.
- The per-line warning accumulation for `.env.example` will require a return
  type richer than `(map[string]string, error)` — likely
  `(map[string]string, []string, error)` or a dedicated result struct. This
  return type cannot be shared with the existing callers without changing their
  call sites.

**Chosen: Option B — New `parseDotEnvExample` function; leave `parseEnvFile` unchanged**

Introduce a new package-private function `parseDotEnvExample` in
`internal/workspace/materialize.go` (or a new `env_example.go` file in the
same package) alongside the existing `parseEnvFile`. The new function handles
the full Node-style syntax required by R6 and returns both a `map[string]string`
and a slice of per-line warning strings. The three existing call sites continue
to call `parseEnvFile` unchanged. Only the new `.env.example` materialization
code calls `parseDotEnvExample`.

**Rationale**

The decisive factor is the error model mismatch. The existing callers expect
`(map[string]string, error)` and abort on failure — they wrap the error with
`fmt.Errorf` and propagate it. The `.env.example` caller needs per-line
tolerance: a bad line emits a warning and parsing continues; the function
returns whatever valid entries it found. These two contracts cannot coexist
in a single function without either: (a) silently changing behavior for the
existing callers (Option A), or (b) adding a mode parameter that controls the
error model — at which point the "shared" function is effectively two functions
under one name with added indirection (Option C).

Option B makes the distinct semantics legible via function naming. A reader
unfamiliar with the codebase sees `parseEnvFile` (basic, strict, abort-on-error)
and `parseDotEnvExample` (Node-syntax, tolerant, warn-per-line) and immediately
understands they serve different contracts. This is idiomatic Go: prefer named
functions over mode-flag dispatch when behaviors differ substantially.

The shared logic between the two functions — split on newline, trim, skip
comments, cut on `=` for the bare-value case — is roughly five lines. This
overlap does not justify coupling two semantically different behaviors.
Deduplication at that granularity buys nothing and costs clarity.

The "no vendored dotenv library" constraint is already satisfied by both
options; it does not differentiate. The "independently testable" constraint
favors Option B: `parseDotEnvExample` tests are fully isolated from
`parseEnvFile` and from the materialization pipeline, enabling a compact unit
test table covering all R6 syntax variants without touching the existing
callers.

**Alternatives Considered**

- **Option A — Rewrite `parseEnvFile` in-place:** Handles R6 syntax at all
  three existing call sites. Rejected because the per-line tolerance model
  required for `.env.example` (warn+continue) conflicts with the abort-on-error
  semantics the existing callers rely on. Making the rewritten function
  tolerant by default silently changes behavior for workspace-level env files:
  a malformed workspace `.env` that today produces a hard error would become
  a silent partial parse. Making it conditionally tolerant (via a parameter)
  collapses into Option C.

- **Option C — Shared internal parser with `ParseMode` flag:** A single
  function with a mode value controlling syntax and error handling. Rejected
  because the two modes differ in syntax accepted, error handling contract,
  and return type shape — a mode flag that governs all three is effectively two
  functions stapled together. Option B achieves the same distinction via
  function names, which is idiomatic Go and adds no type-level complexity. If
  a third variant is needed in the future, both B and C require the same
  change: add a function (B) or add a mode value (C). Neither has an
  extensibility advantage.

**Consequences**

- `parseEnvFile` is unchanged. No risk of behavioral regression at the
  existing three call sites.
- `parseDotEnvExample` is a new package-private function with its own test
  file. Tests can cover all R6 syntax variants in isolation.
- The return signature of `parseDotEnvExample` can be designed for the
  `.env.example` use case without constraining the existing callers: e.g.,
  `(map[string]string, []string, error)` where the `[]string` slice carries
  per-line warning messages.
- Two parsers exist in the package. If both ever need the same syntax support,
  the shared five-line bare-`KEY=VALUE` path can be extracted to a helper at
  that time — no premature extraction required now.
<!-- decision:end -->
