# Phase 6 Structural-Format Review: DESIGN-session-store-teardown

Design: `docs/designs/DESIGN-session-store-teardown.md`
Reference: `skills/design/references/design-format.md` (shirabe 0.19.2-dev), with
`references/quality/considered-options-structure.md` for the Considered Options and
Decision Outcome templates, `skills/writing-style/rules.yaml` for style, and
`skills/public-content/SKILL.md` for the public-repo check. The upstream PRD
(`docs/prds/PRD-session-store-teardown.md`) was read to test altitude claims.

## Verdict

PASS with advisories. The document conforms structurally: all nine sections are
present in canonical order, the frontmatter is valid, there's no PLAN-altitude
content, no section overshoots a documented budget by more than 50%, and there are
no private references, `wip/` paths, emojis or AI attribution. The findings below
are fixes to make before acceptance. None of them blocks.

## 1. Section presence and order

All nine required sections appear as H2 headings in canonical order: Status (l.41),
Context and Problem Statement (l.45), Decision Drivers (l.96), Considered Options
(l.124), Decision Outcome (l.388), Solution Architecture (l.433), Implementation
Approach (l.576), Security Considerations (l.644), Consequences (l.648). FC04 and
FC15 pass.

- `## Status` has the bare word `Proposed` as its first non-blank line, and it
  matches the frontmatter `status: Proposed`. FC03 passes.
- Security Considerations holds only the placeholder "(Written after the security
  review.)". That's expected at this point and isn't a failure here. It does have to
  be replaced before Proposed -> Accepted. The placeholder text also describes the
  authoring workflow, which is the kind of process note a public doc shouldn't
  keep, so the rewrite has to remove it rather than add to it.
- Context-aware sections: this is a tactical design in a public repo with no
  `spawned_from`, so Market Context, Required Tactical Designs and Upstream Design
  Reference are all correctly absent.
- There's no Implementation Issues table, which is correct because the PLAN owns
  it.

## 2. Frontmatter

- Required fields `schema`, `status`, `problem`, `decision`, `rationale` are all
  present. `problem`, `decision` and `rationale` each use a `|` literal block scalar
  and each is a single paragraph. FC01 and FC02 pass.
- The required fields are in canonical order (`schema`, `status`, `problem`,
  `decision`, `rationale`).
- `upstream:` sits between `status` and `problem`. The canonical template in
  design-format.md puts optional fields after `rationale`; the phase-6 step 6.5
  template puts `upstream` after `status`. The two references disagree, and the
  design follows 6.5. Recommendation: move `upstream:` below `rationale:` to match
  design-format.md, which is the named canonical reference. This is low priority.
- `upstream: docs/prds/PRD-session-store-teardown.md` resolves on disk in the
  worktree, is repo-relative and public.
- `user_visible_surface` is missing. The design changes CLI surface: `niwa worktree
  destroy` accepts session ids and handles, new exit codes 3 and 4, and root
  redirect messages. Phase 6 of Implementation Approach also updates
  `docs/guides/worktree.md`. Recommendation: add `user_visible_surface: true` so
  `/plan` reads the docs-coverage signal directly instead of falling back to a body
  scan.
- Mirror check: the frontmatter `decision` matches the Decision Outcome Summary, and
  there's no stale divergence. The `rationale` has an overclaim problem, covered
  under writing style in section 6.

## 3. Section altitude

**No PLAN-altitude content.** Implementation Approach names six phases, each with a
deliverables list of files. There are no issue titles, acceptance criteria per
item, or issue table. File-level deliverables are acceptable at design altitude.

**No new requirements introduced.** I checked every behavioral statement against the
PRD:

- The `worktree list` root redirect with exit 0, the `destroy --by-path` "as
  today" behavior, and the create/apply/attach/detach/go exit-1 message all come
  from the R10 table.
- The 30-second bound and its error wording come from R17. No nesting comes from
  R19. Atomic `instance.json` and skip-after-failed-read come from R22 and R23.
- The D2 key assumption "`niwa worktree list --json` at the root still prints `[]`
  on stdout" isn't stated in the PRD. It follows from R21 ("existing invocations
  keep their ... output and exit codes"), but the design presents it as an
  assumption with no citation. Recommendation: cite R21 so it reads as a derivation
  rather than a new requirement.
- The `NIWA_INSTANCE_ROOT` "never refused" assumption is an implementation
  constraint, not a user requirement, so it's fine at design altitude.

**Advisory: requirements are restated twice.** The format reference says to cite
PRD requirements by number rather than re-narrate them. The design summarizes R1-R25
in Decision Drivers, then the Decision 1 context paragraph (l.128-136) re-lists
R11-R19 one by one, and the Decision 2 context (l.226-231) re-lists R4-R10. The
Context section even announces the restating (l.93-94). Recommendation: keep the
Decision Drivers summaries and have each decision's context name the drivers it
answers instead of listing the R-numbers again. Decision 1's context shrinks to
about two sentences.

