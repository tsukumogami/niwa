# Clarity Review: PRD-machine-identity-vault-sync

## Verdict: FAIL
Multiple ambiguities discovered that could lead developers to implement conflicting interpretations of vault precedence, failure handling, and audit surfaces.

## Ambiguities Found

### 1. **Precedence direction is backwards with existing vocabulary**
   - **Location**: Goals (lines 14-17), R4 (lines 179-185), D-1 (lines 426-435)
   - **The ambiguous text**: "the local file remaining authoritative when both are present" and "Local file wins on conflict"
   - **Why it's ambiguous**: The existing "shadow" vocabulary in niwa means "the shadowing layer WINS" (personal overlay shadows team; personal WINS). But here the PRD proposes "local file wins" while calling vault entries "augmentation" (D-5, lines 474-485). The Phase 2 Architecture analysis explicitly states this is an INVERSION of the personal-overlay-wins pattern. A developer reading "local file is authoritative" might infer the analogy goes: "local = personal overlay role" (which WOULD mean local wins). But the vault-sourced entry is actually closer structurally to personal overlay (distributed, per-user). The PRD introduces new vocabulary ("augmentation," "fallback") to avoid confusion, but the rationale for LOCAL-wins specifically is buried in D-1 and never explicitly justified against the alternative.
   - **Suggested clarification**: Add to Goals or R4: "Precedence is LOCAL-FILE > VAULT > CLI-SESSION. This inverts the personal-overlay-vs-team precedence (where personal wins). We chose local-wins because local-file is the per-machine override layer, and vault is the per-user distribution layer. Vault entries AUGMENT (supplement) local entries; they are never authoritative." Explicitly state which scenario each conflict policy serves (local-wins = developer debugging flexibility; vault-wins = centralized rotation, deferred to v1.1 if needed).

### 2. **Vault unreachability behavior is described with soft language**
   - **Location**: R13 (lines 266-275), Problem statement (line 270)
   - **The ambiguous text**: "Warn to stderr; fall back to local-file / cli-session for every entry."
   - **Why it's ambiguous**: The word "warn" is singular and passive. Does it mean:
     - One aggregate stderr line for the entire vault provider, OR
     - One warning per (kind, project) pair that would have been sourced from vault, OR
     - A single warning that vault is unreachable and the apply is proceeding local-only?
   - The Phase 2 Ops/UX analysis (Lead 5) explicitly flags this as an open question: "Should multiple warnings emit (one per missing credential) or a single aggregate message?" No design decision is documented.
   - **Suggested clarification**: "When the personal vault is unreachable (network down, CLI not installed, not logged in), niwa emits a SINGLE warning line to stderr: 'warning: personal vault provider <name> unreachable; falling back to local-file and cli-session credentials.' Apply continues. If local-file entries cannot satisfy all required (kind, project) pairs, apply fails with the standard missing-credential error at the backend-auth step, not at the vault-fetch step."

### 3. **Audit surface can work offline OR online, but the choice is deferred**
   - **Location**: R11 (lines 242-256), Open Questions (lines 386-393)
   - **The ambiguous text**: The entire R11 describes `niwa status --audit-auth` without saying whether it fetches from vault or reads from state.json. The open question (line 392-393) says "Probably want offline by default with an opt-in `--check-vault` analog. Decision deferred to design."
   - **Why it's ambiguous**: A developer cannot implement R11 without knowing the offline/online choice. Does `niwa status --audit-auth` show what the LAST apply did (offline, reads state.json) or what the NEXT apply WILL see (online, fetches from vault now)? These could produce different results if:
     - Vault entries were added/changed since the last apply
     - Vault entries were deleted since the last apply
     - A local-file entry was added that shadows an existing vault entry
   - The acceptance criterion (line 345-347) says the audit "lists every (kind, project) niwa needed credentials for in the last apply," which implies OFFLINE/historical. But the requirement doesn't explicitly forbid online interpretation.
   - **Suggested clarification**: "R11 AUDIT (offline). `niwa status --audit-auth` reads the credential sources from the most recent apply (stored in state.json + provider-auth.toml snapshot). It does NOT fetch from vault. If you want to verify that vault entries exist and are well-formed, use `niwa vault check-auth` (future feature, deferred to v1.1)."

