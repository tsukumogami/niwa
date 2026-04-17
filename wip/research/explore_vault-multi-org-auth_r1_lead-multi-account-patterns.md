# Lead 2: Multi-Account Patterns Across Dev Tools

## Tools Surveyed

### AWS CLI Named Profiles
- **Single-account**: `aws configure`, default profile, zero config
- **Multi-account**: Named profiles via `aws configure --profile NAME`
- **Per-command selection**: `--profile` flag or `AWS_PROFILE` env var
- **Credential storage**: `~/.aws/credentials` (plaintext, 0o600)
- **Token lifecycle**: Long-lived access keys, or short-lived via STS cached in `~/.aws/cli/cache`

### GitHub CLI Multi-Host Auth
- **Single-account**: `gh auth login` (one host initially)
- **Multi-account**: `gh auth login --hostname <host>` adds hosts additively
- **Per-command selection**: Implicit per-host, or `--hostname` flag
- **Credential storage**: System keychain with file fallback; per-host tokens in `~/.config/gh/hosts.yml`
- **Token lifecycle**: PATs or device flow tokens, manually refreshed

### gcloud Config Configurations
- **Single-account**: Default config, `gcloud auth login` once
- **Multi-account**: `gcloud config configurations create <name>` + set account
- **Per-command selection**: `--configuration=<name>` flag or `gcloud config configurations activate`
- **Credential storage**: `~/.config/gcloud/` with named config dirs
- **Token lifecycle**: Short-lived, auto-refreshed via refresh tokens

### 1Password CLI
- **Single-account**: `op signin <account>` once
- **Multi-account**: `op signin <other>` additive; `op --account` per invocation
- **Per-command selection**: `--account` flag or `OP_ACCOUNT` env var
- **Credential storage**: In-memory tokens or `OP_SERVICE_ACCOUNT_TOKEN` env var
- **Token lifecycle**: Service account tokens (long-lived) → short-lived session tokens

### kubectl Contexts
- **Single-cluster**: Cloud-provider CLI auto-registers context
- **Multi-cluster**: Manual or generated contexts in `~/.kube/config`
- **Per-command selection**: `kubectl --context <name>` or `KUBECONFIG` env var
- **Credential storage**: Unified `~/.kube/config`
- **Token lifecycle**: Provider-specific (OIDC auto-refresh, STS temp creds, etc.)

## Top 3 Patterns for niwa

### Pattern A: AWS Named Profiles + Flag Selection
Store per-org machine-identity credentials in `~/.config/niwa/credentials.toml`.
Default org uses CLI session. Other orgs get `--token` per invocation.
Single-org stays zero-config; multi-org is one-time credential file creation.

### Pattern B: gh CLI Additive Multi-Auth
Keep the `infisical login` session as the default. niwa-specific
credential store for additional orgs. Backend passes `--token` only when
a non-default org is needed. Supports both session fallback and explicit tokens.

### Pattern C: gcloud Named Configs
Named "org profiles" with machine-identity credentials per profile.
Less ceremony than gcloud. One active org per terminal, switchable.

## Recommended Direction

**Combine A + B**: Default to `infisical login` session (zero-config).
For multi-org, store machine-identity credentials in a local-only file.
Pass `--token` per provider invocation when that provider needs a
different org. This is minimal, additive, and leverages existing
Infisical CLI behavior.
