# Research: Onboarding Runbook Ground Truth (for PRD-niwa-onboard)

Sources read in full:
- `docs/guides/machine-identity-vault-sync.md`
- `docs/guides/vault-integration.md`
- `docs/guides/workspace-config-sources.md`
- `docs/guides/init-bootstrap.md`
- `docs/briefs/BRIEF-niwa-onboard.md`
- `docs/designs/current/DESIGN-machine-identity-vault-sync.md`
- `docs/designs/current/DESIGN-vault-multi-org-auth.md`
- `docs/guides/functional-testing.md` (grep only, for overlay fixture naming)

No other doc under `docs/` mentions "onboard" as a feature name; the grep for
onboard/client_secret/provider-auth/overlay/credential surfaced only the files
already in scope plus unrelated hits (worktree/env-provisioning docs, remote-
control docs) that don't bear on vault onboarding.

---

## 1. Ground-truth ordered step list

### TEAM phase

**Nowhere is this phase documented as an admin runbook.** No guide describes a
team admin creating a *team-shared* machine identity, granting it read on a
target environment, or laying down vault folder structure on behalf of a team.
The closest analog is `vault-integration.md` "Walkthrough: two-org setup" §1-6
(lines 306-364), but that is written for an individual developer setting up
**their own personal-org** identity for multi-org auth, not a team admin
provisioning team-wide access. See Gap 1 below — this is a real gap the PRD
must cover, not a step list I can extract from existing docs.

What *is* documented, that a team-phase implementation would need to produce
or assume:

1. A workspace declares its team vault provider in the team config
   (`workspace.toml`'s top-level `[vault.provider]`, or `[env.secrets]` refs
   using it) — `vault-integration.md` §"Declare the provider in
   `workspace.toml`" (lines 44-52):
   ```toml
   [vault.provider]
   kind    = "infisical"
   project = "<your-infisical-project-id>"
   ```
2. Team config values live under `[env.secrets]` (never `[env.vars]`) once
   they're vault-refs — `vault-integration.md` §"`[env.vars]` vs
   `[env.secrets]`" (lines 122-153).
3. Optionally, a `[vault].team_only` allow-list of keys personal overlays may
   never shadow — `vault-integration.md` §"`[vault].team_only`" (lines
   200-213).
4. For multi-source workspaces, `[workspace].vault_scope` must name which
   `[workspaces.<scope>]` block in the personal overlay applies —
   `vault-integration.md` §"`[workspace].vault_scope`" (lines 182-198).

None of this documents the *admin-side provisioning* (create identity →
attach Universal Auth → grant read on environment → create folder path
`/niwa/provider-auth/infisical`) that the BRIEF's team-phase journey
describes. That whole sequence is undocumented ground truth today — it's
tribal/runbook knowledge, per the BRIEF's own problem statement ("today it
lives as hand-run shell in runbooks").

### INDIVIDUAL phase

This phase IS documented, split across two guides depending on which
sub-flow: (A) the credential-sync bootstrap flow (personal vault feeds
machine-identity creds to *other* vault providers), and (B) the multi-org
`provider-auth.toml` file flow (local file, not vault-backed). The BRIEF's
individual-phase journey maps to flow (A). Ordered steps for (A), from
`docs/guides/machine-identity-vault-sync.md` §"Fresh-laptop bootstrap"
(lines 134-157) plus the schema sections above it:

1. **Authenticate against your home/personal org once**: `infisical login`
   (line 140).
