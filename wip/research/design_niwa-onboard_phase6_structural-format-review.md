**Verdict:** FAIL

# Structural-Format Review — DESIGN-niwa-onboard

Reviewed: `docs/designs/DESIGN-niwa-onboard.md` (1276 lines) against the canonical
DESIGN format reference (`skills/design/references/design-format.md`).

The document is structurally strong: all nine sections present and correctly
ordered, altitude clean, no wip/ or private-repo leakage. It fails on two narrow,
cheap-to-fix, in-scope items — a frontmatter optional-field placement deviation and
two banned writing-style words. Everything below the verdict is remediable in ~4
edits.

---

## Q1 — Section presence and order: PASS

All nine required sections present, in canonical order:

1. Status — L46
2. Context and Problem Statement — L55
3. Decision Drivers — L108
4. Considered Options — L155
5. Decision Outcome — L744
6. Solution Architecture — L842
7. Implementation Approach — L990
8. Security Considerations — L1083
9. Consequences — L1193

No missing sections, no out-of-order sections, no duplicated headings. Context-aware
sections (Market Context, Required Tactical Designs, Upstream Design Reference) are
correctly absent — none is triggered (public design, not strategic-altitude, not
spawned_from a parent).

## Q2 — Frontmatter: FAIL (one placement deviation + one advisory)

Correct:
- Required fields `status`, `problem`, `decision`, `rationale` all present.
- Their relative order is canonical (status → problem → decision → rationale).
- `problem` (L4), `decision` (L15), `rationale` (L30) all use YAML literal block
  scalars (`|`). `status` is a bare scalar (correct for a single word).
- FC03 satisfied: frontmatter `status: Proposed` (L2) matches the body `## Status`
  first non-blank line `Proposed` (L48), which stands alone with prose pushed to a
  later paragraph.

Findings:
- **`upstream` is mis-placed (FAIL-level, in-scope for Q2).** The document puts
  `upstream: docs/prds/PRD-niwa-onboard.md` on L3, interposed *between* `status`
  and `problem`, splitting the required-field run. The canonical example places the
  optional `upstream` field *after* the required block (reference L33, after
  `rationale`). Optional fields should follow the required run, not break it.
  Fix: move the `upstream` line to sit after `rationale:` (after L41).
- **`schema: design/v1` is absent (advisory).** The reference's Frontmatter section
  (L44) lists `schema` among required fields and its example (L24) leads with
  `schema: design/v1` ("Pins the artifact-type contract"). The document has no
  `schema` field. Note the reference is internally inconsistent: its Validation
  Rules FormatSpec (L230) declares required fields as only
  status/problem/decision/rationale, excluding schema, so `shirabe validate` FC01
  would pass. Because the review question scoped required fields to
  (status, problem, decision, rationale), this is advisory, not the basis for the
  FAIL — but adding `schema: design/v1` as the first frontmatter line is the correct
  fix and removes the ambiguity.

## Q3 — Section-altitude conformance: PASS

- No PRD-altitude requirements restated as new requirements. The design cites the
  PRD's R1–R22, AC-*, D*, US-* by reference throughout; it never introduces a new
  R-number or defines a requirement. Prose uses of "must" are tied to cited
  requirements (e.g. L72 "must run against the operator's own ... session (R5)"),
  which is citation, not authoring.
- No PLAN-altitude atomic issue list. Implementation Approach (L990–L1081) names
  Phases 0–8 with deliverables and test surface — batch/phase altitude, which the
  reference explicitly expects ("Names the batches or phases"). No Implementation
  Issues table is carried (correctly owned by the downstream PLAN). Phases reference
  AC-* for test coverage but do not enumerate issues with IDs/estimates.

## Q4 — Budget-vs-spec heuristic: PASS (no strict numeric budgets to breach)

The format reference documents no per-section numeric prose budgets, so the ">50%
overshoot" test has no quantitative anchor for body sections. The one measurable
budget is the frontmatter "1 paragraph" constraint on `problem`, `decision`,
`rationale`:
- `problem` (L4–14), `decision` (L15–29), `rationale` (L30–41) are each a single
  unbroken paragraph (no internal blank lines), so each satisfies the
  one-paragraph structural rule. They are dense/long but not multi-paragraph, so
  they do not breach the documented budget.
Considered Options (L155–743, ~590 lines across five fully-elaborated decisions) is
large but proportionate to a genuine 5-decision design; the reference demands
non-strawman depth here, so length is warranted, not overshoot. No section flagged.

## Q5 — Public-visibility hygiene: FAIL (two banned words)

Clean:
- **No `wip/` path references** anywhere in the document (the R25 carve-out is not
  even needed — the doc never names wip).
- **No private repos/paths/issue numbers.** The only issue refs are
  `tsukumogami/niwa#194` (L51) and `tsukumogami/niwa#199` (L52), both explicitly
  allowed. No private-repo (vision/tools/coding-tools/overlay) references.
- **No leaked local filesystem paths.** Paths named are repo-relative code paths
  (`internal/onboard`, `internal/vault/infisical/management.go`, `test/functional/`),
  the repo-relative `docs/prds/PRD-niwa-onboard.md`, and the XDG `~/.config/niwa/` /
  `.niwa/instance.json` locations that are part of the architecture. No absolute
  machine paths (no `/home/...`).
- **No emojis.**

Violations (writing style — CLAUDE.md hard rule):
- **"Comprehensive" — L474.** Alternative heading: "**Comprehensive upfront
  doctor-depth probe, silent-leaning topology, external prompt library.**" Banned
  ("comprehensive/holistic"). Fix: "Exhaustive upfront…" or "Full upfront…".
- **"free tier" — L1242/1243.** Body: "An org on the provider's free tier can
  exhaust its identity allotment…". Banned ("tier"). Note the author already avoided
  it in the adjacent heading ("The free-plan identity cap…", L1241) — this is a slip
  in the body. Fix: "on the provider's free plan".

No occurrences of "robust", "leverage", "facilitate", or "holistic" found.

---

## Verdict rationale

FAIL is rendered on two in-scope, concrete nonconformances:
1. Q2 — `upstream` interposed between `status` and `problem` rather than placed
   after the required-field run (canonical placement is post-`rationale`).
2. Q5 — two banned writing-style words ("Comprehensive" L474, "free tier" L1243).

Remediation is four small edits (move `upstream`; optionally add `schema:
design/v1`; reword two terms). Section structure, altitude, budget, and all other
hygiene dimensions are clean and exemplary. This is a soft FAIL: fix the four items
and the artifact is Accept-ready on structural-format grounds.
