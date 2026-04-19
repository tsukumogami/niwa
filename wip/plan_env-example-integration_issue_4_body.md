---
complexity: critical
complexity_rationale: >
  This phase wires together three independently-built subsystems (config schema,
  parser, classifier) into a new pre-pass inside EnvMaterializer.Materialize.
  It touches the two most central files in the workspace pipeline (materialize.go,
  apply.go), adds two new MaterializeContext fields that every downstream caller
  implicitly depends on, modifies ResolveEnvVars — called from two distinct paths
  with different semantics — via a nil-guarded opening block, and must satisfy
  security requirements (no value text in diagnostics, correct secrets exclusion
  scope) that are not caught by ordinary compilation.
---

## Goal

Wire the opt-out check, Node-style parser, secrets exclusion set, and value classifier into a pre-pass inside `EnvMaterializer.Materialize`, store results on `MaterializeContext`, and seed `ResolveEnvVars` from those fields so `.env.example` becomes the lowest-priority env layer.

## Context

Design: `docs/designs/current/DESIGN-env-example-integration.md`

This is Phase 4 of the implementation. Phases 1–3 deliver the config schema (`ReadEnvExample *bool`), `parseDotEnvExample`, and `classifyEnvValue`. Phase 4 connects them: the pre-pass runs before `ResolveEnvVars`, stores `EnvExampleVars` and `EnvExampleSources` on `MaterializeContext`, and a nil-guarded opening block in `ResolveEnvVars` seeds `vars` from those fields. The settings path (which also calls `ResolveEnvVars`) sees nil fields and is unchanged.

Warning and error output goes through a new `Stderr io.Writer` field on `EnvMaterializer`, following the `FilesMaterializer` precedent. When nil, the field defaults to `os.Stderr` via a `stderr()` helper.

## Acceptance Criteria

### EnvMaterializer.Stderr field and routing

- `EnvMaterializer` has a `Stderr io.Writer` field and a private `stderr() io.Writer` helper that returns `Stderr` when non-nil, `os.Stderr` otherwise.
- Tests inject a `*bytes.Buffer` as `EnvMaterializer.Stderr` and assert that symlink warnings, per-line parse warnings, whole-file skip warnings, undeclared-key warnings, and classification warnings all appear in that buffer — not on `os.Stderr`. No warning produced by the pre-pass may bypass the injected writer.

### Pre-pass in EnvMaterializer.Materialize — full 9-step data flow

**Step 1 — Opt-out check:**
- When the workspace-level `read_env_example` is `false`, the pre-pass is skipped for all repos; `ctx.EnvExampleVars` remains nil.
- When the per-repo `read_env_example` is `false` (checked via `ctx.Effective.Repos[ctx.RepoName]`), the pre-pass is skipped for that repo only.
- When both are unset (nil), the pre-pass runs (default-true behavior).

**Step 2 — Symlink and existence check:**
- `os.Lstat` is called on `filepath.Join(ctx.RepoDir, ".env.example")`.
- If the path is a symlink (`ModeSymlink`), a warning is emitted to `f.stderr()` and the repo is skipped; `ctx.EnvExampleVars` is not set.
- If the path does not exist (`os.IsNotExist`), the pre-pass short-circuits silently; `ctx.EnvExampleVars` is not set.
- Any other `Lstat` error emits a warning and skips the repo.

**Step 3 — File size guard:**
- If `stat(path).Size() > 512*1024`, a warning is emitted to `f.stderr()` and the repo is skipped.

**Step 4 — parseDotEnvExample:**
- `parseDotEnvExample` is called; per-line warnings are written to `f.stderr()` (format: `file:line:problem`; no value text).
- A whole-file error emits a single warning to `f.stderr()` and skips the remaining steps for that repo.

**Step 5 — Secrets exclusion set:**
- The exclusion set is built by walking only the current repo's config layer: `ctx.Effective.Env.Secrets.{Values, Required, Recommended, Optional}`, `ctx.Effective.Claude.Env.Secrets.{Values, Required, Recommended, Optional}`, and `ctx.Effective.Repos[ctx.RepoName].Env.Secrets.{Values, Required, Recommended, Optional}`.
- The walk does NOT iterate over all entries in `ctx.Effective.Repos`; it reads only `ctx.Effective.Repos[ctx.RepoName]`.
- Keys present in any of these sub-tables are removed from the parsed map silently (no key name or value is emitted).
- A key declared as a secret with no resolved value is still excluded; a `.required` stub in `.env.example` cannot silently satisfy a required secret.

