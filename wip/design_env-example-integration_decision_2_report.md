<!-- decision:start id="env-example-integration-point" status="assumed" -->
### Decision: Integration point, opt-out checking, and stderr routing for `.env.example`

**Context**

`ResolveEnvVars` (`internal/workspace/materialize.go:538`) is the merge function that
builds the `map[string]string` written to each repo's `.local.env`. It currently applies
four layers in last-write-wins order: `[env.files]`, `[env.vars]`, `[env.secrets]`, and
the per-repo discovered env file. Adding `.env.example` as the fifth, lowest-priority
layer requires more than inserting code at the top of that function.

The blocking constraint comes from the call graph. `ResolveEnvVars` is called from two
places. `EnvMaterializer.Materialize` uses it to write `.local.env`. `resolveClaudeEnvVars`
(called by `SettingsMaterializer.Materialize`) uses it to resolve the promoted-key set for
`settings.local.json`. The materializer loop runs them in declaration order: hooks →
settings → env → files. Any `.env.example` processing placed inside `ResolveEnvVars`
itself would fire per-key warnings from both callers — once during the settings pass and
once during the env pass — with no clean deduplication path short of call-path sentinels
or shared mutable state.

A separate concern is the public-repo per-repo guardrail. The existing
`guardrail.CheckGitHubPublicRemoteSecrets` in `apply.go:629` checks the config dir's
remotes. `.env.example` probable-secret values must be guarded against the managed app
repo's remotes — a distinct call site, once per repo, not once per pipeline run.

**Assumptions**

- `.env.example` keys are not required to be promotable to `settings.local.json` in v1.
  If `claude.env.promote` lists a key that only exists in `.env.example`, the key is not
  promoted. This is consistent with the PRD, which scopes `.env.example` contribution to
  `.local.env` only. If the requirement changes in a future version, the pre-computed
  field can be populated earlier in the pipeline (e.g., before the materializer loop in
  `apply.go`) without changing this decision's structure.
- The `read_env_example` opt-out flags do not yet exist in the config schema. Schema
  additions (`WorkspaceMeta` or a new `ConfigMeta` struct for workspace-level; `RepoOverride`
  for per-repo) are a prerequisite regardless of which option is chosen. This decision
  assumes those schema fields exist and are accessible via `ctx.Effective` at the time
  `EnvMaterializer.Materialize` runs.
- The per-repo public-remote guardrail for `.env.example` probable secrets will reuse the
  existing `enumerateGitHubRemotes` helper from `internal/guardrail/` against `ctx.RepoDir`,
  not the existing `CheckGitHubPublicRemoteSecrets` entry point (which takes `configDir`).
  A new package-level helper or a targeted inline call is needed; the exact shape is a
  D3-adjacent implementation concern, not this decision.

**Chosen: Option B — Pre-pass in `EnvMaterializer.Materialize`; pre-computed result stored on `MaterializeContext`; `Stderr io.Writer` on `EnvMaterializer`**

`EnvMaterializer.Materialize` gains a pre-pass before calling `ResolveEnvVars`:

1. Check workspace-level opt-out (`ctx.Effective.Config.ReadEnvExample` or equivalent
   field); if false, skip the pre-pass entirely.
2. Check per-repo opt-out from the merged effective config; if false for this repo, skip.
3. Discover and parse `.env.example` at `filepath.Join(ctx.RepoDir, ".env.example")`
   using the new `parseDotEnvExample` function (Decision 1). Per-line warnings are emitted
   to `f.stderr()`, one line each, naming the file, line number, and problem. Whole-file
   failures emit a single warning and short-circuit with no contribution.
