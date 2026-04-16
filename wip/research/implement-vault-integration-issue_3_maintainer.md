# Maintainer Review — PLAN Issue 3 (config schema + MaybeSecret)

Target: `bb1615e90532ecc0c3aec2e0e4ff79c75530a454` on `docs/vault-integration`
Scope: `internal/config/{maybesecret,vault,env_tables,validate_vault_refs}.go` + tests;
modifications to `internal/config/config.go`, `internal/workspace/{override,materialize,workspace_context,scaffold}.go`.

Overall: the package is well-documented. Every exported type, method and validation
helper has a purpose-and-usage GoDoc block. The custom TOML unmarshaler for
`EnvVarsTable` is correctly documented at both the struct site (`config.go:53-76`)
and the hook site (`env_tables.go:16-25`). Backwards-compatibility is covered by
an explicit test (`TestParseV06BackwardsCompat`). The `MaybeSecret` sum-type
contract is stated up front and the parser-vs-resolver responsibility split is
called out in multiple places. The `fileLabel` parameter in `VaultRegistry.Validate`
is nicely reused so personal-overlay errors ("global overlay", "workspaces.<name>
overlay") stay distinguishable from workspace errors.

Below are the specific places where the next developer would form a wrong mental
model or get an error message that sends them on a debugging detour.

---

## Blocking

### B1. `[env.vars]` error message for reserved keys is actionably wrong

`internal/config/env_tables.go:37-66` routes any top-level key under
`[env.vars]` / `[env.secrets]` (and their `claude.env` counterparts) that happens
to be named `required`, `recommended`, or `optional` into the sub-table branch
unconditionally. A user who writes

```toml
[env.vars]
required = "1"
```

meaning "declare an environment variable literally named REQUIRED with value 1"
will hit `coerceDescriptionMap` and see:

> env vars/secrets.required must be a TOML table, got string

That sends the user down the wrong path — they'll try wrapping the value in a
table, which then parses as a sub-table of requirement descriptions and turns
their intended variable definition into a classification entry. Nothing in the
error tells them `required` is a reserved key name. The schema docstring on
`EnvVarsTable` (`config.go:53-76`) mentions the three reserved sub-tables, but
no runtime error points a user at them.

The fix is to detect the "reserved key + scalar value" shape specifically and
produce an error like:

> env.vars: key "required" is reserved for the requirement-description
> sub-table; a variable named "required" is not allowed

Severity: this is the prototypical "misleading error" case — the input is valid,
the state (reserved keyword) is the problem, and the emitted error lies about
which.

### B2. `walkVaultRefsForUnknownProvider` error for `vault://KEY` against named-only registry renders provider name as `""`

`internal/config/validate_vault_refs.go:195-214`. The "anonymous URI against
named-only registry" case is exercised by `TestParseRejectsAnonymousRefWithNamedProvider`
(`vault_test.go:495`), but that test only asserts `err != nil`. The actual
message is:

> env.secrets.GITHUB_TOKEN references provider "" via "vault://GITHUB_TOKEN",
> but the config declares no such provider

Two readability traps:

- The quoted empty string `""` reads as a typo or a missing field, not as "the
  URI omitted the provider segment".
- The user has no hint that the fix is to qualify the URI with the provider
  name (`vault://team/GITHUB_TOKEN`) or switch the registry to the anonymous
  `[vault.provider]` shape.

The next developer triaging a user bug report that quotes this error will spend
a while staring at an empty-string mystery. Prefer:

> env.secrets.GITHUB_TOKEN uses an anonymous `vault://KEY` reference but the
> config declares named providers ([vault.providers.team]); use
> `vault://<provider>/KEY` or switch to `[vault.provider]`.

Fix is to branch on `name == ""` vs `name != ""` inside the `!known[name]`
branch.

---

## Advisory

### A1. `MaybeSecret`'s "exactly one of Plain or Secret" invariant is documented but not enforced

`internal/config/maybesecret.go:8-37`. The struct exposes `Plain`, `Secret`, and
`Token` as public fields with no constructor. The docstring says "Exactly one
of Plain or Secret is populated at any given time", but callers can trivially
build `MaybeSecret{Plain: "foo", Secret: v}` — `String()` silently prefers
`Secret` (safe), `IsSecret()` returns true, and `Plain` becomes invisible
orphaned state.

The only construction sites today are the parser and the yet-to-land resolver,
both internal, and the safe-by-default `String()` behavior prevents a plaintext
leak even if the invariant is violated. So this is not a blocker. But a future
contributor writing a test fixture or a secondary producer (a migration tool,
a second parser path) will read the invariant and assume it is enforced. A
brief `Validate()` method or a `newResolved(value secret.Value, token) MaybeSecret`
constructor that zeros `Plain` would make the contract check-able, and the
parser/resolver could use it.

Alternatively, strengthen the comment to explicitly say "the invariant is held
by convention; `String()` treats `Secret` as authoritative when both are set".

### A2. `Vault` field nil-safety is relied upon silently in three places

`internal/config/validate_vault_refs.go:100` calls `cfg.Vault.KnownProviderNames()`
without a nil check. `KnownProviderNames` (vault.go:145-157) handles `v == nil`
correctly, as do `Validate` and `IsEmpty`. The call-through works, but the
pattern "passing a nil receiver to a method" is unusual in Go and invites a
misread: the next developer sees `cfg.Vault.KnownProviderNames()` and assumes
`cfg.Vault != nil` was established earlier. The same pattern is used at
`config.go:258,403,410` (`.Validate(label)`).

Either nil-guard at each call site, or add a one-line comment at the first
call:

```go
// KnownProviderNames is nil-safe; no guard needed here.
known := cfg.Vault.KnownProviderNames()
```

Preference: a comment is cheaper than restructuring.

### A3. Scaffold template's `[vault]` block appears after `[vault.providers.*]` without a visual separator

`internal/workspace/scaffold.go:80-88`:

```
# [vault.providers.team]
# kind = "infisical"
# project_id = "team-project"
# [vault.providers.personal]
# kind = "sops"
# key_path = "keys/personal.age"
#
# [vault]
# team_only = ["CRITICAL_TOKEN"]
```

A user running `niwa init` and skim-reading the template might uncomment
`[vault.provider]` (singular), `[vault.providers.personal]`, AND `[vault]`
and expect it to work. The "Pick ONE shape" note at line 70-71 is upstream of
the `OR:` marker and is easy to miss once you're eyeballing which providers to
enable. The `[vault]` block at the bottom is orthogonal to the anon-vs-named
choice (it holds `team_only`) but visually appears to be a continuation.

Minor: add a one-line separator comment:

```
# Shared settings (applies regardless of shape chosen above):
# [vault]
# team_only = ["CRITICAL_TOKEN"]
```

### A4. Parser/resolver split is documented verbally but not cross-linked

`maybesecret.go:12-18` refers to "the resolver (Issue 4)". Once Issue 4 lands,
the Issue-number pointer will go stale. Same pattern at `vault.go:29-30`,
`validate_vault_refs.go:30-32`, `materialize.go:312,436`. These are transition
markers and will naturally be cleaned up when Issue 4 lands — flagging only so
the next PR author remembers to scrub the "(Issue 4)" annotations rather than
leave them as permanent fossils.

---

## What reads cleanly

- `VaultProviderConfig.UnmarshalTOML` (vault.go:51-81) — docstring explains why
  the Config map exists (decoupling from compiled-in backends) and the reset
  comment at line 56 pre-empts the "why zero these?" question.
- `EnvVarsTable` split and the commented example in `config.go:62-70` give a
  reader enough to form a correct mental model of the four-bucket layout
  without reading `env_tables.go`.
- `VaultRegistry.Validate`'s use of `fileLabel` so every error is
  source-attributed (workspace config / global overlay / workspaces.<name>
  overlay).
- `extractProviderName`'s explicit "assumes caller has already confirmed the
  vault:// prefix" comment (validate_vault_refs.go:310-313).
- `config_test.go` is consistent: every `map[string]string`-era assertion has
  been migrated to `.Values[K].Plain` and the test assertions now read
  structurally the same as the TOML input. No mixing of old and new patterns.
- `TestParseV06BackwardsCompat` explicitly guards the "a pre-vault config must
  still parse and must not emit spurious warnings" contract — the kind of
  test that prevents silent regressions.

---

## Summary counts

- Blocking: 2
- Advisory: 4

Recommendation: approve after addressing B1 and B2. Both are small, localized
error-message improvements; neither requires structural change.
