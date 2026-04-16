# Issue #6 Pragmatic Review — Materializer Hardening

Target commit: `ddfbf36f261523ebf4a23f8d068bccadf709a8ca`
Branch: `docs/vault-integration`

## Verdict

Approve. No blocking findings.

## Focus Area Assessments

### 1. `injectLocalInfix` applied unconditionally to all `[files]` destinations

Pragmatism call: **keep unconditional**. Making this conditional would be the over-engineered choice, not the simple one.

Reasoning:
- The invariant being enforced is: "every materialized file matches `*.local*`, so the single instance-root `.gitignore` covers everything the workspace writes." Making the infix conditional on "is this destination a secret?" re-introduces the classification problem the design is deliberately avoiding.
- `injectLocalInfix` is already a no-op when the basename contains `.local` (`materialize.go:45-50`). Users who do not want the rename can write `foo.local.json` explicitly — escape hatch is trivial and already exercised by `TestFilesMaterializerPreservesExistingLocal`.
- The `[files]` materializer has no way to know whether a destination is "secret-bearing" without adding a new config axis. That is an architect/maintainer concern if raised at all, and it would be a net-negative simplicity trade.
- Coupling the file-naming invariant to the gitignore pattern is the simplification that justifies the whole approach. Decoupling them would demand a secondary enumeration mechanism.

No finding.

### 2. `EnsureInstanceGitignore` — is it over-engineered?

**No.** The helper is the minimum viable shape:
- 3 branches (no file / has pattern / needs append) — each mapped to a single observable behavior.
- One exported function, one private predicate. No options, no struct, no interface.
- `hasInstanceGitignorePattern` is a 9-line helper called from exactly one place. Under Heuristic 1 this might be a candidate for inlining, but the name carries real meaning (explains *what* the scan is checking for) and keeps the write path readable. **Advisory at most, not worth flagging.**
- The trailing-newline insertion is not gold-plating — it is the behavior a user expects when a merge appends to a hand-edited file that didn't end in `\n`. The test `TestEnsureInstanceGitignoreAppendsPatternWhenNoTrailingNewline` documents this.
- Using `bufio.Scanner` + trim for exact-line match (rather than `bytes.Contains`) avoids the narrow-pattern false positive called out in the doc comment. That is correctness, not over-engineering.

A hand-rolled minimal alternative ("if file exists, append; else create") would be shorter by ~10 lines but would either (a) append duplicates on reruns or (b) need the same scan logic inlined. The current shape is close to optimal.

No finding.

### 3. Test redundancy across the 15 new tests

New tests in this commit:
- **Gitignore** (5): Create / Append / AppendNoTrailingNL / Idempotent / AlreadyHasPattern
- **Mode 0o600** (3): EnvMaterializerWritesMode0600 / SettingsMaterializerWritesMode0600 / FilesMaterializerWritesMode0600
- **Infix** (2): FilesMaterializerInjectsLocalInfix / FilesMaterializerPreservesExistingLocal
- **Reveal** (2): EnvMaterializerRevealsResolvedSecret / SettingsMaterializerRevealsResolvedEnvSecret
- **Integration** (3): CreateNonVaultConfigStillWrites0o600 / CreateWritesInstanceGitignore / CreateMergesInstanceGitignore

Assessment:
- **Gitignore 5**: each test pins a distinct state transition. Dropping any one of them loses a behavior the helper explicitly promises (e.g., no-trailing-newline path is genuinely different code than idempotent no-op). Keep all 5.
- **Mode 0o600 × 3**: one per materializer. Each writes through a different code path (`EnvMaterializer.Materialize`, `SettingsMaterializer.Materialize`, `FilesMaterializer.materializeFile`). Unit tests at the materializer layer are cheaper and more targeted than the `TestCreateNonVaultConfigStillWrites0o600` integration. The integration covers the *bug motivation* ("non-vault configs also get 0o600"); the unit tests cover the *invariant* ("every write path honors `secretFileMode`"). Keeping both is defensible.
- **Reveal × 2**: `EnvMaterializerRevealsResolvedSecret` and `SettingsMaterializerRevealsResolvedEnvSecret` target two different code paths (`ResolveEnvVars` secrets overlay vs. `resolveClaudeEnvVars` promoted-vars inline). Not redundant.
- **Integration × 3**: `TestCreateWritesInstanceGitignore` + `TestCreateMergesInstanceGitignore` could arguably be table-driven into one, but they're independent scenarios with shared scaffolding; splitting is clearer when the test names are the failure message. Not worth a finding.

**One marginal call**: `TestFilesMaterializerWritesMode0600` uses a non-`.local` source (`settings.json` → `.tool/settings.json`). It implicitly exercises `injectLocalInfix` too. `TestFilesMaterializerInjectsLocalInfix` covers the same infix path more directly. There is minor overlap but each asserts a different invariant (mode vs. path), so running both costs ~20 lines of scaffolding. Not flagging.

No finding.

## Other Spot Checks

- `secretFileMode` constant: one symbol, four call sites, clear name. Right call vs. scattering `0o600` literals.
- `maybeSecretString` helper: called from 3 sites across 2 functions, with a non-obvious invariant (post-resolve reveal, must not outlive write). Carries its weight.
- `apply.go:85` — `EnsureInstanceGitignore` called from `Create` only, not `Apply`. This matches the doc comment ("running twice is a no-op after the first run"): new instances get it, existing instances keep whatever `Create` originally wrote. Consistent and deliberate.
- No speculative config flags introduced. No backwards-compat shims. No dead parameters.

## Summary

| Category | Count |
|---|---|
| Blocking | 0 |
| Advisory | 0 |

The commit is tightly scoped: one invariant (`secretFileMode`), one rename helper (`injectLocalInfix`), one setup helper (`EnsureInstanceGitignore`), and the tests that pin each. No scope creep, no speculative generality, no dead paths.
