# Crystallize Decision: session-attach

## Chosen Type

**PRD** (Product Requirements Document)

## Rationale

Issue #117 carries the `needs-prd` label and the exploration confirmed that
requirements are the unsettled axis. The user-story is well-formed at the
"what for" level, but the contract — verb names, error wording, column shape,
flag semantics, state-model field shape — needs to be locked in before a
design doc can compile a coherent technical approach. The technical
approach itself is grounded enough in code precedent (lock primitive, daemon
coordination, transcript validation) that the design doc that follows the PRD
will mostly recapitulate prescriptions already captured in the findings file;
the design doc's value will be tying those prescriptions together as one
coherent implementation plan rather than discovering new decisions.

A PRD is also the right home for the demand-validation caveat surfaced by the
adversarial lead: this is a single-author proposal with no corroborating asks
in the niwa repo. The PRD documents this as an explicit assumption ("the
maintainer values this enough to maintain the new surface") rather than
hiding it.

## Signal Evidence

### Signals Present

- **Single coherent feature emerged from exploration**: `niwa session attach` plus its `detach --force` companion plus the `AVAILABILITY` column on `niwa session list` are tightly coupled — operating one without the others creates UX gaps the operator would feel immediately.
- **Requirements are unclear or contested**: the issue body lists 7 open questions for the PRD; convergence resolved them grounded in code precedent but the resolutions need to be ratified as requirements rather than assumed.
- **Multiple stakeholders need alignment on what to build**: human operator, mesh coordinator (sees `availability` field), future contributors maintaining the lock primitive. The PRD captures the cross-stakeholder contract.
- **The core question is "what should we build and why?"**: not "how" — that's largely settled (flock + sentinel + daemon-pause). The PRD locks in the user-facing surface.
- **User stories or acceptance criteria are missing**: the issue body has a single user story but no acceptance-criteria-shaped statements about exit codes, error messages, column ordering, or filter semantics.

### Anti-Signals Checked

- **Requirements were provided as input to the exploration**: partially true — the issue body sketched UX (verbs, lock-while-attached, --force flag) but left the contract details ("what does --force mean exactly", "what are the error messages", "what columns appear in `session list`") to the PRD. This anti-signal is partial, not full; PRD still wins.
- **Multiple independent features that don't share scope**: not present. Attach + detach + AVAILABILITY column share state and UX surface.
- **Independently-shippable steps**: not present. The three pieces ship together or not at all.

## Alternatives Considered

- **Design Doc**: ranked second. Many architectural decisions (lock primitive, daemon coordination, state-model field shape, transcript validation strategy) were made during exploration and need a permanent home. However, the design doc's existence depends on requirements being settled first — the verb names, error wording, column shape are PRD-level concerns that the design doc consumes as input. Demoted by the anti-signal "What to build is still unclear (route to PRD first)" — partially true given the unsettled acceptance-criteria-shape requirements. **Will run /shirabe:design after the PRD lands.**

- **Plan**: ranked third. The work decomposes into atomic units cleanly (state-schema change, lock primitive, daemon coordination, attach command, detach command, list command updates, MCP error code, functional test, docs). However, the anti-signal "Open architectural decisions need to be made first" is partially present — the design doc needs to lock the field shape (`attach` sub-object), the lock-file path, and the daemon-pause sequencing before the plan can decompose into issues. **Will run /shirabe:plan --single-pr after the design doc lands.**

- **No Artifact**: ranked fourth. Anti-signal "Any architectural, dependency, or structural decisions were made during exploration" is strongly present — the lock primitive, sub-object schema, MCP error code, daemon coordination pattern, and transcript validation strategy are all decisions a future contributor needs to know about and the wip/ directory is cleaned before merge.

- **Decision Record (ADR)**: ranked low. Multiple decisions were made, but they're all part of one feature, not standalone architectural choices that warrant their own ADRs. The decisions belong inside the design doc.

- **Rejection Record**: not applicable. The adversarial-demand lead concluded "demand not validated" but explicitly NOT "validated as absent" — there's no positive rejection evidence and the user has set direction to proceed.

- **VISION, Roadmap, Spike Report, Competitive Analysis**: not applicable. The project (niwa) already exists, this is a single feature within it, no competing products to analyze, and feasibility was confirmed empirically (the transcript-failure-modes lead actually ran `claude --resume` against adversarial inputs).

## Deferred Types

- **Prototype**: not applicable. The transcript-failure-modes lead already prototyped the most uncertain mechanic (`claude --resume` failure behaviour) empirically. No further prototyping is needed before the PRD.

## Next Step

Hand off to `/shirabe:prd --auto` for PRD drafting. The PRD draft will consume:

- `wip/explore_session-attach_findings.md` (Accumulated Understanding section)
- `wip/explore_session-attach_decisions.md` (every choice with rationale)
- `wip/research/explore_session-attach_r1_*.md` and `r2_*.md` (full detail for any section needing depth)
- Issue #117 body (the user-story and the locked-in defaults)
- PR #115 (`docs/niwa-mesh-reliability`) as the in-flight design that establishes precedent for the parallel-sub-object schema pattern
