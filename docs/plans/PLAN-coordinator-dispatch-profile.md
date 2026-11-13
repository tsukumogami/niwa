---
schema: plan/v2
status: Active
execution_mode: multi-pr
split_mode_source: judgment
split_rationale: |
  Hard Constraint. Steps 1 to 5 land in tsukumogami/niwa and end in a niwa
  release carrying `--profile`; step 6 lands in tsukumogami/shirabe, whose
  coordinator dispatch can only pass the flag once that release exists, and a
  pull request can't span repositories. Inside niwa, steps 1 to 5 are slices
  of one launch path and can share one pull request.
tracking_level: none
upstream: docs/designs/DESIGN-coordinator-dispatch-profile.md
milestone: "Coordinator dispatch profile"
step_count: 6
---

# PLAN: coordinator-dispatch-profile

## Status

Active

## Scope Summary

A workspace declares named dispatch profiles under
`[session.dispatch_profiles.<name>]`, and `niwa dispatch --profile <name>`
applies one to that launch only. For Claude, a new contributor adds the
profile's `permissions.deny` list, `disableBundledSkills` and the
account-connector switch to the launch `--settings` document. Codex refuses
the flag through its capability row. The session mapping records the profile,
`niwa list` shows it, and `niwa resume` accepts the profile's keys when they
match what the recorded profile resolves to now. A dispatch without
`--profile` produces today's argv and document byte for byte. Once a niwa
release carries the flag, the coordination skill in tsukumogami/shirabe
dispatches its coordinators with `--profile coordinator` and leads their
prompt with the slash invocation, since a profiled coordinator can't call the
Skill tool.

## Decomposition Strategy

Layered along the launch path, in the order a dispatch reads it: config,
then the per-agent spelling in `internal/agentplan`, then the dispatch
contributor, then resume. The guide and the live checks come last, because
they describe and verify the whole path. Each step leaves the tree building
and its tests passing, and the no-profile byte-identity test lands with the
contributor so no later step can regress it unnoticed.

## Steps

### Step 1: the workspace config declares dispatch profiles

**Goal**: `[session.dispatch_profiles.<name>]` loads with `drop_tools`, `drop_bundled_skills` and `drop_account_connectors`, and refuses malformed declarations at load.

**Acceptance Criteria**:

- [ ] A workspace.toml with the coordinator profile from the design loads, and `SessionConfig.DispatchProfiles["coordinator"]` holds the seven tool names and both booleans true.
- [ ] Load refuses, naming the profile and the entry: a profile name outside `^[a-z0-9][a-z0-9-]*$`, a tool name in lowercase (`advisor`), a scoped rule (`Bash(git *)`), and a profile that declares no drop at all.
- [ ] `mcp__claude_ai_Example` is accepted as a tool name.
- [ ] The vault resolver's deep copy of `[session]` clones `DispatchProfiles`, and a test mutating the copy's `drop_tools` leaves the original unchanged.
- [ ] `niwa apply` writes no file that differs from a run without the table. The table is dispatch-time only.

**Check**: `go test ./internal/config/ ./internal/vault/resolve/` passes, including the new load and deep-copy cases.

**Dependencies**: None

**Files**: `internal/config/session.go`, `internal/config/session_test.go`, `internal/vault/resolve/deepcopy.go`

### Step 2: agentplan spells a profile for Claude and declares Codex unsupported

**Goal**: A `DispatchProfile` launch capability row exists, implemented for Claude and unsupported for Codex with a reason, and `ProfileSettings` returns Claude's settings keys and env value for a profile.

**Acceptance Criteria**:

