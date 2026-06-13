---
status: Planned
upstream: docs/prds/PRD-secret-output-targets.md
decision_provenance: inline-resolved
problem: |
  niwa expands a repo's resolved secrets into a single hardcoded file,
  .local.env, in a single hardcoded dotenv serialization, written by one
  materializer (internal/workspace/materialize.go) on both the instance
  apply and worktree apply paths. The destination is fixed in code while
  the right destination varies per repo by language and tooling.
decision: |
  Add a cascading `env_output` config field (repo -> workspace ->
  personal/global, mirroring env_example_policy) carrying a list of
  targets, each a path plus an optional format. Resolve targets and their
  formats (inferred from extension, override allowed) in internal/config;
  serialize via a new leaf package internal/envformat with a small writer
  per format (dotenv, json, shell); have EnvMaterializer write each
  resolved target; and extend gitexclude.EnsureRepoExclude with extra
  patterns so every custom target name is recorded as niwa-managed git
  ignore coverage, fail-closed.
rationale: |
  Reusing the env_example_policy cascade idiom and the #158 gitexclude
  machinery keeps the change small and consistent: the only genuinely new
  surface is the writer interface and the string-or-table target decoding.
  A leaf envformat package keeps serialization testable and admits a
  fourth format later without touching the materializer.
---

# DESIGN: Configurable secret-output targets

## Status

Planned

Authored under the /scope tactical chain (parent-orchestrated). Decision
questions were resolved inline rather than via parallel decision agents
(decision_provenance: inline-resolved), because the approach was
converged in an upstream brainstorming pass before the chain began;
Considered Options below records the alternatives weighed for each.

## Context and Problem Statement

`EnvMaterializer.Materialize` (internal/workspace/materialize.go:745-782)
resolves a repo's secret/env set via `ResolveEnvVars` and writes it to a
single path built as `filepath.Join(ctx.RepoDir, ".local.env")`, encoded
as dotenv `KEY=value` text. The materializer is the single shared writer
for both the instance apply pipeline (apply.go runRepoMaterializers loop)
and the worktree-content path (worktree_content.go), so both paths share
this behavior.

Two facts that previously justified the hardcoded shape no longer hold.
First, git invisibility used to ride on the `.local` name convention plus
a single `*.local*` line in the instance-root `.gitignore`; as of #158,
`internal/gitexclude.EnsureRepoExclude` records a niwa-managed block
(`*.local*`, `.niwa/`) in each repo clone's `.git/info/exclude`,
worktree-aware via `git rev-parse --git-common-dir`, idempotent, and
fail-closed. Invisibility no longer depends on the filename. Second, the
config layer already carries a per-repo cascade for env behavior
(`read_env_example`, `env_example_policy` on WorkspaceMeta, RepoOverride,
and GlobalOverride), so a per-repo output declaration has an established
idiom to follow.

The technical problem: make the secret-expansion destination (path and
serialization) a resolved, per-repo configuration value instead of a
compile-time constant, while preserving byte-identical default output and
the fail-closed invisibility guarantee, on both materialization paths.

## Decision Drivers

- **No default behavior change.** A repo that configures nothing must
  receive `.local.env` in dotenv form, byte-for-byte (PRD R1, R5).
- **Consistency with the existing cascade.** Precedence and config shape
  should mirror `env_example_policy` so operators and maintainers meet one
  idiom, not two (PRD R2).
- **Invisibility preserved, not weakened.** Custom names must be recorded
  as ignore coverage with the same fail-closed posture #158 established
  (PRD R7, R10).
- **One writer path.** Both apply paths already share `EnvMaterializer`;
  the change must not fork them (PRD R8).
- **Extensible serialization without over-building.** Three formats now,
  a seam for a fourth, no YAML dependency yet (PRD scope).
- **No secret material in diagnostics or in git.** (PRD R11, R7.)

## Considered Options

### Decision 1: Where the target declaration lives

