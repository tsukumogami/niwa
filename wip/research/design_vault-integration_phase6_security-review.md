# Phase 6 Security Review: vault-integration

## Summary

The design's security story holds together. Phase 5's recommendations
were applied in full — the Security Considerations section now covers
all eleven invariants, names the non-scope adversaries explicitly,
documents the guardrail detection boundary, and calls out the two
forward-looking concerns (subprocess env, `ProviderConfig` path
safety). Final verdict: **APPROVE**. No blocking issues.

## Answers to Review Questions

### 1. Attack vectors not considered

I walked the trust boundaries from the PRD Threat Model (lines 126–136)
once more against the Data Flow (lines 930–984) and checked for missed
vectors **inside** the declared scope. Findings:

- **Vault service ↔ vault CLI** — out of niwa's scope (provider's own
  auth). Correctly excluded.
- **Vault CLI ↔ niwa process (stdout)** — resolver wraps into
  `secret.Value` immediately in `provider.Resolve` (Decision 3, line
  779). `vault.ScrubStderr` handles stderr before `%w` wrap (Data Flow
  step "Error paths", lines 971–974). Covered.
- **niwa process ↔ disk (materialized files)** — `0o600`
  unconditionally via Phase 6 materializer changes; `.local` infix
  plus `niwa create` `.gitignore` maintenance. Covered.
- **Disk ↔ git push (config repo)** — R23 resolver returns a new
  `*WorkspaceConfig` (no writeback); R14/R30 guardrail at pipeline
  step 7. Covered.
- **niwa process ↔ logs/stderr** — `secret.Value` formatters plus
  context-scoped `Redactor`. Covered.
- **Team config ↔ personal overlay** — `DetectProviderShadows` at
  step 5 (R12 hard error on same provider name); `DetectShadows` at
  step 9 for per-key overrides (R31 diagnostics). Covered.

One vector I considered carefully: **plaintext values in `*.secrets`
when `--allow-plaintext-secrets` suppresses the guardrail**. The design
(lines 845–852) resolves this by wrapping those values in
`secret.Value` unconditionally — so even when the guardrail is
suppressed, logs/errors still redact. This closes what would otherwise
be a subtle regression path. Good.

Another vector I checked: **redactor scope**. The `Redactor` is
context-scoped per apply (line 325). Secrets registered during resolve
are scrubbed through the rest of the pipeline. Two sub-questions:

- Does the redactor see plaintext values in `*.secrets` that were
  wrapped without going through a provider? The design says the
  resolver wraps them (line 849) — which implies the resolver also
  registers them with the redactor. The design should make that
  explicit (the resolver registers fragments whether the source is a
  `vault://` URI or a plaintext value being auto-wrapped), but this
  is a minor clarity point, not a gap.
- What about secrets read by subprocess CLIs before niwa sees them?
  Out of scope — niwa can only redact what it knows about.

No missed attack vectors inside the declared scope.

### 2. Mitigation sufficiency

Checking each in-scope risk's mitigation for single-layer dependence,
reliance on user behavior, or unenforced assumptions:

- **R14/R30 guardrail** — mitigation is a hard block with explicit
  one-shot bypass. Enumerates ALL remotes (not just `origin`). Doesn't
  depend on user behavior; depends only on `git remote -v` returning
  accurate data, which is in the trust boundary.
- **R24 `0o600`** — unconditional at the materializer layer, applies
  even for non-vault paths. No user-behavior dependence. Defense in
  depth: also covered by `.local` infix + `.gitignore` (R25), so a
  `0o644` regression in one materializer wouldn't compromise git-push
  safety.
- **R26 CLAUDE.md interpolation** — parser-level rejection at load
  time. Single layer, but the layer is correct — `vault://` is
  syntactically rejected before any resolver sees it, so there's no
  way for CLAUDE.md to receive a secret value. Sufficient.
- **R28 no process env publication** — design states "no `os.Setenv`
  call in any code path" (line 1160). This is enforced by convention
  plus grep, not by the type system. A future contributor adding
  `os.Setenv(key, secret.UnsafeReveal(v))` would compile. Phase 5
  flagged this; the design's forward-looking subprocess-env section
  (lines 1250–1272) partly addresses it by naming three implementer
  invariants. Mitigation is sufficient for v1 but leans on code
  review rather than type enforcement. Acceptable — the Decision 2
  discussion of the deferred linter (Option 4, line 1347) names the
  compile-time hardening as v1.1 scope.
- **R29 no disk cache** — structural via `Resolver.CloseAll` at step
  12. Sufficient.
- **R22 redact-logs** — the `Redactor` registers fragments at resolve
  time and scrubs through `%w` chains. Minimum-fragment-length note
  (lines 1237–1242) prevents short-secret false-positive collapse.
  The acceptance test covers provider-CLI stderr. One subtle
  assumption: every error site that touches a `Value` must use
  `secret.Wrap` or `secret.Errorf`. The design acknowledges this is
  enforced at runtime via the `Redactor` scrubbing any interpolated
  string regardless of which site wrapped it — so even a plain
  `fmt.Errorf("%w", err)` inheriting a `secret.Error` in its chain
  will redact via `secret.Error.Error()`. Sufficient.
