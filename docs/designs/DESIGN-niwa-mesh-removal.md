---
status: Planned
problem: |
  niwa's git-worktree creation is implemented inside the internal/mcp package,
  entangled with a non-functional pre-pivot agent-facing mesh (MCP server, task
  and change handlers, a per-worktree daemon, and apply-pipeline hook synthesis).
  61 distinct mcp.* symbols are consumed across 21 CLI files. The mesh cannot be
  deleted without first decoupling the one capability users rely on.
decision: |
  Extract the worktree-and-session lifecycle subsystem into a new leaf package
  internal/worktree/, re-point every surviving consumer (CLI, workspace
  bootstrap, the attach primitive) at it, then delete internal/mcp and the
  pre-pivot CLI cluster wholesale. Worktree creation becomes a pure git+state
  operation with no daemon spawn; apply stops synthesizing mesh hooks and
  spawning daemons. The preserved verbs are renamed niwa session * -> niwa
  worktree * with deprecation aliases. Everything lands in one release.
rationale: |
  The factored CreateSession already takes a params struct and is called
  directly by both the CLI and the bootstrap callback, so the extraction is a
  package move plus import rewrite, not a logic rewrite. A single leaf package
  avoids an import cycle and keeps the mutually-referential lifecycle symbols
  together. Atomic landing is forced by the coupling: any partial deletion
  leaves a non-building tree.
upstream: docs/prds/PRD-niwa-mesh-removal.md
---

# DESIGN: niwa mesh removal + worktree-subsystem extraction

## Status

Planned

Scope amendment (during implementation): the agent-facing surface to delete is
wider than the original "internal/mcp + pre-pivot CLI" framing. It also includes
the coordinator session registry and `niwa session register`, and the
change-review surface (`niwa surface`, `internal/web/*`, the change-store and
audit subsystem). All are agent-facing coordination — squarely the surface this
design removes "in full" — and are deleted in Stage 2. niwa retains no MCP, no
agent-coordination channels, and no change-review web UI. The worktree-attach
primitive is still kept and decoupled (Decision C).

## Upstream Design Reference

This design implements PRD-niwa-mesh-removal (`docs/prds/PRD-niwa-mesh-removal.md`,
Accepted). Requirements R1–R10 are referenced inline. There is no parent
strategic design; this is a tactical design in a tactical repo.

## Context and Problem Statement

niwa accumulated an agent-facing coordination layer before a strategic pivot: an
MCP server, a task-delegation substrate, change-review handlers, a per-worktree
background daemon (`niwa mesh watch`), and an apply pipeline that synthesizes
default mesh hooks into managed workspaces. That layer is non-functional in
practice and nothing exercises it end-to-end. The capability users actually rely
on is git worktree creation, and it is structurally trapped inside the package
slated for deletion.

A code-reality review of the current tree established the exact coupling:

- The real `git worktree add` lives in `internal/mcp/handlers_session.go` in the
  factored `CreateSession` (line 252), which takes a `CreateSessionParams` struct
  (line 44) and returns `(sessionID, worktreePath, branchName, err)`.
- `niwa session create/destroy` (`internal/cli/session_lifecycle_cmd.go`) reach
  it by constructing an `mcp.Server` via `mcp.New(...)` and calling
  `srv.CreateSessionDirect(...)` / `srv.DestroySessionDirect(...)`, thin wrappers
  (`internal/mcp/server.go:182,190`) over the session handlers.
- The workspace bootstrap path invokes the same `CreateSession` through a
  `CreateSessionFunc` callback (`internal/workspace/bootstrap.go:47`), wired in
  `internal/cli/init.go` (`createSessionWrapper`, lines 192–200). The callback
  type carries no mcp types specifically to avoid an import cycle.
- **61** distinct `mcp.*` symbols are consumed across **21** CLI files. Of those,
  **14** files are the pre-pivot cluster slated for deletion (`mesh*.go`,
  `task*.go`, `mcp_*.go` and their tests; `mesh_watch_test.go` alone has 119
  references), and **7** surviving files (`session_lifecycle_cmd.go`,
  `session_register.go`, `daemon_starter.go`, `init.go`, `surface.go`, `go.go`,
  `completion.go`) need re-pointing.
- `internal/mcp/` holds **45** Go files (server, the MCP tools, audit subsystem,
  task/change handlers and stores, watcher, auth, plus the session-lifecycle and
  liveness code we want to keep).
- Worktree creation currently spawns a daemon: `CreateSession` calls
  `params.DaemonStarter` (`handlers_session.go:333`), wired to
  `workspace.EnsureDaemonRunning` (`internal/workspace/daemon.go`), which launches
  `niwa mesh watch` with a 500 ms readiness timeout and full rollback on failure.
