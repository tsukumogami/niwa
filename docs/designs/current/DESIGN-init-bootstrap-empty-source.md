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

### Decision 5: Implementation choices (implicit)

The following are smaller choices baked into Solution Architecture and
Implementation Approach below. Each had at least one viable alternative;
documenting them here so future readers see they were made deliberately.

- **Branch name fixed as `niwa-bootstrap`.** *Alternative:* configurable
  via `--bootstrap-branch <name>` flag. *Chosen because* zero-config is
  the v1 priority; a flag is an easy follow-up if user feedback wants it.
- **Shallow clone via `git init` + `git fetch --depth 1`, not `git clone --depth 1`.**
  *Alternative:* clone into a temp dir then move. *Chosen because* the
  workspace root already exists (created by `os.Mkdir` at `init.go:217`)
  and `git clone` refuses non-empty targets; `git init` + `fetch` works
  in-place without a cross-filesystem move that might fail.
- **`.niwa/claude/.gitkeep` always written by `ScaffoldFromSource`.**
  *Alternative:* opt-in via the `IncludeGitkeep` field in
  `ScaffoldOptions`. *Chosen* the field exists so unit tests can suppress
  the file, but production bootstrap always sets it true.
- **Pre-commit message fixed as `"Initial niwa workspace config"`.**
  *Alternative:* configurable via flag. *Chosen because* the user can
  always `git commit --amend` if they want different wording; a
  flag adds surface area for a once-in-project-lifetime concern.
- **Bootstrap path is GitHub-only in v1.** *Alternative:* support
  `git@host:org/repo` and `file://` source URLs from day one.
  *Chosen because* the typed-error refactor (Decision 3) is
  GitHub-specific; non-GitHub transports stay on the existing raw
  `git clone` stderr path. Bootstrap dispatch checks the source host
  and refuses non-GitHub with a clear error pointing at "v1 supports
  GitHub sources only."
- **Audit-trail success message goes to stderr in WARNING style.**
  *Alternative:* a quiet stdout `note:` like the vault-bootstrap
  pointer. *Chosen because* bootstrap mutates local git state (creates
  a branch, writes a commit), which is more side-effecting than a
  pure config nudge — matches the `--rebind` precedent's prominence.

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

## Solution Architecture

### Overview

The bootstrap path is a sibling of today's `modeClone` flow inside
`runInit`. It activates when the user passes `--bootstrap` (or accepts
the TTY prompt) and `MaterializeFromSource` returns
`*config.NoMarkerError`. Instead of failing, niwa scaffolds a minimal
config, commits it on a feature branch in the workspace root, and
hands the workspace off to the user via the existing landing-path
shell-wrapper mechanism. The cloned tree IS the workspace root; no
separate worktree exists.

### Components

```
internal/cli/
  init.go                  -- new --bootstrap / --no-bootstrap flags;
                              classifier seam at line 265;
                              bootstrap dispatch into workspace.RunBootstrap.
internal/github/
  fetch.go                 -- typed *StatusError replaces string status errors.
  client.go                -- new (*APIClient).GetRepo(ctx, owner, repo).
internal/workspace/
  scaffold.go              -- new ScaffoldOptions struct;
                              new ScaffoldFromSource(dir, opts) sibling of Scaffold;
                              shared template helpers for the schema-link footer
                              and commented [claude.content.workspace] hint.
  bootstrap.go             -- new file. Orchestrates clone + branch + scaffold
                              + commit. Exposes workspace.RunBootstrap.
internal/workspace/
  preflight.go             -- unchanged in v1 (workspace-level sentinels
                              deferred to follow-up per Decision 3).
```

### Key Interfaces

**`internal/github`**

```go
// StatusError carries the HTTP status code from a GitHub API call so
// callers can classify failures via errors.As without parsing the
// message text. The Error() string preserves today's wording for
// callers that print the wrapped error verbatim.
type StatusError struct {
    StatusCode int
    Message    string  // human-readable summary
    URL        string  // request URL (optional, for diagnostics)
}

func (e *StatusError) Error() string { ... }

// GetRepo fetches single-repo metadata. Bootstrap uses it to read the
// 'private' bool for visibility classification. Returns the existing
// *Repo struct so visibility normalization reuses the ListRepos path.
func (c *APIClient) GetRepo(ctx context.Context, owner, repo string) (*Repo, error)
```

**`internal/workspace`**

