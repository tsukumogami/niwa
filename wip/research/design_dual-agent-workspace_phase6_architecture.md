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

# Round 2

# Verdict: FAIL

All six round-one items are closed with the property intact, not just the wording,
and the re-derived batch sequencing earns its conclusion. The failure is narrow and
entirely in the new material: the conflict rule contradicts itself on scope, and one
of its two refusals is over-broad in the direction that costs both delivery and
safety.

## Round-one items — all six closed

**1. Plugin root resolution (was the deepest hole).** Decision 3 now owns it as part
of the decision rather than assuming it (lines 305-327). It states the actual
constraint — niwa's plugin model is name-based, resolving an on-disk directory only
for repository-sourced marketplaces and even then only the marketplace root — and
splits resolution by marketplace kind: parse the manifest niwa already opens for the
name and join the plugin's declared source directory for repository-sourced ones, the
Claude Code user-global cache for github-sourced ones. Crucially it does not hope the
root away: a missing root at apply time is handled under D4 with a per-plugin loud
report naming the plugin and the expected path, explicitly rejecting the silent
dangling symlink, and explicitly because "where a Claude session self-heals by
installing at startup, a Codex session has no equivalent" — the asymmetry I raised.
The second external layout is now on the dependency list in Consequences (lines
1025-1038) beside the codex-cli pins. Property intact.

**2. `.codex` collision.** Closed and generalized beyond what I asked. The
blind-overwrite guard stays retired *for `AGENTS.md`* with the correct reason (niwa
never writes that name), and a conflict rule now covers the two names niwa does
write (lines 613-638), with the honest framing that "a name repositories do not
commit" is an assumption about third-party behavior. The git-exclude bullet now
states the untracked-only limit and points at the conflict rule for tracked names
(lines 607-612) — that is the connection I did not ask for and it is the right one.
See the regressions below for what the rule does not settle.

**3. Worktree composition.** Fixed correctly and against the code, not by adding a
word: lines 695-701 and 721-724 now carry instance, group, the repository layer, and
the framing appended last, and the parenthetical describes the real merge order
(`internal/workspace/worktree_content.go:512` then `:641`). The worktree's committed
`AGENTS.md` is now explicitly inlined under the same regular-file rule.

**4. Blast radius.** Correct in every particular I verified: two of three scenarios
affected with the dispatch scenario standing, three assertions changing, and
`tools/app/AGENTS.md does not exist` explicitly called out as the one that must not
be inverted with the reason attached (lines 554-567). The feature description prose
and `Design:` pointer are included. `WritesRepoLevelContext()` is answered
explicitly — **retired** (lines 530-536) — with the repo-level-skip test deleted
alongside it, and the design says why it is stating the complete set. Decidable now.
One residual for whoever writes the plan, not a blocker: that test asserts the
repo-level skip rather than "the other agent's file is absent", so deleting it fits
R2's criterion only on a file-scoped reading of it. The design takes that reading
openly, which is the right way to carry it.

