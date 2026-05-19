# /prd Scope: init-bootstrap-empty-source

## Problem Statement

A user adopting a freshly-created GitHub repository as a niwa-managed
workspace today must run a sequence of independent commands and
hand-author `.niwa/workspace.toml` before niwa can help them. The friction
discourages first-time adoption and means a brand-new niwa user's first
interaction with the tool is "edit a TOML file you've never seen before."
The bootstrap feature collapses the lifecycle (init → create → session
worktree) into one turnkey command that produces a real niwa workspace
inside a real worktree, with the scaffold committed on a branch the user
can inspect and push. This serves as the intro experience for new niwa
adopters and removes the chicken-and-egg between "you need a workspace
config to use niwa" and "you need to use niwa to know what the config
looks like."

## Initial Scope

### In Scope

- A `--bootstrap` flag on `niwa init` that, when paired with `--from
  <slug>`, runs the full lifecycle to land the user in a real worktree at
  `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`.
- Workspace-name derivation from the slug when no positional is given
  (decision N1).
- A scaffolded `.niwa/workspace.toml` with `[[sources]] repos = [<bootstrap-repo>]`
  to keep the first pipeline run scoped to one repo even when the source
  org has many (decision S2).
- The chained command runs `niwa create` and a `niwa session create`
  equivalent (decision T1).
- Adjacent failure modes (auth 401/403, 404 missing/private, ambiguous
  markers) get case-specific Detail+Suggestion messages via a typed
  `*github.StatusError` and an `errors.As` classifier at the init seam.
- Per-step rollback contract so partial-failure leaves the user in a
  recoverable state.
- TTY-gated prompt when `*config.NoMarkerError` fires without `--bootstrap`;
  non-TTY refusal with a remediation hint pointing at the flag.
- Minimal-ideal scaffold (no `default_branch`, derived `[[sources]]`,
  derived `[groups.<vis>]`, `.niwa/claude/.gitkeep`).
- v1 is GitHub-only; the host check refuses non-GitHub sources before
  any git invocation.

### Out of Scope

- A future flag for niwa to open a PR on the user's behalf (deferred to
  a follow-up — the v1 design must leave room without committing).
- Zero-commit (truly empty) remotes that 404 the tarball endpoint; v1
  surfaces a case-specific hint asking the user to push a first commit
  and retry.
- Non-GitHub remotes (`file://`, GitLab, Gitea); raw `git clone` stderr
  remains the user-visible surface for those.
- Workspace-level sentinels (`ErrSourceConfigMalformed`,
  `ErrSourceAuthFailed`, `ErrSourceNotFound`); v1 ships the typed
  `*github.StatusError` only and defers workspace-level sentinels.
- Adopting an already-configured remote — that's today's clone path
  and stays unchanged.

## Confirmed Decisions (from /shirabe:decision)

| # | Question | Decision |
|---|----------|----------|
| Framing | What lifecycle does `--bootstrap` execute? | **T1** — turnkey chain: init → create → session-create. User lands inside `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`. |
| Source-scope | What does the first pipeline run clone? | **S2** — scaffold `[[sources]] repos = ["<bootstrap-repo>"]` allow-list so the pipeline clones exactly the bootstrap repo. User broadens later by editing the workspace.toml. |
| Name derivation | What name when `--from` has no positional? | **N1** — derive from slug's repo basename (`--from owner/foo` → workspace `foo` at `<cwd>/foo/`). |

## Research Leads (Phase 2)

These are the requirements-level gaps that the three settled decisions
didn't cover. Each is a decision to be made via /shirabe:decision in
Phase 2.

1. **Channels behavior.** T1 assumed `--bootstrap` implies channels-on
   for the chained `niwa create`. The PRD must commit to a specific
   shape: does the scaffolded workspace.toml include `[channels.mesh]`
   (so future creates inherit it), does `--bootstrap` synthesize
   channels ephemerally for the bootstrap pipeline run, or does
   `--bootstrap` require `--channels` and refuse otherwise?
2. **Branch name inside the worktree.** Today `niwa session create`
   hardcodes `session/<sid>` (8 hex chars). Acceptable for bootstrap,
   or do we want a `niwa-bootstrap` prefix the user pushes more
   intuitively? Affects what the user sees when they
   `git push -u origin <branch>` and how PR-creation (future flag)
   would name the head.
3. **Per-step rollback contract.** T1 mentioned per-step rollback
   replacing today's aggressive defer. Concretely: if init succeeds,
   create succeeds, session-create fails — does the instance survive
   for retry, or get torn down? Same question for create-fails-after
   -init. Different policies have different implications for
   user-facing recovery messaging and re-run idempotency.
4. **Multi-run idempotency.** What does `niwa init <name> --from
   owner/foo --bootstrap` do if `<cwd>/<name>/` already exists as a
   valid niwa workspace? Today's `InitConflictError` refuse-with-hint,
   or special bootstrap re-run behavior?

## Coverage Notes

- All six coverage dimensions (who, current situation, what's missing,
  why now, scope, success criteria) have surface coverage from the
  prior exploration and the lifecycle code-mapping; the open items
  above are the gaps the PRD draft must close.
- The `--from` no-name behavior change implied by N1 is intentionally
  scoped to bootstrap-only: today's `niwa init --from <slug>` (no
  positional, no `--bootstrap`) materializes in cwd unchanged. The PRD
  needs to make this scoping explicit so reviewers don't read N1 as a
  global behavior change.
- The future PR-creation flag is intentionally out of scope but flagged
  to ensure the v1 design doesn't paint into a corner (e.g., choice of
  branch name in lead 2 should leave a sensible push-and-PR story
  open).

## Status

Phase 1 complete. Proceeding to Phase 2 (compressed discover — the
three settled decisions front-loaded most of the heavy research; only
the four leads above need /shirabe:decision treatment).