```go
type ScaffoldOptions struct {
    Name           string  // workspace name; positional arg or derived from repo slug
    Org            string  // source org from --from slug; required
    Visibility     string  // "public" | "private" | "" (lookup failed → empty)
    IncludeGitkeep bool    // production always true; unit tests may suppress
}

// ScaffoldFromSource writes .niwa/workspace.toml + .niwa/claude/ (with
// optional .gitkeep) into dir. Sibling of Scaffold(dir, name); the
// existing callers stay on Scaffold.
func ScaffoldFromSource(dir string, opts ScaffoldOptions) error

// RunBootstrap orchestrates the bootstrap path: shallow-fetch the
// source into workspaceRoot, create the bootstrap branch, write the
// scaffold, stage, commit. Idempotent on partial failure (cleans up
// .git/ if init fails; leaves clean state if commit succeeded).
//
// cloneURL is the resolved HTTPS/SSH URL; sourceSlug is the original
// --from argument (used for visibility lookup and registry source URL).
func RunBootstrap(ctx context.Context, workspaceRoot, cloneURL, sourceSlug string,
                  opts BootstrapOptions, fetcher github.FetchClient,
                  reporter *Reporter) error
```

**`internal/cli/init.go` classifier seam (replacing the bare wrap at
line 266):**

```go
materializeErr := workspace.MaterializeFromSource(...)
if materializeErr != nil {
    var ambErr *config.AmbiguousMarkersError
    var noMarkerErr *config.NoMarkerError
    var statusErr *github.StatusError
    switch {
    case errors.As(materializeErr, &ambErr):
        // today's text, formatted as InitConflictError
    case errors.As(materializeErr, &noMarkerErr):
        if initBootstrap {
            // Disarm the workspace-dir cleanup defer and dispatch:
            workspaceCreated = false
            return workspace.RunBootstrap(ctx, workspaceRoot, cloneURL, source, ...)
        }
        // emit existing text + "--bootstrap" hint
    case errors.As(materializeErr, &statusErr) && (statusErr.StatusCode == 401 || statusErr.StatusCode == 403):
        // case-C message: GH_TOKEN scope guidance
    case errors.As(materializeErr, &statusErr) && statusErr.StatusCode == 404:
        // case-D message: "verify slug; private needs GH_TOKEN; zero-commit push README"
    default:
        return fmt.Errorf("materializing config repo: %w", materializeErr)
    }
}
```

### Data Flow

```
niwa init commuter --from dangazineu/commuter --bootstrap
  │
  ▼
runInit
  ├─ resolveInitMode → modeClone
  ├─ os.Mkdir(<cwd>/commuter)                    [today: init.go:217]
  ├─ workspace.MaterializeFromSource              [today: init.go:264]
  │     └─ returns *config.NoMarkerError           [config/discover.go:201]
  │
  ▼ classifier matches NoMarkerError + initBootstrap is true
  │
workspace.RunBootstrap(ctx, workspaceRoot, cloneURL, sourceSlug, ...)
  ├─ git -C <workspaceRoot> init
  ├─ git -C <workspaceRoot> remote add origin <cloneURL>
  ├─ git -C <workspaceRoot> fetch --depth 1 origin HEAD
  ├─ git -C <workspaceRoot> checkout -b niwa-bootstrap FETCH_HEAD
  ├─ visibility ← github.GetRepo(org, repo)  (soft-fail → "")
  ├─ if visibility lookup failed → emit stderr `note:` line
  ├─ workspace.ScaffoldFromSource(<workspaceRoot>, ScaffoldOptions{
  │     Name, Org, Visibility, IncludeGitkeep: true})
  │     ├─ writes .niwa/workspace.toml
  │     └─ writes .niwa/claude/.gitkeep
  ├─ git -C <workspaceRoot> add .niwa/
  ├─ git -C <workspaceRoot> commit -m "Initial niwa workspace config"
  └─ return success
  │
  ▼ fall through to existing post-flight (init.go:288)
  │
  ├─ config.Load(.niwa/workspace.toml) → succeeds
  ├─ emitVaultBootstrapPointer(...)               [no-op: no vault declared]
  ├─ globalCfg.SetRegistryEntry(name, entry)      [today: init.go:328]
  ├─ workspace.SaveState(workspaceRoot, state)    [today: init.go:342]
  ├─ printSuccess(...) + bootstrap WARNING block on stderr
  └─ writeLandingPath(workspaceRoot)              [today: init.go:390]

Shell wrapper drops user inside /home/user/workspaces/commuter
on branch niwa-bootstrap with a clean working tree.
```

