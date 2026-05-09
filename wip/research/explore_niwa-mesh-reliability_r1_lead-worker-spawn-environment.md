# Lead: How does the worker spawn path establish the worker's Claude Code plugin set, and where (and why) is the niwa-mesh skill file written into the worktree?

## Findings

### 1. Worker spawn architecture (two daemon types)

There are two daemon shapes, both running the same `niwa mesh watch --instance-root=<root>` binary. The key difference is what `<root>` is:

- **Main-instance daemon** — started by `EnsureDaemonRunning(instanceRoot, nil)` from `niwa apply` (or any code that touches an unchanneled instance). `instanceRoot` is the workspace root.
- **Per-session daemon** — started by `niwa_create_session` via the same `EnsureDaemonRunning` helper, but with `extraEnv = ["NIWA_MAIN_INSTANCE_ROOT=<workspaceRoot>", "NIWA_SESSION_ID=<id>"]` and `instanceRoot = <worktreePath>`.

Both end up in `runMeshWatch` (`internal/cli/mesh_watch.go:156-310`), which sets up `spawnContext.instanceRoot = <argv-passed root>`. So inside a session daemon, `s.instanceRoot` is the **worktree path**, not the main workspace root.

References:
- `internal/mcp/handlers_session.go:211-225` — `s.daemonStarter(worktreePath, extraEnv)` is the call that starts the per-session daemon. `daemonStarter` is `workspace.EnsureDaemonRunning` (wired in `internal/cli/mcp_serve.go:32`).
- `internal/workspace/daemon.go:70-80` — `cmd := exec.Command(niwaBin, "mesh", "watch", "--instance-root="+instanceRoot)`; env is `os.Environ()` plus `extraEnv`.
- `internal/cli/mesh_watch.go:291-310` — for session daemons (`NIWA_MAIN_INSTANCE_ROOT` set), `taskStoreRoot` switches to the main instance, but `spawnCtx.instanceRoot` stays as the worktree.

### 2. The actual `claude -p` spawn (spawnWorker)

`internal/cli/mesh_watch.go:908-1016` constructs the worker `exec.Command`. The argv contract is:

```
<spawnBin>  -p <prompt>  --permission-mode=<mode>  --mcp-config=<path>  --strict-mcp-config  --allowed-tools <list>
```
(or with `--resume <session-id>` prefix for cross-task resume.)

`spawnBin` is resolved once at daemon start by `resolveSpawnTarget()` (`mesh_watch.go:2198-2230`):
1. `NIWA_WORKER_SPAWN_COMMAND` env var (must be absolute) — test/override path,
2. `exec.LookPath("claude")` — production path.

**Critical absences in argv:** no `--plugin`, no `--marketplace`, no `--settings`, no `--add-dir`, no `CLAUDE_CONFIG_DIR` env override. A repo-wide grep for these flags returns nothing. The worker inherits whatever Claude Code discovers from its CWD upward and from `$HOME/.claude/`.

**Env (`mesh_watch.go:1005-1009`):** `os.Environ()` plus three niwa-owned keys:
```
NIWA_INSTANCE_ROOT=<s.instanceRoot>     # = worktree for session daemon
NIWA_SESSION_ROLE=<evt.role>
NIWA_TASK_ID=<evt.taskID>
```
HOME, PATH, USER all pass through unchanged from the daemon's env (which is the parent niwa MCP server's env, which is the parent Claude Code coordinator's env). `--strict-mcp-config` (line 988) is set explicitly to *prevent* Claude Code from reading `~/.claude.json` MCP servers, but plugin/skill discovery is **not** affected by `--strict-mcp-config` — it scopes only MCP servers.

**CWD (`mesh_watch.go:1012`):** `cmd.Dir = resolveRoleCWD(s.instanceRoot, evt.role)`. `resolveRoleCWD` (`mesh_watch.go:2315-2342`) returns:
- For role `"coordinator"`: `instanceRoot` itself.
- Otherwise: scans `<instanceRoot>/<group>/<repoName>` for a directory matching the role name. Falls back to `instanceRoot` if not found.

For a **main-instance daemon** this resolves to `<workspaceRoot>/<group>/<repo>` (the actual repo checkout).
For a **session daemon**, `instanceRoot = <worktreePath>`, and the worktree directory does NOT have the workspace's `<group>/<repo>` layout — it is just the role's repo checkout. So `resolveRoleCWD` falls through to `return instanceRoot` and the worker CWD is the **worktree root** itself.

### 3. Plugin enablement is not wired into spawn — it is inherited from `.claude/settings.json` discovery

niwa generates Claude Code's `enabledPlugins` and `extraKnownMarketplaces` declaratively into static settings files at `apply` time:

- `internal/workspace/workspace_context.go:136-264` — `InstallWorkspaceRootSettings` writes `<instanceRoot>/.claude/settings.json` with `enabledPlugins` and `extraKnownMarketplaces` populated from `effective.Plugins` / `effective.Claude.Marketplaces`.
- `internal/workspace/materialize.go:450-528` — `SettingsMaterializer.Materialize` writes `<repoDir>/.claude/settings.local.json` with the same plugins/marketplaces (because it shares `buildSettingsDoc`, line 363-387).

So plugin "inheritance" is entirely dependent on Claude Code, at process start, walking the filesystem from CWD up and merging settings.json/settings.local.json. There is no programmatic injection.

This explains issue #108 directly:

- **Coordinator (human)** opens Claude Code at `<workspaceRoot>` — sees `<workspaceRoot>/.claude/settings.json` with `enabledPlugins: { shirabe@shirabe: true, tsukumogami@tsukumogami: true }`. Plugins available.
- **Worker spawned in main-instance daemon** runs at CWD `<workspaceRoot>/<group>/<repo>`. Claude Code discovers `<repoDir>/.claude/settings.local.json` (which DOES contain the same plugins via `SettingsMaterializer`). However, the plugins in `enabledPlugins` are referenced by alias (`shirabe@shirabe`) and rely on the user-level `pluginManagement.plugins` registry in `~/.claude.json`. If that registry isn't populated for the spawned worker, `enabledPlugins` references unknown aliases and quietly drop. (The user-reported list `superpowers:*, vercel:*, claude-api, ...` is consistent with the *user's own* `~/.claude.json` plugin set, with no workspace plugins layered on top — meaning workspace plugin alias resolution failed.)
- **Worker spawned in per-session daemon** runs at CWD `<worktreePath>` (the session worktree root). `<worktreePath>/.claude/settings.local.json` does NOT exist — apply was never run inside the worktree, only the `scaffoldWorktreeNiwa` function is called (`internal/mcp/handlers_session.go:80-108`) which creates only `.niwa/...` directories. So the session worker does not even see the workspace's settings file at the standard discovery path.

The reproducer in #108 (delegating into a session created by `niwa_create_session` to run `/shirabe:prd`) hits the second case — the worktree has no workspace settings on the discovery path at all.

