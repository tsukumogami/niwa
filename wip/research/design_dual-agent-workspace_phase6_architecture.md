# Verdict: FAIL

Reviewed as architecture only (security and format/completeness are owned elsewhere).
The chosen shape — payload at the instance root, per-repo `.codex` symlink under the
default `.git` marker, `AGENTS.override.md` composing the full chain, scoped trust
entries — is the right shape, and the rejections are honest. It fails on four
things the document states confidently and does not establish.

## PRD satisfaction

**Discharged, and visibly so:** R1 (Decision 7A makes preparation unconditional),
R2 (the Claude writers are untouched; the Codex pass is additive), R3 (the
"What `niwa apply` refreshes" section covers regeneration and the
no-accumulation criterion), R5 (Decision 1A needs no environment at all — verified
against `internal/cli/shell_init.go:52`, which wraps `create|destroy|go|init` and
`session create` and never reacts to `cd`, exactly as the design claims), R6 and R12
(Decision 2A's inline-don't-overwrite plus the never-empty and no-file-when-empty
boundary rules), R10 and R13 (Decisions 5 and 6), R14 (the discriminator survives;
`internal/cli/dispatch.go:236` keys on the resolved launch selection and needs no
logic change, as claimed).

**R8 — not discharged.** Decision 3 turns on `<instance>/.codex/skills/<plugin> ->
<plugin root on disk>`, and "plugin root on disk" is the one input niwa does not
have. niwa's plugin model is name-based end to end: it writes
`extraKnownMarketplaces`/`enabledPlugins` into `.claude/settings.json`
(`internal/workspace/workspace_context.go:463-503`) and then shells out to
`claude plugin marketplace add` / `claude plugin install`
(`internal/cli/dispatch_plugins.go:69,90`). It computes a marketplace *root* only
for `repo:`-sourced entries (`internal/workspace/plugin.go:16-50`), and it never
parses `marketplace.json` for the per-plugin subdirectory —
`readMarketplaceManifestName` reads the name and nothing else
(`internal/workspace/workspace_context.go:513-533`). For a github-sourced
marketplace the tree lives in Claude Code's own user-global cache
(`~/.claude/plugins/marketplaces/<name>/`, a path niwa hardcodes in exactly one
place today, `internal/plugin/installer.go:47`), so the design would make the
internal directory layout of a *second* external tool load-bearing for R8 — a
dependency the "leans on upstream discovery invariants" limitation does not
mention. Worse for the design's own strongest driver: pre-warming is explicitly
best-effort and skippable (`SkipPluginInstall`, `auto_install_plugins`, `claude`
absent, timeout — `internal/cli/dispatch_plugins.go:46-49`), and on the Claude side
the session self-heals by installing from `settings.json` at startup. A Codex
session has no such fallback, so the failure mode is "no workspace skills, no
error" — the majority-works/minority-silent shape Decision 2B was rejected for.
The design's measurement ("all twenty skills loaded, correctly namespaced")
establishes that Codex *reads* a symlinked plugin tree correctly; it does not
establish that niwa can *locate or guarantee* that tree at materialize time.

**R11/R12 — a hole at `.codex`.** Decision 7's closing note retires the collision
guard on the grounds that "niwa never writes to that filename", reasoning only about
`AGENTS.md`. But the second per-repository write is a directory entry named
`.codex`, and the design's own Security Considerations paragraph presupposes that a
repository may carry "a `.codex/` directory committed anywhere in the repository's
tree". If a cloned repository commits `.codex/` at its root, planting
`<repo>/.codex -> <instance>/.codex` either fails the apply or replaces tracked
content — deleted tracked files in `git status` (R11) and a clobbered repository file
(R12). The document asserts the guard is unnecessary and, two sections later,
assumes the exact condition that makes it necessary. No detection, no fallback, no
error path is specified.

**R4 — the worktree case drops a layer.** R4 requires the layers "niwa materializes
for Claude: instance, group, repository, and, in a worktree, the worktree's own
framing". The composition rule says a worktree's override "carries instance, group,
and the worktree framing", and the on-disk section says the worktree framing goes
"in place of the repository layer". That is not what Claude gets:
`ApplyToWorktree` installs the repo's own content into the worktree first
(`internal/workspace/worktree_content.go:512`, `InstallRepoContentTo` targeted at
`worktreePath`) and *then* appends the purpose/branch section to the same
`CLAUDE.local.md` (`worktree_content.go:641` → `installWorktreeContextLayer:736-777`,
which merges rather than replaces). So a Claude worktree session sees repo layer +
worktree framing; the Codex worktree file as specified sees only the framing. The
design also never says whether a worktree's committed `AGENTS.md` gets inlined the
way a clone's does — a worktree is a checkout, so it usually has one.