- The apply pipeline synthesizes three mesh hooks in
  `internal/workspace/channels.go:376–404` (`mesh-session-start.sh`,
  `mesh-user-prompt-submit.sh`, `report-progress.sh`) and calls
  `EnsureDaemonRunning` at `channels.go:355` and `:538`.
- `internal/workspace/` is otherwise independent of `internal/mcp/`; the only
  import is in `daemon.go` (`IsPIDAlive`, `ReadState`, `TaskStateRunning`).

The problem this design solves: define the package boundary and the order of
operations that let the mesh be deleted in full (R3, R4, R5, R6) while worktree
creation survives as a first-class CLI command owing nothing to the deleted
package (R1, R2, R8), with the tree building at the single landing point (R9,
R10).

## Decision Drivers

- **Preserve worktree creation, decoupled (R1, R2).** The surviving capability
  must not import the deleted package, transitively or directly.
- **No import cycle.** `internal/workspace/` already bends over backwards
  (callback type with no mcp types, JSON field extraction in
  `DefaultDestroySession`) to avoid importing the session code. The new package
  must be importable by both `internal/workspace/` and `internal/cli/` without a
  cycle.
- **Atomic landing (R10).** Because surviving code consumes 61 mcp.* symbols and
  apply references the daemon path, the tree only builds when the extraction,
  the deletions, and the re-pointing all land together.
- **Honor niwa's identity.** The product is a workspace and worktree manager, not
  an agent-facing tool. The end state must contain no package named or shaped
  around an MCP server, and no background-process side effects from `niwa apply`.
- **Minimum disturbance to the preserved capability.** The factored
  `CreateSession` is already a clean function; the extraction should move it, not
  rewrite it, to keep the diff reviewable and behavior identical.
- **Preserve, don't expand (PRD Out of Scope).** This change keeps the existing
  worktree and attach primitives under a clean boundary; it does not add verbs or
  redesign the attach contract.

## Considered Options

### Decision A — Boundary and shape of the extracted package

The worktree-lifecycle symbols live in `internal/mcp/` but are logically
independent of the MCP protocol. What moves, and into what shape?

**Chosen: A1 — one cohesive leaf package `internal/worktree/`.** Move the
session-and-worktree lifecycle cluster: `CreateSession` + `CreateSessionParams`,
`SessionLifecycleState` and its state I/O (`Read/Write/ListSessionLifecycleState`,
`NewSessionLifecycleState`, the status constants), the session-ID generator and
its atomic reservation, `scaffoldWorktreeNiwa`, `findRepoInWorkspace`, the
`GitInvoker` interface and its std implementation, the session registry
(`SessionEntry`, `WriteSessionEntry`, `SessionRegistry`, `DiscoverClaudeSessionID`),
`AttachState` and its I/O, `DaemonHealth`/`DaemonHealthFor`, and the liveness
helpers (`IsPIDAlive`, `PIDStartTime`). Leave the MCP-protocol code (server, the
tools, audit, task/change handlers and stores, watcher, auth, allowed-tools) in
`internal/mcp/` to be deleted. The package is a leaf: it imports neither
`internal/mcp/` nor `internal/workspace/`, so both can depend on it.

*Why it works:* the moved symbols are mutually referential (`CreateSession`
writes a `SessionLifecycleState`, scaffolds the worktree, and reads attach/PID
state), so they form a natural cohesive unit. The consumers
(`session_lifecycle_cmd.go`, `session_register.go`, the attach primitive) use
these symbols together, not piecemeal.

**Rejected: A2 — split into `internal/worktree/` + `internal/session/`.** A
boundary between "git worktree mechanics" and "session state" reads tidy but the
symbols cross it constantly: `CreateSession` (worktree) constructs and writes
`SessionLifecycleState` (session) and scaffolds the directories the state lives
in. The split produces a chatty inter-package API and risks a cycle, for no
consumer that wants one half without the other. Over-decomposition.

**Rejected: A3 — minimal extraction, leave a shrunken `internal/mcp/`.** Move
only `CreateSession` and its closest dependencies; leave attach-state, liveness,
and the registry behind. Rejected because it defeats R3 (delete `internal/mcp/`
entirely): surviving code (the attach primitive, `workspace/daemon.go`'s
`IsPIDAlive` use) would still import a package named `mcp`, keeping the identity
confusion the whole effort removes, and leaving a vestigial package no one can
fully delete.

### Decision B — Severing the daemon spawn from worktree creation

