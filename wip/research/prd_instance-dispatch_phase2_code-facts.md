# PRD instance-dispatch — Phase 2 code facts

Grounding research for a niwa `dispatch` command. All citations are
`file:line` against the niwa worktree at
`.niwa/worktrees/niwa-ed11e932`.

---

## Q1 — Concurrency on instance naming (HIGHEST STAKES)

**There is NO file lock and NO atomic reservation on instance number/name.**
Two concurrent unnamed creates can pick the same number and/or name; the only
safety is naming the instance after the session id.

Evidence:

- `NextInstanceNumber` (`internal/workspace/state.go:565-586`) scans existing
  instance dirs, builds a `used` map, and returns the lowest unused int. It is a
  pure read-then-return-int with **no lock, no reservation, no write**. Two
  concurrent callers race: both enumerate, both see the same gap, both return
  the same number. (Note: `applier.Create` does NOT call `NextInstanceNumber`;
  the number is derived from the *name* via `instanceNumberFromName`,
  `apply.go:369`. `NextInstanceNumber` is used elsewhere, e.g. bootstrap.)

- `computeInstanceName` (`internal/cli/create.go:70-94`) is the actual create
  naming path. With no `--name`, it `os.Stat`s `<root>/<configName>`, then loops
  `n := 2; ; n++` stat-ing `<configName>-<n>` and returns the first
  non-existent. This is **TOCTOU**: the stat-miss is not followed by any
  reservation. Two concurrent unnamed creates both stat-miss the same candidate
  and both return it.

- `applier.Create` (`internal/workspace/apply.go:268-274`) does
  `os.MkdirAll(instanceRoot, 0o755)`. `MkdirAll` is **not** exclusive (it
  succeeds if the dir already exists), so it does NOT catch the collision — both
  racers proceed into the same directory and clobber each other. `create.go`
  has a pre-check `os.Stat(instanceDir); err == nil -> "already exists"`
  (`create.go:156-159`) but that is also TOCTOU and runs before the (non-atomic)
  MkdirAll.

- **The hook avoids this race by naming from the session id, not the number.**
  `runInstanceHookStart` takes `namePrefix := payload.SessionID[:12]`
  (`internal/cli/instance_from_hook.go:158`, const `sessionNamePrefixLen = 12`
  at `:83`) and passes it as the `customName` to `computeInstanceName`, which
  takes the early `customName != ""` branch (`create.go:71-73`) returning
  `<config>-<prefix>` directly — **never touching the numbered-scan loop**. The
  12-char UUID prefix makes collision negligible. The code comment at
  `instance_from_hook.go:79-83` states this explicitly: the prefix
  "sidestep[s] the NextInstanceNumber race an unnamed concurrent create would
  hit (DESIGN Decision 5)."

**Implication for `dispatch`:** At dispatch time there is **no session id yet** —
the instance is created BEFORE `claude --bg` launches (the session id only
exists after Claude starts; the hook path works because it runs *inside* an
already-started session). So `dispatch` **cannot** reuse the hook's
name-from-session-id trick. Options the PRD must choose among:

- Generate a fresh unique token at dispatch time (e.g. a random/UUID suffix or
  timestamp) and pass it as `--name`, taking the same collision-free
  `customName` branch. This is the only path that is concurrency-safe with the
  current code, since it bypasses the racy numbered scan.
- Relying on the numbered scan (`computeInstanceName` default branch) is NOT
  concurrency-safe: no lock exists anywhere in the create path. Adding a lock
  would be net-new work.

---

## Q2 — Mapping schema (`internal/workspace/session_map.go`)

**`SessionMapping` fields** (`session_map.go:49-60`):

| Field | JSON | Type | Notes |
|-------|------|------|-------|
| `SessionID` | `session_id` | string | liveness key |
| `InstanceName` | `instance_name` | string | |
| `InstancePath` | `instance_path` | string | reaper join key |
| `TranscriptPath` | `transcript_path` | string | |
| `Created` | `created` | time.Time | stamped to now if zero on write |
| `Ephemeral` | `ephemeral` | bool | load-bearing reaper guard |
| `Label` | `label,omitempty` | string | optional human alias; "metadata only, never renames the dir" (`:56-59`) |

