# Test Plan: .env.example Integration

Generated from: docs/designs/current/DESIGN-env-example-integration.md
Issues covered: 6
Total scenarios: 20

---

## Scenario 1: WorkspaceMeta accepts read_env_example TOML field
**ID**: scenario-1
**Category**: infrastructure
**Testable after**: Issue 1
**Commands**:
- `go test ./internal/config/... -run TestParse -v`
**Expected**: TOML round-trip tests pass; a workspace.toml with `[workspace] read_env_example = false` parses into `WorkspaceMeta.ReadEnvExample` as a non-nil pointer to `false`; omitting the field leaves the pointer nil
**Status**: pending

---

## Scenario 2: RepoOverride accepts read_env_example TOML field
**ID**: scenario-2
**Category**: infrastructure
**Testable after**: Issue 1
**Commands**:
- `go test ./internal/config/... -run TestParseRepoOverride -v`
**Expected**: A `[repos.myapp] read_env_example = true` section parses into `RepoOverride.ReadEnvExample` as a non-nil pointer to `true`; omitting the field leaves the pointer nil (inherit)
**Status**: pending

---

## Scenario 3: effectiveReadEnvExample resolver logic — all four combinations
**ID**: scenario-3
**Category**: infrastructure
**Testable after**: Issue 1
**Commands**:
- `go test ./internal/config/... -run TestEffectiveReadEnvExample -v` (or equivalent unit test name)
**Expected**: All four table entries hold:
- workspace=nil, repo=nil → true (default-on)
- workspace=false, repo=nil → false
- workspace=false, repo=true → true (per-repo override wins)
- workspace=true, repo=false → false (per-repo suppression wins)
**Status**: pending

---

## Scenario 4: parseDotEnvExample — basic Node-style syntax variants
**ID**: scenario-4
**Category**: infrastructure
**Testable after**: Issue 2
**Commands**:
- `go test ./internal/workspace/... -run TestParseDotEnvExample -v`
**Expected**: All table cases pass: single-quoted literals, double-quoted escapes (`\n`, `\t`, `\"`, `\\`), `export KEY=VALUE` prefix, CRLF normalization, blank-line skipping, comment skipping; duplicate keys resolve to last-wins
**Status**: pending

---

## Scenario 5: parseDotEnvExample — per-line tolerance and error format
**ID**: scenario-5
**Category**: infrastructure
**Testable after**: Issue 2
**Commands**:
- `go test ./internal/workspace/... -run TestParseDotEnvExample -v`
**Expected**: Lines with invalid key characters, missing `=`, or unknown double-quote escape sequences produce per-line warnings in `file:line:problem` format; no value text appears in any warning string; the rest of the file continues to parse; the error return is nil for these cases
**Status**: pending

---

## Scenario 6: parseDotEnvExample — whole-file failure modes
**ID**: scenario-6
**Category**: infrastructure
**Testable after**: Issue 2
**Commands**:
- `go test ./internal/workspace/... -run TestParseDotEnvExample -v`
**Expected**: Permission denied returns a non-nil error; binary content returns a non-nil error; files larger than 512 KB return a non-nil error; the returned map is nil in all three cases
**Status**: pending

---

## Scenario 7: classifyEnvValue — blocklist prefixes (all 16)
**ID**: scenario-7
**Category**: infrastructure
**Testable after**: Issue 3
**Commands**:
- `go test ./internal/workspace/... -run TestClassifyEnvValue -v`
**Expected**: Table hardcoding all 16 prefixes (`sk_live_`, `sk_test_`, `AKIA`, `ASIA`, `ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`, `glpat-`, `xoxb-`, `xoxp-`, `xapp-`, `sq0atp-`, `sq0csp-`) returns `isSafe=false`; reason contains the prefix string; blocklist result holds even when entropy is below 3.5
**Status**: pending

---

## Scenario 8: classifyEnvValue — safe allowlist patterns
**ID**: scenario-8
**Category**: infrastructure
**Testable after**: Issue 3
**Commands**:
- `go test ./internal/workspace/... -run TestClassifyEnvValue -v`
**Expected**: All nine allowlist values (`""`, `"changeme"`, `"placeholder"`, `"pk_test_xxxxxxxxxxxx"`, `"pk_live_xxxxxxxxxxxx"`, `"<your-api-key>"`, `"https://example.com/callback"`, `"localhost"`, `"127.0.0.1"`) return `isSafe=true`; allowlist overrides entropy (a high-entropy value matching the allowlist is safe)
**Status**: pending

---

## Scenario 9: classifyEnvValue — entropy boundary
**ID**: scenario-9
**Category**: infrastructure
**Testable after**: Issue 3
**Commands**:
- `go test ./internal/workspace/... -run TestClassifyEnvValue -v`
**Expected**: A value with Shannon entropy exactly 3.5 bits/char is tested and the result documents the boundary decision (either safe or unsafe, consistent with implementation); entropy strictly above 3.5 without prefix or allowlist match returns `isSafe=false` with reason containing `"entropy > 3.5"`; entropy strictly below 3.5 returns `isSafe=true`
**Status**: pending

---

## Scenario 10: classifyEnvValue — reason never contains value text
**ID**: scenario-10
**Category**: infrastructure
**Testable after**: Issue 3
**Commands**:
- `go test ./internal/workspace/... -run TestClassifyEnvValue -v`
**Expected**: For every `isSafe=false` case in the test table, the `reason` string does not contain the literal value that was classified; no entropy score applied to a specific value appears in reason
**Status**: pending

---

