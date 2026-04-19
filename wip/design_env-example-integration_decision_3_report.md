<!-- decision:start id="env-example-classification-structure" status="assumed" -->
### Decision: Classification structure for undeclared .env.example keys

**Context**

When `niwa apply` reads a `.env.example` file and finds a key that is not
declared in `[env.vars.*]` or `[env.secrets.*]`, it must decide whether the
value is a probable secret (fail apply with an error) or a safe placeholder
(warn and materialize as an implicit var). The classification logic is small —
roughly 40-60 LOC covering Shannon entropy over value characters and a
hardcoded known-prefix blocklist — but it must be independently unit-testable
per the constraints, and it must be easy to extend when new vendor prefixes or
allowlist patterns appear.

The question is where to place this logic: inside the integration point file,
in a sibling file within the `internal/workspace` package, or in a new
sub-package under `internal/workspace/envclassify/`.

The codebase has no existing sub-packages under `internal/workspace/`. The
package today is a flat collection of `.go` files (classify.go, materialize.go,
status.go, apply.go, etc.) with paired `_test.go` files in the same `workspace`
package. The `niwa status --audit-env` feature that would be the primary reuse
site for an exported classifier is not yet implemented; it is a planned PRD
scope item with no existing call site.

**Assumptions**

- `niwa status --audit-env` will eventually be implemented and will call into
  the same classification logic. If this never ships, the reuse argument for
  Option C is moot; Option B still wins on testability grounds.
- The known-prefix list and allowlist are defined as package-level slices, not
  struct fields, making them editable without API changes in either Option B
  or C.
- Classification does not need to be callable from outside the `niwa` binary
  (no plugin interface, no SDK). Intra-binary reuse does not require a
  separate package.

**Chosen: B — Standalone unexported helper in `internal/workspace/envclassify.go`, unit-tested by `envclassify_test.go`**

Place the classification logic in a dedicated file `internal/workspace/envclassify.go`
with an unexported function `classifyEnvValue(value string) (isSafe bool, reason string)`,
alongside an unexported `envPrefixBlocklist []string` and `envSafeAllowlist []string`
package-level vars. Pair it with `envclassify_test.go` in the same package,
which drives the function directly with table-driven tests covering entropy
boundary values, each blocklist prefix, each allowlist pattern, and empty values.

**Rationale**

The constraint is direct unit testability without a full pipeline. Option A fails
this: an inline private helper inside `materialize.go` can only be tested through
`ResolveEnvVars`, which requires a `MaterializeContext`, a temp repo directory,
and config wiring. Every classification edge case (entropy boundary, prefix
variant, allowlist match) forces a full materializer call. That is unnecessary
test complexity for a pure `(string) -> (bool, string)` function.

Option B delivers direct unit testability with zero additional structure cost.
Go allows any file in a package to test any other file's unexported symbols
from a `package workspace` test file. There is no need to export the function
to test it. The 40-60 LOC fits cleanly in a named file; the name
`envclassify.go` signals intent at a glance.

Option C (a sub-package) provides no benefit that Option B does not already
provide. The only advantage a sub-package offers over a sibling file is that
other packages can import the classifier without importing all of `workspace`.
That matters when there are external callers or a strict dependency boundary to
maintain. Neither condition holds here: `niwa status --audit-env` will live in
`internal/cli/status.go`, which already imports `internal/workspace`; it can
call `workspace.ClassifyEnvValue` just as easily. Creating a sub-package to
host 50 LOC adds a new `package envclassify` declaration, a new directory, and
a new import path, for no measurable gain. It also breaks with every existing
pattern in the `internal/workspace` directory, which has never used sub-packages
for functional grouping.

If `--audit-env` or a future command needs to call the classifier from a context
where importing all of `workspace` is genuinely undesirable, the function can be
promoted to exported and moved to a sub-package at that point. That refactor is
cheap — 50 LOC with one call site. There is no reason to pay the sub-package
overhead speculatively.

**Alternatives Considered**

- **A — Inline private helper in `materialize.go`**: Keeps code co-located with
  its only call site, but testability suffers. Integration tests of
  `ResolveEnvVars` can verify classification indirectly, but table-driven unit
  tests for 15+ boundary cases become impractical without a full pipeline setup.
  The constraint "independently unit-testable without a full pipeline" is not
  met. Rejected.

- **C — Exported `EnvClassifier` struct in `internal/workspace/envclassify/`**:
  Correct from a testability standpoint, but premature from a structural
  standpoint. No existing caller is outside `internal/workspace`. The reuse site
  (`niwa status --audit-env`) already imports `workspace`. A sub-package for
  50 LOC adds directory and package overhead with no current payoff and no
  precedent in the codebase. Rejected as premature.

**Consequences**

- Classification logic lives in `internal/workspace/envclassify.go` and is
  tested directly in `internal/workspace/envclassify_test.go`.
- The function remains unexported (`classifyEnvValue`) until a concrete external
  caller exists. If `--audit-env` needs it from `internal/cli`, export it at
  that point (`ClassifyEnvValue`) without moving it.
- The known-prefix blocklist and safe allowlist are package-level `var` slices in
  `envclassify.go`, making them readable and extendable in one place without
  touching the call site in `materialize.go`.
- Test coverage for classification is fast (no filesystem, no config parsing)
  and exhaustive: each blocklist prefix and allowlist pattern gets its own table
  row, plus boundary values for the entropy threshold.
<!-- decision:end -->
