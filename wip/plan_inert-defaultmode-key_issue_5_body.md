---
complexity: simple
complexity_rationale: Documentation and a test-file comment only; no code or behavior changes, and each correction has a mechanical grep check.
---

## Goal

Correct the seven committed documents that still describe the materialized `permissions.defaultMode` as the route to bypass, so they describe the `--permission-mode` dispatch flag instead.

## Context

Claude Code 2.1.257 stopped honoring `permissions.defaultMode` from a project's `.claude/settings.json`. After the earlier issues in this plan, niwa derives a dispatched worker's posture from the declared `permissions` value it records in instance state, and forwards it on `--permission-mode`. Several committed documents still say the settings file carries the posture, that a root-level bypass applies to every session at the root, or that the break came in 2.1.258. A reader who follows them today gets the wrong mental model and the wrong version.

This issue corrects those documents and the comment block above the permission-mode scenarios in the functional feature file. Archived designs and existing PRDs are historical records and are not edited.

Design: `docs/designs/DESIGN-inert-defaultmode-key.md`
PRD: `docs/prds/PRD-inert-defaultmode-key.md` (requirement R15, acceptance criterion AC18)

## Acceptance Criteria

- [ ] `docs/guides/file-distribution.md` does not contain "maps to Claude Code's", and its trust-prompt section states that `bypass` reaches dispatched workers through `niwa dispatch`.
- [ ] `docs/guides/ephemeral-session-instances.md` does not contain the phrase "applies to **every** session launched at the root", checked with line breaks collapsed to spaces (the current text wraps the phrase across two lines, so a single-line grep misses it).
- [ ] `docs/guides/ephemeral-session-instances.md` contains `--permission-mode`, and states that sessions a developer starts at the root, including ephemeral workers, get the developer's own settings, with `--permission-mode` on the developer's own launch as the route to bypass.
- [ ] `docs/designs/current/DESIGN-workspace-root-claude.md` contains "superseded" inside the "Decision 2: Settings file for non-git instance root" section, next to the "`settings.json` with `bypassPermissions`: works in non-git" experimental finding.
- [ ] `docs/designs/current/DESIGN-mcp-root-instance-distribution.md` contains "2.1.257", recording in or next to "Decision 5: MCP trust prompt" that the decision rested on behavior Claude Code 2.1.257 removed.
- [ ] `docs/designs/current/DESIGN-agent-capability-contract.md` contains `--permission-mode`, noting next to the `permissionsMapping` / `permissions.defaultMode` passage that Claude's posture now travels on the dispatch flag.
- [ ] `docs/designs/current/DESIGN-dispatch-permission-mode.md` does not contain "2.1.258" (all seven current occurrences corrected to 2.1.257).
- [ ] `docs/designs/current/DESIGN-dispatch-permission-mode.md` contains "superseded", recording that its R5 ("reads the already-materialized `.claude/settings.json`, never `workspace.toml` directly") is superseded by R3 of `docs/prds/PRD-inert-defaultmode-key.md`.
- [ ] `test/functional/features/dispatch.feature` does not contain "2.1.258"; the comment block above the permission-mode scenarios names 2.1.257 and describes the derivation as reading the declared posture rather than the materialized settings file.
- [ ] The only change to `test/functional/features/dispatch.feature` is to comment lines; no scenario steps change.
- [ ] No file under `docs/designs/archive/` or `docs/prds/` is modified by this issue.

## Dependencies

Blocked by <<ISSUE:3>>

## Downstream Dependencies

None
