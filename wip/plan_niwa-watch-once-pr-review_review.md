# PLAN Review: niwa watch --once PR-review dispatch

**Artifact reviewed:** `docs/plans/PLAN-niwa-watch-once-pr-review.md`
**Upstream:** `DESIGN-niwa-watch-once-pr-review.md` (Accepted), `PRD-niwa-watch-once-pr-review.md` (In Progress)
**Mode:** single-pr, 8 issues
**Verdict:** CONCERNS (minor -- two AC-coverage gaps; sequencing and atomicity are sound)

---

## 1. Coverage

### DESIGN implementation steps 0-7 -> issues

| Design step | Issue | Status |
|---|---|---|
| 0. State schemas | Issue 1 | Covered |
| 1. GitHub poll (CurrentLogin, SearchReviewRequestedPRs, CreateReview) | Issue 2 | Covered |
| 2. Workspace intersection + dedup + bound | Issue 3 | Covered |
| 3. Hardened PR-head fetch (isolated) | Issue 4 | Covered |
| 4. Containment surface (env allowlist, profile, re-verify) | Issue 5 | Covered |
| 5. watch --once orchestration | Issue 6 | Covered |
| 6. Post/discard subcommands | Issue 7 | Covered |
| 7. Adversarial / live-enforcement test | Issue 8 | Covered |

All 8 design steps map 1:1 to issues. Clean.

### PRD Requirements R1-R17 -> issues

| Req | Home | Status |
|---|---|---|
| R1 stateless single-shot, no resident process | Issue 6 (AC1) | Covered |
| R2 user-review-requested | Issue 2, Issue 3 | Covered |
| R3 workspace restriction | Issue 3 | Covered |
| R4 metadata-only prompt | Issue 6 | Covered |
| R5 dispatch with --detach | Issue 6 | Covered |
| R6 read in clone, write to known location, halt | Issue 4 (checkout) + Issue 6 (prompt) + Issue 7 (draft path) | Covered |
| R7 containment (no egress, writes scoped, fail-closed) | Issue 5 (build) + Issue 8 (egress + write proof) | Partial -- fail-closed permission-mode behavior not live-tested (see gap A) |
| R8 env allowlist | Issue 5 | Covered |
| R9 fail-closed refuse | Issue 6 | Covered |
| R10 bounded, deterministic ordering | Issue 3 | Covered |
| R11 handled-set, on-success-only | Issue 1 + Issue 3 + Issue 6 | Covered |
| R12 fail loud | Issue 6 | Covered (poll branch under-traced -- see gap B) |
| R13 agent-view surfacing | Issue 6 (AC) | Covered |
| R14 trusted post/discard | Issue 7 | Covered |
| R15 deterministic pure-function prompt | Issue 6 (AC18) | Covered |
| R16 GitHub-only, structural | Issue 2 (query) + Issue 3 (query-shape test) | Covered implicitly; R16 never cited by number |
| R17 adversarial verification | Issue 8 | Covered |

### PRD Acceptance Criteria AC1-AC21 -> issues

| AC | Cited in issue | Status |
|---|---|---|
| AC1 (R1,R5) | Issue 6 | Covered |
| AC2 (R4) | Issue 6 | Covered |
| AC3 (R2) | Issue 3 | Covered |
| AC4 (R3) | Issue 3 | Covered |
| AC5 (R11) | Issue 1, Issue 3 | Covered |
| AC6 (R12,R1) empty poll | Issue 6 | Covered |
| AC7 (R6) draft artifact | Issue 6 | Covered |
| AC8 (R10) bound N | Issue 3 | Covered |
| AC9 (R7 egress) | Issue 8 | Covered |
| AC10 (R7 writes) | Issue 8 | Covered |
| **AC11 (R7 fail-closed)** | **none** | **GAP A -- no explicit test home** |
| AC12 (R8 canary) | Issue 5 | Covered |
| AC13 (R9) | Issue 6 | Covered |
| AC14 (R17) | Issue 8 | Covered |
| AC15 (R14 act boundary) | Issue 7 (+ 5/8 baseline) | Covered |
| AC16 (R14 discard) | Issue 7 | Covered |
| AC17 (R11,R12 dispatch-fail) | Issue 6 | Covered |
| AC18 (R15 pure prompt) | Issue 6 | Covered |
| **AC19 (R12 poll-fail branch)** | **Issue 6, folded into generic "poll/dispatch failure" clause; AC19 not named** | **GAP B -- under-traced** |
| AC20 (R13 agent view) | Issue 6 | Covered |
| AC21 (R14 post success) | Issue 7 | Covered |

### Named coverage gaps

**GAP A -- AC11 (fail-closed permission mode) has no acceptance-criteria home.**
AC11 is a *behavioral* criterion: a tool action Claude Code would normally gate behind an approval prompt, attempted in the unattended session, must be **denied** rather than auto-approved. Issue 5 *builds* "a fail-closed permission mode" into the profile, but that is a construction AC, not a behavioral verification. Issue 8 is the live-enforcement issue, yet its ACs enumerate only: hostile-PR dispatch, outbound network (domain + raw socket), and out-of-instance write. Nothing -- unit or live -- asserts that a normally-gated action is denied in the unattended session. AC11 is one of the three legs of R7's containment triad (egress / writes / fail-closed); the first two are live-proven in Issue 8 and the third is not. Recommend adding an AC11 leg to Issue 8 (attempt a gated tool action in the session, assert denial) or an explicit behavioral AC to Issue 5.

