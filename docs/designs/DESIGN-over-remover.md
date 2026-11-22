---
schema: design/v1
status: Planned
problem: |
  niwa removes a stopped instance only when its session has been deleted or a
  person names it to `niwa destroy`, and nothing in niwa can be told when a
  unit's work is finished. `niwa reap`, which also runs before every dispatch,
  create and watch provisioning, checks liveness and nothing else: it never
  calls the loss scan (`workspace.ScanInstance`), so deleting a worker's
  session by hand turns the next dispatch into an unchecked delete. Destroy
  does scan, but it removes the session before the directory, so a session
  whose worktree sits inside the instance survives it. There is no stopped
  state, no size, no age and no over answer anywhere in niwa's records, so
  `niwa list` can't say which units are done or what they cost, and finished
  units pile up until a person clears each one.
decision: |
  niwa gains one removal rule, coded once in a new `internal/removal` package,
  and one bounded pass that every removal niwa makes on its own goes through:
  the opportunistic reap before dispatch, create and watch, `niwa reap`, the
  backstop, failed-dispatch cleanup, the trim and the new `niwa retire`. A
  workspace names an over command in `[session.over]`, which niwa runs with
  the `[session.before_remove]` calling shape and reads only by exit status; on
  exit 0 niwa writes the retire mark at `<instance>/.niwa/retired.json` in the
  earlier workspace-level remover's format. A marked unit is reaped once the
  rule passes, worktrees before the session and never forced. Per-unit records
  (`keep.json`, `discard.json`, `pids/`, `unit.json`) sit in the instance's
  `.niwa/`; host state (the kept set) sits at `~/.niwa/w-<key>/kept-files/`.
  `niwa list` and a fixed dispatch line show every stopped unit nobody retired
  with its answer, age and size. Delivery comes in three parts: the rule and
  worktrees; the over question, mark and listing; the keep record, kept set
  and launch record.
rationale: |
  Putting the rule in one package that every removal calls is the only way
  the verdict-only mode, the retire, `niwa reap` and the pass before dispatch
  can give the same answer, which the PRD requires and a plugin depends on.
  Copying the existing removal-gate pattern for the over seam keeps niwa free
  of koto and forge calls and gives workspaces a contract they already know.
  Following the earlier remover's mark format, rather than inventing niwa's
  own, lets marks written there count here and leaves that work free to settle
  the points it still holds open. Keeping per-unit records inside the instance
  makes them go with the instance and survive `niwa apply`, while the kept set
  follows niwa's host-state location rule in a directory of its own, so it
  never collides with the whole-instance `kept/` directory the plugin release
  store already scans. A single 30-second budget per pass, with fair ordering,
  caps what a dispatch waits for without ever changing what the rule decides.
upstream: docs/prds/PRD-over-remover.md
user_visible_surface: true
decision_provenance: inline-resolved
---

# DESIGN: niwa removes a finished unit that a workspace command says is over

## Status

Planned

## Context and Problem Statement

A unit of work in niwa is a session, the instance it runs in, its Claude Code
job directory (`~/.claude/jobs/<id>/`) and its transcript
(`~/.claude/projects/<encoded cwd>/<session>.jsonl`). niwa removes a stopped
instance on two signals only, and neither one asks whether the work is done.

The first is the reap. `niwa reap` (`internal/cli/reap.go`, a Cobra command
with no flags and no arguments) and its opportunistic form,
`reapOpportunistically` (reap.go:985-995), run `reapWorkspace` and
`sweepHookWorktrees`. `reapOpportunistically` is called before every
`niwa create` (create.go:182-185), every `niwa dispatch` (dispatch.go:613-615)
and every review instance `niwa watch` provisions (watch.go:792), with errors
swallowed. `reapWorkspace` (reap.go:610-717) runs a mapped sweep
(`selectReapTargets`) that joins instance records with session mappings in
`<root>/.niwa/sessions/` and targets an ephemeral instance once its Claude job
entry is gone, a stored-mapping walk, a launch-record sweep, and a backstop
(`reapBackstop`) for unmapped dispatch-named instances older than 30 minutes
that don't carry `.niwa/dispatch-retain`. Each target is taken under the
instance lock (`takeInstanceLock`, instance_lock.go), re-read, offered to the
`[session.before_remove]` gate, and removed by `workspace.DestroyInstance`
(destroy.go:166-186), which prunes plugin records, withdraws trust grants and
runs `os.RemoveAll`. The guards are liveness only. Nothing on the reap path
calls `workspace.ScanInstance`; its only callers are in `niwa destroy`. That is
tsukumogami/niwa#300. So the reap is safe only while a session's job entry
exists, and a person who deletes a worker's session by hand turns the next
dispatch into an unchecked delete of that worker's instance.

The second is `niwa destroy` (`internal/cli/destroy.go`). It takes the
instance lock, refuses a running session or a veto in `destroyPreflight`, runs
`workspace.ScanInstance` unless `--force`, then runs `claude rm` on each
mapped session (`removeOwningSessionsListed`, destroy_session.go:183-231) and
only after that removes the directory. niwa's worktrees live under
`<instance>/.niwa/worktrees/` and go with the directory, so at the moment
`claude rm` runs, a session whose worktree sits inside the instance still has
that worktree, Claude Code refuses to drop it, and the destroy finishes with
the session left behind. Destroy's scan also counts every untracked file as
loss and has no mode that shows what it found without acting.

Neither path has a way to learn that a unit is finished. The only workspace
hook near removal, `[session.before_remove]`, can veto a removal but can't
start one. niwa keeps no stopped state, no size, no age and no answer about a
unit, so `niwa list` (list.go, `--json` only) prints a name, a session name
and a resume command, and nothing that says which of forty instances are done
or how much disk they hold. On a host running parallel agent work, finished
instances, clones, multi-gigabyte build trees, transcripts and dispatch
briefs stay until a person looks at each one, and that doesn't happen.

The accepted PRD (`docs/prds/PRD-over-remover.md`) states what has to change.
In implementation terms: a workspace names one command that answers whether a
stopped unit is over, niwa reads only its exit status and writes a durable
mark on yes, and every removal niwa makes on its own, the reap of a deleted
session's instance included, goes through one rule that never takes a path it
can't regenerate. A workspace that names no command loses nothing it didn't
ask to lose, sees its stopped units with answer, age and size, and has one
command to retire a unit under the same rule. The shape of that rule, and the
formats of the command contract and the retire mark, come from an earlier
workspace-level remover built first in one workspace outside niwa. That work
hasn't landed, so every point taken from it is an open dependency here, listed
in its own section below.

## Decision Drivers

- **D1. One rule, coded once.** The reap of a deleted session's instance, the
  reap of a marked unit, the backstop, failed-dispatch cleanup, the trim and a
  retire must all reach the same verdict for the same unit, and a workflow
  plugin must be able to ask niwa for that verdict instead of carrying a copy
  (R10, R29).
- **D2. Unique work is never taken, and doubt keeps.** A unit goes only when
  every path in it is regenerable, handed forward or declared disposable; a
  git error, an unreachable remote, an unreadable path or a missing record
  keeps (R11, R14, R15). The verdict-only mode must change nothing on disk,
  `.git` included (R29).
- **D3. niwa never decides over on its own.** It reads an exit status and
  nothing else, and the pass makes no koto or forge call to decide over (R3,
  R4).
- **D4. The earlier remover's formats are followed, not settled here.** niwa
  reads and writes the retire mark in that work's format and place, and treats
  every other point taken from it as open until it lands (R6, open
  dependencies).
- **D5. Bounded, and only inside niwa commands.** No daemon, timer or
  background process; each all-unit pass finishes inside a fixed budget, and a
  unit it doesn't reach waits for a later pass, never forever (R21, R34).
- **D6. The listing is local.** `niwa list` reads answer, age and size from
  local state, with no network call and no over run (R36).
- **D7. Host state follows the location rule.** Large host-level state niwa
  owns lives under `~/.niwa/`, in a directory keyed by the workspace root's
  canonical path, never under the root's `.niwa/` or inside an instance (R26).
- **D8. A reap on a mark is undoable for a known window.** niwa holds the
  launch record a restore replays before it reaps on a mark, and refuses a
  restore past the window with the date it ended (R18, R32, R33).
- **D9. No force, worktrees first.** The pass never passes niwa's `--force` or
  Claude Code's flags that discard unpushed work or force-remove a worktree,
  and it removes worktrees before the session so none is left behind (R16,
  R17).
- **D10. A user of niwa alone is no worse off on scratch than with destroy.**
  The project owner chose job scratch disposable by default where no handoff
  brief exists, with a setting to make it unique (R28, R35).
- **D11. Today's paths keep working.** The `[session.before_remove]` veto still
  applies to every removal, deleting a session still leads to its reap, and
  `niwa destroy` keeps its own checks (R20, Out of Scope).

## Considered Options

### Decision 1: Where the rule lives and how every removal reaches it

Resolved inline.

The PRD lists six places niwa removes something without a person naming it,
and wants the same answer from all of them and from the verdict-only mode.
Today each place carries its own guards: the mapped sweep and backstop in
reap.go, the worktree sweep in reap_worktrees.go, destroy's preflight and
scan, the watch's discard. The watch's rollback of a failed stage, the
deferred `destroyInstanceFunc` in `stageReview` (watch.go:809-814), carries
none, though it can run after the review agent has launched
(watch.go:862-864). Adding a loss scan to each would give six copies of
the rule. The question is where the rule's code lives, what drives it, and how
the time budget the PRD asks for is enforced around commands and network
calls that each have their own timeouts.

The existing loss scan, `workspace.ScanInstance` (scan.go), is close but not
the rule. It counts every untracked file as loss, contacts the forge to find
unlanded pull requests, skips nested repositories it isn't told about, and has
no idea of build trees, generated files, job scratch, patterns, discard
records, keep records or handoff briefs. Destroy depends on it as it is, and
the PRD leaves destroy's checks alone.

Key assumptions:
- The rule can run on git plumbing alone (`git status --porcelain=v2`,
  `git ls-files -v`, `git for-each-ref`, `git stash list`, `git ls-remote`,
  `git worktree list`, `git diff-index --cached`, `git merge-tree --write-tree`)
  with no forge call. Git 2.38 or newer is
  needed for `merge-tree --write-tree`; an older git makes the
  merged-by-content check undecidable, which keeps.
- Those plumbing calls give correct answers with system and global git
  configuration ignored and with the command-bearing settings below
  overridden, so the rule never needs to run a program a repository names.
- `claude agents --json --all` and one process listing per pass are cheap
  enough to run before each dispatch (destroy already runs the first).
- A 30-second budget is acceptable latency added to a dispatch in the worst
  case; typical over runs take a few seconds.
- A rename within one directory is atomic, and a process already inside the
  tree keeps its working directory and descriptors across it, so it shows up
  under the new path in the next process listing.
- The workspace configuration gives every repository it clones a clone URL
  niwa can read at pass time, outside the instance.

#### Chosen: a new `internal/removal` package driven by one pass orchestrator

The rule moves into a new package, `internal/removal`. Its entry point,
`removal.Evaluate(ctx, unit, policy) Verdict`, reads and never writes: it
returns exactly one of reap, keep, or keep and trim, every check that keeps in
the fixed order R11 sets (liveness; a keep record's hold; a missing or
unreadable record the rule needs, the launch record for a reap on a mark
included; each repository in path order, with git errors and unreachable
remotes first, then commits and refs no live remote ref holds (every ref
`for-each-ref` lists and each worktree's HEAD from
`git worktree list --porcelain -z`, a detached HEAD included; a branch is
held by its tip commit, a tag only by a proof remote listing the same tag
name at the same object), stashes,
modified tracked files, index entries flagged assume-unchanged or
skip-worktree, untracked files, which git is
made to report in full whatever the repository's configuration says;
paths outside every repository; named
files missing or changed), the evidence for each, and the class of every path
(tracked, pushed, build tree, generated, disposable or unique, with a
git-ignored or job-scratch notice where that applies). Every check runs even
after one keeps, so the verdict-only mode can name them all, and the verdict
names the first.

The package also settles the mechanics the PRD left to the design:

- **Repositories are found by walking.** The walk covers the instance and the
  unit's job directory and finds every `.git` directory or file (clones,
  worktrees, submodules, nested `git init` repositories). It also recognizes
  a git directory by its structure, a `HEAD` file beside `objects/` and
  `refs/` directories, whatever it's named, so a bare repository counts, and
  so does every git directory under a git directory's `modules/` at any
  depth: a submodule that was deinitialized or removed with `git rm` leaves
  its commits there with no working-tree `.git` file pointing at them. The
  walk doesn't descend into a build tree except to sum its size, and that
  descent, which visits every entry anyway, looks for the same `.git`
  entries and git directories; a build tree that holds one is never
  trimmed. Every repository found gets the full checks, the configuration
  check first; one with no work tree (a bare repository, a submodule's
  store with no checkout) gets them with `--git-dir` alone, and the status,
  `ls-files` and index-flag reads, which need a work tree, don't apply to
  it. A nested repository the outer one
  ignores is still a repository with its own checks, so its unpushed commit
  keeps the unit rather than passing as a git-ignored path. A submodule is
  judged the same way, as a repository of its own: its commits, stashes,
  modified and untracked files go through its own checks, and its parent's
  status doesn't look inside it (below), so nothing in it passes on the
  strength of the parent's answer. The workspace configuration names no
  submodule, so a commit in one is held only through a workspace
  repository's clone URL or a `proof_remotes` prefix (below) and otherwise
  keeps with "remote not recognized". The walk uses
  `Lstat` and never follows a symlink: a link is a path of its own, classed by
  its own name, and nothing behind it is read, summed or searched for
  repositories. It stays on the filesystem the instance sits on; a directory
  on another device keeps the unit with "another filesystem at <path>" as
  evidence. Under the job directory the walk compares devices against
  `~/.claude/jobs/`, not the instance. A `.git` file whose `gitdir:` (or the `commondir` it names)
  resolves outside the instance and the job directory, or any git directory
  the walk finds, a `.git` directory or one found by its structure, whose
  `commondir` file does, keeps the unit with "external git directory" as
  evidence and is not followed. niwa reads a `.git` file and a `commondir`
  file through the same `O_NOFOLLOW` open, size-capped, as one line
  (`gitdir: <path>` for the first, a bare path for the second) with only a
  trailing newline allowed; a relative path resolves against the file's own
  directory and is cleaned without following links; anything else keeps
  with "unreadable git file: <path>". The reverse
  case is checked too. A linked worktree's working tree can sit anywhere
  (`git worktree add /tmp/w`), while its index, its `refs/worktree/*` and
  `refs/bisect/*` and every object it made stay in the instance and are
  deleted with it. So every entry `git worktree list --porcelain -z`
  reports for a repository the walk found counts as reached only when the
  walk found its working tree. The main entry, the first one git reports,
  counts as reached when its path is the work tree the walk found for that
  git directory. Every other entry counts as reached only when the walk
  found, at that entry's path, a `.git` file naming that entry's
  admin directory under the common directory's `worktrees/`. The listed
  path alone isn't enough, since it comes from the admin directory's
  `gitdir` file, which the agent can write. niwa also lists `worktrees/`
  itself, and an admin directory there that git doesn't report counts as
  unreached. An unreached entry keeps the unit with "worktree outside the
  instance: <path>" as evidence. The one exception is an entry git marks
  prunable, whose working tree is gone: niwa runs the configuration check
  on its admin directory's `config.worktree`, then reads its HEAD, its
  per-worktree refs and its index against that HEAD
  (`git diff-index --cached`) with `--git-dir=<admin directory>`, through
  `gitCommand`. That HEAD and those refs count like any worktree's, and a
  read that fails or a staged entry that differs from HEAD keeps. A git
  directory the walk recognized by its structure and judges with no work
  tree has a main entry too. A bare one has no work tree to reach, and a
  submodule's store whose configured work tree sits inside the instance or
  the job directory (a deinitialized submodule) gets the no-work-tree
  checks above. But one whose main work tree sits outside the instance, set
  by `core.worktree` or by a `.git` file elsewhere pointing at it
  (`git init --separate-git-dir`), keeps the unit with "work tree outside
  the instance: <path>" as evidence, the same form as an unreached
  worktree: its index and objects live in the instance and would go with it
  unchecked. niwa tells the second case from the configuration read, since
  a git directory that isn't bare, sits under no `modules/` directory and
  sets no `core.worktree` has its work tree wherever a `.git` file names
  it, and the walk found none; the path named is then the git directory's
  own.
- **Git runs in a fixed, hardened environment.** Every git call niwa's
  removal paths make in a repository an agent could have written is built by
  one function, `gitCommand` in `internal/removal/git.go`. That covers the
  rule's checks, the scratch fetch, the re-read, the tombstone check and
  the status niwa runs on each worktree right before removing it, in the
  pass and in `runDestroyInstance`; the hook worktree sweep's calls through
  `worktree.DestroySession` (`git status --porcelain` and `git branch -d`,
  which today start plain `git` with the inherited environment through
  `StdGitInvoker` in `internal/worktree/worktree.go`, as its
  `git worktree remove --force` does); and every call
  `niwa destroy`'s scan, `workspace.ScanInstance`, makes, through `gitOutput`
  in `internal/workspace/scan.go` and, for its durability check, through
  `gitNetwork` (`ls-remote` and `fetch`) and `gitOutputEnv` in
  `internal/workspace/durable.go`, all three of which today also start plain
  `git` with the inherited environment. The implementation changes those
  files so their calls go through `gitCommand` (Components gives how).
  No other code in the package, the
  pass, the sweep or destroy starts git, with one named exception: the read
  of the person's own credential helper, bounded as Decision 8 describes.
  On these paths niwa runs the program-naming configuration check on every
  repository under a unit before the first other git call in any of them.
  The repositories come from niwa's own walk, never from asking git: each
  clone and worktree, and each submodule (whose configuration is a file of
  its own under the parent's `.git/modules/`, which the parent's check never
  reads), gitlink and nested repository under them. A repository whose own
  configuration, in any scope but the command line, names a filter, diff,
  merge, credential or signing program, an fsmonitor hook or any other
  program is kept with that key as evidence, nothing else runs in it,
  and the unit is kept; a submodule is treated exactly as a top-level
  repository is. That first run isn't the last. Seconds can pass between it
  and a later call in the same repository (the network calls and local pack
  before a repository's status, the gate, the kept-set move), and a process
  still alive in the instance can write configuration in between, so the
  `gitCommand` invoker runs the check again on a repository, its common
  directory's configuration and its `config.worktree` included, right before
  it starts every `git status` there and right before niwa removes a
  worktree of it: the rule's status in each repository, the re-read's, the
  status and removal of each worktree in the pass, `DestroySession`'s status
  and removal in the hook sweep, and destroy's scan status and the status
  and removal of each worktree in an unforced destroy. A key found on a
  re-check keeps the unit (in the sweep, the worktree) with "changed during
  the pass: repository configuration names a program: <key>" as evidence,
  and the call or removal isn't started. Every `git status` niwa runs passes
  `--ignore-submodules=all` and `--untracked-files=all`, the status before
  each worktree removal included. `--ignore-submodules=all` means git never
  starts a status of its own inside
  a submodule or gitlink, where it would read that repository's
  configuration and run the drivers it names. The removal itself starts no
  git process: niwa deletes a worktree's tree and its admin directory with
  its own descriptor-relative walk (Data Flow step 7), so no driver can run
  during it. A worktree that holds a submodule, or any other repository
  below its own top-level `.git` file, isn't removed at all and keeps its
  unit with "worktree holds a repository: <path>" as evidence. A git-ignored
  nested repository inside a linked worktree now keeps where git's own
  remove would have deleted it; that fails toward keeping and is intended,
  not a regression.
  `git worktree list`, `for-each-ref`,
  `stash list` and `branch -d` don't descend into submodules. Decision 8 gives the
  settings and the reasons. In short: no inherited environment, system and
  global configuration ignored, hooks, fsmonitor, pagers, askpass,
  credential helpers and signature checks turned off, replace refs, grafts
  and commit-graph files ignored, only `https` and `ssh` allowed, no proxy
  and certificate checks on,
  `--ignore-submodules=all` and `--untracked-files=all` on every status, and
  `--git-dir` and `--work-tree` passed from the walk. niwa removes only a
  worktree its own walk found, opened from the instance's directory
  descriptor, never one at a path read from `git worktree list` or from the
  admin directory's `gitdir` file. Every git output niwa parses is read in its
  NUL-terminated form: `git worktree list --porcelain -z`,
  `git status --porcelain=v2 -z`, `git ls-files -v -z`,
  `git config --list -z`, and `-z` or a `%00` separator wherever else git
  offers one. Without it, git prints a newline in a path as is, so a
  `gitdir` file naming a path with a newline and a `HEAD <pushed id>` line
  after it could add a record that hides a worktree's unpushed detached
  HEAD. A record that doesn't parse keeps the unit. Network
  calls go only to proof remotes (below), take
  an explicit, validated URL niwa builds itself and run against the scratch
  object store, never inside the unit's repository.
- **Held means a proof remote's live ref holds it.** Only a proof remote can
  show that a commit exists somewhere else, because the remotes in a
  repository's configuration are written by the agent, which could add one
  pointing at a host it controls and advertise its own unpushed commit there.
  A proof remote is a remote whose URL equals the clone URL the workspace
  configuration gives that repository, read by `loadWorkspaceConfig` from the
  root and never from `instance.json` or anything else inside the instance,
  or one whose URL starts with a prefix the workspace lists in
  `[session.removal] proof_remotes` (for people who push to forks). URLs are
  compared after normalizing: scheme and host lowercased, the scp-like form
  rewritten as `ssh://`, a trailing `/` or `.git` dropped. Normalization
  does no path cleaning, so a URL that would need it is refused rather than
  compared (below). A match compares the whole authority, scheme, user, host
  and port, with a default port made explicit (443 for `https`, 22 for
  `ssh`), so a remote that names a port the clone URL or prefix doesn't name
  doesn't match: `ssh://git@host:2222/dana/repo` is another service on the
  same host, not a repository under `ssh://git@host/dana/`. A URL niwa
  rebuilds on a match takes its authority from the clone URL or prefix,
  never from the remote. A `proof_remotes` prefix matches on whole path segments
  after normalizing, never as a string prefix: `https://host/owner/` matches
  `https://host/owner/repo` but not `https://host/owner-evil/repo`. A local
  commit whose id is itself a tip `git ls-remote` returns for a proof remote
  is held without reading any object. Any other local commit is held when
  it is an ancestor of such a tip. The tips are every ref `git ls-remote`
  lists for the proof remote with no pattern, so a forge's other namespaces,
  such as `refs/pull/*`, count as well as branches and tags, and the fetch
  brings the history of the tips a check tests against. A branch, and every
  other ref outside `refs/tags/`, is judged by its tip commit as above. A
  tag is held only when a proof remote lists the same name under
  `refs/tags/` at the same object: the commit id for a lightweight tag, and
  for an annotated tag the tag object's own id, which `git ls-remote` lists
  as the tag ref's value. A local tag no proof remote lists that way is
  local-only and keeps the unit with the tag named, even when the commit it
  points at is held: a lightweight tag made on a pushed HEAD and never
  pushed keeps its unit, as does a tag whose name the remote lists at
  another object. This is how the design reads R11's "local-only refs": a
  tag is a name a person gave, and only the remote holding that name at
  that object shows it exists elsewhere. Merged by content means `git merge-tree --write-tree` of the proof
  remote's default branch's live tip and the local branch exits 0, with no
  conflict, and yields the default branch's own tree, which is what a squash
  merge leaves. A repository the workspace configuration doesn't name (a
  nested `git init`, a repository the agent cloned) gives no clone URL of its
  own, so its proof remotes are only those whose URL equals a clone URL the
  workspace configuration gives some repository, or matches a
  `proof_remotes` prefix, on the full authority as above: a clean, fully
  pushed clone of a workspace repository's remote passes wherever it sits in
  the instance (R11), and its held check rebuilds the URL from that clone
  URL. A nested `git init` with no such remote has no proof remote at all.
  Any other remote is never contacted; a commit only such remotes could hold
  keeps the unit with "remote not recognized: <remote name>" as evidence,
  and the verdict names the setting that would widen it. The ancestry test (`git merge-base --is-ancestor`)
  and `merge-tree` run only in a scratch object store outside the unit (a
  bare repository created 0700 under `~/.niwa/w-<key>/scratch/`, below,
  with no `objects/info/alternates` and no refs), never with the unit's
  repository as `--git-dir`. The tips they test against are the object ids
  `ls-remote` returned, never the unit's remote-tracking refs. The scratch
  store carries none of the unit's refs, replace refs or grafts, and replace
  refs, grafts and commit-graph files are turned off for every call
  (Decision 8), so a `refs/replace/` entry, an `info/grafts` file or a forged
  commit-graph in the unit's `.git` can't make a remote tip look like it
  descends from an unpushed commit. Git doesn't re-hash an object when a
  history walk reads it, so the checks never read the clone's object store
  at all: an object written into the clone under the id of a real remote
  commit, with the unpushed commit as its parent, would otherwise make that
  commit read as an ancestor of the tip, or become the merge base that makes
  `merge-tree` return the default branch's own tree. Instead the check
  fetches the live tip's history from the proof remote into the scratch
  store. Since the store has no refs and no alternates, the fetch offers the
  server nothing it already has, so the server sends the whole history of
  that tip, and git computes every object's id from its content as it
  indexes the pack. The local side `merge-tree` needs comes in the same way:
  niwa builds a pack of the local branch's objects in the unit's repository
  (`git pack-objects --revs --stdout`, through `gitCommand`) and reads it
  into the store with `git index-pack --stdin`, which also recomputes each
  id from content and refuses an object whose id the store already holds
  with different content. A forged object therefore lands under its true id,
  or not at all, and never takes the place of the copy the proof remote
  sent; an object the walk needs and the store lacks is a git error, which
  keeps. `merge-tree --write-tree` writes its tree objects into that store
  too, so nothing under the unit's `.git` changes, which the verdict-only
  mode needs. The fetch and the local pack are killed, and the unit kept,
  when the scratch store grows past 1 GiB; the store is removed on every
  exit path, and a pass removes any store an earlier, interrupted pass left.
  The held and merged answers are only as sound as the store is untouched, so
  it lives in a per-user directory niwa owns, `~/.niwa/w-<key>/scratch/`,
  inside the `~/.niwa/` path the guide tells workspaces to deny the agent
  writes to (Decision 9), and never in a shared temporary directory, which
  sandboxes often leave writable and where mode 0700 doesn't stop a process
  running as the person. The two empty directories niwa runs commands from,
  the one every `claude` call runs from and the one the credential-helper
  read runs from (Decision 8), sit there too. Each pass creates its
  directories fresh, with mode 0700, and a directory already at a path it
  creates fails the creation and keeps. Before each use niwa `Lstat`s the
  directory and its parent and checks that both are real directories owned
  by the person's uid with mode 0700, and that no component from below the
  home directory down to it is a link, each owned by the person's uid, the
  same chain rule the job-directory vet uses. Any mismatch fails toward keeping:
  the unit the use serves keeps, and a mismatch before a pass-wide
  call (`claude agents`, the credential-helper read) keeps every unit that
  call serves. A leftover from an earlier pass is removed only after the
  same check. niwa writes the store's `config` itself, and before each
  network call against the store it reads that file again and compares it
  byte for byte with what it wrote; any difference keeps the unit, so a
  `url.<base>.insteadOf` written there can't point `ls-remote` at another
  host and make an unpushed commit read as a live tip. Before each use niwa
  also checks that the store holds no `commondir`,
  `objects/info/alternates` or `objects/info/http-alternates`, none of which
  it writes; any of them keeps.
  Each network call runs under a 10-second context; an error or timeout
  keeps with the git error, credentials scrubbed, as evidence. A proof
  remote whose URL isn't `https`, `ssh` or the scp-like `host:path` form, or
  whose host or path starts with `-`, is never contacted and keeps with
  "remote URL not allowed" as evidence. Before either comparison niwa
  parses every URL involved (the clone URL the configuration gives, each
  `proof_remotes` prefix and each remote URL read from the repository) and
  refuses one whose path holds a `.` or `..` segment or an empty segment
  between two slashes, that carries any percent-encoding, a backslash or a
  control character, or that has userinfo: any userinfo at all in an
  `https` URL, and in an `ssh` or scp-like URL anything other than the bare
  user name `git` (no password, no other name), which is kept as part of
  the compared URL. The reason is that the string niwa compares isn't the
  request git sends: libcurl removes dot segments before it sends, and a
  server may decode percent-escapes, so
  `https://github.com/dana/../mallory/repo` starts with the prefix
  `https://github.com/dana/` but reaches `github.com/mallory/repo`. A
  refused URL is never a proof remote: a refused remote is never contacted,
  a refused clone URL or prefix vouches for nothing, and a commit only it
  could hold keeps the unit with "remote URL not allowed: <reason>" as
  evidence, the reason naming the dot segment, empty segment, encoding,
  character or userinfo found. A clone's own `objects/info/alternates` may
  name directories outside the unit; git reads objects from them when it
  builds the local pack, which is read-only and changes no verdict, since
  every such object is re-hashed on its way into the store and only a proof
  remote's refs count as held.
- **An index flag keeps the unit.** `git status` doesn't compare a file
  whose index entry carries the assume-unchanged or skip-worktree bit, so
  an agent that sets either one with `git update-index` can hide a modified
  tracked file from the modified-files check. niwa reads
  `git ls-files -v` in every repository with a work tree, through
  `gitCommand`, and any entry whose tag letter is lowercase
  (assume-unchanged) or `S` (skip-worktree) keeps the unit with "index flag
  hides changes: <flag> <path>" as evidence. niwa doesn't try to tell a
  harmless flag from a hiding one; its clones aren't sparse, so a flag in
  one is something the agent or its tools set. A sparse checkout sets
  skip-worktree on every path outside its cone and therefore always keeps.
  `.git/info/exclude`, and a `core.excludesFile` the repository's own
  configuration names, are ignore sources exactly as `.gitignore` is: a
  path they match is git-ignored, and `ignored_files` decides what it
  counts as. git's per-user ignore file, which git reads when no
  `core.excludesFile` is set, never decides a class: niwa passes
  `-c core.excludesFile=` when the repository's own configuration names
  none (Decision 8).
- **Build trees** are git-ignored directories carrying `CACHEDIR.TAG` with the
  standard signature line (cargo's `target/` and any tool following the Cache
  Directory Tagging convention), plus directories a workspace declares for a
  repository in `[repos.<name>] build_trees`, given as exact paths relative to
  the repository root with a trailing slash and no wildcards.
- **Generated paths** are the files niwa or Claude Code writes again on
  provisioning: `instance.json` and the other files under `<instance>/.niwa/`
  that niwa manages, the files `managed_files` in `instance.json` lists,
  context files, plugin and marketplace trees, and a job directory's
  `state.json` and `timeline.jsonl`. An empty directory, one with no entries
  or holding only empty directories, is classed generated too wherever it
  sits in the instance or the job directory: it holds nothing to lose, and
  git doesn't report one inside a clone.
