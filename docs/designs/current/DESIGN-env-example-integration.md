---
status: Proposed
upstream: docs/prds/PRD-env-example-integration.md
problem: |
  niwa's ResolveEnvVars function builds the .local.env map by merging config layers
  in last-write-wins order, but has no step for the app repo's .env.example file.
  Adding this lowest-priority layer requires a parser rewrite (the current parseEnvFile
  does not handle quoted values, export prefixes, or CRLF), a secret-classification
  pass for undeclared keys, and integration of the exclusion check against the
  fully-merged config's secrets declarations — all without breaking existing merge
  semantics or the source-fingerprinting system.
decision: |
  EnvMaterializer.Materialize gains a pre-pass (before calling ResolveEnvVars) that
  reads .env.example from the managed app repo, classifies each value, and stores the
  result on two new nil-initialized MaterializeContext fields (EnvExampleVars,
  EnvExampleSources). ResolveEnvVars gains a single nil-guarded opening block that
  seeds vars from those fields, making .env.example the lowest-priority layer.
  parseDotEnvExample (new function) handles Node-style syntax with per-line tolerance.
  classifyEnvValue (in envclassify.go) runs Shannon entropy and prefix detection for
  undeclared keys only. Warnings go via a new Stderr io.Writer field on EnvMaterializer.
  Opt-out flags (read_env_example *bool) are added to the workspace config schema.
rationale: |
  A pre-pass in EnvMaterializer.Materialize avoids the double-warning problem:
  ResolveEnvVars is called from both the env path and the settings path, so processing
  placed inside it fires per-key warnings twice per repo. The pre-pass is structurally
  bound to the correct phase boundary and the settings path sees nil fields, requiring
  no sentinels or deduplication. Keeping classification in envclassify.go satisfies
  the testability constraint without a sub-package. Threading stderr through
  EnvMaterializer follows the FilesMaterializer precedent and avoids global state.
  Collecting all probable-secret errors before failing gives users the full picture
  needed to fix a workspace on first migration rather than requiring multiple apply runs.
---

# DESIGN: .env.example Integration

## Status

Proposed

## Context and Problem Statement

`ResolveEnvVars` (`internal/workspace/materialize.go:538`) is the canonical merge
function that produces the `map[string]string` written to each repo's `.local.env`.
It applies layers in order — `[env.files]`, `[env.vars]`, `[env.secrets]`, per-repo
discovered file — using last-write-wins semantics so higher-priority sources
always override lower ones.

The function currently has no step for the managed app repo's `.env.example`. Adding
one requires solving three distinct technical problems:

1. **Parser gap.** `parseEnvFile` (line 727) splits on `\n` and `=` only. It doesn't
   handle single- or double-quoted values, `export KEY=VALUE` prefixes, escape
   sequences in double-quoted strings, or CRLF line endings. Feeding a typical
   Node-ecosystem `.env.example` through it produces incorrect values or drops lines.

2. **Exclusion check.** Keys declared in any `[env.secrets.*]` table must not receive
   a value from `.env.example`, even as the lowest-priority layer (PRD R4a). The check
   requires the _fully-merged_ config — base workspace + overlay + global override —
   because a key may be promoted to a secret by an overlay the user has applied. The
   merged config is already available in `ctx.Effective` at `ResolveEnvVars` call time.

3. **Classification.** For keys not found in the merged config at all (undeclared),
   the `.env.example` value must be classified: probable secret (error) or safe (warn +
   materialize). Classification runs Shannon entropy over the value's characters and
   checks known secret-vendor prefixes. It must not run for keys already declared as
   `[env.vars]` — those are trusted without inspection.

The public-repo guardrail (PRD R13) adds a fourth concern: the remote-visibility check
must run against the managed app repo's remote, not the workspace config repo's remote.
The existing `CheckGitHubPublicRemoteSecrets` helper checks the config dir's remote;
a per-repo call site is needed in the materializer loop.

## Decision Drivers

- `.env.example` is the lowest-priority layer; every existing layer must continue to
  override it without any new conditional logic.
