# Security Review: vault-integration

Review of `docs/designs/DESIGN-vault-integration.md` against PRD
`docs/prds/PRD-vault-integration.md` (R21–R31 invariants + Threat Model,
lines ~94–140). The design's `Security Considerations` section is
currently stubbed ("Populated by Phase 5" — line 1142) and needs to be
written before the doc can transition out of Proposed status.

## Dimension Analysis

### External Artifact Handling
**Applies:** Yes (documentation-level).

The design invokes `infisical`, `sops`, `age`, and `git` as subprocesses
(Decisions 3 and 5). PATH resolution is implicit — the design never
mentions `exec.LookPath`, absolute-path lookup, or trojan-binary concerns.

The PRD's Threat Model (line 112) explicitly names "compromised provider
CLI binary on $PATH" as **out of scope**. That is a deliberate boundary:
niwa is not a zero-trust vault client; users are expected to manage their
own PATH hygiene. The `git` binary used by the guardrail (Decision 5)
inherits the same boundary — if a user's PATH is poisoned, every tool on
the system is compromised, not just niwa.

**Risk:** None, given the threat model. **Mitigation:** Call this out in
the Security Considerations section so a future reader doesn't mistake
the absence of defense for an oversight.

### Permission Scope
**Applies:** Yes (design is compliant; needs documentation).

- **`os.Setenv`.** The design correctly avoids it. Section "Data Flow"
  step 14 routes secrets through the materializer, and the Components
  table (line 733) shows materializers use `secret.UnsafeReveal` to
  obtain bytes for file writes — not for process-env publication. This
  satisfies R28.
- **Subprocess env.** The PRD (lines 815–822) deferred
  `INV-EXPLICIT-SUBPROCESS-ENV` because niwa today has no subprocess-
  spawn path that carries resolved secrets. This design adds exactly
  such paths: provider CLI invocation (Infisical, sops, age) and the
  guardrail's `git remote -v`. **The design does not specify how
  subprocess env is constructed.** A naive `exec.Command(...).Run()`
  inherits `os.Environ()`, which is benign today (niwa holds no secret
  env) but creates forward-looking risk if any future code calls
  `os.Setenv` or if niwa is invoked from a parent process whose env
  contains secrets.
- **File perms.** `0o600` across EnvMaterializer, SettingsMaterializer,
  FilesMaterializer (Phase 6, line 1071) — closes the pre-existing
  `0o644` bug. R24 satisfied.
- **Workspace boundary.** No design path writes outside the instance
  root. R23 (no config writeback) is structural: the resolver consumes
  `*WorkspaceConfig` and emits a new `*WorkspaceConfig`, never calling
  back into `configDir`.

**Risk:** LOW — the subprocess-env question is the one real gap, and
the PRD acknowledged it as deferred. **Mitigation:** the design SHOULD
at minimum specify "subprocesses receive `os.Environ()` filtered or
passed-through as-is; no niwa-held `secret.Value` is ever injected into
child env." This can be a one-sentence decision in Security
Considerations, not a new package.

### Supply Chain or Dependency Trust
**Applies:** Yes (out of scope per threat model, but `[vault.providers.*]`
config shape deserves scrutiny).

The PRD threat model (line 110) excludes "compromised provider CLI
binary" and "compromised vault provider credentials" from scope. The
design inherits those exclusions. Niwa does not verify binary
signatures, does not pin versions, does not check SLSA provenance.
This is consistent with the threat model.

**One real concern: `[vault.providers.*]` config is attacker-influenced
by design.** A team's `workspace.toml` declares provider config; that
file flows through `config.Load` into the resolver. For the v1.1 sops
backend, sops reads its identity file from `SOPS_AGE_KEY_FILE` env var
or a well-known path; the design does not say whether niwa's
`ProviderConfig` shape for the sops backend lets a workspace.toml
override this. If it does, a malicious team config could point the
sops backend at an arbitrary path on the user's machine (e.g.,
`~/.ssh/id_rsa`), and since sops would try to parse it as an age
identity and fail, the result is likely a decrypt error — not
exfiltration. But the design should state the rule explicitly: **team
config MUST NOT be able to redirect where age/sops reads identity
material from; that is personal-overlay / environment-var territory.**

