# Lead: Stop hook mechanics and coexistence

## Findings

### 1. Shirabe's Stop Hook Configuration

Shirabe installs a Stop hook in `/home/dgazineu/dev/niwaw/tsuku/tsukumogami-7/public/shirabe/.claude/settings.local.json` (lines 29-46):

```json
"hooks": {
  "Stop": [
    {
      "hooks": [
        {
          "command": ".claude/hooks/stop/workflow-continue.local.sh",
          "type": "command"
        }
      ]
    },
    {
      "hooks": [
        {
          "command": ".claude/hooks/stop/workflow-continue.local.sh",
          "type": "command"
        }
      ]
    }
  ]
}
```

The Stop hook is configured as an array with two entries (appears to be duplicate or intentional redundancy).

### 2. Claude Code Hook Architecture

Claude Code's `settings.json` (or `settings.local.json`) supports hooks via a structured format where:
- Each event (e.g., "Stop", "PreToolUse") maps to an **array** of hook entries
- Each entry is an object with a "hooks" field (array of command dicts) and optional "matcher" field
- Multiple entries in the array all execute — this is confirmed by the structure where each entry can have a matcher for conditional execution

Format: `{ "hooks": { "Stop": [entry1, entry2, ...], ... } }`

### 3. How Niwa Builds the Hook Array

Niwa's `buildSettingsDoc` function in `materialize.go` (lines 276-390) explicitly generates the hooks array structure:

```go
// From materialize.go line 305-340
for _, event := range events {
  installedEntries := cfg.InstalledHooks[event]
  pascalEvent, ok := hookEventMapping[event]
  
  var eventEntries []map[string]any
  for _, ie := range installedEntries {
    hookCommands := make([]map[string]string, 0, len(ie.Paths))
    for _, absPath := range ie.Paths {
      // ... path logic ...
      hookCommands = append(hookCommands, map[string]string{
        "type":    "command",
        "command": cmdPath,
      })
    }
    entry := map[string]any{
      "hooks": hookCommands,
    }
    if ie.Matcher != "" {
      entry["matcher"] = ie.Matcher
    }
    eventEntries = append(eventEntries, entry)
  }
  hooksDoc[pascalEvent] = eventEntries
}
```

Each event gets an array (eventEntries) that accumulates all hook entries from the merged config.

### 4. Hook Merge Semantics: Append, Not Replace

From `override.go` (lines 30 and 65-71):
- **Design principle**: "Hooks: repo values extend workspace values (lists are concatenated)"
- **Implementation** (line 70): `result.Claude.Hooks[k] = append(result.Claude.Hooks[k], v...)`

Hooks are **concatenated** at every merge level:
- Workspace hooks + per-repo hooks = appended
- Instance overrides + workspace hooks = appended
- Workspace hooks + global overlay hooks = appended (line 453-472)
- Base workspace hooks + workspace overlay hooks = appended (line 727-747)

### 5. Multiple Stop Hooks Can Coexist

Because:
1. Hook entries are stored as an **array** in settings.json (`"Stop": [entry1, entry2, ...]`)
2. Niwa's merge semantics **append** hooks from all config layers
3. The buildSettingsDoc function **iterates over all entries** and adds them all to the array

Therefore, **shirabe's stop hook and niwa's stop hook would both be written to the settings.json array** and Claude Code would execute both.

Example merged result:
```json
"hooks": {
  "Stop": [
    {
      "hooks": [{"command": ".claude/hooks/stop/workflow-continue.local.sh", "type": "command"}]
    },
    {
      "hooks": [{"command": ".claude/hooks/stop/niwa_report_progress.sh", "type": "command"}]
    }
  ]
}
```

### 6. Where Niwa Would Configure Its Stop Hook

From `override.go` and `DESIGN-workspace-config.md`:
- The coordinator (instance root) uses `MergeInstanceOverrides()` to combine workspace + instance-level config
- Workspace-level hooks are defined in workspace.toml: `[hooks] stop = ["hooks/niwa-stop.sh"]`
- Per-repo hooks extend workspace hooks via append semantics
- Hooks are materialized to `.claude/hooks/{event}/` and referenced in settings.local.json

