<!-- decision:start id="config-skill-content-strategy" status="assumed" -->
### Decision: Config-editing skill content sourcing strategy (RESTART)

**Context**
The config-editing skill must teach an agent how to extend an
already-adopted repo's `.niwa/workspace.toml` (add a hook, wire a secret,
add a plugin, add instance files) covering `claude.*` (including
`claude.hooks` and `claude.settings`), `env.*`, `vault.*`, `files`, and
`instance` blocks. The previous decision chose to have the skill instruct
an agent to read `internal/config/config.go` and `internal/config/
vault.go` live at invocation time, on the reasoning that reading the
actual source can never be stale the way a hand-copied schema (like
`docs/designs/current/DESIGN-workspace-config.md`, confirmed 2+ months
stale) inevitably becomes. A Phase 6 security-verification review found
this breaks for the population the skill exists to serve: per
`docs/guides/workspace-config-sources.md`'s "Single-repo workspace"
section (verified directly, lines ~516-555), `niwa init --from owner/repo`
materializes ONLY the `.niwa/` subtree of the *adopting* repo (commuter,
equity-planner, etc.) into the workspace snapshot -- "the rest of the
repo ... is never fetched." niwa's own source code lives in a completely
separate repository that is never checked out inside a rank-1 adopter's
niwa instance. "Read config.go live" has nothing to read there.

Direct verification during this restart surfaced a second instance of the
same bug the original decision didn't catch: its own vault carve-out,
which pointed the skill at `docs/guides/vault-integration.md` as the
primary reference for `vault.*`, has the identical flaw -- that guide also
lives in the niwa repo's `docs/guides/`, equally absent from a rank-1
adopter's instance. Any content-sourcing strategy that assumes a niwa
checkout is on disk is broken for the target population, not just the
config.go-specific one the reviewer flagged.

**Assumptions**
- config.go's and vault.go's doc comments remain prose-quality going
  forward, since a generator's output quality depends on it. If comments
  degrade to terse/absent, the generated reference degrades to
  field/tag-only listings (still structurally accurate, just less
  explanatory) rather than becoming wrong.
- A new CI job that re-runs the generator and diffs its output against
  git is in scope for this design, since it's what turns "generated once"
  into "enforced never-stale," mirroring the existing gofmt/go vet
  enforcement pattern already in `.github/workflows/test.yml`. Even
  without this job, the generated file starts accurate and only degrades
  if a later config.go change ships unregenerated -- strictly better than
  a hand-copy from day one, but materially better with the CI gate.
- This decision was reached in --auto mode without user confirmation
  (per the decision-block-format status rule, this alone is sufficient to
  mark status `assumed` even though the evidence itself was conclusive,
  not contested).

**Chosen: Build-time-generated reference, embedded via go:embed, CI-freshness-enforced**
The skill's authoritative schema content is no longer "read niwa-repo
files live" in any form -- neither source nor guides. Instead:

1. A new small internal generator (e.g.
   `internal/configschema/gen/main.go`, wired via a `//go:generate`
   directive near `internal/config/config.go`) walks the config package's
   AST using the Go standard library's `go/ast` and `go/doc` packages (no
   new external dependency -- verified nothing like this is imported
   anywhere in the repo today, so this is genuinely new but minimal
   infrastructure) and emits a structured markdown reference covering
   every struct the skill needs to teach: `WorkspaceConfig`,
   `ClaudeConfig`, `ClaudeOverride`, `HooksConfig`, `SettingsConfig`,
   `ClaudeEnvConfig`, `EnvConfig`, `VaultRegistry`,
   `VaultProviderConfig`, `InstanceConfig`, `RootConfig`, and the
   file-distribution blocks -- field name, `toml` tag, and doc comment,
   verbatim from source. Doc comments in this codebase are already
   prose-quality (verified directly: `ClaudeConfig`, `WorkSummaryHooks`,
   `PrBodyHook` all explain rationale, not just restate field names), so
   the generated output carries real explanatory value, not a bare field
   list.