**GAP B -- AC19 (poll-failure branch) is not distinctly traced.**
Issue 6's failure AC reads "Poll/dispatch failure: print the error, exit non-zero, and do not record the affected PR as handled (PRD R12, AC17)." It cites AC17 (which is the *dispatch*-failure case) but not AC19. AC19 is deliberately distinct: it is the *poll-itself-fails* branch (expired/absent auth, rate limit, unreachable host), and the PRD explicitly wants it distinguished from the empty-poll **success** path (AC6). The generic "poll/dispatch failure" wording arguably reaches it, but the plan never names AC19 and never calls out the AC6-vs-AC19 distinction (empty success vs. poll error), so a reader implementing Issue 6 could conflate them. Recommend adding AC19 by number to Issue 6 with the explicit "distinct from empty-poll success (AC6)" contract.

**Minor:** R16 (GitHub-only, enforced structurally by the query) is satisfied by Issues 2/3 but never cited by number. Non-blocking.

---

## 2. Sequencing

**Graph is acyclic and correct.** Edges:
`1->3,6,7`; `2->3,6,7`; `3->6`; `4->6`; `5->6,8`; `6->7,8`.
A valid topological order exists: {1,2,4,5} (no deps) -> 3 -> 6 -> {7,8}. No cycles.

**Security ordering is right.** The two sharpest surfaces -- the hardened fetch (Issue 4) and the containment surface (Issue 5) -- are isolated into their own issues and both precede the orchestration join (Issue 6). The live adversarial boundary proof (Issue 8) is last and depends on {5,6}, correctly positioned as the release gate. The plan explicitly flags 4/5/8 for elevated review scrutiny, matching the design's "enforcement is the crux."

**No missing dependencies found.**
- Issue 7 (post) needs CreateReview (I2), the staged-review record (I1), and the handle (I6): all three are edges. Correct.
- Issue 8 exercises the full contained dispatch, which includes the hardened fetch (I4). It does not list I4 directly, but I8->I6->I4 makes the fetch transitively present by the time I8 runs. Acceptable; not a missing edge.
- Issue 6 requires all of 1-5: all five edges present. Correct.

The stated critical path `(1,2)->3->6->8` is accurate.

---

## 3. Atomicity

Each issue is scoped to one component with its own files and focused tests, independently implementable in a session:
- Issues 1, 2, 4, 5 are self-contained leaf components (state, client methods, fetch, containment) with no cross-file coupling.
- Issue 3 is pure selection logic with table tests.
- Issue 6 is the integration join -- the largest -- but it is wiring pre-built components (fetch, containment, select, state) into the verb, which is a reasonable single session given 1-5 land first.
- Issues 7 and 8 are narrowly scoped (two subcommands; one live test).

No issue is too big or too vague. Issues 6 and 7 both touch `internal/cli/watch.go`; since this is single-pr on one branch with sequential ordering (7 after 6), there is no conflict.

---

## 4. Single-PR fitness

**single-pr is the right mode.** This is one cohesive deliverable -- the `watch --once` feature and its containment -- that the design itself scopes to a single niwa PR reusing existing provisioning and settings-merge machinery. The security containment (fetch hardening + sandbox profile + live proof) only makes sense landing atomically with the verb it protects; splitting across PRs would ship the convenience without its guard, which the PRD forbids ("the convenience cannot ship unless the containment ... ships with it"). The design's own "Negative/costs" section acknowledges "a sizeable single PR" and accepts it. Realistically one PR: 8 issues, net-new but bounded surface (client methods, one containment path, two subcommands, one test file). Large but cohesive and defensible.

---

## 5. AC quality

Acceptance criteria are specific and testable throughout. Strong examples:
- Issue 2 pins exact endpoints and the `event` parameter contract (set by caller, never derived from body, defaults COMMENT).
- Issue 4 enumerates the concrete git hardening flags (`GIT_LFS_SKIP_SMUDGE=1`, `protocol.ext`/`protocol.file` disabled, empty `core.attributesFile`, `GIT_CONFIG_NOSYSTEM=1`) and gives two discriminating fixture tests (LFS no-smudge; export-ignore file still present).
- Issue 5's canary AC names the exact secrets/sentinels (`NIWA_CANARY_SECRET`, `SSH_AUTH_SOCK`, on-disk `~/.netrc`/`~/.config/gh`) and asserts the env is a subset of the allowlist.
- Issue 8 distinguishes a domain connection from a raw socket to a literal IP -- the exact distinction the design's Decision 7 needs to tell deny-all-namespace from proxy-only egress -- and wires a passing escape to fail the build.

Minor AC-quality notes (not blocking): the two gaps above are quality issues -- AC11's behavioral denial is unspecified anywhere, and Issue 6's failure AC collapses two distinct PRD ACs (17 dispatch, 19 poll) into one clause citing only AC17.

---

## Summary

Strong, well-sequenced plan. All 8 design steps and all 17 requirements have homes; 19 of 21 ACs are cleanly traced. The dependency graph is acyclic and correct, security-critical pieces are isolated and correctly ordered, and the live-enforcement test is properly last as the release gate. Two gaps keep this from a clean PASS: **AC11** (fail-closed permission-mode denial) has no explicit test home -- it is built in Issue 5 but never behaviorally verified, leaving one of R7's three containment legs unproven; and **AC19** (poll-failure branch) is folded into a generic "poll/dispatch failure" clause that cites only AC17, losing the PRD's deliberate AC6-vs-AC19 (empty-success vs. poll-error) distinction. Both are fixable by adding/renaming ACs on existing issues (8 and 6 respectively); neither requires a new issue.