- The exclusion set (secrets-declared keys) must be computed from the fully-merged
  config before any `.env.example` key is written to `vars`.
- `parseEnvFile` is called from three sites (`ResolveEnvVars:563`, `ResolveEnvVars:606`,
  `workspace_context.go:311`); the existing `[env.files]` and per-repo env file paths
  only use basic `KEY=VALUE` syntax, so the parser rewrite must not break them.
- Secret classification must be independently testable without a full pipeline setup.
- Warning and error output must be consistent with `FilesMaterializer`'s existing
  `Stderr io.Writer` pattern; no global state.
- Source attribution must integrate with the existing `SourceEntry` / `SourceKind`
  system so fingerprinting remains correct.
- Opt-out flags (`read_env_example = false`) require schema changes to the workspace
  config struct and workspace.toml parsing.
- Performance budget: 5 ms per repo for discovery + parse (PRD R21).

## Considered Options

### Decision 1: Parser implementation strategy

`parseEnvFile` (line 727 of `materialize.go`) is a 15-line function that reads
a file, splits on `\n`, skips comments, cuts on `=`, and returns
`(map[string]string, error)`. Three call sites use it — two in `ResolveEnvVars`
for `[env.files]` and per-repo discovered env files, one in
`workspace_context.go` for context env files. All three propagate any error as
a hard failure.

`.env.example` needs full Node-style syntax (single-quoted literals,
double-quoted escape sequences, `export KEY=VALUE` prefix, CRLF tolerance)
and a fundamentally different error model: per-line tolerance — a bad line
warns and parsing continues; the caller never aborts for a malformed line.

Key assumptions:
- The three existing call sites will not need Node-style syntax in v1.
- The per-line warning return requires a richer signature than
  `(map[string]string, error)`, which cannot be shared with existing callers
  without changing their call sites.

#### Chosen: New `parseDotEnvExample` function alongside `parseEnvFile`

Introduce `parseDotEnvExample` as a new package-private function in
`internal/workspace/` handling the full Node-style syntax. It returns
`(map[string]string, []string, error)` where `[]string` carries per-line
warning messages. The three existing call sites continue to call `parseEnvFile`
unchanged. Only the new `.env.example` materialization code calls
`parseDotEnvExample`.

The decisive factor is the error model mismatch. The two contracts — abort-on-error
vs. warn-per-line — cannot coexist in a single function without silently changing
existing callers or adding a mode flag that is effectively two functions under one
name. Distinct function names make the distinction legible without indirection.

#### Alternatives Considered

- **Rewrite `parseEnvFile` in-place:** Per-line tolerance conflicts with the
  abort-on-error semantics existing callers rely on. Making it tolerant by default
  silently changes behavior for workspace `[env.files]`. Rejected.
- **Shared parser with `ParseMode` flag:** A mode governing syntax, error
  contract, and return type is two functions stapled together. Option B achieves
  the same clarity via named functions without type-level complexity. Rejected.

---

### Decision 2: Integration point, opt-out flag checking, and stderr routing

`ResolveEnvVars` is called from two places: `EnvMaterializer.Materialize` (env
path, writes `.local.env`) and `resolveClaudeEnvVars` (settings path, resolves
`claude.env.promote` keys for `settings.local.json`). The materializer loop runs
settings before env. Any `.env.example` processing placed inside
`ResolveEnvVars` would fire per-key warnings and the per-repo public-remote
guardrail from both callers — once during the settings pass and once during the
env pass — with no clean deduplication path.

Key assumptions:
- `.env.example` keys are not promotable to `settings.local.json` in v1 (PRD
  scopes contribution to `.local.env` only).
- `read_env_example` opt-out schema fields don't yet exist; they're a
  prerequisite for this feature.
- The per-repo guardrail reuses `enumerateGitHubRemotes` against `ctx.RepoDir`,
  not the existing `CheckGitHubPublicRemoteSecrets` (which takes `configDir`).

#### Chosen: Pre-pass in `EnvMaterializer.Materialize`; result on `MaterializeContext`; `Stderr io.Writer` on `EnvMaterializer`

