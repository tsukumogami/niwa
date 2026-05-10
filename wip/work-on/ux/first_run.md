# niwa first-run UX research

Audience: implementer of the first-run polish pass that lands alongside the
mesh reliability redesign.

Sources cited inline as `path:line`. All paths absolute under the workspace
root `/home/dgazineu/dev/niwaw/tsuku/tsuku-3/public/niwa/`.

Empirical observations come from a sandbox build of `cmd/niwa` (the binary
at `/tmp/niwa-ux-test/niwa`) run against three scenarios:

- `niwa init` (no args, scaffold)
- `niwa init <name>` then `niwa apply` (no sources, no channels)
- `niwa init <name>`, edit `[channels.mesh]` in, then `niwa apply`

Plus failure paths: bad TOML, unreachable `--from` URL.

## 1. Step-by-step trace

### Phase A: `niwa init <name>`

Code path: `internal/cli/init.go:130-362`.

1. **Argument validation.** `ValidateInitName(args[0])` runs before mode
   resolution so an empty positional arg is rejected (`init.go:154-158`).
2. **Mode resolution.** `resolveInitMode` selects one of `modeScaffold`,
   `modeNamed`, `modeClone` (`init.go:106-128`). With `<name>` and no
   `--from`, mode is `modeNamed` unless the registry already has a
   SourceURL for that name.
3. **Preflight target check.** `preflightTargetExists(workspaceRoot)`
   returns one of three errors: `ErrWorkspaceExists`,
   `ErrNiwaDirectoryExists`, `ErrTargetDirExists`
   (`init.go:375-418`). Each error includes a `Detail` and `Suggestion`.
4. **Workspace creation.** `os.Mkdir(workspaceRoot, 0o755)` — note the
   `Mkdir` rather than `MkdirAll`, deliberate to avoid a TOCTOU window
   (`init.go:207-211`).
5. **Scaffold or clone.** `workspace.Scaffold(workspaceRoot, name)`
   writes `.niwa/workspace.toml` from a commented template at
   `internal/workspace/scaffold.go:11-89` and creates `.niwa/claude/`.
6. **Post-flight load.** `config.Load(configPath)` parses the freshly
   written file (`init.go:254-258`). Failures here delete nothing —
   the partial workspace is left on disk.
7. **Registry update.** `globalCfg.SetRegistryEntry` writes to
   `~/.config/niwa/config.toml` (`init.go:295-298`); failure becomes a
   warning not a hard error.
8. **Instance state.** `buildInitState` writes
   `<workspaceRoot>/.niwa/instance.json` whenever any state flag is
   set, the positional name is given, or mode is clone
   (`init.go:527-592`).
9. **Success message.** `printSuccess` emits to stdout
   (`init.go:600-630`):

   ```
   Workspace "<name>" initialized at <abs-path>.

   Next steps:
     1. Edit .niwa/workspace.toml to configure sources and groups
     2. Run niwa apply to set up the workspace
   ```

10. **Shell wrapper integration.** `writeLandingPath(absForMsg)` writes
    the resolved root to `NIWA_RESPONSE_FILE` so a sourced shell
    wrapper `cd`s the user in (`init.go:356-360`).

Empirical output (sandbox, no `--from`):

```
$ niwa init my-test
Workspace "my-test" initialized at /tmp/niwa-ux-test/my-test.

Next steps:
  1. Edit .niwa/workspace.toml to configure sources and groups
  2. Run niwa apply to set up the workspace
```

Files left on disk:

```
my-test/.niwa/workspace.toml
my-test/.niwa/instance.json
my-test/.niwa/claude/        (empty dir)
```

### Phase B: `niwa apply`

Code path: `internal/cli/apply.go:73-187` then
`internal/workspace/apply.go:329-489`.

1. **Scope resolution.** `ResolveApplyScope(cwd, applyInstance)` picks
   target instances from cwd or `--instance` flag (`apply.go:84-89`).
2. **Config load and warnings.** `config.Load(configPath)` returns a
   `Result` whose `Warnings` are streamed to stderr
   (`apply.go:100-106`).
3. **URL change check.** `checkConfigSourceURLChange` refuses if the
   registered URL differs from the on-disk source
   (`apply.go:112-114`, full implementation `apply.go:310-346`).
4. **Applier construction.** `workspace.NewApplier(gh)` wires hooks,
   settings, env, files materializers and a TTY-aware reporter
   (`workspace/apply.go:106-125`).
