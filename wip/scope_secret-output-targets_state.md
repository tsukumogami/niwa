---
topic: secret-output-targets
chain_started: 2026-06-13T03:06:41Z
last_updated: 2026-06-13T03:06:41Z
phase_pointer: phase-2
exit: UNSET
exit_artifacts: []
planned_chain:
  - brief
  - prd
  - design
  - plan
chain_skipped: []
chain_ran:
  - brief
visibility: Public
child_snapshots:
  brief:
    status: Draft
    content_hash: 2a791bd6ecd6f26ad41943110ca81b1d90694ce0
    captured_at: 2026-06-13T03:23:35Z
parent_orchestration:
  invoking_child: prd
  suppress_status_aware_prompt: true
  rationale: fresh-chain
---

# Scope state: secret-output-targets

Tactical chain orchestration state. Seed context (brainstormed, design
converged before chain start):

- Configurable per-repo secret-output target path(s) + pluggable output format.
- Replaces hardcoded `.local.env` (internal/workspace/materialize.go:774).
- Config cascade repo -> workspace -> global-default, default `.local.env`
  (no behavior change by default). Mirrors EnvExamplePolicy/ReadEnvExample
  (`*pointer` idiom, `EffectiveX` resolver) from PR #155.
- Target shape: list with bare-string shorthand; entry is string or
  `{ path, format }`.
- Formats: dotenv (default), json, shell. Defer yaml. One-method writer
  interface; per-format value escaping only.
- Format resolution: inferred from extension (.env/.local.env/.env.local ->
  dotenv, .json -> json, .sh -> shell, unknown -> dotenv) with explicit
  `format =` override.
- Safety: build on internal/gitexclude (PR #158). Custom names bypass
  injectLocalInfix and are added to the managed .git/info/exclude block via a
  new `extraPatterns ...string` param on EnsureRepoExclude. Thread resolved
  targets through the apply per-repo loop (apply.go ~1300-1324) and the
  worktree-content path.
- Out of scope (YAGNI): yaml, template rendering, per-variable target routing,
  changing which vars get resolved.
