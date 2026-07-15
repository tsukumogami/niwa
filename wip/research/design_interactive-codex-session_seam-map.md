# F2 Codex-Agent Seam Map (niwa) — DESIGN/PLAN grounding

Read-only research grounding. File:line references from niwa `main` at the time
of authoring. wip/ scratch — do not reference from durable artifacts.

## A. Output-filename write sites (the crux) — 8 sites, 3 files

| Level | file:line | Var | Literal |
|-------|-----------|-----|---------|
| true workspace root | `internal/workspace/root_materializer.go:362` (const `rootClaudeFile` at :50) | `rootClaudeFile` | `CLAUDE.md` |
| instance/workspace | `internal/workspace/content.go:41` | `target` | `CLAUDE.md` |
| group | `internal/workspace/content.go:70` | `target` | `CLAUDE.md` |
| repo (base source) | `internal/workspace/content.go:142` | `target` | `CLAUDE.local.md` |
| repo (overlay-only) | `internal/workspace/content.go:172` | `target` | `CLAUDE.local.md` |
| repo subdir | `internal/workspace/content.go:194` | `target` | `CLAUDE.local.md` |
| worktree (repo content, reused) | `worktree_content.go:457` → `content.go:142` | — | `CLAUDE.local.md` |
| worktree (purpose/branch layer) | `internal/workspace/worktree_content.go:680` | `target` | `CLAUDE.local.md` |

- `installContentFile` (`content.go:259`) is filename-agnostic (takes `target` param);
  the 5 content.go callers own the literal — natural injection point for an
  agent-conditional filename helper.
- `content.go:41/70` are `CLAUDE.md` (non-git dirs); repo/subdir/worktree levels
  are `CLAUDE.local.md` (git dirs). An agent-aware namer must map BOTH the base
  and the `.local` variant.

## A2. Adjacent name-swap candidates the DESIGN must explicitly decide on

- `state.go:118` `overlayClaudeFile = "CLAUDE.overlay.md"`, `state.go:121`
  `globalClaudeFile = "CLAUDE.global.md"` — supplementary import targets written
  verbatim by name (`workspace_context.go:208-235`, `390-417`); imported via
  `@import` from the primary file. DESIGN decides: Codex analogs, or stay
  Claude-only imports referenced from within the AGENTS.md tree?
- Migration shim `removeImportFromCLAUDE` (`workspace_context.go:196,229,411`)
  reads/edits the primary `CLAUDE.md` to strip a stale relative `@import`. If the
  primary becomes `AGENTS.md` under Codex, this targets the wrong file unless
  made agent-aware.
- `instance_from_hook.go:295` READS `CLAUDE.md` for the SessionStart hook
  additionalContext — but hooks/provisioning are OUT of F2 scope, so leave alone.

## B. Selector precedents (mirror these; do NOT add to [claude] cascade)

- **EphemeralSessionMode** — `state.go:117` bool on `InstanceState`, persisted in
  workspace-root `.niwa/instance.json` (`StateDir=".niwa"`, `StateFile="instance.json"`,
  schema v4, `omitempty` additive). Read via fail-safe accessor
  `workspace.EphemeralSessionMode(root)` (`session_map.go:37-43`, returns false on
  error). Set at `niwa init` (`init.go:768,1006-1018`). Workspace default, NO
  per-session override.
- **DispatchModel** — `registry.go:37-43` string on `GlobalSettings`, from
  `~/.config/niwa/config.toml [global]`. Precedence in `dispatch.go:216-226`:
  `--model` flag > `[global].dispatch_model` > forward-nothing. Per-invocation
  override via flag.
