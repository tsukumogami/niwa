---
status: Complete
question: |
  Can niwa install a Claude Code WorktreeCreate/WorktreeRemove hook that Claude
  Code actually fires for an agent operating inside a workspace repo, and at what
  settings scope (workspace-root .claude/settings.json, per-repo .claude/settings.json,
  or user/managed) must the hook live for that to happen?
timebox: "1 session (~half a day)"
---

# SPIKE: niwa as the default worktree mechanism (Claude Code hook delegation)

## Status

Complete

## Question

Can niwa install a Claude Code `WorktreeCreate` / `WorktreeRemove` hook that Claude
Code actually invokes when an agent creates a worktree while operating inside a
workspace repo, and at what settings scope must that hook be installed for Claude
Code to honor it?

The answer is go/no-go for routing the native `EnterWorktree` tool to `niwa worktree
create`, and it determines where niwa must write the hook.

## Context

In a niwa-managed workspace, two worktree mechanisms can coexist and compete:

- **Claude Code's built-in worktree tool** (`EnterWorktree` / `--worktree`) creates
  a bare `git worktree` under `.claude/worktrees/<name>/` at the repo root, with no
  secret materialization, no CLAUDE content sync, and no session tracking.
- **niwa's worktree lifecycle** (`niwa worktree create`) places a worktree at
  `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/` and materializes the full workspace
  context: vault-resolved secrets, repo CLAUDE content, workspace-context imports,
  git-exclude coverage, worktree hooks, and a session-lifecycle state file.

The goal is one mechanism, not two. The ideal is that the native tool transparently
produces niwa worktrees.

Claude Code's hook system makes this possible in principle. The hooks reference and
worktrees guide (code.claude.com/docs/en/hooks, code.claude.com/docs/en/worktrees)
state that a `WorktreeCreate` hook fires "when a worktree is being created via
`--worktree` or `isolation: \"worktree\"`" and "replaces the default git worktree
logic entirely" — and that this applies to git repositories, not only non-git VCS.
The hook receives stdin JSON (`session_id`, `cwd`, `hook_event_name`,
`worktree_path`, `worktree_name`), must print the worktree path to stdout, and any
non-zero exit fails creation. `WorktreeRemove` fires on session exit / subagent
finish and cannot block.

niwa already owns the install surface: `InstallWorkspaceRootSettings()`
(`internal/workspace/workspace_context.go`) writes the workspace-root
`.claude/settings.json` and installs hook scripts on every `niwa apply`, and
instance-root settings are fully generated (no user-merge conflict).

The one unverified link is **settings scope**: Claude Code creates worktrees "at
your repository root" and reads project settings from the repo's `.claude/`. It is
not yet confirmed that a hook written to the workspace-root `.claude/settings.json`
(a parent of the repo) is in the cascade an in-repo agent honors. The answer decides
the design's install strategy.

This spike unblocks a follow-on **delegation-first design doc** that will decide the
design-level questions out of scope here: the stdin -> `niwa worktree create`
adapter and repo resolution from `cwd`/`git_dir`; branch-name reconciliation
(Claude's `worktree-<name>` vs niwa's `session/<sid>`); reconciling the non-blocking
`WorktreeRemove` with niwa's dirty/attached destroy guards; and the
`permissions.deny: ["EnterWorktree","ExitWorktree"]` + CLAUDE.md guidance fallback
for hosts/versions that don't honor the hook.

## Approach

Investigation ran three headless experiments against Claude Code CLI v2.1.183,
each launching `claude -p "<trivial prompt>" --worktree <name>
--permission-mode bypassPermissions` in a throwaway git repo with a logging
`WorktreeCreate` hook (capture stdin, create a worktree at a custom path, echo
that path) configured at a different settings scope:

- **Experiment A — per-repo `.claude/settings.json`.** Standalone git repo, hook in
  its own `settings.json`. Question: does the hook fire in a git repo at all?
- **Experiment B — workspace-root `.claude/settings.json`.** Hook only in a non-git
  parent directory's `.claude/settings.json` (modelling niwa's current install
  location); agent launched in a subdir git repo with no repo-level settings.
  Question: does niwa's existing workspace-root install reach an in-repo agent?
- **Experiment C — per-repo `.claude/settings.local.json`.** Hook only in the repo's
  `settings.local.json` (niwa's actual per-repo materialization target, since repos
  are git dirs and `.local` is gitignored). Question: does the hook fire from the
  file niwa would actually write?

`WorktreeRemove` non-blocking behavior was taken from the docs rather than re-tested
empirically: headless `-p` runs do not auto-clean worktrees (per the worktrees
guide), so a removal would not fire in this harness. This is the one finding below
not validated by hands-on test.

