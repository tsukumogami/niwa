---
schema: prd/v1
status: Done
problem: |
  Developers who run many dispatched sessions in one workspace can't rely on
  niwa's session records at teardown. `niwa worktree destroy` can't resolve the
  session id or handle a developer holds, so its merged-branch check never runs
  before `niwa reap` deletes the instance, and run from the workspace root it
  looks for worktree records in the directory that holds session mappings.
goals: |
  Teardown by the id a developer or script already holds reaches that
  session's niwa-managed worktrees and applies the merged-branch check, or
  reports, in a form a script can act on, that there was nothing to check. No
  worktree subcommand treats a multi-instance workspace root as an instance.
absorbed: docs/briefs/BRIEF-session-store-teardown.md
source_issue: 292
---

## Status

Done

Absorbed [BRIEF-session-store-teardown](docs/briefs/BRIEF-session-store-teardown.md); carried in Absorbed Brief.

## Absorbed Brief

**Why this work exists.** Developers reclaim finished workers with a fixed
sequence: destroy the worker's niwa-managed worktrees, stop and remove the
Claude session, then `niwa reap`. Only the first step refuses to delete an
unmerged branch, and today it can't resolve any id a developer holds, so the
sequence runs with its one safety check silently skipped (#292).

**The outcome a user should experience.** A developer or cleanup script
tearing down a finished worker names it by the id they already have and gets
the merged-branch check applied, or a plain statement, in a form a script can
act on, that there was nothing to check.

**Where the boundary sits.** Teardown covers niwa-managed worktrees only; it
reports honestly rather than extending the check to worktrees Claude Code
creates itself or to branches in an instance's own clones.

## Scope

This document covers teardown id resolution and the workspace-root refusal
(niwa issue #292), which shipped together.

It was originally written to cover niwa issue #297 — ordering concurrent
refreshes of a configuration directory against each other and against niwa's
own writes — as well, on the reading that both issues left the same store
untrustworthy. The work was split during implementation once it was clear the
two halves meet at exactly one call site: teardown's single read of the session
mapping store. That read ships unlocked here, as every reader of that store is
today, and #297 replaces it with a locked read.

The concurrency requirements were removed from this document rather than
carried unmet. Marking a requirement Done that no code satisfies is worse than
having no requirement written down, because a later reader has no way to tell
the two apart. #297 authors its own chain, from the research and review
material the implementing session preserved and from the findings recorded on
the issue itself.

**Two requirement numbers were reused, so a citation written against an earlier
draft can resolve to the wrong requirement instead of failing.** R1-R10 keep
their original numbers and wording. The two survivors from the removed sections
were renumbered: the old R21 (no other behavior changes) is now **R11**, and
the old R24 (pre-fix evidence) is now **R12**. In the earlier draft R11 was a
concurrency requirement and R12 did not exist. Anything citing R11 or R12
against that draft — a comment, a commit message, a review note — now points at
a different requirement silently. The removed numbers R13-R25 resolve to
nothing, which at least fails loudly.

## Problem Statement

niwa keeps two kinds of session record. A dispatched session's mapping,
stored at the workspace root, is the only durable record of which instance
the session became; `niwa list` and the reaper read it. A niwa-managed
worktree's lifecycle record, stored inside its instance, is what
`niwa worktree destroy` reads before removing the worktree and deleting its
branch only if that branch is merged.

At teardown the two records never meet. The ids a developer or a cleanup
script holds for a finished worker are the Claude session id (a UUID) and the
short handle `claude agents` shows. `niwa worktree destroy` accepts neither,
and run from the workspace root it looks for worktree records in the directory
that holds the mappings. Every attempt fails, the merged-branch check is
skipped, and the `niwa reap` that follows deletes the instance and any
unmerged branch in it. The failure looks the same as a check that passed
(#292).

The mappings live inside the workspace-root configuration directory, which
every provisioning command refreshes by rebuilding it elsewhere and swapping it
into place. Teardown therefore reads a store that a concurrent refresh can be
moving. Ordering those against each other is #297; what this document requires
of teardown is that the read fail safely, which R7 and R8 pin by making
resolution act on an enumerated instance directory rather than on a recorded
string.

## Goals

- A developer tearing down a finished worker uses the id they already have,
  from the workspace root or from inside an instance, and gets the
  merged-branch check applied before anything is deleted, or a plain
  statement that the session has no niwa-managed worktree.
- A cleanup script can tell from exit status and stable output whether
  teardown destroyed something, found nothing to destroy, couldn't resolve
  the id, or refused.
- No worktree subcommand run at a multi-instance workspace root treats that
  root as an instance, or reads its session mapping store as though those files
  were worktree records.

## User Stories

- As a developer clearing out finished workers from the workspace root, I
  want to run `niwa worktree destroy` with the session id or handle
  `claude agents` gave me, so that each worker's niwa-managed worktrees are
  torn down with the merged-branch check applied before I stop and reap it.
- As an automated cleanup script working through `claude agents --json`, I
  want teardown's exit status and output to distinguish "destroyed",
  "nothing to destroy", "no such session" and "refused", so that I never read
  a failed lookup as a clean result and never reap past a kept branch without
  logging the warning.
- As a developer who already ran `claude rm` on a session started by the
  ephemeral-session hook, whose mapping records no handle, I want its short
  id to still find its instance, so that removing the Claude session first
  doesn't strand the worktree check.
- As a developer tearing down a Codex worker, whose handle is its full
  session id, I want that one string to work, so that teardown doesn't
  depend on which agent ran the worker.
- As a developer inside an instance, I want `niwa worktree destroy <id>` with
  the id `niwa worktree list` shows, and `--by-path`, to keep working exactly
  as today, so that the fix costs me nothing.
- As a developer standing at a multi-instance workspace root, I want
  `niwa worktree list` to tell me I am at the root rather than printing an
  empty table built from session mappings, so that I am not misled into
  thinking the instance has no worktrees.

## Requirements

### Terms

- **Session id**: the id an agent assigns a session, recorded as the mapping's
  key. For Claude and Codex it is a lowercase UUID.
- **Handle**: the string recorded in a mapping as the agent's own name for the
  session. For Claude it is the name of the session's record directory (the
  `id` field `claude agents --json` reports, 8 lowercase hex characters in
  every observed case); for Codex it equals the session id.
- **Worktree id**: the 8-hex id of a niwa-managed worktree's lifecycle record,
  unique within its instance.
- **Active worktree**: a niwa-managed worktree whose lifecycle record is not
  marked ended or abandoned, whether or not its directory still exists.
- **Instance of this workspace**: a directory that, after symlinks are
  resolved, is one of the instance directories niwa enumerates under this
  workspace's root.
- **Instance location**: a path that, cleaned lexically, is a direct child
  directory of this workspace's root other than `.niwa`. Every instance of the
  workspace sits at an instance location; a removed instance's path still
  names one.
- **Newest mapping for an instance**: of the mappings whose recorded instance
  path is that instance, the one with the latest recorded `created` time; on a
  tie, the first in session-id order. This is the ordering `niwa list` already
  uses to pick an instance's session name.
- **Single-instance layout**: a workspace whose root is itself an instance:
  niwa enumerates no child instance under it, and the root's `instance.json`
  names an instance. Every other workspace is multi-instance, including one
  with no instances yet, and including one whose root `instance.json` names no
  instance, which is what a registered `niwa init` writes.
- **`NIWA_INSTANCE_ROOT`**: when this environment variable is set it names the
  instance directory verbatim, taking precedence over the working directory,
  and is never refused by R10. niwa sets it only to an instance root.

### Functional: teardown id resolution

- **R1.** `niwa worktree destroy <id>` accepts, in addition to a worktree id
  and `--by-path <path>` (both unchanged), a dispatched session's session id
  and its handle.
- **R2.** A session id or handle resolves through the workspace's session
  mapping to that session's instance, and from there to every active worktree
  in that instance, taken in worktree-id order. Each is destroyed with the
  guards `niwa worktree destroy` applies today: refuse an attached worktree,
  refuse one with uncommitted changes, and delete its branch only if merged,
  keeping an unmerged branch with a warning naming it. A refusal leaves that
  worktree in place and does not stop the others from being processed.
  `--force`, which today bypasses all three of those guards for one named
  worktree, is refused when the value resolves to a session: it would delete
  every unmerged branch in the instance, which is the loss this feature exists
  to prevent. Forcing stays available one worktree at a time.
- **R3.** Session resolution works the same from the workspace root, from
  inside any instance of the workspace, and from inside a niwa-managed
  worktree. From any directory outside the workspace, the command fails as it
  does today. When `NIWA_INSTANCE_ROOT` is set it decides the instance and the
  working directory is not consulted.
- **R4.** A value matches a mapping when it equals the mapping's session id,
  equals its recorded handle, or, for a mapping that records no handle and
  only when the value is exactly 8 lowercase hex characters, is the first 8
  characters of its session id. Matching is exact and case-sensitive. A value
  that matches more than one mapping, by any of these routes, is refused.
- **R5.** Inside an instance, a value that is a worktree id there and also
  matches a mapping (per R4) is refused. The refusal names both matches and
  says to pass the full session id to act on the session, or
  `--by-path <worktree path>` to act on the worktree. A full session id never
  collides with a worktree id.
- **R6.** When the resolved mapping is not the newest mapping recorded for its
  instance, teardown refuses and names the newer session, because that session
  now owns the instance.
- **R7.** A resolved mapping's recorded instance path is checked in this
  order, and the first rule that applies decides the outcome:
  1. The path is not an instance location: refused, nothing destroyed.
  2. Nothing exists at the path, tested without following symlinks: the R9
     "instance directory no longer exists" outcome.
  3. Something exists at the path but it is not an instance of this workspace
     (a symlink resolving elsewhere or to nothing, or a file): refused,
     nothing destroyed.
  4. R6 applies.
  5. The instance's active worktrees are processed per R2.
- **R8.** Teardown by session never deletes the mapping, the instance, or the
  instance's repository clones. Running it again after it has destroyed a
  session's worktrees reports that there is nothing to destroy.
- **R9.** Outcomes of `niwa worktree destroy` resolved by session follow this
  contract. "stdout" and "stderr" name the stream each line goes to.

  | Outcome | Exit | Output |
  |---|---|---|
  | One or more worktrees destroyed, none refused | 0 | stdout: one `session: destroyed <worktree-id> (<repo>) at <path>` line per worktree. stderr: the existing `warning: branch <name> was not deleted ...` line for each kept branch |
  | Nothing to destroy: no active worktree in the instance | 0 | stdout: `session: nothing to destroy: session <session-id> has no active niwa-managed worktree in <instance>; branches outside niwa-managed worktrees were not checked` |
  | Nothing to destroy: the instance directory no longer exists | 0 | stdout: `session: nothing to destroy: instance <path> for session <session-id> no longer exists` |
  | At least one worktree refused by a guard | 1 | stdout: the destroyed lines for the others. stderr: the kept-branch warnings for the destroyed ones, plus one `niwa: error:` line per refused worktree with the guard's existing message |
  | Mapping refused (R6, R7) | 1 | stderr: `niwa: error: session <session-id> <reason>`, with one exception: a failure to enumerate the workspace's instances emits `niwa: error: enumerating instances: <reason>`, which names no session because the refusal is not about one |
  | `--force` passed with a session id or handle | 2 | stderr: `niwa: error: --force applies to one worktree; pass a worktree id or --by-path <worktree path>` |
  | The value matches no worktree and no session | 3 | stderr: `niwa: error: no worktree or session matches "<value>"` |
  | The value is ambiguous (R4, R5) | 4 | stderr: `niwa: error: "<value>" matches <match>, <match>`; when a worktree id is among the matches, also the R5 guidance |

  `<instance>` and `<path>` are both the instance directory's path. The
  enriched `session: destroyed <worktree-id> (<repo>) at <path>` line is
  emitted only when the value resolved to a session: destroy by worktree id and
  by `--by-path` keep today's bare `session: destroyed <id>` line and their
  current stderr, and their exit codes are unchanged except when nothing is
  found. Both no-match forms exit 3: a worktree id matching nothing, and a
  `--by-path` path resolving to no worktree, which exits 1 today. Two routes to
  the same outcome must not differ by which one located the target, and a
  cleanup script calling `--by-path` after a reaper pass is exactly the caller
  that needs to tell "already gone" from a guard refusal. The `--by-path`
  message is unchanged; only its code moves.

  Exit 2 stays the usage-error code. These codes are scoped to `destroy`:
  within the `niwa worktree` group, `attach` already exits 3 when the attach
  lock is held and `detach --force` exits 4 after killing a live holder, and
  those keep their meanings. The worktree guide gains a second table so a
  script author reads the codes per subcommand.
- **R10.** At the root of a multi-instance workspace, no `niwa worktree`
  subcommand reads the root session mapping store as worktree records:

  | Subcommand at the root | Behavior |
  |---|---|
  | `destroy <session id or handle>` | Resolves per R1-R9 |
  | `list` | stderr: `niwa: this is the workspace root, not an instance; run inside an instance, or pass a session id to niwa worktree destroy`; no table; exit 0 |
  | `destroy <value>` that matches no session | The R9 no-match line; exit 3 |
  | `destroy --by-path <path>` | As today, except that a path resolving to no worktree exits 3 rather than 1 (R9) |
  | `list --json` | The same stderr line, `[]` on stdout, exit 0 |
  | `apply <x>`, `attach <x>`, `detach <x>`, `create`, and `niwa go <repo> <worktree-id>` | stderr: `niwa: error: this is the workspace root, not an instance; run inside an instance, or pass a session id to niwa worktree destroy`; exit 1. `apply`, `attach`, `detach` and `go` already exit 1 there today, so only the message changes. `create` is the one code change: `create <repo>` already exits 1, but bare `create` exits 2 today because the refusal comes from repo inference rather than from the root check, and the refusal now fires first. `niwa go` gains no session resolution |
  | Shell completion of worktree ids | Offers nothing, as today |
  | The WorktreeRemove hook | Logs and exits 0, as today |

  In the single-instance layout (see Terms), worktree subcommands keep treating
  the root as the instance, as today. A root with no child instance whose
  `instance.json` names no instance is multi-instance for this rule, so the
  table above applies to it.

### Non-functional

- **R11.** Apart from R9's new outcome codes, R10's root-level behavior, and
  the tightened single-instance test in Terms (which changes how a root with no
  named instance is classified), existing invocations keep their prompts
  (none), output and exit codes, and `go.mod` gains no new module.
  `niwa worktree list` at the root keeps exit 0.

### Verification

- **R12.** A test that fails on the pre-fix code shows `niwa worktree
  destroy`, run from the workspace root with a dispatched session's session
  id and with its handle, destroying that session's worktree and keeping an
  unmerged branch with a warning.
## Acceptance Criteria

Criteria marked (pre-fix fails) must fail against the code before this
change. The baseline is the parent commit, and the evidence is a binary built
from it run against the same fixture: unit tests cannot serve, because they
reference identifiers the pre-fix tree does not have.

### Teardown

- [ ] (pre-fix fails) From the workspace root, `niwa worktree destroy
      <session-id>` for a dispatched session whose instance holds one active
      worktree on a merged branch removes the worktree, deletes the branch,
      marks the record ended, prints one `session: destroyed` line, and exits 0,
      with stdin closed. The mapping, the instance directory and its repository
      clones are still present afterwards.
- [ ] (pre-fix fails) Same setup with an unmerged branch: the worktree is
      removed, the branch is kept, the `warning: branch <name> was not deleted`
      line appears on stderr, and the command exits 0.
- [ ] (pre-fix fails) From the workspace root, `niwa worktree destroy
      <handle>` gives the same result, using a mapping whose recorded handle is
      not a prefix of its session id.
- [ ] A mapping whose recorded handle equals its session id (Codex) resolves by
      that string without being reported as ambiguous.
- [ ] From inside a different instance of the workspace, and from inside a
      niwa-managed worktree, both the session id and the handle give the same
      result as from the root.
- [ ] The existing destroy unit and functional tests pass unchanged, and
      destroy by worktree id and by `--by-path` inside an instance produce the
      same stdout, stderr and exit code as before for merged, unmerged,
      attached and uncommitted cases. The only exceptions are the R5
      collision refusal and the no-match exit code 3.
- [ ] A session whose instance has two active worktrees, the first (by
      worktree id) with uncommitted changes and the second clean on a merged
      branch: the second is destroyed, the first is left in place, stdout has
      one destroyed line, stderr has one error line naming the first, and the
      command exits 1.
- [ ] Same shape with the first worktree attached by a live process: same
      result, with the attach refusal on stderr.
- [ ] A session whose instance has no active worktree prints the
      `nothing to destroy ... branches outside niwa-managed worktrees were not
      checked` line and exits 0, and no worktree directory, branch, lifecycle
      record or mapping is created, modified or removed.
- [ ] Running teardown twice for the same session: the second run prints the
      `nothing to destroy` line and exits 0.
- [ ] A session whose mapping exists but whose instance directory is gone
      prints the `instance ... no longer exists` line and exits 0.
- [ ] A value matching no worktree and no session prints the R9 no-match line
      and exits 3, both from the workspace root and from inside an instance.
- [ ] A handle-less mapping resolves by its session id's first 8 characters
      when no other mapping matches; with two handle-less mappings sharing
      that prefix, or with the value also equal to another mapping's recorded
      handle, the command exits 4, names every match, and destroys nothing.
- [ ] A mapping with a recorded handle is not matched by its session id's
      first 8 characters when those differ from the handle (exit 3).
- [ ] The first 7 characters of a matching handle, the uppercase form of a
      matching handle, and a non-hex value each exit 3.
- [ ] From the workspace root, `niwa worktree destroy --by-path <worktree
      path>` gives the same result as before the change.
- [ ] `niwa worktree destroy --by-path <path>` for a path that is no niwa-managed
      worktree exits 3 with its existing message; it exits 1 before the change,
      and this is the one exit code the change moves for an outcome that already
      had one.
- [ ] Inside an instance, a value equal to both a worktree id there and a
      session's handle exits 4, names both, prints the `--by-path` and full
      session id guidance, and destroys nothing.
- [ ] A session whose instance has a newer mapping from another session exits
      1, names the newer session, and destroys nothing.
- [ ] A mapping whose instance path is outside the workspace, one that is the
      workspace root itself, and one that is a symlink resolving outside the
      workspace each exit 1 and destroy nothing.
- [ ] (pre-fix fails) At the root of a multi-instance workspace with at least
      one mapping, `niwa worktree list` prints the R10 root-level message on
      stderr, prints no table, and exits 0.
- [ ] At the same root, `niwa worktree apply <x>`, `attach <x>`, `detach <x>`
      and `niwa go <repo> <x>`, where `<x>` is an 8-hex value that is no
      worktree id in any instance, each print the R10 root-level error on
      stderr and exit 1.
- [ ] At the same root, bare `niwa worktree create` and
      `niwa worktree create <repo>` for a repo that exists in some instance
      each print the R10 root-level error on stderr and exit 1; bare `create`
      exits 2 there before the fix, through the repo-inference usage error the
      root refusal now pre-empts.
- [ ] At the same root, `niwa worktree list --json` prints `[]` on stdout, the
      R10 message on stderr, and exits 0.
- [ ] At the same root, shell completion of worktree ids offers no candidates,
      and a WorktreeRemove hook invocation logs its warning and exits 0 without
      attempting a destroy.
- [ ] With `NIWA_INSTANCE_ROOT` set to an instance directory, every worktree
      subcommand behaves as it does inside that instance, including when the
      working directory is a multi-instance workspace root.
- [ ] From a directory outside any workspace, `niwa worktree destroy <value>`
      fails as it does today.
- [ ] `niwa worktree destroy --force <session-id>` and `--force <handle>` exit
      2 with the R9 usage line and destroy nothing; `--force` with a worktree
      id and with `--by-path` behaves as today.
- [ ] A session-resolved destroy prints the full
      `session: destroyed <worktree-id> (<repo>) at <path>` line, while destroy
      by worktree id prints today's bare `session: destroyed <id>` line.
- [ ] A mapping whose instance path is a dangling symlink, and one that is a
      regular file, each exit 1 and destroy nothing.
- [ ] Teardown run from inside one of the target session's own worktrees
      destroys it and exits per R9; the invoking shell is left in a removed
      directory, which the guide documents.
- [ ] In a single-instance-layout workspace, `niwa worktree create` and
      `destroy <worktree-id>` at the root work as before.

### Platforms

- [ ] `GOOS=windows go build` succeeds for every package this change touches
      (`./internal/workspace/...`, `./internal/worktree/...`).
      `GOOS=windows go build ./...` is not required and does not pass today:
      `internal/cli/sessionattach` and `internal/promptcapture` already fail to
      build for Windows, and this change neither fixes nor worsens that.
- [ ] `go test ./...` passes on Linux and macOS in CI, `go test -race` passes
      over the touched packages, the functional suite passes on Linux, and
      `go.mod` lists no new module.

## Out of Scope

- A merged-branch check for branches niwa does not manage as worktrees:
  worktrees Claude Code creates on its own under a repository, and branches in
  an instance's own repository clones. `niwa reap` still deletes those with the
  instance; teardown says so in its "nothing to destroy" line rather than
  extending the check.
- Recording which session created a worktree. Teardown by session acts on
  every active worktree in the instance its newest mapping owns.
- Accepting an instance name or a session display name as a destroy target,
  and adding the session id or handle to `niwa list --json`. The ids come from
  `claude agents --json` or the dispatch output.
- Listing every instance's worktrees from the workspace root. That is a new
  feature; at the root `niwa worktree list` says where to run instead.
- Ordering concurrent refreshes of a configuration directory against each
  other and against niwa's own writes into it (#297). Teardown's single read of
  the session mapping store is the one place the two meet, and it ships
  unlocked here; see Known Limitations.
- Moving niwa's local state out of the configuration directory so refreshes no
  longer carry it across (#74).
- Redesigning `niwa reap` or the ephemeral-session lifecycle.
- Worktree attach not resuming a conversation (#265) and push credentials for
  dispatched workers (#279).

## Known Limitations

- **Worktrees niwa doesn't manage stay unguarded.** A dispatched worker that
  made its worktrees with Claude Code's own mechanism, or committed on a
  branch in its instance's repository clone, has nothing for
  `niwa worktree destroy` to check. The "nothing to destroy" line says so, and
  the reap that follows still deletes those branches.
- **R6's newest-session check is best-effort until the mapping read is
  serialized.** The rung reads the same store snapshot the match ran against.
  While that read is unlocked, a read taken mid-refresh can miss the *newer*
  mapping for an instance, and a superseded session then passes a check that
  exists to stop exactly that. Containment is unaffected: teardown still acts on
  the enumerated instance directory that the matched mapping named, and R8's
  guards all still run, so the failure is a guard that did not fire rather than
  the wrong instance. Every other way a partial read can go wrong removes
  candidates and therefore fails toward doing nothing.
- **The mapping read is not ordered against a config refresh.** Teardown reads
  the store without a lock, as every reader of it does today, so a read taken
  while the workspace root's configuration directory is being swapped sees the
  directory missing or a subset of its files. That is #297's subject. The read
  fails toward doing nothing — it cannot invent a mapping, and R7's enumeration
  check means it cannot name a directory the workspace does not hold as an
  instance — with the single exception recorded above.
- **Single-instance layout.** Teardown by session is refused where the
  workspace root is itself the instance: the mapping's recorded instance path is
  the root, which is not an instance location under R7. Destroy by worktree id
  and `--by-path` work there as they always have.

## Decisions and Trade-offs

- **Teardown accepts the session id and the handle, not just one.**
  `claude agents --json` reports both (`sessionId` and `id`); the dispatch
  headline prints the full id and its hints print the handle. Accepting only
  one would make the other a dead end. Closes the brief's first open question.
- **Ambiguity is refused, not resolved by precedence.** An 8-hex value can be
  a worktree id or a handle. A precedence rule like `niwa go`'s would silently
  act on the wrong record in the rare collision, and this command deletes
  things. The refusal names both escapes: the full session id for the session,
  `--by-path` for the worktree. Closes the brief's second open question.
- **Teardown by session is instance-scoped, and only the newest session owns
  an instance.** Nothing records which session created a worktree. A
  dispatched instance belongs to one session at a time; when a newer session
  takes it over, the older id no longer speaks for its worktrees.
- **A handle-less mapping matches by its session id's first 8 characters.**
  The ephemeral-session hook, and mappings written before handles were
  recorded, leave the handle empty, and the agent's record directory is gone
  once the developer has run `claude rm`. The fallback is limited to exactly 8
  hex characters, the only form Claude's short id has taken, and refused when
  it matches more than one mapping.
- **Refusals don't stop the batch.** Each worktree's guard is independent, and
  a cleanup script gets the most done in one pass when the clean worktrees are
  destroyed and only the refused ones are left. Exit 1 still tells it
  something needs attention.
- **Distinct exit codes for "no match" and "ambiguous".** Both are lookup
  failures a script handles differently from a guard refusal: a no-match after
  reap is expected, while a refusal means work to review. Codes 3 and 4 are
  new; 0, 1 and 2 keep their meanings.
- **The root redirects worktree subcommands instead of aggregating.**
  `niwa worktree list` at the root could list every instance's worktrees, but
  that's a new feature. Today it prints an empty table read from the wrong
  store. It now prints where to run instead, and keeps exit 0 so an existing
  successful invocation doesn't start failing; the single-worktree actions,
  which already fail there today, exit 1 with the same message.
- **Containment does not depend on the mapping read.** The read is unlocked
  and can return a subset, so the resolver was built not to need more from it:
  R7 hands `DestroySession` a directory that instance enumeration produced,
  never the string a mapping recorded. A crafted or stale mapping can therefore
  cost a refusal, but cannot point teardown at a directory the workspace does
  not hold as an instance. That is what let #297 be split off rather than
  blocking this.
- **`--force` is refused with a session target.** It applies to one worktree,
  and a session can back several. A prefix-matched or mistyped id that forced
  its way through would discard every uncommitted change and unmerged branch in
  the instance at once. Forcing stays available one worktree at a time through
  the worktree id or `--by-path`. This was added after the outcome table was
  first settled, and is called out as a behavior change.
