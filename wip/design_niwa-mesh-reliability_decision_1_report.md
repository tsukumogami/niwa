# Decision 1: Worker discovery channel

## Question

How should worker sessions discover the workspace's Claude Code plugin set
AND the niwa-mesh skill, without leaking the skill into consumer repo
working trees? Covers issues #108 (workspace plugins absent in workers)
and #97 (`.claude/skills/niwa-mesh/SKILL.md` committed to consumer PRs).

## Options

### A. argv flag
- Mechanics: add a flag to the `claude -p` argv assembled in
  `spawnWorker` (`internal/cli/mesh_watch.go:982-1001`). Candidates
  surfaced in research: `--settings <workspaceRoot>/.claude/settings.json`,
  `--add-dir <workspaceRoot>/.claude`, or
  `--plugin <alias>@<marketplace>` per-plugin. The workspace settings
  file is already authored by `InstallWorkspaceRootSettings`
  (`internal/workspace/workspace_context.go:136-264`) with the right
  `enabledPlugins` and `extraKnownMarketplaces`, so we are pointing
  Claude at an existing artifact, not creating a new one.
- Pros: explicit, programmatic, survives any future Claude Code change
  to filesystem-walk-up discovery; symmetric with the existing
  `--mcp-config=<path>` and `--strict-mcp-config` argv (mesh_watch.go:982-988)
  which already deliberately scopes config rather than relying on
  inheritance; works identically for main-instance and per-session
  daemons because it is computed from `NIWA_MAIN_INSTANCE_ROOT` (or
  `s.instanceRoot` when that is empty); zero filesystem changes inside
  consumer repos or worktrees, so it cannot regress #97.
- Cons: depends on Claude Code actually supporting one of these flags;
  research notes (lead 1, "Open Questions") that no evidence in the
  codebase confirms a `--plugin`/`--settings`/`--add-dir` flag exists.
  If only `--add-dir` works, that lets Claude *find* the directory but
  may not force settings merge precedence; if only `--settings` works,
  it may fully replace rather than layer onto user settings, which
  could lose `~/.claude.json` plugin-store registrations.
- Risk: medium. If the chosen flag has subtler semantics than expected
  (e.g. replaces user settings instead of layering), workers may still
  fail to resolve plugin aliases like `shirabe@shirabe`. Mitigatable by
  testing once before committing to the flag.

### B. CLAUDE_CONFIG_DIR env var
- Mechanics: set `CLAUDE_CONFIG_DIR=<workspaceRoot>/.claude` (or
  `<mainInstanceRoot>/.claude` when running in a session daemon)
  alongside the existing `NIWA_*` env injection at
  `mesh_watch.go:1005-1009`. Compute the path once in `spawnWorker`
  using `s.mainInstanceRoot` if set else `s.instanceRoot`.
- Pros: minimal diff (one `cmd.Env = append(...)` line); does not
  depend on argv schema changes; identical for main-instance and
  per-session daemon spawns (the spawn site is shared); survives
  filesystem-walk-up discovery quirks because it overrides the
  discovery root entirely.
- Cons: same load-bearing assumption as A about Claude Code semantics
  - we do not know whether `CLAUDE_CONFIG_DIR` is the right variable
  name, whether it points at `.claude/` or the parent, and whether it
  layers with or replaces `~/.claude.json`. Research lead 1 line 127
  flags this as a hypothesis ("if Claude Code supports..."), not a
  confirmed contract.
- Risk: medium-high. Env-var contracts tend to be undocumented and
  silently change; an undocumented variable could be renamed in a
  future Claude Code release without notice. The argv contract is at
  least visible in `--help`.

### C. Filesystem mirror in scaffoldWorktreeNiwa
- Mechanics: in `scaffoldWorktreeNiwa`
  (`internal/mcp/handlers_session.go:80-108`), after creating
  `.niwa/`, additionally symlink (or copy) `<mainInstance>/.claude/`
  into `<worktree>/.claude/` — or write a minimal
  `<worktree>/.claude/settings.local.json` that references the parent.
  This addresses session daemons only.
- Pros: leans on the discovery mechanism Claude Code already uses
  (filesystem walk-up + `.claude/settings.local.json` lookup); needs no
  new flags or env vars; the worktree path is `<workspaceRoot>/.niwa/worktrees/...`,
  so a mirror at the worktree root is enough for any worker spawned
  with `cmd.Dir = worktreePath`.
