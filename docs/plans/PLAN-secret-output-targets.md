---
schema: plan/v1
status: Active
execution_mode: single-pr
upstream: docs/designs/DESIGN-secret-output-targets.md
milestone: "Configurable secret-output targets"
issue_count: 5
---

# PLAN: Configurable secret-output targets

## Status

Active

Single-pr plan authored under the /scope tactical chain. The five issue
outlines land together in one PR; /work-on consumes the outlines below.

## Scope Summary

Make niwa's secret-expansion destination (path and serialization)
per-repo configurable via a cascading `env_output` field, with dotenv /
json / shell writers and git-invisibility preserved for custom names,
across both the instance-apply and worktree-apply paths.

## Decomposition Strategy

**Horizontal.** The design describes loosely coupled components with
clear interfaces: a config layer (types + resolver + copy/merge), a leaf
serialization package, a leaf git-exclude extension, the materializer
wiring that composes them, and test/doc coverage. The three leaf layers
(config, envformat, gitexclude) have no runtime dependency on each other
and can be built in parallel; the materializer wiring composes all three;
tests and docs follow. There is no end-to-end integration risk that
favors a walking skeleton -- the integration point is a single
materializer method, built once in Issue 4.

## Issue Outlines

### Issue 1: feat(config): add cascading env_output target declaration

**Goal**: Add the `env_output` config field, its types, the
extension-to-format inference, the most-specific-wins resolver, and
copy/merge coverage so per-repo and global overrides survive snapshots.

**Acceptance Criteria**:
- [ ] `OutputFormat` (`dotenv`|`json`|`shell`) with `UnmarshalText`
  validating the enum, mirroring the existing `Action` type.
- [ ] `OutputTarget{Path, Format}` and `OutputTargets` with
  `UnmarshalTOML` accepting a bare string, a list of strings, or a list
  of tables (the "no mixed string+table array" constraint is documented).
- [ ] `EnvOutput OutputTargets` field added to `WorkspaceMeta`,
  `RepoOverride`, and `GlobalOverride` (`toml:"env_output,omitempty"`).
- [ ] `inferFormat(path)`: `.json`->json, `.sh`->shell, else dotenv.
- [ ] `EffectiveEnvOutput(global, ws, repoName)` resolves repo ->
  workspace -> global with list-level replace (not merge), defaulting to
  a single `{.local.env, dotenv}` target; each target's format is the
  explicit value or the inferred one.
- [ ] Copy/merge coverage added at all three sites:
  `internal/vault/resolve/deepcopy.go` (both RepoOverride and
  GlobalOverride call sites + a `deepCopyEnvOutput` helper),
  `internal/workspace/override.go` `copyEnvOutput`, and the override
  merge rule (list-replace).
- [ ] Unit tests: cascade precedence + inheritance + default; inference
  table; all three decode forms; a copy/merge round-trip that fails if
  the field is dropped.

**Dependencies**: None

**Type**: code
**Files**: `internal/config/env_output.go`, `internal/config/config.go`, `internal/vault/resolve/deepcopy.go`, `internal/workspace/override.go`

### Issue 2: feat(envformat): add dotenv, json, and shell writers

**Goal**: Add a leaf `internal/envformat` package that serializes an
ordered set of key-value pairs into dotenv, json, or shell, with the
dotenv writer byte-identical to the current `.local.env` output.

**Acceptance Criteria**:
- [ ] `KV{Key, Value}` and `Marshal(format string, kvs []KV) ([]byte, error)`.
- [ ] dotenv writer lifted from the current `EnvMaterializer`
  serialization, producing byte-identical output (header + ordering).
- [ ] json writer emits a flat object in KV order, deterministically (not
  `json.Marshal(map)`), with correct JSON string escaping.
- [ ] shell writer emits `export KEY='value'` with single-quote escaping
  (`'` -> `'\''`).
- [ ] Unit tests per format including a value with spaces, a quote, and a
  newline that round-trips out of each format; unknown format errors.

**Dependencies**: None

