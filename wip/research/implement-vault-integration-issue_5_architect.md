# Architect Review — Vault Integration Issue #5 (Infisical Backend)

**Target:** commit `dbc217e240bd0884328a595c97c1ff5dca6595ee` on `docs/vault-integration`

**Files reviewed:**
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/infisical.go`
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/subprocess.go`
- `/home/dangazineu/dev/niwaw/tsuku/tsukumogami/public/niwa/internal/vault/infisical/infisical_test.go`

**Supporting references consulted:** `internal/vault/{provider,registry,scrub,errors}.go`, `internal/secret/{value,error,redactor}.go`, `internal/vault/fake/fake.go`, `docs/designs/DESIGN-vault-integration.md` (Decisions 3 & 4), `docs/prds/PRD-vault-integration.md` (R21, R22, R28, Per-backend provenance).

---

## Summary

The backend is structurally well-aligned with Decision 3: Factory + Provider + optional BatchResolver, typed Ref and VersionToken, registration via `vault.DefaultRegistry`. The `commander` indirection is an appropriate extensibility seam that keeps argv-hygiene tests honest without mocking `os/exec`.

The three PRD security invariants under review hold at the code level:
- **R21 (no-argv):** Verified. Only `export`, `--projectId`, `--env`, `--path`, `--format json` reach argv; the Infisical `--projectId`, env slug, and folder path are identifiers (not stored secret values). `TestArgvHygiene` asserts the exact list.
- **R22 (stderr scrub + redact):** Verified. Every non-zero-exit stderr path goes through `vault.ScrubStderr(ctx, ...)` before interpolation; every error touching provider output uses `secret.Errorf`. `TestR22StderrScrubPreventsLeak` exercises the full chain — register fragment on `ctx` redactor, induce CLI failure that echoes the fragment in stderr, assert absence in returned error — and also re-asserts after an outer `fmt.Errorf("%w")` wrap. This test does prove the advertised redaction.
- **R28 (no process-env publication):** Verified. `defaultCommander.Run` sets `cmd.Env = nil` (documented at subprocess.go:61). No `os.Setenv` anywhere in the package. No argument path carries process-env state.

However, one blocking issue exists: the VersionToken is derived from plaintext secret bytes, which contradicts the explicit per-backend derivation rule in Decision 4 and the warning already codified in the fake backend's doc comment. There are also three advisory findings (heuristic substring set, single-flight absence, and a wording mismatch between docstring and implementation).

**blocking_count:** 1
**non_blocking_count:** 3

---

## Findings

### BLOCKING

#### B1. VersionToken derived from plaintext values contradicts Decision 4 derivation rule

**Where:** `internal/vault/infisical/subprocess.go:258-292` (`buildVersionToken`); invoked at line 158.

**Violation.** `buildVersionToken` synthesises `Token` as SHA-256 over the sorted `(key, plaintext-value, NUL)` stream of the entire export payload, then formats the result into `Provenance` as `https://app.infisical.com/projects/%s/audit-logs?version=%s`. This has two separate problems:

1. **Design rule violation.** DESIGN-vault-integration.md Decision 4 (Per-backend provenance, line 511) is explicit:
   > **Infisical** — Token is the Infisical secret-version UUID; Provenance is the audit-log URL for that version.
   The codebase already calls this rule out as a "MUST NOT" for real backends. `internal/vault/fake/fake.go:15-20` documents:
   > "VersionToken.Token is a deterministic SHA-256 hex digest of the value bytes. This is a derivation from post-decrypt plaintext, which real backends MUST NOT do (per DESIGN-vault-integration.md Decision 3 notes on version-token derivation). It is acceptable for the fake because the fixture values are not real secrets."
   The Infisical backend does exactly the derivation the fake is documented as uniquely permitted to do. The code at subprocess.go:99-112 acknowledges the gap in a TODO but ships the synthesised token anyway.

2. **Leak surface.** The token is NOT an internal detail — it propagates widely:
   - It is copied into `secret.Origin.VersionToken` and returned on every Resolve / ResolveBatch (infisical.go:215, 260).
   - It is copied into `config.MaybeSecret.Token` (maybesecret.go:36) for every resolved value.
   - Per Decision 4, it is persisted into `state.json` as `SourceEntry.VersionToken` (design line 499, 956), where it survives across commands.
   - It is interpolated into the `Provenance` URL (line 282) — an unredacted string that goes into `niwa status` output.

   A SHA-256 over plaintext values is not the plaintext, but it IS a high-entropy function of the plaintext. Concretely: given offline access to `state.json` plus the `(key, value)` structure of an export payload, an attacker can brute-force candidate secret values and confirm matches against the stored hash. This is exactly the class of derivation the design rule guards against. The audit-log URL further embeds the same hash as a query parameter in a user-visible string.

