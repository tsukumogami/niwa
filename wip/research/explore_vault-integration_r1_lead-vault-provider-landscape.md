# Lead: Vault provider landscape and free-tier viability

Scope: survey free-tier vault/secret-manager options for niwa's first integration.
niwa's constraints: 1-5 dev teams; macOS+Linux laptops; OSS CLI; must support
both team-shared and per-developer personal scoping; bootstrap from a GitHub org
membership is a plus.

## Findings

### Provider table

| Provider | Free-tier limits | OSS? | Auth methods | Secret shape | CLI | Niwa-fit (1-5) |
|---|---|---|---|---|---|---|
| HashiCorp Vault OSS (self-host) | Free binary, no vendor cap | BSL 1.1 (converts to MPL 2.0 after 4 years) | Token, userpass, OIDC, GitHub, AppRole | Hierarchical paths + KV v2 | `vault` binary, macOS/Linux | 3 |
| HCP Vault Secrets | **End of Sale** | No (SaaS) | HCP CLI, OIDC | Flat KV per "app" | `hcp` CLI | 1 (EOS) |
| HCP Vault Dedicated | Paid clusters; no usable free tier | No | Same as OSS Vault | Same as OSS Vault | `vault` CLI | 1 |
| Infisical Cloud (free) | 5 identities, 3 projects, 3 envs, 10 integrations | MIT core + proprietary `ee/` dir | Email/pw, OIDC, SAML (paid), GitHub OAuth sign-in, machine identities | Project / env / folder / key | `infisical` CLI, macOS/Linux | 4 |
| Doppler (Developer free) | 3 users, 10 projects, 4 envs, 10 configs/env, 3-day audit retention | Proprietary SaaS | Browser OAuth login (`doppler login`), service tokens, OIDC for CI | Project / config / key | `doppler` CLI, macOS/Linux/Win | 4 |
| Bitwarden Secrets Manager (free) | 2 users, 3 projects, 3 machine accounts, unlimited secrets | AGPLv3 (core); server self-host | Email+2FA (authenticator/email); no SSO on free | Project / key, optional path via naming | `bws` CLI, macOS/Linux | 3 |
| 1Password Secrets Automation / `op` | No permanent free tier; Teams Starter $19.95/mo for 10 users; Individual $2.99/mo | Proprietary | Desktop app biometric integration, service accounts, Connect server | Vault / item / field (hierarchical) | `op` CLI, macOS/Linux/Win | 3 |
| AWS Secrets Manager | $0.40/secret/mo + $0.05/10k API calls; 6-month $200 new-account credit only | No | IAM / SSO; assumes AWS account per dev | Flat name with "/" path | `aws secretsmanager` | 2 |
| Azure Key Vault | Per-operation pricing; no real free tier; usable but costs a few cents per dev per month | No | Entra ID (Azure AD) | Flat name | `az keyvault` | 2 |
| sops + age (+ GitHub) | Free; secrets live in git, encrypted | MPL 2.0 (sops), BSD-3 (age) | Local age key pair (no service auth) | File-based YAML/JSON, multiple recipients | `sops`, `age` binaries | 4 |
| GitHub Actions secrets | Free with repo; **not readable outside Actions** | Part of GitHub | GitHub auth, but write-only from dev perspective | Flat name, scoped org/repo/env | None for read | 1 |
| Pulumi ESC | Individual free: 1 user, 25 secrets, 10k API calls/mo | ESC itself is not OSS (Pulumi core is Apache 2.0) | Pulumi Cloud account, OIDC | "Environment" hierarchies with imports/overrides | `esc` / `pulumi env` | 3 |

### Per-provider detail

#### HashiCorp Vault OSS (self-host)
Free binary, BSL 1.1 license (converts to MPL 2.0 four years after each release).
Supports GitHub auth method natively — a user authenticates with a GitHub PAT
and Vault maps their org/team to policies. Runs as a service, so someone on the
team has to operate it (even a single `dev` server is non-trivial to keep up).
Hierarchical path model is an excellent fit for niwa's `workspace/secret` shape.
**Niwa fit**: CLI is excellent, auth-via-GitHub-org solves bootstrap cleanly, but
the "someone has to run the server" tax is a dealbreaker for indie devs.

#### HCP Vault Secrets
End of Sale per current HashiCorp docs. They're funnelling customers to HCP
Vault Dedicated (enterprise clusters, not free). Disqualified.

#### Infisical Cloud + OSS
Free tier is the most generous of the hosted options: 5 identities, 3 projects,
3 envs, 10 integrations. Core repo is MIT (with an `ee/` dir that's proprietary
but not required for core usage). Self-host is a real option via Docker. The
project/env/folder/key hierarchy maps cleanly to niwa workspaces. CLI (`infisical`)
runs on macOS/Linux, authenticates via browser OAuth or machine identities. Login
via GitHub OAuth exists for web sign-up; CLI uses tokens/universal auth. Biggest
wart on the free tier is the 3-project cap, which is tight if each niwa workspace
wants its own project.

