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

### Pre-settled by exploration

These were settled in `/shirabe:explore` and are treated as constraints,
not reopened:

- **Scope confined to the empty-source case** (rank-1 path, remote has
  at least one commit, `.niwa/workspace.toml` absent). Adjacent failure
  modes get fail-loud hints, not auto-scaffold. Rank-2 layouts already
  work and stay unchanged.
- **The worktree-handoff metaphor is the only confirmation gate.** The
  user inspects and pushes themselves. No automatic push from niwa.
- **niwa proposes the minimal-ideal scaffold non-interactively.** No
  prompts for vault/plugins/marketplaces selection; those are user
  follow-ups after bootstrap completes.
- **Trigger requires the explicit `--bootstrap` flag.** No silent
  auto-fallback on `NoMarkerError`. TTY without the flag prompts.
  Non-TTY without the flag fails fast with a remediation hint pointing
  at `--bootstrap`.

## Considered Options

Four interrelated decisions were evaluated. Full reports live in
`wip/design_init-bootstrap-empty-source_decision_*.md`; this section
summarizes each so a future reader understands the alternatives that
were weighed.

### Decision 1: Bootstrap end-to-end UX model

**Context.** When `--bootstrap` triggers the scaffold path, three
sub-choices must hang together as one coherent story: where the
bootstrap branch's worktree lives on disk; when the global-config
registry entry is written; whether niwa pre-commits the scaffold or
leaves it staged/unstaged for the user. The choices were evaluated
together because cross-validation pressure (post-flight verification,
apply discovery via `config.Discover`, registry/state shape) ties them.

**Key assumptions.**

- The bootstrap path performs `git clone --depth 1 <cloneURL>` into
  `<workspaceRoot>/` directly, rather than reusing the success path's
  tarball-of-subpath fetch. Bootstrap needs a working tree to commit
  into.
- `niwa apply` from `<workspaceRoot>` should work as soon as bootstrap
  finishes, allowing local iteration before publish.
- The user's typical first action is inspect-and-push, not substantial
  scaffold rewrite. Pre-committing wastes at most one
  `git commit --amend` keystroke in the substantial-edit case.
- The user wants `niwa list` and `niwa go <name>` to find the
  workspace immediately, not after a deferred publish step.

**Chosen: In-place / immediate registry / pre-commit (W1 + R1 + C1).**

1. **In-place worktree (W1).** The cloned tree IS the workspace root.
   After `git clone --depth 1`, niwa runs
   `git -C <workspaceRoot> checkout -b niwa-bootstrap` to create and
   switch to the bootstrap branch in the main checkout. No separate
   `git worktree add` is invoked.
2. **Immediate registry write (R1).** The existing
   `globalCfg.SetRegistryEntry` at `init.go:328` fires exactly as it
   does for the clone path. `SourceURL` records the `--from` slug. The
   workspace is discoverable to `niwa list`, `niwa go`, and re-invoked
   `niwa init <name>` from this point forward.
3. **Pre-commit the scaffold (C1).** niwa runs `git add` + `git commit -m "Initial niwa workspace config"`
   on the bootstrap branch. The user can `git show HEAD`, amend, and
   `git push -u origin niwa-bootstrap` directly without an interstitial
   commit step.

**Alternatives Considered.**

- **Sub-worktree + marked-pending registry + pre-commit (W2 + R3 + C1).**
  Mirrors `niwa session create`'s worktree placement at
  `<instance>/.niwa/worktrees/<repo>-<id>/`. Main checkout stays on the
  remote's default branch; bootstrap activity lives in a sibling
  worktree; an `InstanceState.BootstrapPending` field gates `niwa apply`
  until the bootstrap is pushed. *Rejected because* post-flight
  verification at `init.go:288` and apply discovery via
  `config.Discover` both expect `<workspaceRoot>/.niwa/workspace.toml`
  to exist on disk in the main checkout — W2 forces reworks in both,
  plus a new InstanceState schema field and apply-gate logic, for no
  offsetting user value. Two on-disk locations also create a "where do
  I run apply from?" footgun.
