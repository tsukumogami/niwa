# Lead: How does niwa's worktree session mechanism work?

## Findings

### 1. What a worktree session is and what it requires

A niwa session is **not** a generic "branch in a worktree" primitive. It is a
mesh-delegation construct that couples four things together:

1. A new git branch `session/<session-id>` cut from the current HEAD of the
   target repo.
2. A git worktree at
   `<instanceRoot>/.niwa/worktrees/<repo>-<session-id>/`.
3. A per-worktree daemon process (PID file, log, fsnotify watchers).
4. A lifecycle state file at
   `<instanceRoot>/.niwa/sessions/<session-id>.json`.

These four are created together in a single transactional flow with rollback
on failure. There is no public API path that creates only #1+#2 (a branch in a
worktree) without also spinning up #3+#4 (the daemon and lifecycle state).

Source of truth: `docs/guides/sessions.md` (the "What a session is" section
explicitly lists all four steps); implementation in
`internal/mcp/handlers_session.go::handleCreateSession`.

### 2. Who creates sessions today (entry points)

There are three callers, all of which assume an applied workspace already
exists on disk:

| Entry point | File | Purpose |
|---|---|---|
| `niwa session create <repo> <purpose>` | `internal/cli/session_lifecycle_cmd.go::runSessionCreate` | Interactive CLI |
| `niwa_create_session` MCP tool | `internal/mcp/handlers_session.go::handleCreateSession` | Coordinator-driven |
| `Server.CreateSessionDirect` | `internal/mcp/server.go` | Shared in-process backbone |

The CLI command and the MCP tool both ultimately call
`Server.CreateSessionDirect`, which is just a wrapper around
`handleCreateSession`. So **there is one creation path**, not two.

`runSessionCreate` itself does very little: it calls `resolveInstanceRoot()`,
constructs an `mcp.Server`, wires daemon funcs, and calls
`CreateSessionDirect(repo, purpose, "")`. Everything load-bearing lives in the
MCP handler.

### 3. The hard preconditions baked into `handleCreateSession`

Walking the handler top-to-bottom, these are the preconditions an `init`
fallback would need to satisfy:

| Precondition | Source | What enforces it |
|---|---|---|
| `<instanceRoot>/.niwa/instance.json` exists | `resolveInstanceRoot()` in `internal/cli/session.go` (via `discoverInstanceRoot` in `internal/cli/task.go:444`) | The CLI walks up looking for `instance.json` and fails with "not inside a workspace instance" otherwise. |
| `<instanceRoot>/.niwa/roles/<repo>/` directory exists | `handleCreateSession` line ~200 (`roleDir := filepath.Join(... ".niwa", "roles", args.Repo); os.Stat(roleDir)`) | Returns `UNKNOWN_ROLE` error if missing. |
| `<repo>` is reachable via `findRepoInWorkspace(instanceRoot, repoName)` | `internal/mcp/handlers_session.go:155` | Scans two levels deep: `<instanceRoot>/<group>/<repo>/.git`. Returns `UNKNOWN_ROLE` if not found. |
| `<workspaceRoot>/<group>/<repo>/.git` is a real git repo (so `git worktree add` succeeds) | The `git worktree add` exec call at line 230 | Returns generic exec error wrapped as "git worktree add: ..." |
| A daemon starter is wired into the MCP server | `s.daemonStarter == nil` check at line 195 | Returns "daemon starter not configured" |

The role-directory check is the lethal one for the `init` use case.
`.niwa/roles/<repo>/` is created by **`apply`** via
`workspace.InstallChannelInfrastructure`
(`internal/workspace/channels.go:241`), and only when channels are enabled.
The role list is derived by walking the cloned repo basenames under groups
(`enumerateRoles`, line 448).

`findRepoInWorkspace` does not look at workspace.toml — it walks the
filesystem. That sounds promising for `init`, but the repo it expects to find
is one cloned by `apply` into a group folder (`<workspaceRoot>/public/foo`,
`<workspaceRoot>/private/foo`, etc.). A repo cloned by `init` into
`<workspaceRoot>/.niwa/` (the config-repo location) is **not** discoverable
via this lookup — `.niwa/` is hidden and `findRepoInWorkspace` skips entries
beginning with a dot.

### 4. Filesystem layout the session machinery assumes

When `handleCreateSession` runs, it expects (and produces):

