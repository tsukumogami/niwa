# Architecture review — DESIGN-niwa-watch-once-pr-review (phase 6)

Scope: SOLUTION ARCHITECTURE + IMPLEMENTATION APPROACH only. Grounded against
`prd_niwa-watch-once-pr-review_phase2_niwa-surface.md`.

Verdict: **CONCERNS** — no hard conflicts with the grounding and the security
reasoning is sound, but several load-bearing mechanisms are under-specified or
internally contradictory and must be tightened before implementation. None
requires a redesign; all are specification-level fixes.

---

## Q1 — Is the architecture clear enough to implement?

Mostly yes. Strong points:

- Components enumerated with target files (`internal/cli/watch.go`, `internal/github`
  extension, containment surface, handled-set store, staged-review record).
- Data-flow diagram is concrete and matches the Decision Outcome step list.
- `PRRef{Owner, Repo, Number, URL, RequestedAt}` and the two new github method
  signatures are given.
- The metadata-only prompt is defined as a pure function of `PRRef` — the
  determinism/injection-proof property is cleanly expressed and testable.

What is NOT clear enough to hand to an implementer without follow-up decisions:
the three interfaces the review question flags (tree exposure, env-allowlist
threading, handle resolution) plus the `post` github method. Details in Q2.

## Q2 — Missing components / interfaces

### (a) Tree exposure without a checkout — SHARPEST GAP, internally contradictory
Decision 2A says expose the tree "by reading blobs by SHA / using a **bare-style
object store** rather than a working-tree checkout that honors filters." But the
metadata-only prompt (and the data-flow line `git fetch PR head into clone`)
tells the agent to read the PR "from its **local clone at the pre-fetched ref**."
"Bare object store, read blobs by SHA" and "local clone at a ref" are two
different mechanisms and the design never picks one:

- If there is no working tree, the agent must `git cat-file`/`git show <sha>:path`
  to read files — the prompt's "read from the local clone" framing is misleading
  and the agent has no ordinary file tree to open.