- **In-place + immediate + stage-only (W1 + R1 + C2).** Same as the
  chosen model but niwa leaves the commit to the user. *Rejected
  narrowly because* `git status` showing a staged file immediately
  after `niwa init --bootstrap` is unusual and may be read as "init
  didn't finish," and the user pays one extra commit step before push.
  The cost delta is small; this was the closest runner-up.
- **In-place + immediate + no stage (W1 + R1 + C3).** Untracked file in
  a fresh init contradicts scaffold-tool convention and is vulnerable
  to `git clean -fd`. *Rejected.*
- **In-place + deferred registry (W1 + R2).** No registry entry until
  the user pushes. *Rejected* — creates a discoverability black hole
  between init and push (`niwa list`/`niwa go` don't see the workspace)
  with no offsetting benefit.
- **Cache-dir worktree (W3, `~/.cache/niwa/bootstrap/<sid>/`).**
  *Rejected* — caches are semantically ephemeral; landing the user in
  one contradicts the "this is your workspace" mental model.
- **Sibling-dir worktree (W4, `<cwd>/<name>-bootstrap/`).** *Rejected*
  — two top-level directories per workspace pollute cwd ancestry and
  inherit W2's post-flight + apply-discovery problems.

> **Note on divergence from the original framing.** The user-stated
> preference in exploration was "use the git worktree setup in niwa to
> land the changes in a branch." Decision 1 chose no separate worktree
> at all (in-place). The trade-off favoring W1 is structural: every
> separated-worktree variant forces reworks of post-flight, apply
> discovery, and registry, for limited offsetting benefit. If the
> user's intent was specifically the worktree-separation pattern
> (e.g., to preserve the main branch state until merge), the W2+R3+C1
> alternative above documents the rejected option and the costs of
> bringing it back.

### Decision 2: v1 handling of zero-commit remotes (404 path)

**Context.** A GitHub repo with zero commits returns HTTP 404 from
`/repos/{owner}/{repo}/tarball/HEAD` upstream of the probe, so it never
reaches `NoMarkerError`. At the HTTP layer, 404 is indistinguishable
from "wrong slug" or "private repo without credentials." v1 must
decide whether to disambiguate (extra `repos/get` API call) or punt
with a clearer error.

**Key assumptions.**

- GitHub web-UI repo creation defaults to "Initialize with README" on,
  so most user-created repos reach the `NoMarkerError` path and the
  zero-commit subset is a minority case.
- The typed-error refactor in `internal/github/fetch.go` lands in v1
  anyway (required by Decision 3), so adding case-specific copy is
  near-zero marginal cost.
- The user-side workaround (push an empty README) is a 30-second
  one-liner; the user pays it once.

**Chosen: C — hint-only middle ground.**

On 404 from `FetchTarball`, niwa stays in fail-loud mode but emits a
case-specific message that explicitly names the zero-commit scenario:
"If the repo is brand new and has no commits yet, push at least one
commit (an empty README is enough) and retry with --bootstrap."
Delivered via the typed `*github.StatusError` classifier from Decision 3.

**Alternatives Considered.**

- **A — No special-case detection.** *Rejected* — ships the worst UX
  for the exact "I just created the repo" flow the user flagged, and
  saves only one message string over C once Decision 3's refactor
  lands.
- **B — Extra `repos/get` API call to detect zero-commit and scaffold
  against an empty tree.** *Rejected for v1* — disambiguation is still
  incomplete against private repos (still 404 without a token);
  requires a new no-clone bootstrap subpath that doesn't share code
  with the `NoMarkerError` worktree-handoff flow; doesn't generalize
  to non-GitHub transports. Revisit in v2 if real users report
  hitting zero-commit-repo friction.

### Decision 3: Adjacent failure-mode classification scope in v1

**Context.** Today every materialize failure surfaces as the generic
`"materializing config repo: <underlying>"`. The exploration proposed
typed sentinels in `workspace/preflight.go` plus typed status errors
in `internal/github/fetch.go`. Decision 2 already committed to part of
this refactor; Decision 3 decides how much further to go.

**Key assumptions.**

- Decision 2's commitment to `*github.StatusError` plus the
  `errors.As`-based 404/auth classifier is binding.
- Production callers of `internal/github/fetch.go` consume the status
  code directly, not the error text. Only four test fakes
  string-match.
- Existing `config.NoMarkerError` / `config.AmbiguousMarkersError` and
  their predicates remain the dispatch shape for marker failures.
- The post-flight TOML parse error already cites line + column, so
  the marginal value of a workspace-level `ErrSourceConfigMalformed`
  sentinel is low for v1.
- Non-GitHub transport stays raw `git clone` stderr in v1.

**Chosen: B-narrow — typed errors for the cases Decision 2 needs, plus
case-specific 401/403 and 404 hints, no workspace-level sentinels for
malformed-config, no non-GitHub classification.**

Ships in v1:

1. Typed `*github.StatusError{StatusCode, Message, URL}` in
   `internal/github/fetch.go`. The four construction sites return the
   typed value; `Error()` preserves today's text so production string
   display is unchanged.
2. Classifier at the `runInit` seam (`internal/cli/init.go`), replacing
   the bare `"materializing config repo: %w"` wrap. Ordered
   most-specific first: `*config.AmbiguousMarkersError` →
   `*config.NoMarkerError` (→ bootstrap if `--bootstrap`, else
   error) → `*github.StatusError{401|403}` → `*github.StatusError{404}`
   → today's generic wrap as fall-through.
3. Reuse of existing `InitConflictError{Detail, Suggestion}` display
   machinery.

**Deferred to follow-up** (not in v1):

- `ErrSourceConfigMalformed` workspace-level sentinel for post-flight
  TOML parse errors.
- `ErrSourceAuthFailed` / `ErrSourceNotFound` workspace-level
  sentinels — the typed GitHub error plus per-class message is
  sufficient.
- Non-GitHub transport classification.

**Alternatives Considered.**

- **A — Bootstrap path only, no adjacent classification.** *Rejected*
  — conflicts with Decision 2's commitment to the typed
  `*github.StatusError` and `errors.As` classifier.
- **B-wide — full exploration proposal.** *Rejected as over-scoped*
  for v1. Adds workspace-level sentinels no production caller
  consumes, plus malformed-config and non-GitHub classification the
  user didn't scope.
- **C — String-match dispatch.** *Rejected* — incompatible with
  Decision 2's `errors.As` shape, brittle against rewording of error
  text, and stylistically inconsistent with the existing typed
  predicates (`config.IsNoMarker`, `config.IsAmbiguousMarkers`).

### Decision 4: Bootstrap scaffold shape and derivation

**Context.** The exploration proposed a minimal-ideal scaffold derived
from `--from` inputs but left several details open: active vs.
commented sections, GitHub-API fallback, `.gitkeep` handling, audit
comment, scaffold function signature.

**Key assumptions.**

- The bootstrap flow stages and commits the scaffolded file (per
  Decision 1).
- `--from`-gated entry means the source org is always parseable.
- The schema doc URL stays at
  `docs/guides/workspace-config-sources.md`; the scaffold's link is
  the only place to update if it moves.
- Visibility lookup failure (network, auth, 404) defaults to
  `[groups.public]` and emits a stderr `note:` explaining the
  fallback.

**Chosen: S-D — exploration proposal plus `.niwa/claude/.gitkeep`.**

The scaffold emits (for `--from <org>/<repo>` where visibility
resolves to public):

```toml
[workspace]
name = "<name>"
content_dir = "claude"

[[sources]]
org = "<org-from-slug>"

[groups.public]
visibility = "public"

# CLAUDE.md content hierarchy: drop a workspace.md in .niwa/claude/ to populate.
# [claude.content.workspace]
# source = "workspace.md"

# See https://github.com/tsukumogami/niwa/blob/main/docs/guides/workspace-config-sources.md
# for the full schema (claude.*, env.*, vault.*, files, channels, instance).
```

Plus on disk: empty `.niwa/claude/.gitkeep` so the directory pushes
cleanly when the user later uncomments `[claude.content.workspace]`.
No stub `workspace.md`. No audit-trail comment. No `default_branch =
"main"`. No `# version = "0.1.0"` comment.

Implementation: new sibling function
`workspace.ScaffoldFromSource(dir, opts ScaffoldOptions)` in
`internal/workspace/scaffold.go`. Existing `workspace.Scaffold(dir,
name)` stays unchanged for `modeScaffold` / `modeNamed` callers. A new
`(*github.APIClient).GetRepo(ctx, owner, repo)` method in
`internal/github/client.go` returns the existing `Repo` struct
(reusing the `private` bool → `Visibility` normalization that
`ListRepos` already does).

**Alternatives Considered.**

- **S-A — exploration proposal, no `.gitkeep`.** *Rejected* — empty
  `.niwa/claude/` silently drops on `git add`, breaking the documented
  content-dir convention the moment the user uncomments
  `[claude.content.workspace]`.
- **S-B — conservative, commented placeholders only.** *Rejected* —
  forces every bootstrap user to edit before `niwa apply` works,
  defeating the "minimal ideal" goal.
- **S-C — S-A plus stub `workspace.md` plus audit comment.**
  *Rejected* — stub file adds empty-file friction without value; a
  durable bootstrap-by/date comment encodes either redundant
  information (slug duplicates parent repo URL) or transient noise
  (timestamp loses meaning the moment the file outlives the bootstrap).

## Decision Outcome

The four decisions compose into one coherent flow:

1. User runs `niwa init commuter --from dangazineu/commuter --bootstrap`.
2. niwa resolves the clone URL and shallow-clones into `<cwd>/commuter/`
   directly (not via tarball — bootstrap needs a working tree).
3. Probe via `config.Discover` (in-memory or via the existing
   `MaterializeFromSource` plumbing) confirms no `.niwa/workspace.toml`
   exists in the cloned tree — `*config.NoMarkerError` returned.
4. The typed-error classifier at `init.go:265` catches `NoMarkerError`
   and, with `--bootstrap` set, routes into the bootstrap path.
5. `workspace.ScaffoldFromSource` writes `.niwa/workspace.toml` (Decision
   4's shape) and `.niwa/claude/.gitkeep` into the cloned tree.
6. niwa creates branch `niwa-bootstrap`, stages, and commits with
   message `"Initial niwa workspace config"` (Decision 1's pre-commit).
7. Post-flight `config.Load` succeeds against the just-scaffolded file;
   `globalCfg.SetRegistryEntry` writes the registry entry; instance
   state is saved (Decision 1's immediate registry).
8. niwa prints a prominent stderr block with the worktree path, branch
   name, and next steps.
9. The shell wrapper drops the user inside `<cwd>/commuter/` on branch
   `niwa-bootstrap` with a clean working tree. The user can run
   `niwa apply` locally before pushing, or `git push -u origin niwa-bootstrap`
   and merge first — both work.

Adjacent failures (Decisions 2 and 3) route through the same classifier
seam: `AmbiguousMarkersError` keeps today's text; `NoMarkerError`
without `--bootstrap` keeps today's text plus a `--bootstrap` hint;
GitHub 401/403 surfaces the `GH_TOKEN` scope guidance as
Detail+Suggestion; GitHub 404 surfaces the zero-commit guidance.
Everything else falls through to today's generic wrap.

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
