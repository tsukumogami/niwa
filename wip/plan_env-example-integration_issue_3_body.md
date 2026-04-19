---
complexity: testable
complexity_rationale: Self-contained pure function with no filesystem or pipeline dependencies. All test cases drive classifyEnvValue directly with a literal value and assert on the (isSafe, reason) pair. Entropy boundary cases, all 16 blocklist prefixes, all allowlist patterns, and the empty-value case can be exercised in a single table-driven test file.
---

## Goal

Implement `classifyEnvValue` in `internal/workspace/envclassify.go` to detect probable secrets via Shannon entropy and known-vendor-prefix matching, with an allowlist that overrides the entropy check for known-safe patterns.

## Context

Design: `docs/designs/current/DESIGN-env-example-integration.md`

This is Phase 3 of the env-example integration. `classifyEnvValue` is the classification kernel called by the `EnvMaterializer` pre-pass (Issue 4) for each undeclared key found in `.env.example`. It must be independently testable without any `MaterializeContext` or filesystem setup.

The function lives in `internal/workspace/envclassify.go` alongside package-level `envPrefixBlocklist` and `envSafeAllowlist` slices. The blocklist wins over the allowlist and entropy: a value that matches a blocklist prefix is always a probable secret regardless of entropy score or allowlist membership. The allowlist overrides only the entropy check: a value matching an allowlist pattern is safe even when its entropy exceeds the threshold. The reason string names the rule and threshold only — it must never include the value, any fragment of the value, or the raw entropy score computed for a specific value.

## Acceptance Criteria

### Function signature and file placement

- `classifyEnvValue(value string) (isSafe bool, reason string)` is defined in `internal/workspace/envclassify.go` as an unexported function.
- `envPrefixBlocklist` and `envSafeAllowlist` are package-level `[]string` variables in the same file.
- A paired `internal/workspace/envclassify_test.go` exists in the same package (`package workspace`).

### Blocklist behavior

The test table in `envclassify_test.go` contains one or more literal test cases for **each of the following 16 prefixes, hardcoded by name** — the test must not range-iterate over `envPrefixBlocklist` to generate cases:

1. `sk_live_` — e.g. `sk_live_abcdefghijklmnop`
2. `sk_test_` — e.g. `sk_test_abcdefghijklmnop`
3. `AKIA` — e.g. `AKIAxxxxxxxxxxxxxxxx`
4. `ASIA` — e.g. `ASIAxxxxxxxxxxxxxxxx`
5. `ghp_` — e.g. `ghp_xxxxxxxxxxxxxxxxxxxx`
6. `gho_` — e.g. `gho_xxxxxxxxxxxxxxxxxxxx`
7. `ghu_` — e.g. `ghu_xxxxxxxxxxxxxxxxxxxx`
8. `ghs_` — e.g. `ghs_xxxxxxxxxxxxxxxxxxxx`
9. `ghr_` — e.g. `ghr_xxxxxxxxxxxxxxxxxxxx`
10. `github_pat_` — e.g. `github_pat_xxxxxxxxxxxxxxxxxxxx`
11. `glpat-` — e.g. `glpat-xxxxxxxxxxxxxxxxxxxx`
12. `xoxb-` — e.g. `xoxb-xxxx-xxxx-xxxx`
13. `xoxp-` — e.g. `xoxp-xxxx-xxxx-xxxx`
14. `xapp-` — e.g. `xapp-xxxx-xxxx-xxxx`
15. `sq0atp-` — e.g. `sq0atp-xxxxxxxxxxxx`
16. `sq0csp-` — e.g. `sq0csp-xxxxxxxxxxxx`

For each: `isSafe` is `false` and `reason` contains the matched prefix (e.g. `"known prefix sk_live_"`). The blocklist result holds even when the value would otherwise score below the entropy threshold.

### Allowlist behavior

The test table contains one or more literal test cases for **each allowlist pattern, hardcoded by name** — not range-iterated from `envSafeAllowlist`. Patterns must include at least:

- Empty string `""` — `isSafe=true`
- `"changeme"` (and case variants if the implementation is case-insensitive)
- `"placeholder"`
- `"pk_test_xxxxxxxxxxxx"` — safe despite `pk_test_` not being on the blocklist
- `"pk_live_xxxxxxxxxxxx"` — safe
- A value matching `<your-...>` pattern, e.g. `"<your-api-key>"`
- A value containing `example.com`, e.g. `"https://example.com/callback"`
- `"localhost"` and `"127.0.0.1"`

For each: `isSafe` is `true`. Allowlist membership overrides a high entropy score: a value that matches an allowlist pattern and has entropy above 3.5 bits/char must still return `isSafe=true`.

### Entropy boundary behavior

- A value with Shannon entropy strictly below 3.5 bits/char that does not match any blocklist prefix and does not match any allowlist pattern: `isSafe=true`.
- A value with Shannon entropy equal to 3.5 bits/char: test explicitly asserts the boundary case; the implementation documents whether equal is safe or unsafe, and the AC matches the documented choice.
- A value with Shannon entropy strictly above 3.5 bits/char that matches no prefix and no allowlist pattern: `isSafe=false` and `reason` contains the threshold (e.g. `"entropy > 3.5"`).
- Tests use short, static strings with known computed entropy — not randomly generated inputs.

### Empty value

- `classifyEnvValue("")` returns `isSafe=true`. Empty values cannot be secrets.

### R22: reason string must not contain value text

- `reason` must not include the value string, any substring of the value, or the raw floating-point entropy score computed for a specific input.
- `reason` names only the rule and threshold (e.g. `"known prefix ghp_"`, `"entropy > 3.5"`, `"allowlist match"`).
- Tests assert that `reason` does not contain the literal value string passed to the function for any case where `isSafe=false`.

### No external dependencies

- `classifyEnvValue` uses only the Go standard library (`math`, `unicode`, `strings`, or similar). No new imports outside stdlib.

## Dependencies

None

## Downstream Dependencies

- Issue 4 (pre-pass integration) calls `classifyEnvValue(value)` and receives `(isSafe bool, reason string)` to decide whether to accumulate a probable-secret error or emit an undeclared-key warning and include the value.