- Agent selector = combine: workspace-scoped persisted default (like
  EphemeralSessionMode's `.niwa/instance.json`, or `workspace.toml`) + per-session
  override resolved via a DispatchModel-style precedence function (flag/env).

## C. Config cascade to AVOID (why selector lives elsewhere)

`[claude]` 4-position merge cascade: `WorkspaceConfig.Claude` (`config.go:245`,
wide `ClaudeConfig`), `RepoOverride.Claude` (`config.go:364`), `InstanceConfig.Claude`
(`config.go:260`), `GlobalOverride.Claude` (`config.go:610`) — latter three narrow
`ClaudeOverride` (`config.go:69-75`). This answers "what Claude config applies to
THIS repo" (per-repo mergeable). Agent is session-global, not per-repo → must NOT
be a 5th field here.

## D. Secret split — ALREADY agent-neutral (near-zero code)

`ClaudeEnvConfig` (`config.go:232-236`) → `EnvVarsTable` (`config.go:215-220`)
`Values map[string]MaybeSecret` keyed by arbitrary env-var name. `ANTHROPIC_API_KEY`
is just a populated key, not a schema field. Resolution via vault
(`resolveClaudeEnvVars`, `internal/vault/`) is key-agnostic. `OPENAI_API_KEY` slots
in as another `[claude.env.secrets]` row — F2 needs ZERO code changes to the
mechanism; DESIGN scope = docs + a round-trip test proving it (template:
`config/vault_test.go:297-323` round-trips ANTHROPIC_API_KEY). `PRD-vault-integration.md`
already documents both keys side by side. Do NOT touch `dispatch_remotecontrol.go:58`
(Claude-Remote special-case; out of scope).

## E. Model seam — `internal/cli/dispatch_model.go`

`modelCategories` (`:18-22`) fast/balanced/powerful → haiku/sonnet/opus;
`knownModelNames` (`:28-33`); `resolveDispatchModel` (`:48-64`); `knownModelHint`
(`:69-72`). Only call site: `dispatch.go:222-226` (background-dispatch launcher).
Per-agent variant = key `modelCategories` by agent (Codex model names TBD by
DESIGN) and thread selected agent into `resolveDispatchModel`.
**Scope tension the DESIGN must resolve honestly:** the ONLY consumer today
(`dispatch.go`) is the launch/dispatch path that F2 puts out of scope. F2 lands
the per-agent map/resolver + tests as keystone groundwork; the live launch
consumer arrives with the dispatch feature. Adding agent-keyed data + an
agent-aware resolver is data/function only — it does not add launch code.

## F. Materialize call graph — where to thread the selected agent

- Instance apply/create: CLI (`apply.go:219`, `create.go:216`, `init.go:192`,
  `reset.go:132`, `instance_from_hook.go:382`) → `Applier.Apply`(`apply.go:429`)/
  `Applier.Create`(`apply.go:280`) → shared `Applier.runPipeline`(`apply.go:614`):
  Step 4 `InstallWorkspaceContent`(1265), Step 5 `InstallGroupContent`(1320),
  Step 6 `InstallRepoContent`(1343), worktree refresh → `ApplyToWorktree`.
- True-root: `MaterializeWorkspaceRoot`(`root_materializer.go:110`) from
  `init.go:768` and re-driven on apply via `apply.go:197` — OUTSIDE runPipeline.
- Worktree: `ApplyToWorktree`(`worktree_content.go`) from
  `session_lifecycle_cmd.go:341`.
- Thread the resolved agent through 3 entry points (runPipeline,
  MaterializeWorkspaceRoot, ApplyToWorktree) into the writer calls. Cleanest:
  a small session-context value / field on `Applier` (session-global), NOT a
  config-cascade field. `RootMaterializeOptions` (`root_materializer.go:81`)
  already carries `EphemeralSessionMode` — a parallel `Agent`/`AgentType` field
  there is the established pattern.

## G. Existing test surfaces

- `internal/workspace/content_test.go` — 20 tests over every content.go write site;
  parameterize by agent.
- `internal/workspace/root_materializer_test.go` — `TestMaterializeWorkspaceRoot_ClaudeMD`(:130)
  asserts CLAUDE.md output; ephemeral/hooks/plugin tests.
- `internal/workspace/worktree_content_test.go` — 13 ApplyToWorktree tests.
- `internal/cli/dispatch_model_test.go` — `TestResolveDispatchModel` table test; template for per-agent map.
- `internal/config/vault_test.go:297-323` — ANTHROPIC_API_KEY round-trip; template for OPENAI_API_KEY.
- `internal/workspace/state_test.go` — schema/migration; selector-state consumption tested at CLI (`init_test.go`, `instance_from_hook_test.go`).
