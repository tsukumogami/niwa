# Crystallize Decision: init-bootstrap-empty-source

## Chosen Type

Design Doc

## Rationale

Requirements came in clear from the user (the chicken-and-egg pain
described in the prompt, the preferred shape of "scaffold a minimal
config, stage on a branch in a worktree, let the user push"). What
exploration produced was not the feature description but a set of
technical decisions: where the fallback plugs into `init.go`, what
primitive performs the staging (a new lightweight helper, not the
existing session API), what the minimal scaffold contents are and how
they're derived from `--from` inputs, what CLI surface gates the
behavior (`--bootstrap` flag), and how adjacent failure modes get
typed-error treatment.

Those decisions need to live in a permanent document. Several were
contested during exploration (auto-fallback vs. flag, worktree primitive
shape, scaffold contents) and have rationale that will be load-bearing
for future contributors. A future reader needs to understand why
`workspace.StageInWorktree` is a new helper rather than a session
extension, and why `--bootstrap` is required rather than auto-falling
back on `NoMarkerError`.

## Signal Evidence

### Signals Present

- **What to build is clear, but how is not**: The `--bootstrap`-driven
  empty-source bootstrap is unambiguous as a feature description; the
  open work is in how it plugs into the materialize path, where the
  worktree lives, when the registry entry is written, and how the
  scaffold contents are derived from inputs.
- **Technical decisions need to be made between approaches**: Re-clone
  the source vs. intercept `materializeAndSwap` before its cleanup
  fires; reuse the session API vs. carve out a new
  `workspace.StageInWorktree`; commit-on-branch vs. leave working-tree
  dirty.
- **Architecture, integration, or system design questions remain**:
  Worktree home (cwd-relative? `~/.cache/niwa`?); registry entry
  timing; interaction with `--overlay` / `--no-overlay`; typed-error
  refactor in `internal/github/fetch.go`.
- **Exploration surfaced multiple viable implementation paths**: All
  six leads produced concrete option lists with trade-offs.
- **Architectural decisions were made during exploration that should
  be on record**: Rejecting silent auto-fallback for 404 ambiguity
  reasons; choosing a new lightweight worktree primitive over session
  reuse; minimal scaffold shape (active sources/groups, dropping
  `default_branch`, no pre-wired vault); fail-loud-with-hint policy
  for adjacent failure modes.
- **Core question is "how should we build this?"**: After Phase 1, the
  user-facing feature was settled (`--bootstrap` triggers
  scaffold+stage). Everything investigated since has been about how.

### Anti-Signals Checked

- "What to build is still unclear" — not present. The feature
  description is clean: empty-source bootstrap, opt-in flag, worktree
  handoff.
- "No meaningful technical risk or trade-offs" — not present.
  Materialize-path integration, worktree primitive carve-out, error
  taxonomy refactor all carry technical risk.
- "Problem is operational, not architectural" — not present.

## Alternatives Considered

- **PRD**: Partial fit (single coherent feature, exploration produced
  the requirement set). Ranked lower because requirements were largely
  given as input — the user came in with a concrete pain and a preferred
  shape ("scaffold a minimal config and stage it on a branch"). What
  exploration produced is technical decisions, not requirements
  consensus. The PRD-vs-Design-Doc tiebreaker (requirements identified
  by /explore vs. given to it) points to Design Doc.

- **Plan**: Ranked lower. No upstream PRD or design doc exists yet,
  and technical approach is still debated (worktree primitive shape,
  registry timing, push behavior). A Plan can't sequence work that
  hasn't been designed.

- **No Artifact**: Strongly demoted. Multiple architectural decisions
  were made during exploration (flag vs. auto-fallback, lightweight
  primitive vs. session extension, minimal scaffold contents). These
  need a permanent home — `wip/` is wiped on PR merge.

- **Decision Record**: Demoted. Multiple interrelated decisions need
  to be captured (CLI surface, worktree primitive, scaffold contents,
  error taxonomy). An ADR fits a single decision; a design doc fits
  this many.

- **Spike Report**: Demoted. Feasibility was never in doubt; the
  question was always "how should we build this" rather than "can we
  build this."

- **Rejection Record / VISION / Roadmap / Competitive Analysis**: Did
  not match. niwa exists, the feature is moving forward (not rejected),
  single feature (not multi-feature sequencing), public repo (rules
  out Competitive Analysis).

## Deferred Types

None matched.