Creating a worktree today spawns `niwa mesh watch` and can fail on a spawn
timeout. The daemon is the mesh; it is being deleted.

**Chosen: B1 — remove the spawn path entirely.** Drop the `DaemonStarter` field
from `CreateSessionParams`, delete the spawn call and its rollback branch
(`handlers_session.go:333` and the timeout error), drop the empty `daemon.pid`
placeholder from `scaffoldWorktreeNiwa`, and delete `workspace.EnsureDaemonRunning`
/ `workspace.TerminateDaemon` along with `workspace/daemon.go`. Remove apply's two
`EnsureDaemonRunning` calls (`channels.go:355,538`) since the mesh hooks they
support are deleted under R4. Worktree creation becomes a pure git-plus-state
operation: no background process (R6), no spawn-timeout failure mode.

*Why it works:* nothing the preserved capability does requires a daemon; the
daemon only ever served the mesh. Removing it also removes a whole class of
flaky failure (the 500 ms readiness race).

**Rejected: B2 — keep `EnsureDaemonRunning`, make `DaemonStarter` optional
(nil → skip).** A nil-guarded call to a function that spawns a deleted command
(`niwa mesh watch`) is dead code by construction, plus the unreachable
`ErrDaemonSpawnTimeout` rollback branch — exactly the confusing dead weight this
effort pays down.

**Rejected: B3 — repurpose the spawn seam for a future replacement.** Keeping a
spawn hook pre-commits a design decision that belongs to the downstream
replacement work, which is out of scope and deliberately sequenced after this
removal (so the workspace never carries two coordination mechanisms at once).

### Decision C — Fate of the worktree-attach primitive

`internal/cli/sessionattach/` (12 files) supervises a tool process in a worktree,
holds an in-use lock via the `attach.state` sentinel, and runs preflight/daemon
health checks. It currently reads mcp types and is wired to the daemon.

**Chosen: C1 — keep the attach primitive, decouple it from the mesh.** Retain
`internal/cli/sessionattach/` as the worktree-attach primitive, but: (a) point
its `AttachState`/liveness reads at the new `internal/worktree/` package, and
(b) remove the daemon-supervision wiring (`EnsureDaemonRunningFn`/
`TerminateDaemonFn`) and the mesh-register coupling, which referenced the deleted
daemon and hooks. The command is renamed under the worktree namespace alongside
the other verbs (Decision E). Its remaining substance — validate the worktree,
acquire the in-use lock, launch the tool in the worktree — is independent of the
mesh.

*Why it works:* the attach primitive is the seam future worktree-lifecycle verbs
compose with; its core (lock + launch in a worktree) does not depend on a mesh
daemon. Decoupling it now keeps the preserved-capability surface intact without
expanding it.

**Rejected: C2 — keep attach and redesign it into a generic shell/editor
launcher.** That is a feature redesign of the attach contract — out of scope per
the PRD (no worktree feature expansion). The shape of a post-mesh attach belongs
to downstream worktree-lifecycle work, not this removal.

**Rejected: C3 — delete attach/detach entirely.** Simplest diff, but it discards
the worktree-attach primitive that downstream worktree-lifecycle verbs are
expected to build on, forcing its later re-creation. Deleting a primitive to
re-add it is churn; decoupling preserves it cheaply.

### Decision D — Landing strategy

**Chosen: D1 — atomic landing in one release (R10).** The extraction, the
re-pointing, the bulk deletion, and the rename ship together; no committed state
on the main branch fails to build. Within the change the work is staged as
ordered commits (extract+re-point, then delete, then rename) for review
legibility, but the merge is one unit.

**Rejected: D2 — incremental deletion across releases.** Any intermediate state
where the mesh is partly deleted leaves surviving CLI referencing missing symbols
or apply referencing a missing daemon path — a non-building tree on main. The
coupling (61 symbols, the apply daemon calls) makes incrementalism impossible
without shipping breakage.

### Decision E — The rename (settled input, recorded for completeness)

`niwa session *` is renamed to `niwa worktree *` with deprecation aliases that
keep the old name working and emit a notice (R7). Folded into this change so the
preserved capability lands once under its final name rather than being renamed in
a follow-up. The only considered alternative — a separate later rename PR —
was rejected to avoid churning the same command surface twice and to land the
preserved verbs under their permanent name immediately.

## Decision Outcome

