# Brief context — niwa mesh removal + worktree-subsystem extraction

Public-safe scope input for the `/scope niwa-mesh-removal` chain. Restated
self-containedly; carries no cross-repo tracking references.

## What and why (settled — BRIEF/PRD are thin)

niwa's pre-pivot agent-facing mesh (the MCP server, the task-delegation
substrate, the per-worktree mesh daemon, and the apply-pipeline hook synthesis)
is non-functional in practice today: the only capability that actually works is
git worktree creation. The mesh code is dead weight that confuses the codebase
and contradicts niwa's identity as a workspace/worktree manager — not an
agent-facing tool, not a session manager. We remove it now, ahead of any
replacement coordination layer, rather than carrying it while a replacement is
built in parallel.

The single load-bearing constraint: **worktree creation must survive the
removal as a first-class CLI command, fully decoupled from the MCP package.**

## Scope

- Delete the `internal/mcp/` tree (MCP server, the MCP tools, audit subsystem,
  error-translation layer, daemon-starter wiring).
- Delete the pre-pivot CLI cluster (`internal/cli/mesh*.go`, `task*.go`,
  `mcp_*.go`) and unregister the orphaned cobra subcommands.
- Narrow the apply pipeline: stop synthesizing default mesh hooks
  (`mesh-session-start.sh`, `mesh-user-prompt-submit.sh`, `report-progress.sh`)
  and strip daemon-spawn calls so `apply` no longer launches background
  processes.
- Rename the preserved worktree verbs `niwa session *` -> `niwa worktree *`
  (with deprecation aliases) so the surviving capability lands under its final
  name. CONFIRMED IN SCOPE.

## Hard requirement (verify against current code; numbers may have drifted)

Today the real `git worktree add` lives in `internal/mcp/handlers_session.go`
(`CreateSession`), and `niwa session create/destroy` reach it via
`mcp.Server.CreateSessionDirect`. A prior code-reality review found surviving CLI
files consume on the order of ~52 `mcp.*` symbols across ~11 files, and
`internal/workspace/bootstrap.go` also calls `CreateSession` through a callback.
A pre-cursor refactor must extract the session-lifecycle + worktree-state
subsystem into a standalone package (e.g. `internal/worktree/`) BEFORE the bulk
deletion.

## Decisions to close in DESIGN

1. New package boundary + exact symbol set to move (CreateSession + params,
   SessionLifecycleState + state I/O, GitInvoker, scaffoldWorktreeNiwa,
   findRepoInWorkspace, attach-state, PID/ID utils).
2. Re-point create/destroy off `mcp.Server.CreateSessionDirect` while keeping
   `bootstrap.go`'s call working with no import cycle.
3. Sever the daemon spawn from worktree creation (create currently launches a
   per-worktree mesh daemon via DaemonStarter -> workspace.EnsureDaemonRunning ->
   `niwa mesh watch`; that mesh is being deleted, so create must stop spawning it).
4. Keep or drop attach/detach (`internal/cli/sessionattach/`) — the
   worktree-attach primitive; if kept, move its attach-state off mcp.
5. Fold the rename (`niwa session *` -> `niwa worktree *`, with deprecation
   aliases) into this change. CONFIRMED IN.
6. Atomic landing: the three deletions ship in one release; intermediate states
   don't compile and the apply pipeline references daemon-spawn calls.

## Out of scope

Backfilling task delegation, building a replacement mesh/coordination substrate,
or any bridge skill — all downstream of (and gated on) this removal. The
materialization pillar (`internal/workspace/` clone/scaffold/snapshot/vault) is
unchanged.

## Acceptance anchor

After the extraction and the `internal/mcp/` deletion: `niwa worktree create
<repo> <purpose>` (+ destroy/list) works end-to-end; build + `go test ./...`
pass with no orphaned `mcp.*` imports; `niwa apply` no longer synthesizes mesh
hooks or spawns daemons.

## Process constraints

- DESIGN doc lands at `docs/designs/` in this (public niwa) worktree; PLAN at
  `docs/plans/`. Do NOT set the design's `upstream:` frontmatter to any
  private-repo path. Restate the reframe self-containedly. The PLAN's issues are
  new niwa-repo issues.
- Do NOT coordinate this work through niwa task-delegation — it is exactly what
  is being deleted; run the chain in-session.
