# Pragmatic Review — Issue 3 (config schema + env.vars/env.secrets split + MaybeSecret)

**Target commit:** `bb1615e90532ecc0c3aec2e0e4ff79c75530a454` on `docs/vault-integration`
**Scope reviewed:** `internal/config/maybesecret.go`, `vault.go`, `env_tables.go`, `validate_vault_refs.go`, and their tests; modifications to `config.go`, `override.go`, `materialize*.go`, `workspace_context.go`, `scaffold.go`.

## Summary

Scope is clean. The diff adds exactly what the PLAN AC enumerates — `MaybeSecret`, `[vault.*]` schema, `env.vars`/`env.secrets` split with three-level ladder, `GlobalOverride.Vault`, `vault_scope`, `team_only`, and the R3 deny-list validator. No unrelated refactors, no premature resolver code, no speculative config knobs. Touches to `override.go`, `materialize*.go`, `scaffold.go`, `workspace_context.go` are the minimum plumbing for the `MaybeSecret`-ification of existing string slots and surface only through `.String()` — exactly what Issue 3 needs to keep tests green without anticipating Issue 4's resolver.

## Heuristic pass

### 1. Custom TOML unmarshaler on `EnvVarsTable` (env_tables.go)

**Necessary.** Standard BurntSushi/toml cannot route the three reserved sub-table names (`required`/`recommended`/`optional`) into separate `map[string]string` fields while routing every *other* top-level string key into `Values map[string]MaybeSecret`. That hybrid flat-plus-nested shape is the PRD R33/R34 schema. A plain struct would force TOML authors into a different, uglier layout. Keep.

### 2. Custom TOML unmarshaler on `VaultProviderConfig` (vault.go)

**Necessary.** The `Kind string` is known; the remaining backend-specific fields (`project_id`, `key_path`, etc.) must be captured without coupling the config layer to the set of compiled-in backends. Decoding rest-fields into `map[string]any` is the decoupling. Keep.

### 3. `KnownProviderNames()` helper

**Single-caller** — only `validate_vault_refs.go:100` uses it, plus one unit test. Inlining is possible but the name genuinely adds clarity (anonymous = empty key is a subtle convention worth naming). **Advisory** — not blocking, small, well-scoped helper.

### 4. `VaultRegistry.IsEmpty()` — dead

**No callers in production code, no tests.** The doc comment even hedges ("team_only alone does not count ... but callers that need to detect a wholly-empty block can still inspect the individual fields"), which is a tell that it was added without a concrete need. Speculative generality. **Advisory** — remove now or let Issue 4 add it when there's a caller. Inert; not blocking.

### 5. `MaybeSecret.MarshalText()` — not exercised in production

Settings/env flow through `.String()` before JSON serialization (see `buildSettingsDoc` → `cfg.ResolvedEnvVars[k]` as plain string). `MarshalText` is never invoked by current code paths. However:
- It's symmetric with `secret.Value.MarshalText` (PLAN Issue 1 AC).
- It's defense-in-depth against a future debug dump or config serializer.
- It's 3 lines + 2 tests.

**Advisory / keep.** Reasonable contract hardening given the whole point of the feature is redaction-by-default.

### 6. R3 deny-list consolidation

Four deny-list paths:
1. `[claude.content.*]` sources — `checkContentSourcesForVault`
2. `[env.files]` — `checkEnvFilesForVault`
3. `[vault.provider*]` config fields — `checkProviderConfigForVault` + `checkAnyMapForVault`
4. Identifier fields — inline `hasVaultPrefix` checks in `validateNoVaultRefs`

These are **reasonably consolidated**. The four paths differ structurally (sources are strings on typed structs; env.files is `[]string`; provider config is `map[string]any` needing recursion; identifiers are string fields on unrelated types). A single walker would need runtime type assertions for each; the current shape is clearer. Keep.

One minor nit: `checkAnyMapForVault` / `checkAnyForVault` recurse into `[]any` and `map[string]any` defensively, but `VaultProviderConfig.Config` values today are flat — nested shapes only appear if a backend declares them. Not over-engineered, since BurntSushi/toml can produce nested tables anywhere, but worth noting.

### 7. Same-file provider-name validation

`walkVaultRefsForUnknownProvider` is ~100 lines of repetitive map-walking. The two closures (`checkEnvMap`, `checkClaudeEnv`) reduce the repetition reasonably. Could perhaps be trimmed with reflection, but that would obscure more than it saves. Keep.

### 8. Test scope

25 new tests:
- 6 MaybeSecret (zero/plain/redact/TOML-decode/MarshalText ×2)
- 19 vault/parse (anon, named, multi-named, mixed-shape, missing-kind, team_only, vault_scope, env-vars/secrets split, 6-subtable ladder, claude.env, v0.6 compat, R3 deny-list ×4 content-variants + env.files + provider-config ×2 + workspace.name + sources.org + repos.url, undeclared-ref, anon-URI-vs-named, accept-anon, global-override + mixed-shapes, settings-accepts-vault, files-accepts-vault, KnownProviderNames ×3 subtests)

Each covers a distinct PLAN AC or R3 deny-list path. **No redundancy.** The `TestParseRejectsVaultURIInContent` uses table-driven cases (3 sub-cases) which is the right shape for a deny list. Scope-proportionate.

### 9. Scope creep check

- No unrelated refactors in `materialize.go` / `override.go` — the diffs are narrowly about swapping `string` for `MaybeSecret` and adding `copyEnvVarsTable` (the one new helper needed because `EnvVarsTable` now has four fields).
- `scaffold.go` additions are commented-out template entries for `[vault.*]` and `[env.secrets]` — documentation-by-example, directly in scope.
- `workspace_context.go` adds one loop over `Env.Secrets.Values` mirroring the existing `Env.Vars.Values` loop — minimal.

No scope creep detected.

### 10. Minor style nits (non-blocking)

- `env_tables.go:79-81` — `if len(tbl) == 0 { return map[string]string{}, nil }` is redundant; the subsequent loop handles empty maps and `make(map[string]string, 0)` is fine. 3 lines that could go.
- `vault.go:56-58` — "Reset to avoid surprising carry-over on reused pointers" — BurntSushi/toml doesn't reuse `UnmarshalTOML` targets across decodes of the same field in the same run, but the reset is cheap insurance. Keep.

## Findings

| # | Severity | Location | Finding |
|---|----------|----------|---------|
| 1 | Advisory | `internal/config/vault.go:135` | `VaultRegistry.IsEmpty()` has zero callers and no test. Speculative. Remove now or let Issue 4 re-add when needed. |
| 2 | Advisory | `internal/config/env_tables.go:79-81` | Redundant `len(tbl) == 0` early-return; drop 3 lines. |
| 3 | Advisory | `internal/config/maybesecret.go:79-81` | `MarshalText` unused in current paths; keep for defense-in-depth symmetry with `secret.Value.MarshalText`. |

**No blocking findings.** Scope is disciplined and matches the PLAN AC closely.

## Counts

- blocking_count: 0
- non_blocking_count: 3