The end state: a new leaf package `internal/worktree/` owns worktree-and-session
lifecycle; `internal/mcp/` and the 14-file pre-pivot CLI cluster are gone;
`internal/workspace/daemon.go` is gone and apply spawns nothing and synthesizes
no mesh hooks; the attach primitive survives, decoupled and renamed; and the
preserved verbs live at `niwa worktree *` with deprecation aliases. The whole set
lands in one release that builds and passes the full test suite (R9, R10).

This composition satisfies every requirement: R1/R2/R8 (worktree creation,
destroy, list, and the bootstrap callback all run against `internal/worktree/`
with no mcp dependency); R3/R4 (mcp package and pre-pivot CLI deleted); R5/R6
(apply synthesizes only declared hooks and spawns no daemon); R7 (rename with
aliases); R9/R10 (atomic, building landing).

## Solution Architecture

### Components after the change

- **`internal/worktree/` (new leaf package).** Holds the ~30 lifecycle symbols
  moved from `internal/mcp/`. Public surface used by consumers: `CreateSession` +
  `CreateSessionParams` (now without a `DaemonStarter` field), the
  `SessionLifecycleState` type and its read/write/list helpers, `GitInvoker`,
  `SessionEntry`/`WriteSessionEntry`/`SessionRegistry`/`DiscoverClaudeSessionID`,
  `AttachState` + I/O, `DaemonHealth`/`DaemonHealthFor` (retained only if status
  reporting keeps a worktree-liveness notion; otherwise dropped — see below),
  and `IsPIDAlive`/`PIDStartTime`. Imports only stdlib and small shared helpers;
  imports neither `internal/mcp/` nor `internal/workspace/`.
- **`internal/cli/` worktree commands.** `session_lifecycle_cmd.go` (renamed to
  the worktree namespace) calls `worktree.CreateSession(ctx,
  worktree.CreateSessionParams{...})` and a destroy path directly, instead of
  constructing an `mcp.Server` and calling `CreateSessionDirect`/
  `DestroySessionDirect`. `session_register.go`, `surface.go`, `go.go`,
  `completion.go`, `daemon_starter.go` re-point their mcp.* references at
  `internal/worktree/`.
- **`internal/cli/sessionattach/`.** Re-pointed at `internal/worktree/` for
  attach-state and liveness; daemon-supervision wiring removed; renamed under the
  worktree namespace.
- **`internal/workspace/`.** `bootstrap.go`'s `createSessionWrapper` (in
  `init.go`) calls `worktree.CreateSession` instead of `mcp.CreateSession`. The
  `CreateSessionFunc` callback seam is retained for test injection. `daemon.go`
  is deleted; `channels.go` loses the three hook generators and the two
  `EnsureDaemonRunning` calls. The materialization code (clone, scaffold,
  snapshot, vault) is untouched (PRD Out of Scope).
- **Deleted.** `internal/mcp/` in full; CLI `mesh*.go`, `task*.go`, `mcp_*.go`
  and their tests (14 files); the `mesh`, `task`, and `mcp-serve` cobra command
  registrations; `workspace/daemon.go`.

### Data flow: `niwa worktree create <repo> <purpose>`

1. CLI resolves the instance root and builds `worktree.CreateSessionParams`
   (instance root, repo, purpose, branch prefix, a `GitInvoker`).
2. `worktree.CreateSession` resolves the repo via `findRepoInWorkspace`, runs
   `git worktree add` on a new branch via the `GitInvoker`, scaffolds the minimal
   `.niwa/` layout (no `daemon.pid` placeholder), allocates a session ID, and
   writes the `SessionLifecycleState` JSON.
3. Returns `(sessionID, worktreePath, branchName)`. No daemon is spawned; there
   is no spawn-timeout failure path.

The bootstrap path is identical, reached through the retained callback.

### Import-cycle analysis

`internal/worktree/` is a leaf (stdlib only). `internal/workspace/` may import it
directly — the `CreateSessionFunc` callback that exists to dodge the old cycle is
no longer load-bearing for cycle-avoidance, though it is kept as a test seam.
`internal/cli/` imports both. No cycle is introduced because the extracted
package depends on nothing in this repo's other internal packages.

### `niwa status` and surface

`niwa status` and `surface.go` read session state and (for status) daemon health.
After removal there is no daemon, so status drops its daemon-health column and
reads only `SessionLifecycleState` from `internal/worktree/`. This is part of the
narrowing (R5/R6), not a feature change.

## Implementation Approach

The work is one atomic landing (R10), staged as three ordered commits for review
legibility. A non-building intermediate never reaches main.

