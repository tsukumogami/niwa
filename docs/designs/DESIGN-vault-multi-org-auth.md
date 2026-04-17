---
status: Proposed
problem: |
  The Infisical backend relies on the CLI's global session for auth, but
  that session is scoped to one organization. Users who work across
  multiple Infisical orgs (team secrets in org A, personal secrets in
  org B) get 403 errors because only one org is reachable per session.
  Single-org users are unaffected; the fix must be additive — zero new
  ceremony for the common case.
decision: |
  Read an optional local credential file (~/.config/niwa/provider-auth.toml,
  0o600) in apply.go before building provider bundles. For each provider
  whose (kind, project) matches a credential entry, obtain a JWT via
  HTTP POST to Infisical's universal-auth endpoint and inject it into
  ProviderConfig["token"]. The Infisical backend appends --token <jwt>
  to its subprocess args when the token is present. No cache; re-auth
  every apply (~100ms per org). Falls back to CLI session when no
  credentials found.
rationale: |
  apply.go already reads ~/.config/niwa/ for the global overlay, so
  reading the credential file there follows the established pattern.
  Factory.Open stays non-blocking and filesystem-free. HTTP POST avoids
  putting client_secret on subprocess argv (R21). No JWT cache avoids
  six failure modes (stale, corrupt, permissions, revoked-token retry,
  concurrent writes, clock skew) for a 200-300ms cost that's under 5%
  of total apply wall-clock. The (kind, project) tuple matching requires
  no new config concepts — the team config already declares both.
---

# DESIGN: Vault Multi-Org Auth

## Status

Proposed

## Context and Problem Statement

niwa's v0.7.1 Infisical backend shells out to `infisical export` and
inherits the CLI's stored session for authentication. That session is
scoped to one Infisical organization — `infisical login` creates a
single session, and switching orgs requires re-logging.

This works for the common case: a developer using one Infisical org
for both team and personal secrets. It breaks when team and personal
vaults live in different orgs — the concrete scenario driving this
design is a user who maintains secrets in the Tsukumogami org (team),
a future Codespar org (another team), and a personal org (personal
PATs). A single `niwa apply` on a tsukumogami workspace needs to
reach all three.

The exploration confirmed that `infisical export --token <jwt>` fully
bypasses the stored session on a per-command basis without mutating it.
This is the designed multi-context mechanism. The gap is that niwa
doesn't obtain or pass per-provider tokens today.

## Decision Drivers

- **Zero ceremony for single-org users.** `infisical login` once +
  `niwa apply` must keep working unchanged. No new files, no new
  config, no new flags.
- **Additive multi-org opt-in.** Multi-org users create a local
  credential file and niwa handles the rest. The file is never
  committed to any repo.
- **No new Go dependencies (R20).** Token acquisition can use the
  `infisical login --method=universal-auth --silent --plain` subprocess
  or a direct HTTP POST — both are stdlib.
- **Threat model alignment.** Per-provider credentials on disk at
  0o600 are within the PRD's accepted risk (same-user processes are
  out of scope). Short-lived JWT caching further bounds exposure.
- **Backend change must be small.** The exploration estimated ~20 lines.
  The design should confirm this stays contained.
- **CI unaffected.** CI uses `INFISICAL_TOKEN` env var, which already
  works as a per-command override. No changes needed.

## Considered Options

### Decision 1: Credential file schema and matching

Four schema options considered:

- **Option 1 — Keyed by project UUID**: `[providers."<uuid>"]` with
  client_id/client_secret. Exact match. Simple but requires users to
  know UUIDs.
- **Option 2 — Keyed by org slug**: `[orgs."<slug>"]`. Requires adding
  an `org` field to the vault provider config — new schema concept.
- **Option 3 — Keyed by alias**: `[auth."<alias>"]` referenced from
  workspace config. Most flexible but adds an indirection concept.