#### Doppler
Developer tier gives 3 users free plus $8/mo/additional user — fine for a 1-5
person team at modest cost. Projects/configs/keys map well to workspaces and
environments. `doppler login` is a clean browser-based OAuth flow. No known OSS
story (proprietary SaaS). CLI is a single binary, trivial install via brew/apt.
3-day audit retention and no org secrets in free tier are the main restrictions.
Mature, widely-used, good DX.

#### Bitwarden Secrets Manager
Free tier is too small: 2 users. A 3-person team already has to pay. AGPLv3 core
is genuinely open and self-hostable. `bws` CLI is solid. Projects + machine
accounts. No SSO on free tier — that's fine for niwa since we were planning GitHub-
based bootstrap anyway, but 2-user cap is the blocker.

#### 1Password (op CLI + Secrets Automation)
No permanent free tier — Teams Starter is $19.95/mo for up to 10 users. The op
CLI and desktop integration are genuinely best-in-class (Touch ID-gated secret
retrieval, zero-friction). Secrets Automation (Connect server / service accounts)
needs a Business plan. Good fit technically, bad fit for "indie developer $0."

#### AWS Secrets Manager
Flat $0.40/secret/mo. A team with 20 secrets pays $8/mo. Six-month $200 credit for
new accounts only. Every dev needs an AWS account and IAM user/role. Too much
setup ceremony for a 1-dev workflow.

#### Azure Key Vault
Similar to AWS but operation-priced. Requires Azure tenancy. Non-starter for
devs not already in Azure.

#### sops + age (+ GitHub)
MPL 2.0 (sops) + BSD-3 (age). Cost: $0. Secrets live encrypted in the repo,
decrypted at runtime with a local age private key. Multiple recipients supported
(every team member's age public key is listed in `.sops.yaml`, and any of them can
decrypt). Key rotation when a member leaves is real work but tractable. Files are
structured (YAML/JSON/ENV), so hierarchical workspace shape is natural. `sops`
and `age` binaries are a single-curl install. **Personal scoping limitation**:
sops encrypts each file for a fixed recipient set. A "personal" secret (the
developer's own tsukumogami PAT) would either need its own encrypted file,
encrypted to just that developer's age key, checked into a personal repo
(`dangazineu/dot-niwa`) — which is fine! — or kept out of sops entirely in an
env var. This actually matches niwa's two-repo model (team `dot-niwa` has shared
secrets; personal `dot-niwa` has per-user secrets).

#### GitHub Actions encrypted secrets
Cannot be read from a dev laptop. Useful for CI but useless for `niwa` running on
a workstation. Disqualified.

#### Pulumi ESC
Individual free tier: 25 secrets, 10k API calls/mo, 1 user. That 1-user limit kills
the team-shared use case on the free plan. Team tier starts at $40/mo. Nice
abstraction (environments import/override) but overpriced for niwa's MVP.

### Scoring (1-5)

| Provider | Cost | Bootstrap UX | Layering fit | OSS fit | Maturity |
|---|---|---|---|---|---|
| Vault OSS self-host | 5 | 1 | 5 | 3 (BSL) | 5 |
| Infisical Cloud | 4 | 4 | 4 | 4 | 4 |
| Doppler | 4 | 5 | 4 | 1 | 5 |
| Bitwarden SM | 3 | 3 | 3 | 5 | 4 |
| 1Password op | 1 | 5 | 4 | 1 | 5 |
| AWS SM | 2 | 2 | 4 | 1 | 5 |
| sops+age | 5 | 4 | 5 | 5 | 5 |
| Pulumi ESC | 2 | 4 | 5 | 2 | 4 |
| HCP Vault Secrets | 1 | — | — | — | 1 (EOS) |
| GitHub Actions | — | — | 1 | 4 | 5 |

## Recommendations

### Finalist 1: sops + age

**Pitch.** Zero cost, zero vendor lock-in, zero service to run. Secrets live
encrypted inside the team's `dot-niwa` repo — so the repo can go public without
leaking anything. Personal secrets live encrypted in `dangazineu/dot-niwa`, using
the developer's own age key. niwa calls `sops -d` at runtime and caches the
plaintext in a tmpfs path. Fits perfectly with niwa's gitops-native model: the
same `git pull` that updates the workspace config also updates the encrypted
secrets. Team onboarding is "add your age public key to `.sops.yaml`, send a PR,
team lead re-encrypts." Works fully offline.

**Biggest risk.** Key rotation when a team member leaves is manual and must be
scripted. Every secret file has to be decrypted and re-encrypted with the reduced
recipient set, and anything the departed member saw should be treated as
compromised. For a 1-5 dev team with low churn, that's tolerable; at 20+ devs
it's a grind. Also, no per-secret access audit — the repo log shows only writes,
not reads, so if you ever need "who fetched this secret, when," sops won't tell
you.

### Finalist 2: Infisical Cloud (with self-host escape hatch)

**Pitch.** The most generous free tier of any hosted option (5 identities,
3 projects, 3 envs). Core codebase is MIT so teams can migrate to self-host
without a rewrite. Project/env/folder model maps cleanly onto niwa's workspace
hierarchy. `infisical run -- cmd` pattern is the same shape niwa wants. And
unlike Doppler, we have a plausible story for users who outgrow the free tier
or want to own their infrastructure: run the Docker image.

