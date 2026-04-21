# Phase 2 Research: Codebase Analyst

## Lead 4: Observable Mesh State and Integration Points

### Findings

#### 1. `niwa session list` — how existing list commands are formatted

The closest analogue is `showSummaryView` in `internal/cli/status.go` (line 137–176). When run from the workspace root it prints a compact table:

```
Instances:

  tsuku        3 repos   0 drifted   applied 2h ago
  tsuku-2      3 repos   1 drifted   applied 5m ago
```

The pattern is: fixed-width left column (`%-12s`), then space-separated columns with counts and human-readable relative timestamps (`formatRelativeTime` at line 309–334, same file).

`showDetailView` (line 178–276) adds a `Repos:` section that lists each repo and a status string. The columns are `%-12s %s` (name, status). This is the model for a `niwa session list` output.

Both views consume `workspace.ComputeStatus` (`internal/workspace/status.go`) which returns `InstanceStatus` — a struct whose `Repos []RepoStatus` uses the same `Name` / `Status` pattern. A `SessionStatus` struct would follow this exactly.

There is no existing `niwa list` command; `niwa status` is the primary observability surface.

Suggested column set for `niwa session list`, derived from the pattern above:

| Column | Source |
|--------|--------|
| Session ID (short, 8 chars) | `sessions.json` entry |
| Role / Repo | `sessions.json` `Repo` field |
| PID | `sessions.json` `PID` (liveness via `os.FindProcess`) |
| Last heartbeat | relative, using existing `formatRelativeTime` |
| Pending messages | count of files in inbox directory |
| Status | "live" / "stale" / "dead" derived from heartbeat age or PID check |

The `formatRelativeTime` helper is in `internal/cli/status.go` and is exported-by-convention within the `cli` package — it would be available directly to a new `session.go` file in the same package.

#### 2. Message log — `niwa session log`

No message log or audit trail exists anywhere in the codebase today. The closest pattern is how managed-file drift is surfaced: `ManagedFile.Sources []SourceEntry` (defined in `internal/workspace/state.go` line 99–119) provides an append-style provenance record per file, tracked in `instance.json`.

For messages, the file-based inbox approach (`<instance-root>/.niwa/sessions/<session-id>/inbox/`) means each message is its own file. A log command would be a directory scan + sort-by-mtime, analogous to how `EnumerateInstances` (`state.go` line 256–277) scans for `instance.json` markers.

There is no existing `--since`, `--tail`, or structured-log pattern in the CLI. The `Reporter` (`internal/workspace/reporter.go`) is a progress reporter, not a log store; it does not persist anything.

Log retention would be a new concept. Nothing in the codebase retains historical output; `niwa status` is purely current-state. A `niwa session log [session-id]` command that lists files in the inbox sorted by mtime would be consistent with the filesystem-scan pattern. TTL-based cleanup would need a new mechanism (cron-style on `niwa apply`, or eager cleanup on `niwa session unregister`).

#### 3. `niwa status` — does it exist and what does it show?

`niwa status` exists at `internal/cli/status.go`. It has two modes:

- **Summary view** (from workspace root): prints all instances with repo count, drift count, and "applied N ago". Triggered when `workspace.DiscoverInstance(cwd)` fails but `config.Discover(cwd)` succeeds.
- **Detail view** (from inside an instance): prints instance name, config name, root path, created/applied timestamps, then a `Repos:` section with clone status, then a `Managed files:` section with drift status.

Flags: `--audit-secrets`, `--check-vault`, `--verbose`. All are domain-specific to the existing materializer outputs.

