# Issue 99 Baseline

## Environment
- Date: 2026-05-05
- Branch: fix/99-shell-init-macos-zsh
- Base commit: ab3eab080570c6aaab68095d20fd788c27460b2e
- Platform: macOS (darwin/arm64)

## Build Status
`go build ./...` succeeds.

## Test Results

`go test -count=1 -short ./...`:
- `internal/config`, `internal/github`, `internal/guardrail`, `internal/mcp`,
  `internal/secret`, `internal/secret/reveal`, `internal/source`,
  `internal/testfault`: PASS
- `internal/cli`: FAIL (20 pre-existing failures, all platform-specific to macOS;
  none touch shell-init code)

### Pre-existing macOS-only failures (not in scope)

All failures fall into three buckets, none related to shell_init.go or
completion.go:

1. Linux `/proc/<pid>/stat` reads (mesh watcher orphan polling):
   - TestReconcile_LiveOrphan, TestReconcile_StartTimeDivergence,
     TestOrphanPolling_WorkerCompletes, TestOrphanPolling_WorkerDies,
     TestPollOrphans_TransientReadErrorKeepsEntry

2. Linux-specific `/bin/true` path / daemon.pid timing / mesh runtime:
   - TestResolveSpawnTarget_OverrideAbsolutePath,
     TestRunEventLoop_CatchupSpawnsWorker,
     TestRunMeshWatch_LogsSpawnTargetAtStartup,
     TestSpawnWorker_ExitEventNotDroppedUnderBackPressure,
     TestRetryCap_UnexpectedExitWithinCap, TestRetryCap_BackoffTiming,
     TestRetryCap_BackoffSliceShorterThanCap,
     TestRetryCap_AbandonedMessageDeliveredToDelegator,
     TestWatchdog_DisabledWhenStallZero,
     TestReconcile_SpawnNeverCompleted, TestConcurrentApply_SingleDaemon

3. macOS `/var/folders` -> `/private/var/folders` symlink (path comparison in
   go subcommand tests):
   - TestGoNoArgs_InsideWorkspace, TestGoSingleArg_RepoInInstance,
     TestGoRepoFlag_InsideInstance, TestGoSingleArg_BothRepoAndWorkspace

### Shell-init test surface (in scope) — all PASS

`go test -run "ShellInit|Completion" ./internal/cli/...`:
- TestHintShellInit_Suppressed, TestHintShellInit_Shown,
  TestShellInitBash_ValidSyntax, TestShellInitZsh_ValidSyntax,
  TestShellInitAuto_DetectsBash, TestShellInitAuto_DetectsZsh,
  TestShellInitAuto_UnknownShell, TestShellInitInstall_CreatesEnvFile,
  TestShellInitInstall_AddsSourceLine, TestShellInitInstall_Idempotent,
  TestShellInitUninstall_RemovesDelegation, TestShellInitUninstall_NoEnvFile,
  TestShellInitStatus_WrapperLoaded, TestShellInitStatus_NotLoaded

All 14 PASS.

## Coverage
Not tracked in this repo (no coverage targets in Makefile).
