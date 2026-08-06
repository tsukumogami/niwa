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
decision: |
  Extend the embedded plugin's auto-install trigger in
  internal/workspace/apply.go to also fire on rank-1 source detection,
  additive to the four existing rank-2 branches, so every rank-1
  workspace -- new or already-adopted -- picks up the plugin automatically
  on its next niwa apply/create. Ship a new edit-config skill inside that
  same plugin, backed by a build-time-generated, CI-freshness-enforced
  markdown reference (a small go/ast-based generator, stdlib only,
  committed and embedded alongside SKILL.md) rather than static schema
  prose or a live source read -- the latter was tried first and found
  non-viable, since rank-1 adopters never have a niwa repo checkout on
  disk to read from. Fix an independently-discovered wiring gap in
  internal/cli/init.go's bootstrap path so the rank-1 trigger reaches a
  freshly-bootstrapped workspace's first session, not just later applies.
rationale: |
  Reaching already-adopted workspaces passively, without requiring any
  edit to their own committed files, ruled out every alternative that
  depends on opt-in per-repo action (instance.files self-declaration) or
  on the workspace owner reading and acting on a notice (notify-only).
  Grounding the skill's content in a build-time-generated, CI-diffed
  reference rather than hand-copied schema prose was forced by concrete
  evidence in this same repo: DESIGN-workspace-config.md, despite being
  marked Current, drifted stale and actively wrong within months. A first
  pass chose to read config.go live instead of generating a reference, but
  a Phase 6 security-verification review found that approach doesn't work
  for the skill's actual target population, so the design was restarted
  once onto the generator-based approach before finalizing. The bootstrap
  wiring fix is a small, independently-verified correction bundled into
  the same PR since it's required for decision 1's reach guarantee to
  actually hold at the moment -- a brand-new adopter's first session --
  that matters most.
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
- The sibling content-sourcing decision (Decision 2) is compatible with
  this delivery mechanism without modification -- the skill ships as a
  `SKILL.md` file (plus a generated reference file) under
  `internal/plugin/files/niwa/skills/<name>/`, exactly like `migrate-config`
  does today.

#### Chosen: Extend the embedded-plugin auto-install gate to rank-1

Add a new, rank-1-gated branch alongside each of the four existing rank-2
branches in `internal/workspace/apply.go`, structurally mirroring them --
no line inside any existing rank-2 branch changes. The new config-editing
skill ships as a second entry in the existing plugin's manifest, with a
version bump so already-installed users pick up the addition. See Solution
Architecture for the exact call sites, notice-ID naming, and per-site
variable details.

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
entirely. `internal/config/config.go` changed 27 times in under 4 months,
concentrated in the `[claude]` block and file-distribution blocks;
`vault.go` changed only twice in the same window.

**This decision was restarted once** after a Phase 6 security-verification
review found the first pass's chosen approach non-viable for the skill's
actual target population -- see below.

**Key assumptions:**
- `config.go`'s and `vault.go`'s doc comments remain prose-quality going
  forward, since the chosen generator's output quality depends on it. If
  comments degrade, the generated reference degrades to field/tag-only
  listings -- still structurally accurate, just less explanatory.
- A new CI job that re-runs the generator and diffs its output against git
  is in scope for this design (mirroring the existing `gofmt`/`go vet`
  enforcement pattern). Even without it, the generated file starts
  accurate and only degrades if a later `config.go` change ships
  unregenerated -- strictly better than a hand-copy from day one, but
  materially better with the CI gate.

#### Chosen: Build-time-generated reference, embedded via go:embed, CI-freshness-enforced