```
<workspaceRoot>/                          <- the instance root
  .niwa/
    instance.json                         <- MUST exist (CLI gate)
    roles/<repo>/                         <- MUST exist (handler gate)
    sessions/                             <- created on demand
    worktrees/<repo>-<sid>/               <- created by handler
      .niwa/
        roles/<repo>/inbox/{...}/
        tasks/
        sessions/
        daemon.pid
        daemon.log
  <group>/                                <- e.g. "public"
    <repo>/                               <- MUST be a git repo
      .git/
```

None of this exists after `niwa init --from <empty-remote>` — even on the
happy path where the remote has a workspace.toml, `instance.json`, role
directories, and group/repo clones only appear after the user runs
`niwa create` and `niwa apply`.

### 5. Sessions today assume an applied workspace

Direct answer to the lead's critical question: **yes, sessions strictly
require an applied workspace.** All three of these must have run before
`niwa session create` is callable:

1. `niwa init` (writes `<workspaceRoot>/.niwa/workspace.toml`).
2. `niwa create` (writes `<workspaceRoot>/.niwa/instance.json` and clones
   group/repo trees).
3. `niwa apply` (runs `InstallChannelInfrastructure` which materializes
   `<instanceRoot>/.niwa/roles/<role>/` from the cloned repo basenames).

The init-time bootstrap scenario the user is sketching — "fresh clone, scaffold
workspace.toml, land it on a branch in a worktree" — happens **before** any of
this. The current session machinery cannot serve this scenario as-is.

### 6. Cleanup, rollback, and partial-state behavior

The current creation handler has fairly disciplined cleanup:

- **Worktree add fails**: returns immediately; no state to clean up.
- **`scaffoldWorktreeNiwa` fails after worktree add**: runs `cleanupWorktree`
  (which calls `git worktree remove --force`), then returns error.
- **State-file write fails**: also runs `cleanupWorktree`.
- **Daemon spawn timeout** (`ErrDaemonSpawnTimeout`): runs `cleanupWorktree`,
  removes the state file with `os.Remove`, AND runs `git branch -D` on
  `session/<sid>` (lines 270-277). This is the only path that scrubs the
  branch.
- **Daemon spawn non-timeout error**: leaves worktree, state, and branch in
  place but sets `daemon_warning` in the response so the caller knows to
  retry or destroy.

What does NOT clean up after itself:

- **No defer**: if the process crashes between worktree add and the next step,
  the worktree, branch, and possibly state file are orphaned.
- **No filesystem GC pass**: there is no `niwa session reap` or
  startup-time orphan sweep. Orphans linger until `niwa session destroy`
  is invoked manually.
- **Branch cleanup on destroy is non-default**: even normal `destroy` uses
  `git branch -d` (safe), so unmerged session branches survive a normal
  destroy. Only `--force` deletes them.

For `init`'s use case, this matters because init failures need to leave the
freshly-cloned repo in a state the user can reason about. The current session
abstraction is leaky here: a partial `create_session` can orphan a worktree
inside a directory the user thinks is "just my clone."

### 7. The "repo not in workspace.toml" question

Sessions today have **no concept of workspace.toml-declared repos at all**.
The handler does not parse workspace.toml. It enforces membership via the
`.niwa/roles/<repo>/` directory's existence on disk. That directory is
created by apply on the basis of cloned repo basenames, not the config.

In principle this means: if `init` were to create `.niwa/roles/<repo>/`
directly, and place the clone in a group folder, the existing session
machinery would happily create a session for it. But this is a hack — it
inverts the apply contract (apply scaffolds role directories from the cloned
topology, not the other way around). Doing this from `init` would require
either:

- (a) Calling a subset of `InstallChannelInfrastructure` from `init` to
  materialize `.niwa/roles/<repo>/` for the freshly cloned repo, OR
- (b) Loosening `handleCreateSession`'s precondition check to accept a
  caller-supplied repo path that bypasses both the role-directory gate and
  `findRepoInWorkspace`.

Both options have downstream consequences (the per-worktree daemon expects
the role-inbox layout to exist; channel infrastructure expects `.mcp.json`
which init has no business writing yet).

## Integration Sketch

Here is the rough call sequence for `init`'s fallback, assuming we want to
preserve the current session machinery rather than invent a new "lightweight
branch+worktree" primitive.

### Option A: Build a minimum-viable instance just for init