**Type**: code
**Files**: `internal/envformat/envformat.go`

### Issue 3: feat(gitexclude): accept extra ignore patterns

**Goal**: Extend `EnsureRepoExclude` and `renderNiwaBlock` to union extra
operator-target patterns into the niwa-managed exclude block, keeping
existing call sites unchanged.

**Acceptance Criteria**:
- [ ] `EnsureRepoExclude(tree string, extraPatterns ...string) error`.
- [ ] `renderNiwaBlock(existing []byte, patterns []string) []byte` unions
  the base (`*.local*`, `.niwa/`) with deduplicated extra patterns in
  stable order.
- [ ] Existing call sites (`apply.go:1324`, `worktree/worktree.go:229`)
  compile and behave identically with no extra patterns.
- [ ] Unit tests: union, dedup, stable order, idempotence
  (`render(render(x)) == render(x)`).

**Dependencies**: None

**Type**: code
**Files**: `internal/gitexclude/exclude.go`

### Issue 4: feat(workspace): write resolved targets with safe, covered output

**Goal**: Rewire `EnvMaterializer.Materialize` to write each resolved
target via envformat, with the path-safety guard, coverage-before-write,
and both call-site backstops, keeping the default path byte-identical.

**Acceptance Criteria**:
- [ ] Materialize resolves `EffectiveEnvOutput` and writes each target.
- [ ] Path-safety guard rejects absolute paths, `..`-escape after
  cleaning, and symlinked-parent escapes (EvalSymlinks containment
  check), failing closed before any `MkdirAll`/`WriteFile`.
- [ ] Coverage is recorded via `EnsureRepoExclude` BEFORE each write; a
  custom (non-`*.local*`) target on a non-git tree fails closed (refused,
  not silently written).
- [ ] Parent dirs created 0o700; target files written 0o600 via envformat.
- [ ] `ctx.EnvOutputs` records written relative paths; the apply loop and
  `ApplyToWorktree` pass them to `EnsureRepoExclude` as a backstop.
- [ ] A repo with no `env_output` produces a byte-identical `.local.env`
  (pinned by a regression test).
- [ ] No diagnostic contains a secret value or fragment.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

**Type**: code
**Files**: `internal/workspace/materialize.go`, `internal/workspace/apply.go`, `internal/workspace/worktree_content.go`

### Issue 5: test+docs: functional coverage and config guides

**Goal**: Add end-to-end functional scenarios and update the config
guides for `env_output` and the inference table.

**Acceptance Criteria**:
- [ ] Functional feature exercising: default unchanged, custom single
  target, multi-target, each of the three formats, explicit format
  override, `.env` git-invisibility (`git status` clean), both apply
  paths, fail-closed bad-format, rejected path-traversal target
  (absolute / `..` / symlinked-parent), and a stderr assertion that no
  secret bytes appear in diagnostics.
- [ ] A `@critical` scenario covers the default-unchanged path in the
  init -> create -> apply workflow.
- [ ] `docs/guides/vault-integration.md` and
  `docs/guides/workspace-config-sources.md` document `env_output`, the
  accepted config forms, and the extension-to-format inference table.

**Dependencies**: Blocked by <<ISSUE:4>>

**Type**: code
**Files**: `test/functional/features/secret-output-targets.feature`, `docs/guides/vault-integration.md`, `docs/guides/workspace-config-sources.md`

## Implementation Sequence

Single-pr mode: no GitHub issues or milestone are created, so the
dependency relationships are carried by the per-issue **Dependencies**
fields above rather than a separate graph. The order:

- **Parallelizable foundation:** Issues 1, 2, and 3 have no
  interdependency and can be built concurrently. Each is independently
  unit-tested.
- **Integration:** Issue 4 (blocked by 1, 2, 3) composes all three and
  carries the security-sensitive logic (path safety,
  coverage-before-write); it is the critical-path node.
- **Verification:** Issue 5 (blocked by 4) follows.
- **Critical path:** Issue 1 (or 2 / 3) -> Issue 4 -> Issue 5.
