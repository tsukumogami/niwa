# Architectural Review: DESIGN-niwa-mesh-reliability

## Summary verdict

The design is broadly sound and fits the existing architecture. The four decisions reuse established mechanisms (the `mainInstanceRoot` redirect that `handleAsk` already encodes, the flock'd `mcp.UpdateState` writer the daemon already calls, the body-opaque convention `niwa_delegate` already follows, and Claude Code's documented argv flags). Citations are mostly accurate. Two real architectural gaps are worth addressing before implementation: (a) the `<repoPath>` value Decision 1 needs at session-spawn time cannot be computed by the existing `resolveRoleCWD(s.instanceRoot, role)` call site because for session daemons `s.instanceRoot` is the worktree, not the main instance; (b) the `taskstore_lost` recreate-stub case cannot reuse `mcp.UpdateState` directly because that helper's first step is `readStateLocked`, which fails when state.json is missing.

## Citation-accuracy findings

Most file:line references are accurate or within a few lines. Spot-checked citations:

| Design citation | Verified | Note |
|---|---|---|
| `mesh_watch.go:776-803` (`handleInboxEvent` dangling block) | accurate | function starts at 776; dangling block 783-803 |
| `mesh_watch.go:908-1016` (`spawnWorker`) | accurate | function starts at 908 |
| `mesh_watch.go:982-1009` (claude command construction) | accurate | the two `exec.Command` blocks at 982-1000 and the env block at 1003-1009 |
| `mesh_watch.go:2315-2342` (`resolveRoleCWD`) | accurate | exact match |
| `mesh_watch.go:283-287` (daemon.pid write) | accurate | match |
| `server.go:768-778` (`isKnownRole`) | accurate | exact match |
| `server.go:780-843` (`handleAsk`) | slightly off | function body is 792-end; doc-comment starts at 780 |
| `server.go:802` (`isKnownRole(args.To)` in handleAsk) | accurate | match |
| `server.go:719` (sendMessageWithID inbox path) | accurate | match |
| `server.go:817-819` (askRoot inline redirect) | accurate | code is at 816-819 (off by 1 line, acceptable) |
| `server.go:264-279` (niwa_delegate registration) | accurate | match |
| `handlers_task.go:130-141` (UNKNOWN_ROLE → createTaskEnvelope) | accurate | match |
| `handlers_task.go:46-55` (`delegateArgs`) | accurate | match |
| `handlers_task.go:111-165` (`handleDelegate`) | accurate | match |
| `handlers_task.go:427-430` (terminal-state short-circuit in await) | accurate | match |
| `handlers_session.go:80-108` (`scaffoldWorktreeNiwa`) | accurate | match |
| `handlers_session.go:212-215` (extraEnv for session daemon) | accurate | match |
| `handlers_session.go:26-50` (`handleListSessions`) | accurate | match |
| `handlers_session.go:146-228` (`handleCreateSession`) | accurate | match |
| `daemon.go:35-102` (`EnsureDaemonRunning`) | accurate | match |
| `liveness.go:14-35` (`IsPIDAlive`) | accurate | match |
| `types.go:171-189` (`validTaskStates`) | accurate | match (190 if you include the closing brace) |
| `types.go:173-200` (`TaskStateAbandoned`) | over-broad | the constant itself is at 177; the cited range covers all five constants and helpers |
| `types.go:206-224` (`TaskEnvelope`) | over-broad | actual struct is 214-224; range starts at the doc-comment of `TaskParty` |
| `channels.go:347-359` (per-repo skill write loop) | accurate | match |
| `channels.go:341` (instance-root skill copy) | accurate | match |
| `channels.go:682-833` (`buildSkillContent`) | doc-comment at 682, body at 690-833 | range is the doc-comment + body, defensible |

No citation is wrong by more than the convention "include the doc-comment in the range". No grossly mismatched line numbers.

## Architectural concerns

### 1. Session-worker `<repoPath>` is not produced by `resolveRoleCWD(s.instanceRoot, role)` (Decision 1)

The design says (Solution Architecture, Worker spawn data flow): "`<repoPath>` = the role's repo working directory, computed via `resolveRoleCWD` (`mesh_watch.go:2315-2342`) — the same path that determines the worker's CWD for main-instance spawns."

For main-instance daemons this is correct: `resolveRoleCWD(s.instanceRoot, role)` returns `<workspace>/<group>/<role>`. For **session daemons** `s.instanceRoot` is the worktree (`<workspace>/.niwa/worktrees/<repo>-<id>/`), not the workspace. `resolveRoleCWD(worktree, role)` reads `worktree/<group>/<repo>` directories that don't exist and falls back to returning `instanceRoot` (the worktree itself). The session worker's `cmd.Dir` is therefore the worktree today (this is correct — the worker is meant to operate inside the worktree). But that means the session-spawn `--add-dir <repoPath>` would also resolve to the worktree if the design's exact instruction is followed — and the worktree has no committed `.claude/` (the niwa repo's `.claude/` is git-untracked, per Discovery point 4).