- [ ] `ProfileSettings` for the coordinator profile returns `permissions: {deny: [the seven names in declared order]}`, `disableBundledSkills: true`, and env `ENABLE_CLAUDEAI_MCP_SERVERS=false`.
- [ ] A profile with only `drop_tools` returns no `disableBundledSkills` key and no env entry. A profile with only `drop_account_connectors` returns no `permissions` key.
- [ ] `ProfileEnvNames()` for Claude returns `ENABLE_CLAUDEAI_MCP_SERVERS`, and the Claude self-wake tool set is `CronCreate` and `ScheduleWakeup`.
- [ ] `Lookup(DispatchProfile, codex)` is unsupported with a non-empty reason, and the generated Codex gap list names it.
- [ ] The existing binding and dispatch-path scan tests pass, and no file under `internal/cli` names an agent.

**Check**: `go test ./internal/agentplan/` passes, including the profile spelling and binding tests.

**Dependencies**: Step 1

**Files**: `internal/agentplan/capability.go`, `internal/agentplan/declaration.go`, `internal/agentplan/profile.go`, `internal/agentplan/profile_test.go`

### Step 3: niwa dispatch applies a profile to one launch and records it

**Goal**: `niwa dispatch --profile <name>` adds the profile's keys to the launch settings document and its env value to the launch environment. The mapping records the profile, `niwa list` shows it, and a dispatch without the flag is unchanged.

**Acceptance Criteria**:

- [ ] With a fake Claude spec, `--profile coordinator` produces one `--settings` element whose document holds the deny list, `disableBundledSkills: true` and `env.ENABLE_CLAUDEAI_MCP_SERVERS = "false"`, alongside the keys the other contributors add. The launched process environment also carries the env value.
- [ ] Without `--profile`, the argv and the `--settings` document are byte-identical to the ones the current code produces for the same fixtures, with and without remote control, inbound acceptance and MCP approval.
- [ ] Each of these refuses before an instance is provisioned, exits non-zero, and names the cause: an unknown profile; `--profile` with the Codex harness, printing the row's reason; a profile dropping `ToolSearch`; and a profile dropping both `CronCreate` and `ScheduleWakeup` when keep-alive resolves on. A profile dropping only one of them is accepted.
- [ ] A profiled launch's process environment and settings `env` block carry `NIWA_DISPATCH_PROFILE=coordinator`. A launch without `--profile` from a base environment holding `NIWA_DISPATCH_PROFILE` and `ENABLE_CLAUDEAI_MCP_SERVERS=false` produces a worker environment with neither, and its argv and `--settings` document still match the no-profile fixtures. A launch without `--profile` from a base holding `ENABLE_CLAUDEAI_MCP_SERVERS=false` but no marker keeps the switch.
- [ ] The session mapping carries `dispatch_profile: "coordinator"`, `niwa list --json` shows it, and the post-launch audit line names it. A dispatch without the flag writes no such field.

**Check**: `go test ./internal/cli/ -run Dispatch` passes, and the new scenario in `dispatch-profile.feature` shows the profiled document and the unchanged no-profile argv.

**Dependencies**: Step 2

**Files**: `internal/cli/dispatch.go`, `internal/cli/dispatch_env.go`, `internal/cli/dispatch_profile.go`, `internal/cli/dispatch_profile_test.go`, `internal/workspace/session_map.go`, `internal/cli/list.go`, `test/functional/features/dispatch-profile.feature`

### Step 4: niwa resume keeps a profiled session's profile

**Goal**: `checkResumeSettings` accepts the profile's keys when they equal what the recorded `dispatch_profile` resolves to in the workspace's current config, and refuses them otherwise.

**Acceptance Criteria**:

- [ ] A record dispatched with `--profile coordinator` resumes, and the relaunch carries the same `--settings` document.
- [ ] The resume refuses, naming the profile, when the recorded profile was removed from the config, when its `drop_tools` changed, and when the recorded document holds a `permissions` key but the record names no profile.
- [ ] A `permissions` value holding anything but `deny` (an `allow` list, say) refuses, whatever the profile says.
- [ ] Records made without a profile resume exactly as before; the existing resume tests pass unchanged.
- [ ] `niwa retask` on a profiled worker passes its saved-options check, and its test asserts that no flag is added on the wake.

**Check**: `go test ./internal/cli/ -run 'Resume|Retask'` passes, including the profiled resume and each refusal.

