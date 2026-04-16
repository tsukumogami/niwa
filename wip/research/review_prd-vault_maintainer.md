# Maintainer Review: PRD-vault-integration.md

Reviewing through the lens of: can a contributor picking this up six months from now form a correct mental model and implement/modify with confidence?

Overall the PRD is unusually thorough — the Decisions-and-Trade-offs section does most of the work of future-onboarding well, and the acceptance criteria are line-item testable. The findings below target places where the document's internal cross-references, naming, or surprise-edges would make a future contributor re-derive knowledge that the PRD already has elsewhere.

---

## MUST-FIX

### MF-1. Q-2 is self-contradictory: labelled "Open" but body says "RESOLVED"
Q-2's first line reads `RESOLVED: both Infisical and sops+age ship in v1.0`, but Q-2 sits under the heading "Open Questions: Questions to resolve before the PRD transitions to Accepted." A future reader will not know whether this question is gating PRD acceptance or settled. The sub-question about sequencing is also not an "open question" in the PRD sense — it's an implementation-ordering concern. **Fix:** move the resolved portion into D-1 as a "first-implemented backend" note, and either drop Q-2 or rewrite it as a focused question ("Which backend lands first if we must sequence?"), not a mixed resolved/open entry.

### MF-2. R4's "source org name" vs R5's "vault_scope" vs D-2's "scope" use three names for the same concept without a glossary tying them together
R4 says "keyed by the workspace's source org name." R5 introduces `vault_scope` as an override. D-2 uses the word "scope" throughout. US-3 ends with "Scoping is automatic because the workspace has one source." A contributor reading R5 first will not know the default scope is `ws.Sources[0].Org` without jumping to R4. A contributor reading D-2 will see "source org" and "scope" used interchangeably and have to reconstruct that `vault_scope` is a free-form string that happens to default to the source org name, not a typed reference to a source. **Fix:** add one sentence in R4 or R5 of the form: "The resolved scope value — whether the implicit `ws.Sources[0].Org` or the explicit `vault_scope` string — is referred to as the *scope key* throughout this PRD; it is a free-form string, not a validated source reference." Then use "scope key" consistently in R4, R5, D-2, and Q-5.

### MF-3. R34's "`[env.required]` is NOT downgraded by `--allow-missing-secrets`" is the kind of rule the next developer will "simplify" away
R34 is the surprising-behavior rule — a reasonable refactor would unify the two code paths (unresolved vault ref + missing required key) into a single "missing value" handler, and `--allow-missing-secrets` would then accidentally bypass `[env.required]`. The rationale is stated once in R34 and once in the Acceptance Criteria. **Fix:** add a comment-to-future-maintainer in R34: "Rationale: these are two different failure classes. `--allow-missing-secrets` is about vault *availability* (I can't reach my provider right now). `[env.required]` is about team-declared *load-bearing-ness* (this key is necessary for the workspace to function, regardless of why it's missing). A unified handler that conflates them would silently weaken team-declared requirements." Also add a test-name hint in the acceptance criteria: `Test_AllowMissingSecrets_DoesNotBypassEnvRequired`.

### MF-4. R3's "Reference-accepting locations" list is the only place the full allow-list appears; the inverse list appears nowhere organized
R3 inlines both "references are accepted in..." and "references are NOT accepted in..." in narrative form mid-requirement. A future contributor adding a new TOML table (say, `[hooks.env.vars]`) must re-derive whether vault refs are accepted there. The acceptance-criteria schema bullets cover the current inventory but don't give a rule for extensions. **Fix:** add a one-line extensibility rule in R3: "New TOML tables that carry user-supplied string values MUST declare their vault-acceptance policy explicitly; the default is NOT accepted unless documented." This makes it a maintenance contract, not a static list.

### MF-5. "Rendezvous names" appears only in D-9's alternatives with no definition
D-9 rejects "shared rendezvous names" without defining the term. The narrative that follows uses "rendezvous" as if it were a known term of art. A contributor reading D-9 to understand *why* the file-local scoping rule exists will bounce off this. **Fix:** inline-define it on first use, e.g., "(a) shared 'rendezvous' names — both configs agree on a vault provider name like `personal`, and the team config writes `vault://personal/github-pat` trusting the user overlay to supply a matching provider."

