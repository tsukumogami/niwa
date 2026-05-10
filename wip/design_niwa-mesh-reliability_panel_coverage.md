# Coverage panel review: DESIGN-niwa-mesh-reliability.md

Source design: `docs/designs/DESIGN-niwa-mesh-reliability.md` (status: Proposed).
Issues in scope: #92, #97, #108, #109, #110, #111, #112, #113, #114, plus #116 (deferred-PRD context).

## Per-issue coverage table

| Issue | AC summary | Addressed | Citation in design | Divergence / notes |
|---|---|---|---|---|
| **#92** AC1 | `niwa_ask(to='coordinator')` reaches live coordinator session, not an ephemeral spawn | Yes | Decision 4 (lines 644-707); Data Flow "Worker asks coordinator" (1101-1118) | Design clarifies #92 is largely already done in PR #93; remaining gap is `isKnownRole` precondition. The Source Issues line "partly fixed, completes the chain via Decision 4" is accurate. |
| **#92** AC2 | Coordinator can receive incoming ask via `niwa_check_messages` and respond | Yes | Data Flow lines 1113-1118 ("coordinator's watcher dispatches task.ask via existing questionWaiters / niwa_check_messages") | Reuses existing mechanism. |
| **#92** AC3 | When no coordinator session is active, fallback to existing spawn path unchanged | Partial / Divergent (acknowledged) | Decision 4 alternatives (709-722), "What was discovered" (99-103) | Design states the spawn-fabricate fallback is **already gone** post-PR #93 — current branch returns `no_live_session` synchronously. Divergence from issue's expectation is documented and acceptable: ephemeral coordinator spawns were intentionally removed; the AC's "fallback to existing spawn path unchanged" is moot. Worth flagging in skill text rewrite (Phase 6). |
| **#92** AC4 | niwa-mesh skill documentation accurately describes how a coordinator receives/responds to asks | Yes | Phase 6 deliverables (1263-1275); Components/`buildSkillContent` rewrite (856-862) | Skill text is rewritten to match runtime. |
| **#97** Expected | Skill injected into agent sessions without writing a file into the consumer repo | Yes | Decision 1 (chosen: argv flags) (371-399); Phase 3 deliverables (1180-1204) | Per-repo skill writes removed in `InstallChannelInfrastructure` (lines 391-394, 853-856). The instance-root copy is reached via `--add-dir <workspaceRoot>`. Plus defense-in-depth `.gitignore` extension (396-399, 864-868). |
| **#97** Implicit | No file in consumer repo working tree | Yes | Phase 3 functional test 5 (1202-1203): "After `niwa apply`, no consumer repo working tree contains `.claude/skills/niwa-mesh/SKILL.md`" | Verified by test. |
| **#108** Expected | Worker's Claude Code session inherits workspace plugin set | Yes | Decision 1 (285-444); Solution Architecture worker config inheritance (778-829) | Verified empirically (Verification Notes G/I). |
| **#108** Impact: `/prd→/design→/plan→/work-on` flow runs end-to-end through mesh | Partial | Implicit via Decision 1 | Design does not name this end-to-end flow as a verified test scenario. Phase 3 functional tests (1190-1204) cover the symmetry contract and the skill-invocability contract, but no test asserts a multi-step shirabe pipeline through the mesh. Acceptable interpretation: if the inheritance contract holds, the flow follows by construction. Could be tightened by adding an integration test; not strictly required by the AC. |
| **#108** Suggested options A/B/C | Design picks a different option (argv flags) | Yes (documented divergence) | Decision 1 alternatives (415-444); Verification Notes (1518-1568) | Issue suggested: (1) pass `~/.claude/`, (2) mount plugin dir, (3) per-role allowlist. Design picks `--add-dir + --setting-sources` — option not enumerated in issue. Divergence is rationalized empirically (Experiment D, F, G, I). Acceptable. |
| **#109** AC1 | Workers can call `niwa_ask(to='coordinator')` without `UNKNOWN_ROLE` | Yes | Decision 4 chosen mechanism (668-682); roleRoot helper applied to `isKnownRole` (672) | Same fix covers `niwa_send_message` (line 673-674: `sendMessageWithID`'s inbox path). |
| **#109** AC2 | Workers can call `niwa_send_message(to='coordinator', ...)` without UNKNOWN_ROLE | Yes | Decision 4 (672-678); Solution Architecture (873-878) | Explicitly addressed via `roleRoot` redirect on `sendMessageWithID`. |
| **#109** Suggested Option A vs B vs C | Design picks A-like (special-case via `roleRoot`) | Yes | Decision 4 alternatives (709-722) | Issue's Option A involved scaffolding `coordinator.json` in worktree at `niwa_create_session`. Design picks an in-code redirect (`roleRoot` helper) that achieves Option A's goal without writing a directory. Rationalized at lines 711-722 — synthetic dir is rejected because daemon would dutifully watch it and re-enable spawn deadlock. Sound. |
| **#110** AC1 | `niwa_create_session` returns error or `daemon_status` reflecting spawn result | Yes | Phase 2 (1160-1174); Components (847-851, 896-902); Key Interfaces `DAEMON_SPAWN_TIMEOUT` (1005-1011) | Design picks "Strict" mode from issue's two options: returns typed `ErrDaemonSpawnTimeout` error rather than `daemon_status` field. |
| **#110** AC1 (a): inotify limit reached | Yes | Phase 2 deliverable + rollback in `handleCreateSession` (897-902) | Rollback path: cleanupWorktree, branch, state file. |
| **#110** AC1 (b): missing/non-executable target binary | Partial | Implicit via "existing pre-spawn errors keep their current return paths" (850, 1011) | The design defers to existing pre-spawn error paths for binary issues — those errors aren't typed in the design. The 500 ms timeout path is the only new typed code. Acceptable but the AC asks for all three test cases, including missing binary. Worth confirming the existing pre-spawn errors meet AC quality at Phase 2 implementation. |
| **#110** AC1 (c): PID file write failure | Partial | Implicit via timeout path (the timeout fires when daemon.pid never appears) | If PID file write fails after fsnotify register, the daemon presumably exits and the timeout fires. Design doesn't explicitly call out this case. Mostly fine, but Phase 2 test plan should cover. |
| **#110** AC2 | Error/status surface integrates with `niwa_list_sessions` | Yes (by elimination) | Failures roll back the session entirely (897-902) | Rollback means the failed session never appears in `niwa_list_sessions`, which is consistent with the AC intent: a failed creation isn't reported as healthy. Could surface differently (lenient mode), but design picks strict; sound rationale. |
| **#110** AC3 | Backwards-compat: existing successful create paths unchanged | Yes | Implicit; only the timeout path changes from "return nil" to "return typed error" | No behavior change on success. |
| **#111** AC1 | `niwa_list_sessions` returns `daemon` sub-object for every session | Yes | Phase 2 (1170-1173); Solution Architecture (903-907); Key Interfaces (1013-1031) | Includes `{alive, pid, started_at}`. |
| **#111** AC2 | A session whose daemon is dead reports `daemon.alive: false` | Yes | "Probe via `<worktreePath>/.niwa/daemon.pid` + `mcp.IsPIDAlive`" (905-906) | Verified mechanism. |
| **#111** AC3 | Session never claimed task → `last_claim_at: null` not missing | **Not addressed (deferred)** | Key Interfaces note (1032-1042): deferred to #116 with rationale | Explicitly deferred. The task description marks the 9-vs-11 and deferred fields as already flagged — accepted scope decision. |
| **#111** AC4 | Backwards-compat: existing fields stay top-level; `daemon` is additive | Yes | Lines 1026-1031 | Sub-object is additive. |
| **#111** Suggested impl heartbeat file vs PID + log parsing | Design picks PID + IsPIDAlive only (no heartbeat) | Yes | Decisions Already Made (276): "No new daemon heartbeat file; `daemon.pid` + `IsPIDAlive` is sufficient." | Documented constraint from exploration. Sound. |
| **#112** AC1 | `niwa_query_task` and `niwa_list_outbound_tasks` return `state: 'dangling'` (or similar) for dangling envelopes | Yes (with structural divergence) | Decision 2 (446-535); Solution Architecture (842-846) | Divergence: design picks `state="abandoned"` with `reason="taskstore_lost"` rather than introducing a sixth state `dangling`. Rationalized: keeps state alphabet at 5; reuses terminal-state guards; recovery via redelegate. Documented and sound. |
| **#112** AC2 (a) | Conditions that trigger dangling are documented | Yes | Phase 6 + `docs/guides/sessions.md` updates (916-921, 1264-1273) | Updated docs cover `taskstore_lost`. |
| **#112** AC2 (b) | Sticky semantics documented | Sound by displacement | Now the state is `abandoned` (genuinely sticky terminal state by design) | The "sticky" property is preserved in a cleaner shape. |
| **#112** AC2 (c) | Recovery method documented | Yes | Lines 500-503; Phase 4/5; sessions.md updates | Recovery is `niwa_redelegate`. |
| **#112** AC3 (optional) | `niwa_resurrect_task` provided | No (rejected with rationale) | Decision 2 alternatives (513-526) | Resurrect rejected: "doesn't satisfy the API truthfulness goal until operator runs the tool" + competes with redelegate. Sound. |
| **#112** Implicit fix to `{too_late, queued}` contradiction | Yes | Phase 4 deliverable (1217-1223) | "only the cancel path needs an early state guard added to remove the `{too_late, queued}` contradiction." Explicit. |
| **#113** AC1 | `niwa_delegate` accepts optional `required_skills: string[]` | Yes (with placement divergence) | Decision 3 (537-642) | Divergence: lives inside `body` (`body.required_skills`), not top-level. Rationale lines 601-624. Documented and sound. |
| **#113** AC2 | When provided, MCP server checks target session's plugin set before queueing | Yes | Lines 587-596; Phase 5 (1238-1241) | Manifest enumerates `<workspaceRoot>/.claude/skills/` and resolved enabledPlugins. |
| **#113** AC3 | On miss, returns structured error with `missing` and `available` arrays; no envelope written | Yes | Key Interfaces `MISSING_SKILLS` (991-1003) | Exact shape matches. |
| **#113** AC4 | If `required_skills` omitted, behavior unchanged | Yes | Body convention is opaque; absence = no peek match | Implicit. |
| **#113** AC5 | `read_only=true` path supports same precondition check | **Partial / not explicitly addressed** | Design says the gate runs in `handleDelegate` and `handleRedelegate`; doesn't explicitly call out `read_only=true` routing | The `read_only=true` branch routes to the main clone instead of a session, but the gate would still inspect the workspace's plugin manifest, so functionally it covers the case. Worth a one-line acknowledgment. **Minor gap.** |
| **#113** Suggested impl: hard dependency on #108 | Yes | Decision 3 (538-571); Phase ordering (1228-1233, 1259-1261) | Explicitly sequenced after Decision 1; design states this. |
| **#114** AC1 | `niwa_redelegate` callable with `source_task_id` + one of `(session_id, read_only)` | Yes | Key Interfaces (939-951) | Schema includes `source_task_id`, `to`, `session_id`, `read_only`, `body_overrides`, `mode`, `expires_at`. |
| **#114** AC2 | Body verbatim from source unless `body_overrides` provided; shallow merge at top level | Yes | Lines 941-948, 1340-1346 | "Shallow-merge into source.body" matches AC's "shallow-merged at the top level." |
| **#114** AC3 | New task's `from` reflects caller of `niwa_redelegate` (not original) | Yes | Lines 953-955: "`from` is the caller's role/PID; `redelegated_from` points to the source." | Attribution preserved. |
| **#114** AC4 | Source task state unchanged | Yes | Lines 956-957: "The source task's state is unchanged" | Explicit. |
| **#114** AC5 | Source state in any of (queued, running, completed, abandoned, cancelled, **dangling**) is reusable; running source warns coordinator | Partial — **structural divergence** | Lines 953-957, 973-980 | The dangling state no longer exists structurally (Decision 2 collapses it into `abandoned` with reason). So redelegate from "any state" naturally handles the case. The "warn the coordinator that they're forking active work" requirement from AC5 is addressed via `source_state_at_fork` field (965-980), which is a structurally honest signal rather than a free-text warning. Soundly handled. |
| **#114** Suggested impl: regenerate task_id, from, sent_at; propagate body, to, session_id | Yes | Lines 952-957 | Matches. |
| **#116** | (Deferred-scope PRD; not an in-scope AC) | Acknowledged | Lines 1032-1042 | Design explicitly defers `last_claim_at`, `last_progress_at`, `watcher_count` to #116. Per the task brief, this is already-flagged scope and not a gap. |

