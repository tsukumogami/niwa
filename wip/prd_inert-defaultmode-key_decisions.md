# /prd Decisions: inert-defaultmode-key

Auto mode, invoked by `/scope` under a `parent_orchestration:` sentinel. Each
decision follows the lightweight decision protocol: frame, gather from loaded
context, decide and record.

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| P0.1 | docs/briefs/BRIEF-inert-defaultmode-key.md | 2 | confirmed | Where do the BRIEF's three open questions go once it is Accepted? |
| P1.1 | wip/prd_inert-defaultmode-key_scope.md | 2 | confirmed | Run Phase 1 as a conversation, or scope from the BRIEF and handoff? |

## P0.1 -- Fold the BRIEF's open questions into the PRD

**Question.** `shirabe transition` moved the BRIEF to Accepted with its Open
Questions section still present, and the brief format forbids Open Questions
at Accepted.

**Evidence.** The brief format names the downstream PRD's Decisions and
Trade-offs section as the canonical closure surface for a brief's open
questions. All three questions -- what the asking declaration produces,
whether workspace-root sessions are owed the posture, whether a resume counts
as a session niwa starts -- are requirements questions.

**Decision.** Remove the section from the BRIEF in the same commit as the
transition; carry the three questions into this PRD's scope as research leads
and close each in the PRD's Decisions and Trade-offs.

## P1.1 -- Scope from the BRIEF and the handoff

**Question.** Phase 1 is conversational by default, but this run has no
interactive author.

**Evidence.** The upstream BRIEF is Accepted and covers all six coverage
dimensions. The `/explore` handoff and its nine research files already answer
most of what a scoping conversation would elicit, including two behavioral
measurements against a real Claude Code build.

**Decision.** Scope from those two sources. Aim Phase 2's leads only at the
gaps they leave open, so discovery does not re-derive settled findings.