**Step 6 — Per-key classification:**
- Keys present in `ctx.Effective.Env.Vars.Values` are treated as declared vars: included without classification.
- Undeclared keys are passed to `classifyEnvValue`:
  - `isSafe=true`: a warning "undeclared key \<key\>, treating as var" is emitted to `f.stderr()`; the key is included.
  - `isSafe=false`: an error entry is accumulated (file, line, key, reason — no value text).

**Step 7 — Probable-secret error gate:**
- If any probable-secret errors were accumulated, all of them are emitted to `f.stderr()` and the pre-pass returns an error. The function does not set `ctx.EnvExampleVars`.

**Step 8 — Store results:**
- `ctx.EnvExampleVars` is set to the filtered map of safe keys.
- `ctx.EnvExampleSources` is set to a `[]SourceEntry` with `Kind: SourceKindEnvExample` and correct path attribution.

**Step 9 — ResolveEnvVars nil-guard:**
- `ResolveEnvVars` has a nil-guarded opening block: if `ctx.EnvExampleVars != nil`, it seeds `vars` from that map and appends `ctx.EnvExampleSources` before the existing merge layers run.
- When `ctx.EnvExampleVars` is nil (settings path, or opt-out), the function behaves exactly as before; no behavior change at any existing call site.
- The signature of `ResolveEnvVars` is unchanged.

### Security constraints

- No warning or error message produced by the pre-pass includes any value text, value fragment, or raw entropy score. Tests capture `f.stderr()` output and assert it contains no substring of any value classified as a probable secret.

### Integration test scenarios

- **Absent file:** repo has no `.env.example`; `ctx.EnvExampleVars` is nil; `ResolveEnvVars` output is identical to pre-feature output.
- **Symlink:** `.env.example` is a symlink; pre-pass emits a warning to the injected writer and skips; `ctx.EnvExampleVars` is nil.
- **Secrets exclusion (workspace layer):** a key present in `ctx.Effective.Env.Secrets.Values` is present in `.env.example`; it does not appear in `ctx.EnvExampleVars`.
- **Secrets exclusion (Claude env layer):** a key present in `ctx.Effective.Claude.Env.Secrets.Values` is present in `.env.example`; it does not appear in `ctx.EnvExampleVars`.
- **Secrets exclusion (per-repo layer):** a key present in `ctx.Effective.Repos[ctx.RepoName].Env.Secrets.Values` is present in `.env.example`; it does not appear in `ctx.EnvExampleVars`.
- **Declared var:** a key present in `ctx.Effective.Env.Vars.Values` is present in `.env.example`; it appears in `ctx.EnvExampleVars` without triggering classification.
- **Undeclared safe value:** an undeclared key with a low-entropy placeholder value (`empty`, `changeme`) appears in `ctx.EnvExampleVars`; a warning naming the key (not the value) is written to the injected writer.
- **Undeclared probable secret:** an undeclared key with a high-entropy value causes the pre-pass to return an error; `ctx.EnvExampleVars` is not set; the accumulated error message names the key and reason but contains no value text.

### Deliverables for downstream issues

- **Issue 5** (probable-secret error accumulation extension): the accumulation pattern — collect all errors across all repos, emit all, return error — is in place and exercised by at least one integration test. Issue 5 extends this pattern; no structural change to the error collection loop is needed.
- **Issue 6** (source attribution in `niwa status --verbose`): the `SourceKindEnvExample` constant is defined and used in `ctx.EnvExampleSources` entries. Issue 6 reads this field; the constant name must be stable.

### Files modified

- `internal/workspace/materialize.go` — `EnvMaterializer` (Stderr field, stderr() helper, pre-pass), `MaterializeContext` (EnvExampleVars, EnvExampleSources fields), `ResolveEnvVars` (nil-guard opening block), `SourceKindEnvExample` constant.
- `internal/workspace/apply.go` — materializer construction updated so `EnvMaterializer.Stderr` is populated correctly (injected or defaulting to os.Stderr via the helper).

## Dependencies

Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>

## Downstream Dependencies

- **Issue 5** extends the pre-pass to add the per-repo public-remote guardrail for probable-secret keys. It depends on the error accumulation loop introduced here.
- **Issue 6** adds `SourceKindEnvExample` display to `niwa status --verbose` and reads `ctx.EnvExampleSources`. It depends on the constant and field being defined and populated by this issue.