The first pass at this decision chose to have the skill instruct an agent
to read `internal/config/config.go`/`vault.go` "live" at invocation time,
reasoning that reading the actual source can never be stale. A Phase 6
security-verification review found this breaks for the population the
skill exists to serve: per `docs/guides/workspace-config-sources.md`'s
"Single-repo workspace" section, `niwa init --from owner/repo` materializes
only the adopting repo's own `.niwa/` subtree into the workspace
snapshot -- "the rest of the repo ... is never fetched." niwa's own source
lives in a separate repository that is never checked out inside a rank-1
adopter's (commuter, equity-planner) niwa instance, so "read config.go
live" has nothing to read there. The same review found the first pass's
`vault.*` carve-out (pointing at `docs/guides/vault-integration.md`) has
the identical flaw -- that guide lives in the same, equally-absent niwa
repo.

Only two kinds of content are actually guaranteed present inside an
arbitrary rank-1 adopter's niwa instance: whatever ships bundled inside the
plugin via Decision 1's already-finalized `go:embed` delivery, and whatever
the `niwa` binary itself can produce at runtime. The chosen approach:

1. A new small internal generator (`internal/configschema/gen/main.go`,
   wired via `//go:generate` near `internal/config/config.go`) walks the
   config package's AST using the Go standard library's `go/ast` and
   `go/doc` packages -- no new external dependency -- and emits a
   structured markdown reference covering every struct the skill needs:
   `WorkspaceConfig`, `ClaudeConfig`, `ClaudeOverride`, `HooksConfig`,
   `SettingsConfig`, `ClaudeEnvConfig`, `EnvConfig`, `VaultRegistry`,
   `VaultProviderConfig`, `InstanceConfig`, `RootConfig`, and the
   file-distribution blocks -- field name, `toml` tag, and doc comment,
   verbatim from source.
2. The generated output is committed to git at
   `internal/plugin/files/niwa/skills/edit-config/reference/schema.md`,
   shipping to every niwa instance via the identical `go:embed` mechanism
   `SKILL.md` already uses -- `internal/plugin/embed.go` embeds the whole
   `internal/plugin/files/niwa/` tree with no per-file allowlist, so this
   adds zero delivery-mechanism change and stays inside Decision 1's
   already-finalized scope.
3. A new CI job re-runs the generator and does a `git diff --exit-code`
   against the committed file, alongside the existing `go vet ./...` and
   `go test ./...` jobs. Any PR that changes `config.go`/`vault.go` without
   regenerating the reference fails CI.
4. The generated file is committed to git, not produced fresh at
   release-build time -- `.goreleaser.yaml` runs a plain `go build` with no
   codegen hook, so every ordinary build and CI entry point (not just
   releases) needs the embedded file present in a plain checkout.
5. `vault.*` folds into this same generated reference; the first pass's
   guide-pointer carve-out is removed, since the sourcing mechanism itself
   is now checkout-independent and there's no reason to treat vault
   differently.
6. `SKILL.md`'s own prose stays exactly as procedural as originally
   intended: short numbered walkthroughs ("add a hook," "wire a secret,"
   "add a plugin," "add instance files," "add a marketplace") that point
   into the generated reference for current field names, rather than
   restating schema facts as text the skill owns.
7. Optional, not required for correctness: `SKILL.md` may additionally
   instruct that if a niwa checkout happens to be detectable on disk (e.g.
   while developing niwa itself), the agent may cross-check the bundled
   reference against live `config.go`. This serves the narrower
   niwa-contributor population without weakening the guarantee for the
   universal rank-1 population, which never depends on this branch firing.

