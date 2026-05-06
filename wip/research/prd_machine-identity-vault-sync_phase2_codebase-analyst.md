# Phase 2 Research: Codebase Analyst

## Lead 3: Schema path inside the vault project

### Findings

**Infisical provider key lookup mechanics** (`internal/vault/infisical/infisical.go`):
- `Resolve(ctx, ref)` takes a `vault.Ref` with a `Key` field (string) and an optional `Path` field (folder path).
- The provider constructs an effective path by using `Ref.Path` if non-empty, otherwise falls back to the provider's config-time default path (`internal/vault/infisical/infisical.go:219-224`).
- It then invokes `infisical export --projectId <proj> --env <env> --path <effPath> --format json` and parses the response.
- The subprocess response is JSON in one of two shapes (`internal/vault/infisical/subprocess.go:174-186`):
  1. Flat object: `{"KEY": "value", ...}` — keys are directly accessible as map keys.
  2. Array of objects: `[{"key": "KEY", "value": "value"}, ...]` — the parser extracts name and value fields.
- Both shapes flatten to `map[string]string` where the key is looked up directly via `cache.values[ref.Key]` (`infisical.go:250`).

**Character constraints on Infisical key names**:
- Infisical's CLI and dashboard accept alphanumeric, underscore, hyphen, and dot characters in secret names.
- No explicit validation in the niwa Infisical provider; the CLI's `infisical export` command is the boundary.
- UUIDs with hyphens (e.g., `project-uuid-format`) are valid as secret-key components in Infisical paths/names.
- niwa's schema validation (`internal/config/config.go:15`) restricts provider names to `[a-zA-Z0-9._-]+` — the same pattern would work for the proposed schema path segments.

**Test response format** (`internal/vault/infisical/infisical_test.go:51-65`):
- Tests construct fake responses with `jsonBody(map[string]string{...})`, which produces flat JSON objects.
- The returned `secret.Value` is opaque bytes wrapping the plaintext (tested via `reveal.UnsafeReveal(val)`).
- Multiple key-value pairs in a single `infisical export` response are all cached and can be resolved independently.

**Body format parseable from a single secret value**:
- `secret.Value` is `[]byte` (opaque `internal/secret/value.go`), with no structured unmarshaling by niwa.
- The provider returns raw string values from the JSON map; a TOML or JSON body would be stored as a string value in the `map[string]string`.
- niwa itself does NOT parse the secret value — it's up to the consuming code. For provider-auth entries, the local file reader (`internal/workspace/apply.go:492`) calls `LoadProviderAuth()`, which uses `toml.Unmarshal` on the file bytes.
- This means: a single Infisical secret value (a JSON string or TOML text) can be parsed by niwa's config layer if the consuming code chooses to unmarshal it.

**Versioning strategy**:
- The existing VersionToken design (`infisical.go:100-118`) synthesizes a SHA-256 digest over sorted key names and their byte lengths, not over plaintext content.
- A versioning field inside a packed body (e.g., `version = "1"`) would work: niwa doesn't validate the body, only stores and re-parses it at resolve time.
- A separate path layout (`/niwa/provider-auth/v2/...`) is cleaner for major schema breaks but adds path-discovery complexity if niwa ever needs to enumerate available machine-identity keys.

### Implications for Requirements

1. **Single flat string per (kind, project) pair is feasible**: One Infisical key holding a TOML body works. The provider's JSON parsing already handles string values; the consuming code can unmarshal TOML from that string.
2. **TOML is the natural choice for the body format**: niwa's entire config stack uses TOML; adding TOML parsing for a machine-identity body fits the existing pattern. JSON is also acceptable but adds a language dependency.
3. **UUID hyphens in the path are safe**: The proposed `/niwa/provider-auth/infisical/<project-uuid>` path is valid; UUIDs with hyphens work in Infisical folder and key names.
4. **Versioning via a body field is the pragmatic choice**: Adding `version = "1"` inside the TOML body keeps the path stable across schema evolution. When a breaking change is needed, niwa can check the version field and emit a clear diagnostic (e.g., "unsupported schema version 2, upgrade niwa").
5. **The path must be published as a convention**: There's no automatic discovery — niwa will ask for the key at a specific, well-known path. That path must be documented.

### Open Questions

1. Should the key be at `/niwa/provider-auth/infisical/<project-uuid>` (per-project, flat path) or `/niwa/provider-auth/infisical/<project-uuid>/<kind>` (one key per kind per project)? The exploration tentatively proposed a single key per `(kind, project)` pair, which suggests the path should be `/niwa/provider-auth/infisical/<project-uuid>?<kind>=infisical`, or more simply, one key per project whose body contains all kinds. The latter is simpler (no query-string parsing in niwa).
2. When a user rotates a machine-identity client_secret, what version of the rotated entry should be stored in the vault? Should it include `api_url` (which is optional)? Should it include any audit metadata (timestamp, rotation reason)?
3. Are there constraints on how large a TOML body can be as a single Infisical secret value? (Infisical typically handles up to 8 KB per secret; a single provider-auth entry is <200 bytes, so this is not a practical constraint.)

