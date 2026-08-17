# Lead: What does niwa's workspace-preparation path actually do today, and where are its agent-specific decision points?

## Findings

### Two independent entry points, five materialization levels

`niwa apply` (`internal/cli/apply.go:96` `runApply`) drives two structurally separate materialization passes:

1. **Workspace root** (once per command, only when `scope.Mode == ApplyAll`): `apply.go:245` calls `workspace.MaterializeWorkspaceRoot(cfg, scope.WorkspaceRoot, RootMaterializeOptions{Agent: resolvedAgent, ...})` (`internal/workspace/root_materializer.go:123`). This is the parent directory holding `.niwa/workspace.toml` and the instance subdirs — e.g. `tsukumogami/public/`.
2. **Instance root + everything below it** (once per instance in `scope.Instances`): `apply.go:274` calls `applier.Apply(ctx, cfg, configDir, instanceRoot)` → `internal/workspace/apply.go:562` `Apply` → `apply.go:748` `runPipeline`, which materializes the instance root, group dirs, cloned repos, and live worktrees in one pass (Steps 4 through 6.75).

`niwa init` (`internal/cli/init.go:785`) calls the same `MaterializeWorkspaceRoot` for first-time setup, and `internal/cli/apply.go:200` / `internal/cli/init.go:198` / `internal/cli/create.go:229` / `internal/cli/reset.go:151` all set `applier.Agent` from `resolveSessionAgent` (`internal/cli/agent.go:16`, wrapping `agent.ResolveAgent`) before calling into the instance pipeline.

The five levels the path actually touches, in materialization order:

| Level | Example path | Materialized by |
|---|---|---|
| Workspace root | `tsukumogami/public/` | `MaterializeWorkspaceRoot` (root_materializer.go) |
| Instance root | `tsukumogami/public/niwa-instance/` | `apply.go` Steps 4–4.5 |
| Group directory | `<instance>/public/` | `apply.go` Step 5 |
| Cloned repository | `<instance>/public/niwa/` | `apply.go` Step 6 + Step 6.5 |
| Worktree | `<instance>/.claude/worktrees/<name>/` | `apply.go` Step 6.6 → `ApplyToWorktree` |

### The `internal/agent` contract and where it is actually consulted

`internal/agent/agent.go` (already on main, unmodified this round) exposes `Agent`, `AgentClaude`/`AgentCodex`, `ParseAgent`, `ResolveAgent`, and three accessors: `RootContextFileName()`, `LocalContextFileName()`, `WritesRepoLevelContext()`.

Grepping every non-test call site of these symbols (`agent.Agent`, `AgentClaude`, `AgentCodex`, the three accessors) turns up a **narrower** wiring than the lead's framing assumes — it is not simply "every materializer call site hardcodes an agent constant" (that was PR #248's problem). On current `main`, three functions already take `ag agent.Agent` and route the *filename* at the root/group level through `ag.RootContextFileName()`:

- `InstallWorkspaceContent` (`internal/workspace/content.go:28,44`) — instance root: `{instanceRoot}/{ag.RootContextFileName()}`.
- `InstallGroupContent` (`content.go:56,73`) — group dir: `{instanceRoot}/{group}/{ag.RootContextFileName()}`.
- `writeRootClaudeMD` (`internal/workspace/root_materializer.go:373,375`), called from `MaterializeWorkspaceRoot` (root_materializer.go:141) — workspace root: `{workspaceRoot}/{ag.RootContextFileName()}`.

Two more functions take `ag agent.Agent` but only use it as a **boolean gate**, not to route the filename:

- `InstallRepoContentTo` (`content.go:127,130`): `if !ag.WritesRepoLevelContext() { return result, nil }` — but every write inside the function after that gate hardcodes the literal `"CLAUDE.local.md"` (content.go:156, 186, 208). `ag.LocalContextFileName()` is never called here.
- `installWorktreeContextLayer` (`internal/workspace/worktree_content.go:736,740`): identical pattern — gates on `ag.WritesRepoLevelContext()`, then hardcodes `target := filepath.Join(worktreePath, "CLAUDE.local.md")` at worktree_content.go:743.

