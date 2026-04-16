# Architect Review — Issue #9 (public-repo plaintext-secrets guardrail)

Commit: `a3ee5b6d63c03216290cef1d9c79297328a4e2ac`
Branch: `docs/vault-integration`

## Verdict

**Approve — non-blocking.**

The implementation respects the existing package boundaries, the pre-resolve
timing is architecturally sound and well-justified in code comments, and the
security posture matches the threat model the PRD scoped (local-UX
guardrail, not an anti-tamper defense).

## Critical checks

### 1. S-1: config package stays git-ignorant

**Verified clean.** `internal/config/` imports only `vault`, `secret`, and
`BurntSushi/toml`. No `os/exec`, no `git`, no `guardrail` import. The
guardrail lives in its own package and imports `config` read-only (one-way
dependency, lower-level `config` → higher-level `guardrail` via the
`apply.go` caller — correct direction).

The new package doc comment on `internal/guardrail/githubpublic.go:1-6`
explicitly calls out the separation: "cross-cutting safety checks that run
during apply but are not themselves part of the resolver, materializer, or
config parser." Each guardrail gets its own narrowly-scoped entry point,
so adding a second guardrail is a new call site in `apply.go`, not a new
bit on a shared struct. This matches the architecture map's pattern of
"explicit registration beats implicit dispatch" for low-frequency
extension points.

### 2. PR timing (pre- vs post-resolve)

**Verified sound.** The resolver's auto-wrap behavior at
`internal/vault/resolve/resolve.go:475-482` is definitive: in a
`*.secrets` table, a plaintext `Plain` becomes `MaybeSecret{Secret: ...}`
with `Plain` cleared. Post-resolve, a previously-plaintext entry is
indistinguishable from a vault-resolved entry — both report
`IsSecret()==true` with empty `Plain`.

The guardrail at `apply.go:361` fires BEFORE `ResolveWorkspace` at line
381. At that point:

- Plaintext literal: `Plain != ""`, `IsSecret() == false` → flagged
- `vault://...` ref: `Plain == "vault://..."`, `IsSecret() == false` → also flagged by the naive check?

No — `offendingKeys` at `githubpublic.go:133-143` uses `v.IsSecret()` to
skip, AND a non-empty `v.Plain` to include. A vault-ref pre-resolve has
`Plain == "vault://key"` and `IsSecret() == false`, which the current
check WOULD flag. Checked the tests: the pre-resolve input in the "clean"
test (`newCfgWithResolvedSecret` at lines 76–90) sets BOTH `Plain:
"vault://..."` AND `Secret: secret.New(...)`, making `IsSecret()` true.
But that test simulates a *post*-resolve state, while the guardrail runs
*pre*-resolve.

This is a real gap: a user whose TOML has `API_KEY = "vault://foo"` will
hit the guardrail because at call time `IsSecret() == false` and `Plain
!= ""`. The intended filter is "Plain is a plaintext literal", which
requires excluding the `vault://` prefix.

Scanning `offendingKeys`:

```go
for k, v := range t.Values {
    if v.IsSecret() { continue }
    if v.Plain == "" { continue }
    keys[k] = struct{}{}
}
```

Missing: `if strings.HasPrefix(v.Plain, "vault://") { continue }`.

**Severity: Blocking if PR claims vault-refs don't fire the guardrail;
advisory if the test matrix doesn't actually exercise a pre-resolve
`vault://` TOML input.** Looking at the test file: there is no test where
the input `cfg` has `Plain: "vault://..."` and `Secret: nil` (pre-resolve
vault ref). The "clean" test pre-populates `Secret` too, masking the
behavior. A user who runs `niwa apply` against `workspace.toml`
containing `[env.secrets] API_KEY = "vault://..."` will hit this
guardrail before resolution promotes it, so this IS user-visible.

Recommendation: add a `strings.HasPrefix(v.Plain, "vault://")` skip in
`offendingKeys`, plus a test with a raw `MaybeSecret{Plain:
"vault://foo"}` that asserts no error. **Reclassifying: blocking.**

(All other critical checks stand — this is a single localized fix and
does not invalidate the architectural approach.)

### 3. R14 ALL-remote enumeration

**Verified.** `enumerateGitHubRemotes` at `githubpublic.go:75-108` runs
`git -C configDir remote -v`, walks every line, dedupes by URL (the
`seen` map), and sorts. The `(fetch)`/`(push)` duplicate-line behavior
is explicitly handled. Test
`TestCheckGitHubPublicRemoteSecretsDeduplicatesRemotes` asserts the
error message names the URL exactly once.

Case-insensitivity is handled at the regex level (`(?i)` flag in both
patterns), not at the dedupe key. This means `GitHub.com` and
`github.com` URLs would be counted as distinct entries by the `seen`
map — but that's a user-facing cosmetic wart, not an escape. In practice
`git remote -v` emits the URL the user wrote, so two entries would only
differ if the user deliberately added two remotes with case-variant
URLs. Advisory: if this matters, dedupe on `strings.ToLower(url)`; not
worth blocking.

