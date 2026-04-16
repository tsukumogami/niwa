# PRD Vault Integration — Pragmatic Review

Reviewer: tsukumogami-pragmatic-reviewer
Date: 2026-04-13
PRD: `docs/prds/PRD-vault-integration.md` (Draft, v0.7 target)

Lens: catch over-engineering, YAGNI, scope creep, speculative abstractions, and over-specified behavior. Flag by severity: MUST-FIX / SHOULD-FIX / NIT.

---

## MUST-FIX (blocks acceptance)

### M-1. Ship one backend in v1; defer the other to v1.1.
Two peer backends in v1 doubles surface area (docs, bootstrap walkthroughs, acceptance criteria, CI, error-message quality bars) for a feature that has zero users today. Q-2 itself admits sequencing doesn't change the v1 story and leans Infisical-first. The "pluggable interface from v1" is already the right abstraction commitment — that's what makes the second backend cheap later. Shipping both at once is the expensive version of "we'll need it eventually."
**Fix:** Pick one (Infisical for OAuth-bootstrap payoff, or sops for zero-dependency self-host). Ship it. Put the other in v1.1 with a dated issue. Keep R1 pluggable-interface language intact.

### M-2. `[files.required] / [files.recommended] / [files.optional]` has no caller in the stated user stories.
R33 extends the three-tier requirement pattern to files, but none of US-1 through US-9 exercise file-level requirement declaration. The only files mentioned are CLAUDE.md/settings.local.json (materialized outputs, not inputs) and `.env` files in `[env.files]` (explicitly out of scope for vault). This is speculative.
**Fix:** Drop `[files.required/recommended/optional]` from R33 and the schema acceptance list. Leave `[env.*]` and `[claude.env.*]`; cut the rest unless a story materializes.

### M-3. Four scopes x three tiers = 12 new tables; only two scopes have user stories.
R33 mandates the required/recommended/optional pattern under `[env.*]`, `[claude.env.*]`, `[repos.<name>.env.*]`, `[instance.env.*]`, and `[files.*]`. The stories demonstrate the need at `[env.*]` (US-3, US-9) and implicitly at `[claude.env.*]` (team-supplied Anthropic keys). `[repos.*.env.*]` and `[instance.env.*]` three-tier tables have no grounded story — they're there because the pattern exists elsewhere.
**Fix:** Ship the three tiers under `[env.*]` and `[claude.env.*]` only. Per-repo and per-instance env can keep `[repos.<name>.env.vars]` / `[instance.env.vars]` as plain binding tables for v1. Add the three-tier split when a story demands it.

### M-4. R17 + D-8 (`raw:` escape) solves a hypothetical collision.
The rationale for `raw:vault://...` is "covers the edge case where a non-niwa tool uses `vault://` for its own URI scheme." No such tool is named. No user story hits it. The envelope is `[env.vars]` — env var values that happen to start with `vault://` in the wild don't exist in the known problem space. Every user of niwa today already knows `vault://` is niwa's scheme.
**Fix:** Drop R17, D-8, and the `raw:` acceptance criterion. If a collision ever arises, add it then. Saves parser code, docs, and test surface.

### M-5. R34 (required beats `--allow-missing-secrets`) over-specifies a flag interaction that the names already communicate.
If a key is declared `[env.required]`, it's required. A flag called `--allow-missing-secrets` shouldn't override team-declared requirements — users won't expect it to. Spelling this out as a separate requirement with its own acceptance criterion inflates the spec for no behavioral clarity gain. It's implementation guidance pretending to be a requirement.
**Fix:** Fold into R10 as a single sentence: "`--allow-missing-secrets` downgrades unresolved `vault://` references; it does not bypass `[env.required]` misses." Drop R34 as a standalone requirement and the dedicated acceptance test.

---

## SHOULD-FIX (worth addressing before implementation)

### S-1. 12 "never leaks" invariants conflate leak-prevention with "good hygiene."
R21–R32 bundles: argv rejection (R21), redaction types (R22), no-writeback (R23), file mode (R24), `.local` infix (R25), gitignore (R26), no CLAUDE.md interp (R27), no status content (R28), no os.Setenv (R29), no disk cache (R30), explicit subprocess env (R31), public-repo guardrail (R32). The threat model the PRD actually claims is "don't leak secrets to git / world-readable files / shell history." R28 (status shows path+status only) is good UX but isn't a leak prevention — `niwa status` runs on the user's own machine, reads files the user already has, and showing content is not a new exposure surface. R25 (`.local` infix naming convention) and R26 (gitignore merge) are a single invariant presented as two. R31 (subprocess env filtering) has no v1 caller — there are no hook scripts in v1.
**Fix:** Collapse R25+R26 into one "materialized secret files are gitignored by convention." Drop R31 until hook scripts exist. Reframe R28 as a UX principle, not a security invariant.

### S-2. R14 + R32 public-repo guardrail depends on GitHub URL parsing.
Detecting "public remote" requires an API call or URL pattern match, which R14 pins to GitHub in v1 and the out-of-scope list confirms (GitLab/Bitbucket deferred). This means the guardrail is partial and silently fails for non-GitHub hosts. The guardrail also duplicates work that `--audit-secrets` (R13) already does, just enforced instead of advisory.
**Fix:** Decide whether the guardrail is mandatory-but-partial (document the gap loudly and pin an issue for other hosts) or make `niwa apply` refuse when vault is configured AND plaintext values exist, regardless of remote — simpler, safer, no URL parsing. Pick one.

