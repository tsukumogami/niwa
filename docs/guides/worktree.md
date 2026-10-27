# Worktrees

A worktree is an isolated git checkout of one repo in your workspace, on its
own branch, with the repo's CLAUDE content installed into it. You create one
when you want a clean place to do a piece of work without disturbing the main
checkout — a feature, a spike, a long-running investigation.

niwa manages worktrees through the `niwa worktree` command group. The legacy
`niwa session` spelling still works as a deprecation alias (see
[Command naming](#command-naming) below).

## What a worktree gives you

When you run `niwa worktree create [repo] [purpose]`, niwa:

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

## How a worktree gets its environment

A worktree does not resolve secrets. It inherits the instance clone's
already-materialized environment by copying the clone's resolved secret-output
file(s) byte-for-byte into the worktree's target paths. The clone holds the
fully-resolved environment from the last `niwa apply` — plaintext vars plus
resolved secrets, written at 0600 and git-excluded — and the worktree mirrors
it exactly. A worktree's env is byte-identical to its instance clone's, for
every configured target and format (dotenv, json, shell, custom names).

Because there's no resolution step, `niwa worktree create` and
`niwa worktree apply` need no secret source. They can't fail on an unreachable
vault, a wrong-org session, or an unassembled provider reference — those
concerns belong to `niwa apply`, which already resolved the environment into the
clone.

That guarantee is about secret resolution specifically, not about the network in
general. A repo that has opted into [setup scripts](setup-scripts.md) running in
its worktrees can have those scripts do whatever they do — `npm ci` reaches the
network, and so does the same script on the clone path today. niwa itself
resolves nothing and contacts nothing.

The same inherit-don't-resolve rule covers `[claude.env] promote`. When a
promoted key's value comes from a secret source — a vault reference or the
machine-identity sync, resolved only at `niwa apply` time and absent from the
static config the worktree path sees — the worktree reads it from the clone's
already-materialized env when writing `settings.local.json`, rather than
re-resolving it. So a worktree's promoted Claude env matches the clone's without
the worktree path ever touching a secret source.

### Refreshing a worktree's environment

`niwa apply` is the refresh. After materializing each clone, the same run fans
out to every existing worktree and re-copies the clone's env into it. So when
you rotate a secret or change config and run `niwa apply`, clones and worktrees
update together — there is no separate worktree-only secret-refresh command.
`niwa worktree apply <id>` re-syncs a single worktree from its clone the same
way, by inheritance, never by resolution.

A worktree that is locked (attached by another process), detached from git, or
whose directory is missing is skipped during `niwa apply` with a warning naming
it; the apply still succeeds. A skipped-but-live worktree keeps its existing env
file — the skip never deletes it.

### Creating a worktree before the first apply

A worktree can only inherit an environment the instance has already
materialized. If a repo's env was enabled after the last apply, the clone holds
no env output yet, so there is nothing to copy. In that case
`niwa worktree create` exits non-zero with an error directing you to run
`niwa apply` first:

```bash
niwa apply              # materialize the instance environment into the clones
niwa worktree create niwa "..."   # now the worktree can inherit it
```

A repo that has no environment configured at all is not an error — the worktree
simply has no env file to inherit.

If your shell integration is active, the shell navigates into the new worktree
directory on success. See `niwa shell-init` for setup.

Two worktrees for the same repo can coexist. Each gets its own branch, its own
directory, and its own CLAUDE content.

## Lifecycle

```
niwa worktree create [repo] [purpose]
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

### Worktree commands at a workspace root

Worktrees belong to an instance. At the root of a multi-instance workspace there
are none, so the worktree subcommands say where to run instead of guessing:

| Command at a multi-instance root | Behavior |
|---|---|
| `destroy <session id or handle>` | Resolves normally — session targets are exactly the case the root can answer |
| `destroy --by-path <path>` | As inside an instance |
| `list` | Prints `niwa: this is the workspace root, not an instance; …` on stderr, no table, exit 0 |
| `list --json` | The same stderr line, `[]` on stdout, exit 0 |
| `create`, `apply`, `attach`, `detach`, `niwa go` | Print the same line prefixed `niwa: error:` on stderr, exit 1 |

`list` exits 0 because there is nothing wrong with asking: a script walking a
set of directories shouldn't be derailed by reaching the root, and `--json`
still gives it a parseable `[]`.

This matters more than it looks. A workspace root carries its own
`.niwa/instance.json` — `niwa init` persists init-time state there for `niwa
create` to read — so anything that resolved an instance by walking up for that
file used to stop at the root and treat it as an instance, then read the root's
session *mapping* store as though those files were worktree records. They are
different stores with different shapes.

The **single-instance layout**, where the root genuinely is the instance, is
unchanged: worktree commands there work exactly as they do inside a child
instance. niwa tells the two apart by whether the root's `instance.json` names
an instance, because a registered `niwa init` writes one without a name.

## Default worktree delegation for Claude Code

In a niwa workspace, `niwa apply` makes niwa the default worktree mechanism for
Claude Code agents. When an agent creates a worktree — whether you ask it to
"work in a worktree" or it spins up an isolated sub-task — that worktree becomes
a full niwa worktree, with the same environment and CLAUDE context a real
checkout has, listed and tracked like any worktree you create by hand. You don't
run a command; it happens through hooks niwa installs.

This is on by default. There's nothing to set up per developer.

### How it works

Every `niwa apply` writes `WorktreeCreate` and `WorktreeRemove` hooks into each
repo's Claude Code settings (`.claude/settings.local.json`) and into the
instance root's `.claude/settings.json`, and the workspace root's settings get
them too. Claude Code reads hooks only from the settings of the directory a
session was launched in, so each of those places needs its own copy; see
[Worktrees from a session at a root](#worktrees-from-a-session-at-a-root) for
how a root session names the repo it wants. The hooks call an internal command,
`niwa worktree from-hook`, which is wired only for Claude to invoke — don't run
it yourself. On create, it routes the agent's request through the same
create flow `niwa worktree create` uses and hands the worktree path back to the
agent as its working directory. On teardown, it reconciles the worktree with
niwa's lifecycle so nothing is left orphaned.

When the agent's worktree is created, the same content install that backs
`niwa worktree create` runs. The worktree inherits the instance clone's
already-materialized environment, so creation needs no secret source and can't
fail on a vault outage (see
[How a worktree gets its environment](#how-a-worktree-gets-its-environment)). If
the clone holds no env output yet, creation reports the error pointing at
`niwa apply`.

The installed hook resolves `niwa` from your `PATH`, falling back to the absolute
path of the binary that ran `niwa apply` only when `PATH` doesn't carry it. From
that apply onward, upgrading niwa takes effect without re-applying — the hook
picks up whichever niwa your `PATH` names.

A workspace applied by an older niwa still carries the previous hook text, which
names one exact binary, so it keeps invoking that binary until you apply it once
more. Run `niwa apply` once per existing workspace to pick up the change; after
that the hook maintains itself.

Two consequences worth knowing. If you install niwa somewhere your agent's
environment can't see — a harness launched from a desktop environment inherits
the session manager's `PATH`, not your shell profile's — the recorded absolute
path keeps the integration working. And because `PATH` is now what decides,
a `niwa` earlier on your `PATH` than the one you apply with is the one the hook
runs; if it's too old to know the `worktree from-hook` subcommand the hook fails
loudly rather than falling back, so keep a single niwa on `PATH`.

### When a delegated create fails

Creating the worktree and installing content into it are two steps, and the
second one can fail — a missing secret, an unreadable config source. When it
does, niwa reconciles rather than leaving the half-built worktree behind: it
runs the same guarded teardown described below, so a failed create leaves no
worktree and no `active` session claiming one. The agent's tool call fails with
an error naming both the original cause and what was cleaned up.

The one exception is a worktree that already holds uncommitted work, which can
only happen when one of your own [worktree hooks](#worktree-hooks) touched
tracked files before a later step failed. That worktree is retained and logged,
exactly as a dirty teardown is.

Note that `niwa worktree create` behaves differently on the same failure: it
retains the worktree and tells you to re-sync it with `niwa worktree apply <id>`.
That's deliberate. You're at a terminal and the worktree is a real place you
asked for, whereas an agent never enters the directory on the failure path and
won't come back to it.

### Teardown: clean vs. dirty

When an agent finishes and its worktree teardown fires:

- A **clean** worktree (no uncommitted work) is destroyed and removed, the same
  as a guarded `niwa worktree destroy`.
- A worktree with **uncommitted changes** is retained, not deleted. niwa logs a
  notice naming the session and leaves the work for you. Reclaim it once you've
  reviewed it:

  ```bash
  niwa worktree destroy <id> --force
  ```

  The session record persists, so the worktree still shows up in
  `niwa worktree list` — it's a surfaced orphan you can act on, never a silent
  delete.

### Agent-tool subagents

Claude Code doesn't send `WorktreeRemove` for an Agent-tool subagent's worktree
when a `WorktreeCreate` hook made it and it's a git checkout, which every niwa
worktree is. So `niwa apply` also writes a `SubagentStop` hook running the same
`niwa worktree from-hook` command, and niwa removes the subagent's worktree
there when the subagent finishes. It only ever touches the worktree its own
`WorktreeCreate` hook made for that subagent: Claude Code names an Agent-tool
worktree `agent-<agent id>`, and `SubagentStop` carries the same id. A subagent
without isolation, whose directory is its parent's, matches nothing.

That teardown is stricter than the clean check above. The worktree is removed
only when it holds nothing beyond what it was created with:

- `git status` shows nothing, untracked files included;
- no tracked file is marked assume-unchanged or skip-worktree, since either
  mark hides edits from `git status`;
- its git-ignored files are exactly the ones there when niwa finished setting
  it up, with the same contents and permissions. niwa records a sha256 of each
  at create time.
  Removing a worktree deletes its ignored files, so an ignored file added since
  (build output, a `.env.local`, notes, under any ignore pattern, niwa's own
  `*.local*` included), deleted since, or edited since (a `settings.local.json`,
  a copied env file) keeps it;
- its HEAD and its `session/<id>` branch still point at the commit it started
  from, so nothing was committed and nothing was checked out;
- no stash entry was made from its branch.

Anything else keeps the worktree, and niwa says on stderr why. Remove a kept one
with `niwa worktree destroy <id>` once you're done with it. The hook never exits
2, so it never sends the subagent back to work.

`niwa reap`, and the reap that runs at the start of `niwa create`, `niwa
dispatch` and `niwa watch`, is the backstop for a subagent that ended without a
`SubagentStop`, such as an interrupted run. It also covers a session that
entered a hook-made worktree and never left. It removes an unchanged hook-made
worktree once the Claude Code session that asked for it has ended, and never
while the worktree is younger than the reap idle grace (`NIWA_REAP_IDLE_GRACE`,
a day by default).

How it tells the session has ended depends on what the hook saw. When the
session had a Claude Code job entry (a background worker, listed in the Agent
View), the worktree stays for as long as that entry is listed, however long
the worker sits idle: a held worker is still resumable, and resuming it finds
its worktree. Once the entry is gone, the next reap removes it. If the jobs
directory is missing or can't be read, it stays. For any other session (one
with no job entry, a payload whose `session_id` isn't a well-formed session
id, or a worktree recorded before niwa kept the session id), the transcript
decides: the worktree goes once the transcript is gone or hasn't been written
for the grace. A session like that, left idle for longer than the grace and
then resumed, finds a clean worktree it was in gone, the same rule reap applies
to idle ephemeral instances. A hook-made worktree whose payload carried no
transcript path, and no job entry, can't be judged and stays.

Before it removes one, the sweep asks the workspace's
[pre-removal command](ephemeral-session-instances.md#the-pre-removal-command)
with `NIWA_REMOVE_STEP=worktree-sweep`, and a refusal keeps the worktree for
that reap. Worktrees from `niwa worktree create`, hook-made ones recorded
before the hook marked them, and those in an instance created with
`--no-worktree-delegation` are never removed this way.

### Fallback on an older harness

The hooks need a Claude Code version that supports per-repo worktree hooks. On
each apply, niwa runs `claude --version` once to check. Above the known-good
baseline, it installs the hooks. Below it, niwa can't honor the integration, so
instead of letting the agent make a degraded bare worktree it denies the native
worktree tool and steers the agent to `niwa worktree create`. The same probe
result applies to every settings file one command writes, so the instance root,
the workspace root and each repo all get the hooks or all get the deny.

This fallback is disclosed, not silent. Because an old harness stays old across
applies, niwa prints the fallback warning on **every** apply, with a one-time
explainer the first time it kicks in. If you see it, run `niwa worktree create`
to get a managed worktree.

If `claude` isn't on your PATH or the probe can't read a version, niwa assumes
the harness is supported and installs the hooks — it won't deny the tool on a
guess.

### Opting out

To keep Claude Code's built-in worktree behavior instead, opt the instance out
at init:

```bash
niwa init <name> --no-worktree-delegation
```

This skips the whole integration — no probe, no hooks, no deny fallback, and at
the instance root no guard and no worktree sentence in the session-start context
(both described below). It's persisted in state, like `--skip-global` and
`--no-overlay`: `niwa init` records it at the workspace root, and `niwa create`
copies it into each instance it creates. From then on it belongs to the
instance. Every apply reads it from the instance's own state, and the hooks at
the shared workspace root check it at runtime, so a session launched there can't
create a niwa worktree in an opted-out instance either. `niwa apply` never
changes the setting, and no other command does; an instance keeps the choice it
was created with.

`--no-worktree-delegation` is per instance. The workspace root has a separate
switch, [`worktree_delegation`](#turning-off-the-workspace-root-integration),
that leaves every instance alone.

## Worktrees from a session at a root

A Claude Code session doesn't have to start inside a repo. `niwa dispatch`
starts its workers at an instance root, the directory holding the
`<group>/<repo>` clones, and an ephemeral session starts at the workspace root
before it moves into the instance it was given. Neither directory is a repo, so
niwa has to be told which repo a worktree is for. It also has to stop the two
ways of building a worktree by hand that can't work from there.

niwa writes three things into the root settings for this:

| Entry | Instance root | Workspace root |
|---|---|---|
| `WorktreeCreate` / `WorktreeRemove` hooks (`niwa worktree from-hook`) | Yes | Yes |
| `PreToolUse` guard (`niwa worktree guard`), matcher `EnterWorktree\|Bash` | Yes | Yes |
| `SessionStart` context (`niwa instance context`) | Yes | No |

Every create or apply of an instance writes its instance root. `niwa init`
writes the workspace root (unless you pass `--no-ephemeral-sessions`), and
`niwa apply` run from the workspace root rewrites it. Repo settings keep the
`WorktreeCreate`/`WorktreeRemove` hooks they've always had and never get the
guard or the context hook, so a session launched inside a repo or inside a niwa
worktree behaves exactly as before.

### Naming the repo

From a root, name the worktree after the repo it's for when calling
`EnterWorktree`:

```text
EnterWorktree(name: "niwa/fix-login")          # <repo>/<slug>
EnterWorktree(name: "public/niwa/fix-login")   # <group>/<repo>/<slug>
```

The prefix picks the repo and the rest of the name becomes the slug, which niwa
records as the worktree's purpose. You get an ordinary niwa worktree: a
`session/<id>` branch, a directory under `<instance>/.niwa/worktrees/`, the
inherited environment and CLAUDE content, and a row in `niwa worktree list`.
The slug never reaches a branch name or a path, so it can contain more slashes
(`niwa/fix/login` has the slug `fix/login`).

`niwa worktree from-hook` picks the repo in this order and stops at the first
rule that applies:

1. A `<group>/<repo>/` prefix, then a `<repo>/` prefix, matched exactly and
   case-sensitively against the repos cloned in the instance. `Niwa/fix-x`
   doesn't match `niwa`; it's treated as a name with no prefix. A prefix is
   only ever compared against that list; niwa never builds a path from it.
2. A prefix naming a repo the instance lists but hasn't cloned is refused with
   `repo "<name>" is configured in this instance but its clone is missing; run
   niwa apply`, rather than falling through to the rules below.
3. The repo whose clone contains the session's current directory. A session
   that has `cd`'d into `public/koto` gets a `koto` worktree from an unprefixed
   name. A directory inside the instance but outside every repo (the instance
   root, a group directory, `.niwa/`) counts as no repo.
4. The only repo, when the instance has exactly one.
5. The repo of the niwa worktree that contains the session's current
   directory. A session that entered `niwa/fix-x` and then asks for another
   worktree gets one in `niwa`.

A name with a `/` that none of these rules resolved, such as
`nosuchrepo/fix-x`, fails here: the tool call returns an error starting
`no repo selected` that lists the instance's repos and gives an example using
the repo a bare name would land in without `NIWA_WORKTREE_REPO` (rule 7 or 8
below):

```text
niwa: error: no repo selected; name the worktree <repo>/<slug> or <group>/<repo>/<slug>, for example koto/<slug>. Repos in this instance: koto, niwa
```

A name with no `/` goes on to three more rules:

6. The repo the `NIWA_WORKTREE_REPO` environment variable names. The hook
   reads it from the environment Claude Code runs hooks in, which is the one
   the session was launched with, so set it in the shell that starts
   `claude`. A value that names no repo cloned in the instance fails the
   create with an error naming the variable.
7. The repo the workspace config names in `[workspace] default_worktree_repo`
   (see below). A value that names no repo cloned in the instance fails the
   create with an error naming the key.
8. The instance's first repo, ordered by group and then by name. Claude Code's
   Agent tool picks a bare name such as `agent-a1b2c3` itself when it isolates
   a subagent, and fails the subagent if the hook refuses, so a session at the
   root of a multi-repo instance gets a worktree in a fixed repo rather than an
   error. niwa prints a notice on stderr naming that repo and the three ways to
   choose another: a `<repo>/<slug>` name, `NIWA_WORKTREE_REPO`, or
   `default_worktree_repo`.

### Choosing the default repo for a workspace

The first-repo rule always picks the same repo, but nobody chose it: sorting by
group puts a `private` group ahead of `public`, for example. A workspace that
wants its root sessions' subagents somewhere else names the repo in its
`workspace.toml`:

```toml
[workspace]
name = "my-project"
default_worktree_repo = "app"
```

Run `niwa apply` after changing it. The hook doesn't load the workspace config,
so apply records the value in each instance's `.niwa/instance.json`, and the
hook reads it from there. A bare name from the root then lands in `app` with no
notice. Removing the key and applying again goes back to the first-repo rule.
If the value names no repo in the workspace, apply warns. While it names no
repo cloned in the instance, the hook refuses bare names from the root, as it
does for an unknown `NIWA_WORKTREE_REPO`, rather than quietly using another
repo. A single-repo instance uses its one repo whatever the key says (rule 4).

`NIWA_WORKTREE_REPO` still wins over the configured default, so one session can
pick its own repo by setting it in the shell that starts `claude`. niwa doesn't
write the default into the root settings' `env` for this reason: Claude Code
applies a settings `env` value over the variable the session was launched
with, and the session's own choice would lose. The key lives on `[workspace]`,
which a workspace overlay can't set.

A name with an empty slug, such as `niwa/`, is refused with `no repo selected`
whatever the other rules say. When the
prefix and the current directory point at different repos, the prefix wins,
since it's what the agent asked for at the moment of the call. A name whose
leading segments match no repo has no prefix at all: when the current
directory picks the repo (rule 3 or 5), its whole text is the slug, and
otherwise it's refused as above.

### Which instance a hook acts on

The hook doesn't trust the payload's `cwd` to find the instance, because `cwd`
follows whatever directory the agent `cd`'d into. It anchors on the directory
the session was launched in instead: `CLAUDE_PROJECT_DIR`, which Claude Code
sets for hooks, or, when that's unset, the hook process's own working directory,
which Claude Code starts in the launch directory. Both are fixed by the harness.

- **Launched at an instance root, in a repo, or in a niwa worktree.** The
  instance is the nearest one at or above the launch directory. The payload's
  `cwd` must lie inside it, or the call is refused as outside the instance the
  session was launched in. That stops a session from planting a fake
  `.niwa/instance.json` somewhere else and pointing the hook at it.
- **Launched at the workspace root.** There's no instance above the launch
  directory, so niwa looks for the instance directly under the workspace root
  that contains `cwd`. An ephemeral session that has moved into its instance
  resolves to that instance, and the naming rules above apply from there. A
  session still sitting at the workspace root resolves to nothing, and the
  create fails with the instances it could move into (up to 20 names, then a
  count of the rest):

  ```text
  niwa: error: not inside a niwa instance; cd into one of: rs, rs-2
  ```

In a single-instance layout the workspace root is the instance, so it resolves
the way an instance root does.

Removal uses the same anchor, but finds the instance from the worktree's path
rather than from `cwd`, so a `WorktreeRemove` sent from the workspace root still
reaches a worktree inside an instance. Teardown is the usual one: a clean
worktree is removed, one with uncommitted work is kept and logged (see
[Teardown: clean vs. dirty](#teardown-clean-vs-dirty)).

### The guard

Two routes look reasonable from a root and don't work there. A worktree built
with `git worktree add` isn't one niwa tracks, and entering it with
`EnterWorktree(path: ...)` raises Claude Code's relocation prompt, which no
permission rule can approve ahead of time outside bypass mode. The guard is a
`PreToolUse` hook that refuses both at the call, before anything happens, and
tells the agent the form that works:

- **`git worktree add`.** A Bash command is refused when any simple command in
  it runs `git`, optionally followed by global options (`-C <dir>`,
  `-c <key=value>`, `--git-dir=...`, `--work-tree=...`, or any other option),
  then `worktree add`. The command line is split on unquoted `;`, `&&`, `||`,
  `|`, `&` and newlines. Leading `VAR=value` assignments and an `env` word are
  skipped, heredoc bodies are skipped, and `git` matches by basename, so
  `/usr/bin/git` counts.
- **`EnterWorktree` with a `path`.** The path is joined with `cwd`, cleaned and
  symlink-resolved, and the call is refused unless the result is a directory
  under the instance's `.niwa/worktrees/`. Entering a niwa worktree that
  already exists by path is allowed. A path that doesn't exist is refused,
  since every allowed target exists.

Everything else passes: `EnterWorktree` with a `name` or with neither field,
the other `git worktree` subcommands (`list`, `move`, `remove`, `lock`,
`unlock`, `prune`, `repair`), and text that only mentions the words, like
`grep "git worktree add"`. A refusal is a tool error the agent reads, not a
permission prompt:

```text
niwa: a worktree built with git worktree add isn't one niwa manages. Instead, use EnterWorktree with name koto/<slug> (or <group>/<repo>/<slug>); niwa creates and tracks the worktree. Repos in this instance: koto, niwa.
```

At the workspace root, with no instance resolved, the same two forms are
refused with the instance names in place of repos (`This session is at a
workspace root; cd into one of: ...`). In an opted-out instance the guard
allows every call.

The guard steers agents toward the form that works. It isn't a security
boundary, and it doesn't try to be. It reads plain simple commands only, so it
doesn't inspect:

- strings passed to `bash -c`, `sh -c` or `eval`
- command substitution, subshells, variables and shell aliases, including a
  command that builds the word `worktree` at run time
- commands run through a wrapper such as `exec`, `command`, `time`, `nice` or
  `nohup`
- a command line it can't tokenize, such as one with an unclosed quote

Any of those goes through. For the `EnterWorktree` route, the `WorktreeCreate`
hook is still what makes the worktree a niwa one.

The guard runs on every Bash call in a root session, so it's built to stay out
of the way. It only looks further at an `EnterWorktree` call or a Bash command
containing the word `worktree`; everything else returns at once. Its decision
starts no process and makes no network call. It has a 5-second timeout instead
of Claude Code's 600-second default, and it fails open: input it can't read or
parse, an internal error, a timeout, or a niwa too old to have the subcommand
all let the call proceed, with any error on stderr. It blocks only by exiting 2,
which Claude Code honors in every permission mode.

### Session-start context

A session at an instance root starts with three facts about where it is,
delivered by the `niwa instance context` hook as `SessionStart` context:

```text
This directory is a niwa instance root.
Repos in this instance:
- public/koto
- public/niwa
Worktrees here are created with EnterWorktree using a name of the form <repo>/<slug>.
```

The hook is registered for the `startup`, `resume`, `clear` and `compact`
sources, so the facts come back after a compaction instead of living in a file
the session read once. The repo list stops at 50 entries and counts the rest. In
an opted-out instance the last sentence is left out. The text states facts
rather than giving orders, because imperative text injected into a session's
context can look like a prompt injection to the model.

This replaces two bullets the generated `workspace-context.md` used to carry,
telling agents to move into a repo before running git commands and before
making changes. They're gone.

An ephemeral session gets the same block appended to the provisioning context
it already receives, for the instance it was given. The workspace root's own
settings carry no context hook: run there outside an instance, the command
prints nothing.

The hook command keeps niwa's output only when it starts with `{`, so a niwa too
old to have `niwa instance context` can't leak its help text into the session's
context. It has a 10-second timeout.

### Turning off the workspace-root integration

The workspace root is shared by every instance, so it has its own switch in
`workspace.toml`:

```toml
[claude]
worktree_delegation = false
```

With it off, the workspace root's settings carry no `WorktreeCreate` or
`WorktreeRemove` hook, no deny fallback and no guard, and the next `niwa apply`
from the workspace root removes them from a root that had them. It's on when the
key is absent. The switch covers the workspace root only: every instance still
gets the integration at its own root and in its repos. To opt one instance out
entirely, use [`niwa init --no-worktree-delegation`](#opting-out).

### When the root isn't trusted

Claude Code checks workspace trust before it resolves any hook source. In a
directory where neither the directory itself nor anything above it is trusted,
it runs none of the root hooks: no worktree integration, no guard, no context.
It also won't start a background session there, so a `niwa dispatch` worker
that's running at all is under a trusted directory.

niwa doesn't change your trust records to work around this. Instead, after it
writes the integration into a root, it reads Claude Code's user config
(read-only) and warns when the root has no trusted ancestor:

```text
warning: /home/me/ws/rs is untrusted in Claude Code, so it won't run niwa's worktree hooks there; trust the workspace root in Claude Code, or set [claude] trust_instances = true
```

The instance-root check runs on every create and apply of an instance; the
workspace-root check runs on `niwa init` and on `niwa apply` from the workspace
root. If the config can't be read or parsed, niwa says nothing rather than
guess.

There are two fixes. Trusting the workspace root in Claude Code covers it and
every instance under it. Setting `trust_instances = true` has niwa mark each
instance root and its repos trusted (see [Instance trust](instance-trust.md)),
which covers sessions launched at an instance root but not the workspace root
itself.

### Native checkouts from earlier sessions

Before niwa wrote hooks at the roots, a root session's settings had no
`WorktreeCreate` hook. A session that `cd`'d into a repo and called
`EnterWorktree(name: ...)` therefore took Claude Code's built-in path, which
creates a plain git worktree at `<repo>/.claude/worktrees/<name>`. That's
where the checkouts you may find in older instances came from. The same thing
still happens wherever no niwa `WorktreeCreate` hook is installed: an opted-out
instance, or a workspace root with `worktree_delegation = false`.

niwa doesn't track these checkouts. They don't appear in `niwa worktree list`,
`niwa apply` doesn't refresh their environment, and `niwa worktree destroy`
can't name them. After upgrading, the same request from a root session gets a
niwa worktree instead, but the old checkouts stay until you remove them
yourself or the whole instance goes away.

From the repo's main checkout, list them:

```bash
git worktree list
```

Before removing one, check it for work you'd lose. `git worktree remove`
refuses a worktree with uncommitted changes, but it won't warn you about
commits that were never pushed:

```bash
git -C .claude/worktrees/<name> status --short
git -C .claude/worktrees/<name> log --oneline @{upstream}..   # unpushed commits, if the branch tracks one
git log --oneline <branch> --not --remotes                    # commits on the branch no remote has
```

Then remove the worktree and, once you're sure its commits are merged or
pushed, its branch (the branch name is the one `git worktree list` shows in
brackets):

```bash
git worktree remove .claude/worktrees/<name>
git branch -d <branch>     # refuses an unmerged branch; -D deletes regardless
```

If the directory was already deleted by hand, `git worktree prune` clears the
stale entry.

## Customizing the worktree content layer

By default the worktree layer is a short generated section naming the repo,
purpose, and branch. To control it, set a template in the workspace config:

```toml
[content.worktree]
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

### `niwa worktree create [repo] [purpose]`

Creates a worktree for a repo: scaffolds the worktree on a new branch,
installs the repo's CLAUDE content plus the worktree rules import and the
purpose/branch layer, runs worktree hooks, runs the repo's own
[setup scripts](setup-scripts.md) if it has opted in, and writes the state file.
The worktree inherits the instance clone's already-materialized environment; it
resolves no secrets (see
[How a worktree gets its environment](#how-a-worktree-gets-its-environment)). If
the clone has no env output to inherit, create exits non-zero and points you at
`niwa apply`.

Both positionals are optional.

- **`repo`** — when omitted, niwa infers it from your current directory: the
  repo whose checkout contains your working directory. A bare
  `niwa worktree create` run from inside a workspace repo just works. If your
  directory isn't inside any workspace repo, niwa exits with code 2 and tells
  you to pass the repo explicitly.
- **`purpose`** — when omitted, niwa uses a generic `session` purpose. Supply a
  description when you want the worktree's CLAUDE.local.md layer and the
  `niwa worktree list` PURPOSE column to say what the worktree is for.

```bash
niwa worktree create niwa "implement the worktree apply command"
niwa worktree create niwa            # repo named, generic purpose
niwa worktree create                 # repo inferred from cwd, generic purpose
```

On success niwa prints the created id and worktree path, lists the content
files it wrote, and (with shell integration active) navigates your shell into
the worktree.

If content installation fails after the worktree is created, niwa reports the
error but leaves the worktree in place — you can re-sync it with
`niwa worktree apply`.

#### Machine-readable output: `--json`

Pass `--json` to emit a single JSON object instead of the human summary and
content-file lines. Use it when a script needs the worktree path or session id
without scraping prose:

```bash
niwa worktree create niwa "spike" --json
```

```json
{
  "session_id": "ab12cd34",
  "worktree_path": "/abs/path/to/.niwa/worktrees/niwa-ab12cd34",
  "repo": "niwa",
  "purpose": "spike",
  "branch": "session/ab12cd34"
}
```

The `worktree_path` and `session_id` fields are stable; more fields may be
added later without breaking callers that read these. The shell-integration
landing behavior is unchanged in `--json` mode — your shell still lands in the
new worktree.

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

Like create, it re-syncs the worktree's environment by inheriting the clone's
materialized output — no secret resolution. For a workspace-wide refresh that
updates clones and every worktree in one pass, run `niwa apply` instead.

### `niwa worktree destroy <target> [--force]`

Marks the worktree ended, removes the working directory, and deletes the branch
when it's already merged (use `--force` to delete regardless).

`--force` applies to a single worktree only — it is a usage error with a session
id or handle. See [Guards](#guards) for why.

```bash
niwa worktree destroy ab12cd34
niwa worktree destroy ab12cd34 --force
```

#### What you can name

There are four ways to name what to tear down, and they fall into two groups.

**One worktree**, which is what destroy has always done:

| Target | Example |
|---|---|
| Worktree id | `niwa worktree destroy ab12cd34` |
| `--by-path <path>` | `niwa worktree destroy --by-path /abs/path/to/.niwa/worktrees/niwa-ab12cd34` |

**A whole session's worktrees**, which is newer:

| Target | Example |
|---|---|
| Session id | `niwa worktree destroy 6f1f8a0e-1f1a-4a3b-9c2d-5e6f70818283` |
| Session handle | `niwa worktree destroy brave-otter` |

The session forms exist because those are the ids you actually have. A
dispatched session is known by its agent session id and by the short handle
`niwa list` shows; neither appears anywhere in the worktree lifecycle store, so
before this existed you had to look the worktree id up first — and if you
didn't, teardown fell through to `niwa reap`, which has no merged-branch or
uncommitted-work guard at all.

A session with no recorded handle can also be named by the first eight
characters of its session id. A session that *does* record a handle is named by
that handle and not by its id prefix, so one session never answers to two short
forms.

A session target destroys **every active worktree of that session's instance**,
in worktree-id order, continuing past a refusal. It never removes the session
mapping, the instance directory, or the repositories cloned inside it — the
instance outlives its worktrees, and reclaiming it is `niwa reap`'s job.

A session that no longer backs its instance — because a newer session was
dispatched into it — is refused, and the error names the newer one. That check
is currently best-effort: it reads the session mapping store without a lock, so
a read taken while the workspace's configuration is being refreshed can miss the
newer mapping and let the superseded session through. Teardown still acts only
on the instance that session's own mapping named, and the merged-branch,
uncommitted-work and attach guards all still run, so the worst case is a guard
that didn't fire rather than the wrong instance. It closes when the mapping
store's reads are serialized against the config refresh.

Session targets work from anywhere in the workspace: the root, another instance,
or inside a worktree. Worktree ids only mean something inside the instance that
owns them.

#### Ambiguity

Inside an instance, a value can name both a worktree of that instance and a
session. There's no safe default between "remove this one worktree" and "remove
all of that session's", so niwa refuses, names both readings, and exits 4. Pass
`--by-path` for the worktree, or the full session id for the session. At the
workspace root the worktree reading doesn't exist, so the same value resolves to
the session with nothing to disambiguate.

Pass exactly one identifier: a positional target or `--by-path`, not both and
not neither. A `--by-path` path is canonicalized (symlinks resolved, `..` and
trailing slashes normalized) before the lookup, so it matches regardless of how
it's spelled.

#### Guards

Two guards protect uncommitted or in-use work:

- **Uncommitted work.** If the worktree has uncommitted changes, destroy refuses
  unless `--force` is passed. The error names the worktree path and the
  commit / stash / `--force` recovery options. This is the worktree analog of
  the instance-level uncommitted-work guard.
- **Active attach.** If an attach lock is held (see
  [Attaching](#attaching-to-a-worktree)), destroy refuses unless `--force` is
  passed. The error carries the holder PID and points at
  `niwa worktree detach <id> --force`.

`--force` applies to one worktree, so it is a usage error with a session id or
handle. Forcing stays available one worktree at a time, through the worktree id
or `--by-path`. A session can back several worktrees, and a mistyped or
prefix-matched id that forced its way through would discard every uncommitted
change and unmerged branch in the instance at once.

#### Destroy exit codes

These are `destroy`'s own codes. They are **not** the same as
[the attach codes](#attach-exit-codes): `attach` uses 3 for lock contention and
`detach --force` uses 4 for killing a live holder. Read the table for the
subcommand you ran.

| Code | Meaning | Output |
|---|---|---|
| 0 | Worktrees destroyed, or nothing left to destroy | stdout: one destroyed line per worktree. stderr: a `warning:` line per branch kept for unmerged commits |
| 1 | At least one worktree refused by a guard, or the session cannot be torn down | stdout: destroyed lines for the others. stderr: one `niwa: error:` line per refusal |
| 2 | Usage error, including `--force` with a session id or handle | stderr: the usage line |
| 3 | The target matched no worktree and no session | stderr: `niwa: error: no worktree or session matches "<value>"` |
| 4 | The target is ambiguous | stderr: the matches, and how to disambiguate |

A session-resolved teardown prints the enriched
`session: destroyed <worktree-id> (<repo>) at <path>` line, because a caller
naming a session needs to know which worktrees went. A worktree id or
`--by-path` keeps the bare `session: destroyed <id>` line.

Two codes moved in the release that added session targets. A worktree id
matching nothing, and a `--by-path` path resolving to no worktree, both exited 1
before and exit 3 now. Both are the same outcome — you named something and niwa
found nothing — and they should not differ by which flag located the target. A
cleanup script running after a reap is exactly the caller that needs to tell
"already gone" from "a guard refused".

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

These are `attach` and `detach`'s codes. `destroy` has
[its own table](#destroy-exit-codes), where 3 and 4 mean something different —
each subcommand documents the codes it returns.

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