`EnvMaterializer.Materialize` gains a pre-pass before calling `ResolveEnvVars`:

1. Check workspace-level and per-repo opt-out from `ctx.Effective`; skip if disabled.
2. Discover and parse `.env.example` at `filepath.Join(ctx.RepoDir, ".env.example")`
   using `parseDotEnvExample`. Per-line warnings emit to `f.stderr()`; whole-file
   failures emit a single warning and short-circuit.
3. Build the secrets exclusion set from `ctx.Effective.Env.Secrets.*` and
   `ctx.Effective.Claude.Env.Secrets.*`; remove those keys from the parsed map.
4. Classify remaining undeclared keys (not in `ctx.Effective.Env.Vars.Values`):
   probable secrets error; safe keys warn and are included.
5. Run the per-repo public-remote guardrail against probable-secret keys.
6. Store the result in `ctx.EnvExampleVars map[string]string` and
   `ctx.EnvExampleSources []SourceEntry`.

`ResolveEnvVars` gains a nil-guarded opening block: if `ctx.EnvExampleVars` is
non-nil, seed `vars` from it first. When nil (settings path), the function is
unchanged. No signature change to `ResolveEnvVars`.

`EnvMaterializer` gains `Stderr io.Writer` and a `stderr()` helper (defaults to
`os.Stderr` when nil), matching the `FilesMaterializer` precedent exactly.

The double-warning problem disqualifies placing processing inside `ResolveEnvVars`.
A new materializer type would introduce a positional ordering dependency the type
system can't enforce. The pre-pass is structurally bound to the correct phase
boundary — it lives where it's used.

#### Alternatives Considered

- **First step inside `ResolveEnvVars`:** Per-key warnings and the per-repo
  guardrail fire twice per repo (settings path + env path). Fixing this requires
  sentinels or shared mutable state — more invasive than Option B. Rejected.
- **New `EnvExampleMaterializer` step:** Correct mechanics, but introduces a
  silent positional ordering invariant (must precede both settings and env
  materializers) the type system can't enforce. Rejected.

---

### Decision 3: Classification structure

Classification runs only for undeclared keys (not in `[env.vars.*]` or
`[env.secrets.*]`). The logic is ~50 LOC: Shannon entropy over value characters
(threshold 3.5 bits/char) plus a hardcoded known-prefix blocklist and safe
allowlist. It must be independently unit-testable without full pipeline setup.

`internal/workspace/` is a flat package with no existing sub-packages. The
primary future reuse site (`niwa status --audit-env`) is explicitly deferred in
the PRD and has no code today.

Key assumptions:
- Classification doesn't need to be callable from outside the `niwa` binary.
- The blocklist and allowlist are package-level slices, editable without API
  changes.

#### Chosen: Standalone unexported helper in `internal/workspace/envclassify.go`

`classifyEnvValue(value string) (isSafe bool, reason string)` is a package-private
function in `internal/workspace/envclassify.go`, paired with
`envclassify_test.go` in the same package. Package-level `envPrefixBlocklist`
and `envSafeAllowlist` slices hold the lists. Tests drive `classifyEnvValue`
directly with a table covering entropy boundaries, every blocklist prefix, every
allowlist pattern, and empty values — no `MaterializeContext` or filesystem needed.

Option A (inline in `materialize.go`) fails the testability constraint — every
entropy edge case would require a full pipeline setup. Option C (sub-package)
adds a directory and package overhead for 50 LOC with no current external caller,
breaking the flat `internal/workspace` convention. If `--audit-env` eventually
needs to call the classifier, promote the function to exported (`ClassifyEnvValue`)
at that point — a one-line change with one call site.

#### Alternatives Considered

- **Inline private helper in `materialize.go`:** Classification can only be
  tested indirectly through `ResolveEnvVars`, requiring full pipeline setup for
  15+ boundary cases. The testability constraint is not met. Rejected.
- **Exported `EnvClassifier` struct in `internal/workspace/envclassify/`:**
  No current external caller. `niwa status --audit-env` already imports
  `workspace`; a sub-package for 50 LOC adds overhead with no payoff and breaks
  the flat package convention. Rejected as premature.