## Gaps requiring attention

Ranked by severity:

1. **(Low) #113 AC5 — `read_only=true` path not explicitly called out.** The acceptance criterion specifies "The `read_only=true` path (which routes to the main clone instead of a session) supports the same precondition check against the main daemon's plugin set." The design's gate text talks about "the target session's plugin set" / "the workspace's `.claude/` tree" without explicitly handling the no-session routing branch. Functionally fine because the manifest is workspace-wide, but a one-line acknowledgment in Decision 3 or in Phase 5 deliverables would close the AC literally.

2. **(Low) #110 AC1 sub-cases (b) missing binary and (c) PID file write failure not enumerated.** The design's typed error covers the timeout path, and the design states "Existing pre-spawn errors keep their current return paths" — but the issue's AC explicitly lists three test cases and the design only narrates one. Phase 2 test plan should explicitly cover (b) and (c) for AC compliance.

3. **(Low) #108 end-to-end `/prd→/design→/plan→/work-on` flow not asserted as a test.** The issue's "Impact" section names the 4-step flow as the user-visible failure. Phase 3 has good symmetry/skill-invocability tests but doesn't have a multi-phase pipeline integration test. Probably out of scope for a unit/functional pass, but a future integration test is implied. Not strictly an AC gap.

4. **(Low) #92 AC3 fallback to spawn path is moot.** Issue AC says "When no coordinator session is active, behavior falls back to the existing spawn path unchanged." Design correctly notes the spawn-fabricate fallback was removed in PR #93 — current behavior is `no_live_session` synchronously. The skill rewrite (Phase 6) needs to explicitly document this contract change so callers know the fallback is gone. Not a coverage gap; a documentation expectation.

