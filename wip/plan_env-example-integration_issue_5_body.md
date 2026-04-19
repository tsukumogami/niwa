---
complexity: testable
complexity_rationale: The guardrail logic is a conditional branch within the existing pre-pass (Phase 4). Its inputs and outputs are well-defined: a repo directory, a set of probable-secret keys, and a flag value. Each code path (public remote, private remote, remote detection failure, flag set) maps directly to an isolated test case without requiring a full pipeline.
---

## Goal

Add a per-repo public-remote guardrail to the `.env.example` pre-pass so that probable-secret keys found in public repos are rejected unless `--allow-plaintext-secrets` is set.

## Context

Design: `docs/designs/current/DESIGN-env-example-integration.md`

Phase 4 wires the `.env.example` pre-pass into `EnvMaterializer.Materialize`. This issue adds the final safety layer from PRD R13: after a key is classified as a probable secret, the pre-pass calls `enumerateGitHubRemotes(ctx.RepoDir)` to check whether the managed app repo's remote is public. If it is, and `--allow-plaintext-secrets` is not set, a guardrail error is accumulated alongside the classification error. Errors across all repos are collected and emitted together at the end of apply.

The guardrail targets `ctx.RepoDir` (the managed app repo), not the workspace config dir. This is intentional: `CheckGitHubPublicRemoteSecrets` (used elsewhere) checks the config dir, which is not the relevant remote here.

## Acceptance Criteria

- When `enumerateGitHubRemotes(ctx.RepoDir)` returns an error (e.g., `ctx.RepoDir` is not a git repo, or the remote URL is malformed), the pre-pass emits a warning to `f.stderr()` and skips the guardrail check for that repo. Apply does not fail due to the remote-detection failure alone; other errors (e.g., classification errors) are still reported normally.

- A test for private-remote + high-entropy value asserts: the returned error is non-nil AND the error message contains classification language (e.g., "probable secret") AND the error message does NOT contain guardrail language (e.g., "public remote"). Checking only `err != nil` is not sufficient.

- When the managed app repo's remote is public and an undeclared key's value is classified as a probable secret, apply accumulates a guardrail error and fails at the end of the pre-pass loop.

- When the managed app repo's remote is public and an undeclared key's value is classified as a probable secret, but `--allow-plaintext-secrets` is set, apply succeeds and the key is included in `ctx.EnvExampleVars`.

- No value text, value fragment, or entropy score appears in any diagnostic output (warnings or errors) produced by the guardrail or the classification step. Tests capture stderr and assert it contains no substring of any value that was classified as a probable secret.

## Dependencies

Blocked by <<ISSUE:4>>

## Downstream Dependencies

None — leaf node.
