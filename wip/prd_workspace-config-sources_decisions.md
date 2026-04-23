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