```
runInit (modeClone branch):
  1. workspace.MaterializeFromSource(...)
     -- if this succeeds, fall through to existing post-flight logic
     -- if this fails with "missing workspace.toml":
        ↓
  2. workspace.ScaffoldEmptyConfig(niwaDir, derivedName)
     -- writes a minimal workspace.toml + workspace-context.md stub
        directly into the cloned repo's .niwa/ subtree
  3. Stage the change in the cloned repo:
     a. git -C <clonePath> checkout -b niwa/init-scaffold
     b. git -C <clonePath> add .niwa/workspace.toml
     c. git -C <clonePath> commit -m "niwa: scaffold workspace.toml"
  4. Print success message with the absolute clone path AND the branch
     name. User can cd in, push, and open a PR themselves.
```

This **does not use the existing session mechanism at all**. The CLI just
creates a branch and commits in the cloned repo. The user discovers the
location via `init`'s success message.

### Option B: Promote the session primitive to support pre-apply use

This requires changes to `handleCreateSession`. Sketch:

```go
// Add a new field or sibling API: CreateSessionForPath
// that bypasses findRepoInWorkspace and roleDir checks.
type createSessionArgs struct {
    Repo             string  // existing
    Purpose          string  // existing
    RepoPath         string  // NEW: caller-supplied absolute repo path
    SkipRoleCheck    bool    // NEW: do not require .niwa/roles/<repo>/
    SkipDaemonStart  bool    // NEW: don't try to start the per-worktree daemon
}
```

Then `init` calls:

```go
srv := mcp.New("init", workspaceRoot)  // workspaceRoot ≈ "instance root"
// (no daemon funcs wired -- the SkipDaemonStart flag lets us skip the gate)
result := srv.CreateSessionDirectForPath(
    repo:           "scaffold",          // synthetic role name
    purpose:        "scaffold workspace.toml",
    repoPath:       clonePath,
    skipRoleCheck:  true,
    skipDaemonStart: true,
)
```

This is heavier. The handler currently couples branch + worktree + daemon +
lifecycle state. To use it pre-apply we'd need to make the daemon and role
directory optional, which is a substantive API surface change.

