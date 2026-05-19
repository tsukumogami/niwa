# Exploration Findings: init-bootstrap-empty-source

## Core Question

When `niwa init <name> --from <empty-remote>` finds a remote that exists
but has no `.niwa/`, what should niwa do instead of failing? The user's
preferred shape: scaffold a minimal-ideal `.niwa/workspace.toml`, stage
on a branch inside a niwa worktree, print the worktree path, leave the
user to inspect and push.

## Round 1

### Key Insights

- **The plug point is precisely identified.** The failure surfaces at
  `internal/config/discover.go:201` as `*config.NoMarkerError`, propagates
  unwrapped from the GitHub branch of `materializeAndSwap` (and wrapped
  once as "EnsureConfigSnapshot" on the non-GitHub branch), and reaches
  `runInit` at `internal/cli/init.go:265-266` where it gets the generic
  "materializing config repo: %w" wrap. A predicate `config.IsNoMarker(err)`
  already exists. Branch on it before the wrap; disarm the cleanup defer
  (`init.go:221-225`) to keep the workspace directory. (lead-failure-mode)

- **GitHub returns 404 indistinguishably for empty/missing/private.** A
  truly empty repo (zero commits, no HEAD) responds 404 on the tarball
  endpoint — same as a nonexistent repo or a private-without-credentials
  one. Only a repo with at least one commit (e.g. the GitHub auto-init
  README) reaches the `NoMarkerError` path. So the user's `dangazineu/commuter`
  case is two scenarios depending on auto-init: the README case works
  through `NoMarkerError`; the zero-commit case 404s upstream of the
  probe and is currently indistinguishable from typos and auth failures.
  (lead-failure-mode, lead-other-failures)

- **niwa sessions can't be reused as-is for init-time staging.** Sessions
  are a tightly-coupled bundle of branch + worktree + per-worktree daemon
  + lifecycle state, gated on `<instanceRoot>/.niwa/instance.json` and
  `<instanceRoot>/.niwa/roles/<repo>/` — both produced by `niwa apply`.
  The pre-apply use case the user described needs a new lightweight
  primitive (~30 lines: `git worktree add ... -b ...` + commit), not
  the session machinery. Call it `workspace.StageInWorktree`.
  (lead-worktree-integration)

- **Today's scaffold is unbalanced; the proposed minimal-ideal is
  smaller and more useful.** Current `workspace.Scaffold` emits 3 active
  lines (`name`, the redundant `default_branch = "main"`, `content_dir`)
  plus ~60 lines of commented examples. The only public reference
  (`tsukumogami/dot-niwa`) uses just `name` + `content_dir` + one active
  `[[sources]]` + one `[groups.<vis>]`. For the `--from <org/repo>` path
  we can derive `[[sources]].org` from the slug and `[groups.<vis>]`
  from one GitHub API call (`repos/get` → `private` bool). Drop
  `default_branch`. Replace the commented blob with one schema doc link.
  Don't pre-wire vault, plugins, marketplaces — those need user intent.
  (lead-minimal-scaffold)

- **niwa's CLI idiom is `--feature` / `--no-feature` pairs gated by
  `IsStdinTTY()` for one-shot, side-effecting, auditable actions.**
  Four such pairs already exist: `--overlay` / `--no-overlay`,
  `--channels` / `--no-channels`, `--pull` / `--no-pull`,
  `--install-plugins` / `--no-install-plugins`. `--bootstrap` /
  `--no-bootstrap` fits the muscle memory. The non-TTY refusal-with-hint
  pattern from `destroy.go` is the template. (lead-cli-surface)

- **niwa reserves typed prompts for filesystem-destructive operations
  only.** The only prompt-gated action today is `niwa destroy`. Everything
  else uses stderr `note:` (vault bootstrap, name override) or
  uppercase `WARNING:` (after-the-fact, e.g. `--rebind`). For an action
  that creates a new file in a directory niwa just created, `note:` is
  the right surface — prompts would be the first "type y to use the
  flag you just passed" in the niwa surface. (lead-confirmation-ux)

### Tensions

- **T1: Auto-fallback vs. require explicit `--bootstrap` flag.**
  lead-confirmation-ux recommends auto-fallback on `NoMarkerError` with
  stderr `note:`; treat the empty-source case as a friendly success.
  lead-cli-surface and lead-other-failures recommend requiring an
  explicit `--bootstrap` flag (with prompt-in-TTY in lead-cli-surface).
  The 404 ambiguity (private/empty/missing all look the same) plus the
  fact that a typo'd slug could resolve to a *different* empty repo
  argue against silent auto-fallback. Require the flag.

- **T2: "Use niwa's worktree session mechanism" — partial fit.** The
  user asked for the worktree handoff. Research showed the existing
  session API gates on apply-time state and can't be invoked pre-apply.
  The pragmatic answer is a new lightweight helper that does the
  branch + worktree + commit dance without the daemon/lifecycle. The
  user's intent (land changes on a branch, print path, let user decide)
  is preserved; the implementation diverges.