**No launch-origin / source marker field exists.** There is no field recording
*how* or *why* the instance was created (no "dispatched" vs "hook" vs "manual"
discriminator). If `dispatch` needs to distinguish its instances from
SessionStart-hook instances, a new field would have to be added. The closest
existing signal is `Ephemeral` (true/false), but both the hook and a dispatch
would presumably set it true. `Label` is free-form metadata and could carry an
origin tag, but nothing reads it for control flow today.

**Validation in `WriteSessionMapping`** (`session_map.go:83-109`): it calls
`sessionMappingPath` (`:71-76`) which rejects via `ValidSessionID` (`:25-27`)
**before constructing any path**. `ValidSessionID` matches the regex
`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`
(`:20`) — a **lowercase-hex canonical UUID (8-4-4-4-12)**. It rejects: uppercase
hex, non-hex chars, wrong segment lengths, missing/extra hyphens, empty string,
anything non-UUID. The write is atomic (temp-then-rename, `:101-107`) and
`Created` is stamped to `time.Now().UTC()` when zero (`:88-90`).

**Can a mapping be written for an instance that exists but has no live session
yet?** Yes, mechanically — `WriteSessionMapping` validates *only* the session id
syntax. It does **not** check that the instance exists, that a job-state file
exists, or that any session is live. It writes whatever `SessionMapping` struct
it is handed (provided `SessionID` is a syntactically valid UUID). So a
`dispatch` flow could create the instance, then write a mapping referencing it,
before/without a live Claude session — the schema and writer permit it. (The
constraint is purely that you must already have a valid UUID to key on, which at
dispatch time you do not yet have until Claude assigns one.)

---

## Q3 — Reaper liveness for a finished session (HIGHEST STAKES)

**YES — a session with job-state `state:"done"` is treated as DEAD and is
reapable.** Confirmed two ways:

- `terminalJobStates` (`internal/cli/job_state.go:42-55`) explicitly includes
  `"done": true` (line 44). Also in the set: `completed, complete, done,
  finished, failed, error, errored, canceled, cancelled, timeout, timedout,
  killed`. Matched **case-insensitively** after trim
  (`strings.ToLower(strings.TrimSpace(js.State))`, `job_state.go:102`).

- `sessionLive` (`job_state.go:86-109`) returns **false (DEAD)** when the state
  is terminal (`:102-104`). `done` is terminal, so `sessionLive` returns false.

**ALL conditions `sessionLive` evaluates** (`job_state.go:86-109`):

1. `jobsDir == ""` (HOME unresolved) → **false/DEAD** (`:87-89`).
2. Job-state file missing / undecodable → **false/DEAD** (`:90-93`, via
   `readJobState`).
3. Recorded `sessionId` present and `!= sessionID` (prefix-collision guard) →
   **false/DEAD** (`:99-101`).
4. `State` is in `terminalJobStates` (case-insensitive) → **false/DEAD**
   (`:102-104`). **This is where `done` lands.**
5. `UpdatedAt` non-zero AND `now.Sub(UpdatedAt) > jobLivenessTTL` → **false/DEAD**
   (`:105-107`).
6. Otherwise → **true/LIVE** (`:108`).

**TTL constant:** `jobLivenessTTL = 30 * time.Minute` (`job_state.go:18`). It is
the backstop for unrecognized terminal labels; `done` does not need it because
it is in the explicit terminal set.

**Confirmation of the full reap chain** for an `Ephemeral:true` mapping whose
session is `done`:

- `selectReapTargets` (`internal/cli/reap.go:90-145`) requires BOTH
  `rec.Ephemeral` (`:112`) AND `mapping.Ephemeral` (`:129`) AND
  `!sessionLive(...)` (`:133`). With `state:"done"` → `sessionLive` is false →
  the `continue`-spare branch is skipped → the instance is appended as a target
  (`:138-141`).
- `reapWorkspace` (`reap.go:153-174`) then calls `destroyInstanceFunc` on the
  target (`:161`) and `DeleteSessionMapping` (`:167`), incrementing the reaped
  count. **So yes: a reap run reclaims an `Ephemeral:true` instance whose
  session is `state:"done"`.**
- Caveat for `dispatch`: this requires the instance's *on-disk record* to also
  be ephemeral (`EnumerateInstanceRecords` → `rec.Ephemeral`, `reap.go:91,112`)
  AND a mapping joined by `instance_path` (`reap.go:100-105,116`). A dispatch
  that creates an instance but writes no mapping, or marks neither ephemeral,
  would NOT be reaped.

---

## Q4 — `applier.Create` contract

