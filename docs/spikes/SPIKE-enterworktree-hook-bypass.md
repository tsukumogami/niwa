---
status: Complete
question: |
  Why does an agent isolating inside a nested-git niwa workspace end up with a
  bare worktree niwa cannot see, instead of the niwa-managed worktree the
  installed WorktreeCreate hook is supposed to produce -- and which direction
  should close the gap?
timebox: "1 session: live reproduction across three harness versions plus a source trace"
---

# SPIKE: agent worktree creation does not produce a niwa worktree

## Status

Complete

**This spike replaces an earlier version that reached the wrong conclusion.**
The earlier version reported that the harness had changed to decide
native-vs-hook by an upward git-repo discovery walk, taking a native path
whenever an enclosing repo was found and never consulting the hook. Direct
reproduction on the exact version that report tested does not support that
finding. This version records what reproduces and what does not, and revises the
recommendation accordingly. The correctness issue it feeds is
`tsukumogami/niwa#221`.

## Question

`niwa apply` installs a Claude Code `WorktreeCreate` / `WorktreeRemove` hook so
that an agent's native "work in a worktree" action produces a niwa-managed
worktree — with secrets, workspace context, and a session record — rather than a
bare in-repo checkout. In practice an agent isolating inside a nested repo ended
up with a plain worktree under `<repo>/.claude/worktrees/<name>` that
`niwa worktree list` had no record of. Why, and what closes the gap?

## Approach

1. Build minimal fixtures that isolate one variable at a time — hook present or
   absent, hook succeeding or failing, cwd inside a git repo or at a non-git
   root — and drive `EnterWorktree` against each from a headless harness run,
   recording the verbatim tool result, the resulting `git worktree list`, and the
   hook's stdin.
2. Repeat the decisive fixtures against the older harness version the original
   report tested, installed side by side, so a behavior change between versions
   would be visible rather than assumed. A third version between the two was
   added later to close the gap the original report's version range left open.
3. Trace the niwa side end to end against a real workspace, with the installed
   binary and again with a binary built from `main`.

## Findings

### 1. The hook fires from inside a git repo, on every version tested

In a git repo carrying a `WorktreeCreate` hook in `.claude/settings.local.json`,
the hook fired and no native worktree was created. Hook stdin carried the
documented payload (`session_id`, `transcript_path`, `cwd`, `hook_event_name`,
`name`) and the path the hook printed to stdout became the session's working
directory.

This held on the version the earlier report tested, on a version between, and on
the current one. There is no upward-discovery behavior that preempts the hook,
and no harness regression to detect.

The design's version floor and its apply-time probe therefore stand as shipped.
The structural limitation the earlier version raised is real and unrefuted — the
probe expresses a floor and cannot express a ceiling — but the regression it was
written to catch did not happen, so nothing here justifies acting on it.

### 2. A hook that RUNS and fails does so loudly; a hook that never runs is silent

Two different failure classes, and the distinction is load-bearing.

**Runtime failure is loud.** A `WorktreeCreate` hook that exits non-zero, exits
zero while printing nothing, prints a path that does not exist, prints extra
lines after the path, is not executable, names a binary that does not exist, or
exceeds its timeout — every one of these makes `EnterWorktree` fail with the
hook's stderr surfaced in the tool result, and creates nothing under
`.claude/worktrees/`. This includes the stale/missing-binary case, which matters
because it means a stale hook command cannot explain an observed bare worktree.

**Structural failure is silent.** A settings document that does not parse, or a
hook entry missing its `"type"` field, is dropped without a warning and the
native path runs, producing a bare worktree. Reproduced on all three versions
tested. Nothing in the tool result says a settings file was rejected.

That second class is a genuine silent-degradation channel against PRD R7 and R8,
and it is not covered by anything niwa does today. niwa marshals its own settings
so it will not emit invalid JSON itself, but it is not the only writer of
`.claude/settings.local.json` — the harness writes permission grants to the same
file, so a torn concurrent write, a hand edit, or a third-party tool can produce
it. Corruption does not spread between scopes: a corrupt sibling
`.claude/settings.json` alongside a valid `settings.local.json` carrying the hook
still delegates. Only the file actually carrying the hook matters.

### 3. The reported symptom needs a session with no EFFECTIVE hook

A repo with no `WorktreeCreate` hook reproduces the reported artifact exactly: a
worktree at `<repo>/.claude/worktrees/<name>` on branch `worktree-<name>`, marked
`locked`, with no niwa session record.

The structural-failure cases in Finding 2 produce a byte-identical artifact, so
"no hook configured" and "hook configured but silently dropped" are
indistinguishable from the outside. Every observed route to the reported symptom
runs through a session where no hook was in effect — never through one where a
hook ran and lost to an enclosing repo.

