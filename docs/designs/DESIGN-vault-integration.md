---
status: Proposed
upstream: docs/prds/PRD-vault-integration.md
problem: |
  Wiring a vault layer into niwa forces structural changes across three
  existing subsystems — the TOML schema, the override-merge pipeline, and
  the materialization pipeline — without breaking v0.6 configs or adding
  Go dependencies. The hardest technical problem is ordering: D-6 requires
  `vault://` URIs to resolve inside each source file's provider context
  BEFORE the merge runs, which inverts the current parse → merge →
  materialize flow into parse → resolve-per-file → merge → materialize.
decision: |
  Placeholder — populated by Phase 4.
rationale: |
  Placeholder — populated by Phase 4.
---

# DESIGN: Vault Integration

## Status

Proposed

## Context and Problem Statement

The [vault-integration PRD](../prds/PRD-vault-integration.md) commits niwa
to adding a pluggable vault layer, with per-file-scoped providers, an
override-aware personal-overlay model, and 11 "never leaks" security
invariants. Those requirements cut across three existing subsystems that
were not designed for this shape:

- **`internal/config/` (TOML schema + parser).** Today's schema has
  `[env.vars]` and `[claude.env.vars]` as flat string maps. The PRD splits
  them into sensitivity-coded siblings (`[env.vars]` + `[env.secrets]`)
  each with three requirement sub-tables, adds a top-level `[vault]`
  table in two mutually-exclusive shapes (`[vault.provider]` anonymous
  vs `[vault.providers.<name>]` named), adds `[workspace].vault_scope`,
  and adds a companion `GlobalOverride.Vault` field with tight merge
  semantics (R12: add-only, replace-forbidden). Several new parse-time
  rejections are also required: mixed anon/named, `vault://` URIs in
  forbidden contexts (`[claude.content]`, `[env.files]`, identifier
  fields), and cross-config provider name references.

- **`internal/workspace/override.go` (merge pipeline).** D-6 keeps the
  existing `MergeOverrides` / `MergeGlobalOverride` infrastructure but
  demands a new ordering: `vault://` URIs must resolve to `secret.Value`
  inside each source file's provider context BEFORE the merge runs.
  This is a structural inversion of today's parse → merge → materialize
  flow. Resolving post-merge would flatten file-of-origin and make
  `vault://<name>/key` ambiguous when layers declare identically-named
  providers (D-9 file-local scoping requires the opposite).

- **`internal/workspace/materialize.go` and `state.go` (materialization
  pipeline).** The PRD adds `secret.Value` as an opaque type that must
  survive every formatter, JSON encoder, error wrapper, and log path.
  It fixes a pre-existing `0o644` bug that currently materializes env
  and settings files world-readable. It adds `.local` infix + instance-
  root `.gitignore` invariants, and introduces `ManagedFile.SourceFingerprint`
  as a SHA-256 reduction of `(source-id, version-token)` tuples so
  `niwa status` can distinguish user-edited drift from upstream
  rotation. For mixed-source materialized files (the common `.local.env`
  case combining workspace env files, discovered repo env files,
  `env.vars` entries, and overlay-merged values), the fingerprint must
  reduce consistently across sources.

Three subsystems, coupled by a single cross-cutting concern. The design
must commit to concrete interfaces, an explicit ordering of the new
resolve-stage relative to the existing merge pipeline, a version-token
scheme that works for both Infisical (API-native versioning) and the
v1.1 sops backend (synthesized from blob hash + commit SHA), and
`secret.Value` machinery strong enough to survive the full error-wrap
chain (including provider-CLI stderr capture — R22's acceptance test
induces an auth error with a known-secret fragment in stderr).

The `vault://` URI resolution is happening in a system that already has
sophisticated override semantics. This design must honor v0.6's
`MergeGlobalOverride` behavior (personal-wins for `Env.Vars`), preserve
the `ClaudeConfig` / `ClaudeOverride` type split shipped in v0.6, and
add new constraints (R12 provider add-only, R31 shadow visibility)
without breaking v0.6 configs that don't use vaults at all.

## Decision Drivers

### From the PRD

- **Resolve-before-merge ordering (D-6, M-1 architect-review fix).** The
  design must specify exactly where in the pipeline `vault://` URIs become
  `secret.Value`. Post-merge resolution cannot honor D-9 file-local
  scoping; pre-merge resolution inverts the existing flow.