---

### Decision 4: Probable-secret error collection strategy

When an undeclared key's value is classified as a probable secret, apply must
fail. The question is whether it fails immediately on the first such key or
collects all errors across all repos first.

#### Chosen: Collect all probable-secret errors across all repos; fail at end

The pre-pass accumulates probable-secret errors for all repos. After all repos
are processed, if any errors were collected, apply emits a single summary and
exits non-zero. Safe undeclared keys are warned about and included in the result
regardless of whether other repos had errors.

Rationale: a user fixing probable-secret problems in `.env.example` needs to see
all of them at once. Fail-fast forces multiple apply runs for what is likely a
one-time migration event. The collect-all approach has no additional cost — the
pre-pass reads every repo's file unconditionally (the opt-out check is per-repo,
not early-exit on first error).

#### Alternatives Considered

- **Fail immediately on first probable-secret key:** Faster signal on the first
  problem, but forces multiple fix-run cycles for workspaces with several
  problematic `.env.example` files. Rejected — worse UX for a one-time migration
  with no compensating benefit.

## Decision Outcome

The three decisions compose into a coherent whole with a minimal footprint:

`EnvMaterializer.Materialize` gains a pre-pass that reads `.env.example`, runs
`parseDotEnvExample` (new Node-style parser, tolerant per-line), builds the
secrets exclusion set from the fully-merged `ctx.Effective`, classifies
undeclared keys via `classifyEnvValue` (new `envclassify.go`), and stores
the result on two new nil-initialized `MaterializeContext` fields
(`EnvExampleVars`, `EnvExampleSources`). `ResolveEnvVars` gains a single
nil-guarded opening block that seeds `vars` from those fields — making
`.env.example` the lowest-priority layer without any conditional logic in the
existing merge stack. The settings path (which calls `ResolveEnvVars` too)
sees nil fields and behaves exactly as before.

The design adds: one new function (`parseDotEnvExample`), one new file
(`envclassify.go` + test), two new `MaterializeContext` fields, one new
`EnvMaterializer` field (`Stderr io.Writer`), and a nil-guarded opening block in
`ResolveEnvVars`. No existing signatures change. No new materializer types. No
new packages.

## Solution Architecture

### Overview

On each `niwa apply`, the env materializer gains a pre-pass that runs before
`ResolveEnvVars`. For each managed repo it: discovers `.env.example` at the
repo root, parses it with a new Node-style parser, filters out secrets-declared
keys, classifies undeclared keys, and stores the result on `MaterializeContext`.
`ResolveEnvVars` then reads those fields as its opening layer — seeding `vars`
before any config-declared layer — so every existing layer automatically overrides
without new conditional logic. When the settings materializer calls
`ResolveEnvVars` for promoted keys, the context fields are nil and the function
behaves exactly as before.

### Components

```
internal/workspace/
├── env_example.go          NEW  parseDotEnvExample, readEnvExampleFile
├── env_example_test.go     NEW  parser unit tests (all R6 syntax variants)
├── envclassify.go          NEW  classifyEnvValue, envPrefixBlocklist, envSafeAllowlist
├── envclassify_test.go     NEW  classifier unit tests (entropy boundaries, all prefixes)
├── materialize.go          MOD  EnvMaterializer (Stderr field, pre-pass), ResolveEnvVars
│                                (nil-guard opening block), MaterializeContext (two fields)
└── apply.go                MOD  set EnvMaterializer.Stderr when constructing materializer

internal/config/
└── workspace.go            MOD  ReadEnvExample bool on workspace-level and per-repo structs
```

### Key Interfaces

**`MaterializeContext` additions (`materialize.go`):**
```go
type MaterializeContext struct {
    // ... existing fields ...
    EnvExampleVars    map[string]string // nil until EnvMaterializer pre-pass runs
    EnvExampleSources []SourceEntry     // matching source attribution entries
}
```

