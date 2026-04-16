# Issue #5 Maintainability Review — Infisical Backend

**Commit**: `dbc217e240bd0884328a595c97c1ff5dca6595ee` on `docs/vault-integration`
**Files**:
- `internal/vault/infisical/infisical.go`
- `internal/vault/infisical/subprocess.go`
- `internal/vault/infisical/infisical_test.go`

**Verdict**: Approve. Two advisory findings worth addressing before/after merge.

---

## Focus-area pass

### 1. Package-level GoDoc (role + user-install-CLI contract)

`infisical.go:1-34` — clear. Covers: role (v1 backend, Provider + BatchResolver), user-install contract ("user-installed `infisical` CLI (R20 — no Go SDK dependency)"), lazy semantics, auth model (cmd.Env=nil, CLI reads its own creds), argv hygiene (R21), stderr hygiene (R22), registration. This is the single most important piece of documentation in the backend and it lands. A new developer can read these 33 lines and form an accurate mental model before scrolling further.

**No findings.**

### 2. Error messages from subprocess path name context

`infisical.go:207` and `:251` — key-not-found errors include project/env/path/key, which is exactly the context a debugger needs when a user reports "my-key not found" and it turns out they pointed at the wrong env.

`subprocess.go:131, 139-141, 144-146, 153-154` — subprocess errors include exit code and scrubbed stderr. They do **not** include project/env/path. When a user sees `infisical: export exited 1: Error: project not found`, they have to cross-reference which project is configured. Not a blocker — the stderr from the Infisical CLI typically echoes the project ID — but adding the niwa-visible values would make the error self-contained.

**Finding M1 (advisory)** — subprocess error messages omit project/env/path. Consider including them so the error is self-contained without having to correlate with config. Example: `"infisical: export(project=%q env=%q path=%q) exited %d: %s"`.

### 3. `commander` test-injection hook documentation

`infisical.go:81` — the config key `"_commander"` appears in the Open GoDoc with a `// test-only.` comment, and the underscore prefix is a clear signal to readers.

`infisical.go:137-144` — the inline comment "Test-only hook: allow a caller to inject a fake commander." reinforces it.

`subprocess.go:19-32` — the `commander` interface GoDoc explains why it exists ("so tests can inject a deterministic stub without forking a real `infisical` binary") and what production uses (`defaultCommander`).

Clear. The next developer will not accidentally reach for `_commander` in production code.

**No findings.**

### 4. TODO comments for v1.1 per-key version IDs

`subprocess.go:107-112` — actionable and complete:
- What to change: "replace the synthesised token with the upstream ID"
- When to change: "when `infisical secrets get --format json` per-key version IDs become reliably available across the CLI versions we support"
- Why the current approximation is acceptable: "our primary use of VersionToken is rotation detection (any change to any value rotates the token) — not single-key provenance"

This is the kind of TODO the next developer can act on without archaeology.

**No findings.**

### 5. `TestR22StderrScrubPreventsLeak` documentation

`infisical_test.go:352-368` — strong. Documents:
- What regression it guards: R22 (redact-logs invariant).
- The concrete scenario: prior Resolve registered a secret on the context Redactor; a later Resolve fails with stderr echoing that secret.
- The guard mechanism: `vault.ScrubStderr` on stderr before error interpolation.
- What happens if it fails: "If this test ever fails, R22 is broken. Investigate before merging."

The next developer who sees this test go red will understand the security stakes without hunting for context.

**No findings.**

### 6. Test names describe scenarios