### 4. **Body validation happens at different points for different errors**
   - **Location**: R8 (lines 217-223), R13 (lines 266-275)
   - **The ambiguous text**: R8 says "niwa reads the `version` field of the fetched body and rejects any value other than `"1"`" with a "clear diagnostic." R13 separates "Body malformed" (hard error) from "Body well-formed but credentials invalid" (hard error from backend). But WHEN does validation happen? During fetch? During parse? Only when credentials are actually needed? What if vault has an entry but it's never used in the apply (e.g., a project isn't referenced)?
   - **Why it's ambiguous**: The wording "niwa reads" and "rejects" doesn't specify the lifecycle. Does niwa:
     - Eagerly fetch and validate ALL entries declared in vault (expensive, N × 200ms)?
     - Lazily fetch only when needed (cheaper, but errors surface mid-apply)?
     - Validate on every apply regardless of usage (wastes time for unused entries)?
   - Phase 2 Architecture research (lead 3) states: "The no-cache stance costs ~200ms per provider per apply. For workspaces with many orgs, this adds up." This implies lazy evaluation, but the PRD never explicitly says so.
   - **Suggested clarification**: "R6 IN-MEMORY + LAZY FETCH. niwa fetches a vault-sourced credential entry only when a workspace references the corresponding (kind, project) pair during apply. Validation (version field, required TOML fields) happens at fetch time. Unused entries are never fetched from vault. This minimizes latency for workspaces that don't use all declared providers."

### 5. **FALLBACK vs ACTIVE labeling in audit is under-specified**
   - **Location**: R11 (lines 254-256)
   - **The ambiguous text**: "When the same `(kind, project)` has entries in both the local file and the vault, both are shown with the local-file source marked **ACTIVE** and the vault entry marked **FALLBACK**."
   - **Why it's ambiguous**: The text table format is never fully specified. The Phase 2 Ops/UX analysis describes the structure as "columns: KIND / PROJECT / SOURCE / SHADOWED" but doesn't say HOW the ACTIVE/FALLBACK markers appear. Are they:
     - Separate rows with the same (kind, project), one marked "local-file [ACTIVE]" and one marked "vault:name [FALLBACK]"?
     - Different rows, different (kind, project) pairs (confusing)?
     - A single row with SOURCE = "local-file [ACTIVE] + vault:name [FALLBACK]"?
     - A separate SHADOWED column that says "vault:name (FALLBACK)" when a vault entry exists?
   - The acceptance criterion (line 333) says "with the vault entry visible in `niwa status --audit-auth` as `FALLBACK`" but again doesn't specify the column structure.
   - **Suggested clarification**: Add to R11: "Example output for a (kind, project) pair with both sources:\n  infisical | <uuid> | local-file [ACTIVE] | — \n  infisical | <uuid> | vault:personal [FALLBACK] | yes (local overrides)\nOr, in the SHADOWED column:\n  infisical | <uuid> | local-file | yes (vault:personal is fallback)" [Choose one and document clearly.]

### 6. **R2 validation timing is ambiguous (parse vs apply)**
   - **Location**: R2 (lines 163-168)
   - **The ambiguous text**: "When `[global.machine_identities] from = "X"` is set, niwa validates at config-parse time that `X` matches a declared provider name in the same file."
   - **Why it's ambiguous**: "Config-parse time" could mean:
     - When the global overlay file is loaded (before any workspace is loaded) → error surfaces immediately if you run ANY niwa command
     - When the workspace config is merged with the global overlay (during apply setup) → error only surfaces during apply
     - When a workspace is chosen (during resolver setup) → depends on which workspace is being applied
   - Phase 2 Codebase analysis states the existing `vault_scope` validation is deferred to apply time (resolver), but R2 says "parse time" and "fails apply with a diagnostic," which is slightly different. The phrase "mirrors `internal/config/validate_vault_refs.go`" suggests parse-time validation, but that code is for vault:// URIs in config blocks, which is different from a top-level config field.
   - **Suggested clarification**: "R2 PARSE-TIME VALIDATION. When `niwa apply` loads the global overlay, the config parser validates that `from = "X"` references a declared provider in the same file's `[global.vault.provider]` or `[global.vault.providers.*]` blocks. If the provider doesn't exist, the parser fails with a diagnostic before the resolver runs: `error: machine-identity-vault-sync references provider "X" but only providers [Y, Z] are declared in this file.`"

