# Phase 6 Structural-Format Review: DESIGN-niwa-watch-once-pr-review

Target: `docs/designs/current/DESIGN-niwa-watch-once-pr-review.md`
Reference: `skills/design/references/design-format.md` (design/v1)
Reviewer role: structural-format (artifact-shape conformance only)

## Verdict: CONCERNS (minor)

Two frontmatter deviations from the canonical shape; everything else conforms.
Neither concern is a validator-blocking (FC01-FC04/FC15) failure, but both are
real shape nits the reviewer flags.

---

## Q1. Section presence and order

PASS. All nine required sections are present in canonical order:

| # | Section | Line |
|---|---------|------|
| 1 | Status | 32 |
| 2 | Context and Problem Statement | 40 |
| 3 | Decision Drivers | 72 |
| 4 | Considered Options | 93 |
| 5 | Decision Outcome | 242 |
| 6 | Solution Architecture | 270 |
| 7 | Implementation Approach | 343 |
| 8 | Security Considerations | 371 |
| 9 | Consequences | 438 |

FC04 (all nine present) and FC15 (canonical order) both satisfied.

Status section (line 32-38): first non-blank line under `## Status` is the bare
word `Proposed` (line 34), followed by a blank line then explanatory prose. FC03
body-shape requirement satisfied — no prose on the status line (the most common
FC03 failure is avoided).

No context-aware sections (Market Context, Required Tactical Designs, Upstream
Design Reference) are required here: the design is tactical-altitude, single-PR,
public, and has no `spawned_from:`. Their absence is correct, not a gap.

## Q2. Frontmatter

PARTIAL / CONCERNS.

- status is `Proposed` (line 2). Correct enum value; matches body `## Status`
  first line (FC03 frontmatter-vs-body match satisfied).
- The four validator-required fields (status, problem, decision, rationale) are
  all present.
- problem (line 4), decision (line 11), rationale (line 21) all use YAML literal
  block scalars (`|`). Correct.
- The four required fields appear in canonical relative order:
  status -> problem -> decision -> rationale.

Two deviations:

1. **`schema: design/v1` is absent.** The format reference lists `schema` as a
   required frontmatter field (line 44: "Required fields: schema, status,
   problem, decision, rationale") and shows it as the first line of the canonical
   block. The document omits it. Note the validator's FC01 required-fields list
   (reference line 231) enumerates only status/problem/decision/rationale, so
   this likely does not fail FC01 — but it is a departure from the documented
   frontmatter contract and worth correcting for schema-pinning.

2. **`upstream` placement is inconsistent with canonical.** The canonical
   example places optional fields (`upstream`, `spawned_from`, ...) *after* the
   required trio, i.e. after `rationale`. Here `upstream` sits at line 3, wedged
   between `status` and `problem`, interrupting the required-field sequence. The
   four required fields keep their relative order, but the optional field is not
   placed consistently with the reference. Minor; recommend moving `upstream`
   below `rationale`.

The `upstream` value itself is fine: `docs/prds/PRD-niwa-watch-once-pr-review.md`
is a repo-relative public PRD path (not a private artifact, not a `wip/` path).

## Q3. Section-altitude conformance

PASS. Content sits at design altitude throughout.

- Context and Problem Statement grounds the gap in named existing code
  (`dispatch_launcher.go`'s `cmd.Env = os.Environ()`, the settings-merge seam,
  `internal/github`, `config.Discover`). It enumerates what "the feature must"
  do as (1)-(5), but frames these as the technical problem shape, not as newly
  minted PRD requirements. It cites PRD requirements/ACs by code (R7, AC12,
  AC9/AC14) rather than introducing them — exactly the DESIGN-cites-requirements
  posture the reference prescribes (content-boundary: "cites requirements ... but
  does not introduce new ones").
- Considered Options carries seven decisions, each with a chosen and a rejected
  option; rejections cite real weaknesses (flag collision, exfiltration channel,
  denylist-fails-open) traced to the drivers — no strawmen.
- Implementation Approach names six phases with sequencing and testability notes;
  it does NOT carry an Implementation Issues table (correctly left to the
  downstream PLAN) and does not decompose into atomic, individually-filed issues.
  It stays at phase/batch altitude.
- One borderline note (not a failure): Decision 2's Option 2A and the Security
  Considerations restate low-level git-hardening specifics
  (`GIT_LFS_SKIP_SMUDGE=1`, `protocol.ext`/`protocol.file`, `core.hooksPath`,
  `GIT_CONFIG_NOSYSTEM=1`). This is unusually concrete for a DESIGN, but it is
  load-bearing to justify the security boundary (the fetch runs in trusted code
  before the sandbox exists), so it reads as design-altitude justification rather
  than PLAN-altitude task decomposition. Acceptable.

## Q4. Length / budget overshoot (flag >50% only)

No section flagged. Considered Options (lines 93-241, ~148 lines) is by far the
longest, but it houses seven genuine decisions each with paired chosen/rejected
analysis; the length tracks the decision count, not padding. No single section
grossly overshoots a reasonable budget for its role. The security-heavy nature of
the feature justifies the Security Considerations and Decision 2/3 depth.

## Q5. Public-visibility compliance

PASS.

- No ED codes.
- No ROADMAP / STRATEGY / SPIKE references.
- No `tsukumogami/` or `vision` references. (A grep for `vision` matches only as
  a substring of `provision`/`provisioning`/`provisions` — false positives, not
  real references.)
- No `wip/` paths in prose or frontmatter.
- No banned writing-style words (tier/tiered, robust, leverage,
  comprehensive/holistic, facilitate). Confirmed by targeted grep.
- No AI attribution ("Generated with...", "Co-Authored-By: Claude").

Writing is direct, uses contractions, and avoids AI tells.

---

## Summary of actionable items

1. (Minor) Add `schema: design/v1` as the first frontmatter field.
2. (Minor) Move `upstream:` below `rationale:` so optional fields follow the
   required trio, matching the canonical frontmatter order.

Both are cosmetic-to-contract nits; no section-presence, section-order, or
FC03 body-shape problems, and full public-visibility compliance.