**`EnvMaterializer` additions (`materialize.go`):**
```go
type EnvMaterializer struct {
    Stderr io.Writer // nil → os.Stderr; tests inject *bytes.Buffer
}
func (e *EnvMaterializer) stderr() io.Writer // returns Stderr or os.Stderr
```

**New parser (`env_example.go`):**
```go
// parseDotEnvExample reads a .env.example file using Node-style syntax.
// Returns the parsed key-value map, per-line warning strings (file:line:problem),
// and a whole-file error (permission denied, binary content, etc.).
// Per-line errors do not set the error return; they accumulate in warnings.
func parseDotEnvExample(path string) (map[string]string, []string, error)
```

**New classifier (`envclassify.go`):**
```go
// classifyEnvValue reports whether value is safe to materialize as an implicit
// var. isSafe=false means the value is a probable secret; reason names the
// detection rule (e.g. "known prefix sk_live_", "entropy 4.2 > 3.5").
// reason MUST NOT include the value, any fragment of the value, or the raw
// entropy score — only the rule name and threshold (R22: diagnostics never
// contain secret bytes).
func classifyEnvValue(value string) (isSafe bool, reason string)

var envPrefixBlocklist = []string{"sk_live_", "sk_test_", "AKIA", "ASIA",
    "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "github_pat_", "glpat-",
    "xoxb-", "xoxp-", "xapp-", "sq0atp-", "sq0csp-"}

var envSafeAllowlist = []string{ /* empty, changeme, pk_test_, pk_live_,
    <your-...>, example.com/org, localhost/127.0.0.1 patterns */ }
```

**Config schema additions (`internal/config/workspace.go`):**
```go
// Workspace-level [config] section
type WorkspaceMeta struct {
    // ... existing fields ...
    ReadEnvExample *bool `toml:"read_env_example"` // nil → true (opt-out default)
}

// Per-repo [repos.<n>] section
type RepoOverride struct {
    // ... existing fields ...
    ReadEnvExample *bool `toml:"read_env_example"` // nil → inherit workspace setting
}
```
Using `*bool` makes "unset" distinguishable from `false`, so per-repo `true` can
override a workspace-wide `false` (PRD R18/R17 interaction).

### Data Flow

For each managed repo during `EnvMaterializer.Materialize(ctx)`:

```
1. Opt-out check (workspace then per-repo via ctx.Effective)
   → skip pre-pass if disabled

2. Symlink check: os.Lstat(ctx.RepoDir + "/.env.example")
   → ModeSymlink → warning to f.stderr(), skip (treat as absent)
   → IsNotExist  → info log, envExampleVars = nil; skip remaining steps
   → other error → warning to f.stderr(), skip remaining steps

3. File size guard: stat(path).Size() > 512 KB
   → warning to f.stderr(), skip (R22 whole-file failure)

4. parseDotEnvExample(ctx.RepoDir + "/.env.example")
   → per-line warnings emitted to f.stderr() (file:line:problem — no value text)
   → whole-file error → warning + skip remaining steps

5. Build secrets exclusion set — walk ALL layers and ALL sub-tables:
   Effective.Env.Secrets.{Values, Required, Recommended, Optional}
   Effective.Claude.Env.Secrets.{Values, Required, Recommended, Optional}
   Effective.Repos[ctx.RepoName].Env.Secrets.{Values, Required, Recommended, Optional}
   → mirrors the scope of offendingKeys in internal/guardrail/githubpublic.go
   → remove matching keys from parsed map (silently; no key or value emitted)

6. For each remaining key:
   a. In ctx.Effective.Env.Vars.Values → trusted, include as-is (no classification)
   b. Undeclared → classifyEnvValue(value)
      isSafe=true  → warn "undeclared key <key>, treating as var" + include
      isSafe=false → accumulate error (file, line, key, reason — no value text)
                     + check per-repo public-remote guardrail (ctx.RepoDir)

7. If any probable-secret errors were accumulated → emit all + return error

8. ctx.EnvExampleVars = filtered map
   ctx.EnvExampleSources = []SourceEntry{Kind: SourceKindEnvExample, ...}

9. ResolveEnvVars(ctx) runs:
   opening block: if ctx.EnvExampleVars != nil { seed vars; append sources }
   then existing layers overwrite in priority order
```