## Rejected-alternative honesty

All five rejections are fair, and each cites the thing that actually killed it
rather than a caricature.

- **1B (per-instance `CODEX_HOME`)** is the strongest of them and is presented as
  the former leading candidate, with its genuine advantage (marketplaces/plugins,
  which the project layer cannot carry) stated before the defect. The defect is
  verifiable and verified: `internal/cli/shell_init.go:52-72` wraps five
  subcommands and nothing else, so a manually opened shell never gets the variable.
  Not a strawman.
- **1C (repoint `project_root_markers`)** is credited as *measured to work* before
  being rejected on blast radius, and the nearest-ancestor-wins argument for why
  `.git` cannot be kept alongside is the real mechanism, not a hand-wave. It also
  volunteers that 1C would still need the same trust entries, which weakens the
  case *for* rejecting it and is included anyway. Honest.
- **2B (fallback filename onto `CLAUDE.local.md`)** is credited with working "for
  most" repositories and costing zero extra writes, and rejected on a concrete
  instance: a repository in this workspace ships a committed `AGENTS.md` today.
  Verified — `public/shirabe/AGENTS.md` exists. The added trust-dependency argument
  (the fallback list lives in the project layer; the hardcoded candidate names do
  not) is a real asymmetry, not padding.
- **3B (loose skill copies)** cites two measured failures (namespace collapse to
  bare `decision`, orphaned plugin-root `references/`/`scripts/`) and draws the
  right conclusion about the unit of delivery.
- **3C (rewrite `${CLAUDE_PLUGIN_ROOT}`)** is the most thoroughly killed: the
  binary contains the string once, as a hook env var name; blind substitution
  corrupts prose that describes the variable; and the `${VAR:-...}` fallback form
  the plugins already use is why verbatim works. Fair.

The one rejection I would push on is 7A's self-justification rather than its
alternatives — see Internal coherence.

## Structural fit

The seam claim is **half right, and the half that is wrong is the half batch 2
depends on.**

Where it holds: at the niwa-owned levels the accessor really is a pure filename
selector and the caller carries the choice. `InstallWorkspaceContent`
(`internal/workspace/content.go:44`) and `writeRootClaudeMD`
(`internal/workspace/root_materializer.go:375`) both do
`filepath.Join(dir, ag.RootContextFileName())` and nothing else agent-dependent, so
running the caller twice genuinely yields both files. `TestContentTreesCoexist`
(`internal/workspace/content_test.go:187-206`) already proves this by calling
`InstallWorkspaceContent` twice with different agents.

Where it does not hold:

1. **`WritesRepoLevelContext()` is an accessor, and it is where the repo/worktree
   exclusivity lives** — `internal/agent/agent.go:85-87`, consumed at
   `internal/workspace/content.go:130` and
   `internal/workspace/worktree_content.go:740`. Running those callers "once per
   agent" writes nothing on the Codex pass. The design names
   `LocalContextFileName()` for retirement but is silent on
   `WritesRepoLevelContext()`, which is the one that matters. Batch 2 is therefore
   net-new code hanging off a *different* seam, not the caller rework batch 1
   builds; the document should say so, because "the per-agent caller seam is what
   the Codex materializer plugs into" (Implementation Approach) is not accurate for
   the repository level.

