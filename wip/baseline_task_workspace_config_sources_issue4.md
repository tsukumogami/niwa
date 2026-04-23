# Baseline

## Environment
- Date: 2026-04-23
- Branch: docs/workspace-config-sources (reused per user instruction "in this same branch")
- Base commit: d409567 (feat(test/functional,github): add tarballFakeServer + end-to-end integration tests)

## Test Results
All `go test ./...` packages green:
- internal/cli — ok
- internal/config — ok
- internal/github — ok
- internal/guardrail — ok
- internal/secret — ok
- internal/secret/reveal — ok
- internal/source — ok (cached)
- internal/testfault — ok (cached)
- internal/vault — ok
- internal/vault/fake — ok
- internal/vault/infisical — ok
- internal/vault/resolve — ok
- internal/workspace — ok
- test/functional — ok

## Build Status
`go build ./...` clean.

## Coverage
Not tracked in this project.

## Pre-existing Issues
- Legacy `git pull --ff-only` paths still load-bearing (the work this task retires):
  - internal/workspace/configsync.go:42
  - internal/workspace/overlaysync.go:34, 45
  - internal/workspace/sync.go:86 (likely workspace-repo, out of scope — to confirm in Step 6)
  - internal/cli/init.go:158 (Cloner.CloneWith)
  - internal/cli/init.go:322, 340 (CloneOrSyncOverlay)
  - internal/cli/config_set.go:64-65 (Cloner.Clone)
- `refreshSnapshotFallback` in internal/workspace/snapshotwriter.go:272 is a no-op stub.
- `instance.json` lives at `<workspace>/.niwa/instance.json` — same parent as snapshot dir. State-file collision risk under `SwapSnapshotAtomic`. Mitigation deferred to Issue 5; this task should not regress instance.json reads.

## Branch Context
This branch (docs/workspace-config-sources) carries 24 commits implementing PRD/Design/Plan + foundation packages + test infrastructure for the workspace-config-sources redesign. Issue 4 is the integration step that retires the legacy paths. Decision A (full replacement) confirmed via /decision framework in this session.
