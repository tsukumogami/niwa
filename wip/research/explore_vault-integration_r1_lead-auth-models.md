# Lead: Auth models — team vault vs personal vault

## Findings

### Per-provider auth table

| Provider | Auth method(s) | GitHub federation? | Credential storage | Bootstrap time (new dev) |
|---|---|---|---|---|
| HashiCorp Vault (self-hosted OSS) | userpass, LDAP, OIDC (incl. GitHub OAuth app), AppRole, `vault login -method=github` with PAT, JWT/OIDC from CI | Yes — native `github` auth method maps GitHub org/team to Vault policies. Dev pastes a PAT with `read:org`. | `~/.vault-token` (plaintext, 0600) by default; tokens are renewable/leased | 15–30 min if a teammate already runs a Vault server and wrote policies; otherwise hours to stand up Vault + configure GitHub auth |
| HCP Vault Secrets (managed) | HCP service principal (client ID + secret), or HCP user SSO for the web UI. CLI uses service principal credentials. | No direct GitHub login for CLI use. HCP org SSO supports SAML but not free-tier GitHub federation for programmatic access. | `~/.config/hcp/` config + service principal secret on disk (plaintext unless user wraps it) | 10–20 min: sign up HCP, create org, create service principal, paste client ID/secret. Shared service principal = shared credential risk. |
| Infisical (cloud or self-hosted OSS) | Email/password + MFA (user), Machine Identities (token / OIDC / AWS IAM / GCP IAM / Kubernetes), Universal Auth (client ID + secret), GitHub OAuth for dashboard login | Partial — GitHub login works for the web dashboard (indie-friendly). CLI historically uses a machine identity or `infisical login` browser flow tied to user email; GitHub-tied programmatic auth is via GitHub Actions OIDC, not laptop OIDC. | OS keyring via `infisical login` (preferred), else env var `INFISICAL_TOKEN` | 5–10 min: `infisical login` opens browser, pick workspace, done. Team admin must pre-invite the user via email. |
| 1Password Secrets Automation | Service Accounts (long-lived token, scoped to vaults) OR Connect Server (self-hosted, uses a credentials file + access tokens). End-user CLI (`op`) also supports signing in with the 1Password desktop app (biometric). | No GitHub federation. Identity is 1Password account membership; SSO (Okta/Azure/Google) exists on Business tier but not GitHub. | Desktop app integration uses OS keychain + biometrics. Service account token is plaintext in env var or `~/.config/op/`. | 5–15 min if the dev already has a 1Password account on the team; otherwise add-seat + invite first. Great UX via biometric unlock. |
| sops + age + GitHub | No network auth. Age key pair on disk; decryption succeeds iff the file's recipients list includes the user's public age key. GitHub is only the transport for ciphertext and public keys. | N/A — there is no "auth service." GitHub org membership gates access to the ciphertext repo; possession of an age private key gates decryption. These are two independent gates. | Age private key at `~/.config/sops/age/keys.txt` (plaintext, 0600), or in OS keyring via helpers, or on a hardware token via `age-plugin-yubikey`/`age-plugin-se`. | 10–15 min: generate age key, PR public key to `dot-niwa`, teammate re-encrypts and merges. No account signup. |
| Doppler | Email/password + MFA (user), Service Tokens (project-scoped read-only), Service Account Tokens (broader), `doppler login` browser OAuth flow. SSO (Google/GitHub/SAML) available but GitHub SSO is on paid Team plan in some eras — verify current pricing. | Partial — GitHub SSO exists on paid tiers for the dashboard; CLI uses `doppler login` browser flow that honors SSO. | `~/.doppler/.doppler.yaml` (plaintext token) or OS keyring if configured | 5–10 min: `doppler login`, pick project+config, CLI writes token. Admin must pre-invite user. |
| GitHub org/repo/environment secrets | GitHub PAT (classic or fine-grained) or `gh auth login` device flow. Identity IS GitHub org membership — no separate account. | Native. This is the only option where GitHub is the identity provider end-to-end. | `gh` stores token in OS keyring (macOS Keychain, libsecret on Linux) via `gh auth login`. | 2–5 min: `gh auth login`, done. But: Actions/env secrets are NOT readable by laptops — they only decrypt inside Actions runners. So this is not a general dev-laptop vault. |

