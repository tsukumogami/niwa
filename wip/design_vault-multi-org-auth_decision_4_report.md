# Decision 4: Credential File Ownership

## Options Evaluated

### Option A: Backend-specific credential files

Each backend owns a separate file under `~/.config/niwa/auth/` (e.g., `infisical.toml`, `sops.toml`). The backend's Factory reads its own file during Open. niwa core has no awareness of credential file formats.

Strong backend isolation, but fragments the user experience. Users managing multiple backends need to know each file's location and schema independently. There's no way to list all configured credentials without iterating every known backend. Each backend also reimplements file reading, permission checks, and TOML parsing -- minor duplication but real.

### Option B: Single generic envelope, backend-specific payload

One file (`~/.config/niwa/provider-auth.toml`) with `[[providers]]` entries, each declaring `kind` and backend-specific fields as a flat map. niwa core reads the file, routes entries by `kind`, and hands the map to the backend's Factory. Mirrors the existing `VaultProviderConfig` pattern from workspace.toml.

Keeps credential management in one place. niwa core handles file reading and 0o600 permission enforcement once. Backends parse only their own fields from the map they receive -- the same pattern Factory.Open already uses for workspace config. The downside is mixed backend fields in one file, but users already see this in workspace.toml and the `kind` field makes each entry self-describing.

### Option C: Backend-specific sections in one file

One file with TOML table names matching backend kinds (`[infisical]`, `[sops]`). Each section has its own schema. Parse errors are somewhat scoped per section.

A hybrid that doesn't clearly improve on either A or B. It breaks from the existing `[[providers]]` pattern in workspace.toml, introduces an unusual TOML layout where backend names are table keys, and still shares a single file so corruption risks remain. Section-level schema evolution is harder than flat map evolution.

## Chosen

**Option B: Single generic envelope, backend-specific payload**

## Rationale

Option B directly extends the pattern already established in `VaultProviderConfig` and `ProviderConfig`. The workspace config already routes by `kind` and passes `map[string]any` to backends -- the credential file does the same thing for auth material. niwa core handles file reading and permission enforcement exactly once, and Factory.Open already knows how to extract typed fields from an opaque map. Backends that don't need a credential file (sops reading from `~/.age/`) simply have no entry, and that's fine. The pattern also enables a single `niwa vault auth list` command without backend-specific discovery logic.

## Rejected

- **Option A (Backend-specific files)**: Fragments user experience and duplicates file-reading infrastructure across backends without meaningful isolation benefit, since backends already share the Factory.Open(ProviderConfig) contract.

- **Option C (Backend-specific sections)**: Introduces a non-standard TOML layout that breaks from the existing `[[providers]]` pattern without solving any problem that Option B doesn't already handle.

## Schema Example

```toml
# ~/.config/niwa/provider-auth.toml
# Permissions: 0o600 (niwa refuses to read if group/other-readable)

[[providers]]
kind          = "infisical"
project       = "c6673ab0-1234-5678-9abc-def012345678"
client_id     = "st.abcdef..."
client_secret = "st.secret..."

[[providers]]
kind          = "infisical"
project       = "d7784bc1-2345-6789-abcd-ef0123456789"
client_id     = "st.ghijkl..."
client_secret = "st.secret2..."
api_url       = "https://infisical.corp.example.com/api"

[[providers]]
kind      = "onepassword"
vault     = "Engineering"
token     = "ops_..."
```

Backends that handle auth externally (sops reads `~/.age/key.txt`, Infisical CLI reads `~/.infisical` for interactive users) need no entry. Entries exist only for machine-identity or service-account credentials that niwa must inject.

## Implementation Implications

- The credential file path is `~/.config/niwa/provider-auth.toml`. A helper in `internal/config` reads and permission-checks this file (0o600 enforcement).
- The parsed structure reuses `VaultProviderConfig` (or a near-identical type) with `kind` + opaque map. The credential-file loader returns `[]ProviderConfig` keyed by kind+project (or kind+backend-specific discriminator).
- `apply.go` (or the resolver stage) matches workspace provider declarations to credential-file entries by kind and a backend-defined matching key (e.g., Infisical matches on `project`). The matching logic lives in each backend, not in niwa core.
- Factory.Open receives a merged config: workspace-declared fields (project, env, path) plus credential-file fields (client_id, client_secret). The merge is shallow -- credential fields override workspace fields for the same key.
- Single-org users who authenticate via `infisical login` or environment variables never create this file. The file is optional; its absence is not an error.
- `niwa vault auth list` reads the credential file and displays all entries grouped by kind, without needing backend-specific discovery.