**`Agent.LocalContextFileName()` has zero call sites anywhere in the module outside `agent_test.go`.** It is reachable dead code today: the accessor exists, is exported, is documented, and is never invoked. This is the concrete shape of "an agent parameter threaded through a function signature without the function's actual write path consulting it" — worth distinguishing from PR #248's problem (a hardcoded constant at the call site with no parameter at all). Here the seam is *present* in the signature but *incomplete* in the body.

Everything else in the preparation path is Agent-blind — no `agent.Agent` parameter, no accessor call, unconditional Claude-shaped output regardless of which agent was resolved:

- `InstallWorkspaceContext` (workspace_context.go:182) — writes `.claude/rules/workspace-imports.md` and mutates `CLAUDE.md` (hardcoded, workspace_context.go:196-197) unconditionally.
- `InstallOverlayClaudeContent` (workspace_context.go:208) — same: hardcodes `CLAUDE.overlay.md`/`CLAUDE.md` imports.
- `InstallGlobalClaudeContent` (workspace_context.go:390) — same: hardcodes `CLAUDE.global.md`/`CLAUDE.md`.
- `InstallWorkspaceRootSettings` (workspace_context.go:242) — writes `{instanceRoot}/.claude/settings.json` and `{instanceRoot}/.claude/hooks/{event}/...` unconditionally; no `agent.Agent` parameter at all.
- `HooksMaterializer` (materialize.go:214-291) — writes `{repoDir}/.claude/hooks/{event}/...`. `MaterializeContext` (materialize.go:70) has no `Agent` field.
- `SettingsMaterializer` (materialize.go:1161-1253) — writes `{repoDir}/.claude/settings.local.json`.
- `EnvMaterializer` (materialize.go:1280+) — writes secret-output files; the target filenames themselves are configured, not agent-branched, so this one is closer to genuinely generic, but it is invoked unconditionally as part of the same per-repo Claude-flavored pipeline (see `runRepoMaterializers`, apply.go:1602).
- `FilesMaterializer` (materialize.go:1757+) — copies verbatim/content files; also configuration-driven rather than agent-branched, and also runs unconditionally.
- `writeRootSettings` (root_materializer.go:235) — writes `{workspaceRoot}/.claude/settings.json`; takes no `agent.Agent`, called unconditionally from `MaterializeWorkspaceRoot` regardless of `opts.Agent`.
- `writeRootSkills` (root_materializer.go:189) — writes `{workspaceRoot}/.claude/skills/<name>/SKILL.md` (a Claude Code project-skills concept) unconditionally, no agent check.
- `plugin.go` `ResolveMarketplaceSource` (plugin.go:16) — resolves Claude Code plugin-marketplace sources (`.claude-plugin/marketplace.json`); concept and file layout are Claude-only, no agent parameter.
- `permissions.go` `WorkerPermissionMode` (permissions.go:25) — **reads** `<instanceRoot>/.claude/settings.json` (hardcoded path) to decide a session's permission mode; no agent parameter, so this reader would silently return `"acceptEdits"` under Codex today (file simply won't exist there, but the path itself is agent-blind by construction, not by an explicit "codex has no permission file" decision).
- `scaffold.go` `Scaffold` (scaffold.go:115,131) — `niwa init`'s config-template writer; hardcodes `contentDir := filepath.Join(niwaDir, "claude")` (creates `.niwa/claude/`) regardless of agent, even though the generated template's own comment (scaffold.go:18) says `# Omit (or "claude") to materialize CLAUDE.md context; "codex" materializes ...` — i.e. the *documentation* already describes agent-conditional behavior that the directory-naming code does not implement.

Genuinely agent-agnostic, by content (not merely by omission):

- `gitignore.go` `EnsureInstanceGitignore` (gitignore.go:33) — writes a plain `.gitignore` containing `*.local*`; no Claude/agent concept anywhere in the file.
- Repo cloning (`apply.go` Step 3, cloneWorker/cloneWithRetry) — generic git operations.
- `DiscoverHooks` / `DiscoverEnvFiles` (`discover.go:21,140`) — read `configDir/hooks` and `configDir/env`, agent-neutral *inputs*; it's their *consumers* (HooksMaterializer, SettingsMaterializer) that are Claude-shaped on the output side.
- State tracking / `ManagedFile` hashing (apply.go Step 7) — generic.

### Error posture

