---
status: Proposed
upstream: docs/prds/PRD-cross-session-communication.md
problem: |
  Niwa workspaces run multiple Claude sessions simultaneously — one per repo, one at
  the root — but those sessions have no way to exchange messages without the user
  acting as a relay. The core technical challenge is waking a session that is not
  actively making tool calls: polling is useless for idle sessions, Claude Code's
  Channels push protocol is not configurable via file-based settings, and there is
  no hook that fires on arbitrary filesystem events. The solution requires a daemon
  that watches session inboxes via fsnotify and resumes idle sessions using
  `claude --resume`, combined with a blocking MCP tool (`niwa_ask`) that holds a
  goroutine open until the target session responds via `niwa_send_message` into the
  caller's inbox. A second problem is activation friction: the initial design required
  users to enumerate all repo roles in workspace.toml before any infrastructure
  materialized, even though the role mapping is obvious from the workspace topology.
decision: |
  Niwa provisions a workspace-scoped session mesh at `niwa apply` time via a new
  ChannelMaterializer. The materializer writes a file-based inbox tree under
  `.niwa/sessions/`, an MCP server entry in `.claude/.mcp.json`, SessionStart and
  UserPromptSubmit hook scripts, and a `## Channels` section in workspace-context.md.
  A persistent daemon (`niwa mesh watch`) watches all session inboxes via fsnotify
  and resumes idle sessions via `claude --resume <claude-session-id>` when messages
  arrive for dead PIDs. The MCP server exposes four tools: `niwa_check_messages` and
  `niwa_send_message` (stateless), and `niwa_ask` and `niwa_wait` (blocking goroutines
  that hold the tool call open until inbox events arrive). Sessions always respond via
  `niwa_send_message` — never via stdout — so the response path is identical whether
  the session was woken by the daemon or by Claude Code's future Channels push protocol.
  Roles are auto-derived from workspace topology (coordinator = instance root, role =
  repo basename) so a bare `[channels.mesh]` section is sufficient to provision the
  full mesh; explicit `[channels.mesh.roles]` overrides are optional. Users may also
  activate channels per-invocation via `--channels`/`--no-channels` flags, with the
  `NIWA_CHANNELS` environment variable as a persistent global default.
rationale: |
  The response-via-tool-call constraint is the key forward-compatibility invariant:
  when Channels can wake sessions natively, the daemon's `claude --resume` step is
  removed but the response detection path (`niwa_ask` watching the inbox) is unchanged.
  A file-based inbox was chosen over a Unix socket broker because it is crash-safe,
  requires no broker process to be live for message durability, and can be inspected
  with standard tools. The daemon is stateless (no in-memory message state) so it
  can be restarted without data loss. `niwa_ask` blocking at the MCP tool layer means
  sessions never need polling instructions — they call `niwa_ask` when they need input
  and block naturally, matching how any other tool call works. Role auto-derivation
  removes config boilerplate: the instance root is always the coordinator by convention,
  and repos already have unique, descriptive names by design. The hybrid activation
  model (config + flag + env var) separates the "team-shared" concern (workspace.toml)
  from the "personal preference" concern (flag default via env var or personal overlay).
---

# DESIGN: Cross-Session Communication

## Status

Proposed

## Context and Problem Statement

When working across multiple repos in a niwa workspace, a common pattern is one Claude
session acting as coordinator (at the workspace root) and per-repo sessions acting as
workers. Currently any exchange — clarifying questions, task delegation, code review
feedback — requires the user to copy-paste messages between terminal windows. The user
is the relay. Parallel multi-session workflows become impractical beyond two sessions.

The technical problem has two distinct halves:

**Transport and routing.** Sessions need to address each other by role (`coordinator`,
`koto`, `shirabe`) without knowing process IDs, socket paths, or session file locations.
The transport must survive session crashes (messages must not live only in memory) and
must work without a network connection.

**Idle session wakeup.** A session at the REPL making zero tool calls will never poll
its inbox. Three mechanisms were evaluated: Claude Code's `notifications/claude/channel`
push protocol requires `claude.ai` OAuth and a CLI flag that cannot be set in any
config file, and has a confirmed bug where idle REPLs display the notification but do
not process it; Claude Code hooks (`SessionStart`, `UserPromptSubmit`) fire only at
session open or on user input, not on arbitrary inbox events; tmux keypress injection
requires sessions to run inside tmux. The only mechanism that works for any idle Claude
session, regardless of how it was opened, is `claude --resume <session-id>` — which
resumes the session with a new user prompt, fires the `SessionStart` hook, and delivers
pending messages via `initialUserMessage`.

The design must connect these two halves: file-based delivery (transport) + daemon that
calls `claude --resume` (wakeup) + blocking MCP tools (session-side API that hides the
complexity) + forward-compatible response routing (response always via `niwa_send_message`
into the inbox, never via stdout, so the Channels upgrade is transparent).

The implementation touches: `internal/workspace/apply.go` (new materializer),
`internal/workspace/workspace_context.go` (new Channels section),
`internal/mcp/server.go` (new blocking tools), `internal/cli/session_register.go`
(Claude session ID registration), new `internal/cli/mesh_watch.go` (daemon), and
`internal/config/config.go` (concrete `[channels.mesh]` config struct).

## Decision Drivers

- Sessions must receive messages and wake up without polling and without user intervention
- Responses travel via `niwa_send_message` tool calls, never stdout — this is the forward-compat invariant for the Channels upgrade path
- Pure Go, no external runtime dependencies beyond stdlib and fsnotify (already in go.mod)
- Crash-safe: messages survive daemon and session crashes (file-based inbox)
- Stateless daemon: restartable without data loss or re-registration
- Claude session ID must be discoverable from within a Claude Code session's shell environment at registration time
- ChannelMaterializer integrates cleanly with the existing `Applier.runPipeline` step 6.5 without breaking existing materializers
- The daemon lifecycle must be manageable as part of instance lifecycle (`niwa apply` starts, `niwa destroy` stops)
- `niwa_ask` must not leak goroutines on timeout
- Role assignment must not require users to enumerate what can be derived from workspace topology
- Channel activation must be expressible both declaratively (workspace config) and imperatively (CLI flag) without the two mechanisms conflicting
- User-level channel defaults must not require modifying shared workspace configuration

## Considered Options

### Decision 1: Daemon Lifecycle Management

`niwa mesh watch` is a persistent background process that watches all session inbox
directories under `<instance-root>/.niwa/sessions/` via fsnotify and resumes idle
Claude sessions via `claude --resume <claude-session-id>` when messages arrive for
sessions whose PID is dead. The question is how this daemon should be started,
supervised, and stopped as part of the workspace lifecycle.

The daemon must be running before the first Claude session opens so it is ready to
resume sessions, and must stop cleanly when the instance is destroyed. It must survive
its own crash restartably without message loss, because the inbox is file-based and
stateless. The user must not need to manually run `niwa mesh watch` — zero-friction
provisioning requires automatic startup.

Key assumptions: `niwa apply` is the canonical provisioning step; the daemon is fully
stateless (all durable state is the inbox filesystem); PID tracking must use the
start-time verification pattern from `internal/mcp/liveness.go` to prevent false
positives from PID recycling; the daemon is instance-scoped (one per instance, not one
per machine or workspace root).

