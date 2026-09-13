---
complexity: testable
complexity_rationale: One resolver function is rebuilt on an existing classifier plus a new layout predicate, which silently changes instance resolution for all nine worktree callers, so the risk is entirely in the layout table and the per-command output contract rather than in any new mechanism.
---

## Goal

Rebuild `discoverInstanceRoot` on `workspace.ClassifyCwd` and a new
`workspace.IsSingleInstanceLayout`, add the `errAtWorkspaceRoot` sentinel
carrying R10's line, and give `worktree list` the branch that prints it and
exits 0, so no `niwa worktree` subcommand treats a multi-instance workspace
root as an instance.

## Acceptance Criteria

- [ ] `workspace.IsSingleInstanceLayout(root string) bool` lands in
      `internal/workspace/state.go` and returns true only when
      `EnumerateInstances(root)` yields no child instance **and** the root's
      `.niwa/instance.json` loads with a non-empty `instance_name`. A unit
      test covers: a root whose `instance.json` has an empty `instance_name`
      (what every registered `niwa init` writes) → false; a root with a
      non-empty `instance_name` and no child instance → true; a root with at
      least one child instance → false; a root with no `instance.json` →
      false; a root whose `instance.json` is unreadable or malformed → false.
- [ ] `discoverInstanceRoot` is rebuilt on `workspace.ClassifyCwd` and no
      longer walks up looking for `.niwa/instance.json` itself:
      `CwdInsideWorktree` and `CwdInsideInstance` return the classification's
      `InstanceDir`; `CwdAtWorkspaceRoot` returns the root when
      `IsSingleInstanceLayout` holds and otherwise returns
      `errAtWorkspaceRoot`; `CwdOutside` returns the existing
      not-inside-a-workspace error naming the start directory.
- [ ] A package-level `errAtWorkspaceRoot` sentinel exists in
      `internal/cli/session.go`, callers test for it with `errors.Is`, and its
      message is exactly `niwa: error: this is the workspace root, not an
      instance; run inside an instance, or pass a session id to niwa worktree
      destroy` — the string `Execute` prints verbatim before exiting 1.
- [ ] `resolveInstanceRoot` still returns `NIWA_INSTANCE_ROOT` verbatim
      without classifying, and a test asserts it never yields
      `errAtWorkspaceRoot` when that variable is set, including when it points
      at a multi-instance workspace root.
- [ ] A table test in `internal/cli` pins resolution for the nine layouts of
      Decision 2 — an instance root, a repository clone inside an instance, a
      niwa worktree under `<instance>/.niwa/worktrees/`, a worktree Claude
      Code created under `<repo>/.claude/worktrees/`, a multi-instance
      workspace root, a freshly initialized root with no instance, a
      single-instance root, a directory outside any workspace, and a run with
      `NIWA_INSTANCE_ROOT` set — each asserting the exact returned root or
      `errors.Is(err, errAtWorkspaceRoot)`.
- [ ] `runSessionLifecycleList` handles `errAtWorkspaceRoot` by printing
      `niwa: this is the workspace root, not an instance; run inside an
      instance, or pass a session id to niwa worktree destroy` to stderr (no
      `error:` segment, matching R10's list row), printing no table and no
      `(no sessions match the current filter)` line, and returning nil so the
      command exits 0. (pre-fix fails) A command test runs it at a
      multi-instance root that holds at least one session mapping.
- [ ] `niwa worktree list --json` at a multi-instance root still prints `[]`
      on stdout alongside the stderr redirect and exits 0, keeping R21's
      existing output.
- [ ] At a multi-instance root, `niwa worktree create`, `worktree apply <x>`,
      `worktree attach <x>`, `worktree detach <x>` and `niwa go <repo> <x>`,
      where `<x>` is an 8-hex value that is no worktree id in any instance,
      each print exactly the R10 error line on stderr and exit 1, with nothing
      on stdout and no read of the root session mapping store as worktree
      records.
- [ ] At the same root, session-id shell completion offers no candidates (the
      completer already swallows resolver errors), and the WorktreeRemove hook
      path in `session_from_hook_cmd.go` logs its existing
      `could not resolve instance root` warning carrying the sentinel text and
      exits 0 without attempting a destroy; a test covers both.
- [ ] `resolveRegistryScope` in `internal/cli/apply.go` uses
      `workspace.IsSingleInstanceLayout` in place of its inline
      `len(instances) == 0` plus `instance.json` stat. A test shows a freshly
      initialized root with an unnamed root `instance.json` is no longer
      treated as single-instance (`Instances` empty, `WorkspaceRoot` set),
      while a named single-instance root still yields `Instances == [root]`
      with `WorkspaceRoot` empty.
- [ ] In a single-instance-layout workspace, `niwa worktree create` and
      `niwa worktree destroy <worktree-id>` at the root produce the same
      stdout, stderr and exit code as before the change, and `worktree list`
      there prints its table rather than the redirect.
- [ ] `go build ./...`, `go vet ./...` and `go test ./internal/cli/...
      ./internal/workspace/...` pass; every existing worktree subcommand test
      passes unchanged; `go.mod` gains no module; and the commit message or PR
      body records the `worktree list` case as failing against the parent
      commit.

## Dependencies

None

## Downstream Dependencies

Issue 6 builds `resolveDestroyScope` on the same two pieces: the
`ClassifyCwd`-based classification (instance directory versus workspace root)
and `workspace.IsSingleInstanceLayout`, which is what lets a single-instance
root count as an instance directory for destroy while a multi-instance root
routes to session resolution. It also relies on `errAtWorkspaceRoot` already
being the resolver's answer at a multi-instance root, so `runSessionDestroy`'s
positional branch can take over there instead of resolving the root as an
instance, and on the workspace root that classification yields being the
directory whose mapping store it reads.
