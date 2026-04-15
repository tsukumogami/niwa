# /prd Scope: vault-integration

## Problem Statement

niwa's `tsukumogami/dot-niwa` team config repo is private today primarily
because it stores plaintext API tokens and secrets. Moving secrets into a
real vault provider would let the team config become public, gives
per-developer PAT scoping for different orgs, and eliminates the
"accidentally committed a secret" risk class — but niwa has no vault
integration, no layering model for team-vs-personal secrets, and no
materialization path that keeps resolved values off disk in
world-readable form.

## Initial Scope

### In Scope

- Pluggable vault provider interface, shipping with two backends in v1:
  - **sops + age** (default; zero-cost, vendor-neutral, git-native; makes
    the team config repo publishable).
  - **Infisical** (hosted upgrade path; MIT-licensed core with
    self-host option; generous free tier).
- Schema additions (niwa v0.7 target):
  - New top-level `[vault.providers.*]` registry for declaring named
    vault backends.
  - `vault://<provider>/<key>` URI reference scheme accepted in every
    string-valued slot (`env.vars`, `claude.env.vars`, `repos.*.env.vars`,
    `instance.env.vars`, `files` source keys, `claude.settings` values).
  - Optional `Vault *VaultRegistry` field on `GlobalOverride` for
    personal-overlay provider declarations.
  - Optional `workspace.vault_scope` string for multi-source or
    borrowed-scope workspaces.
- Scoping algorithm: personal overlay layers on top of team config, keyed
  by `ws.Sources[0].Org` (or explicit `workspace.vault_scope`); resolution
  chain is `personal-scoped → personal-default → team`, personal wins per
  key.
- Conflict-resolution policy: personal wins by default; team config can
  declare `team_only = ["KEY1", ...]` to refuse per-user shadowing on
  specific keys.
- Graceful degradation: fail hard by default when a vault reference
  can't be resolved; `--allow-missing-secrets` CLI flag for exceptional
  runs; per-reference `?required=false` query param for known-optional
  secrets.
- Security hardening (12 "never leaks" invariants):
  1. No secret values on argv (`INV-NO-ARGV`).
  2. Redacting `secret.Value` opaque type for all resolved values
     (`INV-REDACT-LOGS`).
  3. niwa never writes resolved secrets back into `ConfigDir`
     (`INV-NO-CONFIG-WRITEBACK`).
  4. Materialized files containing resolved secrets use mode `0o600`
     (`INV-FILE-MODE`). Fixes a pre-existing `0o644` bug in
     `SettingsMaterializer` and `EnvMaterializer`.
  5. Materialized secret-bearing files carry the `.local` infix
     (`INV-LOCAL-INFIX`).
  6. `niwa create` ensures the instance-root `.gitignore` covers
     `*.local*` (`INV-GITIGNORE-ROOT`).
  7. CLAUDE.md / CLAUDE.local.md never interpolate secrets
     (`INV-NO-CLAUDE-MD-INTERP`).
  8. `niwa status` shows path + status only, never file content
     (`INV-NO-STATUS-CONTENT`).
  9. niwa never calls `os.Setenv` with a resolved secret
     (`INV-NO-PROCESS-ENV-PUBLICATION`).
  10. No disk caching of resolved secrets by niwa
      (`INV-NO-DISK-CACHE`).
  11. Subprocess env built explicitly, not inherited (`INV-EXPLICIT-SUBPROCESS-ENV`).
  12. Public-repo guardrail: niwa refuses to apply a config repo with a
      public remote if plaintext `[env].vars` values are present and a
      vault is configured (`INV-PUBLIC-REPO-GUARDRAIL`).
- `ManagedFile.SourceFingerprint` addition to distinguish user drift
  (file edited locally) from upstream rotation (vault value changed).
- `niwa status --audit-secrets` subcommand that enumerates env entries
  and classifies each as `plaintext | vault-ref | empty`, exits non-zero
  if plaintext values found.

### Out of Scope

- **v1 niwa `vault import`** (plaintext → vault migration tool). Signposted
  but deferred; users can use the provider CLI directly to import.
- **Additional providers beyond sops+age and Infisical**: Doppler,
  1Password, Vault OSS, Bitwarden Secrets Manager stay on the deferred
  list. The pluggable interface means they can be added without
  rearchitecting.
- **Secret rotation automation**: niwa doesn't run scheduled checks or
  trigger rotation. `niwa apply` re-resolves on every run, which picks up
  upstream changes, but niwa isn't the rotation source of truth.
- **Daemon-based vault watching**: no `niwa-agent` process that monitors
  vault values. Pull model only.
- **Windows support**: macOS + Linux only for v1.
- **Secret templating in CLAUDE.md**: explicitly forbidden per
  `INV-NO-CLAUDE-MD-INTERP`; secrets go into env, env goes to
  Claude's settings, Claude reads env at runtime.
- **Per-file permissions customization**: vault-sourced `[files]` entries
  always get `0o600`; not configurable in v1.
- **`[env.files]` vault-backed source paths**: filesystem-path semantics
  unchanged; only the file *content* can be vault-sourced via
  `[files]` (key as vault URI, value as destination path).