- **Pluggable provider interface from v1 (R1, D-1).** The Go interface
  must support Infisical (v1) and sops+age (v1.1) without rework. It
  must admit backends that cache authenticated sessions (Infisical)
  and backends that are stateless per call (sops).
- **Zero new Go library dependencies (R20).** All providers invoke
  subprocesses. This forces design decisions about subprocess lifecycle,
  stderr scrubbing, and version-token parsing from CLI output.
- **Opaque `secret.Value` type (R22).** Redaction must survive formatters,
  JSON encoding, `gob`, `fmt.Errorf("...: %w", err)` chains, and captured
  provider-CLI stderr. The type is load-bearing for the security
  invariants.
- **Eleven security invariants (R21–R31).** Each is a concrete constraint
  on implementation shape: no argv, no config writeback, `0o600`, `.local`
  infix, no CLAUDE.md interpolation, no status content, no `os.Setenv`,
  no disk cache, public-repo guardrail one-shot override, override-
  visibility diagnostics.
- **File-local provider scoping (R3, D-9).** The parser must track which
  file declared which providers; per-file resolution must use the
  right provider table; merging cannot mix provider names across layers.
- **Personal-overlay add-only semantics for providers (R12).** Personal
  overlay may add new provider names but cannot replace team-declared
  names. A collision is a hard apply error with remediation pointing
  to per-key overrides.
- **`SourceFingerprint` provenance (R15).** Version-tokens must carry
  enough metadata that `niwa status` can point a user to "what change
  caused this rotation" — commit SHA for sops/git-native, native
  version ID + audit-log pointer for API-hosted.
- **Public-repo guardrail with remote enumeration (R14, R30).** The
  guardrail must enumerate all git remotes (not just `origin`), detect
  GitHub public patterns, and block apply unless `--allow-plaintext-
  secrets` is supplied (strictly one-shot, no state persistence).
- **Infisical as the v1 backend (R1, D-1).** The design commits to
  Infisical's subprocess interface, auth model, and version-token
  shape. sops is v1.1 — the interface must not over-fit Infisical.

### Implementation-specific

- **Backwards compatibility with v0.6 configs.** Workspaces that don't
  use vaults must keep working unchanged. The `[env.vars]`/`[env.secrets]`
  split must accept v0.6's flat `[env.vars]` shape without migration.
- **Hook into the existing materializer interface.** `EnvMaterializer`,
  `SettingsMaterializer`, and `FilesMaterializer` already implement a
  common materializer contract. The design should preserve that
  boundary and not introduce a parallel "vault materializer."
- **Minimal churn in `MergeGlobalOverride`.** Today's merge is a pure
  function over `*WorkspaceConfig` values. Inserting a resolve-stage
  before it must not bloat `MergeGlobalOverride`'s signature or turn
  every test that merges configs into a test that needs a provider
  mock.
- **Testability of the security invariants.** Each of R21–R31 needs a
  verification path — unit test, functional test, or CI lint. The
  design must pick where each invariant lives (compile-time check vs
  runtime assertion vs acceptance test) to make them enforceable, not
  aspirational.
- **Performance sensitivity for the common case.** Typical workspaces
  hit ≤ ~20 vault references per apply. The subprocess-per-resolve
  model can be slow if each call cold-starts the provider CLI.
  Batching, provider-held session caches, and parallel resolution are
  all design choices that affect end-user latency.
- **Diagnostic ergonomics across the eleven invariants.** R31's
  override-visibility is not just a stderr line — it must integrate
  with `niwa status` and `niwa status --audit-secrets`. R15's
  fingerprint provenance must render via git SHA (for sops) and
  audit-log pointer (for Infisical), which are different UIs.
- **Config-package independence from git (S-1 architect concern).** The
  public-repo guardrail needs remote-URL classification. This must
  not pull git-awareness into `internal/config/`. The guardrail
  belongs in `internal/workspace/apply.go` or a dedicated package.

## Considered Options

Populated by Phase 3 (cross-validation of decision-skill outputs from
Phase 2).

## Decision Outcome

Populated by Phase 4.

## Solution Architecture

Populated by Phase 4.

## Implementation Approach

Populated by Phase 4.

## Security Considerations

Populated by Phase 5.

## Consequences

Populated by Phase 6.
