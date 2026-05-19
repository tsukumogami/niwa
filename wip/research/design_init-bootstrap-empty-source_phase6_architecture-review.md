# Architecture review: init bootstrap from empty source

Reviewer role: architect-reviewer
Design under review: `docs/designs/current/DESIGN-init-bootstrap-empty-source.md`
Date: 2026-05-18

## Summary

The design fits niwa's existing layering and CLI idioms. The four-phase
breakdown is well-sequenced and produces a testable state at each step.
The classifier seam, typed `*github.StatusError`, and
`workspace.ScaffoldFromSource` / `RunBootstrap` are appropriately
scoped — no parallel patterns, no premature abstraction, no layering
inversion. The divergence from the user's "use the worktree setup"
framing is justified structurally.

A few advisory findings on interface shape, one phase-3 sequencing
nit, and one missing-component gap (the registry-source-URL value to
record). No blocking issues.

---

## 1. Architecture clarity

The data-flow diagram (Solution Architecture §Data Flow) is concrete
enough to implement without ambiguity. Component boundaries are clean:

- `internal/github` produces a typed `*StatusError`; `internal/cli`
  matches on it via `errors.As`.
- `internal/workspace` owns `ScaffoldFromSource` (pure file writes)
  and `RunBootstrap` (orchestration: validate → git → scaffold →
  commit). `cli` passes the resolved cloneURL and reporter in.
- Cleanup contract is explicit (caller keeps the defer armed; disarm
  AFTER success).

Two clarifications worth folding into the design before implementation:

**1a. Registry source URL value.** Decision 1 says "`SourceURL` records
the `--from` slug." The existing `modeClone` path (init.go:328) records
whatever `source` resolved to — which is either the raw `--from` slug
(when passed) or the registry's prior entry. For bootstrap, this is the
same code path, so the value flows through automatically — but only if
the existing post-flight + registry-write block at init.go:286-342 is
re-entered after `RunBootstrap` returns. The Data Flow diagram does
show fall-through to "existing post-flight (init.go:288)," but the
Implementation Approach Phase 4 description should explicitly state
that `RunBootstrap` returns control to the existing post-flight block,
not that it duplicates it. (One sentence: "After `RunBootstrap`
returns successfully, control falls through to the unchanged
post-flight block; `globalCfg.SetRegistryEntry` and `SaveState` run
against the in-place workspace.")

**1b. Error wrapping at the classifier seam.** `MaterializeFromSource`
already wraps `FetchTarball` errors as `"EnsureConfigSnapshot: fetch
%s: %w"` (snapshotwriter.go:490) and `materializeFromGitHub` further
wraps non-200 status with `"EnsureConfigSnapshot: fetch %s returned
%d"` (snapshotwriter.go:503). The wrapping at line 503 uses
`fmt.Errorf` with a plain status int, NOT a `%w` wrap of the
underlying typed error. The design's classifier relies on
`errors.As(materializeErr, &statusErr)` finding the typed error — this
will fail at line 503's wrap because the typed error isn't preserved.
**Phase 1 must also change snapshotwriter.go's `materializeFromGitHub`
to wrap the typed error with `%w`,** or to return the `*StatusError`
unwrapped. This is implicit in Decision 3's "the four error-construction
sites" but the construction site count is wrong — there is a fifth
construction site at snapshotwriter.go:503 that swallows the status.
Recommendation: explicitly enumerate this site in Phase 1's deliverables.

## 2. Missing components or interfaces

**2a. `BootstrapOptions` is referenced but not defined.** The
`RunBootstrap` signature in Key Interfaces takes `opts BootstrapOptions`
but the struct shape is never given. The only field hinted at is the
`Name` (workspace name). Other candidates: `Branch` (currently fixed),
`CommitMessage` (currently fixed), `IncludeGitkeep` (currently fixed).
For v1 with no configurability, `BootstrapOptions` is empty or single-
field. Either define it (`type BootstrapOptions struct { Name string }`)
or pass `Name` as a positional parameter and drop the struct.
**Recommendation:** pass `Name` as a parameter; defer introducing
`BootstrapOptions` until a second field exists. Premature struct.

**2b. Visibility lookup parameters.** `RunBootstrap` takes
`sourceSlug` and the design says "visibility ← github.GetRepo(org,
repo)". The slug must be parsed inside `RunBootstrap` to extract org
and repo — but `parseInitSource` already produces a `source.Source`
with `Owner` and `Repo` fields in the cli layer (init.go:249). Passing
the already-parsed `source.Source` instead of re-parsing inside
`RunBootstrap` is cleaner and removes an error path from the
orchestrator. Advisory.