## Documented divergences (design picked X, issue suggested Y)

All material divergences are documented with rationale:

1. **#108 mechanism.** Issue suggested options A/B/C (env propagation, mounting plugin dir, per-role allowlist). Design picked argv flags (`--add-dir + --setting-sources`). Rationale: empirically verified (Experiments D/F/G/I), uses documented Claude Code primitives, additive for future per-spawn customization, doesn't introduce filesystem mirrors. **Sound.**

2. **#109 mechanism.** Issue's Option A scaffolds `roles/coordinator.json` in the worktree. Design picks in-code `roleRoot` helper instead. Rationale: synthetic dir would re-enable ephemeral-coordinator spawn deadlock that PR #93 closed; redirect is the same one already inline in `handleAsk`. **Sound.**

3. **#110 lenient vs strict.** Issue offered both modes. Design picks strict (typed error, rollback). No rationale narrated explicitly, but the strict mode is the stronger contract and matches issue's "Strict" option literally. **Acceptable; could be a one-liner.**

4. **#111 deferred fields.** Issue listed `last_claim_at`, `last_progress_at`, `watcher_count`. Design defers to #116. Rationale: requires daemon heartbeat infrastructure that exploration ruled out. Per the task brief, already-flagged. **Sound.**

5. **#112 state shape.** Issue suggested `state: 'dangling'`. Design picks `state: 'abandoned'` + `reason: 'taskstore_lost'`. Rationale: keeps alphabet at 5, reuses terminal-state guards, composes with redelegate. **Sound.**

