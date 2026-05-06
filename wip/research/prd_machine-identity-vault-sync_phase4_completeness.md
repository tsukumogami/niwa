# Completeness Review: PRD-machine-identity-vault-sync

## Verdict: FAIL
The PRD leaves critical implementer decisions unspecified in failure-mode contracts, audit output formatting, conflict diagnostics, and the bootstrap case, forcing implementation teams to guess at user-facing behavior.

## Issues Found

### 1. Failure-Mode Contract Gap: Vault Unreachable Behavior
**Issue**: R13 states vault unreachability results in "Warn to stderr; fall back" with exit code 0, but does NOT specify whether this is resilient downgrade or hard error when vault is the ONLY credential source.

**Current wording**: 
```
Personal vault unreachable (network down, CLI not installed) | Warn to stderr; fall back to local-file / cli-session for every entry. Apply continues. | 0
```

**Problem**: A developer with `[global.machine_identities]` declared but NO `provider-auth.toml` entries and NO `infisical login` session would hit the unreachable vault, fall back to missing credentials, and the apply would fail LATER (at Infisical auth time) with a different error. The sequencing and exact error wording are ambiguous.

**Suggested Fix**: Add explicit case in R13:
```
Personal vault unreachable but local-file entries cover all needed (kind, project) pairs | Warn to stderr naming vault provider. Apply continues using local-file entries. | 0
Personal vault unreachable and NO local-file entries match needed (kind, project) pairs | Warn to stderr. Fall back to CLI session. If CLI session unavailable, hard error from backend auth. | non-zero (if auth fails)
```

**Affected Requirements**: R13, R9 (failure-mode contract)
**Acceptance Criteria Coverage**: AC "When the personal vault is unreachable, apply continues..." is vague on "continue to what state"

---

### 2. Audit Output Format Unspecified
**Issue**: R11 defines audit CONTENT (source column values) but does NOT specify the command, column order, table format, or whether `--audit-auth` is a flag or subcommand.

**Current wording**: 
```
niwa status --audit-auth lists every (kind, project) pair niwa needed credentials for in the last apply, with a column identifying the source as one of: local-file, vault:<name>, cli-session, none
```

**Problem**: Is this a NEW subcommand (`niwa status --audit-auth`) or a FLAG on `niwa status` to show an additional table? What are the column headers? Are there other columns besides SOURCE? The ops/UX research document (Lead 2) proposed "by analogy to `--audit-secrets`" but the PRD doesn't commit to that design. An implementer must guess at the output format and risk shipping something that doesn't match user expectations.

**Suggested Fix**: Specify in R11 or R12:
```
niwa status --audit-auth (where --audit-auth is a flag, like --audit-secrets)
Outputs a TEXT TABLE (not JSON) with columns: KIND | PROJECT-UUID | SOURCE | ACTIVE

Example output:
KIND       PROJECT-UUID                          SOURCE              ACTIVE
infisical  550e8400-e29b-41d4-a716-446655440000  vault:personal      yes
infisical  660f9511-f40c-52e5-b827-557766551111  local-file          yes
infisical  770g0622-g51d-63f6-c938-668877662222  vault:personal      no (overridden by local-file)
infisical  880h1733-h62e-74g7-d949-779988773333  cli-session         yes
```

**Affected Requirements**: R11, R12 (audit surface)
**Acceptance Criteria Coverage**: "niwa status --audit-auth lists..." leaves output format entirely open

---

### 3. Conflict Diagnostics Ambiguous
**Issue**: R11 states when "same `(kind, project)` has entries in both...both are shown with local-file source marked **ACTIVE** and the vault entry marked **FALLBACK**" but R12 (apply-time stderr) does NOT specify what to emit when a local-file entry overrides a vault entry.

**Current wording R12**: 
```
Shape: auth: <kind>/<project-uuid> source=vault:<name>. No line is emitted for local-file or cli-session sources to avoid noise
```

**Problem**: When a local-file entry shadows a vault entry, should apply emit:
- `auth: infisical/uuid source=local-file (overriding vault:personal)`?
- OR nothing, maintaining "no line for local-file"?
- OR a distinct shadow diagnostic like "shadowed auth infisical/uuid [vault-entry overridden by local-file]"?