- **Option 4 — (kind, project) tuple**: `[[providers]]` array with
  `kind`, `project`, `client_id`, `client_secret`. Matching by the
  same (kind, project) pair the workspace config already declares. No
  new config concepts.

#### Chosen: Option 4 — (kind, project) tuple

```toml
# ~/.config/niwa/provider-auth.toml (0o600, never committed)

[[providers]]
kind          = "infisical"
project       = "c6673ab0-c95d-4570-b947-5f77501ce38a"
client_id     = "1ad0012d-f55d-430e-abb0-858e30029f2e"
client_secret = "b67723bd..."

[[providers]]
kind          = "infisical"
project       = "ec1d0537-0283-4360-995f-e476e159e2ec"
client_id     = "..."
client_secret = "..."
```

Matching: when apply.go builds ProviderSpecs from the workspace config,
each spec has `Kind` and `Config["project"]`. The credential file is
scanned for a matching `(kind, project)` entry. If found, niwa obtains
a JWT and sets `spec.Config["token"] = jwt`.

**Rationale**: the (kind, project) pair is already present in every
workspace config's `[vault.provider]` declaration — no new config
concept for the user to learn. UUIDs are visible in the Infisical
dashboard URL, so users can copy-paste them. Org slugs would require
a new field on the vault provider; aliases would require a new
indirection concept.

### Decision 2: JWT caching strategy

- **Option 1 — No cache**: re-auth every apply. ~100ms HTTP POST per
  org, 200-300ms total for 2-3 orgs.
- **Option 2 — File-based cache**: store JWT at 0o600, check `exp`
  claim. 99.99% cache hit rate with 30-day TTL.
- **Option 3 — In-memory only**: cache within one apply invocation.
  Saves intra-apply latency for same-org providers.
- **Option 4 — infisical login subprocess**: let CLI cache. But
  overwrites stored session — breaks single-org UX.

#### Chosen: Option 1 — No cache, re-auth every apply

200-300ms auth overhead is under 5% of total apply wall-clock time
(dominated by `infisical export` at 300-800ms). Caching introduces
six failure modes (stale/corrupt/permissions/revoked-retry/concurrent-
writes/clock-skew) for negligible gain. The auth interface is
cache-agnostic — file-based caching can be added as a backward-
compatible optimization in the future if latency becomes a real
concern for users with 5+ orgs.

### Decision 3: Token plumbing path

- **Option A — apply.go reads file, injects into ProviderConfig**: one
  read, inject `token` before BuildBundle. apply.go already reads
  `~/.config/niwa/` for the global overlay.
- **Option B — resolve.BuildBundle reads file**: credential logic in
  the vault/resolve package. Breaks resolve's pure-transformer nature.
- **Option C — Factory.Open reads file**: backend self-contained but
  reads file N times, breaks Factory.Open's non-blocking contract.
- **Option D — AuthProvider interface**: clean separation via interface
  injected into ProviderConfig. Adds indirection for 3 call sites.

#### Chosen: Option A — apply.go reads file, injects into ProviderConfig

apply.go already reads `~/.config/niwa/` for the global overlay (lines
296-308). Reading `provider-auth.toml` from the same directory follows
the established pattern. Factory.Open stays non-blocking and
filesystem-free. resolve.BuildBundle stays a pure transformer. The
token is injected as `ProviderConfig["token"]` — the same mechanism
the Infisical backend already uses for `project`, `env`, `path`.

## Decision Outcome

The three decisions compose into a simple, additive feature:

1. **apply.go** reads `~/.config/niwa/provider-auth.toml` (if it
   exists) once per apply. For each ProviderSpec whose `(Kind,
   Config["project"])` matches a credential entry, it does a single
   HTTP POST to Infisical's universal-auth login endpoint, gets a JWT,
   and sets `spec.Config["token"] = jwt`.

2. **Infisical Factory.Open** reads `config["token"]` (new optional
   field, empty string when absent). Passes it to the Provider struct.

3. **Infisical subprocess** (`runInfisicalExport`) appends
   `--token <jwt>` to the args slice when the token is non-empty.
   Falls back to the CLI session when empty (current behavior).

