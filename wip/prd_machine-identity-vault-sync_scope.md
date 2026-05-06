# /prd Scope: machine-identity-vault-sync

## Problem Statement

Today, every developer working across multiple Infisical organizations
must hand-create and maintain `~/.config/niwa/provider-auth.toml` on
each of their machines. The file holds machine-identity credentials
(client_id/client_secret pairs) for each non-default Infisical org
their workspaces touch. There is no distribution mechanism: when a
developer joins a new project they re-create the entry on every
laptop, and rotation requires touching every machine. niwa's existing
infrastructure already authenticates the user's personal-overlay vault
provider — that vault is a natural place to store and distribute these
bootstrap credentials, but the wiring to do so doesn't exist.

## Initial Scope

### In Scope
- A new credential layer that lets the personal-overlay's
  `[global.vault.provider]` (or a named `[global.vault.providers.*]`)
  supply `[[providers]]`-equivalent entries on demand
- Conflict policy between local-file entries and vault-sourced
  entries (preliminary: local wins)
- In-memory only resolution per apply (no on-disk cache)
- A published convention for how credential entries are named/stored
  inside the personal vault project
- A user-visible audit surface so the source of every credential
  is inspectable (extends/mirrors `niwa status --audit-secrets`)
- An opt-in mechanism in the global config (this is not on by default)
- Bootstrap-flow documentation: the personal vault must be
  authenticated via CLI session
- Failure-mode contract: vault unreachable, key missing, malformed
  body, version mismatch

### Out of Scope
- Replacing or shadowing team-declared vault providers (the
  R12/D-9 rejected pattern; not revisited)
- Distributing user-payload secrets like API keys via this mechanism
  — those already work via `vault://` references in `[env.secrets]`
- Non-Infisical vault backends (sops/age remain v1.1+ deferred)
- Writing credentials TO the vault from niwa (no
  `niwa vault auth push`)
- Auto-minting Infisical machine identities (no Infisical admin-API
  call; identity creation stays a manual dashboard step)
- Cross-user credential sharing / team-wide credential distribution
- Removing or deprecating `provider-auth.toml` (it remains the
  per-machine override layer)

## Research Leads

1. **Conflict policy as a published contract**: The exploration
   landed on "local wins" by analogy to personal-vs-team overlay
   semantics. The PRD needs to evaluate whether a flag to invert
   this (vault-wins) has any legitimate use case (e.g., enforcing
   centrally-managed rotation), or whether the simpler one-way
   policy is enough.

2. **Audit surface design**: The exploration proposed
   `niwa status --audit-auth` listing every (kind, project) niwa
   needed credentials for, with source = `vault:<name>` /
   `local-file` / `cli-session` / `none`. The PRD needs to commit
   to a specific surface (subcommand, column on existing audit,
   apply-time stderr line, or combination) and the diagnostic
   wording when both layers contribute.

3. **Schema path inside the vault project**: Pick the canonical
   convention. The exploration's preliminary proposal: one
   Infisical key per (kind, project) pair, value is a packed
   TOML body; key path
   `/niwa/provider-auth/infisical/<project-uuid>`. The PRD needs
   to commit to a path, the body format (TOML vs JSON), and the
   versioning strategy if the body shape ever needs to evolve.

4. **Opt-in shape in the global config**: How does a personal
   overlay declare "use this vault provider as the credential
   source"? The exploration proposed
   `[global.machine_identities] from = "<provider-name>"` with
   anonymous-singular default. The PRD needs to commit to a
   spelling and define what happens when the field references a
   provider name that doesn't exist.

5. **Failure-mode UX**: What does niwa say to the user when:
   the personal vault is unreachable; the conventional key is
   absent; the body is malformed; the body is present but its
   client_secret has been rotated and is now invalid? The PRD
   needs a per-mode contract (error vs warning, exit code,
   stderr text shape).

6. **Multi-provider disambiguation**: When the global config
   declares multiple `[global.vault.providers.*]` blocks, how
   does the credential-sync feature pick which one to query?
   Default to anonymous singular when present; require explicit
   `from = "<name>"` in named-multiple mode. The PRD needs to
   define the validation error when neither is satisfied.

7. **Public-repo guardrail interaction**: The personal overlay
   can itself be a public repo. The plaintext-secrets guardrail
   walks `*.secrets` tables in configs. Does it need a similar
   check for "personal overlay opts into credential sync against
   a vault provider whose project lives in a public-shape
   repo"? The PRD needs a stance, even if the answer is "no
   change needed because credentials live in the vault, not in
   the overlay file."

## Coverage Notes

The exploration didn't pressure-test:
- Failure-mode catalogs — needs explicit enumeration in the PRD's
  acceptance criteria.
- Whether this ships in the v1 vault scope or as v1.1. The PRD
  should state the positioning and any v1 dependencies.
- Interaction with the deferred sops/age backend. Likely
  orthogonal (sops doesn't have machine-identity-style auth)
  but the PRD should explicitly say so.
- Performance under load: ~100ms per provider per apply was
  cited as a comparable cost to today's per-org auth. The PRD
  should set a worst-case bound (e.g., for a workspace touching
  N orgs, total apply latency increase ≤ N × 100ms).
- Rotation flow: when a user rotates an Infisical client_secret,
  what's the user-facing sequence (update vault, next apply
  picks it up, verification command)?
- Telemetry / observability: should `niwa apply` log the
  credential-source mix at end-of-run for ops visibility?

## Decisions from Exploration

The following are settled by exploration and the PRD should treat
them as inputs, not open questions:

- **Not the R12/D-9 rejected pattern**: This feature does NOT swap
  which vault is queried for any `vault://` URI. It only changes
  the source of authentication credentials. The PRD must state
  this distinction crisply, ideally in a §"Why this is not the
  rejected pattern" subsection mirroring the existing guide section.
- **In-memory only on every apply**: No on-disk cache. Re-fetch
  per apply. Aligns with the existing no-token-cache invariant.
- **Local file wins on conflict**: When `(kind, project)` matches
  both a local-file entry and a vault-sourced entry, the local file
  wins. The vault layer augments the local pool; it doesn't replace
  it. Per-key precedence, not per-provider.
- **Personal vault must auth via CLI session**: The vault provider
  that supplies machine-identity entries is itself authenticated
  via `infisical login` (or equivalent), never via an entry sourced
  from itself. The PRD must call this out as a binding constraint.
- **Personal vault is the only credential-sync source**: Team
  configs cannot supply machine identities. Only the global-config
  personal overlay can opt into credential sync. Team configs are
  public-facing and must not carry credentials.
- **Single Infisical key per (kind, project), packed body**: The
  preliminary schema is one key per pair with a packed body.
  No manifest. niwa enumerates the pairs from the resolved
  registries. The PRD should commit to the body format (TOML
  proposed) and the path layout.
- **R31 (override visibility) is binding**: Whatever the design,
  the source of every credential niwa used must be inspectable
  via a CLI surface. This is non-negotiable.
- **Opt-in, not on by default**: A personal overlay that doesn't
  declare credential sync sees no behavior change.
