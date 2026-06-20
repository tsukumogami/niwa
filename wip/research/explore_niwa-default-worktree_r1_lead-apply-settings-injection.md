# Lead: Apply Settings Injection for Worktree Policy

## Findings

### 1. InstallWorkspaceRootSettings() Implementation (workspace_context.go:237-373)

**What it writes:**
- **Location**: `.claude/settings.json` at instance root (line 359, not settings.local.json)
- **File mode**: `secretFileMode = 0o600` (line 26, materialize.go)
- **Permissions block**: 
  - Reads from `effective.Claude.Settings["permissions"]` (buildSettingsDoc, materialize.go:299-308)
  - Translates via `permissionsMapping`: `"bypass"→"bypassPermissions"`, `"ask"→"askPermissions"` (line 243-245)
  - Only creates `permissions.defaultMode` key (no `deny` array support yet)
- **Hooks block**:
  - Discovered via `DiscoverHooks(configDir)` from `hooks/` subdirectory (discover.go:21-69)
  - Two layouts: `hooks/{event}.sh` or `hooks/{event}/*.sh` 
  - Copies scripts to `.claude/hooks/{event}/` with 0o755 mode (line 296, workspace_context.go)
  - Event names snake_case in config → PascalCase in JSON via `hookEventMapping` (materialize.go:250-255)
  - Hook paths made relative or absolute per `UseAbsolutePaths` flag (line 281, workspace_context.go; `UseAbsolutePaths: true` for instance root at line 343)
- **Plugins block**: `enabledPlugins` key from `effective.Plugins` list (buildSettingsDoc:380-386)
- **Marketplaces block**: `extraKnownMarketplaces` from `effective.Claude.Marketplaces` (buildSettingsDoc:389-407)
- **Env block**: From `resolveClaudeEnvVars()` pipeline (line 327, workspace_context.go)
- **Merge behavior**:
  - Starts with workspace defaults via `MergeInstanceOverrides(cfg)` (line 243)
  - Instance-level overrides (from `[instance]` section) are appended/merged, not replacing (override.go:151-239)
  - Hooks append to workspace level (line 187, override.go)
  - Settings values overwrite workspace level (line 180, override.go)

### 2. Hook Discovery & Installation Mechanism (discover.go, workspace_context.go:245-307)

**Hook script discovery flow:**
- `DiscoverHooks(configDir)` walks `{configDir}/hooks/` (discover.go:21-69)
- Returns `config.HooksConfig` = map[event][]HookEntry where HookEntry = {Matcher, Scripts}
- Hook discovery is automatic and directory-driven; no manifest or config file declares them
- Scripts embedded nowhere; they're pure filesystem artifacts in the config directory
- Validated to stay within configDir via `validateWithinDir()` (discover.go:189-212, prevents symlink escape)

**Hook installation (workspace_context.go:245-307):**
- Line 246: `DiscoverHooks(configDir)` returns discovered hooks
- Lines 247-266: Merge discovered hooks into `effective.Claude.Hooks` (append if event not already in effective)
- Lines 268-307: Copy scripts to output `.claude/hooks/{event}/` and track installedHookPaths
- Scripts copied with 0o755 mode, validated within configDir
- Line 301-304: Build `installedHooks` map tracking which paths were actually installed
- Line 370: Only installed paths (not entire .claude/hooks/ walk) tracked as managed files for cleanup

### 3. Permissions.deny Array Support (current limitation)

**Current settings.json permissions structure:**
```json
"permissions": {
  "defaultMode": "bypassPermissions"  // only key supported
}
```

**No existing deny array:**
- buildSettingsDoc() only creates `defaultMode` (materialize.go:305-308)
- SettingsConfig map has no schema for nested structures; maps to strings via permissionsMapping
- `permissions` SettingsConfig value is scalar string not object (line 309, config.go)
- To add deny array, we would need to:
  1. Extend SettingsConfig to allow nested objects (breaking change or special-case)
  2. OR add a new separate `[instance.deny_tools]` list in workspace.toml
  3. AND modify buildSettingsDoc to emit `permissions.deny: [...]` array alongside defaultMode
  
**Risk analysis for option B:**
- No user-settings precedence conflict: instance root settings.json is generated-only, not merged with user edits (line 239 comment: "instance root is a non-git directory")
- Adding deny array is backward compatible (appending to permissions object)
- Could conflict if user has a custom settings.json they manually maintain, but workspace.toml [instance] generation is meant to override it

