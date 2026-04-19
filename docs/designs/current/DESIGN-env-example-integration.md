---
status: Proposed
upstream: docs/prds/PRD-env-example-integration.md
problem: |
  niwa's ResolveEnvVars function builds the .local.env map by merging config layers
  in last-write-wins order, but has no step for the app repo's .env.example file.
  Adding this lowest-priority layer requires a parser rewrite (the current parseEnvFile
  does not handle quoted values, export prefixes, or CRLF), a secret-classification
  pass for undeclared keys, and integration of the exclusion check against the
  fully-merged config's secrets declarations — all without breaking existing merge
  semantics or the source-fingerprinting system.
decision: |
  Extend ResolveEnvVars in materialize.go with a new first step that reads
  .env.example from the managed app repo's working tree, classifies each value, and
  seeds the vars map before any other layer runs. A rewritten parseDotEnvExample
  function handles Node-style syntax. A buildSecretsKeySet helper builds the
  exclusion set from ctx.Effective before merge. Entropy + prefix classification
  runs only for undeclared keys. Warnings go via a new Stderr field on
  MaterializeContext. Opt-out flags are added to the workspace config schema.
rationale: |
  Inserting .env.example as the opening step of ResolveEnvVars is the lowest-risk
  change: the existing last-write-wins semantics mean every higher layer automatically
  overrides it without any conditional logic. Keeping the exclusion set as a
  pre-computed map avoids repeated config walks per key. Entropy classification is
  cheap enough (O(len(value))) to run inline without caching. Threading stderr
  through MaterializeContext is consistent with how FilesMaterializer already handles
  output and avoids global state.
---

# DESIGN: .env.example Integration

## Status

Proposed

## Context and Problem Statement

`ResolveEnvVars` (`internal/workspace/materialize.go:538`) is the canonical merge
function that produces the `map[string]string` written to each repo's `.local.env`.
It applies layers in order — `[env.files]`, `[env.vars]`, `[env.secrets]`, per-repo
discovered file — using last-write-wins semantics so higher-priority sources
always override lower ones.

The function currently has no step for the managed app repo's `.env.example`. Adding
one requires solving three distinct technical problems:

1. **Parser gap.** `parseEnvFile` (line 727) splits on `\n` and `=` only. It doesn't
   handle single- or double-quoted values, `export KEY=VALUE` prefixes, escape
   sequences in double-quoted strings, or CRLF line endings. Feeding a typical
   Node-ecosystem `.env.example` through it produces incorrect values or drops lines.

2. **Exclusion check.** Keys declared in any `[env.secrets.*]` table must not receive
   a value from `.env.example`, even as the lowest-priority layer (PRD R4a). The check
   requires the _fully-merged_ config — base workspace + overlay + global override —
   because a key may be promoted to a secret by an overlay the user has applied. The
   merged config is already available in `ctx.Effective` at `ResolveEnvVars` call time.

3. **Classification.** For keys not found in the merged config at all (undeclared),
   the `.env.example` value must be classified: probable secret (error) or safe (warn +
   materialize). Classification runs Shannon entropy over the value's characters and
   checks known secret-vendor prefixes. It must not run for keys already declared as
   `[env.vars]` — those are trusted without inspection.

The public-repo guardrail (PRD R13) adds a fourth concern: the remote-visibility check
must run against the managed app repo's remote, not the workspace config repo's remote.
The existing `CheckGitHubPublicRemoteSecrets` helper checks the config dir's remote;
a per-repo call site is needed in the materializer loop.

## Decision Drivers

- `.env.example` is the lowest-priority layer; every existing layer must continue to
  override it without any new conditional logic.
- The exclusion set (secrets-declared keys) must be computed from the fully-merged
  config before any `.env.example` key is written to `vars`.
- `parseEnvFile` is called from three sites (`ResolveEnvVars:563`, `ResolveEnvVars:606`,
  `workspace_context.go:311`); the existing `[env.files]` and per-repo env file paths
  only use basic `KEY=VALUE` syntax, so the parser rewrite must not break them.
- Secret classification must be independently testable without a full pipeline setup.
- Warning and error output must be consistent with `FilesMaterializer`'s existing
  `Stderr io.Writer` pattern; no global state.
- Source attribution must integrate with the existing `SourceEntry` / `SourceKind`
  system so fingerprinting remains correct.
- Opt-out flags (`read_env_example = false`) require schema changes to the workspace
  config struct and workspace.toml parsing.
- Performance budget: 5 ms per repo for discovery + parse (PRD R21).
