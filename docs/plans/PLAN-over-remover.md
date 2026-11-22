---
schema: plan/v1
status: Active
execution_mode: coordinated
split_mode_source: intent
split_rationale: |
  Incremental Value. The DESIGN delivers the work in three parts and rejects
  landing it as one change (Decision 7, Delivery in three parts), and each
  part leaves niwa safe to release on its own. Each part is one pull request
  a reader can use alone: the rule and worktrees make reap keep a deleted
  session's unpushed work and name the path, which closes
  tsukumogami/niwa#300; the over question, mark and listing let finished
  units go at the next dispatch with nobody acting and show every stopped
  unit with its answer, age and size; the keep record, kept set and launch
  record let a person hold a unit or keep chosen files and make a reap on a
  mark undoable for a known window.
tracking_level: none
upstream: docs/designs/DESIGN-over-remover.md
milestone: "niwa removes a finished unit that a workspace command says is over"
issue_count: 24
---

# PLAN: over-remover

## Status

Active

## Scope Summary

One removal rule, coded once in a new `internal/removal` package and driven by one bounded pass, that every removal niwa makes on its own goes through, so a stopped unit goes only when nothing in it is unique; on top of it, a workspace-named over command whose exit 0 writes the retire mark, a person's `niwa retire`, a local listing and dispatch line of every stopped unit with answer, age and size, and a kept set, launch records and a restore window that make a reap on a mark undoable.

## Decomposition Strategy

**Walking skeleton.** Issue 1 sends the reap of a deleted session's instance through the new removal path end to end. `niwa reap`, and the opportunistic reap before `niwa dispatch`, `niwa create` and `niwa watch`, call `runPass`, which takes each mapped unit under one 30-second deadline and asks `removal.Evaluate` for a verdict. Every git call in that verdict is built by `gitCommand`, and the clone's refs are compared with the live tips `ls-remote` reports for the workspace's clone URL. A passing unit then goes through the existing gate, the instance lock and `DestroyInstance`. Anything the skeleton can't decide yet keeps the unit, so it never removes something today's reap would keep, and every later issue only makes it stricter.

The skeleton crosses the removal path only, not the over seam, the mark, the listing or the kept set. That follows from the design's Decision 7, which rejected shipping the over seam first: a mark reaped by a rule that doesn't yet scan for loss would take unique work. The same decision requires the first part to carry the hardened git environment, because it already runs git in repositories an agent wrote, so the skeleton carries `gitCommand`'s full environment, its first configuration check and the pass deadline from the start (Decision 1 bounds every all-unit pass by that one deadline).

Every later issue refines that path in the design's phase order, and none moves a deliverable out of the phase the design gives it. Issues 2 to 8 finish the rule and the pass (Phase 1); 9 to 11 bring every other removal under the rule (Phase 2); 12 to 15 add the over seam, the mark and `niwa retire` (Phase 3); 16 to 19 add the listing, the dispatch line, the trim and pid records (Phase 4); 20 to 22 add keep records, the kept set and `niwa discard` (Phase 5); and 23 and 24 add launch records for hook sessions, the restore window and the restore (Phase 6). Phase 1 is cut into eight issues along the rule's own seams (classes and check order, the verdict-only mode, proof remotes, the deny-write guide list, the walk, the configuration refusal, the tombstone), because its deliverables and scenarios are too large for one session. Each refinement keeps the skeleton's reap scenario passing, and tests and guide text sit in the issue whose behaviour they cover.

**Three pull requests, one per delivery part.** Decision 7 chose delivery in three parts, each leaving niwa safe to release, so the 24 issues fall into three PR groups in tsukumogami/niwa, merged in this order:

1. `rule-and-worktrees` (Issues 1-11): reap stops deleting a deleted session's unpushed work and names the path, every removal niwa makes on its own goes through one rule a person can preview with `niwa reap --verdict-only`, and reap and destroy stop leaving sessions behind for instances with worktrees.
2. `over-mark-listing` (Issues 12-19): a workspace that names an over command sees finished units go at the next dispatch, a person removes a clean stopped unit with `niwa retire`, every stopped unit shows its answer, age and size, and idle build trees are trimmed.
3. `keep-and-restore` (Issues 20-24): a person can hold a unit or keep chosen files past its reap, release paths the rule would keep with a confirmed `niwa discard`, and restore a unit reaped on a mark inside a known window.

The issues of one group land together in one pull request, so an issue never reaches the default branch ahead of the rest of its part. The design's six phases each end with niwa safe to release, but the design picked three parts as the delivery unit, so the plan doesn't split further. Contracted to groups, the issue dependencies run only forward: from `rule-and-worktrees` into `over-mark-listing` (Issues 3, 5, 8, 9, 10 and 11 into 12, 13, 15, 17, 18 and 19), from `over-mark-listing` into `keep-and-restore` (Issues 13, 14, 15, 16 and 18 into 20, 22, 23 and 24), and from `rule-and-worktrees` into `keep-and-restore` (Issue 5 into 23). The merge order is acyclic and linear. No non-PR gate is declared: nothing between the groups is a publish, a deploy or a manual step.

## Issue Outlines

### Issue 1: feat(reap): keep a deleted session's instance that holds an unpushed commit

**Goal**: Create the walking skeleton for niwa removing a finished unit: `niwa reap` and the opportunistic reap before `niwa dispatch`, `niwa create` and `niwa watch` run each mapped unit through `runPass` and `removal.Evaluate`, with every git call built by `gitCommand`, so a unit whose session was deleted by hand but whose clone holds a commit that no `ls-remote` of the clone URL shows is kept and printed with its first keeping check and the branch named, while a clean one still goes through the existing gate, the instance lock and `DestroyInstance`.

**Acceptance Criteria**:

- [ ] A new `internal/removal` package exports `Evaluate(ctx, Unit, Policy) Verdict`, which reads and never writes, with the check order as constants (`CheckLiveness`, `CheckHold`, `CheckRecords`, `CheckRepository`, `CheckOutside`, `CheckNamedFiles`) and `Verdict` and `Check` types; every check runs after one keeps, and the verdict names the first keeping check.
- [ ] `Evaluate`'s liveness check carries today's reap liveness verdict unchanged: a live session, or one whose liveness can't be read, keeps the unit.
- [ ] `Evaluate`'s repository check judges each clone in the instance: a ref `for-each-ref` lists whose tip isn't among the live tips `ls-remote` reports for the workspace's clone URL keeps the unit with the ref named, as does any `git status` entry, a git error or an unreachable clone URL. A `.git` entry the skeleton doesn't yet judge also keeps, with its path named.
- [ ] `gitCommand` in `internal/removal/git.go` is the only builder of git commands on this path. It starts git by an absolute path resolved once per pass from a filtered `PATH` (empty and relative entries dropped, each entry resolved through symlinks, any that resolves inside the workspace root dropped), passes no inherited environment except `HOME`, sets the fixed environment and the `-c` overrides of Decision 8, puts `--ignore-submodules=all` and `--untracked-files=all` on every status, and makes no git call outside it. A unit test asserts the built command's environment and arguments.
- [ ] `lsof` and `claude` resolve from the same filtered `PATH` and run by absolute path, and every `claude` call the pass makes runs from an empty directory under the pass's scratch directory.
- [ ] Before any other git call in a repository, the pass reads `git config --list --show-scope --includes` under a 10-second context, and a key from any scope other than `command` that names a program git might run keeps the unit with "repository configuration names a program: <key>", with no further git call made there.
- [ ] Every git output the skeleton parses is read in NUL-terminated form, and a record that doesn't parse keeps the unit.
- [ ] Each pass creates `~/.niwa/w-<key>/scratch/` fresh with mode 0700 and checks it and its parent for owner and mode before each use. A failed check keeps every unit that needs it, and nothing runs from that directory.
- [ ] `internal/cli/pass.go` adds `runPass(ctx, root, mode, scope)`. `runReap` and `reapOpportunistically` call it, and `reapWorkspace` becomes a thin call into it. It runs under one 30-second deadline, takes units in a fresh random order each pass, and leaves a unit it can't reach before the deadline in place.
- [ ] The mapped sweep (a unit whose session is gone) goes through the rule. A passing unit goes through the existing `[session.before_remove]` gate (step `reap`), the instance lock, the re-read and `DestroyInstance`, as today.
- [ ] Clone half of the `@critical` reap loss-scan scenario: a dispatched unit whose session was deleted by hand, with a commit on a branch that was never pushed to the clone URL, survives `niwa reap` with its instance directory unchanged, and the output names the instance, the repository check and the branch.
- [ ] The same unit, with its session deleted and no unpushed commit, is removed by `niwa reap`, and the existing scenario "niwa reap reclaims a dispatch instance after its session is deleted" still passes.
- [ ] A kept unit is printed by `niwa reap` with its first keeping check, and the opportunistic reap before `niwa dispatch`, `niwa create` and `niwa watch` keeps the same unit and never fails the command it runs before.
- [ ] The pass runs only inside niwa commands: niwa adds no daemon, timer or background process for it, and after any pass no niwa process remains running and no launchd, systemd or cron entry was added.
- [ ] CI green.

**Dependencies**: None

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 2: feat(removal): classify every path and run the rule's full check order

