# Exploration Decisions: init-bootstrap-empty-source

## Round 1

- **Scope confined to the empty-source case (rank-1 path, repo has at
  least one commit, `.niwa/` absent or empty).** Why: user explicitly
  scoped the primary feature here. Adjacent failure modes get fail-loud
  hints, not auto-scaffold.

- **The worktree handoff is the only confirmation gate.** Why: user
  said "land the changes in a branch, print the location in the output
  and leave it to the user to decide what to do next." No additional
  push step inside niwa; no auto-push.

- **Minimal-ideal scaffold proposed by niwa (not interactive).** Why:
  user said "propose the minimal ideal setup." No prompts to choose
  vault/plugins/etc — those are user follow-up work in the worktree.

- **Adjacent failure modes (malformed config, auth, 404 missing,
  rank-2, etc.) handled separately from the primary feature.** Why:
  user said "research these scenarios and propose solutions, but the
  main use case I want is the empty repo." Rank-2 already works;
  others fail-loud with hints — not part of the bootstrap feature
  surface.

- **Trigger model: require explicit `--bootstrap` flag (no silent
  auto-fallback).** Why: GitHub 404 ambiguity (private/empty/missing
  all look the same) plus typo-resolves-to-different-empty-repo risk
  rule out silent fallback. The flag matches niwa's existing
  `--feature` / `--no-feature` idiom (4 prior pairs) and the
  "explicit user intent → loud, convention → silent" gradient. In a
  TTY without the flag, niwa prompts; in non-TTY, niwa fails fast
  with a remediation hint pointing at `--bootstrap`.
