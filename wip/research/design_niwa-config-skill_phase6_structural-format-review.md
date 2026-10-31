# Phase 6 Structural-Format Review: DESIGN-niwa-config-skill.md

Reviewed against `skills/design/references/design-format.md`
(shirabe 0.15.1-dev, cached at
`/home/dangazineu/.claude/plugins/cache/shirabe/shirabe/0.15.1-dev/skills/design/references/design-format.md`).

Target: `docs/designs/DESIGN-niwa-config-skill.md` (5099 words total,
worktree `parallel-snuggling-lightning`, branch `docs/niwa-config-skill`).

Per the calling instructions, frontmatter completeness (missing
`schema`, `decision`, `rationale`; placeholder-only `status` +
`problem`) is explicitly out of scope for this pass — it's flagged
as finalized in a later step — so this review does not fail on it.
One frontmatter observation is noted below for the record only, not
as a review failure.

## Q1: Section presence and order

PASS. `grep -n "^## "` returns all nine required sections in exactly
the canonical order:

1. Status (line 23)
2. Context and Problem Statement (27)
3. Decision Drivers (87)
4. Considered Options (111)
5. Decision Outcome (362)
6. Solution Architecture (389)
7. Implementation Approach (471)
8. Security Considerations (534)
9. Consequences (619)

No extra top-level `##` sections, no reordering, no omissions. FC04
and FC15 would both pass on section shape alone.

`## Status`'s first non-blank line is the bare word "Proposed" (line
25) followed by a blank line before body prose resumes at Context and
Problem Statement — satisfies the FC03 shape rule (frontmatter
`status: Proposed` matches the body first line case-insensitively).

Context-aware sections (Market Context, Required Tactical Designs,
Upstream Design Reference) are all correctly absent: no external
product/industry comparison drives this decision space, this isn't a
strategic DESIGN spawning tactical children, and frontmatter carries
no `spawned_from`.

No Implementation Issues table appears anywhere in the document —
correct per the table-ownership rule (PLAN owns that table, not
DESIGN).

Wip-hygiene: `git grep -nE 'wip/' -- docs/designs/DESIGN-niwa-config-skill.md`
returns exactly one hit, line 103: "...applies (no committed `wip/...`
references); niwa conventions apply". This is rule-statement prose
(a Decision Driver restating the workspace-wide wip-hygiene rule
itself), not a path-shaped reference to a specific `wip/...` file —
falls under the R25 carve-out and is not a violation.

## Q2: Section-altitude conformance

Mostly PASS, with one section flagged as running warm.

- **Context and Problem Statement**: stays technical throughout,
  including a self-auditing "Note on the brief's named examples"
  paragraph that appropriately downgrades unverifiable claims rather
  than asserting them — this is diligence, not PRD-altitude
  requirements-gathering. No new requirements are introduced.
- **Decision Drivers**: six named drivers (Reach, Drift resistance,
  Minimal new install surface, No change to rank-2 migration,
  Public-repo guardrails, Self-service friendliness), each a real
  constraint traced to concrete facts rather than generic
  best-practice filler. Minor stylistic gap: the reference says
  drivers are "often numbered (D1, D2, ...) for cross-referencing" —
  these are unnumbered, and Considered Options cross-references them
  by paraphrase ("must reach already-adopted single-repo workspaces")
  rather than by ID. Advisory only; "often" is not "must."
- **Considered Options**: three decisions, each chosen option
  strongly justified against genuine (non-strawman) alternatives with
  explicit weaknesses traced back to Decision Drivers or to
  source-verified facts. This is the section most worth watching for
  altitude drift — see the dedicated discussion under Q3 below, since
  it's also the size outlier.
- **Decision Outcome**: correctly synthesizes the three decisions into
  one coherent narrative without re-arguing alternatives. Appropriate
  altitude.
- **Solution Architecture**: Components/Key Interfaces/Data Flow
  subsections name concrete files, functions, and call sites — this
  is exactly the "concrete enough to sketch the implementation" bar
  the reference asks for, not over-detailed for this section.
