# Explore Scope: vault-multi-org-auth

## Visibility

Public

## Core Question

How should niwa's Infisical backend authenticate across multiple
Infisical organizations in a single `niwa apply`, without adding
ceremony for single-org users? The single-org path (`infisical login`
+ `niwa apply`) must remain the zero-config default. Multi-org is an
opt-in upgrade for users who work across team boundaries (e.g.,
tsukumogami org + codespar org + personal org).

## Context

The v0.7.1 Infisical backend shells out to `infisical export` and
relies on the CLI's global session for auth. That session is scoped to
one Infisical organization. When team and personal vault providers live
in different orgs, only one org's projects are reachable — the other
fails with 403.

The user maintains three orgs (Tsukumogami, Codespar future, personal)
and needs a single `niwa apply` to reach all three. The current design
(D-6 resolve-before-merge, D-9 file-local provider scoping) is correct
for the vault resolution model; the gap is purely in how the Infisical
backend authenticates per provider invocation.

## In Scope

- Infisical multi-org auth at the backend level
- Per-provider credential storage (local-only, never committed)
- UX for single-org (zero extra config) vs multi-org (opt-in)
- Fallback behavior: CLI session when no explicit credentials
- Machine identity token lifecycle (TTL, refresh, expiry)

## Out of Scope

- Other vault backends (sops, 1Password, Doppler)
- Changes to the vault.Provider interface
- New TOML schema in team config repos (team configs stay clean)
- Infisical project/org creation automation

## Research Leads

1. **How does the Infisical CLI store and scope sessions, and can
   `--token` override per-command without affecting the stored session?**
   If `--token` fully bypasses the stored session per invocation, the
   backend can use it selectively for multi-org providers while the
   default session handles single-org.

2. **How do other multi-account dev tools (gh CLI, AWS CLI profiles,
   1Password CLI, gcloud) let users work across orgs/accounts without
   re-logging?**
   Look for patterns that are zero-config for single-account and
   gracefully extend to multi-account. AWS named profiles and gh
   multi-host auth are strong candidates.

3. **What is the Infisical universal-auth token lifecycle — TTL,
   refresh, expiry — and can niwa obtain short-lived tokens from
   machine-identity credentials at resolve time?**
   If tokens can be obtained cheaply (single HTTP call, no browser),
   the backend could authenticate per-provider transparently using
   stored credentials.

4. **Where can per-org machine-identity credentials safely live on the
   user's machine — local config file, OS keychain, env vars,
   provider-CLI-managed cache — and what's the threat model for each?**
   The store must be local-only (never committed), user-readable-only,
   and ideally managed by a familiar tool.

5. **What's the minimal change in the Infisical backend
   (`subprocess.go`) to pass `--token` per invocation when credentials
   are available, while falling back to the CLI session when they're
   not?**
   Map the exact code path, identify the injection point, confirm
   the change is small and contained.