5. **Pipeline.** `Apply()` -> `runPipeline()`
   (`workspace/apply.go:329-489`, then `:494-1382`). Steps observed in
   the code, in order:
   - 0: `injectChannelHooks` mutates `cfg.Claude.Hooks` if channels
     enabled (`apply.go:501`).
   - 0.3: optional personal-overlay snapshot sync
     (`apply.go:589-617`); emits `syncing config...` status line.
   - 0.4-0.6: vault credential-sync provider open and workspace
     overlay sync; `Status("syncing config...")` for the personal
     overlay only (`apply.go:591`). The workspace overlay sync has no
     status line.
   - 1: `discoverAllRepos` enumerates GitHub orgs (`apply.go:820`).
   - 2: `Classify` and `InjectExplicitRepos` (`apply.go:841-852`).
   - 3: `Status("cloning repos... (0/N done)")` then bumped per repo
     (`apply.go:1118, 1135`).
   - 4: `InstallWorkspaceContent` writes content `CLAUDE.md`s
     (`apply.go:1143`).
   - 4.5: `InstallWorkspaceContext` and
     `InstallWorkspaceRootSettings` write `workspace-context.md` and
     `.claude/settings.json` at instance root
     (`apply.go:1164, 1183`).
   - 4.75: `InstallChannelInfrastructure` writes `.mcp.json`,
     `.niwa/sessions/sessions.json`, `.niwa/daemon.pid`,
     `.niwa/daemon.log`, `.niwa/hooks/*`, role inboxes, and the
     instance-root + per-repo `niwa-mesh/SKILL.md`
     (`workspace/channels.go:231-414`). All writes silent unless
     drift is detected by `writeIdempotent`
     (`channels.go:919-951`).
   - 5: `InstallGroupContent` per group (`apply.go:1210`).
   - 5c: `InstallGlobalClaudeContent` if global config dir set
     (`apply.go:1219`).
   - 6: `InstallRepoContent` per repo (`apply.go:1233`).
   - 6.5: materializers (hooks, settings, env, files) per repo
     (`apply.go:1320`).
   - 6.75: `RunSetupScripts` per repo (`apply.go:1332`). Failures land
     as deferred warnings.
   - 7: hash and persist `ManagedFile` entries (`apply.go:1352-1369`).
6. **State save.** `SaveState(instanceRoot, state)`
   (`workspace/apply.go:466`).
7. **Daemon spawn.** `EnsureDaemonRunning(instanceRoot, nil)` if
   channels enabled (`workspace/apply.go:471-475`). 500 ms PID-file
   poll; timeout returns nil silently
   (`workspace/daemon.go:91-101`).
8. **Summary.** `applied <name> (N repos)` permanent line
   (`workspace/apply.go:478-482`), then deferred warnings flush
   (`apply.go:483-486`).

Empirical output (sandbox, no sources, channels enabled):

```
$ niwa --no-progress apply
warning: could not refresh config snapshot for git@github.com:dangazineu/dot-niwa.git: github: HeadCommit returned 404; using cached snapshot fetched at 2026-05-10T03:22:21Z
warning: no git remotes detected; public-repo guardrail skipped
applied mesh-test (0 repos)
```

The two warnings come from the personal-overlay sync attempt
(`workspace/apply.go:595`) and the public-repo guardrail
(`workspace/guardrail.go`-equivalent gate). Neither is recoverable from
the message alone; both fire on every apply against this workspace.

Files actually created on first apply with `[channels.mesh]` enabled
(verified by `find` on the sandbox workspace):

```
.claude/rules/workspace-imports.md
.claude/settings.json
.claude/skills/niwa-mesh/SKILL.md
.mcp.json
.niwa/daemon.log
.niwa/daemon.pid
.niwa/daemon.pid.lock
.niwa/hooks/mesh-session-start.sh
.niwa/hooks/mesh-user-prompt-submit.sh
.niwa/hooks/stop/report-progress.sh
.niwa/instance.json
.niwa/roles/coordinator/inbox/{cancelled,expired,in-progress,read}/
.niwa/sessions/sessions.json
.niwa/tasks/
.niwa/workspace.toml
workspace-context.md
```

The user is told `applied mesh-test (0 repos)`. Nothing in the output
mentions any of the 14 files listed above. A daemon process is also
spawned (verified via `ps aux | grep "mesh watch"`) and never named.

