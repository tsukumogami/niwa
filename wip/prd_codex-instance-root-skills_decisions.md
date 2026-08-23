# /prd decisions: codex-instance-root-skills

Consolidated index of decisions taken during this /prd run (--auto,
chain-driven under the /scope parent).

| id | artifact | tier | status | question |
|----|----------|------|--------|----------|
| PD1 | wip/prd_codex-instance-root-skills_scope.md | 2 | confirmed | Reuse parent-chain research for Phase 2 instead of new agents? |
| PD2 | docs/prds/PRD-codex-instance-root-skills.md | 2 | confirmed | Which of the brief's open questions close at requirements altitude? |
| PD3 | docs/prds/PRD-codex-instance-root-skills.md | 2 | confirmed | How to require row-19 Claude binding honestly given the observed registration defect? |
| PD4 | docs/prds/PRD-codex-instance-root-skills.md | 2 | assumed | Require the live acceptance check to run credential-free with its own gate? |

## PD1 — Phase 2 research reuse

**Frame:** Phase 2 normally fans out 2–4 research agents. Should this run
launch them?

**Gather:** The parent chain already ran two discover-converge rounds (nine
research files under wip/research/), took live measurements against
codex-cli 0.147.0, corrected three wrong agent claims, and declared leads
exhausted. The /scope parent additionally prepared design inputs with late
verifications. Every Phase 1 lead maps to an existing, verified finding.

**Decide:** Skip new agents; treat the parent-chain research as Phase 2's
findings and synthesize from it. Status: confirmed — the evidence is direct
and the exploration's own closing judgment ("leads are exhausted") matches.

## PD2 — requirements-level closure of the brief's open questions

**Frame:** The brief defers four open questions "to the downstream PRD and
design." Which parts are requirements (this PRD closes) and which are design?

**Gather:** Each question splits cleanly: the bound-set mandate, the warning's
truthfulness, collision behavior being stated rather than silent, and the
namespacing outcome are WHAT; the binding mechanics, gate mechanism,
materialization path, and manifest file change are HOW.

**Decide:** The PRD pins the property behind each question as a numbered
requirement and lists the four mechanisms under "Open Questions the Design
Owns" (the PRD-codex-background-dispatch precedent). Status: confirmed.

## PD3 — honest binding for Claude's row 19

**Frame:** Joining the bound set forces a named Claude delivery for row 19,
but the post-crystallize observation found the installed tree unregistered by
Claude Code on the observing machine.

**Gather:** The design inputs are explicit: do not bind the row as though the
delivery were sound; fix the tree shape or record the defect. The chain's
scope excludes making Claude registration work.

**Decide:** Requirement text demands the binding not overclaim: naming a
delivery whose written bytes the agent doesn't register is the drift the rule
exists to catch, so the work must either correct the tree shape as part of the
Codex delivery or record the defect and say exactly what is claimed. The
choice between those two is the design's. Status: confirmed.

## PD4 — credential-free live acceptance gate

**Frame:** Should the PRD require the live scenario to run without a
credential and outside the quota-spending tag, or leave gating entirely to
design?

**Gather:** Measured: codex debug prompt-input works against an empty
CODEX_HOME. The existing live gate copies a real credential; reusing it would
make the scenario skip on machines that could run it. But tags and step
wording are test design.

**Decide:** The PRD requires the observable property — the check needs no
credential and spends no model quota, and skips only where the binary is
absent — and leaves tag/gate mechanics to design. Status: assumed (the
property could arguably be left wholly to design; recorded as a requirement
because the brief's journey names "zero model cost" as user-visible value).
