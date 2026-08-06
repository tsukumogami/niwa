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
