# Ephemeral session instances

An ephemeral instance is a full niwa instance provisioned for the lifetime of a
single Claude Code background session and reclaimed when you delete that session
from the Agent View. The model is one-to-one: **1 Claude Code background session ==
1 ephemeral niwa instance**. A session that finishes its task, goes idle, or is
suspended keeps its instance — it is still listed and resumable, so you can re-open
it into the same clone — and only an explicit delete tears the instance down. When
you dispatch workers with `claude agents` from the workspace
root, each worker gets its own isolated instance — its own clone, its own
secrets, its own CLAUDE context — instead of all the workers sharing the one
working tree at the root and colliding.

This is the instance-level analog of the per-repo
[worktree integration](worktree.md). Worktrees isolate agents one level down,
inside an instance; ephemeral session instances isolate agents at the
workspace-root level, one per dispatched session.

## Why it exists

`claude agents` dispatches background sessions that inherit the launch
directory's working directory. Sessions fanned out from the workspace root all
share that one tree, so parallel agents step on each other. The fix is to give
each dispatched session its own instance, created on `SessionStart` and reclaimed
by the reaper once you delete the session. Teardown is delete-only: the reaper
keys on the session's Claude Code job entry disappearing (the proxy for deletion),
so completed, idle, and suspended sessions keep their instances and stay
resumable.

niwa already owns the analogous surface one level down — per-repo worktree
hooks, a `.niwa/sessions/` store, non-interactive `niwa destroy --force`. This
feature lifts that pattern from the worktree level to the instance level.

## The model

```
claude agents  (at the workspace root, ephemeral mode on)
      |
      | dispatches a background worker
      v
worker SessionStart
      |
      | niwa instance from-hook  (guard passes)
      | niwa create --json       (clones a dedicated instance)
      | write .niwa/sessions/<session_id>.json mapping
      | inject instance path + CLAUDE.md + a cd instruction
      v
agent cd's into the instance and works there in isolation
      |
      | worker finishes / goes idle / is suspended  -> instance is KEPT (resumable)
      |
      | you delete the session (Ctrl+X twice in `claude agents`)
      | -> its ~/.claude/jobs/<id>/ entry disappears
      v
niwa reap  (on demand, or at the next niwa create)
           sees the job entry gone, niwa destroy --force, delete mapping
```

The reaper is the single teardown path. It runs on demand and opportunistically at
the next `niwa create`. Until you delete the session, its instance survives — a
completed-but-resumable session can be re-opened into the same clone.

## The session hooks

`niwa init` (and `niwa apply` from the root) materializes a workspace-root
`.claude/settings.json` carrying a `SessionStart` hook entry that pipes the Claude
hook JSON on stdin to an absolute-path `niwa instance from-hook`. The entry carries
a generous timeout (180 seconds) so a `SessionStart` provision — which clones an
instance and resolves its vault — doesn't trip the harness timeout.