### First-time-setup UX narratives

**HashiCorp Vault (OSS).** New dev receives Vault server URL out-of-band. Runs `vault login -method=github token=<PAT>`, pasting a PAT with `read:org` scope. Vault server verifies GitHub org/team membership via the GitHub API and issues a Vault token written to `~/.vault-token`. Good federation story, but requires someone to have stood up and hardened a Vault server with TLS, unseal keys, and policy config — a multi-hour admin task that indie teams rarely take on. HCP Vault Secrets shortcuts the hosting but loses the GitHub federation.

**HCP Vault Secrets.** Team admin creates an HCP org, creates a service principal per app or per dev, pastes client ID + secret into a shared 1Password entry or into a bootstrap script. New dev gets the credentials, exports them, runs `vlt secrets list`. Bootstrap is fast but the credential model is shared-token by default — rotation is manual and there's no audit trail tying an action to a human.

**Infisical.** New dev runs `niwa login infisical` which shells to `infisical login`. A browser opens, user signs in (email+password, or GitHub OAuth for the dashboard). CLI receives a session token stored in OS keyring. The team admin must have pre-invited the user by email; if not, the user hits a "not a member of this workspace" error and must nudge an admin. Once in, `infisical run --env=dev -- niwa apply` injects secrets into niwa's process. Clean UX for indies who don't mind an email invite dance.

**1Password Secrets Automation.** New dev installs the 1Password desktop app, signs in with the team's account (admin must have added them as a member). Enables CLI integration in settings, which pairs `op` with the desktop app over an OS-level socket. Commands like `op read "op://Team/niwa/GITHUB_PAT"` unlock via Touch ID. For headless use, admin mints a service account token (long-lived, no per-user audit) and pastes it into a shared onboarding doc — this is the "shared credential" risk mode. Desktop+biometric mode is the best laptop UX of any provider surveyed.

**sops + age + GitHub.** New dev runs `age-keygen -o ~/.config/sops/age/keys.txt`, copies the public key, opens a PR to `tsukumogami/dot-niwa` adding their pubkey to `.sops.yaml`'s recipient list. A teammate reviews, merges, then runs `sops updatekeys secrets/*.yaml` which re-encrypts each file to the new recipient set. New dev pulls, and `sops -d secrets/team.env` just works. No account, no SaaS, no credential to rotate on laptop loss (just remove pubkey, re-encrypt). Requires exactly one synchronous re-encryption step by an existing member.

**Doppler.** New dev runs `doppler login`, browser opens, authenticates via SSO if configured (else email+password). CLI stores token in `~/.doppler/`. `doppler setup` picks project+config, persisted in `.doppler.yaml` in the repo. `doppler run -- niwa apply` injects secrets. Admin must pre-invite. Very similar UX to Infisical; cleaner for non-dev secrets (has strong UI) but free-tier has ceilings on project/environment count.

**GitHub org/repo secrets.** New dev runs `gh auth login`, picks device flow, done. But the secrets themselves cannot be read from a laptop — `gh secret list` shows names only, and `gh secret get` does not exist. Only Actions runners decrypt them. So this only works as a vault for niwa if niwa-driven workflows run inside Actions, which breaks the "local dev laptop" model. Usable for CI-only secrets, not as the primary laptop vault.

### Graceful degradation

| Provider | Unreachable behavior | Expired-auth behavior | Cache | niwa integration notes |
|---|---|---|---|---|
| HashiCorp Vault | Hard fail (no network = no token renewal, no read) unless niwa keeps an in-memory lease-based cache. Vault tokens are leased so stale caches are safe for TTL window. | Token expires → 403 → niwa must prompt re-auth or fail | Vault Agent provides file-based caching; raw CLI does not | niwa could shell to Vault Agent to get offline grace |
| HCP Vault Secrets | Hard fail on network | Service principal creds don't expire (manual rotation) | None built-in | niwa would need its own cache layer |
| Infisical | CLI has a local cache flag (`infisical run --cache`); otherwise hard fail | Session token refresh on next login; machine identity tokens are long-lived | `--cache` opt-in | Good: niwa can rely on `--cache` for offline |
| 1Password | Desktop app keeps vault cached locally and decrypted-on-demand with biometrics; works offline for already-synced items. Service accounts: hard fail offline. | Biometric re-prompt on timeout; service account tokens don't expire | Desktop app = full local cache; service account = none | Best offline story of the SaaS options |
| sops + age | Works fully offline — decryption is local. Only fails if the repo is stale. | Never expires; key rotation is a team coordination event, not a per-session event. | Inherent (ciphertext is in the repo) | Best-in-class degradation |
| Doppler | `doppler run --fallback=<file>` persists last-known-good to disk; otherwise hard fail | Token refresh on next `doppler login` | Opt-in fallback file | Decent, requires explicit flag |
| GitHub secrets | N/A (only runs in Actions) | N/A | N/A | Not applicable for laptop |

