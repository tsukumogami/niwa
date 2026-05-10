---
complexity: testable
complexity_rationale: New behavior in worker spawn — three argv items added per spawn and per-repo skill writes removed. Empirically verified mechanism with multiple integration tests covering both spawn paths. Touches `internal/cli/mesh_watch.go` and `internal/workspace/channels.go`; affects every spawned worker.
---

## Goal

Make every worker spawned by niwa inherit the same Claude Code configuration a user would see by running `claude` directly in the role's repo, by appending `--add-dir <workspaceRoot> --add-dir <repoPath> --setting-sources user,project,local` to every `claude -p` invocation in `spawnWorker`, and remove the per-repo niwa-mesh `SKILL.md` writes that no longer have a delivery purpose.

## Context

Design: `docs/designs/current/DESIGN-niwa-mesh-reliability.md`

The worker spawn at `internal/cli/mesh_watch.go:908-1016` invokes `claude -p` with only `--mcp-config=...`, `--strict-mcp-config`, and `--allowed-tools <list>`. Worker config discovery is left to filesystem walk-up from CWD plus user-level `~/.claude.json`. Empirical work (Verification Notes in the design) showed:

- For session workers (CWD = a worktree under `.niwa/worktrees/`), walk-up reaches the workspace's CLAUDE.md but does NOT load the workspace's `enabledPlugins` and does NOT discover the niwa-mesh plain skill at `<workspaceRoot>/.claude/skills/niwa-mesh/SKILL.md`. Workers see zero shirabe/tsukumogami plugins.
- For main-instance workers (CWD = `<workspaceRoot>/<group>/<repo>`), walk-up works but the load is fragile.

The niwa repo's `.claude/` directory is git-untracked — only `CLAUDE.md` is tracked — so a fresh `git worktree add` creates a worktree with no `.claude/` at all. The contract requires explicit injection.

Per the design's Verification Notes, the working flag combination is:
- `--add-dir <workspaceRoot>` brings the workspace's `.claude/skills/`, `.claude/hooks/`, `.claude/CLAUDE.local.md` into Claude Code's discovery scope.
- `--add-dir <repoPath>` brings the repo's `.claude/settings.local.json` and per-repo customizations.
- `--setting-sources user,project,local` ensures all three setting layers are layered (default behavior excludes at least one).

Plain skills (`<project>/.claude/skills/<name>/SKILL.md`, like niwa-mesh) load via `--add-dir`; plugin skills (`shirabe@shirabe` aliases in `enabledPlugins`) load via `--setting-sources`. Both are required.

For session daemons, the spawnWorker computation must use `s.taskStoreRootDir()` for both `<workspaceRoot>` and `<repoPath>` derivation — `s.instanceRoot` resolves to the worktree (which has no `.claude/` after a fresh `git worktree add`), defeating the contract.

`InstallChannelInfrastructure` (`internal/workspace/channels.go:347-359`) writes `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for every non-coordinator role on every `niwa apply`; agents `git add .` and the file ends up in PRs (#97). Once workers find the niwa-mesh skill via `--add-dir <workspaceRoot>`, the per-repo writes are unnecessary and should be removed at the source.

Closes #108. Resolves #97 by elimination.

## Acceptance Criteria

- [ ] `spawnWorker` (`internal/cli/mesh_watch.go:982-1009`) appends three argv items to the `claude -p` invocation, in this exact order: `--add-dir <workspaceRoot>`, `--add-dir <repoPath>`, `--setting-sources user,project,local`.
- [ ] `<workspaceRoot>` is computed as `s.taskStoreRootDir()` for both daemon types.
- [ ] `<repoPath>` is computed as `resolveRoleCWD(s.taskStoreRootDir(), evt.role)` — note this is deliberately different from `cmd.Dir`, which keeps using `resolveRoleCWD(s.instanceRoot, evt.role)`.
- [ ] For main-instance daemons, `s.taskStoreRoot == s.instanceRoot`, so the two computations resolve to the same directory.
- [ ] For session daemons, `cmd.Dir` is the worktree (so git operations land there) while `--add-dir <repoPath>` points at the workspace's actual repo checkout (where the source-of-truth `.claude/` lives).
- [ ] `InstallChannelInfrastructure` (`internal/workspace/channels.go:347-359`) no longer writes `<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for non-coordinator roles. The instance-root copy at `<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md` (line 341) remains.
- [ ] Functional test (named-skill availability checklist): a session worker spawned by `niwa_create_session` + `niwa_delegate` can invoke each of: the niwa-mesh skill; one representative `shirabe:*` skill (e.g., `shirabe:plan`); one representative `tsukumogami:*` skill (e.g., `tsukumogami:work-on`); the workspace's user-level skill set (`superpowers:*`, etc.).
- [ ] Functional test (symmetry): a main-instance worker and a session worker, given the same delegation body, produce equivalent skill-list output for the named-skill set above. Numeric count parity is informational; if a residual gap remains (e.g., the 9-vs-11 shirabe gap surfaced during design verification), the test passes provided every named skill resolves in both contexts.
- [ ] Functional test (hook propagation): a workspace-defined hook in `<workspaceRoot>/.claude/settings.json` (e.g., `PreToolUse Bash`) fires inside the worker session.
- [ ] Functional test (skill-leak regression): after `niwa apply`, no consumer repo working tree contains `.claude/skills/niwa-mesh/SKILL.md`.
- [ ] Worker spawns continue to set `--mcp-config`, `--strict-mcp-config`, and `--allowed-tools` as before.
- [ ] Must deliver: workers see niwa-mesh and the workspace plugin set (required by <<ISSUE:6>> — the `required_skills` gate's manifest is meaningful only once this contract holds). Must deliver: the workspace's `.claude/skills/` is the canonical skill source (required by <<ISSUE:7>> — `niwa_redelegate` runs the gate against this manifest).

## Dependencies

None. Phase 3 is independent of <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:5>> and can ship in parallel.

## Downstream Dependencies

- <<ISSUE:6>> (required_skills gate) reads the workspace skill manifest this issue makes authoritative.
- <<ISSUE:7>> (niwa_redelegate) runs the same gate; same dependency.
- <<ISSUE:8>> (skill text + sessions guide) documents the worker config inheritance contract this issue lands.
