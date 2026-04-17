# /design Scope: vault-multi-org-auth

## Problem Statement

niwa's Infisical backend authenticates via the CLI's global session,
which is scoped to one Infisical organization. Users whose vault
providers span multiple orgs (team secrets in org A, personal secrets in
org B) get 403 errors because only one org's session is active. The
single-org case works perfectly; the multi-org case is broken.

## Initial Scope

### In Scope

- Optional per-provider machine-identity auth via a local credential
  file (`~/.config/niwa/provider-auth.toml` at 0o600)
- Infisical backend change: pass `--token <jwt>` per invocation when
  credentials are available for that provider's project
- Fallback to CLI session when no credentials found (zero-config for
  single-org users)
- Optional JWT caching in `~/.config/niwa/tokens/` to avoid re-auth on
  every apply
- Token lifecycle: obtain via universal-auth HTTP POST, cache for TTL,
  auto-refresh on expiry

### Out of Scope

- Bootstrap UX (`niwa vault auth add`) — manual TOML editing is v1
- OS keychain integration (violates R20)
- Credential rotation automation
- Non-Infisical backends (sops+age has no org concept)

## Research Findings Summary

1. `--token` flag bypasses CLI session per-command without mutation
2. Universal-auth tokens: 30-day TTL, single HTTP POST, ~100ms
3. AWS named-profiles is the closest pattern (local file + per-command flag + default fallback)
4. ~20 lines of backend change (ProviderConfig["token"] → `--token` in args)
5. Local credential file at 0o600 fits the PRD's threat model (same-user processes are accepted risk)

## Decisions from Exploration

- **Credential storage**: `~/.config/niwa/provider-auth.toml` at 0o600 (Option A from Lead 4). Personal overlay repo rejected.
- **Token injection**: `--token` flag on `infisical export` per invocation (Lead 1 confirmed this is the designed mechanism)
- **Fallback**: CLI session when no per-provider credentials found (preserves single-org zero-config)
- **Architecture**: credential reading happens outside the Infisical backend (in apply.go or resolver layer), injected via ProviderConfig["token"]
