# Completeness Review

## Verdict: FAIL
The PRD is substantially complete but has 5 critical gaps and several missing acceptance criteria that would leave implementers unable to fully verify requirements.

## Issues Found

1. **Unresolved Q1 (niwa status display)**: Q1 is flagged as "Decision needed before acceptance" but left open. Without resolving it, implementers cannot determine the full scope of visibility/discovery behavior for `niwa status`.
   - Fix: Resolve Q1 explicitly before shipping. Either add an AC for `niwa status` output or explicitly state it will not display companion information.

2. **Missing AC for R2 (companion local clone deletion)**: R2 requires `niwa config unset private` to delete the local clone of the companion, but the AC on line 117 only checks removal from config.toml, not deletion of `$XDG_CONFIG_HOME/niwa/private/`.
   - Fix: Add AC: "`niwa config unset private` results in `$XDG_CONFIG_HOME/niwa/private/` being deleted from the filesystem."

3. **R5 sync semantics ambiguous about first-vs-subsequent distinction**: R5 says "niwa syncs the companion" but doesn't clarify how this interacts with R6-R8's conditional behavior. The ACs separate first-time and subsequent cases, but the requirements don't establish this distinction clearly.
   - Fix: Rewrite R5 to explicitly reference that first-time clone behavior is governed by R6-R8.

4. **Missing AC for env merge semantics (R11)**: R11 specifies `[env]` merge semantics (files append, env vars per-key) but there are no ACs testing `[env]` merging. All content-related ACs test `[claude.content.*]` only.
   - Fix: Add ACs testing env var and env.files merge from companion into the apply result.

5. **Incomplete security AC for path traversal (R21)**: The AC tests absolute paths and `..` in isolation, but doesn't test a relative path that traverses parent dirs (e.g., `../../.ssh/authorized_keys`). The text of the AC uses this exact example but the format splits it into two cases that could be read as testing only separate conditions.
   - Fix: Confirm the AC covers any path containing `..` components, not just those starting with `../../`.

## Suggested Improvements

1. **Add AC for hook script resolution (R22)**: No AC verifies that companion-declared hooks are resolved to absolute paths within the companion directory before merging.

2. **Add AC for content collision precedence (R16)**: R16 specifies public config wins on collision, but no AC tests the collision case — only the non-collision case is tested.

3. **Add AC for malformed workspace-extension.toml**: No AC specifies behavior when the companion is accessible but `workspace-extension.toml` is malformed. Should this abort or skip gracefully?

4. **Add AC for instance-level skip_private edge cases**: No AC tests behavior when `niwa config set private` is called after a workspace was initialized with `--skip-private`.

## Summary

The PRD establishes a strong foundation with clear goals, well-structured requirements, and most acceptance criteria. Five critical gaps prevent full implementation without guesswork: missing AC for local clone deletion on unset, ambiguous first-vs-subsequent sync semantics in R5, no ACs for `[env]` merge, incomplete path-traversal security AC, and Q1 left unresolved. Fixing these gaps is straightforward and does not require significant rework of the overall structure.
