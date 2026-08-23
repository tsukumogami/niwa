# /brief Discovery: codex-instance-root-skills

## Problem Candidate
Every background worker `niwa dispatch` launches stands at a workspace
instance root, and a Codex worker standing there receives none of the
workspace's plugin skills — while the same worker one directory down
inside any cloned repository receives them all. The root's orientation
document tells the worker to invoke a skill it does not have. The
capability contract records this as niwa's own unbuilt work: rows 18
(RootProjectSkills) and 19 (NiwaPlugin) are declared unavailable for
Codex with reason kind ReasonNotBuilt. Skills are the one project-layer
capability measured to load from an untrusted layer, so the gap is
closable without touching the reserved trust decision.

## Outcome Candidate
A developer dispatches a background Codex worker and it can invoke the
workspace's skills — same namespaced names as a repository-started
session — including the skill the orientation document names. The
guide's generated gap list drops both bullets, the dispatch-time
warning tells the truth about what is still missing at the root, and a
maintainer sees both capability rows bound to real deliveries so drift
fails a test.

## Grounding Anchor
Conversation only — the consumed /scope handoff and /explore findings
(two research rounds, six settled decisions, live measurements against
codex-cli 0.147.0). No ROADMAP governs this work; upstream: omitted.

## Journey Sketch
- Developer dispatches a background Codex worker; the worker resolves
  and invokes workspace skills at the root (previously zero).
- Developer whose root-started Codex session still lacks MCP/posture
  reads a dispatch warning that names the remaining gap truthfully
  instead of going silent.
- Maintainer/contributor reads the capability contract and guide; both
  rows implemented and bound; drift in either direction fails a test.
- Reviewer verifies delivery end-to-end at zero model cost with a
  positive/negative control via codex debug prompt-input.

## Open Questions for Drafting
- Keep design-level choices (row 18 binding shape, plugin manifest,
  warning re-gate mechanism, materialization site) in Open Questions
  deferring to the downstream PRD/design, not settled here.
- Public visibility: no private repo names, no wip/ references; cite
  durable docs (DESIGN/SPIKE/PRD/guide) and code paths.