- **A watch review instance's `pr-clone` is judged by the normal rule** in
  this release, with no class of its own. `watch.FetchPRHead`
  (internal/watch/fetch.go) creates a fresh repository with no remote,
  fetches the PR head's SHA into it, checks that commit out and moves the
  git directory to `pr-clone/.git`. The repository and the fetch-local HOME
  sit under `os.MkdirTemp("", "niwa-watch-fetch-home-")` (fetch.go:104) and
  the move is best effort (fetch.go:136-139), so with the temporary
  directory on another filesystem, a tmpfs `/tmp` on Linux for one, the
  fetch leaves `pr-clone` with no `.git`. A `pr-clone` with its `.git` is a
  repository with no proof remote, so the commit it holds keeps the unit;
  one left without `.git` holds the PR's files outside every repository,
  and they keep it too. So once a fetch has written anything into
  `pr-clone`, the rule keeps the review instance, and it stays on disk as a
  stopped unit, listed by `niwa list`, until a person destroys it. That's
  the known cost of this release. Telling niwa's own fetch apart from what
  a review agent wrote, without handing the agent a path it could write to
  make niwa delete or skip anything, needs a record of each fetch kept
  outside the instance and a `FetchPRHead` that keeps its git directory on
  the instance's filesystem and fails when the move fails. That work, and
  the removal of review instances through the rule that it allows, is
  tsukumogami/niwa#551; `FetchPRHead` stays as it is today here. The review
  draft, `watch-review-draft.md` at the instance root, and anything else the
  review agent wrote are ordinary unit content, judged by the rule like any
  other path.
- **Processes** come from one listing per pass: `/proc/<pid>/cwd` and
  `/proc/<pid>/fd` on Linux, `lsof -n -P -w -F 0pn` on macOS, whose fields
  end in NUL rather than a newline so a crafted file name can't forge a
  field. A failed listing is undecidable and keeps every unit it would have
  cleared. A `claude agents` call that fails, times out, exits non-zero or
  prints output that doesn't decode strictly is undecidable the same way and
  keeps every unit; only a listing that decodes counts a session as gone.

