# Explore Scope: vault-integration

## Visibility

Public

## Scope

Strategic — the exploration's goal is to identify which vault provider(s) to
support first and whether the proposed layering model (team-shared vault +
per-project-scoped personal vault) is implementable as a PRD-worthy feature.
Running in `--auto` mode per user direction. Decisions made during
exploration follow the research-first protocol and are recorded in
`wip/explore_vault-integration_decisions.md`.

## Core Question

What vault provider(s) should niwa integrate with first, and what
architecture supports layering a team-shared vault (declared in a public/
private team config repo like `tsukumogami/dot-niwa`) with a per-project-
scoped personal vault (declared in a personal config repo like
`dangazineu/dot-niwa`), so team secrets (API tokens) and personal secrets
(PATs) cleanly separate while resolving per-workspace?

## Context

- niwa manages multi-repo workspaces with layered Claude Code configuration.
- The `dot-niwa` pattern is private today primarily because it holds
  plaintext secrets (API keys, PATs). Moving secrets to a vault would let
  the team-shared config become public or at least removes the "private
  because of secrets" reason.
- niwa already has a `GlobalOverride` chain (v0.5.0) that layers a global
  config repo over the workspace config. Personal-vs-team layering can
  likely ride this existing machinery.
- Personal secrets are scoped per-project: a `tsukumogami` PAT should only
  resolve for workspaces in the `tsukumogami` org, not for a workspace in
  a different org. Team secrets have no such per-user scoping concern.
- User has flagged this exploration will likely crystallize to a PRD
  (requirements-level artifact). That means the exploration should surface
  requirements gaps and stakeholder alignment needs, not just technical
  feasibility.

## In Scope

- Vendor survey: free-tier vault providers, their auth models, secret
  shapes, CLI/API ergonomics, OSS status.
- Layering semantics: resolution algorithm for team vs personal, conflict
  rules, per-project scoping convention.
- Integration shape with existing niwa schema (`[env]`, `[claude.env]`,
  `[repos.<name>.env]`, `GlobalOverride`).
- Security / runtime secret handling: where resolved secrets live, how to
  avoid leaks, caching policy, threat-model boundary.
- How similar developer tools handle the same problem (direnv, sops-nix,
  mise, 1password per-project, chezmoi, gh auth).

## Out of Scope

- Implementation details (reserved for the design doc that follows the
  PRD).
- Specific provider pricing beyond "is there a free tier that works for
  small teams (1-5 devs) and solo use."
- Non-niwa secret-management systems (Kubernetes Secrets, CI-only
  vaults).
- Windows-specific considerations (niwa targets macOS + Linux for now).

## Research Leads

1. **Vault provider landscape and free-tier viability**
   (`lead-vault-provider-landscape`). Survey HashiCorp Vault (OSS + HCP
   free), Infisical (OSS + free cloud), Doppler (free tier), 1Password
   Secrets Automation / Connect, Bitwarden Secrets Manager,
   AWS/Azure free tiers, sops-style local crypto (gitops-friendly),
   GitHub Actions-style encrypted secrets. For each: free-tier limits
   (users, secrets, reads/month), auth model, secret shape (kv / hierarchical
   / file), CLI / API ergonomics, OSS status, niwa-relevant limitations.
   Pick 2-3 finalists grounded in niwa's needs.

2. **How similar developer tools solve team-vs-personal secret layering**
   (`lead-tool-layering-patterns`). Survey direnv + .envrc, dotenv +
   sops-nix, mise, nix-shell, 1password per-project binding (`.1password`
   config), gh auth (GitHub itself as a vault), chezmoi (dotfiles +
   secrets). What conventions exist for "personal secret scoped by
   project context"? Pull out patterns niwa should reuse rather than
   invent.

3. **Auth models for team vault vs personal vault**
   (`lead-auth-models`). Team-shared vault typically needs org-bound
   identity (SSO, OIDC, service account tokens bound to GitHub org
   membership). Personal vault uses user creds. For the top-3 providers
   from Lead 1: what's the practical auth method for a dev laptop
   without enterprise infra? What's the first-time-setup UX? What
   happens when secrets aren't accessible (auth expired, no access)?
   Graceful degradation story — does niwa fail loud, warn and continue
   with unresolved refs, or prompt the user?

4. **Layering and scoping semantics: the resolution algorithm**
   (`lead-layering-resolution`). Concretely: user is in `tsukumogami/niwa`
   workspace. Their personal config declares a personal vault provider.
   How does niwa know "this workspace wants the tsukumogami PAT, not the
   codespar PAT"? Options to evaluate:
   - (a) Implicit scoping by workspace's source org name.
   - (b) Workspace.toml names an explicit "vault scope" string.
   - (c) Per-org personal-vault map in the personal config
     (`dangazineu/dot-niwa` declares `[vaults.tsukumogami]`,
     `[vaults.codespar]`).
   - (d) A fallback default vault per provider plus an override map.
   Plus: when team config names a specific vault AND personal config
   names another, which wins for vault address vs individual secrets?
   Conflict rules when team and personal both define the same secret
   key.

5. **Integration shape with existing niwa schema**
   (`lead-schema-integration`). niwa has `[env]` (shared), `[claude.env]`
   (Claude-specific), per-repo `[repos.<name>.env]`, and the global
   overlay via `GlobalOverride`. Where does vault binding live? Options:
   - New `[vault]` top-level table.
   - Nested under `[env.vault]`.
   - Per-secret references from existing `[env].vars` using a URI
     scheme (e.g., `value = "vault://<name>/<key>"`).
   - Implicit resolution (no schema change; key prefixes trigger
     lookup).
   How does `GlobalOverride` extend to carry vault refs? How does this
   interact with the recently-shipped `[claude]` consolidation (is
   there a `[claude.vault]` concept, or is vault at workspace level
   only)? Public-repo compatibility: if the team dot-niwa goes public,
   which concrete secret values must never appear in the TOML?

6. **Security and runtime handling**
   (`lead-security-runtime`). Where do resolved secrets live at
   runtime: in-memory only, written to an ephemeral `.env.local`,
   injected as env vars to spawned processes (Claude Code, `niwa
   apply`, `niwa create`)? How to avoid leaks into: logs, error
   messages, generated CLAUDE.md files, `niwa status` output, shell
   history. Caching policy (redaction? TTL? memoization across commands
   in the same shell session?). Threat-model boundary: local user's
   machine trusted, but secrets must not land in the cloned config
   repo working tree. What's the story for `niwa status` showing a
   drift for a secret-backed file? Does `niwa apply` verify resolution
   before committing to the rest of the pipeline? Interaction with
   `git` hooks that prevent accidental commits of secrets.
