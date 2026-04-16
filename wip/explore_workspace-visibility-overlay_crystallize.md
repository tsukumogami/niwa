# Crystallize Decision: workspace-visibility-overlay

## Chosen Type

PRD

## Rationale

The exploration surfaced a single coherent feature (private workspace extension) but the requirements are not yet captured or agreed on. Multiple user stories need articulation (team lead sharing config, new team member bootstrapping, contributor with partial access, CI/CD environment). The core question is "what should we build and why?" — three open design choices (discovery mechanism, graceful degradation semantics, v1 scope of content support) need requirements framing before architecture decisions are worth locking in. Requirements emerged from the exploration itself, not as input to it, making PRD the right artifact before a design doc.

## Signal Evidence

### Signals Present

- **Single coherent feature**: The private workspace extension is a well-bounded, self-contained feature
- **Requirements unclear**: Multiple design choices (pure convention vs explicit field, opt-in vs opt-out, content override scope) cannot be resolved without stated requirements and user priorities
- **Multiple stakeholders**: Team lead (sharing workspace config), contributor without private access, CI/CD operator, individual developer — each has different needs
- **Core question is "what to build"**: Discovery mechanism and graceful degradation model are requirements questions, not implementation questions
- **User stories missing**: No acceptance criteria for "team lead publishes workspace config without leaking private repo names" or "contributor clones workspace and only gets public repos"

### Anti-Signals Checked

- **Requirements provided as input**: Not present — requirements surfaced during exploration
- **Multiple independent features**: Not present — private extension is a single cohesive feature
- **Independently-shippable steps**: Not present — the layers (type, pipeline, CLI) are interdependent

## Alternatives Considered

- **Design Doc**: Ranked lower because "what to build" is still an open question. Three viable implementation paths (Options A/B/C for discovery) exist, but choosing between them requires requirements — we can't design without knowing which stakeholder needs take priority. Design Doc should follow the PRD.
- **Decision Record**: Ranked lower because multiple interrelated decisions (discovery mechanism, degradation model, content scope, migration path) need coordinated capture, not isolated records.
- **No Artifact**: Ranked lower because architectural decisions were made during exploration (scope ruling, all-or-nothing access model), multiple people will work on this, and documentation is needed for contributors and users.

## Deferred Types (if applicable)

None — no deferred types scored well.