The fix is small but explicit: for session daemons the second `--add-dir` must point at `<taskStoreRoot>/<group>/<repo>` (i.e. the repo path *under the main instance*, where the source-of-truth `.claude/` lives), not at the worktree. The right call is `resolveRoleCWD(s.taskStoreRootDir(), evt.role)` for the `--add-dir` argument while leaving `cmd.Dir = resolveRoleCWD(s.instanceRoot, evt.role)` (= worktree) unchanged. The design should call this out and pick a different helper or pass a different first argument; it currently reads as if one call satisfies both needs.

### 2. Decision 2's "recreate state.json stub" cannot use `mcp.UpdateState` directly

Decision 2 says: "The daemon recreates a minimal stub (only state.json, with `state=abandoned`, `reason=taskstore_lost`, transition log seeded `unknown -> abandoned`). envelope.json stays missing." The Mitigation section then says: "implementation must reuse the existing `UpdateState` helper."

`mcp.UpdateState` (taskstore.go:266) does read-modify-write under flock; its second call is `readStateLocked(taskDir)` which fails when state.json is missing. For the dominant `taskstore_lost` sub-case the daemon must first bootstrap the stub (and possibly the task dir itself) before it can call `UpdateState`. This is not a layering violation — both packages already share `mcp.UpdateState`, and adding a sibling helper like `mcp.WriteAbandonedStubLocked(taskDir, reason)` is fine — but the design's "just reuse `UpdateState`" mitigation is wrong on its face and will mislead the implementer. Either (a) document the bootstrap path in Decision 2 (recreate dir + write stub state.json + transitions.log via a new flock'd helper, then optionally `UpdateState` for sub-case 2 only), or (b) have the daemon path go through a single new public helper `mcp.WriteAbandonedTaskStub(taskDir, reason)` that handles both cases.

The shared package itself is fine: `internal/cli/mesh_watch.go` already imports `internal/mcp` and uses `mcp.UpdateState`, `mcp.IsPIDAlive`, `mcp.TaskStateAbandoned`, etc. There is no inversion to add.

### 3. `mainInstanceRoot` terminology mismatch between MCP server and daemon

Throughout Decision 1 and the data-flow sections the design says "`<workspaceRoot>` = `s.mainInstanceRoot` if non-empty (session worker), else `s.instanceRoot`". On the MCP server side (`internal/mcp.Server`) `mainInstanceRoot` is indeed a field. On the daemon side (`internal/cli.spawnContext`) the equivalent field is named `taskStoreRoot` (with helper `taskStoreRootDir()`); there is no `mainInstanceRoot` field on `spawnContext`. The daemon uses `os.Getenv("NIWA_MAIN_INSTANCE_ROOT")` once at startup (mesh_watch.go:294) and stores the value in `taskStoreRoot`.

The design talks as if both packages have a `mainInstanceRoot` symbol; an implementer reading the design and grepping `s.mainInstanceRoot` in `mesh_watch.go` will find nothing. Recommend the design either pick the package-correct names per call site (`s.mainInstanceRoot` for MCP code, `s.taskStoreRootDir()` for daemon code) or define a single neutral term and document the mapping.

### 4. `roleRoot` helper is well-scoped; `daemonOwnsInboxFile` is unaffected

Decision 4's `roleRoot(role string) string` helper on `*mcp.Server` is the right shape. The three call sites the design names (`isKnownRole`, `sendMessageWithID`, `handleAsk`'s askRoot) are the complete set in server.go that need the redirect — I scanned for `s.instanceRoot, ".niwa", "roles"` and the only other usages are in `handleCheckMessages` (which consumes the caller's *own* role inbox via `s.roleInboxDir`, not a target's) and the `roleInboxDir` initialization itself. Both are correctly excluded from the redirect. The design's three-call-site claim is complete.

`daemonOwnsInboxFile` (mesh_watch.go:746-758) only claims files whose body has `type=="task.delegate"`. Worker-originated `niwa_send_message` writes have `type` other than `task.delegate`, so daemon-side ownership is unaffected by the new ability of session workers to write into the main instance's coordinator inbox. The Security Considerations claim ("`daemonOwnsInboxFile` only claims `task.delegate` files") is correct. The new flow does not re-introduce the ephemeral-coordinator-spawn deadlock that PR #93 closed.

### 5. `gitignore.go` extension is not as small as Phase 3 implies