**Signatures:**

- `func (a *Applier) Create(ctx context.Context, cfg *config.WorkspaceConfig,
  configDir, workspaceRoot, instanceName string) (string, error)`
  (`internal/workspace/apply.go:268`). Returns the **instance directory path**
  (`instanceRoot`, returned at `apply.go:404`) and an error.
- `func realProvisionInstance(ctx context.Context, workspaceRoot, cwd,
  namePrefix string) (provisionResult, error)`
  (`internal/cli/instance_from_hook.go:364`). `provisionResult{Name, Path}`
  (`:90-93`). It discovers config from `cwd`, resolves the config name, computes
  the instance name from `namePrefix` (the `--name` suffix), builds an
  `Applier`, and calls `applier.Create` (`:402`), returning name + path.
- `func NewApplier(gh github.Client) *Applier` (`apply.go:137`).

**Does it materialize env (GH_TOKEN/claude.env) into the instance tree?** Yes —
`Create` runs the full pipeline via `a.runPipeline(...)` (`apply.go:328-336`),
which produces `result.shadows` / `result.managedFiles` written into the
instance state and tree. The config's `claude.env.vars` / `claude.env.secrets`
(and repo/instance overlays) are the env surface that the materializer
shadows into the instance (`internal/workspace/shadows.go:63-64,125`;
`required.go:58-78`). Vault `vault://` secrets resolve here too, gated by
`AllowMissingSecrets` / `AllowPlaintextSecrets` (`create.go:171-172`). GH token
for cloning comes from `resolveGitHubToken()` → `github.NewAPIClient(token)`
(`create.go:161-162`, `instance_from_hook.go:386-387`), used by the clone, not
necessarily written into the tree as `GH_TOKEN` unless the config declares it as
an env var. **Net: env declared in the workspace config is materialized into the
instance tree during Create; the GH token is consumed for cloning.**

**Atomicity / half-materialized dirs:** Create is **NOT transactional**, but it
**self-cleans on failure**. `os.MkdirAll(instanceRoot)` is non-exclusive
(`apply.go:272`). On every failure after the dir is made — gitignore
(`:280-283`), config snapshot refresh (`:299-302`), effective-name resolution
(`:309-313`), and the pipeline itself (`:337-340`) — it calls
`os.RemoveAll(instanceRoot)` before returning the error. **However**, if
`SaveState` fails (`:389-391`) it returns the error **without** RemoveAll, and a
crash/kill mid-pipeline leaves the partially-built dir behind. So: clean errors
roll back; hard failures (panic, SIGKILL, SaveState error) can leave a
half-materialized instance dir. The reaper does NOT clean these (no mapping, not
flagged ephemeral on disk) — only the numbered-scan / `os.Stat` "already exists"
checks would later trip over it.