**Advisory: Decision Drivers aren't numbered.** The reference's quality guidance
asks for numbered drivers (D1, D2...) so Considered Options can cite them. The
drivers here are bold-labeled bullets, and the rejections cite PRD R-numbers
directly, which works because each driver carries its R-range. Numbering is
optional polish. The "Implementation fit" driver has no PRD anchor, so rejections
that rest on it ("keeps the leaf rule only on paper") can't point at it by number.

**Implementation Approach sequencing is partial.** The reference asks for explicit
"Batch 1 unlocks Batch 2 because X" reasoning. Phase 1 (failing tests first) and
Phase 3 ("forced tests turn green here") state their reasons. The others don't:

- Phase 4 depends on Phase 2's lock, because the root disclosure write takes it.
- Phase 5 depends on Phase 3, because `loadMappingsForDestroy` relies on the locked
  `ListSessionMappings`.
- Nothing says whether Phase 5 could run in parallel with Phases 2-4.

Recommendation: add one sentence per phase naming what it depends on.

**Internal inconsistency in Phase 1 (l.580-589).** "Place the four hook points in
today's code," but `configdir-lock-contended` fires inside `internal/dirlock`,
which doesn't exist until Phase 2, and the Phase 1 deliverables list only
`snapshotwriter.go`, `snapshot.go` and `apply.go`. Also, "splitting `SaveState`'s
single write where a gap is needed" matches none of the four named points:
`root-state-read` sits at the `LoadState` in `Create`. Recommendation: say that
three points land in Phase 1 and the contended point lands with `dirlock` in
Phase 2, and either name the `SaveState` gap point or drop the clause.

## 4. Section length vs budget

The reference sets numeric budgets in only a few places, and every one of them
holds:

| Location | Budget | Actual | Result |
|---|---|---|---|
| Frontmatter problem / decision / rationale | 1 paragraph each | 1 each (decision is the longest, about 150 words) | within |
| Status | bare word, optional prose | bare word | within |
| Decision 1-4 context | 1-3 paragraphs | 2 / 2 / 2 / 1 (context plus key assumptions) | within |
| Alternative description | 1-2 sentences, then "Rejected because" | 1 sentence each; rejections 1-3 sentences | within |
| Decision Outcome Summary | 2-3 paragraphs | 2 | within |

The Chosen blocks, Solution Architecture, Implementation Approach and Consequences
have no numeric budget in the reference, so the >50% rule doesn't apply to them.

**Advisory: duplication.** The Decision 1 Chosen block (about 560 words plus a
table) and Solution Architecture's snapshotwriter, snapshot and session_map
component bullets plus Data Flow describe the same mechanism twice at the same
level of detail. That covers recovery rules, wrapper staging and `.prev` handling.
The Decision 2 Chosen bullet list and Key Interfaces also repeat the six resolver
functions. Recommendation: in the Decision 1 Chosen block, keep the lock placement,
the mode table and the no-nesting argument, and point to Solution Architecture for
the recovery sweep and the `config.Discover` branch.

## 5. Considered Options

Every decision has a context, a `#### Chosen:` block and an `#### Alternatives
Considered` block. Every decision has at least one alternative.

- **Decision 1 (four alternatives): passes.** Each rejection is specific and tied
  to a requirement. The whole-refresh lock is rejected with concrete timing (3.5 s
  backoff, eight serialized fetches). The move-the-store option is rejected on
  scope plus the rename collision mechanics. Merge-back is rejected on four named
  defects. The atomic exchange is rejected because it doesn't order a mapping write
  against the carry-over, and it's honestly kept as a possible later hardening. The
  bare "#74" should say "niwa issue #74" or reference the PRD's Out of Scope entry,
  so a reader knows which tracker it means.
- **Decision 2 (three alternatives): passes.** The per-command refusal, the
  `--session` flag and the resolver-in-`internal/worktree` interface are all
  plausible options. Each rejection names the concrete cost.