The design says (Components, Phase 3): "extend `internal/workspace/gitignore.go` to add `.claude/skills/niwa-mesh/` to each consumer repo's `.gitignore` on apply". The current `gitignore.go` only handles the **instance-root** `.gitignore` (the `EnsureInstanceGitignore` function). There is no per-consumer-repo write today — `internal/workspace/content.go:CheckGitignore` checks but doesn't write. The "extension" therefore requires (a) a new write function in `gitignore.go` for the per-repo case, parallel to but distinct from `EnsureInstanceGitignore`; (b) a call site in the per-repo materialization path (probably near `materialize.go:521-524` where `CheckGitignore` is consulted, or in `apply.go` near the existing `EnsureInstanceGitignore` call at apply.go:228). Not architecturally problematic — the file is the right home — but the design understates the work. Worth flagging in the Phase 3 deliverables list.

### 6. `niwa_list_sessions` "sub-object" requires a wire-shape change

The design says `handleListSessions` "enriches each `SessionLifecycleState` row" with a `daemon` sub-object. Today `handleListSessions` calls `json.Marshal(filtered)` directly on `[]SessionLifecycleState`. To add a `daemon` key without persisting it, either (a) add a non-persisted, JSON-tagged transient field on `SessionLifecycleState` (similar to existing `BranchWarning string \`json:"-"\`` — but `daemon` *should* serialize, so the precedent is adjacent rather than direct), or (b) build a wrapper struct in the handler. The `Status` single-writer constraint argues for (b) — the lifecycle file shouldn't gain a transient-but-serializing field that risks accidental persistence. The design's Decision Drivers explicitly call out single-writer for `Status`; the same logic implies the handler should compose, not enrich the persisted struct. Worth being explicit about this in Phase 2.

### 7. `extractArgKeys` audit-log fidelity

Already acknowledged by the design (Negative consequences + Security Considerations). No additional finding.

## Sequencing concerns

The dependency graph in "Sequencing summary" is largely accurate, but two implicit dependencies are worth surfacing:

- **Phase 3 → Phase 6 is implicit but real.** The skill rewrite in Phase 6 references the worker-config inheritance contract documented in Phase 3 ("workers inherit the workspace plugin set"). If Phase 6 lands before Phase 3, the skill makes claims the runtime hasn't yet enforced. The graph already places Phase 6 last, so the ordering is correct in practice; the design just doesn't enumerate this dependency.

- **Phase 4 → Phase 5 is partial, not full.** The design says "Phase 4 unblocks Phase 5 only for the dangling-source redelegate case." This is correct — `niwa_redelegate` from a non-dangling abandoned/cancelled source works without Phase 4. But Functional test #2 of Phase 5 (`taskstore_lost` source where envelope.json survived) requires Phase 4's `state=abandoned, reason=taskstore_lost` writer. The graph reads as if Phase 5 has a single dependency on Phase 4; in reality the dependency is per-test. Not a blocker; just a clarity issue.

Phases 1, 2, 4 are genuinely independent; Phase 3 is independent in structure but its functional tests overlap with Phase 1's coordinator-routing tests (a session worker that can reach coordinator AND has the workspace plugin set is the realistic end-to-end shape). Phase 1 + Phase 3 sharing test fixtures is fine.

## Recommendations

Ranked by importance:

1. **Resolve the session-worker `<repoPath>` ambiguity in Decision 1.** Clarify that the second `--add-dir` argument needs `resolveRoleCWD(s.taskStoreRootDir(), role)` for session daemons (or whichever helper produces the workspace's repo path), not `resolveRoleCWD(s.instanceRoot, role)`. Otherwise the session-spawn flag will resolve to the worktree, which is not the workspace repo and does not carry the `.claude/` tree the contract is supposed to expose. (Architectural concern #1.)

2. **Replace "reuse `UpdateState`" with a concrete recreate-stub plan in Decision 2's Mitigations.** `UpdateState` cannot operate on a missing state.json. Either define a sibling flock'd helper (`mcp.WriteAbandonedTaskStub`) or document a two-step path (bootstrap stub via direct write under the per-task flock, then optionally `UpdateState` for sub-case 2). (Architectural concern #2.)

3. **Reconcile `mainInstanceRoot` vs `taskStoreRoot` naming.** The MCP server has `s.mainInstanceRoot`; the daemon's `spawnContext` has `s.taskStoreRoot` / `s.taskStoreRootDir()`. The design uses `s.mainInstanceRoot` for both. Pick package-correct names per call site. (Architectural concern #3.)

4. **Expand the Phase 3 `gitignore.go` deliverable.** It's a new write function and a new integration point in the per-repo materialization path, not an in-place extension of `EnsureInstanceGitignore`. Consider naming the new function explicitly (e.g. `EnsureRepoNiwaMeshGitignore`) and naming the call site (probably `apply.go` or `materialize.go`). (Architectural concern #5.)

5. **Decide the `niwa_list_sessions` shape change explicitly.** Either a wrapper response struct in the handler or a documented transient field. Given the single-writer principle the design already commits to for `Status`, the wrapper struct is cleaner and the design should say so. (Architectural concern #6.)

6. **Add the implicit Phase 3 → Phase 6 edge to the Sequencing graph.** Cosmetic but improves readability for the implementer. (Sequencing concern.)
