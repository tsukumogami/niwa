# Phase 2 Research: User Researcher

## Lead 1: CLAUDE.md Content Layering

### Findings

**Current Content System:**
- CLAUDE.md files use hierarchical selection (workspace → group → repo), not merging
- Each level has a distinct file: `CLAUDE.md` (workspace), `{group}/CLAUDE.md` (group), `{group}/{repo}/CLAUDE.local.md` (repo)
- Claude Code's @import mechanism allows adding supplementary imports to workspace CLAUDE.md that silently fail in child repos (file doesn't exist relative to them)
- Content installation is convention-driven: source files are read from `{content_dir}/` and written to target locations with variable substitution (`{workspace}`, `{workspace_name}`, `{group_name}`, `{repo_name}`)

**Content Fields in WorkspaceConfig:**
- `ContentConfig` has three levels: `workspace` (workspace-wide), `groups` (per-group), `repos` (per-repo with optional `subdirs`)
- Each entry is a reference to a markdown source file; no content is declared inline in the config
- Content sources are immutable after installation — there's no post-install merge or overlay mechanism

**How Content Is Installed (from apply.go):**
1. Workspace content: read `content.workspace.source`, substitute variables, write to `{instanceRoot}/CLAUDE.md`
2. Group content: for each group, read `content.groups[groupName].source`, write to `{instanceRoot}/{groupName}/CLAUDE.md`
3. Repo content: for each repo, read `content.repos[repoName].source`, write to `{group}/{repo}/CLAUDE.local.md`; optionally install subdirectory content
4. Auto-discovery fallback: if no explicit content entry exists for a repo, check for `{content_dir}/repos/{repoName}.md` and use it if found

**Workspace Context Pattern:**
- `workspace-context.md` is auto-generated from classified repos (lists groups and repos) and written to the instance root
- The workspace CLAUDE.md is then modified to include `@workspace-context.md` at the top via `ensureImportInCLAUDE()`
- This @import is visible when Claude Code walks up directory hierarchy from repos, but the file doesn't exist relative to child repos, so the import silently fails
- This pattern demonstrates that the same CLAUDE.md file can serve both instance-root and repo sessions with different effective context based on relative import resolution

**Content Merge Mechanics:**
- Currently there is no content merging — each level installs its own file
- The only mechanism resembling merge is @import, which appends supplementary content to a CLAUDE.md without modifying the base file
- Env vars and hooks use per-key merging in `MergeOverrides()`, but content files do not

### Implications for Requirements

**Personal CLAUDE.md content as a real use case:**
The scope document mentions "personal CLAUDE.md contributions (e.g., 'always respond in English')". This is a valid requirement: personal preferences about how Claude responds (language, tone, style guidelines) should apply consistently across all workspaces. Today's hierarchical selection model has no mechanism for this.

**Two design options emerge:**

1. **@import pattern (lightweight, non-invasive):** Generate `personal-instructions.md` in each instance root and add `@personal-instructions.md` to the workspace CLAUDE.md. The personal content becomes a supplementary import at the workspace level, inherited by all repos through directory traversal. This parallels the existing `workspace-context.md` pattern and requires minimal changes.
   - Pros: reuses proven @import mechanism; no content merging logic needed; personal instructions applied globally without overriding shared content
   - Cons: personal content appears only in workspace-level context; unclear if users expect per-repo or per-group personal overrides

2. **Content file replacement (invasive, merging-heavy):** Extend ContentConfig to support personal content entries (e.g., `[content.personal]`), merge personal content into workspace/group/repo content files at installation time, then write merged output. This allows personal CLAUDE.md sections to be applied at each level (workspace, group, repo).
   - Pros: granular per-level personal content; aligns with existing per-level structure
   - Cons: requires new content merging logic; personal content mixing with shared content in output files; harder to audit what personal config contributed; unclear merge semantics (append? prepend? replace section?)

**Requirements the PRD must specify:**
- Do users need to override CLAUDE.md content at multiple levels (workspace, group, repo) or only at the workspace level?
- If users add personal CLAUDE.md content, should it be visibly separate from shared content (e.g., "Personal preferences: ...") or seamlessly merged?
- Does personal content need to remove or override parts of shared content, or only append?
- Are there per-workspace personal CLAUDE.md overrides, or only global personal instructions that apply to all workspaces?

