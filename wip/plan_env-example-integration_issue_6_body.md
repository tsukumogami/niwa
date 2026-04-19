---
complexity: testable
complexity_rationale: The deliverables are a new constant with a specific string value and a display label change in --verbose output. Both can be verified with a unit test asserting the constant's value and an integration test asserting the exact label shown for keys sourced from .env.example.
---

## Goal

Add `SourceKindEnvExample` to the `SourceKind` enumeration and update `niwa status --verbose` to display `.env.example` as the source label for keys originating from that file.

## Context

Design: `docs/designs/current/DESIGN-env-example-integration.md`

Phase 6 of the design adds source attribution for `.env.example` keys. The existing `SourceKind` enumeration in `internal/workspace/state.go` defines two constants — `SourceKindPlaintext = "plaintext"` and `SourceKindVault = "vault"`. A new constant `SourceKindEnvExample` is needed so that `EnvExampleSources` entries carry a distinct provenance category. Without a unique constant value, attribution for other source kinds risks silent corruption if `SourceKindEnvExample` is assigned the same string as an existing constant.

The `--verbose` output path in `internal/cli/status.go` currently branches on `SourceKindPlaintext` (line 223). It must be updated to recognise `SourceKindEnvExample` and display `.env.example` as the source label — not a vault label, not a plaintext label.

## Acceptance Criteria

- `SourceKindEnvExample` is defined in `internal/workspace/state.go` (or `materialize.go`) with the string value `"env_example"`, which is distinct from `"plaintext"` and `"vault"`.
- A unit test asserts that `SourceKindEnvExample == "env_example"` and that this value does not equal `SourceKindPlaintext` or `SourceKindVault`.
- `EnvExampleSources` entries produced by the `EnvMaterializer` pre-pass use `Kind: SourceKindEnvExample`.
- `niwa status --verbose` displays `.env.example` (literally, not a fallback or existing plaintext/vault label) as the source for keys whose `SourceEntry.Kind` is `SourceKindEnvExample`.
- A test confirms that after `niwa apply` against a workspace containing a managed repo with a `.env.example` file, `niwa status --verbose` shows `.env.example` as the source label for keys that originated from that file — not `plaintext`, `vault`, or any other label.
- Existing source kinds are unaffected: keys from `[env.files]`, `[env.vars]`, and `[env.secrets]` continue to display their prior labels under `--verbose`, and no existing test regressions are introduced.

## Dependencies

Blocked by <<ISSUE:4>>

## Downstream Dependencies

None — leaf node.