**What to change.** Either:
- (a) Use `infisical secrets get --format json` per key to obtain the upstream `version` field per secret, then store that as the Token (matches Decision 4 verbatim); Provenance becomes the audit-log URL for that specific version. This is a per-key call rather than a single export — it undoes the R1 batching benefit, so it may force a design conversation.
- (b) If upstream per-secret version IDs are not yet reliably emitted across the CLI versions the team must support (the TODO's stated reason), synthesise the Token from something that is NOT the plaintext: for example, a SHA-256 over the sorted list of `(key, length)` pairs plus a timestamp bucket. This preserves rotation-detection semantics (any rotation changes at least one length 99%+ of the time, and adding/removing a key always changes the list) while not being a function of decrypted plaintext.
- (c) If the team judges that the rotation-detection property requires a plaintext-derived token for v1 (and accepts the state-file leakage surface), update Decision 4 of DESIGN-vault-integration.md to weaken the Infisical rule, and amend the fake backend's doc comment so it no longer calls out plaintext derivation as something real backends "MUST NOT do." The design document must not contradict ship-merged code.

Without one of these, a future backend author will read `internal/vault/fake/fake.go` and `internal/vault/infisical/subprocess.go` side by side and observe that real backends do exactly what the fake says they must not. That is the parallel-pattern risk this review catches.

---

### ADVISORY (Non-blocking)

#### A1. `looksLikeAuthFailure` substring set is overbroad

**Where:** `subprocess.go:229-256`.

The marker list includes `"auth"` (a strict substring of `"authentication"` and `"unauthori"`, already listed separately) and `"token"`. The `"token"` marker alone will classify any stderr mentioning the word "token" as `ErrProviderUnreachable` — including, plausibly, future Infisical error messages like `"error: rate limit exceeded for token requests"` (a 429) or `"token request failed: network error"` (transient I/O, not auth). `--allow-missing-secrets` downgrades `ErrProviderUnreachable` to a non-fatal empty value, so misclassifying a transient network failure as auth means the user silently ships with an empty secret instead of failing loud.

The false-positive risk is non-zero but contained (the user still sees an error surface in the logs). This is a heuristic in a layer that fundamentally cannot reliably classify CLI output; the alternative (exit-code-only classification) has its own downsides. Not blocking, but consider narrowing to the three markers that are near-unambiguous (`"authentication"`, `"unauthori"`, `"login"`) and dropping `"auth"` (subsumed) and `"token"` (ambiguous).

#### A2. `ensureLoaded` is not actually single-flight

**Where:** `infisical.go:287-323`, with the design claim at `infisical.go:4-11` ("a single `infisical export --format json` invocation per (project, env, path) triple") and at `subprocess.go:79-84`.

The mutex is released before the subprocess runs and re-acquired afterward, with a `!p.loaded` re-check on the second acquisition. This is correct for cache *consistency* (no torn writes), but it is NOT single-flight: N concurrent Resolves during first-load will run N subprocesses, each one a full `infisical export` with its own auth round-trip. Only one of the N results is committed to the cache; the other N-1 are discarded.

For real Infisical workloads this may not matter (the typical path is one synchronous apply with sequential refs), but the package's docstring promises "at most one export per Provider" and callers may rely on that for billing or rate-limit reasoning. The usual fix is `golang.org/x/sync/singleflight` or a `sync.Once` guarding the load. The existing tests run sequentially and do not exercise this.

Either tighten the implementation to match the doc claim, or soften the doc claim to "Resolves issued after the first successful load hit the cache; concurrent first-loads may each run a subprocess." Not blocking because it is a performance concern, not a correctness or security one.

#### A3. Provider `name` empty in error messages when it matters most

**Where:** `infisical.go:199-201`, `292-293`, `313-315`.

Several error paths print `"infisical: provider %q: ..."` with `p.name`. When the Infisical backend is configured as the anonymous singular provider (`[vault.provider]` shape — Decision 3 explicitly supports this, and `Name()` returns `""` for it), the error becomes `infisical: provider "": vault: provider unreachable`. Compare to `runInfisicalExport`'s richer `"infisical: project %q env %q path %q ..."` on the happy-path error branches, which always identify the target. Consider adding project/env context to the close/state errors too, or omitting the `name` field from the message when empty. Purely UX — not blocking.

---

## What's correct

- Factory/Provider/Registry layering matches Decision 3 one-for-one; no parallel abstraction introduced.
- `commander` interface is a tight, test-only seam; no production caller can bypass it since the Factory constructs `defaultCommander{}` by default.
- `cmd.Env = nil` is documented inline against R28 (subprocess.go:57-61). `os.Setenv` does not appear anywhere in the package.
- Argv contains identifiers only; test `TestArgvHygiene` asserts the literal list.
- Every error path that touches CLI output goes through `secret.Errorf` (subprocess.go lines 130, 138, 143, 152; infisical.go lines 199, 206, 239, 250, 291, 313). No `fmt.Errorf` leaks raw stderr into an error chain.
- Stderr is captured to a buffer (never streamed) and scrubbed through `vault.ScrubStderr(ctx, ...)` (subprocess.go:136, 151) before interpolation. `ScrubStderr` applies both the context-attached Redactor and a fresh known-fragments Redactor (scrub.go:35-58).
- The R22 acceptance test (`TestR22StderrScrubPreventsLeak`) is genuinely adversarial: it pre-registers a fragment on the context redactor, embeds that exact fragment in stderr, and asserts both the immediate error string and an outer `fmt.Errorf("%w")` wrap contain the placeholder instead of the fragment. The shared Redactor across error-chain depth is what makes this work (error.go:73-96) and the test exercises it.
- `init()` panic-on-duplicate is the right idempotency posture for `DefaultRegistry`; `go test -count=N` runs `init` only once per binary invocation so there is no flake risk.
- Dependency direction is correct: `internal/vault/infisical` imports `internal/vault` and `internal/secret`, not the reverse.
- `Close` is idempotent and clears the cache; post-Close Resolve returns `ErrProviderUnreachable`. Exercised by `TestCloseClearsCache`.
- `BatchResolver` shares `ensureLoaded` with `Resolve` — one cache, one subprocess lifecycle. Correct.
- Both Infisical CLI output shapes (flat object and array-of-entries) are handled in `parseExportJSON` with a clean error for unexpected top-level tokens.
