# Phase 2 Synthesis: env-example-integration

Phase 2 research was conducted during the preceding `/explore` workflow
and handed off. This file synthesizes those findings into the Phase 2
structure so Phase 3 (Draft) can proceed directly.

## Research carried forward

Round 1 (implementation-shaped):
- `wip/research/explore_env-example-integration_r1_lead-niwa-internals.md`
  — niwa's current env-loading pipeline, clone-vs-resolve ordering,
  guardrail coverage, `secret.Value` wrapping, touch points for the
  integration. Fills the **Codebase analyst** role.
- `wip/research/explore_env-example-integration_r1_lead-industry-prior-art.md`
  — survey of Docker Compose, Vercel, Railway, Fly.io, Heroku,
  Turborepo, Nx, direnv. Fills the **Architecture perspective** role.
- `wip/research/explore_env-example-integration_r1_lead-parsing-security.md`
  — dotenv syntax features, Go library options, entropy-based secret
  detection, public-repo guardrail implications. Fills the **Ops
  perspective** role.

Round 2 (product-shaped):
- `wip/research/explore_env-example-integration_r2_lead-user-workflows.md`
  — onboarding scenarios today vs. imagined future, debuggability,
  five user stories. Fills the **User researcher** role.
- `wip/research/explore_env-example-integration_r2_lead-ecosystem-breadth.md`
  — Node-vs-Python-vs-Ruby-vs-etc. ecosystem survey, v1 scope verdict.
  Additional **User researcher** coverage.
- `wip/research/explore_env-example-integration_r2_lead-migration-drift.md`
  — four-state matrix, drift policy candidates, migration tooling,
  backwards compat analysis, four migration-centric user stories.
  Fills the **Maintainer perspective** role.

## Key themes across all research

1. **Integration is small-code, hard-policy.** The code change spans
   ~5 files and ~100-200 LOC. The hard problems are policy: how to
   separate stubs from secrets, how loud to be about drift, what the
   audit surface looks like. The PRD's job is to lock these in.

2. **Workspace intent must always win.** All research paths
   converge: workspace.toml overrides are deliberate; `.env.example`
   is defaults; on collision, workspace wins. No ambiguity to resolve.

3. **Security rule is the largest risk surface.** False positives
   annoy developers (the codespar `pk_test_Y2FyaW5...` example is a
   legitimate Clerk publishable key that looks like a secret to a
   generic parser). False negatives leak secrets. Hybrid
   entropy-plus-allowlist is the pragmatic rule; the PRD must pin
   down the exact threshold and the allowlist's initial entries.

4. **Node-centric v1 is right.** 100% of niwa's observable userbase
   ships Node-style `.env.example`. Python/Ruby/Rust/Elixir/Go can
   come later without rearchitecting; just extend the parser.

5. **Additive-and-loud beats gatekeeping.** The whole point of the
   feature is to stop silently missing app-declared defaults.
   Requiring a workspace.toml acknowledgement for each new key
   reintroduces the problem we're fixing.

## Decisions already made during exploration

From `wip/prd_env-example-integration_scope.md` "Decisions from
Exploration" section:

- v1 parser is Node-style only.
- Workspace intent wins over app defaults.
- Additive, loud drift policy.
- Per-repo and workspace-level opt-out exist; default is on.
- Public-repo guardrail extends to `.env.example` content.
- `niwa status --audit-env` is v1 scope; `--apply-diff` deferred.
- niwa does not write to managed app repos.
- No vendored dotenv library.

These should appear in the PRD's Decisions and Trade-offs section.

## Gaps for the PRD draft to resolve

- Exact Shannon entropy threshold (research suggested ~3.0 bits/char
  from truffleHog/gitleaks; PRD should pick a value).
- Initial safe-prefix allowlist entries (`pk_test_`, empty values,
  common placeholders like `changeme`, `<your-…>`; PRD should
  enumerate the v1 set).
- `--audit-env` exact output format, exit codes, `--format json`
  support.
- Failure-mode error text for probable-secret values.
- Interaction with `[env.secrets.required]` declarations when the
  same key is also in `.env.example`.
- Windows CRLF/BOM handling (niwa is macOS+Linux only, so the PRD
  can defer Windows-specific concerns, but the parser should at
  least tolerate CRLF line endings as they're common in
  Node-ecosystem tools).

Phase 3 should lock these in as numbered requirements with
testable acceptance criteria.

## Proceed directly to Phase 3

No new research leads emerged. Coverage is sufficient. The PRD draft
should turn the above material into formal requirements.