### Generated artifacts: workspace-context.md and CLAUDE.local.md

**`workspace-context.md`** is generated by
`workspace/workspace_context.go:346-383`. The output for an
empty-source workspace with channels enabled:

```
# Workspace: mesh-test

You are at the root of a multi-repo workspace managed by niwa. This is NOT
a single git repository -- each subdirectory under the group folders is a
separate git repo.

## Repos

## Working in this workspace
...

## Channels
- Role: coordinator
- NIWA_INSTANCE_ROOT: /tmp/niwa-ux-test/mesh-test
- Tools:
  - niwa_delegate
  - niwa_query_task
  ... (14 entries) ...
See the `/niwa-mesh` skill for usage patterns.
```

`## Repos` is empty for a no-sources workspace — the section header is
written unconditionally even when there are no rows
(`workspace_context.go:365`).

**`CLAUDE.local.md`** is per-repo and is built by
`InstallRepoContent`. There is no top-level `CLAUDE.local.md` at the
instance root — the workspace root uses `workspace-context.md` plus
`.claude/rules/workspace-imports.md` to thread imports without
triggering Claude's external-import dialog
(`workspace_context.go:76-101`). The single-line
`workspace-imports.md` content is `@<absolute path to
workspace-context.md>`.

## 2. Pain points in the current flow

### 2.1 Silent successes that should announce what was done

- **Channel infrastructure write is silent.** Fourteen files,
  multiple directories, and a background daemon are created on first
  `niwa apply` with `[channels.mesh]` enabled, but the only emitted
  line is `applied <name> (0 repos)` (`workspace/apply.go:481`). The
  user is given no signal that:
  - A `.mcp.json` now lives at the instance root and Claude Code
    will find niwa MCP tools when launched from this directory.
  - The mesh watch daemon is running in the background and writes to
    `.niwa/daemon.log`.
  - The `niwa-mesh` skill was installed and is callable.
- **Daemon spawn outcome is hidden.** `EnsureDaemonRunning`
  (`workspace/daemon.go:35-102`) polls 500 ms for `daemon.pid` and
  on timeout returns `nil` with the comment "the missing PID file is
  the observable failure signal" (line 99). The user has to know to
  check `ps`, `daemon.log`, and `.niwa/daemon.pid` to verify the
  daemon. There is no apply-time confirmation that the mesh is ready.
- **`niwa init` does not list the files it created.** A `niwa init`
  with no flags writes `.niwa/workspace.toml`, `.niwa/claude/`, and
  (when a positional `<name>` is given) `.niwa/instance.json`. The
  success message names only `.niwa/workspace.toml`
  (`init.go:622-623`).
- **First-time create vs subsequent apply look identical.** The
  summary line is `applied <name> (N repos)` regardless of whether
  this was the first apply (where every file was newly written) or
  the hundredth (where most files were drift-checked and skipped).
  Reporter has no per-step `Reporter.Log("installed ...")` calls in
  the channels installer (`workspace/channels.go:231-414`).

### 2.2 Verbose noise that hides important signals

- **Cobra usage dump on every error.** A TOML parse failure prints
  the error, then the full `niwa apply --help` flag listing, then the
  error again (verified empirically against a workspace with
  `[[sources]` truncated). The signal is buried between two copies of
  the help text.
- **Default repeat warnings.** Every apply emits
  `warning: could not refresh config snapshot for ...` when the
  personal overlay repo is offline (`workspace/apply.go:595`) and
  `warning: no git remotes detected; public-repo guardrail skipped`
  when the workspace config dir has no git remote. Neither has a
  recovery hint and both fire on every invocation. They train the
  user to ignore stderr warnings entirely.

### 2.3 Error messages without recovery hints

- **Bad `--from` URL leaves an empty directory.**
  `niwa init from-clone --from https://nonexistent.example.com/foo/bar`
  fails after creating `<cwd>/from-clone/`. The error is `materializing
  config repo: EnsureConfigSnapshot: fallback: git clone ...: exit
  status 128 ...`. The directory is left empty on disk and a retry
  with `niwa init from-clone` would now hit the
  `ErrTargetDirExists` preflight at `init.go:413-417`. The user is
  not told to remove the directory.