### 7. **The term "should" is used non-normatively in multiple places**
   - **Location**: Glossary (line 35), Open Questions (line 393), Known Limitations (lines 407-408)
   - **The ambiguous text**: "Already may declare" (line 39), "probably want offline by default" (line 392-393), "Bootstrap requires `infisical login`" (line 407)
   - **Why it's ambiguous**: "May," "probably," and "requires" are not consistent normative language. The requirements section should use MUST/SHOULD/MAY (RFC 2119 style). For instance:
     - Line 35: "the user-owned configuration repo registered via `niwa config set global <slug>`" — is this a requirement or description?
     - Line 392-393: "Probably want offline by default" is an open design question, not a requirement. It shouldn't appear in R11.
     - Line 407: "Bootstrap requires `infisical login`" is a known limitation, not a requirement. Good to document, but the phrasing suggests it's a design choice rather than a constraint.
   - **Suggested clarification**: 
     - Move the open design question (offline vs online audit) OUT of R11 and into a separate "Open Requirements" section (or delete it from the requirement, keep in Open Questions).
     - Use consistent language: "niwa MUST validate the provider name at parse time" instead of "validates."
     - Use "Known Limitation" label explicitly: "LIMITATION: Personal vault authentication currently requires running `infisical login` first; zero-step bootstrap is not possible in v1."

### 8. **"As needed" and "appropriate" appear in non-functional requirements**
   - **Location**: R16 (line 300)
   - **The ambiguous text**: "Any implementation must measure and document the actual budget." and "bounded by N × (one Infisical export call + one universal-auth login) ≈ N × 200ms."
   - **Why it's ambiguous**: R16 says the latency budget is "bounded by," then gives an estimate "≈ 200ms," then says "Any implementation must measure and document the actual budget." This creates three conflicting statements:
     - Is 200ms per provider a HARD upper bound (must never exceed)?
     - Is it an estimate for planning (actual may vary)?
     - Is "measure and document" a requirement to prove compliance, or a recommendation?
   - A developer might implement an optimization that takes 250ms per provider and claim "the estimate was approximate" or might spend engineering time to stay under 200ms strictly. Neither is clearly right.
   - **Suggested clarification**: "R16 LATENCY BUDGET (informative, not binding). Current estimate: N × 200ms for a workspace with N vault-sourced providers. Implementation should aim to stay within this budget but may exceed it with justification. The implementation team MUST document actual measured latency and identify any optimization opportunities for v1.1."

### 9. **Precedence between local-file version and vault version is not covered**
   - **Location**: R7 (lines 200-210), R8 (lines 217-223)
   - **The ambiguous text**: "The credential body for a given project is stored at the path `/niwa/provider-auth/infisical/<project-uuid>` as a single secret value whose body is a TOML document with the shape: ... version = "1" ..."
   - **Why it's ambiguous**: What if the local-file entry has `version = "2"` (user hand-edited the file with a future schema) and the vault entry has `version = "1"`? Or vice versa? R8 says "rejects any value other than `"1"`" but doesn't say which entry's version field is checked:
     - Only the vault entry's version? (local file is not parsed, just passed as-is to auth)
     - Both? (errors if either has version != "1")
     - Only when the vault entry is USED (i.e., local-file wins, so its version is not checked)?
   - Phase 2 Codebase analysis notes that local-file credentials are parsed via `toml.Unmarshal` but vault entries are opaque `secret.Value` bytes that need unmarshaling. This implies vault entries are parsed, but the PRD doesn't explicitly say WHEN.
   - **Suggested clarification**: "R8 VERSION VALIDATION (vault entries only). When niwa fetches a vault-sourced credential entry, it parses the TOML body and checks that `version = "1"` is present and has value `"1"`. Vault entries with other versions are HARD ERRORS (apply fails) so users know to upgrade niwa. Local-file entries are not version-checked; users can edit them by hand without strict schema enforcement."