The **age private key** itself is not stored, created, or handled by
niwa — sops reads it from `SOPS_AGE_KEY_FILE` or the user's own
configuration. Niwa never touches the private key bytes. This is
correct.

**Risk:** MEDIUM if the sops `ProviderConfig` accepts an
`identity_file` field from team config; LOW if it doesn't.
**Mitigation:** Security Considerations should state that identity/key
paths are personal-overlay-only (or env-var-only), never team-
declarable. This is a v1.1 concern (sops is stubbed in v1) but worth
naming now to set the interface shape.

### Data Exposure
**Applies:** Yes. This is the core dimension. Walking every path:

| Exit path | Mechanism | Closes the leak? |
|-----------|-----------|------------------|
| **Disk writes** (materialized files) | R24 `0o600` + R25 `.local` infix + `.gitignore` (Phase 6) | YES — and fixes pre-existing `0o644` bug. |
| **stdout/stderr via formatters** | `secret.Value` implements `String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`, `GobEncode` all returning `***` or refusing (lines 303–308) | YES — structural. |
| **Error-wrap chains** (`fmt.Errorf("%w")`) | `secret.Error` + context-scoped `Redactor` that scrubs strings before interpolation (Decision 2) | YES — this is the hardest case and the design's strongest defense. The 186 existing `%w` sites (line 266) are covered because the `Redactor` scrubs the produced string regardless of which site wrapped it. One residual: the redactor scrubs only fragments registered during resolve; a secret that enters niwa by a NON-vault path (e.g., via stdin in a future feature) would not be in the redactor. Acceptable for v1. |
| **Provider-CLI stderr capture** | `vault.ScrubStderr(stderr, known...)` in each backend before wrapping into returned errors (line 410) | YES — PRD R22 acceptance test explicitly verifies this (stderr carrying known-secret fragment). |
| **Structured logs** | `secret.Value` formatters run unconditionally, including for `log/slog` which goes through `fmt` | YES. |
| **`state.json`** | `ManagedFile.ContentHash` is SHA-256 of file bytes, `SourceFingerprint` is SHA-256 of `(source-id, version-token)` tuples, `Sources[].VersionToken` is provider-supplied opaque ID, `Sources[].Provenance` is explicitly "non-secret" (line 461) | YES — design explicitly notes `Shadow` carries "only strings (names, paths, layer labels) — never a `secret.Value`, so R22 compliance is structural" (line 619). Needs a parallel explicit statement for `SourceEntry` that `VersionToken` and `Provenance` are non-secret by contract — otherwise a backend author might stuff the token in plaintext. |
| **`niwa status` output** | R27: path + status only, no content, no diff. `--audit-secrets` shows classification (`vault-ref`/`plaintext`/`empty`) but not values | YES. |
| **`niwa status` provenance rendering** | Per Decision 4, prints git SHA (sops) or audit-log URL (Infisical). Both are non-secret metadata | YES — but confirm the audit-log URL is not a signed URL carrying credentials. Infisical's audit-log URL is a console link requiring user auth, not a bearer-token URL. Good. |
| **Subprocess argv** (R21) | PRD R21 forbids argv secrets; design's Infisical invocation uses `infisical export --format json` (line 825), which reads auth from env/keychain, not argv. sops: `sops -d <file>`, same — decrypt key from env, not argv | YES. Design should assert this explicitly. |
| **Subprocess env** (R28 process env, deferred INV-EXPLICIT-SUBPROCESS-ENV) | Design does not specify. Potential for future regression. | PARTIAL — see Permission Scope above. |
| **Disk cache** (R29) | Design states "Close all providers (Resolver.CloseAll)" at pipeline step 12; no resolved-secret persistence anywhere outside materialized files (which ARE the intended output, covered by R24/R25) | YES. |
| **Shadow diagnostics** (R31) | `Shadow`/`ProviderShadow` structs carry only names and source paths; no value fields. R22 coverage is structural | YES. |
| **`SourceFingerprint` tuples** | Tuple is `(source-id, version-token)`; for plaintext sources, `version-token` = SHA-256 of bytes (not the bytes); for vault sources, the provider-supplied opaque version ID. Content never enters the tuple | YES — and the rollup hash is a SHA-256, non-reversible. |
| **Redactor lifecycle** | Context-scoped, per-apply (line 325). `WithRedactor`/`RedactorFrom` helpers. The redactor itself holds secret fragments in memory — this is a legitimate in-memory secret, bounded by apply lifetime, dropped when context ends | YES for v1 (the threat model explicitly excludes same-user process memory attacks, line 108). |