- **Daemon-spawn timeout is invisible.** Today's
  `EnsureDaemonRunning` returns nil on timeout (`daemon.go:91-101`).
  Apply succeeds. The user has no way to know the mesh is broken
  until they try `niwa_delegate` or look at `daemon.log`. The
  reliability design specs `ErrDaemonSpawnTimeout` and a structured
  error code, but only for `niwa_create_session` flows
  (`docs/designs/DESIGN-niwa-mesh-reliability.md:1045-1051`). The
  apply-time daemon spawn keeps the silent-nil path.
- **Workspace exists / niwa-dir-without-config errors.** The
  `init.go:397-410` paths produce a one-line `Detail` plus a
  `Suggestion`, but the suggestion is concatenated with `\n  `
  (`init.go:171, 198, 408`) so the indent renders as visual prose
  instead of an actionable next-step block.

### 2.4 Surprises and onboarding gaps

- **`niwa init` with no args and no `--from` does NOT register the
  workspace.** Mode `modeScaffold` skips registry update
  (`init.go:272`). A user who runs `niwa init` (no args), then later
  tries `niwa go workspace` from a different directory, gets a
  registry miss with no hint that the original `niwa init` was
  unregistered by design.
- **Two paths for the same outcome.** `niwa init <name>` then
  `niwa apply` is the documented quick start (README.md:36-112). But
  `niwa create` is also presented as a way to "create a workspace
  instance as a subdirectory" (README.md:96-102) — and the only
  user-visible cue distinguishing the two is the flag name. The
  README does not explain when `niwa create` is needed vs the
  `niwa init` + `niwa apply` flow.
- **`Run niwa apply` next-step does not mention sources are
  required.** The success message in `init.go:622` says "Edit
  .niwa/workspace.toml to configure sources and groups" then "Run
  niwa apply". A user who skips the edit step and runs `apply`
  immediately gets `applied <name> (0 repos)` with no error — which
  reads as success. There's no "you have not configured any sources;
  apply was a no-op" hint.
- **Empty `## Repos` heading.** `workspace-context.md` for a
  no-sources workspace contains a literal `## Repos\n\n` followed by
  the next section. From the user's perspective opening Claude in
  the workspace, this looks like a malformed file.

## 3. Mesh-redesign impact

The reliability design
(`docs/designs/DESIGN-niwa-mesh-reliability.md`) proposes three
changes that touch first-run output:

### 3.1 Per-repo `niwa-mesh/SKILL.md` writes are removed

`InstallChannelInfrastructure` currently writes
`<repoPath>/.claude/skills/niwa-mesh/SKILL.md` for every
non-coordinator role on every apply
(`workspace/channels.go:347-359`). The design removes this loop,
keeping only the instance-root copy at `channels.go:341`
(design line 415-419, 896-899).

**First-run output impact:** today's apply has zero output naming
the per-repo skill paths, so removing the write does not orphan any
"skill installed at <path>" line — there isn't one. The drift warning
from `writeIdempotent` (`channels.go:935`) does fire when bytes
differ, and after the design lands an existing repo with a stale
per-repo `SKILL.md` will become "drift" the next time apply scans
managed files. The current `cleanRemovedFiles` pass
(`workspace/apply.go:1386-1399`) should remove the per-repo skill
files because they are still in `existingState.ManagedFiles` from a
previous apply but no longer in the new `result.managedFiles`. Worth
verifying explicitly during PLAN — a lingering `SKILL.md` in a
consumer repo's `.claude/skills/` is exactly the problem the design
is trying to solve.

### 3.2 Worker spawn flag set: `--add-dir`, `--setting-sources`

`spawnWorker` (`internal/cli/mesh_watch.go:982-1001`) currently
invokes `claude -p` with only `--mcp-config`, `--strict-mcp-config`,
`--allowed-tools`, plus the resume/permission flags. The design
adds `--add-dir <workspaceRoot> --add-dir <repoPath>
--setting-sources user,project,local` to every spawn (design line
374-414, 880-884).

**First-run output impact:** the apply pipeline does not announce
what configuration a future spawned worker will see. The apply-time
"## Channels" block in `workspace-context.md`
(`workspace/channels.go:849-856`) lists tools and `NIWA_INSTANCE_ROOT`
but says nothing about which `.claude/` trees are in scope. After
the design lands, a first-run user benefits from a one-line
confirmation that workers will inherit `<workspaceRoot>/.claude/`
and `<repoPath>/.claude/` — without that, the contract change is
invisible until a delegation runs and either succeeds or fails
mysteriously. Suggested surface: a Reporter.Log line at the end of
`InstallChannelInfrastructure` that names the inheritance roots.

