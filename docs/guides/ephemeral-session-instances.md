# Ephemeral session instances

An ephemeral instance is a full niwa instance provisioned for the lifetime of a
single Claude Code background session and reclaimed when you delete that session
from the Agent View. The model is one-to-one: **1 Claude Code background session ==
1 ephemeral niwa instance**. A session that finishes its task, goes idle, or is
suspended keeps its instance — it is still listed and resumable, so you can re-open
it into the same clone — and for a Claude Code session only an explicit delete
tears the instance down, with the exceptions [`niwa reap`](#niwa-reap) lists: a
reap through another mapping of the instance that reads gone, or a session job
entry niwa can't decode, takes the instance and the session; a Codex session's
instance goes after the idle grace; and the backstop takes an unmapped dispatch
instance past its TTL. When
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
by the reaper once you delete the session. For a Claude Code session, teardown
is delete-only: the reaper keys on the session's Claude Code job entry
disappearing (the proxy for deletion), so completed, idle, and suspended
sessions keep their instances and stay resumable, unless another mapping of the
instance reads gone or niwa can't decode the session's own job entry, either of
which lets a reap take the instance and the session. A Codex session's instance
is reclaimed after the idle grace instead, and the backstop takes an unmapped
dispatch instance past its TTL.

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
niwa reap  (on demand, or when niwa dispatch, create or watch runs)
           sees the job entry gone, checks the instance for work found
           nowhere else, runs the on-reap command (if declared), removes
           the instance, deletes the mapping once the command succeeds
```

The reaper is the single automatic teardown path; `niwa destroy` is the manual one
(see [Destroying an instance by hand](#destroying-an-instance-by-hand)). It runs
on demand and opportunistically when `niwa dispatch`, `niwa create` or
`niwa watch` runs. Until you delete the session, its instance survives — a
completed-but-resumable session can be re-opened into the same clone. The
exceptions are in [`niwa reap`](#niwa-reap): a reap through another mapping of
the instance that reads gone, or a session job entry niwa can't decode, takes
the instance and the session; a Codex session's instance is reclaimed after the
idle grace; and the backstop takes an unmapped dispatch instance past its TTL.

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
| `dispatch_id` | Set by `niwa dispatch`: the dispatch's own id. It is also the session's reap key (see [The reap key and the on-reap command](#the-reap-key-and-the-on-reap-command)). |
| `on_reap_failures` | How many on-reap runs failed for this session after it was removed. Absent until one fails; a mapping written before the field existed reads as zero. |

`session_id` flows from untrusted hook stdin straight into a path component and
command arguments, so it is validated against the UUID format before any path is
constructed. An invalid id is rejected without touching the filesystem. Writes
are atomic (write-temp-then-rename).

The on-disk instance directory is never renamed mid-session — renaming would
break the running session's cwd — so durable identity stays the `session_id`,
and any friendly slug is cosmetic metadata in `label`.

## Teardown

For a Claude Code session, teardown is **delete-only** and driven entirely by
the reaper. Its ephemeral instance is reclaimed only when you delete its backing
session from the Agent View (Ctrl+X twice in `claude agents`), which removes the
session's `~/.claude/jobs/<id>/` entry — the signal the reaper keys on. A
session that finishes its task, goes idle, or is suspended keeps its instance
and stays resumable, unless a reap of its instance through another mapping
that reads gone removes it, or niwa can't decode the session's own job entry,
which reads as gone (see [`niwa reap`](#niwa-reap)); either way the reap
removes the session too. Two removals don't wait for a
delete: a Codex session's instance goes once nobody has worked in it for the
idle grace and no writer holds its lock, and the backstop takes an unmapped
dispatch instance past its 30-minute TTL. All of this holds only while the
agent can't write the files the reaper reads: one that can delete or rewrite
the job entry, or rewrite the mapping, can make the reaper reclaim the
instance sooner (see
[What to deny the agent writes to](#what-to-deny-the-agent-writes-to)).

`SessionEnd` is **not** a teardown trigger. It fires on idle-suspend, `/clear`,
logout, and similar, none of which mean the session was deleted, so the
`niwa instance from-hook` SessionEnd branch is a no-op — it never destroys an
instance. (Earlier versions tore the instance down on `SessionEnd`, which reclaimed
instances the moment a worker finished a task or went idle; that was the bug this
behavior fixes.)

See [`niwa reap`](#niwa-reap) for the reclamation mechanics and the liveness rule.

### Destroying an instance by hand

`niwa destroy` (and `niwa destroy --workspace`, for every instance) also removes
the Claude Code background session that owns the instance, so the session doesn't
stay in the Agent View pointing at a directory that no longer exists. The owner is
the session the mapping store names for the instance. A session you started by
hand inside an instance has no mapping, and destroy leaves it alone.

- **Transcript.** destroy removes the session with `claude rm <id>`, which deletes
  the Agent View entry, the session's job entry and any worktree Claude Code
  created for it, but not its transcript. The
  conversation stays under `~/.claude/projects/`, and `claude --resume
  <session-id>` still opens it until Claude Code's own transcript retention
  removes it. destroy prints the command:

  ```
  removed Claude session 7a920b0c; its transcript is kept: claude --resume <session-id>
  ```

- **A running session stops the destroy.** Before any prompt, destroy reads
  `claude agents --json --all` once, and a mapped session whose process is still
  alive (the row carries a `pid` or `status`) keeps the instance, finished or
  not. The workspace's pre-removal command is asked too (see
  [The pre-removal command](#the-pre-removal-command)), and destroy lists every
  reason it found, exits non-zero and removes nothing:

  ```
  niwa: my-ws+fix-0123abcd is still needed:
    - Claude session 7a920b0c (fix-thing) is still running
    - reason from before_remove: "run 42 is still bound here"
  ```

  A worker that has finished its task often keeps an idle process alive for a
  while, so this is common right after a dispatch completes, and it is what
  happens when a session destroys its own instance. destroy never stops a
  session for you. `--force` prints the same reasons as overridden and destroys
  the instance anyway; the running session is still left in place:

  ```
  kept Claude session 7a920b0c: it is still running; once it stops (claude stop 7a920b0c), remove it with claude rm 7a920b0c
  ```

  `niwa destroy --workspace` refuses as a whole when any instance is still
  needed, naming each one, and takes no `--force`. Destroy the others by name,
  or run it again once nothing needs them.

- **Unpushed work.** destroy's own unpushed-work check runs first and is
  unchanged. It covers linked worktrees inside the instance, which is where a
  session's worktree lives. destroy never passes `--discard-unpushed` or
  `--force-remove-worktree` to `claude rm`. If Claude Code still refuses, destroy
  reports the refusal and the session stays listed.
- **A listing that fails refuses.** If `claude` isn't on `PATH` or its listing
  can't be read, destroy can't tell whether a mapped session is running, so it
  refuses an instance that has one and says the listing failed. `--force`
  proceeds. An instance with no mapped session never needs the listing.
- **Other failures don't block the teardown.** If `claude rm` fails, destroy
  warns, leaves the session, and still removes the instance:

  ```
  warning: could not remove Claude session 7a920b0c: <claude's message>; remove it with claude rm 7a920b0c
  ```

- **Keeping it.** `niwa destroy --keep-session` removes no session and leaves it
  listed. A running session still stops the destroy.

When destroy removes a session, or finds it already gone from the Agent View, it
runs the workspace's on-reap command for it (if one is declared) and deletes the
session's mapping once that command succeeds. A failed run keeps the mapping, and
destroy never retries it; the next `niwa reap` does. A kept session keeps its
mapping, and so does a session whose removal failed: neither runs the command.

## `niwa reap`

```bash
niwa reap
```

`reap` reclaims ephemeral instances whose backing session you have deleted. It
enumerates the workspace's instances (`niwa list --json`), joins each against its
session mapping, and removes a mapped instance only when all three hold:

- a session mapping marks the instance **ephemeral**,
- its session is **deleted** by the liveness rule, and
- the removal rule finds nothing in the unit that exists nowhere else.

The name+TTL backstop is the one other path: it takes an unmapped instance
whose name has the dispatch form once it's older than 30 minutes, under the
same removal rule (see
[Never reap a live instance, mapped or not](#never-reap-a-live-instance-mapped-or-not)).

It also walks the stored session mappings for those whose instance is already
gone, and when the workspace declares an on-reap command, it runs it for each
removed session and keeps the mapping until it succeeds (see
[The reap key and the on-reap command](#the-reap-key-and-the-on-reap-command)).

What a reap decides depends on files outside the instance it's judging, and on
programs those files name. A workspace whose agents could write them should
deny that; [What to deny the agent writes to](#what-to-deny-the-agent-writes-to)
lists each place and why it matters.

### The liveness rule

A Claude Code session is **live** as long as its job entry at
`~/.claude/jobs/<session-id>/state.json` (under `CLAUDE_CONFIG_DIR` when that's
set; [What to deny the agent writes to](#what-to-deny-the-agent-writes-to) says
where) exists, decodes, and records that session's `sessionId` when it records
one. It is **dead** when that entry is gone, and an entry that won't decode or
names another session reads the same way.

This is the whole rule: **entry present = live, entry gone = deleted.** Job-entry
presence is a faithful proxy for "the session still exists in the Agent View,"
covering both a running session and an idle-but-resumable one (finished its task,
suspended, but still listed). A completed or idle session keeps its entry — and so
keeps its instance. Only deleting the session removes the entry, and that delete is
what the reaper reclaims on.

A Claude Code session's liveness deliberately does **not** look at the job
`state` label, at `firstTerminalAt`, at an idle TTL, or at transcript mtime.
Each of those is true of a live-but-resumable session, so keying on any of them
would reap an instance whose session you might still re-open. (That is exactly
what the earlier rule did, and the bug it caused.) A Codex session's mapping is
read by activity and the idle grace instead, and the worktree sweep reads
transcripts; the deny list below covers both.

The primary (mapping-keyed) sweep takes only an instance a session mapping
marks ephemeral, and never on the marker alone — the session must be gone. The
mapping is what marks it: a mapping whose `instance_path` names an instance and
whose `ephemeral` is true makes that instance a target, a developer instance
included, which is why the mapping store is on the deny list. An ephemeral
instance with no resolvable mapping is skipped by the primary sweep — without a
session id the entry-present liveness rule can't run. The backstop never takes
a developer instance under its usual name, since that name lacks the dispatch
form, but an instance renamed into that form is a backstop candidate once it's
past the TTL.

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

`reap` reclaims a mapped Claude Code instance only after the judged session
reads gone (its job entry is gone, won't decode, or names another session; a
Codex mapping goes by activity instead), and it spares any instance a job is
still rooted in.

**The `claude rm` step.** Once a unit has passed liveness (the judged mapping
reads gone and no rooted-worker guard spares the instance), the removal rule,
the pre-removal command and the re-read, the reap removes Claude sessions with
`claude rm`. It takes every stopped session Claude Code still lists that any
Claude mapping names for the instance and that still has a job directory, not
only the judged one. A listing that fails, a listed session that is still
running, an id niwa won't pass to a command, a job directory that fails niwa's
checks or a `claude rm` that fails keeps the unit. That differs from
`niwa destroy`, which warns on a failed `claude rm` and still removes the
instance. Today this step runs before the on-reap command, `PrepareDestroy`
and the tombstone re-check, so a unit one of those later checks keeps can be
left with its session already removed, until a later change in this release
moves the step after them.

So a finished session you haven't looked at yet keeps its Agent View entry and
its instance until you delete it, unless the reap takes its instance through
another mapping that reads gone, or can't decode the session's own job entry.
Then the reap removes that session too.

`reap` runs on demand and is also invoked opportunistically at the start of
`niwa create`, and by `niwa dispatch` and `niwa watch`, so session fan-out
self-bounds. The opportunistic call never fails the command that runs it: a
reap error there is swallowed. The reap's own lines still print on stderr: the
instances it reclaimed, kept or spared, and the worktree sweep's removals and
warnings.

> **One known edge.** The Agent View stops a finished background session's
> supervisor process after roughly an hour. It is not yet confirmed whether that
> stop also removes the `~/.claude/jobs/<id>/` entry. If it does, a reap after the
> stop could reclaim an instance whose session is still resumable — a much narrower
> window than the old reap-on-completion behavior, and one the delete-only contract
> accepts. If this ever bites you, re-run the session's work in a fresh dispatch; a
> longer-lived safety-net TTL for genuinely abandoned instances is a possible future
> addition.

### What to deny the agent writes to

A reap checks each unit for work that exists nowhere else before it removes
anything, and keeps the unit when it can't tell. Most of what it reads sits
inside the instance, where niwa can treat everything as the agent's. Some of it
doesn't: the reap also trusts files outside the instance to say what counts
as pushed, which instances it may act on and which sessions are live, and it
runs programs that the person's own git, ssh and Claude Code configuration,
and the workspace's own commands, name. niwa runs as the person, and so does
the agent, so niwa can't stop the agent from writing those files. Whether it
can is up to the sandbox the agent runs under.

**The mechanism.** Deny the agent writes to every place below with sandbox
write restrictions that also cover Bash: Claude Code's sandbox, or an
operating-system equivalent. A path deny rule in Claude Code's permission
settings isn't enough on its own. It covers Claude Code's file tools, not a
shell command that writes the same path, so `echo ... >> ~/.gitconfig` from
Bash gets past it.

**The list.**

- **`~/.niwa/`, all of it.** This is niwa's own state. It holds the
  instance and tombstone locks (`~/.niwa/locks/`) and the scratch directory,
  `~/.niwa/w-<key>/scratch/`. Each pass makes a fresh directory there, mode
  0700, holding the scratch object stores the network reads land in and the
  empty directories that git's network calls, git's version read, every
  `claude` call and the credential-helper read run from. niwa checks the
  owner and mode before each use and keeps the unit when a check fails, but
  an agent that can write there can plant a store or a file between checks.
  The record ledger and the kept set that later releases add live under
  `~/.niwa/` too.
- **The workspace root's `.niwa/`.** Its `workspace.toml` holds the clone
  URLs and the `[session.removal]` `proof_remotes` prefixes, which decide
  which hosts count as proof that a commit is pushed and which hosts get the
  person's credential helper. An agent that can write it chooses both. The
  rest of `[session.removal]` (`disposable`, `ignored_files`, `job_scratch`)
  and each repository's `build_trees` decide which files the removal rule
  treats as disposable rather than as work. The same file holds the
  `[session.on_reap]` command, which a reap runs as the person before it
  removes a session's instance, and the `[session.before_remove]` command,
  whose exit status decides whether a removal goes ahead. Both run under
  `/bin/sh -c` from the workspace root. The session mapping store,
  `.niwa/sessions/<session id>.json`, lives here too. A reap acts on the
  instances an ephemeral mapping names, and through the backstop on unmapped
  dispatch-named instances older than its 30-minute TTL. Each mapping
  records the session id and the agent, and the agent decides where that
  session's liveness is read from: Claude Code's jobs root, or the Codex
  session store. An agent that can write a mapping chooses which instance a
  mapped reap acts on, a developer instance included, since a mapping's
  `instance_path` with `ephemeral` true is what marks an instance ephemeral;
  it also chooses which session the reap asks about and where it looks. And
  it chooses which sessions the reap removes: once a reap takes an
  instance, it runs `claude rm` on every stopped Claude session Claude Code
  still lists that any mapping names for that instance, so a written mapping
  can get another stopped session removed. Deleting a mapping makes its
  instance a backstop candidate, whose liveness then rests only on the
  rooted-worker guards and the TTL. Each mapping also
  carries the count of failed on-reap runs that decides whether a later reap
  runs the command again or drops the mapping.
- **The workspace root directory itself, and every instance in it other
  than the agent's own.** A reap lists instances by the root's directory
  entries, and the backstop takes only an unmapped one whose name has the
  dispatch form, `<config>+<slug>-<8 hex>` (the slug may be empty). It ages
  that instance by the timestamp in its `.niwa/dispatch-pending`, or by the
  instance directory's mtime when the marker is missing or can't be read. An
  agent that can create or rename entries in the root, or write a sibling
  instance's marker or its directory's mtime, decides which instances the
  backstop may take and when their TTL runs out. A sibling's `.niwa/` also
  holds its worktree records, which the worktree sweep reads to decide a
  session has ended, and its `.niwa/dispatch-retain` hold marker. The
  removal rule still keeps an instance that holds work found nowhere else.
- **The root's `.niwa-tombstone-*` entries.** A pass renames an instance to
  `<root>/.niwa-tombstone-<instance>-<8 hex>`, checks it again there and only
  then deletes it. A later pass recovers a tombstone an interrupted pass left.
  An agent that can create or swap one controls what that recovery finds.
- **`~/.gitconfig` and `~/.config/git/config`.** niwa reads the person's
  `credential.helper` from these two files once per pass, and that helper is
  the program its proof-remote network calls run. The read is
  `git config --global --no-includes`, and niwa's environment for it carries
  no `XDG_CONFIG_HOME`, so it reads `~/.config/git/config` even when your
  shell's `XDG_CONFIG_HOME` points elsewhere. Every other git call the removal
  pass makes goes through the pass's one git builder, which ignores both
  files and the system configuration (`GIT_CONFIG_GLOBAL=/dev/null`,
  `GIT_CONFIG_NOSYSTEM=1`). Other git calls in niwa don't: the reap's
  worktree sweep, `niwa destroy`'s scan, clone and sync all read the full
  global and system configuration. The bullet on the worktree sweep and the
  destroy scan below says what that adds to this list.
- **git's global and system attributes files**, `~/.config/git/attributes`
  (the pass's git gets no `XDG_CONFIG_HOME`) and the `gitattributes` file in
  git's own `etc/` directory, which for a Homebrew git sits inside a prefix
  that is often user-owned. The removal pass's git doesn't set these aside,
  so they apply to every status it runs. How far they can change what a
  status reports isn't established; deny writes to them anyway.
- **The whole `~/.ssh/` directory, and every file outside it that an
  `Include` in `~/.ssh/config` names.** Network calls to an ssh remote run
  ssh as `ssh -o BatchMode=yes -o ForwardAgent=no`, which reads the person's
  ssh configuration and known hosts. That configuration decides which host
  answers and can name programs ssh runs.
- **Any script or library those configuration files point at that the agent
  could write.** A credential helper's script, a `ProxyCommand` or
  `Match exec` target, a `PKCS11Provider` library: each runs as the person
  during a reap.
- **Every directory niwa's filtered `PATH` searches, the programs found
  there and the files they link to, and git's libexec directory.** niwa
  starts `git`, `claude` and, on macOS, `lsof` by the first match on that
  `PATH`, and git in turn finds `ssh` and any `git-credential-*` helper
  through it. git also runs programs from its libexec directory, the exec
  path compiled into it, which usually isn't on `PATH`: `git-remote-https`
  and the other transport helpers the proof-remote calls use, and the
  credential helpers git ships. niwa starts a program by its path in the
  `PATH` directory, so when that path is a link, the file it resolves to is
  what runs, such as a `claude` link into `~/.local/share/claude/versions/`
  or a `git` link into a Homebrew Cellar. Deny writes to those targets too.
  The filter drops only empty and relative entries, entries that don't
  resolve, and entries that resolve inside the workspace root. A directory
  outside the root that the agent can write, such as `~/.local/bin`, a
  native `claude` install or a user-owned Homebrew prefix, lets it replace
  the program that decides what is pushed and which sessions are live.
- **Every directory on niwa's whole `PATH`, when the workspace sets an
  on-reap or pre-removal command.** The `[session.on_reap]` and
  `[session.before_remove]` commands run under `/bin/sh -c` from the
  workspace root with niwa's own `PATH`, not the filtered one. A relative
  entry resolves against the workspace root, and an entry inside the root is
  searched like any other, so either can supply a program a command names.
  Deny writes to every directory on that `PATH`, or have the commands name
  each program by absolute path and deny writes to those programs. A
  replaced pre-removal program can approve a removal the workspace's own
  check would refuse.
- **Claude Code's jobs root**, `jobs/` in Claude Code's config directory.
  A Claude Code session is live exactly while its job entry there is present
  and records that session, so an agent that deletes another session's job
  entry, or rewrites it so it no longer decodes or names a different
  `sessionId`, makes that session read as gone, and its instance becomes
  reapable while the session is still resumable. A reap of that instance
  then also removes, with `claude rm`, every other stopped session Claude
  Code still lists that a mapping names for the instance, so a write here
  can get another session removed, not only an instance. The entries' `cwd`
  fields are also the rooted-worker guard: a reap spares any instance a present
  job's `cwd` is at or under. For an unmapped instance the backstop could
  take, that guard and the Codex records below are the only liveness it
  has, so rewriting another job's `cwd` removes that instance's spare.
- **The Codex session store**, in `$CODEX_HOME` when it's set and in
  `~/.codex/` otherwise: its `sessions/` rollouts and its
  `thread-writer-locks/` directory. A mapping whose agent is Codex reads
  liveness from these: the session is live while its rollout was written to
  within the idle grace, or while a writer holds its lock. The rooted-worker
  guard reads the rollouts' recorded `cwd` for every instance, mapped or
  not, whatever agent the workspace dispatches. Backdating a rollout while no
  writer holds its lock, removing a held lock file or rewriting a rollout's
  `cwd` makes a live Codex session read as gone or drops an instance's spare.
- **Claude Code's transcripts**, `projects/` in the resolved config
  directory. The reap's worktree sweep reads the session behind a worktree
  record made without a job entry as ended once the transcript the record
  names is gone or hasn't changed for the idle grace, and can then remove
  the worktree if it's unchanged.
- **Claude Code's user settings and user-scope plugins.** Every `claude` call
  on the removal path (`claude agents`, `claude rm`) runs with the person's
  environment, so it reads the user settings and plugins, and both can
  define hooks or programs `claude` runs. They live in the same config
  directory as the jobs root; "Where the Claude Code files are" says where
  that is.
- **Claude Code's user-scope MCP file**, which can name programs `claude`
  runs, and the legacy `.config.json` in the resolved config directory,
  which Claude Code reads in place of `.claude.json` when it exists.
- **What the reap's worktree sweep and `niwa destroy`'s scan read today.**
  These calls don't yet use the removal pass's git and its program lookup;
  that hardening is tracked in
  [niwa#577](https://github.com/tsukumogami/niwa/issues/577), which moves
  them onto the removal pass's git. Until it lands, they run bare `git`, and
  destroy's pull-request lookups bare `gh` when neither `GITHUB_TOKEN` nor
  `GH_TOKEN` is set, by the first match on niwa's
  unfiltered `PATH`, with niwa's whole environment and git's full global and
  system configuration: `~/.gitconfig` and every file its includes name, all
  of `~/.config/git/` (or `$XDG_CONFIG_HOME/git/` when niwa's environment
  sets it), and the system gitconfig. Those files can set keys, such as
  `core.fsmonitor`, that make git run a program. Deny writes to every
  directory on that `PATH`, entries inside the workspace root included, to
  those configuration files and every file they include, and to `gh`'s own
  configuration (`~/.config/gh/`, or `$GH_CONFIG_DIR` when set). The sweep
  also reads job entries from `~/.claude/jobs/` whatever `CLAUDE_CONFIG_DIR`
  says, so deny `~/.claude/jobs/` even when that variable is set, creating
  it included: a listing there with no entry for a worktree's session reads
  that session as ended.
- **The shell startup files, and niwa itself.** A reap the person runs takes
  `PATH`, `HOME`, `CLAUDE_CONFIG_DIR`, `CODEX_HOME` and
  `NIWA_REAP_IDLE_GRACE` from the shell that starts niwa, and that shell
  builds them from its startup files. Deny writes to the login and
  interactive startup files of every shell the person uses, and to any file
  they source: for example `~/.zshenv`, `~/.zprofile`, `~/.zshrc` and
  `~/.zlogin` for zsh (in `$ZDOTDIR` instead of `~` when that's set),
  `~/.bash_profile`, `~/.bash_login` and `~/.bashrc` for bash, and
  `~/.profile`, which a login `sh` reads and a login bash reads when neither
  of the first two exists. The on-reap and pre-removal commands' `/bin/sh -c`
  is neither a login nor an interactive shell, so a POSIX `sh` reads no
  startup file for it: it reads the file `ENV` names only when interactive,
  and those commands' environment carries `ENV` only if the workspace lists
  it. `/bin/sh` is a different shell on different systems, so check what
  yours reads. Deny writes to niwa's own binary as well, the directory it's
  installed in, and the file it resolves to when it's a link, along with
  the absolute path the root's SessionStart hook runs. An agent that
  replaces niwa, or edits a startup file, controls those variables, or the
  whole program, for every reap the person runs later.

**Where the Claude Code files are.** niwa resolves the config directory once
per pass, as Claude Code does:

- With `CLAUDE_CONFIG_DIR` unset, the jobs root is `~/.claude/jobs/`, the
  settings and plugins are in `~/.claude/`, and the MCP file is
  `~/.claude.json` in the home directory. It sits beside the default config
  directory, not inside it. The legacy file is `~/.claude/.config.json`.
- With `CLAUDE_CONFIG_DIR` set to an absolute, clean directory, deny that
  directory's `jobs/`, settings and plugins, and `.claude.json` inside that
  directory rather than `~/.claude.json`. The legacy file is `.config.json`
  in that directory. Deny `~/.claude/jobs/` as well: the reap's worktree
  sweep reads it whatever `CLAUDE_CONFIG_DIR` says.
- A relative value, one starting with `~`, or one that isn't already in its
  clean form (a `..`, a `.`, or a repeated or trailing separator) makes niwa
  keep every unit, since it can't tell which sessions are live.

**What isn't read.** ssh follows `Include`, so an included file is as much a
part of the configuration as `~/.ssh/config` itself. niwa's credential-helper
read doesn't follow git's includes, and reads only the plain
`credential.helper` key. A helper set only in an included file, or under a
`credential.<url>.helper` key, isn't read: the remote it was needed for reads
as unreachable, and the unit is kept. A helper set in a repository's own
configuration keeps the unit too, since `credential.helper` there is a key
that names a program.

**Without a sandbox there's no boundary.** These places are all outside the
agent's working directory, but that's only where they are; it doesn't stop a
write. A worker that runs without sandbox write restrictions like the ones
above can write every one of them, and nothing in this list protects it. Keep
any niwa the agent starts inside the agent's sandbox, never as a command the
sandbox lets out: niwa takes the home directory from `HOME`, so a process that
starts niwa outside the sandbox with a `HOME` of its choosing points the
scratch directory and the credential-helper read at files it wrote.

**A niwa the agent starts isn't bound by this list.** `niwa dispatch`,
`niwa create` and `niwa watch` each reap as they run, and an agent can run
`niwa reap` itself. That niwa takes `PATH`, `HOME`, `CLAUDE_CONFIG_DIR`,
`CODEX_HOME` and `NIWA_REAP_IDLE_GRACE` from the agent, so the agent picks
where its reap finds the jobs root, `~/.niwa/`, the credential-helper read,
the Codex store and the programs it runs, and none of the denies above bind
that reap. `NIWA_REAP_IDLE_GRACE` sets the idle grace the Codex rule and the
worktree sweep use, 24 hours by default; niwa accepts any positive duration
there, so a smaller value shortens the window before a session reads as gone.
The reap this list protects is one niwa runs with the person's environment,
and that environment is outside the agent's reach only while the shell
startup files and niwa's binary are denied too.

## The reap key and the on-reap command

A workspace can ask niwa to run a command each time it removes a session, so
that state some other tool keeps per session can be closed when the session
goes. Two pieces make that work: a key every session niwa starts carries, and
the command itself.

### `NIWA_REAP_KEY`

Every session niwa starts gets `NIWA_REAP_KEY` in its environment:

- A session `niwa dispatch` launches gets the dispatch id, in both its
  `--settings` document's `env` object and its launch environment, before its
  first command. It's set whether or not the dispatch composed any lineage
  attributes. A `NIWA_REAP_KEY` inherited from the shell that ran the dispatch
  is replaced, and removed when niwa has no valid key to set.
- A session the SessionStart hook provisions an instance for gets its session
  id. `niwa instance from-hook` appends one plain, single-quoted line to the
  hook's `CLAUDE_ENV_FILE`:

  ```
  export NIWA_REAP_KEY='<session-id>'
  ```

- A review session `niwa watch` stages gets a key niwa mints for it, kept in the
  staged review's own record (a review session has no session mapping), and its
  continuation reuses it.

niwa sets or writes the key only when it matches `^[A-Za-z0-9_-]{1,128}$`; a
value that doesn't is left unset and the step still succeeds. The key is an
identifier, not a secret. When niwa removes the session it recomputes the key
from the mapping (the dispatch id when present, the session id otherwise) and
checks the pattern again, so a hand-edited mapping can't hand the command
anything else.

### `[session.on_reap]`

```toml
[session.on_reap]
command = "my-close-tool --key \"$NIWA_REAP_KEY\" --session \"$NIWA_SESSION_ID\""
timeout = "10s"          # per run; the default
env = ["MY_TOOL_HOME"]   # names copied from niwa's environment, when set
```

niwa runs `command` under `sh -c` from the workspace root (the removed
instance may already be gone), in its own process group, with stdin at
`/dev/null` and this environment and nothing else:

- `PATH` and `HOME` from niwa's environment;
- `NIWA_REAP_KEY`, the recomputed key;
- `NIWA_SESSION_ID`, the removed session's id;
- `NIWA_INSTANCE_PATH`, the instance it ran in;
- each name in `env` that is set in niwa's environment.

The `env` list reaches only the command, never a command line, so a name may
also appear in [`[session.dispatch_env]`](session-env-passthrough.md). It gets
the same load-time refusals as those lists. A `timeout` that doesn't parse or
isn't positive is refused at load.

The command's standard output, and the warning lines below, go where the
step's own diagnostics go: niwa's standard error for `niwa reap`, the reaps
other commands run first, and `niwa destroy`; standard output for `niwa
watch`'s discard, beside its "discarding staged review" lines. It has the trust
level of a hook the workspace configuration installs.

**Bounds.** Each run gets its timeout, and on timeout the whole process group is
killed; a run never waits on a pipe a leftover grandchild still holds, so it ends
within its timeout plus a second. All the runs of one niwa step share a
30-second budget, and a run the budget can't start is skipped.

**Failure is never fatal.** A run that fails, times out or is skipped prints one
line, with the reason truncated and stripped of control characters, and the
step's own outcome doesn't change:

```
niwa: warning: on-reap for session <id>: <reason>
```

**Repeat.** niwa keeps a session's mapping until its on-reap run succeeds. The
next reap that finds the session gone runs the command again: `niwa reap`, and
the reaps `niwa dispatch`, `niwa create` and `niwa watch` run first. `niwa
destroy` itself never retries one, and `niwa destroy --workspace` removes the
mapping store with the workspace, so a run that fails there isn't repeated.
After ten failed runs niwa drops the mapping
with a final warning line. A mapping whose stored key fails the pattern gets no
command run and one warning line, and counts as a failed run, as does a run
skipped for lack of budget. A `niwa watch` review session normally has no
mapping, so when its discard's run fails there is nothing to keep: the warning
line is printed and the command isn't run again for that review.

**Kept mappings.** `niwa reap` walks the stored mappings as well as the
instances on disk. A mapping kept because its session was still running when
destroy ran, one left by `niwa destroy --keep-session`, and one whose instance
was deleted by hand are each reaped the same way once the session is gone: the
command runs for it once, and the mapping goes when it succeeds. A mapping
whose session reap can't judge is left alone, as an instance would be: that
includes an agent whose liveness niwa reads from session records it can no
longer match to the directory once the instance is gone.

**Where it runs.** The mapped reap (`niwa reap`, the idle-grace reclaim, and the
reaps `dispatch`, `create` and `watch` run first); `niwa destroy` when it removes
a session or finds it already gone; and `niwa watch`'s discard of a staged
review whose session is gone. **Where it doesn't:** `niwa reset`, `niwa destroy
--keep-session`, the backstop's reap of an instance with no session mapping, and
a failed dispatch's rollback. None of those removes a session niwa knows by id.

**Configuration.** niwa reads `[session.on_reap]` from
`.niwa/workspace.toml` when it removes a session. A workspace with no
configuration file at all declares no command: nothing runs and the mapping is
dropped as before. A file that exists but can't be read or parsed counts as a
failed run for every session that step removes, with the warning line, so the
mappings are kept until the file is fixed (or ten reaps go by).

## The pre-removal command

`[session.on_reap]` runs after a session is removed. `[session.before_remove]`
runs before, and decides whether the removal happens. It is how something
outside niwa that knows a path is still needed, a workflow engine with a run
bound under the instance, keeps that instance on disk, without niwa knowing
anything about the engine.

```toml
[session.before_remove]
command = "my-check-tool needed \"$NIWA_REMOVE_PATH\""
timeout = "10s"          # per run; the default
env = ["MY_TOOL_HOME"]   # names copied from niwa's environment, when set
```

niwa runs `command` under `sh -c` from the workspace root, in its own process
group, with stdin at `/dev/null` and this environment and nothing else:

- `PATH` and `HOME` from niwa's environment;
- `NIWA_REMOVE_PATH`, the absolute path about to be removed;
- `NIWA_REMOVE_STEP`, the step asking: `reap` for the mapped reap,
  `reap-backstop` for the reap's backstop removing an unmapped dispatch
  instance past its TTL (with `NIWA_SESSION_IDS` empty, since there is no
  session to name), `destroy` for `niwa destroy`, `destroy-workspace` for
  `niwa destroy --workspace`, `watch-discard` for `niwa watch` discarding a
  staged review (with the review's session id in `NIWA_SESSION_IDS` when it has
  a valid one) and `worktree-sweep` for the reap removing a worktree niwa's
  WorktreeCreate hook made (with the session id the hook recorded for it, when
  it has one; the line then names the worktree directory);
- `NIWA_SESSION_IDS`, the session ids niwa maps to that instance,
  space-separated, empty when there are none;
- each name in `env` that is set in niwa's environment.

`env` gets the same load-time refusals as the other session lists, and a
`timeout` that doesn't parse or isn't positive is refused at load.

**Exit 0 removes; anything else keeps.** Any other exit, a run past its
timeout, a command that can't start, or a configuration file that can't be read
keeps the path for this removal. The instance and its mapping are left exactly
as they were, including the on-reap failure count, so the next reap asks again.
niwa prints one line on the step's standard error:

```
niwa: kept <instance>: reason from before_remove: "<last line the command printed>"
niwa: kept <instance>: before_remove timed out after 10s
```

The command's own output is never printed. niwa keeps the end of both its
streams and quotes only the last non-empty line, cut to 200 bytes, with control
characters and bidirectional overrides replaced. A command that exits non-zero
without printing anything gets `before_remove exited with status <n>`.

**Every failure keeps.** A broken or slow command keeps instances until it is
fixed; it can never make niwa remove one it would have kept without it. Every
reap says so with the lines above, so a broken command shows up the first time it
matters.

**Bounds.** Each run gets its timeout, and on timeout the whole process group is
killed. One step's runs are sequential and share a 30-second budget: each run's
timeout is cut to what is left of it, and a path whose run would start after it
is spent is kept with a line saying the time ran out.

**With nothing declared,** every removal behaves as it did before and niwa
starts no process for it.

**Where it runs.** Every reap step: the mapped reap (`niwa reap`, the
idle-grace reclaim, and the reaps `niwa dispatch`, `niwa create` and
`niwa watch` run), the backstop (`reap-backstop`) and the worktree sweep
(`worktree-sweep`). In a reap pass it runs after niwa's own checks (the
ephemeral marker, the liveness rule and the removal rule) and before the
re-read, the `claude rm` step, the on-reap command and the tombstone check. In
the worktree sweep it runs after the unchanged check, right before the
worktree is removed. `niwa destroy` asks it
once per instance before any prompt, and lists its reason with every other one
it found instead of printing the `niwa: kept` line; `--force` overrides it.

## The launch store

`niwa dispatch` records what it launched each worker with in
`<workspace root>/.niwa-launch/<dispatch id>.json`: the session id, the agent,
the instance name, the permission mode, the session name, the model, the
settings document it passed, and the environment names and values it set for
the worker. The directory is 0700 and each record 0600. A value whose name the
session lists would refuse as a credential is never written. The record is
written once the session mapping is, atomically; a write that fails prints one
warning and the dispatch goes on.

The store sits outside every instance and outside `.niwa/`, so `niwa destroy`
and the on-reap command leave it alone. It's there so a worker can be brought
back in the posture it was dispatched with after its instance is gone.

A reap removes a Claude Code worker's record once its transcript,
`~/.claude/projects/<encoded instance path>/<session id>.jsonl`, no longer
exists. It keeps the record when the transcript exists, when the transcript's
directory can't be read, when `~/.claude/projects` itself is missing, when the
record is under an hour old, and for an agent whose transcripts niwa doesn't
know how to find. A file it can't read, or whose name isn't a dispatch id, is
left in place with one warning line naming it.

Keep `.niwa-launch/` out of anything published from the workspace root, next
to `.niwa-lineage/`: the records hold the values of the names your
`[session.dispatch_env]` passes through.

### `niwa resume`

`niwa resume <target> [prompt]` brings a dispatched Claude Code worker back
from its launch record. Run it anywhere in the workspace. The target is
matched, exactly and never by prefix, against six identifiers in this order,
and the first that matches anything decides: the instance name, the full
instance path (an absolute path whose parent is the workspace root), the
session id, the session name, the short id (the handle `niwa dispatch`
printed and recorded in the session mapping) and the dispatch id.

Resume owns a worker whose job entry Claude Code no longer lists, or whose
instance is gone. A worker whose instance is there and whose job entry is
still listed belongs to [`niwa retask`](#niwa-retask): Claude Code keeps a
stopped background session's launch options with its job and starts a copy
when a resume passes flags of its own, so resume refuses such a worker
instead of starting a copy, and names `niwa retask`. A worker that is still
running is refused too, with the `claude attach <short id>` that reaches it
and a pointer to `niwa retask` once its turn ends.

When the instance was destroyed, resume restores it under its original name
and path, through the same provisioning `niwa dispatch` uses, so a workspace
with `[claude] trust_instances` on gets the trust entry back. Each repository
is cloned at its current remote state: resume prints the commit each one came
back at, and work that was never pushed from the destroyed instance isn't
restored. An instance that is still there is used as it is.

It writes the session mapping, with the original dispatch id, before it
launches anything, then runs `claude --bg --resume <session id>` from the
instance with the recorded `--permission-mode`, `--name=`, `--model=` and
`--settings`, the recorded environment, and `--` before the prompt when one is
given. It prints the session id and the permission mode it launched with. If
Claude Code reports the conversation under a new id, the mapping and the
launch record follow it.

The permission mode is never more permissive than current policy. Resume
works out the mode a new dispatch into the instance would get with no flag
(the `[global] dispatch_permission_mode`, else the workspace's permissions
posture) and launches with the stricter of that and the recorded one, in
Claude Code's order `plan`, `default`, `acceptEdits`, `bypassPermissions`.
When the current mode wins it prints a notice naming both. When current
policy gives no mode at all, a more permissive recorded mode is replaced by
`default`, passed explicitly, so Claude Code's own settings can't put the
permissive mode back.

A record is read as input, not trusted because niwa wrote it. Resume refuses,
naming the argument and before it restores, maps or launches anything, when:

- no record matches, or more than one does (it lists them);
- the session's transcript is gone, or Claude Code lists the session as
  running (or can't list its sessions);
- the instance is there and Claude Code still lists the session's job entry,
  stopped (that worker is `niwa retask`'s);
- something other than the instance occupies its path: a file, a symlink, or
  a directory without the instance's state;
- the store isn't a real directory owned by you with mode 0700, or the record
  is a symlink, isn't owned by you, or isn't mode 0600;
- the record names an environment variable that is neither one niwa sets nor
  one your current `[session.dispatch_env]` passes, or a value that is too
  long or holds a control character;
- its settings document holds a key `niwa dispatch` never emits (anything but
  the remote-control, cross-session and MCP approval keys and `env`);
- its model or session name doesn't start with a letter or digit, or holds
  anything but letters, digits, `.`, `_` and `-` (a model may also hold `:`,
  `/`, `@`, `+`, `[` and `]`, since dispatch passes a full model id through);
- its permission mode, or the machine's `dispatch_permission_mode`, is outside
  the order above;
- the machine's authority settings fail the checks `niwa dispatch` makes.

The instance path is always the workspace root joined with the recorded
instance name, which must be a dispatch instance's name, and the transcript
path is derived from it, so a record can't point either anywhere else.

### `niwa retask`

`niwa retask <target> <prompt>` hands a dispatched Claude Code worker its next
task in the same session: the same session id, the same job entry, the same
short id, and the context of its earlier work. Run it anywhere in the
workspace. The target is matched exactly as `niwa resume` matches it, against
the instance name, the full instance path, the session id, the session name,
the short id and the dispatch id, never by prefix; a target that matches more
than one record refuses and lists them. A prompt that begins with a dash goes
after `--`: `niwa retask <target> -- "--a prompt"`.

Retask owns a worker whose instance is there and whose job entry Claude Code
still lists, whether its process is live and idle or stopped. Under the
instance's lock, it:

1. checks the Claude Code build with `claude --version` (2.1.314 or later);
2. reads the worker's row in `claude agents --json --all` and its job state,
   and checks that the options Claude Code saved with the job are the ones
   the launch record holds;
3. stops a live worker with `claude stop <short id>`, after matching the row's
   full session id to the mapping, and waits until the listing shows the row
   with no pid and no status (every 250 ms, for up to 15 seconds);
4. runs exactly `claude --bg --resume <session id> -- <prompt>` from the
   instance, with no other argument, so Claude Code re-applies the saved
   options and continues the session under the same id;
5. checks the listing (every 500 ms, for up to 20 seconds) until the same
   session is running, and removes a copy the wake started instead;
6. reads the options Claude Code saved for the woken session and checks them
   against the launch record again, as in step 2.

The last check exists because Claude Code writes the model a session resolved
at startup into that session's own saved options. A wake that re-applies the
saved `--model` keeps the dispatched model, but a session that comes back
without it runs on the machine's default model, which may not be the one it
was dispatched with. When the saved options differ after the wake, retask
stops the woken session again (it keeps it; it never removes the worker's own
session) and refuses with `woke-with-drift`, naming the flag but never its
value.

It writes nothing: the session mapping, the launch record, the short id, the
keep-alive self-wake and the remote-control setting stay attached to the
session they already name. On success it prints the session id, the instance
and the `claude attach`, `claude logs` and `claude stop` lines for the short
id.

A busy worker is refused, not queued: send the follow-up again once its turn
ends. Retask can't see a `claude attach` viewer that is attached but silent,
and stops the worker under it like any other live idle worker.

#### Restarting a worker stuck mid-turn

A worker whose turn never ends, for example one blocked inside a shell
command that doesn't return, is always busy, so plain retask can't restart
it. `niwa retask --interrupt <target> <prompt>` takes it anyway: after the
same build, saved-options and policy checks, it stops the running turn with
`claude stop <short id>`, then settles, wakes, verifies and checks the woken
session's options exactly as it does for an idle worker. The worker comes
back under the same session id, so its session mapping, authority binding
and short id still reach it.

Whatever the stopped turn was doing is cut off, so the prompt should tell the
worker what was interrupted. `--interrupt` changes nothing else: a worker
waiting for an answer is still refused with `awaiting-input`, and a worker
that reads as busy but has no live process refuses with `state-unknown`.

Prefer `--interrupt` to stopping and resuming the worker by hand. A
`claude --bg --resume` issued before the stop has settled, or with flags of
its own, starts a copy under a new session id. That copy keeps only the flags
on its resume command line, so the dispatched name, settings and permission
mode are gone, and with no `--model` it runs on the machine's default model.
Retask waits for the stop to settle, wakes with no flags, removes a copy if
one starts anyway, and checks the model afterwards.

Every refusal names one state word, right after the target:
`niwa: error: cannot retask "<target>": <state>: ...`. The words are stable,
so a script can act on them.

| State | Meaning | Next step |
|-------|---------|-----------|
| `unknown-target` | No launch record matches the target exactly. | Check the target against `niwa list`. |
| `ambiguous-target` | More than one record matches; the error lists their dispatch ids. | Use the dispatch id or the instance name. |
| `invalid-record` | The launch record or the session mapping fails a check, or holds an id niwa won't pass to a command. | Inspect the record under `.niwa-launch/`. |
| `instance-gone` | The instance directory is gone. Retask never restores one. | `niwa resume <target> <prompt>`. |
| `instance-occupied` | Something other than the instance occupies its path. | Move it away. |
| `transcript-gone` | The session's transcript is gone. | Nothing to continue; dispatch again. |
| `job-entry-gone` | Claude Code no longer lists the session. | `niwa resume <target> <prompt>`. |
| `state-unknown` | The listing and the job state don't agree, or don't read as a state retask acts on. | Check `claude agents`. |
| `awaiting-input` | The worker is waiting for an answer. | `claude attach <short id>`. |
| `busy` | The worker is in the middle of a turn. | Retry once it's idle, or, when the turn is stuck, `niwa retask --interrupt <target> <prompt>`. |
| `policy-tightened` | The recorded permission mode is looser than the one current policy gives a new dispatch. | Change the policy back, or dispatch again. |
| `options-drift` | The options Claude Code saved for the job differ from the launch record; the error names the flag, never its value. | Dispatch again. |
| `stray-session` | Another session is rooted in the instance; retask leaves it alone. | Stop or remove that session yourself. |
| `concurrency` | Another niwa command holds the instance's lock (it is named), or the platform has no file locking. | Retry once it finishes. |
| `agent-unsupported` | The worker's agent declares no way to hand it a task in place. | `niwa resume` once its job is gone. |
| `build-forks` | The Claude Code build is older than 2.1.314, or can't be read. | Upgrade, or use the escape below. |
| `stop-failed` | `claude stop` failed. | Retry `niwa retask`. |
| `settle-timeout` | The stop didn't settle in time, so retask didn't wake the worker. | Retry `niwa retask`. |
| `copy-started` | Claude Code started a copy instead of continuing the session; retask removed the copy. | Retry, or use the escape below. |
| `copy-left` | A copy started and retask couldn't remove it; the error prints the `claude stop` and `claude rm` lines that do. | Run them, then retry. |
| `wake-unconfirmed` | The wake ran but the listing never showed the same session running. | Check `niwa list` and `claude logs <short id>`. |
| `woke-with-drift` | The wake continued the same session, but the options Claude Code saved for it afterwards differ from the launch record (the model, most often); retask stopped it again. | Dispatch again. |

After `stop-failed`, `settle-timeout`, `copy-started`, `copy-left` and
`woke-with-drift` the worker may have been left stopped; it stays resumable
with another `niwa retask` (after `woke-with-drift` the saved options no
longer match the record, so retask refuses it with `options-drift`), and a
keep-alive worker's self-wake is silent until the next wake. After
`copy-started`, `copy-left`, `wake-unconfirmed` and `woke-with-drift` the
prompt may already have been acted on: check `niwa list` and
`claude logs <short id>` before sending it again.

Retask removes a copy only when its ids pass niwa's checks, a fresh listing
taken just before the removal still lists it, its working directory is inside
the instance, it isn't the worker's own session, and it wasn't rooted at the
instance before the wake. It stops the copy, waits for the stop to settle,
then runs `claude rm`.

If a Claude Code build always starts a copy, take the worker out of retask's
hands: `claude rm <short id>`, then `niwa resume <instance> "<prompt>"`, which
starts it again on the same conversation from its launch record.

Prompts appear in the process table and in Claude Code's transcripts, a
removed copy's included, so they must not carry secrets.

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

The root `.claude/settings.json` doesn't carry a bypass **permission posture**.
Claude Code 2.1.257 stopped honoring a bypass mode set in a project's settings
file, so a workspace's `permissions = "bypass"` declaration can't reach a
session that way. Sessions a developer starts at the root, including ephemeral
workers, get the developer's own Claude Code settings. The route to bypass for
those sessions is `--permission-mode` on the developer's own launch, for
example `claude --permission-mode bypassPermissions`. Workers launched by
`niwa dispatch` are covered separately: niwa records the declared posture in
each instance's state, and dispatch passes `--permission-mode bypassPermissions`
to the worker when the workspace declared `bypass`.

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
marketplace resolves against a repo that only an instance apply fetches (into
the managed copy described in `docs/guides/repo-marketplaces.md`, or an
instance's own clone), so the root settings can't count on it. Such a
marketplace — and any plugin bound to it — is excluded
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
  is an undocumented Claude Code file, so absent fields decode to zero. Not
  every reader fails toward keeping on a miss. `sessionLive` reads a missing
  entry, one that won't decode and one that names another session alike as
  gone, and `instanceHasLiveJob` skips an entry that won't decode, so it
  spares nothing on that entry's account; the removal rule's own checks still
  run after both. `jobEntryGone`, the worktree sweep's read, does fail toward
  keeping: an unset or unreadable jobs directory, or an entry that won't
  decode, is never gone.
- `niwa reap` doesn't remove through `niwa destroy --force`: each unit goes
  through the removal rule (`removal.Evaluate`), the pre-removal command when
  one is declared, a re-read and a tombstone before it is deleted. The
  mapped sweep takes only instances a mapping marks `ephemeral: true` whose
  session is gone, and the backstop only unmapped instances with a dispatch
  name past the TTL. A developer's normal instance has no such mapping and
  no such name, so it is a target only if a mapping is written for it or it
  is renamed into the dispatch form, which is why the mapping store and the
  workspace root are on the deny list. The `SessionEnd` hook branch destroys
  nothing (it is a no-op); the reaper is the single teardown path.
- The root materializer (`internal/workspace/root_materializer.go`) reuses the
  shared `buildSettingsDoc`, so the root settings ride the same path the
  instance settings do for permissions, hooks, env, plugins, and marketplaces.
  One field is deliberately filtered, not forwarded verbatim: the root receives
  only the **root-hoistable** subset of marketplaces (see `rootHoistableConfig`).
  github-sourced marketplaces hoist as-is; `repo:`-sourced ones resolve against
  a repo only an instance apply fetches, so they and the plugins
  bound to them are excluded and reported. Do not assume the root settings are a
  byte-for-byte copy of an instance's — they match for everything except those
  instance-local marketplaces.
