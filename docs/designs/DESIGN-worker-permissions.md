# DESIGN: Worker session permissions

## Status

Proposed

## Context and problem statement

When niwa's mesh daemon spawns a worker session to handle a delegated task, that
session runs with `--permission-mode=acceptEdits`. This auto-approves file writes
but requires interactive user approval for all shell tool calls — `gh`, `git push`,
`go test`, and similar. Because workers run headless in `-p` mode, no human is
present to approve anything. The worker stalls on the first shell invocation and
the task transitions to `abandoned`.

Both reported failures (issue #86) followed the same pattern: the worker completed
all analytical and coding work, then hit the permission wall at the final execution
step (creating a PR, running tests). The fix must target the final execution step
without changing how coordinators behave.

## Decision drivers

- Workers run headless and unattended. Any permission model requiring interactive
  approval is a non-starter.
- The coordinator's permission mode is already expressed via `settings.local.json`
  (produced by `materialize.go`). Reusing that signal avoids a new configuration
  surface.
- Workers execute delegated subtasks within a coordinator's trust boundary. The
  coordinator chose to delegate; the natural permission model gives workers the same
  execution rights the coordinator has.
- Security: `daemon.go` already mentions the "acceptEdits blast radius" as a known
  limitation. Any broader permission requires careful scoping.
- The `--allowed-tools` flag supports fine-grained tool patterns (`Bash(gh *)`,
  `Bash(git *)`) as an alternative to a full permission mode bypass.
- `niwa_delegate` owns the task envelope; it can carry additional fields without
  breaking existing coordinators.