### 3.3 `DAEMON_SPAWN_TIMEOUT` rendering

The design returns `ErrDaemonSpawnTimeout` from
`EnsureDaemonRunning` on the 500 ms timeout, propagates it as
`DAEMON_SPAWN_TIMEOUT` from `niwa_create_session`, and rolls back
the worktree/branch/state on that path (design line 1045-1051,
892-894).

**Today, at apply time:**

- `Applier.Apply` calls `EnsureDaemonRunning(instanceRoot, nil)`
  inside an `if cfg.Channels.IsEnabled()` block
  (`workspace/apply.go:471-475`).
- The current return is `nil` on timeout
  (`workspace/daemon.go:91-101`).
- The branch handles all errors as a deferred warning:
  `a.Reporter.DeferWarn("could not start mesh daemon: %v", err)`
  (`apply.go:473`).

After the design lands, that branch will receive
`ErrDaemonSpawnTimeout` instead of `nil`. The deferred warning will
say `could not start mesh daemon: spawn timeout after 500ms` (or
similar) — better than nothing, but still buried under the apply
summary line and lacking a recovery hint. The first-run UX work
should change this branch to a non-deferred error or a structured
`Reporter.Warn` with:

- The daemon log path so the user can read `daemon.log`.
- A retry hint (`niwa apply` again to re-attempt spawn).
- A note that channels-dependent commands (`niwa_delegate`, etc.)
  will fail until the daemon is up.

The `niwa_create_session` MCP rollback path is well-specified in the
design; the apply-time path is not, and it should be — apply is
where most users meet the daemon for the first time.

## 4. Cross-doc consistency

### 4.1 README.md vs implementation

- **`niwa init` description matches code.** README line 39-48
  describes scaffolding `./<name>/` with a commented template; the
  scaffold template at `workspace/scaffold.go:11-89` produces exactly
  that. README line 47-48 acknowledges the shell wrapper integration
  that `init.go:356-360` writes.
- **`niwa apply` description partially matches.** README line
  106-112 says apply "clones missing repos, regenerates content
  files, and cleans up repos removed from the config." This matches
  the pipeline at `workspace/apply.go:494-1382`. But README does not
  mention:
  - The mesh daemon spawned when channels are enabled
    (`workspace/apply.go:471-475`).
  - The `.mcp.json` materialized at the instance root
    (`workspace/channels.go:328-336`).
  - The `niwa-mesh` skill installed under
    `.claude/skills/niwa-mesh/`
    (`workspace/channels.go:340-345`).
- **`niwa create` is presented in step 5 of the quick start (README
  line 94-102) without explaining how it differs from `niwa init` +
  `niwa apply`.** Users following the quick start in order get to
  `niwa create` after `niwa init` and `niwa apply` — but
  `niwa create` is for adding a parallel instance, not the first
  setup step. The README quick start ordering is misleading.
- **Channels are not in the quick start.** The README's six-step
  quick start (lines 19-112) never mentions `[channels.mesh]`. A
  user following only the README never discovers channels exist —
  they have to find them via `docs/guides/sessions.md` or by reading
  `workspace.toml` template comments
  (`workspace/scaffold.go:66`).

### 4.2 docs/guides vs implementation

- `docs/guides/sessions.md` accurately describes the session
  lifecycle and matches the code at `internal/mcp/handlers_session.go`.
  It is unaffected by the first-run flow.
- `docs/guides/workspace-config-sources.md` carries an
  "Implementation status (April 2026)" caveat at line 7-19 that no
  longer applies — the snapshot model and provenance marker have
  shipped (verified by reading
  `internal/workspace/snapshot.go`,
  `internal/workspace/provenance.go`). The caveat reads as if
  features are still pending. Out of strict first-run scope but
  worth flagging.
- `docs/guides/cross-session-communication.md` exists but is not
  linked from the README quick start. A first-run user with
  `[channels.mesh]` enabled has no obvious entry point to learn the
  mesh contract.

### 4.3 Scaffold template comments vs current schema

`workspace/scaffold.go:11-89` references
`docs/designs/DESIGN-workspace-config.md` (line 40 of template). The
file at `docs/designs/current/DESIGN-workspace-config.md` is the
actual location — the README also points at the same file at its
"Config reference" link (README line 220). The scaffold reference is
unqualified ("docs/designs/DESIGN-workspace-config.md") and would not
resolve from the user's `.niwa/workspace.toml` directory. Minor but
fixable.

