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