## Implementation Approach

### Phase 1: Config schema and opt-out flags

Add `ReadEnvExample *bool` to the workspace-level config struct and per-repo
override struct in `internal/config/`. Add a helper `effectiveReadEnvExample(ws,
repoName string) bool` that resolves workspace default → per-repo override with
nil-pointer semantics (nil = unset = inherit/default-true).

Deliverables:
- `internal/config/workspace.go` — schema additions
- `internal/config/workspace_test.go` — TOML round-trip tests for the new fields

### Phase 2: Node-style parser

Implement `parseDotEnvExample` in `internal/workspace/env_example.go` covering:
single-quoted literals, double-quoted escape sequences (`\n`, `\t`, `\"`, `\\`),
`export KEY=VALUE` prefix, CRLF normalization, blank-line and comment skipping,
per-line tolerance (bad line → warning + continue, no abort).

Deliverables:
- `internal/workspace/env_example.go`
- `internal/workspace/env_example_test.go` (table-driven, all R6 variants)

### Phase 3: Value classifier

Implement `classifyEnvValue` in `internal/workspace/envclassify.go` with
Shannon entropy calculation, `envPrefixBlocklist` check (blocklist wins
regardless of entropy or allowlist), and `envSafeAllowlist` check (allowlist
overrides entropy check for undeclared keys).

Deliverables:
- `internal/workspace/envclassify.go`
- `internal/workspace/envclassify_test.go` (table-driven: entropy boundaries,
  all blocklist prefixes, all allowlist patterns, empty value)

### Phase 4: Pre-pass integration

Wire everything together in `EnvMaterializer.Materialize`:
- Add `Stderr io.Writer` field and `stderr()` helper to `EnvMaterializer`
- Add `EnvExampleVars`, `EnvExampleSources` to `MaterializeContext`
- Write the pre-pass (opt-out → parse → exclude → classify → store)
- Add nil-guarded opening block to `ResolveEnvVars`
- Update `apply.go` to pass `os.Stderr` (or the injected writer) to
  `EnvMaterializer.Stderr`

Deliverables:
- `internal/workspace/materialize.go` — pre-pass + nil-guard
- `internal/workspace/apply.go` — materializer construction
- Integration tests verifying end-to-end materialization with a sample
  `.env.example`

### Phase 5: Public-repo guardrail for `.env.example`

Add per-repo remote detection in the pre-pass using `enumerateGitHubRemotes`
against `ctx.RepoDir`. When a probable-secret key is found and the repo's remote
is public, fail with the guardrail error (consistent with PRD R13). Integrate
with the existing `--allow-plaintext-secrets` flag check.

Deliverables:
- Pre-pass guardrail call (within `env_example.go` or inline in pre-pass)
- Test: public-remote repo with undeclared high-entropy key → apply fails

### Phase 6: Source attribution in `niwa status --verbose`

Add `SourceKindEnvExample` to the `SourceKind` enumeration. Ensure
`EnvExampleSources` entries use this kind. Update the `--verbose` output
path to display `.env.example` as the source label.

Deliverables:
- `internal/workspace/materialize.go` — new `SourceKind` constant
- `internal/cli/status.go` — `--verbose` output update

## Security Considerations

### Input validation

`parseDotEnvExample` reads files from managed app repos, which may be
controlled by third parties. Three defenses apply before values reach the
classification or write paths:

- **Symlink check.** Before reading `.env.example`, niwa calls `os.Lstat` to
  detect whether the path is a symlink. If it is, niwa skips the file with a
  warning and treats the repo as having no `.env.example`. This prevents a
  crafted symlink from redirecting the read to sensitive system files.
- **Key name validation.** `parseDotEnvExample` rejects keys whose names
  contain characters outside `[A-Za-z0-9_]`. Invalid key names emit a per-line
  warning and are not included in the output map.
- **File size guard.** Files larger than 512 KB are skipped with a warning.
  Real-world `.env.example` files are under 2 KB; an oversized file is treated
  as a whole-file failure per R22.