Note the path asymmetry that makes these distinguishable after the fact: niwa
worktrees live under `.niwa/worktrees/*`, native ones under `.claude/worktrees/*`.
That is what would make post-hoc detection of a silently-dropped hook buildable,
should the structural-failure channel above be worth closing.

### 4. What actually broke: the hook command is pinned to a version-specific path

`niwa apply` writes the hook command from `os.Executable()`
(`internal/workspace/apply.go`). Under a versioned install layout — each release
in its own directory, with a stable shim on `PATH` pointing at the current one —
that resolves to a version-pinned path. Every repo in every workspace instance on
the machine under test carried a hook naming a release several versions behind
the current one.

That pinned release predated the fix for promoted `[claude.env]` key inheritance
in worktrees (#207), so `niwa worktree from-hook` died with `claude.env: promoted
key "GH_TOKEN" not found in resolved env vars`, and agent worktree creation was
broken workspace-wide. Repointing the same hook at a binary built from `main`
made the flow succeed end to end: a niwa worktree under `.niwa/worktrees/`, a
session record, visible to `niwa worktree list`.

Two problems sit here, and only one was already fixed. The promoted-key bug was
fixed upstream. The pinning was not: upgrading niwa strands every previously
applied workspace on the old binary until each is re-applied, and nothing detects
or reports it. The pinning is not recoverable from inside the process — on Linux
`os.Executable()` reads `/proc/self/exe`, which the kernel has already resolved
past every symlink.

The workspace-root `SessionStart` hook (`niwa instance from-hook`) is written the
same way and has the same defect. It is the more consequential of the two,
because it provisions instances in-process: a stale binary running that apply
stamps its own stale path into every repo of each newly created instance.

### 5. A failed create leaves state behind

`from-hook` creates the git worktree and writes the session record before
installing content. Session creation is atomic; content install is not. When
content install failed, the tool call failed loudly but the git worktree and an
`active` session record both survived, so `niwa worktree list` reported an
`active` worktree no process was in. Cleanup took a manual force-destroy plus a
prune. This reproduced on both failing runs.

### 6. Worktree settings do not carry the delegation decision

`ApplyToWorktree` runs the repo materializers against a worktree but never passes
the worktree-delegation decision, and its options struct has no field for one. A
niwa worktree's own `settings.local.json` therefore records neither the hook
entries nor the deny entries, while its clone records one of them.

This is latent rather than user-visible, and the mechanism is worth stating
because it is not obvious: for a session whose cwd is inside a linked git
worktree, the harness resolves settings from the **main checkout** and reports
the main checkout as the hook payload's `cwd`. So the clone's hook is in scope
from inside a worktree, `from-hook`'s cwd-to-repo resolver gets a path it can
resolve, and nested worktree creation delegates correctly. Verified end to end
against an instance mirroring the real layout: creating a worktree from inside a
worktree produced a second niwa-managed worktree with its own session record, not
a native one.

The same normalization is why closing this gap does not break nested creation —
a concern worth checking, since the resolver rejects a `.niwa/worktrees/...` path
when called directly. It is never called with one.

### 7. Validation questions the earlier version left open

- **Does the per-repo `permissions.deny` actually block `EnterWorktree` from
  inside the repo?** Yes, and more completely than expected: the tool is absent
  from the session altogether, including under a `bypassPermissions` default
  mode. The deny+steer fallback works as designed.
- **Does a `WorktreeCreate` hook at a non-git workspace root fire from a non-git
  cwd?** Yes. Hook stdin's `cwd` is that non-git root — which means
  `from-hook`'s cwd-to-repo resolver has no repo to resolve, so a root-scope
  install is not usable without a different resolution strategy.
- **Which version changed the behavior?** None. The premise was wrong. The
  fixtures were run across a contiguous span covering the version the original
  report recorded, one between, and the current one, with no behavioral
  difference at any point.

## Recommendation

Treat this as two ordinary defects in the shipped integration rather than a
harness regression, and do not build harness-detection machinery for a regression
that did not happen.

1. **Make the emitted hook command survive a niwa upgrade.** Resolve `niwa` from
   `PATH` with the recorded absolute path as a fallback, applied to both hook
   consumers through one shared helper.
2. **Reconcile a failed delegated create** through the same guarded teardown the
   remove path already uses, so a failure leaves no phantom `active` worktree.
3. **Close the worktree settings gap** so a worktree's delegation configuration
   matches its clone's.

Not addressed here, and worth its own issue: the structural-failure channel in
Finding 2. A settings document that fails to parse silently degrades to a native
worktree, which is an R7/R8 violation with no coverage today. The path asymmetry
in Finding 3 is what a post-hoc detector would key on.

The delegation model itself needs no revision. Transparent delegation is not
merely still achievable — it works today, once the hook invokes a current niwa.

These are carried by `docs/designs/current/DESIGN-niwa-default-worktree.md`
Decisions 7, 8, and 9, with a re-validation note under Decision 4.