- **Multi-factor auth prompts mid-command**: niwa relies on the vault
  CLI's own auth machinery. If a user's session expired, the next niwa
  command fails with a re-auth hint; niwa itself doesn't prompt for 2FA.

## Research Leads

1. **Vault provider landscape and free-tier viability**: surveyed 10
   providers. Rationale: eliminate by cost, OSS status, auth-model fit,
   and bootstrap UX. Finalists sops+age and Infisical; rejected HCP Vault
   Secrets (EOS), Pulumi ESC (1-user free tier), AWS/Azure (no real
   free tier), GitHub Actions secrets (not readable from laptop),
   1Password (no permanent free tier).

2. **Tool layering patterns**: surveyed direnv, sops-nix, mise, 1Password
   per-project, gh auth, chezmoi, Doppler/Infisical projects. No surveyed
   tool solves niwa's per-org-scoped-personal-overlay requirement
   natively; three patterns compose cleanly (mise's two-file precedence,
   1Password's URI refs, Pulumi-ESC-style resolution tables).

3. **Auth models**: evaluated each finalist's dev-laptop auth story.
   GitHub-federation is rare among hosted providers. sops+age needs
   zero provider auth — the age private key on disk IS the credential.

4. **Layering and scoping semantics**: evaluated 5 options for how to
   resolve per-project personal secrets. Option A (implicit by source
   org) + Option B escape hatch (`vault_scope`) minimizes ceremony for
   the 80% case.

5. **Schema integration shape**: evaluated 5 options for where vault
   declarations live. Option 3 (top-level `[vault.providers.*]` + URI
   refs in existing string slots) has the smallest footprint and the
   widest applicability.

6. **Security and runtime handling**: audited the existing
   materialization pipeline, discovered a pre-existing `0o644` bug,
   enumerated 12 "never leaks" invariants that become acceptance
   criteria.

## Coverage Notes

Questions the exploration did NOT fully resolve that the PRD must address:

- **Personas and user stories**: research produced technical requirements
  but not explicit personas. PRD Phase 2 should produce them — indie
  solo developer, small-team lead, small-team member, open-source
  contributor to a public niwa config.

- **Success metrics**: how do we measure that the feature is working?
  Number of plaintext secrets in the `tsukumogami/dot-niwa` repo drops
  to zero? Number of GitHub secret-scanning alerts for
  committed-by-accident tokens? PRD needs explicit metrics.

- **Rollout plan**: v1 ships with sops only, or sops + Infisical? What's
  the signal for "ready to deprecate plaintext `[env].vars`"? Not a hard
  break, but what's the target date for public-repo guardrail becoming
  the default?

- **Escape syntax for literal `vault://` strings**: picked deferred from
  research. PRD should settle on one option (`\vault://...` or
  `raw:vault://...` or similar) so the eventual implementation doesn't
  break users later.

- **Exact `team_only` enforcement**: is this a parse-time validation or
  a materialize-time check? Where does the error surface for users who
  try to shadow a listed key?

- **Public-repo guardrail detection**: remote URL inspection (look for
  `github.com/<org>/<repo>` with org public-visibility flag) vs
  explicit `public: true` in workspace.toml? Research proposed both;
  PRD should pick one.

- **Migration UX**: what does the user's experience look like going
  from today's plaintext config to a vault-backed one? Is there a
  guided walkthrough (`niwa vault init`)? PRD should sketch the
  step-by-step first-migration flow.

- **v1 stakeholder sign-off list**: does this feature need any external
  alignment (security-minded community members? the `tsuku` ecosystem
  maintainers?) before it ships, or is niwa's own maintainer sufficient?

## Decisions from Exploration

The following were settled during exploration and carry into the PRD as
constraints:

- **Backend strategy**: pluggable interface from v1; sops+age first,
  Infisical second. Other providers (Doppler, 1Password, Vault OSS,
  Bitwarden SM) are deferred.
- **Scoping**: Option A implicit by `ws.Sources[0].Org` + Option B
  `workspace.vault_scope` escape hatch. Multi-source workspaces must
  set the explicit escape hatch.
- **Schema**: Option 3 — top-level `[vault.providers.*]` registry + per-
  value `vault://<provider>/<key>` URI refs. No new override merge
  logic; rides existing `MergeOverrides` / `MergeGlobalOverride` /
  `MergeInstanceOverrides`.
- **Conflict resolution**: personal-wins by default; `team_only = [...]`
  opt-in list on team config to enforce team-controlled keys.
- **Graceful degradation**: fail hard by default; `--allow-missing-secrets`
  CLI flag + per-reference `?required=false` query parameter for opt-outs.
- **Runtime handling**: 12 "never leaks" invariants are acceptance
  criteria, not implementation details.
- **Drift vs rotation**: `ManagedFile.SourceFingerprint` distinguishes
  them; `niwa apply` re-resolves on every run.
- **Caching**: none inside niwa; delegate to vault CLI's own session
  machinery.
- **Out-of-scope boundaries**: vault in CLAUDE.md content forbidden;
  vault in `[env.files]` source paths forbidden; vault in provider
  registry fields forbidden; vault auth tokens themselves never in
  TOML.