## 5. Proposed issues

Each issue listed below is sized for one PR. ACs are written to be
testable against either functional tests
(`test/functional/features/`) or unit tests. Conventional commit
prefixes follow the niwa repo convention (commit log shows
`fix(destroy)`, `feat(mesh)`, `feat(apply)`).

### 5.1 `feat(apply): announce mesh artifacts on first install`

**Goal.** When channels are enabled and
`InstallChannelInfrastructure` writes its 14 artifacts on a fresh
apply, surface a single-block summary on stderr so the user knows
what was installed and where to look next.

**Acceptance criteria:**

1. On a workspace where `[channels.mesh]` is freshly enabled (no
   prior `daemon.pid`, no prior `.mcp.json`), `niwa apply` emits a
   block listing: the instance-root `.mcp.json` path, the
   `niwa-mesh` skill path, the mesh daemon PID, and a pointer to
   `.niwa/daemon.log`.
2. On a subsequent `niwa apply` to the same workspace, the block
   does NOT appear (idempotent installs stay quiet).
3. The block appears AFTER the existing
   `applied <name> (N repos)` summary, in the deferred-message
   region.
4. The block respects `--no-progress` and TTY detection — non-TTY
   output remains append-only.
5. New unit test in `internal/workspace/apply_test.go` asserts the
   announcement is emitted exactly once across two consecutive
   `Apply()` calls.

### 5.2 `feat(daemon): surface DAEMON_SPAWN_TIMEOUT at apply time`

**Goal.** Once `EnsureDaemonRunning` returns a typed
`ErrDaemonSpawnTimeout` per the mesh reliability design, render that
error at apply time with a specific recovery hint instead of a
generic deferred warning.

**Acceptance criteria:**

1. When `EnsureDaemonRunning` returns `ErrDaemonSpawnTimeout`, the
   apply pipeline emits a non-deferred warning with: the
   instance-root path, the `daemon.log` path, the retry command
   (`niwa apply`), and a note that mesh-dependent commands will
   fail.
2. When `EnsureDaemonRunning` returns any other non-nil error
   (e.g., `cmd.Start()` failure), the existing deferred-warning
   path stays in place — only the typed timeout branch gets the new
   surface.
3. The apply itself still exits zero so subsequent runs can retry
   spawn; the warning is informational not blocking.
4. Functional test seeds an environment that triggers the timeout
   (e.g., synthetic daemon binary that exits before writing
   `daemon.pid`) and asserts the new message is in stderr.

### 5.3 `fix(init): clean up partially-created directory on clone failure`

**Goal.** When `niwa init <name> --from <bad-url>` fails after
creating `<cwd>/<name>/`, remove the directory so the user can retry
without hitting `ErrTargetDirExists`.

**Acceptance criteria:**

1. After a failed `niwa init my-test --from <unreachable>`, the
   directory `<cwd>/my-test/` does NOT exist on disk.
2. The error message names the URL that failed and points at
   `niwa init my-test --from <other-url>` as the retry path.
3. The registry is NOT updated (verified via inspection of
   `~/.config/niwa/config.toml`).
4. The cleanup is best-effort — a removal failure logs a warning but
   does not change the original error code.
5. Unit test in `internal/cli/init_test.go` simulates a clone
   failure and asserts the directory is gone.

### 5.4 `fix(cli): suppress cobra usage dump on application errors`

**Goal.** When `runApply` or `runInit` returns a non-nil error from
the application logic, do not print the cobra `Usage:` flag list —
keep that for argument-parsing errors only.

**Acceptance criteria:**

1. A TOML parse failure during `niwa apply` prints only
   `Error: parsing config: ...` and exits non-zero.
