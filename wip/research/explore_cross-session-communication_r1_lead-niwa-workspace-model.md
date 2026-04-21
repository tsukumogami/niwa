# Lead: Niwa workspace model and session registry

## Findings

### What a "workspace" is in niwa's current model

A niwa workspace has a two-level structure on disk:

```
<workspace-root>/           # e.g., ~/ws/myproject/
  .niwa/
    workspace.toml          # configuration (repos, groups, env, vault, channels, ...)
    instance.json           # workspace-root-level state (overlay URL, notices)
  <instance-name>/          # e.g., myproject or myproject-2
    .niwa/
      instance.json         # per-instance InstanceState (schema v2)
    .claude/
      settings.json         # generated Claude Code settings
      hooks/                # generated hooks
    workspace-context.md    # generated @import-visible repo list
    CLAUDE.md               # generated, references workspace-context.md
    <group>/                # e.g., public/ or private/
      <repo>/               # cloned git repos
```

State is stored per-instance in `<instance-root>/.niwa/instance.json` (the `StateDir`/`StateFile` constants). The `InstanceState` struct (`internal/workspace/state.go`) holds:

- `InstanceName`, `InstanceNumber`, `ConfigName` — identity
- `Root` — absolute path to instance directory
- `Created`, `LastApplied` — timestamps
- `Repos map[string]RepoState` — clone status by repo name (URL + bool)
- `ManagedFiles []ManagedFile` — tracked generated files with content hashes
- `Shadows []Shadow`, `DisclosedNotices []string` — overlay conflicts and shown notices
- `OverlayURL`, `OverlayCommit`, `NoOverlay`, `SkipGlobal` — overlay config

There is **no field** for running processes, session IDs, PIDs, or socket paths in `InstanceState`.

### Where niwa stores workspace state

State lives in two places:
1. `<workspace-root>/.niwa/instance.json` — workspace-root-level (overlay notices)
2. `<instance-root>/.niwa/instance.json` — per-instance (repos, managed files, etc.)

A global registry at `~/.config/niwa/config.toml` tracks workspace names to their root directories and source URLs (`RegistryEntry` with `Root`, `Source`, `SourceURL`).

Both state files are plain JSON written by `SaveState()` / read by `LoadState()`. The format has a `schema_version` field (currently `2`) with a documented migration path.

### Lifecycle events where a communication channel could be provisioned

| Event | Function | Location |
|-------|----------|----------|
| `niwa init` | `runInit()` | `internal/cli/init.go` — creates `.niwa/` dir, writes initial state |
| `niwa create` | `Applier.Create()` | `internal/workspace/apply.go` — creates instance dir, runs full pipeline, writes `InstanceState` |
| `niwa apply` | `Applier.Apply()` | `internal/workspace/apply.go` — re-runs pipeline on existing instance, updates `InstanceState` |
| `niwa destroy` | `workspace.DestroyInstance()` | `internal/workspace/destroy.go` — removes instance dir |

The cleanest hook for provisioning a persistent channel is `Applier.Create()`: it already creates the instance directory, runs all setup steps, and writes final state. A socket file or named pipe path could be added to state at this point. `Applier.Apply()` would be the idempotent re-provisioning point (apply is re-entrant by design).

### The `[channels]` placeholder

`WorkspaceConfig` already has a `Channels map[string]any` field (`internal/config/config.go`, line 105) with the comment `// placeholder`. The workspace.toml scaffold template includes a commented-out `[channels]` section. This is clearly reserved for future use but has zero implementation behind it. The config test confirms the field parses but nothing reads it downstream.

### No session tracking exists

A search across all Go files for `pid`, `socket`, `unix`, `ipc`, `channel` (as a runtime primitive), and `session` shows no session-tracking code beyond Go's own channel primitives used in the clone worker pool (`internal/workspace/apply.go`). There are no PID files, no Unix socket files, no named pipes, and no process management anywhere in the codebase.

### Directory structure available to a session registry

