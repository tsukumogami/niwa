---
status: Complete
question: |
  Why does an agent isolating inside a nested-git niwa workspace end up with a
  bare worktree niwa cannot see, instead of the niwa-managed worktree the
  installed WorktreeCreate hook is supposed to produce -- and which direction
  should close the gap?
timebox: "1 session: live reproduction across two harness versions plus a source trace"
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
   would be visible rather than assumed.
3. Trace the niwa side end to end against a real workspace, with the installed
   binary and again with a binary built from `main`.

## Findings

### 1. The hook fires from inside a git repo, on both harness versions

In a git repo carrying a `WorktreeCreate` hook in `.claude/settings.local.json`,
the hook fired and no native worktree was created. Hook stdin carried the
documented payload (`session_id`, `transcript_path`, `cwd`, `hook_event_name`,
`name`) and the path the hook printed to stdout became the session's working
directory.

This held on the version the earlier report tested and on a later one. There is
no upward-discovery behavior that preempts the hook, and no harness regression to
detect. The design's version floor and its apply-time probe stand as shipped.

### 2. A failing hook fails loudly; it does not fall back to a native worktree

A `WorktreeCreate` hook that exits non-zero makes `EnterWorktree` fail with the
hook's stderr surfaced in the tool result. No worktree is created by either path.
This also held on both versions. So a broken hook is a visible failure, not the
silent degradation the feature exists to prevent — which matters, because it
means the observed bare worktree cannot be explained by hook failure.

### 3. The reported symptom needs a session with no hook in scope

A repo with no `WorktreeCreate` hook reproduces the reported artifact exactly: a
worktree at `<repo>/.claude/worktrees/<name>` on branch `worktree-<name>`, marked
`locked`, with no niwa session record. That is the only configuration observed to
produce it.

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
entries nor the deny entries, while its clone records one of them. Latent today —
delegation still resolves for a session working inside a worktree — but the two
configurations drift.

### 7. Validation questions the earlier version left open

- **Does the per-repo `permissions.deny` actually block `EnterWorktree` from
  inside the repo?** Yes, and more completely than expected: the tool is absent
  from the session altogether, including under a `bypassPermissions` default
  mode. The deny+steer fallback works as designed.
- **Does a `WorktreeCreate` hook at a non-git workspace root fire from a non-git
  cwd?** Yes. Hook stdin's `cwd` is that non-git root — which means
  `from-hook`'s cwd-to-repo resolver has no repo to resolve, so a root-scope
  install is not usable without a different resolution strategy.
- **Which version changed the behavior?** None. The premise was wrong.

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

The delegation model itself needs no revision. Transparent delegation is not
merely still achievable — it works today, once the hook invokes a current niwa.

These are carried by `docs/designs/current/DESIGN-niwa-default-worktree.md`
Decisions 7, 8, and 9, with a re-validation note under Decision 4.
