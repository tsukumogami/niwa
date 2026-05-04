# Crystallize Decision: mesh-session-lifecycle

## Chosen Type

PRD

## Rationale

The exploration identified a coherent, bounded feature (coordinator-managed session
lifecycle with worktree anchoring) but did not produce written requirements, user
stories, or acceptance criteria. The "what to build" is directionally clear from
the research, but needs to be captured as a requirements contract before design
and implementation can begin. This matches the PRD signal pattern: requirements
emerged during exploration rather than being given as input to it.

The user explicitly confirmed the target is a PRD. The exploration produced
architectural decisions (per-worktree daemon, universal scope, four lifecycle
states, six decisions captured in the decisions file) that belong in the PRD as
settled constraints, freeing the design doc phase to focus on HOW.

## Signal Evidence

### Signals Present

- **Single coherent feature emerged from exploration**: Coordinator-managed session
  lifecycle with git worktree anchoring — a well-bounded addition to the mesh model.

- **Requirements are unclear or contested**: No acceptance criteria exist for session
  creation, task delegation within a session, session cleanup, or the non-mesh CLI
  equivalent. The exploration identified the shape but not the contract.

- **The core question is "what should we build and why?"**: The exploration answered
  whether this is feasible and desirable. The PRD must answer: what exactly does a
  session guarantee, what does the coordinator experience, what does niwa enforce?

- **User stories or acceptance criteria are missing**: No functional requirements
  exist for the session MCP tools, lifecycle states, cleanup guards, or non-mesh
  session management.

## Anti-Signals Checked

- **Requirements were provided as input to the exploration**: Not present. The user's
  initial message was directional, not a requirements list. Requirements emerged
  from the research.

- **Multiple independent features**: Not present. The session model, worktree
  anchoring, and dirty-workspace fix are all facets of the same feature.

## Alternatives Considered

- **Design Doc**: Scored higher on raw signal count (5 signals) but was demoted
  because the anti-signal "what to build is still unclear" is present — requirements
  haven't been written. A design doc presupposes a PRD. The PRD tiebreaker confirms:
  requirements emerged during exploration -> PRD first.

- **Plan**: Demoted. No upstream PRD or design doc exists to decompose. Technical
  approach has open questions (niwa_ask routing, non-mesh CLI UX) that need to be
  resolved before sequencing work.

- **No Artifact**: Demoted. Multiple architectural decisions were made during
  exploration (decisions file has six entries) that future contributors need on
  record. Direct implementation without capturing requirements risks divergent
  implementations.

- **Decision Record**: Partially fits (the exploration did make architectural
  choices). Demoted because there are multiple interrelated decisions that warrant
  a design doc after the PRD, not isolated decision records.