**5. The re-derived seam and sequencing — judged fresh, it holds.** Decision 7A is
now level-by-level (lines 500-523) and each level's claim matches the code: pure
filename selector at root/instance, one-source writer at group, accessor-gated no-op
at repository/worktree. The re-derivation changed a batch boundary rather than a
sentence — group composition moved out of batch 1 into batch 2 with the reason
stated ("new composition sharing this batch's composer, not a re-run of the existing
group writer"), and batch 1 was retitled to what it actually is. That is the tell
that the premise was rebuilt rather than patched. The gating claim is re-earned on
new grounds and both grounds are real: batch 1 establishes the per-agent pass batch
2's writers ride, and batch 1 alone is what flips `tools/app/CLAUDE.local.md does
not exist` (the Claude pass becoming unconditional causes that, independent of any
Codex write). The design is also candid that this is not reuse. The dependency is
softer than "gates" implies — a net-new Codex materializer could be invoked from the
pipeline without batch 1 — but the ordering is right for the stated reason
(protecting the invariant first), and batch 3's new dependency on batch 2's conflict
verdicts is correctly identified.

**6. Trust-key canonicalization.** Closed at lines 362-373 with full component-wise
symlink resolution, the concrete failure shape, and the PRD criterion named as the
regression check.

## Regressions from the security batch

**A. The conflict rule contradicts itself on scope, and one reading reopens R7.**
Decision 7 states it per-name — "Before writing either name, niwa checks the target
path... niwa writes nothing at that name" (lines 621-625) — and the architecture
section agrees ("Anything already at `.codex` or `AGENTS.override.md`... the write is
skipped", lines 745-747). But Consequences states it repository-wide: a conflict at
either name "means that repository gets no payload link, no composed override, and
for a `.codex` conflict no trust entry" (lines 1000-1004). These are different
designs, and the difference is load-bearing for the coherence question:

- *Repository-wide reading:* a conflicted repository gets nothing from niwa, falls
  back to native discovery, and is reported. Coherent, and the Consequences entry
  describes it accurately.
- *Per-name reading:* a `.codex` conflict skips only the link. The composed
  `AGENTS.override.md` is still written, and Decision 2 established that the override
  name is probed with no trust dependency — so it loads. But the budget is declared
  in the payload's `config.toml`, which is reached only through the `.codex` link
  that was just skipped. That repository therefore runs the composed chain under the
  default 32768 with no declaration, and the design's own byte-budget section says
  truncation eats the tail, which under the chosen outermost-first ordering is the
  repository layer R7 protects. The conflict is reported; the truncation is not. That
  is a silent minority-case failure reached through the rule written to prevent
  silent minority-case failures, and it is the one thing the conflict path does *not*
  degrade loudly about.

The rule must pick one scope and say so in all three places. If per-name, the budget
consequence has to be stated and handled (the honest options are to skip the override
too when the payload link is skipped, or to accept the default budget and say what
that costs).

**B. The regular-file refusal is over-broad, and R6 is cited backwards for it.**
Decision 2's new rule is right that the composer must lstat and refuse to read a
non-regular `AGENTS.md`; the refusal-over-resolution argument (no chained links, no
post-resolution traversal, no check-to-read window) is sound and I have no quarrel
with it. What does not follow is the second half: "niwa reads nothing, inlines
nothing, **writes no override for that repository** — so nothing displaces the
repository's own file in discovery (R6)" (lines 252-256).

R6 requires the session to receive *both* the workspace's context and the
repository's own content. Under the refusal the session receives only the
repository's own file — which is the symlink, which Codex will follow natively on
its own read — and none of the workspace's. So the branch is justified by preserving
R6 while failing R6's workspace half outright, and it leaves the symlink in the
discovery slot rather than out of it. Composing the override from the instance,
group, and repository layers *without* the inline requires reading nothing from the
repository at all, so it costs the refusal nothing: it delivers the workspace layers
(R6's first half, which is recoverable), and because `AGENTS.override.md` wins
first-match it additionally displaces the symlinked `AGENTS.md` from discovery
instead of leaving it as the only thing the session reads. The chosen branch is the
strictly worse one on both axes the design cares about. Either take the composed-
without-inline branch, or state plainly why leaving the symlink as the session's
sole context source is preferable — the current text does not argue that, it asserts
the opposite outcome.

**C. Ownership recognition has no backing on the standalone worktree path.** The
rule recognizes niwa's own writes "the `.codex` symlink by its target... and copied
or composed files through the tracked-content records materialization already keeps"
(lines 628-631). For `.codex` the target test works everywhere. For a worktree's
`AGENTS.override.md` it needs a record, and the record exists on only one of the two
paths that writes it: the instance-apply path hashes `ApplyToWorktree`'s returned
files into managed entries (`internal/workspace/apply.go:1976-2000`), while the
standalone `niwa worktree apply` path calls `ApplyToWorktree` directly and returns
the list without persisting it (`internal/cli/session_lifecycle_cmd.go:371`). On
that path the first re-apply meets its own prior override with no ownership record
and, under the rule as written, must treat it as foreign and refuse to refresh it —
which is R3's worktree-refresh criterion ("the same holds for a worktree after `niwa
worktree apply`") failing by way of the conflict rule. Say what backs the check
there. A content-derived test (niwa's own composed-file marker) or extending the
worktree path's record-keeping would both do; leaving it to "records materialization
already keeps" would not.

## Required changes

1. Settle the conflict rule's scope — per-name or repository-wide — consistently
   across Decision 7 (lines 621-625), the Conflicts section (745-752), and
   Consequences (1000-1004). If per-name, state and handle what a skipped `.codex`
   link does to the budget declaration in that repository, since the composed
   override still loads there and truncation of the R7 layer is silent.

2. Narrow the non-regular-file refusal to the inline, or argue the wider refusal on
   its own terms. As written it withholds the workspace layers — which need no
   repository read — and cites R6 for an outcome that leaves the session with the
   symlink as its only context.

3. State what backs ownership recognition for a worktree's `AGENTS.override.md` on
   the standalone `niwa worktree apply` path, which persists no managed-file record
   today.

## Optional improvements

- Say where the trust writer's lock and same-directory temp file live. Decision 4
  says "nothing else" is written into the developer's Codex config and the write is
  bounded to `[projects.*]` keys; a lockfile and a rename staging file in
  `~/.codex/` are new artifacts outside the instance that the claim does not cover.
  One sentence (either "in niwa's own state directory" or an explicit carve-out)
  keeps the claim exact.
- Reconcile two sentences about the missing plugin root: Decision 3 skips the
  symlink entirely, while the apply-refresh section calls it "a skills link whose
  plugin root never materialized" and "the one dangle apply cannot repair" (lines
  800-805). Under Decision 3 there is no link and so no dangle; the reporting
  behavior agrees, only the noun does not.
- Say whether an unparseable pre-existing Codex config fails the apply or warns.
  "Refuses the trust step... and reports" reads as non-fatal, and the consequence —
  every repository in the instance silently read-only at session time — is severe
  enough that the choice should be explicit rather than inferred.

# Round 3

# Verdict: FAIL

The three round-two findings landed with their properties, the per-name/repository-wide
split is genuinely consistent across all three places, and both optionals are closed
well. The failure is the thing I was asked to look for: the generation-marker rule
does create a new problem on the clone path, and the trust-removal rule added
alongside it has no way to recognize what it is removing.

## Round-two items — closed

**Finding 1, conflict scope.** Settled as per-name detection with one stated one-way
coupling, and I checked all three sites say the same thing rather than sounding
compatible: Decision 7 (lines 651-665), the Conflicts section (805-815), and
Consequences (the "in proportion to what it occupies" bullet) each state the same
three cases with the same outcomes — `.codex` costs the repository everything,
`AGENTS.override.md` costs only the override with link/excludes/trust still
materializing, non-regular `AGENTS.md` costs only the inline. No drift between them.

The coupling argument holds on its merits, and it is the right shape: the override's
budget declaration lives in the payload the refused link would have reached, so an
override written into a `.codex`-conflicted repository runs under Codex's 32768
default and truncates the tail, which under the design's own outermost-first ordering
is the repository layer R7 protects. Suppressing the override there is not
belt-and-braces caution, it is the only way to avoid converting a loudly-reported
conflict into a silently truncated one. The asymmetry is also correctly reasoned: an
`AGENTS.override.md` conflict does not touch the payload, so the budget declaration,
the skills, and trust all remain sound, and there is no reason to withhold them.
Turning the trap into the stated reason for the rule is the right outcome.

**Finding 2, over-broad refusal.** Narrowed exactly as intended (lines 256-269), and
improved beyond the narrowing: the enforcement moved from a check-then-read to a
single `O_NOFOLLOW` open, which removes the TOCTOU window a separate lstat would
have left. That is a better answer than the one I would have accepted.

**Finding 3, worktree ownership.** The mechanism is right and the justification is
correct as far as it goes. A content test is the only ownership signal available on
the standalone `niwa worktree apply` path — I verified that path calls
`ApplyToWorktree` and returns the written list without persisting it
(`internal/cli/session_lifecycle_cmd.go:371`), while the instance-apply path hashes
the same list into managed entries (`internal/workspace/apply.go:1976-2000`). So a
record-based check really would make the standalone path refuse its own refresh and
fail R3's worktree criterion. The "tracked is a conflict regardless of content" half
is the right default, and the forgery carve-out is honest about why it does not
matter. What the justification misses is that adopting a content test for the *write*
decision does not retire the record path's power over the *delete* decision — see A.

**Optionals.** All three closed: the lock now lives in niwa's own state directory
keyed by config path, the same-filesystem staging file is carved out of the "nothing
else" claim explicitly and bounded to the instant of the write, an unparseable config
is an error that makes apply exit non-zero with the reason given (an instance that
"looks prepared while every repository in it silently runs read-only"), and the
missing-plugin-root noun is reconciled — there is no link, so there is no dangle.

## New findings

**A. The generation-marker rule collides with `cleanRemovedFiles` on the clone path,
and the collision deletes the conflicting file.** The conflict rule promises twice
that niwa "modifies and deletes nothing (R12, R11)" (lines 646-647, 815-816). On the
clone path that promise is not niwa's to make, because an existing pipeline step
already deletes by record: `cleanRemovedFiles` removes every path in
`existingState.ManagedFiles` that the current apply did not produce, unconditionally
(`internal/workspace/apply.go:1846-1859`, the bare `os.Remove` at :1854).

A conflict makes niwa write nothing at that name, so the path drops out of
`result.managedFiles`. The reachable sequence is the one the rule exists for: a clean
repository gets `AGENTS.override.md` written and recorded; later a committed
`AGENTS.override.md` arrives, or a committed `.codex` arrives and the coupling
suppresses the override write; the next apply detects the conflict, reports it, writes
nothing — and then `cleanRemovedFiles` deletes the file at that path. If what is now
there is tracked, that is a deletion in `git status` (R11) and a repository-shipped
file removed (R12) — the two requirements the rule cites while promising the
opposite. The design now carries two ownership notions on the clone path, content for
the write decision and records for the delete decision, with nothing reconciling
them, and the record-based one is the destructive one.

The fix has precedent in the codebase, which is also evidence the hazard is real: the
worktree-refresh path already forward-carries prior managed entries verbatim
specifically "so the next cleanRemovedFiles does not delete its live secret file"
(`internal/workspace/apply.go:1900-1906`). Conflicted paths need the same treatment —
forward-carried or explicitly excluded from reconciliation — and the design has to say
so, because the rule as written reads like a guarantee that the pipeline will not
honor.

**B. The trust-removal rule cannot recognize what it removes.** The new
remove-on-later-conflict behavior (lines 681-695) is well motivated — withholding
alone would leave a stale entry vouching for a `.codex` a repository acquired after
its entry was written, reopening the impersonation path on an already-trusted
repository. But the recognition claim does not transfer: "This removal touches only
entries niwa itself wrote (its per-repository keys, recognized the same way its file
writes are)". File writes are recognized by a generation marker in the content and by
untracked status. A TOML table has neither, and Codex writes a
`[projects."<path>"] trust_level` entry of exactly the same shape when the developer
answers the startup trust prompt — which is precisely the prompt this design routes
conflicted repositories to. So niwa cannot distinguish its own entry from the
developer's own answer by shape, and removing the latter breaks both the design's own
additivity claim ("existing keys are never removed, reordered, or altered", Security
Considerations) and the PRD criterion that no pre-existing key is removed.

Records are the right answer here and, unlike the worktree file case, they are
available: trust entries are written only by the instance-apply path, which has
per-instance state. Recording the keys niwa wrote and bounding removal to that
record closes it. The design should say that rather than pointing at the file rule,
whose mechanism does not exist for TOML keys.

**C. The displacement claim holds, but only for one directory, and is stated
unbounded.** The claim (lines 264-269) is that the written override "displaces the
symlinked `AGENTS.md` from the discovery slot — without it, Codex's own native read
would follow the symlink". Checked against the design's own model of Codex: true, and
a real benefit — `AGENTS.override.md` wins first-match, so the root `AGENTS.md` is
never read. But the slot is per directory, and niwa writes the override at the
repository root only (the on-disk shape). The walk contributes one file per directory
from root to cwd, and the design relies elsewhere on subdirectory context files being
read — the budget is sized "plus any committed context files in subdirectories below
a repository root", and the PRD's own criterion exercises a context file in an
intermediate directory. So a symlinked `AGENTS.md` one directory down is read
natively and undisplaced. The degenerate case bounds it further: with no workspace
content at any layer, the never-empty rule writes no override, so nothing displaces
even the root file.

This does not unmake the narrowing — the `O_NOFOLLOW` refusal is the defense and it
holds unconditionally, and preserving R6's workspace half justifies writing the
override on its own. But the text elevates displacement from a welcome side effect to
"itself part of the defense", and a reader will take that as "niwa's write closes the
disclosure path". It closes it at one directory, when there is workspace content to
compose. Bound the claim to what it covers.

## Required changes

1. Reconcile the conflict rule with `cleanRemovedFiles`. A path that becomes a
   conflict drops out of the produced set and is currently deleted by record
   (`internal/workspace/apply.go:1854`), contradicting the rule's "deletes nothing
   (R12, R11)". State that conflicted paths are forward-carried or excluded from
   managed-file reconciliation, following the idiom the worktree-refresh path already
   uses for the same hazard (`apply.go:1900-1906`).

2. Give trust removal a real recognition mechanism. Shape is not identity — Codex
   writes the same key when the developer answers the prompt this design sends
   conflicted repositories to. Bound removal to keys niwa recorded writing (instance
   state is available on the only path that writes them), rather than to keys
   "recognized the same way its file writes are", which has no analogue for a TOML
   table.

3. Bound the displacement claim to the repository root directory and to the case
   where workspace content exists to compose. As written it reads as a
   repository-wide closure of the disclosure path; it is a per-directory,
   content-conditional one, and the `O_NOFOLLOW` refusal is the part that holds
   unconditionally.

## Optional improvements

- The generation marker lands inside agent-visible context and counts against the
  byte budget. Both are trivial, but one clause saying the marker is part of the
  composed document (rather than a sidecar or an xattr) would keep the budget-sizing
  and never-empty rules unambiguous about what they are measuring.

# Round 4

# Verdict: FAIL

Findings 2 and 3 are closed cleanly and the marker optional is handled well.
Finding 1's *conclusion* is right and its *mechanism* is stated backwards: the
sentence an implementer would follow describes the operation that causes the
deletion, and it cites an idiom that does the opposite of what the text says. One
new failure mode falls out of the record-based trust removal.

## What closed

**Finding 3, displacement bounds.** Fully closed, in the stronger form. Lines
262-280 now state the split explicitly — `O_NOFOLLOW` holds unconditionally, the
displacement is a per-directory, content-conditional reinforcement — and spell out
both bounds with their reasons (a subdirectory symlink occupies its own directory's
slot, which nothing niwa writes contests; the never-empty rule writes no override
when no layer has content). "Itself part of the defense" is gone, replaced by
"reinforces the defense where it applies". Security's final-path-component note folds
in without muddying it.

**The generation-marker optional.** Handled coherently (lines 700-706): the marker is
a line of the composed document, not a sidecar or an attribute, and the text draws
the consequence for both rules it touches — it is agent-visible, it counts against
the byte budget, and the never-empty rule is unambiguous because no content still
means no file rather than a marker-only one. That is more than I asked for and it is
the right resolution.

**Finding 2, trust-removal recognition — the mechanism.** Closed. Removal is bounded
by record, not shape (lines 725-738); the record lives in instance state; the
availability argument is correct (the instance-apply path is the only writer); and
the reasoning is stated in full, including that Codex writes an identically-shaped
entry when the developer answers the very prompt this design routes conflicted
repositories to. The asymmetry with the file-side marker test is explained rather
than papered over. The Security additivity bullet carries the qualifier (line 1017).
See B for what the record itself introduces.

## A. The reconciliation exemption says the thing that causes the deletion

The diagnosis in the new paragraph (lines 665-679) is exactly right: reconciliation
deletes by record, a conflicted path drops out of the produced set, and both arrival
sequences are named correctly. The conclusion — no delete — is right. The mechanism
sentence is not:

> a conflicted path is retired from the managed-file record *without* the deletion
> that retirement normally performs — the record entry is dropped, the path untouched
> — following the idiom the worktree-refresh path already uses to shield a live file
> from the same cleanup.

Two problems, and they compound.

*"The record entry is dropped" is the delete trigger, not the exemption.*
`cleanRemovedFiles` iterates `existingState.ManagedFiles` — the state loaded from the
previous apply — and removes every path absent from `result.managedFiles`, the
produced set (`internal/workspace/apply.go:1846-1858`). Dropping the entry from the
produced set is precisely the condition that fires `os.Remove` at :1854. An
implementer who follows this sentence literally writes the bug the paragraph exists
to prevent, and it will look correct in review because the prose says "the path
untouched" right next to it.

*The cited idiom does the opposite.* The worktree-refresh path shields a live file by
**keeping** its prior entries in the produced set: `forwardCarry` returns the prior
`ManagedFile` entries and the caller appends them to `out`
(`internal/workspace/apply.go:1967-1970`, `2028-2036`), so `currentFiles[mf.Path]` is
true and the cleanup skips the path. That is retain-the-record. The design describes
drop-the-record and attributes it to that idiom. Both cannot be true.

This matters because the two viable implementations genuinely differ and the design
lands between them:

- *Forward-carry* (the cited idiom): keep the entry in the produced set. Works with
  no change to `cleanRemovedFiles`, but niwa goes on recording ownership of a path it
  just declared foreign, which is semantically wrong and will confuse the next reader
  of the state file.
- *Drop plus an explicit exemption:* omit the entry and have the reconciliation
  consult a per-apply conflicted-path set before removing. This is the better
  design — the path is genuinely disowned, and if the conflict later clears the marker
  test recognizes a fresh write — but it requires a change to the cleanup that the
  design does not mention, and without that change it is simply the bug.

The Conflicts-section echo is closer, using the right word: conflicted paths are
"exempted" from record-driven cleanup (lines 858-861). But neither passage says what
the exemption is *against*, and the cleanup reads prior state rather than the produced
set, so "exempt" has to name a mechanism to be actionable. Pick one of the two shapes,
say it in terms of the produced set or an exemption the cleanup consults, and either
drop the idiom citation or describe the idiom as it actually works.

## B. Record-based trust removal needs the record cleared, and the "every apply"
guarantee restated

The record closes the shape problem, and I have no quarrel with the mechanism. It
introduces one new failure mode, and it sits exactly where the design's two new
sentences meet:

> a `.codex` conflict, whenever it is detected, means no niwa-written trust entry for
> that repository exists afterward

> removes only keys that record names

The trust namespace is one key per path, shared by two writers. The sequence the
design itself sets up runs: apply writes the entry and records it → the repository
acquires a `.codex` → the next apply detects the conflict and removes the entry → the
developer starts `codex` there, meets the prompt this design deliberately routes them
to, answers yes, and **Codex writes the same key back**. On the apply after that, the
two sentences disagree. If removal cleared the record, there is nothing to remove and
the developer's answer survives — correct, but then the guarantee is not "whenever it
is detected"; it is "niwa never re-adds it while conflicted", which is the honest and
sufficient claim. If removal left the record in place, the next apply removes the
developer's answer at that key — the exact outcome the record test was introduced to
prevent, arriving by a different route.

The design does not say which. State that removal clears the record, and restate the
guarantee as never-re-added rather than always-absent-after-apply.

Two smaller consequences of the record now being load-bearing, worth a clause each:

- *The record dies with the instance.* Instance state lives in the instance
  directory, so destroying an instance destroys the only authority for removing the
  trust keys it wrote. The Security section already books removal-on-destruction as
  planned lifecycle work; that work now has a hard ordering constraint — read the
  record before the instance goes — and it is worth saying so, because the natural
  implementation order is the wrong one.
- *Record-file disagreement is safe in one direction only, and that is worth
  stating.* A recorded key already absent from the file is a harmless no-op; an
  unrecorded key present in the file is correctly left alone. Saying this makes the
  conservative direction of the failure explicit rather than something a reader has
  to derive.

## Required changes

1. Restate the reconciliation exemption in terms an implementer can follow without
   reproducing the bug: name whether a conflicted path is forward-carried into the
   produced set (the idiom as it actually works,
   `internal/workspace/apply.go:1967-1970`, `2028-2036`) or dropped with an explicit
   exemption the cleanup consults. As written, "the record entry is dropped" is the
   condition that fires `os.Remove` at `apply.go:1854`, and the idiom cited does the
   opposite of what the sentence describes.

2. State that removing a trust key also clears its record entry, and restate the
   `.codex`-conflict guarantee as "niwa never re-adds the entry while the conflict
   stands" rather than "no niwa-written entry exists after any apply" — otherwise the
   two rules delete the developer's own answer to the prompt this design sends them
   to.

## Optional improvements

- One clause in the Security section noting that removal-on-instance-destruction must
  read the trust-key record before the instance directory is removed, now that the
  record is the sole authority for what may be removed.
- One clause making the safe direction of record-file disagreement explicit: a
  recorded-but-absent key is a no-op, an unrecorded-but-present key is left alone.

# Round 5

# Verdict: FAIL

One sentence from PASS, and the first thing to say is a process fact: **the document
on disk does not match the briefing.** It has moved again since. Finding 2 and both
optionals are fully closed; finding 1's mechanism is now stated correctly, but the
option it lands on is the one it argues against in the briefing, and it carries one
consequence the design asserts it does not have.

## The document changed mid-review

The briefing describes drop-plus-exemption ("the apply hands its conflict verdicts to
the managed-file cleanup, which skips every path the current apply declared
conflicted"). I read that text on disk at the start of this round. Re-reading the same
region minutes later, it had been replaced: the file is now 1230 lines (briefing said
1225), and both sites — Decision 7 (lines 673-695) and the Conflicts section (line
888) — now say **forward-carry**, consistently, with drop-plus-exemption named as the
reading that "gets the machinery backwards".

So the author appears to have reversed again after the briefing was written. I am
adjudicating what is on disk. If the intent was drop-plus-exemption, the current text
is not it, and someone should decide which one is meant before the plan is written —
the two produce different state files and different `niwa status` output.

## Finding 1, as it now stands: mechanism correct, one consequence mis-stated

The mechanism is now precise and correct against the code. "Absence from the produced
set is the deletion trigger, not an exemption from it" is exactly right
(`internal/workspace/apply.go:1852-1858`), forward-carry is described as it actually
works — retain the prior entries in the produced set so the cleanup sees the path as
still produced — and it matches `forwardCarry` and its caller
(`apply.go:1967-1970`, `2028-2036`). An implementer following this paragraph produces
the right behavior, and the round-four error is corrected rather than papered over.
The choice itself is defensible: it needs no cleanup change, and it is the idiom the
codebase already uses for the same shape of problem.

What is wrong is one claim inside the honesty paragraph. The design says the stale
carried hash is tolerable because "the record is not the ownership authority" and
"the carried record entry's only job is to keep the cleanup's hands off the path."
The record has two other jobs, both user-facing, and both consume `ContentHash`:

- Every `niwa apply` drift-checks each recorded managed file before the pipeline runs
  and warns `managed file <path> has been modified outside niwa` when the hash
  disagrees (`apply.go:597-607` via `CheckDrift`, `state.go:658-676`).
- `niwa status` runs the same check and reports the path as a drifted managed file
  (`internal/workspace/status.go:96`).

A forward-carried entry keeps the hash of niwa's *old* override while a foreign file
sits at the path. So a conflicted repository emits two warnings per apply — the
conflict report and a drift report — and `niwa status` lists the repository's own
committed file, in perpetuity, as a niwa-managed file that has been modified outside
niwa. That is niwa asserting ownership of a path its own conflict rule just declared
foreign, on the surface a developer consults to answer exactly that question. Not
destructive, and not silent — but it is a statement the design elsewhere goes out of
its way not to make, and it is precisely the "left to be discovered" item the
paragraph claims to have eliminated.

This is the answer to the question you asked, inverted by the flip: drop-plus-exemption
would not produce it (no record, no drift check, no status entry), and forward-carry
does. It is a sentence to fix, not a redesign.

## Finding 2 and the optionals: closed

**Trust removal.** Both gaps closed in one passage (lines 729-748), and closed well.
The guarantee is restated exactly as needed — "removes the entry its record names,
clears the record entry with the removal, and never re-adds an entry while the
conflict stands... The guarantee is never-reinstated, not always-absent" — and the
composition argument is spelled out, including that the developer's later answer to
Codex's prompt "is theirs, sits in no niwa record, and later applies leave it alone",
and that a record left in place "would license the next apply to delete the
developer's answer at that key". The old always-absent phrasing greps to zero. The
record-not-shape bound and the reason the two recognition mechanisms differ are
intact.

**Both optionals.** The destruction ordering constraint is in (lines 1058-1062: the
record is the sole removal authority, so destruction must read it before the instance
directory goes). The safe direction of record-file disagreement is stated in the same
passage as finding 2 — recorded-but-absent is a no-op, unrecorded-but-present is left
alone.

## Required changes

1. Correct the claim that the carried record entry's "only job is to keep the
   cleanup's hands off the path". It also drives the apply-time drift warning
   (`internal/workspace/apply.go:597-607`) and `niwa status`
   (`internal/workspace/status.go:96`), both keyed on `ContentHash`, so a
   forward-carried entry makes both surfaces report a conflicted repository's own
   committed file as a drifted niwa-managed file on every apply, forever. Either
   carry the entry with the current on-disk hash so the record describes what is
   actually there and the drift check stays quiet, or state that conflicted paths are
   excluded from the drift report so the conflict warning is the single account.
   Whichever is chosen, the "only job" sentence has to go.

2. Reconcile the document with the briefing. The text on disk is forward-carry; the
   briefing describes drop-plus-exemption. Both are defensible and I would accept
   either with change 1 applied — but the design should say which one it means, once.

## Optional improvements

- None.

# Round 5 — final verdict (supersedes the round-5 section above)

# Verdict: PASS

The section above was written against the forward-carry text that was on disk when I
read it. That text has been reverted to drop-plus-exemption. It is kept as the record
of what was judged when; this section is the ruling on the current file
(1232 lines, md5 `29ca443491c13fa46ca305b8bd6bd4d6`).

## Finding 1 — closed, and my caveat is genuinely satisfied

I set the condition, so I tested it rather than reading for reassurance. Three things
had to be true, and all three are.

*The cleanup change is named as a change.* Lines 674-678: "the mechanism is **an
explicit exemption, which requires a named change to the cleanup**: the apply hands
its per-run conflict verdicts to the managed-file cleanup as an input, and the cleanup
consults that conflicted-path set and skips those paths before removing recorded paths
the apply did not produce."

*It is concrete enough to build without further discovery.* It names the data (the
per-run conflict verdicts, as a conflicted-path set), the direction (apply into
cleanup, as an input), and the semantics (skip those paths before removing recorded
paths not produced). That maps one-to-one onto the real function: `cleanRemovedFiles`
takes `(existingState, result)` and removes every prior path absent from the produced
set (`internal/workspace/apply.go:1846-1858`), so the change is one input plus one
skip before the `os.Remove` at :1854. An implementer reading this builds the right
thing.

*The half-implementation is called out by name.* Lines 678-684: "The cleanup change is
not optional decoration — today the cleanup tests only membership in the produced set,
so absence from that set is the deletion trigger, and dropping a conflicted path's
entry *without* teaching the cleanup about conflicts is precisely the deletion this
paragraph exists to prevent. An implementer must build the consultation, not assume
reconciliation already knows." That is my caveat, in the design's own voice, including
the failure it produces. The statement of today's behavior is accurate against the
code.

Forward-carry is now described correctly as the rejected alternative — "it retains the
entries so the cleanup sees the path as still produced" — with its cost stated (a
stale ownership claim under a hash that no longer describes what sits there). The
round-four warning against the backwards formulation is preserved in the form that is
correct for the chosen mechanism rather than kept as a sentence that would now
contradict the decision, which is the right call. Decision 7 (674-697) and the
Conflicts section (888-892) agree.

The choice also retires my round-five finding rather than carrying it: with the record
entry gone, `CheckDrift` never revisits the path, so neither the apply-time "modified
outside niwa" warning (`apply.go:597-607`) nor `niwa status`
(`internal/workspace/status.go:96`) goes on describing a conflicted repository's own
committed file as a drifted niwa-managed file. One drift warning fires on the apply
that first detects the conflict, from the prior state — accurate, one-shot, and
alongside the conflict report. Nothing perpetual and nothing false.

## Finding 2 and the optionals — closed, unchanged by the revert

Verified in the current file. The trust guarantee reads "removes the entry its record
names, **clears the record entry with the removal**, and never re-adds an entry while
the conflict stands. The guarantee is never-reinstated, not always-absent" (742-745),
with the composition argument intact — the developer's later answer "sits in no niwa
record, and later applies leave it alone", and a record left in place "would license
the next apply to delete the developer's answer at that key". Record-not-shape is
bounded at 755. Record-file disagreement states the safe direction (753-755).
Destruction ordering is at 1064. The displacement bounds hold at 264-272 and 1107.

## Required changes

None.

## Optional improvements

None.

## Closing note on the architecture

Across five rounds this design absorbed nine findings from me without once retreating
into hedging: every fix moved a mechanism or a batch boundary rather than softening a
sentence. The architecture I would now sign off on is the one it started with —
payload at the instance root, per-repository `.codex` under the default marker,
`AGENTS.override.md` composing the full chain, scoped canonicalized trust — with the
edges that were assumed in round one now measured, bounded, or refused. The rejected
alternatives were honest from the start. My remaining reservations are the ones the
design already books as limitations: two external layouts are load-bearing, and the
byte budget is a sized margin rather than a signal.

# Round 6

# Verdict: PASS

One clarification on the record, then the two answers.

## This file was already adjudicated

The current file is 1232 lines, md5 `29ca443491c13fa46ca305b8bd6bd4d6` — byte-identical
to the file my round-5 *final* section ruled on. The correction applies to the round-5
draft, which was written against the forward-carry text; the superseding section below
it already quoted lines 674-684 verbatim, judged the drop-plus-exemption mechanism
against my caveat, and returned PASS. So the ruling is unchanged rather than
re-derived, and nothing in the revert alters it.

For completeness I re-verified rather than trusting my own earlier note: three
mechanism sites and no fourth — line 674-678 states the exemption and the cleanup
change, line 891 echoes it in the Conflicts section, line 688 names forward-carry as
the rejected alternative with its cost. Consistent, once.

## Question 1 — is the cleanup change concrete enough to build?

Yes, and I tested it against the code rather than reading for reassurance. Three
things had to hold and all three do: the change is labelled a change ("**an explicit
exemption, which requires a named change to the cleanup**"); it names the data, the
direction, and the semantics precisely enough to map onto the real function — the
apply hands per-run conflict verdicts to the cleanup as an input, and the cleanup
skips that set before removing recorded paths the apply did not produce, which is one
input plus one skip before the `os.Remove` in `internal/workspace/apply.go:1846-1858`;
and the half-implementation is named as the bug it would be ("dropping a conflicted
path's entry *without* teaching the cleanup about conflicts is precisely the deletion
this paragraph exists to prevent. An implementer must build the consultation, not
assume reconciliation already knows"). The statement of today's behavior — the cleanup
tests only membership in the produced set — is accurate. An implementer building from
this paragraph builds the right thing, and one skipping the cleanup change has been
told in advance exactly what they will have built instead.

## Question 2 — should the design state that a conflicted path drops out of drift
reporting?

No. Leave it out.

It follows from what is already written: the record is the set of files niwa manages,
drift reporting is definitionally a report about managed files, and the design says
plainly that a conflicted path leaves the record so the state stays truthful about
what niwa no longer owns. A reader who wants the consequence can derive it in one
step, and the design is not a catalogue of every downstream effect of an accurate
record.

There is also a reason beyond noise to omit it. Stating drift-exclusion as a property
invites the next reader to treat it as a *goal* of the mechanism rather than a
consequence of the record being honest — and a goal is the kind of thing someone
later preserves artificially, by special-casing drift reporting, when the right
invariant is simply that the record names what niwa owns. The design is stronger with
one rule and derivable consequences than with the rule plus a list of its effects.

My round-five finding was a defect of forward-carry, not a gap in this mechanism.
Under drop-plus-exemption it does not exist to be documented.

One observable behavior, noted for the plan rather than the design: on the single apply
that first detects a conflict, the drift check runs over the *prior* state, which still
holds the entry, so one "modified outside niwa" warning fires alongside the conflict
report. That is accurate, one-shot, and self-consistent — worth a test author knowing,
not worth design ink.

## Required changes

None.

## Optional improvements

None.