A pass orchestrator, `runPass(ctx, root, mode, scope)` in a new
`internal/cli/pass.go`, drives the rule. `reapOpportunistically` and
`runReap` call it for every unit; `niwa retire` calls it for one; the
verdict-only mode calls it with acting turned off. The five things
`reapWorkspace` does today become sources of candidates feeding the same
loop: units whose session is gone (today's mapped sweep), stopped units the
over command may answer, marked units, unmapped dispatch instances (the
backstop, which still honors `.niwa/dispatch-retain`), and trim candidates.
Failed-dispatch cleanup, the watch's discard and the watch's failed-stage
rollback call `removal.Evaluate` for their one instance before
`destroyInstanceFunc`, and the hook worktree sweep
evaluates each worktree's repository checks before `worktree.DestroySession`,
then calls it with force off and with a git invoker built on `gitCommand`.
On the sweep's path, `DestroySession` no longer runs the
`git worktree remove --force` it runs today even when its caller doesn't
force. Its own `git status` is the clean check: a change it reports keeps
the worktree, named like any other disagreement, and otherwise niwa removes
the worktree itself with the walk Data Flow step 7 describes, not through
git, so the sweep never force-removes a worktree. That invoker runs the
configuration check again right before `DestroySession`'s `git status` and
again right before the removal, so a key written after the sweep's first
check keeps the worktree. The sweep records the worktree's HEAD and
per-worktree refs with its evaluation, and the invoker reads them again
right before the removal; any change keeps the worktree with "changed
during the pass: ref <name>".

A watch review instance holds niwa's fetch of the PR and often a whole
review session, so the rule usually keeps one, and the two watch paths say
what happens then. When the rule keeps a review instance at the discard
(`discardStagedRecord`, reached from `pruneStagedRecord` at watch.go:657
for a session no longer live and at :677 for a stale review), the discard
still deletes the staged record and prints the keeping check with the path
it names. The instance stays and is listed as a stopped unit under the
normal lifecycle (once its session has stopped, for a stale review whose
session is still live), with the check that keeps it; since its `pr-clone`
keeps it, it stays until a person destroys it. A keep from the
before-remove gate still leaves the instance and its staged record as they
are, as today.

The handled entry (`watch.AppendHandled`, internal/watch/state.go) keeps
today's format: one entry per PR, at the head last handled,
written by a successful stage (watch.go:896), by a continuation whose
re-review prompt was delivered (watch_continue.go:371) and by a legacy
entry's head adoption (watch.go:329). It names no
instance, and the rule never reads it. The one addition is at the
failed-stage rollback. Whenever the rule keeps the instance there, whether
the stage failed before the review agent launched (the instance marking at
watch.go:818, the fetch at :824, the settings at :840, the digest at :848,
the reap key at :857 or the launch at :864) or after it, the rollback still
appends the handled entry for that PR and head SHA through
`AppendHandled(workspaceRoot, owner, repo string, number int, sha string)`,
so the next watch pass never stages the same PR head into a new instance,
and the kept instance is listed as a stopped unit the same way. The
pre-launch case matters as much as the other: a pre-launch failure can
last, `claude` missing from cron's `PATH` for one, and since a fetch that
wrote anything leaves a `pr-clone` the rule keeps, every pass would
otherwise stage the head into one more full instance. With the entry in
place, `currentHeads` confirms that PR's head and `decideKind` returns
`Noop` while it equals the recorded SHA (select.go:177-178); a new head for
the same PR stages as it would have. No record of kept stages beyond the
handled entry is needed.

That makes a trade. A stage that fails after its fetch wrote anything, at
the settings (:840), the digest (:848), the reap key (:857) or the launch
(:864), even once and for a passing reason, leaves that PR head handled
with no review, and it isn't retried at that head, even after a person
destroys the kept instance: only the next push to the PR stages it again.
Today the pre-launch rollback removes the instance and the next pass
retries. Giving that up is the price of never staging a head into one more
instance each pass. So the four comments that state the old contract are
updated to say a failed stage whose instance the rule keeps records the
head too: `AppendHandled`'s doc comment (state.go:131-136), which says it's
called only after a successful contained dispatch so a transient failure
never suppresses a review, `stageReview`'s doc comment (watch.go:771-772),
the "Record ONLY on success" comment (watch.go:878) and the comment at
`stageReview`'s call site (watch.go:305-306). The rollback's output, besides
the keeping check and path, says that PR head won't be staged again until
it moves. A rollback whose instance the rule passes removes
it as today and appends no handled entry, so the PR stays eligible and the
next watch pass stages that head again: a stage that fails before its
fetch writes anything into `pr-clone` leaves only the provisioned clones
and, at most, an empty `pr-clone`, which is generated. Treating the whole
`pr-clone` tree as niwa's output at a pre-launch rollback, so that such a
rollback could remove the instance, is part of tsukumogami/niwa#551, which
restores the retry for a stage that fails before launch: there a kept
pre-launch stage is recorded against the instance it kept rather than as
handled, and the head is staged again once that instance is gone.

The before-remove gate, the instance lock, the re-read before removal (whose
`git status`, like every status, the invoker precedes with a fresh
configuration check) stay as they are and wrap the
rule's verdict. The on-reap step moves: today `onReap.settle` runs the
on-reap command and deletes the mapping after `destroyInstanceFunc`
(reap.go:668-679), while the pass runs the command after `claude rm` and
before the tombstone, and deletes the mapping only after `DestroyInstance`
(Data Flow step 7). The removal itself
gains two guards. `workspace.DestroyInstance` `Lstat`s the instance directory,
refuses a symlink, and refuses any path that isn't a direct child of the
resolved workspace root. And the last step before it renames the instance to a
tombstone and checks again, so a writer that arrives after the re-read finds
no path to write to.

The tombstone closes most of the gap between the last check and the delete.
The re-read comes before seconds of worktree removal, `claude rm`, on-reap
and `os.RemoveAll`, and the instance lock binds only niwa, so a process the
listing missed or a helper that starts late could otherwise change a
repository or a record after the re-read and have that change deleted
unseen. After on-reap,
and immediately before `DestroyInstance`, `actReap` calls a new
`workspace.TombstoneInstance(root, name)`, which renames `<root>/<instance>`
to `<root>/.niwa-tombstone-<instance>-<8 hex>` in the same directory. A
writer that opens a path under the old name from then on gets "no such
file". niwa then repeats the cheap checks on the tombstone: a fresh process
listing for working directories and open files under it, in which a
registered helper counts as it does at liveness (it doesn't keep a stopped
unit),
`git status --porcelain=v2 -z --ignore-submodules=all --untracked-files=all`
and `git ls-files -v -z`
of every repository the walk found, preceded by a fresh run of the
program-naming configuration check on each of them (a flagged key or a
newly flagged index entry counts as a difference), and, for a prunable
worktree, its `git diff-index --cached` read from its admin directory, the
hashes of the records in `.niwa/`, and a ref snapshot. These compare git
state and records only: neither the walk nor its mount and device check
is repeated there, so a late write outside every repository, to a
git-ignored path or into the job directory isn't seen (Security
Considerations gives what that leaves). `git status` stays
clean when a short-lived writer commits, branches or stashes, so with the
re-read before removal the pass records, for every repository the walk
found, every ref `for-each-ref` lists (branches, tags, remote-tracking refs
and `refs/stash`, and in a linked worktree its own `refs/worktree/*` and
`refs/bisect/*`, a prunable entry's read from its admin directory), HEAD,
the `stash list` and each worktree's HEAD from
`git worktree list --porcelain -z`, all local reads through `gitCommand`. Right before
niwa removes each worktree, it reads that worktree's HEAD and its
per-worktree refs again through `gitCommand` and compares them with the
snapshot; any change keeps the unit with "changed during the pass: ref
<name>" and that worktree isn't removed. The same read runs the
configuration check again on that worktree's repository, and a flagged key
keeps the unit the same way, with the key named. It then runs the
worktree's `git status`, the clean check `git worktree remove` made for
itself before deleting, now made by niwa, and any entry keeps the unit with
"changed during the pass: <path>". The check on the
tombstone takes the same snapshot again and compares: a ref that is new,
has moved or is gone, a new stash entry, or a HEAD or worktree HEAD that
points elsewhere counts as a difference, with "changed during the pass:
ref <name>" as evidence. The only entries allowed to disappear are those of
a worktree niwa itself removed in between, and only those that matched the
snapshot when niwa read them right before that removal. A path the
kept-set move took out may also read as gone or deleted, while anything
found at such a path again counts as a difference. Any difference
renames it back with
`workspace.UntombstoneInstance` and keeps the unit with "changed during the
pass", left as the delete-then-reap path leaves one, its sessions and
worktrees already gone and the unit listed. Otherwise `DestroyInstance`
removes the tombstone, which is a direct child of the root and passes its
checks. Its plugin-record prune and Claude trust withdrawal key on the path
they are given (`pluginrecord.InstanceOwned` matches records under it, and
the trust keys are that path resolved), and the records and grants name the
instance's original path, so on the tombstone path alone both would match
nothing and leave every record and grant behind. The pass therefore calls
or extends `DestroyInstance` so the prune and the withdrawal use the
original path `<root>/<instance>`, with any key that needs the path to
exist resolved before the rename, while the directory removal works on the
tombstone. The instance lock can't guard a directory that has been renamed, so a
root-level tombstone lock, `~/.niwa/locks/t-<workspace key>.lock`, does: a
pass holds it shared from the rename until `DestroyInstance` or the rename
back returns, so several passes can each hold a tombstone at once.
Recovery of a tombstone an interrupted pass left takes the same lock
exclusively and without blocking, and skips recovery for that pass when the
lock is held, so it can never touch a tombstone a live pass owns; the
operating system drops the lock when its holder dies, which is what makes a
tombstone left behind recoverable. Holding the lock, recovery `Lstat`s the
tombstone and goes on only when it is a real directory, never a symlink,
that is a direct child of the root; it then renames the tombstone back when
the instance name is free and evaluates it as usual, or
keeps and lists it when the name is taken. `workspace.EnumerateInstances`,
which the pass and its other callers use, gains a skip of `.niwa` and of
every name starting with `.niwa-`. No such skip exists there today: it drops
only non-directories and names `ValidName` rejects, and a tombstone, which
still holds `.niwa/instance.json`, would be listed as an instance.
`hostdir.InstanceDirs`, which skips only `.niwa`, stays as it is, so the
plugin store's sweep still sees a tombstone's links.

The budget is one deadline. An all-unit pass gets 30 seconds from its start,
the figure the existing removal gate already uses per pass
(`defaultRemovalGateBudget`), and every command and call inside it shares
that deadline: over runs, loss scans, the gate (its budget becomes the
smaller of 30 seconds and what remains), the on-reap step and removals.
Units are taken oldest-first by the time the pass last reached them, units
never reached first, so a unit left over by one pass is near the front of the
next. An over run starts only when the remaining budget covers its per-run
timeout, a loss scan only when 10 seconds remain, and a removal only when 5
seconds remain; a removal already started finishes. A unit the pass doesn't
start is "not reached": it gets no answer and no mark from that pass, and the
count goes to the dispatch line and `niwa reap`'s output. A unit the
deadline cuts short mid-evaluation keeps, counts as not reached, and has its
`reached_at` set to that pass, so the next pass takes it after the units
still waiting; a call that blocks in the kernel is abandoned at the deadline
rather than waited on. A retire's
one-unit pass isn't bound by the all-unit budget; its commands keep their
own timeouts. In a retire and in `niwa destroy`, which have no all-unit
deadline, every local git call runs under its own 10-second context, as the
configuration read does, and one it cuts short keeps. The budget caps how many units a pass handles and never what
the rule decides about one.

#### Alternatives Considered

**Extend `workspace.ScanInstance` and call it from every removal site.** This
reuses a scanner destroy already trusts and gives #300 its narrowest fix.
Rejected because the scanner's classes are destroy's, not the rule's: it
counts every untracked and ignored path as loss, calls the forge for unlanded
pull requests (which D3 forbids in the pass), and knows nothing of build
trees, patterns, discard and keep records or briefs. Bending it to the rule
would change destroy's checks, which the PRD keeps (D11), and leave six
callers each deciding how to combine the scan with liveness and records, so
the verdicts would still drift (D1).

**Run the earlier workspace-level remover's pass from niwa.** That remover
already implements the rule as shell, with its own check order and evidence.
Rejected because it lives in one workspace, pins its own binaries, reads its
own state outside niwa, and decides over with koto and forge calls, all of
which niwa must not depend on (D3); and a shell pass called from Go can't share
niwa's lock, deadline or re-read.

**Give each step its own budget, as today.** Today's gate gets 30 seconds per
sub-pass and on-reap 30 seconds per step, so up to about 90 seconds of gate
time alone. Rejected because the PRD bounds the whole pass with one figure
(R34), and a per-step budget can't promise that; it also lets an early step
starve later units without saying so.

**Delete straight after the re-read, with no tombstone.** No extra step.
Rejected because the window it leaves spans every acting step, seconds long,
and nothing stops a write by path in it.

**Make the tree read-only before deleting.** Writes fail outright. Rejected
because it costs a second full walk, a process running as the same user can
change the mode back, and a failure part-way leaves a half read-only
instance the person has to repair.

**Stop processes inside the instance before deleting.** Rejected because the
pass never signals a process; a live process keeps its unit.

**Recover a tombstone by its owner's pid and a timestamp in its name.** No
new lock. Rejected because a pid can be reused and a clock can jump, so
"owner alive" is a guess, while a lock the operating system releases on the
holder's exit answers the question exactly.

**Count any remote that answers as proof that a commit is held.** Forks and
personal remotes would work with no setting. Rejected because the remotes
are agent-written: an agent that adds a remote it controls and advertises
its own commit there would have that commit read as pushed and deleted.

### Decision 2: The over seam and the retire mark

Resolved inline.

niwa needs a command a workspace names, a way to run it that can't hang a
dispatch, a mark that says a unit is over, and answers to three questions the
PRD left open: whether later work in a unit voids a mark, whether one pass may
both mark and reap a unit, and what niwa does when the configured command
writes a mark itself. The earlier remover's contract shapes all of it: exit 0
for over, 1 for not over with the reason on the last output line, 2 for a
usage error, 3 when a fact couldn't be read, 124 on its deadline; the
instance path as an argument or in `NIWA_REMOVE_PATH`; a 20-second default
deadline; a check-only form that writes nothing and a form that also writes
the mark. Its mark is `<instance>/.niwa/retired.json`, a JSON file with a
schema name namespaced to that remover, the instance's name and creation
time, when and by what it was marked, and a basis (sessions, runs, branches,
pull requests).

Key assumptions:
- niwa leaves files it doesn't manage under `<instance>/.niwa/` alone through
  `niwa apply`, which the earlier remover checked in niwa's source and this
  design keeps true.
- A workspace configures the check-only form of an over command; one that
  configures the mark-writing form gets the same result, because niwa removes
  any mark written during a call that didn't exit 0.
- Session activity can be read as the later of the transcript's modification
  time and the `updatedAt` Claude Code gives for the session.

#### Chosen: a `[session.over]` command run like the removal gate, and niwa writing the earlier format

A workspace names the command in a new `[session.over]` table with the three
fields the existing session commands have: `command`, `timeout` (default
`"20s"`, following the earlier position) and `env` (names passed through,
validated by `ValidateSessionEnvName`). It's parsed into a new
`SessionOverConfig` beside `SessionOnReapConfig` and
`SessionBeforeRemoveConfig` in `internal/config/session.go`, checked by
`validateSessionDeclaration` and `checkTimeout`, and loaded at pass time by
`loadWorkspaceConfig` (on_reap.go:68-81), as the other two are. Leaving it
out turns marking off.

The runner, `overSeam` in a new `internal/cli/over.go`, copies `removalGate`
(removal_gate.go:69-209): `/bin/sh -c` from the workspace root, in its own
process group, with a fixed environment of `PATH`, `HOME`,
`NIWA_REMOVE_PATH` (the instance path, the variable the earlier contract
already reads) and `NIWA_SESSION_IDS`, plus the configured names. On the
per-run deadline niwa sends the group `SIGTERM`, then `SIGKILL` two seconds
later. Exit 0 is over and niwa writes the mark; exit 1 is not over; every
other outcome (another exit code, a signal, a command that can't start, the
deadline) is unknown and keeps. The last non-empty line of standard output,
cleaned by `sanitizeRemovalGateText` and cut to 200 characters, is the
reason, stored for the listing. After any outcome but exit 0, niwa removes
`<instance>/.niwa/retired.json` if it is a regular file owned by the user
whose `marked_at` is later than the moment the call began, so no mark exists
after any answer but over, whoever wrote it. The question and this clean-up
both run under the instance lock, which is a requirement rather than an
accident of order. The lock is taken without blocking, a single try as
`takeInstanceLock` makes today: a pass that finds it held asks nothing and
leaves the unit for a later pass (Data Flow step 4), and a `niwa retire`
that finds it held reports the unit kept, held by another niwa command, try
again, and writes nothing, so the clean-up can't remove a mark a retire
wrote at the same moment.

niwa asks only about units that are stopped, carry no valid mark and aren't
held by a keep record, and it asks each of them again at every pass, within
the pass's budget and the per-run deadline (R2, R34): the over command can
answer from state outside the unit, such as a pull request that merges, so a
"not over" from an earlier pass doesn't stand for later ones. The one
exception is R32's: a unit restored after a reap isn't asked until its
session has worked after the restore and stopped again, which `unit.json`
records as the restore time. A unit's `unit.json` also keeps its last answer
and when it was given, for the listing.
A yes is never asked again; the mark stands until the reap.

niwa writes the mark in the earlier format and place, atomically (a temp file
made exclusively in `<instance>/.niwa/`, mode 0600, then a rename), with the
schema name that work uses, `instance_name`, `instance_created` (from
`instance.json`'s `created`), `marked_at` in UTC RFC 3339, `marked_by` naming
niwa's version and whether the over command or a person's retire wrote it,
and a basis of the sessions niwa read and the branches and heads its
repository walk found. The basis carries no runs and no pull requests,
because niwa reads neither. A person's `niwa retire` writes the same file.
Whichever path writes it, niwa records the mark's SHA-256, size and
`marked_at` in the unit's ledger outside the instance (Decision 9) before the
rename.

A mark counts when it is a regular file, not a symlink, owned by the user,
no larger than 64 KiB, parses, carries the expected schema name, names this
instance's directory and creation time, isn't dated in the future, and
matches the ledger's copy by hash and `marked_at`. Anything else is no mark,
and a mark that parses but has no matching ledger entry is listed as "mark
not written by niwa", so a mark the unit's own agent writes counts for
nothing. Later work voids a mark, following the earlier
position: a session in the unit active after `marked_at`, or a branch in the
basis whose head moved or vanished, makes the mark void. The reader leaves a
voided file in place, the listing shows the unit unmarked with "mark voided"
and why, and the next yes or retire overwrites it. Deleting an untracked file
voids nothing, so the PRD's "marked and clean" recipe holds.

There is no undo interval between a mark and its reap, following the earlier
position: a unit the pass marks can be reaped in the same pass when the rule
passes it. The verdict-only mode shows that outcome ahead of time, and after
the reap the undo is a restore inside the window.

#### Alternatives Considered

**Let the configured command write the mark, as the earlier remover's full
form does.** niwa would only read marks. Rejected because niwa then can't
promise that no mark exists after any answer but over (a killed command may
have written one), the mark's contents would vary by command, and a person's
retire would need a second writer anyway (D4 wants one format, whoever
writes it).

**A niwa-owned mark, `niwa.retire/v1`, at a path of niwa's choosing.** Cleaner
naming inside niwa's namespace. Rejected because the PRD makes niwa follow
the format and place the earlier remover settles (R6), and a second format
would make marks written there count for nothing here.

**Marks that later work never voids.** Simpler to read and test. Rejected
because a unit resumed or retasked after its mark would be reaped on a stale
statement that its work was over, and the earlier position voids it for that
reason.

**A fixed undo interval, such as one pass or one hour, between mark and
reap.** Gives a person a moment to see a mark before the unit goes. Rejected
because the earlier position has none, the PRD follows it, and the restore
window already gives a longer and well-defined undo; the verdict-only mode
covers the person who wants to see first.

### Decision 3: The takeover key an over command reads

Resolved inline.

An over command that answers for a unit whose work moved to a successor needs
to know that the successor's takeover has landed, meaning the successor
finished taking over, not only that it started. The PRD wants this design to
name the key and the seam's documentation to carry it, rather than leave it
to the shirabe command's later work (R5). niwa itself never reads the key;
naming it is about the contract a plugin's command follows. The earlier
remover reads no such key.

koto already has the two pieces. `koto init SUCC --continues PRED --supersede`
appends a `session_superseded` event naming the successor to the
predecessor's log (the fence), creates the successor, copies every key under
each `--carry` prefix into it, and only then writes
`koto/superseded-carry-complete` in the successor (koto's
`src/cli/init_supersede.rs`, `CARRY_COMPLETE_KEY`). A re-run that finds the key
carries nothing, and a carry that stops leaves it unwritten.

Key assumptions:
- `koto/superseded-carry-complete` stays koto's marker for a finished carry.
  It has been read from koto's source, not yet seen in a live run, so the seam
  documentation's example is checked by a functional test of the over seam
  against a real `koto init --supersede`.
- A unit with no koto run has no takeover to find, so its command answers
  unknown with "no takeover found".

#### Chosen: the predecessor's fence naming a successor that holds `koto/superseded-carry-complete`

The seam documentation, a new `docs/guides/over-command.md`, defines a
takeover as landed when both hold: the unit's own koto session carries a
`session_superseded` fence naming a successor, and that successor's context
holds `koto/superseded-carry-complete`. Only then may an over command answer
over for a unit on the grounds that it was replaced. A fence without the key
is a takeover in progress and answers not over; no fence answers on the
unit's own run. The guide carries the exit contract, the environment, the
reason line and this key in one place.

#### Alternatives Considered

**A niwa-defined takeover file in the predecessor's instance.** niwa would
own the format and could document it fully. Rejected because the successor
runs in another instance and would have to write into its predecessor's,
which nothing else does, and because koto already writes a marker at exactly
the point the PRD names.

**Treat the fence alone as landed.** Easier to read, one log event. Rejected
because the fence is written before the carry, so a successor that crashed
mid-carry would let the predecessor's unit go with keys the successor never
received, which is the case the PRD's "finished taking over" excludes.

### Decision 4: The records a unit carries, and who writes them

Resolved inline.

The rule needs per-unit inputs the PRD names but niwa doesn't have: a keep
record that holds a unit or names files to keep, a handoff brief's file list
and time, a discard record that answers for paths, pid records for the
background processes a unit starts, and somewhere to keep each unit's last
answer, time stopped and size so `niwa list` can stay local. Three of these
had no builder in the earlier remover: the pid record writer, the command that
writes a discard record, and the listing behind the dispatch line. The
earlier remover keeps its keep and discard records in its own state outside
the instance, where niwa can't read them.

Key assumptions:
- Records under `<instance>/.niwa/` survive `niwa apply` and go with the
  instance at removal, as `instance.json` does.
- The handoff brief is written by separate work as the `brief` member of the
  unit's `keep.json`, with its file list in `files`, which is how that work
  plans it. This design reads it and writes none.
- A brief's claim that it holds a file's bytes is checked, not taken at its
  word: the brief names where it stored each such file, and niwa hashes that
  copy. Until the brief work defines that location, no such claim can be
  checked and every file flagged `in_brief` counts as unique.
- A person running `niwa discard` or `niwa retire --override-not-over` does so
  from a terminal, where they can read a prompt and type an answer.
- The unit's agent can write any file under its instance, records included,
  so a record is trusted only as far as a copy outside the instance vouches
  for it (Decision 9).

#### Chosen: one directory of small records per unit, inside the instance

Each unit's records sit in its instance's `.niwa/` directory:

- **`keep.json`, the keep record and handoff brief.** It follows the earlier
  remover's fields (owner, reason, written and review dates, files by path and
  SHA-256) and adds a `protects` list drawn from `instance` (a hold),
  `build_tree` (the trim skips this unit's build trees) and `files`, a byte
  count per file, an `in_brief` flag for a file whose bytes travel inside the
  brief, and an optional `brief` member carrying the time the brief was
  written. A record with neither `protects` nor files reads as a hold, which is
  the earlier form. A hold keeps the unit whatever its answer; past its review
  date it still keeps and is listed as due for review. The session working
  after the record's `written_at` ends a hold but not the named files. With a
  brief, files it names are handed forward: each goes to the kept set like any
  named file, except a file flagged `in_brief`, which is left to the brief
  only when niwa can verify the brief's copy. The brief must name that copy as
  `stored_at`, a path under the location the brief work defines for its
  copies; until it defines one, no `stored_at` counts and every `in_brief`
  file is unique, as the assumption above says. The location the brief work
  defines must resolve outside the instance, outside the job directory and
  outside every path niwa removes or expires, and must be durable: nothing
  Claude Code's own retention or the host's temporary-directory cleaning
  expires, and not under the Claude Code config directory; a `stored_at`
  that doesn't resolve there counts as nothing, so the file stays unique. niwa opens it component by component with `O_NOFOLLOW` and `O_NONBLOCK`,
  `fstat`s the descriptor and requires a regular file before reading it, and
  hashes that descriptor, and only a hash equal to both the
  record's SHA-256 and the file in the instance makes the file handed forward.
  Anything else (no `stored_at`, a link, a missing file, a mismatch) makes it
  unique, so it keeps the unit and is named "brief copy not verified". Scratch
  and loose root files older than the brief that it didn't name are
  disposable, and a file whose later of modification time and inode change
  time is after the brief keeps the unit and is named as newer than the brief.
  People and the brief writer write it; niwa ships no command for it in this
  design. `keep.json` has no ledger entry, because niwa doesn't write it; a
  ledger entry written by the brief writer is left to the brief work.
- **`discard.json`, the discard record, and `niwa discard`.** A person runs
  `niwa discard <instance> <path>... [--patch <file>]` from a terminal outside
  every instance. Because a discard record is the one record that turns
  unique paths into ones the rule lets go, it has two controls beyond the
  advisory instance guard (Decision 6). First, the command opens `/dev/tty`
  and refuses with "no controlling terminal" when it can't, which is the case
  for a detached, `setsid` or tool-run process with no terminal; it prints
  the instance and every path with its size to that terminal, with a random
  code drawn fresh for the call, and requires the person to type the code
  back on it. The code, not the instance name, is what's typed, because the
  agent knows the name and a tool-run process that inherited the terminal
  can push input into it. The code stops a process that can only type into
  the terminal. A process running as the person that can read the screen,
  through `tmux`, `screen` or terminal scripting, can answer it, which is
  why discarded paths wait in the kept set until the window ends. No flag
  skips the prompt.
  Second, a discard never destroys on its own: at reap, the discarded paths
  move into the unit's kept-set entry under `discarded/`, due at the end of
  the restore window, so even a confirmation faked through a pseudo-terminal
  an agent allocates (`script`, `expect`) loses nothing until the window ends,
  and the listing shows those paths and their size. niwa checks each path
  with `removal.ResolveRecorded` (below) against the instance or, with a
  `job:` prefix, the unit's job directory, and appends it with the person's
  name and the time. A path matches exactly or as a directory prefix. A
  patch, when given, must resolve inside the instance the same way and be a
  regular file; niwa stores its hash and moves it to the kept set at reap
  like a named file. The fields follow the earlier remover's hand-written
  form. niwa records the written file's hash in the ledger, and a
  `discard.json` that doesn't match it is read as absent, so its paths stay
  unique.
- **`pids/<pid>.json`, pid records, and `niwa helper`.** The program that
  starts a background process for a unit (a watch, a keep-alive helper) runs
  `niwa helper <pid> --purpose <text>` from inside the instance right after
  starting it. niwa refuses unless the named process is alive, is a child of
  the process that ran `niwa helper` (its parent pid is niwa's own parent
  pid), has its working directory inside the instance, and isn't a session
  process `claude agents` lists or an ancestor of niwa. It then writes the
  record in the `niwa.pid/v1` format niwa documents, taking the pid, the
  process start time (so a reused pid never matches) and the command from the
  operating system rather than the caller, plus the purpose, and records its
  hash in the ledger. A live process whose pid and start time match a record
  with a matching ledger entry is the unit's own helper: while the session is
  working it changes nothing, and once the session is stopped it no longer
  counts as liveness, and the listing names it. The pass never signals it;
  whoever registered it accepts that the helper ends on its own when the
  instance goes. `niwa helper` runs in its starter's context, which is the
  agent's or a tool it ran, so under a sandbox that confines the agent's
  writes, Bash included (the boundary Decision 9 asks for), its ledger write
  to `~/.niwa/` fails, nothing is registered, and the helper keeps its unit.
  That is the safe side, and the guide says so; moving registration out of
  the agent's context is later hardening. A pid record with no matching ledger entry is absent. Without
  a valid record, any live process with its working directory in the instance
  keeps the unit, as the PRD requires.
- **`unit.json`, the unit record behind the listing.** Written by the pass
  for each unit it reaches: the last answer (over, not over, unknown, no
  command configured), its exit status and reason, when it was asked, when the
  unit stopped (the later of its transcript's modification time and its job
  entry's update time), its size and when that was measured, the last verdict
  with the first keeping check and its paths, a disagreement's step and
  message, a voided mark's reason, whether the last pass reached it, and the
  time of a restore. `niwa list` and the dispatch line read only this. Its
  hash goes in the ledger too; a `unit.json` that doesn't match is treated as
  never reached, so the unit is asked again and placed first in the order.

Every record is untrusted input on read, because the unit's agent can write
anything under its instance. Each reader in `internal/removal/records.go`
opens the file with `O_NOFOLLOW|O_NONBLOCK`, so a FIFO planted in a record's
place can't block the open (an open waiting in the kernel isn't cancelled by
the pass's deadline), then `fstat`s the descriptor and requires a regular
file owned by the user before reading a byte. The same open serves every
other file niwa opens under an instance, a job directory, the root's
`.niwa/` or `.niwa-launch/` (the kept-set move's sources, launch records,
`instance.json` and a job directory's `state.json` among them), and a
brief's `stored_at` copy. A named file the rule hashes is opened component by component from
the instance's or job directory's descriptor, every directory
`O_DIRECTORY|O_NOFOLLOW`, as the kept-set move opens its sources. Each reader
caps its size (64 KiB for the mark, `discard.json`, `pids/*.json`,
`unit.json` and `entry.json`, 1 MiB for `keep.json`), decodes into a fixed
struct, and rejects an unknown schema name. A failure always lands on the
keeping side: an unreadable `keep.json` keeps the unit as a missing record
the rule needs, and an unreadable mark, discard record or pid record is
absent. Every path a record names (`keep.json` files and brief files,
`discard.json` paths and patch, `entry.json` files) goes through
`removal.ResolveRecorded(base, path)` on every read, not only when niwa
writes it: the path must be relative, non-empty and free of `..`, NUL and
backslash; a `job:` prefix sets the base to the unit's own job directory and
nothing else; and each component is checked from the base down with `Lstat`,
so a symlink anywhere in the path, the last component included, or a
component on another device fails it. A path that fails is named in the
verdict, and the record keeps the unit as unreadable. The record locations
are checked as well as the files: `<instance>/.niwa` and `<instance>/.niwa/pids`
must be real directories (`Lstat`, no symlink) owned by the user, and niwa
opens each with `O_DIRECTORY|O_NOFOLLOW` and does every read, temp-file
write, rename and removal (the mark clean-up included) relative to that
descriptor, so a directory swapped for a link never redirects a write. A
`.niwa` or `pids` that fails this makes the unit's records unreadable, which
keeps, and niwa writes nothing there. niwa writes every record it owns with
mode 0600.

The discard command and the pid record format are the two builders the
earlier remover left open; the listing behind the dispatch line is the unit
record plus Decision 6's output. All three are settled here, and none is left
to the shirabe command's later work.

#### Alternatives Considered

**Keep the records in the workspace root's `.niwa/`, keyed by instance
name.** One place to list them, and they'd survive the instance. Rejected
because the root's `.niwa/` is recopied when the configuration snapshot swaps,
a record that outlives its instance has nothing to describe, and the location
rule forbids large host state there; per-unit records belong with the unit.

**Read the earlier remover's state directory directly.** It already holds
keep and discard records for one workspace. Rejected because that directory
exists only where that remover is installed, so every other workspace would
get nothing (D4 says follow its format, not its install).

**Guard `niwa discard` with the advisory instance guard alone.** No prompt,
so a person can script it. Rejected because a discard record releases unique
paths without the rule's say, and a detached process that changes directory
and drops two variables passes the guard.

**Require a terminal, with a `--yes` flag that skips typing.** Scripts by a
person stay easy. Rejected because a process started from an interactive
session can inherit that session's terminal, so `--yes` would reduce the
control to "a terminal exists". The code stops a process that can only type
into the terminal. A process running as the person that can read the
screen, through `tmux`, `screen` or terminal scripting, can answer it, as
can one that allocates its own pseudo-terminal, which is why discarded
paths wait in the kept set until the window ends.

**Delete discarded paths at reap, as the earlier remover's form does.** No
kept-set disk spent on them. Rejected because the terminal check can be
faked by an agent that allocates a pseudo-terminal, and only a copy until
the window ends makes that harmless.

**Leave pid records unwritten and every live process keeping its unit.** No
new format. Rejected because the PRD gives this design the writer, and a
helper that outlives its session would then keep a finished unit forever with
no way for its starter to say otherwise.

**A `niwa keep` command for keep records.** Symmetric with `niwa discard`.
Rejected for now because the record's form is still an open dependency on the
earlier remover, its main writer is the separate brief work, and a command
fixed before the form lands would need changing; a hand-written record works
for a person meanwhile.

### Decision 5: The kept set, the launch record and the restore window

Resolved inline.

When a unit with named files is reaped, those files have to land somewhere
outside every instance, unchanged by hash, and come back on a restore. A reap
on a mark needs a launch record to replay, which today only `niwa dispatch`
writes, so a unit the SessionStart hook provisioned can't be reaped on a mark
at all. A restore needs a window with an end date. Two existing positions
pull against the PRD here. The earlier remover's kept set never expires: only
a person removes an entry. The PRD instead marks an entry due on its date,
shows it, and removes it at the first pass at least a day after it was first
shown (R27). And `docs/designs/current/DESIGN-plugin-release-hold.md`, which
tsukumogami/niwa#536 implemented, plans for teardown work to
keep whole finished instances at `~/.niwa/w-<key>/kept/<instance>/` and scans
that directory for plugin links (`hostdir.InstanceDirs`), while this kept set
holds only the files keep records name.

Key assumptions:
- The workspace key is `hostdir.WorkspaceKey` ("w-" plus the first 32 hex of
  the SHA-256 of the canonical root, internal/hostdir/hostdir.go:34), which
  tsukumogami/niwa#536 moved there from `internal/mirror`;
  `mirror.WorkspaceKey` now delegates to it.
- Claude Code's transcript retention is `cleanupPeriodDays` in Claude
  Code's user settings, `settings.json` in the config directory niwa
  resolves as Claude Code does (`CLAUDE_CONFIG_DIR` when set, `~/.claude`
  otherwise; Data Flow step 7), 30 days when unset.
- A session the SessionStart hook provisioned can be resumed with
  `claude --resume` from its instance path with no permission mode, settings
  or environment replayed, since niwa didn't launch it with any.

#### Chosen: a separate `kept-files/` directory under the location rule, launch records for hook sessions, and window dates on the launch record

The kept set lives at `~/.niwa/w-<key>/kept-files/`, one entry per unit and
move: `kept-files/<instance>/<UTC time of the move>/` holding `files/` with
each named file at its in-unit path, the keep record with every path
rewritten to its place there, and `entry.json` (schema `niwa.kept/v1`: the
instance, its creation time, when it was moved, the due date of `files/`,
when it was first shown due, and each file's original path, SHA-256 and
size). A move
within one filesystem is a rename; across volumes it's a copy, a hash check
and a delete of the source, and a failed write or a hash mismatch keeps the
unit with nothing removed. The move takes only regular files, and it checks
what it opens rather than the path it checked earlier: each source is opened
component by component from the instance (or job directory) descriptor, every
directory with `O_DIRECTORY|O_NOFOLLOW` and the file with
`O_NOFOLLOW|O_NONBLOCK`, so a directory swapped for a link between the check
and the open fails the open and a FIFO can't block it.
niwa then `fstat`s the descriptor before reading from it (a regular file,
owned by the user, on the instance's device, with a link count of exactly
one) and hashes and copies
from that descriptor, never from
the path; a same-filesystem move is a `renameat` relative to the opened
parent. Anything else (a symlink, a device, a directory, a file with a
second hard link) keeps the unit with
the path named, so a keep record can't point the move at a file outside the
instance, a hard link to one included. A unit's entry also holds the paths its discard record released,
under `discarded/` at their in-unit paths, with their own due date and
first-shown date in `entry.json` (`discarded_due`,
`discarded_first_shown_due`): the due date is the end of the restore window,
whatever review date the keep record gives. Entry directories are created 0700, `entry.json` and
the rewritten keep record 0600, and each file keeps its permission bits with
setuid, setgid and sticky cleared. A per-workspace lock in `~/.niwa/locks/`
guards the entries; every lock file there is created 0600 with `O_NOFOLLOW`.

Using `kept-files/` rather than `kept/` resolves the overlap with the plugin
release store. This design never keeps a whole instance outside the root,
so it writes nothing into `kept/`, and the release store's scan of that
directory finds no entries from it; its entries carry no
`.niwa/marketplaces/` links that scan would need to count. If whole kept
instances ever arrive, they keep the `kept/` name the release store already
reads, and the two never share a directory or an instance name. This is
named as an overlap with tsukumogami/niwa#536, which has landed, so this
work checks the release store's layout as it now stands.

The due-date clash is reconciled by store. niwa's kept set follows R27, and
an entry's `files/` and `discarded/` expire separately, each with its own due
date and first-shown date. `files/` is due at the keep record's review date,
or at the end of the unit's restore window when the record names none;
`discarded/` is due at the end of the restore window. On a part's due date
the pass marks that part due, the listing and the dispatch line show it (an
entry counts as due while either part is), and the first pass at least a day
after that part was first shown removes it. The entry directory goes when
both parts are gone, so a review date before the window's end takes the
named files early but never the discarded paths. The earlier remover's own kept
set, in that remover's state, keeps its never-expires rule; niwa never reads
or removes it, and its entries aren't carried into niwa's. A person who wants
an entry kept longer gives the record a later review date. If the earlier
work lands with never-expires as a rule for every form, the requirement it
would change is R27, and that change goes to the PRD rather than into this
design.

When niwa removes a whole workspace (`DestroyWorkspace`), it leaves
`kept-files/` in place and prints its path, as the release store leaves a
store kept instances still use, because these are files a person asked to
keep and nothing could show them as due once the workspace is gone.

`niwa instance from-hook` writes a launch record for the sessions it
provisions, in the same `niwa.launch/v1` store at `<root>/.niwa-launch/`,
keyed by a launch id it mints in the dispatch-id shape
(`authority.ValidDispatchID`), with the agent, the session, the instance name
and the time, and a new optional `origin` field set to `hook`. The session
mapping gains a `launch_id` field that names it. `resolveLaunchRecord`
already matches by instance name and session, so `niwa resume` finds it
with no change to that lookup. The check that follows it does change.
Today `validateLaunchRecord` (resume.go:579-585) refuses any record whose
instance name fails `isDispatchInstanceName`, and that pattern
(`\+[a-z0-9_]*-[0-9a-f]{8}$`, dispatch.go:1503) matches no hook instance.
The hook names its instance `<config>-` followed by the first 12
characters of the session's UUID (`sessionNamePrefixLen`,
instance_from_hook.go:90 and :262-270), so the name ends in
`-<8 hex>-<3 hex>` and holds no `+`. `validateLaunchRecord` accepts a valid
instance name of that hook shape when the record's `origin` is `hook`, and
a dispatch-shaped one otherwise, as today. A hook record carries none of the fields `niwa resume` replays
for a dispatched session (permission mode, settings, environment), and
resume ignores those fields on any record whose `origin` is `hook`; every
launch record is decoded strictly, with unknown fields refused.

The session mapping `niwa resume` writes before the launch follows the
record's origin. Today `runResume` writes `Origin: MappingOriginDispatch`
and the record's `DispatchID` on every resume (resume.go:419-429), and
`niwa authority check` binds a mapping of dispatch origin to the
instance's dispatch record (authority_check.go:151-157, `bindToRecord` at
:301-314). A hook instance has no dispatch record, so a restored hook
session would answer `record-unusable` where it answered `not-dispatched`
before the reap. For a record whose `origin` is `hook`, resume writes the
mapping in the shape `niwa instance from-hook` writes
(instance_from_hook.go:304-310): the session, the instance's name and path,
the transcript path and `Ephemeral`, with no origin and no `DispatchID`,
plus the `launch_id` naming the record. `niwa authority check` then answers
`not-dispatched` with reason `hook-mapping` for the restored session, as it
did before the reap. A resume of a dispatch-origin record keeps writing
the dispatch shape it writes today, with `launch_id` set as well.

The launch record also carries the window. At every reap of a unit that has
a launch record, whether it was reaped on a mark, on a deleted session or by
the backstop, the pass writes `reaped_at`, `restore_until` (the reap time plus the window) and the path of
the unit's kept-set entry into it. A unit with no kept-set entry, a clean
one with no keep record and nothing discarded, gets `reaped_at` and
`restore_until` and no `kept_entry`: the field is absent, never written
empty. The window is the workspace's
`restore_window` setting when set, otherwise Claude Code's transcript
retention read at reap time. A `restore_window` under seven days is a
configuration error from `validateSessionDeclaration`, naming the setting and
the seven-day minimum, so every command that loads the configuration exits
non-zero and no pass runs. The launch-record sweep, `sweepLaunchRecords`
(internal/cli/launch_records.go), today removes a record once its transcript
is gone. It now keeps every record that carries `reaped_at` until its
`restore_until` has passed, whether or not the transcript still exists, and
only after that applies today's transcript rule. Without that, a reaped
unit's record could go inside its window: Claude Code's retention counts
from the transcript's last write, which comes no later than the reap, so the
transcript can be removed before `restore_until`, and the next pass would
take the record and its `kept_entry` with it. So the record, and the kept
entry it names, are there for the whole window. That alone doesn't make a
unit resumable: `niwa resume` still needs the session's transcript and
refuses, naming it, once the transcript is gone (`checkWorkerInstance`,
resume.go:262-276), so a window longer than Claude Code's retention keeps
the record and the kept entry but can't resume the unit. Restoring a unit
without its session is out of scope. Either way the kept files stay
copyable from the kept set, at the paths `niwa list --kept` shows, until
they're due. `niwa resume` refuses a record past `restore_until` with the
date the window ended, for as long as the record is there. Inside the
window, with the transcript still there, before the session
starts, it moves the entry's files back to their original paths with a hash
check, and refuses, leaving everything in place and naming the file, when one
doesn't match. A record with no `kept_entry` moves no files: inside the
window, with the transcript there, resume starts the session as it does
today and creates no kept-set entry. A later step of the resume can still fail before the session
starts, and its rollback removes the fresh instance (resume.go:385-389), so
the rollback first moves every restored file back into the entry unchanged,
checked by hash; a failed resume leaves the kept set and the restore window as
they were. A file that can't be moved back that way keeps the instance, which
the rollback then leaves in place with the file named. The restore trusts no
path it reads. The launch record sits
under the workspace root, where the agent can reach it, so a `kept_entry`
that is present must name exactly `~/.niwa/w-<key>/kept-files/<instance>/<UTC>/` for the
record's own instance, a direct descendant reached with no symlink, or the
restore refuses. The strict decoding keeps absent and empty apart: the
field is decoded as a `*string`, nil when the field is absent, so an
absent `kept_entry` is valid, and a present empty string is told apart and
refused like any other path that doesn't match. Every original path in `entry.json` and the rewritten keep
record passes `removal.ResolveRecorded` against the fresh instance, and every
source path must resolve inside the entry's own `files/` directory. A
destination with a path component named `.git` or `.claude`, compared
case-insensitively, refuses the restore, as one outside the instance does,
so no restore writes a hook or settings file a later git or `claude` call
runs. Missing
parent directories are created one component at a time, each opened with
`O_DIRECTORY|O_NOFOLLOW` so no link is followed, and each file is created
with `O_CREAT|O_EXCL|O_NOFOLLOW`, never over an existing file; a path that
fails any of this refuses the restore the same way a hash mismatch does,
naming the file. Discarded paths aren't put back; they stay in the entry's
`discarded/` for the person to copy until the window ends. When the entry's
`files/` already left because its review date came
before the window's end, the restore goes ahead without those files and says
which entry's files were gone, since the person who set that date chose it. The
restored instance carries no mark.

#### Alternatives Considered

**Use `~/.niwa/w-<key>/kept/<instance>/`.** One
name for everything kept. Rejected because the release store treats every
real directory there as an instance whose plugin links count and stops its
sweep when one can't be read, so file-only entries would sit in a scan built
for something else, and a later whole-instance keeper would collide on the
same instance names.

**Never expire niwa's kept set, like the earlier remover's.** Nothing a person
kept would ever leave without them. Rejected because R27 requires due-date
removal after a day's notice, and an unbounded store on every host is the
problem this feature exists to fix.

**Leave hook-provisioned sessions without a launch record.** No change to the
hook. Rejected because such a unit can then never be reaped on a mark or
restored, so a user of niwa alone with hook sessions has only the unchecked
route of deleting the session, which the project owner's job-scratch choice
was made alongside.

**Keep window dates in the unit record or the kept set.** Rejected because
both are gone or optional after a reap, and the launch record is already the
one store that outlives the instance and that `niwa resume` reads.

### Decision 6: Names, the listing, the dispatch line and the job-scratch default

Resolved inline.

The PRD left the names of the mark, the retire command and every setting,
the dispatch line's wording, and the verdict-only mode's surface to this
design. The project owner chose the job-scratch default: where a unit has no
handoff brief, its job scratch is disposable by default, a workspace setting
shaped like the ignored-files setting makes it unique, a workspace that
configures an over command for shirabe's workflows should set it unique in
the same configuration, the verdict-only mode lists such scratch as
disposable with a job-scratch notice, and the later idle-time setting ships
off.

Key assumptions:
- `niwa list --json` consumers iterate an array of instance records, so new
  fields can be added to each record but the top-level shape stays an array.
- A person runs `niwa retire` from a shell outside every instance, while a
  worker runs inside its own.

#### Chosen: `[session.removal]` beside `[session.over]`, `niwa retire`, a verdict-only flag on reap, and one fixed dispatch line

The names are:

| Thing | Name |
|-------|------|
| Retire mark | `<instance>/.niwa/retired.json` (the earlier remover's place) |
| Over command | `[session.over]` `command`, `timeout`, `env` |
| Retire command | `niwa retire <instance>` |
| Person-only override of "not over" | `niwa retire --override-not-over` |
| Verdict-only mode | `niwa reap --verdict-only [<instance>] [--json]` |
| Discard record command | `niwa discard <instance> <path>... [--patch <file>]` |
| Pid record command | `niwa helper <pid> --purpose <text>` |
| Record ledger | `~/.niwa/w-<key>/ledger/<instance>.json` |
| Ignored-files setting | `[session.removal] ignored_files = "disposable"` or `"unique"` |
| Job-scratch setting | `[session.removal] job_scratch = "disposable"` or `"unique"` |
| Trim switch | `[session.removal] trim = true` or `false` |
| Idle grace | `[session.removal] trim_after = "24h"` |
| Restore window | `[session.removal] restore_window = "30d"` (unset by default) |
| Workspace disposable patterns | `[session.removal] disposable = [{ pattern = "...", review = "..." }]` |
| Declared build output | `[repos.<name>] build_trees = ["target/"]` |
| Remotes that count as proof beyond the clone URL | `[session.removal] proof_remotes = ["https://github.com/dana/"]` (empty by default) |
| Kept set | `~/.niwa/w-<key>/kept-files/` |

`job_scratch` sits next to `ignored_files` and takes the same two values.
Its default, `"disposable"`, is the project owner's choice. It applies only
where a unit has no handoff brief; with a brief, the brief's rule applies
whatever the setting says. A workspace that configures `[session.over]` for
shirabe's workflows should set `job_scratch = "unique"` in the same file, and
the over seam's guide says so. The later idle-time setting, which ships off,
would sit in the same table; this design doesn't define it. Its shape, as
the project owner chose it: a workspace that turns it on gets a mark on any
unit stopped longer than the setting that no over command answers, counted
from the unit's last stop or restore, and never on a unit answered not over.

`trim_after` defaults to 24 hours. The shipped disposable patterns are
`*.swp`, `*.swo`, `*~`, `.#*`, `.DS_Store`, `__pycache__/` and
`.pytest_cache/`. Pattern syntax is glob, as in `path.Match`, plus `**` for
any number of directories; a pattern with no slash matches a base name
anywhere, one with a slash matches the path from the repository or instance
root, and a trailing slash matches a directory and everything under it.
Each workspace entry needs a non-empty `review` naming the review that added
it, and one without is a configuration error. niwa doesn't check what the
field names; it is an audit trail for the people who edit the configuration,
not a control.

`proof_remotes` is a list of URL prefixes, each an `https://` or `ssh://`
URL ending in `/` with at least a host and one path segment, so a bare host
can't be listed by mistake; a prefix that doesn't parse is a configuration
error. A prefix matches on whole path segments after normalizing, so
`https://host/owner/` covers `https://host/owner/repo` and not
`https://host/owner-evil/repo`, and only on the same scheme, user, host and
port, a default port made explicit, so a remote on another port of the
prefix's host doesn't match it. A prefix, a clone URL or a remote URL with a
dot segment, an empty segment, any percent-encoding, a backslash, a control
character or userinfo (any in `https`; in `ssh`, anything but the bare user
`git`) is refused as Decision 1 describes: it vouches for nothing, and a
commit only it could hold keeps with "remote URL not allowed: <reason>". The
list is empty by default, which leaves the clone URL as the only proof
remote (Decision 1), and a remote matching a prefix also gets the person's
credential helper (Decision 8).

`niwa retire <instance>` resolves the instance by name at the root (or from a
path given as an argument), takes its lock, and refuses when a session in it
is working, saying so. When the unit's last answer was not over it refuses
unless `--override-not-over` is given; the pass never sets the flag. The whole
command, like `niwa discard`, refuses when the instance guard finds it running
inside an instance, so a worker can't retire itself or launder its own work
through either command. The guard, `insideInstance` in a new
`internal/cli/guard.go`, answers yes when the working directory resolves
inside any instance of the workspace, when `NIWA_DISPATCH_ID` or
`NIWA_REAP_KEY` (which niwa sets in the sessions it dispatches) is in the
environment, or when any ancestor process from the pass's process listing has
its working directory inside an instance. The guard is a convenience that
catches the ordinary case with a clear message; an agent that detaches from
its session, changes directory and drops the two variables gets past it.
For a plain retire that is acceptable, because the rule still decides what
goes. It is not enough where a command loosens the rule, so
`--override-not-over`, like `niwa discard` (Decision 4), has a real control:
it opens `/dev/tty`, refuses with "no controlling terminal" when it can't,
prints the unit's name, its last not-over reason and a random code to that
terminal, and requires the person to type the code back, with no flag to
skip it. Past those checks, retire writes the mark and runs the
one-unit pass, and prints one of `niwa: retired <name> and removed it` or
`niwa: retired <name>; kept by <check>: <paths>`, naming the first keeping
check and every path behind it, or the step and message of a disagreement. A
retire stops nothing that runs.

`niwa list` adds a line per instance under the name, read from `unit.json`
and the records, never from the network or the over command:

```
tsuku+fix-cache-3f2a91c0
  stopped 3d 4h, 2.1 GiB, not over: pull request open
  resume: niwa resume tsuku+fix-cache-3f2a91c0
tsuku+bench-77c10d2e
  retired, kept by untracked files: notes.txt
tsuku+docs-1b9e0c44
  held by keep record (owner dana): rerun pending; review due 2026-12-01
kept set: 3 entries, 1 due (niwa list --kept)
```

A unit with no command configured shows `no over command configured`; an
unknown answer shows `unknown: <reason>` when the command gave one, as the
six-kinds table under Relation to Other Work does, and `unknown (exit 3)`
(the exit status it got) when it gave none; a disagreement shows
`retired, kept by a disagreement: <step> refused: <message>`. The JSON form
keeps its array and adds a `unit` object to each record with `state`,
`stopped_since`, `stopped_seconds`, `size_bytes`, `size_measured_at`,
`answer`, `reason`, `retired`, `mark_voided`, `verdict`, `kept_by` (check,
paths, evidence), `disagreement` (step, message), `held` (owner, reason,
review, due) and `not_reached`. `niwa list --kept [--json]` lists the kept set
by entry, file path and size, with the due date and due state of each
entry's `files/` and `discarded/`, never file contents.

Every string that comes from a unit (the over command's reason, git error
evidence, a keep record's owner and reason, paths) passes
`removal.ScrubEvidence` before niwa stores or prints it. Userinfo is stripped
from every URL (`https://user:token@host/x` becomes `https://host/x`, and
`x-access-token:` with it). These become `[redacted]`: known token shapes
(`ghp_`, `gho_`, `ghs_`, `github_pat_`, `glpat-`, `sk-` keys, Slack `xox`
tokens, AWS key ids starting `AKIA` or `ASIA`, JSON web tokens of three
dot-joined base64url parts starting `eyJ`); the value after
`Authorization:` or `Bearer`; the value of any `key=value` or `key: value`
pair, URL query parameters included, whose key contains `token`, `secret`,
`password`, `passwd`, `key`, `auth` or `credential`, in any case; and any
unbroken run of 32 or more base64 characters that mixes upper case, lower
case and digits (git object ids are lower-case hex and survive). Terminal
control and escape sequences are removed by `sanitizeRemovalGateText`, and
only then is the result cut to 200 characters for a reason and 400 for
evidence, so a cut can't leave the front of a token behind. `niwa list`, `--json`
verdicts, retire and the dispatch line apply it again on read, because
`unit.json` and the records are agent-writable and may not hold what the pass
wrote.

Each `niwa dispatch` prints one line to standard error after its pass, with
or without an over command:

```
niwa: 3 stopped units not retired, 4.2 GiB; 1 kept-set entry due; 0 units not reached (niwa list)
```

The template is `niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set
entr(y|ies) due; <U> unit[s] not reached (niwa list)`, with sizes in binary
units to one decimal place (`0 B`, `412 MiB`, `4.2 GiB`). The count and size
leave out marked units the rule keeps, since someone retired them; the
listing shows those. `niwa reap` prints the same three counts with its own
reaped and trimmed counts.

The verdict-only mode prints, for each unit or the one named, the verdict,
every keeping check in order with its paths and evidence, the over answer it
got and the mark it would write with the verdict that mark would lead to, and
the class of every path. A directory whose contents all share one class is
one line, so a clean clone is one tracked line and a build tree one line. A
git-ignored path is listed as disposable with a `git-ignored` notice, and
scratch that's disposable only because the unit has no brief as disposable
with a `job scratch` notice. With `--json` it prints a `niwa.verdicts/v1`
array with the same fields, which is the form a workflow plugin asks for one
unit. It writes no mark, removes and trims nothing, moves nothing into the
kept set, renames no tombstone back, and fetches into the scratch object store only.

#### Alternatives Considered

**Put the settings at the top level as `[removal]`.** Shorter keys. Rejected
because the over command, the veto and on-reap already sit under
`[session]`, which is workspace-scoped and read at removal time rather than at
apply, and the removal settings share both properties.

**A `niwa unit` command group (`niwa unit retire`, `niwa unit discard`,
`niwa unit verdicts`).** Groups the new verbs. Rejected because the PRD keeps
the trim and the verdict-only mode inside the pass rather than as verbs of
their own, `niwa reap` is where a person already looks at what the pass would
do, and two new top-level verbs are easier to discover than a group.

**A dispatch line that prints only when something is listed.** Quieter.
Rejected because R23 asks for the line at each dispatch whether or not a
command is configured, and a fixed line is easier for a person and a test to
read than one that comes and goes.

**Job scratch unique by default.** Keeps the scratch check for everyone.
Rejected by the project owner, because a user of niwa alone has no brief, so
a retire would mostly answer kept and push that user back to deleting the
session, which checks nothing.

### Decision 7: Delivery in three parts

Resolved inline.

The work touches the reap, destroy, dispatch, create, watch, list, resume, the
SessionStart hook and the configuration. Shipping it in one change would put
niwa's riskiest behavioral change, reap becoming stricter and marked units
being reaped, behind a review too large to read. The question is whether it
comes in parts, and which.

Key assumptions:
- Each part leaves niwa safe on its own: no part removes anything the rule
  wouldn't, and no part depends on a later one to undo a loss.

#### Chosen: the rule and worktrees; the over question, mark and listing; the keep record, kept set and launch record

Delivery comes in three parts. The first is the rule and worktrees: the
`internal/removal` package, the pass orchestrator and budget, the reap's loss
scan for mapped units and the backstop (#300), failed-dispatch and watch
cleanup through the rule, worktrees before the session in the pass and in
destroy, the pattern lists, and the verdict-only mode without over answers,
along with the hardened git environment, proof remotes, recorded-path
validation, evidence scrubbing and the tombstone before removal, since the
first part already runs git in agent-written repositories and deletes
instances. The second is the over question, the mark and the listing:
`[session.over]`, the record ledger, the mark writer and reader with its
ledger entry, `niwa retire` with the instance guard and the terminal
confirmation for `--override-not-over`, the unit record, `niwa list`'s new
fields, the dispatch line, the trim with its settings and the root apply
lock, pid records through `niwa helper`, and over answers in the
verdict-only mode. The third is the keep record, the kept set and the launch
record: `keep.json` holds and named files, the handoff brief reader with its
`in_brief` check, the kept set with its permissions and due dates, the
discard record and `niwa discard` with its terminal confirmation and
discarded paths kept until the window ends, launch records for hook
sessions, the restore window and the validated restore of kept files. The
discard record waits for the third part because it must not release a path
before the kept set can hold it. The Implementation Approach follows these
parts.

#### Alternatives Considered

**One change.** Nothing half-done ever ships. Rejected because a single
review of every removal path at once is where a loss slips through, and the
first part alone closes #300, which is the change most users notice.

**Ship the over seam first.** It's the headline. Rejected because a mark
reaped by a rule that doesn't yet scan for loss would take unique work; the
rule must land before anything starts more removals.

### Decision 8: How niwa runs git in a repository the unit's agent controls

Resolved inline.

The rule runs `git status`, `ls-files`, `for-each-ref`, `stash list`, `ls-remote`,
`worktree list`, `diff-index --cached`, `pack-objects`, `merge-tree` and a fetch inside repositories whose `.git`
the unit's agent can write, automatically, before every dispatch. Left to
git's defaults, a repository's own configuration turns those calls into code
execution or network traffic of the agent's choosing: `core.fsmonitor`,
`core.hooksPath`, `core.pager`, `core.sshCommand`, `core.askPass`,
`credential.helper`, filter, diff and merge drivers, a `gpg.program` that
`log.showSignature` makes `stash list` run, and `ext::` or `file://`
remote URLs. The question is how to run the calls the rule needs without
running anything the repository names.

Key assumptions:
- Reading configuration (`git config --list`) runs no program, so niwa can
  look at a repository's settings safely before running anything else in it.
- Command-line `-c` settings take precedence over every file and are passed
  on to the git processes git itself starts.
- The person's own global git configuration is trusted, but it isn't a fixed
  environment: what it holds varies by host.

#### Chosen: one hardened command builder, a refusal on program-naming config, and network calls from the scratch store

`gitCommand` in `internal/removal/git.go` builds every git call niwa's
removal makes: in the pass, in the hook worktree sweep through
`worktree.DestroySession`, and in `niwa destroy`'s status before each
worktree removal and its scan, `workspace.ScanInstance` (Decision 1). The
worktree removal itself makes no git call. The one exception is the read
of the person's credential helper, below. It starts git by absolute path,
resolved once per pass from a filtered `PATH`: empty and relative entries
are dropped, each remaining entry
is resolved through symlinks, and any that resolves inside the workspace
root is dropped too. `lsof` and `claude` are resolved once per pass from the
same filtered `PATH` and run by absolute path, and the over command and the
other session commands keep running under `/bin/sh` by its absolute path,
with that filtered `PATH` in their environment. Every `claude` call niwa
makes (`claude agents`, `claude rm`) runs from an empty directory outside
the workspace, under `~/.niwa/w-<key>/scratch/` and checked for owner and
mode before each use (Decision 1), as the credential-helper read below
does, with the filtered `PATH`, so no instance's `.claude/settings.json` or
`settings.local.json`, which the agent can write, is the project Claude Code
reads for that call; the rest of its environment is the person's, since
`claude` needs their authentication and configuration. Every `claude rm`
niwa makes, retask's included, also waits on a vet of that session's
own job directory (Data Flow step 7), since Claude Code deletes that
directory itself: a root that fails, or an entry under it that is neither
a regular file nor a directory, keeps the unit with no call made. The
`PATH` filter is a narrow control: it stops a
binary planted inside the workspace from being picked up, but the agent runs
as the person and can write other directories on the person's `PATH`
(`~/.local/bin`, say), which niwa can't tell from the person's own tools.
git runs with no inherited environment except `HOME`, and with:

- `LANG=C`, `GIT_CONFIG_NOSYSTEM=1` and `GIT_CONFIG_GLOBAL=/dev/null`, so only
  the repository's own configuration and niwa's overrides apply;
- `GIT_TERMINAL_PROMPT=0` and `GIT_ASKPASS` set to the empty string, which
  stops git from running `core.askPass` or `SSH_ASKPASS`;
- `GIT_ALLOW_PROTOCOL=https:ssh`, so `ext::`, `file://`, local paths and
  every other transport are refused by git itself;
- `GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ForwardAgent=no"`, which
  overrides any `core.sshCommand`. `SSH_AUTH_SOCK` isn't passed, so ssh
  reaches no ssh agent unless the person's own `~/.ssh/config` names one
  with `IdentityAgent`; otherwise it uses the key files that configuration
  and ssh's defaults name, and in batch mode only those it can use without
  a prompt. An ssh proof remote the person reaches only through an agent
  therefore reads as unreachable and keeps. Batch mode also turns ssh's
  question about an unknown host key into a failure, so a proof host not
  yet in the person's `known_hosts` reads as unreachable and keeps too; niwa
  never accepts a key on the person's behalf. ssh still reads the person's
  own `~/.ssh/config` and every file it names with `Include`, so their
  `ProxyCommand`, `Match exec` and other program-running settings run on
  these calls, trusted as the person's own configuration (Decision 9 gives
  the boundary that trust rests on);
- `GIT_PAGER=cat`, `GIT_OPTIONAL_LOCKS=0` (status never rewrites the index,
  so nothing under the unit's `.git` changes), `GIT_NO_LAZY_FETCH=1` (a
  partial clone can't fetch missing objects from its promisor remote during
  `merge-tree`) and `GIT_CEILING_DIRECTORIES` set to the workspace root;
- `GIT_NO_REPLACE_OBJECTS=1` and `GIT_GRAFT_FILE=/dev/null`, so no
  `refs/replace/` entry and no `info/grafts` file changes the history git
  reads; pointing the graft file at an empty file is simpler than detecting
  a grafts file and refusing, and it holds in every repository and in the
  scratch store alike;
- the arguments `--no-pager`, `--git-dir` and `--work-tree` taken from the
  walk (which overrides any `core.worktree`), and `-c core.fsmonitor=false
  -c core.hooksPath=/dev/null -c core.pager=cat -c credential.helper=
  -c submodule.recurse=false -c fetch.recurseSubmodules=false
  -c core.useReplaceRefs=false -c core.commitGraph=false
  -c log.showSignature=false -c status.showUntrackedFiles=all
  -c core.untrackedCache=false -c http.proxy= -c http.sslVerify=true
  -c core.trustctime=true -c core.checkStat=default`, with
  `-c core.excludesFile=` added when the repository's own configuration
  names none, so git's per-user ignore file (`~/.config/git/ignore`, read
  even with `GIT_CONFIG_GLOBAL=/dev/null`) never decides a class; one the
  repository names stays an ignore source (Decision 1). The stat pins keep
  a repository's own `core.trustctime=false` or `core.checkStat=minimal`
  from making git trust the index's cached stat data more than it does by
  default. The two `http` values keep a network call off any proxy and on
  certificate checking; the only configuration file such a call reads is
  the scratch store's, which niwa writes and checks before each call
  (Decision 1). The commit-graph
  setting matters because git loads commit-graph files, its own and those
  of any alternate object directory, without verifying them, so a forged
  one in a clone could otherwise give a commit a parent it doesn't have.
  `git stash list` is `git log -g` underneath, and with `log.showSignature`
  on, git verifies every listed commit that carries a signature by running
  `gpg.program`; the override turns that off, and every log-family call
  (`stash list`, and any `git log` that `ScanInstance` makes) also passes
  `--no-show-signature`. The two untracked settings make every status
  report each untracked file whatever the repository's
  `status.showUntrackedFiles` says, read from the directories rather than
  from an untracked cache the agent could have written into the index, the
  status niwa runs before removing a worktree included;
- `--ignore-submodules=all` and `--untracked-files=all` on every
  `git status`. Without the first, git starts a
  status of its own inside every submodule and gitlink, and that child reads
  the submodule's own configuration, which can name a filter driver; the
  `-c` overrides reach the child, but no override switches off a filter. The
  walk already judges each submodule as its own repository, so the parent
  needn't look inside it.

The ancestry and merged-by-content checks that decide whether work is held
elsewhere run only with the scratch store as `--git-dir`, against the tip
ids `ls-remote` returned (Decision 1). They never read the unit's refs,
replace refs, grafts or commit-graph files, and never its objects: the
scratch store, created fresh under `~/.niwa/w-<key>/scratch/` where the
agent is denied writes and checked for owner and mode before each use, has
no `objects/info/alternates` and no refs, so the fetch
offers the proof remote no haves and brings the tip's whole history, and
the local side arrives as a pack built in the unit's repository by
`pack-objects` and read in by `index-pack`. Every object the ancestry walk
and `merge-tree` read is one git hashed from its content as it indexed a
pack, so an object the agent wrote under another commit's id can't stand
in for the commit the remote has.

`GIT_NO_LAZY_FETCH` needs git 2.45. With a git older than 2.45, a
repository whose configuration sets `extensions.partialClone` or a
`remote.<name>.promisor` keeps after the configuration read, before any call
that could read an object, with "partial clone needs git 2.45" as evidence.

Before any other call in a repository, niwa reads its effective settings with
`git config --list --show-scope --includes` under that environment and
under a 10-second context inside the pass's deadline, since `include.path`
can name any file, a FIFO included, and a read that hangs is killed and
keeps. This read is the first git call on every removal path, the hook
worktree sweep and `niwa destroy`'s scan included, and it runs on every
repository under the unit before the first other git call in any of them:
each clone and worktree and each submodule, gitlink and nested repository
under them, all found by niwa's own walk rather than by git, a submodule's
configuration under `.git/modules/` included. It isn't only the first call.
The invoker `gitCommand` builds runs the same read again on a repository,
under the same 10-second context, right before it starts any `git status`
there, because status is the call that can start a filter and seconds can
pass between the first read and it. The status niwa runs on a worktree
right before removing it is one of these; the removal that follows starts
no git process, and the read runs right before it too, so a key written in
between keeps the unit there as well. A key found
then keeps the unit with "changed during the pass: repository configuration
names a program: <key>" and the call isn't started. `ls-files -v`,
`for-each-ref`, `stash list`, `worktree list`, `diff-index --cached`,
`pack-objects` and `branch -d` start no program the repository names under the overrides, so
they need no second read. When a
key from any scope other than `command` names a program git might run
during the rule's calls, niwa runs nothing more there and
keeps the unit with "repository configuration names a program: <key>" as
evidence. That covers the `local` scope, the `worktree` scope
(`config.worktree` under `extensions.worktreeConfig`) and any file either
includes; only niwa's own `-c` values are exempt. The keys refused by name
are `filter.*.clean`, `filter.*.smudge`, `filter.*.process`,
`diff.external`, `diff.*.textconv`, `diff.*.command`, `merge.*.driver`,
`credential.*.helper`, `gpg.program`, `gpg.ssh.program`,
`gpg.x509.program`, and `core.fsmonitor` set to a hook rather than a
boolean. Beyond that list the rule is general: any key whose name says it
names a program (`*.program`, `*.command`, `*Command`, `*.helper`) is
refused when set outside the `command` scope. niwa matches section and key
names case-insensitively, as git reads them, and compares subsection names
as written. A submodule's key keeps the
unit exactly as a top-level repository's does. The `core.fsmonitor`
override already turns the hook off for every call; refusing it as well
means the submodule case doesn't depend on that override reaching a git process git itself starts.
A repository git refuses for ownership (`safe.directory`) keeps
with git's message; niwa never adds a `safe.directory` entry.

Network calls never run inside the unit's repository, and they go only to
proof remotes. niwa reads each remote URL from the setting listing, keeps
only those that are proof remotes (the clone URL the workspace configuration
gives that repository, any clone URL it gives for a repository it doesn't
name, or a match for a `proof_remotes` prefix; Decision 1),
validates them, and runs `ls-remote` and the fetch against the scratch
object store, whose configuration niwa writes, with a URL niwa builds
rather than the string the agent wrote: on a clone-URL match, the clone URL
the workspace configuration gives; on a `proof_remotes` prefix match, a URL
that takes its scheme, user, host and port from the prefix and only its
path from the remote's parsed, normalized parts. The unit's repository's
`insteadOf`, `uploadpack`, credential and proxy settings therefore never
apply to them, and neither do any written into the store: its
configuration is niwa's own, in a directory the agent is denied writes to
(Decision 9), and
niwa re-reads it before each network call and keeps the unit when it
differs by a byte from what niwa wrote (Decision 1). A remote the agent added is never contacted,
so it can't vouch for a commit, can't turn `ls-remote` into a request to an
internal address of the agent's choosing, and is never offered the person's
ssh keys. For a proof remote, niwa adds the person's own credential helpers,
read beforehand with `git config --global --get-all credential.helper`, as
`-c credential.helper=<value>` after the reset; no other helper runs.
Network calls also pass `-c http.followRedirects=false`, beside the fixed
`-c http.proxy=` and `-c http.sslVerify=true`. Git's default
(`initial`) follows a redirect on the first request of a call, which would
let a proof host hand the call to another host whose refs would then count
as the proof remote's. With redirects off, a proof remote that answers with
one fails the call, which reads as unreachable and keeps with git's message
as evidence; a repository that moved needs its clone URL or prefix updated
in the workspace configuration. That is simpler than following a redirect
and parsing git's warning text to check where it went, and it fails toward
keeping.

That read is the one named exception to `gitCommand`, because it has to see
the person's real global configuration, which `gitCommand` sets aside with
`GIT_CONFIG_GLOBAL=/dev/null`. It is bounded so it touches nothing an agent
wrote: it runs once per pass, before any unit is evaluated, with the same
absolute git and filtered `PATH`, `GIT_CONFIG_NOSYSTEM=1` and the rest of
the fixed environment except the global-file setting, from an empty
directory outside the workspace, under `~/.niwa/w-<key>/scratch/` and
checked for owner and mode before the read (Decision 1), with `GIT_CEILING_DIRECTORIES`
set so no repository is discovered, and under a 10-second context. It reads
values and runs no program. What it returns is used only as the
`-c credential.helper=` value on network calls to proof remotes, made
against the scratch store, and never on any call inside a unit's
repository. A helper the person scopes to a URL (`credential.<url>.helper`)
or sets only in an included file isn't found, so that remote reads as
unreachable and keeps.

#### Alternatives Considered

**Run git with the person's own global configuration and only the `-c`
overrides.** Private remotes would authenticate everywhere as they do in the
person's shell. Rejected because the global configuration varies by host and
can include files and settings niwa can't vet, so the rule's environment and
its results would differ between hosts, and because a helper the person
scopes broadly would hand a stored token to any host an agent-written URL
names.

**Override each driver the configuration names with an empty value instead
of refusing.** Repositories with a per-repository LFS install would stay
reapable. Rejected because whether an empty value disables a driver differs
between keys and git versions, and a driver missed in an include is code
execution; refusing fails toward keeping.

**No credentials for any network call.** The smallest trust surface.
Rejected because every unit cloned from a private `https` remote would then
read as unreachable and keep forever, which defeats the feature for most
private work.

**Contact every validated remote, giving credentials only to the clone
URL.** Forks would count without a setting. Rejected because a remote the
agent added can then advertise the agent's own commit and have it read as
pushed, and every such call is a request from the person's machine to a host
the agent picked.

**Read repositories with a Go git library instead of the git binary.** No
program runs at all. Rejected because it means a new dependency and
reimplementing status, worktree and `merge-tree` semantics the rule relies
on matching git exactly.

### Decision 9: Where niwa keeps the trusted copy of a unit's records

Resolved inline.

Three records loosen what the rule keeps: the mark (a reap candidate), the
discard record (unique paths become disposable) and a pid record (a live
process stops counting once the session stops). All three sit in the
instance, where the unit's agent can write them. A prompt-injected agent
could write a mark, a discard record covering its unpushed files and a pid
record for a live helper, and have its own work deleted at the next pass.
The question is how niwa tells a
record it wrote from one the agent wrote.

Key assumptions:
- The agent writes inside its instance in normal work; writing into
  `~/.niwa/` takes a deliberate step outside it, which sandbox write
  restrictions that also cover Bash (Claude Code's sandbox or an
  operating-system equivalent) can deny. A path deny rule in Claude Code's
  permission settings covers its file tools, not a shell command that
  writes the same path, so it can't deny that step on its own.
- Every writer of these three records can be a niwa command: the pass and
  `niwa retire` for the mark, `niwa discard`, and a starter that runs
  `niwa helper`, which runs in the agent's context, so the same sandbox
  confines it.

#### Chosen: a per-unit ledger under the workspace's host directory, and `niwa helper` for pid records

niwa keeps a ledger per unit at `~/.niwa/w-<key>/ledger/<instance>.json`
(schema `niwa.ledger/v1`: the instance name and creation time, and for each
record niwa wrote, its path under `.niwa/`, SHA-256, size, write time and
writer: over, retire, discard, helper or pass). Under the instance lock, niwa
writes the record to a temp file, records its ledger entry, and only then
renames the record into place, all atomically and 0600, so no reader ever
sees a record niwa wrote without its entry. A
mark, discard record, pid record or `unit.json` whose hash doesn't match its
entry is treated as niwa never wrote it: no mark, no discard record, no
helper, never reached. The records stay in the instance in the earlier
remover's forms, so anything reading that place still finds them; the ledger
only vouches. The pass removes the ledger file after `DestroyInstance`, and a
ledger whose instance is gone or whose creation time differs is ignored and
removed. `DestroyWorkspace` removes `ledger/` with the workspace, unlike
`kept-files/`, since a ledger has nothing to vouch for once its instances are
gone. `niwa helper` replaces hand-written pid records, with the checks in
Decision 4, so a pid record can only name a process its own caller started.
The ledger is only as good as the boundary around `~/.niwa/`, and so is
the scratch directory, `~/.niwa/w-<key>/scratch/`, where the scratch
object store and the empty directories `claude` and the credential-helper
read run from live (Decision 1), so
`docs/guides/ephemeral-session-instances.md` tells workspaces to deny the
agent writes to `~/.niwa/` with sandbox write restrictions that also cover
Bash (Claude Code's sandbox or an operating-system equivalent), and says
that this, not the ledger file, is the actual boundary. A path deny rule in
Claude Code's permission settings covers its file tools and not a shell
command that writes the same path, so the guide says deny rules alone
aren't the boundary, and that a worker with no such sandbox has none: there
the ledger is advisory, since the agent can write an entry as easily as a
record. The same advice
covers three places in the workspace root that also drive removal and
restore: `<root>/.niwa/`, whose `workspace.toml` holds the clone URLs, the
`proof_remotes` prefixes and the over command; `<root>/.niwa-launch/`; and
the `<root>/.niwa-tombstone-*` directories. It also covers the places
outside the workspace whose contents niwa runs: `~/.gitconfig` and
`~/.config/git/config`, whose `credential.helper` niwa reads and starts on
proof-remote calls (Decision 8), and the whole `~/.ssh/` directory, whose
`config` carries program settings ssh runs on those calls, together with
any file outside `~/.ssh/` that an `Include` in `~/.ssh/config` names.
ssh follows `Include`, so an included file's `ProxyCommand` or `Match exec`
runs as surely as one in `~/.ssh/config` itself, while the credential-helper
read doesn't follow git's includes, so the two git files are all it needs
there. All of them sit outside the agent's
working directory, but that says where they are, not what stops a write,
and the guide says so rather than leaving it implied. Under such a sandbox
`niwa helper` can't write the ledger either, since it runs in the agent's
context, so a helper the agent starts can't be registered and keeps its
unit; the guide says that too. Moving registration out of the agent's
context, so a sandboxed helper can be registered, is later hardening.

#### Alternatives Considered

**Move the discard and pid records out of the instance into host state.**
Only niwa would ever write them. Rejected because the earlier remover's form
places the discard record with the unit, and a host copy of the whole record
gains nothing over a hash of it while adding a second place to keep in step.

**Keep the hashes in the launch record.** One host store for each unit.
Rejected because not every unit has a launch record until phase 6 lands,
and the launch record sits under the workspace root, one `cd ..` from the
instance.

**Accept agent-written records.** Simplest, and the
rule still gates every reap. Rejected because the discard record isn't gated
by the rule; it is an input that tells the rule which paths don't matter.

## Decision Outcome

niwa gets one removal rule and one pass. The rule, `removal.Evaluate` in
`internal/removal`, reads a unit (its instance, its job directory, its
sessions and processes, and its records) and returns a verdict with every
keeping check in a fixed order and the class of every path. It runs on git
plumbing and local files, fetches only into a scratch object store outside
the unit, under `~/.niwa/w-<key>/scratch/`, and keeps on anything it can't
decide. The pass, `runPass` in
`internal/cli/pass.go`, gathers candidates where `reapWorkspace` gathers them
today and adds stopped units and marked units, gives every all-unit pass one
30-second deadline shared by everything inside it, takes units oldest-reached
first, and counts the ones it didn't start. `reapOpportunistically`,
`niwa reap`, `niwa retire`, failed-dispatch cleanup, the watch's discard, the
watch's failed-stage rollback and the hook worktree sweep all go through it
or through `removal.Evaluate`.

On top of the rule sits the seam. A workspace names its over command in
`[session.over]`; niwa runs it like the removal gate, in its own process
group under a 20-second default deadline, with the instance path in
`NIWA_REMOVE_PATH`, and reads exit 0 as over, 1 as not over and anything else
as unknown. On over, niwa writes `<instance>/.niwa/retired.json` in the
earlier workspace-level remover's format; after any other answer it removes
a mark written during the call. A mark is a statement, not a permission: the
marked unit is tested at every pass and reaped only when the rule passes it,
the same pass included. Later session activity or a moved branch head voids
a mark. A person retires a unit with `niwa retire`, which needs
`--override-not-over` only when the last answer was not over. An over command
that answers for a replaced unit reads koto's fence and
`koto/superseded-carry-complete` to know the successor finished taking over.

Removal is never forced. Once the rule passes a unit and the gate allows it,
niwa re-reads the sessions, processes and git state and keeps the unit when
any changed, a repository configuration that now names a program included.
The pass vets every session it will pass to `claude rm`, those in the
decoded `claude agents` listing, before its first removal step, as destroy
does, so a failing job directory keeps the unit with nothing moved or removed.
Otherwise it moves named and discarded files into the kept set, removes the
worktrees inside the instance itself, each only after its status comes back
clean, with the same descriptor-relative no-follow walk the trim uses and no
git call, runs
`claude rm` on each session without either override flag, only after a vet
of that session's job directory passes, runs on-reap,
removes the instance through `workspace.DestroyInstance`, and writes the
reap time and the window's end into the launch record, on every reap of a
unit that has one. A refusal from the
gate, from niwa or from Claude Code is a disagreement: the unit stays,
marked, and the listing names the step and its message. `niwa destroy` gains
the same worktrees-first order and the same vet, run on every mapped
session before its first removal step.

Each unit's records sit in its instance's `.niwa/`: the mark, `keep.json`
(holds, named files, build-tree protection and the handoff brief),
`discard.json` written by `niwa discard`, pid records in `pids/` written by
whatever starts a background helper, and `unit.json`, where the pass writes
the last answer, time stopped, size and verdict. `niwa list` and a fixed
dispatch line read only those, so the listing is local. The kept set lives at
`~/.niwa/w-<key>/kept-files/`, apart from the release store's `kept/`, with
due dates shown a day before entries leave. The SessionStart hook writes
launch records, so hook sessions can be reaped on a mark and restored, and
`niwa resume` refuses past `restore_until`, which defaults to Claude Code's
transcript retention; a `restore_window` the workspace sets can't be below
seven days. The launch-record sweep keeps a reaped unit's record until
`restore_until` has passed, even once its transcript is gone; a resume
still needs the transcript and refuses, naming it, without one, while the
kept files stay copyable from the kept set until they're due.

Everything in an instance is treated as written by an untrusted agent. Every
git call the pass, the hook worktree sweep and `niwa destroy` make goes
through one hardened builder that runs nothing the repository names and
reads no history override it carries, the read of the person's own
credential helper being the one named exception, and only a proof remote
(the workspace's clone URL, or a `proof_remotes` prefix matched on whole
path segments, a URL with dot segments, encoding or userinfo refused) can
show a commit is pushed or merged, judged in the scratch store only from
objects git hashed as they came in; no other remote is contacted. That
store, and the empty directories `claude` and the credential-helper read
run from, sit in a directory niwa owns inside the `~/.niwa/` boundary,
created fresh 0700 and checked for owner and mode before each use, and the
store's configuration is compared byte for byte with what niwa wrote
before each network call; any mismatch keeps. Each
worktree's HEAD and per-worktree refs are read again right before it's
removed, and the tombstone check compares a ref snapshot, so a late commit
keeps. A worktree goes by niwa's own no-follow walk from a directory
descriptor, never by git, so a directory swapped for a link can't send that
delete outside the instance. The mark, the discard record, pid records (now
registered through `niwa helper`) and `unit.json` count only when they match
a ledger niwa keeps at `~/.niwa/w-<key>/ledger/`. `niwa discard` and
`niwa retire --override-not-over` need a person at a controlling terminal
typing back a code niwa prints there, and discarded paths wait in the kept set until the
window ends. A file the brief claims to hold counts as handed forward only
when niwa verifies the brief's copy. Every recorded path is resolved without
following a symlink on every read, opened component by component when
moved, and validated again at restore, which never overwrites and never
writes under `.git` or `.claude` or outside the instance. The instance
is renamed to a tombstone under a root-level lock and checked once more
before it's deleted, and every string a unit supplies is scrubbed of
credentials and control sequences before niwa stores or prints it.

The decisions hold together because each one keeps a single owner for each
fact. The rule is in one package, so the verdict-only mode, a retire and the
pass can't disagree. The mark has one format and one writer path, so a mark
counts the same whoever wrote it and none survives an answer but over. Every
per-unit fact lives with the unit and goes with it, while the only host
state, the kept set, the ledger and the launch records, sits where the
location rule and `niwa resume` already look. The budget is one deadline, so it bounds a
dispatch without any step needing to know about the others. The accepted
cost is that niwa follows formats it doesn't own yet, so parts of this design
move when the earlier remover lands; the open dependencies say which.

## Solution Architecture

### Overview

```
niwa dispatch / create / watch ──> reapOpportunistically ─┐
niwa reap [--verdict-only]  ───────────────────────────────┼─> runPass(ctx 30s, root, mode, scope)
niwa retire <instance>  ──> write mark ──> runPass(one) ───┘        │
                                                                    │ per unit, oldest-reached first
                    ┌───────────────────────────────────────────────┤
                    v                                               v
      overSeam.ask  ([session.over])                  removal.Evaluate(unit, policy)
      exit 0 -> write retired.json                     -> verdict, checks, path classes
      exit 1 / other -> clear call's mark                         │
                    │                                              v
                    └──────────> unit.json  <──── act: keep | trim | reap
                                     │              (lock, gate, re-read, kept set,
                                     v               worktrees, claude rm, on_reap,
                         niwa list / dispatch line   tombstone + check, DestroyInstance,
                                                     ledger, launch record)
```

Failed-dispatch cleanup, the watch's discard and the watch's failed-stage
rollback call `removal.Evaluate` for their one instance before
`destroyInstanceFunc`; the hook worktree sweep evaluates a worktree's repository
checks before removing it. Both boxes write their record together with its
entry in the unit's ledger outside the instance, and every git call under
`removal.Evaluate`, the acting steps, the hook worktree sweep and
`niwa destroy` goes through `gitCommand`, with the program-naming
configuration check run on every repository under the unit, submodules and
nested repositories included, before the first other git call in any, and
again right before every status and unforced worktree removal, and
`--ignore-submodules=all` and `--untracked-files=all` on every status.

### Components

**`internal/removal` (new).** The rule, coded once.

- `rule.go`: `Evaluate`, the check order as constants (`CheckLiveness`,
  `CheckHold`, `CheckRecords`, `CheckRepository`, `CheckOutside`,
  `CheckNamedFiles`), and the `Verdict` and `Check` types.
- `walk.go`: the walk of the instance and job directory, repository
  discovery (`.git` entries, and git directories recognized by `HEAD`,
  `objects/` and `refs/`, bare ones and those under `modules/` included,
  inside build trees too), path classes, build-tree detection, generated
  paths, size; it
  `Lstat`s every entry, follows no symlink, stays on one filesystem, and
  keeps on a `.git` file whose git directory lies outside the unit, on a
  worktree entry it didn't reach (the main entry reached only at the work
  tree the walk found for that git directory) and on a git directory whose
  work tree lies outside the instance.
- `git.go`: `gitCommand`, the only builder of git commands, with the fixed
  environment and overrides of Decision 8, and a `GitInvoker` and a scan
  command builder made from it for the worktree and workspace packages; the
  person's credential-helper read, the one git call built outside it;
  `--ignore-submodules=all` and `--untracked-files=all` on every status it
  builds, and `--no-show-signature` on every log-family call; the program-naming
  configuration check, run on every repository the walk finds under a unit
  (submodule git directories, gitlinks and nested repositories included)
  before the first other git call in any, and run again by the invoker on a
  repository right before every `git status` there and before each
  worktree removal niwa makes; the proof-remote match against the
  workspace's clone URL and `proof_remotes`, on scheme, user, host and port
  (default ports made explicit) and whole path segments; remote
  URL validation, refusing dot segments, empty segments, percent-encoding,
  backslashes, control characters and userinfo;
  per-repository checks, held by a proof remote's live refs (a branch by
  its tip commit, a tag only by the remote listing the same name under
  `refs/tags/` at the same object, else kept as local-only), merged by
  content, both run only in the scratch object store (no alternates, the
  tip's history fetched, the local side read in through `index-pack`), with
  its 1 GiB cap and cleanup, 10-second network contexts; the scratch
  directory under `~/.niwa/w-<key>/scratch/`, made fresh 0700 each pass,
  with the owner and mode check on it and its parent before each use and
  before a leftover is removed, the store's `config` written by niwa and
  compared byte for byte before each network call, and the empty
  directories `claude` and the credential-helper read run from; the ref snapshot
  the tombstone check compares. The filtered `PATH` used to resolve
  git, `lsof` and `claude`.
- `claude.go`: the job-directory vet (Data Flow step 7), which
  `realRemoveClaudeSession` in `internal/cli/destroy_session.go`, the
  function `removeClaudeSessionFunc` points to and the only place niwa
  issues `claude rm`, runs on that session's own `~/.claude/jobs/<id>`
  before each call, making no call when the vet fails. That function runs
  the call from the checked empty directory with the filtered `PATH`, and
  every `claude rm` niwa makes goes through it: the pass,
  `removeOwningSessionsListed` in `niwa destroy`, and retask's `removeCopy`.
- `paths.go`: `ResolveRecorded(base, path)`, the read-time check every
  recorded path passes (relative, no `..`, `job:` bound to the unit's job
  directory, `Lstat` on each component, no symlink, one device).
- `scrub.go`: `ScrubEvidence`, which strips URL userinfo, known token shapes,
  secret-named `key=value` pairs and long mixed-case base64 runs, removes
  control sequences, and only then bounds length.
- `patterns.go`: the shipped list and the glob matcher with `**`.
- `records.go`: readers and writers for `retired.json`, `keep.json`,
  `discard.json`, `pids/`, and `unit.json`, with the validity rules each
  carries, size caps, `O_NOFOLLOW|O_NONBLOCK` opens `fstat`ed as regular
  files before any read, strict decoding, 0600 writes and
  the ledger match, all relative to an `O_DIRECTORY|O_NOFOLLOW` descriptor
  for `.niwa` and `pids`.
- `procs.go`: the per-pass process listing (`/proc` on Linux, `lsof` on
  macOS, resolved by absolute path like git) and attribution to instances and
  build trees, pid records applied; process ancestry for the instance guard.
- `brief.go`: the handoff brief's freshness test (later of mtime and ctime)
  and the `in_brief` check, which hashes the brief's `stored_at` copy
  through an `O_NOFOLLOW|O_NONBLOCK` descriptor `fstat`ed as a regular file
  and counts the file as unique unless it matches.
- `keptmove.go`: the kept-set move, opening each source component by
  component with `O_NOFOLLOW` (the file also `O_NONBLOCK`), `fstat`ing the
  descriptor before reading (a regular file, one link only)
  and hashing it, and moving named files to `files/` and discarded paths to `discarded/`.
- `rmtree.go`: the descriptor-relative no-follow removal walk the trim and
  every worktree removal share (Data Flow steps 7 and 8), which `fstat`s
  every directory it opens and stops at one on another device, and
  `RemoveWorktree`, which checks from descriptors that a worktree's
  top-level `.git` file names its admin directory, keeps a locked worktree
  or one holding a repository, and removes the tree and then the admin
  directory. The worktree package gets it as a `WorktreeRemover` beside its
  `GitInvoker`, so that package stays a leaf and imports nothing from this
  one.

**`internal/cli/pass.go` (new).** `runPass` with three modes (act on every
unit, act on one, verdict-only), the deadline, the ordering, candidate
gathering (moved out of `selectReapTargets`, `selectStoredMappingTargets`
and `selectBackstopTargets`), the acting steps (`actReap`, `actTrim`), the
re-read before removal, the read of each worktree's HEAD and per-worktree
refs, its configuration check and its status right before niwa removes it,
the removal of worktrees and of trimmed trees through `rmtree.go`'s
no-follow walk, which stops, and keeps the rest of the tree, at any `.git`
entry or git directory it meets other than a worktree's own top-level
`.git` file, the `claude rm` of each session through
`realRemoveClaudeSession` and its job-directory vet, the tombstone rename
and check before
`DestroyInstance` under the shared tombstone lock, the recovery of
tombstones an interrupted pass left under that lock taken exclusively, and
the counts the dispatch line and reap print.
`reapWorkspace` becomes a thin call into it; `sweepLaunchRecords` and
`sweepHookWorktrees` stay as steps of the pass. `sweepLaunchRecords`
(`internal/cli/launch_records.go`) changes in one way: a record that
carries `reaped_at` is kept until its `restore_until` has passed, whether
or not its transcript still exists, and only after that does today's rule,
which removes a record whose transcript is gone, apply to it (Decision 5).
Every other record is swept as today. `sweepHookWorktrees`
(`internal/cli/reap_worktrees.go`) runs the removal walk over each worktree
and the program-naming configuration check on every repository it finds
there, the worktree's own and each submodule, gitlink and nested repository
under it, before any other git call in any of them; a flagged one keeps the
worktree. It then calls `DestroySession` with force off, the `gitCommand`
invoker, which runs the check again before that call's status and before
its removal, and the `rmtree.go` remover, which does the removal. The sweep
records the worktree's HEAD and per-worktree refs with its evaluation, and
the invoker reads them again right before the removal; any change keeps the
worktree with "changed during the pass: ref <name>".

**`internal/worktree/worktree.go`.** `DestroySession` already takes a
`GitInvoker`, so its `git status --porcelain` and `git branch -d` calls run
hardened once the sweep passes the `gitCommand` invoker instead of
`StdGitInvoker`, which the reap-time sweep in reap_worktrees.go no longer
uses anywhere. It takes a `WorktreeRemover` beside the invoker and removes
the worktree through it before `git branch -d`. The sweep passes the
`rmtree.go` remover, so on the sweep's path `DestroySession` makes no
`git worktree remove` call, forced or not: the remover takes the worktree's
tree and admin directory, and `git branch -d` then finds the branch checked
out nowhere. A removal the `rmtree.go` remover stops comes back as an error
naming the path, where today the result of `git worktree remove` is
discarded. The other callers, the hook teardown (`teardownHookSession`,
internal/cli/session_from_hook_cmd.go:376), the failed-hook reconcile
(`reconcileFailedHookCreate`, session_from_hook_cmd.go:254), the person-run
worktree destroy (`destroyOneWorktree`,
internal/cli/session_lifecycle_cmd.go:663) and `destroySessionWorktrees`
(internal/cli/worktree_destroy_resolve.go:404), keep today's remover and
invoker: they pass `StdGitInvoker` and a remover that runs today's
`git worktree remove --force` through it with its result discarded, so what
they do doesn't change. The flags and the check belong to the sweep's
path alone. `worktreeHasUncommittedChanges` keeps building
`status --porcelain` for every caller, and the sweep's `gitCommand`
invoker, which receives that call's arguments, adds
`--ignore-submodules=all` and `--untracked-files=all` when it builds the
status, and runs the program-naming configuration check on the worktree's
repository right before that status and before the removal, returning an
error that names the key, which keeps the worktree, when it finds one. The
other callers' status stays `git status --porcelain` as today, with no
added flags and no configuration check, since `StdGitInvoker` builds the
call as it's given. With
force off, a change the status reports keeps the worktree, as it does
today; with force on, the status is skipped, as it is today.

**`internal/cli/over.go` (new).** `overSeam`, built like `removalGate`:
`load`, `active`, `ask`, `exec`, with output capture, last-line cleaning, the
per-run timeout, process-group kill, and the mark clean-up after a non-zero
answer, run under the instance lock.

**`internal/cli/retire.go`, `internal/cli/discard.go` (new).** The two
person-facing commands, both refusing when `insideInstance` answers yes, and
both writing their record's ledger entry. `niwa discard`, and `niwa retire`
with `--override-not-over`, call `confirmOnTerminal` in a new
`internal/cli/confirm.go`, which opens `/dev/tty`, refuses without one,
prints what the command will release and a random eight-character code
drawn fresh from `crypto/rand` for this call, and requires that code typed
back on that terminal. The code replaces the instance name, which the
agent already knows: a tool-run process that inherited the person's
terminal can push input into it (`TIOCSTI` on macOS). The code stops a
process that can only type into the terminal. A process running as the
person that can read the screen, through `tmux`, `screen` or terminal
scripting, can answer it, which is why discarded paths wait in the kept set
until the window ends.

**`internal/cli/helper.go` (new).** `niwa helper <pid> --purpose <text>`:
checks the process is the caller's child inside the instance and not a
session, writes `pids/<pid>.json` from the operating system's view of the
process, and records it in the ledger.

**`internal/cli/guard.go` (new).** `insideInstance`, the advisory check by
working directory, `NIWA_DISPATCH_ID` and `NIWA_REAP_KEY`, and process
ancestry.

**`internal/cli/reap.go`.** Gains `--verdict-only`, `--json` and an optional
instance argument allowed only with `--verdict-only`; prints the reaped,
trimmed, kept and not-reached counts.

**`internal/cli/list.go`.** Reads `unit.json`, `keep.json` and the mark for
each record (`annotateFromUnitRecords` beside `annotateFromSessionMappings`),
checks each against the ledger, passes every stored string through
`ScrubEvidence` again on read, prints the state line and the kept-set
summary, adds `--kept`, and adds the `unit` object to
`workspace.InstanceRecord` (state.go:470-511).

**`internal/cli/dispatch.go`.** Prints the dispatch line after
`reapOpportunistically` at step (5).

**`internal/cli/destroy.go`, `destroy_session.go`.** `runDestroyInstance`
removes worktrees inside the instance after the scan and before
`removeOwningSessionsListed`, whose `claude` calls run from an empty
directory under `~/.niwa/w-<key>/scratch/`, checked like the pass's, and
whose `claude rm` goes through `realRemoveClaudeSession`, the function
`removeClaudeSessionFunc` points to, which runs the job-directory vet and
makes the call from the checked empty directory with the filtered `PATH`.
Before its first removal step, destroy runs that job-directory
vet on the job directory of every mapped session it will pass to
`claude rm`, those in the decoded `claude agents` listing; on any failure it stops with
the failing path named and removes nothing, so the instance, its worktrees
and every session stay, the same fail-toward-keep as the pass.
Destroy's existing `--keep-session` flag, which keeps the owning session, is
unchanged: with it destroy passes no session to `claude rm`, so it vets no
job directory. It removes each worktree with `rmtree.go`'s walk, never through git. Unforced
destroy first runs the worktree's configuration check and `git status` with
the scan, before any prompt, and a change it reports, or a locked worktree or
one holding a repository that the walk keeps, joins the scan's findings and is
handled the way destroy handles those today (destroy.go:213-232): on a
terminal destroy lists it with the rest and asks for the instance name, going
ahead only on a match, and when stdin isn't a terminal it aborts with nothing
removed. Once the name matches, a worktree the walk won't remove is left to go
with the directory, as under `--force`. The status that comes right before
each worktree's removal only checks that nothing changed since that listing; a
change the listing didn't show stops the destroy before that worktree goes,
with the path named, since no confirmation covered it.
`--force` keeps its meaning: it skips that status and removes the worktrees
with the same walk before the session, since the person already accepted
the loss, and a worktree the walk won't remove (locked, holding a
repository, a `.git` file that doesn't name its admin directory, a link
where a directory should be) is left to go with the directory, as a flagged
one is. `--force` doesn't skip the job-directory vet, since what it guards
lies outside the unit, so a forced destroy stops on a failing job directory
the same way. That status goes through `removal.gitCommand`, and so does
every git call destroy's existing scan makes: `runDestroyInstance` passes
`workspace.ScanInstance` a `ScanOption` carrying the command builder made
from `gitCommand`, and `gitOutput` in `internal/workspace/scan.go`, with
`gitNetwork` and `gitOutputEnv` in `internal/workspace/durable.go` for the
scan's durability check, start git only through it, so the workspace
package doesn't import the removal package. The scan's own `git status` call
(`scanStatus`, scan.go:366) gains `--ignore-submodules=all` and
`--untracked-files=all`, like every status niwa runs, so it no longer
recurses into a submodule on its own. A person's destroy must still count a
modified submodule as loss, as it does today, since destroy keeps its own
checks and gains only the worktree order: the scan therefore runs the same
status in each submodule and nested repository the walk below hands it,
after that repository's configuration check, and a change in one is loss
the way a change in the parent is. `ScanInstance`
finds no submodules or nested repositories of its own, so before the scan's first git call, and before
`--force` removes a worktree, `runDestroyInstance` runs the removal walk
over the instance and the program-naming configuration check on every
repository it finds, submodules, gitlinks and nested repositories included.
A repository it flags gets no further git call: the scan reports it as a
`LossScanError` naming the key, the way it already reports a repository it
can't read, so an unforced destroy lists it as loss and asks for the instance
name on a terminal, going ahead only on a match, and aborts when stdin isn't
a terminal, as for any other loss; once confirmed, and under `--force`, the
worktree holding that repository goes with the directory. The `gitCommand`
builder runs the check again right before the scan's `git status` in each
repository and before the status that comes right before each unforced
worktree removal. A key found at the scan's status is listed as loss the
same way; one first found at the status before a worktree's removal, after
the confirmation, stops the destroy before that worktree goes, with the key
named; `--force` removal skips that status, so
it starts no git process and needs no second read. What the scan checks and decides is otherwise left as it is;
only how it starts git changes. `niwa destroy --workspace`
(`runDestroyWorkspace`, destroy.go:429) goes through the same scan, order
and vet: it scans every instance through `workspace.ScanInstancesParallel`,
which calls `ScanInstance` for each and is given the same `ScanOption`
after the same walk and configuration check. Those findings are listed with
the rest of every instance's scan and handled as the scan's findings are
there today (from destroy.go:429): without `--confirm`, on a terminal it asks
for the workspace name and goes ahead only on a match, and when stdin isn't a
terminal it aborts; with `--confirm <name>` matching the workspace name it
goes ahead with no prompt; and it takes no `--force`. It removes each
instance's worktrees as an unforced destroy does, after the confirmation and before
`removeOwningSessionsListed`, with the job-directory vet of every listed
session before its first removal step. Today it runs `claude rm` there
(destroy.go:525) before `DestroyWorkspace` removes the instance directories
with their worktrees still inside.

**`internal/cli/instance_from_hook.go`.** Writes the hook session's launch
record and the mapping's `launch_id`.

**`internal/cli/resume.go`.** Decodes the launch record strictly and ignores
its replay fields when `origin` is `hook`. `validateLaunchRecord` accepts a
hook-shaped instance name (`<config>-<8 hex>-<3 hex>`, Decision 5) when the
record's `origin` is `hook`, and a dispatch-shaped one otherwise, as today.
The session mapping `runResume` writes follows the record's origin
(Decision 5): for a hook record, the hook's shape, with no origin, no
`DispatchID` and `launch_id` set, so `niwa authority check` answers
`not-dispatched` for the restored session; for a dispatch record, today's
dispatch shape with `launch_id` added.
It checks `restore_until`, checks that a present
`kept_entry` is a direct descendant of `~/.niwa/w-<key>/kept-files/<instance>/`
reached with no symlink, refusing one present but empty (the field is a
`*string`, nil when absent), and for a record with no `kept_entry` moves
no files and starts the session. It re-validates every path in `entry.json` and the
rewritten keep record with `ResolveRecorded`, which refuses a destination
outside the instance, and also refuses, naming the file, any destination with
a path component named `.git` or `.claude` (compared case-insensitively). It
moves kept-set files back with a hash check, creating parents with
`O_DIRECTORY|O_NOFOLLOW` and files with `O_CREAT|O_EXCL|O_NOFOLLOW`, and
clears nothing else. Its `rollback`, which calls `destroyInstanceFunc` on the
fresh instance when a later step fails, first moves every restored file back
into the entry unchanged by hash, and leaves the instance in place, naming the
file, when one can't be. It writes `restored_at` into the fresh instance's
`unit.json` the way niwa writes every record, recording its ledger entry
before the rename into place.

**`internal/workspace/destroy.go`, `tombstone.go` (new), `ledger_store.go`
(new).** `DestroyInstance` `Lstat`s the instance directory, refuses a symlink
and any path that isn't a direct child of the resolved root.
`TombstoneInstance` and `UntombstoneInstance` do the tombstone rename of
Decision 1, with the tombstone lock at
`~/.niwa/locks/t-<workspace key>.lock`.
`LedgerStore` reads and writes `~/.niwa/w-<key>/ledger/<instance>.json`
atomically with mode 0600 and removes an entry after its instance is
destroyed.

**`internal/workspace/launch_store.go`.** `LaunchRecord` gains optional
`Origin`, `ReapedAt`, `RestoreUntil` and `KeptEntry`, with `KeptEntry` a
`*string` (`json:"kept_entry,omitempty"`) so a record without the field
decodes to nil and one holding an empty string stays distinguishable from
it; the schema name stays
`niwa.launch/v1` since every field added is optional.

**`internal/workspace` (apply lock).** Root `niwa apply` holds an exclusive
lock at `~/.niwa/locks/a-<workspace key>.lock`; the trim tries it without
blocking and skips trimming when it's held.

**`internal/config/session.go`, `config.go`.** `SessionOverConfig`,
`SessionRemovalConfig` (with its enums, durations, the seven-day floor and
pattern validation), and `RepoOverride.BuildTrees`.

**Docs and tests.** `docs/guides/over-command.md` (the seam contract and the
takeover key), updates to `docs/guides/ephemeral-session-instances.md`, and
`@critical` functional scenarios in a new
`test/functional/features/over-remover.feature` plus the held-unit scenario
in `ephemeral-session.feature`.

### Key Interfaces

Configuration:

```toml
[session.over]
command = 'exec my-over-check "$NIWA_REMOVE_PATH"'
timeout = "20s"
env = ["KOTO_HOME"]

[session.removal]
ignored_files = "disposable"   # or "unique"
job_scratch = "disposable"     # or "unique"; set "unique" with an over command for shirabe's workflows
trim = true
trim_after = "24h"
restore_window = "14d"         # unset: Claude Code's cleanupPeriodDays; a value set here can't be below 7d
disposable = [
  { pattern = "*.orig", review = "https://github.com/acme/app/pull/12" },
]
proof_remotes = ["https://github.com/dana/"]   # empty by default: only each repository's clone URL counts

[repos.app]
build_trees = ["dist/"]
```

The over command's contract: run as `/bin/sh -c <command>` from the
workspace root with `PATH`, `HOME`, `NIWA_REMOVE_PATH` and
`NIWA_SESSION_IDS` set; exit 0 over, 1 not over, anything else unknown; the
last non-empty line of standard output is the reason; on the deadline the
process group gets `SIGTERM`, then `SIGKILL` two seconds later. The guide
tells command authors that the instance's contents are agent-written and
untrusted: read them as data, and never `cd` into the instance to run `git`,
`make` or a script from it, or source a file from it.

Per-unit files under `<instance>/.niwa/`:

```
retired.json          the retire mark, the earlier remover's format
keep.json             keep record and handoff brief
discard.json          discard record (niwa discard)
pids/<pid>.json       pid records (niwa.pid/v1, written by niwa helper)
unit.json             the unit record (niwa.unit/v1), written by the pass
```

Every file here is agent-writable and read as untrusted: opened
`O_NOFOLLOW|O_NONBLOCK` and `fstat`ed as a regular file owned by the user
before it's read, size-capped, strictly decoded, every path in
it through `ResolveRecorded`, and the mark, `discard.json`, pid records and
`unit.json` valid only with a matching ledger entry. `.niwa` and `pids` must
be real directories, opened with `O_DIRECTORY|O_NOFOLLOW`.

```json
{"schema":"niwa.keep/v1","instance":"tsuku+bench-77c10d2e","owner":"dana",
 "reason":"benchmark results","written":"2026-11-20","written_at":"2026-11-20T14:02:11Z",
 "review":"2026-11-21","protects":["files"],
 "files":[{"path":"bench/results.csv","sha256":"9f2c...","bytes":20480,"in_brief":false},
          {"path":"notes/plan.md","sha256":"77d0...","bytes":3120,"in_brief":true,
           "stored_at":"/home/dana/.briefs/tsuku+bench-77c10d2e/plan.md"}],
 "brief":{"written_at":"2026-11-20T14:02:11Z"}}

{"schema":"niwa.discard/v1","instance":"tsuku+bench-77c10d2e","owner":"dana",
 "paths":[{"path":"notes.txt","written_at":"2026-11-20T14:05:00Z"}],
 "patch":{"path":"discarded.patch","sha256":"41ab..."}}

{"schema":"niwa.pid/v1","pid":48211,"started":"2026-11-20T13:10:04Z",
 "command":"koto","purpose":"watch for review comments","written_at":"2026-11-20T13:10:04Z"}

{"schema":"niwa.unit/v1","answer":"not_over","exit":1,"reason":"pull request open",
 "asked_at":"2026-11-20T15:00:02Z","stopped_since":"2026-11-17T10:41:00Z",
 "size_bytes":2254857830,"size_measured_at":"2026-11-20T15:00:03Z",
 "verdict":"keep","kept_by":{"check":"liveness","paths":[],"evidence":""},
 "disagreement":null,"mark_voided":null,"reached_at":"2026-11-20T15:00:03Z",
 "restored_at":null}
```

Host files:

```
~/.niwa/locks/a-<workspace key>.lock                 root apply lock
~/.niwa/locks/k-<workspace key>.lock                 kept-set lock
~/.niwa/locks/t-<workspace key>.lock                 tombstone lock (shared to act, exclusive to recover)
~/.niwa/w-<workspace key>/kept-files/<instance>/<UTC>/   0700
    files/<in-unit path>                             named files, unchanged, no setuid bits
    discarded/<in-unit path>                         paths a discard record released, due at window end
    keep.json                                        paths rewritten, 0600
    entry.json                                       niwa.kept/v1: due and first_shown_due (files/),
                                                     discarded_due and discarded_first_shown_due
                                                     (discarded/), files; 0600
~/.niwa/w-<workspace key>/ledger/<instance>.json     niwa.ledger/v1, 0600
~/.niwa/w-<workspace key>/scratch/<pass>/            0700, made fresh each pass; owner and mode of it
                                                     and its parent checked before each use
    store-<n>/                                       scratch object store, bare, config written by niwa
    claude/                                          empty; every claude call runs from it
    helper-read/                                     empty; the credential-helper read runs from it
<root>/.niwa-launch/<launch id>.json                 niwa.launch/v1 + origin, reaped_at,
                                                     restore_until, kept_entry
<root>/.niwa-tombstone-<instance>-<8 hex>/           an instance between its last check and delete
```

```json
{"schema":"niwa.ledger/v1","instance":"tsuku+bench-77c10d2e",
 "instance_created":"2026-11-20T09:12:40Z",
 "records":{"retired.json":{"sha256":"c03e...","bytes":612,"written_at":"2026-11-20T15:00:02Z","writer":"over"},
            "discard.json":{"sha256":"41ab...","bytes":233,"written_at":"2026-11-20T14:05:00Z","writer":"discard"}}}
```

Every git call niwa's removal makes (Decision 8), in the pass, the hook
worktree sweep and `niwa destroy`:

```
env (nothing else inherited):
  PATH=<pass PATH, empty and relative entries and any resolving into the root dropped>
  HOME=<home>  LANG=C
  GIT_CONFIG_NOSYSTEM=1  GIT_CONFIG_GLOBAL=/dev/null
  GIT_TERMINAL_PROMPT=0  GIT_ASKPASS=
  GIT_ALLOW_PROTOCOL=https:ssh
  GIT_SSH_COMMAND="ssh -o BatchMode=yes -o ForwardAgent=no"
  GIT_PAGER=cat  GIT_OPTIONAL_LOCKS=0  GIT_NO_LAZY_FETCH=1
  GIT_NO_REPLACE_OBJECTS=1  GIT_GRAFT_FILE=/dev/null
  GIT_CEILING_DIRECTORIES=<workspace root>
args:
  /abs/git --no-pager --git-dir=<from walk> --work-tree=<from walk>
    -c core.fsmonitor=false -c core.hooksPath=/dev/null -c core.pager=cat
    -c credential.helper= -c submodule.recurse=false -c fetch.recurseSubmodules=false
    -c core.useReplaceRefs=false -c core.commitGraph=false
    -c log.showSignature=false -c status.showUntrackedFiles=all -c core.untrackedCache=false
    -c http.proxy= -c http.sslVerify=true
    -c core.trustctime=true -c core.checkStat=default
    -c core.excludesFile= (only when the repository's own configuration names none)
  every status: --ignore-submodules=all --untracked-files=all
  every log-family call (stash list, git log): --no-show-signature
  worktree removal: no git call. Unforced, first the configuration check and git status
    on the worktree (any entry keeps); then niwa's no-follow walk removes the tree from
    the instance's descriptor and worktrees/<id> from the common git directory's, the
    tree only one niwa's walk found (never a path from git worktree list or gitdir);
    git worktree remove and git worktree prune are never run
index flags: ls-files -v in every repository with a work tree; a lowercase tag
  (assume-unchanged) or S (skip-worktree) on any entry keeps
first call in each repository: git config --list --show-scope --includes, 10-second context,
  run on every repository the walk finds under the unit (submodules, gitlinks, nested
  repositories, bare repositories and git directories under modules/, build trees
  included) before the first other git call in any
network calls (ls-remote, fetch): proof remotes only (the workspace's clone URL or a
  proof_remotes prefix matched on whole path segments), --git-dir=<scratch store>,
  a validated URL niwa builds (the configured clone URL on a clone-URL match, or one
  rebuilt from the remote's parsed, normalized parts on a prefix match; never the
  agent-written string; no dot or empty segment, percent-encoding, backslash,
  control character, or userinfo beyond the ssh user git),
  -c credential.helper=<person's helper>, -c http.followRedirects=false (a redirect
  fails the call, which keeps); fetch killed past 1 GiB, store removed on exit;
  before each one the store's config is re-read and compared byte for byte with
  what niwa wrote, and any difference keeps
scratch store: under ~/.niwa/w-<key>/scratch/<pass>/, bare, 0700, its directory and
  parent Lstat'ed for owner uid and mode before each use, and no component from below
  the home directory down to it a link, each owned by the person's uid (a mismatch
  keeps), config written by niwa, no objects/info/alternates, no refs; before each use
  no commondir, objects/info/alternates or objects/info/http-alternates in the store,
  none of which niwa writes (any keeps); the fetch offers no haves
  and brings the tip's whole history; the local side comes in as
  pack-objects --revs --stdout (in the unit) read by index-pack --stdin (in the store)
ancestry (merge-base --is-ancestor) and merge-tree --write-tree: --git-dir=<scratch store>
  only, against the tip ids ls-remote returned; every object read came through
  index-pack; tree objects written there; merged by content needs merge-tree's
  exit 0 and its tree equal to the default branch's
other remotes: never contacted; a commit only they could hold keeps ("remote not recognized")
keep without running: from any scope but command (local, worktree, any included file)
  a filter.*.{clean,smudge,process}, diff.external, diff.*.{textconv,command},
  merge.*.driver, credential.*.helper, gpg.program, gpg.ssh.program, gpg.x509.program,
  core.fsmonitor naming a hook, or any other *.program, *.command, *Command or *.helper
  key, in any repository under the unit, a submodule included
the one call outside gitCommand: git config --global --get-all credential.helper,
  once per pass, from an empty directory under ~/.niwa/w-<key>/scratch/<pass>/,
  checked for owner and mode first, 10-second context
```

Commands and outputs:

```
niwa retire <instance> [--override-not-over]
  niwa: retired <name> and removed it
  niwa: retired <name>; kept by <check>: <paths>
  niwa: cannot retire <name>: its session is working
  niwa: cannot retire <name>: its over command answered not over (<reason>); pass --override-not-over to retire it anyway
  niwa: cannot retire from inside an instance; run it from a shell outside the workspace's instances
  niwa: cannot override for <name>: no controlling terminal
  (with --override-not-over, on the terminal) type <code> to retire <name> although its over command answered not over:

niwa discard <instance> <path>... [--patch <file>]
  niwa: cannot discard from inside an instance; run it from a shell outside the workspace's instances
  niwa: cannot discard for <name>: no controlling terminal
  (on the terminal) <path> <size> ... these move to the kept set at reap and leave on <window end>; type <code> to confirm discarding from <name>:
niwa helper <pid> --purpose <text>
  niwa: cannot record <pid>: it is not a child of the calling process
niwa reap [--verdict-only [<instance>] [--json]]
niwa list [--json] [--kept]
niwa resume <target>
  niwa: cannot resume <name>: its restore window ended on 2026-12-20

dispatch line:
  niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set entr(y|ies) due; <U> unit[s] not reached (niwa list)
```

Go entry points:

```go
package removal

func Evaluate(ctx context.Context, u Unit, p Policy) Verdict
func ResolveRecorded(base Base, path string) (string, error) // Base: the instance or the unit's job directory
func ScrubEvidence(s string, max int) string

type Verdict struct {
    Kind   Kind      // Reap, Keep, KeepAndTrim
    Checks []Check   // every keeping check, in rule order
    Paths  []PathClass
    Trim   []string  // build trees the trim would remove
    SizeBytes int64
}

package cli

func runPass(ctx context.Context, root string, mode passMode, scope passScope) passResult
```

### Data Flow

An all-unit pass, before a dispatch:

1. Load the workspace configuration (`loadWorkspaceConfig`) and build the
   policy: settings, patterns, build trees, the over seam and the gate. A load
   error ends the pass with nothing removed; a `restore_window` under seven
   days fails the command before the pass.
2. Start the 30-second deadline. Resolve git, `lsof` and `claude` to
   absolute paths from the filtered `PATH`. Create the pass's scratch
   directory under `~/.niwa/w-<key>/scratch/`, 0700, with its empty
   directories. Read `claude agents --json
   --all` once, run like every `claude` call from the empty directory there,
   after the owner and mode check. A `claude agents` call that fails, times
   out, exits non-zero or prints output that doesn't decode strictly is
   undecidable and keeps every unit, as a failed process listing does; only
   a listing that decodes counts a session as gone. Then read the job entries, the session mappings, the launch records,
   the ledgers and one process listing. Note each repository's clone URL and
   the `proof_remotes` prefixes from the configuration loaded in step 1.
   Remove scratch stores an interrupted pass left, each only after the same
   owner and mode check.
3. Enumerate units: every instance, with its sessions and job directories.
   If the tombstone lock can be taken exclusively without waiting, a
   tombstone left by an interrupted pass is renamed back when its name is
   free, or kept and listed; if not, recovery waits for a later pass. Sort by
   `reached_at` in a ledger-matched `unit.json`, never-reached first.
4. For each unit while the deadline allows: try its instance lock without
   blocking (held means skip it this pass, no failure). Check that `.niwa`
   and `pids` are real directories and open them without following links.
   Read its records as untrusted input, each size-capped, checked against
   the ledger, and every path in them through `ResolveRecorded`. Decide
   liveness. A working unit is noted as working and left alone.
5. For a stopped unit with no valid mark and no hold, other than a restored
   unit whose session hasn't worked since `restored_at` (R32), and with an
   over command configured, ask the seam at every pass if the remaining
   budget covers its timeout; an earlier answer of not over or unknown
   doesn't stop the question. Exit 0 writes the mark and its ledger entry;
   any other outcome clears a mark written during the call, still under the
   instance lock. Store the answer, scrubbed.
6. Run `removal.Evaluate`, every git call built by `gitCommand`, the
   configuration check run on every repository the walk found (submodules
   included) before any other git call and again right before each
   repository's `git status`, since the network calls and local pack before
   it can take seconds, and every
   network call made only to a proof remote whose URL passed validation and
   matched the clone URL or a `proof_remotes` prefix on whole path segments.
   Every git output is read in its NUL-terminated form
   (`git worktree list --porcelain -z` among them), and a record that
   doesn't parse keeps. A worktree git lists that the walk didn't reach, by
   a `.git` file at its path naming its admin directory (for the main
   entry, by its path being the work tree the walk found), keeps with
   "worktree outside the instance: <path>"; a prunable one is judged by its
   HEAD, per-worktree refs and index read from its admin directory, and a
   failed read keeps. A git directory found by its structure whose work
   tree sits outside the instance keeps with "work tree outside the
   instance: <path>".
   A commit that is itself a live tip is held; any other is judged in a
   fresh scratch store with no alternates, under the pass's scratch
   directory and checked for owner and mode before each use, into which the
   proof remote's tip history is fetched and the local side read through
   `index-pack`; before each network call the store's config is compared
   byte for byte with what niwa wrote, and any mismatch keeps. A
   commit no proof remote holds keeps, and an `in_brief` file whose brief
   copy doesn't verify is unique.
   A unit is a reap candidate when it carries a valid
   mark, when its session is gone (the delete-then-reap path, which needs no
   mark and no launch record), or when the backstop finds it unmapped.
   Anything else that's stopped and has idle build trees is a trim candidate.
7. For a reap: ask the gate (step names as today: `reap`, `reap-backstop`,
   and the new `retire`), re-read sessions, processes and git state (each
   repository's status and index-flag read, the status preceded by a fresh
   configuration check, and, for a prunable worktree, its
   `git diff-index --cached` read from its admin directory), take
   each repository's ref snapshot, and keep with "changed during the pass"
   when anything differs or a key is flagged. niwa also re-hashes the
   brief copy of every `in_brief` file the rule judged handed forward,
   opening it as the rule does, before removing that file; a missing copy or a
   hash mismatch makes the file unique, and it keeps the unit. The pass
   vets every session it will pass to `claude rm`, those in the decoded
   `claude agents` listing,
   before its first removal step, as destroy does: before the kept-set move
   and the worktree removal, and a failing vet keeps the unit with nothing
   moved or removed; the vet right before each `claude rm` still runs. Then
   move named
   files, discarded paths and a discard patch into the kept set (regular
   files only, one link, opened component by component, hashed from the
   descriptor, 0700 entry), remove worktrees inside the instance, each one
   niwa's own walk found and only after a fresh read of its HEAD and
   per-worktree refs matches the snapshot, a fresh configuration check of
   its repository flags nothing and its `git status` reports no entry (any
   of these keeps), by niwa's own removal below, `claude rm` each
   session from the same empty directory, checked again first, and only
   after the vet below passes on that session's own job directory, its
   root chain and every entry under it, run on-reap, take the tombstone
   lock shared, rename the instance
   to its tombstone and repeat there the process listing, the configuration
   check on every repository, git status (`--ignore-submodules=all
   --untracked-files=all`), the
   index-flag read and, for a prunable worktree, its
   `git diff-index --cached` read from its admin directory, the ref snapshot
   and the record checks (any change, a
   new, moved or gone ref, a flagged key or a flagged index entry renames it
   back and keeps; only the entries of a worktree niwa removed, as they
   stood at that fresh read, may be gone, and a path the kept-set move took
   out may read as gone or deleted, while anything found at such a path
   again counts as a change),
   `DestroyInstance` on the
   tombstone, its plugin-record prune and trust withdrawal on the original
   path (Decision 1), release the lock, remove the ledger, delete
   the mapping, and write `reaped_at`, `restore_until` and `kept_entry` into
   the unit's launch record when it has one, whichever of the three made it
   a candidate; a unit with no kept-set entry gets the first two and no
   `kept_entry`. Any refusal is a disagreement, kept and named with its
   message scrubbed.

   niwa removes a worktree itself, never through `git worktree remove`,
   whose recursive delete goes by full path strings and so follows a
   directory swapped for a link after git's own checks, outside the
   instance and outside any sandbox the agent runs under. It opens the
   worktree's directory component by component from the instance's
   directory descriptor, and its admin directory, `worktrees/<id>` under
   the common git directory, from that git directory's descriptor, itself
   opened the same way from the instance's, every directory with
   `O_DIRECTORY|O_NOFOLLOW`. From those descriptors it checks that the
   tree's top-level `.git` file, opened `O_NOFOLLOW|O_NONBLOCK` and
   `fstat`ed as a regular file, names that admin directory, that the admin
   directory holds no `locked` file, and that the walk found no repository
   below the tree's top level and no git directory in the admin directory;
   any of these keeps, with the path named. It then removes the tree with
   the walk step 8 describes, the tree's own `.git` file last, then the
   admin directory the same way, then `worktrees/` itself if that leaves it
   empty. A link the walk meets as a link is removed as one, never
   followed; an open that fails, because a directory became a link or for
   any other reason, stops the walk the same way a nested `.git` entry
   does, keeps the rest and the admin directory, and keeps the unit with
   "changed during the pass: <path>", so the worktree stays registered and a later pass
   judges what is left. No git call follows: the worktree's HEAD, index,
   logs and per-worktree refs live in the admin directory and go with it.
   `git worktree prune` is no substitute and niwa never runs it, since it
   also deletes `worktrees/<id>` by path and follows one swapped for a
   link.

   `claude rm` takes a session id, not a path, and Claude Code deletes the
   session's job directory by its own means, which niwa can't see. So right
   before each call niwa vets that session's own job directory. `<id>` is
   the session's id as Claude Code's own `claude agents --json --all`
   listing gives it (step 2), the same id `claude rm` takes, never a value
   read from the instance or the job directory. niwa resolves the jobs root
   once per pass as Claude Code does, from `CLAUDE_CONFIG_DIR` when the
   person sets it and `~/.claude` otherwise, and the rule's walk and the
   vet both use that one root, written `~/.claude/jobs/` here. niwa uses
   the variable's value only when it's an absolute path that already
   equals its cleaned form, and never expands `~` in it; any other value,
   a relative one or one holding a `..`, a `.` or a repeated or trailing
   separator, keeps the unit, so niwa vets exactly the path Claude Code
   uses. niwa
   `Lstat`s `~/.claude/jobs/` and then `~/.claude/jobs/<id>`, and each must
   be a real directory, not a link, owned by the person's uid, with no
   write permission for group or others. Below the home directory, which
   may itself be a link (a home mounted elsewhere), no component may be
   one; a config directory outside the home gets the same chain check
   from its own root down. `<id>` must sit on the same device as
   `~/.claude/jobs/`. A listed session whose job directory doesn't exist
   keeps its unit, as doubt does, with no relaxation, and the vet is never
   skipped for any listed session. A session absent from the listing gets
   no `claude rm` and so no vet.
   After those root checks pass, niwa walks `~/.claude/jobs/<id>` with
   `Lstat`, follows no link, and reads each directory's entries sorted by
   name, so the path named is the same on every run. It keeps the unit on
   the first entry that is neither a regular file nor a directory (a
   symbolic link, a device, a named pipe, a socket), and on any directory
   under `<id>` whose device differs from `~/.claude/jobs/`, with "another
   filesystem at <path>" named. An `Lstat` or directory read that fails
   keeps. Any mismatch keeps the unit with the path named
   in the evidence, and niwa makes no `claude rm` call for it. A unit whose
   job directory legitimately holds a link keeps; the person retires it by
   hand after removing the link. The vet that runs before the first
   removal step runs under the pass's deadline, and one the deadline cuts
   short leaves the unit not reached; in a retire, `niwa destroy` or a
   retask, which have no all-unit deadline, it runs under its own
   10-second context and keeps when that expires. The vet lives in
   `realRemoveClaudeSession`, the function `removeClaudeSessionFunc` points
   to, so every `claude rm` niwa makes goes through it: the pass,
   `removeOwningSessionsListed` in `niwa destroy`, and retask's
   `removeCopy`, which removes a copy session it started by mistake. The vet
   covers that chain and every entry under the root; the residual in
   Security Considerations gives the races it leaves.
8. For a trim: check the root apply lock, no keep record protecting build
   trees, no process with its working directory in the tree or a file under it
   open, no repository found inside the tree, the newest write older than
   `trim_after`, and the gate (step `trim`); then remove
   each tree by walking it from the instance's directory descriptor, every
   directory opened with `O_DIRECTORY|O_NOFOLLOW` and every entry removed
   relative to its parent's descriptor, as the kept-set move opens its
   sources, so a directory swapped for a link sends nothing outside the
   instance. Every directory it opens is `fstat`ed, and one on a device
   other than the instance's stops the walk, keeps the rest and names
   "another filesystem at <path>". The removal walk stops at any `.git` entry or git directory
   (a `HEAD` beside `objects/` and `refs/`) it meets, keeps the rest of the
   tree and names the path, so a repository created in the tree after the
   checks isn't removed; a partly trimmed build tree is regenerable. When
   the walk removes a registered worktree (step 7), the one exception is
   that worktree's own `.git` file at the top of its tree, which step 7
   checked names its admin directory and removes last; a `.git` entry
   anywhere below that top level, or one at the top that isn't a regular
   file, stops the walk there as it does here. The
   instance, checkout and session stay.
9. Write `unit.json` (0600) with the answer, time stopped, size, verdict,
   scrubbed evidence and `reached_at`, and its hash into the ledger.
10. Handle the kept set, each entry's `files/` and `discarded/` apart: mark
    a part due on its own date (`due` for `files/`, `discarded_due` for
    `discarded/`) and note in `entry.json` when that part was first shown
    (`first_shown_due`, `discarded_first_shown_due`), remove a part first
    shown due at least a day before, and remove the entry directory once
    both parts are gone. An entry is found by enumerating
    `kept-files/<instance>/` and `Lstat`ing each direct child, which must be
    a real directory, never a symlink; the removal takes that enumerated
    directory, or its `files/` or `discarded/` child checked the same way,
    and never a path read from `entry.json`.
11. Stop when the deadline is reached; count the units not started. A unit
    the deadline cuts short mid-evaluation keeps, counts as not reached and
    has its `reached_at` set to this pass, and a call blocked in the kernel
    is abandoned rather than waited on. Print the dispatch line.

A retire runs steps 1 and 2 for its one unit with no all-unit deadline (the
instance guard and the working check read step 2's listings), then writes its
mark and runs steps 4 and 6 to 9, with no over question. The verdict-only
mode runs steps 1 to 6 for every unit or the one named, without step 3's
tombstone recovery, which it leaves to the next pass that acts, asks the seam
where step 5 would, clears
any mark the call wrote, computes the verdict the mark would lead to, and
prints; it writes nothing else. `niwa resume` reads the launch record, checks
`restore_until` and that `kept_entry`, when present, sits directly under
`kept-files/<instance>/` (one present but empty refuses), provisions the instance by name, re-validates every kept-set
path against the fresh instance and the entry's `files/`, refuses, naming the
file, any destination with a path component named `.git` or `.claude`
(compared case-insensitively), as it refuses one outside the instance, moves the files
back with a hash check and without following links or overwriting anything,
and starts the session. A record with no `kept_entry` skips the kept-set
steps and only provisions and starts the session. A step that fails after the files are back and before
the session starts moves them into the entry again, unchanged by hash, before
the rollback removes the instance.

## Implementation Approach

The phases follow the three parts of Decision 7: phases 1 and 2 are the rule
and worktrees, phases 3 and 4 the over question, mark and listing, and
phases 5 and 6 the keep record, kept set and launch record. Each phase ends
with niwa safe to release.

### Phase 1: The rule and reap's loss scan

The rule package and the pass, wired into every reap path, with no new
reason to remove anything. Reap gets stricter on purpose: an instance whose
session was deleted but that holds unique work is now kept and listed.

Deliverables:
- `internal/removal` with `Evaluate`, the walk, path classes, build-tree
  markers, generated paths, git checks with the scratch object store (under
  `~/.niwa/w-<key>/scratch/`, made fresh 0700 each pass, its directory and
  parent checked for owner and mode before each use and before a leftover
  is removed, its config written by niwa and compared byte for byte before
  each network call, its 1 GiB cap and cleanup on every exit path), patterns, and
  `.niwa/dispatch-retain` as a hold for unmapped instances.
- `gitCommand` with the hardened environment and overrides, the filtered
  `PATH` for git, `lsof`, `claude` and the session commands, `claude` and
  the credential-helper read run from empty directories under the same
  scratch directory with the same check, `-c http.proxy=` and
  `-c http.sslVerify=true` among the fixed overrides, the program-naming
  configuration check, run first and again by the invoker before every
  status and unforced worktree removal, proof remotes (the clone URL from the workspace
  configuration and `proof_remotes`, matched on the full authority, port
  included) as the only remotes contacted or
  counted, remote URL validation, and network calls against the scratch
  store with the person's helper for proof remotes only; the walk's
  no-symlink, one-filesystem, external-git-directory, unreached-worktree
  (the main entry included) and outside-work-tree rules; NUL-terminated
  parsing of every git output;
  `ResolveRecorded`; `ScrubEvidence` with the widened patterns and
  truncation after scrubbing; size-capped, strictly decoded record readers
  that open with `O_NOFOLLOW|O_NONBLOCK` and `fstat` for a regular file
  before reading, working from `O_DIRECTORY|O_NOFOLLOW` descriptors for
  `.niwa` and `pids`.
- `internal/cli/pass.go` with the deadline, units taken in a fresh random
  order each pass until Phase 4 writes `unit.json` (until then every unit
  reads as never reached), re-read, the tombstone
  rename and check under the tombstone lock with lock-guarded recovery, and
  the mapped sweep and backstop routed through the rule; kept instances
  printed with their first keeping check. `DestroyInstance`'s `Lstat` and
  root-child checks; lock files 0600 with `O_NOFOLLOW`.
- `[session.removal]` with `ignored_files`, `job_scratch`, `disposable` and
  `proof_remotes`; `[repos.<name>] build_trees`.
- The guide's deny-write advice, the whole list. This is the first phase
  whose pass runs the person's credential helper and ssh, and niwa already
  runs `claude rm` today, so the list ships complete here: `~/.niwa/`
  with the scratch directory (the ledger and kept set later phases add sit
  there too), the root's `.niwa/` and `.niwa-tombstone-*`, `~/.gitconfig`
  and `~/.config/git/config`, `~/.ssh/` with every file its `Include`
  names, any script or library those configuration files name that the
  agent could write (a credential helper's script, a `ProxyCommand` or
  `Match exec` target, a `PKCS11Provider` library), Claude Code's user
  settings with the user-scope plugins, which can define hooks or programs
  `claude` runs, in the config directory resolved as Data Flow step 7
  resolves the jobs root, and Claude Code's user-scope MCP file, which can
  name programs `claude` runs too: `.claude.json` in the home directory
  when `CLAUDE_CONFIG_DIR` is unset, beside the default config directory
  rather than inside it, and `.claude.json` inside the directory the
  variable names when it's set, all with sandbox write restrictions that also
  cover Bash (Claude Code's sandbox or an operating-system equivalent)
  rather than path deny rules alone; its note that ssh follows `Include`
  while the credential-helper read doesn't follow git's includes; and its
  note that a worker without such a sandbox has no boundary. Phase 6 adds only
  `.niwa-launch/` and its restore-window note.
- `niwa reap --verdict-only [<instance>] [--json]` without over answers.
- Unit tests per check and class; `@critical` functional scenarios for
  reap's loss scan (clone and worktree), an unreachable remote, a nested
  repository, the path-class cases, the overlapping-pass and held-lock
  cases, and a tombstone an interrupted pass left, which the verdict-only
  mode leaves in place and the next reap renames back.
- Security scenarios: a repository with `core.fsmonitor`, `core.hooksPath`,
  `core.sshCommand`, `core.pager` and `credential.helper` set to a script
  that writes a marker file runs none of them through a reap and the
  verdict-only mode; a `filter.x.clean` in repository config keeps the unit
  with the key as evidence; a `filter.x.clean` set to a marker-writing
  script, with an attributes entry and a stat-dirty tracked file that
  select it, planted in a repository's configuration after the pass's first
  configuration check and before the rule's `git status` there, or before
  the re-read's, keeps the unit with "changed during the pass" and the key
  named, and the filter never runs (no marker file); the same driver set only in `config.worktree`,
  with `extensions.worktreeConfig` on, keeps the unit with the key named and
  never runs; a repository holding a signed commit in `refs/stash`, with
  `log.showSignature` set and a `gpg.program` that writes a marker file,
  writes no marker through a reap and the verdict-only mode and keeps with
  `gpg.program` named; a repository with `status.showUntrackedFiles=no`
  and an untracked `notes.txt` keeps the unit with "untracked files:
  notes.txt"; a deinitialized submodule whose `.git/modules/<name>` holds a
  commit no proof remote has keeps the unit, as do a bare repository in the
  instance and a nested repository inside a `CACHEDIR.TAG` build tree, each
  holding such a commit; an `ext::` and a `file://` remote are never
  contacted and keep; a repository whose agent-added remote advertises the
  local commit, served by a test server, is kept with "remote not
  recognized" and the server sees no request; a commit pushed to the clone
  URL is held; a remote matching a `proof_remotes` prefix is held; a symlink
  inside the instance pointing at a file outside is never read or summed; a
  `.niwa` replaced by a symlink keeps the unit and nothing is written
  through it; a `.git` file pointing outside keeps; a linked worktree added
  outside the instance (`git worktree add <temporary dir>`), with a commit
  only on its `refs/worktree/` and a file staged only in its index, keeps
  the unit with "worktree outside the instance" and the path named; a git
  directory inside the instance whose work tree is outside it (made with
  `git init --separate-git-dir`, or through `core.worktree`) keeps the unit
  with "work tree outside the instance" and the path named; a
  prunable worktree whose working tree was deleted and whose
  `refs/bisect/` holds a commit no proof remote has keeps the unit; a
  worktree's `gitdir` file rewritten to a path holding a newline and a
  `HEAD` line naming a pushed commit doesn't hide that worktree's unpushed
  detached HEAD, which keeps the unit; an untracked, not
  ignored, file written into a repository's working tree after the re-read
  renames the tombstone back and keeps; a second
  pass started while the first holds a tombstone leaves it alone; a
  credential in a remote URL, an `AKIA` key id, an `xoxb-` token and a
  `?access_token=` query value never appear in `unit.json` or any output; a
  `PATH` with an empty entry and an entry symlinked into the root never
  resolves git from either; an unpushed commit stays kept when the clone
  carries a `refs/replace/` entry, an `info/grafts` file or a forged
  commit-graph that makes the proof remote's tip appear to descend from it,
  and the verdict-only mode leaves the clone's `.git` unchanged after a
  merged-by-content check; a scratch store planted at the path the pass
  creates its store at, holding a pack that maps the proof remote's tip id
  to a forged commit whose parent is the unpushed commit, keeps the unit,
  and so does a store whose config is rewritten after niwa writes it with a
  `url.<base>.insteadOf` pointing at a test server that advertises the
  unpushed commit, which sees no request; a scratch directory, or its
  parent, with the wrong owner or a mode other than 0700 keeps the unit,
  and nothing runs from it; with `CLAUDE_CONFIG_DIR` set to another
  absolute directory, the walk reads that directory's `jobs/`, and the
  guide's deny-write list and the restore window's default (once its phase
  lands) name that directory's settings rather than `~/.claude`, the
  deny-write list naming that directory's `.claude.json` rather than
  `~/.claude.json`, while a relative value keeps the unit; a
  `CLAUDE_CONFIG_DIR` value that is absolute but not already clean, such as
  one containing `..`, keeps the unit; a repository whose configuration names no
  `core.excludesFile`, with a per-user ignore file planted at
  `~/.config/git/ignore` that matches an untracked `notes.txt`, keeps the
  unit with "untracked files: notes.txt", since that ignore file is never
  honored; a repository whose own configuration sets
  `core.trustctime=false` and `core.checkStat=minimal`, with a tracked file
  rewritten to its old size and modification time, keeps the unit with that
  file named as modified; a local branch whose `git merge-tree
  --write-tree` against the proof remote's default branch hits a conflict
  and exits non-zero isn't merged by content and keeps the unit; a scratch
  store holding a `commondir`, an `objects/info/alternates` or an
  `objects/info/http-alternates` file before a use keeps the unit, and no
  network call runs; a program-naming key written in another case
  (`Core.FSMonitor`, `Credential.Helper`) and set to a marker-writing
  script is matched as its canonical name, keeps the unit with the key
  named and writes no marker file; and with a git older than 2.45, a
  repository whose configuration sets `extensions.partialClone` or a
  `remote.<name>.promisor` keeps with "partial clone needs git 2.45" before
  any call that could read an object.
- Security scenarios for the proof-remote and tombstone checks, each
  through a reap and, where it applies, the verdict-only mode:
  with `proof_remotes = ["https://<test host>/dana/"]`, a remote
  `https://<test host>/dana/../mallory/repo` added in the clone, whose
  target advertises the unit's unpushed commit, keeps the unit with "remote
  URL not allowed" naming the dot segment, and the test server sees no
  request; the same with a percent-encoded `..`, and with userinfo in an
  `https` URL, keeps the same way; a remote under
  `https://<test host>/dana-evil/` that advertises the commit doesn't match
  the prefix and keeps with "remote not recognized", and so does a remote on
  `https://<test host>:<other port>/dana/repo`; a loose object written
  into the clone under the id of a real proof-remote commit, with the unit's
  unpushed commit as its parent, leaves that commit kept by both the
  ancestry and the merged-by-content checks; a commit made in the instance after the
  re-read and before the rename, which leaves `git status` clean, renames
  the tombstone back and keeps with the ref named.

### Phase 2: Worktrees before the session, and every other removal

Deliverables:
- Worktrees inside the instance removed before `claude rm` in the pass and
  in `runDestroyInstance` by niwa's own removal (Data Flow step 7), with the
  status before each one built by `gitCommand`; `rmtree.go`'s no-follow walk
  ships here, and the trim reuses it in Phase 4.
- The job-directory vet in `realRemoveClaudeSession`, the function
  `removeClaudeSessionFunc` points to, so every `claude rm` niwa makes goes
  through it: the pass, `removeOwningSessionsListed` in destroy, and
  retask's `removeCopy` (Data Flow step 7). It runs before each call on that
  session's own job directory, and the pass vets every session it will
  pass to `claude rm`, those in the decoded `claude agents` listing, before
  its first removal step, as destroy does with every mapped session the
  listing holds, removing nothing when one fails.
- Failed-dispatch cleanup, the watch's discard and the watch's failed-stage
  rollback (`stageReview`'s deferred cleanup) calling `removal.Evaluate`
  before `destroyInstanceFunc`, with a review instance's `pr-clone` judged
  by the normal rule (Decision 1); on a keep, a keeping verdict leaving the
  instance on disk with the keeping check and path named, the discard
  deleting the staged record, and any rollback, before or after the review
  agent launched, appending the handled entry for that PR head and saying
  in its output that the head won't be staged again until it moves, with
  the four comments that state the old contract (`AppendHandled`'s doc
  comment at state.go:131-136, `stageReview`'s doc comment at
  watch.go:771-772, the "Record ONLY on success" comment at watch.go:878
  and the call-site comment at watch.go:305-306) updated to say a kept
  failed stage records the head too, the handled entry otherwise as today, the instance listed
  as a stopped unit either way; `FetchPRHead` unchanged, with the fetch record, the removal of review
  instances through the rule and the rest of Decision 1's follow-up in
  tsukumogami/niwa#551; the hook worktree sweep evaluating each worktree's
  repository checks.
- The hook worktree sweep (`internal/cli/reap_worktrees.go`,
  `internal/worktree/worktree.go`) and `niwa destroy`'s scan
  (`internal/workspace/scan.go`) building every git call with `gitCommand`,
  every status with `--ignore-submodules=all` and `--untracked-files=all`,
  every log-family call with `--no-show-signature`, the program-naming
  configuration check run on every repository niwa's walk finds under the
  worktree or instance, submodules, gitlinks and nested repositories
  included, before the first other git call in any, and `DestroySession`,
  on the sweep's path, removing its worktree through the `rmtree.go`
  `WorktreeRemover` instead of
  `git worktree remove --force`, with its own status as the clean check when
  force is off.
- Security scenarios: a hook-provisioned worktree's repository and an
  instance repository with `core.fsmonitor` set to a script that writes a
  marker file, run through the hook worktree sweep (by `niwa dispatch`) and
  through `niwa destroy`, write no marker; a `filter.x.clean` in such a
  repository keeps the worktree through the sweep and makes an unforced
  destroy refuse with the key named; a submodule (and, separately, a nested
  repository registered as a gitlink) whose own configuration carries a
  `filter.x.clean` driver, with a `.gitattributes` entry and a stat-dirty
  file that would trigger it, keeps the unit through a reap, the
  verdict-only mode and the hook worktree sweep, makes an unforced destroy
  refuse with the key named, and the driver never runs (no marker file); the
  same driver planted in a worktree's repository after the first check and
  before that worktree's status or removal in a reap, before
  `DestroySession`'s status or removal in the hook worktree sweep, and before
  the scan's status or an unforced worktree removal's status in
  `niwa destroy`, keeps the unit (the worktree, in the sweep; an unforced
  destroy refuses) with the key named, and the driver never runs.
- Security scenario for the worktree removal, run through a reap, the hook
  worktree sweep (by `niwa dispatch`), `niwa destroy` and
  `niwa destroy --force`: a clean unit whose niwa worktree holds
  `deep/a/b/file.txt`, and a directory outside the instance holding a
  marker file, with its listing, sizes and hashes recorded first. A test
  hook in the removal walk, called after the walk reads an entry's type and
  before it opens it, renames `deep/` aside and puts a symlink to the
  outside directory in its place; in a second run the hook fires before the
  removal opens the worktree's own directory and swaps that directory for
  such a symlink; in a third the hook swaps the worktree's `worktrees/<id>`
  admin directory for such a symlink before the walk opens it. The hook makes the swap land on every run rather than by
  chance. In every run nothing outside the instance is deleted: the outside
  directory's listing, sizes and hashes match the recorded ones and the
  marker file is still there. The unit (the worktree, in the sweep) keeps
  with "changed during the pass" and the swapped path named, and an
  unforced destroy refuses the same way; under `--force` the worktree is
  left to go with the instance directory, and the outside directory is
  still unchanged after the destroy.
- Security scenario for the job directory's vet, run through a reap: a
  clean unit whose `~/.claude/jobs/<id>` is replaced by a symlink to a
  directory outside the sandbox holding a marker file, with its listing,
  sizes and hashes recorded first; in further runs `~/.claude/jobs/` itself
  is that symlink, `~/.claude` itself is that symlink,
  `~/.claude/jobs/<id>` is on another device (a mounted disk image), and
  `~/.claude/jobs/<id>` is a real directory with the
  wrong owner, or with group or other write permission. In every run the
  unit keeps with the failing path named, a `claude` wrapper logs no
  `claude rm` call, and the outside directory's listing, sizes and hashes
  match the recorded ones with the marker file still there.
- Functional scenario for the vet's scope, run through a reap: a unit whose
  session the person already deleted, absent from the `claude agents`
  listing, reaps with no `claude rm` for it, while a listed session with no
  job directory keeps its unit with the missing path named.
- Security scenario for an inner link under the job directory, run through
  a reap: a clean unit with no handoff brief whose
  `~/.claude/jobs/<id>/scratch/out` is a symlink to a directory outside the
  sandbox holding a marker file, with its listing, sizes and hashes
  recorded first; in a further run the entry is a named pipe; in a third
  the same link sits under a session mapped to an instance and the run
  goes through `niwa destroy`; in a fourth it sits under a copy session
  retask started and then removes through `removeCopy`. In every run the
  unit keeps (destroy stops; retask leaves the copy in place) with the
  entry's path named, a `claude` wrapper logs no `claude rm` call, and the
  outside directory's listing, sizes and hashes match the recorded ones with
  the marker file still there. A control run with the link replaced by a
  regular file reaps the unit, a `claude` wrapper logging one `claude rm`
  for that session, as the functional scenario below also shows.
- Functional scenarios: a clean unit holding a niwa worktree leaves no session
  behind after a reap and after `niwa destroy`; failed-dispatch cleanup keeps
  `partial.txt`, and the watch's failed-stage rollback after launch keeps a
  file the review agent wrote, in place with its contents unchanged, names
  the first keeping check, `pr-clone`'s repository check, and appends the
  handled entry, so the next watch pass stages no new instance for that PR
  head, while `niwa reap --verdict-only` lists that file among the keeping
  checks; a stage whose fetch fails before it writes anything into
  `pr-clone` leaves no instance and appends no handled entry, and the next
  watch pass stages that PR head again; with every launch failing through
  the `dispatchLaunch` seam (dispatch_launcher.go:70, called at
  watch.go:864) and `claude` on `PATH`, so the rule's liveness read
  decodes, and the rule keeping the instance on `pr-clone`'s repository
  check, two `watch --once` passes over the same PR head leave one
  instance, not two, because the first rollback appended the handled
  entry, and the first pass's output names that check and path and says
  the head won't be staged again until it moves; the watch's discard of a
  review instance holding the review draft keeps the instance with its
  first keeping check named, `pr-clone`'s repository check (a commit no
  remote ref holds), leaves the draft on disk with its contents unchanged,
  deletes the staged record and lists the instance as a stopped unit,
  which `niwa list` still shows, and `niwa reap --verdict-only` lists the
  draft among the keeping checks, after `pr-clone`'s; no `claude` call a wrapper logs carries an override flag,
  no `git` call a wrapper logs is `git worktree remove` or
  `git worktree prune`, and none carries `--force` outside `niwa destroy
  --force`; every `claude` call a wrapper logs runs from a directory outside
  the workspace; an instance whose `.claude/settings.json` and
  `settings.local.json` define every hook event with a command that writes
  a marker file writes no marker through a reap and through `niwa destroy`.

### Phase 3: The over seam, the mark and the retire command

Deliverables:
- `LedgerStore` and the ledger match, first used here for the mark, and
  the guide's note that the ledger is advisory for a worker without the
  sandbox Phase 1's deny-write advice names.
- `[session.over]`, `overSeam`, and the mark writer, reader and voiding
  rules, with the mark's ledger entry and the clean-up under the instance
  lock.
- `niwa retire` with `--override-not-over`, refusing inside an instance
  through `insideInstance` (`internal/cli/guard.go`), and the override
  requiring `confirmOnTerminal` (`internal/cli/confirm.go`).
- The guide's advice to over-command authors that the instance is
  untrusted input.
- Marked-unit reaps in the pass, needing the launch record dispatch already
  writes; a marked unit without one kept with that reason. Until Phase 6,
  the undo for a reap on a mark is `niwa resume` as it works today, which
  restores the instance under its name from that launch record and resumes
  the session while Claude Code keeps its transcript; from Phase 5 the named
  files such a reap moves wait in the kept set, shown by `niwa list --kept`,
  for the person to copy back. The window's dates, the refusal past it and
  the restore of kept files arrive in Phase 6.
- `docs/guides/over-command.md` with the contract and the takeover key, and a
  functional test of the key against a real `koto init --supersede`.
- Functional scenarios for the three answers, a timeout leaving no process and
  no mark, a held marked unit not asked, a mark that doesn't parse, a copied
  mark, a removed mark, a mark written by hand with no ledger entry, a retire
  run from inside an instance and from a process whose ancestor sits in one,
  a detached `--override-not-over` with no controlling terminal refusing,
  the instance name typed in place of the printed code refusing, a
  disagreement, the six kinds kept and listed, a retire of a clean, stopped
  unit whose session `claude agents` lists, reaped with one `claude rm` a
  `claude` wrapper logs after that session's vet, and a unit a retire
  reaped, brought back by `niwa resume` under its original name with its
  session started.

### Phase 4: The listing, the dispatch line, the trim and pid records

Deliverables:
- `unit.json` written by the pass with its ledger entry; `niwa list` state
  lines and the JSON `unit` object, re-scrubbed on read; the dispatch line;
  reap's counts; and the pass taking units by `reached_at`, never-reached
  first, as Data Flow step 3 describes.
- Over answers in the verdict-only mode, with the mark each would write.
- The trim with `trim` and `trim_after`, the root apply lock, the process
  checks for build trees, the refusal of a tree that holds a repository, and
  removal by `rmtree.go`'s no-follow walk from Phase 2, from the instance's
  descriptor, stopping at any `.git` entry or git directory it meets.
- `niwa helper` with its child-of-caller and not-a-session checks and its
  ledger entry; pid record reading in liveness; the pid record format and
  `niwa helper` in the guide, with its note that under the sandbox Phase
  1's deny-write advice names `niwa helper` can't register a helper, which
  then keeps its unit.
- Functional scenarios for the no-command dispatch line, the idle build tree,
  the time budget and units not reached, the local listing with the network
  off, a pid record written by hand that doesn't release a live process, a
  `niwa helper` call naming a process that isn't the caller's child, a
  `unit.json` carrying terminal escapes that the listing prints clean, and,
  with more units than one pass reaches, the units it didn't reach taken
  first by the next pass.

### Phase 5: Keep records, the handoff brief and the kept set

Deliverables:
- `keep.json` holds, review dates, build-tree protection and named files;
  holds ended by later work.
- The handoff brief reader, the newer-than-the-brief test, and the
  `in_brief` check against the brief's `stored_at` copy.
- `keep.json` and brief file paths through `ResolveRecorded` on every read.
- The kept set at `~/.niwa/w-<key>/kept-files/` with the move of regular
  files only, each source opened component by component and hashed from its
  descriptor, 0700 entry directories and 0600 metadata, setuid bits
  cleared, due dates, the day's notice, `niwa list --kept` showing paths and
  sizes only, and the line for due entries; `DestroyWorkspace` leaving it in
  place and printing its path.
- `niwa discard` with `insideInstance` and `confirmOnTerminal`, its ledger
  entry and the ledger match on read, and discarded paths moved to the
  entry's `discarded/`, due at the end of the restore window (computed from
  Claude Code's retention until phase 6 adds the setting) and removed on
  their own date and notice, apart from the entry's `files/`. It lands here
  rather than in phase 1 because a discard must never release a path before
  the kept set can hold it.
- Functional scenarios for the keep-record, newer-scratch and held-unit
  cases, including the read-only kept-set volume; a keep record naming a
  symlink to a file outside the instance, which keeps the unit and copies
  nothing; a source directory swapped for a link after the check, which the
  move refuses; an `in_brief` file with no `stored_at` and one whose copy
  differs, both kept as unique; an `in_brief` file whose `stored_at` copy
  matches but resolves under the job directory or the instance, which
  counts as nothing and keeps as unique; a `discard.json` written by hand with no
  ledger entry, which releases nothing; a `discard.json` path through a
  symlink or with `..`, which keeps; a detached, session-less
  `niwa discard`, which refuses with "no controlling terminal"; a reap of a
  clean unit whose `keep.json` names an untracked file in its main clone,
  which removes the instance, with the file and its hash in the kept-set
  entry; and a confirmed discard whose paths are found in the kept set after
  the reap.

### Phase 6: Launch records for hook sessions, the restore window and restore

Deliverables:
- `niwa instance from-hook` writing launch records; the mapping's
  `launch_id`.
- `reaped_at`, `restore_until` and `kept_entry` on the launch record at
  every reap of a unit that has one, with `kept_entry` absent for a unit
  with no kept-set entry; `restore_window` with the seven-day floor;
  `sweepLaunchRecords` keeping a
  record that carries `reaped_at` until `restore_until` has passed, and
  applying its transcript rule only after.
- `niwa resume` refusing past the window and restoring kept files first,
  a present `kept_entry` checked to sit directly under
  `kept-files/<instance>/` and one present but empty refused, a record with
  no `kept_entry` resumed with no files moved, every
  path re-validated, a destination with a path component named `.git` or
  `.claude` (compared case-insensitively) or outside the instance refused
  with the file named, parents created without following links, files created
  with `O_CREAT|O_EXCL|O_NOFOLLOW`.
- The guide's deny-write advice gains the root's `.niwa-launch/` this
  phase writes, under the same sandbox write restrictions, and its note
  that `cleanupPeriodDays` in Claude Code's user settings, already on
  Phase 1's list, now sets the default restore window. The rest of the
  list ships in Phase 1.
- Strict decoding of launch records, and `niwa resume` ignoring the replay
  fields on a record whose `origin` is `hook`.
- `validateLaunchRecord` accepting a hook-shaped instance name
  (`<config>-<8 hex>-<3 hex>`) when the record's `origin` is `hook`, and
  `niwa resume` writing a hook record's session mapping in the shape
  `niwa instance from-hook` writes, with no origin, no `DispatchID` and
  `launch_id` set, so `niwa authority check` answers `not-dispatched` for
  the restored session; a dispatch record keeps today's dispatch shape with
  `launch_id` added.
- Functional scenarios for restore inside and past the window, with and
  without the setting, for a clean unit with no keep record whose record
  has no `kept_entry`, resumed with nothing restored, for an `entry.json` edited to name `../` or an
  absolute path, a launch record whose `kept_entry` names another directory,
  and a fresh instance with a symlink where a kept file goes (all refuse and
  write nothing outside the instance), for an `entry.json` naming
  `.git/hooks/pre-commit`, `.CLAUDE/settings.local.json` or a path outside
  the instance, each refusing the restore with that file named and nothing
  written, and for the three-day setting failing the configuration load.
- A security scenario for the guide: its deny-write list names the whole
  `~/.ssh/` directory and, as an example, a file outside it that an
  `Include` in `~/.ssh/config` names (`Include ~/dotfiles/ssh/hosts`).

## Open Dependencies on the Earlier Stage

Each point below comes from the earlier workspace-level remover, which hasn't
landed. The position given is where that work stands now. niwa follows it as
this design describes, and if it changes before landing, this design follows
the change rather than settling the point on its own.

1. **The over command's contract.** Current position: `over <instance>`, or
   the path in `NIWA_REMOVE_PATH` with no argument; exit 0 over (and that form
   writes the mark), 1 not over, 2 usage, 3 a fact couldn't be read, 124 on
   its deadline; the reason on the last output line; a 20-second default
   deadline, and a backstop in its own pass of the deadline plus 10 seconds.
   Its check-only entry point, `over check`, writes nothing and is named as the
   form niwa calls. Settled by that work's command contract as it merges.
   niwa's side: it runs the configured command with the removal gate's calling
   shape, reads only the exit status, uses 20 seconds as the default per-run
   timeout, and makes the backstop unnecessary by writing the mark itself and
   clearing any mark written during a call that didn't exit 0.
2. **The retire mark's schema, place and voiding.** Current position:
   `<instance>/.niwa/retired.json`, with a schema name namespaced to that
   remover; required name, creation time, marked-at and marked-by fields and a
   basis; later session activity or a moved branch head voids it. Its reader
   also counts a mark only when its hash and marked-at time match a copy in
   that remover's own launch record outside the instance, which niwa can't
   read. Unsettled there: its launch record names a branch's repository by
   path while the mark names it by a bound key. Settled by that work's mark
   reader and by its step that first compares the two shapes. niwa's side:
   same file, same fields, same voiding, the repository named by its path
   inside the instance until that step settles one shape, and the same kind of
   outside copy, kept in niwa's own ledger (Decision 9), so neither reader
   counts a mark the unit's agent wrote, and each counts only marks its own
   writer made. niwa's reader is pinned to that remover's exact schema name
   and field set and reads any other schema version as no mark, so a change
   in that format can only make niwa count fewer marks; a conformance fixture
   taken from that work's own writer joins the tests when it lands.
3. **The keep record's form and the kept set's layout.** Current position:
   keep records written by hand into that remover's own state, never expiring
   (past the review date they still keep and are only flagged due), and a kept
   set of one directory per unit and move with a manifest, which never
   expires. Settled by that work's keep-record reader, detailed but not built,
   and its kept-set move, not yet built. niwa's side: the same fields in
   `<instance>/.niwa/keep.json` plus `protects`, `bytes`, `in_brief` and
   `brief`; a kept set at `~/.niwa/w-<key>/kept-files/` whose entries go due
   and leave as R27 requires (the first clash, reconciled in Decision 5).
4. **The pattern list's format.** Current position: one entry per line, the
   pattern, a tab, and the review that added it; a line naming no review is a
   parse error that keeps every unit; no pattern syntax is defined. That
   reading of that work hasn't been checked against its code, which may
   already match entries as a shell `case` glob. Settled by
   that work's list format and the first additions from its review of passes
   run without acting. niwa's side: the shipped list and the workspace's `disposable`
   entries, each with a required review, in the glob-plus-`**` syntax Decision
   6 defines, offered as the syntax that work adopts.
5. **The launch record.** Current position: that remover writes its own per
   unit, because it can't change niwa, naming the session, job directory,
   transcript, repositories, branches, a copy of the last mark and a
   handoff-brief field that is always empty so far. Settled by that work's
   launch-record step and by whether it moves to niwa's record once niwa writes
   one for every unit. niwa's side: its own `niwa.launch/v1` record serves the
   reap-on-a-mark check and restore, written for hook sessions too; the
   handoff brief is read from `keep.json`.
6. **The pid record.** Current position: nobody writes one; that work leaves
   the writer to the work that follows it, which is this. Settled here: the
   starter of a background helper runs `niwa helper`, which writes
   `pids/<pid>.json` in the `niwa.pid/v1` format with a ledger entry, and
   niwa reads it. Open only in that the earlier remover's liveness check
   adopts this format when it lands.
7. **The discard record.** Current position: the unit's owner writes it by
   hand, naming paths in the unit and optionally a patch file; no command
   writes one. Settled here: `niwa discard` writes `discard.json` with that
   form's fields and a ledger entry after a confirmation at the person's
   terminal, a hand-written one counts for nothing in niwa, and niwa keeps
   the paths a discard releases in the kept set until the restore window
   ends rather than deleting them at reap. Open only in that the form may change before that work
   lands, in which case `niwa discard` follows it.
8. **The undo interval.** Current position: none; a fresh mark can carry a
   reap in the same pass, and only a unit seen for the first time waits one
   interval, because that remover's launch record is new. Settled by that
   work's pass order as it merges. niwa's side: no interval; the restore
   window is the undo.
9. **The interim scratch copy.** Current position: a 48-hour copy of a unit's
   job directory and loose root files, skipped in passes run without acting, made for
   every reaped unit because the handoff-brief field is always empty there.
   Settled by that work's acting step. niwa doesn't carry it: a unit with no
   brief lets its scratch go by the project owner's default, and a workspace
   that wants it kept sets `job_scratch = "unique"`.
10. **The takeover key.** Current position: that remover reads no key saying a
    successor took over. Settled here by Decision 3: the fence naming a
    successor that holds `koto/superseded-carry-complete`. Open only in that
    the key has been read from koto's source and not yet seen in a live run,
    which the seam's functional test closes.
11. **Linked worktrees inside the unit.** Current position: the earlier
    remover always keeps linked worktrees, clean or not, and removes none.
    Settled by that work's acting step as it merges. niwa's side: a clean
    linked worktree inside the instance is removed by niwa's own walk before
    the session (Decision 1, Data Flow step 7), since one left in place makes
    `claude rm` refuse and the session outlives the reap; a worktree outside
    the instance, or one that isn't clean, keeps. This is the second clash,
    kept on niwa's side because the worktrees-first order is what lets a reap
    leave no session behind.
12. **Where a brief stores the bytes it holds.** This point is open on the
    separate work that writes the handoff brief, not on the earlier remover.
    Current position: that work defines no location for the bytes of a file
    a brief holds, so niwa has no copy to check an `in_brief` claim against
    (R28). Settled by the brief work as it defines that location. niwa's
    side: until then, every `in_brief` file counts as unique and keeps its
    unit.

## Relation to Other Work

**tsukumogami/niwa#300**, the reap's missing loss scan, is closed by phase 1:
the mapped sweep and the backstop evaluate the rule before removing, a scan
error keeps, and the other non-interactive removals (failed-dispatch cleanup,
the watch's discard, the watch's failed-stage rollback) go through the same
rule.

**tsukumogami/niwa#458** asks for a way to keep one chosen instance from reap.
The holding form of `keep.json` is that first-class marker, and `niwa list`
shows the unit as held with its owner and reason. `.niwa/dispatch-retain`
keeps its meaning for the backstop and is read as a hold by the rule.

**tsukumogami/niwa#459** asks for a functional test of reap keeping a marked
instance and reclaiming an unmarked one. The phase 5 scenario with two
dispatched units, one held, both sessions deleted, is that test, in
`ephemeral-session.feature`.

**tsukumogami/niwa#536** implemented `docs/designs/current/DESIGN-plugin-release-hold.md`
and reads `~/.niwa/w-<key>/kept/` as a directory of whole kept instances.
This design's kept set sits in `kept-files/` beside it and writes nothing to
`kept/`, and both compute the same workspace key, `hostdir.WorkspaceKey`;
that's the overlap this work checks against the landed layout.

**Destroy leaving sessions behind.** Today `niwa destroy` runs `claude rm`
before it removes the directory, so a session whose worktree sits inside the
instance still has that worktree when Claude Code is asked to drop it, the
request is refused, and the session outlives the destroy. Phase 2 removes the
worktrees first, in destroy and in the pass.

**A worktree-cleanup policy proposed separately** would remove a worktree
seven days after its spawn finished whatever it holds and archive at
teardown. Its time-based
deletion would remove exactly the worktrees this rule keeps for holding
unique work. This design's removal rule takes precedence for every removal
niwa makes: a worktree is a path like any other and goes only when the rule
passes it. Superseding that policy is outside this design.

**The delete-only teardown decision** in
`docs/guides/ephemeral-session-instances.md` and
`docs/designs/current/DESIGN-ephemeral-session-instances.md`, that a finished
session's instance is kept until the session is deleted, holds for every
workspace with no over command: nothing goes unasked but idle build trees.
A workspace that names a command changes it for itself.

**`docs/designs/DESIGN-instances-outlive-sessions.md`** describes the
before-remove gate and the launch records `niwa resume` replays. This design
keeps the gate at every removal, adds the `retire` step name, and extends the
launch record with optional fields.

**A workspace hook that denies `niwa reap`** is untouched; the workspace
decides when to lift it now that reap applies the rule.

**The shirabe command's later work** ships an over command for shirabe's
workflows against the contract in `docs/guides/over-command.md`. It inherits
the six kinds of unit a shipped command never marks, which niwa shows as
whatever answer and reason the command gives, each kept and listed with its
size and time stopped until a person retires it:

| Kind of unit | Answer | Shown as |
|--------------|--------|----------|
| A merged pull request whose workflow run is still live | not over | `not over: run not finished` with the run's state |
| A run that ended blocked, waiting on a person | not over | `not over: blocked, waiting for a person` |
| An abandoned pull request that is still open | not over | `not over: pull request open` |
| A session whose work a person moved elsewhere by hand | unknown | `unknown: no takeover found` |
| A top session started by a person, with nothing above it | unknown | `unknown: no evidence` |
| A plain chat with no workflow run | unknown | `unknown: no evidence` |

niwa doesn't tell these apart; it shows the reason the command printed. A
not-over unit goes only through `niwa retire --override-not-over`, an unknown
one through a plain `niwa retire`, and either way the rule decides what goes.

## Security Considerations

The trust boundary is the instance. The unit's agent is untrusted, and every
file in its instance and job directory is untrusted input: the repositories
and their `.git` directories, the records under `.niwa/`, `unit.json`, and
anything a kept-set entry carries back at restore. The pass runs as the
person, automatically, before every dispatch, create and watch, so whatever
it runs or deletes on the strength of that input acts with the person's
rights. Each part below gives the risk, what the design does about it, and
what is left.

### External artifact handling

niwa reads five kinds of untrusted input: the over command's exit status and
output, the per-unit records, the git repositories inside the instance, the
history a proof remote sends into the scratch store, whose branches the agent
may have pushed and whose objects git re-hashes as it indexes them, and the
kept-set entries it reads back at restore. Git is the largest. A
repository's configuration can make `git status` or a fetch run a program
(`core.fsmonitor`, hooks, a pager, `core.sshCommand`, askpass, credential
helpers, filter, diff and merge drivers, a `gpg.program` that
`log.showSignature` makes `git stash list` run) or contact a host of the
agent's choosing through an `ext::` or `file://` URL. It can also hide
work: `status.showUntrackedFiles=no` makes `git status` report no
untracked file at all. The remotes themselves are
agent-written too: an agent can add a remote pointing at a host it controls
and serve a ref at its own unpushed commit, or a default branch whose tree
equals the merge result, so that the commit reads as pushed or merged. The
records are the other large input: a mark, a discard record and a pid record
together could turn an agent's own unpushed work into something the next
pass deletes, and a brief's `in_brief` claim could mark an unpushed file as
handed forward with no copy anywhere.

Every git call niwa's removal paths make goes through `gitCommand`
(Decision 8): the pass, the hook worktree sweep through
`worktree.DestroySession`, and `niwa destroy`'s status before each worktree
removal and its scan, `workspace.ScanInstance`. The sweep and the scan start plain `git`
with the repository's own configuration today, in exactly the repositories
an agent controls, and the sweep runs before every dispatch, so the
implementation changes `internal/worktree/worktree.go`,
`internal/cli/reap_worktrees.go` and `internal/workspace/scan.go` to build
their calls with it. The builder means no inherited environment, system and
global configuration ignored, hooks, fsmonitor, pagers, askpass,
credential helpers and signature checks off, replace refs, grafts and
commit-graph files ignored, every untracked file reported whatever the
repository's settings, only `https` and `ssh` allowed, no proxy and
certificate checks on, and `--git-dir` and
`--work-tree` taken from the walk. The program-naming configuration check
is the first git call in
every repository on all of these paths, and it runs on every repository
under a unit, each submodule, gitlink and nested repository included, found
by niwa's own walk, before the first other git call in any of them. It
isn't a single gate: the invoker runs it again on a repository right before
every `git status` there, the status before each worktree removal included,
and right before each worktree removal, since a process still alive in the
instance could add a key in the seconds between the first check and those
calls, and a key found then keeps the unit with
"changed during the pass" and the key named. The
walk recognizes a git directory by its structure as well as by the name
`.git`, so a bare repository, a removed submodule's store under `modules/`
and a repository inside a build tree are all found and checked; one whose
work tree sits outside the instance, through `core.worktree` or a `.git`
file elsewhere, keeps the unit, since its index would go unread. A
repository whose configuration, in any scope but the command line, names a
filter, diff, merge, credential or signing program, an fsmonitor hook or
any other program is kept, with its unit, without
running anything more in it, and a submodule is treated the same way as the
repository around it. That matters because a submodule's configuration is a
file of its own, and a plain `git status` in the parent starts a status
inside every submodule and gitlink that reads it and runs any filter it
names; every `git status` niwa runs therefore passes
`--ignore-submodules=all` (and `--untracked-files=all`). A worktree's
removal starts no git process at all: niwa deletes it with its own walk,
after a status of the worktree that carries the same flags and that the
re-check precedes (the late-write residual under Permission scope gives the
gap that still leaves), and a worktree holding a submodule or any other
repository below its top level isn't removed. Each submodule's own unpushed work is judged by its own checks as
a repository of its own. The one git call built outside
`gitCommand` is the read of the person's own credential helper, which needs
the person's real global configuration: it runs once per pass from an empty
directory outside the workspace, under `~/.niwa/w-<key>/scratch/` and
checked for owner and mode first, discovers no repository, runs no program,
and its value goes only to network calls to proof remotes made against the
scratch store. The ancestry and merged-by-content checks run only in the
scratch store, against the tip ids `ls-remote` returned, so a replace ref, a
grafts file or a forged commit-graph in the unit's `.git` can't make an
unpushed commit read as held, and the tree objects `merge-tree` writes land
in the scratch store, not the unit. Git doesn't re-hash objects during a
history walk, so the store has no alternates into the clone: the proof
remote's tip history is fetched into it with no haves offered, the local
side is read in through `index-pack`, and every object the checks read was
hashed from its content on the way in. An object the agent writes into the
clone under a remote commit's id can't take that commit's place in those
checks. That holds
only while nothing else writes into the store, so the store lives under
`~/.niwa/w-<key>/scratch/`, inside the `~/.niwa/` boundary the guide tells
workspaces to deny, never in a shared temporary directory a sandbox may
leave writable; each pass creates it fresh, 0700, and before each use niwa
`Lstat`s the directory and its parent and keeps the unit unless both are
real directories owned by the person's uid with mode 0700. niwa writes the
store's `config` and compares it byte for byte with what it wrote before
each network call, so a planted `url.<base>.insteadOf`, proxy or
certificate setting keeps the unit instead of steering `ls-remote` to a
host that would advertise the unpushed commit as a tip; the fixed
`-c http.proxy=` and `-c http.sslVerify=true` back that up. Only a
proof remote counts as evidence that a
commit is held or merged: the clone URL the workspace configuration gives the
repository, read at the root and never from the instance, or a match for a
`proof_remotes` prefix the workspace lists (Decision 1), matched on scheme,
user, host and port, a default port made explicit, and on whole path
segments, so a remote naming a port the clone URL or prefix doesn't name
doesn't match. A URL with a dot segment, an empty segment, percent-encoding,
a backslash, a control character or userinfo (beyond the bare ssh user
`git`) is refused before any comparison, because git sends a cleaned path
that can name a repository outside the prefix the string appeared to
match; it is never a proof remote, and a commit only it could hold keeps
with "remote URL not allowed" and the reason. Any other remote is
never contacted, and a commit only it could hold keeps the unit with
"remote not recognized". Network calls run against the scratch store, whose
configuration is niwa's own and checked before each call, so no setting
the agent wrote applies to them, with a validated URL niwa builds,
never the string the agent wrote: the workspace's configured clone URL on
a clone-URL match, or, on a prefix match, a URL that takes its scheme,
user, host and port from the prefix and only its path from the remote's
parsed, normalized parts. They pass `http.followRedirects=false`, so a proof host that answers with a
redirect can't hand the call to another host whose refs would then count;
the call fails, the remote reads as unreachable, and the unit keeps. The
mark, `discard.json`, pid records and `unit.json` count only when they match
the ledger outside the instance (Decision 9), and pid records come only
through `niwa helper`, which accepts a process only when it is the caller's
own child. An `in_brief` file counts as handed forward only when niwa hashes
the brief's stored copy and it matches; otherwise it is unique. Every record
is opened with `O_NOFOLLOW` and `O_NONBLOCK`, and `fstat`ed as a regular
file before it's read, from a `.niwa` directory opened without following
links, with a size cap and strict decoding, and any failure lands on the
keeping side. The over command's reason, git evidence and every other string
a unit supplies pass `ScrubEvidence` and `sanitizeRemovalGateText` before
they're stored and again when `niwa list`, `--json` or the dispatch line
reads them back. The over-command guide tells authors to treat the instance
the same way: as data, never as a directory to `cd` into and run `git`,
`make` or a script from, and never a source of files to source.

What is left: git still parses objects, the index and configuration the
agent wrote, and a parser flaw in git is outside niwa's reach. The refusal
covers the program-naming keys git has today and any key named like one
(`*.program`, `*.command`, `*Command`, `*.helper`); a later git key that
names a program under some other kind of name would need adding, though
the fixed overrides and the scratch-store network
calls don't depend on that list. A clone's `objects/info/alternates` can
name directories outside the unit, which git then reads objects from when
it builds the local pack; that is read-only and can't change a verdict,
since those objects are re-hashed by `index-pack` and only a proof remote's
refs count. A workspace that lists a broad `proof_remotes` prefix trusts every
repository under it: an agent able to push there can make a commit read as
held, though the commit then does exist on that host.

Normalization treats the scp-like and `ssh://` forms of a URL as one
remote, and a path with `.git` and one without it as one too. On a server
other than the big forges they can name different repositories: scp-like
`host:path` is relative to the login's home, while `ssh://host/path` is
absolute. A prefix match can therefore count a repository the person never
listed, on a host they did. Later hardening would keep these forms
distinct unless the host is known to treat them alike.

The handoff brief is
written by the unit's own workflow by design, so a brief can still make job
scratch and loose root files older than it disposable, and no ledger can
vouch for it; a ledger entry written by the brief writer is left to the
brief work. The inputs that decide a path's class are agent-written too:
`.gitignore`, `.git/info/exclude` and any `core.excludesFile` the
repository's configuration names, a directory's `CACHEDIR.TAG`, the
`managed_files` list in `instance.json` and a job directory's `state.json`
all sit inside the unit. So do the index's assume-unchanged and
skip-worktree bits, which make `git status` skip a tracked file however
it has changed; those don't decide a class, because the rule reads
`git ls-files -v` and keeps the unit when either flag is set on any entry,
naming the flag and the path (Decision 1), and the tombstone check reads
them again. An agent can therefore make a file holding unique results read as
disposable or regenerable by ignoring it through any of those ignore
sources (released while `ignored_files` is
`"disposable"`, the default), by tagging an ignored directory as a build
tree, or by listing it as a managed file. This reaches only the unit's own
files, which the agent could delete outright anyway, so it changes what a
careless agent loses rather than what an attacker can reach; the controls
are `ignored_files = "unique"`, which makes every ignored file count, and
the verdict-only mode, whose git-ignored notice names each ignored path
before anything is released. The same holds for a commit whose id is a live
tip: niwa trusts the id and doesn't check that the clone's object under it
is the one the remote holds, so an object the agent wrote under a tip's id
can make its own files read as pushed. Like the ignore sources, this
reaches only the unit's own files. Later hardening would check the objects
reachable from each worktree's HEAD against the scratch store, at the cost
of a fetch for every unit. git also trusts the index's stat data, so an
index written to match a modified file's stat still hides the change;
niwa pins `core.trustctime` and `core.checkStat`, so what's left needs a
deliberate forgery of the file's change time, and it too reaches only the
unit's own files. The configuration read follows `include.path` to any file the
repository names, a FIFO included; it runs under a 10-second context, so
such a file can hang it only until the context ends, which keeps. niwa's
own reads of agent-written files (every file niwa opens under an instance,
a job directory, the root's `.niwa/` or `.niwa-launch/`, and a brief's
copy: records, named files, the kept-set move's sources, launch records,
`instance.json` and a job directory's `state.json`) can't be bounded that
way, since the pass's deadline doesn't interrupt an open waiting in the
kernel, so each is opened with `O_NONBLOCK` and checked by `fstat` as a
regular file before any read: a FIFO planted in one's place fails the
check, which lands on the keeping side, and can't hang every dispatch on
the host. A named file the rule hashes is opened component by component
from the instance's or job directory's descriptor, every directory
`O_DIRECTORY|O_NOFOLLOW`, as the kept-set move opens its sources. The
credential-helper read misses a helper scoped to a URL
(`credential.<url>.helper`) or set only in an included file, and the remote
it was needed for then reads as unreachable and keeps. The launch records
under `<root>/.niwa-launch/` sit where the agent can reach them, and a
dispatched session's record carries fields `niwa resume` replays
(permission mode, settings, environment). That is true today; this design
widens it by writing records for hook sessions and adding fields. Every
launch record is decoded strictly, `niwa resume` ignores the replay fields
on a record whose `origin` is `hook`, and `kept_entry` is checked as
Decision 5 describes, but no ledger vouches for a launch record, so the
boundary for the replay fields is the deny-write advice below.

### Permission scope

The pass deletes directories, registered worktrees among them, moves files
between volumes, and runs `claude rm` and on-reap as the person, and
`niwa destroy` and retask run `claude rm` the same way. Claude Code
reads project settings, hook commands included, from the project it runs
in, and an instance's `.claude/settings.json` and `settings.local.json` are
agent-writable, so a `claude` call started inside an instance could run a
hook the agent wrote, unsandboxed. A recorded
path through a symlink could have the kept-set move copy a secret from
outside the instance into a directory that persists (a `keep.json` naming
`bench/link` pointing at `~/.ssh/id_ed25519`), or a restore could write
outside the fresh instance, or into a hook or settings file a later git or
`claude` call runs. The walk could follow a link or a mount out of
the instance, `os.RemoveAll` could be handed a symlinked or misplaced
directory, a recursive delete that goes by path, as `git worktree remove`'s
does, would follow a directory swapped for a link out of the instance, and
a write landing between the last check and the delete is
lost unseen. A directory swapped for a link between a check and an open
would redirect the move or a record write, and two passes overlapping could
have one recover a tombstone the other is still deleting. `niwa discard` is
the one command that releases unique paths without the rule's say, so an
agent able to run it for itself could have its own work deleted.

Every recorded path passes `ResolveRecorded` on every read: relative, no
`..`, `job:` bound to the unit's own job directory, each component `Lstat`ed
with no symlink and no change of device. The kept-set move opens each source
component by component with `O_NOFOLLOW`, `fstat`s the descriptor and hashes
and copies from it, so a swap after the check fails the open, and it takes
only a file with a link count of one, so a hard link to a file outside the
instance keeps the unit with the path named. The trim removes a build tree
the same way, walking from the instance's directory descriptor with every
directory opened `O_DIRECTORY|O_NOFOLLOW`, so a directory swapped for a
link mid-removal can't send the delete outside the instance, and the walk
stops and keeps the rest of the tree at any `.git` entry or git directory
it meets, so a repository created in the tree after the trim's checks
isn't removed with it. Every directory it opens is `fstat`ed too, and one
on a device other than the instance's stops the walk the same way, with
"another filesystem at <path>" named. Every worktree removal, in the pass, the hook
worktree sweep and `niwa destroy` with or without `--force`, uses the same
walk instead of `git worktree remove`: the tree from the instance's
directory descriptor, its `worktrees/<id>` admin directory from the common
git directory's, and only after a check from those descriptors that the
tree's top-level `.git` file names that admin directory. That `.git` file
is the one `.git` entry the walk removes, and it goes last; a nested one
stops the walk as in the trim, and a worktree holding a repository below
its top level isn't removed at all. Every `claude` call niwa makes (`claude agents`,
and every `claude rm`, retask's included) runs from an empty
directory outside the workspace, under `~/.niwa/w-<key>/scratch/` where the
agent is denied writes and checked for owner and mode before each call,
with the filtered `PATH`, so an
instance's settings aren't the project Claude Code reads for it, and no
`.claude/settings.json` written into a shared temporary directory can take
their place; a
functional scenario checks that hooks an instance's settings define run
through neither a reap nor `niwa destroy`. Before every `claude rm` niwa
makes, retask's included, niwa `Lstat`s `~/.claude/jobs/`
and that session's `~/.claude/jobs/<id>`: each must be a real
directory owned by the person, not writable by group or others, with no link
on any component below the home directory, which may itself be a link, and
the session's directory on the same
device as `~/.claude/jobs/`; then it walks `~/.claude/jobs/<id>` with
`Lstat`, following no link, and any entry that is neither a regular file
nor a directory fails the vet, as does any directory under `<id>` whose
device differs from `~/.claude/jobs/`, with "another filesystem at <path>"
named. Any mismatch keeps the unit with the path
named and no `claude rm` runs for it. `.niwa` and
`pids` must be real directories, and every record read and write happens
relative to their `O_NOFOLLOW` descriptors. The restore checks that a
`kept_entry` that is present names a directory directly under
`kept-files/<instance>/`, refuses one present but empty, moves no files for
a record without one, re-validates every path, refuses, naming the file, any destination with a
path component named `.git` or `.claude` (compared case-insensitively), as
well as one outside the instance, creates parents and files with
`O_NOFOLLOW` and never overwrites. Kept-set expiry removes only a direct-child directory of
`kept-files/<instance>/` that it found by enumeration and `Lstat`ed as a
real directory, never a path read from `entry.json`, so an edited entry
can't point the removal anywhere else. The hook worktree sweep never forces
a worktree's removal: with force off, `DestroySession` removes a worktree
only after its own status comes back clean. Every worktree niwa removes is
one its own walk found, opened from the instance's descriptor, never a path
read from `git worktree list` or a `gitdir` file, so an edited worktree
entry can't name a directory elsewhere. The walk never follows a link,
stays on one filesystem and keeps a unit whose git directory sits outside it. `DestroyInstance` refuses a
symlink and anything that isn't a direct child of the root. On Linux and
macOS Go's `os.RemoveAll` removes descriptor-relative, opening each
directory from its parent with `O_NOFOLLOW` and unlinking a link as a link,
so a directory swapped for a link while it runs is removed as a link and
nothing behind it goes; the implementation keeps a test that pins this. The tombstone
rename (Decision 1) takes the instance's path away from late writers and
repeats the cheap checks before the delete, renaming back on any change; the
tombstone lock, held shared by every pass that holds a tombstone and taken
exclusively for recovery, keeps recovery away from a live pass's tombstone.
Recovery `Lstat`s a tombstone before renaming it back and goes on only for
a real directory that is a direct child of the root, so a
`.niwa-tombstone-*` symlink can't have it rename or evaluate a directory
elsewhere. The check on the tombstone reruns the program-naming
configuration check on every repository before its `git status`, as the
invoker does before every other `git status` and every worktree removal on
every removal path (the rule's status in each repository, the re-read,
each worktree's status and removal, the hook sweep's `DestroySession` and
destroy's scan), so a driver planted after the first
check still runs nothing and keeps the unit, with "changed during the
pass" and the key named; the residual below gives the gap that's left. The
tombstone check also compares a ref snapshot taken with the re-read, so a commit, branch,
tag or stash made in between, which leaves `git status` clean, renames the
tombstone back and keeps the unit with the ref named. A worktree niwa
removes takes its admin directory with it, so its HEAD and per-worktree
refs are read again right before niwa removes it: a detached-HEAD
commit or a new `refs/worktree/*` or `refs/bisect/*` entry made since the
re-read keeps the unit before that worktree goes, and the tombstone check
lets a removed worktree's entries be missing only when they matched the
snapshot at that read, and lets a path the kept-set move took out read as
missing or deleted, while anything found at such a path again counts as a
change.
Lock files in `~/.niwa/locks/` are created 0600 with `O_NOFOLLOW`, and the
mark clean-up after an over answer runs under the instance lock. niwa uses no
sudo, opens no listener and passes no force flag to `claude`; in the pass,
the reap-time sweep and `niwa destroy` it passes none to git outside
`niwa destroy --force` and runs neither `git worktree remove` nor
`git worktree prune`, which a functional test checks.

`niwa retire`, `niwa discard` and `--override-not-over` refuse inside an
instance, by working directory, by `NIWA_DISPATCH_ID` and `NIWA_REAP_KEY`,
and by process ancestry. That guard is a convenience, not the control. For
a plain retire it doesn't need to be more, because the rule still decides
what goes. For `niwa discard` and `--override-not-over`, which loosen the
rule, the control is `confirmOnTerminal`: the command needs a controlling
terminal, prints a random code drawn fresh for that call, and needs the code
typed back on it, so a detached or `setsid` process with no terminal is
refused. The code is there because a tool-run process can inherit the
person's controlling terminal and, on macOS, push input into it with
`TIOCSTI`; the instance name is known to the agent, so typing it back would
prove nothing, while the code is written to the terminal, which a process
that can only push input can't read. The code stops a process that can
only type into the terminal. A process running as the person that can read
the screen, through `tmux`, `screen` or terminal scripting, can answer it,
which is why, behind that, discarded paths are never deleted at reap
but moved into the kept set until the restore window ends.

What is left: after the tombstone check, a process that already held a
descriptor inside the tree and that the process listing missed (a
short-lived one `lsof` didn't see, or one in a container, a virtual
machine, another user's account or another mount namespace, which a listing
of this host's processes can't see at all) can still write there before
`os.RemoveAll` finishes. Such a writer also leaves its unit looking stopped
at liveness. A helper registered through `niwa helper` can write there too,
and the listing sees it but counts it as it does at liveness, so it doesn't
keep a stopped unit: it's the one writer the design knows is alive during
a reap, and its working directory moves with the rename, so its relative
writes still land in the tombstone. The mount and device check runs in the walk, and
the tombstone check doesn't repeat it: a filesystem mounted inside the tree
after the walk isn't seen, and `os.RemoveAll` descends into it and deletes
what it can reach there. Mounting one takes root on most hosts, or a FUSE
or disk-image mount the person's account is allowed to make, and whatever
such a mount exposes the agent could already delete as the person. An agent that allocates its own pseudo-terminal
(`script`, `expect`) can answer the terminal prompt itself; what it gains is
a discard whose paths still sit in the kept set until the window ends, so a
person who reads the listing can get them back. The agent runs as the
person, so niwa can't stop it from writing outside its instance; whether it
can is up to the sandbox it runs under. niwa takes the home directory from
`HOME`, so a process that starts niwa with a `HOME` of its choosing points
the ledger, the kept set, the scratch directory and the credential-helper
read at files it wrote. The guide says to run niwa inside the agent's
sandbox, never as a command the sandbox lets out; later hardening would take
the home directory from the password database. The guide names every place
outside the instance whose contents drive removal and restore, or that
niwa runs, and tells workspaces to deny the agent writes to them:
`~/.niwa/` (the ledger, the kept set, the locks, and the scratch directory
that holds the scratch object store and the empty directories `claude`
and the credential-helper read run from); in the workspace root
`.niwa/` (whose `workspace.toml` holds the clone URLs, the `proof_remotes`
prefixes and the over command), `.niwa-launch/` and `.niwa-tombstone-*`;
`~/.gitconfig` and `~/.config/git/config`; the whole `~/.ssh/`
directory, with any file outside it that an `Include` in `~/.ssh/config`
names, since ssh follows `Include` while the credential-helper read
doesn't follow git's includes; any script or library those files name that
the agent could write (a credential helper's script, a `ProxyCommand` or
`Match exec` target, a `PKCS11Provider` library); Claude Code's user
settings with the user-scope plugins, which can define hooks or programs
`claude` runs, in the config directory niwa resolves as Claude Code does
(Data Flow step 7), which every `claude` call niwa makes reads, hooks
included, and whose settings' `cleanupPeriodDays` sets the default
restore window; and Claude Code's user-scope MCP file, which can name
programs `claude` runs too: `.claude.json` in the home directory when
`CLAUDE_CONFIG_DIR` is unset, beside the default config directory rather
than inside it, and `.claude.json` inside the directory the variable names
when it's set. Later hardening would pass a hooks-off flag on niwa's
`claude` calls, once Claude Code offers one. The implementer confirms once
whether the `claude` subcommands niwa runs (`claude agents`, `claude rm`)
start MCP servers. An agent that can write the
root's `workspace.toml` chooses the hosts that count as proof and receive
the person's credential helper, and the over command niwa runs before
every dispatch, so that file is trusted only as far as the agent is kept
from writing it; later hardening would record its hash at `niwa apply` and
refuse network calls and the over command when it changed. The guide names
the mechanism too: sandbox write restrictions that also cover Bash (Claude
Code's sandbox or an operating-system equivalent), since a path deny rule
in Claude Code's permission settings covers its file tools and not a shell
command that writes the same path. All of these places are outside the
agent's working directory, but that's where they are, not what stops a
write: a worker without such a sandbox has no boundary at all, the ledger
is advisory there, and the guide says so. Under such a sandbox
`niwa helper` can't write the ledger either, since it runs in the agent's
context, so a helper the agent starts can't be registered and keeps its
unit, which is the safe side. The over command is code the
workspace chose, with the same trust as hooks and the removal gate; it now
runs more often, inside every dispatch, under its fixed environment,
process-group kill and the shared 30-second budget.

The tombstone check compares git state and the records, not every path.
A write after the re-read to a path outside every repository (a loose file
at the instance root), to a git-ignored path inside one, or into the job
directory isn't seen, and it's deleted with the rest; the job directory is
never tombstoned at all, and `claude rm` removes it after the re-read. This
needs a writer the process listing missed, or a registered helper, as in
the case above. That delete is Claude Code's, done by a mechanism this
design doesn't state, since `claude rm` takes a session id, not a path.
niwa's vet runs right before each `claude rm` and checks the chain down to
the job directory's root and every entry under it, keeping the unit on any
link or special entry. What it can't stop is a race: a subdirectory swapped
for a link while Claude Code deletes the tree, or, between the vet and the
call, the root or an entry under it swapped, a new link or special entry
added under it, or a filesystem mounted there. If Claude Code's delete
follows such a link, what goes is whatever the link reaches that the person
can delete, outside every unit and outside the agent's sandbox.
The vet covers `~/.claude/jobs/<id>` under the resolved config directory
and nothing else; whatever else `claude rm` removes is Claude Code's own
and outside the vet. The implementer confirms once what `claude rm`
removes and whether its delete follows links.
The trim has the same gap in a smaller form: its removal
walk stops at a repository it meets, but a plain file written into a build
tree after the trim's checks is removed with the tree. Later
hardening would record a manifest of path, size, mtime and ctime for the
paths outside every repository and for the job directory during the
evaluation, compare it at the tombstone, and re-check the job directory
against it before `claude rm`. A commit or a file written in the instant
between a worktree's last reads (its refs, its configuration and its
status) and niwa removing it is lost the same way. The removal doesn't
widen that: it works through descriptors, not paths, so a writer racing it
can lose its own write but can't send the delete outside the instance. A
file written into the tree while the walk runs is either removed with it
or leaves a directory the walk can't remove, which stops the walk and keeps
the rest.

Passes can overlap, and removing a scratch store an earlier pass left takes
no lock, so one pass can remove a store another pass is still using. The
other pass's next git call against it then fails, and its unit keeps, so
the cost is a wasted check, not a wrong verdict. Later hardening would hold
a lock per store, shared while it's in use, and remove a leftover only when
that lock can be taken exclusively.

One delete still goes by path: the `git branch -d` that follows a
worktree's removal has git remove the branch's ref file and its reflog in
the common git directory by path. A directory there swapped for a link
between niwa's checks and that call can point git at a file of the
branch's name somewhere else; what can go is that one file and any empty
directories git prunes above it, not a tree.

The configuration re-checks narrow the gap for a planted filter; they don't
close it. Between niwa's `git config --list` and git's own read of the
configuration at the start of the `git status` that follows (a worktree's
removal starts no git process, so the status before it is the call left
there) there's a sub-second gap, and a writer that watches for
niwa's read can still win it; the tombstone check's re-run lives with the
same race. The writers that can try are a helper registered through
`niwa helper`, which the design lets live through the reap with its
working directory inside the instance, and any process the listing missed
or whose working directory and open files sit outside the instance. Under
a sandbox that confines the agent's writes a helper can't be registered,
but an unregistered one still lives: it keeps its unit, yet the rule still
runs every check there, its status included, so it can race that status
the same way. What a
win costs is more than one unit's files: the filter runs as the person,
outside any sandbox the agent runs under, and can write `~/.niwa/` or the
root's `workspace.toml`, which defeats the deny-write boundary the ledger,
discard records and pid records rest on.

### Supply chain or dependency trust

niwa installs nothing new: no library, binary or plugin. The one thing it
downloads is data, the proof-remote history the held and merged checks fetch
into the scratch store (Decision 1), which nothing runs, capped at 1 GiB per
store and costed in Consequences. Its trust inputs are the over command, which
comes from workspace configuration like the hooks it already runs; the git,
`lsof`, `claude` and `sh` binaries; git's version, since
`merge-tree --write-tree` needs 2.38 and `GIT_NO_LAZY_FETCH` 2.45; and
formats borrowed from the earlier remover, which hasn't landed.

Git, `lsof` and `claude` are resolved once per pass to absolute paths from a
`PATH` with empty and relative entries dropped and every entry that
resolves, through symlinks, inside the workspace root removed, so an
instance can't put a binary in front of them; session commands run under
`/bin/sh` by absolute path with that same `PATH`. `lsof`'s output is read
with NUL-terminated fields (`-F 0pn`), so a file name carrying a newline
can't forge a pid or path field in the process listing. That is a narrow control: it
covers binaries planted in the workspace, not a directory elsewhere on the
person's `PATH` that the agent can write as the person. An older git makes
the checks that need it
undecidable, which keeps, and a partial clone on a git older than 2.45
keeps after the configuration read, before any call that could read an
object. niwa's mark reader is pinned to the earlier
remover's exact schema name and field set and reads any other version as no
mark, so a change there can only make niwa count fewer marks, and a
conformance fixture from that work's writer joins the tests when it lands.
The `review` field on a workspace `disposable` pattern is free text niwa
doesn't check; it is an audit trail, not a control. The koto marker
`koto/superseded-carry-complete` is read only by over commands, never by
niwa.

What is left: the binaries on the person's own `PATH` outside the workspace
are trusted, including any the agent managed to write there, and an over
command can do anything its author wrote; niwa bounds its environment and
its time, not its behavior.

### Data exposure

The kept set copies files a person asked to keep, which can include `.env`
files, keys or results, into `~/.niwa/w-<key>/kept-files/`, where they stay
until their due date and survive `DestroyWorkspace` on purpose. Git error
text can carry a remote URL with a token in it into `unit.json`, the listing,
`--json` verdicts and the dispatch output. Left open, `ls-remote` and the
scratch fetch would contact every remote a repository names, telling hosts
the agent chose the machine's address, offering them the person's ssh keys,
and reaching internal addresses (loopback, a link-local metadata endpoint)
on the agent's behalf.

Kept-set entry directories are 0700 and their metadata 0600, nothing is
copied through a link, and `niwa list --kept` shows paths and sizes, never
contents. `ScrubEvidence` strips URL userinfo, `x-access-token`, known token
shapes (GitHub, GitLab, `sk-`, Slack `xox`, AWS key ids, JSON web tokens),
the values of secret-named `key=value` pairs and query parameters, and long
mixed-case base64 runs, then bounds length, so truncation can't leave a
token's front. Network calls go only to proof remotes, with no credentials
except the person's own helper for those, and ssh runs in batch mode with
agent forwarding off; a remote the agent added is never contacted.
`unit.json`, the records niwa writes, the ledger and the launch-record fields
are 0600. `NIWA_SESSION_IDS` and `NIWA_REMOVE_PATH` go only to the configured
over command, whose fixed environment keeps other secrets out unless the
workspace lists them in `env`. Transcript modification times and sizes are
read, never copied or sent, and no telemetry is added.

What is left: a host behind a `proof_remotes` prefix the workspace listed
learns the machine's address and, over ssh, the public keys ssh offers it:
those in the key files the person's ssh configuration and ssh's defaults
name that ssh can use without a prompt, and an agent's keys only when that
configuration names the agent with `IdentityAgent`, since git runs without
`SSH_AUTH_SOCK`. That is no more than the person's own pushes show it.
Batch mode makes an unknown host key a failure rather than a question, so
a proof host missing from the person's `known_hosts` reads as unreachable
and its units keep until the person connects to it once; niwa never adds a
host key. ssh also honors the person's own `~/.ssh/config`, so a
`ProxyCommand`, `Match exec` or `ProxyJump` there runs or applies on every
ssh call niwa makes to a proof remote. ssh follows `Include`, so the same
holds for every file that configuration includes, inside `~/.ssh/` or
outside it (a tool's own ssh configuration, a dotfiles directory); the
credential-helper read, by contrast, doesn't follow git's includes. Those
files are trusted like the binaries on the person's `PATH`: they sit
outside the workspace, and an agent able to write them as the person could
change them whether or not niwa ran.
The person's `~/.gitconfig` and `~/.config/git/config` sit in the same
place with more at stake: niwa reads their `credential.helper` and starts
it on proof-remote calls, so a `!<command>` helper an agent wrote there
runs as the person, outside the agent's sandbox, on the next pass that
needs a fetch, with no action by the person. These files, the whole
`~/.ssh/` directory and every file an `Include` in `~/.ssh/config` names
among them, are trusted only as far as the agent is kept from writing
them, which is why the guide lists them with `~/.niwa/`.
Scrubbing is pattern-based, so a short secret with no telling key name or
shape inside a git error could still reach `unit.json`, which is 0600 but
inside the instance, where the agent can read it. Evidence names files (the
untracked or modified path that keeps a unit, a kept file, a discarded
path), and it is printed on the dispatch's standard error, in `niwa list`
and in `--json` verdicts; scrubbing removes token shapes from those names,
not the names themselves, so a file name that is sensitive on its own (a
customer's name, say) shows wherever that output goes, a terminal log or a
CI transcript included. Kept files, discarded paths included, are exactly
as sensitive as the files they were. The person's credential-helper value
goes on the command line of each network call to a proof remote, as
`-c credential.helper=<value>`, so while that call runs it shows in a
process listing to anyone on the host who can list processes, the agent
included. niwa never prints or stores the value. The agent gains nothing
from it, since it runs as the person and can read the same value from the
person's global configuration; a helper whose command line itself carries
a secret is shown the same way whenever the person's own git starts it, so
niwa adds no exposure beyond what that helper already has.

git runs the person's helper on niwa's calls as it would on the person's
own, so the helper gets `store` after a call that authenticates and `erase`
after one that fails authentication, not only `get`. An agent that picks a
repository path under a `proof_remotes` prefix whose host answers 401 can
therefore make niwa erase the person's stored credential for that host,
and every successful call writes to the person's store. The person then
signs in again; nothing leaves the machine. Later hardening would forward
only `get` to the person's helpers.

### Data loss

The feature deletes on its own, so a wrong verdict loses work nobody chose to
lose. The rule keeps on doubt throughout: every check runs, any error or
undecidable answer keeps, a record that fails validation lands on the
keeping side, removal is never forced, and in a reap or retire the gate,
the re-read and the tombstone check stand between the verdict and the
delete. Only a proof
remote can show a commit is pushed or merged, so a remote the agent added
can't launder unpushed work. The commit checks count every ref
`for-each-ref` lists and each worktree's HEAD from `git worktree list`, so
a commit made on a detached HEAD in a worktree and never put on a branch
keeps the unit like any other unpushed commit. That listing, like every git
output niwa parses, is read NUL-terminated (`--porcelain -z`), so a newline
written into a worktree's `gitdir` can't forge a HEAD line that hides one.
A worktree git lists that the walk never reaches, such as one added outside
the instance, keeps the unit: its working files sit elsewhere, but its
index, its `refs/worktree/*` and `refs/bisect/*` and its objects live in
the instance and would go with it, unchecked where the same refs and index
in a worktree inside the instance are checked. A prunable one, whose
working tree is already gone, is judged by its HEAD, per-worktree refs and
index read from its admin directory, or kept. The main entry is held to the
same test: it counts as reached only at the work tree the walk found for
its git directory, and a git directory inside the instance whose main work
tree sits outside it, set by `core.worktree` or by a `.git` file elsewhere,
keeps the unit for the same reason, since a file staged only in its index
would otherwise go unchecked. A forged mark, discard record or pid record
counts for nothing without its ledger entry, a discard needs a person at a
terminal, and what a discard releases waits in the kept set until the
window ends. An `in_brief` file counts as handed forward only when the
brief's copy hashes to the same bytes. Tombstone recovery never touches a
tombstone a live pass holds. The verdict-only mode shows every outcome
before it happens, named files go to the kept set with a hash check, and the
restore window brings the session and those files back.

What is left: the restore window restores the session and kept files, not
paths the rule found disposable; discarded paths are kept but not put back,
so a person copies them from the kept set. The ledger's value rests on the
agent not writing `~/.niwa/`; an agent that can write there can forge a
ledger entry as easily as a record, which is why the guide tells workspaces
to deny the agent writes to `~/.niwa/`, and to the root's `.niwa/`,
`.niwa-launch/` and `.niwa-tombstone-*`, with sandbox write restrictions
that also cover Bash, not path deny rules alone; where a worker runs
without one, the ledger is advisory. And a brief written by the unit's own workflow can still release
scratch and loose root files older than it.

A reap that stops after the kept-set move, because the tombstone renames
back or a later step (worktree removal, or the configuration re-check or
status before it, `claude rm`, on-reap) disagrees,
leaves the unit listed with its named and discarded files already in a
kept-set entry no launch record points at, and that entry's parts still
leave on their due dates. A named file tracked in a linked worktree makes that worktree
dirty once it's moved, so the status niwa runs before removing it reports
the change, the removal keeps, and this happens
with no race at all. The loss isn't silent, since the entry is listed and
shown due a day before it leaves, but files a person asked to keep can
expire while their unit stays. Later hardening would move the files back,
which were hashed on the way out, when a reap stops, or do the kept-set
move last, after `claude rm` and on-reap and just before the tombstone.

A kept-set entry has no size cap, and the move doesn't gate which kinds of
hold it acts for: whatever a keep record or a discard names moves, however
large, and takes that much of the home disk until the entry is due. A move
that runs out of space fails a write, which keeps the unit with nothing
removed (Decision 5), so the cost is disk, not loss. Later hardening would
cap an entry and keep a unit whose move would pass the cap; niwa doesn't do
that now.

The seven-day floor holds only for a `restore_window` the workspace sets.
With the setting unset, the window is Claude Code's `cleanupPeriodDays`,
which a person, or an agent able to write Claude Code's user settings in
the resolved config directory, can set
to a day or less; the paths a discard released and the kept files then
leave that soon, which shortens the safety a faked confirmation relies on.
Later hardening would apply the same floor to that value and read a
missing, zero or negative one as the floor.

Files inside `.git` other than refs, objects and the index have no class
and go with the instance. The one kind of unique work that can sit there is
Git LFS content: a commit pushed while git-lfs's pre-push hook skipped the
upload reads as held, while the large files it points at exist only under
`.git/lfs/objects/`, and they go with the instance. Later hardening would
keep a unit only when its local LFS objects aren't on the remote; niwa
doesn't do that now.

Failed-dispatch cleanup, the watch's discard and the watch's failed-stage
rollback act on the rule's verdict
without the re-read and tombstone check a reap makes, so a write between
their evaluation and the destroy is lost unseen. A failed dispatch's
instance has seen little agent time, which keeps that case small. A watch
review instance hosted a whole review session, so its case isn't small,
and in this release the rule covers it by keeping: the discard and the
rollback remove a review instance only when nothing in it keeps, and its
`pr-clone`, judged by the normal rule, keeps it whenever the fetch wrote
anything there. The draft or any other file the agent wrote keeps it too.
The discard then deletes the staged record, and any rollback whose
instance the rule keeps, before or after the agent launched, appends the
handled entry, so the kept instance is listed as a stopped unit and that
PR head isn't staged again. Such an instance stays until a person destroys
it; removing review instances through the rule once niwa can tell its own
fetch from the agent's work is tsukumogami/niwa#551. What remains is a
write that lands after the evaluation, which is lost unseen as above.

A commit reachable only from a reflog, left behind by a `git reset` or a
rebase, counts for nothing and goes with the instance. git's own garbage
collection would drop it in time, and a stash, which lives in a reflog, is
counted through `stash list`.

A branch whose commits undo each other reads as merged by content, since
its net change is empty. Its intermediate commits go with the instance,
though no file in its result is lost.

## Consequences

### Positive

- Every removal niwa makes on its own gives the same verdict, and a person or
  a plugin can see it first with `niwa reap --verdict-only`.
- Deleting a worker's session by hand no longer risks its unpushed work; the
  reap keeps the instance and names the path.
- A workspace that names an over command sees finished units go at the next
  dispatch with nobody acting, and can restore one for a known window.
- A user of niwa alone sees every stopped unit with answer, age and size, a
  count at every dispatch, and has one command that removes a clean unit
  without `--force`.
- Idle build trees in units that stay are trimmed by default, which is most
  of the disk a kept unit holds.
- Destroy and reap no longer leave sessions behind when an instance holds a
  worktree.
- Hook-provisioned sessions become restorable and can be reaped on a mark.

### Negative

- A dispatch can wait up to 30 seconds longer than today when stopped units
  are many or over commands are slow.
- Reap now keeps instances it used to remove, so hosts that relied on
  deleting sessions to free disk see kept instances listed instead.
- `niwa watch` review instances, which the watch's discard removes today,
  stay kept and listed as units in this release: the rule judges a review
  instance's `pr-clone` like any other repository, and it nearly always
  keeps, so each one stays on disk as a stopped unit until a person
  destroys it. Their removal through the rule is tsukumogami/niwa#551.
- A watch stage that fails after its fetch wrote anything, even once and
  for a passing reason, leaves that PR head handled with no review when the
  rule keeps the instance, and the head isn't retried, even after a person
  destroys the instance, until the next push to the PR; today the next pass
  would retry it. That's the price of never staging a head into one more
  instance each pass, and the rollback's output says so; for a stage that
  fails before launch, tsukumogami/niwa#551 restores the retry.
- niwa follows formats it doesn't own: the mark's schema name, the keep and
  discard record fields and the over contract can still move.
- niwa counts only marks it wrote itself, so a mark the earlier remover or a
  person writes by hand into `retired.json` reads as no mark in niwa, and
  each remover ignores the other's marks.
- A repository whose own configuration names a filter, diff, merge,
  credential or signing program, an fsmonitor hook or any other program (a
  per-repository LFS install, for one, or a local `core.sshCommand` or
  `gpg.program`), a submodule's included, keeps its unit until a person removes
  the setting or destroys the instance.
- A linked worktree that holds a submodule or any other repository below its
  top level is never removed by niwa, so its unit keeps until a person
  removes that repository or the worktree, or runs `niwa destroy --force`.
- A commit that isn't itself a live tip is judged in a fresh scratch store
  under `~/.niwa/w-<key>/scratch/` that offers the proof remote nothing, so
  each such check downloads the
  tip's whole history, which costs network and disk on every pass that
  reaches the unit, up to the 1 GiB cap, and a repository whose history
  exceeds the cap or can't be fetched within the 10-second network context
  keeps its unit whenever such a check is needed. A squash-merged branch is
  the common case that needs one. The store sits on the disk that holds the
  person's home, not in the host's temporary directory, so that disk needs
  room for it, and a scratch directory with the wrong owner or mode keeps
  every unit that would use it until a person fixes or removes it.
- A dotfiles setup that makes `~/.claude`, `~/.claude/jobs` or `~/.niwa` a
  link, or a listed session with no job directory, keeps every unit it touches
  until the person changes it; the evidence names the path. This design
  doesn't know which sessions get a job directory under the jobs root,
  hook-provisioned ones in particular, so the implementer confirms it; a
  listed session without one keeps its unit all the same, with no
  relaxation, and the vet is never skipped for any listed session.
- A unit whose job directory holds a link, socket, named pipe or device
  keeps, and destroy stops with or without `--force`, until a person removes
  that entry. If a Claude Code release starts writing such an entry there,
  every unit keeps until niwa learns that entry, which the clean-unit reap
  scenario would show.
- An ssh proof remote the person reaches only through an ssh agent reads as
  unreachable, since git runs without `SSH_AUTH_SOCK`, unless the person's
  ssh configuration names the agent with `IdentityAgent`.
- A remote URL with a dot segment, percent-encoding or `https` userinfo,
  even one a person wrote, no longer counts as proof, and its commits keep
  their unit.
- Only the workspace's clone URL counts as proof by default, so work pushed
  only to a fork or a personal remote keeps its unit, and a repository the
  workspace doesn't name (one the agent cloned for reference, a nested
  `git init`) keeps its unit whenever it holds a commit, unless its remote
  is a workspace repository's clone URL or matches a `proof_remotes` prefix.
- `niwa discard` and `niwa retire --override-not-over` need a person at a
  terminal typing back a code niwa prints there, so neither can be scripted, and
  `niwa discard` arrives in the third part rather than the first.
- Discarded paths take kept-set disk until the restore window ends, and a
  cross-volume move of a large discarded directory costs a copy.
- Until the brief work names where a brief stores its copies, every file
  flagged `in_brief` counts as unique, so a brief can't yet hand a file
  forward without the kept set.
- Tombstone recovery waits whenever any pass holds a tombstone, so a
  tombstone an interrupted pass left can sit for a while on a busy host.
- A helper's starter has to run `niwa helper`; a pid record written by hand
  no longer releases a live process, and a helper that daemonizes before
  registering can't be registered. Nor can any helper the agent starts
  under a sandbox that denies it writes to `~/.niwa/`, so under the boundary
  the guide recommends every such helper keeps its unit.
- A worker can no longer retire itself or write its own discard record:
  `niwa retire` and `niwa discard` refuse inside an instance.
- The ledger is a second host store per unit; a ledger lost or out of step
  makes every mark, discard record and pid record in its unit count for
  nothing.
- A reap takes a little longer for the tombstone check, and a restore
  refuses outright when a kept file's destination already exists in the
  fresh instance.
- With job scratch disposable by default, a retire of a unit with no brief
  lets its scratch go, and the user least likely to know scratch exists gives
  up that check unless they set it unique.
- The PRD's check that a hook-provisioned unit has no launch record stops
  arising once phase 6 lands.
- Measuring size by walking each stopped unit costs time on large build trees.
- A helper with a pid record keeps running after its unit is reaped, with its
  working directory gone.

### Mitigations

- The budget only caps how many units a pass reaches, never what it decides,
  and units not reached are counted on the dispatch line and taken first at
  the next pass.
- Every kept instance is listed with the check and path that keep it, so the
  fix (push, discard, retire) is named where the person looks.
- Each borrowed format is listed as an open dependency with what settles it,
  and the per-unit record readers accept the earlier forms (a keep record with
  no `protects` reads as a hold).
- A mark never skips the rule, and a mark with no ledger entry is listed as
  "mark not written by niwa", so a person sees why it didn't count and can
  run `niwa retire`; an over command's exit 0 writes niwa's own mark.
- A kept unit's listing names the configuration key that stopped the rule,
  so the fix (drop the setting, or `niwa destroy`) is named where the person
  looks.
- A local commit that is itself a tip the proof remote advertises needs no
  fetch at all, which covers a unit whose branches were pushed and not
  rewritten; the store is removed on every exit path, so the disk is held
  only while the check runs, and a kept unit names the fetch error or the
  cap, and the refused URL with its reason, where the person looks.
- The listing names each unrecognized remote with "remote not recognized"
  and the `proof_remotes` setting that would count it, so a person who
  pushes to forks lists their own prefix once; a reference clone of any
  other remote can be released with a confirmed `niwa discard`, and its paths then wait in the
  kept set like any other discard.
- The terminal prompt is the point: the code stops a process that can only
  type into the terminal. A process running as the person that can read the
  screen, through `tmux`, `screen` or terminal scripting, can answer it,
  which is why discarded paths wait in the kept set until the window ends.
  Ordering the discard after the kept set means no part ever releases a path
  it can't hold.
- Discarded paths are listed by `niwa list --kept` with their size and leave
  on their own due date, the window's end, after a day's notice, even when
  the entry's named files left earlier; within one filesystem the move is
  a rename.
- The `in_brief` check fails toward keeping: an unverified file keeps its
  unit and is listed as "brief copy not verified", so nothing is lost while
  the brief work is pending, and a brief writer that drops the flag gets the
  file into the kept set as an ordinary named file.
- Recovery runs at the first pass that finds no tombstone held, and a held
  tombstone is listed, so the person can see it.
- The guide documents `niwa helper` and says to run it before a helper
  detaches; an unregistered helper keeps its unit, which is the safe side.
  The guide also says registration fails under a sandbox that denies the
  agent writes to `~/.niwa/`, so a sandboxed workspace knows its helpers
  keep their units until registration moves out of the agent's context.
- The over command, which runs from the workspace root, answers for workers;
  a person retires or discards from a terminal outside the instances.
- Every failure of the ledger lands on keeping, and the next over answer,
  retire or discard writes a fresh entry.
- The tombstone check reuses the pass's own listing code and runs only for
  units already passing the rule; a refused restore leaves the kept-set entry
  in place and names the file, so nothing is lost.
- The verdict-only mode marks scratch it would let go with a `job scratch`
  notice, and the over seam's guide tells shirabe workspaces to set
  `job_scratch = "unique"`.
- The hook-session check's no-launch-record setup moves to a unit whose
  launch record was removed by hand, a PRD amendment recorded after
  acceptance.
- Size is measured in the rule's own walk and stored in `unit.json`, so the
  listing and dispatch line read it rather than walking again.
- A pid record is the starter's statement that its helper ends on its own; a
  helper with no record keeps its unit.
- A kept review instance is never staged into a second one at the same PR
  head, since any kept rollback appends the handled entry, so the disk it
  holds grows with the PR heads reviewed, not with watch passes; the
  listing names each one with its keeping check, and `niwa destroy` removes
  it until tsukumogami/niwa#551 lets the rule do so.
