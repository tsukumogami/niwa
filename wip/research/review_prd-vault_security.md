# Security Review: PRD-vault-integration

**Reviewer role:** Security reviewer
**Scope:** Threat model, 12 "never leaks" invariants (R21–R32), bootstrap, multi-layer override, `--allow-missing-secrets`, rotation-signaling, public-repo guardrail bypass.
**Review date:** 2026-04-13
**PRD status at review:** Draft

---

## Executive summary

The PRD's 12 invariants are a strong skeleton, but several have gaps between the *stated* invariant and what a Go implementation can actually enforce (R22 error-wrapping paths; R26 best-effort write). The threat model is largely silent on three realistic adversaries: a compromised personal-overlay repo (R7's personal-wins becomes a supply-chain injection), a compromised provider CLI wrapper on PATH, and a forged-fingerprint (R15) rotation-hiding attack. The bootstrap stories have trust-on-first-use gaps for both backends (sops PR review burden; Infisical session-token theft). The public-repo guardrail (R14/R32) has two named bypasses (private mirror, `--allow-plaintext-secrets`) whose persistence semantics aren't specified.

Severity legend:
- **MUST-FIX** — shipping without this creates a plausible leak path or silent override-injection path in the threat model the PRD claims to address.
- **SHOULD-FIX** — defense-in-depth gap, ambiguous spec that implementers are likely to get wrong, or missing invariant a security-minded implementer expects.
- **NIT** — clarification / phrasing / future-proofing.

---

## MUST-FIX findings

### M1. R22 (`redact-logs`) error-wrapping is underspecified; `fmt.Errorf("... %w", err)` with an err-chain carrying a `secret.Value` leaks on `%s`

**Threat:** Go's `fmt.Errorf("reading %s: %w", path, err)` walks the err chain on `Error()`; if any wrapped error carries a bare string that was derived from a secret (e.g., a provider CLI's stderr pasted into an error message), `secret.Value`'s opaque formatter does nothing — the raw string is already in the error chain.

**Mitigation:** R22 must mandate (a) a `secret.Error` wrapper type that redacts any error whose `Unwrap()` chain touches a `secret.Value`; (b) a lint-time check that no `fmt.Errorf` / `errors.New` in the `vault` package takes a raw string variable that was sourced from a provider CLI's stdout/stderr; (c) `os/exec` calls scrubbing provider CLI stderr through a redactor before wrapping. Add an explicit acceptance test: "inducing an infisical auth error whose body contains a token fragment does not print the fragment."

---

### M2. R7 (personal-wins) plus R12 (personal can replace team provider) is a supply-chain injection vector the PRD does not acknowledge

