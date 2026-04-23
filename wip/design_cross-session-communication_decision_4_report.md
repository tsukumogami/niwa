<!-- decision:start id="worker-spawn-contract" status="assumed" -->
### Decision: Worker spawn contract and NIWA_WORKER_SPAWN_COMMAND shape

**Context**

When a task is queued for a role with no running worker, the daemon
performs the consumption rename (PRD R16) and spawns a fresh worker via
`claude -p`. The spawn carries no task body — only a fixed bootstrap
prompt containing the task ID — so the worker must retrieve its
envelope via `niwa_check_messages` on its first tool call. The design
must specify the exact argv, env, CWD, and flags niwa produces; how the
task ID reaches both the LLM (for prompting) and the MCP server
subprocess (for authorization of `niwa_finish_task`, `niwa_report_progress`,
etc.); the shape of the `NIWA_WORKER_SPAWN_COMMAND` override that
functional tests and user wrappers rely on; and the daemon's env
inheritance policy. Reproducibility matters: the same task_id on a
retrying spawn must produce the same niwa-controlled argv and env so
tests can assert on captured invocations.

The existing codebase resolves `claude` once via `exec.LookPath` at
daemon start and passes positional arguments via `exec.Command`. Niwa
already uses dedicated `NIWA_*` env vars to propagate identity and
scope to subprocesses (`NIWA_INSTANCE_ROOT`, `NIWA_SESSION_ROLE`,
`NIWA_SESSION_ID`). PRD R32 fixes the bootstrap prompt literal, R33
pins the three spawn flags, and R51 specifies
`NIWA_WORKER_SPAWN_COMMAND` as the binary-substitution override.

**Assumptions**

- The MCP server is a per-worker-process stdio subprocess launched by
  Claude Code from `<instanceRoot>/.claude/.mcp.json` (Decision 3's
  leading option). The MCP server inherits env from the worker process.
  If Decision 3 selects a per-instance shared MCP server, this
  decision's argv and env contract is still correct but the MCP
  server's task-ID read path changes (it would read the calling
  worker's identity via the MCP connection rather than its own env).
- Claude Code's `claude -p <prompt> [flags]` tolerates R33's flags in
  any position on the command line. If flags must precede `-p`, niwa
  reorders; the contract is otherwise unaffected.

**Chosen: Alternative 1 — Path-override + dual task-ID propagation + pass-through env with niwa-owned additions**

The daemon resolves the spawn binary once at startup:

```go
spawnBin := os.Getenv("NIWA_WORKER_SPAWN_COMMAND")
if spawnBin == "" {
    spawnBin, _ = exec.LookPath("claude")
}
```

`NIWA_WORKER_SPAWN_COMMAND` is a filesystem path, not a command
template; users who need shell wrapping point the env var at a wrapper
script. If neither the override nor `claude` on PATH resolves, the
daemon refuses to spawn and the task stays `queued` until resolved (the
operator sees the task via `niwa task list`).

For every spawn, niwa constructs a fixed argv:

```go
const bootstrapTemplate = "You are a worker for niwa task %s. " +
    "Call niwa_check_messages to retrieve your task envelope."
prompt := fmt.Sprintf(bootstrapTemplate, taskID)
argv := []string{
    "-p", prompt,
    "--permission-mode=acceptEdits",
    "--mcp-config=" + filepath.Join(instanceRoot, ".claude", ".mcp.json"),
    "--strict-mcp-config",
}
cmd := exec.Command(spawnBin, argv...)
cmd.Dir = resolveCWD(targetRole, instanceRoot, workspaceConfig)
cmd.Env = buildWorkerEnv(os.Environ(), instanceRoot, taskID, targetRole)
cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
```

The argv is identical for every spawn path (real `claude`, override
binary, functional-test fake). No branching on "is this a test?" — the
spawn binary is different; niwa's contract is constant.

CWD resolution is:
- `role == "coordinator"` → `instanceRoot`
- otherwise → `workspaceConfig.Roles[role].RepoDir` (explicit entry) or
  the auto-derived repo path.

If the resolved path does not exist, the daemon abandons the task with
`reason: "target_repo_missing"` rather than spawning a worker in an
undefined directory.

Task-ID propagates via two channels:

1. **Argv** — the bootstrap prompt contains `<task-id>`, so the LLM
   has an unambiguous task reference for `niwa_check_messages` and
   subsequent `niwa_finish_task(task_id=...)` calls. Satisfies AC-D5
   (argv contains only the fixed bootstrap with `<task-id>` substituted,
   no task-body fields).

2. **Env (`NIWA_TASK_ID=<task-id>`)** — the worker's MCP server
   subprocess inherits env and reads `NIWA_TASK_ID` to scope
   authorization. Argv alone would not reach the MCP server; env alone
   would hide the task ID from the model. Both are needed.

The env built by `buildWorkerEnv` is:

```go
func buildWorkerEnv(base []string, instanceRoot, taskID, role string) []string {
    env := make([]string, 0, len(base)+4)
    for _, kv := range base {
        // Scrub NIWA_SESSION_ID — workers are not sessions; do not let a
        // leaked daemon env value mislead the MCP server.
        if strings.HasPrefix(kv, "NIWA_SESSION_ID=") {
            continue
        }
        env = append(env, kv)
    }
    // Append niwa-owned vars last; Go's exec uses last-wins on duplicates,
    // so these overwrite any stale daemon values.
    env = append(env,
        "NIWA_INSTANCE_ROOT="+instanceRoot,
        "NIWA_TASK_ID="+taskID,
        "NIWA_SESSION_ROLE="+role,
    )
    return env
}
```