The asymmetry between "emit per-provider" for vault but "no line for local-file" creates ambiguity when BOTH exist.

**Suggested Fix**: Clarify R12:
```
On every apply that uses at least one vault-sourced credential:
- Per vault-sourced entry USED: emit auth: <kind>/<project-uuid> source=vault:<name>
- Per local-file entry that OVERRIDES a vault entry: emit auth: <kind>/<project-uuid> source=local-file (fallback: vault:<name> overridden)
- No line for CLI-session sourced credentials to minimize noise
```

**Affected Requirements**: R11, R12 (audit & diagnostics)
**Acceptance Criteria Coverage**: AC about stderr signal doesn't specify the override case wording

---

### 4. Anonymous Provider Default Behavior Unclear
**Issue**: R1 states "when [global.machine_identities] table is absent, the feature is disabled" and "when `from` is empty/unset, niwa uses the anonymous `[global.vault.provider]` if declared and errors otherwise." But what if a user DECLARES `[global.machine_identities]` with an empty `from = ""` field in a config that has MULTIPLE named providers and NO anonymous provider?

**Current wording R1**: 
```
from = "" (unset), in which case niwa uses the anonymous [global.vault.provider] if declared and errors otherwise
```

**Problem**: Is the error "multiple vault providers declared, must specify one via from" (config-layer error) or "no anonymous provider declared, declare one or specify from" (validation error)? The research phase 2 codebase analysis found that anonymous and named are mutually exclusive, so this case is impossible, but the PRD doesn't state that constraint.

**Suggested Fix**: Add to R2 validation requirement:
```
R2 clarification: If from field is present but empty (from = ""), and the global config declares multiple [global.vault.providers.*] (named-only mode), niwa fails at parse time with: "machine-identity-vault-sync requires an explicit provider name when multiple vault providers are declared. Set from = \"<provider-name>\" or declare a single anonymous [global.vault.provider]."

If from field is empty and no vault providers are declared at all, error: "machine-identity-vault-sync is enabled ([global.machine_identities] present) but no vault provider is declared. Either declare [global.vault.provider] or [global.vault.providers.<name>] and set from = \"<name>\" if named."
```

**Affected Requirements**: R1, R2 (opt-in & validation)
**Acceptance Criteria Coverage**: AC doesn't cover the multiple-named-providers case with empty `from`

---

### 5. Schema Version Mismatch Error Wording Missing
**Issue**: R8 requires version validation but does NOT specify the exact stderr message or the code location where the version check happens.

**Current wording R8**: 
```
niwa reads the version field of the fetched body and rejects any value other than "1" with a clear diagnostic ("unsupported provider-auth schema version X; upgrade niwa or use a v1-compatible body")
```

**Problem**: The parenthetical is a TEMPLATE, not a final message. When should this check occur? During body parsing (before trying to unmarshal fields) or after parsing? If a body has `version = "2"`, should niwa emit the message and skip that entry (fallback to local-file) or hard-error?

R13 states "wrong `version`" is a "Hard error. Apply fails." but R8 is ambiguous on timing.

**Suggested Fix**: Update R8:
```
When niwa deserializes a vault-fetched body for a (kind, project) pair:
1. Extract the "version" field (top-level TOML string)
2. If version is present and not "1", emit hard error with exit non-zero: 
   "provider-auth body at /niwa/provider-auth/<kind>/<project> has unsupported schema version \"<version>\"; 
    this niwa version supports v1. Upgrade niwa or update the vault entry to use v1."
3. If version is missing entirely, treat as v1 (backward compatibility for vault entries created before versioning was added)

The version check occurs BEFORE attempting to unmarshal client_id/client_secret fields, so parse errors are caught cleanly.
```

**Affected Requirements**: R8, R13 (schema validation)
**Acceptance Criteria Coverage**: "when version = 2, apply fails" is present but message wording not finalized

---

### 6. Personal Vault Self-Auth Validation Scope Incomplete
**Issue**: R9 validates that the personal vault's `(kind, project)` does not match a credential-pool entry, but does NOT specify what happens if the personal vault's PROVIDER is actually the vault we're trying to populate.