#### Chosen: niwa apply spawns the daemon directly via exec

At the end of `runPipeline`, after all materializers write their files and after
`SaveState` writes the instance state, `niwa apply` checks whether a live daemon is
already running for this instance by reading `<instance-root>/.niwa/daemon.pid` and
calling `IsPIDAlive`. If not, it spawns `niwa mesh watch --instance-root=<instance-root>`
via `exec.Command`, configured so the child inherits no open file descriptors, runs in
its own process group (`Setsid: true`) so it is not killed when the user's shell session
ends, and writes its PID and start time to `daemon.pid` atomically (write to
`daemon.pid.tmp`, then rename) before entering the watch loop.

The PID file stores two fields: the integer PID and the jiffies-since-boot start time
from `/proc/<pid>/stat`. This allows the existing `IsPIDAlive` function from
`internal/mcp/liveness.go` to be called directly for both the idempotency check
(`niwa apply`) and the stop check (`niwa destroy`). The spawn step is gated on
`cfg.Channels` being non-empty, so workspaces without channel config are unaffected.

`niwa destroy` is extended to stop the daemon before removing the instance directory:
read `daemon.pid`, call `IsPIDAlive`, send `SIGTERM`, wait up to 5 seconds, send
`SIGKILL` if needed, then remove `daemon.pid` and proceed with `DestroyInstance`. This
prevents the daemon from watching a directory being deleted.

If the daemon crashes between apply runs, `daemon.pid` remains on disk but `IsPIDAlive`
returns false. The next `niwa apply` detects the stale PID and spawns a fresh daemon.
Messages that arrived during downtime sit in the inbox directories and are delivered
when the daemon restarts or when sessions next open manually.

**Machine restart**: `niwa mesh watch` is a regular user process, not a system service.
After a machine restart or logout, all instance daemons are gone. `niwa apply` is the
documented recovery path — run it on an individual instance, or from the workspace root
to restart daemons for all instances at once (each instance's `IsPIDAlive` check will
return false and trigger a fresh spawn).

**Multi-instance `niwa apply` from workspace root**: when `niwa apply` is run from the
workspace root (applying to all instances), the daemon spawn step runs once per instance
in sequence. Since the `IsPIDAlive` check is per-instance and the PID file is scoped to
each instance root, concurrent instances do not interfere with each other.

**Handling `rm -rf`**: if a user removes the instance directory without calling `niwa
destroy`, the daemon's fsnotify watcher will receive errors or `REMOVE` events for its
watched path and must detect this condition and exit cleanly rather than looping on
errors. The daemon startup loop should check whether `<instance-root>/.niwa/sessions/`
still exists on each iteration; if missing, log a warning and exit. This prevents a
leaked daemon process from consuming resources indefinitely after an unclean removal.
Users should be directed to use `niwa destroy` rather than `rm -rf` for instances with
mesh configured.

#### Alternatives Considered

**Option A — startup script written by `niwa apply`**: `niwa apply` writes a script the
user or shell init hook runs manually.
Rejected because it violates the zero-friction requirement; shell init hooks are
unreliable (fire only in interactive shells) and the lifecycle is not tied to
`niwa apply`/`niwa destroy` without extra instrumentation.

**Option C — SessionStart hook starts the daemon if not already running**: Every
session's hook checks for a live daemon and spawns it if absent.
Rejected because the daemon must exist _before_ the first session opens, not when it
opens; concurrent session starts race on spawn; running a daemon spawn inside a hook
adds latency and coupling; and the mechanism required (PID file + atomic spawn) is
identical to the chosen option — there is no simplification, only worse causal ordering.

**Option D — daemon embedded in `niwa mcp-serve` as a side effect**: The first MCP
server instance also watches all session inboxes; subsequent instances skip this.
Rejected because `niwa mcp-serve` is session-scoped and exits when its session closes,
so the embedded daemon has the same lifetime as one session rather than the workspace.
When the first session closes, the daemon dies. The coordination logic requires the
same PID-file mechanism as the chosen option but with a different (session-scoped)
ownership boundary.

**Option E — systemd/launchd unit written by `niwa apply`**: `niwa apply` writes an OS
service unit and enables it.
Rejected because it violates the "no external deps beyond stdlib" constraint, requires
user systemd or launchd to be available (not guaranteed in containers or CI), needs
different code paths for Linux vs macOS, and adds complexity that a simple SIGTERM
cannot improve on.

---

### Decision 2: Claude Session ID Discovery

`niwa session register` is called by the SessionStart hook every time a Claude Code
session opens or resumes. It writes a `SessionEntry` to `sessions.json` including the
niwa session UUID, role, PID, process start time, and inbox path. The daemon needs
`claude --resume <claude-session-id>` to wake idle sessions — so `niwa session register`
must also capture the Claude Code session ID and store it in `SessionEntry`.

The key constraint is that `niwa session register` runs as a subprocess of the
SessionStart hook script, which is itself spawned by Claude Code. It cannot call Claude
Code internals. It knows `NIWA_INSTANCE_ROOT`, the current working directory, and
whatever Claude Code injects into the hook subprocess environment.

Key assumptions: Claude Code maintains a live session registry at `~/.claude/sessions/`
with per-PID JSON files containing `sessionId`, `cwd`, and `startedAt`; the hook
subprocess chain is shallow and deterministic (niwa → hook shell → Claude process),
making a two-level PPID walk reliable on POSIX; `CLAUDE_SESSION_ID` may or may not be
exported to hook subprocesses (unconfirmed); without a session ID, the daemon cannot
resume idle sessions and must fall back to manual-open delivery.

#### Chosen: Try CLAUDE_SESSION_ID, then ~/.claude/sessions/\<ppid\>.json, then filesystem scan

Read the Claude session ID in priority order:

1. **`CLAUDE_SESSION_ID` environment variable**: If Claude Code exports this to hook
   subprocesses, it is authoritative and zero-cost. Check first with no I/O.

2. **`~/.claude/sessions/<ppid>.json`**: Claude Code writes a JSON file per running
   process containing `sessionId`, `cwd`, and `startedAt`. `niwa session register`
   calls `os.Getppid()` to get the hook script's PID, then reads the Claude process
   PID (the parent of the hook shell) and reads its sessions file. This is fast (one
   file read), requires no encoding math, and directly correlates the Claude PID to its
   session ID. The `cwd` field is cross-checked against the current working directory
   to detect stale entries from recycled PIDs.

3. **Filesystem scan of `~/.claude/projects/<base64url-cwd>/`**: Encode the CWD using
   base64url (standard encoding, no padding). List `*.jsonl` files sorted by mtime
   descending and take the most recently modified. This is a heuristic fallback for
   when the live registry file is absent, but the race window is narrow since
   `SessionStart` fires at session open when the JSONL file was just created.

4. **Leave `claude_session_id` empty and log a warning**: If all three paths fail, the
   field is omitted. The daemon skips `claude --resume` for this session and relies on
   SessionStart hook delivery when the session next opens manually — the graceful
   degradation path specified in the PRD.

