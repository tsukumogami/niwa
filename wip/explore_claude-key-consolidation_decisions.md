# Exploration Decisions: claude-key-consolidation

## Round 1

- **Migration policy**: accept both `[content]` and `[claude.content]`
  for N releases with a deprecation warning (the user confirmed this
  during scoping, not during convergence — it was a pre-decided input,
  recorded here for the design handoff).
- **Per-repo override shape**: recommend splitting `ClaudeConfig` into
  a full form (workspace-level, with `Content` and `Marketplaces`) and
  a narrower `ClaudeOverride` (no `Content`, no `Marketplaces`) used
  by `RepoOverride.Claude`, `InstanceConfig.Claude`, and
  `GlobalOverride.Claude`. Rationale: the TOML decoder auto-surfaces
  `[repos.<name>.claude.content]` as an "unknown field" warning — zero
  extra validation code, self-documenting at the type layer.
- **Scope stays tight to the rename.** `workspace.content_dir` renaming
  and the question of whether `InstanceConfig.Claude` should grow its
  own `Content` field are flagged for the design doc to resolve, not
  for separate exploration rounds.
- **Artifact type**: design doc. Small, linear, but has three
  design-worthy architectural decisions (deprecation mechanic, type
  split, naming symmetry) that warrant a permanent record before
  `wip/` is cleaned.