### S-3. US-7 `team_only` has an unresolved enforcement layer (Q-7) and conflict with US-9 path 2.
US-7 locks keys from personal shadowing. US-9 path 2 offers "override individual secret refs at the key level." These collide for any key in `team_only` — the PRD addresses it with a distinct error message (US-9 last paragraph), but Q-7 hasn't decided whether enforcement is parse-time or runtime. A feature with unresolved enforcement semantics shouldn't block on acceptance.
**Fix:** Resolve Q-7 before accepting. Runtime (materialize-time) enforcement is the simpler answer and doesn't require `niwa status` to fetch the team repo.

### S-4. R11 (`?required=false` query parameter) duplicates `[env.optional]`.
Two mechanisms now express "this secret is optional": (a) declare the key in `[env.optional]`, (b) tack `?required=false` onto the URI. They target different layers (key-level vs reference-level), but in practice the team config author controls both, and having two syntaxes for "optional" invites divergence. `?required=false` is also URI-syntax weight for a rare case.
**Fix:** Keep `[env.optional]` as the canonical mechanism. Drop R11 and the per-URI query parameter. If a user needs per-reference optionality without a key-level declaration, they can declare the key in the optional tier.

### S-5. R15 `SourceFingerprint` is coupled to vault metadata that two backends may not expose uniformly.
`SourceFingerprint` captures "vault version/etag metadata." Infisical's API gives versioned secrets with explicit etags; sops+age decrypts a local file whose "version" is the file's git-tracked content hash. The semantics are not the same thing — the fingerprint will end up being a provider-specific blob, and `niwa status` has to render both uniformly. The `stale` vs `drifted` distinction is elegant but depends on the fingerprint actually differentiating upstream rotation from local edit across backends.
**Fix:** Confirm during design-doc phase that both backends can produce a meaningful fingerprint. If sops's fingerprint is just the encrypted file's hash, the `stale` state collapses into `drifted` for sops users and the UX promise doesn't hold. Worth validating before writing the acceptance test.

### S-6. Anonymous vs named provider declaration (R2, D-11) adds two syntaxes where one would do.
D-11 rejects "always require naming" as "ceremony for the 80% case." But the ceremony is one line: `[vault.providers.default]` vs `[vault.provider]`. In exchange, the parser handles two shapes, error messages must disambiguate which mode a file is in, and docs must explain both. The rationale cites mixing being a parse error — which is itself a failure mode that wouldn't exist with a single syntax.
**Fix:** This is a judgment call; the current choice is defensible but adds complexity. If kept, ensure the design doc specifies which syntax the scaffolded `niwa init` template uses (picking named-with-default would simplify doc examples). If dropped, require naming always and move on.

---

## NIT (optional polish)

### N-1. 70+ acceptance criteria have duplication.
The Schema section has 13 criteria that largely restate R2/R3/R5/R8/R33 as negatives ("rejects X", "rejects Y", "rejects Z"). The Resolution section repeats R7/R8/R9 as checkable lines. There's real value in explicit criteria, but 4 of the "rejects" items in Schema can be one criterion: "rejects `vault://` URIs in any non-reference-accepting location."
**Fix:** Consolidate the "rejects" criteria. Target under 50 checkable items.

### N-2. US-8 (`--audit-secrets`) is a plaintext-migration tool that fights the "no migration tool in v1" scope boundary.
The Out-of-Scope section defers `niwa vault import` (automated migration). US-8 gives users an audit command that tells them what still needs migrating, which is the complement of an import tool. Shipping the audit without the import is defensible (audits are read-only), but the framing of US-8 as "track migration progress" implies a migration UX that isn't being built. Reframe as "inspect config secret surface" and let users use it for any purpose.

### N-3. R19 performance budget (5s for ≤20 references) is untestable without a concrete backend baseline.
Infisical is network-bound (RTT dependent); sops+age is CPU-bound (local decrypt). Without specifying which backend the 5s applies to, the requirement is unenforceable. Either pick a backend as the baseline or drop the numeric budget and say "resolution SHOULD emit a progress indicator for > N refs."

### N-4. R20 ("zero additional external dependencies ... no vault-specific Go library") couples a pluggable interface to a subprocess-only implementation choice.
Forcing subprocess-only for Infisical means shelling out to `infisical` CLI, which has its own auth/session machinery — fine. But R20 forbids future backends (HashiCorp Vault OSS, 1Password SDK) from using a Go library even if the CLI is unavailable or significantly worse. This is an implementation preference masquerading as a requirement.
**Fix:** Soften to "v1 backends invoke provider CLIs as subprocesses. Future backends may link Go libraries if the trade-off is justified."

---

## Summary

Top offenders: **two backends in v1** (M-1), **three-tier pattern sprayed across four scopes** (M-2, M-3), **escape syntax for a hypothetical collision** (M-4), **flag-interaction requirement that restates names** (M-5). The PRD is thoughtful but over-scoped; shipping half of the requirements (one backend, env-only three-tier, no `raw:` escape) would deliver the stated goals — publishable team configs, per-user PAT scoping, no-leak guarantees — with materially less surface.

Keep intact: the pluggable interface commitment (R1), personal-wins with `team_only` (R7/R8), `vault://` URI scheme (R3), file-local scoping (R3/D-9), fail-hard with explicit opt-outs (R9/R10), `secret.Value` opaque type (R22), `0o600` materialization fix (R24), public-repo guardrail concept (R14 — but see S-2).