Single-org users: nothing changes. No credential file → no token
injection → CLI session handles auth. Multi-org users: create
`provider-auth.toml` once, populate with machine-identity credentials
for each org, and `niwa apply` handles the rest.

## Solution Architecture

### Overview

Three changes:

```
~/.config/niwa/provider-auth.toml (new, local-only, 0o600)
        │
        ▼
internal/workspace/apply.go (read file, HTTP POST, inject token)
        │
        ▼
internal/vault/infisical/ (read token from ProviderConfig, pass --token)
```

### Credential file

```toml
# ~/.config/niwa/provider-auth.toml
# Machine-identity credentials for multi-org Infisical auth.
# niwa reads this at apply time. NEVER commit this file.

[[providers]]
kind          = "infisical"
project       = "c6673ab0-c95d-4570-b947-5f77501ce38a"  # Tsukumogami dot-niwa
client_id     = "1ad0012d-f55d-430e-abb0-858e30029f2e"
client_secret = "b67723bd..."

[[providers]]
kind          = "infisical"
project       = "ec1d0537-0283-4360-995f-e476e159e2ec"  # dangazineu-personal
client_id     = "..."
client_secret = "..."
```

### apply.go changes

New function `loadProviderAuth(configDir string) ([]ProviderAuthEntry, error)`:
- Reads `provider-auth.toml` from `configDir` (the `~/.config/niwa/`
  directory where the global config already lives).
- Returns a slice of `{Kind, Project, ClientID, ClientSecret}` entries.
- If file doesn't exist, returns nil (no error — single-org path).
- If file exists but permissions are not 0o600, returns an error
  (security guardrail).

New function `authenticateProvider(ctx, entry) (string, error)`:
- HTTP POST to `https://app.infisical.com/api/v1/auth/universal-auth/login`
  with `clientId` + `clientSecret`.
- Returns the JWT string.
- Uses `net/http` + `encoding/json` (stdlib, R20 compliant).
- Errors wrapped via `secret.Errorf` since `clientSecret` is sensitive.

In `runPipeline`, after reading the global overlay and before building
bundles:

```go
// Multi-org auth: read local credential file, obtain per-provider JWTs.
authEntries, err := loadProviderAuth(niwaConfigDir)
if err != nil { return nil, err }

injectProviderTokens(ctx, authEntries, teamSpecs)
injectProviderTokens(ctx, authEntries, personalSpecs)
```

Where `injectProviderTokens` iterates specs, matches by (kind, project),
authenticates if matched, and sets `spec.Config["token"] = jwt`.

### Infisical backend changes

**infisical.go** — `Factory.Open`: read optional `config["token"]`
string, store on `Provider.token` field.

**subprocess.go** — `runInfisicalExport`: accept `token string`
parameter. If non-empty, append `"--token", token` to the args slice
before `"--format"`.

**Estimated diff**: ~25 lines in infisical.go + subprocess.go. ~60
lines in apply.go (loadProviderAuth + authenticateProvider +
injectProviderTokens). Total ~85 lines of new code.

### Data flow

**Single-org (no credential file):**
```
apply.go → loadProviderAuth → nil (file absent)
         → BuildBundle → Factory.Open(config without token)
         → infisical export (no --token; uses CLI session)
```

**Multi-org (credential file present):**
```
apply.go → loadProviderAuth → [{kind:infisical, project:abc, ...}]
         → injectProviderTokens → HTTP POST → jwt
         → spec.Config["token"] = jwt
         → BuildBundle → Factory.Open(config with token)
         → infisical export --token <jwt> (bypasses CLI session)
```

## Implementation Approach

### Phase 1: Infisical backend --token support

Add `token` field to Provider, read from ProviderConfig in Factory.Open,
pass to runInfisicalExport, conditionally append `--token` to args.