### 4. R30 one-shot (no state persistence)

**Verified clean.** The function has no package-level mutable state, no
disk writes, no global map. `allowPlaintextSecrets` is a function
parameter passed in from `Applier.AllowPlaintextSecrets`, which is a
struct field populated by the CLI each run. Test
`TestCheckGitHubPublicRemoteSecretsOneShotReevaluates` asserts sequential
invocations re-evaluate from scratch.

### 5. Fallback on git error (skip-with-warning)

**Defensible as a local-UX check.** The PRD frames this guardrail as a
pre-flight reminder, not a security control. An attacker with write
access to the working directory can trivially bypass it regardless
(delete `.git`, point `configDir` at a different tree, edit the TOML
after the check, etc.). Hard-failing on `git` subprocess error would
break legitimate offline workflows (freshly-downloaded tarballs, Nix
store paths, non-git config distribution) without raising the security
bar.

The warning is loud (stderr, single line, names the skip condition) so
users who expected the check can notice. This is the right default.

One advisory note: the warning text at line 209 reads "no git remotes
detected; public-repo guardrail skipped". That's accurate for both the
"not a git tree" and "no remotes at all" cases — fine. But if `git`
itself is missing from PATH, `exec.Command.Output()` returns a distinct
error (ENOENT-class) that currently collapses into the same branch. A
terser message might say "skipped: git unavailable or no remotes" but
this is stylistic, not structural.

### 6. github.com regex rejects github.mycorp.com

**Verified by inspection and by tests.** The regex
`^https?://([\w.-]+@)?github\.com/[\w.-]+/[\w.-]+(\.git)?/?$` anchors the
hostname to `github.com/` — the trailing `/` is required by the next
capture group. For `https://github.mycorp.com/acme/tools.git`, the
regex engine matches `github` fine, then expects `\.com/`, but the
input has `.mycorp.com/`, so matching fails at the `c` of `com`
vs. `m` of `mycorp`. Test
`TestCheckGitHubPublicRemoteSecretsGithubEnterpriseNotFlagged` at
lines 212-237 exercises this plus gitlab/bitbucket.

The regex-samples table test (lines 470-501) also enumerates both
positive and negative cases including the trailing-slash variant and
the missing-repo-path variant.

## Architectural fit

- **Package placement:** `internal/guardrail/` as a new package is the
  right call. It imports `config` (value types only) and stdlib
  (`os/exec`, `regexp`). No upward dependencies. The package doc comment
  pre-registers the expansion pattern (one file per guardrail, one call
  site per guardrail in `apply.go`), which is consistent with the
  existing "explicit wiring" theme in `apply.go` (materializers are a
  slice, vault bundles are built per-layer, shadow detection is a
  separate pure function).

- **Apply pipeline placement:** The call site at `apply.go:361` sits
  between provider-bundle construction (needed for R12 collision
  detection) and the resolver (which auto-wraps, destroying the
  guardrail's discriminant). The rationale is inline at lines 351-360
  and is the only correct position given the auto-wrap contract.

- **No parallel patterns:** The guardrail does not duplicate the
  resolver's walker, does not re-parse TOML, does not add a second
  config pass. It reads the same `WorkspaceConfig` struct fields the
  resolver reads, using the same `MaybeSecret` semantics. The four
  table locations (`cfg.Env.Secrets`, `cfg.Claude.Env.Secrets`,
  `repo.Env.Secrets`, `repo.Claude.Env.Secrets`,
  `cfg.Instance.Env.Secrets`, `cfg.Instance.Claude.Env.Secrets`) match
  the set the resolver walks.

- **Error surface:** Returns a single `error` with a structured message
  naming remotes, keys, and migration path. Does not define a new error
  type, which is consistent with the rest of `apply.go`'s use of
  `fmt.Errorf`.

## Issues

### Blocking

**B1 — pre-resolve `vault://` refs falsely flagged.** `offendingKeys` at
`githubpublic.go:133-143` treats any `MaybeSecret` with non-empty `Plain`
and `IsSecret() == false` as plaintext. Pre-resolve vault references
satisfy both conditions: `Plain == "vault://foo"` and `IsSecret() ==
false`. Users with a valid `vault://` config will hit the guardrail on
first apply, before the resolver has a chance to promote the value.

Fix: skip entries whose `Plain` starts with `vault://`. Add a test with a
raw `MaybeSecret{Plain: "vault://foo"}` input that asserts no error.

### Advisory (not blocking)

**A1 — case-variant URL dedup.** `seen` dedupes on exact URL string.
`GitHub.com` and `github.com` would be counted separately in the error
message. Cosmetic; only surfaces when a user deliberately adds
case-variant remotes. Consider `strings.ToLower(url)` as the map key.

**A2 — skipped warning message specificity.** The "no git remotes
detected; public-repo guardrail skipped" message collapses "not a git
tree", "no remotes", and "git missing from PATH" into one string. Harmless
but could be more precise if users report confusion.

## Approval conditions

With B1 fixed, approve. The rest of the architecture is clean and the
extension pattern is well-established for future guardrails.
