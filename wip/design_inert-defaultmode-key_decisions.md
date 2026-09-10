# /design Decisions: inert-defaultmode-key

Auto mode, invoked by `/scope` under a `parent_orchestration:` sentinel. The
parent binding for Phase 2 is Decision-bypass-with-inline-resolution: each
decision is resolved inside this design's own evaluation and recorded in
Considered Options, with `decision_provenance: inline-resolved` in frontmatter.

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| G1.1 | wip/design_inert-defaultmode-key_coordination.json | 2 | confirmed | How many decision questions, and which are coupled? |

## G1.1 -- Three decision questions

**Evidence.** The PRD settles the WHAT completely, including the expected
value for every document under every override combination, so the open
choices are HOW choices. Three are independent. The first is where the
derivation's input comes from, which is critical: it is the one choice with a
security edge, since a wrong input source lets a file grant bypass. The second
is how the materializer's mapping and validation change. The third is how the
tests divide across layers. The doc corrections and the dead-reader deletion
are required by the PRD and involve no choice. The first and third are mildly
coupled, because the derivation's unit test takes the shape of its input, but
the options for one don't constrain the options for the other.

**Decision.** Three questions (1 critical, 2 standard), within the 1-5 band,
proceeding normally.
