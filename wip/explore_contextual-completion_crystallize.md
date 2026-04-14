# Crystallize Decision: contextual-completion

## Chosen Type

Design Doc

## Rationale

Requirements are clear (Lead 2's coverage map specifies 14 identifier
positions across 10 commands and identifies the 3 data sources behind
them). The engineering question is entirely about *how*, not *what*. The
exploration surfaced several design decisions that need a permanent home
before `wip/` is cleaned on PR merge:

- Disambiguation policy for `niwa go [target]` (Option B: union with
  TAB-decorated kind, over Options A/C/E)
- No caching layer, grounded in Lead 3's measurements that pin the 100ms
  risk only to pathological walks
- Extraction of `EnumerateRepos(instanceRoot)` as a shared helper (Lead 2)
- Flag-dependent completion for `niwa go -r` reading the resolved `-w`
  value from `cmd.Flag()`
- No-friction completion for destroy/reset per CLI precedent
- Two-tier test strategy (unit + functional via `niwa __complete`)
- No install-path changes needed — both paths already ship completion

A design doc captures these as a single coherent record. `/plan` can then
decompose the work; `/implement` consumes the plan. The standard
exploration -> design -> plan -> implement pipeline applies.

## Signal Evidence

### Signals Present

- **What to build is clear, but how to build it is not**: Lead 2 defined
  the scope (11 of 14 positions in v1); the open questions are all about
  mechanism (decoration protocol, helper extraction, flag interdependency,
  test tiers).
- **Technical decisions need to be made between approaches**: Options A/B/C
  for disambiguation (Lead 4); caching vs no caching (Lead 3); destroy/reset
  UX policy (Lead 2); shared helper placement (Lead 2).
- **Architecture, integration, or system design questions remain**:
  `EnumerateRepos` extraction point; `cmd.Flag()` read pattern inside
  completion closures; install-path staleness boundary (Lead 6).
- **Exploration surfaced multiple viable implementation paths**: Lead 4
  explicitly scored four disambiguation options.
- **Architectural or technical decisions were made during exploration that
  should be on record**: Option B, no caching, 3-helper pattern, test tier
  structure, install-path non-requirement. These vanish on `wip/` cleanup
  unless captured.
- **Core question is "how should we build this?"**: The user's question
  framed the feature as a behavior already agreed-on; the exploration
  focused on delivery mechanics.

### Anti-Signals Checked

- **What to build is still unclear**: Not present. Lead 2's table is
  precise.
- **No meaningful technical risk or trade-offs**: Not present. The `go -r`
  flag interdependency, the destroy/reset footgun, and the decoration
  cross-shell variance are real trade-offs.
- **Problem is operational, not architectural**: Not present. The work
  extends the cobra command tree and the `internal/workspace/` package.

## Alternatives Considered

- **Plan**: Score 1 after demotion (one anti-signal: open architectural
  decisions need to be made first). Several concrete decisions were taken
  during exploration — writing a plan without first recording them in a
  design doc would leave future contributors to rediscover the rationale
  when Lead 4's alternatives resurface.

- **PRD**: Score -1 after demotion (one anti-signal: requirements were
  given as input). The user's request specified the feature directly; no
  requirements emerged during exploration that need stakeholder alignment.

- **No Artifact**: Score -1 after demotion (one anti-signal: architectural
  decisions were made during exploration that must be recorded). The scope
  is arguably small enough for direct implementation, but the decisions
  listed in the Rationale section need permanent capture.

- **Decision Record**: Not scored formally. A decision record fits a
  single architectural choice; this exploration produced several
  intertwined ones (disambiguation, caching, helper location, test tier).
  A design doc is the right granularity.

## Deferred Types

None applicable.