**Does `Create` call `reapOpportunistically`?** No. `applier.Create` itself does
not. The opportunistic reap is invoked by the **CLI command** `runCreate`
(`internal/cli/create.go:141`) *before* calling `applier.Create` (`:188`).
`realProvisionInstance` (the hook's provisioner) does **NOT** call
`reapOpportunistically` at all. So a `dispatch` built on `applier.Create`
directly would not get opportunistic reaping for free — it must call
`reapOpportunistically(workspaceRoot)` itself if desired (`reap.go:181-192`,
swallows all errors, never blocks).

---

## Q5 — Workspace resolution (`ClassifyCwd`, `internal/workspace/cwd_classify.go`)

`ClassifyCwd(cwd) (CwdClassification, error)` (`cwd_classify.go:86-141`) returns
a `CwdClassification` struct (`:63-68`) with fields:

- `Class CwdClass`
- `WorkspaceRoot string` — absolute, populated for the three in-workspace classes
- `InstanceDir string` — populated for inside-instance and inside-worktree
- `WorktreeDir string` — populated for inside-worktree only

The four classes (`:16-38`), in resolution order (most specific first):

| cwd location | Class | WorkspaceRoot | InstanceDir | WorktreeDir |
|---|---|---|---|---|
| inside a session worktree (`<inst>/.niwa/worktrees/<name>/...`) | `CwdInsideWorktree` (`:97-109`) | set | set | set |
| inside an instance (not its worktree) | `CwdInsideInstance` (`:112-128`) | set | set | "" |
| at/inside workspace root, not an instance | `CwdAtWorkspaceRoot` (`:132-137`) | set | "" | "" |
| outside any workspace | `CwdOutside` (`:140`) | "" | "" | "" |
| inside a repo | — (no dedicated class) | depends | depends | depends |

**"Inside a repo" has no dedicated class.** A repo dir lives under either an
instance or a worktree, so it classifies as `CwdInsideInstance` or
`CwdInsideWorktree` depending on which subtree it sits in (repos under
`<inst>/<repo>` → inside-instance; repos under a worktree → inside-worktree).
A bare repo *outside* any niwa workspace → `CwdOutside`.

It errors only on filesystem-resolution failures (`filepath.Abs`), not on
"outside niwa" — that returns `CwdOutside` with empty paths (`:81-83,140`).

`String()` values (`:42-55`): `inside-instance`, `at-workspace-root`,
`inside-worktree`, `outside`.

**For `dispatch`:** the command will need to handle launch from
`CwdAtWorkspaceRoot` (the expected case — dispatch a worker from the workspace
root) and refuse/handle re-entrancy from `CwdInsideInstance`/`CwdInsideWorktree`
(mirroring the hook's re-entrancy guard, `instance_from_hook.go:266-270`, which
no-ops when `DiscoverInstance`+`ValidateInstanceDir` succeed). `resolveHook
WorkspaceRoot` (`instance_from_hook.go:346-358`) shows the pattern: classify,
require non-empty `WorkspaceRoot`.

---

## Q6 — Existing command precedent: `niwa session attach`

The generalization target lives in `internal/cli/sessionattach/`. The exec
itself is `Supervise` (`supervise.go:35-77`):

- **Binary:** `opts.ClaudeBin`, or `exec.LookPath("claude")` when empty
  (`supervise.go:36-43`).
- **Args:** `exec.CommandContext(ctx, bin, "--resume", opts.ConvID)`
  (`supervise.go:44`). Hard-coded `--resume <conv_id>` — NOT `--bg`. A dispatch
  command would generalize the arg list (e.g. swap in `--bg`/headless flags and
  the prompt).
- **cmd.Dir:** `opts.WorkerCWD` (`supervise.go:45`). For attach this is
  `<worktreePath>/<repo>` (`attach.go:117`). For dispatch this would be the
  instance root (or a repo within it).
- **Env:** **NOT set** — `cmd.Env` is never assigned, so the child **inherits
  the parent process environment** as-is (`supervise.go:44-52` set Dir, Stdin,
  Stdout, Stderr, SysProcAttr but no Env). Materialized instance env lives as
  files in the tree (per Q4), not injected via `cmd.Env`.
- **stdio:** **streamed/inherited, NOT captured.** `cmd.Stdin/Stdout/Stderr` are
  wired to `opts.Stdin/Stdout/Stderr`, which default to `os.Stdin/Stdout/Stderr`
  (`supervise.go:46-48`, `stdinOrDefault`/`stdoutOrDefault`/`stderrOrDefault`
  `:79-98`). It does **not** use `cmd.Output()` / `CombinedOutput()` / a
  `bytes.Buffer`. This is an **interactive foreground** supervision model:
  `cmd.Start()` then block in a select loop forwarding SIGINT/SIGTERM/SIGHUP to
  the child's process group until `cmd.Wait()` returns (`:54-77`).
- **Process group:** `SysProcAttr{Setpgid: true}` (`supervise.go:52`) so signals
  target the child's group. Comment notes this "matches how niwa spawns workers
  (Setsid=true makes PID == PGID)" (`:50-52`).
- **Exit code:** `exitCodeFromWaitErr` propagates the child code capped at 125
  (`:101-118`).

**Gap vs `dispatch` requirement:** the PRD wants to launch `claude --bg` and
**capture stdout** (to read back the session id / job handle). `Supervise` today
**streams** stdio to the terminal and **blocks** until the child exits — neither
the capture nor the fire-and-return (background) behavior exists. To generalize:
(a) parameterize the arg list (currently fixed `--resume <id>`), (b) add a
capture mode that sets `cmd.Stdout` to a buffer/pipe instead of inheriting, and
(c) add a non-blocking/background launch path (current code always
`cmd.Wait()`s synchronously). The `AttachRun` wrapper (`attach.go:36-171`) also
carries an flock-based in-use lock (`attach.go:75-108,178-191`), preflight, and
attach-sentinel state that are attach-specific and would not apply to a fresh
background dispatch.