- **Decision 3 (four alternatives): passes.** The first alternative (unexported
  hook variables) is really the chosen mechanism in a narrower form, which the
  rejection admits ("Rejected in that form ... keeps the mechanism and fixes both
  gaps"). It isn't a strawman, but it's closer to a predecessor than a competitor.
  The `testfault` and build-tag rejections are specific and well grounded.
- **Decision 4 (one alternative): thin.** The only alternative is a scope
  extension (also lock the root read), not a different answer to the same question.
  The questions a reader will actually ask aren't addressed: why skip the write
  after a failed read rather than fail the command or retry the read, and why
  write a temp file and rename rather than use another atomic-write approach. The
  context is one paragraph with no key assumptions. Recommendation: add the "fail
  `Create` when the root read fails" alternative and its rejection. That rejection
  is presumably that it turns a transient torn read into a failed provision,
  against R11 and R21.

**Decision Outcome labels:** "**Chosen: 1A + 2A + 3A (registry variant) + 4**"
(l.390) uses option letters that are never defined in Considered Options. Those
options have names, not letters, and Decision 4 has no letter at all. "(registry
variant)" points at a variant name from the per-decision research reports, which
aren't committed. It isn't a `wip/` path, so it doesn't break the wip-hygiene rule,
but a cold reader can't resolve it. Recommendation: rewrite it with the Chosen
headings' short names, for example "Chosen: short per-directory lock with unlocked
fetch + CLI destroy resolver with central root refusal + `internal/testhook`
registry + atomic `SaveState` with skip-after-failed-read".

The Rationale sub-section is well formed: it explains why the combination works (a
single locked read shared by the reaper and destroy, and a registry shaped by the
package boundary) without repeating the rejections.

## 6. Writing style and public-content

**Mechanical checks:**

- None of the banned words or adverb openers from `rules.yaml` appear.
- There are no em dashes, so the density rule doesn't fire.
- There are no emojis and no AI attribution.
- There are no `wip/` paths in the body or frontmatter.
- There are no private repo names or paths (vision, tools, dot-niwa-overlay,
  `private/`).
- There's no organization-internal workflow mention apart from the Security
  Considerations placeholder noted in section 1.
- `claude agents --json` and "Codex" are neutral technical references to supported
  agents, not competitive content.

**Judgment findings:**

- **Overclaim in frontmatter `rationale`.** "the one ordering that ... making lost
  writes, resurrected deletes, a gutted `.niwa` and mid-swap reads impossible"
  contradicts the document's own Consequences. Non-unix platforms keep today's
  races, the ephemeral-session hook's read and config readers are unlocked, and
  the PRD's Known Limitations already concede these points. Recommendation: drop
  "the one" and scope "impossible" to "on Linux and macOS, for niwa's own mapping
  writes and destructive reads".
- **Synonym cycling.** "a gutted `.niwa`" (rationale, Positive consequences) names
  the same condition that Decision 1 and the PRD call "the directory never ends
  without its configuration" (R14). Pick the one phrase the PRD uses and repeat
  it.
- **Unresolved references in Data Flow.** "hook; rename `D` to `D.prev`; hook;" at
  l.563-564 doesn't say which hooks. Name `SnapshotCarriedOver` and
  `SnapshotMovedAside` there.
- **Consequences pairing.** Five negatives, four mitigations. The last negative ("A
  reap that removes an instance between teardown's read and its destroys shows up
  as per-worktree errors and exit 1") has no mitigation. Either add one (for
  example, that it fails closed, leaving nothing wrongly deleted, and exit 1 is the
  R9 "refused" signal) or say explicitly that the risk is accepted.
- **Cross-check for the architecture reviewer.** The first negative consequence
  says that a destroy by worktree id inside an instance can now exit 1 on a lock
  timeout. The PRD's acceptance criteria require destroy by worktree id to produce
  "the same stdout, stderr and exit code as before," with exceptions only for the
  R5 collision and no-match exit 3. The PRD's R11 permits the R17 wait error only
  for refreshes. That's a possible PRD conflict worth confirming. It isn't a format
  defect.
- No vacuous sentences, empty conclusions or antecedent-less "this/that" sentence
  openers were found. The Rationale's closing sentence carries a real claim, and
  the Overview paragraph is short and does its job.

## Recommendations, in priority order

1. Replace the Security Considerations placeholder with the security review output
   before acceptance.
2. Rewrite the Decision Outcome "Chosen:" line to use the options' names instead
   of the undefined letters and "(registry variant)".
3. Qualify the frontmatter `rationale` overclaim ("the one ordering ...
   impossible").
4. Strengthen Decision 4 with a real competing alternative, such as failing
   `Create` on a failed root read.
5. Fix the Phase 1 hook-point inconsistency and add dependency sentences to
   Phases 4 and 5.
6. Add `user_visible_surface: true`, and move `upstream:` below `rationale:`.
7. Stop re-listing PRD requirements in each decision's context, and trim the
   duplication between Decision 1's Chosen block and Solution Architecture.
8. Small items: cite R21 for the `list --json` assumption, pair a mitigation with
   the reap-race negative, name the hooks in Data Flow, use one term instead of
   "gutted `.niwa`", and qualify "#74".
