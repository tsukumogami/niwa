# Explore Scope: machine-identity-vault-sync

## Core Question

Can niwa let the user's personal vault (declared via the global config's
`[global.vault.provider]` block) supply the machine-identity credentials
that today must be hand-edited into `~/.config/niwa/provider-auth.toml`
on every machine? Doing so would let a developer create one machine
identity per project and distribute it across their laptops via their
own Infisical org.

## Context

### What the global config gives us today

`niwa config set global <slug>` clones a personal-overlay repo to
`~/.config/niwa/global/` and parses its `niwa.toml`. The overlay can
declare a `[global.vault.provider]` block (or named `[global.vault.providers.*]`).
At apply time (`internal/workspace/apply.go:746`), `injectProviderTokens`
already runs against the personal-overlay's vault registry, matching it
against `provider-auth.toml` entries by `(kind, project)` tuple. So the
personal vault is *already a reachable, authenticatable surface* inside
the apply pipeline.

### What `provider-auth.toml` does today

`internal/workspace/providerauth.go` reads `~/.config/niwa/provider-auth.toml`
once per apply (`apply.go:492`). Each `[[providers]]` entry holds a
machine-identity credential (`client_id`/`client_secret`/`api_url`) for
one Infisical project, matched by `(kind, project)`. The file is optional;
single-org users never create it. There is no command to generate or
distribute it — `niwa vault auth add` is explicitly listed as deferred
(PRD-vault-integration §Future Work).

### Why this is not the rejected pattern

PRD-vault-integration explicitly rejected (R12, D-9) a personal overlay
that declares `[workspaces.<scope>.vault.providers.team]` to *swap* the
team's vault provider in bulk. The rationale was supply-chain risk
(silent redirection of all team vault calls) and override-visibility
violation. **This proposal is different**: the team config still queries
the team's vault — only the credentials used to authenticate are sourced
from the personal vault instead of from a hand-edited local file. No
`vault://` URI resolves to a different place; the bytes that come back
are unchanged.

### Where the threat model still bites

R31 (override visibility) survives intact and applies here. If a vault
entry silently overrides a local-file entry for the same `(kind, project)`,
the user can't tell which credential authenticated. Whatever this
proposal lands on must make the source of every credential visible
in `niwa status` and apply diagnostics.

### Constraints from the existing API surface

- `vault.Provider.Resolve` is single-key only; no list/scan API exists.
  We cannot "iterate every credential in a folder" without naming them.
- `secret.Value` is a flat opaque string; it does not carry structured
  data. A multi-field credential (client_id + client_secret + api_url)
  must either be three separate keys or one packed string (JSON/TOML).
- Infisical supports folder paths, so namespacing under e.g.
  `/niwa/provider-auth/...` is feasible.

## In Scope

- Sync direction: vault -> local credential pool (memory or disk)
- Conflict resolution between local file entries and vault-sourced entries
- Convention path layout inside the personal vault project
- Bootstrap flow on a fresh machine (chicken-and-egg: vault auth needed
  to fetch credentials that include vault auth)