**Dependencies**: Step 3

**Files**: `internal/cli/resume.go`, `internal/cli/resume_test.go`, `internal/cli/retask_test.go`

### Step 5: the guide documents profiles and the three live checks

**Goal**: `docs/guides/dispatch-profiles.md` covers the table, the flag, the refusals, what a profile can't drop and why, resume and retask behaviour, and the three Evidence checks from the design with the Claude Code version they last passed on. The checks are run once against a build of this branch, with the results recorded.

**Acceptance Criteria**:

- [ ] The guide gives the coordinator profile from the design as its example and explains that a profiled coordinator's entry skill has to start its prompt as a slash command.
- [ ] Each of the three checks is written as steps a reviewer can follow: how to read turn-0 context from the transcript's first `usage`, the deferred tools to load and use, and what an interactive session must still show.
- [ ] Run against this branch's build: a coordinator dispatched with `--profile coordinator` reads below 35,000 tokens at turn 0 and uses SendMessage, a Cron tool and TaskStop without error. A dispatch without the profile, made once from a plain shell and once from inside that profiled coordinator, and an interactive session all show their full tool lists, AskUserQuestion and Skill included, and the account connectors in `/mcp`. The live run also records whether the coordinator's leading slash invocation expanded under `--bg` with Skill denied, which answers the design's first open question. The figures go in the pull request's test plan.
- [ ] CLAUDE.md's guide index gains the new guide.
- [ ] A niwa release carries `--profile`.

**Check**: The live run's three results, read from the transcripts as the guide describes, are recorded in the pull request's test plan.

**Dependencies**: Step 4

**Files**: `docs/guides/dispatch-profiles.md`, `CLAUDE.md`

### Step 6: the coordinator dispatch passes the profile and leads with the slash invocation

**Goal**: In tsukumogami/shirabe, the coordination skill's dispatch of a coordinator passes `--profile coordinator` and starts the worker's prompt with the `/shirabe:coordinate <args>` invocation, so the profiled coordinator loads its skill without the Skill tool. Other dispatches it makes are unchanged.

**Acceptance Criteria**:

- [ ] A coordinator dispatch's `niwa dispatch` call carries `--profile coordinator`, and its prompt's first token is `/shirabe:coordinate`, followed by the same arguments the brief's invocation line names.
- [ ] A worker dispatch, one that isn't a coordinator, carries no `--profile`, and its prompt is unchanged.
- [ ] Against a niwa older than the step 5 release, where `niwa dispatch --help` doesn't list `--profile`, the coordinator dispatch leaves the flag out and keeps the leading slash invocation.
- [ ] A coordinator dispatched this way, in a workspace declaring the profile, passes the design's first Evidence check.

**Check**: A coordinator dispatched by the changed skill reads below 35,000 tokens at turn 0 in its transcript's first `usage`.

**Dependencies**: Step 5

**Files**: the coordination skill's dispatch script and brief template in tsukumogami/shirabe

## Implementation Sequence

Steps 1 to 5 run in order in tsukumogami/niwa, one commit or more each, and
can share one pull request. Step 5 ends in a niwa release. Step 6 then lands
in tsukumogami/shirabe as its own pull request. The design's Evidence section
is the acceptance for the feature as a whole. Step 5's live run checks it
against a real launch, and step 6's last criterion checks it on a real
coordinator.

Notes to carry into execution:

- The trimmed launches behind the design ran on Claude Code 2.1.319. If the
  installed version differs when step 5 runs, rerun the checks there and
  record that version in the guide.
- MCP servers that are still connecting at turn 0 move the turn-0 sum by a
  few thousand tokens between otherwise identical launches. Compare runs where
  the same servers had connected, or take the lower of two.
- A coordinator under the profile can't call the Skill tool, so step 6's
  leading slash invocation is what loads its skill. Until step 6 lands, a
  coordinator dispatched by hand with `--profile coordinator` needs its
  prompt written that way too.