The `os.Mkdir`-cleanup defer at `init.go:221-225` must be disarmed
explicitly before the bootstrap call returns success. Today's success
path disarms it at `init.go:395`; the bootstrap path needs equivalent
disarming. Setting `workspaceCreated = false` before invoking
`RunBootstrap` is the simplest pattern.

## Implementation Approach

Four phases, each a self-contained PR with tests and CI green before
the next phase starts.

### Phase 1: Error classification foundation

Build the typed-error infrastructure that both bootstrap and adjacent
failure-mode handling need.

Deliverables:
- `internal/github/fetch.go`: introduce `*github.StatusError` type; the
  four error-construction sites (lines 69, 72, 145, 149) return the
  typed value. `Error()` preserves today's text.
- Update the four test fakes in
  `internal/workspace/snapshotwriter_test.go` to construct
  `&StatusError{StatusCode: ...}`.
- `internal/cli/init.go`: replace the bare `"materializing config repo: %w"`
  wrap with the `errors.As` classifier described in Key Interfaces.
  Wire `*AmbiguousMarkersError` → existing text; `*NoMarkerError` →
  existing text plus a hint pointing at `--bootstrap` (the flag itself
  ships in Phase 2 — the hint is forward-looking); 401/403 → GH_TOKEN
  scope message; 404 → zero-commit / typo / private message.
- Unit tests for the classifier covering each typed-error case.
- `@critical` Gherkin scenarios in `test/functional/features/` for the
  401, 403, and 404 user-visible messages.

### Phase 2: Flag surface + prompt UX

Add the `--bootstrap` / `--no-bootstrap` flag pair with TTY-gated
prompt and non-TTY refusal. Dispatch stubs to a "not yet implemented"
error so the flag surface is testable before the orchestrator lands.

Deliverables:
- `internal/cli/init.go`: declare `--bootstrap` and `--no-bootstrap`
  flags. Mutual-exclusion check (matches `--overlay` / `--no-overlay`
  at `init.go:135-137`).
- TTY-gated prompt: when `*NoMarkerError` fires, the user is in a TTY,
  and neither flag is set, prompt `[Y/n]` using
  `cli.ReadConfirmation`. Yes → proceed as if `--bootstrap` was set
  (stub error in this phase). No → existing fail-loud path.
- Non-TTY without flag: fail fast with hint pointing at `--bootstrap`,
  matching destroy.go's `IsStdinTTY()` refusal pattern.
- Unit tests for flag wiring, mutual exclusion, prompt-yes/no, non-TTY
  refusal.
- The bootstrap dispatch is a stub returning
  `errors.New("bootstrap not implemented yet")` — full integration in
  Phase 4.

### Phase 3: Scaffold derivation

Build the scaffold + visibility-lookup machinery independent of the
bootstrap orchestrator.

Deliverables:
- `internal/github/client.go`: new `(*APIClient).GetRepo(ctx, owner, repo)`
  method returning the existing `*Repo` struct.
- `internal/workspace/scaffold.go`: new `ScaffoldOptions` struct; new
  `ScaffoldFromSource(dir, opts)` function. Implements the literal
  TOML body from Decision 4, including `.niwa/claude/.gitkeep`.
- Shared helper for the schema doc-link footer reused between
  `Scaffold` and `ScaffoldFromSource` (small extraction).
- Soft-fail behavior for visibility lookup: any error → empty
  `Visibility`, scaffold emits `[groups.public]`, stderr `note:` line
  explains the fallback.
- Unit tests for: scaffold body matches expected TOML; visibility
  lookup feeds into the right `[groups.<vis>]` line;
  visibility-lookup failure emits the expected note and falls back to
  `[groups.public]`; `.gitkeep` is written.

### Phase 4: Bootstrap orchestration + end-to-end

Wire everything into a working flow.

Deliverables:
- New `internal/workspace/bootstrap.go`. Implements `RunBootstrap`
  per the Data Flow diagram: `git init` → `remote add` → `fetch --depth 1`
  → `checkout -b niwa-bootstrap` → visibility lookup → scaffold →
  stage → commit.
- `internal/cli/init.go`: replace the Phase 2 stub with the real
  `workspace.RunBootstrap` call. Disarm `workspaceCreated` before
  invoking; on bootstrap failure, the cleanup defer fires normally.