A third option (B'): write a new internal primitive `workspace.StageInWorktree`
that does branch + worktree without the lifecycle/daemon machinery. `init`
calls it directly; future code can call it too. The session abstraction
remains untouched as "the mesh-delegated branch+worktree+daemon bundle."

### Recommended call sequence (Option A or B')

```
1. niwa init --from dangazineu/commuter
2. workspace.ResolveCloneURL(...) -> URL
3. git clone <URL> <workspaceRoot>     # not .niwa/, the workspace root itself
4. detect: .niwa/workspace.toml absent
5. workspace.StageInWorktree(<workspaceRoot>, branchName="niwa/init-scaffold"):
   - git -C <workspaceRoot> branch niwa/init-scaffold
   - git worktree add <somewhere>/scaffold-<hex>/ niwa/init-scaffold
   - return worktreePath
6. workspace.Scaffold(<worktreePath>, name)  # uses existing scaffold logic
   -> writes .niwa/workspace.toml inside the worktree
7. git -C <worktreePath> add .niwa/workspace.toml
   git -C <worktreePath> commit -m "niwa: scaffold workspace.toml"
8. Print to stdout:
     Workspace scaffolded at: <worktreePath>
     Branch:                  niwa/init-scaffold
     Next steps:
       cd <worktreePath>
       # inspect, then:
       git push -u origin niwa/init-scaffold
       # then open a PR or merge directly, then run `niwa apply`
9. Exit 0.
```

The big simplification vs. the existing session machinery: no `.niwa/sessions/`
state file, no per-worktree daemon, no `.niwa/roles/` requirement, no
`instance.json` requirement.

## Implications

1. **Sessions are not the right primitive for init-time staging.** They're
   built for the post-apply mesh world where coordinators delegate to repos
   that already exist on disk under group folders. Reusing them pre-apply
   requires either inventing escape hatches in `handleCreateSession` or
   accepting a misaligned semantic.

2. **The cleanup story is missing.** Even if we taught sessions to work pre-
   apply, the existing failure-mode cleanup is geared around the "instance
   already exists, recover the orphan" model. Init failures need different
   semantics: the entire workspace root may need to be removed (and `init.go`
   already has a `workspaceCreated` defer to do this — line 215-225).

3. **Output discovery is solvable but new.** The existing session CLI writes
   the worktree path to stdout (`session: created %s at %s`) and to
   `NIWA_RESPONSE_FILE` for the shell wrapper to consume. `init` can adopt
   the same pattern, but the wrapper's "cd to landing path" behavior is
   probably wrong for an "inspect-then-push" handoff — the user wants
   to *see* the worktree path printed, not silently land in it without
   knowing what just happened.

4. **A new primitive is cheap.** The actual git operations are just
   `git worktree add ... -b ...` plus a commit. We don't need the
   lifecycle, the daemon, or the role inbox. The session abstraction's
   value is in *mesh delivery*; init has no mesh.

5. **The "is repo declared in workspace.toml" question is a red herring.**
   The current session machinery doesn't check workspace.toml at all. It
   checks `.niwa/roles/<repo>/`, which exists as a side effect of apply
   having processed workspace.toml. Init's use case sidesteps both because
   there is no workspace.toml yet.

## Surprises

1. **Sessions never read workspace.toml.** I expected a config-loading
   gate. There isn't one — the gate is the role directory, which is a
   filesystem proxy for "this repo was declared and cloned."

2. **`findRepoInWorkspace` skips hidden directories.** Line 161 of
   `handlers_session.go` does `strings.HasPrefix(top.Name(), ".")` and
   continues, so a clone placed under `.niwa/` is invisible to it. This
   eliminates the otherwise-tempting hack of cloning into `.niwa/repo/`
   and treating it as a discoverable role.

3. **Branch cleanup on destroy is non-default.** Even a `niwa session destroy`
   with no `--force` leaves the `session/<sid>` branch in place if it has
   unmerged commits. The branch persists in git history forever unless
   manually pruned. For init's use case this is actually a feature — the
   user inspects the branch before pushing — but it does mean an aborted
   init that called the session API would leave both an orphan branch and
   a worktree behind.

4. **No filesystem-GC pass exists.** If init crashed after creating a
   session-like artifact, nothing reaps it. The only reaper is `niwa session
   destroy <sid>`, and the user would need to know the session ID.

5. **The session ID is 8 hex chars, scoped to `<instanceRoot>/.niwa/sessions/`.**
   It does not embed any reference to the source URL or repo identity. A
   fresh init's "session" couldn't carry the URL information forward.

6. **`niwa session attach` requires a captured `claude_conversation_id`.**
   The session abstraction assumes a Claude worker has already run inside
   the worktree. An init-staged worktree has never seen a worker, so
   attach would fail with the "no captured conversation id" error. The
   user can still `cd` into the worktree manually, but the session-aware
   tooling (`niwa go`, `niwa session attach`) won't help them.

## Open Questions

1. **Does the user want the bootstrap commit pre-staged, or just the file
   on a branch?** The sketch above commits the file. An alternative is to
   leave it as a working-tree-dirty change so the user reviews + commits
   themselves. The latter matches the "needs your inspection" tone better.

2. **Where does the worktree live for a not-yet-applied workspace?**
   Sessions today use `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. For
   init-fallback there is no instance, and the clone *is* the workspace
   root. Plausible homes: `<workspaceRoot>/../scaffold-<hex>/`, or
   `~/.cache/niwa/scaffold/<sid>/`, or just `<workspaceRoot>` itself on a
   detached branch (no separate worktree at all).

3. **Should the success exit be `0` or a non-zero "needs review" code?**
   The current `niwa init` returns 0 on success and non-zero on conflict.
   A new "scaffolded but not pushed" outcome may want its own exit code
   so CI catches it.

4. **Does the team-config rank-2 path interact with this?** If the empty
   remote later gets a rank-2 config, the deprecation notice machinery
   (`workspace.EmitRank2Notice`) needs to be re-evaluated post-scaffold.
   Probably out of scope for this lead, but flagging.

5. **What happens if the user pushes the scaffold branch and someone else
   races them with a manual config?** Pushing to a branch isn't a merge, so
   this is mostly a PR-review concern, but worth keeping in mind.

6. **Does the existing `niwa session destroy` need to learn about
   init-scaffold worktrees, or are they a separate species?** Recommendation:
   separate species. Don't conflate.

## Summary

niwa's worktree session mechanism is a tightly-coupled bundle of branch +
worktree + per-worktree daemon + lifecycle state, designed for post-apply
mesh delegation; it hard-requires `<instanceRoot>/.niwa/instance.json` and
`<instanceRoot>/.niwa/roles/<repo>/`, both produced by apply. Sessions never
read workspace.toml — they enforce membership via the role directory on
disk, which means they cannot be reused for init-time staging without
either bypassing those gates or carving out a new in-package
"branch-in-a-worktree" primitive. The recommended path is to give init its
own lightweight helper (e.g. `workspace.StageInWorktree`) that performs the
branch + worktree + commit dance without invoking the session lifecycle,
prints the worktree path on stdout, and leaves the user to inspect and
push.
