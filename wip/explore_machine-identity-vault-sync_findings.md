# Exploration Findings: machine-identity-vault-sync

This file consolidates the research conducted during /shirabe:explore
for handoff to /prd. The exploration ran one round of investigation
across six leads.

## Round 1 Summary

### Lead 1 — Global config + provider-auth flow today

`niwa config set global <slug>` clones the personal-overlay repo to
`~/.config/niwa/global/` and writes the slug to
`~/.config/niwa/config.toml` (mode 0600). At apply time
(`internal/cli/apply.go`), the applier reads the global config dir,
parses `niwa.toml` from the clone via
`config.ParseGlobalConfigOverride()` (`internal/config/config.go:450`)
into a `GlobalConfigOverride` (with `Global` and `Workspaces` fields).

`internal/workspace/apply.go:492` loads
`~/.config/niwa/provider-auth.toml` via `LoadProviderAuth`. The
returned entries are injected at THREE sites:
- `apply.go:592` — workspace overlay vault registry
- `apply.go:742` — team config vault registry
- `apply.go:746` — personal-overlay global vault registry
  (`globalOverride.Global.Vault`)

This means: **the personal-overlay's vault provider is already
authenticatable via provider-auth.toml today.** The "auth gap" the
proposal might appear to fill is already closed for the
personal-overlay layer; the proposal's actual contribution is to
let credentials FOR OTHER ORGS be sourced FROM the personal vault.

### Lead 2 — What the existing PRD/design rejected

`docs/prds/PRD-vault-integration.md` (US-9, lines 396-407) and
`docs/guides/vault-integration.md` (§"Why there's no 'replace the
whole team provider' path") explicitly rejected a personal-overlay
shape that declares `[workspaces.<scope>.vault.providers.team]` to
swap the team's vault provider in bulk. Two reasons:

1. Cross-file provider-name coupling (D-9 file-local scoping)
2. Supply-chain risk: a compromised personal overlay silently
   redirects all team vault calls

This proposal is materially different: it doesn't redirect
`vault://` URIs. The team config still queries the team's vault.
Only the credentials used to authenticate that vault are sourced
from the personal vault instead of from a hand-edited local file.
The bytes returned from `vault://team/X` are unchanged.

**However, R31 (override visibility) survives intact.** The PRD
requires that any override be individually visible in `niwa status`
and diagnostics. If a vault entry silently overrides a local-file
entry for the same `(kind, project)`, the user can't tell which
credential authenticated. This is the binding constraint on the
design.

`niwa vault auth add` is listed in PRD-vault-integration as
deferred — credential bootstrap automation is recognized as a
known future need, not a closed door.

### Lead 3 — Vault provider API surface

`vault.Provider.Resolve(ctx, ref)` is a single-key API. There is
no list/scan/prefix-fetch surface. This means niwa cannot
"enumerate all credentials in a folder"; it must know which keys
to ask for.

`secret.Value` is a flat opaque string, not structured data
(`internal/secret/value.go`). A multi-field credential
(`client_id` + `client_secret` + `api_url`) must either be three
separate keys or one packed string body (JSON or TOML).

The Infisical provider (`internal/vault/infisical/`) supports
`path` (folders), `env`, and `project` config fields. URIs in
named-provider mode are flat (`vault://name/KEY`); in
anonymous-provider mode they support nested paths
(`vault://folder/sub/KEY`).

The implication for schema design: niwa already knows the
`(kind, project)` pairs it needs credentials for (it walks the
resolved vault registries). It can ask the personal vault for
exactly those keys without needing a manifest or list API.

## Accumulated Understanding

The proposal is to add a third layer to credential resolution:

```
 [today]                                    [proposed]
                                            
 vault registry      provider-auth.toml     vault registry
       |                    |                     |
       v                    v                     v
   (kind, project) -> (kind, project) lookup -> match? token
                                                     ^
                                                     |
                                            personal vault entry
                                            (when local miss)
```

Concretely, when niwa needs to authenticate a vault provider with
`(kind="infisical", project="X")`:

1. Look up `(infisical, X)` in `provider-auth.toml`. If found, use it.
2. Otherwise, ask the personal vault for the credential at the
   conventional key derived from `X`. If found, use it.
3. Otherwise, fall back to the CLI session.

Decisions that emerged during exploration:

- **Not the rejected pattern.** Proposal preserves "team config
  controls which vault is queried" and only changes where
  authentication credentials are sourced. R12/D-9 do not block it.
- **In-memory only on every apply.** Aligns with the existing
  no-token-cache invariant. ~100ms per provider per apply.
- **Local file wins on conflict.** Mirrors personal-overlay-vs-team
  precedence (personal wins). Lets users pin individual
  credentials locally without editing the vault.
- **Personal vault must auth via CLI session.** Avoids
  chicken-and-egg. The personal vault provider's credentials
  cannot themselves come from the personal vault.

Open questions for the PRD:

- Conflict policy precedence wording (vault augments local, local
  wins): is there a user-facing flag to invert this for "I want
  vault to override even when local is set" cases?
- Visibility surface design: new `niwa status --audit-auth`
  subcommand vs a column on `--audit-secrets` vs a stderr line
  during apply.
- Schema layout published contract: which exact path inside the
  Infisical project is the convention? (e.g.,
  `/niwa/provider-auth/<project-uuid>` as a single key holding a
  packed TOML body.)
- Multi-provider-in-global-config disambiguation: how does the
  user opt into one of several `[global.vault.providers.*]` as
  the credential source?
- Failure-mode UX: what does niwa say when the personal vault is
  unreachable, when a credential key is missing, when a vault
  entry is malformed?

## Tensions

**Convenience vs auditability.** The whole point of the feature
is to remove a manual step (creating provider-auth.toml on every
machine). But making it invisible removes the user's ability to
notice when their bootstrap credentials have changed. The PRD has
to land somewhere on this spectrum and justify the choice.

**Backward compatibility.** Today's users have `provider-auth.toml`
files. After this lands, "the file" is no longer the source of
truth — it's one layer in a pool. The PRD has to define what
upgrade looks like, what migration (if any) is required, and
whether existing files keep working unchanged.

**Public-repo guardrail interaction.** The personal overlay can
itself be a public repo. The plaintext-secrets guardrail walks
`*.secrets` tables in workspace configs. Does it need to grow a
similar check for "personal overlay declares credential-sync
against a vault provider whose project UUID matches a public
remote"? Probably not (the credentials are stored in the vault,
not in the overlay), but the PRD should explicitly say so.

## Gaps

- No exploration of failure modes (network down, vault returns
  malformed entry, vault returns plaintext that's not actually a
  credential body). The PRD's R-section should enumerate.
- No exploration of whether this should ship as part of the v1
  vault scope or as a v1.1 add-on. Worth a sentence on positioning.
- No exploration of how this interacts with the deferred sops/age
  backend. Likely orthogonal but worth confirming.

## Decision: Crystallize