2. **Register the personal overlay**: `niwa config set global
   https://github.com/<you>/niwa-overlay` (line 143). This is a **local
   machine** edit — it writes `~/.config/niwa/config.toml`'s `[global_config]`
   pointer (per `workspace-config-sources.md` CLI reference, "niwa config set
   global \<slug\>" and "personal-overlay source"), NOT the workspace's
   upstream source repo.
3. **Declare the personal-overlay vault provider** in the overlay's
   `niwa.toml`, `[global.vault.provider]` block — `machine-identity-vault-
   sync.md` §"Enabling credential sync" (lines 46-67):
   ```toml
   [global.vault.provider]
   kind = "infisical"
   project = "your-personal-org-uuid"
   ```
   This single anonymous declaration is BOTH the `vault://` URI-resolution
   provider AND (per the design doc's Decision 4/Alt 2) the implicit opt-in
   for credential-sync — no separate opt-in block. This edit lands in the
   **personal-overlay repo** (`<you>/niwa-overlay`'s `niwa.toml`), which is
   a repo on the operator's own GitHub, not the team's `dot-niwa`/workspace
   source repo.
4. **Mint a machine-identity client_id/client_secret** for the OTHER org
   (the org whose `provider-auth.toml` entry this replaces) — this is the
   Infisical-dashboard/CLI action; not spelled out step-by-step in
   `machine-identity-vault-sync.md` (it assumes the identity already exists
   from the "two-org setup" walkthrough in `vault-integration.md` §1-3,
   lines 313-329: create machine identity → enable Universal Auth → create
   client secret).
5. **Compute the vault path/key** — exact-shape constants, from
   `machine-identity-vault-sync.md` §"Vault key schema" (lines 69-89):
   ```
   Path: /niwa/provider-auth/infisical
   Key:  p-<project-uuid>
   ```
   `<project-uuid>` = the Infisical project ID of the OTHER org (team's
   vault org), NOT the personal vault's own project. The `p-` prefix on the
   KEY (not the path) is mandatory — Infisical rejects secret keys whose
   first character is a digit (~37.5% of UUIDv4s), and niwa always prepends
   `p-` before fetching.
6. **Build the TOML credential body** — exact required shape,
   `machine-identity-vault-sync.md` §"Vault key schema" (lines 91-98):
   ```toml
   version = "1"
   client_id = "<your-machine-identity-client-id>"
   client_secret = "<your-machine-identity-client-secret>"
   api_url = "https://app.infisical.com"   # optional; omit for default
   ```
   `version` gates schema; a body with no `version` field is treated as `"1"`
   for backward compatibility, but niwa v1 accepts ONLY `"1"`.
7. **Store the body into the personal vault** — `machine-identity-vault-
   sync.md` §"Populating the vault" (lines 104-132):
   ```sh
   infisical login   # switch active session to personal org
   infisical secrets set \
     --path "/niwa/provider-auth/infisical" \
     "p-<project-uuid>=$(cat cred.toml)"
   ```
   This is a **local-machine CLI action against the remote vault service**
   — it writes to the vault provider (Infisical's hosted/self-hosted API),
   not to any git repo, local file, or upstream source repo.
8. **Run apply** — `niwa apply` (step 3 of the bootstrap chain, line 147).
   niwa fetches each per-org machine identity from the personal vault as
   apply touches the provider that needs it (lazy, per-`(kind, project)`).
9. **Verify** — `niwa status --audit-auth` (see §3 below).

Constants recap for the individual phase (all exact-shape, all called out
in the BRIEF as failure-prone if hand-typed):
- Local overlay pointer file: `~/.config/niwa/config.toml` (`[global_config]`
  block; registered via `niwa config set global <slug>`).
- Personal-overlay config file: `niwa.toml` at the overlay repo's root
  (see §2 — this is NOT `workspace-overlay.toml`; that name only appears as
  a test-fixture convention in `functional-testing.md:69`, unrelated to the
  real file convention).
- `[global.vault.provider]` — anonymous block, both roles (URI resolution +
  credential-sync source). `[global.vault.providers.<name>]` (named) does
  URI resolution only, never credential-sync.
- Vault path: `/niwa/provider-auth/infisical` (kind-suffixed; today only
  `infisical` kind exists).
- Vault key: `p-<project-uuid>` — the `p-` prefix is mandatory and applies
  to the key, not the path.
- Body fields: `version` (required by parser, defaults to `"1"` if absent),
  `client_id` (required), `client_secret` (required), `api_url` (optional).

There's also a DIFFERENT individual-phase file, the local override layer,
which the wizard likely does NOT touch (it's the pre-existing multi-org
mechanism credential-sync layers on top of): `~/.config/niwa/provider-
auth.toml`, `0o600`, `[[providers]]` array with `kind`, `project`,
`client_id`, `client_secret`, `api_url` — `vault-integration.md` §"Credential
file format" (lines 366-407). This file is per-machine and NEVER vault-
backed; local file always wins over vault when both have an entry for the
same `(kind, project)`.

---

## 2. Personal-overlay setup steps

**File and location**: The personal overlay is a separate config repo,
registered via `niwa config set global <org/repo>` (or by hand-editing
`~/.config/niwa/config.toml`'s `[global_config]`/pointer field). Its config
file is **`niwa.toml` at the overlay repo's root** — confirmed explicitly in
`workspace-config-sources.md`'s note (lines 606-615):

> The personal global config overlay keeps its existing file convention
> (`niwa.toml` at repo root with `[global]` and `[global.vault.*]`
> sections); niwa clones the entire repo verbatim and reads the file.

This is unaffected by the rank-1/rank-2/rank-3 workspace-source discovery
model described earlier in the same doc — that discovery model applies only
to **workspace** config sources, not the personal global overlay. (This is a
point of real ambiguity risk for a wizard implementer: two different "config
discovery" systems exist in this codebase, and only one applies to personal
overlays.)

**(a) Personal-vault provider block** — required for credential-sync
activation and for `vault://` refs the operator writes into their own
overlay:
```toml
[global.vault.provider]
kind = "infisical"
project = "your-personal-org-uuid"
```
Source: `machine-identity-vault-sync.md` lines 48-56. This MUST be the
anonymous singular form (`[global.vault.provider]`, no name) to serve as the
credential-sync source; a named form
(`[global.vault.providers.<name>]`) participates in `vault://` resolution
only. Anonymous vs. named is mutually exclusive within one file — mixing is
a parse error (`vault-integration.md` lines 90-120, general vault-provider
schema rule that also governs this block).

**(b) Per-workspace personal secrets** — `vault-integration.md` §"Personal
overlay flow" (lines 214-248):
```toml
[global.vault.provider]
kind    = "infisical"
project = "dangazineu-personal"

[workspaces.tsukumogami.env.secrets]
GITHUB_TOKEN = "vault://tsukumogami/github-pat"

[workspaces.codespar.env.secrets]
GITHUB_TOKEN = "vault://codespar/github-pat"
```
Each `[workspaces.<name>.env.secrets]` (or `.env.vars`) table scopes personal
overrides to one workspace by name; the value is a `vault://` URI resolved
against the SAME `[global.vault.provider]` (or one of its named siblings)
declared in the same overlay file. For single-source workspaces the scope is
implicit; multi-source workspaces need `vault_scope` set in the TEAM config
(not the overlay) per §1 point 4 above.

**Same-login vs. split-login topology** — the docs do not use the terms
"same-login"/"split-login" verbatim (those are BRIEF-coined names), but they
describe the underlying mechanics:

- **Same account** (no login switch needed): "If all your vault providers
  point at projects in the same org, `infisical login` is enough and you can
  skip this section entirely." (`vault-integration.md` lines 279-281). No
  `provider-auth.toml` entry, no credential-sync vault entry — the CLI
  session covers everything.
- **Split accounts** (login switch / multi-org auth needed): triggered
  "when your workspace references Infisical projects across different
  organizations" (line 283). Two DIFFERENT mechanisms exist for this and the
  docs don't unify them into one topology decision for the operator:
  1. **Local-file mechanism** (`provider-auth.toml`, lines 291-407):
     operator manually authenticates a *second* machine identity's
     `client_id`/`client_secret` into a flat local TOML file, matched by
     `(kind, project)`. No vault involved; per-machine, must be repeated on
     every laptop.
  2. **Credential-sync mechanism** (`machine-identity-vault-sync.md`,
     entire doc): the SAME kind of credential ends up centralized in the
     operator's personal vault instead of a local file, so every machine
     picks it up on `niwa apply` without re-typing. This is the "vault
     entry" layer in the precedence table (line 30-41): local file wins
     over vault, vault wins over CLI session.

  The BRIEF's "split-login" journey (workspace vault in a dedicated org,
  overlay vault in the operator's personal account) maps onto scenario (2):
  the "login switch in the middle" is literally `infisical login` switching
  the active CLI session from the team org to the personal org so
  `infisical secrets set` (step 7 in §1) writes to the right project. The
  docs describe this switch only inside the `infisical secrets set`
  walkthrough's inline comment ("Switch the active session to your personal
  org," line 111) — it is not framed as a named topology decision anywhere.
  **This framing (same-login vs. split-login as an explicit up-front
  choice) does not exist in the docs; it's new PRD scope.**

---

## 3. Verification: `niwa status --audit-auth`

Fully documented in `machine-identity-vault-sync.md` §"The `niwa status
--audit-auth` command" (lines 159-198) and §"Apply-time stderr signal"
(lines 200-223).

- **Command**: `niwa status --audit-auth`. **Fully offline** — no vault
  calls, no network. Reads `state.json` from the most recent apply (per the
  design doc, the `auth_sources` map added in state schema v4).
- **Output shape** (table, one row per `(kind, project)` pair needed by the
  last apply):
  ```
  KIND       PROJECT-UUID                          SOURCE                     FALLBACK
  infisical  550e8400-e29b-41d4-a716-446655440000  local-file                 vault:personal-overlay
  infisical  660f9511-f40c-52e5-b827-557766551111  vault:personal-overlay     —
  infisical  770a0622-a51d-63f6-c938-668877662222  cli-session                —
  ```
- **SOURCE values**: `local-file`, `vault:personal-overlay` (or
  `vault:personal-overlay(name)` if multiple credential-sync providers ever
  exist), `cli-session`, `none`.
- **Healthy state**: every row has a non-`none` SOURCE. Exit code `0`.
- **Broken state**: at least one row has SOURCE = `none` (no source produced
  a usable credential for that pair). Exit code non-zero. This is exactly
  the "developer confirms onboarding actually landed" journey in the BRIEF —
  re-running the check should point at which `(kind, project)` pair is
  unresolved.
- **FALLBACK column**: populated only when local file AND vault both had an
  entry and local won; shows the vault entry that lost. Empty (`—`)
  otherwise.
- **Apply-time signal** (separate from the audit command, fires during
  `niwa apply` itself): one stderr line per vault-sourced pair, e.g.
  `auth: infisical/550e8400-... source=vault:personal-overlay`, plus a
  `fallback=` variant when local overrides vault. These lines fire BEFORE
  backend authentication, so even a subsequently-failing apply still
  discloses where credentials came from.
- **Common failure diagnostics** the wizard's verification step should be
  able to recognize/reproduce (§"Common errors", lines 241-292):
  - chicken-and-egg cycle (personal vault can't bootstrap itself)
  - malformed TOML body
  - missing `client_id`/`client_secret` field
  - unsupported schema `version`
  - personal vault unreachable (network/auth/CLI-missing) — falls back to
    local-file + cli-session, warns once per provider per apply.

There is a related but DIFFERENT command, `niwa status --check-vault`
(`vault-integration.md` line 550): "Re-resolve every `vault://` reference
against the configured providers and compare fingerprints to stored state.
Does NOT materialize." This is a live (non-offline) check of secret
*resolution*, distinct from `--audit-auth`'s offline check of credential
*sourcing*. The BRIEF's "confirms it resolves before declaring success"
language plausibly wants both: `--audit-auth` proves the credential-sync
layer produced a usable source, `--check-vault`-style live resolution proves
the actual secret value comes back. Neither guide states that `--audit-auth`
alone proves end-to-end resolution — it only proves a SOURCE was found, not
that the vault provider is reachable and returns a valid value right now
(that's what makes it "fully offline"). **This is a real gap**: the BRIEF's
promised "confirms it resolves before declaring success" is closer to
`--check-vault` semantics or to the live validation from the folded-in
niwa#199 spike than to `--audit-auth`'s offline-only guarantee. The PRD needs
to be explicit about which check(s) the wizard's final verification step
actually runs.

---

## 4. Gaps: documented constraints the BRIEF doesn't mention

1. **No documented team-admin runbook exists at all.** The BRIEF's team-
   phase journey (create machine identity → attach Universal Auth → grant
   read access on target environment → create folder structure) has no
   corresponding guide. `vault-integration.md`'s only "create machine
   identity" walkthrough (lines 306-364) is framed as an individual
   developer provisioning their OWN personal-org identity for multi-org
   auth — not a team admin provisioning shared team infrastructure. The PRD
   must either derive the team-phase step order from first principles
   (Infisical's admin API/dashboard flow) or treat it as genuinely new
   ground, not extracted-from-docs ground truth. This is the single
   biggest gap.

2. **Two distinct "multi-org" mechanisms exist and the docs never ask the
   user to choose between them as a topology decision.** `provider-auth.toml`
   (local-file, per-machine, manual) and vault-backed credential-sync
   (`machine-identity-vault-sync.md`, centralized) both solve "I need a
   credential for an org I'm not logged into," and a user could plausibly
   set up either — or both, with local-file always winning. The BRIEF wants
   the wizard to present ONE topology choice (same-login vs. split-login);
   the docs don't currently frame this as a single decision because they
   present two overlapping mechanisms rather than one flow with a fork. The
   PRD needs to decide (and state) that the wizard specifically drives the
   credential-sync (vault-backed) mechanism, not the older local-file
   mechanism, and needs to justify why (durability across machines, per the
   BRIEF's outcome language).

3. **The chicken-and-egg self-bootstrap constraint (R9)** is a hard
   constraint the docs describe in detail (`machine-identity-vault-sync.md`
   lines 243-253; `DESIGN-machine-identity-vault-sync.md` Decision 5, two-
   stage validation) but the BRIEF never mentions it. If the wizard is
   choosing project UUIDs and org logins on the operator's behalf, it must
   independently avoid ever wiring the personal vault to authenticate
   itself — this is a real implementation constraint the PRD needs a
   requirement for, likely "the wizard MUST detect and refuse a self-
   referential (kind, project) before writing anything."

4. **The overlay repo doesn't exist yet is unaddressed.** Every guide
   assumes `niwa config set global <org/repo>` points at an ALREADY-EXISTING
   overlay repo/`niwa.toml`. Nothing in `machine-identity-vault-sync.md`,
   `vault-integration.md`, or `workspace-config-sources.md` describes what
   happens (or what the wizard should do) when the operator has no personal
   overlay repo at all yet — vs. `init-bootstrap.md`'s `--bootstrap` flag,
   which handles the analogous "no config yet" case for WORKSPACE sources
   specifically, not personal overlays. The BRIEF's individual-phase journey
   ("developer joins a team... sets up their personal overlay") implies the
   wizard creates this repo/file from scratch when absent, which has no
   documented precedent to model against except the workspace-side
   bootstrap flow (which is GitHub-only, requires an empty-or-missing-marker
   repo, and produces a local commit the user pushes themselves — see
   `init-bootstrap.md` lines 44-64, 169-190). The PRD should decide whether
   the wizard reuses/generalizes this same bootstrap machinery for the
   overlay repo case.

5. **`env_output`, `team_only`, `vault_scope`, and multi-source workspaces**
   are all real config surfaces (`vault-integration.md` lines 182-213;
   `workspace-config-sources.md` `env_output` section) that interact with
   whether a personal-overlay secret actually takes effect, but the BRIEF's
   scope boundary doesn't mention any of them. If a workspace is
   multi-source and lacks `vault_scope`, `niwa apply` hard-fails
   independently of anything the wizard does correctly — the wizard's
   verification step should probably surface this as a distinct failure
   mode rather than let the operator interpret it as a wizard bug.

6. **Verification ambiguity** — see §3 above: `--audit-auth` (offline,
   source-only) vs. `--check-vault` (live, resolves and fingerprints) are
   different guarantees and the BRIEF's "confirms it resolves" language
   doesn't disambiguate which one (or both) the wizard's success gate uses.

7. **Plan-gated admin steps have no enumerated list.** The BRIEF's "team
   admin hits a step their plan won't allow" journey wants the wizard to
   "recognize the gated step" and give exact dashboard instructions — but no
   doc enumerates which Infisical admin operations are plan-gated (free vs.
   paid tier) or what the exact dashboard click-path is for each. This has
   to come from Infisical's own product docs/API error responses, not this
   repo's guides — worth flagging so the PRD doesn't assume this list
   already exists internally.

8. **No mint-and-store precedent in-repo.** The BRIEF says niwa#194 ("mint-
   and-store a credential on an existing identity") and niwa#199 ("a doctor
   that validates the credential contract live") are prior closed draft PRs
   being folded in, but neither an issue body, design doc, nor guide for
   either exists under `docs/` in this checkout — grep for `#194`/`#199`
   only hits the BRIEF itself. The PRD can't inherit implementation detail
   from those efforts via this repo's docs; whatever ground truth they
   contain (if still needed) has to come from the actual GitHub issues/PRs,
   not from `docs/`.

---

## 5. Upstream source repo vs. local machine — which side each edit lands on

| Step | Where it writes |
|---|---|
| `infisical login` (any org) | Local machine only (Infisical CLI's own session cache; niwa never sees or stores the token) |
| `niwa config set global <slug>` | Local machine: `~/.config/niwa/config.toml` (`[global_config]` pointer) |
| Declaring `[global.vault.provider]` in the overlay's `niwa.toml` | The **personal-overlay repo** (`<you>/niwa-overlay` or whatever slug is registered) — a git repo on the operator's own GitHub account, committed/pushed like any other repo. NOT the team's workspace source repo. |
| Declaring `[workspaces.<name>.env.secrets]` in the overlay | Same personal-overlay repo as above. |
| Creating the Infisical machine identity, enabling Universal Auth, creating a client secret | Remote: the Infisical **vault service itself** (dashboard or CLI against Infisical's API) — not a git repo at all. |
| `infisical secrets set --path ... "p-<uuid>=..."` | Remote: the Infisical vault service (the operator's PERSONAL vault project specifically, for the credential-sync flow). |
| `~/.config/niwa/provider-auth.toml` (the OLDER local-file multi-org mechanism, distinct from credential-sync) | Local machine only, `0o600`, per-machine — never committed anywhere, explicitly "outside any git repo." |
| Team config's `[vault.provider]`, `[env.secrets]`, `[vault].team_only`, `[workspace].vault_scope` | The **team's workspace source repo** (e.g. `tsukumogami/dot-niwa` or a brain-repo's `.niwa/workspace.toml`) — requires PR/merge access, is the "privileged write surface" referenced in the design doc's Decision 5 threat model. |
| `niwa apply` | Reads from all of the above; writes only to the **local workspace's materialized state** (`state.json`, `.local.env`/configured `env_output` targets) — never writes back to any source repo. |
| `niwa status --audit-auth` | Read-only everywhere; reads local `state.json` only. |

**Practical implication for the wizard**: the INDIVIDUAL phase is almost
entirely local-machine + personal-overlay-repo + vault-service writes — it
never needs write access to the team's source repo. The TEAM phase, by
contrast, needs both vault-service admin writes (identity/auth/grant/folder)
AND (per §1) plausibly a one-time edit to the team's workspace source repo
if `[vault.provider]`/`[env.secrets]`/`team_only`/`vault_scope` aren't
already declared there — that latter edit is the one place in this whole
choreography that could touch the upstream team source repo, and the docs
don't currently describe the team admin's wizard writing there
automatically (all documented instances of editing `workspace.toml` are
manual, human-driven edits in the surrounding guides).
