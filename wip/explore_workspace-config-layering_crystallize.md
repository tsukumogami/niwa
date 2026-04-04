# Crystallize Decision: workspace-config-layering

## Chosen Type

PRD

## Rationale

Key requirements for the personal config layer emerged during exploration rather than being given as input. The user came in with a problem and a rough direction (GitHub-backed personal config repo), but exploration surfaced specific requirements that weren't pre-stated: how CLAUDE.md content layering should work, whether opt-out at init persists across future applies, the precise merge semantics per field type, and the full registration UX. These open requirements need to be captured in a PRD before architectural design begins. A design doc assumes "what to build" is settled; a PRD is the right place to settle the remaining what-and-why questions.

## Signal Evidence

### Signals Present

- Single coherent feature emerged from exploration: personal config layer with GitHub-backed repo, registered once per machine, synced at apply time
- Requirements incomplete: CLAUDE.md content layering, opt-out persistence, and opt-out flag naming are unresolved requirements, not just implementation details
- Requirements emerged during exploration rather than provided as input: merge semantics, registration UX, workspace name as identifier, plugins union behavior -- all discovered during research

### Anti-Signals Checked

- "Requirements were provided as input to the exploration": partially present (user had a rough direction) but outweighed -- most specific requirements were identified by /explore, not given to it

## Alternatives Considered

- **Design Doc**: Strong scoring (5 signals, 0 anti-signals on first pass) but tiebreaker favors PRD when requirements emerged during exploration rather than being given as input. Design doc is the right next artifact after PRD.
- **Plan**: Demoted -- no design doc exists yet and open design questions remain (CLAUDE.md layering, opt-out persistence). Can't sequence implementation without those resolved.
- **No Artifact**: Demoted -- architectural decisions were made (plugins union, sync placement, workspace name as key) that need to be on permanent record.