**Mental model clarification:**
The current scope document says personal config is keyed by `workspace.name`, with per-workspace overrides. The question is whether this extends to content. A plausible mental model:
- Global personal defaults: CLAUDE.md instructions that apply to all workspaces (e.g., "always respond in English")
- Per-workspace personal overrides: could include per-workspace CLAUDE.md modifications (e.g., workspace "tsuku" gets additional context about tsuku's architecture)

The @import pattern supports only global instructions; content file replacement supports both global and per-workspace.

### Open Questions

1. **Scope of personal CLAUDE.md contribution:** Is the requirement limited to global workspace-level instructions, or do users need per-group or per-repo personal overrides?
2. **Content merge or supplement:** Should personal content be merged into shared content files (risky for debugging), or supplementary imports (cleaner separation)?
3. **Per-workspace personal content:** Should personal config include workspace-specific CLAUDE.md sections (e.g., config keyed by workspace.name like other personal overrides), or only global personal instructions?
4. **Personal content removal/replacement:** Can personal config remove or replace shared content (dangerous for debugging), or only append?
5. **User mental model:** How should users think about personal CLAUDE.md? ("My instructions get appended to every workspace's context" vs "My instructions override the workspace's instructions" vs "I can customize any part of any CLAUDE.md"?)

---

## Lead 2: Opt-out Persistence Mental Model

### Findings

**Current Instance State Structure:**
- `InstanceState` (persisted in `.niwa/instance.json`) tracks: schema version, config name, instance name, instance number, root, creation time, last applied time, managed files, and repos
- There is no field for opt-out preferences or feature flags
- The state is updated on every `niwa apply` and carries forward between applies (e.g., `LastApplied` is mutated in place)

**Init Command Design (from DESIGN-init-command.md):**
- `niwa init` creates a new workspace instance
- No mention of `--skip-personal` or personal config opt-out in the init command design
- Init command is responsible for pre-flight checks (conflict detection), scaffold creation, and registry updates
- Post-flight validation runs after initialization

**Current Opt-out Pattern in Niwa:**
- `niwa apply --no-pull` is a one-time flag that skips pulling repo changes; it does not persist
- Each `niwa apply` invocation independently decides whether to pull based on the flag passed that day
- This is consistent with Unix CLI conventions: flags are per-invocation, not persistent state

**Analogous Patterns in Unix/Package Managers:**
- `git clone --depth 1`: one-time shallow clone flag, not persisted. Subsequent `git fetch` uses the repository's depth setting, not the original flag.
- `npm install --no-scripts`: skips hook execution during that install; running `npm install` again without the flag will execute hooks (one-time, not persistent)
- `npm ci --offline`: one-time flag for that invocation
- Convention: flags modify behavior for that command invocation only; to persistently change behavior, write it to config

**Precedent from Existing Niwa Config:**
- Personal config is registered via `niwa config set personal <repo>` (stored in `~/.config/niwa/config.toml`)
- Workspace config is stored in `.niwa/workspace.toml`
- Instance state (`instance.json`) tracks instance-specific runtime state (repo clone status, managed files, timestamps), not preferences

**User Mental Models:**
Two distinct mental models for opt-out:

1. **Persistent opt-out (stored in instance state):** User runs `niwa init --skip-personal`, and every subsequent `niwa apply` on that instance skips personal config. User forgets they opted out, runs `niwa apply` and gets surprised by the behavior. To re-enable personal config, user must edit `.niwa/instance.json` or run a new command like `niwa enable-personal`.

2. **One-time opt-out (flag per invocation):** User runs `niwa init --skip-personal` at init time to prevent personal config from being applied during creation. Subsequent `niwa apply` commands must explicitly pass `--skip-personal` if the user wants to skip personal config. This mirrors `git clone --depth 1` and `npm install --no-scripts`.

**Evidence from Niwa's Current Design:**
- No instance-level preferences stored in `InstanceState` today (only runtime state: repos, managed files, timestamps)
- `--no-pull` in `niwa apply` is one-time per invocation
- The distinction between instance state (runtime tracking) and config (preferences) is clear in the codebase: workspace config lives in `.niwa/workspace.toml`, global config in `~/.config/niwa/config.toml`, instance state in `.niwa/instance.json`

**Scenarios by Mental Model:**

Scenario: User is onboarding a CI/CD pipeline that should not use personal config.

- **Persistent opt-out:** `niwa init my-workspace --skip-personal` once during setup; all future CI runs use the workspace without personal config automatically.
- **One-time opt-out:** `niwa init my-workspace --skip-personal` once during setup; all future CI runs must include `--skip-personal` in the apply command, or the CI must configure it via a run script (e.g., `run.sh` with `niwa apply --skip-personal`).

Scenario: User wants to test a workspace without personal config, then re-enable it.

- **Persistent opt-out:** `niwa init ... --skip-personal`, test, then must manually re-enable (unclear UX).
- **One-time opt-out:** `niwa apply --skip-personal` for testing, then `niwa apply` (without flag) to re-enable (clear, reversible per invocation).

Scenario: User forgets they opted out at init time and later wonders why personal config isn't applied.

- **Persistent opt-out:** User confused; must debug instance state or check init history.
- **One-time opt-out:** User not affected; they never opted out on subsequent applies, so personal config applies normally.

### Implications for Requirements

**Principle: Consistency with Unix conventions and existing niwa patterns**

Flags should modify single-invocation behavior, not persistent state. Persistent preferences should be stored in config files (like `niwa config set personal`), not command-line flags. This aligns with how `--no-pull` works and how `git clone --depth 1` works.

**Recommendation for the PRD:**
- `niwa init --skip-personal`: one-time flag; prevents personal config sync during workspace creation
- Subsequent `niwa apply` commands do not skip personal config unless explicitly passed `--skip-personal`
- To persistently disable personal config for a workspace, add a new preference mechanism: e.g., `niwa config set personal skip <workspace-name>` (declarative in config, discoverable)

**Implication: Init behavior needs clarity**

The PRD must specify: what does `--skip-personal` do at init time?
- Option A: Skips personal config sync during init only; doesn't affect future applies
- Option B: Sets an instance-level preference that persists; affects all future applies until re-enabled

The evidence favors Option A (one-time), but the PRD should make this explicit.

**Implication: CI/CD and shared machines need a different control**

If a CI/CD pipeline or shared machine should never apply personal config, the user needs an opt-out mechanism at apply time, not just init time. The scope document lists "CI/CD systems, shared machines, testing" as users who would skip personal config. These users need to pass `--skip-personal` on each apply (or write it into a run script), not rely on a one-time init flag.

**Implication: Future discoverability**

If personal config is persistent, users need a way to discover what's opted out:
- `niwa status` should report if personal config is skipped
- `.niwa/instance.json` should document the field (if stored there)
- Error messages should mention the opt-out state if personal config fails

If personal config is per-invocation, this is less critical because the flag is explicit in the command.

### Open Questions

1. **Is the opt-out one-time (at init) or persistent (stored in state)?** The scope document is ambiguous. Evidence from Unix conventions and existing niwa patterns (--no-pull) suggests one-time is correct.
2. **If persistent opt-out, where is it stored?** Instance state (.niwa/instance.json), workspace config (.niwa/workspace.toml), or global config (~/.config/niwa/config.toml)?
3. **If one-time opt-out, does niwa need a persistent skip mechanism?** E.g., for CI/CD to opt out for a specific workspace without passing --skip-personal on every apply.
4. **What does the user see when they apply with personal config disabled?** Should there be a message like "skipped personal config (opt-out enabled)" or silent skip?
5. **Can users re-enable personal config after opting out at init?** If persistent, is there a `niwa enable-personal` command or must they edit state files?

---

## Summary

Personal CLAUDE.md content layering requires the PRD to choose between @import supplementary contributions (global-only, clean separation) vs content file replacement (per-level, complex merge logic). The current content system uses hierarchical selection, not merging, and @import demonstrates that both instance-root and repo sessions can share the same CLAUDE.md when relative path resolution is exploited.

Opt-out persistence should be one-time (per invocation) rather than persistent state, following Unix CLI and existing niwa patterns (--no-pull). If persistent opt-out is needed, it belongs in config (like `niwa config set`), not in instance state or init flags. The PRD must clarify the mental model and whether CI/CD needs a separate persistent skip mechanism.