No `SessionEnd` hook is installed. `SessionEnd` fires on idle-suspend
(`reason: resume`), `/clear`, logout, and similar — never uniquely when you delete
a session — so it is not a teardown trigger. The reaper owns teardown (see
[Teardown](#teardown) and [`niwa reap`](#niwa-reap) below). A workspace whose
`settings.json` was materialized before this change may still carry a `SessionEnd`
entry until it re-applies; the `niwa instance from-hook` SessionEnd branch is a
no-op, so that stale entry never destroys anything.

`niwa instance from-hook` is wired only for Claude to invoke — don't run it
yourself. It is deliberately distinct from `niwa worktree from-hook`: the
worktree command operates at the worktree level on `WorktreeCreate`/
`WorktreeRemove` events, this one at the instance level on Claude
`SessionStart`/`SessionEnd` events. They share nothing but the `from-hook`
suffix convention. The subcommand reads the payload, validates `session_id`, and
branches on `hook_event_name`.

### The SessionStart guard

Provisioning is inert unless all three parts of the guard hold. Any failure is a
clean no-op (exit 0, no output), so ordinary sessions are untouched.

1. **Ephemeral mode is on.** The opt-in master switch — a workspace-root state
   flag, default off — gates the whole feature. A workspace with no root state,
   or the flag absent or false, never has a session touched. A read or parse
   failure fails safe to "off."
2. **The session is a dispatched background worker.** Within ephemeral mode,
   provision only when the session's Claude Code job state at
   `~/.claude/jobs/<session-id>/state.json` carries `template == "bg"`. An
   interactive session carries `template == "claude"`. This is the confirmed
   coordinator-vs-worker discriminator; no native hook field distinguishes them.
   The guard locates the job dir by session id (the dir name is the session-id
   prefix; the full `sessionId` inside `state.json` confirms the match) and does
   NOT consult the `CLAUDE_JOB_DIR` env var, which is not reliably set.
3. **Not already inside an instance.** If the launch cwd already resolves inside
   a niwa instance, the hook no-ops, so a worker that itself dispatches
   sub-sessions doesn't nest.

`~/.claude/jobs/.../state.json` is an undocumented internal Claude Code file, so
the `template` read is a stability risk if its format changes. The opt-in master
switch bounds the blast radius to workspaces that chose the feature, and the
reaper reclaims any instance a misfire creates — a format change degrades to
wasted clones, not corrupted developer instances.

### Provisioning and context injection

On passing the guard, the SessionStart branch:

1. Runs `niwa create --json --name <session-id-prefix>` — a 12-character prefix
   of the session UUID. No topic slug exists yet at SessionStart, the UUID
   prefix is filesystem-safe, and naming from it dodges the
   `NextInstanceNumber` race an unnamed concurrent create would hit.
2. Writes the `.niwa/sessions/<session_id>.json` mapping.
3. Emits a `hookSpecificOutput.additionalContext` JSON carrying the instance
   path, the instance's `CLAUDE.md` content (so the agent operates under the
   instance's guidance without a re-root), and an explicit instruction to `cd`
   into the instance before any work.

The injection is needed because a mid-session `cd` moves only the Bash tool's
working directory and does not reload `CLAUDE.md`. Injecting the instance's
guidance plus the cd instruction is how the agent enters and operates inside its
instance.

## The mapping store

The session-to-instance binding is written at the workspace root under
`.niwa/sessions/<session_id>.json`. It is the single source of truth for the
reaper, which joins each instance to its session by `session_id` and checks that
session's job entry — never by cwd. (Keying on cwd would be wrong anyway: a hook's
reported cwd is the launch root, not the instance, since the agent's `cd` moves
only the Bash tool's directory.)

| Field | Description |
|-------|-------------|
| `session_id` | The Claude session UUID. Validated against the canonical UUID format before use as a path component. |
| `instance_name` | The provisioned instance's directory name. |
| `instance_path` | Absolute path to the instance directory. |
| `transcript_path` | The session's transcript path from the hook payload. |
| `created` | RFC3339 creation timestamp (UTC). |
| `ephemeral` | Always `true` for a provisioned instance. The load-bearing marker that gates teardown and reaping. |
| `label` | Optional human-friendly alias derived later from the session topic. Metadata only — never used to rename the on-disk instance. |

`session_id` flows from untrusted hook stdin straight into a path component and
command arguments, so it is validated against the UUID format before any path is
constructed. An invalid id is rejected without touching the filesystem. Writes
are atomic (write-temp-then-rename).

The on-disk instance directory is never renamed mid-session — renaming would
break the running session's cwd — so durable identity stays the `session_id`,
and any friendly slug is cosmetic metadata in `label`.

## Teardown

Teardown is **delete-only** and driven entirely by the reaper. An ephemeral
instance is reclaimed only when you delete its backing session from the Agent View
(Ctrl+X twice in `claude agents`), which removes the session's
`~/.claude/jobs/<id>/` entry — the signal the reaper keys on. A session that
finishes its task, goes idle, or is suspended keeps its instance and stays
resumable.

`SessionEnd` is **not** a teardown trigger. It fires on idle-suspend, `/clear`,
logout, and similar, none of which mean the session was deleted, so the
`niwa instance from-hook` SessionEnd branch is a no-op — it never destroys an
instance. (Earlier versions tore the instance down on `SessionEnd`, which reclaimed
instances the moment a worker finished a task or went idle; that was the bug this
behavior fixes.)

See [`niwa reap`](#niwa-reap) for the reclamation mechanics and the liveness rule.

## `niwa reap`

```bash
niwa reap
```

`reap` reclaims ephemeral instances whose backing session you have deleted. It
enumerates the workspace's instances (`niwa list --json`), joins each against its
session mapping, and force-destroys an instance only when BOTH hold:

- the instance is marked **ephemeral**, and
- its session is **deleted** by the liveness rule.

### The liveness rule

A session is **live** as long as its Claude Code job entry at
`~/.claude/jobs/<session-id>/state.json` exists (and the `sessionId` recorded
inside, when present, matches). It is **dead** only when that entry is gone.

This is the whole rule: **entry present = live, entry gone = deleted.** Job-entry
presence is a faithful proxy for "the session still exists in the Agent View,"
covering both a running session and an idle-but-resumable one (finished its task,
suspended, but still listed). A completed or idle session keeps its entry — and so
keeps its instance. Only deleting the session removes the entry, and that delete is
what the reaper reclaims on.

Liveness deliberately does **not** look at the job `state` label, at
`firstTerminalAt`, at an idle TTL, or at transcript mtime. Each of those is true of
a live-but-resumable session, so keying on any of them would reap an instance whose
session you might still re-open. (That is exactly what the earlier rule did, and the
bug it caused.)

`reap` never destroys a non-ephemeral (developer) instance, and never reaps on a
marker alone — the session must be gone. An ephemeral instance with no resolvable
mapping is skipped by the primary (mapping-keyed) sweep — without a session id the
entry-present liveness rule can't run.

### Never reap a live instance, mapped or not

Above the mapping-keyed rule sits a second, mapping-**independent** liveness
guard that both the primary sweep and the name+TTL backstop consult:
`instanceHasLiveJob`. A dispatched worker launches rooted in its instance
(`cmd.Dir == <instance>`), so its Claude Code job-state records that directory as
its `cwd`. Before destroying any instance, the reaper scans the jobs dir and
spares any instance a present job's `cwd` resolves inside. This closes a
data-loss class the mapping-keyed rule alone could not: an **unmapped**
dispatch-named instance (a lost or not-yet-written mapping) whose worker is still
alive was previously reclaimable by the backstop on name and age alone — and a
dispatched session can live for hours, far past the backstop's 30-minute TTL, so
a long-lived worker was guaranteed to age into a reap. Because the caller of a
reap is itself a live session rooted in its own instance, this guard is also what
stops `niwa dispatch` (which reaps opportunistically before provisioning) from
deleting the instance it is running inside.

`reap` runs on demand and is also invoked opportunistically at the start of
`niwa create`, so session fan-out self-bounds. The opportunistic call never
fails create: a reap error there is swallowed, and only successful reclamations
are noted on stderr.

> **One known edge.** The Agent View stops a finished background session's
> supervisor process after roughly an hour. It is not yet confirmed whether that
> stop also removes the `~/.claude/jobs/<id>/` entry. If it does, a reap after the
> stop could reclaim an instance whose session is still resumable — a much narrower
> window than the old reap-on-completion behavior, and one the delete-only contract
> accepts. If this ever bites you, re-run the session's work in a fresh dispatch; a
> longer-lived safety-net TTL for genuinely abandoned instances is a possible future
> addition.

## Context-aware `niwa apply`

Hosting the session hooks, the permission posture, and a root `CLAUDE.md` makes
the workspace root a managed-config surface. `niwa init` lands it; `niwa apply`
refreshes it. Rather than a separate refresh verb, `apply` is context-aware: it
converges the **subtree rooted at the current scope** and never climbs above it
or touches siblings.

niwa classifies your cwd into one of three managed scopes and resolves the apply
scope from it (or from `--instance`, or a registry name argument):

1. If cwd is inside a **worktree**, converge that worktree alone.
2. If cwd is inside an **instance**, converge that instance and its worktrees.
3. If cwd is at the **workspace root**, materialize the root-managed config,
   then converge every instance and each instance's worktrees.

The workspace root is **never** converged as an instance. `niwa init` persists
an `.niwa/instance.json` at the root to carry init-time state that `niwa create`
reads, so the root carries both `.niwa/workspace.toml` and `.niwa/instance.json`.
The `workspace.toml` is authoritative: a directory that has it is the workspace
root, classified as scope 3 above, not scope 2 — apply manages only its
root-level config and clones no repos into the root. (Before this distinction
was enforced, the root's `instance.json` made `apply` at the root treat the root
as instance-0 and clone every configured repo directly under it.)

Worktrees are refreshed as part of the instance apply, not by a separate niwa
cascade: an instance-scope `apply` refreshes the instance's environment and the
worktree path inherits that already-materialized environment (the upstream
inherit refresh). A worktree-scope `apply` likewise inherits the instance's
environment rather than resolving secrets on the worktree path.

This refines the prior behavior, where `apply` from anywhere inside an instance
converged the whole instance. A worktree is now its own scope. This is an
intentional, pre-1.0 semantics change toward a uniform "converge my subtree"
model.

`apply` re-runs vault resolution at the root and instance scopes; at worktree
scope the worktree inherits the instance's already-materialized environment
instead of resolving secrets on the worktree path. It is drift-aware (a no-op
where everything is already current, via content-materializer hashing), and
destroys nothing — destruction is `niwa destroy` / `niwa reap`.

### `--no-cascade`

`niwa apply --no-cascade` at the workspace root refreshes only the root-managed
config and does not re-converge the instances beneath it. Its primary use is
picking up a hook, permission, or `CLAUDE.md` edit at the root without paying for
a full reconvergence of every instance. The flag has no effect at an instance
(its worktrees refresh with it under the inherit model — a worktree is a derived
view of its instance, not an independently skippable scope) or at a worktree
(a leaf scope with nothing below it).

```bash
# At the workspace root: refresh root config only, no instance reconvergence.
niwa apply --no-cascade
```

### Blast radius per scope

What `apply` converges depends on where you run it and whether `--no-cascade` is
set:

| Scope (cwd) | `niwa apply` converges | `niwa apply --no-cascade` converges |
|-------------|------------------------|-------------------------------------|
| Workspace root | Root-managed config (`.claude/settings.json` + root `CLAUDE.md`) and vault, then every instance, then each instance's worktrees (refreshed via the inherit refresh as part of each instance apply) | Root-managed config and vault only — no instance reconvergence |
| Instance | That instance, then its worktrees (refreshed as part of this apply via the inherit refresh, not a separate niwa cascade) | Same as without the flag — the worktrees refresh with the instance under the inherit model, so `--no-cascade` has no effect here |
| Worktree | That worktree alone (inherits the instance's materialized environment; no secret resolution on the worktree path) | That worktree alone (leaf scope, no children to descend into; the flag is a no-op) |

At no scope does `apply` climb above the current node or touch a sibling.

## The workspace-root `CLAUDE.md` and permission posture

A session launched at the workspace root loads the root `CLAUDE.md` at startup.
Without it, the coordinator and any root session start with no workspace
orientation. The root materializer writes a `CLAUDE.md` at workspace altitude
describing the workspace as a multi-repo tree of instances and the
ephemeral-session model. (At init time there are no cloned repos to enumerate,
so this is a minimal orientation file, not the per-instance generated
workspace-context.)

The root `.claude/settings.json` also carries a **permission posture**
(`permissions.defaultMode`), sourced the same way instance materialization
sources it. Note its scope: settings resolve at launch and cannot be scoped per
session, so a root-level bypass-permissions posture applies to **every** session
launched at the root, not only dispatched workers. This is wider than
per-instance bypass; the opt-in ephemeral mode bounds it to workspaces that
chose the feature.

### Workspace plugins and skills at the root

The root `.claude/settings.json` also carries the workspace's **plugins** and
**marketplaces** (`enabledPlugins` / `extraKnownMarketplaces`), so a session
launched at the workspace root — including a dispatched worker before its
ephemeral instance is provisioned — loads the workspace's plugins and the skills
they carry. This is the same `enabledPlugins` / `extraKnownMarketplaces` block an
instance gets, with one exception below.

Forwarding these into the root scaffold (rather than relying on the SessionStart
hook to deliver them) is deliberate: Claude Code resolves plugins, marketplaces,
hooks, and env from the launch directory's `settings.json` **at startup**, before
the SessionStart hook runs. The hook injects only `additionalContext` and an
instruction to `cd` into the instance; a mid-session `cd` cannot re-resolve plugin
or settings configuration (the same reason it does not reload `CLAUDE.md`). So the
only place a plugin can become available to a root-launched session is the root's
own `settings.json`.

The one exception is marketplaces with no root-resolvable path. A github-sourced
marketplace (`org/repo`) hoists to the root unchanged. A `repo:`-sourced
marketplace resolves to a directory inside an instance checkout (for example a
private `tools` repo) that does not exist at the workspace root, so it has no
root-stable path. Such a marketplace — and any plugin bound to it — is excluded
from the root settings and a notice is printed at `niwa init` / `niwa apply` time
(no silent drop). Those plugins still load normally inside a provisioned instance,
where the `repo:` source resolves. In short: a root-launched session has the
workspace's github-sourced plugins/skills; instance-local (`repo:`) plugins are
available only once the session is inside its instance.

## Opting out

The feature installs by default at `niwa init`. To skip it:

```bash
niwa init <name> --no-ephemeral-sessions
```

This suppresses the whole root config — no SessionStart hook, no
root `CLAUDE.md`, and ephemeral mode stays off in root state. The install is
reversible: re-run `niwa init` without the flag, then `niwa apply` from the
root, and the root config installs again.

## Contributor notes

- The hook entry point is `niwa instance from-hook`
  (`internal/cli/instance_from_hook.go`), deliberately separate from the
  per-repo worktree hook (`internal/cli/session_from_hook_cmd.go`). Keep them
  disjoint — they operate at different levels on different events.
- The mapping store lives in `internal/workspace/session_map.go`. `session_id`
  is validated against the UUID pattern on every path construction; don't relax
  that check — it guards path traversal from untrusted hook input.
- The job-state read is shared by three consumers
  (`internal/cli/job_state.go`): the SessionStart guard keys on
  `template == "bg"`; the reaper's mapping-keyed liveness rule (`sessionLive`)
  keys on the job entry being present (live) vs gone (deleted); and the reaper's
  mapping-independent guard (`instanceHasLiveJob`) keys on any present job's
  `cwd` resolving inside the instance, sparing an instance a live session is
  rooted in even when it has no usable mapping (the backstop's case). `state.json`
  is an undocumented Claude Code file, so absent fields decode to zero and every
  reader fails safe on a miss.
- `niwa reap` destroys with `--force`, which skips the uncommitted-work guard. It
  is constrained to instances carrying the `ephemeral: true` marker whose session
  is gone (job entry deleted) — a developer's normal instance has no such marker
  and is never a target. The `SessionEnd` hook branch destroys nothing (it is a
  no-op); the reaper is the single teardown path.
- The root materializer (`internal/workspace/root_materializer.go`) reuses the
  shared `buildSettingsDoc`, so the root settings ride the same path the
  instance settings do for permissions, hooks, env, plugins, and marketplaces.
  One field is deliberately filtered, not forwarded verbatim: the root receives
  only the **root-hoistable** subset of marketplaces (see `rootHoistableConfig`).
  github-sourced marketplaces hoist as-is; `repo:`-sourced ones point into an
  instance checkout that does not exist at the root, so they and the plugins
  bound to them are excluded and reported. Do not assume the root settings are a
  byte-for-byte copy of an instance's — they match for everything except those
  instance-local marketplaces.