The `SessionEntry` struct gains `ClaudeSessionID string \`json:"claude_session_id,omitempty"\``.

#### Alternatives Considered

**Option A alone — rely solely on `CLAUDE_SESSION_ID`**: Read only the env var.
Rejected because no documentation or community evidence confirms Claude Code exports
this variable to hook subprocesses. Shipping code that silently stores an empty session
ID in production would disable daemon wakeup without any observable warning.

**Option C — daemon uses `--continue` or hook-only delivery; no session ID needed**:
Instead of `claude --resume <session-id>`, use `claude --continue` (most recent session
for CWD) or rely entirely on SessionStart hook delivery.
Rejected because `claude --continue` picks the most recent session for the CWD, which
is wrong when multiple sessions exist for the same directory at different times.
Hook-only delivery means idle sessions are never woken autonomously — the daemon becomes
a message monitor with no wakeup capability, removing its core value.

**Option D — sentinel file written by Claude Code**: Configure Claude Code to write the
session ID to a known path at session start, which the hook reads.
Rejected because it requires users to modify their Claude Code configuration outside of
niwa's provisioning scope. Niwa cannot require external Claude Code config to make a
niwa feature work.

**Option E — skip session ID for v1**: Accept that the daemon cannot resume sessions;
deliver only via SessionStart hook.
Rejected as the primary path because the daemon's core function is autonomous wakeup.
Without session ID, idle sessions are never woken by message arrival. This is retained
as graceful degradation (when all three discovery methods fail), not the primary path.

---

### Decision 3: niwa_ask Blocking Mechanism

`niwa_ask` is a blocking MCP tool: it sends a `question.ask` message to a target
session's inbox, then holds the tool call open until the target replies with a
`question.answer` bearing a matching `reply_to` field. The MCP server today processes
tool calls synchronously — `dispatch` blocks until the response is written to stdout.
`niwa_ask` must block inside `dispatch` without blocking the whole server loop, while
guaranteeing timeout enforcement and goroutine cleanup.

The existing server already has a fsnotify watcher goroutine (`watchInbox`) that fires
when files appear in the calling session's inbox. The design question is how to wire
the dispatch goroutine and the watcher goroutine together so the dispatch goroutine
sleeps on the reply without racing or leaking.

Key assumptions: Claude's session is blocked waiting for the tool result while
`niwa_ask` is in flight and will not send additional requests during this window, so
blocking the scan loop is safe; the inbox watcher goroutine is already running and the
reply-watch path must reuse it to avoid double-watching the same directory; goroutine
cleanup on timeout is mandatory (a late watcher send to a closed channel causes a panic
or silent interference with the next call).

#### Chosen: Block in dispatch goroutine; background watcher signals via buffered channel

`niwa_ask` blocks inside `dispatch`. Before blocking, it registers a reply channel in a
server-level map keyed by the expected `reply_to` message ID. The existing `watchInbox`
goroutine is extended to check this map on every new inbox file it observes. When the
file's `reply_to` field matches a registered waiter, the watcher sends the parsed
message on the channel and moves the file to `read/` atomically to prevent
`niwa_check_messages` from double-reading it. The dispatch goroutine selects on the
channel and a timeout timer, then removes its entry from the map via `defer cancel()`
before returning.

The channel is buffered with capacity 1. This lets the watcher goroutine send without
blocking even if the dispatch goroutine has already timed out and `cancel()` has run —
the send succeeds into the buffer and nobody reads it, which is safe. Without the
buffer, a watcher trying to send after timeout would block the watcher goroutine
indefinitely.

Shared state additions to `Server`:

```go
waitersMu sync.Mutex
waiters   map[string]chan toolResult  // keyed by expected reply_to message ID
```

`registerWaiter(msgID)` returns the buffered channel plus a cancel function that deletes
the map entry. `handleAsk` sends the question, registers the waiter, then selects on
`replyCh` and `time.After(timeout)`. The `defer cancel()` guarantees map cleanup on
both exit paths.

`niwa_wait` (collect N messages of given types) uses a parallel `typeWaiters` map with
per-waiter filter criteria (type set, sender set, count threshold, accumulation buffer).
The watcher checks `typeWaiters` after the `reply_to` check, appending matching messages
and firing the channel when the buffer reaches the requested count.

The polling fallback path (`watchInboxPolling`) calls the same `notifyNewFile` function,
so the waiter check works equally in environments where fsnotify is unavailable.

#### Alternatives Considered

**Option B — new goroutine per niwa_ask; non-blocking dispatch; response written out-of-band**:
The dispatch function returns immediately; a spawned goroutine later writes the delayed
JSON-RPC response via a shared mutex-protected encoder.
Rejected because writing out-of-order relative to other responses may confuse MCP
clients (Claude Code's handling of out-of-order responses is unverified and difficult
to test), and because cleanup requires a context passed to every spawned goroutine —
complexity with no benefit since there is no concurrent traffic during a blocking call.

**Option C — poll inbox directory with ticker**: `niwa_ask` blocks using a 500ms ticker
to poll for the reply file.
Rejected because 500ms per tick adds latency in multi-exchange conversations, and the
existing watcher fires within ~10ms of file creation. Polling is the anti-pattern the
watcher was built to replace.

**Option D — named pipe or Unix socket for the reply signal**: A per-session named pipe
signals reply arrival; `niwa_ask` selects on the pipe read.
Rejected because it adds IPC infrastructure not already present and solves a problem
already solved by the in-process channel approach. A named pipe between the daemon and
the MCP server also re-introduces a single point of failure that the file-based inbox
design specifically avoids.

---

### Decision 4: ChannelMaterializer Integration Point

`runPipeline` in `internal/workspace/apply.go` runs ordered steps to provision a
workspace instance. Step 4.5 writes `workspace-context.md` via
`InstallWorkspaceContext()`. Step 6.5 runs per-repo materializers
(`HooksMaterializer` → `SettingsMaterializer` → `EnvMaterializer` →
`FilesMaterializer`). Channel infrastructure must write both workspace-wide artifacts
(sessions directory, `sessions.json`, `.mcp.json`) and per-repo artifacts (hook scripts),
and must also append a `## Channels` section to `workspace-context.md` — a file already
written before any materializer runs.

Key assumptions: the `## Channels` section does not vary by repo — role is resolved at
runtime by `niwa session register` from `NIWA_SESSION_ROLE` or the `[channels.mesh.roles]`
table, not baked into the file; workspace-wide infrastructure must be written exactly once
per apply, not once per repo; `MaterializeContext` does not expose `instanceRoot`
(deriving it from `RepoDir` via path depth is fragile); hook scripts for SessionStart and
UserPromptSubmit can flow through the existing `HooksMaterializer` if declared in
`cfg.Claude.Hooks`.

#### Chosen: Separate workspace-level step in runPipeline at step 4.75

A new `InstallChannelInfrastructure(ctx, cfg, instanceRoot, writtenFiles)` function is
called at step 4.75 in `runPipeline`, between step 4.5 (`InstallWorkspaceContext`) and
step 5 (group CLAUDE.md installation). This follows the naming and call-site pattern of
`InstallWorkspaceContext`, `InstallOverlayClaudeContent`, and
`InstallWorkspaceRootSettings` — free functions called sequentially from `runPipeline`
with `instanceRoot` as a named argument.