Pass-through is the default because workers must behave like a direct
user `claude -p` invocation — they need `HOME`, `PATH`, locale vars,
`ANTHROPIC_API_KEY` (if set), `XDG_*`, etc. Niwa overwrites only the
three keys it owns. `NIWA_SESSION_ID` is actively scrubbed because
workers do not register as sessions and any inherited value would
mislead downstream consumers.

Daemon detaches the worker into a new process group (`Setpgid: true`).
The worker's PID is recorded in `.niwa/tasks/<task-id>/state.json`
immediately after `cmd.Start()`. The daemon does **not** call
`Process.Release()` — it must `waitpid` the worker to receive exit
status for the unexpected-exit detection path (R34). On daemon crash,
the new daemon at startup loses the ability to `waitpid` the orphan
and falls back to a PID liveness poll (Decision 2, already handled by
the daemon-architecture decision).

**Rationale**

- **PRD alignment.** R32 (bootstrap prompt literal, no body), R33
  (three fixed flags, CWD rule), R51 (`NIWA_WORKER_SPAWN_COMMAND` as
  env-var-style binary override), and AC-D4/AC-D5 all fall out of Alt
  1 without interpretation gaps.

- **Dual-channel task-ID propagation is load-bearing.** The LLM needs
  the task ID in its prompt context; the MCP server (a different
  process that only inherits env) needs it independently. Picking one
  channel only breaks one of the two consumers. The two-channel
  approach has near-zero cost (one extra `append` to the env slice)
  and removes a class of failure.

- **Path-only override beats template.** A template-with-substitution
  shape (`"claude --flag {task_id}"`) requires niwa to implement
  shell-style parsing (`strings.Fields` is not enough for quoted
  paths), and it either strips or duplicates R33's fixed flags. A
  path-only override lets niwa reuse one argv-construction path for
  all spawns (real, override, test); users who want argv reshaping
  write a wrapper script that sees the niwa-generated argv and does
  what it wants. The wrapper pattern is already idiomatic for shell
  tool integration.

- **Pass-through env with niwa overwrites beats minimal whitelist.**
  Claude needs environment niwa cannot fully enumerate. A whitelist
  forces every new claude env-var requirement to become a niwa patch.
  Overwriting the niwa-owned keys via last-wins in `cmd.Env` gives
  the same safety without the enumeration burden.

- **Real-vs-test symmetry.** The override binary receives the same
  argv and env as real `claude -p`. Tests assert on captured
  invocations using the same expectations as production; users
  reading test logs see exactly what niwa sends to production claude.

**Alternatives Considered**

- **Alt 2 — Shell-style command template with `{task_id}` / `{role}`
  substitution.** Rejected: (a) strips R33's fixed flags for the
  override path, making functional tests and user wrappers receive a
  different spawn shape than production — violating AC-D4 unless the
  template duplicates the flags; (b) requires shell parsing niwa does
  not have in stdlib; (c) loses the `NIWA_TASK_ID` env propagation
  path, pushing the MCP-server task-ID read onto `/proc/<ppid>/cmdline`
  parsing which is Linux-only.

- **Alt 3 — Argv + JSON envelope on stdin.** Rejected: the upstream
  PRD explicitly rejected stdin envelope delivery (known `claude -p`
  bug where stdin above ~7KB returns empty output); the minimal env
  whitelist bundled with this alternative adds maintenance cost
  without benefit over pass-through with overwrites.

- **Alt 4 — CLI flag on `niwa mesh watch` instead of env var.**
  Rejected: contradicts R51's env-var form; functional tests run the
  daemon indirectly via `niwa apply` and cannot thread a CLI flag
  through cleanly; shell-profile users prefer persistent env over per-
  invocation flag syntax.

**Consequences**

- `internal/cli/mesh_watch.go` keeps the `exec.LookPath("claude")` lookup
  pattern but gates it on `NIWA_WORKER_SPAWN_COMMAND` first. The
  existing `exec.Command(...).Start()` invocation shape is preserved;
  `Process.Release()` is removed (the daemon waits on workers now).
- A new helper `buildWorkerEnv(base, instanceRoot, taskID, role)` in
  (e.g.) `internal/cli/mesh_spawn.go` centralizes env construction and
  scrubbing. Unit-testable in isolation.
- Tests add a small `fake-claude-p` binary under
  `test/functional/helpers/fake-claude-p/` compiled at test-suite
  init. The binary reads `NIWA_TASK_ID`, `NIWA_INSTANCE_ROOT`, its own
  argv, and scripts MCP calls via the MCP server spawned by
  `.mcp.json`. Functional tests set
  `NIWA_WORKER_SPAWN_COMMAND=<path-to-fake>` at the start of a scenario.
- The MCP server (per-worker-process, per Decision 3's leading option)
  gains a single env read: `taskID := os.Getenv("NIWA_TASK_ID")`. If
  empty, the server is running under a coordinator (no task scope)
  and uses `NIWA_SESSION_ROLE` for role-scoped authorization as
  today. If set, the server is a worker and authorization-gated tools
  accept only calls for that task ID.
- Users who want to wrap `claude` (e.g., for logging, for an internal
  binary path) write a shell script that forwards all argv and env.
  No new config or flag is needed.
- What becomes easier: deterministic testability (AC-D4, AC-D5 are
  straightforward); env-based diagnostics (`NIWA_TASK_ID` appears in
  `ps auxe`).
- What becomes harder: nothing meaningful — the chosen shape is
  already the niwa idiom. The only ongoing discipline is adding
  `NIWA_*` scrubbing when a new per-session env var is introduced;
  the current design has one (`NIWA_SESSION_ID`) and will gain more
  only if the Decision 3 outcome introduces them.
<!-- decision:end -->