2. The generated output is committed to git at
   `internal/plugin/files/niwa/skills/edit-config/reference/schema.md`
   (exact path TBD in implementation), which ships to every niwa
   instance via the identical `go:embed` mechanism `SKILL.md` already
   uses today (verified: `internal/plugin/embed.go` embeds the whole
   `internal/plugin/files/niwa/` tree with no per-file allowlist, so
   adding a second file requires zero delivery-mechanism change and
   stays entirely inside Decision 1's already-finalized scope).
3. A new CI job re-runs the generator and does a `git diff --exit-code`
   against the committed file, run alongside the existing `go vet ./...`
   and `go test ./...` jobs in `.github/workflows/test.yml`. Any PR that
   changes `config.go` or `vault.go` without regenerating the reference
   fails CI -- the same enforcement discipline this repo already applies
   to `gofmt`. This bounds drift to zero at merge time, tighter than the
   "at most one release cycle" the investigation prompt initially
   floated, because the check runs on every PR, not just at release cut.
4. The generated file must be committed to git, not produced fresh at
   release-build time: `.goreleaser.yaml` runs a plain `go build` with no
   `before.hooks` codegen step, and `go build ./cmd/niwa` /
   `go test ./...` (every ordinary build and CI entry point, not just
   releases) would break if the embedded file weren't present in a plain
   checkout. Committing it keeps every existing build path working
   unmodified.
5. `vault.*` folds into this same generated reference instead of keeping
   a special-cased guide pointer -- the original decision's carve-out is
   no longer needed once the sourcing mechanism itself is
   checkout-independent; there's no reason to treat vault differently
   from claude/env/instance now that all of them are equally
   bundle-embedded.
6. SKILL.md's own prose stays exactly as procedural as the original
   decision intended: short numbered walkthroughs ("add a hook", "wire a
   secret", "add a plugin", "add instance files", "add a marketplace")
   that point into the generated reference for the authoritative current
   field names, rather than restating schema facts as text the skill
   owns or reading source live.
7. Optional enhancement, not required for correctness: SKILL.md may
   additionally instruct that if a niwa checkout happens to be
   detectable on disk (e.g. running the skill while developing niwa
   itself), the agent may cross-check the bundled reference against live
   `config.go` for the freshest possible view. This covers the narrower
   niwa-contributor population without weakening the guarantee for the
   universal rank-1 population, which never depends on this branch
   firing.

**Rationale**
Only two kinds of content are actually guaranteed present inside an
arbitrary rank-1 adopter's niwa instance: whatever ships bundled inside
the plugin via the already-finalized go:embed delivery, and whatever the
`niwa` binary itself can produce at runtime. Every alternative that reads
a niwa-repo file live -- source or guide -- fails the first test, which is
exactly the bug this restart exists to fix; it doesn't matter whether the
file in question is `config.go` or `vault-integration.md`, since both are
equally absent from the target population's checkout. A CLI-introspection
command (`niwa config schema --json`) does satisfy the availability
requirement, since the binary is always present, but Go reflection cannot
recover doc comments -- only AST-level parsing at a time source is
available (build/generate time) can, and this codebase's doc comments are
exactly what makes the guidance more than a bare field list. Building new
CLI surface to get a strictly worse (comment-free) result than a
generator already gives for less delivery risk isn't a good trade.
Extending the guides with new content-accuracy CI tooling was reconsidered
under the restart's premise and doesn't survive it: `docs/guides/` is as
unavailable to the target population as `config.go` is, so no amount of
CI enforcement on guide content fixes the fact that the skill can't reach
the guide from inside a rank-1 instance in the first place. The generated
committed reference is the one candidate that satisfies drift-resistance
(CI-diffed, tighter than the original decision's live-read guarantee,
which had zero staleness by construction but only worked for a narrower
population), availability (ships identically to SKILL.md, zero new
delivery mechanism), and content quality (keeps the prose value config.go
already carries) all at once.

**Alternatives Considered**
- **New `niwa` CLI introspection command** (e.g. `niwa config schema
  --json` via reflection over the live struct types): satisfies
  availability -- the binary is always present -- but reflection cannot
  recover Go doc comments, so it would regress guidance quality below
  what config.go's comments already provide, and requires new
  user-facing CLI surface (flags, output format, tests, docs) to do
  strictly less than the chosen generator does for less risk. Rejected;
  noted as reasonable future work if a runtime-introspection need
  independent of this skill emerges later.
- **Context-branching (live read if a checkout is detectable, else
  fallback)**: not rejected outright -- folded into the chosen option as
  an optional enhancement (step 7) for the narrower niwa-contributor
  population, since it costs nothing and the fallback it needs is the
  chosen generated reference anyway. Not viable as a standalone strategy,
  because the universal rank-1 population never has the checkout branch
  fire, so a working fallback is mandatory regardless.
- **Extend existing guides + new CI content-accuracy tooling** (the
  vault-integration.md carve-out pattern, generalized and enforced): this
  is what the original decision effectively already did for `vault.*`,
  and the restart's own trigger finding shows it fails the identical
  checkout-availability test config.go failed. Adding content-accuracy CI
  enforcement would close the *drift* gap the guides have always had, but
  does nothing about the more fundamental *reachability* gap -- the skill
  running inside a rank-1 adopter's instance has no path to
  `docs/guides/workspace-config-sources.md` at all, accurate or not.
  Rejected; the guides remain useful as source material a human or the
  new generator's author reads once, and as general documentation for
  people browsing the niwa repo directly, just not as the skill's live
  reference target.
- **Hand-written schema, hand-maintained** (carried forward from the
  original decision, still rejected on the same grounds): identical in
  structure to `DESIGN-workspace-config.md`, already proven to drift
  within months on a repo where config.go changes roughly weekly.

**Consequences**
The skill gains a genuinely new build artifact and a new CI job that
didn't exist before this decision -- a small generator package
(`go/ast`/`go/doc`-based, stdlib only) and a freshness-diff check
alongside the existing `go vet`/`go test` gates. This is real, if modest,
new infrastructure the original decision explicitly avoided building; the
restart's finding that live-read is non-viable for the target population
changes that calculus, since the alternative to "build a small generator"
is no longer "read source live" (which doesn't work here) but "ship
something hand-copied and already-proven-to-drift" or "ship nothing and
regress to a worse-quality reflection-based command." The generated
reference file becomes a second embedded artifact reviewers see diffs for
in every PR that touches the config schema, which is a net legibility
win -- schema changes become visible in the same PR that causes them,
rather than requiring a separate doc-sync pass. The vault-specific
carve-out from the original decision is removed as unnecessary complexity
now that all schema areas share one sourcing mechanism, simplifying the
skill's own prose (no more "except for vault, which works differently").
The optional live-checkout-detection enhancement means niwa's own
contributors get marginally fresher guidance than the bundled reference
in the rare case they're editing config.go and the skill in the same
session, at no cost to the correctness guarantee for the universal
population, which the enhancement never gates on.
<!-- decision:end -->