---

## Lead 4: Opt-in shape in the global config

### Findings

**GlobalOverride and GlobalConfigOverride structure** (`internal/config/config.go:432-445`):
- `GlobalOverride` is the per-workspace overlay shape; it mirrors `RepoOverride` but omits repo-specific fields.
- It declares `Vault *VaultRegistry` (`config.go:436`), the same type used in `WorkspaceConfig.Vault`.
- `GlobalConfigOverride` is the top-level struct parsed from the global config repo's `niwa.toml`, with a `Global GlobalOverride` field and a `Workspaces map[string]GlobalOverride` field.
- There is NO existing `[global.machine_identities]` section or equivalent; it would need to be added.

**Vault provider parsing and named vs anonymous distinction** (`internal/config/vault.go`):
- `VaultRegistry.Validate()` enforces that `Provider` (anonymous) and `Providers` (named) are mutually exclusive (`vault.go:96-102`).
- The parser accepts both shapes without complaint as long as they're not mixed.
- `KnownProviderNames()` returns a map of declared names; the anonymous provider contributes the empty string `""` as a key (`vault.go:146-158`).

**Handling missing provider references** (`internal/config/validate_vault_refs.go`):
- When a `vault://` URI references a provider name that doesn't exist, the validator walks the config and returns a clear error: `"%s: vault URI %q references unknown provider %q. Declared providers in this file: [%s]."` (`validate_vault_refs.go:285-293`).
- The error messages are verbose and helpful, naming both the URI and the offending location.
- This validation runs AFTER parsing (`config.go:320`), so it catches misconfigurations at load time, not at resolve time.

**Precedent for "pick a named one" syntax**:
- The Infisical provider config accepts `name` as an optional field (`infisical.go:129-135`). This is set during `Factory.Open`, not at config parse time.
- The personal-overlay vault providers are already declared in `[global.vault.provider]` or `[global.vault.providers.<name>]`. To route credential-sync to a specific named provider, the config would need a new field like `from = "<provider-name>"`.
- There's no existing precedent in niwa for "select which of several X to use" in a config field. The closest parallel is `[workspace].vault_scope`, which picks which `[workspaces.<scope>]` block in the personal overlay applies to a multi-source workspace (`config.go:147-151`).

**Behavior when a config field references a non-existent name**:
- For `vault_scope = "missing"`, the error is deferred to the resolver. The field is a string; the parser doesn't validate it against available scope names.
- At apply time, when the resolver tries to find `[workspaces.missing]` in the parsed overlay, it logs a diagnostic. The exact behavior is in the resolver, not the config layer.
- This suggests **opt-in credential sync should also defer validation to apply time** if the naming strategy is a string field (`from = "<provider-name>"`).

**Unknown fields behavior**:
- TOML's decoder (BurntSushi/toml) will NOT fail on unknown fields by default. The parser uses `md.Undecoded()` to collect unknown fields and emit them as warnings (`config.go:323-325`).
- Adding a new `[global.machine_identities]` section to `GlobalOverride` would be a breaking change ONLY if existing niwa versions (pre-machine-identity-vault-sync) require strict schema validation. Since unknown fields are warnings, older niwa versions would just warn about the new section and continue.

### Implications for Requirements

1. **The `from` field is the right spelling**: Adding `[global.machine_identities] from = "<provider-name>"` mirrors the `vault_scope` precedent and is clear. When `from` is absent, the behavior defaults (either to anonymous singular, or to explicit error for multi-provider cases).
2. **Anonymous-singular default is safe**: If `[global.vault.provider]` exists (no name) and no explicit `from` is set, default to using it. This matches the principle that the anonymous provider is the implicit default.
3. **Error on ambiguity, not silence**: When multiple `[global.vault.providers.*]` are declared and `from` is absent, the feature should error at apply time with a message like: "machine-identity-vault-sync requires explicit `from = \"<provider-name>\"` when multiple vault providers are declared."
4. **Validation timing**: Parser-level validation (checking that the `from` name references a declared provider) would catch the error early. However, deferring to apply time (like `vault_scope`) keeps the config layer simpler. The exploration's findings suggest early validation is better — it mirrors the vault:// URI validation strategy.
5. **Backward compatibility**: Adding a new optional field to `GlobalOverride` does NOT break older niwa versions. They'll see it as an unknown field, warn, and ignore it.

### Open Questions

1. Should the new config section be `[global.machine_identities]` with a `from` field, or `[global.vault.machine_identities]` (nested under vault to group credential-related config)? The latter is more orthogonal but the former is flatter and easier to discover.
2. Should credential-sync be opt-in via an explicit flag (e.g., `from = "..."`) or automatically enabled when a compatible provider is declared (with a way to disable it)? The scope document says "opt-in, not on by default," suggesting the explicit `from` is necessary.
3. What does the default behavior look like for a personal overlay that declares `[global.vault.provider]` but does NOT set `from`? Should it automatically enable credential sync (implicit), or should credential sync stay disabled until explicitly opted in?

---

## Lead 6: Multi-provider disambiguation

### Findings

