# Architect Review — Issue #3 (config schema + MaybeSecret)

Target: commit `bb1615e90532ecc0c3aec2e0e4ff79c75530a454` on `docs/vault-integration`.
Scope: structural fit, layering, interface contracts, dependency direction.

## Verdict

Structurally sound and ready to land. Layering is clean, package dependencies
flow downward, the `[vault]` shape fits naturally into the existing
`GlobalConfigOverride` structure, and the v0.6 backward-compatibility path
is demonstrated by `TestParseV06BackwardsCompat`.

Two non-blocking items worth noting for follow-on work.

## Dependency Direction (check 1 and 8)

- `internal/config` now imports `internal/secret` and `internal/vault` only
  (from `maybesecret.go`). No other packages are pulled in.
- `internal/secret` and `internal/vault` do NOT import `internal/config` or
  `internal/workspace`. Reverse grep confirmed.
- `internal/config` does NOT import `internal/workspace`, `cmd/`, or
  `internal/cli`. Reverse grep confirmed.

This is the intended flow: `secret` (leaf) ← `vault` ← `config` ← `workspace`.

## `GlobalOverride.Vault` Fit (check 6)

`GlobalOverride.Vault *VaultRegistry` reuses the same `VaultRegistry` struct
as `WorkspaceConfig.Vault`. `ParseGlobalConfigOverride` validates each
overlay independently with a file-label, and `TestParseGlobalOverrideVault`
demonstrates that anon-or-named shapes, Validate rejection of mixed shapes,
and the per-workspace `[workspaces.<ws>.vault.*]` path all behave the same
as workspace config. No parallel pattern introduced.

## `MaybeSecret` Sum Type (check 2)

Documented invariant: "Exactly one of Plain or Secret is populated at any
given time." This invariant is NOT mechanically enforced — both fields are
public and directly assignable by the resolver (Issue 4) and by tests.

`String()` prefers `Secret` when non-empty, so a `Plain` value beside a
`Secret` is hidden from most callers, but `IsSecret()` only reports
`!m.Secret.IsEmpty()` and ignores `Plain`. If the Issue 4 resolver populates
`Secret` without clearing `Plain`, a caller reading `.Plain` directly (e.g.,
a diagnostic path) could leak the pre-resolution URI — the URI itself is
not secret, so the worst case is a confusing log entry, not a disclosure.

The struct deliberately keeps fields public so `encoding.TextUnmarshaler`
(line 70) can decode TOML strings directly. A constructor-gated version
would complicate decoding. Given the resolver lands in the same PR,
contained risk.

**Non-blocking.** Suggest a one-line `m.Plain = ""` contract at the resolver
call site in Issue 4, or a helper like `MaybeSecret.SetResolved(v, t)` that
zeroes Plain.

## v0.6 Backward Compatibility (check 3)

`TestParseV06BackwardsCompat` (vault_test.go:330) parses a config with:
- no `[vault]` block — `cfg.Vault` is nil, no warnings mention "vault".
- flat `[env.vars]` string map with `LOG_LEVEL = "info"` — parses into
  `Values` with `Plain="info"`.

The custom `EnvVarsTable.UnmarshalTOML` routes non-reserved keys into
`Values` as `MaybeSecret{Plain: v}`. The pre-existing consumers
(`internal/workspace/materialize.go` line 442, `workspace_context.go` 227)
already call `.String()` on the values, which returns `Plain` unchanged.
No user action is required.

## `EnvVarsTable.UnmarshalTOML` Edge Cases (check 4)

Walked through:

- empty `[env.vars]` (no sub-keys): `raw` is empty map, loop no-ops,
  all four fields stay nil, `IsEmpty()` returns true.
- `[env.vars]` with only `[env.vars.required]` sub-table: only the
  "required" branch runs; `Values` stays nil.
- empty `[env.vars.required]` table: `coerceDescriptionMap` returns
  `map[string]string{}` (non-nil but empty). This is a minor quirk —
  downstream code testing `len() > 0` still behaves correctly.