### 4. Dormant Delegation Hooks (option C feasibility)

**Hook event naming:**
- Current events: `pre_tool_use`, `post_tool_use`, `stop`, `notification` (hookEventMapping, materialize.go:250-254)
- New worktree events would need names like `worktree_create`, `worktree_destroy` or use Claude Code's hook event names if they exist
- **No evidence Claude Code has worktree-lifecycle hooks yet** — the memory mention of `permissions.deny: ["EnterWorktree","ExitWorktree"]` refers to tool restrictions, not hook events

**niwa's worktree-lifecycle hooks (separate from Claude hooks):**
- `DiscoverWorktreeHooks(configDir)` exists (discover.go:85-131) and scans `worktree-hooks/` directory
- These are for niwa's own ApplyToWorktree flow, NOT for Claude Code's settings.json
- Called during `niwa worktree create`/`apply` to run scripts as part of niwa's lifecycle (not Claude's)
- Not wired into Claude Code's settings at all

**For option C (dormant Claude worktree hooks):**
- Would require Claude Code to add new hook event types to its hook system
- niwa could preemptively discover and install them from `hooks/worktree-create.sh` style entries
- Hooks would sit dormant until Claude Code honors them
- Implementation: extend hookEventMapping with `"worktree_create"→"WorktreeCreate"`, modify buildSettingsDoc to emit them
- **Stdin/stdout contract needed:** Claude Code's hook stdin should carry {session_id, cwd, git_dir, worktree_path}; hook must print worktree path to stdout and exit 0 to signal success (matching EnterWorktree semantics)
- No blocker; hooks are just command paths — Claude Code doesn't validate hook names against a whitelist, so unrecognized event names will be silently ignored until Claude honors them

### 5. CLAUDE.md Guidance Layering (current system)

**How CLAUDE.md is currently layered by niwa:**
- Not written by niwa per-repo; users maintain CLAUDE.md in each repo's source
- niwa installs workspace-level CLAUDE.md at instance root: `workspace-context.md` via `InstallWorkspaceContext()` (workspace_context.go:177-202)
- niwa imports CLAUDE.overlay.md and CLAUDE.global.md via `.claude/rules/workspace-imports.md` using absolute @import paths (workspace_context.go:128-155, 204-235, 379-406)
- Rules file is auto-generated; workspace-context.md is auto-generated; no manual per-repo CLAUDE.md is managed by niwa at instance root

**Where worktree guidance would live:**
- Option A: Add guidance to auto-generated `workspace-context.md` section (regenerated on every apply, so durable)
  - E.g., "### Worktree Lifecycle: Use niwa worktree create" under "Working in this workspace"
  - Risk: Agent-facing guidance is in a generated file, so it changes if niwa's template changes
  - Benefit: Always fresh, doesn't require user-maintained CLAUDE.md edit
- Option B: Add guidance to an optional user-maintained `CLAUDE.overlay.md` or `CLAUDE.global.md`
  - Imported via workspace-imports.md, so agents see it
  - More durable but requires user to maintain it
- Option C: Add new niwa-generated layer like `CLAUDE.niwa.md` similar to workspace-context.md
  - Absolute import in workspace-imports.md
  - Always regenerated; can carry niwa-specific guidance without overwriting user content
  - Pattern: workspace-context.md (structure), CLAUDE.niwa.md (guidance), CLAUDE.overlay.md (private), CLAUDE.global.md (personal)

**Current CLAUDE.md layering shown in niwa/.claude/settings.local.json:**
- Hooks get discovered per repo; no per-repo CLAUDE.md is generated
- Workspace-root context is in workspace-context.md (auto-generated template)
- All repos inherit workspace context via `.claude/rules/workspace-imports.md` absolute @import chain

## Implications

**For enabling worktree as default:**
- **Option A** (permissions.deny) is clean: minimal code change, one line in buildSettingsDoc, one new setting key in SettingsConfig or dedicated [instance.deny_tools] section in workspace.toml. No merge/precedence issues for instance root (non-git, fully generated). **Recommendation: implement this first** — it's a blocker on worktree adoption until Claude Code's default behavior stops offering EnterWorktree.

- **Option B** (dormant delegation hooks) is zero-friction but waits on Claude Code. Can ship preemptively; hooks sit inert until Claude adds the event type. Requires extending buildSettingsDoc to handle new hook event names and emitting them unchanged to settings.json. No config schema changes needed (hooks are already flexible arrays).