**Current wording R9**: 
```
if the personal vault's (kind, project) matches an entry in the local credential pool (local-file or vault-sourced), niwa fails apply with a diagnostic describing the chicken-and-egg cycle
```

**Problem**: This catches the case where provider-auth.toml has an entry for (infisical, personal-vault-project-uuid). But what if the personal vault's config itself has `path` or other settings that might be misunderstood? 

More critically: If a developer accidentally sets `from = "team"` (pointing to a team-layer vault provider instead of their personal overlay), the validation at R9 only catches the "(kind, project) match" case. An implementer needs to know: should R9 also validate that the named provider is from the personal-overlay layer, not the team layer?

**Suggested Fix**: Extend R9:
```
R9 clarification: The personal vault provider (the one referenced by [global.machine_identities] from = "<name>") is validated as follows:
1. Its (kind, project) pair MUST NOT match any entry in the local credential pool (local-file or vault-sourced)
   → If it does, error: "personal vault provider <name> (kind=<k>, project=<p>) would create a chicken-and-egg cycle. Either use a different personal vault project, or move this identity to [global.machine_identities] do not reference it as a provider."

2. The named provider (if from = "<name>") MUST be declared in the personal overlay's [global.vault.providers.<name>], NOT in team config's [vault.providers.<name>]
   → This is enforced by R12 (R12 collision rule forbids same-named providers in both layers).
   → If R12 fires, the error message already covers this. No additional check needed in R9.
```

**Affected Requirements**: R9 (personal vault auth validation)
**Acceptance Criteria Coverage**: AC mentions "apply fails with chicken-and-egg diagnostic" but doesn't bound the scope of validation

---

### 7. Multi-Project Identity Scenario Underspecified
**Issue**: User Story US-1 "Multi-project contributor sets up a new laptop" does NOT specify how the personal vault is POPULATED with entries for multiple Infisical orgs. The PRD assumes the vault is pre-populated but provides no distribution contract.

**Current wording**:
```
As a developer who works across two Infisical orgs and has just unboxed a new laptop, I want niwa apply to authenticate against both orgs without me hand-editing a credential file, so that the fresh-machine setup time is minutes not hours.
```

**Problem**: 
- How did the personal vault GET the entries for both orgs? Was the developer manually entering them via the Infisical dashboard?
- The feature OUT OF SCOPE section says "Writing credentials to the vault from niwa" — no `niwa vault auth push`. So this feature is NOT the mechanism for populating the vault.
- The personal-overlay README or guide must document the manual step (or automation) to get entries into the vault, but this PRD doesn't define that contract.