### No value text in diagnostic output

All warning and error messages produced by the classification system, the
per-repo public-remote guardrail, and per-line parse errors MUST include only
the file path, line number, key name, and a reason description. No value text,
value fragment, or entropy score applied to a value may appear in any diagnostic
output. This extends the existing R22 requirement ("diagnostics never contain
secret bytes") to all new code paths in `envclassify.go` and the per-repo
guardrail. Tests MUST assert that captured stderr contains no substring of any
value that was classified as a probable secret.

### Secrets exclusion set completeness

The set of keys excluded from `.env.example` materialization is built from all
`[env.secrets.*]` sub-tables at every config layer: workspace-level, per-repo,
and instance overrides, including the `required`, `recommended`, and `optional`
sub-tables alongside the flat `Values` map. The exclusion walk mirrors the scope
of `offendingKeys` in `internal/guardrail/githubpublic.go`. A key present in
any sub-table — even with no resolved value yet — is excluded. This preserves
the `.required` contract: a missing required secret cannot be silently satisfied
by a stub value from `.env.example`.

### Trust boundary

`.env.example` files originate from managed app repos, which may have external
contributors not under the workspace maintainer's control. Workspace maintainers
who manage third-party repos or repos with untrusted contributors should set
`[repos.<n>] read_env_example = false` for those repos. The per-repo opt-out
disables `.env.example` discovery entirely for the named repo.

The default of `read_env_example = true` applies to all repos including
newly-discovered ones. Workspace maintainers adding a new repo to org-level
auto-discovery should review that repo's `.env.example` — or explicitly opt it
out — before the next `niwa apply`.

### Classification limitations

The entropy-based secret detector (3.5 bits/char threshold) has known
false-negative cases: structured secrets such as base32-encoded tokens or JWT
payloads may score below the threshold and pass as safe values. The
known-prefix blocklist catches common vendor token patterns. For any key the
workspace maintainer knows is sensitive, declaring it explicitly as
`[env.secrets]` bypasses probabilistic detection entirely and is the recommended
path.

## Consequences

### Positive

- Onboarding improves automatically: new teammates on a workspace with Node apps
  get all app-declared defaults without any workspace.toml changes.
- The `.required` contract is protected: secrets-declared keys are excluded at
  merge entry; a missing required secret remains visibly unresolved.
- Fully backward compatible: nil-guarded `MaterializeContext` fields mean every
  existing `ResolveEnvVars` call site continues to work without modification.
- Parser and classifier are independently testable: `env_example_test.go` and
  `envclassify_test.go` cover all boundary cases without pipeline setup.
- No new packages, no new public APIs, no signature changes to `ResolveEnvVars`.

### Negative

- `parseDotEnvExample` duplicates ~5 lines of basic `KEY=VALUE` logic from
  `parseEnvFile`. Two parsers exist in the package.
- `MaterializeContext` grows by two fields. Any test that constructs it directly
  doesn't need to set them (nil is correct), but the struct is larger.
- Apply output becomes more verbose on first run against workspaces with
  `.env.example` files: one warning per undeclared key per repo.
- The pre-pass adds one `os.Stat` + `os.ReadFile` per managed repo per apply.
  Within the 5 ms budget, but not zero.
- `.env.example` keys are not available for `claude.env.promote` in v1. If
  a promoted key only exists in `.env.example`, it won't appear in
  `settings.local.json`.

### Mitigations

- The 5-line duplication is intentional and documented (Considered Options,
  Decision 1). No extraction needed; if a third call site ever needs Node syntax,
  `parseDotEnvExample` can be called from it directly.
- Struct growth is negligible; nil-field convention is already established.
- Verbosity on first run is the intended behavior (additive-loud per PRD). Users
  with noisy workspaces can suppress per-repo via `read_env_example = false`.
- The I/O overhead is bounded by `.env.example` file sizes (typically < 2 KB);
  the 5 ms budget has significant headroom.
- The `claude.env.promote` gap is documented in the PRD as a Known Limitation
  and will be resolved if settings promotion of `.env.example` keys is requested.