The function:

1. Checks whether `cfg.Channels` is non-empty; returns immediately for workspaces
   without channel config.
2. Creates `<instance-root>/.niwa/sessions/` and `artifacts/` subdirectory via
   `os.MkdirAll` (idempotent).
3. Creates `sessions.json` only if absent, so re-apply does not overwrite a populated
   registry.
4. Writes `.claude/.mcp.json` with the `niwa mcp-serve` entry and
   `NIWA_INSTANCE_ROOT` baked in.
5. Appends the `## Channels` section to `workspace-context.md` using an idempotent
   check-then-append helper (same pattern as `ensureImportInCLAUDE`). The section
   contains the sessions registry path, the four tool names, the registration command,
   and behavioral instructions per PRD R5 — without a per-repo role.
6. Returns written file paths for `writtenFiles` tracking.

Hook scripts for SessionStart and UserPromptSubmit are declared in (or synthesized
into) `cfg.Claude.Hooks` when `[channels.mesh]` is present, so the existing
`HooksMaterializer` at step 6.5 writes them per-repo without any changes to the hook
pipeline.

#### Alternatives Considered

**Option A — ChannelMaterializer as a per-repo materializer at step 6.5**:
A `ChannelMaterializer` struct implementing the `Materializer` interface would fit
structurally into the existing materializer slice.
Rejected because `MaterializeContext` does not expose `instanceRoot`, and deriving it
from `RepoDir` via path-depth arithmetic bakes in a structural assumption not
contractually guaranteed. Workspace-wide writes (sessions dir, `.mcp.json`) would also
need a "first repo only" guard, introducing state into a stateless per-repo call. The
`workspace-context.md` append at step 6.5 writes to a file two levels above `repoDir`,
which is architecturally inconsistent with how materializers are expected to operate.

**Option C — split into ChannelInfraMaterializer + ChannelHooksMaterializer**:
Two types, one at step 4.75 and one at step 6.5.
Rejected because `ChannelHooksMaterializer` at step 6.5 would duplicate what
`HooksMaterializer` already does; hook scripts for channel events are standard hook
entries and should travel through the standard hook pipeline. `ChannelInfraMaterializer`
at step 4.75 is effectively the chosen option wrapped in an interface that adds no value
for a function called once.

**Option D — extend workspace_context.go to include the channels section at step 4.5**:
Modify `generateWorkspaceContext` to write `## Channels` during the initial write.
Rejected because it conflates workspace topology context (repos, groups) with channel
behavioral instructions, and all other channel infrastructure (`.mcp.json`, sessions
directory) still needs a separate step. Co-locating the workspace-context.md write does
not justify splitting channel provisioning across two pipeline positions.

---

### Decision 5: Dynamic Role Derivation

The initial implementation requires an explicit `[channels.mesh.roles]` map in
workspace.toml to assign each repo a role name. This map is documentation-only at
runtime: it renders into `workspace-context.md` to tell sessions their role, but no
routing table reads from it at message-delivery time. Sessions discover their own role
via `NIWA_SESSION_ROLE` at registration time. The provisioning gate uses `IsEmpty()`,
which returns true when the roles map has zero entries — meaning `[channels.mesh]`
with an empty (or absent) roles map provisions nothing.

The consequence is that users must enumerate all repos with explicit role names before
any infrastructure materializes, even though the mapping is obvious: the instance root
session is always the coordinator, and each repo session's natural role is the repo
name. The roles map ends up restating what the workspace topology already expresses.

Key constraints: the roles map was never used for routing; role derivation from CWD
is already the implicit fallback in `niwa session register`; repo names are unique and
descriptive by design in well-structured workspaces.

#### Chosen: Auto-derive roles from workspace topology; roles map becomes optional override

The coordinator convention is fixed by topology: the session running at the instance
root is always the coordinator. Per-repo session roles default to the repo directory
basename — the key used in `[[sources]]` or `[[repos]]` workspace config entries. No
configuration is required for this mapping.

The `[channels.mesh.roles]` map becomes optional. When present, it provides name
overrides for specific repos (e.g., mapping repo `codespar-enterprise` to role
`enterprise` for a shorter token). When absent or incomplete, the fallback is the
repo basename. This preserves backwards compatibility: existing configs with explicit
role maps continue to work.

Two things change:

**Provisioning gate**: `IsEmpty()` is replaced by an `IsEnabled()` method that returns
true whenever the `[channels.mesh]` TOML section is present in the config, regardless
of role map content. A bare `[channels.mesh]` section now provisions the full channel
infrastructure.

**Role resolution in `niwa session register`**: The priority chain becomes:
1. `--role` flag (explicit invocation override)
2. `NIWA_SESSION_ROLE` environment variable (hook script injects this per-repo)
3. `[channels.mesh.roles]` map lookup for the current repo (explicit name override)
4. Repo directory basename derived from `pwd` relative to `NIWA_INSTANCE_ROOT`

The `## Channels` section in `workspace-context.md` describes roles as auto-derived
rather than hardcoding a list, since the list is now a runtime property.

#### Alternatives Considered

**Option A — retain explicit roles map as required**: Keep `IsEmpty()` behavior;
channels require explicit role declarations.
Rejected because the roles map states what the workspace topology already expresses.
Any workspace with N repos has an obvious N+1 role assignment; requiring users to type
it out adds friction with no semantic value over the auto-derived default.

**Option B — auto-derive only, no override**: Remove `[channels.mesh.roles]` entirely;
roles are always derived from topology.
Rejected because some workspaces have repos with generic names (`core`, `api`, `web`)
where the repo name is a poor role identifier in multi-session instructions. The
override mechanism has minimal implementation cost and avoids a later breaking change
when users need it.

---

### Decision 6: Channels Activation Model

With Decision 5's auto-derivation, a bare `[channels.mesh]` section in workspace.toml
now activates channels. But this is still a config-only trigger: it is committed to
the shared workspace config and applies equally to all contributors. There is no way to
enable channels for an existing workspace without modifying the shared config, and no
way to disable channels for a specific instance when the shared config enables them.

Two use cases are unserved: (a) a user who wants to try channels on their own machine
without touching the team config; (b) a user who wants to create a lightweight instance
from a workspace where channels are normally enabled. The question is whether CLI flags
should complement the config trigger, and if so, how the two interact.

#### Chosen: Hybrid — `[channels.mesh]` config section + `--channels`/`--no-channels` flags

The config section is the declarative "always on for this workspace" path — appropriate
for team workspaces where channels are a shared expectation. The flags are the
per-invocation path:

- `--channels` on `niwa create` or `niwa apply` enables channel provisioning even when
  `[channels.mesh]` is absent from workspace.toml. The auto-derivation logic (Decision
  5) determines roles when no explicit config section exists.
- `--no-channels` disables provisioning even when `[channels.mesh]` is present. This
  is the escape hatch for lightweight instances from channel-enabled workspaces.

Priority order (highest to lowest): `--no-channels` explicit flag > `--channels`
explicit flag > `[channels.mesh]` config section > user-level default (Decision 7).