---

## SHOULD-FIX

### SF-1. "personal overlay" vs "personal config" vs "personal config repo" vs "GlobalOverride" vs "GlobalConfigOverride"
Five terms for the user-scoped layer across the document:
- US-3 uses "personal config"
- US-9 uses "personal overlay"
- R4 uses "GlobalConfigOverride.Workspaces"
- R12 uses "GlobalOverride struct" and "VaultRegistry"
- D-3 uses "MergeGlobalOverride"

A contributor won't know whether `GlobalOverride` and `GlobalConfigOverride` are the same type (they appear to be — R12 names the Go struct `GlobalOverride`, R4 names it `GlobalConfigOverride`). **Fix:** pick one canonical name for the Go type (check the existing codebase) and use it everywhere a Go identifier is meant; use "personal config" for the user-facing concept and drop "personal overlay" or define both.

### SF-2. "team config" sometimes means the file, sometimes the repo, sometimes the layer
- US-1: "move the repo to public" — "team config" = repo
- R7: "supplied by both the team vault and a personal vault" — "team" = layer
- R8: "A team workspace config MAY declare..." — "team config" = file
- D-9: "team configs cannot write `vault://personal/...`" — "team config" = the committed content

**Fix:** introduce a mini-glossary near the top of the PRD: "team config repo" (the `org/dot-niwa` git repo), "team config file" (the `.niwa/workspace.toml` inside it), "team layer" (the resolved config merged from team sources). Use these three terms consistently.

### SF-3. US-3's description string surfacing is implicit; R33 makes it explicit; the link between them is not stated
US-3's example uses `GITHUB_TOKEN = "GitHub PAT with repo:read scope"` without explaining that `"GitHub PAT with repo:read scope"` is a description that gets surfaced in error messages. R33 later states the rule, and an acceptance-criterion restates it. A reader seeing US-3 in isolation might think the string is just a TOML comment-substitute. **Fix:** add one sentence to US-3's example block: "(The description string on the right of `=` is not a placeholder — it appears verbatim in `niwa apply`'s missing-key diagnostic, per R33.)"

### SF-4. No US-level example for US-7 (team_only) or US-5 (rotation drift distinction)
US-3 has a full worked example; US-9 has a three-path example. US-5's rotation-vs-user-drift distinction is the feature with the highest misread-risk (a contributor who doesn't understand `SourceFingerprint`'s purpose could conflate it with the content hash and lose the `stale` vs `drifted` signal). US-7's `team_only` has three distinct failure modes (parse-time, resolve-time, error-message-distinct-from-auth-failure). **Fix:** add a minimal worked example to US-5 (two-line TOML + two-line output showing `stale path/to/.env.local` vs `drifted path/to/.env.local`) and US-7 (team config with `team_only = ["TELEMETRY_ENDPOINT"]`, personal config trying to shadow it, resulting error). US-3's example-density is the right bar; the lighter stories are below it.

### SF-5. R6's resolution chain ordering is stated only in prose, not in pseudocode or a diagram
R6 reads: "personal-scoped → personal-default → team." The next developer implementing this will want to see it as pseudocode to be confident about fallback semantics — e.g., does a personal-scoped provider *failure* (auth error) continue to personal-default, or fail the whole resolution? Does "first successful lookup wins" mean first provider returning non-empty, or first provider returning without error? **Fix:** expand R6 with three pseudocode-level bullets: "(1) Provider-not-declared at a layer → skip to next layer. (2) Provider declared but fails to authenticate → hard error, do NOT fall through. (3) Provider reachable but key not found → skip to next layer."