**Anonymous vs named provider coexistence** (`internal/config/vault.go:92-102`):
- `VaultRegistry.Validate()` explicitly rejects mixing `[vault.provider]` (anonymous) and `[vault.providers.*]` (named) in the same file.
- The error message is clear: `"%s declares both [vault.provider] (anonymous) and [vault.providers.*] (named) -- pick one shape"` (`vault.go:96-101`).
- This validation runs at parse time for workspace configs and global overrides alike (`config.go:313-314`, `config.go:458-467`).
- **Therefore, it is impossible for a single file to declare BOTH an anonymous singular provider AND a named provider.**

**Collision detection for same-named providers across layers** (`docs/guides/vault-integration.md`, §"Conflict resolution"):
- When a workspace declares `[vault.providers.team]` and the personal overlay also declares `[vault.providers.team]`, this is a hard error (R12 collision rule).
- The resolver enforces this; exact code path: `internal/vault/resolve/resolve.go` (not fully read, but the guide documents the rule as an error).
- **This means: a provider name can't exist in both team and personal overlay.**

**What happens with multiple named providers and no explicit selection**:
- There's no existing code path for "pick one of several providers implicitly." The existing resolver always knows which provider to use because:
  - In anonymous-single-provider mode, there's only one choice.
  - In named-multiple mode, every `vault://` URI explicitly names the provider (`vault://providerName/key`).
- For machine-identity-vault-sync, the feature is NEW and requires explicit routing because there's no vault:// URI to guide the selection.

**Error handling for invalid provider-name references** (`internal/config/validate_vault_refs.go`):
- Validation for `vault://` URIs checks that the named provider exists in the same file.
- If a named provider is missing, the error is `"vault URI %q references unknown provider %q. Declared providers in this file: [%s]"` (`validate_vault_refs.go:285-293`).
- This could be a model for credential-sync: if `from = "missing-name"` is set, the parser could check it against `known = cfg.Vault.KnownProviderNames()` and emit a similar error.

**Handling when personal overlay declares named provider, team workspace is anonymous** (hypothetical collision scenario):
- Personal: `[global.vault.providers.my-personal]` (named)
- Team: `[vault.provider]` (anonymous)
- These would NOT collide because they're in different files and in different shapes (named vs anonymous).
- The resolver stacks them: team's anonymous provider + personal's named provider(s). Both are available.
- For credential-sync, the feature would need to pick one: if the personal overlay declares multiple providers, it can't silently assume which one to use.

### Implications for Requirements

1. **The mutual-exclusion rule is a hard boundary**: A file cannot declare both anonymous and named shapes. This means:
   - If a personal overlay uses `[global.vault.provider]` (anonymous), `from` is not needed — there's only one choice.
   - If a personal overlay uses `[global.vault.providers.*]` (named), `from` is required (or else the feature errors).
2. **Explicit `from` for named-multiple is necessary**: When multiple named providers are declared, there's no implicit default. The config must say which one supplies machine-identity entries.
3. **R12 collision already handles cross-file name conflicts**: If a personal overlay declares `[global.vault.providers.team]` and the workspace declares `[vault.providers.team]`, R12 (collision detection) will error before credential-sync even runs.
4. **Validation can be parser-time or apply-time**: Parser-time validation (check `from` name against declared providers) would catch errors early and is consistent with vault:// URI validation. Apply-time validation (defer the check until the feature runs) is simpler but less helpful.
5. **The proposed design handles all cases clearly**:
   - Anonymous singular: credential-sync uses it (no `from` needed).
   - Named single: credential-sync uses it (no `from` needed, but explicit `from = "name"` is also valid and explicit).
   - Named multiple: `from` is required, must match a declared name, or error.

### Open Questions

1. Should a personal overlay with a single named provider (e.g., `[global.vault.providers.personal]` and nothing else) require an explicit `from = "personal"`, or should niwa infer it? The simpler rule is: `from` is only required when multiple providers are declared. This matches the logic for `vault_scope` on multi-source workspaces.
2. If `from` references a provider declared in the personal overlay that ALSO shadows a team provider (R12 collision), should credential-sync still use it? The answer is "no" because R12 would already have errored. But the PRD should note this clearly.

---

## Summary

**Lead 3** confirms that storing one TOML body per `(kind, project)` pair at a well-known Infisical path is feasible; UUIDs in paths are valid, and versioning via a body field is pragmatic. The key challenge is documenting the canonical path convention so users know where to store entries.

**Lead 4** shows that adding `[global.machine_identities] from = "<provider-name>"` to the global config follows niwa's existing precedents (vault_scope, provider-name validation patterns) and is backward-compatible. Parser-time validation of the `from` name against declared providers would catch errors early, mirroring vault:// URI validation.

**Lead 6** reveals that the mutual-exclusion rule between anonymous and named providers is hard-coded at parse time, eliminating the ambiguity of "which provider to use" when only one shape is in play. Explicit `from` is needed only when multiple named providers are declared, a requirement that's clear and unambiguous.

All three leads point toward a design that is implementable, consistent with niwa's existing patterns, and has clear error semantics for the main failure cases (missing provider, invalid schema version, auth failure).