- If there IS a working tree, that is a checkout, and the design's own
  requirement is that it must NOT honor filters (LFS smudge/`.gitattributes`
  drivers). A filter-disabled checkout is achievable (`GIT_LFS_SKIP_SMUDGE=1`,
  `filter.lfs.smudge`/`.process` unset, empty `core.attributesFile`,
  `GIT_CONFIG_NOSYSTEM=1`), but the design describes this as *rejected* ("rather
  than a working-tree checkout").

Implementer needs one concrete answer: either (1) a filter-neutered checkout into
a writable path the agent reads as normal files, with the exact git settings that
neutralize smudge, or (2) no working tree + a prompt that instructs `git show`.
Right now the prose points both ways. Recommend option (1) with explicit git
flags — it matches the prompt and is the least surprising for the agent — and
rewrite Decision 2A to say "checkout with all filters disabled" instead of "no
checkout / bare object store."

### (b) Draft-write path vs "writes scoped to its clone" — tension
The draft location is `<instanceRoot>/watch-review-draft.md` (Decision / "known
draft location"), but Decision 3 scopes the sandbox's filesystem *writes* "to its
clone," and the clone is a subdirectory of the instance root, not the instance
root itself. If the sandbox write policy is the clone dir, the agent cannot write
the draft one level up. The writable region and the draft path must be reconciled
— either put the draft inside the clone (writable) or widen the write scope to
the instance root. Unspecified today; a launch-time contradiction if not fixed.

### (c) `post` needs a github `CreateReview` method not listed in Components
The Components section lists only `SearchReviewRequestedPRs` and `CurrentLogin`
as net-new github methods. But Decision 6 / data flow require
`POST /repos/{o}/{r}/pulls/{n}/reviews` from the trusted `post` path. Grounding
confirms the github client today has only `ListRepos`/`GetRepo` — so the review
POST is also net-new and belongs in the Components list with a signature (event
fixed by trusted code, body opaque). Currently missing.

### (d) Handle resolution under-specified
The staged-review record is `{handle, owner, repo, number, url, draftPath}`
persisted "alongside the existing `SessionMapping`," and `post <handle>` /
`discard <handle>` "resolve a handle to its PR and draft." But the design never
says what `handle` *is* or where its value comes from. Grounding shows dispatch
"captures session, writes durable `SessionMapping`" with a `shortID`. The natural
(and probably intended) answer is handle == the dispatch session shortID shown in
Agent View — the data flow even says "agent view shows the staged session … niwa
watch post <handle>." State it explicitly: handle = session shortID, and the
staged-review record keys off it. Also specify the store's location/format
(a file under `.niwa/`? a field appended to the SessionMapping record?) — "along-
side" is not a storage decision.

### (e) env-allowlist threading — implementable but signature unspecified
Decision 3 / Components say an env-allowlist parameter is "threaded into the
launch seam (`dispatchLaunch`)," with a synthetic `HOME`. Grounding pins the seam
at `realDispatchLaunch` (dispatch_launcher.go:25-46), `cmd.Env = os.Environ()` at
:40, and the `realDispatchLaunch` naming implies a swappable func var. The design
doesn't give the new signature or say whether the allowlist arrives as a param, a
field on a launch-spec struct, or a functional option — and it must default to
"no filtering" so "the ordinary dispatch path is unchanged" holds. Low risk but
needs a concrete signature so the two callers (dispatch vs watch) are clear.

### (f) Second writer to `.claude/settings.json` — mechanism unstated
Step 4 merges the sandbox profile into the instance settings *after*
`applier.Create` already wrote them (grounding: settings written during
materialize via `MergeInstanceOverrides`/`writeRootSettings`). So watch is a
second writer to that file. The design should say whether watch reuses
`MergeInstanceOverrides` or writes directly, and the ordering
(Create → watch-merge → re-verify → launch). The re-verification step (Decision 7)
covers correctness, but the write mechanism itself is unnamed.

## Q3 — Are phases correctly sequenced and independently testable?

Sequencing is sound and each phase names its test surface:

- P1 (poll) and P2 (intersect/dedup/bound) are pure and independently testable
  against the existing fake server / table tests. Good.
- P3 bundles a lot (env-allowlist + synthetic HOME + profile builder + settings
  merge + post-merge re-verify + hardened fetch). These are independently
  unit-testable but it is the heaviest phase; consider splitting the hardened
  fetch (item b/2a) from the env/settings work — they share no code and the
  fetch carries the sharpest risk, so it benefits from isolation.
- P4 depends on P1-P3; P5 depends on the staged-review record produced in P4;
  P6 depends on all. Correct order.

Gap: the handled-set store and staged-review record have no phase of their own —
they appear only inside P4, yet P5 depends on the record schema. Call out the
record/store schema as an explicit early deliverable so P5 isn't blocked on a
format invented mid-P4.

## Q4 — Simpler alternatives / over-engineering / single-PR realism

The design already concedes this is "a sizeable single PR," and most of the
surface is the point rather than gold-plating (the containment IS the feature).
Observations:

- **The load-bearing assumption is external, not built here.** Grounding line 33:
  "NO network/process sandbox exists today. Sandbox profile is net-new." The
  entire security thesis rests on the Claude Code harness actually enforcing
  `sandbox.enabled` + empty `sandbox.network.allowedDomains` at the OS layer.
  niwa has never wired these. The preflight "actively probe that the OS sandbox
  can be created" (Decision 7) is the right instinct, but the design should state
  plainly that egress denial is delegated to the harness's sandbox and name the
  minimum harness version/capability it requires. A "walking skeleton" that
  cannot itself verify egress is denied is really the whole feature riding one
  external capability — worth making that dependency explicit and gating on it.
- **Full-instance provisioning per PR is heavy.** `applier.Create` is the same
  path as `niwa create` (grounding), which clones the workspace's repos; a review
  needs only the one PR's repo, up to 3 instances per run. Reusing the path keeps
  the PR small (the stated goal) but the cost/heaviness deserves a one-line note
  and a "provision only the target repo" follow-up.
- **Possible scope trim:** `post`/`discard` (Decision 6A) could be a follow-up PR;
  the rejected 6B (print the draft location / command) is a smaller first cut that
  still delivers the contained-review skeleton. Not required, but it is the
  cleanest lever if the single PR proves too big.
- **`CurrentLogin` may be avoidable:** GitHub search supports
  `user-review-requested:@me`, which would drop the `GET /user` round-trip and one
  method. Grounding even suggested `@me`. Minor simplification the design passed
  over by resolving an explicit login first.
- **`RequestedAt` / oldest-first adds cost for marginal first-version value** —
  see Q5, it is also not cleanly sourceable.

## Q5 — Conflicts with grounding facts

No hard contradictions. Reuse claims check out:

- `applier.Create` / dispatch provisioning: matches
  `provisionInstanceFunc -> applier.Create` (grounding). ✓
- Settings-merge seam: matches `InstallWorkspaceRootSettings` /
  `buildSettingsDoc` / `MergeInstanceOverrides` / `writeRootSettings`. ✓
  (caveat: watch is a *second* writer — see Q2(f), a gap not a conflict.)
- `--settings` flag collision (Decision 1B) correctly matches the grounded
  dispatch injection at dispatch.go:248-261. ✓ Good use of grounding.
- github client reuse (`NewAPIClient`, `NIWA_GITHUB_API_URL` override) and the
  "no PR-search path exists" gap both match grounding. ✓
- env vector (`cmd.Env = os.Environ()` at dispatch_launcher.go:40) matches the
  "Confirmed vector for R8" grounding; Decision 3 targets exactly it. ✓
- `--detach` semantics match ("watch --once wants --detach"). ✓

One factual concern (not a grounding conflict, a GitHub-API fact):
`PRRef.RequestedAt` powering "oldest-request-first" is not reliably available from
`GET /search/issues`. The search-issues payload exposes the issue/PR
`created_at`/`updated_at`, not the moment the review was *requested* (that lives in
the PR timeline / `requested_reviewers` events). So "oldest-request-first" as
written cannot be satisfied from the single search the design commits to without
an extra per-PR timeline call — which contradicts Decision 4A's "one GitHub
search." Either relabel the ordering to PR `created_at` (honest about the source)
or accept the extra calls. Flag before P1 fixes the `PRRef` shape.

---

## Summary of must-fix items before implementation

1. Resolve the tree-exposure contradiction (checkout-with-filters-disabled vs
   bare object store) and align the prompt wording. [Q2a]
2. Reconcile the draft path with the sandbox write scope so the agent can write
   it. [Q2b]
3. Add the net-new `CreateReview` github method to Components with a signature. [Q2c]
4. Define `handle` (= session shortID?) and the staged-review store
   location/format. [Q2d]
5. Give the env-allowlist launch-seam signature and the default that keeps the
   ordinary dispatch path unchanged. [Q2e]
6. Fix `RequestedAt`/oldest-first: it is not sourceable from the single search;
   relabel or accept extra calls. [Q5]

## Should-address (not blocking)

- State the harness sandbox capability/version the whole feature depends on and
  gate the preflight on it. [Q4]
- Name the settings second-writer mechanism and ordering. [Q2f]
- Consider `@me` to drop `CurrentLogin`; consider deferring post/discard or
  splitting the hardened fetch out of P3 to shrink the PR. [Q4/Q3]
