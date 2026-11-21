<!-- decision:start id="config-skill-delivery-mechanism" status="assumed" -->
### Decision: Delivery mechanism for the rank-1 config-editing skill

**Context**
niwa ships an embedded Claude Code plugin (`internal/plugin/files/niwa/`, installed to
`~/.claude/plugins/marketplaces/niwa/`) with one skill, `migrate-config`, whose install is
triggered exclusively by rank-2 (deprecated whole-repo config) detection, at four
duplicated call sites in `internal/workspace/apply.go` (~443, ~595, ~927, ~956). Rank-1
single-repo workspaces -- the normal, non-deprecated layout, and the population that needs
in-session guidance for editing `.niwa/workspace.toml` -- trigger nothing today. A new
config-editing skill needs a delivery path that reaches these workspaces, covering both new
adopters and repos that already have a `.niwa/workspace.toml` today.

Two already-shipped niwa mechanisms compete for this role. The embedded-plugin install
(`plugin.Install`, wired through `InstallNiwaPlugin`) already runs unconditionally on every
`niwa apply`/`niwa create`; rank is already a computed value at all four call sites, so
extending the trigger to include rank-1 is structurally additive. Separately,
`[instance.files]` (`internal/config/config.go`'s `InstanceConfig.Files`, materialized by
`internal/workspace/materialize.go`) verbatim-copies files from an adopting repo's own
`.niwa/` into its instance root on every apply, confirmed present in the repo's current
latest tag (v0.21.1) -- i.e. already available without a new niwa release.

Independent research for this decision confirmed niwa has no mechanism that pushes a
`workspace.toml` content change into an already-adopted repo's own committed file --
`.niwa/workspace.toml` is written only by `niwa init`'s scaffold functions, never by
`apply`. So any mechanism resting on an adopting repo's own `workspace.toml` declaring
something new is fundamentally a pull (opt-in, per-repo), while the embedded-plugin route,
once its trigger is extended, is a push (automatic, passive) that reaches every rank-1
workspace the next time its owner runs `niwa apply`/`niwa create` -- something they do
routinely regardless of this change.

**Assumptions**
- Workspace owners run `niwa apply`/`niwa create` as part of ordinary, routine usage, so a
  mechanism wired into that path constitutes genuine passive reach rather than requiring
  separate action. If a meaningful population of already-adopted rank-1 workspaces never
  re-runs `niwa apply` after adopting, this decision's reach guarantee weakens for that
  population specifically (though it still activates the first time they do).
- The manifest version-bump discipline (`internal/plugin/files/niwa/manifest.json`'s
  `version` field) will be enforced going forward by a functional test asserting the new
  skill's file exists in HOME after install, not merely that the plugin installed. If this
  discipline lapses on a future skill addition, `plugin.Install`'s idempotence check
  (bare string equality on `version`) silently withholds the update from already-installed
  users with no error surfaced.
- The `dangazineu/commuter`/`dangazineu/equity-planner` examples named in the originating
  brief remain unverified (unreachable via `gh` from this environment) and are treated as
  illustrative only. This decision is grounded in first-principles reading of niwa's own
  code, not on those specific repos existing as described, so the choice is unaffected if
  they turn out not to match the illustrative pattern.
- The sibling content-sourcing decision (decision 2, already confirmed: "live-source-
  grounded content," where the skill teaches an agent to read `internal/config/config.go`
  live rather than owning static schema prose) is compatible with this delivery mechanism
  without modification -- the skill ships as a `SKILL.md` file under
  `internal/plugin/files/niwa/skills/<name>/`, exactly like `migrate-config` does today.