4. Run the exclusion set check: build the set of keys declared in any `[env.secrets.*]`
   table from `ctx.Effective.Env.Secrets.Values` and `ctx.Effective.Claude.Env.Secrets.Values`;
   remove those keys from the parsed map before storing. No warning is emitted for excluded
   keys (they're intentionally withheld; the user declared them as secrets).
5. Run per-key classification on the remaining undeclared keys (keys not in
   `ctx.Effective.Env.Vars.Values`). Keys classified as probable secrets emit a warning
   and are excluded from the stored result. Keys classified as safe emit a "new from
   .env.example" notice and are included.
6. Run the per-repo public-remote guardrail against probable-secret keys found in
   `.env.example` (against `ctx.RepoDir`). This check fires once per repo, in this method.
7. Store the classified, filtered result in a new `EnvExampleVars` field on
   `MaterializeContext` (type `map[string]string`) alongside a matching `EnvExampleSources`
   field (type `[]SourceEntry`).

`ResolveEnvVars` gains one new block at its very beginning: if `ctx.EnvExampleVars` is
non-nil, seed `vars` from it (with matching `SourceEntry` attribution). This block is a
no-op when the field is nil — which is the case when `resolveClaudeEnvVars` invokes
`ResolveEnvVars` during the settings pass, since `EnvMaterializer` has not run yet.

`EnvMaterializer` struct gains a `Stderr io.Writer` field, matching the existing
`FilesMaterializer.Stderr` pattern exactly. A `func (e *EnvMaterializer) stderr() io.Writer`
helper defaults to `os.Stderr` when the field is nil. Tests inject a `bytes.Buffer`.

`MaterializeContext` gains two fields: `EnvExampleVars map[string]string` and
`EnvExampleSources []SourceEntry`.

No changes to `ResolveEnvVars`'s external signature. No changes to `SettingsMaterializer`.
No new materializer types.

**Rationale**

The double-warning problem disqualifies Option A. Placing `.env.example` processing inside
`ResolveEnvVars` means the per-key classification and warning loop runs once per call of
that function. Because `resolveClaudeEnvVars` (settings path) and `EnvMaterializer.Materialize`
(env path) both call `ResolveEnvVars` for every repo, every key-level warning fires twice
per repo. The only fixes — a call-path sentinel flag on `MaterializeContext`, a deduplicated
warning set, or splitting `ResolveEnvVars` — are all more invasive than Option B. Option A
also places the opt-out check and the per-repo guardrail inside a function already called
from two distinct call sites, compounding the double-execution problem for the guardrail as well.

Option C (new `EnvExampleMaterializer`) has the same correct mechanics as Option B but
wraps them in a new pipeline step with a positional ordering dependency. The step must run
before both `SettingsMaterializer` and `EnvMaterializer` for `.env.example` keys to be
available during settings promotion. That ordering is not enforced by the type system — it
is a convention that future contributors can violate. In contrast, Option B's pre-pass is
structurally bound to `EnvMaterializer.Materialize`: it runs immediately before `ResolveEnvVars`
is called, so the data is always present at the right moment for the env path and absent
(nil) for the settings path. No ordering convention is needed.

The `FilesMaterializer` precedent for `Stderr io.Writer` on the materializer struct is
directly applicable. Adding the same field to `EnvMaterializer` is idiomatic and testable.
Adding it to `MaterializeContext` instead (as Option A would require) makes stderr a
pipeline-wide concern when it is specific to env-example processing.

The two new fields on `MaterializeContext` (`EnvExampleVars`, `EnvExampleSources`) are
the minimal interface between the pre-pass and `ResolveEnvVars`. They carry the same types
already used throughout the pipeline (`map[string]string`, `[]SourceEntry`), so no new
types are introduced. Nil-field semantics are already the convention on `MaterializeContext`
(see `DiscoveredEnv`, `SourceTuples`).

**Alternatives Considered**

- **Option A — First step inside `ResolveEnvVars`:** Seeding `vars` at the top of the
  function guarantees lowest-priority semantics without conditional logic, and
  `ctx.Effective` is fully available. Rejected because `ResolveEnvVars` is called from
  both `EnvMaterializer` and `resolveClaudeEnvVars` (settings path). Per-key warnings and
  the per-repo public-remote guardrail would fire twice per repo. Fixing this requires
  call-path sentinels, shared warning-deduplication state, or splitting the function — all
  more invasive than Option B. Threading `Stderr` through `MaterializeContext` for Option A
  would also expose a warning concern to all materializers for logic that only the env path
  needs.

- **Option C — New `EnvExampleMaterializer` pipeline step:** Correct mechanics, clean
  separation, and no changes to `ResolveEnvVars`. Rejected because it introduces a positional
  ordering dependency in the materializer slice that the type system cannot enforce. The step
  must run before `SettingsMaterializer` (for promotion parity) and `EnvMaterializer` (to
  pre-populate the context field). Today's order (hooks → settings → env → files) would
  require either inserting the new step at position 0 or reordering the existing steps.
  That ordering is a silent invariant. Option B achieves the same result without a new type
  by running the pre-pass inside `EnvMaterializer.Materialize`, which is already the correct
  phase boundary for `.local.env` materialization. The logic lives where it is used.

**Consequences**

- `EnvMaterializer` gains a `Stderr io.Writer` field and a `stderr()` helper, matching
  `FilesMaterializer` exactly. Tests inject a buffer; production leaves it nil (defaults to
  `os.Stderr`).
- `MaterializeContext` gains `EnvExampleVars map[string]string` and
  `EnvExampleSources []SourceEntry`. Both are nil by default. Tests that call `ResolveEnvVars`
  directly (without a pre-pass) continue to pass without modification — nil fields mean no
  `.env.example` contribution.
- `ResolveEnvVars` gains a nil-guarded read of `ctx.EnvExampleVars` at its opening. When
  non-nil, those entries seed `vars` first. When nil (settings path), the function behaves
  exactly as before. No signature change.
- The opt-out check fires before any disk I/O in `EnvMaterializer.Materialize`. A repo with
  `read_env_example = false` never reads `.env.example` from disk.
- The public-repo guardrail for `.env.example` probable secrets runs once per repo in
  `EnvMaterializer.Materialize`, against `ctx.RepoDir`. It does not replace the existing
  pipeline-level guardrail in `apply.go:629` (which guards the config repo's secrets tables).
- `.env.example` keys are not available for `claude.env.promote` in v1. This is by design:
  the promoted-key resolve runs during the settings pass before `EnvMaterializer` populates
  `EnvExampleVars`. If promotion of `.env.example` keys is needed in the future, the
  pre-pass can be extracted to a shared helper called earlier in the pipeline without
  changing the field-based interface.
<!-- decision:end -->