The pipeline is close to uniformly hard-fail for the Claude-specific writers, with two exceptions:

- **Hard fail** (propagates `error` up through `runPipeline` → `Apply` → `runApply`, aborting the whole instance): `InstallWorkspaceContent`, `InstallWorkspaceContext`, `InstallOverlayClaudeContent`, `InstallWorkspaceRootSettings`, `InstallGroupContent`, `InstallGlobalClaudeContent`, `InstallRepoContent`, `HooksMaterializer.Materialize`, `SettingsMaterializer.Materialize`, `MaterializeWorkspaceRoot` and its sub-steps (`writeRootSettings`, `writeRootClaudeMD`, `writeRootSkills`) — every one of these returns `(nil, err)` on any I/O failure and the caller does not downgrade it to a warning.
- **Silent skip, no warning, no error**: `InstallWorkspaceRootSettings`'s hook-script copy loop (workspace_context.go:291-296) does `os.MkdirAll(...)` and `os.WriteFile(...)` with the returned errors **discarded**, and `continue`s past a hook script it fails to read (workspace_context.go:292-294) — a hook that can't be copied just doesn't appear, with nothing surfaced to the user. This is a stricter silent-skip than "warn and continue": there isn't even a warning.
- **Warn-and-continue**: worktree-delegation fallback disclosure (apply.go:1560-1579, `worktreeFallbackDisclosure`) — when niwa's own binary path can't be resolved, it downgrades to the deny-fallback and emits `a.Reporter.DeferWarn`/`.Log` rather than failing. Setup-script failures (Step 6.75, apply.go:1665-1693) are also warn-and-continue by design (`RunSetupScripts` errors become `a.Reporter.DeferWarn` plus a `setupIncomplete` list, never a hard `return nil, err`), but that step is generic (arbitrary repo-provided scripts), not Claude-specific.

I did not find a step that is "warn instead of fail" specifically *because* it is Claude-specific in the sense the lead's framing suggested (e.g., "plugin install failed, so just warn since only Claude needs plugins"). The Claude-specific writers that do run are either hard-fail-on-error or (in the one hook-copy case) silently skip a single file — there is no observed branch of the form "if agent is Codex, warn instead of failing on a Claude-only step," because no step currently checks the agent before deciding to run at all (other than the two `WritesRepoLevelContext()` gates and the three `RootContextFileName()` naming calls already covered above).

## The Preparation Call Graph