- Override visibility: making the source of every credential auditable
- The personal vault as the only sync source (not team vault — team
  configs are public-facing and shouldn't carry machine identities)

## Out of Scope

- Distributing user-PAT-style secrets that already work via vault://
  references in `[env.secrets]` (this proposal is specifically about
  the bootstrap-credential pool, not user payloads)
- Replacing or shadowing team-declared vault providers (covered by the
  R12/D-9 rejection and not revisited)
- Non-Infisical backends (sops/age) — deferred per existing v1 scope
- Writing credentials back to the vault from niwa (no `niwa vault auth push`)
- Auto-generating machine identities (no Infisical API call to mint
  identities — that stays a manual dashboard step)
- Cross-user distribution (this is per-user, across that user's machines,
  not team-wide credential sharing)

## Research Leads

### 1. Conflict resolution: which wins when local file and vault disagree on a `(kind, project)` entry?

Three credible models — local-wins, vault-wins, vault-only-when-opted-in.
Each has a coherent story. The right answer depends on what user
behavior we want to make easy and what we want to make explicit.
We need to map each option to the override-visibility requirement
(R31) and to the "fresh machine vs configured machine" lifecycle.

**Preliminary position**: vault-augments-local-wins-on-conflict.
Mirrors how the personal overlay relates to team config (personal
wins on shadow). Lets a developer override one credential locally
without touching the vault. Visibility surfaced via existing
shadow-diagnostics infrastructure.

### 2. Materialization: in-memory only, write-through to disk, or opt-in cache?

Today's vault integration explicitly avoids a token cache ("re-authenticate
on every apply", per vault-integration.md). Mirroring that for fetched
machine-identity entries means no on-disk surface to leak, but adds
~100ms per provider per apply. Disk materialization gives fast subsequent
applies but creates the question of who owns the file.

**Preliminary position**: in-memory only on every apply. Aligns with
the existing no-cache invariant and avoids the "who edited this file
last" ambiguity. The local `provider-auth.toml` keeps its current
role (manually-managed, persistent overrides) and the vault layer is
ephemeral on top.

### 3. Convention layout inside the vault project

The Infisical provider exposes `path` (folders) and flat string keys.
Two natural shapes:
- (a) One Infisical key per credential field: `/niwa/provider-auth/<project-uuid>/client_id`, `/niwa/provider-auth/<project-uuid>/client_secret`, `/niwa/provider-auth/<project-uuid>/api_url`
- (b) One Infisical key per credential entry, value is a packed JSON/TOML body: `/niwa/provider-auth/<project-uuid>` -> `{"client_id":"...","client_secret":"...","api_url":"..."}`

Shape (a) gives finer access control and matches Infisical's natural
key model. Shape (b) is one secret per project (simpler to manage in
the UI, matches the user's mental model "one machine identity per
project").

Both require some way to enumerate which projects have entries, since
`vault.Provider.Resolve` is single-key. Options for that:
- A manifest key (e.g., `/niwa/provider-auth/manifest` with a JSON list of project UUIDs)
- Iterate the `[[providers]]` UUIDs declared in the workspace + personal
  overlay configs (no enumeration needed — we already know which projects
  we want credentials for)

**Preliminary position**: shape (b), a single key per project holding a
TOML or JSON body, and NO manifest — niwa enumerates the (kind, project)
pairs it needs from the resolved vault registries and asks the vault
for exactly those keys. Misses fall through to the local file / no auth.

### 4. Bootstrap flow on a fresh machine — what's the chicken-and-egg cost?

The personal vault itself needs auth. If the credential to authenticate
the personal vault lives only inside the personal vault, bootstrap is
impossible. Today's natural answer: the user runs `infisical login`
once (their primary org, which is their personal Infisical org), and
the personal vault uses *that* session — no entry in `provider-auth.toml`
needed for the personal vault. We need to confirm this is the only
supported path, document it, and decide what to do if a user puts
their personal vault in a non-default org.

**Preliminary position**: explicitly require that the personal vault
provider authenticates via the CLI session (no entry-in-itself). Document
this constraint. If a user's personal vault lives in a non-default org,
they fall back to the manual `provider-auth.toml` for *that one entry*.

### 5. Visibility surface: how does the user audit "which credential did niwa use"?

The existing shadow-diagnostic pattern (stderr line + `niwa status` count
+ `--audit-secrets` SHADOWED column) is the right precedent. We need a
new "credential source" dimension on the audit. Open question: is this
a new column on `--audit-secrets`, a new `--audit-auth` subcommand, or a
new line in `niwa status`?

**Preliminary position**: a new `niwa status --audit-auth` view that
lists every (kind, project) pair niwa needed credentials for, the source
it found (`vault:<provider-name>`, `local-file`, `cli-session`, `none`),
and a SHADOWED indicator when both vault and local-file entries exist
for the same pair. Modeled on `--audit-secrets`. Matches existing UX.

### 6. What about `[global.vault.providers.<name>]` (multiple named providers in the global config)?

The personal overlay can declare multiple named vault providers, not
just one anonymous `[global.vault.provider]`. If credential sync reads
from "the personal vault," which one? Single? All? A specific named
one (e.g., `[global.vault.providers.identities]`)?

**Preliminary position**: a dedicated opt-in field in the global config
that names which provider to query for machine-identity entries — e.g.,
`[global.machine_identities] from = "identities"` (referencing one of
the declared `[global.vault.providers.*]`). Anonymous singular
`[global.vault.provider]` is the implicit default when no name is given
and credential sync is enabled. Explicit beats magic.