**Threat:** A compromised developer laptop (or a malicious PR to `dangazineu/dot-niwa` whose owner doesn't notice) can declare `[workspaces.tsukumogami.vault.providers.team] kind = "sops"` pointing at an attacker-controlled sops file. Subsequent `niwa apply` resolves *team* `vault://team/...` refs against the attacker's provider with no team-side signal. `ANTHROPIC_API_KEY` becomes whatever the attacker chose; tools downstream authenticate to attacker-controlled endpoints if the secret happens to be a URL/DSN.

The PRD frames R7 as a UX convenience (US-4 debug override, US-9 fork-and-PR). It does not discuss that the override also silently replaces team-controlled provider identity.

**Mitigation:** Add an invariant **R33 (INV-OVERRIDE-VISIBILITY)**: when any team-declared provider or key is shadowed by a personal overlay, `niwa apply` MUST log a structured "shadowed" diagnostic to stderr naming the overridden provider/key, and `niwa status` MUST include a `shadowed` count in its summary. Consider a per-key `[vault].team_only` default-on for any key matching `*_TOKEN` / `*_KEY` / `*_SECRET` / `DSN` naming, with explicit opt-out. At minimum, `niwa status --audit-secrets` must flag every shadowed team key so the user can detect unexpected shadowing.

---

### M3. R15 (`SourceFingerprint`) rotation-signaling has no integrity binding; a compromised provider can forge "normal rotation"

**Threat:** The PRD says the fingerprint "captures the resolution inputs (config reference + vault version/etag metadata)." If the fingerprint is computed from provider-supplied metadata (Infisical's secret version, sops file SHA, etc.) with no signature or pinning, a compromised provider (Infisical cloud breach; sops file tampered in the repo) can return: new secret value + new version number → niwa reports `stale` → user shrugs and runs `niwa apply` → attacker's secret is materialized. The same `stale` label is used for legitimate rotation, so there's no distinguishing signal.

**Mitigation:** R15 must specify the fingerprint's integrity properties:
- For sops: fingerprint includes the git commit SHA of the sops file, and `niwa status` surfaces "fingerprint changed because sops file was committed in `<SHA>` by `<author>`" — tying rotation to an identifiable commit.
- For Infisical: fingerprint includes a pinning mode where the team can declare an expected `key_identity` (e.g., a stable ID or a hash-chain of historical values niwa remembers across apply invocations); unexpected identity shifts become a *distinct* status (`rotated-unexpected`) separate from normal `stale`.
- Add a new invariant **INV-ROTATION-PROVENANCE**: rotation reports name the source of truth for the rotation (git commit, Infisical audit log URL) so the user has a path to verify legitimacy.

---

### M4. R14/R32 public-repo detection: no spec for private mirror / rename / cross-host cases; `--allow-plaintext-secrets` persistence is unspecified

**Threat A (bypass via mirror/rename):** R14 says detection uses "the git remote URL" and R32 limits v1 detection to "GitHub remote URL patterns only." A developer who adds a private GitLab remote as `origin` and keeps the GitHub remote as `upstream` sees the guardrail evaluate only `origin` → passes. An in-flight rename from public to private (or vice-versa) uses whatever the local git config says, which may lag.

**Threat B (bypass via `--allow-plaintext-secrets`):** The PRD specifies the flag exists but does not say whether it's one-shot (only affects the current `niwa apply`) or sticky (the state file records the acknowledgment so future applies skip the guardrail). If sticky, a compromised / social-engineered single run unlocks all future runs silently.

**Mitigation:**
- R14 detection MUST enumerate ALL configured git remotes, not just `origin`. Any remote resolving to a public GitHub repo triggers the guardrail. Future remotes added after initial apply MUST be re-checked every apply (this is cheap).
- R32 MUST specify `--allow-plaintext-secrets` is **strictly one-shot**: the flag never writes state, never persists to config, and each new `niwa apply` invocation re-evaluates the guardrail from scratch.
- Add an acceptance test: "add a public remote after `--allow-plaintext-secrets` has been used once → next apply without the flag fails."
- The PRD's "Out of Scope" item for non-GitHub source control (GitLab/Gitea/Bitbucket) is actually a security gap, not a scoping gap: the guardrail silently no-ops on those hosts. Downgrade to a SHOULD-FIX only if the PRD explicitly says "on any non-GitHub remote, `niwa apply` warns that it cannot evaluate the guardrail."

---

### M5. R10 (`--allow-missing-secrets`) empty-string downgrade is dangerous when downstream tools treat unset-vs-empty identically

**Threat:** Many CLIs and SDKs (GitHub CLI, `docker login`, Terraform, `kubectl`) treat an empty `GITHUB_TOKEN`, `AWS_ACCESS_KEY_ID`, `DOCKER_CONFIG` exactly the same as unset — they fall through to anonymous, unauthenticated, or default-credential-chain behavior. For `AWS_*`, that default chain can include IMDSv2 on a CI runner (SSRF-adjacent) or a shared `~/.aws/credentials` profile the user didn't expect to use. For `GITHUB_TOKEN`, anonymous access is rate-limited and may silently "succeed" with unexpected data. R34 protects the subset declared in `[env.required]`, but the *implicit* danger — any secret-like key becoming empty — is not addressed.

**Mitigation:**
- Strengthen R10: when `--allow-missing-secrets` downgrades a ref, niwa MUST emit a stderr warning *per key* naming the downstream tool risks (e.g., "GITHUB_TOKEN=\"\" — GitHub CLI will operate unauthenticated").
- Add a conservative blocklist: R10 MUST NOT downgrade keys whose names match `AWS_*`, `GCP_*`, `AZURE_*` (cloud credential chain triggers) without a second flag `--allow-missing-cloud-credentials`. These keys, when empty, can silently pivot to a different identity, which is strictly worse than erroring out.
- Acceptance test: invoking `niwa apply --allow-missing-secrets` with an unresolvable `AWS_ACCESS_KEY_ID` fails, distinct from a non-cloud key which downgrades.

---

## SHOULD-FIX findings

### S1. Missing invariant: INV-NO-CORE-DUMP / INV-NO-SWAP

**Gap:** The 12 invariants cover log/argv/disk paths but not process memory leaking via core dumps, ptrace, or swap. A `secret.Value` opaque type prevents accidental printing but does not prevent `/proc/<pid>/core` (enabled by default on some Linux distros) from capturing the plaintext.

**Mitigation:** Add **R35 (INV-NO-CORE-DUMP)**: on non-test builds, niwa calls `setrlimit(RLIMIT_CORE, 0)` on startup and optionally `prctl(PR_SET_DUMPABLE, 0)` when `NIWA_HARDEN=1`. Document that users with threat models including local forensic adversaries should enable swap encryption (niwa can't fix that). This is SHOULD not MUST because it's defense-in-depth for an adversary who has already achieved local code execution.

---

### S2. Missing invariant: INV-STATE-JSON-REDACTION

**Gap:** R15 introduces `ManagedFile.SourceFingerprint` in `state.json`. The PRD says the fingerprint captures "resolution inputs + vault version/etag metadata." If the fingerprint is computed from the *resolved secret bytes* (a natural implementation choice: hash of the plaintext), it's effectively an offline-crackable oracle for low-entropy secrets (short PATs, DSN fragments). `state.json` is at least readable by other processes under the user — and may be committed by accident.

**Mitigation:** Add **R36 (INV-STATE-FINGERPRINT-SALT)**: the fingerprint MUST be a keyed HMAC using a niwa-instance-local random salt stored at `state.json`-adjacent `state.salt` (mode `0o600`). The salt is per-instance, never shared, never committed. This prevents offline pre-computed dictionary attacks on the fingerprint. Also specify: `state.json` MUST never contain the fingerprint's source secret under any formatting or debug flag.

---

### S3. R22 and `secret.Value` — missing `encoding/json` Marshaler

**Gap:** Go's `encoding/json` does not go through `fmt.Stringer`. A struct embedding `secret.Value` serialized with `json.Marshal` leaks plaintext unless `secret.Value` implements `json.Marshaler` explicitly. The PRD names `String`/`GoString` but is silent on JSON, YAML, TOML, and `encoding/gob`.

**Mitigation:** Specify that `secret.Value` MUST implement `fmt.Stringer`, `fmt.GoStringer`, `json.Marshaler`, `encoding.TextMarshaler`, and return `error` on `gob.GobEncoder` (refusing serialization outright). Add a compile-time check (`go vet`-compatible lint) that any struct embedding `secret.Value` does not also have a `json:"field"` tag that would cause reflection-based marshaling to bypass the Marshaler.

---

### S4. Bootstrap trust-on-first-use for sops (US-2)

**Gap:** US-2's sops bootstrap: "new dev publishes an age public key via PR to the team repo, and the team lead re-encrypts." The PRD assumes the team lead visually verifies the new dev's public key belongs to the right human. No channel-binding, no key-signing, no fingerprint-over-second-channel confirmation is specified.

**Threat:** An attacker who compromises the dev's GitHub account (or phishes them and opens a PR from a lookalike account) publishes the attacker's age key; team lead merges → attacker now decrypts all team secrets.

**Mitigation:** The bootstrap walkthrough (required by acceptance criteria) MUST prescribe:
- Team lead confirms the age public key fingerprint out-of-band (Slack DM, video call, signed commit).
- Optional integration: niwa supports declaring `.sops.yaml` entries with a comment naming the GitHub username whose profile SSH/GPG key must sign the introducing PR.

This is SHOULD not MUST because it's a documentation/process concern rather than a code invariant, but the PRD's "10 minute bootstrap" target implicitly pressures the team lead to skip verification.

---

### S5. Bootstrap session-token exposure for Infisical (US-2)

**Gap:** `infisical login` performs browser OAuth and caches a session token in `~/.infisical` (or wherever the Infisical CLI decides). The PRD treats the Infisical CLI as trusted and out-of-scope for niwa. But niwa's subprocess-env invariant (R31) and argv invariant (R21) don't cover what happens if the Infisical CLI itself writes a token to a location niwa-managed files later read.

**Threat:** Stale Infisical session token sitting on disk at `0o644` (the CLI's default, unknown to niwa); local malware reads it and calls Infisical directly with the user's identity.

**Mitigation:**
- Document that niwa's threat model explicitly trusts the provider CLI's credential storage. This is a scoping statement, not a code change.
- Add an acceptance test / doc note: `niwa status --audit-secrets` surfaces the location of each provider's session store and its file mode (warning if `0o644`).
- Consider a `niwa doctor` check that warns on world-readable provider credential files.

---

### S6. R31 (`explicit-subprocess-env`) scope is too narrow

**Gap:** R31 covers niwa's own spawned subprocesses (vault CLI, future hook scripts). It does NOT cover the materialized-file-then-consumed-by-Claude-or-shell path, which is the majority of apply's output. If a user's `.envrc` or a Claude-launched subprocess inherits from a shell that has `niwa apply`'s stderr logs in its scrollback, that's technically a leak.

**Mitigation:** Clarify that R31 governs niwa's direct subprocess spawning, not downstream consumers of materialized files. Add a statement to the PRD threat-model section: "Once a secret is written to `.env.local` or `settings.local.json`, control passes to the user's shell / Claude / downstream tools. niwa's guarantees end at materialization. Users are responsible for the security of processes that read those files."

---

### S7. `team_only` (R8) enforcement layer unresolved (Q-7)

**Gap:** The PRD's open question Q-7 asks whether `team_only` is enforced at parse time or materialize time. Security-wise, parse-time is the right answer (static check, no secret resolution required), but Q-7 flags it as open. A materialize-time-only implementation means a personal-config-injected `team_only` override succeeds until the moment of resolution, which means niwa has already contacted the personal vault and potentially leaked the request to the personal provider before the block fires.

**Mitigation:** Close Q-7 in favor of parse-time enforcement. If materialize-time is the chosen path, R8 must specify that the personal vault MUST NOT be contacted for `team_only` keys even to produce a better error message.

---

### S8. `vault://` URI injection via upstream content

**Gap:** R3 forbids `vault://` URIs in `[claude.content.*]`, `[env.files]`, and identifier fields. Good. But it does not address: what if a team config's `[env.vars] FOO = "vault://team/foo"` resolves to a value that *itself starts with `vault://`*? Does niwa recursively resolve it? If yes, a compromised provider can inject `vault://personal/compromised-key` as the value of a team key, causing niwa to resolve *from the personal vault* as if the team authorized it.

**Mitigation:** Add an invariant **R37 (INV-NO-RECURSIVE-RESOLUTION)**: the output of `vault://` resolution is NEVER itself interpreted as a `vault://` URI, regardless of content. `raw:` prefix semantics (R17) apply only at parse time, not at resolution time. Add acceptance test.

---

## NIT findings

### N1. R27 (INV-NO-CLAUDE-MD-INTERP) phrasing is stronger than needed

The invariant says "No secret interpolation into Markdown content." This is a *behavior* spec, not a type-level invariant. Rephrase as: "The materializer for Markdown files MUST NOT invoke the secret resolver; any `vault://` string found in a Markdown source path is a parse error (already covered by R3)." This aligns the invariant with what the type system actually enforces.

### N2. R26 (INV-GITIGNORE-ROOT) best-effort write failure path unspecified

If the `.gitignore` write fails (permissions, full disk, read-only FS), does `niwa create` proceed or abort? For a security invariant, it must abort — otherwise the instance is created with materialized secrets and no gitignore protection. Clarify: `niwa create` MUST fail if the gitignore cannot be written and MUST NOT leave a partially-created instance on disk.

### N3. R24 (INV-FILE-MODE) platform-specific

`0o600` is POSIX-specific. The PRD's "Out of Scope" correctly notes Windows is not supported for v1, but an implementer reading R24 alone without the scope doc might miss that. Cross-reference from R24 to the Windows scoping decision.

### N4. R10 (`--allow-missing-secrets`) warning noise floor

Stderr warnings per missing ref can get lost in CI logs. Recommend: niwa MUST exit with a structured summary line (`niwa-warning: 3 secrets missing; see log for details`) as the last stderr line, detectable by `grep` in CI. This is ergonomic, not a security control, so NIT.

### N5. `raw:vault://` escape (R17) unclear on double-escape

What does `raw:raw:vault://` decode to? The literal string `raw:vault://`, or an error? Specify: `raw:` is a one-shot prefix consumed at parse time; `raw:raw:vault://` decodes to the literal `raw:vault://`. Add a schema-test acceptance case.

### N6. `niwa status --check-vault` is opt-in (acceptance criteria)

The acceptance criteria note default `niwa status` is offline. This is correct for avoiding accidental vault calls from a pager/watch-loop but means a compromised-secret scenario remains invisible until the user explicitly probes. Consider a nudge: the first `niwa status` after N days without a vault check prints a one-line hint "run `niwa status --check-vault` to re-resolve upstream." NIT-level because it's UX, not invariant.

---

## Threat model completeness summary

The PRD's implicit trust boundary includes:
- Local machine (correct — out of scope for any vault tool)
- Provider CLIs (correct — but see S5 and M3 about session-token leakage and provider compromise respectively)
- Git credentials (correct — but see M4 about remote-URL detection)
- Personal config repo (**gap — see M2**; this repo's compromise is functionally equivalent to team-vault compromise under R7)
- `state.json` on disk (**gap — see S2**; fingerprints may be oracles)
- Go runtime (**gap — see S1**; core dumps leak)
- Error-wrapping paths (**gap — see M1**; `%w` doesn't know about `secret.Value`)

The three adversaries the PRD does not explicitly address:
1. **Compromised personal overlay repo** (M2): attacker injects provider override, silently redirects team secrets.
2. **Compromised vault provider** (M3): attacker serves rotated secrets that look like normal rotation.
3. **Local forensic adversary** (S1, S2): attacker with read access to the user's filesystem extracts from core dumps, state.json fingerprints, or provider CLI caches.

Adding explicit acknowledgment of (1) and (2) in the PRD's "Out of Scope" or a new "Threat Model" section — plus the corresponding invariants M2 → R33, M3 → INV-ROTATION-PROVENANCE — closes the two most critical gaps.

---

## Recommended additions before transitioning PRD to Accepted

1. Add an explicit **Threat Model** section enumerating trusted vs untrusted components and the three adversaries above.
2. Add invariants **R33 (override-visibility)**, **R35 (no-core-dump)**, **R36 (state-fingerprint-salt)**, **R37 (no-recursive-resolution)** to round out the set to 16.
3. Tighten R22 to name error-wrapping paths (M1).
4. Tighten R14/R32 detection to cover all remotes and specify `--allow-plaintext-secrets` is one-shot only (M4).
5. Tighten R10 to block empty-string downgrade for cloud credential keys without a second explicit flag (M5).
6. Close Q-7 (parse-time enforcement of `team_only`) — S7.
7. Add compile-time / vet-level assertions that `secret.Value` can't escape via `json.Marshal` or reflection — S3.

Findings from this review map onto pre-existing PRD R-numbers via references in each section. None of the MUST-FIX items require rearchitecting the PRD; they are spec tightenings and one or two new invariants.