## Scenario 11: EnvMaterializer pre-pass — opt-out gates pre-pass per-repo
**ID**: scenario-11
**Category**: infrastructure
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- `go test ./internal/workspace/... -run TestEnvMaterializerEnvExample -v`
**Expected**: With workspace `read_env_example=false`, pre-pass is skipped for all repos and `ctx.EnvExampleVars` remains nil; with workspace `true` but per-repo `false`, that repo's pre-pass is skipped; both nil → pre-pass runs
**Status**: pending

---

## Scenario 12: EnvMaterializer pre-pass — symlink and absent file handling
**ID**: scenario-12
**Category**: infrastructure
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- `go test ./internal/workspace/... -run TestEnvMaterializerEnvExample -v`
**Expected**: A symlink at `.env.example` emits a warning to the injected `Stderr` buffer and leaves `ctx.EnvExampleVars` unset; an absent `.env.example` is a silent short-circuit with no output and no error
**Status**: pending

---

## Scenario 13: EnvMaterializer pre-pass — secrets exclusion across all three layers
**ID**: scenario-13
**Category**: infrastructure
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- `go test ./internal/workspace/... -run TestEnvMaterializerEnvExample -v`
**Expected**: A key declared as a secret in workspace `[env.secrets]`, in `[claude.env.secrets]`, and in per-repo `[env.secrets]` is each independently excluded from `ctx.EnvExampleVars`; no diagnostic is emitted for excluded keys; `ctx.EnvExampleVars` does not contain any of these keys
**Status**: pending

---

## Scenario 14: EnvMaterializer pre-pass — undeclared safe key warns without failing
**ID**: scenario-14
**Category**: infrastructure
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- `go test ./internal/workspace/... -run TestEnvMaterializerEnvExample -v`
**Expected**: An undeclared key with a safe value (e.g., `DATABASE_URL=localhost`) emits a warning naming the key (not the value) to the injected `Stderr` buffer; the key is present in `ctx.EnvExampleVars`; `Materialize` returns nil error
**Status**: pending

---

## Scenario 15: EnvMaterializer pre-pass — undeclared probable secret accumulates error and fails
**ID**: scenario-15
**Category**: infrastructure
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- `go test ./internal/workspace/... -run TestEnvMaterializerEnvExample -v`
**Expected**: An undeclared key with a probable-secret value accumulates an error (key + reason, no value text); after all keys are processed, `Materialize` returns a non-nil error containing the key name; `ctx.EnvExampleVars` is not set; captured stderr contains no substring of the secret value
**Status**: pending

---

## Scenario 16: .env.example is the lowest-priority layer (end-to-end override)
**ID**: scenario-16
**Category**: use-case
**Environment**: automatable — uses filesystem only; no network
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- Set up a temp workspace with a managed app repo containing `.env.example` with `APP_URL=from-example`
- Add `[env.vars] APP_URL = "from-workspace"` to workspace.toml
- Run `niwa apply` (or equivalent test helper)
- Read `.local.env` in the repo directory
**Expected**: `.local.env` contains `APP_URL=from-workspace`; the workspace vars layer has overridden the `.env.example` value; `niwa status --verbose` (scenario 19) would show the plaintext source, not `.env.example`
**Status**: pending

---

## Scenario 17: Public-remote guardrail blocks probable-secret keys from public repos
**ID**: scenario-17
**Category**: use-case
**Environment**: environment-dependent — requires a managed app repo with a GitHub remote configured as public
**Testable after**: Issues 1, 2, 3, 4, 5
**Commands**:
- Configure a managed repo with a public GitHub remote
- Place `.env.example` with an undeclared high-entropy key (e.g., `SECRET_KEY=sk_live_XXXXXXXXXXXXXXXXXXXX`)
- Run `niwa apply`
**Expected**: `niwa apply` exits non-zero; error message names the key and that the remote is public; no value text appears in stderr; the `.local.env` file is not written with the secret key
**Status**: pending

---

## Scenario 18: --allow-plaintext-secrets bypasses public-remote guardrail
**ID**: scenario-18
**Category**: use-case
**Environment**: environment-dependent — requires a managed app repo with a public GitHub remote
**Testable after**: Issues 1, 2, 3, 4, 5
**Commands**:
- Same setup as scenario 17
- Run `niwa apply --allow-plaintext-secrets`
**Expected**: `niwa apply` succeeds; the key is present in `.local.env`; no guardrail error is emitted
**Status**: pending

---

## Scenario 19: niwa status --verbose shows .env.example source label
**ID**: scenario-19
**Category**: use-case
**Environment**: automatable — uses filesystem only; no network
**Testable after**: Issues 1, 2, 3, 4, 6
**Commands**:
- Set up a temp workspace with a managed app repo containing `.env.example` with a safe undeclared key, e.g., `LOG_LEVEL=debug`
- Run `niwa apply`
- Run `niwa status --verbose`
**Expected**: The verbose output shows `.env.example` (literal string) as the source for `LOG_LEVEL`; the label is not `plaintext`, `vault`, or any other existing kind; existing keys from other sources still display their correct source labels (no regressions)
**Status**: pending

---

## Scenario 20: Declared [env.vars] key from .env.example requires no classification warning
**ID**: scenario-20
**Category**: use-case
**Environment**: automatable — uses filesystem only; no network
**Testable after**: Issues 1, 2, 3, 4
**Commands**:
- Set up a temp workspace with `[env.vars] DB_HOST = ""` (declared but empty)
- Place `.env.example` with `DB_HOST=high-entropy-value-XXXXXXXXXXXXXXXXXXX`
- Run `niwa apply`
- Inspect stderr output
**Expected**: No classification warning is emitted for `DB_HOST` (declared keys bypass classification); `niwa apply` succeeds; `.local.env` reflects the workspace-declared value (empty string or whatever the workspace resolves), not the `.env.example` value
**Status**: pending