Out of scope for this spike (deferred to the design): the production stdin->niwa
adapter, branch-name policy, full reconciliation of niwa's destroy guards, and the
fallback wiring.

## Findings

**1. The `WorktreeCreate` hook fires in a git repo and fully replaces default git
worktree creation. (Experiment A — confirmed.)** The hook ran, and no
`.claude/worktrees/` was created; the worktree landed at the custom path the hook
echoed, on the branch the hook chose. `git worktree list` showed
`/tmp/.../custom-worktrees/x-spikeA  [spike-spikeA]`. This directly disproves the
issue's "hooks fire for non-git VCS only" premise.

**2. Real `WorktreeCreate` stdin contract (for `--worktree`).** The hook received:
```json
{"session_id":"...","transcript_path":"...","cwd":"/tmp/spike-wt/exp-a/repo",
 "hook_event_name":"WorktreeCreate","name":"spikeA"}
```
Notable vs. the issue's assumed schema: the worktree name field is `name` (not
`worktree_name`), and there is no `git_dir` or `worktree_path` for create (the path
doesn't exist yet — the hook returns it). `cwd` is the repo root, so the niwa adapter
can resolve the repo directly from `cwd`. The hook must print the destination path to
stdout; Claude then uses that path as the session working directory.

**3. The workspace-root install location does NOT work. (Experiment B — confirmed,
decisive for niwa.)** With the hook only in the non-git parent's
`.claude/settings.json` and the agent launched in a subdir git repo, the hook did
NOT fire. Claude fell back to default git behavior, creating
`<repo>/.claude/worktrees/spikeB` on branch `worktree-spikeB`. Claude resolves
project settings from the repo (git root) plus user/managed scope; it does not walk
up to a parent directory's `.claude/`. niwa's current `InstallWorkspaceRootSettings()`
target is therefore insufficient on its own.

**4. The per-repo `settings.local.json` install location works. (Experiment C —
confirmed.)** With the hook only in the repo's `settings.local.json` — niwa's actual
per-repo materialization target — the hook fired and again replaced default behavior
(worktree at the custom path, branch `spike-spikeC`). This is the correct install
surface, and niwa already has the per-repo path to write it (`SettingsMaterializer` +
`HooksMaterializer` in the shared `runRepoMaterializers` loop).

**5. The settings `env` block does not reach the hook subprocess.** An
`"env": {"HOOK_SCOPE": "perRepo"}` entry in the same `settings.json` that carried the
(firing) hook did not appear in the hook's environment (logged `unknown`). niwa must
pass any context to the hook via the command string, not the settings `env` block.

**6. `WorktreeRemove` is fire-and-forget (from docs, not re-tested).** The hooks
reference states `WorktreeRemove` failures are logged in debug mode only and cannot
block removal. Claude treats the worktree as removed regardless of the hook's exit —
which must be reconciled with niwa's dirty/attached destroy guards in the design.

**7. Delegation dissolves the branch-name conflict.** Because the hook fully owns
creation, niwa picks the branch and path; Claude just consumes the returned path. The
`worktree-<name>` vs `session/<sid>` mismatch noted in exploration is moot under
delegation — niwa controls both, and Claude's `name` can feed niwa's purpose.

## Recommendation

**GO on delegation, with the install scope narrowed to per-repo
`.claude/settings.local.json`.**

Hook delegation is viable today and is the right mechanism for "one worktree system,
not two": the native `EnterWorktree`/`--worktree` path can be made to produce niwa
worktrees transparently. The only correction to the exploration's assumption is the
install location — it must be per-repo, not workspace-root.

Conditions and next steps for the follow-on design:

- **Install the `WorktreeCreate`/`WorktreeRemove` hooks per-repo** via niwa's existing
  `SettingsMaterializer`/`HooksMaterializer` path (writing `settings.local.json`),
  applied to every workspace repo on `niwa apply` and into every niwa worktree. Do
  NOT rely on `InstallWorkspaceRootSettings()` for the hook.
- **Build a thin stdin->niwa adapter**: read the hook JSON, resolve the repo from
  `cwd`, map `name` to a purpose, call `niwa worktree create`, and print the resulting
  worktree path to stdout. This requires `niwa worktree create` to emit its worktree
  path in a machine-readable form (or the adapter to capture it).
- **Reconcile `WorktreeRemove`'s non-blocking nature with niwa's destroy guards**:
  the remove hook should detach / best-effort clean, not force-destroy, leaving
  dirty/attached teardown to `niwa worktree destroy`.
- **Keep deny+steer as the documented fallback** (`permissions.deny:
  ["EnterWorktree","ExitWorktree"]` + CLAUDE.md guidance) for hosts/Claude versions
  that don't honor the per-repo hook — now a true fallback, not the default.

No further spike is needed before `/design`.
