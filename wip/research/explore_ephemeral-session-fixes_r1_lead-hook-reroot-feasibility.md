# Lead: Can a SessionStart hook re-root a session to a different working directory so that settings.json and CLAUDE.md resolve from that directory?

## Findings

### 1. What SessionStart hooks can output (SUPPORTED)

From the official Claude Code hooks documentation (https://code.claude.com/docs/en/hooks.md), SessionStart hooks support these `hookSpecificOutput` fields:

- `additionalContext` (string): Added to Claude's context
- `sessionTitle` (string): Sets the session display name
- `initialUserMessage` (string): First message in non-interactive mode (-p)
- `watchPaths` (array of paths): For FileChanged events
- `reloadSkills` (boolean): Re-scan skill directories

**Critical: SessionStart hooks CANNOT:**
- Block or control behavior through decision fields
- Change the working directory persistently
- Reload settings files directly

Additionally, hooks have access to `CLAUDE_ENV_FILE` for writing environment variables for subsequent Bash commands, but this affects only Bash execution, not session-level settings resolution.

### 2. Can a hook re-root settings.json resolution? (NO)

**Definitive answer: No.** Here's why:

1. **Settings resolution timing:** Per https://code.claude.com/docs/en/settings.md, Claude Code loads settings files at LAUNCH, not mid-session. The precedence order is:
   - Managed (highest)
   - Command-line arguments
   - Local (`.claude/settings.local.json`)
   - Project (`.claude/settings.json`)
   - User (`~/.claude/settings.json`)

2. **Project/local settings discovery:** Project and local settings load only from `<cwd>/.claude/` where `<cwd>` is the working directory at launch. They do not have parent-directory fallback (unlike skills and CLAUDE.md which do search parent directories).

3. **Hot reload behavior:** Settings files ARE watched and reloaded automatically when changed, but this happens IN PLACE—it doesn't cause Claude Code to look in a different directory. A mid-session `cd` does not trigger re-discovery of a new `.claude/settings.json`.

4. **Hook output limitations:** SessionStart hooks cannot change working directory persistently, and no hook output field allows injecting new settings, plugins, env, or hooks into a running session after launch.

### 3. Can a background session be launched with a different cwd? (PARTIALLY)

**Status: Yes, but only at dispatch time, not via hook output.**

From https://code.claude.com/docs/en/agent-view.md and the Agent SDK documentation:

- **At dispatch time:** 
  - CLI: `claude --bg "<prompt>"` runs in the current shell's `cwd`
  - Agent view: Sessions dispatch in the directory where `claude agents` was opened (or specified via `@<repo>` mention)
  - Agent SDK: The `cwd` parameter in `query()` options sets where the session looks for project settings

- **Key quote from Agent SDK docs:** "The `cwd` option determines where the SDK looks for project-level inputs. CLAUDE.md and rules load from `<cwd>` and from every parent directory. Skills load from `<cwd>` and from every parent directory up to the repository root. Project `settings.json` and hooks load only from `<cwd>/.claude/` with no parent-directory fallback."

- **Background session hosting:** Per https://code.claude.com/docs/en/agent-view.md section "How background sessions are hosted," the supervisor applies "that session's directory, settings, and credentials to it" when assigning a pre-warmed worker.

### 4. Can settings/plugins/env/hooks be delivered AFTER launch? (NO)

**Definitive answer: No.** 

- `enabledPlugins`, `extraKnownMarketplaces`, `env`, and `hooks` are all **launch-time-resolved** from filesystem sources and/or environment variables.
- No hook output field allows injecting these after launch.
- The `CLAUDE_ENV_FILE` mechanism writes environment variables for subsequent Bash commands, but does not affect Claude Code's internal setting resolution.
- Even the `reloadSkills` field only re-scans skill directories; it does not reload settings or plugins.

**CLI flags that carry through to background sessions** (from https://code.claude.com/docs/en/agent-view.md):
```
--mcp-config, --strict-mcp-config
--settings
--add-dir
--plugin-dir
--fallback-model
```

These are applied at dispatch, not injected via hook. The `--settings` flag CAN override settings for agent view and all dispatched sessions (v2.1.142+), but this is a launch-time mechanism, not a hook-time mechanism.

### 5. How does Claude Code decide which .claude/settings.json to load for a background session? (DOCUMENTED)

From https://code.claude.com/docs/en/agent-view.md section "Permission mode, model, and effort":

> "A background session reads its settings from the directory it runs in, the same as if you had started `claude` there."

And from the Agent SDK docs:

> "The `cwd` option determines where the SDK looks for project-level inputs."

**Resolution order:**
1. If `cwd` is passed (SDK) or shell cwd is set (CLI), use that directory's `.claude/settings.json`
2. If no project settings found, fall back to user `~/.claude/settings.json`
3. Local settings at `<cwd>/.claude/settings.local.json` layer on top

The session does NOT search for settings in other directories after launch.

## Implications

**For niwa's dispatch mechanism:**

1. **SessionStart hook cannot fix the problem.** A hook cannot re-root the session to an instance directory after launch.

2. **Only solution: Dispatch with cwd specified.** The session must be launched (via CLI `--bg` or Agent SDK `cwd` parameter) pointing to the instance directory from the start.

3. **Current workaround (mid-session `cd`) is incomplete.** The `cd` in a SessionStart hook moves Bash's cwd but does NOT cause Claude Code to reload `.claude/settings.json`, load plugins, or re-scan skills from that new directory. The session still uses settings from the original launch directory.

4. **Two separate bugs require two separate fixes:**
   - **Bug 1:** Background sessions don't inherit the dispatcher's project settings (SessionStart hooks can provide context but cannot reload settings)
   - **Bug 2:** Mid-session `cd` doesn't cause settings re-resolution (architectural limitation)
   
   **Neither can be fixed with a hook redesign.** Both require either:
   - Launching the session with the correct cwd from the start, OR
   - A new Claude Code feature to reload settings mid-session from a new cwd (not currently supported or documented)

## Surprises

1. **SessionStart hooks are purely informational.** They can emit context and reload skills but cannot change session state, directory, or settings resolution. This is by design—they're meant to provide context, not reconfigure the session.

2. **The `CLAUDE_ENV_FILE` escape hatch only works for Bash.** It doesn't affect Claude Code's internal setting resolution, so environment variables written by a hook don't flow into plugin loading, MCP server config, or settings.json precedence.

3. **Agent SDK's `cwd` parameter is THE way to control settings resolution**, not CLI flags. The CLI has no `--cwd` flag (it uses the shell's cwd), but the SDK makes it explicit in the `query()` options.

4. **Project settings (.claude/settings.json) have NO parent-directory fallback.** This is different from skills and CLAUDE.md. This makes instance-level settings isolation possible but means you must launch in the instance directory to pick them up.

## Open Questions

1. **Could a SessionStart hook spawn a fresh session in the instance directory and transfer context?** The docs don't describe a hook mechanism for spawning sibling sessions. SessionStart hooks can only emit context and metadata—they don't have access to the session-spawning API.

2. **Is there a "reload settings from disk" command or API that could run mid-session?** Not documented. The settings are watched and reloaded automatically when the files change, but there's no command to force a re-resolution from a new cwd.

3. **Do niwa's SessionStart hooks currently emit `CLAUDE_ENV_FILE` writes that the session could then use?** If so, could those be leveraged to set `ANTHROPIC_*` environment variables that influence behavior? Possibly, but this would only affect the model/provider layer, not plugin/skill/settings loading.

## Summary

**SessionStart hooks cannot re-root a session to resolve .claude/settings.json from a different directory.** Settings are resolved at launch from the cwd used to start the session, and no hook output (additionalContext, sessionTitle, reloadSkills, env variables, or others) can change that. The architectural limitation is that hooks fire AFTER launch, after settings are already resolved. To load instance-specific settings, niwa must dispatch background sessions with the instance directory as the cwd at launch time—not via hooks, but via the dispatch call itself (CLI `--bg` or Agent SDK `cwd` parameter). This means the two bugs require two fixes at different layers: dispatch-time directory configuration, not session-time hook intervention.

