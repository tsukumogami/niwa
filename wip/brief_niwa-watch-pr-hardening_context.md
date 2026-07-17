# /brief Context: niwa-watch-pr-hardening

## Entry Mode
freeform

## Upstream Path
none

## Topic Slug
niwa-watch-pr-hardening

## Visibility
Public

## Artifact Decision
produce

## Phase
0

## Notes
Invoked under /scope parent orchestration (sentinel in
wip/scope_niwa-watch-pr-hardening_state.md, rationale: fresh-chain).
Phase 5 uses parent-delegated-approval: BRIEF stays Draft; the /scope
parent surfaces it to the dispatcher at the hard stop.
Grounding sources: dispatch brief (ephemeral, outside repo), ED roadmap
entry ED2 (private vision repo -- do not reference by path in the
public BRIEF), merged ED1 code in internal/watch + internal/cli/watch.go,
ED1 docs BRIEF/PRD-niwa-watch-once-pr-review.md (Done).
Artifact decision rationale: framing exists only in ephemeral/private
sources; the public niwa repo needs a durable standalone framing doc,
and the BRIEF feeds three downstream artifacts (PRD, DESIGN, PLAN).