- **Implementation Approach**: four phases with deliverables. Names
  batches and sequencing rationale (Phase 2 depends on Phase 1's
  branch landing per the Decision Outcome's dependency note; Phase 4
  explicitly calls out which scenario proves Phase 2's fix). Stays at
  phase/deliverable granularity — no per-issue acceptance criteria,
  no issue numbers, no assignees, no atomic task list. Does not cross
  into PLAN altitude as an Implementation Issues table would.
- **Security Considerations**: six named attack-vector subsections,
  each with specific mitigations traced to real code
  (`stageAndRename`, `sliceContains(disclosedNotices, ...)`,
  `githubpublic.go`'s guardrail scope). No generic boilerplate. This
  is the correct altitude and arguably a model instance of the
  "mitigations cite real defenses" guidance — but see Q3 for its
  length.
- **Consequences**: four positive, five negative, three mitigations,
  each negative paired with a mitigation or explicitly marked
  unresolved ("left unresolved by this design — deferred, not
  solved") rather than hidden. Correct altitude, honest tone matching
  the reference's explicit instruction that "a DESIGN with no negative
  consequences is hiding them."

No section smuggles PRD-altitude requirements-gathering language
("users need...", "the spec requires...") or a PLAN-altitude atomic
issue list/table. The Content Boundaries rule is not violated in the
strict sense (no new requirements introduced, no issues table
carried). The altitude concern that does exist is a matter of degree,
not of a wrong artifact type showing up — flagged below.

## Q3: R19 budget-vs-spec

The format reference does not publish an explicit numeric
word-count budget per section — `design-format.md`'s Quality
Guidance describes expected *content* per section qualitatively, not
a length ceiling. Absent a stated number, this heuristic falls back
to (a) relative proportion within this document, and (b) whether
length correlates with legitimate content (more decisions, more
attack vectors) or with altitude creep (content that belongs in a
different artifact).

Section word counts:

| Section | Words | % of body |
|---|---|---|
| Status | 1 | ~0% |
| Context and Problem Statement | 472 | 10% |
| Decision Drivers | 191 | 4% |
| Considered Options | 2048 | 42% |
| Decision Outcome | 203 | 4% |
| Solution Architecture | 517 | 10% |
| Implementation Approach | 314 | 6% |
| Security Considerations | 711 | 14% |
| Consequences | 471 | 10% |

Considered Options, by itself, is 42% of the document's body prose —
more than four other sections combined. Breaking it down by decision:
Decision 1 (778 words), Decision 2 (621 words), Decision 3 (615
words). Each individual decision's share (600-780 words) is not
outlandish for a decision with a chosen option plus 2-3 genuinely
argued alternatives; the section total is large because this design
bundles three decisions into one document, which is a legitimate
document-shape choice, not a per-decision overshoot. Applying the
task's ">50% over budget" framing at the decision level (not the
section level, since no per-decision budget exists either) does not
surface a clear outlier — the three decisions are within about 25% of
each other.

Security Considerations at 711 words (14%) is the second-largest
section, roughly double Solution Architecture's per-topic density,
but it covers six distinct, non-overlapping attack surfaces
(external-artifact handling, permission scope, supply chain, data
exposure, secret-handling guardrails, install-mechanism integrity).
~120 words per surface is not excessive for the reference's own bar
("names the attack vectors considered... mitigations cite real
defenses, not generic boilerplate").

**Altitude flag (the one worth carrying into Phase 6.2 feedback):**
the overshoot concern raised in the task brief is real but shows up
as *granularity*, not raw length. Two passages read closer to
PLAN-issue write-up than DESIGN-architecture description:

- Decision 1's Chosen Option (lines 147-179) specifies exact call-site
  line numbers four times over (~443, ~595, ~927, ~956) and describes
  the new code's exact structure ("guard on a new notice-ID constant
  via `sliceContains(disclosedNotices, ID)`, emit a notice, append to
  `disclosedNotices`, call `a.InstallNiwaPlugin(...)`") — this is
  implementation-ready pseudocode, not an architectural description a
  PLAN author would need to further decompose.
- Decision 3's Chosen Option (lines 322-334) is even more pointed: "add
  one line ... immediately after `applier := workspace.NewApplier(gh)`
  and before `applier.Create` is invoked:
  `configurePluginAutoInstall(applier, initNoInstallPlugins)`" is
  effectively a completed, atomic implementation instruction — the
  kind of content a PLAN issue's body would carry, not a DESIGN's
  Considered Options rejection-and-selection narrative.

This granularity is *largely restated* in Solution Architecture (the
Components subsection repeats the same line numbers and call
structure), which is where the reference says this level of detail
belongs. The duplication is the actual finding: Considered Options
already reads as fully implementation-decided by the time Solution
Architecture repeats it, so Considered Options is doing double duty
as both "why we chose this" and "here is the diff," which pushes its
altitude toward PLAN/issue-body territory even though no formal issue
list or table appears.

This does not rise to a hard violation of Content Boundaries (no new
requirements, no issues table), and per the design's own framing this
granularity is intentional — grounded in adversarially-validated
decision reports from Phase 2, verified "by direct source reading" —
so the specificity is earned, not padding. But it is the kind of
overshoot the R19 sub-rubric exists to catch: recommend trimming the
line-number/pseudocode detail out of Considered Options' Chosen
Option write-ups (keep the *why*, defer the *exact diff shape* to
Solution Architecture, where it already lives) rather than shortening
the section's argumentative content.

## Frontmatter note (non-blocking, for the record)

Current frontmatter carries only `status` and `problem` (placeholder
per handoff), missing `schema: design/v1`, `decision`, and
`rationale`. Per this review's scope this is not flagged as a
failure — it's understood to be finalized in Phase 6.5. Noting only
so the finalization step doesn't drop it: `decision` and `rationale`
should mirror Decision Outcome and the Considered Options rejection
logic respectively, one paragraph each, per the format reference.

## Summary verdict

- Q1 (presence/order): PASS, no issues.
- Q2 (altitude): PASS overall; one advisory (Decision Drivers
  unnumbered) that doesn't block.
- Q3 (budget): No hard overshoot by section-length alone (no section
  or sub-decision clears the 50% threshold against any stated or
  inferable budget). One legitimate altitude flag: Considered
  Options' Chosen Option write-ups for Decision 1 and Decision 3
  duplicate implementation-pseudocode detail that Solution
  Architecture already owns — recommend trimming that duplication
  before acceptance, not because the document is too long, but
  because the same content at two altitudes in two sections is a
  maintenance/staleness risk (the two copies can drift from each
  other exactly the way `DESIGN-workspace-config.md` drifted from
  code).