- **Option C** (CLAUDE.md guidance) works today but needs careful placement. Workspace-context.md is auto-generated and can carry the guidance, but it's regenerated on every apply (risk of user edits). Better: introduce a new `CLAUDE.niwa.md` layer (similar to workspace-context.md structure) that's added to workspace-imports.md. Template it with "Always use `niwa worktree create` instead of Claude Code's worktree tools." Pattern is already proven (overlay, global, workspace-context).

**System design insight:**
- niwa's settings injection mechanism is well-isolated: instance root is non-git, fully generated, no user override merge (line 239 comment)
- Hook discovery is elegant: automatic from filesystem, no manifest coupling
- Instance settings fully override defaults per [instance] section; hooks append to not replace (unlike settings values which overwrite)
- Can inject new policy levers without breaking existing configs: deny array is additive, new hooks are ignored by old Claude Code, new CLAUDE.md layers are just more imports

## Surprises

1. **niwa has two parallel hook systems:** Claude hooks (copied to settings.json) and worktree hooks (run by niwa itself on `niwa worktree` commands). They're completely independent — worktree hooks can't call Claude Code and Claude hooks can't run during niwa's lifecycle. This explains why option C (delegation hooks) requires Claude Code feature adoption: Claude hooks run inside Claude's sandbox, not during niwa workflow.

2. **Settings at instance root are fully generated, never merged with user edits.** Unlike repo-level settings.local.json (which can coexist with a user's personal settings.json), the instance root .claude/settings.json is the only source of truth. No precedence layering. This makes it safe to add deny array without worrying about conflicting with user customization.

3. **SettingsConfig is a flat map[string]MaybeSecret, not a nested structure.** The "permissions" value is currently a scalar string that maps to a JSON object. Adding deny would either require expanding the grammar (breaking) or special-casing permissions in buildSettingsDoc to emit nested objects from a simple setting (hacky but backward compatible).

4. **Hook event names are flexibly mapped snake_case→PascalCase**, and unknown events don't error — they just become unrecognized events in the JSON. Claude Code silently ignores unknown hook types, so deploying dormant hooks is completely safe. No validation occurs on hook event names anywhere in the code.

## Open Questions

1. **Should deny list be a workspace.toml [instance.claude] setting or a new [instance.deny_tools] section?** Current pattern: `[instance.claude]` has `settings = {permissions = "bypass"}`. Could add `settings = {permissions = "bypass", deny_tools = ["EnterWorktree"]}` and special-case the deny_tools key in buildSettingsDoc to extract and emit as permissions.deny array. Cleanest from config perspective.

2. **If implementing worktree delegation hooks, what event names should niwa preemptively support?** Claude Code would need to define the hook event(s) and stdin/stdout contract. Likely candidates: `WorktreeCreate`, `WorktreeRemove` (or `worktree_create`, `worktree_destroy` in snake_case config, then mapped). Needs explicit discussion with Claude Code team.

3. **Where should "use niwa worktree create" guidance live for maximum agent visibility?** workspace-context.md (auto-regen, always fresh) vs new CLAUDE.niwa.md (patterned after workspace-context.md, sole purpose is niwa guidance)? The former is faster to ship; the latter scales better if more niwa guidance is needed later.

4. **Should SettingsConfig grammar be extended to support nested objects, or should we special-case permissions in buildSettingsDoc?** Current code maps `permissions` string value to `{defaultMode: ...}` in buildSettingsDoc. To support deny array, we'd need to either:
   - Parse permissions string as JSON object (e.g., `permissions = "{\"defaultMode\": \"bypass\", \"deny\": [...]}"` in TOML — terrible UX)
   - Special-case permissions key in buildSettingsDoc to combine multiple config values
   - Redesign SettingsConfig as map[string]map[string]any (requires config parser overhaul)

## Summary

niwa's instance-root settings injection is clean and isolated: `InstallWorkspaceRootSettings()` merges workspace defaults with [instance] overrides, discovers hooks from the filesystem, and generates `.claude/settings.json` via `buildSettingsDoc()`. Adding worktree policy (option A: permissions.deny array) requires only a new SettingsConfig key + buildSettingsDoc special-case to emit the deny array alongside defaultMode. Option C (guidance) works with the existing auto-generated workspace-context.md or a new CLAUDE.niwa.md layer. Option B (delegation hooks) is ready to deploy but awaits Claude Code integration.

