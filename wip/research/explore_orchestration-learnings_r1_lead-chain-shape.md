# Chain shape vs. outcome: what the Aug 1-4 dispatch batch actually shows

Research output for the orchestration-learnings exploration. Every claim below is
tagged **verified** (I read the brief, the PR body, the design doc, or ran the
command shown) or **inferred** (a reading of verified facts that could be wrong).

## Scope correction before anything else

The task brief says 17 workers. **Verified: the Aug 1-4 batch in
`/home/dgazineu/dev/niwaw/tsuku/.niwa/dispatch-briefs/` contains 29 files, of which
26 are worker briefs.** Command:

```
cd /home/dgazineu/dev/niwaw/tsuku/.niwa/dispatch-briefs/
for f in $(ls -t --time=mtime | head -60); do t=$(stat -c '%y' "$f" | cut -c1-16); \
  case "$t" in 2026-08-0[1234]*) echo "$f [$t] lines=$(wc -l < $f)";; esac; done
```

Excluded from the 26: `_common.md` (167 lines — a shared working agreement the Aug 2-3
briefs incorporate by reference, not a dispatch), `orchestration-learnings.md` (259
lines — this exploration), and `release-v0-13-0.md` (95 lines — a release-skill
dispatch with no issue and no code change, held out of the shape analysis).