**Recommended niwa policy** (provider-independent): on vault resolution failure, `niwa apply` should fail closed by default with a clear error identifying which ref failed and a suggested remediation command (`niwa vault login <provider>`). A `--allow-unresolved` flag may let users proceed with empty strings for non-critical refs (e.g., telemetry tokens) but must never be the default. A short-lived in-memory cache (single `niwa` invocation) is safe; on-disk cache must be opt-in and encrypted.

### Decision matrix

Scores: 1 = worst, 5 = best for indie/small-team laptop use.

| Provider | Bootstrap (min) | Graceful-degradation | GitHub federation | Ongoing maintenance |
|---|---|---|---|---|
| HashiCorp Vault OSS | 60+ / score 1 | 3 (lease cache possible) | 5 (native GitHub auth) | 1 (server to run, unseal, patch) |
| HCP Vault Secrets | 15 / score 3 | 2 | 1 | 3 (paid tier ceilings, service-principal rotation manual) |
| Infisical | 8 / score 4 | 4 (`--cache`) | 2 (dashboard only) | 3 (account + invites) |
| 1Password | 10 / score 4 | 5 (desktop app offline) | 1 | 4 (paid seats, but the team likely already owns it) |
| sops + age + GitHub | 12 / score 4 | 5 (offline native) | 3 (org gates repo, not decryption) | 4 (re-encrypt on team change) |
| Doppler | 8 / score 4 | 3 (`--fallback`) | 2 (paid tier SSO) | 3 (free-tier ceilings) |
| GitHub secrets | 3 / score 5 | 1 (unusable on laptop) | 5 | 5 |

**Ranking for bootstrap UX** (new dev with only GitHub org access, nothing else): 1. sops+age+GitHub, 2. 1Password (if team already uses it), 3. Infisical, 4. Doppler, 5. HCP Vault Secrets, 6. HashiCorp Vault OSS. GitHub secrets disqualified for non-CI laptop use.

### sops-specific verdict

**Viable as the primary auth model for the team-shared vault.** Strengths: zero external service, zero credential rotation at the service layer, works offline, reviewable-in-git audit trail of who gained/lost access (because membership is a PR to `.sops.yaml`), cost is $0, and "log in with GitHub" is implicit because cloning the ciphertext repo already requires GitHub org membership. The key-per-user model is exactly the property niwa wants — there is no shared team token to leak.

**Frictions to mitigate:**

1. **Re-encryption on membership change.** Every add/remove requires a teammate to run `sops updatekeys` and commit. niwa can ship a `niwa vault rekey` subcommand that wraps this, and `dot-niwa` can include a GitHub Action that runs `sops updatekeys` automatically when `.sops.yaml` changes.
2. **Key loss recovery.** If a dev loses their age key (laptop wiped, no backup), they cannot decrypt — the fix is identical to key rotation: generate new key, PR pubkey, teammate re-encrypts. niwa should document backup to a password manager as the recommended practice, and optionally support `age-plugin-yubikey` so the private key lives on a hardware token.
3. **No per-user audit.** sops cannot tell you who decrypted which secret when. For indie teams this is acceptable; for regulated environments it isn't. niwa docs should call this out so teams self-select.
4. **Personal vault layering.** sops-per-user doesn't naturally express "personal" secrets since the mechanism is already per-user. Personal-vault layer can simply be a second sops file encrypted to only the user's own key, stored in their personal `dot-niwa`, with niwa's existing `GlobalOverride` chain merging them. No new auth model needed.
5. **Binary dependency.** `sops` and `age` must be installed. Both are single static Go/Rust binaries; niwa can install them via tsuku as a prerequisite or vendor them. Not a blocker.

