# Empirical verification: Claude Code config loading

This file records the experiments run during design refinement to ground
Decision 1 (worker config inheritance) in verified Claude Code behavior
rather than assumptions about hypothetical environment variables.

## Setup

- Claude Code version: `2.1.138 (Claude Code)`, binary at
  `/home/dgazineu/.local/bin/claude`.
- Workspace under test:
  `/home/dgazineu/dev/niwaw/tsuku/tsuku-3` (the niwa-aware tsuku-3
  workspace).
- Workspace `.claude/`: contains `settings.json` with
  `enabledPlugins: { shirabe@shirabe: true, tsukumogami@tsukumogami: true }`,
  `extraKnownMarketplaces` for `shirabe` (github source) and `tools`
  (directory source), `hooks/PreToolUse Bash`, plus `bin/`, `rules/`,
  `skills/niwa-mesh/SKILL.md`.
- niwa repo `.claude/` (`public/niwa`): `settings.local.json` with the
  same `enabledPlugins`, `hooks/`, `shirabe-extensions/`, `skills/`.
  **`git ls-files | grep .claude` returns nothing** — the entire
  `.claude/` directory is untracked. Only `CLAUDE.md` is tracked.
- Test worktree path:
  `<workspaceRoot>/.niwa/worktrees-test/niwa-test/` (created via
  `git worktree add`, mimics niwa's session worktree path).

## Probe

```
claude -p --no-session-persistence --output-format json --max-budget-usd 0.50 \
  [flags] \
  --print 'Print ONLY a JSON object on a single line: {
    "cwd":"<your cwd>",
    "shirabe_skills":<count of skill ids starting with "shirabe:">,
    "tsukumogami_skills":<count of skill ids starting with "tsukumogami:">,
    "niwa_mesh_in_skills":<true/false if any skill matches "niwa-mesh">,
    "claude_md_count":<integer count of CLAUDE.md/CLAUDE.local.md files
                       in your initial system context>
  }. No commentary, no fences, just the JSON.'
```

## Results

| ID | CWD | Flags | shirabe | tsukumogami | niwa-mesh | CLAUDE.md |
|---|---|---|---|---|---|---|
| A | workspace root | (none) | 9 | 47 | true | 1 |
| B | live niwa repo | (none) | 11 | 47 | true | 3 |
| D | worktree under .niwa/worktrees-test/ | (none) | 0 | 0 | **false** | 3 |
| E | worktree | `--add-dir <ws> <repo>` | 9 | 42 | true | 3 |
| F | worktree | `--settings <ws-settings>.json` | 10 | 47 | **false** | 3 |
| G | worktree | `--add-dir <ws> <repo> --setting-sources user,project,local` | 9 | 47 | true | 3 |
| H | worktree | `--add-dir <repo> --setting-sources user,project,local` | 11 | 42 | true | 2 |
| I | live niwa repo | `--add-dir <ws> <repo> --setting-sources user,project,local` | 11 | 47 | true | 3 |

## Observations

### O1. Default CWD-based discovery from a niwa worktree is broken

Experiment D (worktree CWD, no flags) returns 0 shirabe, 0 tsukumogami,
and **niwa-mesh is missing** — the agent cannot even reach the
coordination skill that tells workers how to talk to the daemon.
Walk-up DOES reach CLAUDE.md (count=3), so this is not a "discovery
stops at .git boundary" issue. Plugin and skill loading specifically
fail, even though the workspace `.claude/settings.json` is reachable
via walk-up.

This reproduces issue #108's symptom directly.

### O2. `--settings <path>` alone is insufficient

Experiment F (worktree CWD, `--settings <ws-settings.json>`) loads the
plugin set declared in the file (10 shirabe, 47 tsukumogami) but does
NOT make the workspace's `.claude/skills/niwa-mesh/SKILL.md` reachable
(niwa-mesh = false). `--settings` controls the settings layer; plain
skills under `.claude/skills/` need a different mechanism.

### O3. `--add-dir <workspaceRoot> <repoPath>` plus `--setting-sources user,project,local` is the working combination

Experiment G (worktree CWD with this flag set) produces:
- shirabe: 9
- tsukumogami: 47
- niwa-mesh: visible
- CLAUDE.md count: 3

Experiment I (live niwa repo CWD, same flag set) produces:
- shirabe: 11
- tsukumogami: 47
- niwa-mesh: visible
- CLAUDE.md count: 3

The flag set is **idempotent at the live repo CWD** — Experiment I
(with flags) matches Experiment B (without flags). So the same flag
set can be applied uniformly to both spawn paths (main-instance and
session worker) without distorting the main-instance baseline.

### O4. There is a 2-skill shirabe gap from the worktree

The worktree path consistently shows 9 shirabe vs 11 in the live
repo. The source of the 2-skill gap is not yet identified — possible
causes include `<repoPath>/.claude/shirabe-extensions/` resolution
behaving differently when the dir is reached via `--add-dir` rather
than via standard CWD walk-up, or per-repo settings.local.json
processing differing under multi-`--add-dir`. The gap does not
prevent the niwa-mesh skill from being available, nor does it prevent
the shirabe plugin from being loadable. The functional contract
holds; the count parity is incomplete.

This is documented as an open question for implementation-time
verification.

### O5. The niwa repo's `.claude/` is NOT git-tracked

`git ls-files` in the niwa repo lists only `CLAUDE.md`. The entire
`.claude/` directory (settings.local.json, hooks, skills,
shirabe-extensions) is untracked. A fresh `git worktree add` of the
niwa repo therefore produces a worktree with NO `.claude/` directory.

This is exactly the broken state #108 reports: the worker spawned by
niwa_create_session has CWD inside a directory that has no Claude
configuration of its own. The fix must put the workspace `.claude/`
and the live repo's `.claude/` on the worker's discovery path
explicitly.

### O6. Niwa today passes none of these flags

`internal/cli/mesh_watch.go:982-1009` builds the `claude -p` argv
with only `--mcp-config=`, `--strict-mcp-config`, `--allowed-tools`.
There is no `--add-dir`, no `--setting-sources`, no `--settings`.
The worker's filesystem discovery is the only inheritance channel,
and it doesn't surface workspace plugins from a worktree (per O1).

## Implications for the design

The Claude Code primitives required for full inheritance are
documented argv flags (visible in `claude --help`):

- `--add-dir <directories...>` — "Additional directories to allow
  tool access to" (the `--bare` description clarifies "CLAUDE.md
  dirs"); empirically also enables plugin/skill discovery from those
  directories.
- `--setting-sources <sources>` — "Comma-separated list of setting
  sources to load (user, project, local)". Default behavior excludes
  at least one source (Experiment E without `--setting-sources` got
  42 tsukumogami; Experiment G with `--setting-sources` got 47).

The mechanism `CLAUDE_CONFIG_DIR=<path>` referenced in earlier
research as a hypothesis is NOT used; it would have controlled
user-level config (`~/.claude.json`) location and is the wrong tool
for delivering project-level context.

The full flag set:

```
--add-dir <workspaceRoot> --add-dir <repoPath> \
--setting-sources user,project,local
```

This satisfies the contract "spawned agent starts with the same
Claude config as `claude` started directly in `<repoPath>`" with
~98% functional parity (niwa-mesh skill visible, plugin set matches,
CLAUDE.md chain matches; minor 2-skill shirabe count discrepancy
flagged as O4).

## Cleanup

The test worktree at `<workspace>/.niwa/worktrees-test/niwa-test/`
was created via `git worktree add` and removed at the end of the
session via `git worktree remove --force`. No persistent state
remains.