- **T3: Commit on the branch vs. leave working-tree dirty.** Two valid
  UX models: (a) niwa creates the branch, scaffolds the file, commits
  with a fixed message — user can inspect, amend, push; (b) niwa
  creates the branch, scaffolds the file, leaves it staged or dirty —
  user makes the first commit themselves. (a) is one-and-done; (b)
  invites the user to author the commit message. The user said "leave
  it to the user to decide what to do next" — slight lean toward (b),
  but either works.

### Gaps

- **G1: Where does the worktree live on disk?** Current sessions use
  `<instanceRoot>/.niwa/worktrees/<repo>-<sid>/`, but there's no
  instance yet. Plausible homes: `<workspaceRoot>` itself (the
  freshly-cloned tree), `<workspaceRoot>/../scaffold-<hex>/`,
  `~/.cache/niwa/scaffold/<sid>/`. Each has trade-offs.

- **G2: Registry write timing.** Today `init` writes the registry entry
  after post-flight succeeds. In the bootstrap path, post-flight runs
  against the scaffolded file (which IS valid). Should the registry
  entry exist before the user pushes? If yes: workspace registered, but
  remote has no config. If no: subsequent `niwa init <name>` (no
  `--from`) needs the registry to know about this workspace; deferred
  registration breaks the wrapper-driven workflow.

- **G3: Zero-commit vs. auto-init repos.** The user said "I already
  created the repo at dangazineu/commuter." If GitHub's auto-init
  README toggle was on, the repo has a commit and reaches
  `NoMarkerError`. If off, the repo has zero commits and 404s the
  tarball endpoint. v1 likely handles the auto-init case; the
  zero-commit case requires distinguishing 404-empty from 404-missing
  via an extra API call (e.g. `GET /repos/{owner}/{repo}` to confirm
  existence, then inspect `default_branch` / `size`).

- **G4: Should the scaffold push to the remote when `--bootstrap` is
  passed in non-interactive mode?** lead-cli-surface's sketch leaves
  push to the user. lead-other-failures notes the `--bootstrap` flag
  itself is the explicit signal. Auto-push in non-TTY mode might be
  fine for fully-automated provisioning, but it conflicts with the
  worktree-handoff model.

### Decisions

(captured in `wip/explore_init-bootstrap-empty-source_decisions.md`)

### User Focus

User chose the `--bootstrap` flag model (T1 resolution): no silent
auto-fallback. TTY without the flag prompts; non-TTY without the flag
fails fast with a remediation hint. Matches niwa's existing
`--feature` / `--no-feature` idiom and sidesteps the 404 ambiguity.

## Accumulated Understanding

The empty-source bootstrap feature is shaped by five concrete findings:

1. **Plug point**: `internal/cli/init.go:265`, branching on
   `config.IsNoMarker(err)` before the existing wrap, with the cleanup
   defer disarmed when the bootstrap path takes over.

2. **CLI surface**: `--bootstrap` / `--no-bootstrap` flag pair, gated by
   `IsStdinTTY()` for the interactive prompt. Non-TTY without the flag
   fails fast with a remediation hint (the `destroy.go` template).
   Auto-fallback is rejected because of the 404 ambiguity and the
   "typo'd slug → different empty repo" risk.

3. **Minimal scaffold**: drops `default_branch`; emits active
   `[workspace]` (name, content_dir), active `[[sources]] org = "<derived
   from --from slug>"`, active `[groups.<vis>] visibility = "<from
   GitHub repos/get>"`, commented `[claude.content.workspace] source = "workspace.md"`,
   and one schema doc link. Vault/plugins/etc. omitted; the dot-niwa
   pattern of "advertise needs in base, supply providers in overlay"
   only applies once the user has needs to advertise.

4. **Worktree primitive**: a new lightweight helper
   (`workspace.StageInWorktree` or similar) that does `git worktree add
   ... -b ...` and prints the path, separate from the existing session
   machinery. The session API stays reserved for apply-time mesh
   delegation.

5. **Adjacent failure modes**: malformed config, missing config inside
   `.niwa/`, auth failures, 404 missing-repo, and rank-2 layouts all
   fail-loud with case-specific hints reusing the `InitConflictError`
   pattern. Rank-2 (E/G) already handled correctly. Requires three new
   sentinels in `workspace/preflight.go` plus a typed-error refactor in
   `internal/github/fetch.go` to replace string-based status errors.

The primary feature is well-scoped and ready for design. Open questions
G1-G4 are design decisions, not exploration gaps.

## Decision: Crystallize

User chose "ready to decide" at the end of Round 1. Proceeding to
Phase 4 (Crystallize).