2. A `--help`-style argument error still prints the full usage
   block (cobra's default).
3. Mechanism: set `cmd.SilenceUsage = true` on the apply, init,
   create, and destroy commands; `SilenceErrors = false` so the
   one-liner still prints once.
4. Regression test: existing tests that assert on usage output
   must keep passing for genuine arg-parse failures.

### 5.5 `feat(init): warn when init scaffold ends without a registered workspace`

**Goal.** A no-arg `niwa init` that scaffolds in cwd does not
register a workspace name in the global registry. Today this is
silent; users discover the gap only when `niwa go <name>` or
`niwa apply <name>` fails. Tell the user up front.

**Acceptance criteria:**

1. `niwa init` with no positional name appends a stderr note: "this
   workspace is not registered. Run `niwa init <name>` instead to
   make it discoverable from `niwa go` and `niwa apply <name>`."
2. The note does NOT appear when a positional name is given (the
   `modeNamed` and `modeClone` paths register normally).
3. The note is suppressed by `--no-progress` (it's
   workflow-instructional, not progress).
4. README quick start step 2 (line 36-48) is updated to call out
   the registered-vs-unregistered distinction.

### 5.6 `fix(apply): warn when apply runs against a workspace with zero sources`

**Goal.** Today `niwa apply` against a freshly scaffolded workspace
(all `[[sources]]` blocks still commented out) prints
`applied <name> (0 repos)` and exits zero. This reads as success
but is functionally a no-op.

**Acceptance criteria:**

1. When `cfg.Sources` is empty AND `cfg.Repos` is empty AND no
   overlay contributes repos, `niwa apply` emits a stderr note: "no
   sources or explicit repos configured — edit
   `.niwa/workspace.toml` and re-run `niwa apply`."
2. Exit code stays zero (the apply did not fail; it just had
   nothing to do).
3. The note is suppressed when at least one repo would have been
   classified, even if all were filtered out by groups
   (no spurious notes).
4. New unit test in `internal/workspace/apply_test.go` asserts the
   note for the empty-config case and asserts its absence for a
   single-source case.

### 5.7 `fix(workspace): suppress empty `## Repos` section in workspace-context.md`

**Goal.** When a workspace has zero classified repos,
`generateWorkspaceContext` emits a literal `## Repos\n\n` with no
rows (`workspace_context.go:365`). Skip the section header when
there are no repos.

**Acceptance criteria:**

1. `generateWorkspaceContext` with an empty `classified` slice
   produces a workspace-context.md without a `## Repos` heading.
2. The "Working in this workspace" section is reworded so it does
   not assume any repos exist (or stays as-is if the assumption is
   harmless).
3. With one or more repos, output is unchanged.
4. Unit test in `internal/workspace/workspace_context_test.go`
   asserts both branches.

### 5.8 `docs: add channels to README quick start and link cross-session-communication`

**Goal.** The README quick start (lines 19-112) does not mention
channels or the mesh. Add a short subsection so first-time users
discover the feature, and link the existing
`docs/guides/cross-session-communication.md` and
`docs/guides/sessions.md`.

**Acceptance criteria:**

1. README gains a "Channels (optional)" subsection after step 6
   (line 112) that names `[channels.mesh]` and points at the two
   guides above.
2. README's `## Commands` table gains a row for
   `niwa session list` if not already present (verified — current
   table at line 116 has no session entries).
3. The scaffold template's `[channels]` comment
   (`workspace/scaffold.go:66`) carries a one-line pointer to the
   guides so users editing `workspace.toml` find docs without
   leaving the file.

### 5.9 `feat(apply): document worker config inheritance in mesh-installed message`

**Goal.** Once the mesh-reliability design lands and workers
inherit `<workspaceRoot>/.claude/` and `<repoPath>/.claude/` via
`--add-dir`, surface that contract at apply time so users know what
the mesh worker will see.

**Acceptance criteria:**

1. The first-install announcement from issue 5.1 includes a line
   naming both `--add-dir` roots and the
   `--setting-sources user,project,local` setting.
2. The message does NOT name the literal flags — it names the
   inheritance contract in user terms ("workers see the same Claude
   config a user running `claude` in the repo would see").
3. The line is dropped on subsequent applies (idempotent surface).
4. Depends on issue 5.1 and on the mesh-reliability Phase 3 work
   landing first; gates on a feature-flag if needed.

### 5.10 `docs: align scaffold template doc reference with current path`

**Goal.** The scaffold template at `workspace/scaffold.go:40`
references `docs/designs/DESIGN-workspace-config.md` but the actual
file is at `docs/designs/current/DESIGN-workspace-config.md`. Fix
the reference so users reading the freshly scaffolded
`workspace.toml` find the schema reference on first try.

**Acceptance criteria:**

1. `workspace/scaffold.go:40` points at
   `docs/designs/current/DESIGN-workspace-config.md`.
2. The README link at line 220 is checked for the same path.
3. New unit test in `internal/workspace/scaffold_test.go` asserts
   the path string is present in the template body so future
   schema-doc moves don't silently rot the comment.