This satisfies drift-resistance (CI-diffed, tighter than a live-read
guarantee that only worked for a narrower population), availability (ships
identically to `SKILL.md`, zero new delivery mechanism), and content
quality (keeps the prose value `config.go`'s doc comments already carry)
all at once -- the one candidate that clears all three bars for the actual
target population, not just the narrower "editing niwa itself" case.

#### Alternatives Considered

- **Hand-written schema, hand-maintained**: identical in structure to
  `DESIGN-workspace-config.md`, already proven to drift within months on a
  repo where `config.go` changes roughly weekly. Rejected by this
  decision's own constraints, both passes.
- **New `niwa` CLI introspection command** (e.g. `niwa config schema
  --json` via reflection over the live struct types): satisfies
  availability -- the binary is always present -- but Go reflection cannot
  recover doc comments, so it regresses guidance quality below what the
  chosen generator gives, while requiring new user-facing CLI surface to
  do strictly less. Rejected; noted as reasonable future work if a
  runtime-introspection need independent of this skill emerges later.
- **Live-source-grounded content, with a narrow guide carve-out for vault**
  (the first pass's chosen option): reading `config.go`/`vault.go` (or the
  existing guides) live at invocation time can never itself be stale, but
  requires a niwa repo checkout to be present at invocation. Verified this
  is never true for the skill's actual target population (rank-1
  single-repo adopters) -- `niwa init --from owner/repo` materializes only
  the adopting repo's own `.niwa/` subtree, never niwa's own source or
  docs. Rejected after the restart; the underlying insight (source of
  truth should track code, not be hand-copied) survives into the chosen
  option's generator, just decoupled from requiring a live checkout.
- **Context-branching (live read if a checkout is detectable, else
  fallback)**: not rejected outright -- folded into the chosen option as
  an optional enhancement for the narrower niwa-contributor population,
  since the fallback it needs is the chosen generated reference anyway.
  Not viable as a standalone strategy, since the universal rank-1
  population never has the checkout branch fire.
