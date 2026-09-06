# Setup scripts

A repo can carry its own provisioning steps in `scripts/setup/`. niwa runs them
against the clone it materializes, and — when the repo opts in — against each of
that repo's worktrees.

This is the contract those scripts run under. Until now it existed only inside
design documents, which is why scripts were written against assumptions that
happened to hold for a clone and silently did not hold anywhere else.

## Where scripts live and how they run

Executable files directly inside `scripts/setup/` run in lexical order. Name
them with a numeric prefix — `01-git-hooks.sh`, `02-install-deps.sh` — because
that ordering is the only sequencing niwa provides.

- **The working directory is the repo root**, never the setup directory. A
  script at `scripts/setup/01-foo.sh` runs with its cwd two levels above itself.
- **Non-executable files are warned about and skipped**, not run. `chmod +x`.
- **Subdirectories are not descended into.** Only files directly in
  `scripts/setup/` run.
- **The first non-zero exit stops that repo's remaining scripts.** Other repos
  still get their turn.
- **A failure never fails the command.** It is reported and counted; the exit
  code does not change. See [When a script fails](#when-a-script-fails).

Change the directory with `setup_dir` on the workspace, or per repo. Setting it
to `""` for a repo disables setup entirely for that repo.

## What a script may assume about where it is

**Do not compute paths by walking up from the working directory.** This is the
single most important line in this document, and the reason it exists.

A clone lives at `<instance>/<group>/<repo>`, so `cd ../..` reaches the instance
root. A worktree lives at `<instance>/.niwa/worktrees/<repo>-<id>`, where the
same expression reaches `<instance>/.niwa` — a directory that exists and is
writable. A script doing that arithmetic does not fail in a worktree. It
succeeds, writes to the wrong place, and exits 0. There is nothing in any log to
notice. The group segment is absent from a worktree path too, so reaching
sideways to a peer repo is wrong in a second, independent way.

Use the environment instead:

| Variable | Set on | Meaning |
|---|---|---|
| `NIWA_INSTANCE_ROOT` | clone and worktree | The instance root. Use this instead of `cd ../..`. |
| `NIWA_WORKTREE_PATH` | worktree only | Absolute path of the worktree. **Its presence is how a script knows which it is running in.** |
| `NIWA_WORKTREE_REPO` | worktree only | Repo name. |
| `NIWA_WORKTREE_PURPOSE` | worktree only | The worktree's purpose string. |
| `NIWA_WORKTREE_BRANCH` | worktree only | The worktree's branch. |

Everything else in the environment is inherited from the process that invoked
niwa.

**What is deliberately removed.** `NIWA_RESPONSE_FILE` is unset before any
script runs. It is the channel the shell wrapper uses to change your working
directory, and a script that wrote to it would redirect the operator's shell.
The unset happens in the root command's pre-run and is pinned by
`TestRootPersistentPreRun_NotShadowed`, which exists because a command group
that declared its own pre-run once silently disabled it for every subcommand
underneath.

**Secrets reach scripts by file, never by environment.** niwa adds no resolved
secret to a script's environment. Values it resolved are written to the env
output files the repo declares, which sit in the working directory.

## Scripts must be safe to re-run

niwa runs setup on every `niwa apply`, and on every worktree the repo has opted
in for. A script that appends to a file, or that fails when a directory already
exists, will misbehave the second time.

This has always been the requirement; it had never been written down. Guard the
expensive parts:

```sh
#!/bin/sh
set -e
# Skip the install when the dependency tree is already current.
[ package-lock.json -ot node_modules ] && exit 0
npm ci
```

## Running in worktrees

Off by default. A repo opts in:

```toml
[repos.web]
worktree_setup = true
```

Or, for a workspace where most repos want it:

```toml
[workspace]
worktree_setup = true

[repos.docs]
worktree_setup = false   # this one does not
```

The per-repo value wins over the workspace value; with neither set, the answer
is off.

**Why off by default.** Every setup script that existed before this feature was
written for a clone. Turning it on for everything would run them all in a place
they had never seen, and the failure mode above is silent. Opting in is a
statement that someone has read this page.

### Scripts that must not run per worktree

One `scripts/setup/` can hold both kinds. A dependency install has to run in
every tree, because `node_modules` is per-tree. A git-hooks installer must not,
because hooks live in the shared `git-common-dir` — running it per tree is
duplicate work writing to one place.

The per-repo switch is all-or-nothing for the directory, so the script gates
itself:

```sh
#!/bin/sh
# Shared state: the clone's run covers every worktree.
[ -n "$NIWA_WORKTREE_PATH" ] && exit 0
git config core.hooksPath .githooks
```

### Scripts must be committed

`git worktree add` materializes tracked files only. An uncommitted script exists
in the clone and not in the worktree, so it runs on the clone and silently does
not run in worktrees.

### When a worktree is skipped

`niwa apply` does not provision a worktree that is missing, that another process
holds a lock on, or that git no longer registers. The lock case is the one you
will meet: a worktree an agent is currently working in is skipped, and picked up
by the next apply after that process finishes. It does not depend on a clean
exit — a crashed process's lock reads as stale and the worktree is provisioned
normally.

To provision one immediately, run `niwa worktree apply <session-id>` against it.

## When a script fails

The failure is reported and the command's exit code does not change.

- On `niwa worktree create` and `niwa worktree apply`, the failing script and
  the worktree are named on stderr. **The worktree is left in place**, with
  whatever the script managed to do. Fix the script and re-run
  `niwa worktree apply`.
- On the delegated `WorktreeCreate` hook, the same diagnostic goes to stderr and
  the worktree is kept, so an agent lands in a tree that exists.
- On `niwa apply`, the failure joins the deferred warnings and the counted
  verdict below the summary, naming the worktree rather than just the repo — a
  repo can fail in its clone and in several of its worktrees, and they are
  distinguishable.

A failing setup script never destroys a worktree. That is deliberate and it is
load-bearing: see the note in `internal/workspace/worktree_content.go` where the
outcome is recorded, and niwa#285.

**Script output is scrubbed.** Values niwa knows to be secret are replaced with
`***` in the streamed output, on every surface. Short values are not redacted —
a six-character minimum keeps common words from being blanked out of every log
line — so do not rely on redaction for a secret short enough to be a word.

## Migrating from a `worktree-hooks/` workaround

If your config repo carries a `worktree-hooks/apply/` script that provisions
worktrees by branching on `NIWA_WORKTREE_REPO`, this feature replaces it.

1. Move each repo's provisioning into that repo's own `scripts/setup/`, and
   commit it.
2. Add `worktree_setup = true` for those repos in `workspace.toml`.
3. Delete the branching hook from the config repo.

The gain is that per-repo knowledge stops living in a foreign repository's
switch statement. A repo that knows how to provision itself does so in its own
tree, on the clone and in every worktree, with no config-repo edit when it
changes.

Worktree hooks remain the right mechanism for anything genuinely
workspace-wide — see [worktree hooks](worktree.md#worktree-hooks).

## What this does not do

- It does not resolve secrets in a worktree. A worktree inherits the clone's
  already-materialized env output.
- It does not make setup failures fail a command. If you want that, say so on
  the issue tracker; the mechanism was specified and deliberately deferred.
- It does not run scripts for a repo that has not opted in. No process is
  spawned at all in that case.