- **Chosen: a top-level `env_output` field on WorkspaceMeta,
  RepoOverride, and GlobalOverride**, typed as a list of targets. This is
  exactly how `env_example_policy` is placed and resolved, so the cascade
  resolver, deep-copy, and TOML surface all follow a known pattern.
- *Alternative: nest `output` inside the existing `[env]` EnvConfig
  block.* Rejected: `EnvConfig` lives on `RepoOverride.Env` and the
  workspace `[env]` block, but `GlobalOverride` has no `Env` block, so the
  personal/global rung of the cascade would have nowhere to live. The
  top-level `env_*` placement is what `env_example_policy` already uses
  for this exact reason.

### Decision 2: Target encoding (string vs table)

- **Chosen: a custom `OutputTargets` type implementing
  `toml.Unmarshaler`** that accepts a bare string (one target), a list of
  strings (each inferred), or a list of tables (`{ path, format }`).
  BurntSushi/toml v1.6.0 (already the project's decoder) passes the raw
  decoded value to `UnmarshalTOML(interface{})`, so the type normalizes
  `string`, `[]interface{}`, and `map[string]interface{}` into
  `[]OutputTarget`.
- *Alternative: an array-of-tables `[[env_output]]` only.* Rejected as
  too heavy for the common case (a single filename), which the brief
  settled as the dominant path.
- *Constraint recorded:* a single array mixing bare strings and tables in
  the same list is not a supported form; an operator who needs an explicit
  format on any element writes that list as a list of tables. This keeps
  decoding unambiguous and avoids relying on heterogeneous-array decoding.

### Decision 3: Format selection

- **Chosen: infer from file extension, with an explicit per-target
  `format` override.** `OutputFormat` is a validated string enum
  (`dotenv`, `json`, `shell`) using `UnmarshalText` like the existing
  `Action` enum. The inference table: `.json` -> json; `.sh` -> shell;
  everything else (including `.env`, `.local.env`, `.env.local`, and any
  extensionless name) -> dotenv. An explicit `format` always wins.
- *Alternative: always-explicit format.* Rejected: forces a table form on
  every target to serve a rare ambiguous case.

### Decision 4: Serialization seam

- **Chosen: a new leaf package `internal/envformat`** exposing one writer
  per format behind a single-method interface, selected by a small
  registry/switch. The dotenv writer is the current materializer
  serialization extracted verbatim so the default stays byte-identical.
- *Alternative: inline switch inside `EnvMaterializer`.* Rejected: mixes
  serialization with file orchestration and is harder to unit-test per
  format; a leaf package (like `internal/gitexclude`) keeps both clean and
  importable without cycles.

### Decision 5: Recording ignore coverage for custom names

- **Chosen: extend `gitexclude.EnsureRepoExclude(tree, extraPatterns
  ...string)`** and have `renderNiwaBlock` union the static base
  (`*.local*`, `.niwa/`) with the deduplicated extra patterns. The
  materializer surfaces each repo's resolved target relative paths; both
  call sites pass them.
- *Alternative: force a `.local` infix onto custom names.* Rejected: it
  defeats the feature -- the operator asked for `.env`, not `.env.local`.

## Decision Outcome

The destination becomes a resolved value. Configuration adds an
`env_output` list to the three cascade structs. `internal/config` gains
`OutputTarget`/`OutputTargets`/`OutputFormat` types and an
`EffectiveEnvOutput` resolver that returns the repo's ordered list of
`(path, resolved-format)` pairs, defaulting to a single
`(.local.env, dotenv)` when no rung sets it. `internal/envformat`
serializes a repo's ordered `[]KV` into the chosen format.
`EnvMaterializer.Materialize` resolves the targets, writes each one
(creating parent directories as needed, mode 0o600), and records each
target's relative path so the apply and worktree paths can pass them to
`EnsureRepoExclude` as extra ignore patterns. The default path stays
byte-identical because the dotenv writer is the lifted current code and
the default resolution yields exactly today's single target.

## Solution Architecture

### Config layer (internal/config)

New types (new file `internal/config/env_output.go`):

```go
type OutputFormat string // "dotenv" | "json" | "shell"
func (f *OutputFormat) UnmarshalText(b []byte) error // validate enum

type OutputTarget struct {
    Path   string       `toml:"path"`
    Format OutputFormat `toml:"format,omitempty"` // "" => infer
}

type OutputTargets []OutputTarget
func (t *OutputTargets) UnmarshalTOML(v interface{}) error
// accepts: string | []interface{} (each string|map) | map
```

Field added to `WorkspaceMeta`, `RepoOverride`, `GlobalOverride`:

```go
EnvOutput OutputTargets `toml:"env_output,omitempty"`
```

Resolver:

```go
// Most-specific-wins at the list level (a set list replaces, it does not
// merge). Returns resolved (path, concrete-format) pairs.
func EffectiveEnvOutput(global OutputTargets, ws *WorkspaceConfig, repoName string) []ResolvedTarget
```

Resolution order: repo `env_output` (if non-empty) -> workspace
`env_output` (if non-empty) -> global `env_output` (if non-empty) ->
default `[{Path: ".local.env", Format: dotenv}]`. Format per target:
explicit `Format` if set, else `inferFormat(path)`.

```go
func inferFormat(path string) OutputFormat // .json->json, .sh->shell, else dotenv
```

Deep-copy and merge support must reach every site that handles
`EnvExamplePolicy` today, or the new field silently drops (the exact
#155 bug class). There are three:

- `internal/vault/resolve/deepcopy.go` -- two call sites (RepoOverride at
  ~line 97, GlobalOverride at ~line 133) plus a `deepCopyEnvOutput`
  helper alongside `deepCopyEnvExamplePolicy`.
- `internal/workspace/override.go` -- `copyEnvExamplePolicy` (~line 315)
  gains a `copyEnvOutput` analog.
- `internal/workspace/override.go` -- the per-field merge resolver
  (~lines 411-423) gains an `EnvOutput` rule. Because resolution is
  list-level most-specific-wins (a set list replaces, not merges), the
  merge rule is: if the more-specific rung's `EnvOutput` is non-empty, it
  wins outright; otherwise inherit. This is simpler than the per-category
  `EnvExamplePolicy` merge beside it.

### Serialization layer (internal/envformat)

New leaf package (stdlib only):

```go
package envformat
type KV struct{ Key, Value string }
func Marshal(format string, kvs []KV) ([]byte, error)
```

Three internal writers consume the same ordered `[]KV` so ordering is
deterministic and consistent across formats:

- **dotenv** -- the current `EnvMaterializer` serialization lifted
  unchanged (byte-identical default; PRD R5).
- **json** -- a flat object built in KV order via an ordered encoder
  (not `json.Marshal(map)`, to keep output deterministic); standard JSON
  string escaping.
- **shell** -- `export KEY='value'` with single-quote escaping
  (`'` -> `'\''`).

### Materializer change (internal/workspace/materialize.go)

`EnvMaterializer.Materialize` resolves `EffectiveEnvOutput`, then for each
resolved target, in this order (the order is security-load-bearing -- see
Security Considerations):

1. Validate the target path is safe (see the path-safety algorithm in
   Security Considerations); fail closed on violation.
2. Establish ignore coverage for the target's relative path via
   `gitexclude.EnsureRepoExclude(ctx.RepoDir, targetRel)` BEFORE writing.
   For a target name not already matched by the managed base pattern
   (`*.local*`), coverage must be positively confirmed: if
   `EnsureRepoExclude` cannot record coverage because the tree is not a
   git repository, the materializer fails closed and refuses to write that
   target (a custom-named secret with no confirmable git invisibility is
   an error, not a silent write). The default `.local.env` and other
   `*.local*`-matching names need no positive confirmation -- they are
   covered by the base pattern and carry the legacy posture.
3. `MkdirAll` the parent with mode 0o700 (a freshly created subdir holding
   a secret must not be world-readable).
4. `envformat.Marshal` the resolved vars and
   `os.WriteFile(target, data, secretFileMode)` (0o600).
5. `ctx.recordSources(target, sources)` and append the relative path to
   the returned `files` list and to a new `ctx.EnvOutputs []string`.

### Ignore coverage change (internal/gitexclude)

```go
func EnsureRepoExclude(tree string, extraPatterns ...string) error
func renderNiwaBlock(existing []byte, patterns []string) []byte
```

`patterns` = base (`*.local*`, `.niwa/`) unioned with deduplicated
`extraPatterns`, in stable order. Existing call sites
(`apply.go:1324`, `worktree/worktree.go:229`) pass no extra patterns and
behave exactly as today. Coverage for custom targets is established by the
materializer (step 2 above) before each write, so a custom secret file is
never on disk before its exclude line exists.

### Call-site wiring (both materialization paths)

The instance apply loop (apply.go ~1300-1324) already calls
`EnsureRepoExclude(repoDir)` per repo after `runRepoMaterializers`; it is
extended to pass `ctx.EnvOutputs` as a deduplicated backstop. The worktree
path is the asymmetric one: `internal/worktree/worktree.go:229` records
coverage at worktree *creation*, before `ApplyToWorktree`
(worktree_content.go) materializes content -- so that early call cannot
know the custom targets. The materializer's own coverage step (step 2)
already protects every custom write on both paths; additionally,
`ApplyToWorktree` gains a post-materialization
`EnsureRepoExclude(worktreePath, matEnvOutputs...)` backstop so the
worktree's persisted exclude block lists the targets explicitly, matching
the instance path's end state.

### Data flow

```
config TOML --(BurntSushi decode + OutputTargets.UnmarshalTOML)--> OutputTargets
        --(EffectiveEnvOutput + inferFormat)--> []ResolvedTarget
ResolveEnvVars --> ordered []KV
  for each ResolvedTarget:
     validate path safety (fail closed)
     EnsureRepoExclude(RepoDir, targetRel)  // coverage BEFORE write; fail closed for custom names
     MkdirAll(parent, 0o700)
     envformat.Marshal --> bytes --> WriteFile (target, 0o600)
     record relative path in ctx.EnvOutputs
apply loop:        EnsureRepoExclude(repoDir, ctx.EnvOutputs...)        // idempotent backstop
ApplyToWorktree:   EnsureRepoExclude(worktreePath, ctx.EnvOutputs...)  // idempotent backstop
```

## Implementation Approach

1. **Config types + resolver + copy/merge** (`internal/config`,
   `internal/vault/resolve`, `internal/workspace/override.go`): add
   `OutputFormat`, `OutputTarget`, `OutputTargets` (+ `UnmarshalTOML`,
   `UnmarshalText`), the `EnvOutput` field on the three structs,
   `inferFormat`, `EffectiveEnvOutput`, plus copy/merge coverage at all
   three sites enumerated in Solution Architecture (deepcopy.go x2,
   override.go `copyEnvOutput`, and the override merge rule). Unit-test the
   cascade (list-replace semantics), the inference table, string/list/table
   decoding, and a copy/merge round-trip that would have caught the #155
   drop bug.
2. **envformat package** (`internal/envformat`): the three writers and
   `Marshal`, with the dotenv writer lifted from the current materializer.
   Unit-test each format's escaping and a spaces/quote/newline round-trip.
3. **gitexclude extension**: add `extraPatterns` to `EnsureRepoExclude`
   and the `patterns` arg to `renderNiwaBlock`; keep existing call sites
   passing nothing. Unit-test union/dedup/idempotence.
4. **Materializer wiring**: resolve targets; per target apply the
   path-safety guard, establish coverage before write (fail-closed for
   custom names on non-git trees), `MkdirAll` 0o700, write via envformat
   0o600, record `ctx.EnvOutputs`. Extend the apply-loop
   `EnsureRepoExclude` call to pass `ctx.EnvOutputs` and add the
   post-materialization backstop call in `ApplyToWorktree`. Confirm the
   default path is byte-identical.
5. **Functional coverage**: a feature file exercising default-unchanged,
   custom single target, multi-target, each format, an explicit override,
   a `.env` git-invisibility check (`git status` clean), both apply paths,
   a fail-closed bad-format error, a rejected path-traversal target
   (absolute, `..`-escape, and symlinked-parent), and assertions that no
   secret bytes appear in any error output.
6. **Docs**: update the vault-integration / workspace-config-sources
   guides with the `env_output` field and the inference table.

## Security Considerations

The threat model is wider than "operator misconfigures their own
machine": a niwa workspace config (`workspace.toml`) is itself a file in a
repo, potentially authored by other members of an org. A target path is
therefore untrusted input, and the guards below are mandatory, not
advisory.

- **Path traversal (precise, symlink-aware).** A target path is
  operator/config-controlled and joined to `RepoDir`. String-cleaning
  alone is insufficient because `MkdirAll`/`WriteFile` follow symlinks: an
  in-tree-looking path like `subdir/.env` escapes the repo if `subdir` is
  a symlink. The materializer MUST apply this algorithm before any
  `MkdirAll`/`WriteFile`, failing closed (R10) on any failure:
  1. Reject an absolute target path outright.
  2. `clean := filepath.Clean(target)`; reject if `clean == ".."` or
     starts with `".." + sep` (escapes after cleaning, covers `a/../../x`).
  3. `joined := filepath.Join(RepoDir, clean)`; compute `rel` of `joined`
     against the symlink-resolved repo root and assert it stays within the
     root (`rel` does not start with `..`).
  4. For the deepest already-existing ancestor of `joined`, resolve
     symlinks (`filepath.EvalSymlinks`) and assert the resolved ancestor
     is still within the resolved `RepoDir`; reject a target written
     through a symlinked parent that points outside the repo.
- **Ignore coverage established before the write, fail-closed for custom
  names.** Custom names are not matched by `*.local*`, so the safety
  guarantee shifts from the name to the recorded exclude block. The
  ordering is load-bearing: the materializer records coverage
  (`EnsureRepoExclude`) BEFORE writing each target, so a custom-named
  secret never exists on disk ahead of its exclude line. `EnsureRepoExclude`
  already fails closed when the exclude file is unwritable
  (internal/gitexclude/exclude.go: errors from MkdirAll/ReadFile/WriteFile
  propagate). The one gap this design closes deliberately: `EnsureRepoExclude`
  no-ops on a non-git tree, which was safe when invisibility rode on the
  `.local` name but is NOT safe for an arbitrary custom name. So for a
  target name not matched by `*.local*`, a non-git tree (coverage cannot
  be positively confirmed) is a fail-closed refusal, not a silent write.
  `*.local*`-matching names (including the default `.local.env`) retain
  the legacy non-git no-op, which carries no exposure.
- **No secret material in diagnostics.** Errors name the repo, the target
  path, and the format only; values are never interpolated into error
  text or logs (R11). Format-writer errors must not echo the value being
  escaped.
- **File mode.** Every target is written 0o600 like the current
  `.local.env`, regardless of format or name.
- **No new untrusted execution.** No target content is executed by niwa;
  the shell format produces a file the operator chooses to `source`, which
  is their action, not niwa's.

## Consequences

### Positive

- Operators get secrets in the file and format their stack reads, with no
  manual rename or translation, configured once and inherited.
- The invisibility guarantee is preserved and now independent of the
  filename, extending #158 rather than working around it.
- Serialization is isolated and testable; a fourth format is a new writer,
  not a materializer change.

### Negative / trade-offs

- A new config surface (`env_output`) and a custom TOML decoder add
  parsing complexity and a documented "no mixed string+table array"
  constraint operators must learn.
- More files written per repo (multi-target) means more exclude entries
  and more write operations per apply; negligible in practice.
- The dotenv writer must be lifted verbatim and pinned by a
  byte-compatibility test, or the default silently drifts.

### Mitigations

- The byte-compatibility test (criterion in PRD R5) guards default drift.
- The path-traversal guard and fail-closed exclude coverage are unit- and
  functionally-tested; diagnostics are asserted secret-free.
- The decoder constraint is documented in the config-sources guide with
  examples of each accepted form.