**Chosen: Extend the embedded-plugin auto-install gate to rank-1**
Add a new, rank-1-gated branch alongside each of the four existing rank-2 branches in
`internal/workspace/apply.go` (~443, ~595, ~927, ~956). Each site already computes rank
(`teamConfigRank`/`overlayRank`) as a plain `int` before these blocks run, so the new
branch mirrors the existing one structurally: guard on a new notice-ID constant via
`sliceContains(disclosedNotices, ID)`, emit a notice, append to `disclosedNotices`, call
`a.InstallNiwaPlugin(nil, a.Reporter, a.SkipPluginInstall)`. No line inside any existing
rank-2 `if` block changes. The new config-editing skill ships as a second entry in
`internal/plugin/files/niwa/manifest.json`'s `skills` array, alongside `migrate-config`,
with a `version` bump so already-installed users' on-disk plugin (gated by a bare
string-equality version check in `plugin.Install`) actually picks up the addition.

The outcome-reporting notice a rank-1 install produces requires no new code: `plugin.Install`
already calls `EmitPluginNotice` with `NoticeIDPluginInstalled`/`NoticeIDPluginSkipped`
internally, unconditional on rank, inside the same shared install path both
`InstallNiwaPlugin` and the existing `niwa plugins install` manual command call. A
rank-1-specific pre-install notice (mirroring `EmitRank2Notice`'s shape) is added purely to
explain *why* rank-1 detection triggered an install, reusing the existing
`DisclosedNotices`/`noticeDisclosed`/`mergeDisclosedNotices` bookkeeping with zero new
state-tracking infrastructure.

A new functional-test scenario, adapted from `test/functional/features/config-source-rank2.feature`'s
existing two `@critical` scenarios (swap the rank-2 config-repo fixture for a rank-1 one;
assert stderr contains the new notice text and that both `manifest.json` and the new
skill's file exist in HOME; add a `--no-install-plugins` opt-out variant), covers this
change to the standard tsuku/niwa functional-test bar.

**Rationale**
This is the only one of the candidate mechanisms that satisfies "must reach already-adopted
single-repo workspaces" as a passive, automatic guarantee rather than a best-effort or
opt-in one -- and the constraint is phrased as "must," not "should ideally." The mechanism
it extends already runs unconditionally on every `apply`/`create`; adding a rank-1 branch
requires no adopting repo to edit or commit anything, and durably keeps covering new
adopters as they appear (unlike a one-time coordinated campaign against today's adopter
set, which would need to be re-run to cover repos that adopt rank-1 configs later). It also
best satisfies the preference for reusing an already-shipped mechanism: rather than
repurposing a general-purpose file-materialization mechanism built for a different job
(`.mcp.json` distribution), it extends the trigger for a mechanism that already does
exactly the needed job -- installing a skill into `~/.claude/`. The change is verified,
by direct source reading across four independent reviewers, to be fully additive: it shares
no mutated state with the rank-2 branches beyond an append-only notices slice, and
`plugin.Install`'s own idempotence makes a same-apply double-install (a rank-2 team-config
hit plus a rank-1 overlay hit, for example) a harmless no-op re-check rather than a
double-write. This satisfies "must not change rank-2 migration behavior" structurally, not
just by test coverage.

The trade-off knowingly accepted: this mechanism cannot, on its own, satisfy the soft
preference that a workspace owner adopt "without waiting on a niwa release" -- the skill's
content ships via `go:embed` inside the niwa binary regardless of which trigger delivers
it, so no implementation choice within this alternative closes that gap. A candidate
companion (documenting `[instance.files]`'s already-shipped materialization mechanism as an
early-adoption escape hatch, landing the skill inside the repo directory itself via a
nested destination like `"skills/" = "<group>/<repo>/.claude/skills/"`) was evaluated in
depth and rejected for this decision -- see Alternatives Considered.

**Alternatives Considered**
- **`[instance.files]` self-declared per adopting repo**: mechanically real (the
  materialization mechanism is already shipped in v0.21.1, and destination containment is
  checked only against escaping the instance root, not against nesting depth, so a
  workspace-specific nested destination does land inside the repo directory a session
  actually uses). Rejected for this decision's shipped scope because it is fundamentally a
  pull -- nothing in niwa rewrites an already-committed `workspace.toml`, so reach depends
  on a human or agent editing and committing that specific repo's file, whether
  self-service or via an external PR campaign -- and because the exact nested-destination
  pattern it depends on is explicitly disclaimed by the project's own
  `docs/guides/file-distribution.md` ("destinations stay at the project root... not for
  niwa-internal directories," repo-subdirectory sessions "not covered," with the separate
  `[files]` mechanism named as the intended per-repo tool). No test in the current suite
  covers this usage, so a future maintainer aligning the code with the documented
  limitation could silently break any repo relying on it. Retained as candidate future
  work -- gated on first reconciling that guide with what the code actually enforces, and
  evaluating whether `[files]` rather than `[instance.files]` is the properly-sanctioned
  mechanism for a per-repo early-access path -- not part of this decision's shipped change.
- **Notify-only, pointing at `niwa plugins install`**: cheapest and lowest-risk of the
  three by construction (a purely additive sibling notice block, no filesystem writes of
  its own), but rejected because it duplicates, rather than adds to, what the chosen
  mechanism already provides. `plugin.Install`'s existing `EmitPluginNotice` call already
  fires the same installed/skipped disclosure unconditional on rank, inside the shared
  install path -- so a distinct notify-only alternative supplies no capability Alternative
  1 doesn't already have for free. Taken alone it also cannot satisfy "must reach," since a
  notice a user doesn't act on delivers zero bits to disk.
- **Hybrid (plugin-gate extension + documented `[instance.files]` escape hatch)**:
  reconsidered after the `[instance.files]` doc-contradiction finding surfaced. What began
  as an apparently free pairing ("two independent, zero-overlap deltas") turned out to
  depend on documenting a pattern the project's own guide disclaims, with a separate,
  independently-disqualifying content-availability gap (the sibling content-sourcing
  decision's "live-source-grounded content" answer means there is no frozen artifact for an
  early adopter to copy before a release merges, and `[instance.files]` has no staleness-
  detection analog to the plugin route's manifest version check). Rejected as scope creep
  for this PR; noted as a candidate follow-up once the prerequisite doc/mechanism questions
  are resolved on their own track.

**Consequences**
`internal/workspace/apply.go` gains four new, additive `if rank == 1 && !sliceContains(...)`
blocks adjacent to the existing rank-2 ones. `internal/plugin/files/niwa/manifest.json`
gains a second `skills[]` entry and a required version bump. A new `SKILL.md` (whose
authorship and content strategy is decided separately) must exist under
`internal/plugin/files/niwa/skills/<name>/` for the manifest entry to resolve. Every
existing rank-1 workspace picks up the plugin -- and, by extension, both `migrate-config`
and the new config-editing skill -- automatically the next time its owner upgrades niwa and
runs `apply`/`create`, with no edit to that repo's own files required. Rank-1 users also
receive the `migrate-config` skill bundled alongside, whose description is entirely about a
rank-2 scenario that doesn't apply to them -- a minor plugin-browsing conceptual mismatch,
not a functional problem, since nothing forces its invocation. The manifest version-bump
step becomes a required, easy-to-silently-skip discipline for every future skill addition
to this plugin, not just this one -- the new functional test asserting the skill file's
presence in HOME (not just "plugin installed") is the safeguard against a repeat of that
failure mode escaping to production undetected.

This decision does not resolve, and explicitly does not need to resolve, the soft
preference for pre-release self-service adoption; that gap is left open, with a documented
path (fix `docs/guides/file-distribution.md`, evaluate `[files]` as the right per-repo
mechanism) for a future, separately-scoped decision if real demand for early access
materializes. This decision also does not resolve the separate, deferred question of
whether `niwa init --bootstrap`'s scaffold template should seed `[instance.files]` entries
for brand-new adopters -- that mechanism is additive to, not a substitute for, the
already-adopted-repo answer given here, since the scaffold path by construction never
re-fires for a repo that already has a `.niwa/workspace.toml` marker.
<!-- decision:end -->