```
runApply (cli/apply.go:96)
├─ resolveSessionAgent (cli/agent.go:16) → applier.Agent   [flag > NIWA_AGENT > workspace default_agent > claude]
│
├─ [Level: workspace root, ApplyAll scope only]
│  MaterializeWorkspaceRoot (root_materializer.go:123)      Claude-specific?   Agent-consulted?   Error posture
│  ├─ writeRootSettings (root_materializer.go:235)           .claude/settings.json   yes            no (no Agent param)     hard fail
│  ├─ writeRootClaudeMD (root_materializer.go:373)           {root}/{RootContextFileName()}  yes(filename varies)  YES (ag.RootContextFileName)  hard fail
│  ├─ writeRootSkills (root_materializer.go:189)              .claude/skills/<name>/SKILL.md  yes            no                        hard fail
│  └─ materializeVerbatimFiles ([root.files])                 generic verbatim copy           no             n/a                        hard fail
│
└─ applier.Apply → runPipeline (workspace/apply.go:748)  [per instance]
   ├─ Step 3  clone repos (generic git)                                                        no             n/a                        hard fail
   ├─ Step 4  InstallWorkspaceContent (content.go:28)          {instance}/{RootContextFileName()}  yes(filename)  YES                     hard fail
   ├─ Step 4.5a InstallWorkspaceContext (workspace_context.go:182)  .claude/rules/workspace-imports.md + CLAUDE.md  yes  no                hard fail
   ├─ Step 4.5b InstallOverlayClaudeContent (workspace_context.go:208)  CLAUDE.overlay.md + CLAUDE.md import  yes  no                     hard fail
   ├─ Step 4.5c InstallWorkspaceRootSettings (workspace_context.go:242)  .claude/settings.json + .claude/hooks/  yes  no                  hard fail (except hook-copy loop: silent skip)
   ├─ Step 5  InstallGroupContent (content.go:56), per group                {instance}/{group}/{RootContextFileName()}  yes(filename)  YES  hard fail
   ├─ Step 5c InstallGlobalClaudeContent (workspace_context.go:390)          CLAUDE.global.md + CLAUDE.md import  yes  no                  hard fail
   ├─ Step 6  InstallRepoContent → InstallRepoContentTo (content.go:108,127), per repo   {repo}/CLAUDE.local.md (+ subdirs)  yes  gate-only (WritesRepoLevelContext; filename NOT routed through LocalContextFileName)  hard fail
   ├─ Step 6.4  worktree-delegation probe (apply.go:1544-1580)               decides hook-vs-deny for Step 6.5's SettingsMaterializer  no  n/a  warn-and-continue on binary-path failure
   ├─ Step 6.5  runRepoMaterializers, per repo (apply.go:1602)
   │  ├─ HooksMaterializer (materialize.go:214)                {repo}/.claude/hooks/{event}/...  yes  no (no Agent field on MaterializeContext)  hard fail
   │  ├─ SettingsMaterializer (materialize.go:1161)             {repo}/.claude/settings.local.json  yes  no                                       hard fail
   │  ├─ EnvMaterializer (materialize.go:1280)                  {repo}/{configured secret-output path}  no (config-driven)  n/a                    hard fail
   │  └─ FilesMaterializer (materialize.go:1757)                {repo}/{configured verbatim path}  no (config-driven)  n/a                        hard fail
   │  → gitexclude.EnsureRepoExclude (per repo)                 {repo}/.git/info/exclude  no  n/a                                                  hard fail
   ├─ Step 6.6  refreshWorktreeEnvs (apply.go:1915) → ApplyToWorktree, per live worktree
   │  └─ installWorktreeContextLayer (worktree_content.go:736)  {worktree}/CLAUDE.local.md  yes  gate-only (WritesRepoLevelContext; filename NOT routed through LocalContextFileName)  hard fail
   ├─ Step 6.75  RunSetupScripts, per repo                      generic repo-provided scripts  no  n/a                                              warn-and-continue (by design)
   └─ Step 7+  ManagedFile hashing / state save / plugin-record heal / marketplace registry reconcile  generic bookkeeping  no  n/a  hard fail (state save) / best-effort elsewhere

Cross-cutting, level-independent:
- WorkerPermissionMode (permissions.go:25) — reads {instanceRoot}/.claude/settings.json to pick a dispatched worker's permission mode. No Agent param; hardcoded read path.
- Scaffold (scaffold.go:115) — niwa init's config-template writer, hardcodes .niwa/claude/ content dir regardless of agent; template comment documents per-agent behavior the code doesn't implement.
```

## Implications

- The contract question is not "introduce agent-awareness where there is none" everywhere — it's already partially introduced, in exactly the two shapes a good design has to unify: (1) filename-routing done right (`RootContextFileName()` at three call sites) and (2) filename-routing *declared but not wired* (`LocalContextFileName()`, zero call sites, two call sites that should use it but hardcode a literal instead). A contract test that asserts "no bare `CLAUDE.local.md`/`CLAUDE.md` string literals appear in a write path once an `agent.Agent` value is in scope" would fail today at `content.go:156/186/208` and `worktree_content.go:743` — that's a concrete, mechanically-checkable instance of "every capability is either implemented or explicitly declared unavailable," except right now it's neither: it's implemented for Claude and silently wrong (not even attempted) for any other agent that might one day set `WritesRepoLevelContext() == true`.
- The much bigger gap is the settings/hooks/plugin/marketplace layer (`InstallWorkspaceRootSettings`, `HooksMaterializer`, `SettingsMaterializer`, `writeRootSettings`, `writeRootSkills`, `plugin.go`) — this is the majority of what actually gets written (by file count and by complexity: hook script installation, settings.json construction, plugin/marketplace resolution, project skills) and it has *no* agent parameter anywhere in the call chain, not even a boolean gate. Under Codex today, niwa still writes `.claude/settings.json`, `.claude/hooks/`, and `.claude/skills/` into every instance, group... no, correction: group/root/instance directories per the actual call sites above. A contract that only covers context-file naming (the two accessors that exist) leaves this whole surface unaddressed; a capability contract aimed at "every capability is either implemented or explicitly declared unavailable with a reason" needs to enumerate *this* set of writers, not just the CLAUDE.md-vs-AGENTS.md one.
- `WritesRepoLevelContext()` is currently the only accessor used as a true capability gate (skip vs. run). It's a boolean, not a per-capability declaration — it doesn't distinguish "Codex doesn't write repo-level context because that's a deliberate design choice" (documented at content.go:121-126) from "Codex doesn't write settings.json because nobody's implemented it yet" (undocumented, just absent). A contract that wants "explicitly declared unavailable with a reason" needs a richer shape than one bool for one capability.

