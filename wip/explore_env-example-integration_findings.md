# Exploration Findings: env-example-integration

## Round 1 Summary (implementation-shaped)

Three leads: niwa internals, industry prior art, parsing + security.

- **Integration is small.** Repos cloned before env resolution;
  touch points span ~5 files (materialize, apply, guardrail); ~100-200
  LOC.
- **No industry precedent.** No declarative tool reads `.env.example`.
  Greenfield.
- **Secret detection is the hard problem.** Hybrid entropy + safe-prefix
  allowlist is the pragmatic rule.

Research files: `wip/research/explore_env-example-integration_r1_*`.

## Round 2 Summary (product-shaped)

Three leads: user workflows, ecosystem breadth, migration + drift.

### User workflows

The observable contract is **merge semantics + transparency**:

- Workspace intent always wins over app-declared defaults.
- Users see which file supplied each value (status output or a new
  audit subcommand).
- New keys from `.env.example` flow automatically with a visible
  "new from .env.example" signal on apply.
- Per-repo opt-out for trust-boundary cases.
- Loud (not silent) error when `.env.example` ships a value that
  looks like a real secret.

Five user stories draft: onboarding, override-safety, debuggability,
secret-refusal, third-party opt-out.

### Ecosystem breadth

- Node.js dominates the convention. codespar (the trigger) is Node.
- Python/Ruby/Rust/Go/Elixir: convention is weak or absent.
- v1 scope: **Node-style `.env.example`**, parsed with a
  niwa-dedicated function (quoted values, comments, `export`
  prefix). No vendored dependency. Variable expansion, multi-line,
  non-Node variants are deferred.

### Migration + drift

- Four-state matrix (example-present × workspace-vars-present)
  resolved with workspace-wins precedence and a redundancy warning
  on exact matches.
- `niwa status --audit-env` gives maintainers visibility into
  redundant/overridden/new-in-example keys; v1 is read-only.
- Drift strategy: **additive, loud** — new keys in `.env.example`
  flow automatically; apply output lists them.
- Backwards compat: feature ships on by default; existing workspaces
  keep working (State D); consolidation is opportunistic.
- Optional workspace-level `read_env_example = false` opt-out.
- Per-repo opt-out for third-party repos.

## Consolidated picture of the feature (PRD-shaped)

**Capability:** niwa discovers `.env.example` at each managed repo's
root and merges its key-value pairs into `.local.env` as the
lowest-priority defaults layer, below workspace `[env.vars]`, below
per-repo overrides, below vault-resolved secrets.

**Default:** on. Per-repo and workspace-level opt-outs available.

**Scope of syntax (v1):** `KEY=VALUE`, single/double-quoted values,
full-line `#` comments, `export` prefix. Deferred: variable expansion,
multi-line values, non-Node parsers.

**Security rule:** entropy-based stub-vs-secret detection with a
safe-prefix allowlist (`pk_test_`, explicit placeholders). Values
flagged as probable-secret fail apply with a pointer at the
`.env.example` line and a recommendation to declare the key under
`[env.secrets.required]` with a vault ref. An `--allow-plaintext-secrets`-style
escape hatch covers exceptional cases. The public-repo guardrail
extends to these values.

**Drift behavior:** new keys in `.env.example` flow automatically
(additive, loud). Apply output emits `new from .env.example: [...]`.
Values overridden by workspace remain silent unless audited.

**Observability:** `niwa status --audit-env` reports per-repo
`new-in-example`, `redundant`, `overridden`. `niwa status --verbose`
shows per-key source trace.

**Non-goals:**
- Framework-specific `.env.*` variants (Next.js `.env.local`,
  Laravel, etc.)
- Writing to managed app repos
- Changing the `.env.example` format itself
- Issue #61 (static env-files parity) — related but not on this
  feature's critical path
- Issue #62 (vault URIs in recommended/optional) — unrelated

## Convergence decision

Ready to crystallize. The remaining unknowns are design-level
decisions (exact entropy threshold value, exact audit CLI surface),
not research questions. The PRD can scope those as decisions for the
design phase.

**Target artifact: PRD.** The feature's WHAT is now concrete:
capability, default behavior, security rule, drift policy, audit
command, opt-outs, scope, non-goals. A PRD captures these as
requirements and acceptance criteria; a follow-up design doc picks
the implementation structure.