### SF-6. R15's fingerprint contents are under-specified
R15 says the fingerprint "captures the resolution inputs (config reference + vault version/etag metadata)." A contributor will ask: do all backends expose etag metadata? What happens for a sops backend where "version" is the git commit of the `.sops.yaml`? For Infisical, is it the secret's `updatedAt`, its version number, or a content digest? If the metadata shape varies, how does `niwa status` compare fingerprints across backends? **Fix:** either name the specific field per backend (sops → git SHA of containing file, Infisical → secret version number from API) or add an explicit acceptance criterion: "`SourceFingerprint` opacity: niwa compares fingerprints byte-for-byte; backends choose their own fingerprint encoding, but the encoding MUST be deterministic for an unchanged upstream secret."

### SF-7. Q-1 and Q-8 are process questions, not PRD-content questions
Q-1 ("are there other archetypes?") and Q-8 ("does this need security-review sign-off?") are workflow-gating questions, not design questions. Mixing them with Q-3/Q-5/Q-7 (which are substantive design questions) makes it harder to see what remains to decide about the design itself. **Fix:** split Open Questions into "Design questions" (Q-3, Q-5, Q-7) and "Process / sign-off questions" (Q-1, Q-8), or move the sign-off questions to a separate section.

---

## NIT

### N-1. "12 'never leaks' invariants" — the count is stated, but counting R21–R32 gives exactly 12; future additions will break the literal-number reference
Goals and R21's section header both name the number. If R33 (or R35) ever adds a 13th invariant, three places in the document need updating. **Fix:** either drop the literal count ("the 'never leaks' invariants") or add a comment: "This count MUST be updated if INV-* requirements are added or removed."

### N-2. "OOTB" is used in Goals and R1 without being expanded on first use
"Out Of The Box" is the likely expansion but not stated. External contributors (the very audience the PRD is enabling) may not recognize the acronym. **Fix:** expand on first use.

### N-3. "ABI" is not used in this PRD
(Task brief mentioned it as a potential undefined term; confirmed it does not appear in the document. No action.)

### N-4. US-9's three paths are numbered (1)(2)(3) in the story body but R9's remediation pointers reference "US-9's three paths" without re-listing. R9's error message is the user's first contact with these paths; forcing them to read US-9 to understand the error is a minor friction
**Fix:** in R9, briefly name the three paths inline: "(a) replace the team provider in personal overlay, (b) shadow the specific key, (c) `--allow-missing-secrets`."

### N-5. `[files.required]` / `[files.recommended]` / `[files.optional]` semantics are stated once in R33 and once in the acceptance criteria; the diagnostic-message shape is only specified for env keys
R33 says "where the table keys are file destinations and the description strings document why the file is expected." What does "missing a required file" look like at apply time — a file that didn't materialize? A source that failed to read? **Fix:** add one acceptance criterion for the files-level case or state in R33 that files-table diagnostics follow the same three-level policy as env.

### N-6. D-2's alternative (c) "per-provider default + key namespacing convention" is cryptic
One sentence of rationale would help: what did this alternative look like concretely, and what was the silent coupling? **Fix:** add a half-sentence example or drop the alternative if it's not load-bearing for the decision.

### N-7. "Same binary, same command, two backends resolved in one pass" (US-3 closing) is a nice summary phrase but assumes the reader has internalized R6's resolution chain
Minor redundancy is fine — but a future reader who jumps straight to US-3 won't know what "two backends" means in the resolution model. **Fix (optional):** add a pointer to R6.

---

## Notable strengths (not findings — flagging things not to lose in revision)

- The Decisions section with explicit "Alternatives considered" is exactly the shape that pays off six months later when someone asks "why isn't it X?" — preserve this format in future PRDs.
- The 12 numbered INV-* invariants each have a distinct mnemonic name (`INV-NO-ARGV`, `INV-REDACT-LOGS`, etc.). This is strong — test names and commit messages can reference them directly.
- Acceptance Criteria grouping by subsystem (Schema / Resolution / Backends / Materialization / Security / Audit / Rotation / Bootstrap) maps cleanly to test-file organization. Keep this structure.
- R17's `raw:` escape rationale being recorded in D-8 means a future contributor proposing a simpler backslash-escape has the rejection already documented. This is exactly the job of D-* entries.
