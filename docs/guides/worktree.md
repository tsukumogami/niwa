# Worktrees

A worktree is an isolated git checkout of one repo in your workspace, on its
own branch, with the repo's CLAUDE content installed into it. You create one
when you want a clean place to do a piece of work without disturbing the main
checkout — a feature, a spike, a long-running investigation.

niwa manages worktrees through the `niwa worktree` command group. The legacy
`niwa session` spelling still works as a deprecation alias (see
[Command naming](#command-naming) below).

## What a worktree gives you

When you run `niwa worktree create <repo> <purpose>`, niwa:

1. Creates a git branch `session/<id>` from the current HEAD of the repo.
2. Adds a git worktree at `<instance>/.niwa/worktrees/<repo>-<id>/`.
3. Installs the owning repo's CLAUDE content into the worktree, the same class
   of accessories `niwa apply` installs into a repo checkout (CLAUDE.local.md,
   subdirectory content, settings, env, hooks).
4. Writes a `.claude/rules/worktree-imports.md` file that imports the
   workspace-context.md from the instance root (plus the overlay and global
   CLAUDE files where they exist), so the worktree sees workspace context when
   launched as its own Claude Code project root.
5. Appends a purpose/branch layer to the worktree's CLAUDE.local.md naming the
   repo, the purpose you gave, and the branch.
6. Runs any worktree hooks discovered under the config repo's
   `worktree-hooks/` directory.
7. Writes a lifecycle state file at `<instance>/.niwa/sessions/<id>.json`.

Step 3 onward is the work of `workspace.ApplyToWorktree`, which reuses the same
installers the instance apply pipeline uses. A worktree and a repo checkout
cannot drift, because there is a single materializer path behind both.

If your shell integration is active, the shell navigates into the new worktree
directory on success. See `niwa shell-init` for setup.

Two worktrees for the same repo can coexist. Each gets its own branch, its own
directory, and its own CLAUDE content.

## Lifecycle

```
niwa worktree create <repo> <purpose>
         |
         v
    [status: active]
    worktree exists on branch session/<id>
    CLAUDE content installed
         |
         | niwa worktree apply <id>   ← re-sync content, idempotent, repeatable
         |
         v
niwa worktree destroy <id>
         |
         v
    [status: ended]
    worktree directory removed
    branch deleted if merged (kept otherwise)
    state file left on disk
```

A worktree is terminal once ended. There's no resume-from-ended path; create a
new worktree to continue work.

## Filesystem layout

After `niwa worktree create` completes:

```
<instance>/
  .niwa/
    sessions/
      <id>.json                  # lifecycle state (status, worktree_path, branch_name, ...)
    worktrees/
      <repo>-<id>/               # the git worktree (your working directory)
        CLAUDE.local.md          # repo content + the purpose/branch layer
        .claude/
          rules/
            worktree-imports.md  # @import of the instance workspace-context.md
```

The `sessions/<id>.json` state file is the source of truth for status. Its
fields:

| Field | Description |
|-------|-------------|
| `session_id` | 8-character lowercase hex identifier |
| `repo` | Repo this worktree is for |
| `purpose` | Description set at creation |
| `status` | `active`, `ended`, or `abandoned` |
| `creation_time` | RFC3339 timestamp |
| `worktree_path` | Absolute path to the worktree directory |
| `branch_name` | Git branch backing the worktree (defaults to `session/<id>`) |

After `niwa worktree destroy` runs, the worktree directory is removed, `status`
becomes `ended`, and the state file stays on disk so `niwa worktree list
--status ended` still shows closed worktrees.

> "Session" survives only as an internal state noun: the state directory is
> `.niwa/sessions/`, the on-disk schema is `SessionLifecycleState`, and the
> identifier is still called a session id in JSON output. The user-facing
> concept is the worktree.

## Customizing the worktree content layer

By default the worktree layer is a short generated section naming the repo,
purpose, and branch. To control it, set a template in the workspace config:

```toml
[claude.content.worktree]
source = "worktree.md"
```

When `source` is set, niwa renders that template as the worktree layer instead
of the default section. The template is expanded with worktree variables —
`{purpose}`, `{branch}`, `{repo_name}`, `{worktree_path}` — alongside the
instance variables `{workspace}` and `{workspace_name}`. The source path is
containment-checked and rendered in memory, so a crafted template cannot escape
its directory. An absent entry leaves the default behavior unchanged.

### Worktree hooks

Scripts under the config repo's `worktree-hooks/` directory run on every
`create` and `apply`, against the worktree, in lexical order. They are the
worktree analog of instance setup scripts and come from the config repo you
already trust. Each script runs with the worktree as its working directory and
the worktree context exported as environment:

| Variable | Value |
|----------|-------|
| `NIWA_WORKTREE_PATH` | Absolute path to the worktree |
| `NIWA_WORKTREE_REPO` | Repo name |
| `NIWA_WORKTREE_PURPOSE` | Purpose string |
| `NIWA_WORKTREE_BRANCH` | Branch name |

A non-executable script is warned about and skipped (matching the setup-script
policy). The first non-zero exit stops the run. A missing `worktree-hooks/`
directory or no scripts for the event is a no-op.

## The worktree branch in git

`niwa worktree destroy` does NOT unconditionally delete the `session/<id>`
branch. The default uses `git branch -d`, which removes the branch only if it's
already merged. If the branch has unmerged commits, niwa leaves it and prints a
warning to stderr before the `session: destroyed` line, naming the manual
deletion command.

To remove the branch regardless of merge status, pass `--force`:

```bash
niwa worktree destroy <id> --force
```

To clean up branches by hand after reviewing the work, from inside the repo
(not the worktree):

```bash
git branch -d session/<id>    # safe: fails if unmerged
git branch -D session/<id>    # unsafe: deletes regardless
git branch --list 'session/*' # list all worktree branches
```

## Command reference

### `niwa worktree create <repo> <purpose>`

Creates a worktree for the named repo: scaffolds the worktree on a new branch,
installs the repo's CLAUDE content plus the worktree rules import and the
purpose/branch layer, runs worktree hooks, and writes the state file.

```bash
niwa worktree create niwa "implement the worktree apply command"
```

On success niwa prints the created id and worktree path, lists the content
files it wrote, and (with shell integration active) navigates your shell into
the worktree.

If content installation fails after the worktree is created, niwa reports the
error but leaves the worktree in place — you can re-sync it with
`niwa worktree apply`.

### `niwa worktree apply <id>`

The worktree analog of `niwa apply`: re-installs the owning repo's CLAUDE
content (plus the rules import and purpose/branch layer) into an existing
worktree. It does not scaffold a new worktree; the worktree must already exist
and be active.

```bash
niwa worktree apply ab12cd34
```

It is idempotent by construction. Re-running overwrites the repo content,
re-points the rules import without duplicating `@import` lines, and replaces the
worktree-context section rather than appending a second copy. Applying to an
ended or abandoned worktree is refused.

### `niwa worktree destroy <id> [--force]`

Marks the worktree ended, removes the working directory, and deletes the branch
when it's already merged (use `--force` to delete regardless).

```bash
niwa worktree destroy ab12cd34
niwa worktree destroy ab12cd34 --force
```

Two guards protect uncommitted or in-use work:

- **Uncommitted work.** If the worktree has uncommitted changes, destroy refuses
  unless `--force` is passed. The error names the worktree path and the
  commit / stash / `--force` recovery options. This is the worktree analog of
  the instance-level uncommitted-work guard.
- **Active attach.** If an attach lock is held (see
  [Attaching](#attaching-to-a-worktree)), destroy refuses unless `--force` is
  passed. The error carries the holder PID and points at
  `niwa worktree detach <id> --force`.

### `niwa worktree list [--repo <name>] [--status …] [--attached|--available]`

Lists per-worktree lifecycle states with their attach availability.

```bash
niwa worktree list
niwa worktree list --status active
niwa worktree list --repo niwa
niwa worktree list --attached
niwa worktree list --available
niwa worktree list --json
```

Output columns: SESSION_ID, REPO, STATUS, AVAILABILITY, CREATED, PURPOSE.

The AVAILABILITY column has three values:

| Value | Meaning |
|-------|---------|
| `available` | No attach lock held. Free for `niwa worktree attach`. |
| `attached`  | A `niwa worktree attach` process holds the lock. |
| `stale`     | A sentinel exists but the holder PID is dead. The lock is no longer effective; the next read reaps the sentinel. |

Sort order: attached worktrees first, then active before terminal status, then
creation time descending — so "is anyone in there?" sits at the top of the
table.

Filters AND-combine. `--attached` and `--available` are mutually exclusive.
Worktrees with `AVAILABILITY=stale` appear under neither filter; run without
filters to see them. `--json` emits one object per worktree (with the `attach`
sub-object when a live lock is held) instead of a table.

## Attaching to a worktree

`niwa worktree attach <id>` lets you step into a worktree interactively: it
acquires an exclusive lock, validates the worktree, and launches Claude Code
with `--resume` so you pick up the worktree's conversation transcript. When you
exit Claude Code (Ctrl-D or `/exit`), niwa releases the lock automatically.

```bash
niwa worktree list               # find the worktree you want to enter
niwa worktree attach ab12cd34    # acquire the lock, launch claude --resume
# [interactive Claude Code TUI; type /exit when done]
```

While attached, other operators see `AVAILABILITY=attached` with your PID in
`niwa worktree list`. The lock is recorded in a sentinel at
`<worktree>/.niwa/attach.state` so the availability projection is visible to
`list`.

### Detach

Normal release is automatic on Claude Code exit — there's no command to run.
The explicit `niwa worktree detach` is an operator escape hatch for stale locks
left by an SSH disconnect or a terminal crash.

```bash
niwa worktree detach ab12cd34            # auto-recover a dead-holder lock
niwa worktree detach ab12cd34 --force    # break a live attach lock
```

Without `--force`, detach succeeds silently when the holder PID is dead and
exits with code 3 if the holder is alive (with a message pointing at `--force`).
With `--force`, it SIGTERMs the holder, waits `NIWA_DESTROY_GRACE_SECONDS`
(default 5 seconds), SIGKILLs if needed, and exits with code 4 to signal a live
holder was killed. If `niwa worktree list` reports `AVAILABILITY=stale`,
`--force` is not needed — the flagless detach reaps the dead-holder sentinel.

### Attach exit codes

| Code | Meaning |
|------|---------|
| 0 | Clean exit (Claude Code returned 0). |
| 1 | Pre-flight validation failure (status not active, transcript missing/empty). |
| 2 | Usage error (e.g. detach with no id). |
| 3 | Lock contention (attach lock held by a live process). |
| 4 | `niwa worktree detach --force` killed a live holder. |
| 1–125 | Propagated from Claude Code (codes ≥ 126 are clamped to 125). |

## Command naming

`niwa worktree` is canonical. The historic `niwa session` spelling is retained
as an alias so existing scripts keep working: any `niwa session <subcommand>`
resolves to the matching `worktree` subcommand and prints a one-line deprecation
notice to stderr. Behavior and exit code are unchanged.

```text
$ niwa session list
"niwa session" is deprecated; use "niwa worktree"
  SESSION_ID   REPO   STATUS   AVAILABILITY  CREATED   PURPOSE
  ...
```

Prefer `niwa worktree` in new scripts and documentation.

## Contributor notes

The lifecycle state schema is versioned. The `SessionLifecycleState` struct in
`internal/worktree/session_lifecycle.go` is the authoritative definition. The
`branch_name` field was added in schema v1.1; readers must call
`EffectiveBranchName()` rather than reading `BranchName` directly, so pre-v1.1
state files fall back to the historic `session/<id>` default.

Session ids are 8-character lowercase hex strings from `crypto/rand`, validated
against `^[0-9a-f]{8}$` on every read to guard against path traversal from
caller-supplied values — don't relax this check.

The content install is `workspace.ApplyToWorktree` in
`internal/workspace/worktree_content.go`. It reuses `InstallRepoContentTo` and
the shared `runRepoMaterializers` loop rather than forking a parallel installer,
so adding a materializer to the default set reaches both the instance apply
pipeline and the worktree path.