**Edge case the design MISSES:** The `Redactor` accumulates secret
fragments as strings. When a secret is short (e.g., a 4-byte token) or
has high base-rate occurrence (e.g., literal byte values that collide
with common words), naive string-replace scrubbing can produce false
positives that confuse users or, worse, accidentally scrub a useful
error fragment. The design should specify: minimum-fragment-length
threshold (suggest 6–8 bytes) before a value is registered, and/or
hex-encoded-only matching, and/or whole-word-boundary matching. This
is a correctness concern for `Redactor`, not a leak path.

**Risk:** LOW overall for data exposure. The two items needing design
attention are (a) subprocess-env statement, (b) Redactor fragment
handling (minimum length / base-rate handling). Both are documentation-
level or small code notes.

### Override-Visibility / Supply-Chain Attack
**Applies:** Yes (design appears correct).

R12 (personal overlay cannot replace team-declared provider names) is
enforced by `vault.DetectProviderShadows` (pipeline step 5) which
raises a hard error on collision. This fires BEFORE any resolution
against either bundle, so the attack surface ("personal overlay
silently redirects a team vault to attacker-controlled backend") is
closed structurally.

R31 shadow-visibility diagnostics cover `env-var`, `env-secret`,
`files`, `settings`, and `provider` kinds (line 860). The design
persists `Shadow` records to `state.json` so `niwa status` can surface
them offline.

**One potential gap — finer granularity than R12 covers.** A personal
overlay cannot replace a team provider NAME, but it CAN add a
per-key override in `[workspaces.<scope>.env.secrets]` that redirects
an individual team secret ref to a personal-vault key. That's R7
legitimate-override territory. The question is whether R31's
diagnostics surface this at per-key granularity. Reading `Shadow`:
yes, `Kind: "env-secret"` covers this. Good.

**Another gap — vault scope via `[workspace].vault_scope`.** A
personal overlay cannot change this (team config owns `[workspace]`),
so the attack "overlay silently rescopes which vault folder my secrets
come from" is not possible. Good.

**Risk:** LOW. **Mitigation:** Security Considerations should name
R12 + R31's joint coverage explicitly as the supply-chain-attack
defense for the personal-overlay surface.

### Public-Repo Guardrail
**Applies:** Yes (documentation needed around the detection boundary).

Decision 5 chose Option 1 (URL pattern match only). Acknowledged
trade-offs:

- **Private `github.com` false positive:** Design correctly notes
  this is handled via the one-shot `--allow-plaintext-secrets` flag
  (R30). Not a security hole — the flag is explicit user action.
- **GitHub Enterprise:** Not covered by default GitHub URL patterns.
  A GHE public repo at `github.mycorp.com/team/repo` would NOT trigger
  the guardrail. **This is a real gap** but aligned with PRD line 1120
  ("Non-GitHub source control... GitLab, Bitbucket, self-hosted Gitea
  stay in a deferred list"). GHE should be named in that deferred list
  explicitly — the PRD mentions "Non-GitHub" but GHE is technically
  GitHub-operated, so a reader might assume coverage.
- **No git directory:** Design correctly notes (line 1174) the
  guardrail emits "no git remotes detected; guardrail skipped" and
  trusts the `--allow-plaintext-secrets` path. This is arguably too
  permissive: an attacker who extracts a tarball of a public repo
  could materialize plaintext secrets without the guardrail firing.
  But this isn't really an attack — it's a user workflow. If the user
  has plaintext secrets in `[env.secrets]`, they committed them; the
  guardrail's job is to prevent FURTHER commits, which a non-git
  working tree can't do anyway.
- **Manipulated `git` binary:** Out of scope per threat model (line
  112). The guardrail trusts `git remote -v` output.
- **Symlinked config dir / git alternate:** `git -C <configDir>
  remote -v` operates on whatever `<configDir>` resolves to on disk.
  A symlink pointing at a non-git directory would produce a git
  error, which the guardrail should treat as "no remotes" (warn +
  proceed) — design should clarify.

**Risk:** MEDIUM for GitHub Enterprise false-negatives; LOW for
others. **Mitigation:** Document the detection boundary explicitly in
Security Considerations. List GHE in the deferred-coverage set. State
the "git error → skip with warning" behavior.

### Threat Model Consistency
**Applies:** Yes (compliant, needs documentation).

Verifying the design doesn't over-defend (implying out-of-scope
adversaries are in scope) or under-defend (missing in-scope attacks):

**Over-defense check:** The design does not attempt to:
- Verify provider binary signatures (correct — out of scope).
- Encrypt `state.json` (correct — same-user-process is out of scope,
  `0o600` is sufficient per threat model).
- Detect PATH manipulation (correct — out of scope).
- Implement its own auth to vaults (correct — provider CLI handles it).
- Defend against root attackers (correct — out of scope).

All correct. No accidental implication that the excluded adversaries
are in scope.

**Under-defense check:** The design DOES defend against every
invariant R21–R31:

| Invariant | Design mechanism | Pass |
|-----------|------------------|------|
| R21 no-argv | Subprocess invocations use config/env for auth, not argv | YES |
| R22 redact-logs | `secret.Value` + `secret.Error` + `Redactor` (Decision 2) | YES |
| R23 no-config-writeback | Resolver is read-only over `*WorkspaceConfig` | YES |
| R24 file-mode | All three materializers switch to `0o600` | YES |
| R25 local-gitignored | Materializers use `.local` infix; `niwa create` maintains `.gitignore` | YES |
| R26 no-CLAUDE.md-interp | Parser rejects `vault://` in `[claude.content]` (line 732) | YES |
| R27 no-status-content | `niwa status` reads state only (offline) | YES |
| R28 no-process-env | No `os.Setenv` call in design | YES |
| R29 no-disk-cache | `CloseAll` at pipeline step 12; no cache file | YES |
| R30 public-repo-guardrail | `CheckGitHubPublicRemoteSecrets` + one-shot flag | YES |
| R31 override-visibility | `DetectShadows` + persist + stderr + status + `--audit-secrets` column | YES |

All eleven in-scope invariants have a mechanism. The only ambiguous
one is the deferred `INV-EXPLICIT-SUBPROCESS-ENV` (out of scope for
R21–R31, but the PRD says it re-enters scope once niwa spawns
secret-bearing subprocesses — which this design does). The design
inherits the PRD's deferral; that is defensible but should be
documented as a forward-looking concern.

**Risk:** LOW. **Mitigation:** explicit acknowledgment in Security
Considerations.

## Recommended Outcome

**OPTION 2 — Document considerations.** The design is substantively
compliant with the PRD's invariants and threat model; no architectural
changes are needed. The one file that needs edits is the
`## Security Considerations` section of the design doc itself (line
1142), which currently reads "Populated by Phase 5." Phase 5 is now.

---

### Draft `Security Considerations` Section

```markdown
## Security Considerations

This design implements the eleven "never leaks" invariants (R21–R31)
enumerated in the PRD. The threat model and scope boundaries defined
in PRD §"Threat Model" apply unchanged; this section documents how
each invariant is realized, the residual risks niwa does not defend
against (by design), and the small number of forward-looking
concerns implementers must keep in mind.

### Invariant Coverage

| Invariant | Realized by |
|-----------|-------------|
| R21 (no-argv) | Infisical and sops subprocess invocations read auth from provider-CLI env/keychain, never from argv. Confirmed by Phase 5 and Phase 11 acceptance tests. |
| R22 (redact-logs) | `secret.Value` opaque type (Decision 2) with formatters returning `***` for every standard Go emission path (`String`, `GoString`, `Format`, `MarshalJSON`, `MarshalText`, `GobEncode`). `secret.Error` + context-scoped `Redactor` scrub error-chain interpolation including captured provider-CLI stderr via `vault.ScrubStderr`. |
| R23 (no-config-writeback) | Resolver is a pure function `(*WorkspaceConfig) → (*WorkspaceConfig)` returning a new struct; no filesystem write into `configDir`. |
| R24 (file-mode 0o600) | `EnvMaterializer`, `SettingsMaterializer`, `FilesMaterializer` write `0o600` unconditionally (Phase 6). Fixes the pre-existing `0o644` bug. |
| R25 (.local + .gitignore) | Materializer filename convention + `niwa create` idempotent `.gitignore` maintenance. |
| R26 (no CLAUDE.md interpolation) | Parser rejects `vault://` URIs in `[claude.content.*]` at load time. |
| R27 (no status content) | `niwa status` reads `state.json` only; renders `path + status` plus non-secret `Provenance` strings. |
| R28 (no process env publication) | No `os.Setenv` call in any code path. Secrets flow into the materializer's file-write path and nowhere else. |
| R29 (no disk cache) | `Resolver.CloseAll` at pipeline step 12; resolved secrets exist in process memory only for the duration of a single `niwa apply`. |
| R30 (public-repo guardrail) | `guardrail.CheckGitHubPublicRemoteSecrets` at pipeline step 7; one-shot `--allow-plaintext-secrets` flag with no state persistence. |
| R31 (override-visibility) | `DetectShadows` + `DetectProviderShadows` persist shadow records in `state.json`; stderr diagnostic at apply time; `niwa status` summary line; `--audit-secrets` SHADOWED column. |

### Explicit Non-Scope

niwa is a developer-tool workspace manager, not a zero-trust vault
client. The following adversaries are out of scope per PRD §"Threat
Model":

- **Malicious same-user processes.** Can read `0o600` files the user
  owns; niwa does not encrypt state or materialized files at rest.
- **Root attackers or compromised kernel.** Out of scope.
- **Physical laptop theft without FDE.** Out of scope.
- **Compromised provider CLI binary** (trojan `infisical` on PATH,
  unsigned `sops` binary, etc.). niwa invokes provider CLIs via
  standard PATH lookup and trusts their stdout output. We do not
  verify binary signatures, pin versions, or lock the subprocess
  PATH. A user whose PATH is poisoned has bigger problems than niwa
  can solve.
- **Compromised vault service or credentials.** niwa's security story
  assumes the provider backend is honest and the user's vault
  credentials are uncompromised.

### Explicit In-Scope Defenses

niwa actively prevents the following accidents:

- **Accidental `git commit` of plaintext secrets in a public config
  repo** — R14/R30 guardrail enumerates ALL remotes (not just
  `origin`), regex-matches GitHub HTTPS/SSH URL patterns, and blocks
  apply when `[env.secrets]` or `[claude.env.secrets]` contains a
  non-`vault://` value. Bypass requires explicit one-shot
  `--allow-plaintext-secrets`.
- **Accidental materialization under world-readable permissions** —
  `0o600` is unconditional.
- **Accidental inclusion in CLAUDE.md** — parser-level rejection.
- **Accidental disclosure via logs, stderr, error chains, or
  provider-CLI stderr** — structural via `secret.Value`, `secret.Error`,
  and the `Redactor`.
- **Silent personal-overlay supply-chain attack** — R12 forbids
  personal overlays from replacing team-declared provider NAMES
  (hard error at apply time); R31 surfaces per-key shadowing at
  three diagnostic surfaces so a compromised overlay cannot silently
  redirect individual secrets.

### Guardrail Detection Boundary

The public-repo guardrail uses URL pattern matching, not authenticated
probes. Explicit boundaries:

- **Detects:** `github.com` HTTPS and SSH URLs across all remotes
  reported by `git remote -v`.
- **Does NOT detect:** GitHub Enterprise Server hosts, GitLab,
  Bitbucket, Gitea, self-hosted git at arbitrary hosts. A repo on
  `github.mycorp.com` or any non-`github.com` host will NOT trigger
  the guardrail even if public. Non-GitHub host coverage is
  tracked as deferred in the PRD Out-of-Scope list.
- **No git working tree:** If `git -C <configDir> remote -v` errors
  (no `.git`, missing binary, corrupted refs), the guardrail emits
  a warning and proceeds. Users extracting a config tarball outside
  a git clone bypass the guardrail by construction; the guardrail's
  purpose is to prevent future commits, which a non-git tree cannot
  perform.

### Redactor Implementation Notes for Implementers

The `Redactor` scrubs strings by replacing registered fragments
with `***`. Two implementation notes affect correctness, not
security per se, but matter for error-message usability:

- **Minimum fragment length.** Short secrets (< 6 bytes) have high
  collision rates with ordinary English/log text. The `Redactor`
  SHOULD skip registering fragments shorter than a safe threshold
  and SHOULD NOT apply substring matching to such fragments.
  Secrets that short should be rejected at resolution time with a
  hard error (users with a 4-byte API token should not be using
  niwa for it).
- **Fragment ordering.** Scrub longest fragments first to avoid a
  substring of fragment A shadowing fragment B.
- **Whole-token matching.** Consider word-boundary or
  base64/hex-alphabet-boundary matching to prevent false positives
  in user-facing error text. This is a quality bar for the
  Redactor's acceptance tests, not a security invariant.

### Forward-Looking: Explicit Subprocess Env

The PRD deferred `INV-EXPLICIT-SUBPROCESS-ENV` because niwa today
carries no secret-bearing env. This design changes that: the vault
resolver holds `secret.Value`s in process memory during apply. The
subprocesses niwa spawns during that window (provider CLIs, `git
remote -v`) inherit `os.Environ()` by default. Three invariants
implementers MUST honor:

1. **No `os.Setenv(secret)`.** Ever. Secret bytes never enter the
   niwa process's own environment. (R28.)
2. **No injection of secrets into subprocess env.** Provider CLIs
   obtain their auth from the user's shell env or keychain, not
   from niwa-built env. niwa does not forward `secret.Value` bytes
   into `exec.Cmd.Env`.
3. **Inherited env is passed through unchanged.** `exec.Cmd.Env =
   nil` (inherit) is the default; do not filter, do not extend with
   secrets.

A future feature that spawns CLAUDE Code or hook scripts with
materialized secrets will need to revisit this section and
potentially promote these points to a formal invariant with
acceptance tests.

### Forward-Looking: Backend `ProviderConfig` Safety

The v1.1 sops backend and any future backend that reads identity or
key material from a filesystem path MUST NOT accept that path from
team-declared provider config. Identity file paths belong in
personal-overlay config or environment variables only. This prevents
a malicious team config from redirecting sops at an attacker-chosen
path on the user's machine. When the sops backend lands in v1.1,
its `ProviderConfig` schema MUST reject `identity_file` /
`key_file` / equivalent fields from team-layer sources.

### Residual Risks Accepted

- Provider CLI binary integrity (user responsibility).
- Same-user process memory inspection (out of scope per threat
  model; covered by OS user isolation).
- GitHub Enterprise public repos (deferred; same bucket as GitLab,
  Bitbucket).
- Users who bypass the guardrail with `--allow-plaintext-secrets`
  and then `git push` (the flag is explicit, one-shot, loud; this
  is user agency, not a niwa bug).
```

## Summary

The design is substantively secure and implements all eleven PRD
invariants (R21–R31) through structural mechanisms — the opaque
`secret.Value` type, the context-scoped `Redactor`, `0o600` file
writes, and the resolve-before-merge pipeline ordering. No
architectural changes are needed. The only gap is the stub
`## Security Considerations` section, which must be filled before
the doc leaves Proposed status; the draft above covers invariant
realization, the guardrail detection boundary, two forward-looking
concerns (subprocess env and `ProviderConfig` path-safety for v1.1
sops), and a handful of Redactor implementation notes that affect
error-message quality.
