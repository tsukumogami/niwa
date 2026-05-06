# Testability Review: Machine Identity Vault Sync

## Verdict: FAIL (9 issues)
The acceptance criteria are incomplete and contain vague language that would prevent a test author from writing verifiable tests without consulting the PRD requirements or implementation author. Key failure modes from R13 lack explicit acceptance criteria, and several criteria use ambiguous language around exit codes, timing guarantees, and edge cases.

## Untestable Criteria

### 1. AC 6: Vault Unreachable Fallback
**Issue**: Exit code guarantee is conditional and ambiguous.
- Current text: "When the personal vault is unreachable, apply continues using whatever the local file and CLI session can supply, with a stderr warning naming the unreachable provider."
- **Problem**: Does not specify exit code when vault is down AND a credential is missing (not in local file, not in CLI session).
- **R13 contract**: "Apply continues. Exit code 0 (apply succeeds if no required credentials are missing)"
- **Testability gap**: A test needs to know: should exit 0 always, or only when all credentials are available locally?

**How to fix**: Clarify the exit-code contract in AC 6 with two explicit subscenarios:
- "When vault is unreachable but all required credentials are available in local file or CLI session, apply continues with exit 0 and stderr warning."
- "When vault is unreachable and a required credential is missing from both local file and CLI session, apply fails with exit non-zero."

---

### 2. AC 9: Circular Auth Detection
**Issue**: "Before any vault call" is testable only by inspecting internal timing/logs.
- Current text: "When the personal vault's own `(kind, project)` matches a credential entry (local-file or vault-sourced), apply fails with a chicken-and-egg diagnostic **before any vault call**."
- **Problem**: The phrase "before any vault call" is an implementation detail. A functional test cannot verify no vault call occurred without access to logs or internal instrumentation. The exit code and error message are testable, but the timing guarantee is not.

**How to fix**: Move the timing constraint to a non-functional requirement (R9 should state this as an implementation note). Rewrite AC 9 as:
- "When the personal vault's own `(kind, project)` matches a credential entry, apply fails at parse time with a chicken-and-egg diagnostic naming the conflict."

---

### 3. AC 7 & 8: Exit Codes Unspecified
**Issue**: ACs do not explicitly state "exit non-zero."
- AC 7: "When a vault-sourced body has `version = "2"`, apply fails with a 'unsupported provider-auth schema version' error."
- AC 8: "When a vault-sourced body is missing `client_secret`, apply fails with a 'malformed provider-auth body' error naming the missing field."
- **Problem**: "apply fails" could mean "logs an error and continues" or "exits non-zero." The R13 table clearly states these are hard errors (non-zero exit), but the ACs don't say it.

**How to fix**: Add explicit exit code:
- AC 7: "...apply fails **with exit code non-zero** and a diagnostic containing 'unsupported provider-auth schema version'."
- AC 8: "...apply fails **with exit code non-zero** and a diagnostic containing 'malformed provider-auth body' and the missing field name."

---