- **Generation/sync mechanism (go:generate or CI content-diff)**: the first
  pass considered and rejected this as out-of-scope infra-building, on the
  assumption that live-read was viable as a lower-cost alternative. With
  live-read shown non-viable for the target population, this became the
  chosen option on restart -- the cost/benefit calculus changed once the
  alternative to "build a small generator" was no longer "read source
  live" (doesn't work here) but "ship something hand-copied and
  already-proven-to-drift."

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
Instead, wire `defaultRunBootstrap` through the same
`configurePluginAutoInstall` helper the other four Applier-constructing
call sites already use, reusing the `--no-install-plugins` flag `initCmd`
already declares for a different code path. See Solution Architecture for
the exact insertion point.

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
skill's `SKILL.md` as procedural guidance backed by a build-time-generated,
CI-freshness-enforced schema reference embedded alongside it -- not live
source reads, which a Phase 6 review found don't work for the skill's
actual target population (rank-1 adopters never have a niwa checkout on
disk); (3) fix a real, independently-discovered wiring gap in
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
plus `SKILL.md`, a small new schema-generator package and a CI
freshness-diff job, and no changes to any byte-equality-pinned template
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
  `EmitRank1Notice(id, identifier string, reporter *Reporter)` function --
  three parameters, matching `EmitRank2Notice`'s actual signature
  (`func EmitRank2Notice(id, identifier string, reporter *Reporter)`), not
  the two-parameter shape an earlier draft of this design incorrectly
  described. `EmitRank1Notice` logs why a rank-1 source triggered an
  install, distinct from `EmitPluginNotice`'s installed/skipped outcome
  report (both fire; they answer different questions -- "why" vs. "what
  happened"). The `id` parameter lets one function serve both new notice
  IDs (team-config vs. overlay), mirroring how `EmitRank2Notice` already
  does this today.
- **`internal/workspace/apply.go`**: four new `if teamConfigRank == 1 &&
  !sliceContains(<call-site's own disclosed-notices variable>,
  NoticeIDRank1...)` blocks, one adjacent to each existing rank-2 block
  (~443, ~595, ~927, ~956). The four rank-2 blocks each reference a
  *different* local variable for the disclosed-notices slice
  (`initDisclosedNotices`, `wsDisclosedNotices`, and
  `opts.disclosedNotices` at two sites) -- the rank-1 blocks must each
  reference that same call site's own variable, not a single shared name;
  a literal copy-paste using one generic name would fail to compile at
  three of the four sites. Each block calls `EmitRank1Notice`, appends the
  notice ID to that site's variable, and calls
  `a.InstallNiwaPlugin(nil, a.Reporter, a.SkipPluginInstall)` --
  structurally identical to the rank-2 blocks, sharing no other mutated
  state.
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
  procedural content per Decision 2 -- short numbered walkthroughs (add a
  hook, wire a secret, add a plugin, add instance files, add a
  marketplace) that point into the bundled generated reference for
  current field names, rather than restating schema facts or reading
  niwa-repo files live.
- **`internal/plugin/files/niwa/skills/edit-config/reference/schema.md`**
  (new file, generated): committed, `go:embed`-shipped schema reference
  produced by the new generator (see below). This is what `SKILL.md`
  actually points an agent at for current field names and doc comments.
- **`internal/configschema/gen/main.go`** (new file): small internal
  generator, `go/ast`/`go/doc`-based (stdlib only, no new dependency),
  wired via a `//go:generate` directive near `internal/config/config.go`.
  Walks `WorkspaceConfig`, `ClaudeConfig`, `ClaudeOverride`, `HooksConfig`,
  `SettingsConfig`, `ClaudeEnvConfig`, `EnvConfig`, `VaultRegistry`,
  `VaultProviderConfig`, `InstanceConfig`, `RootConfig`, and the
  file-distribution blocks, emitting the markdown reference above.
- **CI**: a new job alongside the existing `go vet ./...`/`go test ./...`
  jobs that re-runs the generator and does `git diff --exit-code` against
  the committed reference file, failing any PR that changes
  `config.go`/`vault.go` without regenerating it.
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

**Simpler alternative considered and deferred:** since `plugin.Install` is
already idempotent and rank is now checked at every site regardless of
value (1 or 2, no other value occurs), `InstallNiwaPlugin` could instead be
called unconditionally once per pipeline invocation, with only the
notice-text logic staying rank-gated -- cutting the new per-site
duplication roughly in half. This design keeps the four separate,
rank-gated blocks instead, so that zero lines change inside the existing
rank-2 branches (a stronger diff-safety guarantee for "must not change
rank-2 migration behavior" than a shared unconditional call would give,
since the latter touches code both ranks depend on). Recorded here as a
deliberate trade-off, not an overlooked option.

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

### Phase 3: Build the schema-reference generator and wire CI enforcement

Write `internal/configschema/gen/main.go` (stdlib `go/ast`/`go/doc` only)
and a `//go:generate` directive near `internal/config/config.go`. Run it
once to produce the committed
`internal/plugin/files/niwa/skills/edit-config/reference/schema.md`. Add a
new CI job alongside the existing `go vet ./...`/`go test ./...` jobs that
re-runs the generator and fails on `git diff --exit-code` against the
committed file.

Deliverables:
- `internal/configschema/gen/main.go`
- `//go:generate` directive wiring
- Committed `internal/plugin/files/niwa/skills/edit-config/reference/schema.md`
- New CI freshness-diff job

### Phase 4: Author the config-editing skill content

Write `internal/plugin/files/niwa/skills/edit-config/SKILL.md` per
Decision 2's content strategy -- short procedural walkthroughs pointing
into the generated reference from Phase 3, not restated schema facts. Add
the `skills[]` entry and bump `version` in
`internal/plugin/files/niwa/manifest.json`. Per Security Considerations,
the `claude.settings` and `vault.*` walkthroughs must include explicit
guardrail language against writing plaintext secrets into those blocks
(prefer `vault://` references; provider config carries only non-secret
fields, credentials handled out-of-band via the provider's own CLI) --
niwa's automated public-repo secret guardrail does not walk either of these
struct paths, so the skill is the only backstop here.

Deliverables:
- `internal/plugin/files/niwa/skills/edit-config/SKILL.md`, including
  explicit secret-handling guardrail language for `claude.settings` and
  `vault.*`
- `internal/plugin/files/niwa/manifest.json` changes

### Phase 5: Functional test coverage

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

## Security Considerations

**External artifact handling.** This design introduces no new download,
fetch, or execution path. The plugin content written to
`~/.claude/plugins/marketplaces/niwa/` is compiled into the niwa binary via
`go:embed` (`internal/plugin/embed.go`) and is identical regardless of
which trigger (rank-1 or rank-2) fires the install -- `plugin.Install` never
reads from the network or from the adopting repo's own source.

**Permission scope.** No new permission type is introduced.
`InstallNiwaPlugin` already writes to `~/.claude/plugins/marketplaces/niwa/`
on every `apply`/`create`; this design changes which condition (rank-1 in
addition to rank-2) triggers that existing, unchanged write. The existing
opt-outs (`--no-install-plugins`, `auto_install_plugins = false`) apply
identically to the new rank-1 branches -- verified all four new call sites
pass the same `a.SkipPluginInstall` field used by the four existing rank-2
sites. The practical effect of this design is a scale/frequency change: the
same trusted, idempotent write now happens for the common case (rank-1)
instead of only the rare, deprecated case (rank-2). `teamConfigRank` is a
structural property of the config source's own directory layout
(marker-file presence), not an attacker-settable flag independent of that
layout; a malicious config source can only "claim" the rank its actual file
layout implies, and controlling that rank yields no leverage beyond
toggling installation of niwa's own fixed, embedded, already-trusted
plugin -- a source repo an operator already trusts enough to point a
workspace at already has far larger avenues of impact (arbitrary hooks,
arbitrary `claude.settings`, arbitrary `env.*` wiring) than this toggle.

**Supply chain / dependency trust.** An earlier pass of Decision 2 had the
`edit-config` skill read `internal/config/config.go`/`vault.go` "live" from
whatever niwa checkout the skill happened to be invoked in. A Phase 6
security-verification review found this doesn't work for the skill's
actual target population -- rank-1 single-repo adopters never have a niwa
checkout on disk at all, per `docs/guides/workspace-config-sources.md`'s
"Single-repo workspace" section -- so the design was restarted onto a
build-time-generated, committed, `go:embed`'d reference instead (see
Considered Options, Decision 2). This removes the live-checkout trust
question entirely: the schema reference is generated once, at PR time, by
a small stdlib-only (`go/ast`/`go/doc`) generator, CI-diffed for
freshness, reviewed like any other diff, and shipped as static content
identical in trust posture to `SKILL.md` itself -- no code executes at
skill-invocation time to produce it, and nothing about the adopting
workspace's own repo is read to generate it. The only new supply-chain
surface is the generator itself: new, but minimal (a single Go file over
the standard library, no new dependency), reviewed in the same PR that
introduces it, and re-run only in CI and by maintainers via `go generate`,
never inside an adopting workspace's own niwa instance.

**Data exposure.** No new telemetry, network call, or external transmission
is introduced. The new `EmitRank1Notice` mirrors `EmitRank2Notice`'s
existing shape: a plain informational stderr line naming the config source
identifier and a fixed instructional string, no secret material or file
contents.

**Secret-handling guardrails in skill guidance.** niwa already enforces a
public-repo plaintext-secret guardrail
(`internal/guardrail/githubpublic.go`), but it walks a specific, narrow set
of struct paths: `env.secrets` and `claude.env.secrets` (workspace-, repo-,
and instance-level). It does not walk `claude.settings` (`SettingsConfig`,
typed `map[string]MaybeSecret` precisely because it can carry vault-backed
values) or `vault.provider`/`vault.providers.*` config
(`VaultProviderConfig.Config`, untyped `map[string]any`). Both are blocks
the `edit-config` skill's Decision 2 walkthroughs explicitly cover ("wire a
secret," "add a plugin," provider setup). This means the codebase's
automated safety net does not reach two of the exact blocks this skill
teaches an agent to edit. `SKILL.md`'s procedures for `claude.settings` and
`vault.*` edits must therefore carry their own explicit guardrail language:
never write a literal secret value into those blocks; always use a
`vault://` reference for anything secret-shaped in `claude.settings`; and
for `vault.provider`/`vault.providers.*` config, only non-secret fields
(e.g., a project identifier) belong in `workspace.toml` at all -- actual
credentials are handled out-of-band by the provider's own CLI (e.g.
`infisical login`), matching the existing pattern documented in
`docs/guides/vault-integration.md`. This is a content requirement on Phase
4's `SKILL.md` deliverable, not a code or architecture change.