**Stage 1 — Pre-cursor extraction and re-pointing (the tree still builds).**
Create `internal/worktree/`, move the lifecycle symbols (a `git mv`-style move
plus package rename, no logic change), remove the `DaemonStarter` field and the
spawn call from `CreateSession`, and re-point all 7 surviving CLI files, the
attach primitive, and the bootstrap wrapper at `internal/worktree/`. At the end
of this stage `internal/mcp/` still exists but no surviving code imports it. The
build passes; the test suite for the moved code moves with it.

**Stage 2 — Bulk deletion (the tree still builds).** Delete `internal/mcp/`
entirely, the 14-file pre-pivot CLI cluster, and `workspace/daemon.go`.
Unregister the `mesh`, `task`, and `mcp-serve` cobra commands. Remove the three
hook generators and the two `EnsureDaemonRunning` calls from `channels.go`. Drop
the daemon-health column from `niwa status`. Revise/delete the affected tests
(8 CLI test files deleted; `workspace/daemon_test.go` deleted with `daemon.go`;
the moved lifecycle tests already relocated in Stage 1).

**Stage 3 — Rename with deprecation aliases (R7).** Rename `niwa session *` to
`niwa worktree *`, register deprecation aliases that keep the old paths working
and emit a notice, and rename the attach subcommand under the worktree namespace.
Add a `@critical` functional scenario covering `niwa worktree create` end to end
per the repo's testing convention.

**Verification (R9).** `go build ./...`, `go vet ./...`, `go test ./...`, and
`make test-functional-critical` all pass after Stage 3. A grep confirms zero
references to `internal/mcp` remain outside deleted files.

## Security Considerations

- **Command execution / argument injection.** Worktree creation shells out to
  `git` via the `GitInvoker` with `repo` and `purpose` as inputs. This behavior
  is unchanged by the extraction — the same `exec.CommandContext("git", args...)`
  with argument slices (not shell strings) moves verbatim into
  `internal/worktree/`. No new injection surface is introduced; the move must
  preserve the slice-argument form and must not interpolate inputs into a shell
  string. **Mitigation:** the extraction is a verbatim move; a review check
  confirms no `sh -c`/string-built git invocation is introduced.
- **Path traversal in repo/purpose.** `findRepoInWorkspace` resolves a repo by
  scanning the instance root, and the session ID (not user input) names the state
  file. The extraction preserves the existing resolution; it does not widen it to
  accept caller-supplied paths. **Mitigation:** keep `findRepoInWorkspace`'s
  bounded scan; do not add a caller-supplied path parameter.
- **Reduced attack surface (positive).** Deleting the MCP server, the
  task/change handlers, the audit subsystem, and the background daemon removes a
  large amount of network-/IPC-adjacent and process-spawning code. `niwa apply`
  no longer launches a detached background process (`Setsid`), eliminating a
  spawned-process lifecycle the operator did not request.
- **State-file I/O.** `SessionLifecycleState` and `AttachState` use atomic
  tmp+rename writes with 0600 perms; this is preserved by the move. No new
  secrets are read or written; the vault/secret path is untouched (Out of Scope).
- **Not applicable.** No new external input, network listener, authentication
  surface, or deserialization of untrusted data is introduced — the change is net
  subtractive. The retained code paths are the ones that already shipped.

## Consequences

### Positive

- Worktree creation survives as a first-class command with no mcp dependency and
  no daemon side effect; a class of flaky spawn-timeout failures disappears.
- `internal/mcp/` (45 files) and 14 CLI files are gone; the codebase a
  contributor reads contains only working capabilities, matching niwa's identity.
- `niwa apply` is side-effect-honest: only declared hooks, no background process.
- The preserved verbs land under their permanent `niwa worktree` name in one
  release, with aliases bounding migration cost.

### Negative / trade-offs

- One large change rather than several small ones (forced by R10). Mitigated by
  staging into three ordered, individually-building commits for review.
- The attach primitive is kept in a decoupled-but-not-yet-redesigned state: its
  daemon-supervision wiring is removed, but the final shape of a post-mesh attach
  is left to downstream worktree-lifecycle work. Mitigation: the decoupling is
  mechanical and the command still validates+locks+launches; the redesign
  question is explicitly deferred, not silently dropped.
- `DaemonHealth`/`DaemonHealthFor` and the liveness helpers move into
  `internal/worktree/` even though, post-removal, their only remaining consumer
  is the attach primitive's preflight. If a later review finds attach no longer
  needs liveness, these can be dropped then; carrying them now keeps the
  extraction a pure move. Mitigation: flagged for the downstream attach redesign.

### Neutral

- Downstream replacement coordination (a new mesh or bridge) remains out of scope
  and gated on this removal, so the workspace never carries two coordination
  mechanisms at once.
