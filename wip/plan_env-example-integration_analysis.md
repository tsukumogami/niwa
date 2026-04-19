# Plan Analysis: DESIGN-env-example-integration

review_rounds: 1

## Source Document
Path: docs/designs/current/DESIGN-env-example-integration.md
Status: Accepted
Input Type: design

## Scope Summary
Add `.env.example` as the lowest-priority layer in `ResolveEnvVars`, covering
Node-style parsing, secrets exclusion, entropy-based classification for undeclared
keys, and per-repo public-remote guardrail. Six implementation phases across four
packages, adding two new files and modifying four existing ones.

## Components Identified
- **Config schema**: `ReadEnvExample *bool` on `WorkspaceMeta` and `RepoOverride`
  in `internal/config/workspace.go`; `effectiveReadEnvExample` resolver
- **Node-style parser**: `parseDotEnvExample` in `internal/workspace/env_example.go`
  — single/double quotes, `export` prefix, CRLF, per-line tolerance
- **Value classifier**: `classifyEnvValue` in `internal/workspace/envclassify.go`
  — Shannon entropy, `envPrefixBlocklist`, `envSafeAllowlist`
- **Pre-pass integration**: `EnvMaterializer.Materialize` pre-pass in
  `internal/workspace/materialize.go`; two new `MaterializeContext` fields;
  `Stderr io.Writer` on `EnvMaterializer`; nil-guard in `ResolveEnvVars`;
  `apply.go` construction update
- **Public-repo guardrail**: per-repo `enumerateGitHubRemotes` call against
  `ctx.RepoDir` for undeclared probable-secret keys
- **Source attribution**: `SourceKindEnvExample` constant; `--verbose` display
  in `internal/cli/status.go`

## Implementation Phases (from design)

### Phase 1: Config schema and opt-out flags
Add `ReadEnvExample *bool` to the workspace-level config struct and per-repo
override struct in `internal/config/`. Add a helper `effectiveReadEnvExample(ws,
repoName string) bool` that resolves workspace default → per-repo override with
nil-pointer semantics (nil = unset = inherit/default-true).

Deliverables:
- `internal/config/workspace.go` — schema additions
- `internal/config/workspace_test.go` — TOML round-trip tests for the new fields

### Phase 2: Node-style parser
Implement `parseDotEnvExample` in `internal/workspace/env_example.go` covering:
single-quoted literals, double-quoted escape sequences (`\n`, `\t`, `\"`, `\\`),
`export KEY=VALUE` prefix, CRLF normalization, blank-line and comment skipping,
per-line tolerance (bad line → warning + continue, no abort).

Deliverables:
- `internal/workspace/env_example.go`
- `internal/workspace/env_example_test.go` (table-driven, all R6 variants)

### Phase 3: Value classifier
Implement `classifyEnvValue` in `internal/workspace/envclassify.go` with
Shannon entropy calculation, `envPrefixBlocklist` check (blocklist wins
regardless of entropy or allowlist), and `envSafeAllowlist` check (allowlist
overrides entropy check for undeclared keys).

Deliverables:
- `internal/workspace/envclassify.go`
- `internal/workspace/envclassify_test.go` (table-driven: entropy boundaries,
  all blocklist prefixes, all allowlist patterns, empty value)

### Phase 4: Pre-pass integration
Wire everything together in `EnvMaterializer.Materialize`:
- Add `Stderr io.Writer` field and `stderr()` helper to `EnvMaterializer`
- Add `EnvExampleVars`, `EnvExampleSources` to `MaterializeContext`
- Write the pre-pass (opt-out → symlink check → size guard → parse → exclude →
  classify → store)
- Add nil-guarded opening block to `ResolveEnvVars`
- Update `apply.go` to pass `os.Stderr` to `EnvMaterializer.Stderr`

Deliverables:
- `internal/workspace/materialize.go` — pre-pass + nil-guard
- `internal/workspace/apply.go` — materializer construction
- Integration tests verifying end-to-end materialization with a sample `.env.example`

### Phase 5: Public-repo guardrail for `.env.example`
Add per-repo remote detection in the pre-pass using `enumerateGitHubRemotes`
against `ctx.RepoDir`. When a probable-secret key is found and the repo's remote
is public, fail with the guardrail error. Integrate with `--allow-plaintext-secrets`.

Deliverables:
- Pre-repo guardrail call in the pre-pass
- Test: public-remote repo with undeclared high-entropy key → apply fails

### Phase 6: Source attribution in `niwa status --verbose`
Add `SourceKindEnvExample` to the `SourceKind` enumeration. Ensure
`EnvExampleSources` entries use this kind. Update the `--verbose` output
path to display `.env.example` as the source label.

Deliverables:
- `internal/workspace/materialize.go` — new `SourceKind` constant
- `internal/cli/status.go` — `--verbose` output update

## Success Metrics
- `.env.example` values appear in `.local.env` as lowest-priority defaults
- Secrets-declared keys are never overridden by `.env.example` values
- Probable-secret undeclared keys cause apply to fail with a clear error
- `parseEnvFile` is unchanged; Node-style syntax is handled by the new parser
- Classification is independently testable without pipeline setup
- `--verbose` shows `.env.example` as the source label

## External Dependencies
- `internal/guardrail/githubpublic.go` — `enumerateGitHubRemotes` helper reused
  in per-repo guardrail call
- `internal/workspace/materialize.go` — `ResolveEnvVars`, `EnvMaterializer`,
  `MaterializeContext` (all existing)
- `internal/config/workspace.go` — `WorkspaceMeta`, `RepoOverride` (extending)
- `internal/cli/status.go` — existing `--verbose` output path (updating)