- Cons: only addresses the per-session daemon case. Main-instance
  workers (CWD `<workspaceRoot>/<group>/<repo>`) are not improved by
  this option — they already have `<repoDir>/.claude/settings.local.json`
  via `SettingsMaterializer` and the empirical failure in #108
  suggests plugin alias resolution is broken even when settings ARE
  present. So this option fixes one half of #108 at most. It also
  re-introduces filesystem coupling exactly when we are trying to
  remove it for #97 — a copied skill in `<worktree>/.claude/skills/`
  is one mis-configured `.gitignore` away from re-leaking. Symlinking
  reduces but does not eliminate that risk (worktrees are git
  worktrees with their own working tree state). Adds a new failure
  mode: stale mirrors when the workspace is reapplied while a session
  is open.
- Risk: high for full coverage; medium for session-only. Does not
  satisfy the "main-instance daemon AND per-session daemon" criterion
  on its own.

## Chosen: A (argv flag) with B as runtime fallback; C explicitly rejected

The primary mechanism is option A: pass an explicit argv flag to
`claude -p` in `spawnWorker` that points Claude at the workspace
`.claude/` directory or settings file. If experimentation shows that
the only working channel is an env var, fall back to B; both can be
implemented at the same code site (`mesh_watch.go:982-1009`). The
implementation should pick whichever flag/env Claude Code actually
honors; from a niwa-architecture perspective A and B are the same
decision (programmatic injection at spawn time, no filesystem
side-channel), and the choice between them is an empirical Claude
Code question to settle during implementation. C is rejected as
primary because it does not fix main-instance daemon workers and it
keeps the filesystem-side-channel pattern that lead 1's "Implications"
section identifies as fragile.

## Rationale

The two issues share a thematic root that lead 1 calls out at line 120:
*"niwa relies on side-channel filesystem layout to communicate Claude
Code config to workers, instead of explicit programmatic config."*
Every existing pain point in the spawn pipeline traces back to this —
plugins drop because filesystem walk-up is unreliable (lead 1 §3,
mesh_watch.go:1012 + handlers_session.go:78-79), the niwa-mesh skill
leaks because it is delivered via working-tree files
(channels.go:347-359, lead 2 §i), and `--strict-mcp-config` exists
specifically because the team already learned the lesson for MCP
servers (lead 1 §6 surprises). Continuing the filesystem pattern (C)
would extend the same fragility instead of fixing the architecture;
adding programmatic injection (A/B) brings plugin handling up to the
same level of explicitness MCP config already has at
`mesh_watch.go:982-988`.

Between A and B, argv flags are preferred because they are visible in
`--help` output, are documented contracts in CLI tools, and are
symmetric with the `--mcp-config=` and `--strict-mcp-config` already
in use. Env vars are a reasonable fallback if argv does not expose the
right control, but they are inherently less discoverable and more
likely to be renamed silently. Both A and B work uniformly for
main-instance and per-session daemons because `spawnWorker` is the
single spawn site and can compute the workspace root from
`NIWA_MAIN_INSTANCE_ROOT` (set by `niwa_create_session` per
handlers_session.go:211-225) or fall back to `s.instanceRoot` for the
main-instance case where the instance root IS the workspace root.

C is rejected on the merits, not just on architectural style. Lead 1
§3 (line 66) establishes that for the per-session daemon, the
worktree path is `<workspaceRoot>/.niwa/worktrees/<repo>-<id>/`, which
walks up *into* `<workspaceRoot>/.claude/settings.json`. So in
principle filesystem discovery should already work for session
workers. The empirical failure described in #108 means either
(a) Claude Code does not walk up past `.niwa/worktrees/`, or
(b) plugin alias resolution silently drops aliases when the user-level
plugin store does not know them — and option C addresses neither root
cause. A `.claude/` mirror inside the worktree is just a re-statement
of the discovery contract that is already failing.

## Skill-leak (#97) corollary

The chosen mechanism makes it safe to delete the per-repo skill writes
entirely.

Concrete change:

1. Remove `internal/workspace/channels.go:347-359` (the
   `for _, r := range roles` loop that writes
   `<repoPath>/.claude/skills/niwa-mesh/SKILL.md`). Keep the
   instance-root write at line 341
   (`<instanceRoot>/.claude/skills/niwa-mesh/SKILL.md`) — that path is
   not inside any consumer repo working tree and is already on the
   workspace `.gitignore` surface.