### 10. **R12 (stderr signal for vault credentials) has no aggregation rule**
   - **Location**: R12 (lines 258-264)
   - **The ambiguous text**: "On every apply that uses at least one vault-sourced credential, niwa emits a stderr line per provider listing the source it used. Shape: `auth: <kind>/<project-uuid> source=vault:<name>`."
   - **Why it's ambiguous**: 
     - "A stderr line per provider" — does this mean per provider NAME (e.g., "vault:personal") or per unique (kind, project) pair?
     - If five (kind, project) pairs all come from the same vault:personal provider, do we emit five lines or one?
     - What if a workspace has 20 providers and 15 are vault-sourced — does emitting 15 lines count as "noise" or "diagnostic value"?
   - Phase 2 Ops/UX analysis (Lead 2, line 40) explicitly flags this as an open question: "Should the apply-time stderr signal (R12) be aggregated or per-provider? Per-provider is more verbose but clearer; aggregated is cleaner. Probably per-provider for diagnostic value, but worth confirming with users."
   - This should not appear in R12; it should be deferred to design.
   - **Suggested clarification**: "R12 (DEFER AGGREGATION CHOICE). On every apply that uses at least one vault-sourced credential, niwa emits a signal to stderr. The aggregation strategy is TBD: (a) one line per (kind, project) pair, or (b) one line per vault provider name with a count. Design will decide based on user feedback. PROVISIONAL SHAPE: `auth: <kind>/<project-uuid> source=vault:<name>` (per-provider option)."