- Success-message block on stderr in WARNING style (matches the
  `--rebind` precedent's prominence): workspace path, bootstrap branch
  name, "next steps" (review with `git show HEAD`, push with
  `git push -u origin niwa-bootstrap`, then `niwa apply`).
- Bootstrap path checks the source host: GitHub → proceed; non-GitHub
  → refuse with "v1 supports GitHub sources only; file a follow-up if
  you need <host>" message.
- `@critical` Gherkin scenario covering the full
  `niwa init <name> --from <empty-github-remote> --bootstrap` flow
  using the `localGitServer` test helper. Verify: workspace.toml on
  disk, branch created, commit landed, registry entry written, shell
  wrapper landing-path written, success message format.
- Documentation update in `docs/guides/` or `README.md` describing the
  bootstrap flow and the `--bootstrap` flag.

## Consequences

### Positive

- **Single-command bootstrap** of a niwa-managed workspace from a
  freshly-created GitHub repo. The chicken-and-egg friction the user
  identified is removed.
- **Better error messages across the materialize surface.** 401/403,
  404, ambiguous, and no-marker all get case-specific Detail +
  Suggestion remediation pointers via the same classifier seam.
- **Typed-error infrastructure** in `internal/github` makes future
  classification work (malformed config sentinel, non-GitHub transport
  classification) small follow-up PRs rather than refactors.
- **No regression to existing init paths.** `modeScaffold` and
  `modeNamed` are untouched. The classifier replaces only the generic
  error wrap, with the generic case preserved as fall-through.
- **Coherent with niwa's CLI idioms.** `--bootstrap` / `--no-bootstrap`
  matches four existing flag pairs; TTY gating reuses `IsStdinTTY()`;
  the success-WARNING matches the `--rebind` audit-trail style;
  stderr `note:` for visibility-lookup soft-fail matches the
  vault-bootstrap pointer pattern.

### Negative

- **v1 does not handle truly empty (zero-commit) remotes** that 404
  upstream of the probe. The user must push at least one commit before
  retrying. Decision 2 commits to a clear remediation message but
  defers the disambiguation API call to v2.
- **In-place worktree model** means the cloned tree, bootstrap branch,
  and workspace root all share `<cwd>/<name>/`. There is no
  "throwaway worktree to inspect before promoting" — if the user
  decides the scaffold is wrong, the recovery is `rm -rf <cwd>/<name>/`
  plus a registry-prune step.
- **New API surface widens the maintenance area.** Three additions:
  `*github.StatusError`, `(*github.APIClient).GetRepo`, and
  `workspace.ScaffoldFromSource` + `workspace.RunBootstrap`. Each is
  small but adds to the contract the package owes.
- **The bootstrap path performs a real `git fetch`** rather than
  reusing the tarball probe — slightly slower for large remotes (the
  user pays this cost once per bootstrap). Mitigated by `--depth 1`.
- **The original "use the worktree setup in niwa" framing was rejected
  for structural reasons** (post-flight + apply discovery require
  `<workspaceRoot>/.niwa/workspace.toml` to exist in the main checkout).
  Documented in Decision 1's Alternatives.

### Mitigations

- **Zero-commit case:** Decision 2's 404 message names the
  remediation explicitly ("push at least one commit and retry with
  --bootstrap"). v2 can revisit if real users report this is common
  enough to warrant the extra `repos/get` API call.
- **In-place recovery:** Documented in release notes and the
  `--bootstrap` flag's `--help` text — recovery is git-native
  (`rm -rf` + manual or future `niwa registry prune`). Reaffirms the
  user-statement that bootstrap is a one-shot setup action, not a
  workflow that runs repeatedly.
- **Maintenance surface:** The new types and methods have minimal,
  intentional shape. `StatusError` carries three fields; `GetRepo`
  reuses the existing `*Repo` type; `ScaffoldFromSource` and
  `RunBootstrap` have narrow, well-documented contracts. Functional
  test coverage at `test/functional/` enforces user-visible
  behavior.
- **Fetch latency:** `--depth 1` keeps the fetch O(latest commit) and
  not O(history). For the user's scenario (brand-new repo, README
  only), the fetch is effectively zero-bytes-over-the-wire.
- **Worktree-framing divergence:** Decision 1's note section makes the
  rationale explicit. The W2+R3+C1 alternative is documented for
  reviewers who might prefer the separated-worktree pattern.

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
