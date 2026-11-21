# Exploration Findings: niwa-config-skill

## Core Question

Single-repo niwa workspaces (commuter, equity-planner) commit a hand-authored
`.niwa/workspace.toml` directly into the adopting repo. An agent later working
inside that repo's own niwa instance -- with no access to the tsukumogami org's
private context -- may need to extend that config (add a hook, wire a secret,
add a plugin, add instance files). Today it has no in-session guidance on the
config schema; the only trail is doc-link comments pointing at
`docs/guides/workspace-config-sources.md` and `docs/guides/vault-integration.md`
in the niwa repo, which the agent has to go discover and fetch cold. What
mechanism should ship this guidance into single-repo workspaces, and what
should that guidance contain?

## Round 1

### Key Insights

- **The embedded plugin's install gate is rank-2-only, at 4 duplicated call
  sites, and rank-1 sources trigger nothing today.** `internal/workspace/apply.go`
  lines 443, 595, 927, 956 -- all inside rank-2-detection conditionals. There
  is no partial rank-1 trigger to build on. (`lead-plugin-install-gate`)
- **`niwa plugins install` already exists as a rank-agnostic, unconditional
  manual-install path** (`internal/cli/plugins.go`) that sidesteps the rank-2
  gate entirely -- the cleanest existing lever if the goal is "make the plugin
  reachable regardless of rank." (`lead-plugin-install-gate`)
- **Adding a second skill to the existing plugin is mechanically trivial**:
  drop `skills/<name>/SKILL.md` under `internal/plugin/files/niwa/`, add an
  entry to `manifest.json`'s `skills` array, no Go changes needed (`go:embed`
  and the installer walk the whole tree). The plugin was deliberately named
  bare `niwa`, not `niwa-migration`, specifically to accommodate future
  niwa-owned skills. (`lead-migrate-config-skill-pattern`)
- **The real blocker is the install trigger, not the skill-authoring
  pattern.** Landing a new skill in the existing plugin doesn't help rank-1
  workspaces unless the install gate also changes. (`lead-migrate-config-skill-pattern`,
  `lead-plugin-install-gate`)
- **`[instance.files]` is a real, activated mechanism** (not the dead field
  it once was) that verbatim-copies files or whole directories from `.niwa/`
  into each instance root on every `niwa create`/`niwa apply`, with
  drift-tracked cleanup on removal. `"skills/" = ".claude/skills/"` is
  mechanically supported (directory-copy confirmed by
  `TestMaterializeVerbatimFilesDirSourceVerbatim`). No enforced guard blocks
  `.claude/` as a destination in the main workspace.toml. Niwa's own
  `root_materializer.go` proves the `.claude/skills/<name>/SKILL.md` target
  shape works with Claude Code -- it's how niwa ships its own `dispatch`
  skill, just via Go-embed rather than workspace.toml-authored config.
  (`lead-instance-files-mechanism`)
- **`[instance.files]` for skill delivery is a documented, well-supported,
  but unprecedented use of the mechanism** -- every existing example (docs,
  tests, the activating PRD) is `.mcp.json`. Nothing in-repo uses it for
  `.claude/skills/` today. (`lead-instance-files-mechanism`)
- **The `niwa init --bootstrap` scaffold is a byte-equality-contract
  template** (`internal/workspace/scaffold.go:149-169`) that references no
  plugin, skill, or `[instance.files]` entry today, and only fires once, at
  first adoption -- it is structurally incapable of reaching already-adopted
  workspaces like commuter and equity-planner. Changing it only affects
  future adopters. (`lead-bootstrap-scaffold`)
- **A working precedent for "auto-install an embedded plugin/skill with
  opt-outs and idempotency" already exists** (the rank-2 trigger) and could
  be generalized to a bootstrap-time or rank-1 trigger instead of reinventing
  the delivery mechanism from scratch. (`lead-bootstrap-scaffold`)
- **Schema drift risk is real and concentrated, not uniform.**
  `internal/config/config.go` (`WorkspaceConfig`, `ClaudeConfig`, `EnvConfig`,
  `InstanceConfig`) changed 27 times in ~4 months; `vault.go` changed only
  twice in the same window. `docs/designs/current/DESIGN-workspace-config.md`
  -- marked `status: Current` -- is the concrete cautionary tale: 2+ months
  stale, documents a `[channels]` block removed from the codebase, misplaces
  `[hooks]`/`[settings]` at the wrong nesting level, and omits `[vault]`,
  `[claude.marketplaces]`, `env_output`, `[claude.content]`, `[instance]`,
  and `[root]` entirely. (`lead-workspace-toml-schema`)
- **No single doc is schema-complete for the skill's exact use cases.**
  `workspace-config-sources.md` and `vault-integration.md` are accurate and
  thorough for what they cover (discovery/rank, env-policy, marketplaces;
  vault/secrets) but neither documents `[claude.hooks]` or `[claude.settings]`
  structurally -- exactly the "add a hook" use case the skill needs to teach.
  (`lead-workspace-toml-schema`)
- **The scaffold template (`scaffold.go`) is the most reliably current
  worked example in the repo** -- it's exercised by `niwa init` and its own
  tests, so it can't silently rot the way a prose doc can. It already
  demonstrates every block the skill needs (hooks, secrets, plugins, files,
  instance, vault). (`lead-workspace-toml-schema`)
