# PRD Decisions: workspace-config-sources

## Auto Mode

PRD workflow runs in `--auto` mode at user request after the explore-handoff
checkpoint. Default `max-rounds = 2` for the discover loop. Each downstream
decision (round continuation, scope resolutions during draft, jury revisions)
will be made by research-first protocol and recorded here.

## Resume from /explore handoff
- Detected `wip/prd_workspace-config-sources_scope.md` written during
  /shirabe:explore Phase 5; resumed PRD workflow at Phase 2 per the skill's
  resume logic.
- Phase 1 (conversational scoping) is satisfied by the handoff artifact.
- Visibility: Public (niwa CLAUDE.md). Upstream: none (no --upstream flag).

## Phase 4 — jury revision decisions (research-first protocol, --auto)

Jury verdict: 1 PASS (clarity, 9 ambiguities), 2 FAIL (completeness 11 issues,
testability 10 untestable ACs + 10 coverage gaps + 7 fixture gaps). Applied all
revisions in one pass; rationale per item below.

### New requirements added
- **Same-URL upgrade lazy conversion** (completeness #1): when `<workspace>/.niwa/`
  is a legacy working tree but the registry source URL is unchanged, niwa
  lazy-converts to a snapshot on next apply with a one-time notice and no
  `--force`. Rationale: the URL-change `--force` gate protects developer edits
  during a meaningful identity change; for same-URL upgrades the identity is
  preserved, so the conversion is a no-op-from-the-user's-perspective.
- **`niwa config set global` mechanism specified** (completeness #2): both the
  CLI command and direct `~/.config/niwa/config.toml` edit are entry points for
  URL change; both trigger R23/R24 detection. Rationale: Story 2 narrates the
  CLI path but Phase 2 maintainer research's AC-9 covers the manual-edit path
  too; both must be supported and behave consistently.
- **Workspace-name-mismatch validation** (completeness #3): on URL change,
  niwa refuses without `--rename` if the new source declares a different
  `[workspace].name`. Rationale: prevents silently `--force`-ing a workspace
  into the wrong project's config (named in maintainer Phase 2 AC-9.5).
- **Canonical source / mirror reconciliation** (completeness #4): `source_url`
  is canonical; hand-edited mismatched mirror fields trigger reconciliation +
  stderr warning on next save. Rationale: registry file is human-editable;
  the canonical-vs-derived rule must be explicit.
- **GH_TOKEN auth source** (completeness #11): niwa uses `GH_TOKEN` env for
  GitHub fetches with anonymous fallback. Rationale: ratifies the existing
  pattern in `internal/github/client.go` (already uses `GH_TOKEN`); adding
  `gh auth token` integration is a follow-up.
- **Repo-rename behavior** (completeness #10): follow GitHub 301 once and emit
  a one-time DisclosedNotices-style warning. Rationale: the rename is real
  drift the user should see; following silently masks it; failing hard is too
  brittle when the user did nothing wrong.

### New user story added
- **Story 5: CI/automation operator** (completeness #9): narrates the
  fail-on-stale workflow currently deferred to a follow-up `--strict-refresh`
  flag. Establishes the user perspective even though the flag itself is not
  in v1 scope.

### Clarity-ambiguity revisions
- R10: tighten "no extras" — explicitly enumerate prohibited side-effect files
  (no `.git/`, no `pax_global_header`, no tarball wrapper directory, no VCS
  metadata outside `.git/`). Rationale: prevents implementer-A vs implementer-B
  divergence on what counts as "the snapshot."
- R12: spell out the atomic-rename sequence concretely (sibling-write, swap,
  delete-old) and acknowledge platform best-effort fallback. Rationale: the
  word "atomic" plus "rename" alone is POSIX-ambiguous.
- R14: pin to Go's `archive/tar` (no system `tar`). Rationale: matches the
  org-wide self-contained-no-system-deps invariant from CLAUDE.md and removes
  GNU/BSD `tar(1)` flag-divergence risk.
- R26: cite the existing `CheckGitHubPublicRemoteSecrets` function so the
  contract is anchored in code; only the input source changes (marker tuple
  vs `git remote -v`).
- R27: pin deprecation timing to v1 / v1.1 (already in Decisions section).
- R30: drop SHOULD-shaped performance budgets from Requirements; move
  expectations to Known Limitations. Rationale: SHOULD-shaped acceptance is
  neither testable nor binding; the v1 contract is correctness, not perf.
- R33: enumerate cleanup paths explicitly (success, error, context cancel)
  vs the SIGKILL exception.
- R20/R29 lazy migration: anchor on "first command that loads the registry"
  (eager) — re-resolves clarity-vs-functional inconsistency.
- AC for R8: match R6/R7 specificity — diagnostic must name the resolved
  source slug, the resolved subpath (root), the missing setting, and the
  explicit-opt-in escape hatch.

### New ACs for missing coverage
- R3 strict parsing: 4 ACs covering the 4 rejection classes.
- R8 explicit `"."` opt-in: positive AC.
- R34 marker readability: AC opens marker with `cat`/jq, asserts each field
  is present.
- R10/R11 marker contents: AC enumerating required keys.
- R20 lazy upgrade preserves data: regression-guard AC.
- R26 positive guardrail behavior: paired positive case.
- R27 deprecation across processes: cross-invocation AC.
- Known limitation ACs: silent edit discard, slug repo-root workaround,
  submodules/LFS, content_dir = "." opt-in.

### New Test Strategy subsection
Names `tarballFakeServer`, fault-injection seams, state-file factory, and
legacy-working-tree fixture as in-scope test infrastructure deliverables.
Rationale: addresses the testability jury's dominant gap.

### Skipped revisions / accepted limitations
- **Suggestion: cross-link `docs/prds/PRD-config-distribution.md`** — that PRD
  doesn't exist; not adding a dangling reference.
- **Suggestion: snapshot-edit warning on stale mtime detection** — interesting
  follow-up but adds scope without obvious v1 win; documented as Out of Scope
  alongside read-only enforcement.
- **Suggestion: rename "rank N" terminology** — kept as-is; the rank
  terminology is already established by Phase 2 research and the test
  acceptance-coverage doc will use the same vocabulary.

## Phase 4 — re-validation skipped
- Decision: do NOT re-run jury after revisions. Rationale: the revisions
  applied are direct text-level fixes the jury asked for; re-running the
  jury would catch only minor wording polish at the cost of another round
  of agent dispatch. The user-approval checkpoint (Phase 4.6) is the
  appropriate next gate.

## Phase 3 — open-question resolutions (research-first protocol)

Resolved during draft based on Phase 2 agent recommendations. Each cites the
agent that made the call and the strongest reason.

1. **Slug delimiter: `:subpath@ref`** (UX). `:` matches niwa's existing
   slug-shaped CLI surface, dodges the `?` glob trap in zsh, and reads
   correctly for users who already know Renovate's `org/repo:preset`
   convention.

2. **Migration UX direction: Direction A (URL-change detection in
   `niwa apply` + `--force` opt-in, no new command)** (UX). Extends
   niwa's existing destructive-operation pattern (`destroy --force`,
   `reset --force`); zero new commands; the error message is the
   migration guide. Direction B's interactive prompt would break the
   existing tone.

3. **Default-branch ref resolution timing: re-resolve every apply,
   record resolved commit, surface "(default branch)" in `niwa status`**
   (UX). Matches the dominant `git clone` mental model; doesn't force
   refs on users who don't need them; the new tarball fetcher resolves
   `HEAD` server-side naturally so re-resolution is cheap.

4. **Offline behavior when default branch can't be re-resolved:
   continue with cached snapshot, print loud `Reporter.Warn` notice**.
   Matches the existing `SyncRepo` "fetch-failed → continue
   informationally" precedent (`sync.go:108-110`) that codebase analyst
   surfaced; punishing users for ephemeral network loss is the wrong
   pose when the snapshot on disk is still valid.

5. **`content_dir` requirement: REQUIRED when discovery resolves to
   repo root via rank-3 `niwa.toml`; `"."` is a valid explicit opt-in
   for "the whole brain repo is content"** (codebase analyst). Forces
   brain-repo authors to declare which subdir is content; existing
   standalone `dot-niwa` users (rank 2) keep `content_dir` optional;
   the validator hooks cleanly into discovery rank reporting.

6. **`vault_scope = "@source"` shorthand: deferred to v1.1** (UX).
   Real use case but workable manual answer (one explicit string per
   workspace). Risk of binding to a wrong expansion scheme is higher
   than the saving.

7. **Multi-host adapter scope: GitHub-first-class + git-clone fallback
   for everything else; per-host adapters deferred to v1.x** (codebase
   analyst). No host-detection layer to retrofit; fallback covers all
   git-reachable hosts the day v1 ships; per-host adapters become
   pure performance optimizations.

8. **GitHub Enterprise Server treatment: goes through git-clone
   fallback at v1** (codebase analyst). Symmetric with the
   plaintext-secrets guardrail's existing v1-scope-is-strictly-github.com
   stance; consistent v1 surface; GHE adapter is a follow-up.

9. **`--allow-dirty` disposition: silently accept-and-warn for one
   release, hard-remove in v1.1**. Less disruptive than immediate
   removal; users with `--allow-dirty` in scripts get a clear
   deprecation signal before the flag goes away.

10. **Slug repo-root sentinel: bare slug runs discovery; explicit
    `:subpath` is always non-empty**. Reconciles the codebase analyst's
    contradiction: empty-after-colon is rejected (per AC-1.4); the way
    to "explicitly want repo root" is to omit the colon entirely. If
    discovery is ambiguous, the only resolution is removing one of the
    markers in the source repo (the user can't disambiguate from the
    consumer side). Documented as a known limitation.

## Phase 2 — role selection
- Selected 3 roles for the PRD-shaped open questions and coverage gaps:
  - **Codebase analyst**: v1 host coverage commitment, `niwa.toml`
    content_dir requirement, edge-case behavior contracts,
    failure-mode narratives. Codebase-grounded technical questions.
  - **UX perspective**: slug delimiter `:` vs `//`, migration cutover
    ergonomics, default-branch ref resolution timing,
    `vault_scope = "@source"` shorthand, user-story narratives for the
    four scenarios in scope coverage notes. Developer-experience contract
    questions.
  - **Maintainer perspective**: acceptance criteria framing (elevate
    round-1 decisions into testable contracts), documentation outline.
    Long-term maintenance and PRD-completion questions.
- Rationale: feature is technical/infrastructure but its v1 boundary
  hinges on developer-facing trade-offs, so the canonical
  Codebase + Ops + Architecture trio is replaced with
  Codebase + UX + Maintainer to cover the actual decision surface.
- Used `general-purpose` agent type for all 3 (need Write access).
