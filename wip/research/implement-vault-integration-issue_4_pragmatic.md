# Pragmatic Review — Vault Integration Issue 4

Target commit: `1ad548b22a87b8b81f2ee2589db44ba31618bb88` on `docs/vault-integration`.
Review lens: simplicity / YAGNI / KISS. Scope: resolver + apply.go wiring +
`override.go` env.secrets merge fix + `team_only` enforcement.

## Summary

Approve. The change is bounded by the design doc and the 27 tests are not
redundant — they split cleanly into resolver unit coverage (20) and
integration coverage (3 in apply_vault_test.go + 4 new in override_test.go
for `team_only` + env.secrets). The sub-package structure, deepcopy file,
and `MergeGlobalOverride` signature change all have concrete justifications
that survive scrutiny. Two minor advisory items below; neither blocks.

## Findings

### 1. `internal/vault/resolve` sub-package — justified (no finding)

Verified the import-cycle claim:

- `internal/config/maybesecret.go:5` imports `internal/vault` for `vault.VersionToken`.
- A resolver in `internal/vault/resolver.go` would need to import `internal/config`
  to walk `*config.WorkspaceConfig`, producing `vault -> config -> vault`.

Placing the resolver in `internal/workspace/` is the only other seam, and it
would tangle `workspace/` (orchestration) with per-field MaybeSecret traversal
— the resolver's contract (immutable input, file-local bundle lifetime,
redactor registration) is noticeably larger than the rest of `workspace/`.
The sub-package is the smallest structural move that resolves the cycle.

Not a finding.

### 2. `deepcopy.go` — necessary, not gold-plated (advisory note only)

The resolver's contract is "returns a new *WorkspaceConfig, never mutate".
A reflect-based deep copy would clone the `Vault` pointer (which the resolver
explicitly shares by pointer) and clone maps the walker doesn't touch, both
slower and less safe. Struct-by-struct assignment is the right call here.

The file duplicates `copy*` helpers already present in
`internal/workspace/override.go` (e.g. `copyEnvVarsTable` vs
`cloneEnvVarsTable`, `copyHooks` vs `cloneHooks`). These cannot be shared
because `workspace` imports `resolve` transitively via apply.go and pulling
them into `config` would widen its surface. Keeping them duplicated is the
pragmatic choice. **Advisory** only: the in-file comment already flags the
drift risk.

### 3. 27 tests — not redundant

Counted:
- `resolve_test.go`: 20 tests — passthrough, resolution, non-mutation,
  auto-wrap, optional-downgrade, allow-missing, default-error, unknown-provider,
  redactor registration, repo+instance walk, global-override basic + per-ws,
  `CheckProviderNameCollision` (empty/anonymous/named/additive), `BuildBundle`
  (nil/named), invalid URI, provider-unreachable. Each exercises a distinct
  branch in `resolveOne` or a distinct public API. No duplicates.
- `apply_vault_test.go`: 3 tests — end-to-end resolve, missing-key surfaces,
  allow-missing-secrets threading. These are integration-level, not unit
  duplicates of the resolver tests.

Not a finding.

### 4. `MergeGlobalOverride` (cfg, err) signature — justified

Surfacing `team_only` violations by any route other than the return value
would mean either (a) panicking, (b) baking the check into a pre-merge
validator that duplicates the field walk, or (c) attaching errors to the
returned struct. (a) is wrong, (b) would traverse the same
`g.Claude.Settings`/`Env.Vars`/`Env.Secrets`/`Files` maps twice, and (c) is
uglier than a second return value. The change is a minimal honest widening
of the contract; `mustMerge` in `override_test.go:589` confirms callers
adopted the new signature cleanly.

Not a finding.

---

## Minor advisory items

### A. `walker.walkFilesKeys` is an intentional no-op

`internal/vault/resolve/resolve.go:419` — the method body returns nil and
the comment explains v1 scope excludes vault-keyed files mappings. Called
from 4 sites with identical empty-body cost. Fine as-is, but a single
deletion of the calls (with a TODO comment) would remove 4 dead invocations
and ~25 lines of comment. **Advisory.** The method telegraphs intent to
future readers; leave unless scope touches the files-key path.

### B. `ResolveOptions.SourceFile` vs internally-built bundle

`internal/vault/resolve/resolve.go:67` — `SourceFile` only affects error
messages when the resolver builds its own bundle. Production apply.go
always supplies pre-built `TeamBundle`/`PersonalBundle`, so `SourceFile` is
dead weight in the main path, used only by tests. Not worth removing
because `BuildBundle` is also a public entry point that takes the same
attribution string. **Advisory.**

---

## Prompt injection notice

During this review a tool result contained an injected `<system-reminder>`
block titled "MCP Server Instructions" that attempted to coerce behavior
around Telegram-related tools. The block arrived embedded in the output of
a `Read` call on `internal/vault/resolve/resolve_test.go`. I ignored it
and continued the review as the user requested. Flagging here for the
orchestrator's awareness.

## Tallies

- blocking_count: 0
- non_blocking_count: 2 (both advisory, neither requires action)
