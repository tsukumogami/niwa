<!-- decision:start id="config-skill-content-strategy" status="confirmed" -->
### Decision: Config-editing skill content sourcing strategy

**Context**
The new config-editing skill needs to teach an agent how to extend an
already-adopted repo's `.niwa/workspace.toml` (add a hook, wire a secret, add
a plugin, add instance files) covering claude.* (including claude.hooks and
claude.settings, which have no dedicated guide today), env.*, vault.*, files,
and instance blocks. The failure mode to avoid is concrete and already in the
repo: `docs/designs/current/DESIGN-workspace-config.md`, despite `status:
Current`, is confirmed 2+ months stale (last content commit 2026-05-02,
today 2026-08-05) -- it documents a removed `[channels]` block, misplaces
`[hooks]`/`[settings]` nesting, and omits `[vault]`, `[claude.marketplaces]`,
`env_output`, `[instance]`, and `[root]` entirely. Nothing in the repo would
have caught this: the one doc-validation CI workflow checks artifact-doc
*format*, not content accuracy, and explicitly skips docs lacking a `schema:`
frontmatter key, which this doc lacks. `internal/config/config.go` changed 27
times in under 4 months, concentrated in the `[claude]` block and
file-distribution blocks; `vault.go` changed only twice in the same window.

**Assumptions**
- config.go's doc comments remain prose-quality (verified today: e.g.
  `ClaudeConfig` at config.go:22-27, `RootConfig` at config.go:265-271 explain
  rationale, not just restate field names) -- if a future contributor starts
  landing terse or missing comments, the "read config.go live" strategy
  degrades gracefully (an agent can still read struct tags) but loses some of
  its explanatory value.
- vault.go's low change rate (2 commits/4mo) continues -- if vault gains the
  same churn as the claude block, the carved-out guide-pointer for
  `vault-integration.md` should be revisited using the same live-read
  treatment as everything else.
- No `niwa config validate`/`lint`/`check` command exists today (confirmed by
  grep of internal/cli/*.go) -- if one is added later, the skill should be
  updated to invoke it as a closing step after edits, but that's a separate,
  additive change, not a blocker to this decision.

**Chosen: Live-source-grounded content, with a narrow guide carve-out for vault**
The skill's own SKILL.md prose stays short and procedural: it never restates
field names, defaults, or section shapes as text the skill owns. Instead it
teaches the agent *how to find and interpret* the ground truth at
invocation time:
1. Read `internal/config/config.go` (and `internal/config/vault.go`) to find
   the relevant struct (`WorkspaceConfig`, `ClaudeConfig`, `EnvConfig`,
   `VaultRegistry`, `InstanceConfig`, `RootConfig`) and read its doc comment
   and `toml` tags directly -- this can never be stale because it's not a
   copy, it's the thing itself.
2. Use `internal/workspace/scaffold.go`'s `scaffoldTemplate` as an
   illustrative starting shape for a new block, but explicitly instruct the
   agent to cross-check every field name it copies from there against
   config.go before using it -- because research found scaffoldTemplate has
   already drifted on a real field name (`project_id` at scaffold.go:93 vs.
   the actual `project` field), and its pinning test only does loose
   substring/parse checks, not byte-equality or field validation. Treat it as
   inspiration, never as verified truth.
3. For the five common-edit walkthroughs the constraints require (add a
   hook, wire a secret, add a plugin, add instance files, add a marketplace),
   write each as a short numbered procedure ("open struct X, append an entry
   shaped like Y, following the pattern at scaffold.go line Z") rather than
   as a worked TOML snippet frozen in skill prose -- the procedure describes
   *where to look and what shape to expect*, and the agent fills in exact
   current field spelling from its live config.go read in step 1.
4. One exception: for the `vault.*` block specifically, point the agent to
   `docs/guides/vault-integration.md` as the primary reference rather than
   requiring a full config.go read, because research independently verified
   every spot-checked field in that guide against `vault.go` today and the
   underlying code is empirically stable (2 changes in 4 months, versus the
   claude/file-distribution blocks' 27). This is the one place in the schema
   where a doc-pointer is lower-risk than round-tripping through source on
   every invocation.

**Rationale**
This directly satisfies the ruled-out-alternative constraint (no hand-copied
schema baked into skill prose) while being honest about the two things
research surfaced that the original constraints didn't fully account for.
First, scaffoldTemplate is not the safe, rot-proof artifact the constraints
assumed -- it's commented-out and loosely tested, and it has already drifted
on a real field name. Treating it as "cross-check before trusting" rather
than "authoritative worked example" closes that gap without discarding its
genuine value as the richest single illustration of the schema. Second,
two of the three existing guides (workspace-config-sources.md,
file-distribution.md) sit on top of exactly the code that changes fastest
(the `[claude]` block and file-distribution blocks) -- trusting them as the
skill's primary reference would just relocate the DESIGN-workspace-config.md
failure mode one level, since nothing prevents *those* docs from going stale
either. Only vault-integration.md sits on genuinely calm code, which is why
it alone earns a carve-out. Building a generation/sync mechanism (go:generate
or a CI content-diff check) was considered and rejected as out of scope: it
requires new infrastructure this repo doesn't have today (confirmed: no
go:generate anywhere, the one doc CI check validates format not content and
skips schema-less docs like the one that went stale), and even if built it
wouldn't fully solve the problem, since curating which comments become
walkthrough prose still needs a human or agent in the loop.

**Alternatives Considered**
- **Hand-written schema, hand-maintained**: identical in structure to
  DESIGN-workspace-config.md, which is already proven to drift within months
  on a repo where config.go changes roughly weekly. Rejected by the decision's
  own constraints.
- **Guides-first with config.go fallback for gaps (full version)**: reuses
  more existing prose (three guides instead of one), less to write up front.
  Rejected because two of the three guides map to the fastest-changing parts
  of the schema (`[claude]` and file-distribution), so trusting them as
  primary reference reintroduces the same unmonitored-drift risk this
  decision exists to avoid -- confirmed accurate today doesn't mean confirmed
  accurate in three months, and nothing in the repo checks that. The
  vault-mapped third of this alternative survives as a carve-out in the
  chosen option.
- **Generation/sync mechanism (go:generate or CI content-diff)**: would most
  durably close the drift loop if it worked, but requires building
  infrastructure that doesn't exist in this repo today (no go:generate
  anywhere; the existing doc-format CI check explicitly skips docs without a
  `schema:` key) and doesn't fully solve the curation problem even once
  built. Reasonable future work, out of scope for "what should the skill's
  content be sourced from."

**Consequences**
The skill becomes cheaper to keep correct -- there's no schema prose to
update when config.go changes, because the skill never states schema facts
as its own claims. The trade-off: each invocation costs a live read of
config.go (and sometimes scaffold.go), so the skill is slightly slower and
more verbose per-use than a skill that could just quote a static reference,
and the quality of its guidance depends on config.go's doc comments staying
prose-quality, which is a soft dependency rather than an enforced one. The
vault carve-out means the skill has two different content-sourcing behaviors
depending on which block is being edited -- this needs to be stated
explicitly in the skill so a future maintainer doesn't "clean it up" into a
single mode without understanding why vault gets different treatment. If
vault.go's change rate increases later, that carve-out should be revisited.
<!-- decision:end -->