Scanning the suite: `TestFactoryKind`, `TestInfisicalFactoryRegisteredInDefaultRegistry`, `TestFactoryOpenRejectsMissingProject`, `TestFactoryOpenRejectsMalformedConfig`, `TestOpenIsLazy`, `TestResolveFetchesAndCaches`, `TestResolveReturnsKeyNotFound`, `TestResolveBatch`, `TestArgvHygiene`, `TestAuthFailureMapsToUnreachable`, `TestGenericFailureDoesNotMapToUnreachable`, `TestStartFailureMapsToUnreachable`, `TestR22StderrScrubPreventsLeak`, `TestR22ScrubStderrWithKnownValues`, `TestCloseClearsCache`, `TestEnvAndPathDefaults`, `TestArrayShapeParses`, `TestEmptyExportParses`, `TestMalformedJSONIsGenericError`, `TestResolveReturnsSecretValue`, `TestTokenChangesOnRotation`, `TestLooksLikeAuthFailure`, `TestRegistryBuildsInfisicalProvider`.

All describe scenarios or observable behavior ("FetchesAndCaches", "ReturnsKeyNotFound", "MapsToUnreachable", "ChangesOnRotation"). One test, `TestLooksLikeAuthFailure`, is named after the helper it exercises rather than a scenario — acceptable since the test IS explicitly documented as a direct helper exercise and the assertions match the name.

**No findings.**

---

## Additional maintainability observations

### M2 (advisory) — Auth-marker substring ordering is surprising but not buggy

`subprocess.go:243-249` — the marker slice is:

```
"authentication",
"unauthori",
"auth",
"login",
"token",
```

The next developer will notice `"auth"` is a substring of `"authentication"` and wonder whether the ordering or the longer marker is load-bearing. It isn't — the loop short-circuits on the first match, and since this is substring-based any occurrence of "auth" matches regardless of whether "authentication" or "unauthori" is listed. That makes the first two entries effectively redundant.

Two options, either fine:
- Drop `"authentication"` and `"unauthori"` (redundant given `"auth"`).
- Add a one-line comment explaining they are kept for readability / grep-ability even though `"auth"` would catch them.

The current code is correct; this is purely a readability-for-the-next-reader note. Advisory.

### Stale-comment / name check

`subprocess.go:44-46` — "Constants (command name, argv flag names) live on the type so tests that want to probe argv hygiene can do so via the commander indirection." The constants are not actually defined on `defaultCommander`; they are literals inline in `runInfisicalExport` (`"export"`, `"--projectId"`, etc.). This comment describes an intent the code does not carry out.

Either:
- Hoist the argv flag names into named constants on `defaultCommander` or the package (so the comment's claim is true), or
- Rewrite the comment to match reality (something like: "argv flag names are literals in runInfisicalExport; tests probe them by capturing the `args` slice passed to commander.Run").

As written the comment sends the next developer looking for something that isn't there. Advisory, but close to blocking because of the "stale comment" heuristic. Calling advisory because the misread is local — the reader is already at subprocess.go, the literals are 80 lines below, and the tests demonstrate the real pattern.

### Minor clarity observations (not findings)

- `infisical.go:160-169` — the `Provider` struct comment on `values` ("plaintext values as returned by `infisical export --format json`") explicitly calls out that the cache holds plaintext. Good warning for the next developer thinking about logging or serialising Provider state.
- `infisical.go:287-323` — `ensureLoaded` has a useful multi-line comment about releasing the mutex across subprocess I/O and re-checking `loaded` after re-acquiring. A reader would otherwise wonder about the double-check; the comment pre-answers the question.
- `subprocess.go:126-133` — explicitly documents why the start-error `err.Error()` is safe to interpolate without scrubbing (filesystem/syscall message, not secret material). Exactly the kind of invisible-safety reasoning the next developer benefits from.
- `infisical_test.go:67-83` — `openWithCommander` casts the fake to the unexported `commander` interface at the call site (`commander(cmd)`). This is a nice pattern that keeps the test intent visible (we ARE using the test hook) and the cast local.

---

## Summary

| Severity | Count | Items |
|----------|-------|-------|
| Blocking | 0 | — |
| Advisory | 2 | M1 (subprocess error context), M2 (auth markers redundancy + stale constants-on-type comment) |

The package reads cleanly. Package-level GoDoc, invariant callouts (R20/R21/R22/R28), TODO for v1.1 version IDs, and the R22 regression-test narrative are all above the bar for maintainability. Approve.