2. **`InstallGroupContent` cannot produce a composed group file by
   re-parameterization.** It writes exactly one source — the group's own entry
   (`internal/workspace/content.go:57-86`). Run it with `AgentCodex` and
   `<group>/AGENTS.md` carries the group layer only, which violates the design's own
   composition rule ("a group's carries instance plus group"). So the group-level
   Codex writer is also net-new composition, not the same writer with a different
   filename. Batch 1's phrase "the composed AGENTS.md" hints at this; Decision 7A's
   justification ("no accessor needs to change meaning", "the Claude pass exactly as
   today and a Codex pass that writes the `AGENTS.md` files") reads as if
   parameterization sufficed. It does not.

Fits well elsewhere. The git-exclude extension lands cleanly:
`EnsureRepoExclude(tree, extraPatterns...)` already takes extras
(`internal/gitexclude/exclude.go:54`), already unions and dedupes them
(`unionPatterns:88-105`), and already resolves `--git-common-dir`
(`gitCommonDir:120-141`) so one write covers a repository and all its linked
worktrees — which is why the design's per-worktree exclude story needs no new
mechanism. The bare-vs-trailing-slash warning is exactly right and has precedent in
the tree: `worktreeRulesFile` was already added as a scoped extra for the same
reason (`internal/workspace/worktree_content.go:625`). Decision 4's trust writer is
genuinely new surface with no existing seam to fight. Decision 7B's rejection of
generalizing `buildSettingsDoc` is correct — its keys are Claude Code API surface.

## Blast-radius accuracy

The tally is wrong in both directions, and R2's acceptance criterion turns the exact
set of permitted test edits into a pass/fail condition, so this is not cosmetic.

`test/functional/features/codex-agent.feature` has three scenarios, but only **two**
assert materialization exclusivity — scenario 2 (`niwa dispatch refuses`, lines
42-60) asserts a refusal the design itself says is unchanged. And the file carries
**four** "does not exist" assertions, not two:

- line 37 `CLAUDE.md does not exist` — inverts, as described.
- line 86 `AGENTS.md does not exist` — inverts, as described.
- line 38 `tools/app/CLAUDE.local.md does not exist` — **also breaks, and the design
  misses it.** It is not "the other agent's file"; it asserts that a Codex-default
  workspace writes *no* repo-level content. After the change the Claude pass writes
  `CLAUDE.local.md` in every repository regardless of `default_agent`, so this line
  must change too.
- line 39 `tools/app/AGENTS.md does not exist` — **survives**, since niwa writes
  `AGENTS.override.md`, not `AGENTS.md`. The design implies all the negatives invert;
  this one must not.

Also unlisted: the Feature description (lines 2-7) narrates the exclusive model
verbatim and the `Design:` line 8 points at the superseded design.

Unit scope:

- `internal/workspace/root_materializer_test.go:151-190`
  (`TestMaterializeWorkspaceRoot_AgentFilename`) — correctly identified; the
  `absentFile` assertion at line 179 inverts.
- `internal/workspace/content_test.go:107-156` (`TestContentFilenameByAgent`) —
  correctly identified; `assertNotExist(otherRootFile(...))` at line 133 inverts.
- `internal/workspace/content_test.go:158-185`
  (`TestRepoContentSkippedUnderCodex`) — **fate undefined.** It survives untouched
  if `WritesRepoLevelContext()` keeps returning false for Codex (leaving a
  permanently-dead accessor branch and making "no accessor needs to change meaning"
  true only by accident), and must be deleted if the accessor is retired. R2's
  criterion enumerates permitted modifications, so the design has to pick.
- `internal/cli/instance_from_hook.go:499` hardcodes `applier.Agent =
  agent.AgentClaude`; `internal/cli/session_lifecycle_cmd.go:337` sets it from the
  resolved agent. Both become inert under the new model. Same category as the
  dispatch comments the design does flag, and unmentioned.

Over-claimed nothing else; the dispatch-refusal "needs no logic change" reading is
correct (`internal/cli/dispatch.go:236`).

## Internal coherence

The seven decisions mostly compose. Three frictions:

1. **Composition rule vs. Decision 7A** — covered above: the rule demands composed
   output at the group and repository levels, which the "run the existing writers
   once per agent" cut cannot produce. The two sections describe different amounts
   of work.

2. **Collision guard vs. Security Considerations** — Decision 7 retires the guard
   because niwa "never writes to that filename", reasoning only about `AGENTS.md`;
   Security Considerations then reasons at length about repositories that commit
   their own `.codex/`. Both cannot be true for the `.codex` write.

3. **Byte budget vs. the composition rule, ordering.** These are consistent as
   described — one counter, drained outermost-first, so the composed override's tail
   is what truncation eats, which is exactly the innermost layer R7 protects, and the
   generous budget declaration is the stated defense. But the design never notices
   that layer *ordering inside the composed file* is a free second defense: putting
   the repository layer first would make truncation degrade gracefully instead of
   destroying precisely the protected layer. It commits to the risky ordering
   without acknowledging the choice. Related: the budget is computed once per
   instance at apply time from what is on disk then, while committed context files
   in repository subdirectories can grow after the apply with no signal — the
   document's "generous headroom" is doing more work than it admits.

## Load-bearing assumptions

The document is unusually good about this — it names the codex-cli 0.147.0 pins
(`AGENTS.override.md` precedence, the bare-stat marker check that admits a
worktree's `.git` file, symlink-following in the project loader) as a limitation
with a drift-detection mitigation. What it asserts without establishing:

- **Claude Code's plugin-cache layout** becomes a second external invariant (see R8
  above), and is not listed alongside the codex-cli pins.
- **`claude plugin install` having succeeded** is a precondition for the skills
  symlinks resolving, and it is documented in the code as best-effort and
  skippable. "A dangling payload symlink is harmless in the interim — apply repairs
  it" is asserted; it is not true when the reason for the dangle is a missing
  `claude` binary or a disabled auto-install, in which case apply repairs nothing,
  forever, silently.
- **`AGENTS.override.md` is "a name repositories do not commit."** Plausible, and
  weaker than the `.codex` problem, but it is an assertion about third-party
  behavior offered as the reason no guard is needed.
- **Trust-key path normalization.** The PRD criterion says a "present-but-miskeyed
  entry fails", and the design's own worktree analysis shows Codex does non-trivial
  path resolution (following a `.git` file to the main repository root). The design
  never states what form the `[projects."<path>"]` key must take — canonicalized
  (`EvalSymlinks`) or as-configured. An instance reached through a symlinked parent
  (a very common `/tmp`, `/home` → autofs, or symlinked-workspace setup) would get a
  miskeyed entry, and the failure is the silent read-only sandbox the design calls
  the worst shape.
- **`.codex` reachable from inside every repository** means any tree-walking tool
  that follows symlinks (search, indexing, an agent's own grep) now sees the whole
  plugin payload from inside each repository. Noted only as a security exposure, not
  as a functional consequence.

## Required changes

1. **Specify how the plugin install root is resolved, for both marketplace kinds,
   and what happens when it does not exist.** Cover `repo:`-sourced marketplaces
   (niwa resolves the marketplace directory today but not the per-plugin
   subdirectory — say who parses `marketplace.json`) and github-sourced ones (the
   root lives in Claude Code's user-global plugin cache, populated by a best-effort
   `claude plugin install` that can be skipped or fail). State the behavior when the
   root is missing at apply time: given the design's own hostility-to-silent-failure
   driver, "dangling symlink, apply repairs it" is not sufficient when apply cannot
   repair it. Add the Claude-Code-plugin-cache layout to the load-bearing-invariants
   limitation next to the codex-cli pins.

2. **Handle a repository that already carries `.codex` at its root.** Decision 7's
   collision-guard retirement reasons only about `AGENTS.md` and is contradicted by
   the design's own security section. Specify detection and the chosen behavior
   (skip that repository with a report, error the apply, or something else), and say
   what it costs the feature in that repository. Same treatment, briefly, for a
   committed `AGENTS.override.md`.

3. **Fix the worktree composition to match R4.** A worktree's `AGENTS.override.md`
   must carry the repository content layer as well as the worktree framing — that is
   what `ApplyToWorktree` gives a Claude session
   (`internal/workspace/worktree_content.go:512` then `:641`). State whether a
   worktree's committed `AGENTS.md` is inlined the way a clone's is.

4. **Correct the blast radius and resolve `WritesRepoLevelContext()`.** Two
   scenarios affected, not three; three assertions change, not two — including
   `tools/app/CLAUDE.local.md does not exist` (feature line 38), which is a
   repo-level-skip assertion rather than an other-agent-file assertion; and
   `tools/app/AGENTS.md does not exist` (line 39) must *not* be inverted. Say
   explicitly whether `WritesRepoLevelContext()` is retired (deleting
   `TestRepoContentSkippedUnderCodex`) or kept, since R2's criterion enumerates the
   permitted test edits. Add the Feature description prose and `Design:` line, and
   the now-inert `internal/cli/instance_from_hook.go:499` /
   `internal/cli/session_lifecycle_cmd.go:337` assignments.

5. **Restate Decision 7A's seam accurately.** "The exclusivity lives in the callers,
   not the accessors" holds at the workspace-root and instance levels only. At the
   group level composition is net-new (`InstallGroupContent` writes one source); at
   the repository/worktree level the exclusivity is in an accessor
   (`internal/agent/agent.go:85`) and the Codex writer is net-new. The batch
   sequencing rationale ("the per-agent caller seam is what the Codex materializer
   plugs into") should be re-derived from the corrected picture.

6. **State the trust-key path form.** Say whether `[projects."<path>"]` keys are
   canonicalized and against what, so a symlinked instance path cannot produce a
   silently miskeyed entry.

## Optional improvements

- Order the composed override innermost-layer-first so budget truncation degrades
  gracefully, and say so — it costs nothing and makes the budget declaration a
  second line of defense rather than the only one.
- Say what happens to `--agent` on `niwa create` / `niwa apply`. Its help text
  ("select the coding agent to prepare the workspace for", `internal/cli/apply.go:33`,
  `internal/cli/create.go:27`) becomes false under R1, and the design's "survives only
  where launch-time selection is real" does not cover a flag on a non-launching
  command.
- Note in "What stays Claude-only" that the embedded workspace-root project skills
  (`writeRootSkills`, `internal/workspace/root_materializer.go:189`, e.g. `dispatch`)
  get no Codex analogue, so R8's "same set of skills" is scoped to plugin-delivered
  skills inside instances.
- Add the search/indexing consequence of `.codex` being reachable from every
  repository to the Negative section — an agent grepping a repository now sees the
  whole plugin payload — alongside the security framing it already has.
- The budget sizing is computed at apply time but the inputs (committed context files
  in repository subdirectories) can grow between applies. One sentence acknowledging
  that window would match the honesty of the stale-inline limitation next to it.