This is not strictly a gap in THIS PRD (it's infrastructure/ops docs scope), but it IS a gap in the user story completeness. An implementer reading US-1 might assume niwa will auto-populate the vault.

**Suggested Fix**: Add a Known Limitation or note to US-1:
```
US-1 assumes the developer has already populated the personal vault with entries for both orgs using the Infisical CLI or dashboard. See [GUIDE: Populating Machine Identities in Your Personal Vault] for instructions. This PRD does not define a mechanism for auto-minting or distributing identities; identities are created manually per the Infisical workflow.
```

**Affected Requirements**: User story clarity
**Acceptance Criteria Coverage**: No AC for "personal vault can be pre-populated"

---

### 8. Backward Compatibility Path Unspecified
**Issue**: The PRD states "Add zero behavior change for users who don't opt in" (goal 5) but does NOT specify what happens to users WITH existing `provider-auth.toml` files who DO NOT declare `[global.machine_identities]`.

**Current wording R15**: 
```
A user who has no ~/.config/niwa/provider-auth.toml, has no [global.machine_identities] table, and uses only their infisical login session continues to work identically.
```

**Problem**: R15 covers the case of "no file, no opt-in, single org." But what about a user who HAS a provider-auth.toml AND does NOT opt into vault-sync? Does the local file continue to work unmodified? Yes, likely — but the PRD should EXPLICITLY state this to prevent implementers from adding unnecessary migration logic or warnings for non-opting-in users.

Additionally: The research exploration (findings.md) noted "Backward compatibility" as a tension ("after this lands, 'the file' is no longer the source of truth — it's one layer in a pool"). The PRD should state clearly whether existing provider-auth.toml entries continue to work with the same precedence they had before.

**Suggested Fix**: Expand R15 or add R16a:
```
R15 clarification: When a user does NOT declare [global.machine_identities] (credential-sync opt-in):
- If provider-auth.toml exists, it is used as the sole credential source (as before)
- If provider-auth.toml does not exist, behavior is unchanged: fall back to CLI session
- Personal vault is never consulted for machine-identity credentials
- This backward-compatible behavior ensures existing niwa workflows see zero changes

Non-opting-in users see NO warnings, NO new errors, NO new latency. Provider-auth.toml remains the authoritative source when credential-sync is disabled.
```

**Affected Requirements**: R15 (backward compat), Goal 5 (zero behavior change)
**Acceptance Criteria Coverage**: AC for "single-org users see no behavior change" is too narrow; doesn't cover existing multi-org users with provider-auth.toml

---

## Suggested Improvements

### 1. Clarify the Relationship to R12/D-9 Explicitly in Diagnostics
**Rationale**: The scope document emphasizes distinguishing this from the rejected R12/D-9 pattern, but the PRD never states explicitly where the user-facing diagnostic should surface this distinction. When a developer configures `from = "team"` (accidentally pointing to a team-layer provider instead of personal), the error should make clear: "This feature uses the PERSONAL vault to supply credentials, not the team vault. Team vaults remain under team config control (R12)."

**Suggested addition**: R2 or new requirement section
```
R2b — Clear distinction from R12/D-9 in diagnostics:
Validation errors that involve provider naming must explicitly reference whether the provider is personal-overlay or team-layer, to avoid confusion with the R12/D-9 rejected bulk-provider-swap pattern.

Example: if from = "team" references a team-layer provider:
"The named provider \"team\" is declared in the workspace team config, not in your personal overlay. Machine-identity-vault-sync only uses personal-overlay vault providers. Either declare a personal-overlay vault provider or update [global.machine_identities] from to match a declared [global.vault.providers.<name>]."
```

### 2. Specify Default TOML Body Schema Examples
**Rationale**: R7 specifies the path `/niwa/provider-auth/infisical/<project-uuid>` and that the body is TOML with `version`, `client_id`, `client_secret`, and optional `api_url`. But a developer populating this manually has no examples. This should be a user guide, but the PRD should reference the contract.

**Suggested addition**: Clarify R7 with an example block:
```
R7 example - The credential entry in the personal vault:

Path: /niwa/provider-auth/infisical/550e8400-e29b-41d4-a716-446655440000
Secret name: niwa_infisical_provider_auth (or similar, user-defined)
Secret value (TOML document):

version = "1"
client_id = "your-infisical-client-id"
client_secret = "your-infisical-client-secret"
api_url = "https://app.infisical.com"  # optional; omit if using Infisical Cloud default

When niwa resolves this credential, it parses the TOML body and extracts these three fields.
```

### 3. Document Opt-In Validation Order Explicitly
**Rationale**: The research document identified "parser-time vs apply-time validation" as a design choice, but the PRD doesn't commit. An implementer needs to know: does R2 (provider name validation) run during config.Parse(), or during apply graph traversal? This affects error timing and user experience.

**Suggested addition**: New requirement section or R2 clarification
```
R2c — Validation timing:
The check for [global.machine_identities] from = "<name>" against declared vault providers occurs at CONFIG PARSE TIME (mirroring vault:// URI validation in validate_vault_refs.go), not at apply time. This gives users early feedback when they mistype a provider name.

If from = "missing", the parse-time error is:
"[global.machine_identities] from = \"missing\" references unknown vault provider. Declared providers in [global.vault.*]: [<list>]"

This error is reported before any apply graph is built.
```

## Summary

The PRD is well-structured and covers most of the functional scope, but **leaves critical user-facing behavior unspecified in eight areas**: failure-mode sequencing and wording, audit output format and table structure, conflict override diagnostics, anonymous provider defaulting, schema version error messages, personal vault self-auth validation scope, vault population assumptions in user stories, and backward-compatibility guarantees for non-opting-in users. These gaps would force an implementation team to make assumptions or bike-shed decisions that could diverge from intended UX. Addressing these eight issues via expanded requirements or acceptance criteria would enable an implementer to build this feature with confidence and avoid rework.

