# Design Summary: contextual-completion

## Input Context (Phase 0)
**Source:** /explore handoff
**Problem:** Add dynamic tab-completion to niwa commands that accept
workspace, instance, or repo identifiers, with delivery on by default via
the existing install paths.
**Constraints:**
- Cobra v1.10.2's `__complete` pipeline is already wired through the emitted
  bash V2 and zsh scripts; only `ValidArgsFunction` /
  `RegisterFlagCompletionFunc` closures are missing.
- 11 of 14 identifier positions in scope for v1 (see design doc for the
  skip list).
- Option B (union + TAB-decorated kind) for `niwa go [target]`
  disambiguation.
- No caching layer; raw data-source calls per tab press.
- Extract `EnumerateRepos(instanceRoot) []string` helper in
  `internal/workspace/` to consolidate the repo-scan logic currently
  duplicated in `findRepoDir` and three callers.
- Two-tier test strategy: unit tests in `internal/cli/completion_test.go` +
  functional tests in `test/functional/features/completion.feature`.
- No install-path changes; `install.sh` and the in-repo tsuku recipe at
  `.tsuku-recipes/niwa.toml` already ship completion.

## Current Status
**Phase:** 0 - Setup (Explore Handoff)
**Last Updated:** 2026-04-13
