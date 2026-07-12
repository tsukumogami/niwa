# /prd Scope: niwa-onboard

## Problem Statement

Developers and team admins onboarding a machine-identity workspace vault must
hand-run a long, cross-context choreography from runbooks: a team phase (create
identity, attach Universal Auth, grant ACL, create folders) and an individual
phase (personal overlay, mint client secret, store at the exact credential-sync
contract shape, verify). Every step is mechanical, the credential shape is
unforgiving, and mistakes surface silently at a later `niwa apply`, far from
the cause. Upstream framing: docs/briefs/BRIEF-niwa-onboard.md (Accepted).

## Initial Scope

### In Scope
- One `niwa onboard` command, two setups (team admin once; individual per
  developer), delivered as an interactive wizard with branches.
- Vault topology as an explicit first-class choice: same-login (workspace vault
  and personal overlay in one account, zero login pauses) vs split-login
  (dedicated workspace-vault org + personal account, one login switch);
  switching shapes later by re-running.
- Producing the credential-sync contract shape by construction; verifying
  resolution before declaring success (folds in #199 doctor logic).
- Minting client secrets on the existing team identity via Infisical
  universal-auth REST (folds in #194 mechanics, including secret hygiene).
- Delegating privileged team-phase steps to the operator's own `infisical` CLI
  session; graceful degradation on plan-gated steps (dashboard instructions).
- Generic command surface: workspace constants from config/overlay, never
  hardcoded.

### Out of Scope
- niwa holding admin tokens or reimplementing the provider's admin REST API.
- Non-Infisical vault backends for admin/provisioning steps in v1.
- Shipping #194/#199 as standalone commands.

## Research Leads

1. codebase-seams: What exists in the niwa codebase for the wizard to build on
   -- cobra command registration and exit-code mapping, any existing
   interactive prompt UX, the `internal/vault/infisical` subprocess delegation
   and universal-auth REST client, config surfaces (workspace-overlay.toml,
   VaultRegistry, `[global.vault.provider]`), `niwa status --audit-auth`, and
   what is net-new. Requirements must distinguish reuse from new surface.
2. prior-design-mechanics: Synthesize the code-verified mechanics from the
   superseded PRs' design docs (mint/store REST endpoints, secret hygiene
   rules, credential-sync read topology, contract validator) into
   requirement-shaping constraints the PRD must respect.
3. infisical-admin-surface: Which team-phase admin operations the `infisical`
   CLI can perform in an operator's session (identity creation, UA attach, ACL
   grant, folder creation), which are dashboard-only, and which are plan-gated
   -- this determines the wizard's delegation vs degrade-gracefully requirement
   set per step.
4. onboarding-runbook-ground-truth: The manual steps as documented today in the
   repo's guides (machine-identity-vault-sync, vault-integration,
   workspace-config-sources, init-bootstrap) -- the authoritative enumeration
   of what the wizard must automate, and any step the BRIEF framing missed.

## Coverage Notes

- The BRIEF settles who/what/why/boundaries; the open details it deferred
  (setup detection, login pause/resume mechanics, topology naming/detection)
  become PRD Decisions and Trade-offs entries.
- Uncertainty to resolve in Phase 2: exact infisical CLI capability matrix for
  admin steps (which steps can be automated vs dashboard-guided), and how the
  wizard should verify team-phase results without admin API access.
