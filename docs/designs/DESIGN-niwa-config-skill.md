---
status: Proposed
problem: |
  Single-repo niwa workspaces (e.g. commuter, equity-planner) commit a
  hand-authored `.niwa/workspace.toml` directly into the adopting repo.
  An agent later working inside that repo's own niwa instance -- with no
  access to the tsukumogami org's private context -- may need to extend
  that config (add a hook, wire a secret, add a plugin, add instance
  files). Today it has no in-session guidance on the config schema; the
  only trail is doc-link comments pointing at
  docs/guides/workspace-config-sources.md and
  docs/guides/vault-integration.md in the niwa repo, which the agent has
  to discover and fetch cold, with no signal it should. niwa already
  ships an embedded Claude Code plugin (internal/plugin/files/niwa/)
  with one skill (migrate-config), but its install is gated on rank-2
  (deprecated whole-repo config) detection -- a condition that never
  fires for rank-1 single-repo workspaces, which are the normal case
  needing this guidance.
---

# DESIGN: niwa-config-skill

## Status

Proposed

## Context and Problem Statement

Single-repo niwa workspaces (the pattern commuter and equity-planner are
cited as examples of, per the dispatch brief -- see note below on their
verifiability) commit a hand-authored `.niwa/workspace.toml` directly into
the adopting repo. An agent working later inside that repo's own niwa
instance -- with no access to the tsukumogami org's private context -- may
need to extend that config: add a hook, wire a new secret, add a Claude
plugin, add instance files. Today that agent gets no in-session guidance on
the config schema. The only trail is doc-link comments pointing at
`docs/guides/workspace-config-sources.md` and `docs/guides/vault-integration.md`
in the niwa repo, which the agent has to discover and fetch cold, with no
signal it should look.

niwa already ships an embedded Claude Code plugin
(`internal/plugin/files/niwa/`, installed to
`~/.claude/plugins/marketplaces/niwa/`) with one skill, `migrate-config`,
scoped to walking a user through the rank-2 -> rank-1 migration. Its install
is triggered exclusively by rank-2 (deprecated whole-repo config) detection,
at four duplicated call sites in `internal/workspace/apply.go`
(`internal/config/config.go`-adjacent) -- a condition that never fires for
rank-1 single-repo workspaces, which are the normal case needing this
guidance. `niwa plugins install` (`internal/cli/plugins.go`) already exists
as a rank-agnostic manual install path that sidesteps this gate.

A parallel mechanism, `[instance.files]`, was recently activated
(`internal/config/config.go`, `internal/workspace/materialize.go`) to
verbatim-copy files or directories from `.niwa/` into each instance root on
every `niwa create`/`niwa apply`, with drift-tracked cleanup on removal --
mechanically capable of materializing a skill directory into
`.claude/skills/`, though no existing config, doc, or test in the repo uses
it for that purpose. `niwa init --bootstrap`'s scaffold template
(`internal/workspace/scaffold.go`) is a test-enforced, byte-equality-pinned
TOML skeleton that references neither mechanism today, and by construction
can only affect brand-new adopters -- it never re-fires for a repo that
already has a `.niwa/workspace.toml` marker.

Concrete evidence of drift risk exists in this repo already:
`docs/designs/current/DESIGN-workspace-config.md`, despite carrying
`status: Current`, is over two months stale and actively wrong -- it
documents a `[channels]` block removed from the codebase, misplaces
`[hooks]`/`[settings]` at the wrong nesting level, and omits `[vault]`,
`[claude.marketplaces]`, `env_output`, `[instance]`, and `[root]` entirely.
`internal/config/config.go` changed 27 times in under 4 months; a
hand-written schema copy baked into a new skill would very likely follow
the same trajectory.

