---
status: Proposed
problem: |
  `niwa init <name> --from <org/repo>` fails when the remote exists but
  has no `.niwa/workspace.toml` — the materialize step returns
  `*config.NoMarkerError` and `runInit` wraps it as "materializing config
  repo: ...". This creates a chicken-and-egg friction when a user wants
  to adopt a freshly-created GitHub repo as a niwa-managed workspace:
  they must clone the repo outside the workspace, hand-author
  `.niwa/workspace.toml`, push, and only then run `niwa init` for real.
  The design must specify how a bootstrap fallback plugs into the
  existing init flow without regressing the failure paths for malformed
  configs, auth errors, 404s, or rank-2 layouts.
---

# DESIGN: init bootstrap from empty source

## Status

Proposed

## Context and Problem Statement

A user creating a new project on GitHub today cannot bootstrap a
niwa-managed workspace in a single step. They run
`niwa init commuter --from dangazineu/commuter` against an empty (or
auto-initialized but `.niwa/`-less) remote and see:

```
Error: materializing config repo: no niwa config found: probed
.niwa/workspace.toml and workspace.toml at source root. ...
```

The workaround is to clone the repo outside any workspace, author
`.niwa/workspace.toml` by hand, push it back, then re-run init. This is
manual, error-prone, and asks the user to know the workspace.toml
schema by heart before niwa can help them.

Exploration confirmed the materialize failure surfaces at
`internal/config/discover.go:201` as `*config.NoMarkerError`, reaches
`runInit` at `internal/cli/init.go:265-266`, and gets generically
wrapped. By the time the error propagates back, every disk artifact
(staging dir, temp clone, workspace root) has been cleaned up via
defers — a fallback path must interpose before those defers fire, or
re-clone the source. The natural plug point is `init.go:265`, branching
on `config.IsNoMarker(err)` (predicate already exists) and disarming
the workspace-dir cleanup defer at `init.go:221-225`.

The user's preferred UX is to scaffold a minimal-ideal
`.niwa/workspace.toml`, land it on a feature branch inside a git
worktree, print the worktree path, and exit successfully — leaving
inspection and `git push` to the user. Exploration showed niwa's
existing worktree session mechanism cannot be reused as-is for
init-time staging: sessions require `<instanceRoot>/.niwa/instance.json`
and `<instanceRoot>/.niwa/roles/<repo>/`, both produced by `niwa apply`.
A new lightweight primitive (call it `workspace.StageInWorktree`) that
does the branch + worktree + commit dance without the daemon/lifecycle
is required.

Adjacent failure modes (malformed `workspace.toml`, `.niwa/` with no
`workspace.toml`, auth failures, 404 missing repo, rank-2 layouts) must
not regress and should gain case-specific remediation hints. Rank-2 is
already handled correctly. GitHub returns HTTP 404 indistinguishably
for empty-but-no-commits, missing, and private-without-credentials —
the bootstrap fallback must therefore be gated on explicit user intent,
not auto-triggered on any 404.

## Decision Drivers

- **Avoid silent classification**: GitHub 404 ambiguity (empty / missing
  / private-without-credentials all look the same) plus the risk of a
  typo'd slug resolving to a different empty repo argue against silent
  auto-fallback. The trigger must be explicit user intent.
- **Respect niwa's CLI idioms**: niwa has four `--feature` /
  `--no-feature` flag pairs already (`--overlay`, `--channels`,
  `--pull`, `--install-plugins`); the bootstrap trigger should match
  that shape. Prompts are reserved for filesystem-destructive
  operations (`destroy`); non-TTY refusal-with-hint is the
  destroy.go template.
- **Reuse the InitConflictError pattern**: existing error display in
  `init.go:174,183,201` uses `Detail` + `Suggestion`. New sentinels for
  adjacent failure modes should drop into this pattern.
- **Keep the worktree primitive scoped**: the existing session API is
  about mesh delegation post-apply. The bootstrap helper should not
  drag in the daemon/lifecycle/role-directory machinery.
- **Minimal scaffold over bulky scaffold**: the dot-niwa reference
  workspace.toml is 4 active sections (workspace, sources, groups,
  claude). Today's scaffold emits 3 active lines plus ~60 lines of
  commented examples. The bootstrap scaffold should land closer to
  dot-niwa's shape, with `--from` inputs supplying derived values
  (org from slug, visibility from one GitHub API call).
