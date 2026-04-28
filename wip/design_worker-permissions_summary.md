# Design summary: worker-permissions

## Input context (Phase 0)

**Source:** GitHub issue #86 (freeform topic)
**Problem:** Worker sessions spawn with `--permission-mode=acceptEdits`, which only
auto-approves file edits. Shell tools (gh, git, go test) require interactive approval
that never arrives in headless mode. Workers abandon tasks at execution steps.
**Constraints:**
- Workers are always headless (run with `-p` flag)
- Coordinator permission mode is stored in `settings.local.json` (`permissions.defaultMode`)
- `--allowed-tools` supports fine-grained Bash patterns
- daemon.go notes `acceptEdits` blast radius as existing known limitation
- `niwa_delegate` task envelope can carry new fields

## Current status

**Phase:** 0 - Setup complete
**Last updated:** 2026-04-28