Deliverables:
- `internal/vault/infisical/infisical.go` (token field + Open change)
- `internal/vault/infisical/subprocess.go` (token parameter + args)
- Tests: with-token and without-token paths
- ~25 lines

### Phase 2: Credential file reading + token injection in apply.go

Add `loadProviderAuth`, `authenticateProvider`, `injectProviderTokens`.
Wire into `runPipeline` before bundle building. Permission check on
file (must be 0o600).

Deliverables:
- `internal/workspace/apply.go` (new functions + pipeline wiring)
- `internal/workspace/providerauth.go` (new file, types + loader)
- Tests: file present, file absent, file wrong permissions, HTTP
  mock for authenticateProvider
- ~60 lines

### Phase 3: Documentation

Update `docs/guides/vault-integration.md` with multi-org setup
walkthrough. Document the credential file format and per-org setup.

## Security Considerations

### Credential file on disk

`~/.config/niwa/provider-auth.toml` stores long-lived machine-identity
credentials (client_id + client_secret) in plaintext at 0o600. This
matches the PRD's threat model: same-user processes are explicitly out
of scope, and the file is never committed to any repo. The permission
check at read time rejects world-readable files.

The client_secret is a long-lived credential that does not expire by
default in Infisical's universal-auth. Users should rotate client
secrets periodically via the Infisical dashboard. niwa does not manage
rotation.

### JWT on argv

The JWT is passed via `--token <jwt>` on the `infisical export`
subprocess argv. This is visible to `ps` for the duration of the
subprocess call (~300-800ms). The JWT has a bounded TTL (default 30
days). The PRD's threat model does not defend against same-user
process inspection, so this is accepted. The alternative (passing via
environment variable) is equally visible to same-user processes.

The client_secret is NOT passed on argv — it goes via HTTP POST body
(`net/http`), keeping it out of the process table.

### HTTP call for token acquisition

niwa makes a single HTTPS POST to `app.infisical.com` (or the
configured domain) to acquire the JWT. The request body contains
`clientId` + `clientSecret`. This uses Go's `net/http` with default
TLS — no certificate pinning, but the Infisical API is TLS-only.
Error responses are scrubbed via `secret.Errorf` before being
surfaced to the user (the client_secret must not appear in error
messages).

### Fallback behavior

When no credential file exists (single-org path), no HTTP calls are
made and no tokens are injected. The Infisical CLI's stored session
handles auth. This path is unchanged from v0.7.1.

## Consequences

### Positive

- **Multi-org works.** A user with secrets in 3 Infisical orgs can
  `niwa apply` and reach all of them in a single run.
- **Single-org unchanged.** No new files, flags, or ceremony.
- **Small change.** ~85 lines of new code across 3 files (2 backend +
  1 apply layer). No interface changes. No new packages.
- **Cache-agnostic.** The auth interface (`credentials in → JWT out`)
  supports adding JWT caching later without changing callers.
- **CI unaffected.** `INFISICAL_TOKEN` env var still works as before.

### Negative

- **Long-lived credentials on disk.** `provider-auth.toml` holds
  client_secrets that don't expire by default. Mitigated by 0o600
  permissions and the accepted threat model.
- **Manual credential file setup.** No `niwa vault auth add` command
  in v1. Users edit TOML by hand. Mitigated by documenting the format
  clearly.
- **~100ms per org per apply.** No JWT caching means a fresh HTTP POST
  per org on every apply. Acceptable for 2-3 orgs; may need caching
  if users hit 5+.
- **New HTTP dependency in apply path.** apply.go now makes outbound
  HTTPS calls (previously only git and GitHub API). The calls are
  quick (~100ms) and failure is surfaced as a clear error.

### Mitigations

- **Permission check**: niwa refuses to read `provider-auth.toml` if
  it's not 0o600, surfacing a clear error message.
- **Error scrubbing**: all HTTP error paths go through `secret.Errorf`
  so client_secret never appears in error output.
- **Documentation**: the vault-integration guide includes a multi-org
  walkthrough with copy-pasteable TOML examples.
