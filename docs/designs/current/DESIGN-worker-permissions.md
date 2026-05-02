---
status: Current
problem: |
  Worker sessions spawned by niwa's mesh daemon use --permission-mode=acceptEdits,
  which auto-approves file writes but blocks shell tool calls. Workers run headless
  and unattended, so approval dialogs never get answered. Tasks that reach the
  execution step (gh pr create, go test, git push) abandon instead of completing.
decision: |
  Workers inherit the coordinator's configured permission mode, resolved once at
  daemon startup from settings.local.json and stored in spawnContext. Bypass-configured
  coordinators produce bypass workers; unconfigured coordinators produce workers with
  acceptEdits plus a curated set of Bash tool patterns for common dev tools. Reading
  once at startup (not per spawn) eliminates the TOCTOU surface where an acceptEdits
  worker could write settings.local.json to escalate to bypass.
rationale: |
  The user's existing permission setting is the right authority signal for workers.
  Startup-time caching is the security-correct approach here because workers run as
  the same OS user as the daemon and can write to any file that user owns, including
  settings.local.json. A per-spawn read creates a privilege escalation path that does
  not exist in the current hardcoded design. Accepting that permission changes require
  a daemon restart is a reasonable trade-off for closing that surface. The curated
  Bash fallback list handles the common case without a full bypass.
---

# DESIGN: Worker session permissions

## Status

Current

## Context and problem statement

When niwa's mesh daemon spawns a worker session to handle a delegated task, that
session runs with `--permission-mode=acceptEdits`. This auto-approves file writes
but requires interactive user approval for all shell tool calls — `gh`, `git push`,
`go test`, and similar. Because workers run headless in `-p` mode, no human is
present to approve anything. The worker stalls on the first shell invocation and
the task transitions to `abandoned`.