6. **#112 recovery primitive.** Issue offered `niwa_resurrect_task` as optional. Design rejects in favor of `niwa_redelegate` as single recovery path. Rationale: API truthfulness goal requires non-operator-action transition; resurrect's surface area shrinks once real state lands. **Sound.**

7. **#113 placement.** Issue placed `required_skills` as top-level on `delegateArgs`. Design places inside `body`. Rationale: keeps wire schema small, body opacity is load-bearing, redelegate propagates for free, future per-spawn customization composes. Acknowledges audit-log fidelity loss. **Sound.**

8. **#114 dangling source state.** Issue lists "dangling" as one of the source states. Design eliminates dangling structurally (per Decision 2), so redelegate from "any state" naturally covers it via `abandoned`. **Sound.**

9. **#114 running-source warning.** Issue says "warn the coordinator." Design provides `source_state_at_fork` field instead of a textual warning. **Sound — structurally cleaner.**

## Source Issues closing claims (accuracy check)

- **#92 — "partly fixed, completes the chain via Decision 4"** ✓ Accurate. PR #93 wired live-coordinator routing; Decision 4 fixes the `isKnownRole` precondition that blocked it.
- **#97 — "resolved by Decision 1's per-repo write removal"** ✓ Accurate. `InstallChannelInfrastructure` per-repo write loop is removed; `.gitignore` defense-in-depth added.
- **#108 — "resolved by Decision 1's inheritance contract"** ✓ Accurate, with the open 9-vs-11 shirabe gap acknowledged.
- **#109 — "resolved by Decision 4"** ✓ Accurate.
- **#110 — "resolved by Phase 2's typed timeout"** ✓ Accurate, modulo the unenumerated AC1 sub-cases noted above.
- **#111 — "resolved by Phase 2's `daemon` sub-object"** ✓ Accurate within the deferred-fields scope; deferral captured in #116.
- **#112 — "resolved by Decision 2 and `niwa_redelegate`"** ✓ Accurate.
- **#113 — "resolved by Decision 3, with reduced urgency framing"** ✓ Accurate; the framing-shift from "load-bearing prerequisite" to "drift gate" is honest.
- **#114 — "resolved by Phase 5"** ✓ Accurate.

## Summary verdict

The design provides strong, coherent coverage of all nine issues. Every material divergence is documented with sound rationale, and the four decisions compose into a single reliability pass that closes the issue cluster. Three minor literal-AC gaps (read_only path in #113, sub-case enumeration in #110, end-to-end flow test in #108) are low-severity and can be tightened at implementation time without re-opening design decisions. The design is ready to advance from Proposed.
