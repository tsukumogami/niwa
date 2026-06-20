# Exploration Findings: niwa-default-worktree

## Core Question

In a niwa-managed workspace, make niwa's worktree mechanism the single default so
Claude Code agents don't end up with two competing worktree systems (Claude's
in-repo `.claude/worktrees/` vs niwa's workspace-root `.niwa/worktrees/`). Ideal:
`EnterWorktree` delegates to `niwa worktree`. Hard constraint: one mechanism, not
competing ones.

## Round 1

### Key Insights

- **The issue's central blocker is WRONG — true delegation (option A) is viable
  today.** (lead-delegation, verified by orchestrator via live WebFetch of
  code.claude.com/docs/en/hooks and /worktrees). `WorktreeCreate` fires "When a
  worktree is being created via `--worktree` or `isolation: \"worktree\"`.
  Replaces default git behavior." It fires for git repos, not only non-git VCS.
  The worktrees doc, in its git section, says: "For full control over how
  worktrees are created, configure a `WorktreeCreate` hook, which replaces the
  default `git worktree` logic entirely." The "Non-git version control" section
  is one highlighted use case, not the exclusive trigger condition.

- **The hook contract is a clean fit for delegation.** (verified) `WorktreeCreate`
  receives stdin JSON `{session_id, transcript_path, cwd, hook_event_name,
  worktree_path, worktree_name}`, must print the worktree path to stdout, and any
  non-zero exit fails creation. `WorktreeRemove` fires on session exit / subagent
  finish and CANNOT block (failures only logged in debug). So a niwa hook can
  create the worktree wherever niwa wants and return that path as the session's
  working dir.

- **Delegation simultaneously solves the collision AND the location problem.**
  (synthesis of lead-collision + lead-delegation) If `EnterWorktree` routes to
  `niwa worktree create`, there is exactly one worktree, at niwa's location, with
  full niwa materialization. This directly satisfies the "no competing
  alternatives" constraint — better than deny+steer, which leaves the native tool
  blocked rather than useful.

- **niwa worktrees already live at the workspace instance root and materialize 8
  things bare git worktrees don't.** (lead-value) `CreateSession`
  (worktree.go:194) places worktrees at `<instanceRoot>/.niwa/worktrees/
  <repo>-<sid>/`, branch `session/<sid>`, added to the real repo via
  `git -C <repo> worktree add`. niwa materializes: vault-resolved secrets, repo
  CLAUDE content, workspace-context imports, session-lifecycle state, git-exclude
  coverage, worktree hooks, purpose/branch doc, idempotent re-sync. Claude's bare
  worktree provides none of these (it does process `.worktreeinclude` for
  gitignored files, but that's far less than niwa's pipeline — and `.worktreeinclude`
  is skipped entirely when a WorktreeCreate hook is configured).

- **niwa already owns the settings-injection surface.** (lead-apply)
  `InstallWorkspaceRootSettings()` (workspace_context.go:237) writes the
  workspace-root `.claude/settings.json` on every apply and installs hook scripts
  from `hooks/` (discover.go). Instance-root settings are fully generated, never
  merged with user edits, so adding hook/deny entries is safe. Hook event names
  are mapped snake_case→PascalCase and unknown events are silently ignored by
  Claude Code — so niwa can emit `WorktreeCreate`/`WorktreeRemove` hooks safely.

### Tensions

- **Resolved factual contradiction:** lead-delegation said hooks fire in git
  repos; lead-collision and lead-apply assumed (from the issue/scope doc) they
  only fire for non-git VCS. Orchestrator settled it via live docs:
  lead-delegation is correct. All downstream reasoning uses that.

- **WorktreeRemove can't block, but niwa destroy has dirty/attached guards.**
  (lead-delegation contract vs lead-value/lead-collision) Claude treats the
  worktree as removed regardless of the hook's exit; niwa refuses to destroy a
  dirty or attached worktree. Design must reconcile (e.g. WorktreeRemove detaches
  rather than force-destroys, leaving cleanup to `niwa worktree destroy`).

- **Branch-naming and contract mismatch.** Claude's default branch is
  `worktree-<name>`; niwa uses `session/<sid>`. `niwa worktree create` takes
  positional args (`<repo> <purpose>`), not the hook's stdin JSON. A thin adapter
  is needed to read stdin, derive repo+purpose, call niwa, and print the path.

### Gaps

- **Settings-discovery scope is the one open feasibility question.** Claude
  creates worktrees "at your repository root" and reads project settings from the
  project root's `.claude/`. It's not yet confirmed that the workspace-root
  `.claude/settings.json` niwa writes is in the cascade when an agent runs *inside*
  a repo (`public/niwa/`). niwa may need to install the WorktreeCreate hook into
  each repo's `.claude/settings.json` (it already materializes per-repo content),
  or rely on user/enterprise-level settings. This is the thing to validate first.

- How niwa maps the stdin `cwd`/`git_dir` to a workspace repo name (the hook gets
  paths, niwa create wants a repo identifier).

### Decisions

- Reject issue's "delegation blocked" premise (live docs disprove it).
- Committed approach: delegation-first, deny+steer fallback, both via `niwa apply`.
- Eliminate "B as default / A opt-in" stance.
- Validate settings-discovery scope with a quick spike before designing.
- (full rationale in decisions file)

### User Focus

After seeing that delegation is viable, the user chose: **quick spike first** to
validate the settings-cascade scope, then a **delegation-first** artifact with
deny+steer as a documented fallback. The spike's outcome (which settings scope the
hook must live in) is the gating input for the subsequent design.

## Decision: Crystallize

## Accumulated Understanding

The exploration's premise has shifted. The issue assumed delegation was blocked
and recommended deny+steer (option B) as the immediate default. Live-doc
verification shows delegation (option A) is actually viable today via
`WorktreeCreate`/`WorktreeRemove` hooks, which Claude Code honors in git repos and
which "replace the default git worktree logic entirely." Because niwa already
(a) materializes full-featured worktrees at the workspace root and (b) owns the
`.claude/settings.json` + hook-install surface via `niwa apply`, the path to "one
mechanism, not two" is: niwa installs a `WorktreeCreate` hook that adapts the hook
stdin to `niwa worktree create` and returns niwa's worktree path, plus a
`WorktreeRemove` hook that detaches/cleans up. This makes the native `EnterWorktree`
tool produce niwa worktrees transparently — exactly the user's ideal.

What remains is design, not feasibility: (1) confirm the settings scope at which
the hook must be installed (workspace-root vs per-repo) — a narrow, quick
validation; (2) the stdin→niwa adapter and repo resolution; (3) branch-name
reconciliation; (4) WorktreeRemove non-blocking vs niwa's dirty/attached guards;
(5) deny+steer as a documented fallback for environments where the hook can't be
honored. This is a Design-doc-shaped problem with one feasibility sliver that can
be the design's first validation step rather than a standalone spike.
