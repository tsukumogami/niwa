# Crystallize Decision: delegation-model-isolation

## Chosen Type

Design Doc

## Rationale

What to build is settled: `niwa_delegate` without `session_id` should be rejected. The PRD's backward-compatibility rationale (R13) is revisable because niwa has no users, and R13 directly contradicts the PRD's stated Goal 3 ("main clone always stays on main"). The user confirmed the direction.

How to build it is entirely open: the specific rejection behavior (error code, response shape), whether and how read-only tasks can opt out, the impact on coordinator prompts that currently omit `session_id`, the human-in-main-clone guidance, and whether the PRD requires a formal amendment or just a superseding design decision. Multiple viable implementation paths were surfaced during exploration and none was resolved. These decisions need to be made, compared, and permanently recorded.

A Decision Record was the closest alternative but demoted because multiple interrelated technical decisions remain — not just a single option-selection. A PRD amendment was considered but the existing PRD is "Done" and the open questions are all architectural (how, not what).

## Signal Evidence

### Signals Present

- **What to build is clear, how to build it is not**: mandatory sessions is the direction; API changes, error codes, read-only handling are all open.
- **Technical decisions need to be made between approaches**: reject hard vs. warn vs. read-only flag are distinct implementation paths with different coordinator UX implications.
- **Architecture and system design questions remain**: `handleDelegate` must change; the error response must be defined; the coordinator-facing API surface changes.
- **Multiple viable implementation paths surfaced**: full rejection, warning-only, or a `read_only: true` escape hatch — each has different trade-offs.
- **Architectural decisions were made during exploration that should be on record**: the choice to make sessions mandatory, and the rejection of implicit per-task sessions, need permanent documentation so future contributors understand why.
- **Core question is "how should we build this?"**: the what is decided; the how is not.

### Anti-Signals Checked

- "What to build is still unclear": not present — the direction is settled.
- "No meaningful technical risk or trade-offs": not present — read-only handling and coordinator UX are real trade-offs.
- "Problem is operational, not architectural": not present — API changes are required.

## Alternatives Considered

- **Decision Record**: 4 signals matched (single decision evaluated, future contributors need the rationale, alternatives compared, clear "which option and why" core question). Demoted because the anti-signal fires: multiple interrelated decisions need a design doc, not a single decision entry. The API shape, read-only escape hatch, and coordinator impact are all unresolved.
- **PRD**: 3 signals matched. Demoted because an existing PRD (PRD-mesh-session-lifecycle.md) already covers this topic and is marked Done. Per tiebreaker logic, a design doc is preferred when the upstream artifact exists and the open questions are architectural.
- **Plan**: demoted — technical approach is still debated and open architectural decisions need to be made first.
- **No Artifact**: demoted — multiple structural decisions were made during exploration that must be permanently documented; others will need documentation to build from.
