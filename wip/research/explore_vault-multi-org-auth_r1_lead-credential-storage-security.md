# Lead 4: Credential Storage Security

## Options Evaluated

### Option A: Local config file (`~/.config/niwa/provider-auth.toml` at 0o600)
- **Pros**: Simple, portable, niwa-managed, familiar (AWS credentials precedent)
- **Cons**: Plaintext on disk; any same-user process can read
- **Threat model fit**: Yes — same-user processes are explicitly out of scope per PRD
- **Verdict**: Strong fit for long-lived machine-identity credentials

### Option B: OS keychain (macOS Keychain, Linux libsecret)
- **Pros**: Encrypted at rest, OS-managed access control
- **Cons**: Platform-specific, blocks headless CI, needs Go library (violates R20)
- **Threat model fit**: Better than A, but integration cost too high
- **Verdict**: Let the Infisical CLI handle keychain for user auth; niwa shouldn't manage it

### Option C: Environment variables (per-shell, user-set)
- **Pros**: No file on disk, familiar for CI
- **Cons**: Visible to `ps`, doesn't scale to 3+ orgs, user must set per session
- **Threat model fit**: Partially — fragile at scale
- **Verdict**: OK as CI fallback (INFISICAL_TOKEN), not for local multi-org

### Option D: Infisical CLI's own token cache (~/.infisical)
- **Pros**: Transparent after `infisical login`
- **Cons**: Single-org only — exactly the problem we're solving
- **Verdict**: Keep as fallback for single-org default

### Option E: niwa-managed JWT cache (`~/.config/niwa/tokens/` at 0o600)
- **Pros**: Stores SHORT-LIVED JWT (not long-lived client_secret); natural TTL expiry bounds exposure; auto-refreshed by niwa
- **Cons**: Plaintext on disk during TTL window
- **Threat model fit**: Strong — bounded exposure time makes plaintext acceptable
- **Verdict**: Strong fit for cached access tokens

### Option F: Personal overlay repo (private)
- **Pros**: Version-controlled, synced across machines
- **Cons**: Long-lived credential in a git repo; violates the core principle that drove vault integration
- **Verdict**: REJECT — this is exactly what vault integration was designed to eliminate

## Recommended Architecture

Two-layer storage:

1. **Machine-identity credentials** (client_id + client_secret) → `~/.config/niwa/provider-auth.toml` at 0o600
   - Per-provider entry keyed by (kind, project)
   - Familiar AWS-credentials-file pattern
   - Never in git, never in any repo
   - Fallback: `INFISICAL_TOKEN` env var for CI

2. **Short-lived JWT cache** �� `~/.config/niwa/tokens/` at 0o600
   - niwa obtains JWT via Infisical universal-auth endpoint using stored credentials
   - Cached until TTL expires; auto-refreshed on next apply
   - Token passed to `infisical export --token <jwt>` per invocation
   - Natural expiry bounds the exposure window

3. **Fallback for single-org** → Infisical CLI session + `INFISICAL_TOKEN`
   - If no per-provider credentials found, use existing CLI auth
   - Zero-config for single-org users; multi-org is opt-in

## Threat Model Alignment

- **Trusted**: local filesystem at 0o600, Infisical CLI subprocess
- **Not defended**: same-user processes reading files (PRD accepted risk)
- **Defended**: git commits of credentials (never in any repo), world-readable files (0o600)
- **Bounded exposure**: JWT cache is short-lived; credential file is long-lived but local-only