## Surprises

- `internal/agent` is not dead code the way PR #248's abstraction was — three call sites (`InstallWorkspaceContent`, `InstallGroupContent`, `writeRootClaudeMD`) genuinely branch behavior on it today, correctly. The lead's framing ("every materializer call site hardcoded an agent constant") describes PR #248's *closed* prototype, not the current state of `main`, which already has commit `7c80744 feat(agent): add OpenAI Codex as a selectable agent (#208)` partially wired in. Any design work should account for this partial wiring rather than starting from a blank slate — and should explain why `LocalContextFileName()` exists with no callers (looks like an oversight from #208, not a deliberate deferral — contrast with `WritesRepoLevelContext()`'s deferral, which *is* documented in a comment).
- The two "root" materializers are easy to confuse: `MaterializeWorkspaceRoot` (root_materializer.go) targets the true workspace root (parent of instances), while `InstallWorkspaceRootSettings` (workspace_context.go) — despite its name — targets an *instance* root. The doc comment at root_materializer.go:117-120 calls this out explicitly ("It is the workspace-root counterpart to InstallWorkspaceRootSettings (which, despite its name, targets an INSTANCE root)"), so this is known-and-named confusion, not something I'm inferring. A contract design should either rename one of these or make the level explicit in both signatures.
- The silent-skip error posture (workspace_context.go:291-296, hook-script copy) is stricter than "warn" — it's not even logged. This is worth flagging distinctly from the lead's premise that "several Claude-side steps only warn": at least one step doesn't even warn.

## Open Questions

- Should `EnvMaterializer`/`FilesMaterializer` be treated as in-scope for the agent contract at all? They're configuration-driven (target paths come from `[repos.<name>.files]`/`[env]` config, not from agent identity), but they're only ever invoked as part of a per-repo pipeline that also runs the two Claude-hardcoded materializers in the same loop — so "declare unavailable" at the per-materializer level vs. at the per-repo-pipeline level changes what the contract needs to express.
- Does the contract need to cover *plugin marketplace* semantics (`plugin.go`, `.claude-plugin/marketplace.json`) as a capability Codex might someday have an equivalent for, or is that permanently Claude-only? The design doc will need to decide whether "no known Codex equivalent" is itself a valid "explicitly declared unavailable" terminal state, or whether the contract should only cover capabilities both agents plausibly need (context files, settings, hooks).
- `WorkerPermissionMode` (permissions.go) is a *reader*, not a materializer — should a capability contract govern read-side consumers of agent-specific state too, or only the write path the lead's lead scoped this investigation to?

## Summary

The prep path already threads `agent.Agent` through three call sites that correctly branch the context-file *name* (`RootContextFileName`, used at the workspace-root, instance-root, and group levels), but two more call sites (`InstallRepoContentTo`, `installWorktreeContextLayer`) accept the same `agent.Agent` parameter and only use it as a skip/run gate, hardcoding `"CLAUDE.local.md"` inside the gated body instead of calling the sibling accessor `LocalContextFileName()`, which as a result has zero callers anywhere in the module. The larger and more consequential gap is the settings/hooks/plugin/marketplace materializer set (`InstallWorkspaceRootSettings`, `HooksMaterializer`, `SettingsMaterializer`, `writeRootSettings`, `writeRootSkills`, `plugin.go`) — the bulk of what the pipeline actually writes — which carries no `agent.Agent` parameter at all and runs unconditionally and Claude-shaped regardless of the resolved agent; error posture across almost all of this is hard-fail, with one previously-undocumented silent-skip (a discarded-error hook-copy loop) rather than a warn.