**Biggest risk.** The 3-project cap on free tier is tight for niwa's model where
each workspace could plausibly be its own project. If niwa maps every workspace
to an Infisical project, users hit the limit at 3 workspaces and have to either
collapse workspaces onto shared projects (awkward namespacing) or pay. The
mitigation — map workspaces to folders inside a single project — works but is
less clean.

### Finalist 3: Doppler

**Pitch.** The smoothest bootstrap UX of any option here. `doppler login`
opens a browser, done. CLI is a single binary, everywhere. The project/config
shape maps onto niwa workspaces. 3 free users covers the typical indie-team MVP
scenario. Audit logs, rotation, and integrations are mature. If "just make it
work for a small team" is the product goal, Doppler is the path of least
resistance.

**Biggest risk.** Closed-source SaaS with no self-host option. niwa is OSS and
vendor-neutrality is a stated value. Recommending Doppler as niwa's first
integration pushes users toward a single proprietary vendor with no exit. The
$8/mo/additional-user charge kicks in quickly (4-person team = $8/mo, 5-person =
$16/mo), which contradicts the "indie developer free" positioning.

### sops-specific verdict

**sops is the strongest first option.** It scores top on cost, OSS-fit, and
layering, and uniquely solves the "make the `dot-niwa` repo publishable" goal
without introducing a new service dependency. The personal-scoping requirement
is *met*, not failed: personal secrets go in `dangazineu/dot-niwa` encrypted to a
single age key (the developer's own). Team secrets go in `tsukumogami/dot-niwa`
encrypted to the team's age recipient list. The two-repo split niwa already has
maps directly onto sops's recipient model.

The "but sops has no per-user access control on a single file" critique doesn't
apply here because niwa already has per-user and per-team *repos*. Each repo is
its own recipient set. That's the scoping primitive.

What sops does *not* give you is audit logging (who read the secret, when),
managed key rotation, or a UI. If niwa's users are indie devs + small teams,
they don't need those things for an MVP. If/when they do, Infisical or Doppler
become natural upgrade paths.

**Recommended path**: ship sops+age as the default ("no vendor") backend, and
add Infisical as the second backend for teams that want hosted key management
and audit. Treat Doppler as a stretch integration if demand appears.

## Surprises

- HCP Vault Secrets is being discontinued — anyone currently recommending it is
  working off stale information. Worth checking anything in niwa docs/issues
  that references it.
- 1Password has no permanent free tier at all, only a 14-day trial. The op CLI
  is great but strictly a paid product. Many developer blog posts gloss over
  this.
- Bitwarden Secrets Manager free tier (2 users) is smaller than Bitwarden
  Password Manager free tier (unlimited users). These are separate products
  despite the shared brand.
- Pulumi ESC's "Individual free" is 1-user only, so it literally cannot serve a
  team-shared use case without paying — making it unsuitable as niwa's default
  backend even though the environment abstraction is elegant.
- Infisical's free tier identity-count (5) and Bitwarden's (2) are tighter than
  Doppler's (3 + pay-per-user), so for a 4-person team Doppler + $8/mo actually
  beats Infisical's free tier on headroom-vs-cost.

## Open Questions

- **niwa's vault interface**: does niwa want to abstract over "the vault" with a
  pluggable backend (so sops and Infisical can coexist), or pick one and commit?
  That choice shapes which finalist is the default vs. the escape hatch.
- **Offline behavior spec**: what exactly does niwa do when a workspace is
  opened and the vault is unreachable? Fail hard, use last-known plaintext from
  a tmpfs cache, or prompt? Different backends make different trade-offs here
  (sops is trivially offline once the repo is cloned; Doppler/Infisical need
  either network or a local cache layer).
- **Key rotation ergonomics for sops**: is there a pre-built tool niwa can call
  to automate "rotate after member leaves"? `sops updatekeys` exists but needs a
  workflow around it; worth a tactical spike.
- **Personal vs. shared resolution**: niwa's two-repo overlay already composes
  team config + personal config. Does the same overlay apply to secrets, or do
  we need a separate `secrets:` resolution order? Should be spelled out in the
  design doc.
- **Does niwa want to be a vault client at all, or just emit env-vars via `exec`
  wrappers**? The second is simpler and vendor-neutral (niwa runs
  `sops exec-env` or `infisical run`, doesn't parse secrets itself).
- Infisical CLI's GitHub-OAuth-for-login story on the free tier needs
  confirmation — docs were thin on whether web-SSO extends to CLI bootstrap.

## Summary

sops + age is the recommended first integration: it's the only option that
costs $0, needs no hosted service, keeps niwa vendor-neutral, and actually lets
the team's `dot-niwa` repo go public because the secrets inside it are
encrypted. Infisical (MIT-licensed, self-hostable, generous free tier) is the
right second integration for teams that want managed rotation and audit logs.
The biggest open question is whether niwa commits to one backend or builds a
pluggable interface — that choice determines whether Infisical is "the upgrade
path" or "an equal peer."
