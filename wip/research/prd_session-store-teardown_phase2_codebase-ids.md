# Phase 2 Research: Codebase analyst (teardown id resolution)

(Findings returned by a read-only research agent; saved verbatim-in-substance by the orchestrator.)

## Lead 1: Id forms a developer or script can hold
### Findings
- `niwa dispatch` prints `Dispatched session <full UUID>`, `instance: <path>`, optional `session name:`, then per-verb hints `claude attach|logs|stop <handle>` (internal/cli/dispatch.go:936-947; agentplan/dispatch.go:386-390). Headline = UUID, hints = handle.
- `niwa list` human output prints a `resume:` command built from `mapping.Handle` (fallback to SessionID only for HandleSessionID agents; list.go:215-241). `niwa list --json` emits InstanceRecord{name, path, ephemeral, accepts_session_messages, keep_alive?, session_name?} with NO session id or handle (workspace/state.go:381).
- niwa never parses `claude agents --json`; it reads `~/.claude/jobs/<dir>/state.json` (`sessionId`, `cwd`). Handle = filepath.Base(dir) (session_records.go:216-224), "never a slice of the UUID" by intent. Observed layout is dir = UUID[:8] (job_state.go:111-117,176-205; all fixtures use sid[:8]; watch ShortID same).
- Codex: HandleSessionID -> handle is the full session id (agentplan/dispatch.go:450-452).
- Ephemeral-hook mappings (instance_from_hook.go:217-223) set no Handle, no Agent, no Origin.
- Worktree lifecycle id: 8 random hex, unique per instance only (session_lifecycle.go:152-162); shown by `worktree create`, `worktree list`, and in the worktree path.
- Id inventory: Claude UUID; Claude handle (jobs dir name); Codex session id (= handle); worktree lifecycle id; worktree path (--by-path); instance name/path; display session name (display-only).
### Implications
- Accept UUID, handle, Codex id, plus existing lifecycle id and --by-path.
- Do not assume handle == UUID[:8]; prefer recorded Handle; UUID-prefix only as fallback for handle-less mappings.
- `niwa list --json` lacks session id/handle (script journey relies on claude agents --json).

## Lead 2: Resolution paths
### Findings
- UUID: ReadSessionMapping -> InstancePath -> ListSessionLifecycleStates(<instance>/.niwa/sessions), non-terminal records.
- Handle: scan ListSessionMappings for Handle == h; for handle-less mappings, either jobs-dir lookup (gone after `claude rm`) or HasPrefix(SessionID, h) (precedent in readJobState).
- Nothing links a lifecycle record to a Claude session: ParentSessionID is never set by any caller; ClaudeConversationID has no production writer. Only link is instance-level.
- Several mappings can point at one instance (list.go:175-205 picks newest). Resolution by session returns every worktree in that instance, including hand-made and hook-made ones.
- Workspace root discovery: workspace.ClassifyCwd(cwd).WorkspaceRoot (cwd_classify.go:86-150) used by list.go, destroy.go, instance_from_hook.go; or config.Discover + Dir.
- ListSessionLifecycleStates filters ^[0-9a-f]{8}\.json$; ListSessionMappings reads any *.json (would decode lifecycle files as mappings if pointed at an instance dir).
- Mapping content is same-user-writable/untrusted; reap deletes mappings on reclaim; `niwa destroy` does not.
### Implications
- Resolution is instance-scoped. Validate mapping InstancePath is a real instance under this workspace root before destroying.
- Distinct outcomes: unknown id; mapping whose instance is gone; zero active worktrees; N destroyed with per-worktree branch warnings.

## Lead 3: 8-hex collision
### Findings
- Handle and lifecycle id share ^[0-9a-f]{8}$; lifecycle ids unique only per instance. UUIDs (dashed) are unambiguous.
- Precedents: `niwa go` (repo in current instance wins over same-named workspace, stderr note, -r/-w flags; go.go:185-232); `worktree destroy --by-path` explicit flag; `niwa destroy` interprets arg by cwd (destroy.go:81-85); capture/watch refuse on ambiguity.
### Implications
- Options: cwd-sensitive precedence with notice, or explicit `--session` flag; never silently pick when both match.

## Lead 4: Other callers with the root-vs-instance conflation
### Findings
- resolveInstanceRoot callers: runSessionCreate, runSessionApply, runSessionDestroy, runSessionLifecycleList, completeSessionIDs, runFromHookRemove, runSessionAttach/Detach, resolveSessionWorktree (go). At the root all read the mapping store: list empty, completion empty, apply/destroy/attach/detach/go fail.
- workspace.DiscoverInstance shares the conflation (resolveCurrentInstanceRepo, resolveContextAware, completeGoTarget, ResolveRepoFromCwd).
- ClassifyCwd already excludes the root via !isWorkspaceRoot (cwd_classify.go:113-121), covered by apply-root-not-instance.feature.
- Refusing the root in resolveInstanceRoot breaks no found test: unit tests use NIWA_INSTANCE_ROOT; functional steps run from the instance dir; runFromHookRemove logs and returns nil on resolution failure.
### Implications
- Decide destroy-only vs every lifecycle reader.

## Lead 5: Tests and harness
### Findings
- Unit: session_lifecycle_cmd_test.go:327; session_lifecycle_issue1_test.go:244-372 (newCreateFlowFixture, resets sessionDestroyByPath global); worktree_test.go DestroySession; session_from_hook_cmd_test.go:179-313; completion_test.go:355-428.
- Functional: worktree.feature, session_attach.feature; steps in session_steps_test.go:340-420 run destroy with cwd = instance.
- Harness: fake claude for dispatch with session "<uuid>" + dispatch from workspace root yields real instance + mapping; fake writes $HOME/.claude/jobs/<sid[:8]>/state.json so handle = uuid[:8]. Mapping existence steps in ephemeral_session_steps_test.go:115-151.
- Missing: step to create a worktree inside the dispatched instance and assert on it. Unit alternative: temp root with .niwa/workspace.toml, WriteSessionMapping, t.Chdir(root) (not NIWA_INSTANCE_ROOT) reproduces the bug on main.
### Implications
- Acceptance tests: from root, destroy <uuid> and <handle> reach the dispatched instance's worktree and keep an unmerged branch with warning; from instance, destroy <lifecycle id> still works; unknown id / reaped instance / zero worktrees each get a distinct result and exit code.
- Consider a handle test whose jobs dir name != uuid[:8].

## Summary
Three session ids exist (Claude UUID, Claude handle = jobs dir name, Codex id); none links to a worktree record, so resolution is mapping -> instance -> every active worktree there. resolveInstanceRoot conflates the root with an instance for nine callers; ClassifyCwd already has the fix and refusing the root breaks no test. The fake-claude dispatch harness supports a destroy-by-UUID/handle test with one new step.