The flag result is merged into the runtime config before the provisioning step runs,
so `InstallChannelInfrastructure` remains oblivious to whether channels were activated
via config or flag. When `--channels` is passed without a config section, the runtime
behaves as if a bare `[channels.mesh]` were present.

#### Alternatives Considered

**Option A — config-only (current)**: `[channels.mesh]` is the only activation path.
Rejected because it requires shared-config edits for personal preferences and provides
no escape hatch when the config enables channels but the user wants a lightweight instance.

**Option B — flag-only**: Remove config-based activation; `--channels` required on
every invocation.
Rejected because team workspaces where channels are always desired would require every
contributor to pass `--channels` on every `niwa create` and `niwa apply` with no
persistent expression of the workspace's intent in the committed config.

---

### Decision 7: User-Level Channels Default

With Decision 6's hybrid activation model, a user who always wants channels enabled
needs a mechanism to express this preference without modifying shared workspace configs
or passing `--channels` on every command. Two scopes need independent solutions:

**Workspace-scoped**: the user wants channels on for a specific workspace on their
machine, without editing the shared workspace.toml.

**Global**: the user wants channels on for every workspace they ever create or apply,
as a persistent personal preference.

#### Chosen: Personal overlay for workspace-scoped; NIWA_CHANNELS env var for global

**Workspace-scoped**: the personal overlay (already supported by `niwa init --overlay`)
can include a `[channels.mesh]` section. When the personal overlay is loaded, it merges
with the workspace config before the provisioning step — channels are enabled for all
instances the user creates from this workspace without committing to the shared config.
No new mechanism needed; this is a direct consequence of Decision 6's hybrid model and
the existing overlay merge.

**Global**: the `NIWA_CHANNELS=1` environment variable acts as a persistent global
default. If set in the user's shell profile (`.bashrc`, `.zshrc`), it is equivalent to
passing `--channels` on every `niwa create` and `niwa apply`. `NIWA_CHANNELS=0` disables
channels globally. The priority order from Decision 6 applies: explicit flag beats env
var, env var beats absence (but not a config section, which is a declarative workspace
intent). The env var is read at CLI argument parsing time and applied as the pre-flag
default for `--channels`. Values other than `"1"` and `"0"` are ignored (treated as
unset) with a warning logged to stderr.

This is consistent with niwa's existing env var pattern (`NIWA_INSTANCE_ROOT`,
`NIWA_SESSION_ROLE`) and requires no new file format or config schema.

#### Alternatives Considered

**Option B — global config file `~/.config/niwa/config.toml`**: A structured global
preferences file with `[channels] default = true`.
Rejected as over-engineering for a single boolean preference. A new config format
requires a parser, validation, and documentation. An env var provides identical
semantics with one `os.Getenv` call. If global config requirements grow beyond one
flag, this option is the natural next step.

**Option C — personal overlay only**: Require the personal overlay approach for both
workspace-scoped and global defaults; no separate global mechanism.
Rejected because the personal overlay is per-workspace — a user with many workspaces
would need to add `[channels.mesh]` to each overlay. The env var solves the global case
with zero per-workspace configuration.

## Decision Outcome

**Chosen: 1B + 2(A+B+fallback) + 3A + 4B + 5C + 6C + 7(C+A)**

### Summary

Niwa provisions a workspace session mesh at `niwa apply` time through a new
`InstallChannelInfrastructure` function inserted at step 4.75 of `runPipeline`. The
function creates `.niwa/sessions/` (the inbox tree), initializes `sessions.json` if
absent, writes `.claude/.mcp.json` registering the `niwa mcp-serve` MCP server, and
appends a `## Channels` section to `workspace-context.md` that tells each session
which tools are available and how to register. Hook scripts for SessionStart and
UserPromptSubmit are injected into `cfg.Claude.Hooks` from the `[channels.mesh]` config
so the existing `HooksMaterializer` writes them per-repo without changes. After all
materializer steps complete and `SaveState` writes the instance state, `niwa apply`
spawns `niwa mesh watch` as a detached child process (via `Setsid: true`) gated behind
an `IsPIDAlive` check so repeated applies are idempotent. `niwa destroy` terminates the
daemon with SIGTERM (5-second grace period, then SIGKILL) before removing the instance
directory.

Channels are activated when any of the following is true: `[channels.mesh]` is present
in workspace.toml (or the personal overlay), the `--channels` flag is passed to `niwa
create` or `niwa apply`, or the `NIWA_CHANNELS=1` environment variable is set. The
`--no-channels` flag disables provisioning even when config enables it. This priority
order (explicit flag > config section > env var) is enforced at the start of
`runPipeline` before any provisioning step runs. When channels are activated without an
explicit `[channels.mesh]` section, the runtime synthesizes a minimal config equivalent
to a bare section with no role overrides.

Session roles are auto-derived from workspace topology: the session running at the
instance root is always registered as `coordinator`; per-repo sessions default to the
repo directory basename as their role. When `niwa session register` is called, it
resolves the role via priority chain: (1) `--role` flag, (2) `NIWA_SESSION_ROLE` env
var injected by the hook script, (3) explicit entry in `[channels.mesh.roles]` for this
repo, (4) basename of `pwd` relative to `NIWA_INSTANCE_ROOT`. The roles map is now
purely an override mechanism — workspaces with descriptive repo names need no role
config at all.

When a Claude session opens, the SessionStart hook runs `niwa session register`, which
records the session's role, PID, start time, inbox path, and Claude session ID in
`sessions.json`. The session ID is discovered by trying `CLAUDE_SESSION_ID` first, then
reading `~/.claude/sessions/<ppid>.json` (a two-level PPID walk: niwa → hook shell →
Claude process), then falling back to a mtime-sorted scan of
`~/.claude/projects/<base64url-cwd>/`. If all three fail, the field is left empty and
a warning is logged; the session can still receive messages via SessionStart delivery
when opened manually, but the daemon cannot resume it autonomously.

When session A calls `niwa_ask(to="coordinator", ...)`, the MCP server writes a
`question.ask` message file to the coordinator's inbox directory, registers a buffered
Go channel keyed by the sent message ID in `waiters`, then blocks in `select` on that
channel and `time.After(10 * time.Minute)`. The daemon's fsnotify watcher detects the
new inbox file; if the coordinator's PID is dead (idle), the daemon calls
`claude --resume <coordinator-session-id>`. The SessionStart hook fires for the resumed
session, `niwa session register` updates `sessions.json` with the new PID, and the
pending `question.ask` is delivered as `initialUserMessage`. The coordinator calls
`niwa_check_messages`, composes an answer, and calls `niwa_send_message` to write a
`question.answer` file bearing `reply_to: <msg-id>` to session A's inbox. The
`watchInbox` goroutine in session A's MCP server detects the new file, finds the
matching waiter, moves the file to `read/` atomically (preventing double-delivery), and
sends the result on the buffered channel. Session A's `handleAsk` returns the answer as
the tool result. `defer cancel()` always removes the waiter map entry, whether the
select exits via reply or timeout, and the capacity-1 buffer ensures a late watcher send
after timeout does not block.

