# Decision 1: Credential File Schema and Matching

## Question

What is the credential file schema, where does niwa read it, and how
does it match credentials to providers?

## Chosen Option

**Option 4 (keyed by kind + project tuple) with read location C
(Factory.Open).**

The credential file uses a TOML array-of-tables keyed by `(kind,
project)`. The Infisical `Factory.Open` reads the file itself when the
provider config does not already contain a token (e.g., from
`INFISICAL_TOKEN`). No new config concept is introduced; no workspace
config schema changes.

## Schema

File: `~/.config/niwa/provider-auth.toml`, permissions 0o600.

```toml
[[providers]]
kind = "infisical"
project = "a1b2c3d4-..."
client_id = "mid.xxxxxxxx"
client_secret = "sk.xxxxxxxx"

[[providers]]
kind = "infisical"
project = "e5f6g7h8-..."
client_id = "mid.yyyyyyyy"
client_secret = "sk.yyyyyyyy"
```

Each entry declares a `kind` (matching the vault backend kind string),
a `project` (matching the workspace config's provider `project` field),
and the machine-identity credentials (`client_id`, `client_secret`)
that niwa uses to obtain a short-lived JWT via Infisical's
universal-auth login endpoint.

## Matching Algorithm

When `Factory.Open` constructs an Infisical Provider, it:

1. Checks `config["token"]` -- if present (CI via `INFISICAL_TOKEN`,
   or explicitly set in the workspace config), skip the credential
   file entirely. This preserves zero-ceremony for single-org and CI
   flows.
2. Reads `~/.config/niwa/provider-auth.toml`. If the file doesn't
   exist, proceed without it (falls back to the Infisical CLI's
   stored session, which is the current behavior).
3. Scans the `[[providers]]` array for an entry where `kind ==
   "infisical"` AND `project == config["project"]`. First match wins.
4. If found, calls `infisical login --method=universal-auth
   --client-id=<cid> --client-secret=<cs> --silent --plain` (or an
   equivalent HTTP POST to `/api/v1/auth/universal-auth/login`) to
   obtain a short-lived JWT.
5. Stores the JWT on the Provider struct so `runInfisicalExport`
   passes `--token <jwt>` on argv, bypassing the stored session.

If no matching entry is found, the provider proceeds unchanged --
the Infisical CLI uses its own session, exactly as it does today.

## Read Location: Factory.Open (Option C)

The credential file is read inside `Factory.Open` in the infisical
package. Each provider reads it independently.

### Why not Option A (apply.go)?

Injecting credentials in `apply.go` would couple the workspace layer
to backend-specific auth details. `apply.go` calls
`resolve.BuildBundle` which calls `Registry.Build` which calls
`Factory.Open`. The workspace layer knows nothing about Infisical
projects or universal-auth -- it shouldn't start now. The credential
file is a backend concern.

### Why not Option B (resolve.BuildBundle)?

`BuildBundle` converts config structs to `ProviderSpec` values and
calls `Registry.Build`. It works with the generic `ProviderConfig
map[string]any` and has no backend-specific knowledge. Merging
credentials here would either require BuildBundle to understand
every backend's auth model, or require a generic "credential
injection" abstraction that is over-engineered for a single backend.

### Why Option C works

`Factory.Open` already reads backend-specific config keys (`project`,
`env`, `path`). Adding credential-file reading here is natural:

- The factory knows its own `kind` and the `project` it's opening,
  so it has exact match keys.
- The file is small (a few entries). Reading it N times (once per
  provider) is negligible. If profiling ever shows this matters, a
  package-level `sync.Once` cache is a trivial follow-up.
- The backend stays self-contained: no other package needs to know
  about the credential file format.
- Test injection via `_commander` already exists; credential-file
  reading can be similarly abstracted for testing.

## Why Not the Other Schema Options

### Option 1 (keyed by project UUID)

Functionally similar to Option 4 but uses TOML table keys:
`[providers."a1b2c3d4-..."]`. UUIDs as TOML keys are awkward to read
and edit. The array-of-tables shape is more natural for a list of
credentials where the user might add/remove entries. Option 4 gets
the same exact-match semantics with a cleaner file format.

### Option 2 (keyed by org slug)

Requires adding an `org` field to the workspace config's provider
declaration -- a schema change that affects every workspace, not just
multi-org users. The org slug is also not currently available in the
Infisical provider config, so the matching is indirect. Project-level
matching is more precise and doesn't require schema changes.

### Option 3 (keyed by alias)

Adds a new concept (`auth = "tsukumogami"` in the workspace config)
that every user sees in the schema even if they never use multi-org.
The alias indirection is useful when many projects share one credential,
but that can be handled later if needed. For now, the credential file
is small enough that duplicating entries is acceptable.

## Single-Org User Impact

None. If `~/.config/niwa/provider-auth.toml` doesn't exist, the code
path is unchanged: `Factory.Open` proceeds without it, and the
Infisical CLI uses its stored session. The feature is purely additive.

## Risks and Mitigations

| Risk | Mitigation |
|------|------------|
| File contains long-lived secrets on disk | 0o600 permissions enforced on read; niwa warns if permissions are too open. Machine-identity secrets are already the accepted threat model per PRD. |
| N file reads per apply (one per provider) | File is tiny; OS page cache handles it. `sync.Once` available if needed. |
| `client_secret` on argv for `infisical login` | Use HTTP POST to `/api/v1/auth/universal-auth/login` instead of CLI subprocess to avoid argv exposure in `ps`. Falls back to CLI if HTTP fails. |
| Credential file schema evolution | The `kind` field future-proofs for non-Infisical backends. Array-of-tables is extensible without breaking existing entries. |

## Implementation Notes

- The credential file path should be a constant in the infisical
  package, with a test helper to override it.
- `Factory.Open` should validate 0o600 permissions and return a clear
  error if the file is world-readable.
- The JWT obtained from universal-auth login should be cached on the
  Provider struct (not written to disk) and passed via `--token` to
  `infisical export`.
- The `commander` interface may need a second method (or a separate
  `authenticator` interface) for the universal-auth login call.
