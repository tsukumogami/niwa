# Phase 2 Research: Architecture Perspective

## Lead 1: Conflict policy as a published contract

### Findings

**R12 and R31 exact wording** (`PRD-vault-integration.md`):

R12 (lines 598–609): "The `GlobalOverride` struct MUST support an optional `Vault *VaultRegistry` field so a personal overlay can declare its own providers. Merge semantics: the personal overlay can **add** new provider names that the team config didn't declare; it **cannot replace** a team-declared provider name."

R31 (lines 801–821): "When the personal overlay shadows a team-declared vault provider (R12) or a key that appears in the team config's `[env.vars]` / `[claude.env.vars]`, niwa MUST surface the shadow so the user can detect unexpected overrides."

**Existing precedence vocabulary**: The personal overlay shadowing team config is called "shadow" (`internal/workspace/shadows.go`). The `Shadow` struct carries only the key name and layer name—no secret values. The shadow is visible in three places: a stderr diagnostic during `niwa apply`, `niwa status` summary line, and `niwa status --audit-secrets` SHADOWED column.

**Personal-wins semantics in existing feature (D-3, line 1182–1196)**: "Personal wins" is explicitly stated as matching niwa's existing `MergeGlobalOverride` precedence. Teams can opt into `team_only` to enforce team-controlled keys that cannot be shadowed. The design doc notes this was chosen over team-wins or error-on-conflict because "users' local overrides should work without team involvement."

**Proposed machine-identity-vault-sync conflict**: The exploration decision states "local file wins on conflict" by analogy to personal-vs-team overlay semantics. However, the analogy is **backwards**: 
- In overlay semantics: **personal overlay** (per-user, distributed) shadows **team config** (shared, static). Personal wins.
- In machine-identity: **local file** (per-machine, static) vs **vault-sourced entry** (per-user, distributed). 
  
The vault-sourced credential is the closer analog to "personal overlay" (distributed per-user, not per-machine). The local file is the static base that the vault entry augments—like the team config role.

**Impact of inversion**: If vault-wins were the policy instead, it would align with "team-shared credentials override local machine-level ones"—useful for enforcing centrally-managed rotation where local pinning is not desired. But the current decision says local-wins, mirroring personal-overlay-wins, which assumes the **local file is the user's control point** (like personal overlay today).

### Implications for Requirements

1. **Diagnostic vocabulary must distinguish**: The existing "shadow" word works for personal-overlay-vs-team (overlay defines the new source of truth). For machine-identity, if local-wins is the policy, the vault entry is being rejected/ignored, not shadowing. Either call it "fallback" or "augmentation" (vault augments local, local takes precedence) or explicitly redefine "shadow" to cover both directions.

2. **R31 precedent binding**: Whatever the machine-identity conflict policy is, it MUST be surfaced with the same R31 diagnostic rigor. A user must be able to see "this provider/credential came from vault" vs "this one came from local file" in `niwa status` and apply-time stderr.

3. **No discovered use case for vault-wins in rotation**: No existing PRDs or exploration docs mention a centrally-managed rotation scenario where vault-wins would be needed. The `team_only` feature for env vars is a team-declared allow-list that **rejects** personal overlay, not a flag to invert precedence. If vault-wins is needed later, it would need to be either:
   - A separate opt-in flag like `--prefer-vault` per apply, OR
   - A per-provider config option in global config like `[global.machine_identities] from = "..." prefer_vault = true`
   
   The PRD should explicitly state which model (if any) is deferred.

### Open Questions

1. Is the analogy to personal-overlay-wins the right mental model here? The local file and the vault-sourced entry play different structural roles than personal and team configs.

2. Should the PRD introduce new diagnostic language ("augments", "fallback") distinct from "shadow" to make the precedence direction crystal-clear to users?

3. Is there a legitimate future requirement for vault-wins (centrally-enforced rotation)? Should the PRD reserve syntactic space for a future flag, even if it's not implemented in v1?

---

## Lead 7: Public-repo guardrail interaction

### Findings

**Guardrail implementation** (`internal/guardrail/githubpublic.go`):

The plaintext-secrets guardrail walks four locations:
1. Workspace-level `[env.secrets]` and `[claude.env.secrets]` (line 218–219)
2. Per-repo overrides at `cfg.Repos[*].Env.Secrets` and `cfg.Repos[*].Claude.Env.Secrets` (lines 221–226)
3. Instance-level overrides at `cfg.Instance.Env.Secrets` and `cfg.Instance.Claude.Env.Secrets` (lines 228–230)

The guardrail **does not walk `*.vars` tables**—only `*.secrets`. It checks the boolean predicate `isPlaintextSecret()` (line 176): returns true only for non-empty Plain values that are not vault URIs and not already resolved secrets.