If the coordinator session's PID is alive (busy), messages queue in the inbox and the
daemon takes no action. The coordinator will eventually call `niwa_check_messages` or
complete its current task and become idle. This is the accepted busy-session tradeoff.

### Rationale

The seven decisions form a coherent stack: the provisioning step creates the filesystem
structures the daemon watches; the daemon uses the session IDs that `session register`
records; the blocking `niwa_ask` uses the watcher goroutine that is already running in
every MCP server instance; and the response routing invariant (answer via
`niwa_send_message`, never stdout) means the `niwa_ask` waiter path is identical
regardless of whether the session was woken by the daemon or by a future Channels push
protocol. The combination is crash-safe at every level: messages survive in inbox files
if the daemon crashes, the daemon restarts without data loss on the next `niwa apply`,
and `niwa_ask` goroutines clean up via `defer cancel()` even on timeout.

The activation and role decisions layer cleanly on top: the provisioning gate moves
from a content check (roles map non-empty) to a presence check (`[channels.mesh]`
section exists or equivalent flag/env is set), which makes the config simpler without
changing what gets provisioned. Role auto-derivation does not affect the daemon or MCP
tools at all — they continue to use roles as stored in `sessions.json` at registration
time. The hybrid activation model separates team intent (config) from personal
preference (flag/env var) without introducing runtime branching in the provisioning
code itself.

## Solution Architecture

### Overview

The session mesh is a file-based message bus with daemon-managed wakeup and
in-process blocking tool support. It consists of five cooperating components: a
provisioning step that writes the mesh infrastructure at `niwa apply` time, a persistent
daemon that watches inboxes and resumes idle sessions, a session registration command
that populates the session registry at open time, four MCP tools that form the
session-side API, and the inbox filesystem itself as the durable transport.

### Components

```
niwa apply
  └─ InstallChannelInfrastructure (step 4.75)
       writes: .niwa/sessions/, sessions.json, .claude/.mcp.json,
               ## Channels in workspace-context.md
  └─ SpawnDaemon (after SaveState)
       spawns: niwa mesh watch --instance-root=...

niwa mesh watch (daemon, internal/cli/mesh_watch.go)
  ├─ fsnotify watcher on .niwa/sessions/*/inbox/
  ├─ reads sessions.json → SessionEntry.PID + ClaudeSessionID
  ├─ IsPIDAlive check → calls claude --resume <session-id> for dead PIDs
  └─ PID file: .niwa/daemon.pid (pid\nstart-jiffies\n, written atomically)

niwa session register (internal/cli/session_register.go)
  ├─ resolveRole: --role flag → NIWA_SESSION_ROLE env → roles map → pwd basename
  ├─ discoverClaudeSessionID: env var → ~/.claude/sessions/<ppid>.json → fs scan
  └─ upserts SessionEntry in sessions.json

niwa mcp-serve (internal/mcp/server.go)
  ├─ niwa_check_messages  — stateless inbox read
  ├─ niwa_send_message    — stateless inbox write (to target session's inbox/)
  ├─ niwa_ask             — blocking: registers waiter, selects on reply channel
  └─ niwa_wait            — blocking: registers type-waiter, collects N messages
  shared state:
    waiters     map[string]chan toolResult  (protected by waitersMu)
    typeWaiters map[string]*typeWaiter     (protected by waitersMu)

Inbox filesystem layout:
  .niwa/sessions/
  ├── sessions.json                 ← SessionEntry registry
  └── <session-uuid>/
      ├── inbox/                    ← incoming messages (JSON files, atomic rename)
      │   └── read/                 ← consumed messages moved here by notifyNewFile
      └── artifacts/                ← shared file drop for inter-session artifacts
```

### Key Interfaces

**SessionEntry** (sessions.json):

```go
type SessionEntry struct {
    UUID            string    `json:"uuid"`
    Role            string    `json:"role"`
    RepoDir         string    `json:"repo_dir"`
    InboxDir        string    `json:"inbox_dir"`
    PID             int       `json:"pid"`
    StartTime       int64     `json:"start_time"`    // jiffies-since-boot
    ClaudeSessionID string    `json:"claude_session_id,omitempty"`
    RegisteredAt    time.Time `json:"registered_at"`
}
```

**Message** (inbox JSON files):

```go
type Message struct {
    ID        string `json:"id"`         // UUID assigned by niwa_send_message
    Type      string `json:"type"`       // question.ask, question.answer, task.delegate, etc.
    From      string `json:"from"`       // sender role
    To        string `json:"to"`         // recipient role
    Body      string `json:"body"`
    TaskID    string `json:"task_id,omitempty"`
    ReplyTo   string `json:"reply_to,omitempty"` // message ID being answered
    ExpiresAt string `json:"expires_at,omitempty"`
}
```

**Waiter map** (in-process, not persisted):

```go
// keyed by expected reply_to message ID
waiters map[string]chan toolResult
// keyed by wait-UUID; holds filter criteria and accumulation buffer
typeWaiters map[string]*typeWaiter
```

**PID file** (`daemon.pid`):

```
<pid>\n
<start-time-jiffies>\n
```

Written atomically: `daemon.pid.tmp` → rename to `daemon.pid`. The daemon writes this
file only after establishing its fsnotify watch loop, so `niwa apply` never sees a
partial PID as a valid daemon.

**Channels activation check** (at top of `runPipeline`, before step 4.75):

```go
// Priority: --no-channels flag > --channels flag > config section > NIWA_CHANNELS env var
channelsEnabled := cfg.Channels.IsEnabled() // [channels.mesh] section present
if os.Getenv("NIWA_CHANNELS") == "1" { channelsEnabled = true }
if opts.ChannelsFlag { channelsEnabled = true }    // --channels passed
if opts.NoChannelsFlag { channelsEnabled = false }  // --no-channels passed
// When enabled without config section, synthesize a bare MeshConfig so
// InstallChannelInfrastructure can proceed with auto-derived roles.
if channelsEnabled && !cfg.Channels.IsEnabled() {
    cfg.Channels.Mesh = &MeshConfig{}
}
```

**Daemon spawn** (in `Applier.Apply` / `Applier.Create`, after `SaveState`):

```go
if cfg.Channels.IsEnabled() && !IsPIDAlive(readPIDFile(instanceRoot)) {
    cmd := exec.Command(os.Args[0], "mesh", "watch", "--instance-root", instanceRoot)
    cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, daemonLog, daemonLog
    cmd.Start()
}
```

### Data Flow

**Session open (happy path):**

```
Claude opens session
  → SessionStart hook fires
  → niwa session register
      → discoverClaudeSessionID (env → ppid file → fs scan)
      → upsert SessionEntry in sessions.json (PID, ClaudeSessionID, role)
```

**niwa_ask from session A to coordinator (coordinator idle):**

