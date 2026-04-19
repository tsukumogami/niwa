---
design_doc: docs/designs/current/DESIGN-env-example-integration.md
input_type: design
decomposition_strategy: horizontal
strategy_rationale: "Six design phases define stable interfaces between layers; each component can be built and tested independently before the integration phase wires them together."
confirmed_by_user: false
issue_count: 6
execution_mode: single-pr
---

# Plan Decomposition: DESIGN-env-example-integration

## Strategy: Horizontal

Each design phase becomes one issue. Phases 1-3 are independent additions (config
schema, parser, classifier) that can be worked in any order. Phase 4 (integration)
depends on all three. Phases 5 and 6 extend the integration and can be parallelized
with each other after Phase 4 merges.

## Issue Outlines

### Issue 1: feat(config): add read_env_example opt-out flags to workspace config
- **Type**: standard
- **Complexity**: simple
- **Goal**: Add `ReadEnvExample *bool` to `WorkspaceMeta` and `RepoOverride` in
  `internal/config/workspace.go`, with an `effectiveReadEnvExample` resolver using
  nil-pointer semantics (nil = unset = default true).
- **Section**: Implementation Approach — Phase 1
- **Milestone**: .env.example Integration
- **Dependencies**: None

### Issue 2: feat(workspace): implement parseDotEnvExample for Node-style .env.example syntax
- **Type**: standard
- **Complexity**: testable
- **Goal**: Implement `parseDotEnvExample` in `internal/workspace/env_example.go`
  with full Node-style syntax support (quoted values, `export` prefix, CRLF, escape
  sequences) and per-line tolerance, paired with table-driven tests covering all
  syntax variants.
- **Section**: Implementation Approach — Phase 2
- **Milestone**: .env.example Integration
- **Dependencies**: None

### Issue 3: feat(workspace): implement classifyEnvValue for probable-secret detection
- **Type**: standard
- **Complexity**: testable
- **Goal**: Implement `classifyEnvValue` in `internal/workspace/envclassify.go`
  using Shannon entropy (3.5 bits/char threshold), `envPrefixBlocklist`, and
  `envSafeAllowlist`; paired with table-driven tests covering all entropy
  boundaries, every blocklist prefix, every allowlist pattern, and empty values.
- **Section**: Implementation Approach — Phase 3
- **Milestone**: .env.example Integration
- **Dependencies**: None

### Issue 4: feat(workspace): integrate .env.example pre-pass into EnvMaterializer
- **Type**: standard
- **Complexity**: critical
- **Goal**: Wire config, parser, and classifier into `EnvMaterializer.Materialize`
  as a pre-pass (opt-out → symlink check → size guard → parse → exclude secrets →
  classify undeclared → store), add `EnvExampleVars`/`EnvExampleSources` to
  `MaterializeContext`, add `Stderr io.Writer` to `EnvMaterializer`, and add the
  nil-guarded opening block to `ResolveEnvVars`.
- **Section**: Implementation Approach — Phase 4
- **Milestone**: .env.example Integration
- **Dependencies**: <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

### Issue 5: feat(workspace): add per-repo public-remote guardrail for .env.example secrets
- **Type**: standard
- **Complexity**: testable
- **Goal**: Extend the pre-pass to call `enumerateGitHubRemotes` against `ctx.RepoDir`
  for each undeclared probable-secret key; fail apply when the remote is public and
  `--allow-plaintext-secrets` is not set.
- **Section**: Implementation Approach — Phase 5
- **Milestone**: .env.example Integration
- **Dependencies**: <<ISSUE:4>>

### Issue 6: feat(workspace): add SourceKindEnvExample and verbose source attribution
- **Type**: standard
- **Complexity**: testable
- **Goal**: Add `SourceKindEnvExample` to the `SourceKind` enum; populate
  `EnvExampleSources` entries with it; update `--verbose` output in
  `internal/cli/status.go` to display `.env.example` as the source label.
- **Section**: Implementation Approach — Phase 6
- **Milestone**: .env.example Integration
- **Dependencies**: <<ISSUE:4>>