**2c. `GetRepo` 404 handling for the visibility lookup.** The design
says visibility lookup "soft-fail → empty." `GetRepo` will likely
return `*StatusError{404}` for missing/private. The soft-fail logic
needs to suppress the typed error (don't propagate it as the
materialize-classifier sees it — that would route into the 404 user
message even though the materialize succeeded). The Phase 3 deliverable
list mentions "soft-fail behavior for visibility lookup" but should
state explicitly: inside `RunBootstrap`, the visibility lookup error
is logged via `reporter` and discarded; the materialize classifier
seam already executed by then so there's no cross-talk. Worth one
sentence in Phase 3 or Phase 4.

**2d. Branch already exists on origin.** Security Considerations lists
"Branch-name collision (`niwa-bootstrap` already exists upstream)" as
an operational edge case. After the user pushes, future re-runs of
`niwa init --bootstrap` against the same remote will collide. The
design correctly treats this as "out of scope — bootstrap is
one-shot," but the bootstrap error message when `git checkout -b`
fails should hint at this case ("if a niwa-bootstrap branch already
exists upstream, delete it or rename it before retrying"). Not
architectural; just a UX nit worth surfacing in Phase 4.

## 3. Phase sequencing

The four-phase split is correct in ordering:

1. **Phase 1 (error classification foundation)** — installs the typed
   error and classifier so Phase 2's hint at `--bootstrap` from the
   no-marker case has a place to land. Independently shippable: the
   401/403/404 hints improve user-facing errors even before bootstrap
   exists.
2. **Phase 2 (flag + prompt UX)** — adds the flag surface and stub
   dispatch. Independently shippable behind a "not yet implemented"
   error; flag wiring, mutual exclusion, prompt yes/no, non-TTY
   refusal all testable without orchestration.
3. **Phase 3 (scaffold derivation)** — `ScaffoldFromSource` and
   `GetRepo` are pure functions, testable in isolation.
4. **Phase 4 (orchestration + e2e)** — wires the above together;
   functional test exercises the full path.

Each phase produces a CI-green state with passing unit tests.

**One concern about Phase 1 + Phase 2 ordering interaction.** Phase 1
ships a "no-marker error → forward-looking `--bootstrap` hint" while
Phase 2 introduces the flag. Between the merge of Phase 1 and Phase 2,
a user could read the hint and try `--bootstrap` only to get "unknown
flag." This is a minor UX gap during the implementation interval; the
design should either (a) note that Phase 1's hint mentions a flag
that's coming, or (b) defer the `--bootstrap` hint string until Phase
2 lands the flag. Pragmatic preference: (b). Advisory.

## 4. Layering and dependency direction

Existing dependency direction: `cli` → `workspace` → `{config,
github, source}`. The design preserves this:

- `internal/workspace/bootstrap.go` will import `github` (for
  `FetchClient` and `GetRepo`), `config` (for marker probe re-use), and
  `source`. No new upward edges.
- `internal/cli/init.go` imports `workspace.RunBootstrap`, matching
  the existing `workspace.MaterializeFromSource` and `workspace.Scaffold`
  calls.
- `internal/github` gains the typed error and `GetRepo`. No new
  imports from github upward.

No layering violations. `cli` does not reach past `workspace` into
`github` for the bootstrap path (the existing `cli.runInit` already
constructs `github.NewAPIClient` for fetcher injection, which is the
established pattern; bootstrap inherits it via the same injection).

## 5. Interface contracts

**`*github.StatusError{StatusCode, Message, URL}`.** Three fields is
right-sized. `URL` (optional, for diagnostics) is slight scope creep
— no caller in v1 reads it — but it costs nothing and serves
future log-correlation needs. Acceptable. Confirm that `Error()`
preserves today's exact text including the `(verify GH_TOKEN scopes;
...)` parenthetical for 401/403 — the existing test fakes
string-match this in snapshotwriter_test.go:261/302/343/376, so
Phase 1's "preserves today's text" claim is load-bearing for
not breaking unrelated tests.

**`(*github.APIClient).GetRepo(ctx, owner, repo)`.** Returns the
existing `*Repo` struct — good reuse, no parallel type. Maps cleanly
into `ScaffoldOptions.Visibility` via the same `Private` → `"private"
| "public"` normalization `ListRepos` does (client.go:92-99). The
normalization helper should be extracted to a private function so
both call sites share one implementation; otherwise there are two
parallel normalizations to keep in sync. Advisory.

**`workspace.ScaffoldOptions{Name, Org, Visibility, IncludeGitkeep}`.**
Right-sized. `IncludeGitkeep` justified by the test-suppress use case
(Decision 5). No fields here that aren't consumed by the template
or the file-write logic — no schema drift.

**`workspace.RunBootstrap(ctx, workspaceRoot, cloneURL, sourceSlug,
opts, fetcher, reporter)`.** Seven parameters is at the edge of
"refactor to a struct." Two observations:
- `cloneURL` and `sourceSlug` are both derived from the original
  `--from`. Passing both means two sources of truth for "what remote
  are we talking to." Pass a single parsed `source.Source` (which
  carries `Owner`, `Repo`, and can re-resolve `CloneURL` from
  `globalCfg.CloneProtocol`) or pass only `cloneURL` and parse the
  slug back out where needed.
- If the param list grows in v2, fold into `BootstrapOptions`.

These are advisory shape concerns, not blocking.

## 6. Simpler alternatives the design considered

The Decision sections enumerate alternatives thoroughly. The one
simpler approach not explicitly evaluated:

**Could `RunBootstrap` reuse `Cloner.CloneWith(..., depth=1)` instead
of inlining `git init` + `git fetch --depth 1`?** Decision 5 answers
this — `git clone` refuses non-empty targets, and the workspace dir
already exists from `os.Mkdir`. The `git init` + `git fetch` choice
is correct. (One micro-alternative: have `init.go` defer the
`os.Mkdir` until after the materialize probe, letting `git clone`
own directory creation. This would unify the clone path with
`modeClone`'s existing cloner. But it inverts the existing
preflight/Mkdir order and ripples through the cleanup-defer logic.
Not worth the disruption.)

The `Cloner` abstraction at clone.go:43 is currently only used by
the overlay path. The bootstrap path's three-command sequence
(`init` + `remote add` + `fetch`) is structurally different enough
that introducing a `Cloner.Bootstrap()` method would be a leaky
abstraction. Inline `exec.CommandContext` calls in `bootstrap.go`,
matching the existing pattern, is the right call.

## Critical validation: divergence from the worktree framing

The user-stated preference was "use the git worktree setup in niwa."
Decision 1 chose in-place over separated-worktree. The justification:

- Post-flight verification at `init.go:288` reads
  `<workspaceRoot>/.niwa/workspace.toml` from the main checkout.
- `niwa apply` discovery via `config.Discover` expects the same path.
- A separated worktree forces reworks of both, plus an
  `InstanceState.BootstrapPending` field, plus apply-gate logic.

I confirmed these constraints by reading init.go:287-291 (post-flight
loads from `<workspaceRoot>/.niwa/workspace.toml`) and the discovery
contract is documented in `docs/guides/workspace-config-sources.md`.
The separated-worktree variant would indeed require coordinated
changes across the init success path, apply discovery, and a new
InstanceState field for the pending bootstrap.

**The divergence is well-justified.** The structural cost of W2+R3+C1
is high (touches three subsystems) and the user benefit (preserving
main-branch state until merge) is achievable via the in-place model
plus the user's existing `git push -u origin niwa-bootstrap` step
without merging. Decision 1's "Note on divergence" section explicitly
documents the rejected alternative for reviewers who might prefer the
separated pattern, which is the right way to handle this disagreement
— surface the trade-off, don't bury it.

The design is not overruling user intent without sufficient reason. It
is choosing the cheaper structural option while preserving the user's
underlying goal ("the user inspects and pushes themselves; no
automatic push from niwa"). The in-place model still leaves the user
on a non-default branch with a clean working tree and a clear push
target. That's the worktree-framing's user-visible benefit, delivered
without the worktree.

## Recommendations before approval

| Priority | Item |
|----------|------|
| Should fix | Phase 1 deliverable list: enumerate the fifth status-error site at `snapshotwriter.go:503` (`materializeFromGitHub`'s `returned %d` wrap), or the typed-error classifier in `cli` will silently miss the production 404 path. |
| Should clarify | Phase 4 description: state that control falls through to the existing post-flight + registry-write block; `RunBootstrap` does not duplicate that work. |
| Advisory | Define `BootstrapOptions` shape or drop it in favor of a `Name string` parameter; current design references the struct without specifying fields. |
| Advisory | Pass a parsed `source.Source` to `RunBootstrap` instead of separate `cloneURL` + `sourceSlug` (two sources of truth otherwise). |
| Advisory | Extract the `Private bool → Visibility string` normalization to a shared private helper used by both `ListRepos` and `GetRepo`. |
| Advisory | Phase 1's `NoMarkerError`-with-hint message should defer mentioning `--bootstrap` until Phase 2 lands the flag, to avoid an interim UX gap. |
| Advisory | Phase 4 branch-collision error: hint at "delete or rename existing niwa-bootstrap branch" so users hitting the re-run case have a recovery path. |

No blocking architectural issues. The design is approval-ready
after the two "should" items are folded in.
