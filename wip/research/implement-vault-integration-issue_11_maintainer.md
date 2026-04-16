# Issue 11 Maintainer Review — Vault Integration Docs

Commit `f1dd62bb290fc63b1c8acaa2d40342bb848bd685`, branch `docs/vault-integration`.

Files:
- `docs/guides/vault-integration.md` (new)
- `docs/guides/vault-integration-acceptance-coverage.md` (new)

## Verification scope performed

- Spot-checked 30+ test-function references in the AC matrix against the
  actual test files. Every named function exists at the claimed path.
- Every CLI flag the guide documents (`--allow-missing-secrets`,
  `--allow-plaintext-secrets`, `--audit-secrets`, `--check-vault`) exists
  in `internal/cli/{apply,status}.go`.
- Every code example references config shapes actually parsed: `[vault.provider]`
  with `kind` + `project`, `[vault.providers.<name>]`, `[env.vars]` /
  `[env.secrets]`, `[workspace].vault_scope`, `[vault].team_only`,
  `[global.vault.provider]`, per-workspace overlay blocks.
- `vault://<key>?required=false` URI syntax exists (`internal/vault/ref.go`
  and `TestParseRefRequiredFalse`).
- `niwa config set global <repo>` shape matches `internal/cli/config_set.go`.
- `.local.env` filename, `0o600` mode, and `*.local*` gitignore pattern
  are accurate (`internal/workspace/materialize.go`,
  `internal/workspace/gitignore.go`).
- No AI writing patterns (comprehensive/robust/leverage/facilitate/etc.)
  found in either file.

## Blocking findings

### B1. Quick-start Step 5 describes a stderr message `niwa apply` does not emit

`docs/guides/vault-integration.md:313-315` (Plaintext-to-vault migration
Step 5) and `:68-71` (Quick start Step 5, implicitly):

> "niwa resolves the refs, re-materializes the affected files, and
> (if a team secret rotated upstream) prints `rotated <path>` to stderr."

There is no `rotated` stderr emission in `internal/workspace/apply.go`.
The `rotated`-style message lives in `internal/cli/status_check_vault.go:221`
as `vault-rotated %s\n` — and that path is ONLY invoked by
`niwa status --check-vault`, never by `niwa apply`. The guide even
has the prefix wrong (`rotated` vs `vault-rotated`).

A developer using the guide to predict what apply will print (a CI log
grep, for instance) will debug into the apply codebase looking for a
log line that does not exist. **Blocking** because the misread sends
the next developer into a wrong-codepath debugging detour.

Fix: either remove the "prints `rotated <path>`" clause, or redirect
the reader to `niwa status --check-vault` for the user-visible
rotation readout and describe what `apply` actually does (silently
re-materializes; state fingerprint updates).

### B2. `*.required` behavior promised by the guide is not implemented

`docs/guides/vault-integration.md:157-166` (Requirement sub-tables table):

> | `*.required` | Hard error; `niwa apply` fails. |

and

> `--allow-missing-secrets` downgrades vault misses to empty strings
> but does NOT downgrade `*.required` misses. A required key remains
> a hard error even with the flag set.

The `Required` / `Recommended` / `Optional` sub-tables ARE parsed
(`internal/config/env_tables.go:52-58`) and stored on `EnvVarsTable`,
but no code path iterates over them to enforce the claimed behavior.
`grep`-ing `internal/` finds every read-site is either
a copy/deepcopy helper (`override.go:743`, `resolve/deepcopy.go:140`) or
a test. Nothing in the resolver or applier consults `Required`.

The runtime behavior you actually get:

- If the user writes `GITHUB_TOKEN = "vault://..."` in `[env.secrets]`
  AND the vault provider returns `ErrKeyNotFound`, the resolver errors
  by default and is downgraded by `--allow-missing-secrets`
  (`resolve.go:514-524`). This is the behavior the PRD R34 says must
  NOT happen for required keys.
- If the user only lists `GITHUB_TOKEN` in `[env.secrets.required]`
  (the description-only classification) and forgets to reference it in
  `[env.secrets]` at all, nothing fails. The required metadata is dead.

The PRD does call for this behavior (R34, PRD:869-877) but the code
shipped in this branch does not implement it. The guide documents the
PRD promise, not the code.

A developer who leans on `[env.secrets.required]` to enforce a load-bearing
credential will get silent-pass behavior where they expected a loud
failure. This is textbook misread-leads-to-bug territory. **Blocking.**

Fix options:
1. Either wire up `Required` enforcement in the resolver/applier
   before the guide promises it; OR