```
Session A calls niwa_ask(to="coordinator", body="...", task_id="...")
  → handleAsk writes question.ask to coordinator's inbox/
  → registerWaiter(msgID) → buffered chan stored in waiters map
  → select { case <-replyCh | case <-time.After(10min) }   ← A blocks here

niwa mesh watch fsnotify fires for coordinator's inbox/
  → read sessions.json → coordinator PID is dead
  → claude --resume <coordinator-session-id>

Claude resumes coordinator session
  → SessionStart hook fires → niwa session register (updates PID)
  → pending question.ask delivered as initialUserMessage

Coordinator calls niwa_check_messages (or reads initialUserMessage)
  → composes answer
  → niwa_send_message(to="session-A-role", reply_to=msgID, body="...")
      → writes question.answer to session A's inbox/

niwa_mcp-serve (session A) watchInbox fires for new file
  → notifyNewFile reads file, checks m.ReplyTo against waiters map
  → match found: moves file to inbox/read/, sends to replyCh
  → handleAsk's select exits via replyCh
  → defer cancel() removes waiter entry
  → tool result returned to Claude
```

**niwa_ask timeout:**

```
select exits via time.After
  → errResultCode("ASK_TIMEOUT", ...) returned
  → defer cancel() removes waiter entry
  → if watcher fires late: buffered send succeeds, value is GC'd
```

## Implementation Approach

### Phase 1: Schema and Config

Define the data types used across all components. No I/O.

Deliverables:
- `internal/config/config.go`: `[channels.mesh]` config struct and `ChannelsConfig`
- `internal/mcp/message.go` (or `server.go`): `Message` struct with `ReplyTo`, `ExpiresAt` fields
- `internal/cli/session_register.go`: `SessionEntry.ClaudeSessionID` field, `discoverClaudeSessionID()` function

### Phase 2: Provisioning

Write the workspace infrastructure at apply time.

Depends on: Phase 1 (config struct).

Deliverables:
- `internal/workspace/apply.go`: `InstallChannelInfrastructure()` at step 4.75
- `internal/workspace/workspace_context.go`: idempotent `## Channels` append
- Hook injection: synthesize SessionStart + UserPromptSubmit `HookEntry` values from `[channels.mesh]` into `cfg.Claude.Hooks` at top of `runPipeline`
- Functional test: `niwa apply` on a workspace with `[channels.mesh]` creates sessions dir, sessions.json, .mcp.json, and the ## Channels section

### Phase 3: Session Registration

Populate the session registry at open time, including Claude session ID.

Depends on: Phase 2 (sessions.json path exists).

Deliverables:
- `internal/cli/session_register.go`: three-tier `discoverClaudeSessionID()`, cross-check `cwd` against current directory, graceful empty fallback with warning log
- Functional test: `niwa session register` with a fake `~/.claude/sessions/<pid>.json` fixture populates `claude_session_id` in `sessions.json`

### Phase 4: Daemon

Build the workspace-scoped watcher and wakeup process.

Depends on: Phase 2 (sessions dir layout), Phase 3 (ClaudeSessionID in SessionEntry).

Deliverables:
- `internal/cli/mesh_watch.go`: fsnotify watcher loop, `IsPIDAlive` check, `claude --resume` invocation, SIGTERM handler with in-flight subprocess cleanup
- Daemon spawn in `Applier.Apply` / `Applier.Create` (after `SaveState`), behind `IsPIDAlive` idempotency check, `Setsid: true`, log to `.niwa/daemon.log`
- `niwa destroy` extension: SIGTERM + 5s wait + SIGKILL before `DestroyInstance`
- PID file write (atomic rename) + `IsPIDAlive` reuse from `internal/mcp/liveness.go`

### Phase 5: MCP Tools

Extend the MCP server with the four channel tools.

Depends on: Phase 2 (.mcp.json written), Phase 3 (sessions.json populated at runtime).

Deliverables:
- `internal/mcp/server.go`: `waiters` and `typeWaiters` maps + `waitersMu`; `registerWaiter()` helper
- `handleCheckMessages()`, `handleSendMessage()` (may already exist; extend if needed)
- `handleAsk()`: write question.ask, registerWaiter, select on reply/timeout, defer cancel
- `handleWait()`: register type-waiter, select on count-reached/timeout, defer cancel
- `notifyNewFile()` extension: check `ReplyTo` against `waiters`, check type/sender against `typeWaiters`, move file to `read/` before signaling
- Refactor `handleSendMessage` to return a struct (message ID + status) rather than text output, so `handleAsk` does not need `extractSentMessageID` string parsing

### Phase 6: Channels Ergonomics

Implement role auto-derivation, hybrid activation flags, and the NIWA_CHANNELS env var.

Depends on: Phase 1 (config struct), Phase 2 (provisioning gate), Phase 3 (session register role resolution).

Deliverables:
- `internal/config/config.go`: replace `IsEmpty()` with `IsEnabled()` (presence-based); add `MeshConfig.IsEnabled()` method; `[channels.mesh.roles]` map remains optional
- `internal/workspace/apply.go`: add `--channels`/`--no-channels` bool flags to `niwa create` and `niwa apply`; read `NIWA_CHANNELS` env var; apply priority logic at top of `runPipeline`; synthesize bare `MeshConfig{}` when channels enabled without config section
- `internal/cli/session_register.go`: add `resolveRole()` function implementing the four-tier priority chain (`--role` flag → `NIWA_SESSION_ROLE` → roles map lookup → `pwd` basename relative to `NIWA_INSTANCE_ROOT`)
- `internal/workspace/channels.go`: update `buildChannelsSection` to describe roles as auto-derived rather than hardcoding the roles list; remove the `IsEmpty()` guard (caller now uses `IsEnabled()`)
- Functional tests:
  - `niwa create --channels` on a workspace without `[channels.mesh]` provisions channel infrastructure with auto-derived roles
  - `niwa create` on a workspace with `[channels.mesh]` followed by `niwa apply --no-channels` does not duplicate infrastructure
  - `NIWA_CHANNELS=1 niwa create` enables channels (env var default)
  - `niwa session register` in a repo assigns role = repo basename when no explicit role is configured

## Security Considerations

**Threat model.** This feature's security properties hold when all processes running as
the message-passing user are trusted. It does not protect against a malicious process
running as the same UID. Multi-user systems or shared containers require OS-level
namespace isolation outside the scope of this design.

**File permissions.** All files under `.niwa/sessions/` must be created with mode
`0600` (files) and `0700` (directories). `InstallChannelInfrastructure` must set these
modes explicitly, independent of umask.

**Input validation.** `ClaudeSessionID` must be validated against
`^[a-zA-Z0-9_-]{8,128}$` at registration time. Values that don't match must be rejected
and logged; they must not be written to `sessions.json` or used in `exec.Command`
arguments. All `Message` fields used as path components (`Type`, `From`, `To`) must be
validated against a similarly constrained pattern.

**Role auto-derivation.** When `resolveRole()` falls back to the repo basename (tier 4),
the derived string is validated against the same path-component pattern before being
written to `sessions.json`. A repo with a non-conformant name (unlikely but possible
with non-standard workspace configs) must fall back gracefully: log a warning and use
`unknown` as the role rather than writing an invalid value.

**NIWA_CHANNELS env var.** Only the literal values `"1"` (enable) and `"0"` (disable)
are recognized. Any other value is ignored with a warning logged to stderr. The env var
has no path or command components, so there is no injection risk beyond the boolean
behavior.