- **R31 override-visibility** — three surfaces (stderr, `niwa status`
  summary, `--audit-secrets` SHADOWED column) plus state persistence.
  Defense in depth — a user who misses the stderr noise still sees
  the shadowed count on next `niwa status`. Sufficient.

One mitigation I want to note as adequate-but-thin: **the `Redactor`
minimum fragment length is a SHOULD, not MUST** (line 1239). The
design justifies this as a "correctness not security" concern. That's
defensible — a 4-byte secret is genuinely unsafe regardless, and
rejecting them at resolve time (line 1241) is the right answer. But
the "SHOULD" could allow a conforming implementation that registers
tiny fragments and produces unusable error output. Not a security
blocker; flagged as non-blocking below.

No mitigations that depend on user behavior for security outcomes
(the `--allow-plaintext-secrets` flag is user agency by design, not a
mitigation niwa relies on).

### 3. N/A justifications

The design's Explicit Non-Scope list (lines 1171–1188) claims five
items:

- **Malicious same-user processes** — per PRD line 108. Correctly
  out of scope. The design doesn't make any claim that would pull
  this back in-scope (no "encrypts state.json" claim, no "protects
  against memory dump" claim). Justification stands.
- **Root attackers / compromised kernel** — PRD line 111. Correct.
- **Physical laptop theft without FDE** — PRD line 109. Correct.
- **Compromised provider CLI binary** — PRD line 112. The design
  invokes provider CLIs via standard PATH lookup (Decision 3). It
  does NOT claim to verify signatures or pin versions. Justification
  stands.
- **Compromised vault service or credentials** — PRD line 110.
  Correct.

One item I checked carefully: the design uses `git remote -v` for the
guardrail (Decision 5). A trojan `git` binary on `$PATH` could
misreport remotes. Is that in-scope? No — it's the same category as
trojan `infisical` (PRD line 112). The design correctly inherits the
PRD's PATH-hygiene scope decision.

No "not applicable" justification is actually applicable.

### 4. Residual risk escalation

The design lists four accepted residual risks (lines 1287–1294):

1. **Provider CLI binary integrity** — user responsibility. This is
   PRD line 112. No escalation warranted.
2. **Same-user process memory inspection** — out of scope per threat
   model. PRD line 108. No escalation warranted.
3. **GitHub Enterprise public repos** — deferred; same bucket as
   GitLab/Bitbucket. PRD line 1120 explicitly lists "Non-GitHub
   source control" as out-of-scope for v1. The PRD's list uses
   "Non-GitHub" which could be read as "not github.com at all"
   — the design correctly interprets this to include GitHub
   Enterprise (which is GitHub-operated but not `github.com`). No
   escalation warranted.
4. **Users who bypass with `--allow-plaintext-secrets` and then
   `git push`** — user agency. The flag is one-shot, explicit, and
   emits a structured error listing offending keys. No escalation
   warranted.

None of these should be escalated to in-scope. A reasonable reviewer
might ask "should GHE public-repo coverage be v1?" — but the PRD
deliberately deferred non-`github.com` hosts (line 1120), so
promoting GHE unilaterally would exceed the PRD's scope decision.
That's a PRD-change conversation, not a design-blocker.

## Blocking Issues

None.

The design substantively implements all eleven PRD invariants, the
Security Considerations section covers Phase 5's recommendations
verbatim, and the residual-risk accounting is honest and aligned
with the PRD scope.

## Non-Blocking Suggestions

1. **Tighten Redactor minimum-fragment-length from SHOULD to MUST.**
   Lines 1237–1242 currently say the `Redactor` SHOULD skip
   sub-threshold fragments. A conforming implementation could still
   register them and produce unusable output. Consider strengthening
   to "MUST reject secrets shorter than N bytes at resolve time"
   (which the design already recommends as the companion defense).
   Non-security concern — usability of error messages.

2. **Explicit statement that the resolver registers every resolved
   value with the `Redactor`, including plaintext values auto-wrapped
   in `*.secrets`.** Lines 845–852 describe the auto-wrap behavior;
   the Security Considerations section could state "the resolver
   registers the bytes with the context `Redactor` whether the source
   is `vault://` or auto-wrapped plaintext" to make the redactor-
   coverage guarantee complete. Clarity, not a gap.

3. **The "no git working tree" path note in Guardrail Detection
   Boundary (lines 1224–1229) could name the symlink case explicitly.**
   A `configDir` that's a symlink to a non-git directory produces a
   git error; the design says the guardrail "emits a warning and
   proceeds." Phase 5 recommended clarifying this; it's covered at
   the category level but the symlink wording would remove one
   future-FAQ round. Documentation polish.

None of these are blocking. The design is ready to leave Proposed.