`scaffoldWorktreeNiwa` deliberately does NOT copy `.mcp.json`, `.claude/settings.json`, or `CLAUDE.md` into the worktree (comment at `handlers_session.go:78-79`: *"It does NOT create mcp.json or workspace-context.md — those are main-instance artifacts that are not needed in session worktrees."*). The implicit assumption — that Claude Code will discover the parent workspace's config by walking up from the worktree — fails because the worktree path is `<workspaceRoot>/.niwa/worktrees/<repo>-<id>/`, which DOES walk up into `<workspaceRoot>/.claude/settings.json`. So discovery should at least find the instance root's settings... but only if `.niwa/worktrees/` does not contain its own intercepting `.claude/`. It doesn't, so the parent walk should reach `<workspaceRoot>/.claude/settings.json`. This means workers **should** see `enabledPlugins` from the workspace root settings, but only if Claude Code's plugin alias resolution succeeds (i.e. the marketplace is reachable and the plugin is installed in the user's plugin store). The empirical failure in #108 suggests one of:
- Claude Code's settings discovery does not walk above CWD when CWD is inside `.niwa/worktrees/`.
- Or plugin alias resolution fails (marketplaces not seen, plugins not installed in worker's user-level Claude store).

Either way, niwa today does nothing to actively guarantee plugin inheritance.

### 4. Why `.claude/skills/niwa-mesh/SKILL.md` lands in consumer repos (issue #97)

The skill file is written by `InstallChannelInfrastructure` in `internal/workspace/channels.go:338-359`:

```go
// Step 5: niwa-mesh SKILL.md at instance-root and per-repo. Content
// is identical across paths (flat uniform skill, Decision 5).
skillContent := buildSkillContent()
instanceSkill := filepath.Join(instanceRoot, ".claude", "skills", "niwa-mesh", "SKILL.md")
if err := writeIdempotent(instanceSkill, skillContent, 0o600, os.Stderr); err != nil {
    return fmt.Errorf("writing instance SKILL.md: %w", err)
}
*writtenFiles = append(*writtenFiles, instanceSkill)

for _, r := range roles {
    if r.name == coordinatorRole {
        continue
    }
    if r.repoPath == "" {
        continue
    }
    repoSkill := filepath.Join(r.repoPath, ".claude", "skills", "niwa-mesh", "SKILL.md")
    if err := writeIdempotent(repoSkill, skillContent, 0o600, os.Stderr); err != nil {
        return fmt.Errorf("writing %s: %w", repoSkill, err)
    }
    *writtenFiles = append(*writtenFiles, repoSkill)
}
```

`r.repoPath` for non-coordinator roles is the **cloned repo's working tree** at `<instanceRoot>/<group>/<repoName>` (built in `enumerateRoles`, `channels.go:516`: `topologyPaths[repoName] = filepath.Join(groupDir, repoName)`).

So niwa unconditionally writes `<consumerRepo>/.claude/skills/niwa-mesh/SKILL.md` for every non-coordinator role, on every `niwa apply` (idempotent — only writes when bytes differ). The file is mode 0600 but is otherwise a normal file in the repo's working tree.

There is no `.gitignore` mitigation for this path:
- `internal/workspace/gitignore.go` only ensures `*.local*` is gitignored at the **instance root** (not in consumer repos).
- `CheckGitignore` (called in `materialize.go:523`) only emits a warning if the repo's `.gitignore` lacks a `*.local*` pattern; it never adds `.claude/skills/niwa-mesh/`.

When a coordinator delegates work, the worker's CWD is the role repo (main-instance case) or the worktree (session case). For the main-instance case, the worker spawns directly inside the repo and any `git add -A` or `git add .claude` picks up `SKILL.md`. The reproducer in issue #97 (the file appeared in `codespar/codespar-web` PR #321) shows exactly this: the agent was working in the role repo, ran something equivalent to `git add .`, and committed `.claude/skills/niwa-mesh/SKILL.md` along with its real changes.

Note that the per-repo write was an explicit design decision (referred to as "Decision 5, flat uniform skill") — the comment at `channels.go:338-339` calls out *"Content is identical across paths (flat uniform skill, Decision 5)"*. So this is current intended behavior, not a bug in the implementation. The bug is that this design choice has the side effect described in #97.

### 5. Are #108 and #97 the same mechanism?

**No, they are independent:**

- #97 is about file-system pollution at apply time (`InstallChannelInfrastructure` writes the SKILL.md unconditionally into every non-coordinator role's repo path). The fix is upstream of any worker spawn — change where/how the niwa-mesh skill is delivered.
- #108 is about plugin inheritance at spawn time (the worker `exec.Command` does not pass plugins through, and CWD-based settings discovery does not reliably surface workspace plugins to workers). The fix is inside `spawnWorker` or its config plumbing — pass through plugin config explicitly, or arrange the worker's `.claude/` discovery path so it always finds workspace plugins.

They share a deeper *thematic* root — niwa relies on side-channel filesystem layout to communicate Claude Code config to workers, instead of explicit programmatic config — but the two failures sit on different files and call sites.

### 6. Cleanest injection points

**For plugins (issue #108):**

- Add a flag to `spawnWorker`'s argv. If Claude Code supports a `--settings <path>` or `--plugin <name>@<marketplace>` invocation, pass `<workspaceRoot>/.claude/settings.json` directly. This is the smallest change — a few lines around `mesh_watch.go:982-1001`.
- Or, set a env var on the worker (`CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` or similar) so the worker reads workspace settings regardless of CWD. This goes in `mesh_watch.go:1005-1009` next to the existing NIWA_* env injection.
- For session workers specifically, the cleanest fix is in `scaffoldWorktreeNiwa` (`internal/mcp/handlers_session.go:80-108`) — symlink or copy `<mainInstance>/.claude/settings.json` into `<worktree>/.claude/settings.local.json` so CWD-discovery finds workspace plugins. This is a one-function change.

**For the niwa-mesh skill leak (issue #97):**

- Stop writing per-repo copies. Inject the skill content via a different channel — `CLAUDE.local.md` (already discussed in the issue) or via a dynamically-written file outside the repo working tree.
- The change site is exactly `internal/workspace/channels.go:347-359` (the `for _, r := range roles` loop). Removing those lines and rerouting the skill content via, e.g., `CLAUDE.local.md` injection in `workspace_context.go` would resolve the leak.

## Implications

- The "everything Claude Code needs is on the filesystem" pattern is fragile. It works for the coordinator (a human launching `claude` at the workspace root) but breaks twice for workers: once because the configured settings path is not reliably discovered (#108), once because filesystem-side-channel files leak into commits (#97).
- Fixing #108 with explicit argv plugin flags or `CLAUDE_CONFIG_DIR` is more durable than relying on filesystem walk-up discovery — it survives any future Claude Code change to discovery semantics.
- Fixing #97 by injecting via `CLAUDE.local.md` (which is already part of the workspace_context flow) keeps the skill content out of the repo while preserving Claude Code's ability to load it.
- Session workers (per-session daemons) are doubly disadvantaged: they have neither workspace-context nor plugin reach. Any reliability work should treat session daemons as a first-class case rather than expecting parity with main-instance daemons via implicit discovery.

## Surprises

- `--strict-mcp-config` is explicitly used to prevent the user's `~/.claude.json` MCP servers from leaking into workers (`mesh_watch.go:959-965`), but plugin scope is left fully implicit. The same care is absent for plugins.
- `scaffoldWorktreeNiwa` (`handlers_session.go:78-108`) deliberately omits `mcp.json` / workspace-context.md from the worktree, citing them as "main-instance artifacts" — but the *consequence* (workers losing access to workspace plugins/skills/context) is not acknowledged anywhere in the file's commentary. Issue #110 (mentioned in the task) likely discusses this.
- The niwa-mesh skill is committed to the design as "flat uniform skill, Decision 5" — meaning the per-repo write was an intentional choice, not a bug. The design didn't anticipate that worker agents would `git add` the SKILL.md.
- The session worktree path is *inside* the workspace root (`<workspaceRoot>/.niwa/worktrees/...`), so in principle Claude Code's settings discovery walking upward from the worktree CWD should reach `<workspaceRoot>/.claude/settings.json`. The empirical failure in #108 implies discovery does NOT walk that far, OR plugin alias resolution silently drops aliases the user-level config doesn't know.
- `WorkerMCPConfig` (`channels.go:54-113`) goes to careful lengths to bake `NIWA_MAIN_INSTANCE_ROOT` and `NIWA_SESSION_ID` into the per-spawn MCP config so the niwa MCP subprocess inside the worker resolves the right workspace. This same care could (and does not) extend to handing the worker its plugin set.

## Open Questions

- Does Claude Code support a `--plugin`, `--settings`, `--add-dir`, or `CLAUDE_CONFIG_DIR` flag/env that would let `spawnWorker` inject workspace plugin config without copying files into the worktree? (No evidence in the codebase; would need Claude Code documentation.)
- Why exactly does the user-reported plugin set in #108 contain only the user's *personal* `~/.claude` plugins (`superpowers:*`, `vercel:*`, etc.) and zero workspace plugins? Two possible explanations:
  1. Claude Code does not walk up from CWD when discovering settings.json, so workers in `<workspace>/<group>/<repo>` see only that repo's `settings.local.json` plus `~/.claude.json`. But the repo's `settings.local.json` *does* contain `enabledPlugins`, so this would only explain partial loss, not full loss.
  2. `enabledPlugins` references plugin aliases like `shirabe@shirabe`, but the worker's `~/.claude.json` does not know how to resolve `shirabe` as a marketplace, so the alias drops silently. This matches the behavior described.
- Issue #110 (mentioned in instructions: *"MCP server `niwa mcp-serve` is the spawner of session daemons"*) — should be checked next round to see if it discusses or proposes a fix for the worktree config gap.
- Does the per-session daemon's worktree gain anything from `<workspaceRoot>/.claude/settings.json` discovery? The worktree path is `<workspaceRoot>/.niwa/worktrees/<repo>-<id>/`, which IS under workspaceRoot. If Claude Code walks up at all, settings discovery would land. So #108's persistent failure may indicate Claude Code is NOT walking up past CWD. This is a Claude Code question, not a niwa question.
- Should the fix for #97 simply remove per-repo skill writes and rely solely on the instance-root SKILL.md (`<workspaceRoot>/.claude/skills/niwa-mesh/SKILL.md`)? That would put the skill outside any consumer repo — but session workers (CWD = worktree, which is under workspaceRoot) would still need Claude Code's discovery to walk up from `.niwa/worktrees/<repo>-<id>/` to reach it. Same Claude Code discovery question as #108.

## Summary

The worker spawn at `internal/cli/mesh_watch.go:908-1016` invokes `claude -p` with no plugin, settings, or config-dir flags — it inherits HOME and PATH from `os.Environ()` and relies entirely on Claude Code's CWD-based filesystem discovery to surface workspace plugins. Session workers spawned via `niwa_create_session` run with CWD set to a session worktree under `.niwa/worktrees/`, where `scaffoldWorktreeNiwa` deliberately writes only `.niwa/...` (no `.claude/` mirror), so the worker sees neither workspace settings nor workspace context unless Claude Code walks up to the workspace root, which empirically does not happen. The niwa-mesh skill leak (#97) is unrelated to spawn — it comes from `InstallChannelInfrastructure` in `internal/workspace/channels.go:347-359` writing `.claude/skills/niwa-mesh/SKILL.md` into every non-coordinator role's repo working tree on every `niwa apply`, with no `.gitignore` mitigation.