**Precedent for personal-overlay repo risking**: The personal overlay CAN be a public repo (documented in scope and guides). Today's risk is that the overlay's `[workspaces.<scope>.env.secrets]` could contain plaintext secrets if the author makes a mistake. But the guardrail walks the merged config at apply-time, so plaintext secrets in the personal overlay are caught the same way as in the team config.

**Proposed feature's exposure**: The machine-identity-vault-sync feature stores credentials **in the Infisical vault**, not in the overlay file. The overlay file declares:
```toml
[global.machine_identities]
from = "vault-provider-name"  # Reference to a declared provider
```

This declaration is not a secret—it's topology. But it reveals:
- "I use an Infisical org for machine identities" (org name not necessarily hidden)
- "This Infisical project UUID holds my bootstrap credentials" (if path/naming convention is predictable)

**Non-parallel between env-secrets and machine-identities guardrail**: The plaintext-secrets guardrail exists because plaintext values in `[env.secrets]` on a public repo are an accidental-leak risk. Machine identities stored in the vault are not in the config at all. The analogous risk would be:
- A malicious overlay PR that declares `from = "attacker-org/attacker-project"` OR
- A typo in the `from` reference that points to a non-existent provider, causing fallback to CLI session (exposing that path)

**Precedent from R12 enforcement**: R12 forbids personal overlay from declaring a provider name that shadows a team-declared provider. This is enforced by checking provider names at parse/merge time, before any vault provider is invoked. The code path for R12 enforcement is in the config resolver (exploration finding cites `PRD-vault-integration.md` US-9 section 396–407 as the rejection rationale).

**No precedent for "personal overlay can't declare X without opt-in"**: The feature doesn't need the same guardrail as plaintext-secrets because:
1. The vault stores the actual secrets, not the overlay file.
2. Declaring `[global.machine_identities] from = "..."` is opt-in by itself—the feature is disabled if the field is absent.
3. Unlike plaintext-secrets (accidental-leak), there's no "accidental declaration" risk; declaring the feature requires explicit config.

However, if the personal overlay is public and the Infisical project UUID is discoverable from the overlay (e.g., if naming convention is `niwa/<workspace-name>/<project-uuid>`), an attacker could enumerate the vault structure. But this is topology leakage, not secret leakage—and the current vault provider config already leaks project UUIDs in `[vault.provider] project = "..."` in public team configs.

### Implications for Requirements

1. **The guardrail does NOT need to extend**: Plaintext-secrets walks `*.secrets` tables specifically because those values are secrets. Machine-identity credentials are not in the overlay; they're in the vault. A public personal overlay declaring machine-identity sync does not create a new plaintext-secret exposure.

2. **R14 (public-repo guardrail) remains unchanged**: The feature adds no new condition for `CheckGitHubPublicRemoteSecrets`. The guardrail scope is pre-vault config only.

3. **Topology leakage is orthogonal**: If the PRD commits to a predictable path convention (e.g., `/niwa/provider-auth/<kind>-<project-uuid>`), users should understand that a public personal overlay exposes this topology. This is not a secret leak and does NOT block the feature. Document it as an operational consideration, not a security invariant violation.

4. **Suggest explicit documentation in PRD**: State clearly: "Machine-identity credentials are stored in the vault provider, not in the overlay file. The overlay file only declares the opt-in and provider reference. A public personal overlay that declares `[global.machine_identities]` does not expose secret values; it only exposes the vault topology. This is not a threat-model violation under R21–R31."

### Open Questions

1. Should the PRD explicitly define a naming/path convention for vault entries to be used in operational guidance? (E.g., "we recommend `/niwa/machine-identities/<kind>-<project-uuid>` so the path structure mirrors the niwa internal semantics, understanding this is topology not secret.")

2. Is there a scenario where a user would want `team_only`-style protection for machine-identity declarations (e.g., "team must approve machine-identity opt-in before personal overlay can declare it")? The feature explores this as out-of-scope, but should it be called out as a future pattern?

3. Should the bootstrap guide include a note: "If your personal overlay repo is public, the machine-identity opt-in and provider reference are visible but not secrets"?

---

## Summary

The conflict policy "local file wins" mirrors the personal-overlay-wins precedent but inverts the structural relationship: vault-sourced entries are more akin to personal overlay (distributed, per-user) than the local file (static, per-machine). The decision is sound for machine-identity use cases (let developers pin credentials locally without vault changes), but the PRD should clarify whether the analogy holds and whether to reserve syntax for a future "vault-wins" option for centralized rotation. The plaintext-secrets guardrail does not need to extend because machine-identity credentials live in the vault, not the overlay file; the feature adds no new secret-leakage risk. The R31 override-visibility pattern MUST apply to machine-identity source attribution so users can audit which layer supplied each credential.

