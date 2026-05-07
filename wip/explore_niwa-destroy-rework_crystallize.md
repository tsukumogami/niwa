# Crystallize Decision: niwa-destroy-rework

## Chosen Type

**Design Doc** — produces `docs/designs/proposed/DESIGN-niwa-destroy.md` (or
similar per repo convention) capturing the technical architecture for the
reworked destroy command.

## Rationale

Requirements were given as input to the exploration: the user provided a
detailed routing matrix and the workspace-self-destroy guardrail. Round 1
research did not surface gaps in the "what to build" — it surfaced
"how to build" concerns: where the picker code lives, how the shell wrapper
extends, how to detect non-pushed work cheaply across worktrees, how to keep
reset's helpers untouched, what new types and packages to introduce, how the
lifecycle commands ordering works, and how the documentation surface
amendments interact.

The lightweight decision protocol resolved six secondary UX/scope questions
(reset scope, single-instance picker, confirmation token, PRD R2 cleanup,
workspace-wipe ordering, inside-instance prompt) — those decisions belong in
a permanent document so the next implementer doesn't relitigate them.

## Signal Evidence

### Signals Present

- **What to build is clear, but how to build it is not**: routing matrix and
  workspace-self-destroy spec are settled; picker package layout, scan helper
  design, and wrapper change strategy are open questions resolved via
  research.
- **Technical decisions need to be made between approaches**: picker reuse
  (4 paths considered, "copy" chosen); helper organization (extend
  `CheckUncommittedChanges` vs new sibling, sibling chosen); scan output
  shape (typed `Loss` records vs unstructured strings, typed chosen); ordering
  (concurrent vs sequential, sequential chosen).
- **Architecture, integration, or system design questions remain**: how does
  the workspace-wipe path coexist with `ValidateInstanceDir`'s safety guard;
  where does the typed-confirmation prompt sit relative to the response-file
  write; how do the three new prompts interact with non-TTY mode.
- **Exploration surfaced multiple viable implementation paths**: especially
  for picker reuse and for the non-pushed-work scan organization.
- **Architectural or technical decisions were made during exploration that
  should be on record**: copy picker into `niwa/internal/tui/`; new
  `internal/workspace/scan.go` and `internal/workspace/destroy_workspace.go`;
  preserve `ValidateInstanceDir` invariant; sequential workspace-wipe ordering;
  typed-confirmation before landing-path emit.
- **The core question is "how should we build this?"**: the user's framing
  was "Rework `niwa destroy` so it does X, Y, Z" — the X/Y/Z is given; the
  *how* is what this exploration produced.

### Anti-Signals Checked

- **What to build is still unclear**: not present. The routing matrix and the
  workspace-self-destroy guardrail are settled before exploration began.
- **No meaningful technical risk or trade-offs**: not present. Real trade-offs
  exist around helper sharing with reset, picker reuse strategy, scan cost,
  workspace-wipe ordering.
- **Problem is operational, not architectural**: not present. Multiple new
  types, new packages, and a new shell-wrapper command-list extension are
  involved.

## Alternatives Considered

- **PRD**: Ranked below Design Doc because requirements were given as input,
  not identified during exploration. The PRD-vs-Design-Doc tiebreaker
  ("Identified → PRD; Given → Design Doc") explicitly favors Design Doc here.
  A new `PRD-niwa-destroy.md` is still warranted per L6 findings, but it
  captures the *what* and references the design doc for the *how*. The
  design doc is the immediate next artifact; the PRD is a complementary
  document we'll produce alongside.
- **Decision Record**: Ranked below because the rework involves multiple
  interrelated decisions (picker reuse, helper organization, ordering, scan
  design, prompt timing). A single decision record would fragment the story;
  the design doc carries them as a coherent set.
- **No Artifact**: Demoted by the "architectural decisions were made during
  exploration" anti-signal. Implementing directly would lose the rationale
  for the picker copy decision, the new helper layout, and the prompt-timing
  invariant.
- **Plan**: Demoted by the "no upstream artifact exists" anti-signal. A plan
  can't sequence issues for an unwritten design.

## Deferred Types (if applicable)

None applicable. No spike or prototype is needed — feasibility is established.

## Companion Artifacts

The Phase 5 produce step focuses on the design doc. The PRD work is a
follow-on artifact that the design doc cross-links:

- **Primary**: `docs/designs/proposed/DESIGN-niwa-destroy.md` (this phase
  produces).
- **Companion (recommended for Phase 5 to scaffold or hand off)**:
  `docs/prds/PRD-niwa-destroy.md` (covers the R-level requirements; the
  design doc references it but isn't blocked on it for implementation).
- **PRD amendments**: light edits to `PRD-shell-integration.md` (R1, R11
  to add destroy to cd-eligible set; D3 / Out-of-Scope paragraph), and
  `PRD-cross-session-communication.md` (R38 + AC-P11 multi-instance
  clause), and a one-line softening on `PRD-workspace-config-sources.md`
  line 1001.
- **Design doc amendments**: light edits to
  `DESIGN-instance-lifecycle.md` (Decision 4),
  `DESIGN-shell-navigation-protocol.md` (cd-eligible list), and
  `DESIGN-contextual-completion.md` (Decision 3 reconciliation).