- **The dispatch brief's specific "live example" claim about commuter's
  `[instance.files]` usage is unverifiable and likely illustrative, not a
  real repo this environment can see.** `dangazineu/commuter` and
  `dangazineu/equity-planner` don't resolve via `gh repo view`, `gh api`, or
  GitHub search under the authenticated account, and don't appear among that
  account's real repos. This claim should not be restated as verified fact
  in downstream artifacts. (`lead-live-workspace-examples`)

### Tensions

- **Delivery mechanism candidates pull in different directions on reach vs.
  precedent.** The embedded plugin (extending the rank-2-triggered install,
  or generalizing it) reaches workspaces on every apply and matches an
  existing pattern, but its current gate is wrong for rank-1 and would need
  real code changes to fire correctly. `[instance.files]` reaches new and
  existing single-repo workspaces alike (any workspace author can add the
  entry, retroactively, without a niwa code change) and is mechanically
  proven, but it's an unprecedented use of the mechanism for skill delivery
  and requires each adopting repo's `workspace.toml` to opt in explicitly.
  Bootstrap-scaffold seeding only reaches brand-new adopters, leaving
  commuter/equity-planner-style existing workspaces permanently uncovered
  unless paired with something else.
- **Static schema reference vs. drift risk.** The brief's own open question
  ("static schema reference... versus something that stays in sync") maps
  directly onto the concrete evidence of `DESIGN-workspace-config.md`'s
  staleness -- a static copy in the skill would likely repeat that failure.
  But no existing doc is complete enough to just link to for the hooks/
  instance-files use cases specifically. The scaffold template threads this
  needle: less likely to drift (test-enforced), and already covers the
  needed blocks.

### Gaps

- Whether a `niwa config validate` or similar dry-run/lint command exists
  that a skill could invoke after an edit, closing the drift-risk loop
  without a fully-current static reference, was not investigated.
- `docs/guides/file-distribution.md` (the third guide, covering `[files]`/
  `[instance]`/`[root]` in depth) was not read in full by any lead --
  directly relevant to the "add instance files" use case.
- No real single-repo workspace example was confirmed reachable to ground
  "common edits" content in actual usage; the brief's named examples are
  unverifiable from this environment.
- Whether `docs/dot-niwa` or another repo needs a companion change is
  unresolved (out of scope for this dispatch per its guardrails; note only).

### Decisions

- **Rule out bootstrap-scaffold-only delivery.** It can never reach
  already-adopted single-repo workspaces (commuter/equity-planner-style),
  which the brief identifies as the primary target population needing this
  guidance today. It may still be worth pairing with another mechanism to
  seed new adopters, but it cannot be the sole delivery path.
- **Rule out a bare static schema copy baked into the skill.** Concrete
  in-repo evidence (`DESIGN-workspace-config.md`'s staleness) shows this
  fails within months for the `[claude]` and file-distribution blocks
  specifically, which are exactly the blocks the skill needs to teach.
- **Treat the brief's commuter/equity-planner "live example" claims as
  unverified/illustrative** in downstream artifacts rather than restating
  them as confirmed fact.
- **Defer the final delivery-mechanism choice (embedded plugin vs.
  `[instance.files]` vs. some combination) to the design phase.** The
  evidence gives real trade-offs on each side (reach vs. precedent, opt-in
  vs. automatic) rather than a single obvious winner -- this is exactly the
  kind of "design decision where reasonable people could disagree" that
  `/shirabe:design` exists to resolve, not something to force in exploration.

### User Focus

Running in `--auto` mode with no live user for this round; the narrowing
choice was made via the research-first decision protocol (see Decisions
above) rather than an interactive question.

## Accumulated Understanding

Single-repo niwa workspaces need in-session guidance for editing
`workspace.toml`, and today they get none -- the one existing skill
(`migrate-config`) is scoped to rank-2 migration, and its install path never
fires for the rank-1 workspaces that are the normal case here. Two credible
delivery mechanisms emerged from research, each mechanically proven but each
requiring a real decision about install triggers or opt-in and reach:
extending/generalizing the embedded-plugin install gate (which currently
fires only on rank-2 detection, at four duplicated call sites), or using the
`[instance.files]` mechanism to materialize a skill file into
`.claude/skills/` from `workspace.toml` itself (unprecedented for this
purpose but functionally proven and unguarded). Bootstrap-scaffold seeding
alone was ruled out as a sole mechanism since it can never retroactively
reach already-adopted workspaces.

On content, the evidence rules out a bare static schema copy -- the repo's
own `DESIGN-workspace-config.md` is a live cautionary tale of exactly that
failure mode, stale and actively wrong within months on the same blocks
(`[claude.hooks]`, `[claude.settings]`, file-distribution) the new skill
needs to teach. The safest content anchor is the scaffold template
(`internal/workspace/scaffold.go`), which is test-enforced and already
demonstrates every needed block, paired with pointers to
`internal/config/config.go` (ground truth) and the existing guides for what
they do cover well (vault/secrets, discovery/rank, marketplaces). The
"common edits" content cannot be grounded in the brief's named live examples
(commuter, equity-planner) since those repos are unreachable/unverifiable
from this environment and should be treated as illustrative, not confirmed.

This is a medium-complexity problem: the goal is clear, but the delivery
mechanism has multiple viable approaches with real trade-offs that
reasonable people could resolve differently, and the content-shape question
similarly benefits from a documented technical decision. That points toward
a Design Doc as the next artifact, evaluating the mechanism options with
structured trade-off analysis, followed by a Plan to decompose the chosen
approach into issues.

## Decision: Crystallize