2. Workers find the skill via the same mechanism that delivers the
   plugin set in option A: when the worker's `claude -p` is launched
   with `--settings <workspaceRoot>/.claude/settings.json` (or
   equivalent), Claude Code's skill discovery will look in
   `<workspaceRoot>/.claude/skills/` per its standard search path and
   find `niwa-mesh/SKILL.md` there. No per-repo copy needed.

3. As a defense in depth, the implementation should also extend the
   `internal/workspace/gitignore.go` rule (currently only writing to
   the instance root per lead 1 §4) to add
   `.claude/skills/niwa-mesh/` to each consumer repo's `.gitignore`
   on apply — this protects against any historical commits and any
   future regressions that re-introduce the per-repo write.

This eliminates #97 at the source: the file the agent could `git add`
no longer exists at the path the agent is working in. The deeper
"Decision 5, flat uniform skill" intent (lead 2 §i) is preserved
because the skill content is unchanged and is still authoritatively
written by `buildSkillContent` at one canonical path; only the
*delivery* surface narrows from N+1 paths to 1.

For session-daemon workers specifically, the worktree's
`.niwa/worktrees/<repo>-<id>/` path means the workspace's
`<workspaceRoot>/.claude/skills/` is reachable via the same argv-flag
path used for main-instance workers, with no per-worktree mirroring
needed (no C-style change). This keeps `scaffoldWorktreeNiwa` minimal
and on-message with its existing comment that worktrees should not
copy main-instance artifacts.

## Confidence

medium

The architectural call (programmatic injection over filesystem
side-channel) is high-confidence — it is the same pattern niwa
already uses for `--mcp-config`/`--strict-mcp-config`, and lead 1
documents the failure modes of the side-channel pattern in detail.

The choice between A (argv) and B (env var) within the programmatic
camp is medium-confidence: the research explicitly flags Claude Code
flag/env support as an open question (lead 1 "Open Questions" line
152). Implementation must verify which flag actually works before
committing to the exact spelling. The fallback structure (try A, fall
back to B at the same code site) absorbs this risk.

The #97 corollary is high-confidence: removing the per-repo skill
write at channels.go:347-359 cannot regress worker skill discovery if
A/B work, because the workspace-root copy remains and is exactly what
A/B make discoverable.

## Assumptions

- Claude Code supports at least one of `--settings <path>`,
  `--add-dir <path>`, `--plugin <alias>@<marketplace>`, or
  `CLAUDE_CONFIG_DIR=<path>`. If it supports none, the design must
  fall back to C (filesystem mirror) for session daemons and accept
  that main-instance daemons need a different fix (e.g. fixing
  whatever causes plugin alias resolution to drop today). Research
  lead 1 §3 implies the user-level `~/.claude.json` plugin-store may
  also need population for aliases to resolve — option A may need to
  be paired with materializing plugin-store entries, which is out of
  scope for this decision.
- Claude Code's skill discovery looks under
  `<configDir>/skills/` when `<configDir>` is supplied via flag or
  env. If skills are discovered separately from settings (e.g. only
  via plugin manifests), the #97 corollary needs an additional
  delivery channel — possibly bundling the skill into a niwa-owned
  plugin instead of a bare `.claude/skills/` directory.
- `NIWA_MAIN_INSTANCE_ROOT` is reliably set for session daemons (per
  handlers_session.go:211-225) and absent for main-instance daemons,
  so `spawnWorker` can branch on its presence to compute the right
  workspace root. Lead 1 §1 confirms this.
- The instance-root SKILL.md path
  (`<workspaceRoot>/.claude/skills/niwa-mesh/SKILL.md`) is already
  outside every consumer repo's working tree. This is true for the
  default workspace layout (consumer repos at
  `<workspaceRoot>/<group>/<repo>`) but should be re-checked if
  workspace layouts ever flatten (e.g. `<workspaceRoot>/<repo>` with
  no group).
- Squash-merge is the workspace's PR strategy (per the project
  CLAUDE.md `wip/` discussion), so any historical
  `.claude/skills/niwa-mesh/SKILL.md` already in feature branches will
  not survive merge. Otherwise, a one-time cleanup commit per
  consumer repo would be required.