2. Rewrite the table row for `*.required` to match actual behavior
   (today it is "description-only metadata surfaced in
   provider-miss diagnostics") and remove the
   `--allow-missing-secrets`-immunity claim until R34 lands; OR
3. Mark the section "Planned for v1.x; in v1 only the flag
   precedence is shipped" and cross-reference the tracking issue.

### B3. Two AC-matrix rows map to tests that do not cover the stated AC

`docs/guides/vault-integration-acceptance-coverage.md:41`:

> `--allow-missing-secrets` does NOT downgrade `[env.required]` →
> `internal/cli/apply_test.go` → `TestApplyCmd_AllowFlagsThreadToApplier`

`TestApplyCmd_AllowFlagsThreadToApplier` (apply_test.go:108-133) only
asserts that cobra's `ParseFlags` populates the package-level flag
variables. It does NOT exercise R34 (required-key precedence over
`--allow-missing-secrets`). No test in the repo does — see B2.

`docs/guides/vault-integration-acceptance-coverage.md:42`:

> 2 sources with no `vault_scope` fails with ambiguity error →
> `TestResolveGlobalOverridePerWorkspaceBlock` (scope selection;
> ambiguity path covered by scope test)

`TestResolveGlobalOverridePerWorkspaceBlock`
(resolve_test.go:446-475) sets exactly one workspace entry and
resolves against it. It does not assert any ambiguity error. I ran
`grep -ri ambigu internal/` and found zero hits that relate to
multi-source vault-scope selection. The AC has no coverage; the matrix
claims otherwise.

The AC matrix is a correctness claim. A developer cutting a
future release will point at these rows as evidence the invariants
are locked in and will skip a coverage review. **Blocking** because
it misrepresents the safety net.

Fix: Either flag these rows as `UNCOVERED` (parallel to the existing
`ORPHANED` convention for deferred features), or add the missing
tests before merging docs that assert coverage.

## Non-blocking findings

### A1. `TestArgvHygiene` is claimed to cover an invariant it doesn't assert

`docs/guides/vault-integration-acceptance-coverage.md:79`:

> niwa never calls `os.Setenv` during apply →
> `TestArgvHygiene (covers argv + env invariants for the backend call)`

`TestArgvHygiene` (infisical_test.go:264-292) inspects `capturedName`
and `capturedArgs` only. It does not assert `cmd.Env == nil`, does
not assert `os.Setenv` was never called, and the `fakeCommander`
struct has no `capturedEnv` field. The invariant lives in production
code (`subprocess.go:62` sets `cmd.Env = nil` with an R28 comment)
but there is no regression test. Less severe than B3 because the
linked test IS related work, but the parenthetical overstates what
it checks. **Advisory.**

### A2. Guardrail "no `.git` tree" wording is slightly looser than the implementation

`docs/guides/vault-integration.md:337-339`:

> "When the config directory has no `.git` tree, the guardrail emits
> a warning and proceeds."

The actual trigger (`guardrail/githubpublic.go:229-232`) is "no
`git remote -v` output" — which fires on both a non-git tree AND
a git tree with no remotes configured. The warning text in code says
"no git remotes detected" which is the accurate characterization. Low
risk of misread because both conditions land in the same "can't
check" bucket, but the stricter code wording is cleaner. **Advisory.**

### A3. Quick start implies guardrail runs but doesn't explain when it's silent

Quick start Step 5 runs `niwa apply` on a fresh local workspace and
describes success. If a first-time reader copies the setup into a
directory that isn't a git repo OR is a git repo with no remote, the
public-repo guardrail silently skips (A2 above). The guide's
"Public-repo guardrail" section later explains this, but a skim
reader who stops after Quick start won't know the guardrail didn't
run. **Advisory.** Adding a single line to the Public-repo guardrail
section cross-referencing Quick start (or vice versa) would close
the loop.

## Accurate checks that came back clean

- All CLI flags exist and match the four-flag list Issue 10 added.
- AC-matrix test names resolve 1:1 to existing test functions (30+
  spot-checks); the rows that are OVERSTATED in scope are called out
  above. No rows point at genuinely nonexistent tests.
- Schema examples (all TOML blocks) parse against the types in
  `internal/config/config.go` and `internal/config/vault_test.go`.
- `niwa config set global <repo>` exists and matches the usage.
- `infisical export --projectId --env --path --format json` shape
  matches `subprocess.go:123-129`.
- The `--audit-secrets` table columns `KEY / CLASSIFICATION / TABLE /
  SHADOWED` match `status_audit.go:237`.
- `--allow-plaintext-secrets` is one-shot-in-apply with re-evaluation
  on next invocation (verified against
  `TestCheckGitHubPublicRemoteSecretsOneShotReevaluates`).
- `?required=false` URL parameter parses and downgrades silently
  (verified against `ref.go:17-18`, `resolve.go:515-517`).
- Link targets (`../prds/PRD-vault-integration.md §Threat Model`,
  `../designs/DESIGN-vault-integration.md §Security Considerations`)
  resolve to existing sections.
- ORPHANED rows for sops are correctly flagged and explained.
- No AI-writing patterns in either file.