A Phase 6 security-verification review noted that `offendingKeys`'s
existing `walk()` helper already handles `map[string]MaybeSecret` (the
exact type `SettingsConfig` is), so extending it to also cover
`cfg.Claude.Settings` (at all four positions it appears: workspace,
per-repo, instance, global-override) would convert this from an
unenforced, `SKILL.md`-only backstop into an actual code-level guardrail,
for less implementation cost than other pieces of this design. This design
does not bundle that guardrail extension -- it changes shared,
security-relevant behavior for every `workspace.toml` author, not just
users of the new skill, which is a broader blast radius than this
design's `public/niwa`-scoped remit warrants folding in silently. It's
recorded here as a concrete, low-cost, recommended follow-up (see
Consequences), same treatment as the `[instance.files]` escape hatch and
the stale scaffold doc pointer.

**Install-mechanism integrity.** `plugin.Install`'s `stageAndRename`
(`internal/plugin/installer.go`) writes the full embedded tree to a `.next`
staging directory first, then promotes it into place via `os.Rename`
(atomic on POSIX), moving any existing install aside to `.prev` first and
rolling back on a failed promote. There is no window in which a
partially-written tree is visible at the install path. The manifest
version-bump discipline required for `edit-config` to reach
already-installed users is enforced by the functional test already planned
in Implementation Approach Phase 5 (asserting the `edit-config` skill file
exists in HOME post-install).

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
  `docs/designs/current/DESIGN-workspace-config.md` did: the generated
  reference is CI-diffed against `config.go`/`vault.go` on every PR, and
  the skill teaches an agent to consult that reference rather than owning
  a hand-copied schema.