Mesh state is **not** in `niwa status` today. The question of whether to integrate it or keep it separate comes down to coupling: `niwa status` reads `InstanceState` from `instance.json`, which is written by `niwa apply`. Session state (`sessions.json`) is runtime state written by live sessions — a fundamentally different writer and lifecycle. Integrating mesh info into `niwa status` risks stale data (a session that crashed doesn't update `instance.json`) and makes the output noisier for users who don't use channels. A separate `niwa session list` subcommand keeps concerns cleanly separated, matching how `config set` is a separate subgroup from the core commands.

A brief summary line in `niwa status` (e.g., `3 sessions registered (2 live)`) would be low-cost and consistent — analogous to the shadows summary at status.go line 265–273 — without requiring full integration.

#### 4. `.mcp.json` and MCP provisioning

No `.mcp.json` file and no MCP-related code exists anywhere in the niwa codebase. The only settings files niwa writes are:

- `<repo>/.claude/settings.local.json` — written by `SettingsMaterializer.Materialize` (`internal/workspace/materialize.go` line 460–528)
- `<instance-root>/.claude/settings.json` — written by `InstallWorkspaceRootSettings` (`internal/workspace/workspace_context.go` line 144–262)

Both use `buildSettingsDoc` (materialize.go line 276–390) which builds a `map[string]any` JSON document. The current keys are `permissions`, `hooks`, `env`, `includeGitInstructions`, `enabledPlugins`, and `extraKnownMarketplaces`.

Claude Code's `.mcp.json` format (per the Claude Code docs) is a top-level object with an `"mcpServers"` key whose values are objects with `"command"`, `"args"`, and optionally `"env"`. A `ChannelMaterializer` would need to:

1. Generate a server entry pointing to the `niwa` binary itself (e.g., `niwa mcp-serve`).
2. Write this to `<instance-root>/.claude/.mcp.json` (or merge into `settings.json` if Claude Code supports MCP servers there — needs verification).

The `InstallWorkspaceRootSettings` function writes to `.claude/settings.json` (not `.local`), using `os.MkdirAll` to create `.claude/` first (materialize.go line 499–500, workspace_context.go line 243). A parallel `InstallMCPConfig` function following the same pattern would be appropriate. The `buildSettingsDoc` function does not need modification — a separate JSON document for `.mcp.json` is cleaner than extending `buildSettingsDoc` with MCP-specific keys.

A `niwa_check_messages` MCP tool entry would look like:

```json
{
  "mcpServers": {
    "niwa": {
      "command": "/path/to/niwa",
      "args": ["mcp-serve"],
      "env": {
        "NIWA_INSTANCE_ROOT": "/path/to/instance"
      }
    }
  }
}
```

The instance root path would be baked in at `niwa apply` time, matching how absolute paths are used in `InstallWorkspaceRootSettings` (the `UseAbsolutePaths: true` field in `BuildSettingsConfig`).

#### 5. `workspace-context.md` generation

`generateWorkspaceContext` is in `internal/workspace/workspace_context.go` line 374–411. The output is plain markdown with:

- An `# Workspace: <name>` header
- A preamble explaining the multi-repo layout
- A `## Repos` section with groups as `### <group>/` subsections and repos as bullet list items
- A `## Working in this workspace` section with behavioral instructions

The file is written to `<instance-root>/workspace-context.md` and imported into `CLAUDE.md` via `@workspace-context.md` (an @import directive recognized by Claude Code). The import is level-scoped: it resolves at the instance root but silently fails in child repos, so only the coordinator session sees the full workspace context.

A `## Channels` section appended by the `ChannelMaterializer` would follow the same markdown style:

```markdown
## Channels

This workspace has a session mesh enabled. Sessions can exchange messages
via the `niwa_check_messages` and `niwa_send_message` MCP tools.

- Session registry: `.niwa/sessions/sessions.json`
- Your inbox: `.niwa/sessions/<session-id>/inbox/`
- To register: `niwa session register --repo <repo-name>`
```

The `generateWorkspaceContext` function currently takes `cfg *config.WorkspaceConfig` and `classified []ClassifiedRepo` as arguments. Adding channel info requires reading `cfg.Channels`, which is already present as `map[string]any`. The function would need a guard (`if len(cfg.Channels) > 0`) to conditionally append the section.

The `InstallWorkspaceContext` function (workspace_context.go line 27–42) calls `generateWorkspaceContext` and writes the file — this is where the channel section would be injected.

#### 6. Niwa's existing CLI structure — how to add `niwa session`

All commands live in `internal/cli/` as separate `.go` files, each with an `init()` function that calls `rootCmd.AddCommand(...)`. The `config` group is the only existing multi-level subcommand:

- `internal/cli/config.go` — defines `configCmd` and calls `rootCmd.AddCommand(configCmd)`
- `internal/cli/config_set.go` — defines `configSetCmd` and calls `configCmd.AddCommand(configSetCmd)`
- `internal/cli/config_set.go` also defines `configSetGlobalCmd` and calls `configSetCmd.AddCommand(configSetGlobalCmd)`

The `session` subgroup would follow the exact same pattern:

- `internal/cli/session.go` — define `sessionCmd`, call `rootCmd.AddCommand(sessionCmd)`
- `internal/cli/session_register.go` — define `sessionRegisterCmd`, call `sessionCmd.AddCommand(sessionRegisterCmd)`
- `internal/cli/session_unregister.go` — define `sessionUnregisterCmd`, call `sessionCmd.AddCommand(sessionUnregisterCmd)`
- `internal/cli/session_list.go` — define `sessionListCmd`, call `sessionCmd.AddCommand(sessionListCmd)`

The `main.go` at `cmd/niwa/main.go` calls `cli.Execute()` only — no changes needed there. The `init()` registration pattern ensures new subcommands are picked up automatically.

The `PersistentPreRunE` on `rootCmd` (root.go line 31–43) handles the shell-wrapper protocol and `NO_COLOR` detection — this runs automatically for all subcommands including session commands.

Tab completion for instance/repo names uses `completeInstanceNames` and `completeRepoNames` (defined in `internal/cli/completion.go`). A `completeSessionIDs` function following the same pattern would read `sessions.json` and emit session IDs.

### Implications for Requirements

**`niwa session list`**

The PRD shall require that `niwa session list` output follows the same column-alignment convention as `niwa status` summary view: fixed-width left column for session ID (truncated to 8 chars), then space-separated columns for role/repo, PID, heartbeat age (via `formatRelativeTime`), pending message count, and live/stale/dead status. The command must be runnable from inside an instance or from the workspace root with an instance name argument, matching the `niwa status [instance]` pattern.

The PRD shall require that `niwa status` detail view includes a brief mesh summary line (e.g., `3 sessions (2 live)`) when channels are configured in `workspace.toml`, without listing individual sessions. Full session detail lives in `niwa session list` only.

**Message log**

The PRD shall require that `niwa session log [session-id]` lists messages in the session's inbox directory sorted by arrival time, showing sender, timestamp, and message type. The command shall accept a `--since <duration>` flag (e.g., `--since 1h`).

The PRD shall require that message files in inbox directories are retained until explicitly acknowledged or until TTL expiry. TTL cleanup shall run as part of `niwa apply` (idempotent pipeline step) and `niwa session unregister`. No background daemon is needed for cleanup.

The PRD shall require that a message's on-disk format be a JSON file with fields for sender session ID, sender role, timestamp, message type, and body — making `niwa session log` a simple directory scan without a separate log database.

**`niwa status` integration**

The PRD shall require that mesh state is kept in a separate `sessions.json` file alongside `instance.json`, not embedded in `InstanceState`. This preserves the separation between provisioning-time state (niwa-owned, written by `niwa apply`) and runtime state (session-owned, written by `niwa session register`).

The PRD shall require that `niwa status` reads `sessions.json` opportunistically (missing file = no sessions, not an error) and appends a one-line mesh summary to the detail view when channels are configured.

**`.mcp.json` provisioning**

The PRD shall require that a `ChannelMaterializer` (implementing the `Materializer` interface from `internal/workspace/materialize.go` line 140–144) writes `<instance-root>/.claude/.mcp.json` when `cfg.Channels` is non-empty. The file shall declare a single MCP server entry pointing to the `niwa` binary with a `mcp-serve` subcommand and an `NIWA_INSTANCE_ROOT` environment variable baked in at apply time.

The PRD shall require that the generated `.mcp.json` path is tracked in `InstanceState.ManagedFiles` so `niwa status` can detect drift and `niwa apply` can remove it when channels are disabled.

The PRD shall require that a new `niwa mcp-serve` subcommand starts a stdio MCP server exposing at minimum `niwa_check_messages` (list unread messages in caller's inbox) and `niwa_send_message` (deliver a message to another session's inbox by session ID or role). The server must be stateless enough to start fresh on each Claude tool call — no persistent socket or daemon.

**`workspace-context.md` channel section**

The PRD shall require that `generateWorkspaceContext` appends a `## Channels` section when `cfg.Channels` is non-empty. The section shall include: the sessions registry path, the MCP tool names available (`niwa_check_messages`, `niwa_send_message`), and the `niwa session register` command a session must call at startup. This injects channel awareness into the coordinator session's context without modifying CLAUDE.md directly.

**CLI structure for `niwa session`**

The PRD shall require that `niwa session` be implemented as a cobra subcommand group registered from `internal/cli/session.go`, following the exact pattern of `internal/cli/config.go`. Each subcommand (`register`, `unregister`, `list`) shall live in a separate file in `internal/cli/`.

The PRD shall require that `niwa session register` exit with the registered session ID on stdout so calling scripts can capture it (e.g., `export NIWA_SESSION_ID=$(niwa session register --repo niwa)`).

The PRD shall require that `niwa session register` accepts a `--repo <name>` flag that defaults to the repo name inferred from `DiscoverInstance(cwd)` + the cwd relative path, following the same cwd-walk pattern used by `runStatus` in `internal/cli/status.go`.

### Open Questions

1. **`.mcp.json` location**: Claude Code's `.mcp.json` is read from the project root (the directory where Claude is launched). For a coordinator session launched from the instance root, `<instance-root>/.claude/.mcp.json` would be correct. For a per-repo session launched from inside a cloned repo, it would need to be at `<repo>/.claude/.mcp.json` (or `.mcp.json` in the repo root). Does the `ChannelMaterializer` need to write MCP configs per-repo too, or only at the instance root? This determines whether `ChannelMaterializer` runs in the workspace-root path (like `InstallWorkspaceRootSettings`) or in the per-repo materializer loop (like `SettingsMaterializer`).

2. **`niwa mcp-serve` session identity**: The MCP server runs as a subprocess of Claude, so it inherits the environment. How does it know which session it belongs to? Options: (a) `NIWA_SESSION_ID` env var set by the user after `niwa session register`, or (b) the server looks up the caller's PID and matches it against `sessions.json`. Neither is fully reliable — the env var requires user action; PID matching is fragile for multi-process setups.

3. **Concurrent writes to `sessions.json`**: Multiple sessions calling `niwa session register` simultaneously would race on writes. The codebase has no file-locking primitives. This is flagged as an open question in the existing research (`wip/research/explore_cross-session-communication_r1_lead-niwa-workspace-model.md`). The PRD needs to decide: atomic rename pattern (write to temp file, rename), advisory lock (via `flock`), or a single-writer model where only `niwa apply` initializes `sessions.json` and sessions append entries via a registration queue.

4. **`buildSettingsDoc` vs. separate `.mcp.json`**: Claude Code may support MCP server declarations inside `settings.json` (under a `mcpServers` key) rather than requiring a separate `.mcp.json`. This needs verification against Claude Code docs. If `settings.json` supports it, the `buildSettingsDoc` function could be extended; otherwise a separate file and function are needed. This is a gap the code doesn't answer.

5. **`niwa session list` liveness check**: The codebase has no OS-process utilities. Checking whether a PID is alive requires `os.FindProcess` + signal 0 (Unix) — this is Go stdlib but not currently imported in any niwa package. The alternative (heartbeat file mtime) requires sessions to actively update a file; if a session crashes, the heartbeat goes stale after the TTL window. The PRD needs to specify which mechanism is normative for v1.

6. **`niwa status` session summary threshold**: Should the mesh summary line appear in `niwa status` only when channels are configured in `workspace.toml`, or whenever `sessions.json` exists? A session registry could exist even if the user removed `[channels]` from the config. The PRD should define the trigger condition.

## Summary

Niwa's existing observability pattern — compact fixed-width tables in `niwa status`, cwd-based instance discovery, the `formatRelativeTime` helper, and `cobra` subcommand groups following the `config set global` precedent — provides a complete template for `niwa session list` and `niwa session log` without new patterns. The two genuine gaps are the `.mcp.json` file (nothing writes it today; `buildSettingsDoc` only produces `settings.json`/`settings.local.json`) and concurrent write safety for `sessions.json` (no file-locking infrastructure exists). The `ChannelMaterializer` fits cleanly into the existing `Materializer` interface and pipeline in `Applier.runPipeline`, and the `## Channels` section in `workspace-context.md` can be injected by extending `generateWorkspaceContext` with a guard on `cfg.Channels`. These two integration points — the materializer pipeline and the workspace context generator — are the highest-fidelity hooks the codebase offers for channel provisioning.