### 4. AC 5: "Local Entry Is Used" Unverifiable Without Mock
**Issue**: How to verify a local-file credential was used without intercepting the auth backend?
- Current text: "A user with both a local-file entry and a vault entry for the same `(kind, project)` has the local-file entry used for authentication, with the vault entry visible in `niwa status --audit-auth` as `FALLBACK`."
- **Problem**: The phrase "the local-file entry is used for authentication" requires either:
  1. Mocking/controlling the Infisical auth backend to return different responses for local vs vault credentials (complex), or
  2. Inspecting internal niwa state/logs (not a functional test), or
  3. Assuming "apply succeeded" means "correct credential was used" (indirect, fragile—if both credentials happen to be valid, test passes but doesn't verify the right one was chosen).

**How to fix**: Split into two testable ACs:
- AC 5a: "When local file and vault both have entries for `(kind, project)`, apply succeeds and uses a working credential."
- AC 5b: "`niwa status --audit-auth` shows the vault entry with status `FALLBACK` and marks the local-file entry as `ACTIVE`."

---

### 5. AC 10: "Correct Source Column" Dependent on Audit Implementation
**Issue**: What counts as "correctly populated"? Behavior depends on whether audit fetches vault or reads state.json offline.
- Current text: "`niwa status --audit-auth` lists every `(kind, project)` niwa needed credentials for in the last apply, with the source column populated correctly."
- **Problem**: The phrase "source column populated correctly" presupposes the audit implementation (offline from state.json vs live vault fetch). The open question in the PRD asks: "Should `--audit-auth` trigger a vault fetch or work fully offline?"
- **Implication**: A test cannot be written without knowing the answer to this design question.

**How to fix**: Answer the open question first, then rewrite AC 10 to match:
- If offline: "...shows the source that was used in the last apply (read from state.json)."
- If live: "...shows the source that will be used in the next apply (fetched from vault now)."

---

### 6. AC 11: Stderr Signal Format Underspecified
**Issue**: "One line per credential" is ambiguous when multiple projects use the same vault provider.
- Current text: "On every apply that uses at least one vault-sourced credential, stderr carries one `auth: <kind>/<project> source=vault:<name>` line per such credential."
- **Problem**: What is a "credential" here? Does "per such credential" mean:
  - One line per unique (kind, project) pair? Or
  - One line per vault provider used? Or
  - One line per distinct {(kind, project), source} tuple?
- The R12 requirement says "one line per provider," but AC 11 says "one line per credential" without clarifying if (kind1, proj1) and (kind1, proj2) from the same vault provider are one or two lines.

**How to fix**: Clarify the grouping with an explicit example:
- "For each unique (kind, project) pair sourced from a vault provider, stderr carries exactly one line: `auth: <kind>/<project-uuid> source=vault:<provider-name>`."

---

## Missing Test Coverage

### 1. R13.2: Vault Key Absent (Silent Fallback)
**Missing AC**: When a `(kind, project)` credential is absent from the vault, niwa falls back to local-file/CLI-session silently and notes it in audit.
- **R13 contract**: "Conventional key absent in vault → Silent. Treated as 'no vault entry for this (kind, project).' Falls through to local-file / cli-session. Visible in audit."
- **Coverage gap**: No explicit AC for the happy-path silent fallback.
- **How to add**: "When a vault-sourced credential key is absent from the vault, apply continues silently, falls back to local-file or CLI-session, and the audit shows no vault entry for that (kind, project)."

---

### 2. R13.4: Credentials Well-Formed but Invalid (e.g., Rotated Secret)
**Missing AC**: When a vault entry parses correctly but the Infisical auth call fails (e.g., HTTP 401 due to rotated secret), apply fails with a clear diagnostic.
- **R13 contract**: "Body well-formed but credentials invalid → Hard error. Apply fails. Exit code non-zero. Diagnostic names the project UUID and the auth error."
- **Coverage gap**: No AC for this common failure mode (user rotates secret in vault, forgets to update provider-auth.toml, and niwa must surface the 401).
- **How to add**: "When a vault-sourced credential is well-formed but authentication fails (e.g., invalid client_secret), apply fails with exit code non-zero and a diagnostic naming the (kind, project) and the auth error."

---

### 3. R6: In-Memory Only, Re-fetched Per Apply
**Missing AC**: Vault-sourced credentials are never written to disk and are re-fetched on each apply.
- **Coverage gap**: No AC verifies that credentials are not cached or persisted.
- **How to add**: "Vault-sourced credentials are fetched into memory per apply and never written to disk. On the next apply, they are re-fetched fresh from the vault."
- **Testability note**: Would require inspecting vault call logs to verify re-fetch, or mocking vault to return different values across applies.

---

### 4. Happy-Path End-to-End Success
**Missing AC**: Credentials are successfully fetched from vault and used to authenticate a provider in an apply.
- **Current ACs 1-2**: Imply this but don't state it explicitly.
- **How to add**: "When a user opts into credential-sync with a valid vault provider containing valid Infisical credentials, apply succeeds and uses the vault-sourced credentials."
- **This AC would be the "baseline happy path"** before testing failure modes.

---

### 5. R9 Explicit Positive Case: Personal Vault Auth via CLI
**Missing AC**: The personal vault itself is successfully authenticated via the CLI session (not via a bootstrap credential in provider-auth.toml).
- **Current coverage**: Only AC 9 (negative case: error if circular).
- **How to add**: "The personal vault provider is authenticated via the CLI session (e.g., `infisical login`) and credential-sync succeeds without requiring a bootstrap entry in provider-auth.toml."

---

### 6. TOML Parse Error (Not Just Missing Field)
**Missing AC**: When a vault-sourced body is not valid TOML (syntax error), apply fails with a diagnostic naming the parse error.
- **Coverage gap**: AC 8 tests missing `client_secret`, but not malformed TOML syntax.
- **How to add**: "When a vault-sourced body has invalid TOML syntax, apply fails with exit code non-zero and a diagnostic containing the TOML parse error."

---

### 7. Body Missing client_id (Not Just client_secret)
**Missing AC**: When `client_id` is missing (only client_secret is tested in AC 8).
- **How to add**: "When a vault-sourced body is missing `client_id`, apply fails with exit code non-zero and a diagnostic naming the missing field."

---

## Testability Issues by Criterion

| AC | Testability Level | Primary Blockers |
|----|-------------------|------------------|
| 1 (Named provider opt-in) | HIGH | None; straightforward config + vault call verification |
| 2 (Anonymous opt-in) | HIGH | None |
| 3 (Missing provider error) | HIGH | None |
| 4 (No providers error) | HIGH | None |
| 5 (Local wins conflict) | MEDIUM | Can't verify "local entry used" without auth backend mock; split into 5a (happy path) + 5b (audit table) |
| 6 (Vault unreachable) | MEDIUM | Exit code guarantee is conditional; needs clarification for missing-credential case |
| 7 (Body version invalid) | MEDIUM | Missing explicit "exit non-zero" statement |
| 8 (Body missing field) | MEDIUM | Same; also doesn't test all missing fields (client_id, api_url) |
| 9 (Circular auth) | MEDIUM | "Before any vault call" is an implementation detail not verifiable in functional test |
| 10 (Audit surface) | MEDIUM | Depends on whether audit is offline or online; open question must be answered first |
| 11 (Stderr signal) | MEDIUM | "One line per credential" is ambiguous about grouping; needs explicit example |
| 12 (No behavior change) | HIGH | None; straightforward baseline test |
| 13 (No new files) | HIGH | None; straightforward filesystem check |

---

## Summary

The PRD defines the failure-mode contract clearly in R13 (6 specific cases), but only 3-4 of these have explicit acceptance criteria. Several criteria use vague language ("apply fails," "before any vault call," "correctly") that presupposes implementation details or requires clarification. Additionally, critical path scenarios—credentials successfully fetched and used, silent fallback on missing vault keys, auth failure after parsing—have no explicit ACs. A test author reading only the ACs without the PRD would not have enough information to write complete and verifiable tests. **Recommendation: Add 7-8 clarifying ACs, rewrite 6 ambiguous ones, and answer the open question about audit behavior (offline vs online) before implementation.**