- Schema changes become visible in the same PR that causes them -- the
  generated reference file shows up as a diff alongside any `config.go`
  change that touches it, rather than requiring a separate, easy-to-forget
  doc-sync pass.
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
- This design adds real, if modest, new infrastructure that didn't exist
  before it: a small AST-based generator package and a new CI job. An
  earlier pass of this decision explicitly avoided building this,
  preferring a live read; the checkout-availability finding that forced
  the restart means this cost is now required, not optional.
  Guidance quality also depends on `config.go`'s doc comments staying
  prose-quality -- a soft dependency, not an enforced one, though a
  degraded outcome (field/tag-only listings) is still accurate, just less
  explanatory.
- The soft preference for pre-release, before-a-niwa-upgrade self-service
  adoption is left unresolved by this design -- deferred, not solved.
- The public-repo plaintext-secret guardrail
  (`internal/guardrail/githubpublic.go`) still doesn't cover
  `claude.settings` or `vault.provider` config after this design ships --
  `SKILL.md`'s own guardrail language is the only backstop for those two
  blocks. A code-level fix is recorded as a follow-up (see Mitigations),
  not bundled here.

### Mitigations
- A new functional-test scenario asserts the `edit-config` skill's file
  (not just "plugin installed") exists in HOME after a rank-1 install and
  after a bootstrap `Create` call, catching both a dropped version bump and
  a re-broken bootstrap wiring path before it reaches users.
- The new CI freshness-diff job (Implementation Approach Phase 3) enforces
  that the generated schema reference can never silently drift from
  `config.go`/`vault.go`, the same enforcement discipline this repo
  already applies to `gofmt`.
- The `[instance.files]` early-access escape hatch, the plain `Scaffold()`
  template's stale doc pointer, and extending
  `internal/guardrail/githubpublic.go`'s `walk()` helper to cover
  `claude.settings` (converting the skill's prose-only secret guardrail
  into an enforced code-level one) are recorded here as explicit, named
  follow-up work (see Considered Options and Security Considerations)
  rather than silently dropped, so they're discoverable by a future
  contributor without re-deriving them from scratch.
