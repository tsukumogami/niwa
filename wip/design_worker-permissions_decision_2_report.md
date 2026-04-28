<!-- decision:start id="worker-permission-resolution" status="assumed" -->
### Decision: Worker permission resolution at spawn time

**Context**

niwa's mesh daemon spawns worker Claude Code sessions via `spawnWorker()` in
`internal/cli/mesh_watch.go`. The spawn call currently hardcodes
`--permission-mode=acceptEdits`. The design adds a richer permission config
derived from the coordinator's configured mode, which `niwa apply` materializes
into `<instanceRoot>/.claude/settings.local.json` as
`permissions.defaultMode` (values: `"bypassPermissions"` or
`"askPermissions"`).

The daemon starts once and may run for hours or days. `niwa apply` re-runs
whenever the user updates workspace config, re-materializing
`settings.local.json`. No mechanism currently notifies the daemon of changes
to that file.

The workspace package (`internal/workspace`) already owns all
materialization logic and the mapping between niwa permission strings and
Claude Code flag values (`permissionsMapping`). It also already exposes
per-spawn helpers — `WorkerMCPConfig` and `WorkerMCPConfigPath` — that
`spawnWorker` calls today.

**Assumptions**

- The permission mode to pass to workers derives from
  `settings.local.json` (the materialized coordinator config). If Decision 1
  chooses a hardcoded permission scope (e.g., always `bypassPermissions`),
  this resolution mechanism is not needed and the spawn call is simply updated
  to the fixed value.
- No existing function in the workspace package reads back the materialized
  permission mode. If one exists, the implementation calls it directly.

**Chosen: Option D — New package-level function in workspace package**

Add `WorkerPermissionMode(instanceRoot string) string` to
`internal/workspace`. The function reads
`<instanceRoot>/.claude/settings.local.json`, extracts
`.permissions.defaultMode`, and returns the corresponding `--permission-mode`
flag value. If the file is absent, unreadable, or has no permissions key, it
returns `"acceptEdits"` to preserve current behavior. `spawnWorker` calls
this function at spawn time and uses the result in place of the hardcoded
`"--permission-mode=acceptEdits"` argument.

**Rationale**

Option D reads the current file at each spawn (never stale) and places all
`settings.local.json` knowledge in the workspace package, which already owns
it. This mirrors the existing `WorkerMCPConfig` pattern: workspace provides
a per-spawn helper, the daemon calls it. The function is independently
testable with a temp-dir fixture, and `mesh_watch.go` needs only a single
call-site change. Option B's startup-caching approach creates a real staleness
hazard — a daemon started before the user tightens permissions would keep
spawning workers with the old (wider) permission mode for the rest of its
lifetime with no way to correct it short of a daemon restart. Option C
requires the coordinator agent to carry workspace-config knowledge on every
delegation call, coupling agent behavior to infrastructure config, and adds a
schema field to `TaskEnvelope`. Options A and D are functionally equivalent;
D is preferred because it avoids duplicating the mapping logic and improves
testability.

**Alternatives Considered**

- **Option A (inline read in spawnWorker)**: Read `settings.local.json`
  directly inside `spawnWorker` without a workspace package abstraction.
  Always current but duplicates mapping logic already in the workspace package
  and is harder to unit-test in isolation. Strictly weaker than Option D.
- **Option B (cache in spawnContext at startup)**: Read once at daemon init and
  store in `spawnContext`. Zero per-spawn I/O, but stale whenever the user
  runs `niwa apply` during the daemon's lifetime. The staleness is
  asymmetric — if the user tightens permissions, workers keep the wider mode
  until daemon restart. Rejected because the daemon's long-running nature
  makes this risk likely to manifest.
- **Option C (pass via task envelope)**: Include `permission_mode` in the
  `TaskEnvelope.Body` when the coordinator calls `niwa_delegate`. Allows
  per-task control but requires the coordinator agent to know and pass a
  workspace-config value on every delegation, adding coordinator coupling.
  Also requires a new schema field, additional file reads in `spawnWorker`,
  and updates to all existing coordinators and functional tests. Rejected as
  over-engineered for a value that belongs to workspace config, not task
  payloads.

**Consequences**

- `internal/workspace` gains a new exported function `WorkerPermissionMode`.
  It is independently unit-testable and reuses or mirrors `permissionsMapping`.
- `spawnWorker` in `mesh_watch.go` gains one function call per spawn;
  structural shape of the function is unchanged.
- The daemon correctly picks up permission-mode changes after `niwa apply`
  without a restart, since the file is read fresh each spawn.
- `ClaudeAllowedTools` (used by functional tests) is unaffected.
- Workspaces that have no `[claude.settings] permissions` stanza continue to
  receive `--permission-mode=acceptEdits` (the current default), preserving
  backward compatibility.
<!-- decision:end -->