One more accounting note that matters for the analysis: **one brief has no issue and
one issue has two briefs.** `shell-d-lifecycle-rerender.md` names no issue number, and
its PR (#2465) closes #2439 — the issue that `set-env-no-effect.md` was already
dispatched against. So 26 briefs map to 24 distinct issues and 23 PRs (one PR, #2442,
was closed unmerged).

---

## 1. The table

Shape codes: **FULL** = `shirabe:explore` -> `shirabe:scope` -> `shirabe:execute`.
**SCOPE** = `shirabe:scope` -> `shirabe:execute` (no separate explore). **WORK-ON+D** =
`shirabe:work-on` with escalation to `shirabe:decision` **stated in the Required
workflow section**. **WORK-ON(d)** = plain `work-on` in the Required workflow, with a
conditional "use `shirabe:decision` if…" hint buried in the Context section.
**WORK-ON** = plain `work-on`, no decision mention anywhere. **DESIGN** = design
artifact only, stop for human review.

Ordered by dispatch time (brief mtime, oldest first).

| Brief | Lines | Target | Shape | Stated reason (verbatim) | PR |
|---|---|---|---|---|---|
| `slack-wedge-design.md` | 148 | none (ED5 roadmap item) | DESIGN | "This is design, **not** implementation — no product code." | (draft PR, out of this repo's tracker) |
| `set-env-no-effect.md` | 95 | tsuku#2439 | WORK-ON | "The issue is fully written and pushed… This brief only adds the parts of the conversation the issue doesn't record." | **#2442 CLOSED** (superseded) |
| `niwa-227-env-secrets-stale-apply.md` | 85 | niwa#227 | WORK-ON | "Start by invoking `shirabe:work-on` on the issue" (no reason given) | niwa#229 MERGED |
| `verify-additional-dead-schema.md` | 76 | tsuku#2440 | WORK-ON | "it carries the full investigation, a worked demonstration, acceptance criteria and a validation script" | #2446 MERGED |
| `zig-mirror-unreachable.md` | 78 | tsuku#2441 | WORK-ON | "it carries the failing CI run, the error, mirror probe results and acceptance criteria" | #2444 MERGED |
| `test-recipe-pathspec-2445.md` | 84 | tsuku#2445 | WORK-ON | "it carries the reproduction, the affected lines and the acceptance criteria" | #2458 MERGED |
| `gdbm-patchelf-glibc-2447.md` | 69 | tsuku#2447 | WORK-ON | "it carries the full error, the evidence it is pre-existing, and the acceptance criteria" | #2452 MERGED |
| `git-source-cargo-2449.md` | 93 | tsuku#2449 | WORK-ON | "it carries the reproduced root cause and the acceptance criteria… Root cause is already established — start from it" | #2450 MERGED |
| `download-archive-fallback-2443.md` | 122 | tsuku#2443 | SCOPE | "Run the full tactical chain, then execute it… **The PLAN must come out with `execution_mode: single-pr`.**" | #2455 MERGED |
| `shell-d-lifecycle-rerender.md` | 196 | (none; PR closes #2439) | FULL | "Do not accept this brief's framing as settled — its job is to give you the evidence, not to pre-decide the artifact type or the design." | #2465 MERGED |
| `nvm-data-root.md` | 139 | tsuku#2464 | FULL | "Do not accept the issue's three candidate fixes as the complete option set, and do not assume the first one is right" | #2467 MERGED |
| `dependency-cleanup-actions.md` | 129 | tsuku#2462 | WORK-ON(d) | "This is a well-scoped bug with a clear fix; it does not need the explore/scope/design chain." | #2466 MERGED |
| `extract-symlink-escape.md` | 147 | tsuku#2473 | FULL | "Do not treat those as the complete option set and do not assume the first is right — the issue's job is to give you the evidence and a verified reproduction, not to pre-decide the design." | #2479 MERGED |
| `gc-prefix-collision.md` | 69 | tsuku#2474 | WORK-ON(d) | "This is a contained bug with a clear failure mode; it does not need the explore/scope/design chain." | #2491 MERGED |
| `storage-plan-fields.md` | 75 | tsuku#2468 | FULL | "The full chain rather than a plain `work-on`, because this is a `state.json` schema change with a backfill question and no schema version to gate on" | #2484 MERGED |
| `install-reinstall-flag.md` | 71 | tsuku#2463 | WORK-ON(d) | "The install machinery already supports reinstalling; this is a flag, a bypass, and two stale messages." | #2487 MERGED |
| `ci-push-codecov.md` | 50 | tsuku#2494 | WORK-ON(d) | "This is a CI configuration fix." | #2498 MERGED |
| `library-staging.md` | 65 | tsuku#2486 | WORK-ON(d) | "The tool path already demonstrates the sequence; this is applying it, not designing it." | #2518 MERGED |
| `stale-exported-plan.md` | 64 | tsuku#2496 | **WORK-ON+D** | "escalating to `shirabe:decision` on the marker question below. The problem is well understood; the cost of the obvious fix is what needs deciding." | #2504 MERGED |
| `shirabe-ci-gate.md` | 32 | shirabe#244 | WORK-ON | (no reason; the brief's distinguishing content is "This task is in a different repository") | shirabe#246 MERGED |
| `eval-deps-prefix.md` | 26 | tsuku#2480 | WORK-ON | (no reason; "The fix already exists one package over, and shipped hours ago.") | #2503 MERGED |
| `update-all-stale-cleanup.md` | 26 | tsuku#2470 | WORK-ON | (no reason stated) | #2515 MERGED |
| `design-docs-gc.md` | 28 | tsuku#2488 | WORK-ON | "Documentation correction, not a design exercise." | #2502 MERGED |
| `functional-undefined-steps.md` | 26 | tsuku#2492 | WORK-ON(d) | (no reason in the workflow line; "Use `shirabe:decision` if the blast radius is larger than it looks") | #2510 MERGED |
| `doctor-path-precedence.md` | 26 | tsuku#2475 | WORK-ON(d) | (no reason in the workflow line; "Decide what 'wins' means, and use `shirabe:decision`.") | #2513 MERGED |
| `fish-shell-support.md` | 31 | tsuku#2471 | **WORK-ON+D** | "run `shirabe:decision` on the direction before writing code — this is the one issue in the current batch where the answer is genuinely open." | #2508 MERGED |

**Verified** via `wc -l` on each brief and `gh issue view <n> --json closedByPullRequestsReferences`.

Note the brief-length collapse over the batch: Aug 1 briefs run 76-148 lines, Aug 2
briefs 129-196, and the Aug 3 16:34-16:35 cohort runs 26-32. The short ones all
delegate their standing content to `_common.md` (167 lines), which the long Aug 1
briefs did not have. **Inferred: brief length is not comparable across the batch and
should not be used as a proxy for anything.**

---

## 2. Outcomes

All 24 issues are CLOSED/COMPLETED. **Verified** with
`gh issue view <n> --repo tsukumogami/tsuku --json number,state,stateReason`.

| PR | Shape | State | +add/-del | Files | Commits | Rejected or revised the stated direction? |
|---|---|---|---|---|---|---|
| #2465 | FULL | MERGED | +3929/-407 | 47 | 12 | **Yes, emphatically.** Rejected the brief's own suggested mechanism. |
| #2455 | SCOPE | MERGED | +2538/-99 | 27 | 11 | Partly — rejected two naming shapes and the "widen `url` to string-or-array" shape. |
| #2467 | FULL | MERGED | +1717/-16 | 18 | 21 | **Yes** — rejected both of the issue's first two candidate paths. |
| #2479 | FULL | MERGED | +1435/-107 | 6 | 5 | **Yes** — rejected the issue's first suggestion and the in-tree precedent the brief pointed at. |
| #2466 | WORK-ON(d) | MERGED | +1352/-126 | 20 | 14 | Yes — rejected "merge the dependency's cleanup actions into the parent's". |
| #2513 | WORK-ON(d) | MERGED | +1163/-1 | 7 | 1 | Partly — chose warn-only, declined the acknowledgment mechanism. |
| #2484 | FULL | MERGED | +1087/-354 | 10 | 7 | Yes — rejected the `PlanFormatVersion` bump, "the obvious alternative". |
| #2442 | WORK-ON | **CLOSED** | +1067/-147 | 23 | — | Superseded by #2465. See §3. |
| #2515 | WORK-ON(d) | MERGED | +1014/-323 | 11 | 7 | No explicit rejection recorded. |
| #2446 | WORK-ON | MERGED | +994/-60 | 22 | 6 | No — widened scope (four extra drop sites closed alongside). |
| #2487 | WORK-ON(d) | MERGED | +986/-24 | 10 | 5 | No explicit rejection recorded. |
| niwa#229 | WORK-ON | MERGED | +620/-49 | 12 | — | Not assessed (different repo, no review file). |
| shirabe#246 | WORK-ON | MERGED | +596/-7 | 9 | — | Yes — the brief said "the issue claims `SKIPPED`… confirm both"; the PR gates on `bucket`. |
| #2504 | WORK-ON+D | MERGED | +698/-49 | 15 | 4 | Yes — and it **reverted part of itself mid-PR** (see §5). |
| #2450 | WORK-ON | MERGED | +587/-30 | 7 | 3 | No. |
| #2518 | WORK-ON(d) | MERGED | +553/-5 | 3 | 2 | Partly — declined to import a failure mode, filed #2512 about it. |
| #2491 | WORK-ON(d) | MERGED | +549/-85 | 8 | 4 | **Yes** — "The issue suggested two directions. Validating the remainder lexically does not work." |
| #2452 | WORK-ON | MERGED | +520/-66 | 6 | 5 | No. |
| #2503 | WORK-ON | MERGED | +364/-113 | 4 | 2 | Yes — has a "The decision, and the alternative" section, with no brief instruction to run one. |
| #2510 | WORK-ON(d) | MERGED | +354/-41 | 8 | 3 | No explicit rejection recorded. |
| #2508 | WORK-ON+D | MERGED | +266/-17 | 6 | 1 | **Yes** — chose "drop fish", the brief's second option, and filed #2505 for the first. |
| #2458 | WORK-ON | MERGED | +129/-3 | 2 | 5 | No. |
| #2502 | WORK-ON | MERGED | +86/-34 | 3 | 3 | No. |
| #2444 | WORK-ON | MERGED | +72/-30 | 2 | 5 | Self-correcting: "An earlier revision of this PR ranked candidates by DNS address count and claimed the alternatives were all single-address. That was wrong and has been removed." |
| #2498 | WORK-ON(d) | MERGED | +8/-2 | 2 | 4 | Yes — "Rejected outright: `skip_validation: true` and `use_pypi: true`". |

**Verified** via `gh pr view <n> --json number,state,additions,deletions,changedFiles,commits,body`.

### The PR that contradicted its brief hardest

**#2465 (FULL, shell.d lifecycle).** The brief is literally titled
`shell-d-lifecycle-rerender.md` and its problem statement concludes:

> "The remaining direction is genuine **re-rendering**: after any event that changes
> which version is active, regenerate that tool's shell.d files… the shape above is an
> observation, not a mandated design."

The design doc the worker produced says (verified,
`docs/designs/current/DESIGN-shell-d-lifecycle.md`, "Decision Outcome"):

> "This design does not deliver a re-render. The brief anticipated one while
> explicitly leaving the shape open; the conclusion is that re-rendering is the wrong
> primitive and the better move is to make it unnecessary."

Re-rendering is Option A in the doc, and it is rejected on coverage: `source_command`
mode can only be reproduced by executing a binary from the tool directory, which
`tsuku remove` should not do.

---

## 3. Does the shape correlate with anything measurable?

### 3a. Diff size — the correlation holds directionally and is not clean

**Verified** from the table above.

- FULL (n=4): total churn 4336, 1733, 1542, 1441. Median ≈ 1638. Files: 47, 18, 10, 6.
- SCOPE (n=1): 2637 churn, 27 files.
- WORK-ON+D (n=2): 747, 283.
- WORK-ON / WORK-ON(d) (n=19): median total churn ≈ 600; range 10 to 1478.

FULL's *smallest* (#2484, 1441) is smaller than WORK-ON's largest (#2466, 1478), and
#2513 (1164), #2442 (1214) and #2515 (1337) all sit inside FULL's range. File counts
overlap worse: #2446 (22f) and #2466 (20f) beat three of the four FULL PRs.

**So: FULL work is bigger on average, and diff size does not separate the shapes.**
A coordinator picking shape by expected diff size would have mis-sorted at least
three of the nineteen work-on issues.

### 3b. Did FULL surface a design rejection? — yes, all four, and the doc is the evidence

**Verified** by reading each design doc's Considered Options section.

- **#2465** — the doc says: "Four mechanisms were built out in full — each championed
  by an agent that then attacked its own design — and cross-examined on two axes."
  Options A (re-render), B (store rendered bytes per version), D (containment only),
  C (version-key the filename, chosen). Option B was rejected on a *measurement*:
  "`nvm.sh` is 161,810 bytes; gzipped and base64'd it is 46,236 bytes per version per
  shell, and `state.json` is parsed in full by effectively every command — a measured
  240 µs to 5.0 ms on a clean install."

  **Correction to the task's premise:** the number is **four**, not six.
  `grep -n "^### Option" docs/designs/current/DESIGN-shell-d-lifecycle.md` returns
  four. The count reaches six only if you add the three cheap fixes the *brief*
  pre-ruled-out. The rest of the claim — that it rejected the brief's own suggestion —
  is verified and quoted above.

- **#2479** — "Five mechanisms were evaluated": `os.Root` (chosen), `EvalSymlinks`
  resolve-then-check, `securejoin`, two-pass header validation, staging + verify. The
  rejection that earns the exploration: `securejoin` "**Returns `err=<nil>` for every
  attack path** — it clamps rather than rejects (`b/pwned` -> `dest/pwned`)", measured,
  not reasoned. And `EvalSymlinks` — which the *brief* pointed at as "the shape the
  issue's suggested direction is gesturing at, already written and reviewed in this
  repo" (`internal/actions/install_program_files.go`) — is marked **disqualifying**
  against the #2275 bottle case because those links dangle at extraction time.

- **#2467** — the issue named three candidate fixes; the first was
  "`$TSUKU_HOME/share/nvm`". The design rejected `share/` ("`share/` cannot carry this
  [deletion] policy because its existing contents need the opposite") and rejected
  `$HOME/.nvm` with a checked-against-source refutation: "Its two headline arguments
  both reverse on inspection… the Homebrew precedent, checked against the formula,
  writes nothing into `$HOME/.nvm` either." Chose a new `$TSUKU_HOME/data/` tree.

- **#2484** — rejected the `PlanFormatVersion` 5->6 bump, "the obvious alternative",
  on coverage and on a counted cost: "the bill is 110 golden files plus a
  version-history entry claiming the executor plan format changed, when the eval output
  is byte-identical before and after."

**Verdict: the exploration earned its cost in all four FULL cases, by the measure "it
killed a plausible option with evidence the issue did not contain."**

### 3c. Did a bare work-on turn out to need design? — yes, once, and it is the batch's best evidence

**Verified.** `set-env-no-effect.md` (WORK-ON, tsuku#2439, dispatched Aug 1 14:03)
produced **PR #2442, +1067/-147 across 23 files, CI green — and then closed unmerged.**

The subsequent `shell-d-lifecycle-rerender.md` brief explains why, in the coordinator's
own words:

> "I fixed that narrowly in **PR #2442**… Routing `set_env` through shell.d works for a
> single installed version, but it made an existing structural gap load-bearing:
> **nothing in the system ever re-renders a shell.d file.**"

> "This is **not specific to `set_env`.** I verified the same run leaves `nvm.bash` —
> written by the pre-existing `install_shell_init` — still containing **0.40.6's
> entire `nvm.sh` script** after 0.40.6 is removed."

So the work-on produced a correct, tested, green fix for the stated bug, and the fix
was structurally unshippable — the same session had to be re-dispatched as a FULL chain
that discarded the mechanism. That is under-shaping caught by the coordinator rather
than by the worker, and it cost roughly 1200 lines of thrown-away diff.

**Inferred, and important: the work-on did not fail. It surfaced the gap.** The brief
for the FULL run says "Read that PR and its branch first — it is a working prototype
and its diff is the cheapest way to absorb the mechanism." Read that way, #2442 is a
spike that was mislabelled as a fix, and the shape error is one of naming, not of
sequencing.

Secondary, weaker under-shaping signal: **#2508 (WORK-ON+D, fish)** resolved the
contested question by *deferring the expensive half* — it filed **#2505 (OPEN):
"feat(shellenv): deliver shell init fragments to fish users"**. The decision framework
picked the cheap branch. The review then found the PR's replacement docs promised
completions delivery tsuku does not have, and a **third** issue, **#2520 (OPEN):
"install_completions writes files no shell ever reads"**, now exists in the same area.
**Inferred: the fish question was a design question wearing a bug's clothes, and
work-on+decision bought a partial answer.**

### 3d. Did any FULL issue turn out trivial? — no

**Verified.** The smallest FULL PR is #2484 at 1441 lines of churn over 10 files, and
its design doc contains a decision (`StorageVersion` marker vs. `PlanFormatVersion`
bump) whose rejected branch had a counted 110-file cost. None of the four is trivial by
diff, and none has a design doc whose Considered Options section is perfunctory.

The nearest thing to over-shaping is **SCOPE (#2455, download-archive fallback):
+2538/-99 across 27 files for a feature whose recipe-facing surface is one new array
field.** Its D1 decision spends its argument on *naming* (`fallback_urls` vs `mirrors`
vs `sources`) and on whether to widen `url` to string-or-array. Those are real
decisions, but they are the kind an experienced reviewer settles in a comment.
**Inferred: this is the one case where the chain may have cost more than it returned —
but note the chain was mandated as `--auto` with "decisions following the recommended
option", so it was cheap to run.**

---

## 4. The reasons, clustered

Every stated reason, grouped. The cluster labels are properties of the work, as asked.

**Cluster A — "the mechanism already exists in-tree; this is application, not design."**
Always WORK-ON.
- library-staging: "The tool path already demonstrates the sequence; this is applying
  it, not designing it."
- install-reinstall-flag: "The install machinery already supports reinstalling; this is
  a flag, a bypass, and two stale messages."
- eval-deps-prefix: "The fix already exists one package over, and shipped hours ago."
- git-source-cargo: "Root cause is already established — start from it."

**Cluster B — "the failure mode is known, contained, and its blast radius is bounded."**
Always WORK-ON.
- gc-prefix-collision: "This is a contained bug with a clear failure mode; it does not
  need the explore/scope/design chain."
- dependency-cleanup-actions: "This is a well-scoped bug with a clear fix; it does not
  need the explore/scope/design chain."
- ci-push-codecov: "This is a CI configuration fix."

**Cluster C — "the artifact is not code."** Always WORK-ON.
- design-docs-gc: "Documentation correction, not a design exercise."

**Cluster D — "the issue is already an investigation; the brief only adds what the
issue can't know."** Always WORK-ON. This is the Aug 1 cohort's whole framing.
- verify-additional: "it carries the full investigation, a worked demonstration,
  acceptance criteria and a validation script"
- zig-mirror: "it carries the failing CI run, the error, mirror probe results and
  acceptance criteria"
- test-recipe-pathspec: "it carries the reproduction, the affected lines and the
  acceptance criteria"
- gdbm-patchelf: "it carries the full error, the evidence it is pre-existing, and the
  acceptance criteria"

**Cluster E — "a persisted-schema change with a backfill question and no version to
gate on."** FULL.
- storage-plan-fields: the reason quoted verbatim in the table. This is the only brief
  in the batch that states its shape choice as a general rule about a *property of the
  change*.

**Cluster F — "the option set in the issue is not the option set."** FULL.
- nvm-data-root: "Do not accept the issue's three candidate fixes as the complete
  option set, and do not assume the first one is right"
- extract-symlink-escape: "Do not treat those as the complete option set and do not
  assume the first is right"
- shell-d-lifecycle: "Do not accept this brief's framing as settled"

**Cluster G — "one named question is genuinely open; everything around it is
settled."** WORK-ON+D.
- fish-shell-support: "this is the one issue in the current batch where the answer is
  genuinely open."
- stale-exported-plan: "The problem is well understood; the cost of the obvious fix is
  what needs deciding."

### Which criteria the data supports, and which are only plausible

**Supported by the data:**

- **Cluster E (persisted-schema change with a backfill question) predicts FULL.**
  Supported: #2468 was dispatched FULL on exactly this reason and the design doc's
  Decision 1 is titled "What a read of a pre-fix record means." The property is
  observable before dispatch (does the change alter something already written to
  `state.json`? is there a version field to gate on?).
- **Cluster F (the issue's option set is incomplete) predicts FULL, and it paid off
  every time.** All three FULL-with-this-reason PRs rejected an option the issue or
  brief named. Verified in §3b.
- **Cluster G (exactly one open question, bounded) predicts WORK-ON+D.** Both #2508 and
  #2504 record a real decision with rejected alternatives, and neither produced a
  design doc.
- **Cluster A (mechanism exists in-tree) predicts WORK-ON, reliably.** All four
  produced merged PRs with no design doc and no escalation to a chain.

**Plausible but not supported:**

- **"Documentation correction" (Cluster C) as a low-risk shape.** n=1, and the review
  found a real problem with it (PR-2502: the new citation goes stale the moment #2503
  merges). One data point, and it went wrong.
- **"CI configuration fix" (Cluster C-adjacent) as low-risk.** #2498 is +8/-2 and
  clean, but #2510 (also test/CI-shaped, WORK-ON(d)) is +354/-41 and #2452 is +520/-66.
  The label does not predict size.
- **Cluster D ("the issue is already an investigation") as a criterion at all.** This
  is a property of the *issue-writing*, not of the work — and the issue was written by
  the same coordinator. It is circular: the coordinator decides how much to investigate
  before filing, then cites its own investigation as evidence the work is contained.
  #2439 is the counter-example (§3c): a fully investigated issue whose contained fix
  was structurally wrong.

---

## 5. Counter-evidence

**5a. WORK-ON PRs rejected the stated direction just as often as FULL ones.** The
sharpest is **#2491** (gc-prefix-collision, WORK-ON(d)):

> "## The decision, and the alternative
> The issue suggested two directions. Validating the remainder lexically does not work"

That is exactly the behavior §3b credits to the FULL chain, produced by a `work-on`
whose brief said it "does not need the explore/scope/design chain." **#2503** did the
same with *no* decision mention in its brief at all. **So "design rejection happens"
does not distinguish the shapes — only whether a durable design doc is produced does.**
Verified: `gh pr view <n> --json files` shows only the four FULL PRs and #2455 *created*
a `docs/designs/current/DESIGN-*.md`; #2466, #2487, #2502 and #2504 only edited
existing ones.

**5b. The two WORK-ON+D PRs got the worse review verdicts.** See §6. If escalating to
`shirabe:decision` reliably improved quality, the direction should run the other way.
n=2 on each side, so this is a flag, not a finding.

**5c. #2504 reverted part of itself inside the PR.** The review verified: "The revert
is clean: no `TestGoldenPlansCarryTheStorageMarker` left behind, and
`scripts/regenerate-golden.sh` is byte-identical to `main` (commit `16467afb`'s message
claimed it 'stops stripping' the key, which was never true — it never stripped it;
`dc54cd6b` states this correctly)." A brief that mandates a decision on one named
question does not stop the worker from thrashing on a second, unnamed one.

**5d. The `_common.md` confound.** The Aug 3 16:34-16:35 cohort (26-32 lines) and the
Aug 1 cohort (76-148 lines) are not comparable, because the short briefs delegate 167
lines of standing agreement to `_common.md` while the Aug 1 briefs carry theirs inline.
Any criterion phrased in terms of "how much the coordinator wrote" is measuring the
refactor, not the work.

**5e. Brief-length and shape are almost perfectly confounded with dispatch date.** All
four FULL briefs are Aug 1-2; every Aug 3 brief is work-on. **Inferred: the shift may
reflect the backlog shifting from "structural gaps found by review" to "sibling bugs
filed by the previous PRs" — but a purely chronological explanation ("the coordinator
got more confident / more rushed") fits the same data and cannot be ruled out from these
artifacts.**

**5f. The tidy criteria do not explain #2446.** `verify-additional-dead-schema.md` is
Cluster D, dispatched WORK-ON, and the PR came out at +994/-60 across 22 files after
closing "four other places that would have dropped the field" the issue did not name.
It was right to widen — but no criterion in §4 predicted it would need to.

---

## 6. Reviews

Four review files exist, all written Aug 3 ~20:21-20:35 local. **Verified: all four
target work-on-shaped PRs, and no FULL-chain PR is among them** — the review batch ran
against PRs still open at that moment, and #2465, #2467, #2479 and #2484 had all
already merged. The `_review-guide.md` confirms this: it names those as background
context ("Seven PRs merged into `main` today, in this order…"), not as review targets.

| Review | PR | Shape | Verdict | Lead finding is… |
|---|---|---|---|---|
| PR-2502.md | #2502 | WORK-ON (docs) | merge, note for follow-up | **Defect in the artifact.** The new citation in `DESIGN-shell-d-lifecycle.md:220-230` goes stale the moment #2503 merges — "the exact class of defect this PR exists to remove." |
| PR-2504.md | #2504 | WORK-ON+D | **merge after addressing** | **Both.** Real defect: the warning fires on all 110 golden plans and every pre-existing `tsuku eval` output, demonstrated by running the built binary. Plus a **false claim in the body** — "the warning never fires on current `eval` output… so there is no habituation to overcome" is "load-bearing" and false. |
| PR-2508.md | #2508 | WORK-ON+D | **merge after addressing** | **Both.** False claim in shipped user docs (`SKILL.md:158` "On fish this gets you PATH and completions" — `tsuku shellenv` does not touch completions), plus a false reachability claim in the body ("that invalid wrapper is now unreachable") that hides a live migration gap for existing `state.json` records. |
| PR-2513.md | #2513 | WORK-ON(d) | merge, note for follow-up | **Defect in code**, verified by writing a throwaway probe test: a symlink from `~/.local/bin` into `$TSUKU_HOME` is reported as a shadow. `resolveOnPath` returns the unresolved path. |

Three of the seven PRs open at review time (#2498, #2503, #2510) got no file, which
under the guide's rule ("Write the file only if a human should act before merging")
means clean. All three are WORK-ON.

**Cross-reference against shape:**

- **The question "did FULL PRs get cleaner reviews" cannot be answered from this data.**
  No FULL PR was reviewed. Saying otherwise would be inventing the comparison.
- Within the reviewed set, the pattern runs *against* more shaping: both **merge after
  addressing** verdicts landed on the two WORK-ON+D PRs, and both of those contained a
  false claim in the PR body. The two lighter verdicts landed on WORK-ON and WORK-ON(d).
  **Inferred, weakly (n=4): a mandated decision escalation produces a PR body that
  argues harder, and arguing harder produces more claims that can be falsified.** The
  review guide independently anticipates this: "These PRs are written by agents and are
  unusually thorough — and that is exactly why the body is not evidence."
- Three of four findings are **false or unverified claims**, not code defects (PR-2502's
  is a defect in a doc; PR-2504 and PR-2508 are mixed; only PR-2513's lead finding is
  purely code). **Inferred: at this batch's quality level, the residual risk has moved
  from "the code is wrong" to "the prose about the code is wrong", and chain shape has
  no visible effect on that.**

---

## Recoverable criteria

Ordered by how much evidence stands behind each.

**C1 — A change to a persisted schema with no version field to gate reads on takes the
full chain.**
*Supports:* #2468 was dispatched FULL for exactly this stated reason, and the design
doc's first decision is the backfill semantics; the chosen `StorageVersion` marker
beat the obvious `PlanFormatVersion` bump on a counted 110-golden-file cost. #2496 is
the same schema question one layer out (the marker did not survive `plan export`) and
was correctly given the lighter WORK-ON+D shape, because by then #2484 had settled
*what* the marker means and only its *cost* was open.
*Strains:* the property is stated once, in one brief. Nothing in the batch tests the
contrapositive — there is no schema change dispatched as bare work-on to see it fail.

**C2 — When the fix's option set is genuinely larger than the issue's, take the full
chain; the evidence is that a named option dies with data.**
*Supports:* all four FULL PRs killed an option the issue or brief named, each on
evidence the issue did not contain — a measured 46 KB/version/shell for Option B in
#2465; a measured `SecureJoin("b/pwned") -> dest/pwned, err=nil` in #2479; a
read-the-Homebrew-formula refutation in #2467; a counted 110-file bill in #2484.
*Strains:* **#2491 and #2503 did the same thing on `work-on`.** The chain is not what
produces the rejection; it is what produces the durable *record* of it. If the record
is what you want, C2 is a criterion for design-doc production, not for exploration.

**C3 — When the mechanism already exists in-tree and the work is applying it, take
work-on.**
*Supports:* four briefs state this property explicitly (library-staging,
install-reinstall-flag, eval-deps-prefix, git-source-cargo); all four merged with no
design doc and no chain escalation, at 553, 986, 364 and 587 added lines.
*Strains:* #2518 (library-staging, "applying it, not designing it") still filed three
follow-up issues, one of which — #2512, "a canceled or failed tool reinstall can leave
no installation at all, which is the failure mode this PR declined to import" — is a
design question about the very mechanism being copied. Copying a mechanism means
inheriting its unexamined failure modes, and nothing in the shape choice surfaces that.

**C4 — When exactly one question is open and everything around it is settled, take
work-on and mandate the decision on that named question.**
*Supports:* both WORK-ON+D briefs name the question in one clause (fish: which
resolution; stale-plan: the marker's cost), both PRs record a real decision with
rejected alternatives, both merged, neither needed a chain.
*Strains hard:* both got **merge after addressing**, and both contained a false claim
that a reviewer had to catch. #2508's decision also resolved by deferring the expensive
half into #2505, which is arguably not answering the question. n=2.

**C5 — "The issue is already a full investigation" is NOT a recoverable criterion.**
*Why it looks like one:* four Aug 1 briefs cite it and all four merged cleanly and
small.
*Why it isn't:* the coordinator wrote both the issue and the brief, so the criterion
reduces to "I already thought about it." #2439 is the falsifier — a fully investigated
issue with a validation script, dispatched work-on, whose green PR was thrown away
because the investigation had stopped one structural layer short. The property that
actually mattered there was not "how much investigation exists" but "does the fix make
an existing structural gap load-bearing", and no brief in the batch states that
property in advance.

**C6 — Diff size is not recoverable as a criterion and should not be back-fitted into
one.** FULL and work-on diffs overlap in both lines and files (§3a). A coordinator
cannot predict shape from expected size, and the observed correlation (FULL median
~1638 churn vs work-on ~600) is downstream of C1/C2, not independent evidence for them.