**Recommendation:** sops+age is the strongest default for niwa v1 auth model, with Infisical as the opt-in SaaS alternative for teams that want a web UI and don't want to coordinate re-encryption. 1Password is a strong option specifically for teams that already pay for 1Password.

## Implications

- **niwa should not ship with a service-token-paste flow as the default.** Every SaaS provider surveyed has a "paste a shared token" mode, and every one of those modes is a security footgun for indie teams. Default to user-identity flows (`login <provider>` browser OAuth) or sops-per-user.
- **A `niwa vault login <provider>` subcommand is the right abstraction.** Each provider plugin implements `login`, `get`, and `status` verbs. This lets niwa present a uniform UX regardless of backend.
- **Fail-closed with helpful remediation is non-negotiable.** Warn-and-continue with empty strings silently breaks apps (imagine `GITHUB_TOKEN=""` when the intent is "repo-scoped PAT"). The error must name the failing ref and the command to fix it.
- **Cache policy should be single-invocation in-memory by default.** Persistent on-disk caches duplicate the secrets on disk in a second location, doubling the blast radius of a stolen laptop. Offer opt-in disk cache only for offline-first workflows.
- **Team-vs-personal layering is orthogonal to auth provider.** The `GlobalOverride` chain already exists; personal vault is "same provider, different config repo" for every backend except sops (where it's "second encrypted file, only the user's key"). This simplifies the PRD.

## Surprises

- **GitHub org/repo secrets cannot be read from a laptop.** The mental model "use GitHub as the vault" does not work for local dev. Only Actions decrypts them. This rules out a tempting zero-dependency option.
- **HashiCorp Vault has native GitHub auth.** `vault login -method=github` maps org/team → Vault policy. If a team is willing to run a Vault server, the federation story is actually excellent — but the server-ops burden kills it for indies.
- **1Password's desktop-app + biometric flow is the best UX of any provider** — better than anything OSS — but requires the team to already be paying for 1Password and has zero GitHub federation.
- **Infisical's GitHub login only works for the dashboard, not CLI.** Many indies will assume "GitHub OAuth" means end-to-end federation; it doesn't. CLI uses session tokens from email-based login.
- **Doppler's free tier has tightened over time.** Worth re-verifying current free-tier limits before committing; what was free two years ago may now require a paid plan.

## Open Questions

- Should niwa ship sops+age support as a first-party backend, or recommend it via docs and let users wire `sops exec-env` manually? First-party support is more work but removes the biggest friction (re-encryption UX).
- For the SaaS backends, should niwa orchestrate `login` itself (embedding OAuth flows) or shell out to the provider's CLI? Shelling out is simpler and stays current with provider changes but adds an install dependency.
- What's the right `niwa apply` behavior when some refs resolve and others fail? All-or-nothing is safest; partial-apply-with-warnings is more ergonomic. Probably needs a per-ref `required = true|false` flag in config.
- Does niwa need to support hardware-token-backed age keys (YubiKey, Secure Enclave) in v1, or is disk-stored sufficient for the 1-5 dev target? Disk-stored with 0600 perms matches the threat model for indies; hardware tokens are a v2 polish.
- How should niwa handle the "I just joined, teammate hasn't re-encrypted yet" race on sops? A clear error message pointing to the PR status seems sufficient, but worth prototyping.
- Is there value in a "hybrid" default: sops for team vault, OS keychain for personal vault? This avoids even a second sops file for personal secrets and leans on existing per-user keychain infrastructure.

## Summary

The finalists split cleanly into three auth archetypes: SaaS with shared service tokens (HCP Vault, Doppler, Infisical machine-identity mode) which are fast to bootstrap but risky for indie teams; SaaS with user-identity sessions (Infisical browser login, 1Password desktop, Doppler browser login) which are the right default for SaaS but require account provisioning; and local crypto (sops+age) which has no service, no shared token, no recurring cost, and the best offline degradation — at the price of a synchronous re-encryption step when membership changes. For niwa's indie/small-team target and the "under 10 minutes" bootstrap goal with only GitHub org access assumed, sops+age+GitHub is the strongest default, with Infisical and 1Password as the recommended SaaS alternates and HashiCorp Vault OSS explicitly not recommended despite having the best GitHub federation story.