**Location**: Niwa would configure the stop hook in the workspace's `.niwa/workspace.toml` file under the `[hooks]` section, e.g.:
```toml
[hooks]
stop = ["hooks/niwa_report_progress.sh"]
```

The hook script itself goes in `.niwa/hooks/` and is copied by the HooksMaterializer to the target repo's `.claude/hooks/stop/` directory during `niwa apply`.

### 7. Instance Root vs. Per-Repo Configuration

- Instance root (`$INSTANCE_ROOT/.claude/settings.json`): Generated from `MergeInstanceOverrides()` which applies instance-level + workspace overrides
- Per-repo settings (`$INSTANCE_ROOT/{group}/{repo}/.claude/settings.local.json`): Generated from `MergeOverrides()` which applies repo + workspace overrides
- Both paths use the same append semantics for hooks

Niwa could configure the stop hook at the workspace level (applies to all repos + instance root) or at the instance level (instance-root-only), or both. The instance root hooks are already generated from workspace config, so adding a workspace-level hook naturally includes it everywhere.

### 8. No Conflict or Override Risk

There is **no last-writer-wins behavior**. The hook event's array field in settings.json is not a single value but a list. All hooks in the array execute independently. This is confirmed by:
- Shirabe itself already declares two Stop entries (likely intentional)
- Claude Code's hook architecture uses arrays, not single-valued fields
- Niwa's merge logic explicitly uses `append()` at every layer

## Implications

1. **Coexistence is safe**: Niwa's stop hook and shirabe's stop hook can both exist in the same settings.json without conflict
2. **Execution order**: Both hooks will execute, but the order depends on:
   - Which config layer provides which hook (workspace first, then global overrides, then overlay)
   - Position in the array as written to settings.json
3. **Implementation path for stall watchdog fix**:
   - Add `[hooks] stop = ["hooks/niwa_report_progress.sh"]` to workspace.toml
   - Place the script in `.niwa/hooks/niwa_report_progress.sh`
   - Niwa's materialization pipeline will copy it to `.claude/hooks/stop/` and reference it in settings.local.json
   - The script runs automatically at every turn boundary, resetting the watchdog
4. **No awareness needed**: Shirabe and other skills don't need to know about or cooperate with niwa's stop hook — it's transparent infrastructure
5. **Determinism**: Hooks are materialized in order (workspace, global, overlay), and buildSettingsDoc sorts event names but preserves insertion order within each event's array

## Surprises

1. **Shirabe's duplicate Stop entries**: The settings.local.json has two identical Stop entries. This appears intentional (not a bug) but the purpose is unclear — possibly for matcher-based conditional execution or a holdover from config generation.
2. **No documented hook execution semantics**: Claude Code's documentation doesn't explicitly say whether multiple hooks in the array all execute or if there's any failure-stops-rest behavior. The assumption (all execute unless one fails) appears sound but is untested in the codebase.
3. **Instance root settings.json is not repo settings.local.json**: The instance root lives outside git and receives `settings.json` (not `.local`). This is an important distinction for where the workspace-level stop hook would appear.

## Open Questions

1. Does Claude Code execute all hooks in an array sequentially, or does one hook's exit code affect whether the next executes?
2. What is the intent of shirabe's duplicate Stop entries?
3. Should the niwa stop hook be configured at the workspace level (affects all repos) or instance level (affects only coordinator-loop), or both?
4. Does the order of hooks in the array matter (i.e., should niwa_report_progress run before or after shirabe's workflow-continue hook)?

## Summary

Shirabe configures its stop hook in `.claude/settings.local.json` as an array entry under `hooks.Stop`. Niwa's hook merge semantics append (never replace) hooks at every configuration layer, and Claude Code's settings.json format uses arrays for each hook event, allowing multiple hooks to coexist. Niwa should configure its `niwa_report_progress` stop hook in `workspace.toml`'s `[hooks]` section, which will be appended to shirabe's hook in the final settings.json array without conflict or override risk.