**Goal**: Refine the skeleton's rule so `removal.Evaluate` completes its liveness check with the pass's one process listing, runs its full per-repository check order (stashes, modified tracked files with the stat pins, index flags, untracked files reported in full whatever the repository's settings), then paths outside every repository, and gives every path exactly one class (tracked, pushed, build tree, generated, disposable or unique), honoring build-tree markers, generated paths, the shipped and workspace patterns, `[session.removal]` and `[repos.<name>] build_trees`, and the `.niwa/dispatch-retain` hold, so reap keeps any unit with unique work and names the check and the path.

**Acceptance Criteria**:

Check order and verdict:
- [ ] `internal/removal/rule.go` defines the check order as constants (`CheckLiveness`, `CheckHold`, `CheckRecords`, `CheckRepository`, `CheckOutside`, `CheckNamedFiles`), and `Evaluate` runs every check in that order even after one keeps; the returned verdict lists every keeping check with its paths and evidence and names the first one.
- [ ] `internal/removal/procs.go` takes the pass's one process listing (`/proc/<pid>/cwd` and `/proc/<pid>/fd` on Linux, `lsof -n -P -w -F 0pn` on macOS, run by the absolute path resolved from the filtered `PATH` and read NUL-terminated), records each process's pid, parent pid, start time, working directory and open files, and attributes each process to the instance and any build tree its working directory or an open file sits in; a unit test feeds a recorded listing for each platform and checks the attribution.
- [ ] Liveness keeps a unit that has a live process with its working directory inside the instance, naming the process, as it keeps one whose session is working; a listing that fails, times out or doesn't parse keeps every unit it would have cleared. Scenario: a background process whose working directory is inside a deleted session's otherwise clean instance keeps the unit through `niwa reap`, with the liveness check and the process named.
- [ ] Inside each repository, in path order, the checks run as git errors and unreachable remotes, then commits and refs, then stashes, then modified tracked files, then index flags, then untracked files; a unit test asserts this order on a repository that fails all of them.
- [ ] A repository with a stash keeps the unit with the stash named as evidence; `git stash list` is built by `gitCommand` with `--no-show-signature`.
- [ ] A modified tracked file keeps the unit with the file named as modified; every `git status` the rule runs is `--porcelain=v2 -z` with `--ignore-submodules=all` and `--untracked-files=all`, and its output is parsed NUL-terminated, a record that doesn't parse keeping the unit.
- [ ] An index entry flagged assume-unchanged (lowercase tag in `git ls-files -v -z`) or skip-worktree (`S`) keeps the unit with "index flag hides changes: <flag> <path>"; a sparse checkout keeps.
- [ ] An untracked file that no ignore source matches keeps the unit with "untracked files: <path>".
- [ ] A file outside every repository in the instance that isn't generated, disposable or in a build tree keeps the unit under `CheckOutside` with the path named.
- [ ] An unmapped instance carrying `.niwa/dispatch-retain` is kept by the rule with the hold named; the marker name reuses `dispatchRetainMarker` in `internal/cli/dispatch.go` rather than a second literal.

Path classes:
- [ ] Every path in the unit gets exactly one class (tracked, pushed, build tree, generated, disposable or unique), with a `git-ignored` notice on a path disposable because it is ignored and a `job scratch` notice on job scratch disposable because the unit has no brief; a unit test covers each class and each notice.
- [ ] `.gitignore`, `.git/info/exclude` and a `core.excludesFile` the repository's own configuration names are ignore sources; with `ignored_files = "disposable"` (the default) an ignored path is disposable with the `git-ignored` notice, and with `ignored_files = "unique"` the same path keeps the unit.
- [ ] A git-ignored directory carrying `CACHEDIR.TAG` with the standard signature line is a build tree, as is a directory listed in `[repos.<name>] build_trees`; a `CACHEDIR.TAG` with the wrong signature, or on a directory that isn't git-ignored, doesn't make a build tree.
- [ ] `[repos.<name>] build_trees` accepts only exact paths relative to the repository root ending in `/` with no wildcards; any other entry is a configuration error that ends the pass with nothing removed.
- [ ] Generated paths are `instance.json` and the other niwa-managed files under `<instance>/.niwa/`, the files `managed_files` in `instance.json` lists, context files, plugin and marketplace trees, and a job directory's `state.json` and `timeline.jsonl`; each is classed generated and keeps nothing.
- [ ] An empty directory (no entries, or holding only empty directories) outside every repository, at the instance root or in job scratch, is classed generated and keeps nothing, whatever `job_scratch` and `ignored_files` say.
- [ ] The shipped disposable patterns are `*.swp`, `*.swo`, `*~`, `.#*`, `.DS_Store`, `__pycache__/` and `.pytest_cache/`; pattern matching follows `path.Match` plus `**`, a pattern with no slash matching a base name anywhere, one with a slash matching from the repository or instance root, and a trailing slash matching a directory and everything under it, each case covered by a unit test.
- [ ] `[session.removal] disposable` entries add workspace patterns; an entry with an empty or missing `review` is a configuration error.
- [ ] `[session.removal] job_scratch` takes `"disposable"` (the default) or `"unique"` and applies only to a unit with no handoff brief: with `"disposable"` its job scratch is disposable with the `job scratch` notice, with `"unique"` it keeps the unit. Any other value of `job_scratch` or `ignored_files` is a configuration error.
- [ ] `[session.removal]` with `ignored_files`, `job_scratch` and `disposable`, and `build_trees` on `[repos.<name>]`, are added to `internal/config` (beside `SessionConfig` in `internal/config/session.go` and `RepoOverride` in `internal/config/config.go`) and read by `loadWorkspaceConfig` into the policy; `proof_remotes` is left to <<ISSUE:4>>.

Functional scenarios (`@critical`) and security scenarios:
- [ ] The path-class scenarios pass through a reap: a unit whose only leftovers are a build tree, generated files, shipped-pattern files and git-ignored files is removed, as is a unit whose only leftover is an empty directory at the instance root, and the first unit with one unique file added is kept with that file and its check printed.
- [ ] A directory in the clone made unreadable with `chmod 000` keeps the unit, with the read error as evidence.
- [ ] A repository with `status.showUntrackedFiles=no` in its own configuration and an untracked `notes.txt` keeps the unit with "untracked files: notes.txt".
- [ ] A repository whose configuration names no `core.excludesFile`, with a per-user ignore file at `~/.config/git/ignore` matching an untracked `notes.txt`, keeps the unit with "untracked files: notes.txt" (git runs with `-c core.excludesFile=`).
- [ ] A repository whose own configuration sets `core.trustctime=false` and `core.checkStat=minimal`, with a tracked file rewritten to its old size and modification time, keeps the unit with that file named as modified (git runs with `-c core.trustctime=true -c core.checkStat=default`).
- [ ] E2E flow still works (do not break the skeleton): the skeleton's reap scenario, a deleted session's instance holding an unpushed commit kept with the branch named and a clean one removed, still passes.

Downstream deliverables:
- [ ] Must deliver: a `Verdict` that carries every keeping check in order with its paths and evidence, and the class of every path with its `git-ignored` or `job scratch` notice, in a form the verdict-only printer can render without re-running checks (required by <<ISSUE:3>>).
- [ ] Must deliver: the per-repository check sequence as a function the walk can call for any repository it finds, with a no-work-tree mode (`--git-dir` alone) that skips the status, `ls-files` and index-flag reads, plus the build-tree detector the walk uses to decide where not to descend (required by <<ISSUE:6>>).
- [ ] Must deliver: the status and index-flag reads exposed so the re-read and tombstone check can repeat them and compare, and the `.niwa/dispatch-retain` hold applied inside `Evaluate` so the backstop's unmapped instances get it once routed through the rule (required by <<ISSUE:8>>).
- [ ] Must deliver: the process listing as a value the pass takes once and can take again and compare with the first process by process, each entry carrying its pid, parent pid, start time and attribution, so the re-read and the tombstone check can repeat it (required by <<ISSUE:8>>).

**Dependencies**: Blocked by <<ISSUE:1>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 3: feat(reap): add niwa reap --verdict-only with strict record readers and evidence scrubbing

**Goal**: A person runs `niwa reap --verdict-only [<instance>] [--json]` and sees each unit's verdict, every keeping check with its paths and evidence and every path's class, read through size-capped, strictly decoded, no-follow record readers and `ResolveRecorded`, with every printed string passed through `ScrubEvidence`, and the mode writes, removes and moves nothing.

**Acceptance Criteria**:

Verdict-only mode:
- [ ] `niwa reap --verdict-only` prints, for each unit, the verdict, every keeping check in rule order with its paths and evidence, and the class of every path; a directory whose contents all share one class prints as one line, so a clean clone is one tracked line and a build tree one line.
- [ ] A git-ignored path is listed as disposable with a `git-ignored` notice, and job scratch that's disposable only because the unit has no brief is listed as disposable with a `job scratch` notice.
- [ ] `niwa reap --verdict-only <instance>` prints only that unit; `niwa reap <instance>` without `--verdict-only` exits non-zero with a usage error and removes nothing.
- [ ] `niwa reap --verdict-only --json` prints a `niwa.verdicts/v1` array carrying the same fields as the text form (verdict, keeping checks with paths and evidence, path classes).
- [ ] The mode runs no over command and its output carries no over answer or would-be mark.
- [ ] After a verdict-only run over a workspace holding a passing unit and a kept unit, both instances still exist, no `claude rm` was issued, and every file under each instance (each repository's `.git` included) and under the root's `.niwa/` is byte-for-byte unchanged; the only writes are fetches into the scratch object store. That the mode renames no tombstone and moves nothing into the kept set is checked once those exist, in <<ISSUE:17>> and <<ISSUE:21>>.

`ResolveRecorded`:
- [ ] Unit tests show `removal.ResolveRecorded(base, path)` accepts a relative, clean path and rejects an empty path, an absolute path, a `..` component, a NUL and a backslash, each with an error naming the path.
- [ ] Unit tests show a `job:` prefix resolves against the unit's own job directory and nothing else, and that a symlink in any component, the last one included, fails the check.
- [ ] A path that fails `ResolveRecorded` is named in the verdict and keeps the unit as unreadable.

`ScrubEvidence`:
- [ ] Unit tests show URL userinfo stripped (`https://user:token@host/x` becomes `https://host/x`, `x-access-token:` with it).
- [ ] Unit tests show `[redacted]` in place of `ghp_`, `gho_`, `ghs_`, `github_pat_`, `glpat-`, `sk-`, Slack `xox` tokens, `AKIA` and `ASIA` key ids, three-part `eyJ` JSON web tokens, the value after `Authorization:` or `Bearer`, the value of any `key=value` or `key: value` pair (URL query parameters included) whose key contains `token`, `secret`, `password`, `passwd`, `key`, `auth` or `credential` in any case, and any run of 32 or more base64 characters mixing upper case, lower case and digits.
- [ ] Unit tests show a 40-character lower-case hex git object id survives unchanged.
- [ ] Unit tests show terminal control and escape sequences removed through `sanitizeRemovalGateText`, and truncation to the given maximum (200 for a reason, 400 for evidence) applied only after scrubbing, so a token straddling the cut leaves no prefix behind.
- [ ] Every unit-sourced string printed by `niwa reap` (the kept line included) and by the verdict-only mode, text and JSON, has passed `ScrubEvidence`.

Record readers:
- [ ] The shared reader in `internal/removal/records.go` opens each file with `O_NOFOLLOW|O_NONBLOCK`, `fstat`s the descriptor and reads nothing unless it's a regular file owned by the user; a symlink in a record's place is refused.
- [ ] A FIFO planted in a record's place doesn't block the open: the pass finishes within its deadline and the record counts as unreadable.
- [ ] A record over its size cap, one that fails strict decoding into its fixed struct, or one with an unknown schema name is rejected, and every such failure lands on the keeping side.
- [ ] `<instance>/.niwa` and `<instance>/.niwa/pids` are checked with `Lstat` as real directories owned by the user and opened with `O_DIRECTORY|O_NOFOLLOW`, and every read under them is made relative to that descriptor.

Scenarios (`@critical` functional, through a reap and the verdict-only mode):
- [ ] A `.niwa` replaced by a symlink keeps the unit with the reason named, and the link's target directory is unchanged afterwards.
- [ ] A credential in a remote URL (`https://user:secret@host/repo`) never appears in reap's output or in the verdict-only text or JSON output.
- [ ] An `AKIA` key id placed in unit-sourced evidence never appears in any output.
- [ ] An `xoxb-` token placed in unit-sourced evidence never appears in any output.
- [ ] A `?access_token=` query value in a remote URL never appears in any output.

Skeleton and downstream:
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept and named by `niwa reap`, and a clean one is still removed.
- [ ] Must deliver: a verdict-only `runPass` mode and a text and `niwa.verdicts/v1` JSON output with a place for each unit's over answer and the mark it would write, plus `ScrubEvidence` for stored answers (required by <<ISSUE:17>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 4: feat(removal): count a commit as held only when a proof remote shows it

**Goal**: Refine the skeleton's held check so a pushed or squash-merged branch counts as held only through the workspace's clone URL or a `proof_remotes` prefix matched on the full authority, decided by ancestry and merged-by-content checks in a scratch store with its owner, mode, config and alternates checks, its 1 GiB cap and the person's credential helper, while a commit only an agent-added, refused or unreachable remote could hold keeps the unit with the reason named.

**Acceptance Criteria**:

Proof remotes and URL validation:
- [ ] A proof remote is a remote whose URL equals the clone URL `loadWorkspaceConfig` reads for that repository from the root (never from `instance.json` or anything inside the instance), or matches a `proof_remotes` prefix; for a repository the workspace configuration doesn't name, a proof remote is one whose URL equals a clone URL the configuration gives some repository or matches a `proof_remotes` prefix, both on the full authority, and a commit only other remotes could hold keeps with "remote not recognized: <remote name>" and the verdict names the `proof_remotes` setting that would widen it.
- [ ] URLs are compared after normalizing (scheme and host lowercased, scp-like form rewritten as `ssh://`, trailing `/` or `.git` dropped, no path cleaning), on the whole authority with the default port made explicit (443 for `https`, 22 for `ssh`); a prefix matches on whole path segments only; a URL rebuilt on a match takes its scheme, user, host and port from the clone URL or prefix and only its path from the remote.
- [ ] Every clone URL, prefix and remote URL is parsed and refused when its scheme isn't `https`, `ssh` or scp-like, its host or path starts with `-`, its path holds a `.`, `..` or empty segment, or it carries percent-encoding, a backslash, a control character or userinfo (any in `https`; anything but the bare user `git` in `ssh` or scp-like); a refused URL is never contacted and vouches for nothing, and a commit only it could hold keeps with "remote URL not allowed: <reason>" naming what was found.
- [ ] `[session.removal] proof_remotes` in `internal/config/session.go` accepts a list of `https://` or `ssh://` prefixes ending in `/` with a host and at least one path segment, is empty by default, and a prefix that doesn't parse is a configuration error.

Held, ancestry and merged by content:
- [ ] A local commit whose id is itself a tip `git ls-remote` returns (no pattern, so `refs/pull/*` and tags count) for a proof remote is held without reading any object; any other local commit is held only when `git merge-base --is-ancestor` shows it is an ancestor of such a tip.
- [ ] A branch is merged by content only when `git merge-tree --write-tree` of the proof remote's default branch's live tip and the local branch exits 0 and yields the default branch's own tree.
- [ ] The ancestry and `merge-tree` checks run only with the scratch store as `--git-dir`, against the tip ids `ls-remote` returned, with the proof remote's tip history fetched into the store and the local side built by `git pack-objects --revs --stdout` in the unit (through `gitCommand`) and read by `git index-pack --stdin` in the store.

Scratch store and network calls:
- [ ] The scratch store is a bare repository created fresh at mode 0700 under `~/.niwa/w-<key>/scratch/<pass>/`, with no refs and no alternates; a directory already at the path fails the creation and keeps.
- [ ] niwa writes the store's `config` itself and re-reads it before each network call, and any byte of difference keeps the unit with no call made; before each use a `commondir`, `objects/info/alternates` or `objects/info/http-alternates` in the store keeps the unit with no call made.
- [ ] A fetch or local pack that grows the store past 1 GiB is killed and the unit kept; the store is removed on every exit path, and a pass removes a store an earlier, interrupted pass left only after the owner and mode check.
- [ ] Every network call runs against the store with the validated URL niwa built, `-c credential.helper=<person's helper>`, `-c http.followRedirects=false`, `-c http.proxy=` and `-c http.sslVerify=true`, under a 10-second context; an error, timeout or redirect keeps with git's error, credentials scrubbed, as evidence.
- [ ] The person's credential helpers are read once per pass, before any unit is evaluated, with `git config --global --get-all credential.helper` from an empty directory under the scratch directory (checked for owner and mode first, a mismatch keeping every unit), with `GIT_CEILING_DIRECTORIES` set and a 10-second context; the value goes only on network calls to proof remotes, and a helper scoped to a URL or set only in an included file isn't found, so that remote reads as unreachable and keeps.

Scenarios (each through a reap):
- [ ] `@critical` functional: a unit whose only proof remote is unreachable keeps with the git error named.
- [ ] An `ext::` remote and a `file://` remote are never contacted and keep the unit.
- [ ] An agent-added remote, served by a test server that advertises the local commit, keeps the unit with "remote not recognized" and the server sees no request.
- [ ] A commit pushed to the clone URL is held, and the unit passes the rule.
- [ ] A commit on a remote matching a `proof_remotes` prefix is held, and the unit passes the rule.
- [ ] A clean, fully pushed clone of the workspace repository's remote made at `extra/other` under the instance root, outside every repository niwa provisioned, is held through that clone URL, and the unit passes the rule rather than keeping for loose files or "remote not recognized".
- [ ] An unpushed commit stays kept when the clone carries a `refs/replace/` entry, an `info/grafts` file or a forged commit-graph that makes the proof remote's tip appear to descend from it.
- [ ] The clone's `.git` is unchanged after a merged-by-content check, so `niwa reap --verdict-only` (added by the same group) writes nothing there.
- [ ] A scratch store planted at the path the pass creates its store at, holding a pack that maps the proof remote's tip id to a forged commit whose parent is the unpushed commit, keeps the unit.
- [ ] A store whose config is rewritten after niwa writes it, with a `url.<base>.insteadOf` pointing at a test server that advertises the unpushed commit, keeps the unit and the server sees no request.
- [ ] A scratch directory, or its parent, with the wrong owner or a mode other than 0700 keeps the unit and nothing runs from it.
- [ ] A local branch whose `git merge-tree --write-tree` against the proof remote's default branch hits a conflict and exits non-zero isn't merged by content and keeps the unit.
- [ ] A scratch store holding a `commondir`, an `objects/info/alternates` or an `objects/info/http-alternates` file before a use keeps the unit, and no network call runs.
- [ ] With `proof_remotes = ["https://<test host>/dana/"]`, a remote `https://<test host>/dana/../mallory/repo` whose target advertises the unpushed commit keeps the unit with "remote URL not allowed" naming the dot segment, and the test server sees no request.
- [ ] The same remote with a percent-encoded `..` keeps the same way, naming the encoding.
- [ ] An `https` remote with userinfo under the prefix keeps the same way, naming the userinfo.
- [ ] A remote under `https://<test host>/dana-evil/` that advertises the commit doesn't match the prefix and keeps with "remote not recognized".
- [ ] A remote on `https://<test host>:<other port>/dana/repo` doesn't match the prefix and keeps with "remote not recognized".
- [ ] A loose object written into the clone under the id of a real proof-remote commit, with the unit's unpushed commit as its parent, leaves that commit kept by both the ancestry and the merged-by-content checks.
- [ ] Unit tests cover URL normalization, the authority and whole-segment match, each refusal reason, and the held, ancestor and merged-by-content outcomes.
- [ ] E2E flow still works (do not break the skeleton): the skeleton's reap scenario still keeps a deleted session's instance holding an unpushed commit and removes a clean one.
- [ ] Must deliver: the pass's credential-helper read and ssh calls to proof remotes in place, the scratch directory at `~/.niwa/w-<key>/scratch/`, and the credential-helper read's behaviour of not following git's includes, which the guide's deny-write list and its notes describe (required by <<ISSUE:5>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:6>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 5: docs(guides): ship the complete deny-write list in the ephemeral-session guide

**Goal**: `docs/guides/ephemeral-session-instances.md` gains the whole deny-write list the first part needs, with sandbox write restrictions that cover Bash, the note that ssh follows `Include` while the credential-helper read doesn't follow git's includes, the note that a worker without such a sandbox has no boundary, and the Claude Code paths resolved from `CLAUDE_CONFIG_DIR` when it's set.

**Acceptance Criteria**:

- [ ] `docs/guides/ephemeral-session-instances.md` has a section, reachable from the `niwa reap` part of the guide, that tells workspaces to deny the agent writes to each place below and says why each one matters to removal.
- [ ] The list names `~/.niwa/`, including the scratch directory that holds the scratch object store and the empty directories `claude` and the credential-helper read run from, and says the ledger and kept set that later releases add live there too.
- [ ] The list names the workspace root's `.niwa/` (whose `workspace.toml` holds the clone URLs and the `proof_remotes` prefixes) and the root's `.niwa-tombstone-*` entries.
- [ ] The list names `~/.gitconfig` and `~/.config/git/config`.
- [ ] The list names the whole `~/.ssh/` directory together with every file outside it that an `Include` in `~/.ssh/config` names.
- [ ] The list names any script or library those configuration files point at that the agent could write, with a credential helper's script, a `ProxyCommand` or `Match exec` target and a `PKCS11Provider` library as the examples.
- [ ] The list names Claude Code's user settings and user-scope plugins, which can define hooks or programs `claude` runs, in the config directory resolved as the design's Data Flow step 7 describes, and Claude Code's user-scope MCP file, which can name programs `claude` runs.
- [ ] With `CLAUDE_CONFIG_DIR` unset, the guide names `~/.claude/` for the settings and plugins and `~/.claude.json` in the home directory for the MCP file, saying it sits beside the default config directory rather than inside it.
- [ ] With `CLAUDE_CONFIG_DIR` set to an absolute directory, the guide names that directory's settings rather than `~/.claude` and `.claude.json` inside that directory rather than `~/.claude.json`.
- [ ] The guide says the protection must be sandbox write restrictions that also cover Bash (Claude Code's sandbox or an operating-system equivalent), and that a path deny rule in Claude Code's permission settings alone isn't enough because it covers the file tools and not a shell command that writes the same path.
- [ ] The guide says ssh follows `Include` while niwa's credential-helper read doesn't follow git's includes, so a helper set only in an included file or under a `credential.<url>.helper` key isn't read and the remote it was needed for reads as unreachable and keeps the unit.
- [ ] The guide says a worker that runs without such a sandbox has no boundary at all, and that niwa should run inside the agent's sandbox rather than as a command the sandbox lets out.
- [ ] The section doesn't list `.niwa-launch/` or the `cleanupPeriodDays` restore-window note, which arrive with <<ISSUE:23>>.
- [ ] E2E flow still works (do not break the skeleton): the skeleton's reap scenario, a deleted session's instance holding an unpushed commit kept and printed with its first keeping check, still passes.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:4>>, <<ISSUE:8>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 6: feat(removal): keep work held in nested, bare, submodule or outside-the-instance repositories

**Goal**: Refine the skeleton's repository check so the walk in `internal/removal/walk.go` finds every repository in the instance and the unit's job directory under its no-symlink, one-filesystem, external-git-directory, unreached-worktree and outside-work-tree rules, and unpushed work in a nested, bare or deinitialized-submodule repository, a repository inside a build tree, a worktree or work tree outside the instance, or a prunable worktree keeps the unit with the repository and path named.

**Acceptance Criteria**:

- [ ] The walk finds every `.git` directory and `.git` file in the instance and the job directory, plus every git directory recognized by a `HEAD` file beside `objects/` and `refs/` whatever its name, including bare repositories and every git directory under a `modules/` directory at any depth, and including those inside a build tree during the size descent.
- [ ] Every repository the walk finds gets the rule's repository checks as a repository of its own. One with no work tree (a bare repository, or a submodule store whose configured work tree is inside the instance or job directory) is checked with `--git-dir` alone, and the status, `ls-files` and index-flag reads are skipped for it.
- [ ] Reap loss scan, worktree half: an instance whose session was deleted by hand and whose linked worktree inside the instance holds a commit the clone URL doesn't show is kept, and the output names the first keeping check, the worktree path and the branch.
- [ ] A nested repository inside the clone that the clone's `.gitignore` excludes, holding an unpushed commit, keeps the unit with that repository named; the commit doesn't pass as a git-ignored path.
- [ ] A deinitialized submodule whose `.git/modules/<name>` holds a commit no proof remote has keeps the unit.
- [ ] A bare repository in the instance holding a commit no proof remote has keeps the unit.
- [ ] A nested repository inside a `CACHEDIR.TAG` build tree, holding a commit no proof remote has, keeps the unit.
- [ ] A symlink inside the instance pointing at a file outside it is never read or summed: the target's contents and size don't appear in the verdict, and the link is classed by its own name.
- [ ] A directory on a device other than the instance's keeps the unit with "another filesystem at <path>"; under the job directory the device is compared against the jobs root.
- [ ] A `.git` file whose `gitdir:` (or the `commondir` it names) resolves outside the instance and job directory keeps the unit with "external git directory" and is not followed. A `.git` or `commondir` file that is not a single line in the expected form keeps with "unreadable git file: <path>".
- [ ] A linked worktree added outside the instance (`git worktree add <temporary dir>`), with a commit only on its `refs/worktree/` and a file staged only in its index, keeps the unit with "worktree outside the instance: <path>" naming that path.
- [ ] An admin directory under the common directory's `worktrees/` that git doesn't report also counts as unreached and keeps the unit with "worktree outside the instance: <path>".
- [ ] A git directory inside the instance whose work tree is outside it, made with `git init --separate-git-dir` and, separately, through `core.worktree`, keeps the unit with "work tree outside the instance: <path>" naming the path.
- [ ] A prunable worktree whose working tree was deleted and whose `refs/bisect/` holds a commit no proof remote has keeps the unit. Its HEAD, per-worktree refs and index are read with `--git-dir=<admin directory>` through `gitCommand`, and a failed read or a staged entry that differs from HEAD keeps.
- [ ] A worktree's `gitdir` file rewritten to a path holding a newline and a `HEAD` line naming a pushed commit doesn't hide that worktree's unpushed detached HEAD, which keeps the unit.
- [ ] With `CLAUDE_CONFIG_DIR` set to another absolute, clean directory, the walk reads that directory's `jobs/` rather than `~/.claude/jobs/`.
- [ ] With `CLAUDE_CONFIG_DIR` set to a relative value, the unit is kept.
- [ ] With `CLAUDE_CONFIG_DIR` set to an absolute value that isn't already clean (one containing `..`, `.`, or a repeated or trailing separator), the unit is kept; `~` in it is never expanded.
- [ ] Unit tests in `internal/removal` cover each walk rule (symlink, device, external git directory, unreadable git file, unreached worktree, main entry, outside work tree, prunable entry) and each repository kind (nested, bare, submodule store, inside a build tree).
- [ ] Must deliver: the walk's list of every repository it found, each with its git directory and its work tree or none, in a form the configuration check can run on before any other git call in that repository, prunable worktrees' admin directories included (required by <<ISSUE:7>>).
- [ ] E2E flow still works (do not break the skeleton)

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 7: feat(removal): refuse program-naming repository configuration before and during a pass

**Goal**: The program-naming configuration check runs first in every repository the walk finds and again before every status and unforced worktree removal, so a filter, diff, merge, credential, signing, pager, ssh or fsmonitor program named in any case, set before the pass or planted during it, never runs and keeps the unit with the key named, as does a partial clone on git older than 2.45, while an unsafe `PATH` entry is dropped from the filtered `PATH` so git is never resolved from it.

**Acceptance Criteria**:

- [ ] The configuration read is the first git call in every repository the walk finds (each clone, worktree, submodule, gitlink, nested and bare repository, a submodule's configuration under `.git/modules/` included), and it runs on all of them before the first other git call in any of them. A unit test with a recording invoker shows the order.
- [ ] Every key on the refused list, and every key matching `*.program`, `*.command`, `*Command` or `*.helper`, set in the `local` or `worktree` scope or in a file either includes, keeps the unit with "repository configuration names a program: <key>". Nothing more runs in that repository. Only niwa's own `-c` values are exempt. There is one unit test per refused key and per pattern.
- [ ] A repository with `core.fsmonitor`, `core.hooksPath`, `core.sshCommand`, `core.pager` and `credential.helper` each set to a script that writes a marker file runs none of them through `niwa reap` or through `niwa reap --verdict-only`. No marker file exists afterwards.
- [ ] A `filter.x.clean` in a repository's configuration keeps the unit, with the key as evidence.
- [ ] A `filter.x.clean` set to a marker-writing script, with an attributes entry and a stat-dirty tracked file that select it, planted in a repository's configuration after the pass's first configuration check and before the rule's `git status` there, keeps the unit with "changed during the pass" and the key named. The filter never runs: no marker file.
- [ ] The same driver planted after the first check and before the re-read's `git status` keeps the unit the same way, with "changed during the pass", the key named and no marker file.
- [ ] The same driver set only in `config.worktree`, with `extensions.worktreeConfig` on, keeps the unit with the key named and never runs.
- [ ] A repository holding a signed commit in `refs/stash`, with `log.showSignature` set and a `gpg.program` that writes a marker file, writes no marker through a reap or through the verdict-only mode, and keeps the unit with `gpg.program` named.
- [ ] A program-naming key written in another case (`Core.FSMonitor`, `Credential.Helper`) and set to a marker-writing script is matched under its canonical name. It keeps the unit with the key named and writes no marker file.
- [ ] A `PATH` with an empty entry and an entry symlinked into the workspace root never resolves git from either. A git binary planted in each one never runs, and git is taken from the first entry that survives the filter.
- [ ] With a git older than 2.45, a repository whose configuration sets `extensions.partialClone` or a `remote.<name>.promisor` keeps the unit with "partial clone needs git 2.45". That happens right after the configuration read, before any call that could read an object. A unit test using a faked git version shows that no later call is made.
- [ ] A repository git refuses for ownership keeps the unit with git's message, and niwa never writes a `safe.directory` entry.
- [ ] The scenarios above are `@critical` functional scenarios under `test/functional/`, and each one runs through a reap and, where it applies, the verdict-only mode.
- [ ] Must deliver: an invoker-level re-check that runs the configuration read on a given repository and returns the flagged key. `gitCommand`'s invoker calls it right before every `git status`. It is callable on its own right before a worktree removal that starts no git process, and on a flagged key it returns a keep reason of the form "changed during the pass: repository configuration names a program: <key>" (required by <<ISSUE:9>>).
- [ ] E2E flow still works (do not break the skeleton): the reap of a deleted session's instance that holds an unpushed commit still keeps it and prints its first keeping check with the branch named, and a clean one is still removed.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:6>>, <<ISSUE:8>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 8: feat(reap): re-check a passing unit on a tombstone and route the backstop through the rule

**Goal**: Refine the skeleton's reap so a passing unit is re-read, renamed to a tombstone under the tombstone lock and checked again before `DestroyInstance`, so the backstop's unmapped instances go through the same rule, and so `DestroyInstance` and the lock files refuse symlinks and misplaced paths.

**Acceptance Criteria**:

Re-read and tombstone:
- [ ] After the gate allows a reap, the pass re-reads sessions, the process listing and, for every repository the walk found, its `git status` (preceded by a fresh program-naming configuration check), index-flag read and, for a prunable worktree, `git diff-index --cached` from its admin directory, all through `gitCommand`; any difference or flagged key keeps the unit with "changed during the pass" and nothing is removed.
- [ ] With the re-read, the pass records a ref snapshot per repository: every ref `for-each-ref` lists (in a linked worktree its own `refs/worktree/*` and `refs/bisect/*`, a prunable entry's read from its admin directory), HEAD, `stash list` and each worktree's HEAD from `git worktree list --porcelain -z`, and a hash of each record in `.niwa/`.
- [ ] After on-reap and immediately before `DestroyInstance`, the pass takes `~/.niwa/locks/t-<workspace key>.lock` shared and renames the instance to `<root>/.niwa-tombstone-<instance>-<8 hex>` in the same directory; a path opened under the old name afterwards gets "no such file".
- [ ] On the tombstone the pass repeats the process listing, the configuration check on every repository, `git status --porcelain=v2 -z --ignore-submodules=all --untracked-files=all`, `git ls-files -v -z`, the prunable worktree's `git diff-index --cached`, the record hashes and the ref snapshot; a new, moved or gone ref, a new stash entry, a HEAD or worktree HEAD pointing elsewhere, a flagged key, a flagged index entry or a process that differs from the first listing counts as a difference.
- [ ] Any difference calls `workspace.UntombstoneInstance`, which renames the tombstone back to `<root>/<instance>`, and the unit is kept with "changed during the pass" (with the ref, key or path named) and listed as the delete-then-reap path leaves one; otherwise `DestroyInstance` removes the tombstone. The tombstone lock is released once `DestroyInstance` or the rename back returns.
- [ ] `DestroyInstance` is called or extended so its plugin-record prune and Claude trust withdrawal use the instance's original path `<root>/<instance>` (any trust key that needs the path to exist resolved before the rename) while the directory removal works on the tombstone: after a reap through the tombstone, no Claude Code plugin record under `<root>/<instance>` and no trust entry niwa recorded for the instance root or its repositories remains, as `TestDestroyInstance_PrunesOwnedRecords` and the trust-withdrawal tests show for a direct destroy.
- [ ] Scenario (`@critical`): an untracked, not ignored, file written into a repository's working tree after the re-read renames the tombstone back, keeps the unit with "changed during the pass" and the path named, and the file still exists under `<root>/<instance>`.
- [ ] Scenario (`@critical`, through a reap and the verdict-only mode where it applies): a commit made in the instance after the re-read and before the rename, which leaves `git status` clean, renames the tombstone back and keeps the unit with "changed during the pass: ref <name>".

Locks, overlapping passes and recovery:
- [ ] Scenario (`@critical`, held lock): a unit whose instance lock another process holds is skipped by the pass without blocking, isn't counted as a failure, and is still present after the reap exits 0.
- [ ] Scenario (`@critical`, overlapping passes): two reaps started together over the same workspace remove each clean deleted-session unit once, neither reports an error for a unit the other took, and no `.niwa-tombstone-*` directory remains afterwards.
- [ ] Scenario (`@critical`): a second pass started while the first holds a tombstone (tombstone lock held shared) doesn't take the lock exclusively, leaves that tombstone alone and doesn't rename, evaluate or delete it.
- [ ] Scenario (`@critical`, interrupted pass): with a `.niwa-tombstone-<instance>-<8 hex>` left by a killed pass and no holder of the tombstone lock, the verdict-only mode leaves it in place, and the next reap takes the lock exclusively without blocking, renames it back to `<root>/<instance>` and evaluates it as usual.
- [ ] Recovery keeps and lists a leftover tombstone whose instance name is already taken, without renaming it.
- [ ] Recovery `Lstat`s each leftover tombstone and skips one that is a symlink or isn't a direct child of the root: the link's target is neither renamed nor evaluated.
- [ ] `workspace.EnumerateInstances` gains a skip of `.niwa` and every name starting with `.niwa-` (today it skips only names `ValidName` rejects), so a tombstone holding `.niwa/instance.json` is never read as an instance; `hostdir.InstanceDirs` is unchanged and still lists `.niwa-tombstone-*`.
- [ ] Lock files in `~/.niwa/locks/` (instance and tombstone locks) are created with mode 0600 and `O_NOFOLLOW`; a symlink planted at a lock path makes the lock fail, the link's target is unchanged, and the unit is kept.

Backstop and `DestroyInstance`:
- [ ] The backstop's unmapped dispatch-named instances older than 30 minutes are gathered as candidates in `runPass` and judged by `removal.Evaluate` through the same re-read and tombstone: one holding an unpushed commit is kept and printed with its first keeping check, a clean one is removed, and one carrying `.niwa/dispatch-retain` is kept as held.
- [ ] `workspace.DestroyInstance` `Lstat`s the instance directory and refuses a symlink, and refuses any path that isn't a direct child of the resolved workspace root; in both cases it returns an error, deletes nothing, and the pass keeps the unit with the refusal named.
- [ ] Every refusal in the acting steps is reported as a disagreement, kept and named, with its message passed through the evidence scrubber when it's printed.

Skeleton:
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding a commit no `ls-remote` of the clone URL shows is still kept and printed with its first keeping check and the branch named, and a clean one is still removed.

Downstream deliverables:
- [ ] Must deliver: the per-repository ref snapshot taken at the re-read, in a form a later read can compare against, and an `actReap` step order with a place between the re-read and the tombstone rename for removing worktrees, plus a tombstone comparison that accepts the set of entries a removed worktree may lose (empty for now) (required by <<ISSUE:9>>).
- [ ] Must deliver: an `actReap` sequence in which a refusal from any step before the first removal keeps the unit with nothing moved or removed, and a named point before each `claude rm` where a check can keep the unit (required by <<ISSUE:10>>).
- [ ] Must deliver: the per-unit loop in `runPass` under the instance lock taken without blocking, with the hashes of `.niwa/` records taken at the re-read and compared at the tombstone, so a mark file added later is covered by the same check, and lock files that are 0600 with `O_NOFOLLOW` (required by <<ISSUE:12>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:2>>, <<ISSUE:3>>, <<ISSUE:6>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 9: feat(reap): remove worktrees inside the instance before the session, by a no-follow walk

**Goal**: Refine the skeleton's removal path so the pass and `runDestroyInstance` remove each worktree inside the instance before `claude rm`, through `rmtree.go`'s descriptor-relative no-follow walk with a `gitCommand`-built status before each one, so a clean unit holding a niwa worktree leaves no session behind and a link swapped in mid-removal deletes nothing outside the instance.

**Acceptance Criteria**:

- [ ] `internal/removal/rmtree.go` provides the descriptor-relative no-follow removal walk: every directory opened with `O_DIRECTORY|O_NOFOLLOW` and `fstat`ed, every entry removed relative to its parent's descriptor, a link met as a link removed as one and never followed, a directory on another device stopping the walk with "another filesystem at <path>", and any `.git` entry or git directory (a `HEAD` beside `objects/` and `refs/`) other than a worktree's own top-level `.git` file stopping the walk with the rest of the tree kept and the path named.
- [ ] `RemoveWorktree` in `rmtree.go` keeps the worktree, with the path named, when its top-level `.git` file is not a regular file opened `O_NOFOLLOW|O_NONBLOCK` or doesn't name its admin directory, when the admin directory holds a `locked` file, or when the walk finds a repository below the tree's top level or a git directory in the admin directory; otherwise it removes the tree with its `.git` file last, then the admin directory, then `worktrees/` if empty.
- [ ] An open that fails during the walk (a directory turned into a link, or any other failure) stops it, keeps the rest of the tree and the admin directory, and keeps the unit with "changed during the pass: <path>", so the worktree stays registered.
- [ ] In the pass, each worktree niwa's walk found inside a unit is removed after the job-directory vet of every listed session (from <<ISSUE:10>>) and before any `claude rm`, and only after a fresh read of its HEAD and per-worktree refs matches the snapshot and a `git status` built by `gitCommand` reports no entry; any difference keeps the unit.
- [ ] `runDestroyInstance` removes worktrees inside the instance after the scan and the job-directory vet of every listed session, and before `removeOwningSessionsListed`, always through the `rmtree.go` walk. Unforced, the worktree's `git status` runs with the scan, and a change it reports, or a worktree the walk keeps (locked, holding a repository), joins the scan's findings with the path named and is handled as destroy handles them today: on a terminal destroy lists it and asks for the instance name, going ahead only on a match (a worktree the walk won't remove then goes with the instance directory), and when stdin isn't a terminal it aborts with nothing removed. A change the status before a worktree's removal finds that the listing didn't show stops the destroy before that worktree goes, with the path named.
- [ ] `niwa destroy --force` skips that status, removes worktrees with the same walk before the session, and leaves a worktree the walk won't remove (locked, holding a repository, a `.git` file that doesn't name its admin directory, a link where a directory should be) to go with the instance directory.
- [ ] Swap scenario, deep entry: a clean unit whose niwa worktree holds `deep/a/b/file.txt`, with an outside directory holding a marker file whose listing, sizes and hashes are recorded first; a test hook in the walk, after it reads an entry's type and before it opens it, renames `deep/` aside and puts a symlink to the outside directory in its place. Through a reap, `niwa destroy` and `niwa destroy --force`, the outside directory's listing, sizes and hashes match and the marker is still there.
- [ ] Swap scenario, worktree directory: the same setup with the hook swapping the worktree's own directory for such a symlink before the removal opens it; through the same three paths nothing outside the instance is deleted.
- [ ] Swap scenario, admin directory: the same setup with the hook swapping the worktree's `worktrees/<id>` admin directory for such a symlink before the walk opens it; through the same three paths nothing outside the instance is deleted.
- [ ] In every swap run, the reap keeps the unit with "changed during the pass" and the swapped path named, an unforced `niwa destroy` refuses the same way, and under `--force` the worktree goes with the instance directory while the outside directory stays unchanged after the destroy.
- [ ] A clean unit holding a niwa worktree leaves no session behind after a reap and after `niwa destroy`: a `claude agents` listing after each shows no session for it.
- [ ] Across the reap and destroy scenarios, no `git` call a wrapper logs is `git worktree remove` or `git worktree prune`, and none carries `--force` outside `niwa destroy --force`.
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept by `niwa reap` with its first keeping check and branch named, and a clean one is still removed.
- [ ] Must deliver: `RemoveWorktree` and the removal walk as an exported surface the worktree package can take as a `WorktreeRemover` beside its `GitInvoker` without importing `internal/removal`, and the swap scenarios' test hook and setup in a form the hook worktree sweep's runs can reuse (required by <<ISSUE:11>>).
- [ ] Must deliver: the pass's worktrees-before-the-session order, never forced, as the step a marked unit's reap runs through (required by <<ISSUE:13>>).
- [ ] Must deliver: the walk callable on an arbitrary tree from the instance's directory descriptor, stopping at any `.git` entry or git directory and at another device (required by <<ISSUE:18>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:7>>, <<ISSUE:8>>, <<ISSUE:10>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 10: feat(session): vet each session's job directory before every claude rm

**Goal**: `realRemoveClaudeSession` vets the session's own job directory before every `claude rm` niwa makes, in the pass, in destroy and in retask's `removeCopy`, and the pass and destroy vet every listed session before their first removal step, so a linked, foreign-owned, writable, other-device or missing job directory, or a link, socket, pipe or device inside one, keeps the unit with the path named and no `claude rm` runs.

**Acceptance Criteria**:

- [ ] `realRemoveClaudeSession` runs the job-directory vet on that session's own `~/.claude/jobs/<id>` (its root chain and every entry under it) right before each `claude rm`, and makes no call when the vet fails, returning the failing path as the reason.
- [ ] `realRemoveClaudeSession` runs `claude` by the absolute path resolved from the filtered `PATH`, from an empty directory under `~/.niwa/w-<key>/scratch/` that is checked for owner and mode before each use, and never passes `--discard-unpushed` or `--force-remove-worktree`.
- [ ] Before its first removal step the pass vets every session it will pass to `claude rm` (those in the decoded `claude agents` listing) and, when one fails, removes nothing for that unit and keeps it with the failing path named. The vet runs under the pass's deadline, and a vet the deadline cuts short leaves the unit in place.
- [ ] Before its first removal step `niwa destroy` vets every mapped session the listing holds that it will pass to `claude rm`. On any failure it stops with the failing path named, and the instance, its worktrees and every session stay. `--force` doesn't skip the vet, and `--keep-session` passes no session and so vets none. `niwa destroy --workspace` vets every instance's mapped sessions the same way before its first removal step, and on any failure stops with the failing path named and removes nothing.
- [ ] In `niwa destroy` and retask, which have no all-unit deadline, the vet runs under its own 10-second context and keeps when that expires.
- [ ] A `CLAUDE_CONFIG_DIR` that is relative, holds `..` or `.`, has a repeated or trailing separator, or starts with `~` keeps the unit with the value named. A valid absolute value moves the vet and the rule's walk to that one root.
- [ ] Security scenario, through a reap: a clean unit whose `~/.claude/jobs/<id>` is a symlink to a directory outside the sandbox holding a marker file (listing, sizes and hashes recorded first) keeps with the failing path named. A `claude` wrapper logs no `claude rm`, and the outside directory's listing, sizes and hashes match with the marker still there.
- [ ] Same scenario with `~/.claude/jobs/` itself as that symlink: same result.
- [ ] Same scenario with `~/.claude` itself as that symlink: same result.
- [ ] Same scenario with `~/.claude/jobs/<id>` on another device (a mounted disk image): same result.
- [ ] Same scenario with `~/.claude/jobs/<id>` a real directory owned by another uid: same result.
- [ ] Same scenario with `~/.claude/jobs/<id>` a real directory with group or other write permission: same result.
- [ ] Functional scenario for the vet's scope, through a reap: a unit whose session the person already deleted, absent from the `claude agents` listing, reaps with no `claude rm` for it, while a listed session with no job directory keeps its unit with the missing path named.
- [ ] Inner-link scenario, through a reap: a clean unit with no handoff brief whose `~/.claude/jobs/<id>/scratch/out` is a symlink to a directory outside the sandbox holding a marker file keeps with that entry's path named. A `claude` wrapper logs no `claude rm`, and the outside directory is unchanged with the marker still there.
- [ ] Inner-link scenario with that entry a named pipe, through a reap: same result.
- [ ] Inner-link scenario with the link under a session mapped to an instance, through `niwa destroy`: destroy stops with the entry's path named, no `claude rm` is logged, and the outside directory is unchanged.
- [ ] Inner-link scenario with the link under a copy session retask started, removed through `removeCopy`: retask leaves the copy in place with the entry's path named, no `claude rm` is logged, and the outside directory is unchanged.
- [ ] Control run: with the inner link replaced by a regular file, the unit reaps and a `claude` wrapper logs exactly one `claude rm` for that session.
- [ ] No `claude` call a wrapper logs, through a reap, `niwa destroy` or retask, carries an override flag, and every one runs from a directory outside the workspace.
- [ ] An instance whose `.claude/settings.json` and `settings.local.json` define every hook event with a command that writes a marker file writes no marker through a reap and through `niwa destroy`.
- [ ] The path a failing vet names is the same on every run, because entries are read sorted by name.
- [ ] Must deliver: the vet inside `realRemoveClaudeSession` and the pre-removal vet of every listed session, both exposed to the pass, so a reap on a retire mark gets the same keep-on-failure behaviour with no extra wiring (required by <<ISSUE:13>>).
- [ ] E2E flow still works (do not break the skeleton): the skeleton's reap scenario still keeps a deleted session's instance that holds an unpushed commit and still removes a clean one.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:6>>, <<ISSUE:8>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 11: fix(cli): run failed-dispatch cleanup, the watch's discard, the hook worktree sweep and destroy's scan through the rule

**Goal**: Failed-dispatch cleanup, the watch's discard and the watch's failed-stage rollback call `removal.Evaluate` before `destroyInstanceFunc`, and the hook worktree sweep and `niwa destroy`'s scan build every git call with `gitCommand` and run the program-naming configuration check on every repository they find, with `DestroySession` removing through the `WorktreeRemover`, so `partial.txt` survives a failed dispatch and a planted driver keeps the worktree or refuses an unforced destroy with the key named and never runs.

**Acceptance Criteria**:

Failed-dispatch cleanup, the watch's discard and the watch's failed-stage rollback:

- [ ] The failed-dispatch rollback calls `removal.Evaluate` for its instance before `destroyInstanceFunc`; a keeping verdict leaves the instance directory on disk and prints the first keeping check with the path it names.
- [ ] Scenario: a dispatch that fails after a file `partial.txt` is written in its instance leaves `partial.txt` in place with its contents unchanged, and the output names it.
- [ ] Scenario: a dispatch that fails without writing anything leaves no instance behind.
- [ ] `discardStagedRecord` calls `removal.Evaluate` for the record's instance before `destroyInstanceFunc`; a keeping verdict leaves the instance directory on disk and names the check and path.
- [ ] The deferred rollback `stageReview` arms in `internal/cli/watch.go`, which runs when staging fails, including after the review agent has launched, calls `removal.Evaluate` for its instance before `destroyInstanceFunc`, as failed-dispatch cleanup does; a keeping verdict leaves the instance directory on disk and names the check and path.
- [ ] Scenario: a watch stage in which the review agent writes a file with unique contents in its instance and staging then fails leaves that file in place with its contents unchanged, the instance stays, and the output names the keeping check and the file.

The hook worktree sweep and `DestroySession`:

- [ ] Before any other git call, the sweep runs the removal walk over each worktree and the program-naming configuration check on every repository it finds there (the worktree's own, each submodule, gitlink and nested repository), and evaluates the worktree's repository checks; a flagged or failing repository keeps that worktree with the key or check named.
- [ ] The sweep calls `DestroySession` with force off, a git invoker built on `gitCommand` and the `rmtree.go` remover as its `WorktreeRemover`; the reap-time sweep (`internal/cli/reap_worktrees.go`) passes `worktree.StdGitInvoker` nowhere any more.
- [ ] `DestroySession` takes a `WorktreeRemover` beside its `GitInvoker` and removes the worktree through it before `git branch -d`. With the sweep's `rmtree.go` remover it makes no `git worktree remove` call, forced or not; that remover takes the worktree's tree and admin directory, and a removal that stops returns an error naming the path.
- [ ] The other callers of `DestroySession`, the hook teardown (`teardownHookSession`, `internal/cli/session_from_hook_cmd.go:376`), the failed-hook reconcile (`reconcileFailedHookCreate`, `session_from_hook_cmd.go:254`), the person-run worktree destroy (`destroyOneWorktree`, `internal/cli/session_lifecycle_cmd.go:663`) and `destroySessionWorktrees` (`internal/cli/worktree_destroy_resolve.go:404`), keep today's remover and invoker: they pass `worktree.StdGitInvoker` and a remover that runs today's `git worktree remove --force` through it, so what they do doesn't change.
- [ ] With force off, a change `DestroySession`'s own `git status` reports keeps the worktree, named like any other disagreement; with force on, that status is skipped as today.
- [ ] `DestroySession`'s `git status` carries `--ignore-submodules=all` and `--untracked-files=all`, and the invoker runs the configuration check again right before that status and right before the removal, returning an error that names the key and keeps the worktree.

`niwa destroy`'s scan:

- [ ] `runDestroyInstance` passes `workspace.ScanInstance` a `ScanOption` carrying a command builder made from `gitCommand`, `gitOutput` starts git only through it, and `internal/workspace` imports nothing from `internal/removal`.
- [ ] The scan's durability check in `internal/workspace/durable.go`, `gitNetwork` (`ls-remote` and `fetch`) and `gitOutputEnv`, also starts git only through that builder, so no call the scan makes starts plain `git` with the inherited environment.
- [ ] `scanStatus` passes `--porcelain=v1 --ignore-submodules=all --untracked-files=all`, and every log-family call the scan makes carries `--no-show-signature`.
- [ ] The scan runs the same status in each submodule and nested repository the walk hands it, after that repository's configuration check, and a change found in one is loss the way a change in the parent is. Scenario: an instance whose submodule holds a modified tracked file makes an unforced `niwa destroy` refuse as loss with the submodule's path named, and the instance stays.
- [ ] Before the scan's first git call, and before `--force` removes a worktree, `runDestroyInstance` runs the removal walk over the instance and the configuration check on every repository it finds, submodules, gitlinks and nested repositories included; a flagged repository gets no further git call and is reported as a `LossScanError` naming the key, so an unforced destroy lists it as loss, asks for the instance name on a terminal and goes ahead only on a match, and aborts when stdin isn't a terminal; once confirmed, and under `--force`, that worktree goes with the instance directory.
- [ ] The builder runs the check again right before the scan's `git status` in each repository and before the status that precedes each unforced worktree removal; a key found at the scan's status is listed as loss the same way, and one first found at the status before a worktree's removal, after the confirmation, stops the destroy before that worktree goes, with the key named. A `--force` removal starts no git process.
- [ ] `niwa destroy --workspace` (`runDestroyWorkspace`) goes through the same scan and order as an unforced `runDestroyInstance`: it passes `workspace.ScanInstancesParallel` the same `gitCommand`-built `ScanOption` after the same walk and configuration check on every instance, and lists a change, a flagged key or a worktree the walk keeps with the rest of the scan's findings, handled as it handles them today: without `--confirm` it asks for the workspace name on a terminal and goes ahead only on a match, and aborts when stdin isn't a terminal; with `--confirm <name>` it goes ahead with no prompt; it takes no `--force`. After the confirmation and the job-directory vet of every listed session, and before `removeOwningSessionsListed`, it removes each instance's worktrees through the `rmtree.go` walk with the same status before each one, a change the listing didn't show stopping the destroy with the path named before any `claude rm` or instance directory removal. Scenario: `niwa destroy --workspace --confirm <name>` on a workspace whose instance holds a clean niwa worktree and a stopped session leaves no session in a `claude agents` listing, and a `claude` wrapper that records whether the worktree path exists when `claude rm` runs finds it gone; an instance holding an unpushed commit is listed as loss the way single-instance destroy lists it.

Security scenarios:

- [ ] A hook-provisioned worktree's repository, and separately an instance repository, with `core.fsmonitor` set to a script that writes a marker file, run through the hook worktree sweep (by `niwa dispatch`) and through `niwa destroy`: no marker file exists afterwards.
- [ ] A `filter.x.clean` driver in such a repository keeps the worktree through the sweep, and an unforced `niwa destroy` exits non-zero with the key named.
- [ ] A submodule whose own configuration carries a `filter.x.clean` driver, with a `.gitattributes` entry and a stat-dirty file that would trigger it, keeps the unit through a reap, `niwa reap --verdict-only` (from <<ISSUE:3>>) and the hook worktree sweep, makes an unforced `niwa destroy` refuse with the key named, and leaves no marker file.
- [ ] The same as the previous scenario with a nested repository registered as a gitlink in place of the submodule.
- [ ] The same driver planted after the first check, before a worktree's status or removal in a reap, keeps the unit with "changed during the pass: repository configuration names a program: <key>" and no marker file.
- [ ] The same driver planted after the first check, before `DestroySession`'s status or removal in the hook worktree sweep, keeps the worktree with the key named and no marker file.
- [ ] The same driver planted after the first check, before the scan's status or an unforced worktree removal's status in `niwa destroy`, makes the destroy refuse with the key named and leaves no marker file.
- [ ] Swap scenarios through the hook worktree sweep (by `niwa dispatch`), with the setup and test hook of <<ISSUE:9>>'s three runs (a `deep/` entry swapped for a symlink to an outside directory holding a marker file, the worktree's own directory swapped, its `worktrees/<id>` admin directory swapped): in every run the outside directory's listing, sizes and hashes match the ones recorded first, the marker file is still there, and the sweep keeps the worktree with "changed during the pass" and the swapped path named.
- [ ] Across the sweep scenarios, no `git` call a wrapper logs is `git worktree remove` or `git worktree prune`, and none carries `--force`.

Skeleton and downstream:

- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept by `niwa reap` with its first keeping check and branch named, and a clean one is still removed.
- [ ] Must deliver: every removal niwa makes without a person naming it (failed-dispatch cleanup, the watch's discard, the watch's failed-stage rollback, the hook worktree sweep, and the pass) decides through `removal.Evaluate` or `gitCommand` with the configuration check, and the sweep never forces a worktree's removal, so the marked-unit reap adds a reason to remove without adding a path around the rule (required by <<ISSUE:13>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:3>>, <<ISSUE:7>>, <<ISSUE:9>>, <<ISSUE:10>>

**Repo**: tsukumogami/niwa

**Group**: rule-and-worktrees

### Issue 12: feat(over): add the record ledger and the over seam that writes a vouched retire mark

**Goal**: With `[session.over]` configured, the pass asks each stopped unit's over command, and exit 0 writes `retired.json` with its `LedgerStore` entry while any other outcome leaves no mark, and a mark that doesn't parse, was copied, was removed or was written by hand without a ledger entry reads as no mark, cleaned up under the instance lock.

**Acceptance Criteria**:

Configuration and the seam:
- [ ] A workspace with `[session.over]` `command`, `timeout` and `env` loads; an invalid timeout or env name fails the configuration load with the field named; leaving the table out turns marking off and no over command runs.
- [ ] The over command runs as `/bin/sh -c` from the workspace root in its own process group with only `PATH`, `HOME`, `NIWA_REMOVE_PATH` (the instance path), `NIWA_SESSION_IDS` and the configured env names set, and its stdin empty.
- [ ] The pass asks every unit that is stopped, carries no valid mark and isn't held, and asks each of them again at every pass, within the pass's budget and the over command's per-run deadline, so a unit answered not over or unknown at one pass is asked again at the next; a unit with a valid mark is never asked again.
- [ ] Three answers: exit 0 writes `<instance>/.niwa/retired.json`; exit 1 writes no mark and hands the reason to the pass, which <<ISSUE:16>> stores in `unit.json`; exit 3 (and any other code, a signal or a command that can't start) reads as unknown, keeps the unit and writes no mark. The reason is the last non-empty stdout line, sanitized and no longer than 200 characters.
- [ ] Timeout: an over command that sleeps past its deadline gets `SIGTERM` to its process group, then `SIGKILL` two seconds later; after the pass no process from that group is left running and no mark exists.
- [ ] The pass makes no koto or forge call to decide whether a unit is over. Scenario: with no over command configured and wrappers for `koto` and `gh` on `PATH` that log calls, a dispatch over a stopped unit whose pull request merged and whose koto run is terminal leaves the unit unmarked and still listed by `niwa list`, and neither wrapper logs a call from niwa.
- [ ] The ask step skips a unit the rule reports as held: a unit test giving the pass a held verdict for a stopped, unmarked unit shows the seam is never called for it and no mark is written. The functional scenario with a real `keep.json` hold belongs to <<ISSUE:20>>.
- [ ] After any answer but exit 0, a `retired.json` that is a regular file owned by the user with `marked_at` later than the call's start is removed, so a command that writes its own mark and then exits 1 or times out leaves none. The question and this clean-up both run under the instance lock, taken without blocking: a test holding the instance lock shows the pass asks nothing, removes no mark and leaves the unit for a later pass.

The mark and the ledger:
- [ ] The mark is written atomically (exclusive temp file in `<instance>/.niwa/`, mode 0600, then rename) with the earlier remover's schema name, `instance_name`, `instance_created` from `instance.json`, `marked_at` in UTC RFC 3339, `marked_by` naming niwa's version and the writer (over command or retire), and a basis of the sessions read and the branches and heads the repository walk found, with no runs or pull requests.
- [ ] The mark's ledger entry (SHA-256, size, write time, writer `over`) is in `~/.niwa/w-<key>/ledger/<instance>.json`, mode 0600, before the mark is renamed into place; a reader never sees the mark without its entry.
- [ ] A mark that doesn't parse reads as no mark.
- [ ] A copied mark, taken from another instance (another directory or creation time), reads as no mark in the instance it was copied into.
- [ ] A removed mark: after `retired.json` is deleted by hand, the unit reads as unmarked and is asked again at the next pass.
- [ ] A mark written by hand with no ledger entry reads as no mark, and the reader reports it as "mark not written by niwa".
- [ ] A mark that is a symlink, not owned by the user, larger than 64 KiB, carrying another schema name or dated in the future reads as no mark; the reader opens it `O_NOFOLLOW|O_NONBLOCK` from an `O_DIRECTORY|O_NOFOLLOW` descriptor for `.niwa` and `fstat`s it as a regular file before reading.
- [ ] A session in the unit active after `marked_at`, or a basis branch whose head moved or vanished, voids the mark: the file stays in place, the reader reports "mark voided" with the reason, and the next yes overwrites it. Deleting an untracked file voids nothing.
- [ ] `LedgerStore` removes a unit's ledger file after `DestroyInstance`, and ignores and removes a ledger whose instance is gone or whose creation time differs.
- [ ] `DestroyWorkspace` removes `~/.niwa/w-<key>/ledger/` with the workspace.
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept and printed with its first keeping check and the branch named, and a clean one is still removed.

Downstream deliverables:
- [ ] Must deliver: a mark reader returning valid, voided (with reason) or no mark (with "mark not written by niwa" where it applies), the seam called from the pass, and a place to record a refused removal (the refusing step and its message) against a mark that stays valid, so marked-unit reaps can be gated on it and report a disagreement (required by <<ISSUE:13>>).
- [ ] Must deliver: the over-command contract as built (env names, exit codes, reason line, deadline and kill) and the ledger path and schema, stable enough to document (required by <<ISSUE:15>>).
- [ ] Must deliver: each unit's last answer and reason, its mark state and any disagreement in a form the pass can write into `unit.json`, and `LedgerStore` able to vouch for `unit.json` with writer `pass` (required by <<ISSUE:16>>).
- [ ] Must deliver: an ask path that returns the answer and the mark it would write without writing a mark or a ledger entry (required by <<ISSUE:17>>).
- [ ] Must deliver: `LedgerStore` entries for any record under `.niwa/`, `pids/<pid>.json` included, with writer `helper` (required by <<ISSUE:19>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:8>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 13: feat(reap): reap a marked unit once the rule passes it, keeping one with no launch record

**Goal**: Refine the skeleton's reap path so the pass reaps a unit carrying a valid mark when it has the launch record `niwa dispatch` already writes and the rule passes it, worktrees before the session and never forced, and keeps a marked unit with no launch record with that reason, so `niwa resume` remains the undo for a reap on a mark.

**Acceptance Criteria**:

- [ ] A stopped, clean, dispatched unit (launch record present) whose over command exits 0 is reaped by the next pass: the mark is written, the gate is asked with the step name `reap`, the instance directory and its session mapping are gone, and reap's output names it as removed.
- [ ] In that reap, every worktree inside the instance is removed before the session, and a `claude` wrapper's log shows exactly one `claude rm` per session, made after that session's job-directory vet, with no force flag; no `--force`, `git worktree remove` or `git worktree prune` is run.
- [ ] After that reap, the unit's launch record is still in `<root>/.niwa-launch/` and `niwa resume <instance>` finds it (the undo path stays available); the pass doesn't remove the record.
- [ ] A marked, stopped, clean unit whose launch record was never written is kept, the instance and its session are untouched, and reap's output names the instance with "no launch record" as its keeping check.
- [ ] A marked unit whose launch record exists but fails strict decoding is kept with the unreadable record named, and nothing is removed.
- [ ] A marked unit with no launch record that also holds an unpushed commit reports "no launch record" as its first keeping check, not the commit, matching the rule's fixed check order.
- [ ] A marked unit whose launch record is present but whose clone holds a commit no proof remote holds is kept with that commit's check, so the mark never overrides the rule.
- [ ] A unit whose session was deleted, with no mark and no launch record, is still reaped under the rule as before (the delete-then-reap path needs neither).
- [ ] Disagreement: a marked, stopped, clean unit with its launch record whose removal is refused, in one run by the gate (a `[session.before_remove]` command exiting non-zero for step `reap`), in another by niwa (its job-directory vet failing) and in a third by Claude Code (a `claude rm` wrapper exiting non-zero), stays with its instance in place, its mark still reads as valid, reap's output names the refusing step and its message, and the next pass doesn't ask its over command again.
- [ ] Launch-record test setup: a unit the SessionStart hook provisioned at the workspace root, stopped, clean and marked by the over command, is kept by the next dispatch's pass and by `niwa reap`, each naming it with "no launch record".
- [ ] Six kinds: with an over command that answers each of six stopped units with the answers and reasons in the design's table (`not over: run not finished` with the run's state, `not over: blocked, waiting for a person`, `not over: pull request open`, `unknown: no takeover found`, `unknown: no evidence` for a top session and for a plain chat), no unit is marked and every unit is kept with its instance and session in place; the listing of each one's answer and reason belongs to <<ISSUE:16>>.
- [ ] Must deliver: a pass entry that reaps one named unit on its mark through the same launch-record check, gate, vet, worktree-first removal and tombstone path, reporting removed or kept with the keeping check and paths, so the retire command can call it with the `retire` step name (required by <<ISSUE:14>>).
- [ ] Must deliver: a per-unit pass outcome that records whether a unit was reaped on a mark, reaped on a deleted session, or kept, with its first keeping check and the over answer, for the pass to store in `unit.json` and count on the listing and dispatch line (required by <<ISSUE:16>>).
- [ ] Must deliver: one place in the marked-reap path, after `DestroyInstance` succeeds, where the launch record is in hand to receive `reaped_at`, `restore_until` and `kept_entry`, and the hook-provisioned "no launch record" scenario kept as a separate test the later change can move to a unit whose launch record was removed by hand (required by <<ISSUE:23>>).
- [ ] E2E flow still works (do not break the skeleton)

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:9>>, <<ISSUE:10>>, <<ISSUE:11>>, <<ISSUE:12>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 14: feat(cli): add niwa retire with the instance guard and a terminal-confirmed override

**Goal**: A person runs `niwa retire <instance>` from a shell outside the instances to write the mark and run the one-unit pass, which prints removed or kept with the check and paths, while it refuses inside an instance through `insideInstance` and `--override-not-over` refuses without a controlling terminal or a correct code typed back through `confirmOnTerminal`.

**Acceptance Criteria**:

- [ ] `niwa retire <instance>` resolves the instance by name at the workspace root, or from a path given as the argument, takes the instance lock without blocking (when another niwa command holds it, retire reports the unit kept, held by another niwa command, try again, and writes no mark, which a test holding the lock shows), writes the mark through the mark writer with `marked_by` naming a person's retire and its ledger entry recorded before the rename, then runs the one-unit pass on that instance.
- [ ] A retire whose unit the rule passes prints `niwa: retired <name> and removed it`; one the rule keeps prints `niwa: retired <name>; kept by <check>: <paths>`, naming the first keeping check and every path behind it, or the step and message of a disagreement.
- [ ] A retire of an instance whose session is working refuses with `niwa: cannot retire <name>: its session is working` and writes no mark.
- [ ] A retire of a unit whose last answer was not over, without `--override-not-over`, refuses with `niwa: cannot retire <name>: its over command answered not over (<reason>); pass --override-not-over to retire it anyway` and writes no mark. The pass never sets the flag.
- [ ] `insideInstance` in `internal/cli/guard.go` answers yes when the working directory resolves inside any instance of the workspace, when `NIWA_DISPATCH_ID` or `NIWA_REAP_KEY` is in the environment, or when an ancestor process, found by parent pid in the pass's process listing from `internal/removal/procs.go`, has its working directory inside an instance; when it answers yes, `niwa retire` (with or without the override) prints `niwa: cannot retire from inside an instance; run it from a shell outside the workspace's instances`, exits non-zero and writes no mark.
- [ ] `confirmOnTerminal` in `internal/cli/confirm.go` opens `/dev/tty`, prints the unit's name, its last not-over reason and an eight-character code drawn fresh from `crypto/rand` for that call as `type <code> to retire <name> although its over command answered not over:`, and proceeds only when that code is typed back on the same terminal. No flag skips it.
- [ ] Scenario: `niwa retire` run with its working directory inside an instance refuses with the inside-an-instance message, and the instance and its mark state are unchanged.
- [ ] Scenario: `niwa retire` run from a process outside the instances whose ancestor process has its working directory inside an instance refuses with the same message, and the instance is unchanged.
- [ ] Scenario: a detached `niwa retire --override-not-over` (run under `setsid` with no controlling terminal) refuses with `niwa: cannot override for <name>: no controlling terminal`, writes no mark and removes nothing.
- [ ] Scenario: with `--override-not-over` on a terminal, typing the instance name in place of the printed code refuses, writes no mark and removes nothing.
- [ ] Scenario: with `--override-not-over` and the printed code typed back on a test pty, the retire of a clean not-over unit removes it, and the retire of a not-over unit whose clone holds an untracked `notes.txt` reports it kept and names `notes.txt`.
- [ ] Scenario: retire asks the `[session.before_remove]` gate with step `retire`; with a gate command that exits 1 and prints `held by test`, the retire of a clean stopped unit reports it kept by a disagreement naming the veto and `held by test`, `niwa list` shows the same, and the unit stays marked.
- [ ] Scenario: a retire of a clean, stopped unit whose session `claude agents` lists reaps the instance, and a `claude` wrapper logs exactly one `claude rm` for that session, after that session's job-directory vet.
- [ ] In retire, which has no all-unit deadline, the vet runs under its own 10-second context and keeps the unit when that expires.
- [ ] Scenario: a unit a retire reaped is brought back by `niwa resume` under its original name, from the launch record dispatch wrote, with its session started.
- [ ] A retire passes no force flag to `claude` or to git and stops no running process.
- [ ] Scenario (one rule): `niwa reap --verdict-only`, `niwa retire`, `niwa reap` and the pass before `niwa dispatch` give the same verdict for the same unit, on one unit of each verdict, and the verdict-only `--json` output for that one unit carries the same verdict and the same first keeping check.
- [ ] Must deliver: `insideInstance` and `confirmOnTerminal` as reusable functions taking the instance and the text to show (what the command will release and the prompt), so `niwa discard` can call both with its own messages (required by <<ISSUE:22>>).
- [ ] E2E flow still works (do not break the skeleton): the reap of a deleted session's instance holding an unpushed commit still keeps it and names the path.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:13>>, <<ISSUE:16>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 15: docs(guides): write the over-command guide with the contract and the takeover key

**Goal**: Add `docs/guides/over-command.md`, which documents the over-command contract, the takeover key and the advice to treat the instance as untrusted input, notes that the ledger is advisory for a worker without the sandbox the deny-write list names, and comes with a functional test that runs the guide's takeover-key example against a real `koto init --supersede`.

**Acceptance Criteria**:

- [ ] `docs/guides/over-command.md` exists and shows a `[session.over]` example with `command`, `timeout` (default `"20s"`) and `env`, and says that leaving the table out turns marking off.
- [ ] The guide states the exit contract: exit 0 means over and niwa writes the mark, exit 1 means not over, and any other outcome (another exit code, a signal, a command that can't start, the deadline) means unknown and the unit is kept.
- [ ] The guide states how niwa runs the command: `/bin/sh -c` from the workspace root, in its own process group, with only `PATH`, `HOME`, `NIWA_REMOVE_PATH` (the instance path) and `NIWA_SESSION_IDS` plus the configured `env` names, and on the deadline `SIGTERM` to the group followed by `SIGKILL` two seconds later.
- [ ] The guide states the reason line: the last non-empty line of standard output, sanitized and cut to 200 characters, stored and shown in the listing.
- [ ] The guide says niwa writes `<instance>/.niwa/retired.json` itself, that a mark the command writes during a call that doesn't exit 0 is removed, and that a mark without niwa's ledger entry counts as no mark, so a command should only answer and never write the mark.
- [ ] The guide says which units niwa asks about (stopped, carrying no valid mark, not held), that it asks each of them again at every pass, and that a yes is never asked again.
- [ ] The guide defines a landed takeover as both of these holding: the unit's own koto session carries a `session_superseded` fence naming a successor, and that successor's context holds `koto/superseded-carry-complete`. It says a fence without the key is a takeover in progress and must answer not over, that a unit without a fence is answered on its own run, and that a unit with no koto run answers unknown with "no takeover found".
- [ ] The guide gives a takeover-key example command that a reader can copy: exit 0 for a landed takeover, exit 1 for a fence without the key, and an unknown exit with the reason "no takeover found" for a unit with no koto run.
- [ ] The guide tells authors to treat the instance as untrusted input: read it as data, never `cd` into it and run `git`, `make` or a script from there, and never source a file from it.
- [ ] The guide notes that the record ledger is advisory for a worker that runs without sandbox write restrictions covering Bash, because such an agent can write a ledger entry as easily as a record, and it links to the deny-write list in `docs/guides/ephemeral-session-instances.md` instead of repeating it.
- [ ] The guide tells a workspace that configures `[session.over]` for shirabe's workflows to set `[session.removal] job_scratch = "unique"` in the same file.
- [ ] A functional test runs the guide's takeover-key example against a predecessor and successor made by a real `koto init SUCC --continues PRED --supersede` (koto is not stubbed) and sees exit 0 with the over reason for the predecessor once the successor holds `koto/superseded-carry-complete`.
- [ ] The same test runs the example against a unit with no koto run and sees the unknown exit with the reason "no takeover found", and against a koto session with no fence and sees an exit other than 0.
- [ ] The test runs in a CI job that supplies a real koto binary, and fails with a message naming koto when that binary is missing rather than skipping silently. `TestCI_AuthorityRunsWithoutKoto` in `test/functional/authority_ci_test.go` stays green, so koto never resolves on the PATH the authority tests run with.
- [ ] E2E flow still works (do not break the skeleton): the reap of a deleted session's instance holding an unpushed commit still keeps the unit and names the path.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:5>>, <<ISSUE:12>>, <<ISSUE:16>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 16: feat(list): show each stopped unit's answer, age and size in niwa list and the dispatch line

**Goal**: The pass writes `unit.json` with its ledger entry, `niwa list` prints a state line and a JSON `unit` object per instance re-scrubbed on read, each dispatch prints the fixed count line, reap prints its counts, and the pass takes units by `reached_at` with never-reached first, all read locally with the network off.

**Acceptance Criteria**:

- [ ] For each unit it reaches, the pass writes `<instance>/.niwa/unit.json` with mode 0600 and schema `niwa.unit/v1`, holding `answer` (over, not over, unknown, no command configured), `exit`, `reason`, `asked_at`, `stopped_since` (the later of the transcript's modification time and the job entry's update time), `size_bytes` and `size_measured_at` from the rule's own walk, `verdict`, `kept_by` (check, paths, evidence), `disagreement`, `mark_voided`, `reached_at` and `restored_at`, and records the file's hash in the workspace ledger.
- [ ] Every string stored in `unit.json` (the over command's reason, git error evidence, paths) has passed `removal.ScrubEvidence` first: a reason or evidence holding `https://user:ghp_xxx@host/x` or a `?access_token=` value is stored with the userinfo stripped and the token as `[redacted]`.
- [ ] Scenario, the `unit.json` half of the credential scenario whose output half <<ISSUE:3>> covers: after a pass over a unit whose remote URL carries a credential (`https://user:secret@host/repo`) and a `?access_token=` query value, and whose evidence carries an `AKIA` key id and an `xoxb-` token, none of the four appears in that unit's `unit.json`.
- [ ] `unit.json` is read only after an `O_NOFOLLOW|O_NONBLOCK` open and an `fstat` showing a regular file owned by the user, capped at 64 KiB and decoded strictly; a symlinked, oversized, unknown-schema or hand-edited `unit.json` whose hash doesn't match its ledger entry is treated as never reached.
- [ ] The pass sorts units by `reached_at` from a ledger-matched `unit.json`, never-reached first, so a unit with no record or a non-matching one is taken before every unit with a valid one.
- [ ] Time budget scenario: with the 30-second deadline shortened in the test and more units than one pass can reach, `niwa reap` reports a non-zero count of units not reached, the units it didn't start get no answer and no mark, a unit the deadline cut short mid-evaluation keeps and has its `reached_at` set to that pass, and a vet the deadline cuts short leaves the unit counted as not reached.
- [ ] In the same scenario, the reported not-reached count equals the number of units with no call in an over-command wrapper's log, and `niwa reap`'s elapsed time is no more than the shortened deadline plus the elapsed time of a reap on a workspace with no stopped units.
- [ ] Units-not-reached scenario: running the next pass after that one takes the units the first pass didn't reach before the ones it did, shown by their `reached_at` values and by the second pass's not-reached count dropping.
- [ ] `niwa list` prints, under each instance's name, a state line read only from `unit.json` and the records: `stopped <age>, <size>, not over: <reason>`; `no over command configured`; `unknown: <reason>` when the command gave a reason, or `unknown (exit <n>)` when it gave none; `retired, kept by <check>: <paths>`; or `retired, kept by a disagreement: <step> refused: <message>`, with the existing `resume:` line kept; a unit whose mark was voided shows unmarked with `mark voided` and why, and one whose mark has no ledger entry with `mark not written by niwa`.
- [ ] Six kinds scenario, the listing half of the one <<ISSUE:13>> keeps: with an over command answering six stopped units as the design's table does, `niwa list` shows each one under its name, scrubbed, with its time stopped and size, the three not-over units as `stopped <age>, <size>, not over: <reason>` with the reasons `run not finished` (with the run's state), `blocked, waiting for a person` and `pull request open`, and the three unknown units as `unknown: no takeover found` or `unknown: no evidence` (for a top session and for a plain chat).
- [ ] `niwa list --json` keeps its array and adds a `unit` object to each record with `state`, `stopped_since`, `stopped_seconds`, `size_bytes`, `size_measured_at`, `answer`, `reason`, `retired`, `mark_voided`, `verdict`, `kept_by`, `disagreement`, `held` and `not_reached`; the existing fields and their `omitempty` behaviour are unchanged.
- [ ] Terminal-escape scenario: a `unit.json` written with a matching ledger entry whose `reason` and `kept_by.evidence` carry ANSI escape and control sequences and a token shape prints through `niwa list`, `niwa list --json` and the dispatch line with no escape or control byte and the token as `[redacted]`, because every stored string is scrubbed again on read.
- [ ] Network-off scenario: with every network call failing (a `git` wrapper that fails `ls-remote` and fetch, no route out), `niwa list` and `niwa list --json` exit 0 with the same output as with the network on, and a wrapper log shows no `git`, no over-command and no `claude` call made by the listing.
- [ ] Each `niwa dispatch` prints one line to standard error after its opportunistic pass, in the form `niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set entr(y|ies) due; <U> unit[s] not reached (niwa list)`, with sizes in binary units to one decimal place (`0 B`, `412 MiB`, `4.2 GiB`) and correct singular and plural forms.
- [ ] No-command dispatch scenario: in a workspace with no `[session.over]` command configured, `niwa dispatch` still prints the dispatch line, counting stopped units whose state is `no over command configured`.
- [ ] No-command removal scenario: in a workspace with no `[session.over]`, with two stopped, clean, dispatched units whose sessions are present and a listing of the workspace root, both instances and their job directories recorded first, `niwa dispatch` leaves both instances, their job directories and their transcripts on disk, the before and after listings differ only by the new worker, and the dispatch line counts 2 stopped units.
- [ ] The dispatch line's count and size leave out marked units the rule keeps, which the listing still shows as `retired, kept by ...`.
- [ ] `niwa reap` prints the stopped-not-retired, kept-set-due and not-reached counts together with its reaped and kept counts.
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept by `niwa reap` with its first keeping check and branch named, and a clean one is still removed through the gate and `DestroyInstance`.
- [ ] Must deliver: the reap count line and the `unit.json` writer in the pass, built so a trimmed count and a trim entry slot in beside the reaped and kept counts (required by <<ISSUE:18>>).
- [ ] Must deliver: the state-line printer and the `unit` object's `held` field, with a place for the `held by keep record (owner <o>): <reason>; review due <date>` state and a kept-set count source the dispatch line reads (required by <<ISSUE:20>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:13>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 17: feat(reap): show over answers in the verdict-only mode

**Goal**: `niwa reap --verdict-only` shows each unit's over answer alongside its verdict, with the mark each answer would write, and still writes no mark.

**Acceptance Criteria**:

- [ ] With `[session.over]` configured, `niwa reap --verdict-only` runs the over command once for each stopped, unmarked unit the acting pass's step 5 would ask, under the instance lock, with the same environment and timeout `overSeam` uses.
- [ ] A command that exits 0 makes the unit's entry show the answer `over` and the reason line. It also shows the mark niwa would write (the fields the <<ISSUE:12>> mark writer puts in `retired.json`) and the verdict that mark leads to: reap, or keep with each keeping check and its paths.
- [ ] A command that exits 1 makes the entry show `not over` with the reason from the last non-empty output line, no mark, and the verdict computed without one.
- [ ] A command that exits 2, is killed on its deadline or can't start makes the entry show `unknown` with the exit status or cause, and no mark. A timed-out command leaves no process from its group running after the mode exits.
- [ ] With no `[session.over]` table, each stopped unit's entry says no command is configured, the mode starts no process, and the verdicts match what <<ISSUE:3>>'s mode prints for the same workspace.
- [ ] A working unit and a unit with a valid, ledger-matched mark aren't asked. A working unit's entry says it's working. A marked unit's entry shows its existing mark and the verdict it leads to, and the mark file is byte-for-byte unchanged afterwards.
- [ ] After a run where the over command answered over for a unit, the unit has no `.niwa/retired.json`, its ledger has no new entry, and no `unit.json` was written or changed.
- [ ] An over command that writes `.niwa/retired.json` itself and then exits 0 leaves no `retired.json` after the mode exits, and the output still shows the mark niwa would write.
- [ ] `niwa reap --verdict-only <instance>` asks only that unit's over command. A second instance with a stopped, unmarked unit gets no call, which a command that logs `NIWA_REMOVE_PATH` to a file outside the workspace shows.
- [ ] `niwa reap --verdict-only --json` prints a `niwa.verdicts/v1` array whose entries carry the answer (using the values of `niwa.unit/v1`'s `answer` field), the exit status, the reason, the mark that would be written (or null) and the verdict it leads to, and the output decodes strictly.
- [ ] A reason line holding terminal escape sequences or a credential-shaped string (an `AKIA` key, an `xoxb-` token) prints scrubbed through `ScrubEvidence`, in both the text and the `--json` output.
- [ ] Apart from fetches into the scratch object store and what the over command does itself, the mode leaves the workspace as it found it. A before-and-after listing of the workspace root, each instance's `.niwa/` and the ledger directory shows no mark, ledger entry, unit record, trimmed tree or renamed tombstone; that the mode moves nothing into the kept set is checked in <<ISSUE:21>>.
- [ ] Functional scenarios cover the over, not over, unknown and no-command answers in the verdict-only mode, the mark-writing command whose mark is cleared, and the `--json` form.
- [ ] E2E flow still works (do not break the skeleton): reap still keeps a deleted session's instance that holds an unpushed commit and prints its first keeping check with the branch named.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:3>>, <<ISSUE:12>>, <<ISSUE:16>>, <<ISSUE:18>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 18: feat(reap): trim idle build trees in units that stay

**Goal**: Refine the skeleton's pass so that, with `trim` and `trim_after`, it removes a kept unit's idle build tree (no process in it, no repository inside it, no write newer than `trim_after`) unless the root apply lock is held, using `rmtree.go`'s no-follow walk from the instance's descriptor that stops at any `.git` entry or git directory, and reap counts what it trimmed.

**Acceptance Criteria**:

- [ ] `[session.removal] trim` (bool, default `true`) and `trim_after` (duration, default `24h`) parse from workspace config. An invalid `trim_after` fails config loading with a message that names the key. With `trim = false` the pass trims nothing and reap reports `0` trimmed.
- [ ] Root `niwa apply` holds an exclusive lock at `~/.niwa/locks/a-<workspace key>.lock` for its run. The trim tries that lock without blocking, and while apply holds it the pass trims nothing for that workspace, removes no file under any build tree and doesn't wait on the lock.
- [ ] `removal.Evaluate` returns `KeepAndTrim` for a stopped unit it keeps that has an idle build tree, with `Verdict.Trim` listing each tree the trim would remove. `niwa reap --verdict-only` shows those trees and leaves every file in them in place.
- [ ] Idle build-tree scenario: a stopped unit that stays (it is stopped but not over, or another check keeps it) has a git-ignored `target/` carrying `CACHEDIR.TAG` with the standard signature, and its newest write is older than `trim_after`. After `niwa reap`, `target/` is gone, the instance directory, its clone, its other files and its session are still there, and reap's count line reports `1` trimmed.
- [ ] Run with the over command answering not over for that unit, the same scenario then resumes the session, sends it one prompt and waits for it to stop: `claude agents --json --all` lists the session with its original id and the instance's path as its working directory, and its transcript holds the messages from before the trim and the new prompt.
- [ ] A directory declared in `[repos.<name>] build_trees` (an exact path with a trailing slash) is trimmed the same way when idle. A directory without `CACHEDIR.TAG` and without a declaration is never trimmed.
- [ ] A build tree with a file or directory written within `trim_after` (newest write measured across the whole tree) isn't trimmed, and stays in the unit's verdict.
- [ ] A build tree that a running process uses as its working directory, or that holds a file a process has open, isn't trimmed. The check uses the pass's single process listing from `internal/removal/procs.go` (`/proc` on Linux, `lsof -n -P -w -F 0pn` on macOS) and its build-tree attribution. If that listing fails, no tree in any unit it covers is trimmed.
- [ ] A build tree with a `.git` directory or file, or a git directory recognized by structure (`HEAD` beside `objects/` and `refs/`), anywhere inside it is never trimmed. The unit is kept, and the verdict names the repository's path.
- [ ] The gate is asked for each trim with step name `trim`. When the gate refuses, nothing in that tree is removed.
- [ ] The trim removes through `rmtree.go`'s walk from the instance's directory descriptor, with every directory opened `O_DIRECTORY|O_NOFOLLOW`, and never through `os.RemoveAll` or a path-based delete. If a `.git` entry or git directory is created inside the tree after the trim's checks, the walk stops there, keeps the rest of the tree and names the path. A directory on another device stops the walk with `another filesystem at <path>` named.
- [ ] A trim that stops partway leaves the unit in place with what's left of the tree, and the pass carries on to the next unit within its deadline.
- [ ] Unit tests cover each refusal above (lock held, process inside, recent write, repository inside, gate refusal, `trim = false`) as well as the idle case that does trim, and `go test ./...` passes.
- [ ] E2E flow still works (do not break the skeleton): reaping a deleted session's instance that holds an unpushed commit still keeps it and names the branch, and a clean one still goes through the gate and `DestroyInstance`.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:9>>, <<ISSUE:16>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 19: feat(cli): add niwa helper and read registered pid records in liveness

**Goal**: Add `niwa helper <pid> --purpose <text>`, which records a live child of its caller that isn't a session in `pids/<pid>.json` with a ledger entry, and make liveness read those records, so a registered helper no longer keeps its stopped unit while a hand-written record or a process that isn't the caller's child still does, with the record format and the command documented in the guide.

**Acceptance Criteria**:

- [ ] `niwa helper <pid> --purpose <text>`, implemented in `internal/cli/helper.go`, run from inside an instance with `<pid>` naming a live child of the calling process (its parent pid equals niwa's own parent pid) whose working directory is inside that instance, exits 0 and writes `<instance>/.niwa/pids/<pid>.json`.
- [ ] The record uses schema `niwa.pid/v1` and carries the pid, the process start time and the command as read from the operating system (not from the caller's arguments), plus the `--purpose` text; the file is mode 0600.
- [ ] The write happens under the instance lock in the ledger order: temp file, then a ledger entry with the record's path under `.niwa/`, SHA-256, size, write time and writer `helper`, then an atomic rename into place. A test that blocks the ledger write sees a nonzero exit and no `pids/<pid>.json` left behind.
- [ ] Scenario: `niwa helper` naming a live process that isn't the caller's child exits nonzero with `niwa: cannot record <pid>: it is not a child of the calling process` and writes neither a record nor a ledger entry.
- [ ] `niwa helper` also exits nonzero and writes nothing when the named process isn't alive, when its working directory is outside the instance, when it is a session process `claude agents` lists, or when it is an ancestor of niwa (one test per case).
- [ ] `niwa helper` refuses to write when `<instance>/.niwa` or `.niwa/pids` is a symlink or not a directory; both are opened `O_DIRECTORY|O_NOFOLLOW`.
- [ ] Liveness, through the per-pass process listing in `internal/removal/procs.go`, treats a live process whose pid and start time match a ledger-matched pid record as the unit's own helper: once the session is stopped it no longer keeps the unit, and while the session is working the verdict is unchanged.
- [ ] A test with a registered helper alive in a stopped unit that the rule otherwise passes sees the unit reaped and the helper never sent a signal by niwa.
- [ ] Scenario: a pid record written by hand (no matching ledger entry) for a live process with its working directory in the instance doesn't release it: the unit is kept by the liveness check.
- [ ] A record whose start time doesn't match the live process with that pid (a reused pid) doesn't release it, and the unit is kept by liveness.
- [ ] The pid-record reader in `internal/removal/records.go` opens each file `O_NOFOLLOW|O_NONBLOCK`, requires a regular file owned by the user by `fstat` before reading, caps it at 64 KiB, decodes it strictly and rejects an unknown schema name; a symlink, FIFO, oversized, malformed or wrong-schema record reads as absent, so its process keeps the unit (one test per case).
- [ ] The pid records apply at every point the pass reads the process listing `internal/removal/procs.go` takes, at liveness and at the re-read after the gate, each record matched by pid and start time to an entry in that listing.
- [ ] The tombstone re-check's fresh process listing applies the same pid records, so a registered helper that is still alive in the tombstone doesn't block a stopped unit's reap, while an unregistered process there still does.
- [ ] `docs/guides/ephemeral-session-instances.md` documents the `niwa.pid/v1` fields, the `niwa helper` command with its checks and refusal message, that a helper is never signalled and ends on its own when the instance goes, and that under the deny-write sandbox the guide names `niwa helper` can't write the ledger, so the helper isn't registered and keeps its unit.
- [ ] E2E flow still works (do not break the skeleton): the reap of a deleted session's instance holding an unpushed commit still keeps it and names the first keeping check and the branch.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:5>>, <<ISSUE:12>>, <<ISSUE:13>>

**Repo**: tsukumogami/niwa

**Group**: over-mark-listing

### Issue 20: feat(removal): hold a unit on its keep record and check in_brief files against the brief's copy

**Goal**: The rule reads `<instance>/.niwa/keep.json` for holds, review dates, build-tree protection and named files, ends a hold once the session works after the record was written, and reads the handoff brief with the newer-than-the-brief test and the `in_brief` check against the brief's `stored_at` copy, with every recorded path passed through `removal.ResolveRecorded` on every read, so a held unit keeps and is listed as held and an unverified brief file counts as unique.

**Acceptance Criteria**:

Keep record reading:
- [ ] `internal/removal/records.go` gains a `keep.json` reader for schema `niwa.keep/v1`: opened `O_NOFOLLOW|O_NONBLOCK` from the `.niwa` directory descriptor, `fstat`ed as a regular file owned by the user, capped at 1 MiB, strictly decoded into a fixed struct (`instance`, owner, reason, `written`, `written_at`, `review`, `protects`, `files` with `path`, `sha256`, `bytes`, `in_brief`, `stored_at`, and `brief.written_at`), with an unknown schema name or unknown field rejected.
- [ ] A `keep.json` that is a symlink, a FIFO, oversize, malformed or of another schema keeps the unit as a missing record the rule needs, and `niwa reap --verdict-only` names the record and the reason.
- [ ] A record with `protects` containing `instance`, or with neither `protects` nor `files`, is a hold: reap keeps the unit whatever its session state or over answer, and the verdict names the keep record with its owner and reason after `ScrubEvidence`.
- [ ] Past the record's `review` date a hold still keeps the unit and is reported as due for review; nothing about the unit changes because the date passed.
- [ ] When the unit's session has worked after the record's `written_at`, the hold no longer counts and the unit is judged by the rest of the rule; the files the record names still count.
- [ ] `protects` containing `build_tree` makes the trim skip that unit's build trees, and the verdict-only output says so.
- [ ] Every file the record names, and every file a brief names, goes through `removal.ResolveRecorded` against the instance (or, with a `job:` prefix, the unit's own job directory) on each read, not only when written; a path that fails is named in the verdict and the record keeps the unit as unreadable.
- [ ] Until <<ISSUE:21>> moves named files into the kept set, a unit whose keep record names a file that exists keeps, with that file named; no named file is ever removed in this issue.

Handoff brief:
- [ ] `internal/removal/brief.go` reads the brief from the `brief` member of `keep.json` and applies the freshness test: a file whose later of modification time and inode change time is after `brief.written_at` keeps the unit and is named as newer than the brief.
- [ ] With a brief present, job scratch and loose root files older than the brief that it doesn't name are disposable, whatever `[session.removal] job_scratch` says.
- [ ] The `in_brief` check accepts a `stored_at` only under the brief copy location, which must itself resolve outside the instance, the job directory, every path niwa removes or expires, the host's temporary directory and the Claude Code config directory. While no location is defined, no `stored_at` counts.
- [ ] The `stored_at` copy is opened component by component with `O_NOFOLLOW|O_NONBLOCK`, `fstat`ed as a regular file and hashed from that descriptor; the file counts as handed forward only when that hash equals both the record's `sha256` and the file in the instance, the latter opened component by component from the instance's or job directory's descriptor.
- [ ] Any other outcome (no `stored_at`, a link, a missing copy, a hash mismatch, a location not defined) makes the file unique: the unit keeps and the file is listed as "brief copy not verified".
- [ ] At reap, right before removal, the pass re-hashes the brief copy of every `in_brief` file the rule judged handed forward, opened the same way; a missing copy or a mismatch makes the file unique and keeps the unit.

Listing:
- [ ] `niwa list` prints a held unit as `held by keep record (owner <owner>): <reason>; review due <date>` and the JSON `unit` object carries `held` with `owner`, `reason`, `review` and `due`, every string re-scrubbed on read and read locally with no network call.

Unit tests:
- [ ] Unit tests cover each reader failure, the hold forms, the review date, a hold ended by later work with named files still counted, `build_tree` protection, the freshness test at the boundary, and each `in_brief` outcome, with the brief copy location set by the test to reach the matching path.

Functional scenarios (`@critical`, in `test/functional/features/over-remover.feature` unless noted):
- [ ] Keep record: a clean stopped unit with a hold is kept on reap and listed as held with its owner and reason; after the session works past `written_at`, the next reap no longer treats it as held.
- [ ] Newer scratch: a unit with a brief, one job scratch file older than the brief and one newer; the verdict lists the older as disposable and keeps the unit naming the newer one as newer than the brief.
- [ ] Held unit, in `test/functional/features/ephemeral-session.feature`: two dispatched units, one held by `keep.json`, both sessions deleted; reap removes the unheld instance and keeps the held one, which `niwa list` shows as held.
- [ ] Held unit not asked: with `[session.over]` configured, a stopped, unmarked unit held by `keep.json` is never asked, an over-command wrapper's log shows no call for it, and it carries no mark after the pass.
- [ ] Keep record through apply: after `niwa apply` in a held unit's instance, its `keep.json` is still there unchanged and still holds the unit through the next reap.
- [ ] A keep record naming a symlink to a file outside the instance keeps the unit with the path named, and nothing is copied or moved anywhere.
- [ ] An `in_brief` file with no `stored_at` keeps the unit as unique with "brief copy not verified".
- [ ] An `in_brief` file whose `stored_at` copy differs from the recorded hash keeps the unit as unique with "brief copy not verified".
- [ ] An `in_brief` file whose `stored_at` copy matches but resolves under the unit's job directory counts as nothing and keeps the unit as unique.
- [ ] An `in_brief` file whose `stored_at` copy matches but resolves under the instance counts as nothing and keeps the unit as unique.

Skeleton and downstream:
- [ ] E2E flow still works (do not break the skeleton): the deleted-session reap scenario still keeps an instance holding an unpushed commit and removes a clean one.
- [ ] Must deliver: the decoded keep record with each named file's resolved path, recorded hash, byte count and review date, and the rule's per-file verdict separating files bound for the kept set from `in_brief` files handed forward, exposed from `internal/removal` for the kept-set move (required by <<ISSUE:21>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:16>>, <<ISSUE:18>>

**Repo**: tsukumogami/niwa

**Group**: keep-and-restore

### Issue 21: feat(removal): move a reaped unit's named files into the kept set

**Goal**: A reap of a unit whose keep record names files moves those regular files into `~/.niwa/w-<key>/kept-files/` unchanged by hash, before the instance goes, and the kept set is listed with `niwa list --kept`, marked due on its date, removed only after a day's notice, and left in place by `DestroyWorkspace`.

**Acceptance Criteria**:

- [ ] When a unit that passes the rule has a `keep.json` naming files, the reap moves each named file into `~/.niwa/w-<key>/kept-files/<instance>/<UTC time of the move>/files/<in-unit path>`, where `<key>` is `hostdir.WorkspaceKey` of the workspace root, before any worktree removal, tombstone rename or `DestroyInstance`.
- [ ] The entry also holds the keep record with every path rewritten to its place in the entry, and an `entry.json` with schema `niwa.kept/v1` carrying the instance, its creation time, when it was moved, the due date, `first_shown_due` (empty at the move) and each file's original path, SHA-256 and size.
- [ ] Entry directories are created 0700, and `entry.json` and the rewritten keep record are 0600; each moved file keeps its permission bits with setuid, setgid and sticky cleared.
- [ ] Each source is opened component by component from the instance's directory descriptor (or the job directory's), every directory with `O_DIRECTORY|O_NOFOLLOW` and the file with `O_NOFOLLOW|O_NONBLOCK`; niwa `fstat`s the descriptor and moves only a regular file owned by the user, on the instance's device, with a link count of exactly one, and hashes and copies from that descriptor, never from the path.
- [ ] A named path that is a symlink, a device, a FIFO, a directory or a file with a second hard link keeps the unit with that path named, and nothing is moved or removed.
- [ ] Within one filesystem the move is a `renameat` relative to the opened parent; across volumes it is a copy, a hash check against the recorded SHA-256 and then a delete of the source. A failed write or a hash mismatch keeps the unit with nothing removed and the source still in place.
- [ ] The rule's `CheckNamedFiles` check, on every pass and in the verdict-only mode, keeps the unit with the file named when a named file is missing or its SHA-256, hashed from the descriptor opened as above, doesn't match the one the keep record holds; this replaces <<ISSUE:20>>'s interim keep for a named file that exists and applies to a same-filesystem `renameat` as much as to a cross-volume copy.
- [ ] Nothing goes into the kept set from the instance except the files a keep record names and the paths a discard record releases, which wait under the entry's `discarded/` until the restore window ends; a reaped unit with neither a keep record that names files nor a discard record creates no entry.
- [ ] A per-workspace lock at `~/.niwa/locks/k-<key>.lock`, created 0600 with `O_NOFOLLOW`, guards every write to and removal from the kept set.
- [ ] After the move, the tombstone re-check treats a path the move took out as gone or deleted without calling it a change, while anything found at such a path again counts as "changed during the pass" and renames the tombstone back.
- [ ] The due date of an entry's `files/` is the keep record's review date when it has one, and otherwise the move time plus the restore window read from `cleanupPeriodDays` in Claude Code's user settings (30 days when unset); the due date of its `discarded/` is always the move time plus the restore window.
- [ ] On or after `files/`'s due date, the pass marks it due and writes `first_shown_due` into `entry.json` the first time it is shown; `files/` is still on disk after a second pass the same day and is removed by the first pass at least a day after it was first shown.
- [ ] An entry's `files/` and `discarded/` expire separately: the pass marks `discarded/` due on `discarded_due` and writes `discarded_first_shown_due` the first time it is shown, removes it at the first pass at least a day after that, leaves it in place when `files/` goes earlier, and removes the entry directory only once both parts are gone. A unit test over an entry written with both parts and a `due` before `discarded_due` shows `files/` leaving first, `discarded/` staying until a day after it was first shown due, and the entry directory going last.
- [ ] `niwa list` ends with `kept set: <n> entries, <m> due (niwa list --kept)`, with `<m>` the entries this issue marks due, an entry counting as due while either of its parts is.
- [ ] The dispatch line keeps <<ISSUE:16>>'s one-line template, `niwa: <N> stopped unit[s] not retired, <size>; <D> kept-set entr(y|ies) due; <U> unit[s] not reached (niwa list)`, with its `<D> kept-set entr(y|ies) due` count read from the entries this issue marks due: with one due entry it reads `1 kept-set entry due`, and with two it reads `2 kept-set entries due`.
- [ ] An entry to remove is found by enumerating `kept-files/<instance>/` and `Lstat`ing each direct child, which must be a real directory; a symlink there is skipped and left alone, and no path read from `entry.json` is ever passed to a removal.
- [ ] `niwa list --kept` lists every entry by instance, moved time and due state, with each file's path and size and never its contents; `niwa list --kept --json` prints the same as JSON; every path and string printed passes `removal.ScrubEvidence`.
- [ ] `DestroyWorkspace` leaves `~/.niwa/w-<key>/kept-files/` in place and prints its path.
- [ ] Unit tests cover the component-by-component open, each refused file type, the permission and setuid handling, the cross-volume hash check, the due-date choice and the day's notice.
- [ ] Functional scenario: a reap of a clean unit whose `keep.json` names an untracked file in its main clone removes the instance, and the kept-set entry holds that file with the SHA-256 recorded at setup and an `entry.json` naming it.
- [ ] Functional scenario: with a separate volume mounted at the kept set's directory (a disk image on macOS, a tmpfs on Linux), the reap removes the instance and the file lands with its recorded SHA-256; with that volume remounted read-only before the reap, the reap reports the unit kept, the file is still in the instance with its recorded SHA-256, and the kept set gains no file.
- [ ] Functional scenario: a clean unit whose `keep.json` names an untracked file, with that file changed after the record was written, so its hash no longer matches, the reap keeps the unit, the reason names that file under the named-files check, and the kept set gains no entry; with the named file deleted instead, the unit is kept the same way.
- [ ] Functional scenario: a source directory swapped for a symlink after the rule's check and before the move makes the open fail, the move refuses, the unit is kept with the path named, and nothing outside the instance is copied or removed.
- [ ] Functional scenario, the kept-set half of the verdict-only mode's write-nothing check: `niwa reap --verdict-only` over a clean unit whose `keep.json` names an untracked file leaves that file in the instance with its recorded SHA-256, and `~/.niwa/w-<key>/kept-files/` gains no entry.
- [ ] Must deliver: a kept-set entry writer that can place released paths under the entry's `discarded/` at their in-unit paths with their own due date in `entry.json` (`discarded_due`), using the same open, `fstat`, hash and permission rules (required by <<ISSUE:22>>).
- [ ] Must deliver: the move returns the entry directory it created, so the pass can record it as `kept_entry` (required by <<ISSUE:23>>), and the restore-window value is read in one place that `discarded_due` is computed through and a later `restore_window` setting can override (required by <<ISSUE:22>> and <<ISSUE:23>>).
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept and printed with its first keeping check, and a clean one is still removed.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:20>>

**Repo**: tsukumogami/niwa

**Group**: keep-and-restore

### Issue 22: feat(cli): add niwa discard with a terminal-confirmed release kept until the window ends

**Goal**: Refine the skeleton's reap so a person can release paths the rule would otherwise keep: `niwa discard` runs from a shell outside the instances, is confirmed by a code typed back at the terminal, and writes a ledger-vouched `discard.json` whose paths the reap moves to the kept-set entry's `discarded/` until the restore window ends, while a detached run, a hand-written record or a path through a symlink or `..` releases nothing.

**Acceptance Criteria**:

- [ ] `niwa discard <instance> <path>... [--patch <file>]` exists in `internal/cli/discard.go`; run from inside an instance (as `insideInstance` decides), it exits non-zero with `niwa: cannot discard from inside an instance; run it from a shell outside the workspace's instances` and writes no `discard.json` and no ledger entry.
- [ ] Scenario: a detached, session-less `niwa discard` with no controlling terminal exits non-zero with `niwa: cannot discard for <name>: no controlling terminal`, and the instance has no `discard.json` and its ledger no `discard` entry.
- [ ] With a terminal, the command prints to `/dev/tty` the instance and every path with its size, the window end, and a fresh random code from `confirmOnTerminal`; only that code typed back writes the record, any other answer (the instance name included) exits non-zero and writes nothing, and no flag skips the prompt.
- [ ] Each path is checked with `removal.ResolveRecorded` against the instance, or the unit's job directory for a `job:` prefix, before it is written; a `--patch` file must resolve inside the instance the same way and be a regular file, and its SHA-256 is stored in the record.
- [ ] A confirmed discard writes `discard.json` (schema `niwa.discard/v1`, the earlier remover's fields, each path with the person's name and time) 0600 under the instance lock, recording its ledger entry with writer `discard` before the rename into place.
- [ ] The rule treats a discard path as released when it matches a unique path exactly or as a directory prefix, and only when the record's hash matches its ledger entry; a record that fails the reader's checks (size cap, strict decode, schema name, ledger match) is read as absent.
- [ ] Scenario: a `discard.json` written by hand with no ledger entry releases nothing; the reap keeps the unit with the same unique paths named as without the file.
- [ ] Scenario: a `discard.json` naming a path through a symlink, and one naming a path with `..`, each keep the unit with that path named and the record read as unreadable.
- [ ] Scenario: after a confirmed discard of an untracked file, a reap of the otherwise clean unit removes the instance, and the discarded path (and the patch, when given) is found under the kept-set entry's `discarded/<in-unit path>`, moved with the same regular-file, one-link, component-by-component open the named-file move uses.
- [ ] The kept-set entry's `entry.json` records `discarded_due` as the end of the restore window, computed by the single restore-window function <<ISSUE:21>> provides (Claude Code's transcript retention read at reap time, until <<ISSUE:23>> adds `restore_window`), and `niwa list --kept` shows the discarded paths with their sizes, that date and their own due state, apart from the entry's `files/`.
- [ ] Scenario: a unit with a keep record whose review date is one day ahead, naming an untracked file, plus a confirmed discard of another untracked file, is reaped. After the entry's `files/` leaves, `discarded/` is still on disk until `discarded_due`, is then shown due by `niwa list --kept`, and goes at the first pass at least a day later, after which the entry directory is gone.
- [ ] E2E flow still works (do not break the skeleton): the reap of a deleted session's instance that holds an unpushed commit still keeps it and names the path.

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:14>>, <<ISSUE:21>>

**Repo**: tsukumogami/niwa

**Group**: keep-and-restore

### Issue 23: feat(hook): write launch records for hook sessions and stamp a reap's restore window

**Goal**: `niwa instance from-hook` writes a launch record with `origin` set to `hook` and the mapping's `launch_id`, a reap on a mark records `reaped_at`, `restore_until` and `kept_entry`, `restore_window` with its seven-day floor sets the window, launch records decode strictly with the replay fields ignored for hook records, and the guide gains `.niwa-launch/` and the restore-window note.

**Acceptance Criteria**:

- [ ] `niwa instance from-hook` writes `<root>/.niwa-launch/<launch id>.json` with schema `niwa.launch/v1`, a launch id that passes `authority.ValidDispatchID`, the agent, the session, the instance name, the time and `origin` set to `hook`, and with no permission mode, settings or environment fields.
- [ ] The session mapping `from-hook` writes carries a `launch_id` field equal to the record's launch id.
- [ ] `niwa resume <instance>` for a hook-provisioned unit finds its record through `resolveLaunchRecord` by instance name and session, with no change to that lookup.
- [ ] A hook record (`origin` = `hook`) edited by hand to add a permission mode, settings or environment is resumed with none of them applied: the `claude --resume` invocation carries no permission-mode flag, no settings and none of the added environment.
- [ ] A launch record with an unknown field is refused by `niwa resume` with a non-zero exit and an error naming the decode failure, and no instance is created and no session started; this holds for dispatched and hook records alike.
- [ ] A reap on a mark writes `reaped_at`, `restore_until` (equal to `reaped_at` plus the window) and `kept_entry` (the unit's kept-set entry path under `~/.niwa/w-<key>/kept-files/<instance>/`) into the unit's launch record, after the mapping is deleted.
- [ ] With `restore_window = "14d"` set, a reaped unit's `restore_until` is 14 days after its `reaped_at`.
- [ ] With `restore_window` unset and `cleanupPeriodDays = 20` in the resolved config directory's `settings.json` (`CLAUDE_CONFIG_DIR` when set), `restore_until` is 20 days after `reaped_at`; with neither set, it is 30 days after.
- [ ] The same window sets the kept set's due dates: with `restore_window = "14d"` set, a kept-set entry whose keep record names no review date is due 14 days after its move, and a confirmed discard's `discarded_due` is 14 days after the reap; with it unset, both still follow `cleanupPeriodDays` as before.
- [ ] `validateSessionDeclaration` rejects a `restore_window` under seven days with an error naming `restore_window` and the seven-day minimum.
- [ ] Functional scenario: with `restore_window = "3d"` in the workspace configuration, the next `niwa apply` exits non-zero with an error naming the restore-window setting and the seven-day minimum, no pass runs, and no instance or kept-set entry changes.
- [ ] A clean, stopped hook-provisioned unit carrying a valid mark is now reaped by the pass rather than kept with "no launch record", and its launch record carries the three window fields afterwards.
- [ ] The test setup for "a marked unit with no launch record is kept with that reason" (added in <<ISSUE:13>> with a hook-provisioned unit) moves to a unit whose launch record was removed by hand; that scenario still reports the unit kept with "no launch record" from a retire and from the next dispatch.
- [ ] `docs/prds/PRD-over-remover.md` is amended so its check that a hook-provisioned unit has no launch record uses a unit whose launch record was removed by hand.
- [ ] `docs/guides/ephemeral-session-instances.md`'s deny-write advice names the root's `.niwa-launch/` under the same sandbox write restrictions as the rest of the list, and notes that `cleanupPeriodDays` in Claude Code's user settings, already on the list, now sets the default restore window.
- [ ] Security scenario for the guide: its deny-write list names the whole `~/.ssh/` directory and, as an example, a file outside it that an `Include` in `~/.ssh/config` names (`Include ~/dotfiles/ssh/hosts`).
- [ ] E2E flow still works (do not break the skeleton): a deleted session's instance holding an unpushed commit is still kept by `niwa reap` with the path named.
- [ ] Must deliver: launch records with `origin`, `reaped_at`, `restore_until` and `kept_entry` written at reap, a strict decoder `niwa resume` uses for every record, the window computation (setting, then `cleanupPeriodDays`, then 30 days) and the seven-day floor, so the restore can check the window and trust only `kept_entry` values it re-validates (required by <<ISSUE:24>>).

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:5>>, <<ISSUE:13>>, <<ISSUE:21>>, <<ISSUE:22>>

**Repo**: tsukumogami/niwa

**Group**: keep-and-restore

### Issue 24: feat(resume): restore kept files inside the restore window and refuse past it

**Goal**: Refine the skeleton's resume path so `niwa resume` refuses a unit past its restore window with the date the window ended and, inside the window, restores the unit's kept files to the fresh instance before the session starts, trusting no path it reads.

**Acceptance Criteria**:

- [ ] `niwa resume <target>` on a launch record whose `restore_until` has passed exits non-zero with `niwa: cannot resume <name>: its restore window ended on <date>`, provisions no instance and starts no session.
- [ ] `niwa resume` checks that `kept_entry` names exactly `~/.niwa/w-<key>/kept-files/<instance>/<UTC>/` for the record's own instance, a direct descendant reached with no symlink, and refuses otherwise.
- [ ] Inside the window, `niwa resume` provisions the instance by name, moves the entry's files back to their original paths with a hash check before the session starts, and the restored instance carries no mark.
- [ ] Before `niwa resume`'s rollback removes the fresh instance after a later step fails, it moves every restored file back into the kept-set entry unchanged by hash, so a failed resume leaves the kept set as it was; a file that can't be moved back that way leaves the instance in place with the file named.
- [ ] Failure-injection test: a resume made to fail after the kept files are restored and before the session starts leaves every kept file in the kept-set entry with its SHA-256 unchanged, and the launch record's `restore_until` and `kept_entry` unchanged.
- [ ] The restore records its time as `restored_at` in the unit's `unit.json`, and the pass doesn't ask the restored unit's over command until its session has worked after that time and stopped again: an over-command wrapper's log shows no call for it at a dispatch before then, and one call at the first dispatch after it has worked and stopped. `docs/guides/over-command.md`'s account of which units niwa asks about gains this exception.
- [ ] The `unit.json` write that records `restored_at` carries its ledger entry, recorded before the rename into place like every other record niwa writes.
- [ ] A kept file whose hash doesn't match refuses the restore with that file named, leaving every kept file in place in the entry.
- [ ] Every original path in `entry.json` and the rewritten keep record passes `removal.ResolveRecorded` against the fresh instance, and every source path must resolve inside the entry's own `files/` directory, or the restore refuses with the file named.
- [ ] Missing parent directories are created one component at a time, each opened with `O_DIRECTORY|O_NOFOLLOW`, and each file is created with `O_CREAT|O_EXCL|O_NOFOLLOW`, never over an existing file; a failure refuses the restore with the file named.
- [ ] Discarded paths under the entry's `discarded/` are not put back.
- [ ] When the entry's `files/` already left because its review date came before the window's end, the restore goes ahead without those files and the output names the entry whose files were gone.
- [ ] Scenario: a unit reaped on a mark with `restore_window` set and resumed inside the window gets its kept files back at their original paths with matching hashes.
- [ ] Scenario: a unit reaped on a mark with no `restore_window` set (window from Claude Code's transcript retention) and resumed inside the window gets its kept files back.
- [ ] Scenario: the same two units resumed past `restore_until` each refuse with the date the window ended, with and without the setting.
- [ ] Scenario: an `entry.json` edited to name a `../` path refuses the restore and writes nothing outside the instance.
- [ ] Scenario: an `entry.json` edited to name an absolute path refuses the restore and writes nothing outside the instance.
- [ ] Scenario: a launch record whose `kept_entry` names another directory refuses the restore and writes nothing outside the instance.
- [ ] Scenario: a fresh instance with a symlink where a kept file goes refuses the restore and writes nothing outside the instance or through the link.
- [ ] Scenario: an `entry.json` naming `.git/hooks/pre-commit` refuses the restore with that file named and nothing written.
- [ ] Scenario: an `entry.json` naming `.CLAUDE/settings.local.json` refuses the restore with that file named and nothing written (the `.claude` check is case-insensitive).
- [ ] Scenario: an `entry.json` naming a path outside the instance refuses the restore with that file named and nothing written.
- [ ] `go test ./...` and the functional suite pass.
- [ ] E2E flow still works (do not break the skeleton)

**Dependencies**: Blocked by <<ISSUE:1>>, <<ISSUE:15>>, <<ISSUE:23>>

**Repo**: tsukumogami/niwa

**Group**: keep-and-restore

## Dependency Graph

```mermaid
graph TD
    subgraph RW["rule-and-worktrees"]
        I1["1: Reap keeps unpushed work"]
        I2["2: Path classes and check order"]
        I3["3: reap --verdict-only, scrubbing"]
        I4["4: Proof remotes decide held commits"]
        I5["5: Deny-write list in session guide"]
        I6["6: Walk finds every repository"]
        I7["7: Refuse program-naming config"]
        I8["8: Tombstone re-check and backstop"]
        I9["9: Remove worktrees before session"]
        I10["10: Vet job directory before rm"]
        I11["11: Other removals through rule"]
    end

    subgraph OML["over-mark-listing"]
        I12["12: Ledger and over seam mark"]
        I13["13: Reap a marked unit"]
        I14["14: niwa retire"]
        I15["15: Over-command guide"]
        I16["16: List answer, age and size"]
        I17["17: Over answers in verdict-only"]
        I18["18: Trim idle build trees"]
        I19["19: niwa helper and pid records"]
    end

    subgraph KR["keep-and-restore"]
        I20["20: Keep record and in_brief check"]
        I21["21: Move named files to kept set"]
        I22["22: niwa discard"]
        I23["23: Hook launch records, window"]
        I24["24: Restore inside the window"]
    end

    I1 --> I2
    I2 --> I3
    I2 --> I4
    I3 --> I4
    I6 --> I4
    I4 --> I5
    I8 --> I5
    I2 --> I6
    I6 --> I7
    I8 --> I7
    I2 --> I8
    I3 --> I8
    I6 --> I8
    I7 --> I9
    I8 --> I9
    I10 --> I9
    I6 --> I10
    I8 --> I10
    I3 --> I11
    I7 --> I11
    I9 --> I11
    I10 --> I11
    I8 --> I12
    I9 --> I13
    I10 --> I13
    I11 --> I13
    I12 --> I13
    I13 --> I14
    I16 --> I14
    I5 --> I15
    I12 --> I15
    I16 --> I15
    I13 --> I16
    I3 --> I17
    I12 --> I17
    I16 --> I17
    I18 --> I17
    I9 --> I18
    I16 --> I18
    I5 --> I19
    I12 --> I19
    I13 --> I19
    I16 --> I20
    I18 --> I20
    I20 --> I21
    I14 --> I22
    I21 --> I22
    I5 --> I23
    I13 --> I23
    I21 --> I23
    I22 --> I23
    I15 --> I24
    I23 --> I24

    classDef done fill:#c8e6c9
    classDef ready fill:#bbdefb
    classDef blocked fill:#fff9c4
    classDef needsDesign fill:#e1bee7
    classDef needsPrd fill:#b3e5fc
    classDef needsSpike fill:#ffcdd2
    classDef needsDecision fill:#d1c4e9
    classDef tracksDesign fill:#FFE0B2,stroke:#F57C00,color:#000
    classDef tracksPlan fill:#FFE0B2,stroke:#F57C00,color:#000

    class I1 ready
    class I2,I3,I4,I5,I6,I7,I8,I9,I10,I11,I12,I13,I14,I15,I16,I17,I18,I19,I20,I21,I22,I23,I24 blocked
```

**Legend**: Green = done, Blue = ready, Yellow = blocked, Purple = needs-design, Orange = tracks-design/tracks-plan

The graph draws direct edges only. Most outlines also list Issue 1, the skeleton, among their blockers; every issue already reaches it through Issue 2, so that edge isn't drawn again.

## Implementation Sequence

**Merge order:** `rule-and-worktrees` (Issues 1-11), then `over-mark-listing` (Issues 12-19), then `keep-and-restore` (Issues 20-24). Each group lands as one pull request, opened only after the group before it merges.

**Critical path:** Issue 1 -> Issue 2 -> Issue 3 -> Issue 8 -> Issue 7 -> Issue 9 -> Issue 11 -> Issue 13 -> Issue 16 -> Issue 18 -> Issue 20 -> Issue 21 -> Issue 22 -> Issue 23 -> Issue 24 (15 of 24 issues). The chain is long because the design's six phases build one removal rule in order. Other chains of the same length run through Issue 6 in place of Issue 3 (1 -> 2 -> 6 -> 8 -> ...) and through Issue 10 in place of Issue 7 (... -> 8 -> 10 -> 9 -> ...).

**Parallelization:** Issue 1 is the only one that can start right away. After that, issues at the same depth have no edge between them and can be worked side by side, inside their group's pull request:

- `rule-and-worktrees`: Issues 3 and 6 after Issue 2; Issues 4 and 8 after those; then Issues 5, 7 and 10 together once Issue 8 is in (Issue 5 also waits on Issue 4). Issue 9 follows Issues 7 and 10, and Issue 11 closes the group.
- `over-mark-listing`: Issue 12 can start as soon as Issue 8 is done, alongside the rest of the first group's work, but Issue 13 waits on Issues 9 through 12. After Issue 13, Issues 16 and 19 run together; after Issue 16, Issues 14, 15 and 18 run together; Issue 17 closes the group once Issue 18 is in.
- `keep-and-restore`: Issues 20, 21, 22, 23 and 24 form a single chain, with no parallel work inside the group.

**Recommended order:** follow the critical path, and pick up the off-path issues (4, 5, 6, 10, 12, 14, 15, 17, 19) as soon as their blockers are in, so none of them holds up the group's pull request.
