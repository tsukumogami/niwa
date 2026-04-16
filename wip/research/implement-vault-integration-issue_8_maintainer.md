# Maintainer Review — Issue #8 (shadow detection + diagnostics)

Commit `5ffd02adbb00b899a32c7e4e4e90e7d182fefa62` on `docs/vault-integration`.

Focus: maintainability — can the next developer understand this and change it
with confidence?

## Verdict

Approve. No blocking findings. Two advisory items worth mentioning; neither
rises to the bar of misread-risk for the next developer.

## Checklist walkthrough

### 1. Shadow `Kind` values defined as constants — PASS

`internal/workspace/shadows.go:57-64` declares the six kind values as
unexported package constants (`shadowKindEnvVar`, `shadowKindEnvSecret`,
`shadowKindClaudeEnvVar`, `shadowKindClaudeEnvSecret`, `shadowKindFiles`,
`shadowKindSettings`) and `DetectShadows` uses them exclusively through the
`add()` helper. The comment at lines 53-56 explains why the constants stay
unexported: "diagnostics match on the string, not the identifier." That is
the right call — it avoids coupling CLI consumers to the Go identifier and
forces them to match on the wire format, which is what `Shadow.Kind` actually
documents (lines 22-24).

The exported constant `ShadowLayerPersonalOverlay` (line 51) is used both by
`DetectShadows` and by `status.go:238` (inside a comment). Good.

Note that `apply.go:349-350` and the `renderShadowedColumn` callers still use
`sh.Kind` / raw string literals for matching, but that is appropriate given
the package boundary.

### 2. Stderr diagnostic format stability and documentation — PASS (advisory)

The two format strings are:

    shadowed provider %q [personal-overlay shadows team: team=%s, personal=%s]
    shadowed %s %q    [personal-overlay shadows team: team=%s, personal=%s]

Pinned by `apply_vault_test.go:327` (the full line including bracket
attribution). That test is the de-facto format spec; the assertion is a
substring match so reordering inside the brackets would break it. Adequate
for v1.

**Advisory.** The format is not documented in a comment (e.g., a package-level
doc or a constant labeled "diagnostic format"). The next developer adding a
new shadow source will grep for `shadowed %s %q` and discover the pattern
empirically. Given that the test pins the exact string and the format only
lives in two `fmt.Fprintf` call sites, this is acceptable but worth a
follow-up note once Issue 10 settles on the final audit-secrets table layout.

### 3. Summary line grammar for 1 vs N shadows — PASS

`status.go:217-224`:

    suffix := "keys"
    if len(state.Shadows) == 1 {
        suffix = "key"
    }

`TestStatusSummaryLineReflectsShadowCount` in `status_test.go:185-253` locks
in three cases: zero (no line), one ("1 key shadowed by personal overlay"),
multiple ("2 keys shadowed by personal overlay"). Grammatically correct and
regression-guarded.

### 4. Error messages from `DetectProviderShadows` — N/A

`vault/shadows.go:51` — the function signature is
`DetectProviderShadows(team, personal *Bundle) []ProviderShadow`. No error
return, no error messages to review. The doc comment (lines 34-50) explains
the nil-bundle contract ("Both bundles nil returns nil") and the ordering
guarantee, which is what a caller needs.

### 5. `renderShadowedColumn` TODO / hand-off to Issue 10 — PASS

`status.go:229-241` is explicit about the hand-off:

  - "Issue 10 wires the flag and subcommand surface; Issue 8 installs this
    helper so the state-side data is already in place once the flag parser
    lands."
  - "Threading scope into Shadow is a follow-up once Issue 10 designs the
    column header."

The doc names Issue 10 three times and describes the specific v1 gap
(scope= suffix not yet emitted). The next developer picking up Issue 10 will
not miss this.

### 6. `VaultRegistry` lacking `SourceFile` — PASS

Two separate comments document this intentional omission:

  - `internal/workspace/shadows.go:66-75`: "Both files are the canonical v1
    locations; Issue 7+ may add per-struct SourceFile fields, in which case
    DetectShadows can be extended to prefer those values over the defaults."
  - `internal/vault/shadows.go:26-33` (ProviderShadow.TeamSource doc): "The
    Bundle does not retain ProviderSpec sources post-Build, so v1 callers
    populate this from the pipeline's known file paths; the field is carried
    here for forward compatibility with a future Bundle.ProviderSource API."

A future maintainer wiring per-provider attribution will find both breadcrumbs
without having to reconstruct the rationale.

## Advisory findings

### A1. Divergent twins: `"workspace.toml"` / `"niwa.toml"` literals in apply.go

`internal/workspace/apply.go:328` hard-codes the two source paths in the
provider-shadow stderr line:

    fmt.Fprintf(os.Stderr,
        "shadowed provider %q [personal-overlay shadows team: team=%s, personal=%s]\n",
        label, "workspace.toml", "niwa.toml")

Meanwhile `internal/workspace/shadows.go:72-75` declares
`teamSourceDefault = "workspace.toml"` / `personalSourceDefault = "niwa.toml"`
as unexported constants and uses them for the `DetectShadows` path. The two
code sites will silently drift if either value is renamed, because the
apply.go callsite cannot reach the unexported constants from shadows.go
(both files are in the `workspace` package, actually, so it could — the
apply.go line just doesn't use them).

The comment at `vault/shadows.go:44-46` explicitly hands this attribution job
to the CLI wrapper, so the architecture is sound. But the next developer who
rationalizes the string defaults will have to touch both files and notice
the twin. **Advisory** — not blocking because the strings are single-line,
both kept literal-beside-literal, and the Issue 9/10 file-path work is
already scheduled to revisit attribution. A one-line fix is to reference
`teamSourceDefault` and `personalSourceDefault` from apply.go; safe since
they are in the same package.

### A2. Pre-existing issue outside Issue 8 scope (not blocking)

Unrelated to this commit but flagged for completeness while reading status.go:
lines 129-132 contain a dead branch — `driftLabel` is set to `"drifted"` in
both the initial assignment and the `if status.DriftCount == 0` body, so the
conditional is a no-op. The summary view prints `"0 drifted"` regardless of
count. Not introduced by Issue 8 and not in scope, but worth a follow-up
issue if no one has filed one.

## Summary

The diagnostic pipeline is well-annotated. Every forward-compatibility
decision (unexported kind constants, default source paths, omitted scope
suffix, ProviderShadow sources left blank, renderShadowedColumn as a dormant
helper) is backed by a comment naming the specific future issue or
constraint. The tests lock in the invariants that matter most — no secret
bytes in stderr, no secret.Value on the Shadow struct, singular/plural
suffix, sort determinism.

The one divergent-twin in apply.go is cheap to tighten but not load-bearing.

**Recommendation: approve.**
