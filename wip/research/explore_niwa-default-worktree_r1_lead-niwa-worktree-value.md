# Lead: What does niwa's existing worktree lifecycle materialize that bare `git worktree` (Claude Code's EnterWorktree) does NOT — and how is niwa's worktree mechanism surfaced to agents/users today?

## Findings

### 1. Content Materialized into niwa Worktrees (vs. bare `git worktree`)

**niwa worktree create/apply installs:**

1. **Repo CLAUDE content** — CLAUDE.local.md + subdirectory files (same as instance apply). This comes from the repo's config and is installed via `InstallRepoContentTo` — the shared function both instance apply and worktree paths use (`worktree_content.go:223`). So worktree and instance content cannot drift.

2. **Repo materializers** — HooksMaterializer, SettingsMaterializer, EnvMaterializer (which resolves vault:// secrets and writes .local.env files), FilesMaterializer. All run against the worktree via the shared `runRepoMaterializers` loop (`worktree_content.go:244`). Secrets are fully resolved through the vault pipeline (`session_lifecycle_cmd.go:62-70`), with AllowMissingSecrets=true for transient vault outages.

3. **Git exclude coverage** — `.niwa/` and materialized files are recorded in `.git/info/exclude` so niwa-authored content stays invisible to `git status` (`worktree_content.go:271`, `gitexclude/exclude.go:35`).

4. **Worktree rules import** — `.claude/rules/worktree-imports.md` with an absolute `@import` to the instance's workspace-context.md. When overlay or global CLAUDE files exist at the instance root, they're appended too. This lets the worktree, when launched as its own Claude Code project root, see the full workspace-context hierarchy (`worktree_content.go:278`, `worktree_content.go:323-351`).

5. **Worktree-context layer** — CLAUDE.local.md section appended with purpose, branch, repo name. Configurable via `[claude.content.worktree].source` template (`worktree_content.go:286`, worktree guide line 105-121).

6. **Worktree hooks** — Scripts from `worktree-hooks/` in the config repo, run on create/apply with worktree context exported as NIWA_WORKTREE_* env vars (`worktree_content.go:295`, worktree guide line 122-139).

7. **Session lifecycle state** — `.niwa/sessions/<id>.json` recording status, worktree path, branch name, creation time, creator PID (`session_lifecycle.go:236-240`). Used for worktree list/attach/detach operations.

8. **Worktree scaffolding** — `.niwa/worktrees/` directory structure created; `.niwa/sessions/` exists at instance root (`worktree.go:105-117`).

Claude Code's bare `EnterWorktree` creates only the git worktree directory with no content layer, no secrets materialization, no CLAUDE context, no rules import, and no session tracking.

### 2. What `niwa worktree apply` Does (vs. `niwa apply`)

`niwa worktree apply <id>` is the worktree analog of `niwa apply`: it re-materializes all the above content idempotently into an existing active worktree, without scaffolding a new one. Key differences from instance apply:

- **No git worktree add**: The worktree already exists; apply only re-syncs content.
- **Idempotency is stricter**: Re-pointing the rules import doesn't duplicate `@import` lines; replacing the worktree-context section overwrites rather than appends (`worktree.md` line 195-198, `worktree_content.go:385`).
- **Vault resolution is lenient**: AllowMissingSecrets=true allows transient vault outages to warn-and-continue instead of hard-failing (`session_lifecycle_cmd.go:57`).
- **Overlay merge pre-happens**: The worktree apply path calls `mergeWorktreeOverlay` before the resolve+merge helper, so overlay-augmented repos get overlay-merged content matching what a repo checkout would receive (`session_lifecycle_cmd.go:37-47`).

### 3. Discoverability of niwa Worktrees Today

**Very low — almost invisible to agents/users.**

- **niwa CLAUDE.md**: Mentions worktree guide in contributor section only; no user-facing intro (`public/niwa/CLAUDE.md` line 8).
- **Public CLAUDE.md**: Zero mention of worktrees; only references in private vision repo (`public/CLAUDE.md`).
- **Workspace-context.md**: Not generated or distributed; agents have no automatic awareness (`workspace-context.md` is instance-level state).
- **CLI help**: `niwa worktree create --help` exists but is hidden under the `session` alias (deprecated but still primary in many workflows). Commands are in `internal/cli/session_lifecycle_cmd.go` but not surfaced prominently.
- **Agents/Claude Code**: No agent is instructed to prefer `niwa worktree create` over EnterWorktree. The skill/command ecosystem does not expose worktree as a primitive agents reach for by default.
- **GitHub/PR/Issue context**: No public issues or designs discuss "use niwa worktrees as the default multi-repo checkout strategy" — this is a private research topic.

No workspace-root CLAUDE.md suggests `niwa worktree` as the isolation mechanism. Agents naturally fall back to Claude Code's bare EnterWorktree without guidance.

### 4. Value Proposition Gap: What Agents LOSE Using Bare Worktree Instead of niwa's

Agents using Claude Code's bare EnterWorktree vs. niwa worktree lose:

1. **Secret materialization** — No .env files with resolved vault:// secrets. Vault credentials stay literal in the config, never materialized into the worktree's environment. Agents cannot access secrets that would be available in a real checkout.

2. **CLAUDE content sync** — No repo-specific CLAUDE.local.md, no subdirectory files, no settings/hooks/env materializers. The worktree receives no Claude Code configuration context. Agents launch into a bare git checkout with no project-specific tooling.

3. **Workspace context import** — No .claude/rules/worktree-imports.md. The worktree doesn't see workspace-context.md, overlay content, or global CLAUDE files. An agent's `__SYSTEM_PROMPT_DYNAMIC_BOUNDARY__` lacks the workspace org context.

4. **Purpose/branch documentation** — No purpose/branch layer in CLAUDE.local.md. The agent sees no metadata about why the worktree exists or what branch it's on (absent from the context the agent sees).

5. **Worktree hooks** — No custom setup scripts run on entry. Initialization that depends on worktree context (e.g., loading environment, setting up tooling) does not execute.

6. **Session tracking** — No `.niwa/sessions/<id>.json`. niwa worktree list/attach/detach operations cannot discover or manage the worktree. The worktree is orphaned from workspace state.

7. **Git exclude coverage** — No .git/info/exclude rules. Materialized files (if an agent were to manually materialize them) show as untracked in `git status`, polluting the working tree.

8. **Idempotent re-sync** — Bare worktrees have no way to re-apply content after workspace config changes. Agents must manually re-create or manually apply updates, increasing friction for long-lived worktrees.

## Implications

1. **Current design split**: niwa worktrees are a fully-featured workspace primitive with tight content coupling (via shared `runRepoMaterializers` loop and overlay merge), but agents are not instructed to use them. Bare worktrees are discoverable (Claude Code's EnterWorktree is built-in) but incur a massive functionality gap.

2. **Making niwa worktrees default would require**:
   - Documenting the value proposition in workspace-root CLAUDE.md and agent guides.
   - Wiring agents to call `niwa worktree create` instead of EnterWorktree by default.
   - Potentially adding a skill (or hook in the coordinator) that wraps worktree lifecycle for agent-driven workflows.
   - Ensuring skills/commands that currently use EnterWorktree switch to niwa worktrees or offer both paths with clear tradeoffs.

3. **The "why" gap is critical**: Even if niwa worktrees are wired up, agents won't understand why they're better. The docs explain *how* (command reference) but not *why* (workspace consistency, secret resolution, content coupling, long-term maintainability).

## Surprises

1. **Vault secrets are resolved at worktree-apply time, not bootstrap** — I expected secrets to be resolved at instance create and cached. Instead, `session_lifecycle_cmd.go:62-70` runs the full resolve+merge pipeline every time `niwa worktree create/apply` is called. This is defensible (transient vault outages don't break worktree creation), but it means worktrees don't inherit pre-resolved secrets from the instance bootstrap — they re-resolve on demand.

2. **Overlay merge is pre-applied before resolve+merge** — I expected a single merge pass. Instead, the worktree path calls `mergeWorktreeOverlay` first (line 37-47), then the resolve+merge helper. This ensures overlay-augmented repos in the instance see the same base niwa and agents see the overlay-merged CLAUDE content, not the base-only version.

3. **Materialization is reused, not forked** — Both instance and worktree paths call `runRepoMaterializers` with the same materializer list. Adding a materializer in the future reaches both paths automatically. This tight coupling is elegant but means introducing a worktree-specific materializer requires care.

4. **The session state lives at instance scope, not repo scope** — Sessions are stored in `.niwa/sessions/` at the instance root, not under the repo. This makes `niwa worktree list` workspace-wide and keeps the state directory clean, but it means the instance must exist and be accessible for any worktree to exist (no standalone worktrees in a bare repo).

## Open Questions

1. **Why is AllowMissingSecrets=true the right default?** Shouldn't a worktree apply inherit the same strictness as instance apply? The comment says "instance create/apply already enforced strict secret resolution at bootstrap," but a manual `niwa worktree create` late in development (when vault is unavailable) silently skips secrets. Is this a regression surface?

2. **How do agents discover they should use niwa worktrees?** No skill, command, or agent instruction mentions it. Is the plan to add a skill wrapper, update CLAUDE.md files, or wait for agents to naturally prefer them?

3. **Is the worktree rules import resilient if instance files are deleted?** The import is absolute (`@import /path/to/instance/workspace-context.md`). If the instance is deleted or moved, the worktree's import breaks. Is this acceptable? Should the import be relative or conditional?

4. **Worktree hooks are only run on `apply` event, not `create` or `destroy`. Why?** The code discovers `apply`-event hooks only (`worktree_content.go:488`). Is `create` and `destroy` deliberately not hooked, or is this an oversight? Instance setup scripts run on apply; feature parity would suggest worktree hooks should too.

## Summary

niwa worktrees materialize a complete workspace-integrated checkout: secret resolution, CLAUDE content sync, workspace context imports, session tracking, and hooks. Claude Code's bare worktrees skip all of this. Discoverability is near-zero — no CLAUDE.md guidance, no CLI prominence, no agent instruction — so agents naturally default to bare worktrees despite the 8-item value gap. Making niwa worktrees the default requires documenting the "why," wiring skills to call them, and addressing the AllowMissingSecrets policy.
