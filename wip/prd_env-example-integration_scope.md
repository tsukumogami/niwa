# /prd Scope: env-example-integration

## Problem Statement

Managed app repos commonly ship `.env.example` files declaring the
env vars they need, with sensible defaults for non-sensitive fields
and placeholder stubs for secrets. niwa currently requires these
values to be duplicated into `[repos.<name>.env.vars]` in the
workspace config repo, creating drift risk: when the app repo adds
a new var or changes a default, the workspace config does not
notice, and teammates can end up with silently broken `.local.env`
files. niwa should consume the app repo's `.env.example` directly
so it becomes the source of truth for defaults, with workspace
overrides explicitly winning on collision.

## Initial Scope

### In Scope

- Auto-discovery of a `.env.example` file at the root of each
  cloned managed repo; automatic merge into that repo's
  `.local.env`.
- Merge precedence: `.env.example` is the lowest-priority defaults
  layer, below workspace `[env.vars]`, below
  `[repos.<name>.env.vars]` per-repo overrides, below vault-resolved
  `[env.secrets]`.
- Node-style `.env.example` syntax for v1: `KEY=VALUE` lines,
  single- and double-quoted values (with basic escape handling on
  double-quoted), full-line `#` comments, `export` prefix.
- A secret-detection rule: values whose shape suggests a real
  secret (high Shannon entropy, known secret-vendor prefixes)
  fail apply unless an escape hatch is invoked. A safe-prefix
  allowlist permits intentional test values (e.g., `pk_test_`)
  and explicit placeholders (empty values, `changeme`,
  `<your-api-key>`).
- Public-repo guardrail extension: when the managed repo is
  public, the existing plaintext-secrets guardrail must cover
  values read from its `.env.example` too.
- Drift behavior: new keys from `.env.example` flow into
  `.local.env` automatically; `niwa apply` output lists them
  under a "new from .env.example" banner (additive, loud).
- Observability: `niwa status --audit-env` reports per-repo
  `new-in-example`, `redundant`, and `overridden` keys.
  `niwa status --verbose` shows per-key source trace.
- Opt-outs: workspace-level `[config] read_env_example = false`
  and per-repo `[repos.<n>] read_env_example = false`. Default is
  on.
- Backwards compat: existing workspaces that duplicate vars in
  workspace.toml keep working; workspace values continue to win
  on collision; consolidation is opportunistic via the audit
  command.

### Out of Scope

- Framework-specific `.env.*` variants (`.env.local`,
  `.env.development`, Laravel `.env`, Next.js loader layering).
- Writing back into managed app repos. niwa stays read-only on
  app repos.
- Changing the `.env.example` format itself; it's a community
  convention.
- Python / Ruby / Rust / Elixir / Go `.env` parsers or
  convention-specific support beyond the Node-style syntax
  above.
- Variable expansion (`${FOO}`, `${FOO:-default}`) and multi-line
  quoted values — deferred until a concrete user need appears.
- Issue #61 (static env-files parity): related but separate
  scope. This feature may eventually subsume
  `[env].files`, but v1 ships both paths side-by-side.
- Issue #62 (vault URIs in `recommended/optional` sub-tables):
  unrelated.

## Research Leads

Carried forward from exploration; the /prd process may pull
deeper on user-specific ones.

1. **User personas and workflows.** Concrete walkthroughs of: a
   new team member onboarding to a codespar repo; an app team
   member adding a new env var to `.env.example`; a workspace
   maintainer consolidating duplicated vars; a security-conscious
   reviewer checking the feature's failure modes. Exploration
   round 2 captured 5 user stories — the PRD should expand or
   pare these into formal acceptance criteria.

2. **Acceptance-criteria precision.** Each of the scope items
   above needs testable criteria: what does "additive, loud" look
   like exactly? What's the entropy threshold and allowlist? What
   does the `--audit-env` output schema look like? These are
   product decisions the PRD locks in; the follow-up design
   document turns them into implementation.

3. **Risk surface and mitigations.** The secret-detection rule is
   a risk surface — false positives annoy developers, false
   negatives leak secrets into materialized files. The PRD
   should document the threat model (what niwa defends against,
   what it explicitly doesn't) in a way that makes the
   trade-offs legible.

## Coverage Notes

Things the exploration did NOT fully resolve that the PRD should
address:

- **Exact secret-detection rule specification.** Exploration
  settled on hybrid entropy + allowlist; the PRD needs to commit
  to specific values (entropy threshold, allowlist entries,
  escape-hatch mechanics).
- **`--audit-env` CLI surface.** Exploration sketched the output
  structure; the PRD should define exit codes, `--format json`
  support, and how the command integrates with CI use cases.
- **Failure-mode UX.** When apply fails due to a probable-secret
  value, what exact error text does niwa emit? What's the
  remediation guidance? The PRD should lock in the UX contract.
- **Interaction with existing `[env.secrets.required]`
  declarations.** If the app repo's `.env.example` and the
  workspace's `[env.secrets.required]` list the same key, what's
  the precedence and what's the signalling?
- **Windows / CRLF / BOM handling.** niwa today is macOS+Linux
  only (per the vault guide). The PRD should explicitly defer
  Windows-specific quirks or affirm that the parser tolerates
  them anyway.

## Decisions from Exploration

These are settled by the two rounds of research and should not be
re-opened during PRD work:

- **v1 parser is Node-style only.** Python/Ruby/Rust/Elixir/Go
  support is deferred.
- **Workspace intent wins over app defaults.**
  `[repos.<n>.env.vars]` > `.env.example` on collision.
- **Additive, loud drift policy.** New keys flow automatically;
  apply output surfaces them.
- **Per-repo and workspace-level opt-out exist.** Default is on.
- **Public-repo guardrail extends to `.env.example` content**
  when the managed repo is public.
- **`niwa status --audit-env` is v1 scope.** A `--apply-diff`
  auto-rewrite of workspace.toml is deferred to v2.
- **niwa does not write to managed app repos.** All output lands
  in `.local.env` files as today.
- **No vendored dotenv library.** niwa extends its existing
  `parseEnvFile` with ~50 LOC for quoted values, comments, and
  `export`.