### 11. **R9 validates "personal vault must auth via CLI session" but the enforcement is unclear**
   - **Location**: R9 (lines 225-234)
   - **The ambiguous text**: "if the personal vault's `(kind, project)` matches an entry in the local credential pool (local-file or vault-sourced), niwa fails apply with a diagnostic describing the chicken-and-egg cycle"
   - **Why it's ambiguous**: 
     - Does "matches" mean exact match (same kind AND same project UUID)?
     - Can a vault provider be for a different (kind, project) than what it authenticates itself with? (Yes — the vault provider could be an Infisical org used only for storing machine-identity secrets, different from the org whose credentials you're bootstrapping.)
     - The phrase "personal vault's `(kind, project)`" assumes the vault provider has a (kind, project) identifier, but the Phase 2 Codebase analysis doesn't confirm this. Vault providers have `kind` (e.g., "infisical") and `project` (UUID), but are these always uniquely identifying?
     - Example: Suppose vault provider is (kind=infisical, project=personal-uuid) and local file has an entry (kind=infisical, project=personal-uuid). Do we error? Or if local file has (kind=infisical, project=team-uuid), do we NOT error?
   - **Suggested clarification**: "R9 ANTI-BOOTSTRAP-CYCLE VALIDATION. If the personal vault provider's declared `(kind, project)` matches ANY entry in the local credential pool (both local-file and vault-sourced), niwa fails apply before fetching from vault. Diagnostic: 'personal vault provider (kind=<k>, project=<p>) cannot be bootstrapped by an entry in the local credential pool; this creates a chicken-and-egg cycle. Authenticate the personal vault via CLI session (infisical login for Infisical) instead.' Example: If vault provider is (infisical, uuid-ABC) and local file contains a (infisical, uuid-ABC) entry, this fails. If local file contains (infisical, uuid-XYZ) for a different org, no error."

### 12. **"Opt-in" is never formally defined; does declaration without `from` count as opt-in?**
   - **Location**: Goals (line 97), R1 (lines 150-161), D-7 (lines 500-510)
   - **The ambiguous text**: "eliminate the 'edit provider-auth.toml on every machine' step for developers who opt in to credential sync" and "When the table is absent, the feature is disabled."
   - **Why it's ambiguous**: 
     - R1 says "empty / unset" means use anonymous provider OR error. This implies declaring `[global.machine_identities]` with an empty `from` field IS opting in.
     - But does declaring the section without populating any vault entries count as "opted in"? Phase 2 Codebase analysis implies the validate-at-parse-time rule means an empty `from` with no anonymous provider is an error, so the feature IS activated (not silently disabled).
     - The distinction between "feature is disabled" (line 161) and "feature is enabled but has no provider" (error) is not clear in the PRD, only in the analysis.
   - **Suggested clarification**: "R1 OPT-IN RULES. The feature activates if and only if `[global.machine_identities]` is declared in the personal overlay. When present: if `from` is unset or empty, niwa uses the anonymous `[global.vault.provider]` if declared (error if not). If `from = "<name>"`, niwa uses `[global.vault.providers.<name>]` (error if not found). When `[global.machine_identities]` is absent, the feature is disabled; niwa behaves identically to today's releases."

## Suggested Improvements

1. **Add a "Terminology and Precedence" section**: Define LOCAL > VAULT > CLI-SESSION explicitly, explain why local-wins (vs vault-wins), and justify against the personal-overlay-wins analogy from vault-integration PRD.

2. **Separate Open Design Decisions from Requirements**: R11 includes an open question about offline/online behavior. Move this to a "Design Decisions Pending" section. Acceptance criteria should not depend on unresolved design choices.

3. **Normalize language to RFC 2119**: Replace "may," "should," "probably," "appropriate," "as needed" with MUST/SHOULD/MAY. Define what "audit-auth" does offline vs online explicitly; don't defer in the requirement itself.

4. **Specify error behavior for vault unreachability per-use-case**: Is the single "warn and proceed" message (R13) the right UX for a fresh machine (many missing entries) vs a rotation scenario (few new entries)? Design may need two different messages or a `--quiet` flag.

5. **Define the exact text-table format for `niwa status --audit-auth`**: Include a concrete example with both local-file and vault entries for the same (kind, project) pair so developers can implement the exact column layout.

6. **Make latency budget informative, not prescriptive**: R16 currently mixes hard boundary ("bounded by") with soft estimate ("≈ 200ms") with recommendation ("document the actual budget"). Pick one stance or clearly label each.

7. **Clarify version validation scope**: Specify that vault-sourced entry version fields are validated but local-file entries are not (or vice versa). This affects error UX significantly.

8. **Document R12 aggregation decision explicitly**: The open question about per-provider vs per-credential aggregation MUST be decided before implementation. Add it to Decisions and Trade-offs with a chosen option, not to R12 itself.

9. **Add examples for R9 chicken-and-egg validation**: Give concrete (kind, project) examples so implementers know exactly which overlaps trigger the error and which don't.

10. **Define "at parse time" vs "at apply time" consistently**: The PRD uses both phrases. Clarify: parse time = when global overlay file is loaded (immediate error if you run `niwa apply`), apply time = during workspace setup (error surface during specific `niwa apply` invocation).

## Summary

The PRD introduces new vocabulary ("augmentation," "fallback") to avoid collision with existing "shadow" semantics, which is good. However, the precedence direction (local-file > vault) is not explicitly justified against the personal-overlay-wins precedent from vault-integration, creating risk that developers misunderstand the design rationale. Vault unreachability error handling, audit-surface design (offline vs online), and body validation timing are partially deferred to "design" without clear placeholders in the requirements, making acceptance criteria ambiguous. Key failure scenarios lack concrete examples. The latency budget mixes prescriptive and informative language. Normative language (MUST/SHOULD) is inconsistent. **The PRD is implementable but will likely require clarification meetings during development.**