Both reported failures (issue #86) followed the same pattern: the worker completed
all analytical and coding work, then hit the permission wall at the final execution
step (creating a PR, running tests). The fix must target the final execution step
without changing how coordinators behave.

## Decision drivers

- Workers run headless and unattended. Any permission model requiring interactive
  approval is a non-starter.
- The coordinator's permission mode is already expressed via `settings.local.json`
  (produced by `materialize.go`). Reusing that signal avoids a new configuration
  surface.
- Workers execute delegated subtasks within a coordinator's trust boundary. The
  coordinator chose to delegate; the natural permission model gives workers the same
  execution rights the coordinator has.
- Security: `daemon.go` already mentions the "acceptEdits blast radius" as a known
  limitation. Any broader permission requires careful scoping.
- Workers run as the same OS user as the daemon and can write any file that user
  owns. The permission source must be read before any worker runs.
- The `--allowed-tools` flag supports fine-grained tool patterns (`Bash(gh *)`,
  `Bash(git *)`) as an alternative to a full permission mode bypass.

## Considered options

### Decision 1: Worker session permission scope

**Context**

niwa's mesh daemon spawns worker sessions to handle delegated tasks. Workers always
run headless (`claude -p`), with no human present to answer approval dialogs. The
current spawn uses `--permission-mode=acceptEdits`, which auto-approves file writes
but not shell tool calls. Any worker that needs to run `gh pr create`, `go test`,
or `git push` hits an approval dialog that never gets answered and the task
transitions to `abandoned`.

The coordinator's permission mode is already expressed in
`<instanceRoot>/.claude/settings.local.json` as `permissions.defaultMode`
(`bypassPermissions` or `askPermissions`), written by `materialize.go` from the
user's niwa config (`permissions = "bypass"` or `permissions = "ask"`). The daemon
has `instanceRoot` available in its `spawnContext` struct.

`daemon.go` already treats the `acceptEdits` blast radius as security-relevant: it
SIGKILLs all worker process groups before SIGTERMing the daemon during teardown,
specifically to bound the exfiltration window of a worker with wide permissions.

Key assumptions:

- `--permission-mode=bypassPermissions` is a valid Claude CLI flag (same spelling
  as the `settings.local.json` value).
- Curated Bash patterns (`gh`, `git`, `go test`, `go build`, `make`) cover the
  primary worker shell operations for the current user base.
- Workers run as the same OS user as the daemon.
- `settings.local.json` is reliably present after `niwa apply` runs.

#### Chosen: B — Inherit coordinator mode

Workers derive their `--permission-mode` from the coordinator's
`permissions.defaultMode` in `settings.local.json`:

- If `defaultMode == "bypassPermissions"`: spawn with
  `--permission-mode=bypassPermissions`
- Otherwise (any other value, absent key, or parse error): spawn with
  `--permission-mode=acceptEdits` plus an extended `--allowed-tools` list that
  appends curated Bash patterns (`Bash(gh *)`, `Bash(git *)`, `Bash(go test *)`,
  `Bash(go build *)`, `Bash(make *)`) to the existing niwa MCP tool list

The curated Bash patterns for the fallback branch live in
`internal/mcp/allowed_tools.go` alongside `ClaudeAllowedTools`, so both the daemon
and the functional-test harness reference the same source.

No user-facing configuration changes are required. The permission mode is resolved
once when the daemon starts, stored in `spawnContext`, and applied to all workers
spawned in that daemon lifetime.

The user's existing permission setting is the right authority signal. A user who
configured `permissions = "bypass"` has already accepted that their coordinator
session runs shell commands without approval; restricting workers to a curated list
while the coordinator has full bypass creates an inconsistent and surprising model.
Conversely, a user who configured `permissions = "ask"` or left it unconfigured has
not accepted a full bypass, and workers should not silently expand past that intent.

#### Alternatives considered

- **Full bypass (A)**: Workers always receive `bypassPermissions`. Rejected because
  it silently maximizes blast radius for all users regardless of their coordinator
  permission setting, inconsistent with the codebase's existing security posture.
- **Curated preset only (C)**: Workers always use `acceptEdits` plus a fixed list of
  Bash patterns. Rejected as the sole mechanism because it ignores the user's bypass
  signal (bypass users get unnecessarily constrained workers) and requires ongoing
  pattern-list maintenance. The curated list is retained as the fallback branch
  within option B.
- **Per-delegation specification (D)**: `niwa_delegate` gains an `allowed_tools`
  field. Rejected because it doesn't fix the immediate bug without updating all
  coordinator skills, adds schema complexity, and shifts cognitive burden onto
  coordinator AI agents that follow a fixed skill prompt.

---

### Decision 2: Permission resolution at daemon startup

**Context**

`spawnWorker()` in `internal/cli/mesh_watch.go` currently hardcodes
`--permission-mode=acceptEdits`. The design needs to replace this with a value
derived from `settings.local.json`.

Workers run as the same OS user as the daemon and have `acceptEdits` permission,
which auto-approves file writes. A per-spawn read of `settings.local.json` creates
a privilege escalation path: an `acceptEdits` worker could write the file to inject
`bypassPermissions`, and the next spawn would pick up the tampered value. This
attack surface does not exist in the current hardcoded design.

The workspace package already owns all materialization logic, including
`permissionsMapping`, and exposes per-spawn helpers (`WorkerMCPConfig`,
`WorkerMCPConfigPath`) that `spawnWorker` calls today.

Key assumptions:

- Permission mode derives from `settings.local.json`; no existing workspace package
  function reads back the materialized permission mode.
- The staleness trade-off is acceptable: if `permissions` changes in niwa config
  and the user runs `niwa apply`, the new permission mode takes effect on the next
  daemon restart, not the next spawn. This is consistent with other daemon-init
  parameters.

#### Chosen: D — New workspace package function, called at daemon startup

Add `WorkerPermissionMode(instanceRoot string) string` to `internal/workspace`.
The function reads `<instanceRoot>/.claude/settings.local.json` using a minimal
struct (see Data Exposure in Security Considerations), extracts
`.permissions.defaultMode`, and returns `"bypassPermissions"` if present, or
`"acceptEdits"` otherwise.

This function is called **once at daemon startup** (not per spawn) and the result
is stored in `spawnContext.workerPermMode`. All workers spawned in that daemon
lifetime receive the same permission mode. Because the mode is fixed at startup
before any worker runs, no worker can influence what mode subsequent workers receive.

This mirrors the existing `WorkerMCPConfig` pattern for the interface: workspace
provides the logic, the daemon calls it. The function is independently unit-testable
with temp-dir fixtures.

#### Alternatives considered

- **Per-spawn read (Option A/D variant)**: Read `settings.local.json` in
  `spawnWorker` on each spawn. Always current, but introduces the TOCTOU
  escalation path described above. Rejected because it creates a novel attack
  surface absent from the current code.
- **Cache in spawnContext, per-spawn read, with hash check**: Read per spawn but
  verify a checksum stored by `niwa apply`. Rejected because workers run as the
  same OS user and can write to `.niwa/` too, so they could update the checksum to
  match a tampered `settings.local.json`.
- **Pass via task envelope (Option C)**: `niwa_delegate` includes `permission_mode`.
  Rejected as over-engineered: workspace config, not task payloads, is the right
  owner for this value.

## Decision outcome

Workers inherit the coordinator's configured permission mode, resolved once at daemon
startup from `settings.local.json` via a new `WorkerPermissionMode` helper in the
workspace package. The result is stored in `spawnContext` and applied to every
worker spawned in that daemon lifetime. When the coordinator has `bypassPermissions`,
workers get full bypass. When the coordinator has no explicit bypass configured, workers
get `acceptEdits` plus curated Bash patterns for common dev tools. Startup-time
resolution closes the TOCTOU escalation path that a per-spawn read would introduce.

## Solution architecture

### Overview

`spawnWorker` currently builds a fixed `exec.Command` with a hardcoded permission
mode. The change replaces that hardcoded value with one resolved at daemon startup:
a new workspace package function reads `settings.local.json` once, and the result
propagates through `spawnContext` to every subsequent spawn.

### Components

```
internal/workspace/
  channels.go (existing)
    WorkerMCPConfig()         per-spawn worker MCP config (unchanged)
    WorkerMCPConfigPath()     path for worker MCP config (unchanged)
  permissions.go (new)
    WorkerPermissionMode()    read settings.local.json at startup; return mode string

internal/mcp/
  allowed_tools.go (modified)
    ClaudeAllowedTools        niwa MCP tool names (unchanged)
    WorkerFallbackBashTools   curated Bash patterns for non-bypass workers (new)

internal/cli/
  mesh_watch.go (modified)
    spawnContext              add workerPermMode string field
    runMeshWatch()            call WorkerPermissionMode at startup; store in spawnContext
    spawnWorker()             read mode from s.workerPermMode; inline bypass check;
                              append WorkerFallbackBashTools when mode != bypass
```

### Key interfaces

**`workspace.WorkerPermissionMode(instanceRoot string) string`**

Reads `<instanceRoot>/.claude/settings.local.json` using a minimal struct:

```go
// settingsPermissionsDoc reads only the permissions key from settings.local.json.
// The env key is intentionally absent — it may carry secret material and is not
// needed here. Do not widen this struct without auditing callers for secret exposure.
type settingsPermissionsDoc struct {
    Permissions struct {
        DefaultMode string `json:"defaultMode"`
    } `json:"permissions"`
}
```

Returns `"bypassPermissions"` if `defaultMode` equals that value; returns
`"acceptEdits"` in all other cases (file absent, key missing, parse error,
unknown value). Never returns an empty string.

**`mcp.WorkerFallbackBashTools`**

Package-level `[]string` in `allowed_tools.go`. Initial set:

```go
var WorkerFallbackBashTools = []string{
    "Bash(gh *)",
    "Bash(git *)",
    "Bash(go test *)",
    "Bash(go build *)",
    "Bash(make *)",
}
```

Lives next to `ClaudeAllowedTools` so both the daemon and functional tests use the
same source. This list grants substantial capability (see Security Considerations)
and is a usability default, not a security boundary.

**`spawnContext.workerPermMode string`**

New field added to the existing `spawnContext` struct. Set once in `runMeshWatch`
before the event loop starts. Read in `spawnWorker` as a plain string field lookup.

### Data flow

```
niwa apply
  → materialize.go writes settings.local.json
    { permissions: { defaultMode: "bypassPermissions" } }  (if bypass configured)
    {}                                                       (if not configured)

daemon startup (runMeshWatch / runWatchDaemon)
  → mode = workspace.WorkerPermissionMode(instanceRoot)
      reads settings.local.json once with minimal struct
      returns "bypassPermissions" OR "acceptEdits"
  → spawnCtx.workerPermMode = mode
  → [event loop begins — no worker can run before this point]

per-spawn (spawnWorker)
  → mode = s.workerPermMode  (in-memory, no file read)
  → tools = mcp.ClaudeAllowedTools
    if mode != "bypassPermissions" {
        tools = append(tools, mcp.WorkerFallbackBashTools...)
    }
  → exec.Command(
        s.spawnBin,
        "-p", prompt,
        "--permission-mode=" + mode,
        "--mcp-config=" + workerMCPPath,
        "--strict-mcp-config",
        "--allowed-tools", strings.Join(tools, ","),
    )
```

> **Note (niwa 0.9.4 — DESIGN-coordinator-loop.md Phase 3, Proposed):**
> `spawnWorker` gains a `resumeMode` parameter. On the resume branch the first
> positional arguments change from `"-p", prompt` to
> `"--resume", session_id, "-p", reminder`. The `--permission-mode` and
> `--allowed-tools` values are identical across both branches — resume semantics
> do not change permission scope.

## Implementation approach

### Phase 1: Workspace helper and fallback tool list

Add `internal/workspace/permissions.go` with `WorkerPermissionMode`. Add
`mcp.WorkerFallbackBashTools` to `internal/mcp/allowed_tools.go`.

Deliverables:
- `internal/workspace/permissions.go` (new)
- `internal/workspace/permissions_test.go` (new, temp-dir fixtures for bypass, ask,
  absent, malformed, and tampered-after-startup scenarios)
- `internal/mcp/allowed_tools.go` (add `WorkerFallbackBashTools`)

### Phase 2: Wire into daemon startup and spawnWorker

Add `workerPermMode string` to `spawnContext`. Call `WorkerPermissionMode` at
daemon startup (in `runMeshWatch` or the init block that builds `spawnContext`).
Replace the hardcoded `"--permission-mode=acceptEdits"` in `spawnWorker` with
`s.workerPermMode`, and conditionally append `WorkerFallbackBashTools`.

Deliverables:
- `internal/cli/mesh_watch.go` (modify `spawnContext`, `runMeshWatch`, `spawnWorker`)

### Phase 3: Functional test coverage

Add a `@critical` Gherkin scenario exercising both branches: bypass path (coordinator
configured with bypass, delegated worker runs a shell command) and fallback path
(no bypass configured, worker runs a command from the curated list).

Deliverables:
- `test/functional/features/mesh.feature` (new scenario)

## Security considerations

This design expands worker execution rights, introducing several security dimensions
worth documenting for implementers.

**Permission scope (bypass branch — high severity)**

Workers spawned under a bypass-configured coordinator receive
`--permission-mode=bypassPermissions`, granting full unmediated shell access as the
daemon's OS user. This blast radius matches the coordinator's existing surface — the
user accepted it when they set `permissions = "bypass"`. The daemon's teardown
SIGKILL ordering (SIGKILL all workers before SIGTERM to daemon) bounds the
exfiltration window at destroy time and must be preserved.

One consequence: if a coordinator legitimately needs bypass for one broad task and
the workspace is long-lived, all delegated subtasks in that workspace session also
get bypass. Future work could scope bypass per delegation via a `niwa_delegate`
field, but that is out of scope for this design.

**Permission scope (curated Bash fallback — medium severity)**

The fallback branch adds patterns like `Bash(gh *)`, `Bash(git *)`, and `Bash(make *)`.
These are not security boundaries. `Bash(gh *)` allows any `gh` subcommand, including
`gh api` and `gh secret`. `Bash(git *)` allows `git push --force` and
`git remote set-url`. `Bash(go test *)` and `Bash(make *)` are code-execution
primitives, not simple wrappers: a compromised `_test.go` or `Makefile` in a
cloned repo can run arbitrary code under these patterns. The curated list is a
usability default for the unattended worker case, not a restriction. Users who need
tighter control should set `permissions = "bypass"` explicitly (to get full bypass
with clear intent) or wait for a future per-delegation override mechanism.

**TOCTOU surface (closed by this design — high severity if left open)**

Workers run as the same OS user as the daemon and have `acceptEdits` permission,
which auto-approves file writes. A per-spawn read of `settings.local.json` would
create a privilege escalation path: an `acceptEdits` worker writes the file to inject
`"bypassPermissions"`, and the next spawn picks up the tampered value. This path
does not exist in the current hardcoded design. Closing it requires that the
permission mode be resolved before any worker runs — which is what startup-time
caching in `spawnContext` achieves. Changing `permissions` in niwa config and running
`niwa apply` while the daemon is running takes effect on the next daemon restart.

One residual vector (low severity): a compromised worker that writes
`settings.local.json` and then triggers a daemon crash could cause a process
supervisor (systemd, launchd) to restart the daemon with the tampered file, granting
bypass to subsequent workers. This requires the worker to also be able to crash the
daemon, which is a separate vulnerability. Users running niwa under a process
supervisor should be aware of this; no design change is required here.

**Data exposure (low severity)**

`WorkerPermissionMode` reads `settings.local.json`, which may contain secret-backed
env values in its `env` key. Using a minimal struct (parsing only `permissions.defaultMode`)
avoids loading that material into process memory unnecessarily. See the interface
definition in Solution Architecture for the struct shape.

**Prompt injection (pre-existing — high severity)**

Workers receive task bodies from the coordinator via `niwa_check_messages`. A
malicious actor who can influence the coordinator's context could inject a task body
directing a bypass-mode worker to exfiltrate data or push malicious code. This risk
predates this design and is unchanged by it. Existing mitigations — fixed bootstrap
prompt (`"You are a worker for niwa task %s. Call niwa_check_messages..."` in argv,
task body only retrieved via MCP tool call), daemon SIGKILL before teardown — are
correct baselines and must be preserved. Audit logging via a `post_tool_use` hook
on workers would improve forensic visibility but is not required for this design.

## Consequences

### Positive

- Workers under bypass-configured coordinators can complete real tasks end-to-end
  without abandoning at the execution step. This was the root cause of both issue
  #86 failures.
- Workers under non-bypass coordinators gain access to common dev tools (`gh`,
  `git`, `go`, `make`) without any config change.
- Startup-time resolution eliminates the TOCTOU escalation path that a per-spawn
  read would introduce.
- No new user-facing configuration surface is introduced.

### Negative

- `permissions` config changes via `niwa apply` require a daemon restart to take
  effect for workers. (They already require one for any other daemon-init parameter.)
- Workers with bypass permissions can run arbitrary shell commands during normal
  operation. The SIGKILL ordering bounds the teardown window but does not restrict
  runtime behavior.
- All workers in a daemon lifetime share the same permission level. A coordinator
  that legitimately uses bypass for one broad task effectively grants bypass to all
  delegated subtasks in that session, even unrelated ones. Per-delegation override
  (scoping bypass to specific task envelopes) is planned future work.
- The curated Bash pattern list is a maintained artifact. Users whose workflows
  require tools outside the initial list (`cargo`, `npm`, `docker`) will see workers
  stall on those calls until their pattern is added or bypass is configured.

### Mitigations

- Restart requirement: document in release notes that `niwa daemon restart` is
  needed after changing `permissions` in workspace config.
- Bypass blast radius: daemon teardown SIGKILL ordering is already present and
  must not be removed.
- Curated list gaps: the list is a single `var` in `allowed_tools.go`, easy to
  extend. Future work can expose it as a workspace config override.