- **Don't pre-wire vault, plugins, marketplaces**: dot-niwa's pattern
  is "advertise needs in base, supply providers in overlay." A fresh
  scaffold has nothing to advertise yet. Pre-wiring invites a broken
  first `niwa apply`.
- **Preserve the existing init failure-cleanup contract**: today
  failures roll back the workspace dir via deferred `os.RemoveAll`. The
  bootstrap path must explicitly disarm that defer when it takes over;
  failures inside the bootstrap path should still leave the user in a
  reasonable state.
- **Auditable side effects**: bootstrap creates a branch in the cloned
  repo. The success message should be prominent enough that an
  automated agent's invocation leaves an audit trail, matching the
  `--rebind` precedent (uppercase WARNING on stderr).

## Decisions Already Made

These are settled by exploration and should be treated as constraints
by the design, not reopened.

- **Scope confined to the empty-source case** (rank-1 path, repo has at
  least one commit, `.niwa/` absent or empty). Adjacent failure modes
  get fail-loud hints, not auto-scaffold. Rank-2 layouts already work
  and are out of scope.
- **The worktree handoff is the only confirmation gate**. The user
  inspects and pushes themselves. No automatic push from niwa.
- **niwa proposes the minimal-ideal scaffold non-interactively**. No
  prompts for vault/plugins/marketplaces selection; those are user
  follow-ups in the worktree.
- **Trigger model: require explicit `--bootstrap` flag**. No silent
  auto-fallback on `NoMarkerError`. In a TTY without the flag, niwa
  prompts; in non-TTY without the flag, niwa fails fast with a
  remediation hint pointing at `--bootstrap`. This matches niwa's
  existing four `--feature` / `--no-feature` pairs and the
  "explicit user intent → loud, convention → silent" gradient.

## Open Design Questions (for the design phase)

Carried forward from the exploration as items the design must resolve:

- **G1: Worktree location on disk**. Plausible homes: `<workspaceRoot>`
  itself (the freshly-cloned tree, on a feature branch); a sibling
  directory like `<workspaceRoot>/../scaffold-<hex>/`; a niwa cache
  location like `~/.cache/niwa/scaffold/<sid>/`. Each affects cleanup,
  the shell-wrapper `cd` behavior, and what a `git status` in the
  worktree shows.
- **G2: Registry-write timing**. Today the registry entry is written
  after post-flight `config.Load` succeeds. In the bootstrap path,
  post-flight runs against the scaffolded file (which IS valid). Should
  the registry entry be written before push? After push? Deferred
  until `niwa apply`?
- **G3: Zero-commit (truly empty) repos vs. auto-init repos**. The
  bootstrap path only fires when the clone succeeds and the probe
  returns `NoMarkerError`. A zero-commit repo 404s the tarball
  endpoint upstream of the probe. Does v1 detect this case (extra
  GitHub API call to confirm existence and distinguish empty from
  missing), or does it punt with a 404 message asking the user to push
  a first commit?
- **G4: Commit on the bootstrap branch vs. leave working-tree dirty**.
  Either is a viable UX. The user said "leave it to the user to
  decide what to do next" — slight lean toward leaving the scaffold
  staged-but-uncommitted so the user authors the first commit message.
- **G5: Typed-error refactor scope**. Adjacent failure modes
  (malformed config, auth, 404 missing) need case-specific hints that
  require typed sentinels in `workspace/preflight.go` and a typed
  status error in `internal/github/fetch.go`. Is this refactor part of
  v1 (cleaner taxonomy from the start) or a follow-up (smaller v1
  surface)?
- **G6: Interaction with `--overlay` / `--no-overlay` and
  `--install-plugins`**. When bootstrap fires, the overlay-discovery
  side path (`init.go:602-630`) and plugin auto-install (currently
  gated on rank-2) need explicit behavior. Recommendation from
  exploration: bootstrap implies a rank-1 layout, so plugin
  auto-install never fires; overlay discovery silently no-ops (the
  overlay repo also doesn't exist yet).

## References

- Exploration scope: `wip/explore_init-bootstrap-empty-source_scope.md`
- Exploration findings: `wip/explore_init-bootstrap-empty-source_findings.md`
- Exploration decisions: `wip/explore_init-bootstrap-empty-source_decisions.md`
- Lead research files:
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-failure-mode.md`
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-minimal-scaffold.md`
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-worktree-integration.md`
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-cli-surface.md`
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-other-failures.md`
  - `wip/research/explore_init-bootstrap-empty-source_r1_lead-confirmation-ux.md`