**Note on the brief's named examples:** `dangazineu/commuter` and
`dangazineu/equity-planner`, cited in the dispatch brief as live examples of
this pattern (including a specific claim that commuter's `workspace.toml`
uses `[instance.files] "skills/" = ".claude/skills/"`), do not resolve via
`gh repo view`, `gh api`, or GitHub search under the environment's
authenticated account, and don't appear in that account's real repo
listing. Exploration could not verify these repos or the specific claim
about their config. This design treats the single-repo pattern itself as
real (it's documented in niwa's own guides and exercised by
`niwa init --bootstrap`'s scaffold-from-source path) but does not rely on
the named repos or the specific `[instance.files]` usage claim as confirmed
fact.

## Decision Drivers

- **Reach**: the mechanism must reach already-adopted single-repo
  workspaces (not just future ones), since bootstrap-scaffold seeding alone
  is structurally incapable of retrofitting existing adopters.
- **Drift resistance**: whatever content the skill carries must not become
  stale the way `DESIGN-workspace-config.md` did. A bare static schema copy
  baked into the skill is ruled out; the content strategy must either
  regenerate, delegate to a test-enforced source, or accept a bounded and
  monitorable drift surface.
- **Minimal new install surface**: prefer reusing or extending an existing,
  already-shipped mechanism (the embedded-plugin installer, or
  `[instance.files]`) over inventing a third delivery path.
- **No change to rank-2 migration behavior**: the existing `migrate-config`
  skill and its install trigger must keep working exactly as they do today.
- **Public-repo guardrails**: `public/niwa` only for this change; wip-hygiene
  applies (no committed `wip/...` references); niwa conventions apply
  (gofmt, go vet, conventional commits, functional-test coverage for
  user-facing CLI behavior changes).
- **Self-service friendliness**: a workspace owner should be able to adopt
  the mechanism without waiting on a niwa release, if the chosen approach
  allows it (relevant to `[instance.files]`, which is workspace.toml-authored
  and needs no niwa binary change to take effect in a given repo).

## Considered Options

### Decision 1: Delivery mechanism for the rank-1 config-editing skill

niwa ships an embedded Claude Code plugin (`internal/plugin/files/niwa/`,
installed to `~/.claude/plugins/marketplaces/niwa/`) with one skill,
`migrate-config`, whose install is triggered exclusively by rank-2
(deprecated whole-repo config) detection, at four duplicated call sites in
`internal/workspace/apply.go` (~443, ~595, ~927, ~956). Rank-1 single-repo
workspaces -- the normal, non-deprecated layout, and the population that
needs in-session guidance for editing `.niwa/workspace.toml` -- trigger
nothing today. A new config-editing skill needs a delivery path that
reaches these workspaces, covering both new adopters and repos that already
have a `.niwa/workspace.toml` today.

Two already-shipped niwa mechanisms compete for this role: the embedded-plugin
install (already runs unconditionally on every `niwa apply`/`niwa create`,
with rank already computed at all four call sites), and `[instance.files]`
(verbatim-copies files from an adopting repo's own `.niwa/` into its
instance root on every apply, already shipped in v0.21.1, no new niwa
release required).

**Key assumptions:**
- Workspace owners run `niwa apply`/`niwa create` as part of ordinary,
  routine usage, so wiring into that path constitutes genuine passive reach.
- The manifest version-bump discipline (`manifest.json`'s `version` field)
  must be enforced by a functional test asserting the new skill's file
  exists in HOME after install, not merely that the plugin installed --
  `plugin.Install`'s idempotence check silently withholds updates from
  already-installed users if this discipline lapses.
- The sibling content-sourcing decision (Decision 2: "live-source-grounded
  content") is compatible with this delivery mechanism without
  modification -- the skill ships as a `SKILL.md` file under
  `internal/plugin/files/niwa/skills/<name>/`, exactly like `migrate-config`
  does today.

#### Chosen: Extend the embedded-plugin auto-install gate to rank-1

Add a new, rank-1-gated branch alongside each of the four existing rank-2
branches in `internal/workspace/apply.go` (~443, ~595, ~927, ~956). Each
site already computes rank as a plain `int` before these blocks run, so the
new branch mirrors the existing one structurally: guard on a new notice-ID
constant via `sliceContains(disclosedNotices, ID)`, emit a notice, append to
`disclosedNotices`, call `a.InstallNiwaPlugin(nil, a.Reporter,
a.SkipPluginInstall)`. No line inside any existing rank-2 `if` block
changes. The new config-editing skill ships as a second entry in
`internal/plugin/files/niwa/manifest.json`'s `skills` array, alongside
`migrate-config`, with a `version` bump so already-installed users' on-disk
plugin actually picks up the addition. The outcome-reporting notice requires
no new code -- `plugin.Install` already calls `EmitPluginNotice` internally,
unconditional on rank.

This is the only candidate that satisfies "must reach already-adopted
single-repo workspaces" as a passive, automatic guarantee rather than a
best-effort or opt-in one. The mechanism it extends already runs
unconditionally on every `apply`/`create`; adding a rank-1 branch requires
no adopting repo to edit or commit anything. It also best satisfies the
preference for reusing an already-shipped mechanism built for exactly this
job (installing a skill into `~/.claude/`), rather than repurposing a
general-purpose file-materialization mechanism built for a different job
(`.mcp.json` distribution). The change is verified, by direct source
reading, to be fully additive -- it shares no mutated state with the rank-2
branches beyond an append-only notices slice.

The trade-off knowingly accepted: this mechanism cannot, on its own, satisfy
the soft preference that a workspace owner adopt "without waiting on a niwa
release," since the skill's content ships via `go:embed` inside the niwa
binary regardless of which trigger delivers it.

#### Alternatives Considered

- **`[instance.files]` self-declared per adopting repo**: mechanically real
  (already shipped in v0.21.1, destination containment is checked only
  against escaping the instance root, not nesting depth). Rejected because
  it is fundamentally a pull -- nothing in niwa rewrites an already-committed
  `workspace.toml`, so reach depends on a human or agent editing and
  committing that specific repo's file -- and because the exact
  nested-destination pattern it depends on is explicitly disclaimed by the
  project's own `docs/guides/file-distribution.md` ("destinations stay at
  the project root... not for niwa-internal directories," with `[files]`
  named as the intended per-repo tool instead). No test in the current suite
  covers this usage. Retained as candidate future work, gated on first
  reconciling that guide with what the code actually enforces.
- **Notify-only, pointing at `niwa plugins install`**: cheapest and
  lowest-risk by construction, but rejected because it duplicates, rather
  than adds to, what the chosen mechanism already provides for free --
  `plugin.Install`'s existing `EmitPluginNotice` call already fires the same
  disclosure unconditional on rank. Taken alone it also cannot satisfy "must
  reach," since an unread notice delivers zero bits to disk.
- **Hybrid (plugin-gate extension + documented `[instance.files]` escape
  hatch)**: reconsidered after the `[instance.files]` doc-contradiction
  finding surfaced. What began as an apparently free pairing turned out to
  depend on documenting a pattern the project's own guide disclaims, with a
  separate, independently-disqualifying content-availability gap (Decision
  2's "live-source-grounded content" answer means there is no frozen
  artifact for an early adopter to copy before a release merges).
  Rejected as scope creep for this design; noted as a candidate follow-up
  once the prerequisite doc/mechanism questions are resolved on their own
  track.

### Decision 2: Config-editing skill content sourcing strategy

The new config-editing skill needs to teach an agent how to extend an
already-adopted repo's `.niwa/workspace.toml` (add a hook, wire a secret,
add a plugin, add instance files) covering `claude.*` (including
`claude.hooks` and `claude.settings`, which have no dedicated guide today),
`env.*`, `vault.*`, `files`, and `instance` blocks. The failure mode to
avoid is concrete and already in the repo:
`docs/designs/current/DESIGN-workspace-config.md`, despite `status:
Current`, is confirmed 2+ months stale -- it documents a removed
`[channels]` block, misplaces `[hooks]`/`[settings]` nesting, and omits
`[vault]`, `[claude.marketplaces]`, `env_output`, `[instance]`, and `[root]`
entirely. Nothing in the repo would have caught this: the doc-validation CI
workflow checks artifact-doc format, not content accuracy, and skips docs
lacking a `schema:` frontmatter key, which this doc lacks.
`internal/config/config.go` changed 27 times in under 4 months, concentrated
in the `[claude]` block and file-distribution blocks; `vault.go` changed
only twice in the same window.

**Key assumptions:**
- `config.go`'s doc comments remain prose-quality; if they degrade, the
  strategy still works (struct tags remain readable) but loses explanatory
  value.
- `vault.go`'s low change rate continues; if vault gains the same churn as
  the claude block, its guide-pointer carve-out should be revisited.
- No `niwa config validate`/`lint`/`check` command exists today (confirmed
  by grep); if one is added later, the skill should invoke it as a closing
  step, but that's separate, additive future work.

#### Chosen: Live-source-grounded content, with a narrow guide carve-out for vault

The skill's own `SKILL.md` prose stays short and procedural: it never
restates field names, defaults, or section shapes as text the skill owns.
Instead it teaches the agent how to find and interpret the ground truth at
invocation time: (1) read `internal/config/config.go` (and
`internal/config/vault.go`) to find the relevant struct and read its doc
comment and `toml` tags directly; (2) use `scaffold.go`'s `scaffoldTemplate`
as an illustrative starting shape, but explicitly cross-check every field
name against `config.go` before using it -- research found `scaffoldTemplate`
has already drifted on a real field name (`project_id` vs. the actual
`project`), and its pinning test only does loose substring/parse checks; (3)
write each common-edit walkthrough (add a hook, wire a secret, add a
plugin, add instance files, add a marketplace) as a short numbered
procedure describing where to look and what shape to expect, not a worked
TOML snippet frozen in skill prose; (4) one exception -- for `vault.*`
specifically, point the agent to `docs/guides/vault-integration.md` as the
primary reference, because every spot-checked field in that guide matches
`vault.go` today and the underlying code is empirically stable (2 changes
in 4 months, versus 27 for the rest of the schema).

This directly satisfies the ruled-out-alternative constraint (no
hand-copied schema baked into skill prose) while being honest that
`scaffoldTemplate` is not the rot-proof artifact the original constraints
assumed -- it's commented-out, loosely tested, and has already drifted.
Treating it as "cross-check before trusting" rather than "authoritative
worked example" closes that gap without discarding its value as the
richest single illustration of the schema.

#### Alternatives Considered

- **Hand-written schema, hand-maintained**: identical in structure to
  `DESIGN-workspace-config.md`, already proven to drift within months on a
  repo where `config.go` changes roughly weekly. Rejected by this decision's
  own constraints.
- **Guides-first with config.go fallback for gaps (full version)**: reuses
  more existing prose, less to write up front. Rejected because two of the
  three guides (`workspace-config-sources.md`, `file-distribution.md`) map
  to the fastest-changing parts of the schema, so trusting them as primary
  reference reintroduces the same unmonitored-drift risk this decision
  exists to avoid. The vault-mapped third survives as a carve-out in the
  chosen option.
- **Generation/sync mechanism (go:generate or CI content-diff)**: would most
  durably close the drift loop if it worked, but requires building
  infrastructure that doesn't exist in this repo today, and doesn't fully
  solve the curation problem even once built. Reasonable future work, out
  of scope here.

### Decision 3: Should niwa init --bootstrap's scaffold template change to seed discoverability for brand-new adopters?

Decision 1 extended the embedded-plugin auto-install gate to fire on rank-1
detection, so every rank-1 workspace picks up the plugin automatically the
next time its owner runs `niwa apply`/`niwa create`. The working hypothesis
was that this already covers brand-new `niwa init --bootstrap` adopters for
free, since bootstrap's own `Create` call goes through the identical
`runPipeline` that Decision 1's new branch lives in -- narrowing this
decision to, at most, a cosmetic doc-pointer fix.

Direct verification against the current source confirmed half of that
premise and falsified the other half. `defaultRunBootstrap` does call
`applier.Create`, the identical method `niwa create` calls, and
`runPipeline` does compute rank the normal way (always rank 1 for a
freshly-bootstrapped workspace). But `defaultRunBootstrap` constructs its
own `Applier` and wires only `Reporter`, `ConfigSourceURL`,
`GlobalConfigDir`, and `Agent` onto it -- it never calls
`configurePluginAutoInstall`, the one function that wires the
`Applier.InstallNiwaPlugin` seam to the real implementation. Every other
Applier-constructing call site in the CLI calls it; `defaultRunBootstrap` is
the sole exception. Consequence: a freshly-bootstrapped rank-1 workspace's
own `Create` call silently skips the auto-install, both today and after
Decision 1's rank-1 branch lands, since that branch only alters the trigger
condition guarding the check, not the wiring behind it. This matters
concretely because the very first Claude Code session in a brand-new
bootstrap adopter's worktree -- precisely the "first contact" moment
`--bootstrap` exists to optimize -- is guaranteed to lack the plugin.

**Key assumptions:**
- None required for the core finding -- resolved by direct source reading
  with no residual ambiguity (status: confirmed).
- Decision 1's rank-1 branch is assumed to land as described in its own
  report; this fix is additive to and dependent on that landing.

#### Chosen: No scaffold-template content change; fix the CLI wiring gap in internal/cli/init.go instead

Do not modify `scaffoldFromSourceTemplate` -- its TOML content is not the
blocking factor, and its trailing doc-pointer comment is already accurate.
Instead, add one line to `defaultRunBootstrap`, immediately after
`applier := workspace.NewApplier(gh)` and before `applier.Create` is
invoked: `configurePluginAutoInstall(applier, initNoInstallPlugins)`. This
mirrors, verbatim, the pattern already used at the other four
Applier-constructing call sites. `initCmd` already declares the
`--no-install-plugins` flag for a different code path (rank-2 handling
inside `runInit`'s non-bootstrap `modeClone` branch); this change makes the
bootstrap path honor the same, already-documented flag, at no cost of a new
flag or new user-facing surface.

This is the only candidate that closes the actual, source-verified gap: the
premise ("bootstrap already goes through the same runPipeline that calls
InstallNiwaPlugin the same way niwa create does") is true for the call
graph but false for the wiring. No amount of scaffold-template editing can
fix a Go wiring omission. The fix is a one-line, additive change matching
an existing four-times-repeated pattern exactly, touches zero bytes of the
PRD Appendix A-pinned template string, and reuses a flag that already
exists on the same command.

#### Alternatives Considered

- **No change at all** (decision 1 already covers this for free): rejected
  -- the premise it rests on is false, verified by direct source reading.
- **Scaffold-template doc-pointer fix**: the plain (non-bootstrap)
  `Scaffold()` template does have a genuinely stale doc pointer
  (`scaffold.go:45`), but that lives in a different template used by a
  different init mode, not `scaffoldFromSourceTemplate`. Bundling it here
  would be scope creep onto an unrelated file and would do nothing to close
  the real gap. Noted as a low-priority, out-of-scope finding.
- **Substantive scaffold-template content change**: rejected on the same
  grounds Decision 1 already used against a related alternative -- this is
  a byte-equality-pinned string requiring PRD Appendix A-level rigor to
  touch, and by construction the scaffold path never re-fires for an
  already-bootstrapped repo, so it only helps future runs and does nothing
  for the mechanism-not-firing gap found here.

## Decision Outcome

Three decisions compose into one coherent change, with no conflicts between
them: (1) extend `internal/workspace/apply.go`'s embedded-plugin
auto-install trigger to also fire on rank-1 source detection, additive to
the four existing rank-2 branches; (2) author the new config-editing
skill's `SKILL.md` as procedural guidance that teaches an agent to read
`internal/config/config.go` live rather than owning static schema prose,
with a narrow doc-pointer carve-out for the empirically stable `vault.*`
block; (3) fix a real, independently-discovered wiring gap in
`internal/cli/init.go`'s bootstrap path so the rank-1 trigger from (1)
actually reaches a workspace's first session, not just its second `apply`
onward.

Decision 3 is a direct dependency correction surfaced by researching
Decision 1's own scope, not a competing or contradictory choice --
Decision 1's report correctly deferred the bootstrap-scaffold *content*
question, but its Consequences section implicitly assumed bootstrap's
*install trigger* already worked, which Decision 3's research falsified.
Together, all three land in a single PR: two changes to
`internal/workspace/apply.go` and `internal/cli/init.go` (small, additive,
mirroring existing patterns four times over), one new `manifest.json` entry
plus `SKILL.md`, and no changes to any byte-equality-pinned template
string. The `[instance.files]` early-access escape hatch and the plain
`Scaffold()` template's stale doc pointer are both explicitly deferred as
separate, lower-priority follow-up work, not silently dropped.

## Solution Architecture

### Overview

Three coordinated, individually-small changes land in one PR, each reusing
an existing, already-tested pattern rather than introducing new machinery:
a new rank-1 branch in the install pipeline, a new skill shipped inside the
existing embedded plugin, and a one-line wiring fix so the bootstrap path
picks up the same install trigger every other CLI entry point already has.

### Components

- **`internal/workspace/disclosure.go`**: two new notice-ID constants,
  `NoticeIDRank1TeamConfig` / `NoticeIDRank1Overlay` (string values
  `"rank1-plugin-install:team-config"` / `"rank1-plugin-install:overlay"`,
  following the existing `rank2-deprecation:*` naming shape), and a new
  `EmitRank1Notice(identifier string, reporter *Reporter)` function
  mirroring `EmitRank2Notice`'s shape -- logs why a rank-1 source triggered
  an install, distinct from `EmitPluginNotice`'s installed/skipped outcome
  report (both fire; they answer different questions -- "why" vs. "what
  happened").
- **`internal/workspace/apply.go`**: four new `if teamConfigRank == 1 &&
  !sliceContains(disclosedNotices, NoticeIDRank1...)` blocks, one adjacent
  to each existing rank-2 block (~443, ~595, ~927, ~956), each calling
  `EmitRank1Notice`, appending the notice ID, and calling
  `a.InstallNiwaPlugin(nil, a.Reporter, a.SkipPluginInstall)` -- structurally
  identical to the rank-2 blocks, sharing no mutated state beyond the
  append-only `disclosedNotices` slice.
- **`internal/cli/init.go`**: one new line in `defaultRunBootstrap`,
  immediately after `applier := workspace.NewApplier(gh)` and before
  `applier.Create` is invoked: `configurePluginAutoInstall(applier,
  initNoInstallPlugins)`.
- **`internal/plugin/files/niwa/manifest.json`**: a new `skills[]` entry
  (name `edit-config`, matching the existing `migrate-config` naming
  convention -- verb-noun, kebab-case) and a `version` bump so
  already-installed users' on-disk plugin picks up the addition per
  `plugin.Install`'s string-equality idempotence check.
- **`internal/plugin/files/niwa/skills/edit-config/SKILL.md`** (new file):
  procedural content per Decision 2 -- teaches an agent to read
  `internal/config/config.go` live, cross-check `scaffold.go`'s
  `scaffoldTemplate` before trusting it, and defer to
  `docs/guides/vault-integration.md` for the `vault.*` block specifically.
- **`internal/workspace/disclosure.go`'s existing `NoticeIDPluginInstalled`
  log line** (line 41) needs a wording update: it currently hardcodes "Use
  /niwa:migrate-config to invoke the migration skill," which becomes
  misleading once a rank-1 install brings in a plugin whose relevant skill
  is `/niwa:edit-config`, not `/niwa:migrate-config`. The line should name
  both skills or drop the specific skill name and point at `niwa plugins
  install --help` / the plugin's own skill list instead.

### Key Interfaces

- `Applier.InstallNiwaPlugin func(state *InstanceState, reporter *Reporter,
  skipInstall bool)` (`apply.go:92`) -- unchanged function-field seam, now
  invoked from four additional call sites.
- `configurePluginAutoInstall(applier *workspace.Applier, optOut bool)`
  (`internal/cli/plugin_adapter.go`) -- unchanged, called from a fifth site
  (`defaultRunBootstrap`) in addition to its existing four.
- `plugin.Install(state *InstanceState, reporter *Reporter, opts
  InstallOpts)` (`internal/plugin/installer.go`) -- unchanged; rank-1 and
  rank-2 branches both funnel into the same idempotent install.
- `manifest.json`'s `skills[]` array shape -- unchanged schema, one
  additional entry.

### Data Flow

**Existing/already-adopted rank-1 workspace:** `niwa apply` or `niwa
create` -> `runPipeline` computes `teamConfigRank` (== 1) -> new rank-1
branch fires (once per workspace, per `DisclosedNotices` bookkeeping) ->
`EmitRank1Notice` logs why -> `a.InstallNiwaPlugin` -> `plugin.Install` ->
`EmitPluginNotice` logs the outcome -> plugin tree written to
`~/.claude/plugins/marketplaces/niwa/` -> both `migrate-config` and
`edit-config` skills available starting the next Claude Code session in
that workspace.

**Brand-new `niwa init --bootstrap` adopter:** `defaultRunBootstrap` builds
an `Applier`, now wired via `configurePluginAutoInstall` -> calls
`applier.Create` -> same `runPipeline` as above, `teamConfigRank` is always
1 for a freshly-bootstrapped workspace -> rank-1 branch fires on this very
first `Create` call -> plugin installed before `RunBootstrap` prints its
landing instructions and hands the user into their first worktree session.

## Implementation Approach

### Phase 1: Wire the rank-1 install trigger

Add `NoticeIDRank1TeamConfig`/`NoticeIDRank1Overlay` and `EmitRank1Notice`
to `internal/workspace/disclosure.go`. Add the four new rank-1 branches to
`internal/workspace/apply.go`, adjacent to the existing rank-2 ones. Update
`NoticeIDPluginInstalled`'s log line to not hardcode a single skill name.

Deliverables:
- `internal/workspace/disclosure.go` changes
- `internal/workspace/apply.go` changes (4 new blocks)
- Corresponding unit tests in `internal/workspace/apply_test.go` /
  `disclosure_test.go`

### Phase 2: Fix the bootstrap wiring gap

Add the single `configurePluginAutoInstall(applier, initNoInstallPlugins)`
line to `defaultRunBootstrap` in `internal/cli/init.go`.

Deliverables:
- `internal/cli/init.go` one-line change
- Unit test asserting `Applier.InstallNiwaPlugin` is non-nil after
  `defaultRunBootstrap` constructs its `Applier`

### Phase 3: Author the config-editing skill content

Write `internal/plugin/files/niwa/skills/edit-config/SKILL.md` per
Decision 2's content strategy. Add the `skills[]` entry and bump `version`
in `internal/plugin/files/niwa/manifest.json`.

Deliverables:
- `internal/plugin/files/niwa/skills/edit-config/SKILL.md`
- `internal/plugin/files/niwa/manifest.json` changes

### Phase 4: Functional test coverage

Per `docs/guides/functional-testing.md`, add scenarios covering user-facing
CLI behavior:
- Rank-1 source triggers plugin install on `niwa apply`/`niwa create`
  (adapted from `test/functional/features/config-source-rank2.feature`'s
  existing `@critical` scenarios: swap the rank-2 fixture for rank-1,
  assert stderr contains the new notice text, assert `manifest.json` and
  the `edit-config` skill file exist in HOME).
- `--no-install-plugins` opt-out variant for the rank-1 trigger.
- `niwa init --bootstrap` installs the plugin/skill on the first `Create`
  call, not only on a subsequent `apply` -- this is the scenario that
  specifically proves Phase 2's fix, since it must fail before that fix
  lands and pass after.
- `--no-install-plugins` opt-out variant for the bootstrap path.

Deliverables:
- New/extended `.feature` files under `test/functional/features/`
- CI green on the full functional suite

## Consequences

### Positive
- Every rank-1 workspace -- new or already-adopted -- gets in-session
  config-editing guidance automatically, with no edit required to that
  repo's own files, the first time its owner upgrades niwa and runs
  `apply`/`create`/`init --bootstrap`.
- The fix is fully additive: zero lines change inside any existing rank-2
  branch, and `plugin.Install`'s own idempotence makes a same-apply
  double-install (e.g. a rank-2 team-config hit plus a rank-1 overlay hit)
  a harmless no-op re-check.
- The skill's content can never go stale the way
  `docs/designs/current/DESIGN-workspace-config.md` did, because it never
  owns a copy of the schema -- it teaches an agent to read the live source.
- Decision 3's bootstrap-wiring fix closes a real gap that existed
  independent of this design (any future rank-1-triggered install would
  have hit the same silent no-op on the bootstrap path) -- a byproduct
  correctness win, not just a means to this design's end.

### Negative
- Rank-1 users now also receive the `migrate-config` skill bundled
  alongside `edit-config`, whose description is entirely about a rank-2
  scenario that doesn't apply to them -- a minor plugin-browsing conceptual
  mismatch, not a functional problem, since nothing forces its invocation.
- The manifest version-bump discipline becomes a required, easy-to-silently-
  skip step for every future skill addition to this plugin, not just this
  one -- if skipped, `plugin.Install`'s bare string-equality version check
  silently withholds the update from already-installed users with no error
  surfaced.
- The `edit-config` skill's guidance costs a live read of `config.go` (and
  sometimes `scaffold.go`) per invocation, making it slightly slower and
  more verbose per-use than a skill that could just quote a static
  reference. Its quality also depends on `config.go`'s doc comments staying
  prose-quality -- a soft dependency, not an enforced one.
- The `vault.*` content carve-out means the skill has two different
  content-sourcing behaviors depending on which block is being edited; a
  future maintainer could "simplify" this into one mode without
  understanding why vault gets different treatment.
- The soft preference for pre-release, before-a-niwa-upgrade self-service
  adoption is left unresolved by this design -- deferred, not solved.

### Mitigations
- A new functional-test scenario asserts the `edit-config` skill's file
  (not just "plugin installed") exists in HOME after a rank-1 install and
  after a bootstrap `Create` call, catching both a dropped version bump and
  a re-broken bootstrap wiring path before it reaches users.
- The `edit-config` `SKILL.md` states its two-mode content strategy
  explicitly (live-read for everything except `vault.*`, doc-pointer for
  `vault.*`) so a future maintainer sees the rationale in the same file
  they'd be editing, rather than needing to rediscover it from this design
  doc.
- The `[instance.files]` early-access escape hatch and the plain
  `Scaffold()` template's stale doc pointer are recorded here as explicit,
  named follow-up work (see Considered Options, Decision 1 and Decision 3
  Alternatives) rather than silently dropped, so they're discoverable by a
  future contributor without re-deriving them from scratch.