`EnumerateInstances()` (`state.go`) scans the workspace root for immediate subdirectories containing `.niwa/instance.json`. A session registry could follow the same pattern: each running session drops a registration file at a well-known path, and discovery scans for live entries. The `.niwa/` directory under the instance root is already the right place (it's in `.gitignore` for `*.local*` patterns via `EnsureInstanceGitignore`).

## Implications

### What a session registry data structure would look like

A new `SessionEntry` in `InstanceState` (or as a separate file `sessions.json` alongside `instance.json`):

```go
type SessionEntry struct {
    SessionID   string    `json:"session_id"`   // UUID or hash
    Repo        string    `json:"repo"`          // repo name this session works in ("" = instance root)
    PID         int       `json:"pid"`           // for liveness checking
    SocketPath  string    `json:"socket_path"`   // Unix socket or named pipe
    RegisteredAt time.Time `json:"registered_at"`
    LastHeartbeat time.Time `json:"last_heartbeat,omitempty"`
}
```

A `Sessions []SessionEntry` field on `InstanceState` would mirror the pattern of `Repos` and `ManagedFiles`. Alternatively, a sibling file `<instance-root>/.niwa/sessions.json` avoids muddling provisioning-time state (written by `niwa apply`) with runtime state (written by sessions themselves). The sibling file approach has a cleaner separation of concerns: `instance.json` is niwa-owned, `sessions.json` is session-owned.

### Changes needed to the CLI

1. **`niwa session register`** — new subcommand. A session calls this at startup to write its entry into `sessions.json`. Parameters: `--repo <name>`, `--socket <path>` (or let niwa generate the socket path based on session ID). Returns the session ID and socket path on stdout.

2. **`niwa session unregister`** — called (or triggered via atexit hook) when a session exits.

3. **`niwa session list`** — shows all registered sessions in an instance, with liveness status (check PID or heartbeat age). Could be a view within `niwa status`.

4. **`niwa apply` changes** — during `Create()` and `Apply()`, provision the communication infrastructure: create the socket directory, write initial `sessions.json` (empty), and store the socket directory path in `InstanceState` or as a well-known path (`<instance-root>/.niwa/sessions/`).

5. **`niwa destroy` changes** — clean up sessions (signal or kill registered PIDs, remove socket files).

### Where to store socket files

The `.niwa/` directory is already excluded from git via `EnsureInstanceGitignore` (patterns for `*.local*`). A subdirectory `<instance-root>/.niwa/sessions/` could hold:
- `sessions.json` — the registry
- `<session-id>.sock` — the Unix socket for each live session

This is discoverable from the instance root the same way `instance.json` is, uses the existing state directory convention, and stays within the non-committed `.niwa/` control directory.

### The `[channels]` config key

The existing `Channels map[string]any` placeholder in `WorkspaceConfig` is the right place to declare the communication layer in `workspace.toml`. A workspace could opt in to a specific channel type:

```toml
[channels.mesh]
transport = "unix"
```

The `Applier` pipeline already reads this config during `runPipeline()`. Adding a `ChannelMaterializer` to the existing `Materializers` slice (alongside `HooksMaterializer`, `SettingsMaterializer`, etc.) would be the idiomatic extension point.

## Surprises

1. **`[channels]` already exists as a placeholder.** The field is in the Go struct, in the scaffold template, and in tests. This strongly signals that channel provisioning was anticipated when the config schema was designed, even though nothing is implemented. The feature has a pre-reserved namespace.

2. **`apply` is idempotent by design.** The pipeline is safe to re-run on existing instances (`Apply()` loads existing state, runs the full pipeline, then writes updated state). This means channel provisioning via a `ChannelMaterializer` would be re-entrant without special-casing — if a session socket file already exists, the materializer can leave it alone or regenerate it.

3. **No "start" lifecycle event.** Niwa has `init`, `create`, `apply`, and `destroy`, but no `start` or `open` command that represents "a user is now working in this instance." Session self-registration is therefore the only realistic discovery mechanism — there is no existing hook where niwa could inject itself as a session broker.

4. **The instance root is not a git repo.** `EnsureInstanceGitignore` handles this by writing a `.gitignore` with `*.local*` patterns. The `.niwa/` directory inside it is the right place for runtime state since it already acts as a control directory.

## Open Questions

1. **Who owns `sessions.json` writes?** If sessions write their own entries, concurrent writes require file locking or an atomic swap. Alternatively, a small long-running daemon (started by `niwa apply`) could own the registry and expose a local socket for registration requests — but that introduces a daemon model niwa currently doesn't have.

2. **How does a session know its workspace instance?** A session started from inside an instance directory can call `DiscoverInstance(cwd)` (already implemented), but a session launched from a tool that opens a new terminal might not have a reliable cwd. The registration UX needs design.

3. **What is the session ID?** Should it match the Claude session/conversation ID (if that's accessible via the SDK or environment), or should niwa generate its own UUID? If it maps to a Claude session, the mesh can route by conversation identity. If niwa generates it, sessions need some other way to identify themselves to peers.

4. **Heartbeat vs. PID liveness.** The lead on IPC mechanisms will inform whether a heartbeat or PID-existence check is simpler. The registry needs a way to prune stale entries, especially after a crash.

5. **What does `workspace.toml` `[channels]` configuration actually look like?** The placeholder accepts `map[string]any`, meaning the schema is completely open. A concrete design needs to decide: is the channel type declared per-workspace, or is it always Unix sockets on the same machine for v1?

6. **Does `niwa destroy` need to be session-aware?** If sessions are running, destroying the instance should either wait or force-terminate them. The current `destroy` only checks for git uncommitted changes.

## Summary

Niwa's workspace model already has a well-defined state directory (`.niwa/instance.json` per instance) and a provisioning pipeline (`Applier.Create`/`Apply`) where communication infrastructure could be injected via a new `ChannelMaterializer` alongside existing materializers. The config schema even has a reserved `[channels]` placeholder in `WorkspaceConfig` that anticipates this feature. What's entirely absent is any notion of running sessions: no PID tracking, no socket files, no session registry, and no lifecycle event between "instance created" and "instance destroyed" — sessions would need to self-register at startup, making `niwa session register` a new subcommand and `<instance-root>/.niwa/sessions/` the natural home for the registry and socket files. The biggest open question is concurrency ownership: since niwa has no daemon today, concurrent session writes to `sessions.json` require either file locking or a registry broker process.