**Message authentication.** The HMAC-SHA256 signing scheme — a per-instance shared
secret at `.niwa/channel.key` (mode `0600`, generated at `InstallChannelInfrastructure`
time), where `niwa_send_message` signs outgoing messages and `watchInbox` verifies
signatures before routing — should be tracked as a follow-on requirement from the
start. Unsigned messages must eventually be rejected with a warning. This is deferred
from the initial implementation but must not be forgotten.

**sessions.json concurrency.** All reads and writes to `sessions.json` must be
protected by a file lock (advisory lock on a `.sessions.lock` file in `.niwa/`). This
applies to both `niwa session register` and the daemon's read path.

**Message retention.** Consumed messages in `inbox/read/` must be purged. The daemon
should sweep `read/` on startup and periodically (e.g., hourly) and delete files older
than a configurable TTL (default: 24 hours). Message bodies must not appear in log
output; log message IDs and types only.

**Binary path resolution.** The `claude` binary path must be resolved with
`exec.LookPath` once at daemon start and stored as an absolute path. Do not rely on
PATH at each invocation.

**Graceful shutdown.** The daemon's SIGTERM handler must stop accepting new fsnotify
events, complete any in-progress file moves, and sync open file descriptors before
exiting. This prevents partial writes to `sessions.json` during `niwa destroy`.

## Consequences

### Positive

- **Crash-safe transport**: messages survive daemon crashes and session crashes because
  the inbox is a filesystem tree; no message lives only in memory
- **Stateless daemon**: restartable without data loss or re-registration; `niwa apply`
  detects stale PID and spawns a fresh daemon automatically
- **No goroutine leaks**: `defer cancel()` always removes the waiter map entry; the
  capacity-1 buffered channel absorbs any late watcher send after timeout without
  blocking
- **Forward-compatible response path**: sessions respond via `niwa_send_message` (never
  stdout), so when Claude Code's Channels protocol can wake sessions natively, only the
  daemon's `claude --resume` step is replaced — the `niwa_ask` waiter detection path is
  unchanged
- **Idempotent apply**: repeated `niwa apply` calls do not spawn duplicate daemons,
  overwrite `sessions.json`, or duplicate the `## Channels` section
- **Reply latency matches fsnotify latency** (~10ms), not polling latency (500ms+)
- **Hook pipeline unchanged**: channel hook scripts flow through `HooksMaterializer`
  without modification; no second hook-writing path to maintain
- **`instanceRoot` stays out of `MaterializeContext`**: existing materializer interface
  is stable
- **Zero-config for common case**: a bare `[channels.mesh]` section (or `--channels`
  flag) is sufficient to provision a full session mesh; workspaces with descriptive
  repo names need no role configuration at all
- **Personal channels without shared config changes**: users can enable channels via
  personal overlay or `NIWA_CHANNELS=1` without modifying workspace.toml; team-shared
  config stays clean

### Negative

- **Busy session serialization**: if the target session's PID is alive (it is busy with
  another task), messages queue in its inbox. The daemon takes no action until the PID
  dies. The sender's `niwa_ask` eventually times out if the busy session does not
  finish and check its inbox within the timeout window.
- **Live coordinator delivery gap**: an interactive coordinator session (PID alive,
  user at the terminal) cannot be resumed by the daemon. It relies on the
  `UserPromptSubmit` hook or the coordinator periodically calling `niwa_wait` to detect
  incoming messages.
- **Claude session ID discovery can fail**: if `CLAUDE_SESSION_ID` is not exported and
  the `~/.claude/sessions/` file is absent or stale, `claude_session_id` is left empty
  and the daemon cannot resume that session autonomously. The session is still reachable
  via SessionStart delivery when it opens manually.
- **Daemon not auto-restarted on crash or machine restart**: `niwa mesh watch` is a
  regular user process, not a system service. It does not survive machine restarts or
  logouts. After a reboot, all instance daemons must be restarted manually via `niwa
  apply`. Sessions that arrive while the daemon is down queue messages durably but are
  not woken until the daemon is back.
- **Role auto-derivation can surprise users with generic repo names**: a repo named
  `core` gets role `core`; if multiple workspaces have a repo named `core`, sessions
  across those workspaces will have the same role name. The roles map override exists
  to handle this, but users must know to use it.
- **`--channels` flag activates without config validation**: when passed without
  `[channels.mesh]` in workspace.toml, there is no config section to validate; the
  runtime synthesizes a bare `MeshConfig{}`. Users who pass `--channels` and later
  forget it on re-apply will get channels disabled on that apply (unless `NIWA_CHANNELS`
  is set or the personal overlay includes the section).
- **`rm -rf` on an instance leaves a transient leaked daemon**: removing an instance
  directory without `niwa destroy` bypasses daemon shutdown. The daemon detects the
  missing instance root via fsnotify errors and exits, but this is an unclean termination.
  There is no recovery path for the leaked process other than waiting for it to self-exit.
- **`SysProcAttr.Setsid` is Unix-specific**: a Windows port would need a different
  process detachment mechanism (Windows is out of scope for v1).
- **Forward-compatibility risk for filesystem scan**: the base64url encoding of the CWD
  path in `~/.claude/projects/` must match Claude Code's exact algorithm. If Claude Code
  changes the encoding, the fallback scan produces wrong results (the primary PPID path
  is unaffected).

### Mitigations

- **Busy session**: accepted tradeoff per PRD Known Limitations. The `niwa_ask` timeout
  is configurable per call (default 10 minutes); callers can set shorter timeouts for
  time-sensitive exchanges and retry.
- **Live coordinator gap**: the `## Channels` section in workspace-context.md includes
  the instruction to call `niwa_wait` periodically when acting as coordinator. The
  `UserPromptSubmit` hook also fires on each user input, delivering any pending messages.
- **Session ID failure**: graceful degradation is the designed behavior. A warning log
  entry makes the gap observable. Future work: functional test with a fake
  `~/.claude/sessions/` fixture to catch regressions in the discovery logic.
- **Daemon not running (crash or reboot)**: `niwa apply` is the recovery path for any
  instance. Running `niwa apply` from the workspace root restarts daemons for all
  instances at once. A future `niwa mesh restart` subcommand could expose targeted
  per-instance restart more explicitly.
- **`rm -rf` safety**: document in user-facing guides that instances with mesh configured
  must be removed via `niwa destroy`, not `rm -rf`. The daemon's self-exit on missing
  instance root limits blast radius, but the guidance should be explicit.
- **Windows**: `SysProcAttr` is set behind a `//go:build !windows` build tag if Windows
  support is added in the future.
- **Encoding forward-compat**: document the base64url algorithm dependency in a code
  comment adjacent to the filesystem scan fallback so it is easy to locate and update
  independently if Claude Code changes the encoding.
- **Generic repo name role collisions**: document in the `## Channels` section of
  workspace-context.md that roles are derived from repo names, so coordinators
  addressing workers by role are addressing by repo name. If a workspace has ambiguous
  repo names, the user should add `[channels.mesh.roles]` overrides.
- **Transient `--channels` flag without config**: if persistent channel activation is
  desired without a shared `[channels.mesh]` config, users should add the section to
  their personal overlay or set `NIWA_CHANNELS=1` in their shell profile. The
  `niwa apply` output should include a note when channels were activated via flag rather
  than config, suggesting the persistent alternatives.
