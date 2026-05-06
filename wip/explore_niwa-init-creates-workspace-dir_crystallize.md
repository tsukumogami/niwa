# Crystallize Decision: niwa-init-creates-workspace-dir

## Chosen Type

Design Doc

## Rationale

The exploration produced concrete architectural choices that need to live in
permanent documentation:

- **Name-override mechanism**: option (b) — extend `InstanceState` with a
  name-override field — was chosen over (a) in-place toml rewrite (rejected:
  dirties cloned config) and (c) registry-only override (rejected: leaves
  user-visible inconsistency in `niwa status`/`niwa apply`).
- **Pre-flight check shape**: caller-side existence check producing a new
  `ErrTargetDirExists` sentinel; `CheckInitConflicts(targetDir)` signature
  unchanged. Chosen over a `CheckInitConflicts(parent, name)` signature because
  it cleanly separates filesystem pre-gates from niwa-state validation.
- **Scope decision**: `niwa create` shares the same papercut but is
  intentionally deferred to a follow-up issue to keep this change small.
- **Backward-compat strategy**: clean breaking change, no `--in-place` /
  `--here` escape hatch. Justified by pre-1.0 status and user-visible
  remediation message.

These are architectural and product-shape decisions, not implementation detail.
A future contributor reading `niwa init`'s code in six months needs to know why
`InstanceState` has a name override field, why the existence check sits in
`init.go` instead of `preflight.go`, and why `niwa create` doesn't follow the
same convention. `wip/` is cleaned before merge, so the decisions need a
permanent home.

The exploration was short (1 round) and produced no requirements gaps — the
"what to build" question was given, not discovered. The "how" question was
the load-bearing one, and that's exactly what a Design Doc is for.

## Signal Evidence

### Signals Present (Design Doc)

- **What to build is clear, but how to build it is not**: requirements were
  given by the user; exploration focused entirely on implementation shape
  (override mechanism, preflight semantics, ripple effects).
- **Technical decisions need to be made between approaches**: three named
  options for name-override (a/b/c), two for preflight check location, two
  for niwa-create scope.
- **Multiple viable implementation paths**: research evaluated them and
  recommended winners with stated trade-offs.
- **Architectural/technical decisions made during exploration that should be
  on record**: option (b), caller-side preflight, defer-niwa-create — all
  need permanent capture.
- **Core question is "how should we build this?"**: confirmed.

### Anti-Signals Checked

- **What to build is still unclear**: not present. User stated requirements
  clearly upfront and answered scope-narrowing questions directly.
- **No meaningful technical risk or trade-offs**: not present. Real
  trade-offs evaluated for name-override and preflight shape.
- **Problem is operational, not architectural**: not present. State-shape
  changes (`InstanceState` field) and error-flow changes (new sentinel)
  are architectural.

## Alternatives Considered

- **PRD**: Ranked lower (demoted by anti-signal). Requirements were given as
  input by the user, not discovered through exploration. The "what" question
  was already answered; only the "how" needed work.
- **Plan**: Ranked lower. Small feature scope makes Plan a candidate, but
  the disambiguation rule applies: architectural decisions were made during
  exploration and have no permanent home yet. A design doc must come first;
  the plan can decompose the doc into issues afterward (likely 1-2 issues
  given the scope).
- **No artifact**: Ranked lower (demoted by anti-signals). Decisions made
  during exploration would be lost when `wip/` is cleaned at merge time.
  Public OSS repo means future contributors need to understand why the
  implementation looks the way it does.

## Deferred Types

None applied. No spike (feasibility was never in question), no decision
record (multiple decisions were made, not a single isolated choice), no
competitive analysis, no prototype, no roadmap.

## Notes for the Design Doc

The doc should be **lean**. Small feature with limited surface area. Sections
that genuinely need coverage:

1. The user-facing behavior change (init creates `<cwd>/<name>/`; positional
   name overrides cloned `[workspace] name`).
2. The name-override mechanism and why option (b) was chosen.
3. The pre-flight conflict semantics and where the check lives.
4. Backward compat: no escape hatch, error messaging strategy.
5. Out-of-scope: `niwa create` extension (note as follow-up).

Sections that should NOT bloat the doc: an exhaustive risk register, a
multi-page implementation plan, or alternatives sections that just restate
what's in this crystallize file at length.