- reserved-name collision (user writes `required = "some-value"` as a
  top-level string): `reservedEnvVarsSubtables[k]` fires, then
  `coerceDescriptionMap` rejects with
  `env vars/secrets.required must be a TOML table, got string`.
  The error message is clear; the user gets a hint to rename.
- inline-table form (`vars = { KEY = "val" }`): exercised by
  `config_test.go:82`. Works — BurntSushi/toml decodes inline tables as
  `map[string]any`.

No structural issues in the unmarshaler.

## `validate_vault_refs.go` Home (check 5)

The post-parse walk aggregates all R3 deny-list rules (identifier fields,
`[claude.content.*]`, `[env.files]`, `[vault.provider*]`, identifier
collisions) plus same-file provider-name resolution in one place. Rejects
a parallel pattern where each struct grows its own `Validate` method and
each `Validate` duplicates the prefix-check logic.

This is the right home. The alternative — scattering a `hasVaultPrefix`
check into every relevant struct — would make it easy to forget a slot
when future schema additions arrive.

Two small refinements, neither blocking:

1. `extractProviderName` duplicates scheme-parsing logic that
   `vault.ParseRef` already implements. The file's comment (line 10-11)
   claims an import-cycle rationale, but `config` already imports `vault`
   (see `maybesecret.go:5`); there is no cycle. A future change could
   replace the manual parse with `vault.ParseRef` to eliminate the
   `vaultURIPrefix` duplication. Not doing it now keeps the diff tight.

2. `walkVaultRefsForUnknownProvider` walks the config structure
   explicitly per location. If more `MaybeSecret` slots are added later,
   each one must be added here. A reflection-based walk would eliminate
   the drift risk but would complicate error messages. The explicit
   walk is the right trade for now; just note it as a maintenance point.

**Not blocking.**

## Ripple into `internal/workspace/` (check 7)

Changes in `workspace/override.go`, `materialize.go`, `workspace_context.go`
are minimal and call `MaybeSecret.String()` or read `.Plain` — no deep
coupling to the secret internals. The code comments explicitly flag
"The resolver (Issue 4) has not yet processed MaybeSecret values"
(materialize.go:436) — good handoff.

## Observation (not a finding)

`internal/workspace/override.go` — both `MergeOverrides` (per-repo),
`MergeInstanceOverrides` (instance), and the global-overlay merge paths
check `override.Env.Secrets.IsEmpty()` as a gate (line 148) but do NOT
merge `override.Env.Secrets.Values` into the result. Only
`override.Env.Vars.Values` are merged. The workspace-level
`cfg.Env.Secrets.Values` are copied into result via `copyEnv`, so
workspace-declared secrets work; repo- / instance- / global-override
secrets are silently dropped.

This is not strictly Issue 3's concern — Issue 3 scopes to "adding the
schema" — but the merge gap is live: a user can declare
`[repos.foo.env.secrets]` today and see their values disappear without
warning. If Issue 4's "resolver auto-wraps plaintext values in
`*.secrets` tables into `secret.Value`" acceptance test uses a
workspace-level secret, this gap will survive unnoticed.

Flagging here so Issue 4 or 6 picks it up. **Not blocking for Issue 3.**

## Summary

- blocking_count: 0
- non_blocking_count: 2

### Non-blocking items

1. `MaybeSecret` sum-type discipline is documented, not mechanically
   enforced. When Issue 4 populates `Secret`, it should also clear
   `Plain` (or use a helper). Risk is contained (URI != secret), but
   the invariant is load-bearing for future diagnostic code.

2. `extractProviderName` in `validate_vault_refs.go` duplicates
   scheme-parsing from `vault.ParseRef`; the stated "import cycle"
   rationale is stale (config already imports vault). A future cleanup
   can route through `vault.ParseRef` to remove the duplicated
   `vaultURIPrefix` constant.

### Out-of-scope observation

`internal/workspace/override.go` never merges `override.Env.Secrets`
into the result (only `Env.Vars`). Not Issue 3's fault — the schema
change surfaced a dormant merge gap — but worth flagging so Issue 4/6
picks it up.
