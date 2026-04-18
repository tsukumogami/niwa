# Research: niwa env-loading internals

## Static env file path & parsing

- Read location: `internal/workspace/materialize.go:557-579` in `ResolveEnvVars()`.
- Parser: `parseEnvFile()` at `materialize.go:727-746`. Line-based, skips blanks and `#` comments, `KEY=VALUE` split, no quote handling, no variable expansion, no `export` support.
- Merge order: file-based vars are added first (557-579), then inline `[env.vars]` / `[env.secrets]` entries overlay on top (593-602).
- **Inline wins on collision** because inline runs after file parsing.

## Per-repo env merge precedence

- `internal/workspace/override.go:MergeOverrides()` lines 112-131.
- Workspace-level `[env.vars]` is base; repo-level `[repos.<n>.env.vars]` wins per key (116-122). Same for secrets (124-131).
- Files: repo files are appended to workspace files (line 113); both are read, vars win per key on collision.

## Clone timing vs env resolution

- `runPipeline()` in `internal/workspace/apply.go`:
  - Step 3 (line 687): repos are cloned.
  - Step 6.5 (line 820): materializers run, including env resolution.
- **Repos are cloned before env resolution.** A `.env.example` reader in the materializer could safely read files from inside a managed repo.

## Guardrail coverage

- `internal/guardrail/githubpublic.go:offendingKeys()` lines 152-191.
- Walks `cfg.Env.Secrets`, `cfg.Claude.Env.Secrets`, per-repo overrides, instance overrides.
- Intentionally does NOT walk `[env].files` content (per issue #61). Config file plaintext only.
- For `.env.example` integration: a new guardrail walk would be needed over repo-local files. Current `offendingKeys()` processes in-memory `MaybeSecret` values from TOML only.

## `secret.Value` wrapping

- `internal/config/maybesecret.go` lines 20-71 define the resolution contract.
- All keys under `[env.secrets]` and `[claude.env.secrets]` are auto-wrapped to `secret.Value` by the resolver regardless of source (plaintext or `vault://`).
- Keys under `[env.vars]` remain plain strings unless containing a `vault://` reference.
- For `.env.example` integration: desirable to wrap all resolved values post-detection to uphold R22 redaction, even when the source file is plaintext.

## Touch points for a `.env.example` integration

| Component | File | Function / Lines | Change |
|-----------|------|------------------|--------|
| Env discovery | `materialize.go` | `DiscoverEnvFiles()` | Scan each cloned repo root for `.env.example` |
| Discovered state | `materialize.go` | `DiscoveredEnv` struct, ~line 60 | Add `RepoExampleEnvFiles map[string]string` |
| Env materialization | `materialize.go` | `ResolveEnvVars()` around line 604 | Insert `.env.example` merge *after* repo-file vars, *before* inline `[env.vars]` |
| Pipeline wiring | `apply.go` | `runPipeline()` lines 821-836 | Pass discovered example files into the materializer context |
| Guardrail | `guardrail/githubpublic.go` | `CheckGitHubPublicRemoteSecrets()` | Add walk of discovered `.env.example` files |
| Per-repo override | `override.go` | `MergeOverrides()` line 31 | No change — existing per-repo precedence already wins |

**Sizing.** The integration lands in a handful of functions — 5-6 files, roughly 100-200 lines including tests. Critical dependency: repos are cloned before env resolution, so the read-after-clone ordering is already correct.
